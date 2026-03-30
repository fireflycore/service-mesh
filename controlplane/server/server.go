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
)

// Server 是控制面的最小 gRPC 服务实现。
type Server struct {
	controlv1.UnimplementedMeshControlPlaneServiceServer

	store  *snapshot.Store
	loader *snapshot.Loader

	mu             sync.RWMutex
	subscribers    map[uint64]chan *controlv1.ConnectResponse
	trackedTargets map[string]model.ServiceRef
	nextSubscriber uint64
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
		subscribers:    make(map[uint64]chan *controlv1.ConnectResponse),
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
		for {
			req, err := stream.Recv()
			recvCh <- recvResult{req: req, err: err}
			if err != nil {
				close(recvCh)
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
				if err := s.handleRegister(stream, body.Register); err != nil {
					return err
				}
			case *controlv1.ConnectRequest_Heartbeat:
				if err := s.handleHeartbeat(stream, body.Heartbeat); err != nil {
					return err
				}
			case *controlv1.ConnectRequest_Subscribe:
				if err := s.handleSubscribe(stream, body.Subscribe); err != nil {
					return err
				}
			}
		case resp := <-pushCh:
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

	for _, serviceSnapshot := range s.store.AllServiceSnapshots() {
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

	for _, routePolicy := range s.store.AllRoutePolicies() {
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

func (s *Server) handleSubscribe(stream grpc.BidiStreamingServer[controlv1.ConnectRequest, controlv1.ConnectResponse], subscribe *controlv1.TargetSubscription) error {
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

	for _, target := range targets {
		if _, ok := changedKeys[targetKey(target)]; ok {
			continue
		}
		snapshot, _ := s.store.Lookup(&controlv1.ServiceRef{
			Service:   target.Service,
			Namespace: target.Namespace,
			Env:       target.Env,
			Port:      target.Port,
		})
		if snapshot == nil {
			continue
		}
		if err := stream.Send(&controlv1.ConnectResponse{
			Body: &controlv1.ConnectResponse_ServiceSnapshot{
				ServiceSnapshot: snapshot,
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
		s.broadcast(&controlv1.ConnectResponse{
			Body: &controlv1.ConnectResponse_ServiceSnapshot{
				ServiceSnapshot: snapshot,
			},
		})
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
	s.subscribers[id] = ch
	return id, ch
}

func (s *Server) removeSubscriber(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if ch, ok := s.subscribers[id]; ok {
		delete(s.subscribers, id)
		close(ch)
	}
}

func (s *Server) broadcast(resp *controlv1.ConnectResponse) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, subscriber := range s.subscribers {
		select {
		case subscriber <- resp:
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
