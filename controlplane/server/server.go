package server

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/controlplane/snapshot"
	"github.com/fireflycore/service-mesh/pkg/model"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// Server 是控制面的最小 gRPC 服务实现。
type Server struct {
	controlv1.UnimplementedMeshControlPlaneServiceServer

	store  *snapshot.Store
	loader *snapshot.Loader

	mu             sync.RWMutex
	subscribers    map[uint64]*subscriber
	trackedTargets map[string]model.ServiceRef
	nextSubscriber uint64
}

type subscriber struct {
	pushCh   chan *controlv1.ConnectResponse
	identity *controlv1.DataplaneIdentity
	targets  map[string]model.ServiceRef
}

// New 用给定的 snapshot store 创建控制面服务。
func New(store *snapshot.Store) *Server {
	return NewWithLoader(store, nil)
}

// NewWithLoader 用给定的 snapshot store 和 loader 创建控制面服务。
func NewWithLoader(store *snapshot.Store, loader *snapshot.Loader) *Server {
	return &Server{
		store:          store,
		loader:         loader,
		subscribers:    make(map[uint64]*subscriber),
		trackedTargets: make(map[string]model.ServiceRef),
	}
}

// Connect 维护一条双向流，按消息类型分发到 register / heartbeat 处理器。
func (s *Server) Connect(stream grpc.BidiStreamingServer[controlv1.ConnectRequest, controlv1.ConnectResponse]) error {
	type recvResult struct {
		req *controlv1.ConnectRequest
		err error
	}

	recvCh := make(chan recvResult, 1)
	go func() {
		defer close(recvCh)
		for {
			req, err := stream.Recv()
			select {
			case recvCh <- recvResult{req: req, err: err}:
			case <-stream.Context().Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	var subscriberID uint64
	var pushCh <-chan *controlv1.ConnectResponse
	removeSubscriber := func() {
		if subscriberID == 0 {
			return
		}
		s.removeSubscriber(subscriberID)
		subscriberID = 0
	}
	defer removeSubscriber()

	for {
		select {
		case result, ok := <-recvCh:
			if !ok {
				return nil
			}
			if result.err != nil {
				if result.err == io.EOF {
					return nil
				}
				return result.err
			}

			switch body := result.req.GetBody().(type) {
			case *controlv1.ConnectRequest_Register:
				if pushCh == nil {
					subscriberID, pushCh = s.addSubscriber()
				}
				s.updateSubscriberIdentity(subscriberID, body.Register.GetIdentity())
				if err := s.handleRegister(stream, body.Register); err != nil {
					return err
				}
			case *controlv1.ConnectRequest_Heartbeat:
				if err := s.handleHeartbeat(stream, body.Heartbeat); err != nil {
					return err
				}
			case *controlv1.ConnectRequest_Subscribe:
				if err := s.handleSubscribe(stream, subscriberID, body.Subscribe); err != nil {
					return err
				}
			}
		case resp, ok := <-pushCh:
			if !ok {
				pushCh = nil
				continue
			}
			if resp == nil {
				continue
			}
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
	}
}

// handleRegister 在 dataplane 首次注册时回放当前快照与策略。
func (s *Server) handleRegister(stream grpc.BidiStreamingServer[controlv1.ConnectRequest, controlv1.ConnectResponse], register *controlv1.DataplaneRegister) error {
	if register == nil || register.GetIdentity() == nil {
		return nil
	}
	identity := register.GetIdentity()

	for _, serviceSnapshot := range selectBestSnapshotsForIdentity(s.store.AllServiceSnapshots(), identity) {
		// 第十四版开始，register 后优先回放控制面当前已知的全部快照，
		// 让 dataplane 默认依赖 controlplane 状态，而不是本地直连 source。
		if err := stream.Send(&controlv1.ConnectResponse{
			Body: &controlv1.ConnectResponse_ServiceSnapshot{
				ServiceSnapshot: serviceSnapshot,
			},
		}); err != nil {
			return err
		}
	}

	for _, routePolicy := range selectBestRoutePoliciesForIdentity(s.store.AllRoutePolicies(), identity) {
		// 路由策略和快照一样采用“全量当前状态回放”，保持 dataplane 本地视图完整。
		if err := stream.Send(&controlv1.ConnectResponse{
			Body: &controlv1.ConnectResponse_RoutePolicy{
				RoutePolicy: routePolicy,
			},
		}); err != nil {
			return err
		}
	}

	return nil
}

// handleHeartbeat 为后续更复杂的控制面状态机保留入口。
func (s *Server) handleHeartbeat(stream grpc.BidiStreamingServer[controlv1.ConnectRequest, controlv1.ConnectResponse], heartbeat *controlv1.DataplaneHeartbeat) error {
	if heartbeat == nil || heartbeat.GetDataplaneId() == "" {
		// 当前 heartbeat 不严格报错，尽量保持控制面最小实现简单可用。
		return nil
	}

	return nil
}

func (s *Server) handleSubscribe(stream grpc.BidiStreamingServer[controlv1.ConnectRequest, controlv1.ConnectResponse], subscriberID uint64, subscribe *controlv1.TargetSubscription) error {
	if subscribe == nil || len(subscribe.GetServices()) == 0 {
		return nil
	}

	targets := make([]model.ServiceRef, 0, len(subscribe.GetServices()))
	for _, service := range subscribe.GetServices() {
		if service == nil || strings.TrimSpace(service.GetService()) == "" {
			continue
		}

		target := model.ServiceRef{
			Service:   service.GetService(),
			Namespace: service.GetNamespace(),
			Env:       service.GetEnv(),
			Port:      service.GetPort(),
		}
		s.TrackTarget(target)
		targets = append(targets, target)
	}

	if len(targets) == 0 {
		return nil
	}
	s.updateSubscriberTargets(subscriberID, targets)
	selector := selectorFromSubscriber(s.lookupSubscriber(subscriberID))

	changedKeys := make(map[string]struct{})
	if s.loader != nil {
		changed, err := s.loader.RefreshMany(stream.Context(), targets)
		if err != nil {
			return err
		}
		for _, snapshot := range changed {
			changedKeys[targetKey(model.ServiceRef{
				Service:   snapshot.GetService().GetService(),
				Namespace: snapshot.GetService().GetNamespace(),
				Env:       snapshot.GetService().GetEnv(),
			})] = struct{}{}
			if err := stream.Send(&controlv1.ConnectResponse{
				Body: &controlv1.ConnectResponse_ServiceSnapshot{
					ServiceSnapshot: snapshot,
				},
			}); err != nil {
				return err
			}
		}
	}

	sentPolicies := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if _, ok := changedKeys[targetKey(target)]; ok {
		} else {
			snapshot, _ := s.store.Lookup(&controlv1.ServiceRef{
				Service:   target.Service,
				Namespace: target.Namespace,
				Env:       target.Env,
				Port:      target.Port,
			})
			if snapshot != nil {
				if err := stream.Send(&controlv1.ConnectResponse{
					Body: &controlv1.ConnectResponse_ServiceSnapshot{
						ServiceSnapshot: snapshot,
					},
				}); err != nil {
					return err
				}
			}
		}

		_, policy := s.store.Lookup(&controlv1.ServiceRef{
			Service:   target.Service,
			Namespace: target.Namespace,
			Env:       target.Env,
			Port:      target.Port,
		})
		if policy == nil {
			continue
		}
		if !matchesSelectors(selector, selectorFromRoutePolicy(policy, target, true)) {
			continue
		}
		key := targetKey(model.ServiceRef{
			Service:   policy.GetService().GetService(),
			Namespace: policy.GetService().GetNamespace(),
			Env:       policy.GetService().GetEnv(),
			Port:      policy.GetService().GetPort(),
		})
		if _, ok := sentPolicies[key]; ok {
			continue
		}
		sentPolicies[key] = struct{}{}
		if err := stream.Send(&controlv1.ConnectResponse{
			Body: &controlv1.ConnectResponse_RoutePolicy{
				RoutePolicy: policy,
			},
		}); err != nil {
			return err
		}
	}

	return nil
}

// TrackTarget 把目标服务加入控制面后续刷新的已知集合。
func (s *Server) TrackTarget(target model.ServiceRef) {
	if strings.TrimSpace(target.Service) == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.trackedTargets[targetKey(target)] = target
}

// UpsertRoutePolicy 写入或更新指定服务的路由策略，并按订阅目标/身份主动推送。
func (s *Server) UpsertRoutePolicy(policy *controlv1.RoutePolicy) bool {
	if policy == nil || policy.GetService() == nil {
		return false
	}

	_, current := s.store.Lookup(policy.GetService())
	if proto.Equal(current, policy) {
		return false
	}

	s.store.PutRoutePolicy(policy)
	target := model.ServiceRef{
		Service:   policy.GetService().GetService(),
		Namespace: policy.GetService().GetNamespace(),
		Env:       policy.GetService().GetEnv(),
		Port:      policy.GetService().GetPort(),
	}
	s.broadcastRoutePolicy(policy, target)
	return true
}

// RefreshTracked 刷新当前已知目标集合，并把变化快照推给已连接 dataplane。
func (s *Server) RefreshTracked(ctx context.Context) error {
	if s.loader == nil {
		return nil
	}

	targets := s.trackedTargetList()
	if len(targets) == 0 {
		return nil
	}

	changed, err := s.loader.RefreshMany(ctx, targets)
	if err != nil {
		return err
	}
	for _, snapshot := range changed {
		target := model.ServiceRef{
			Service:   snapshot.GetService().GetService(),
			Namespace: snapshot.GetService().GetNamespace(),
			Env:       snapshot.GetService().GetEnv(),
			Port:      snapshot.GetService().GetPort(),
		}
		s.broadcastForTarget(&controlv1.ConnectResponse{
			Body: &controlv1.ConnectResponse_ServiceSnapshot{
				ServiceSnapshot: snapshot,
			},
		}, target)
	}
	return nil
}

// StartBackgroundRefresh 周期性刷新已知目标集合。
func (s *Server) StartBackgroundRefresh(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		_ = s.RefreshTracked(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.RefreshTracked(ctx)
			}
		}
	}()
}

func (s *Server) addSubscriber() (uint64, <-chan *controlv1.ConnectResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextSubscriber++
	id := s.nextSubscriber
	ch := make(chan *controlv1.ConnectResponse, 16)
	s.subscribers[id] = &subscriber{
		pushCh:  ch,
		targets: make(map[string]model.ServiceRef),
	}
	return id, ch
}

func (s *Server) removeSubscriber(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sub, ok := s.subscribers[id]; ok {
		delete(s.subscribers, id)
		close(sub.pushCh)
	}
}

func (s *Server) broadcast(resp *controlv1.ConnectResponse) {
	s.broadcastForTarget(resp, model.ServiceRef{})
}

func (s *Server) broadcastRoutePolicy(policy *controlv1.RoutePolicy, target model.ServiceRef) {
	if policy == nil {
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, subscriber := range s.subscribers {
		if !matchesSelectors(selectorFromSubscriber(subscriber), selectorFromRoutePolicy(policy, target, true)) {
			continue
		}
		select {
		case subscriber.pushCh <- &controlv1.ConnectResponse{
			Body: &controlv1.ConnectResponse_RoutePolicy{
				RoutePolicy: policy,
			},
		}:
		default:
		}
	}
}

func (s *Server) broadcastForTarget(resp *controlv1.ConnectResponse, target model.ServiceRef) {
	resource := selectorFromResponse(resp, target)
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, subscriber := range s.subscribers {
		if !matchesSelectors(selectorFromSubscriber(subscriber), resource) {
			continue
		}
		select {
		case subscriber.pushCh <- resp:
		default:
		}
	}
}

func (s *Server) trackedTargetList() []model.ServiceRef {
	s.mu.RLock()
	defer s.mu.RUnlock()

	targets := make([]model.ServiceRef, 0, len(s.trackedTargets))
	for _, target := range s.trackedTargets {
		targets = append(targets, target)
	}
	return targets
}

func targetKey(target model.ServiceRef) string {
	return target.Namespace + "/" + target.Env + "/" + target.Service
}

func (s *Server) updateSubscriberIdentity(id uint64, identity *controlv1.DataplaneIdentity) {
	if id == 0 || identity == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if subscriber, ok := s.subscribers[id]; ok {
		subscriber.identity = identity
	}
}

func (s *Server) updateSubscriberTargets(id uint64, targets []model.ServiceRef) {
	if id == 0 || len(targets) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	subscriber, ok := s.subscribers[id]
	if !ok {
		return
	}
	for _, target := range targets {
		subscriber.targets[targetKey(target)] = target
	}
}

func (s *Server) lookupSubscriber(id uint64) *subscriber {
	if id == 0 {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	subscriber, ok := s.subscribers[id]
	if !ok {
		return nil
	}
	return subscriber
}
