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
	watchedTargets map[string]struct{}
	watchCtx       context.Context
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
		watchedTargets: make(map[string]struct{}),
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
	return newDeliveryCycle(s.store).RegisterBatch(register.GetIdentity()).Send(stream)
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
	subscriber := s.lookupSubscriber(subscriberID)
	cycle := newDeliveryCycle(s.store)
	var changed []*controlv1.ServiceSnapshot
	if s.loader != nil {
		var err error
		changed, err = s.loader.RefreshMany(stream.Context(), targets)
		if err != nil {
			return err
		}
	}

	return cycle.SubscribeBatch(subscriber, targets, changed).Send(stream)
}

// TrackTarget 把目标服务加入控制面后续刷新的已知集合。
func (s *Server) TrackTarget(target model.ServiceRef) {
	if strings.TrimSpace(target.Service) == "" {
		return
	}

	s.mu.Lock()
	s.trackedTargets[targetKey(target)] = target
	watchCtx := s.watchCtx
	s.mu.Unlock()

	if watchCtx != nil {
		s.startTargetWatch(watchCtx, target)
	}
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

func (s *Server) StartBackgroundWatch(ctx context.Context) {
	if ctx == nil {
		return
	}

	s.mu.Lock()
	s.watchCtx = ctx
	targets := make([]model.ServiceRef, 0, len(s.trackedTargets))
	for _, target := range s.trackedTargets {
		targets = append(targets, target)
	}
	s.mu.Unlock()

	for _, target := range targets {
		s.startTargetWatch(ctx, target)
	}
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

	newDeliveryCycle(s.store).TargetBroadcastBatch(s.subscribers, &controlv1.ConnectResponse{
		Body: &controlv1.ConnectResponse_RoutePolicy{
			RoutePolicy: policy,
		},
	}, target).Push()
}

func (s *Server) broadcastForTarget(resp *controlv1.ConnectResponse, target model.ServiceRef) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	newDeliveryCycle(s.store).TargetBroadcastBatch(s.subscribers, resp, target).Push()
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

func (s *Server) startTargetWatch(ctx context.Context, target model.ServiceRef) {
	if s.loader == nil || ctx == nil || strings.TrimSpace(target.Service) == "" {
		return
	}

	key := targetKey(target)

	s.mu.Lock()
	if _, ok := s.watchedTargets[key]; ok {
		s.mu.Unlock()
		return
	}
	s.watchedTargets[key] = struct{}{}
	s.mu.Unlock()

	updates, err := s.loader.Watch(ctx, target)
	if err != nil || updates == nil {
		s.mu.Lock()
		delete(s.watchedTargets, key)
		s.mu.Unlock()
		return
	}

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.watchedTargets, key)
			s.mu.Unlock()
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case update, ok := <-updates:
				if !ok {
					return
				}
				if update.Snapshot != nil {
					s.broadcastForTarget(snapshotResponse(update.Snapshot), update.Target)
				}
			}
		}
	}()
}
