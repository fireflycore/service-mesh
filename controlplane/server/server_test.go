package server

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/controlplane/snapshot"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source"
	"github.com/fireflycore/service-mesh/source/memory"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type sequenceProvider struct {
	mu        sync.Mutex
	snapshots map[string][]model.ServiceSnapshot
	counts    map[string]int
}

func (p *sequenceProvider) Name() string {
	return "sequence"
}

func (p *sequenceProvider) Resolve(_ context.Context, target model.ServiceRef) (model.ServiceSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := target.Namespace + "/" + target.Env + "/" + target.Service
	versions := p.snapshots[key]
	index := p.counts[key]
	if index >= len(versions) {
		index = len(versions) - 1
	}
	p.counts[key]++
	return versions[index], nil
}

type restartingWatchProvider struct {
	mu       sync.Mutex
	watchers []chan source.WatchEvent
	watches  int
}

func (p *restartingWatchProvider) Name() string {
	return "restarting-watch"
}

func (p *restartingWatchProvider) Resolve(context.Context, model.ServiceRef) (model.ServiceSnapshot, error) {
	return model.ServiceSnapshot{}, errors.New("not implemented")
}

func (p *restartingWatchProvider) Watch(ctx context.Context, target model.ServiceRef) (source.WatchStream, error) {
	p.mu.Lock()
	p.watches++
	watchIndex := p.watches
	ch := make(chan source.WatchEvent, 8)
	if watchIndex > 1 {
		p.watchers = append(p.watchers, ch)
	}
	p.mu.Unlock()

	if watchIndex == 1 {
		close(ch)
	}

	stream := &testWatchStream{events: ch}
	go func() {
		<-ctx.Done()
		_ = stream.Close()
	}()
	return stream, nil
}

func (p *restartingWatchProvider) Upsert(snapshot model.ServiceSnapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()

	event := source.WatchEvent{
		Kind:     source.WatchEventUpsert,
		Target:   snapshot.Service,
		Snapshot: snapshot,
	}
	for _, ch := range p.watchers {
		select {
		case ch <- event:
		default:
		}
	}
}

type testWatchStream struct {
	events chan source.WatchEvent
	once   sync.Once
}

func (s *testWatchStream) Events() <-chan source.WatchEvent {
	return s.events
}

func (s *testWatchStream) Close() error {
	s.once.Do(func() {
		defer func() {
			recover()
		}()
		close(s.events)
	})
	return nil
}

// TestServerConnectSendsSnapshotAndPolicy 验证 register 后会收到基础控制面状态。
func TestServerConnectSendsSnapshotAndPolicy(t *testing.T) {
	store := snapshot.NewStore()
	// 先把快照和策略预灌进 store，模拟一个已有控制面状态的场景。
	store.PutServiceSnapshot(&controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.10", Port: 19090, Weight: 1},
		},
		Revision: "v1",
	})
	store.PutRoutePolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
		},
		Retry: &controlv1.RetryPolicy{
			MaxAttempts:     1,
			PerTryTimeoutMs: 300,
		},
		TimeoutMs: 900,
	})
	store.PutRoutePolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Retry: &controlv1.RetryPolicy{
			MaxAttempts:     2,
			PerTryTimeoutMs: 500,
		},
		TimeoutMs: 1500,
	})
	store.PutServiceSnapshot(&controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "payments",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.11", Port: 29090, Weight: 1},
		},
		Revision: "v2",
	})
	store.PutServiceSnapshot(&controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.9", Port: 18090, Weight: 1},
		},
		Revision: "fallback",
	})
	store.PutServiceSnapshot(&controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "prod",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.12", Port: 39090, Weight: 1},
		},
		Revision: "v3",
	})
	store.PutRoutePolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "prod",
		},
		Retry: &controlv1.RetryPolicy{
			MaxAttempts:     3,
			PerTryTimeoutMs: 700,
		},
		TimeoutMs: 2000,
	})

	grpcServer := grpc.NewServer()
	controlv1.RegisterMeshControlPlaneServiceServer(grpcServer, New(store))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		_ = grpcServer.Serve(listener)
	}()
	defer grpcServer.Stop()

	conn, err := grpc.DialContext(
		context.Background(),
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	streamCtx, cancelStream := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelStream()
	stream, err := controlv1.NewMeshControlPlaneServiceClient(conn).Connect(streamCtx)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer stream.CloseSend()

	// register 后，服务端应该立刻回放当前控制面已知的快照和策略。
	if err := stream.Send(&controlv1.ConnectRequest{
		Body: &controlv1.ConnectRequest_Register{
			Register: &controlv1.DataplaneRegister{
				Identity: &controlv1.DataplaneIdentity{
					DataplaneId: "dp-1",
					Mode:        "agent",
					NodeId:      "node-1",
					Namespace:   "default",
					Service:     "service-mesh-agent",
					Env:         "dev",
				},
			},
		},
	}); err != nil {
		t.Fatalf("send register failed: %v", err)
	}

	var snapshotCount int
	var policyCount int
	var ordersSnapshotAddress string
	var ordersPolicyTimeout uint64
	for i := 0; i < 3; i++ {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv replay response failed: %v", err)
		}
		if resp.GetServiceSnapshot() != nil {
			snapshotCount++
			if resp.GetServiceSnapshot().GetService().GetService() == "orders" {
				ordersSnapshotAddress = resp.GetServiceSnapshot().GetEndpoints()[0].GetAddress()
			}
			continue
		}
		if resp.GetRoutePolicy() != nil {
			policyCount++
			if resp.GetRoutePolicy().GetService().GetService() == "orders" {
				ordersPolicyTimeout = resp.GetRoutePolicy().GetTimeoutMs()
			}
			continue
		}
		t.Fatalf("expected snapshot or route policy response")
	}
	if got, want := snapshotCount, 2; got != want {
		t.Fatalf("unexpected snapshot replay count: got=%d want=%d", got, want)
	}
	if got, want := policyCount, 1; got != want {
		t.Fatalf("unexpected route policy replay count: got=%d want=%d", got, want)
	}
	if got, want := ordersSnapshotAddress, "10.0.0.10"; got != want {
		t.Fatalf("unexpected orders snapshot chosen by priority: got=%s want=%s", got, want)
	}
	if got, want := ordersPolicyTimeout, uint64(1500); got != want {
		t.Fatalf("unexpected orders route policy chosen by priority: got=%d want=%d", got, want)
	}
}

func TestServerRefreshTrackedBroadcastsChangedSnapshot(t *testing.T) {
	store := snapshot.NewStore()
	loader := snapshot.NewLoader(
		store,
		memory.New(map[string]model.ServiceSnapshot{
			"default/dev/orders": {
				Service: model.ServiceRef{
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
				Endpoints: []model.Endpoint{
					{Address: "10.0.0.21", Port: 19090, Weight: 1},
				},
			},
		}),
	)
	srv := NewWithLoader(store, loader)
	srv.TrackTarget(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})

	grpcServer := grpc.NewServer()
	controlv1.RegisterMeshControlPlaneServiceServer(grpcServer, srv)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		_ = grpcServer.Serve(listener)
	}()
	defer grpcServer.Stop()

	conn, err := grpc.DialContext(
		context.Background(),
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	streamCtx, cancelStream := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelStream()
	stream, err := controlv1.NewMeshControlPlaneServiceClient(conn).Connect(streamCtx)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer stream.CloseSend()

	if err := stream.Send(&controlv1.ConnectRequest{
		Body: &controlv1.ConnectRequest_Register{
			Register: &controlv1.DataplaneRegister{
				Identity: &controlv1.DataplaneIdentity{
					DataplaneId: "dp-1",
					Mode:        "agent",
					NodeId:      "node-1",
					Namespace:   "default",
					Service:     "service-mesh-agent",
					Env:         "dev",
				},
			},
		},
	}); err != nil {
		t.Fatalf("send register failed: %v", err)
	}

	if err := srv.RefreshTracked(context.Background()); err != nil {
		t.Fatalf("refresh tracked failed: %v", err)
	}

	recvCh := make(chan *controlv1.ConnectResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, recvErr := stream.Recv()
		if recvErr != nil {
			errCh <- recvErr
			return
		}
		recvCh <- resp
	}()

	select {
	case err := <-errCh:
		t.Fatalf("recv pushed snapshot failed: %v", err)
	case resp := <-recvCh:
		if resp.GetServiceSnapshot() == nil {
			t.Fatalf("expected pushed service snapshot")
		}
		if got, want := resp.GetServiceSnapshot().GetService().GetService(), "orders"; got != want {
			t.Fatalf("unexpected pushed service: got=%s want=%s", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("expected pushed snapshot after refresh")
	}
}

func TestServerRefreshTrackedPushesOnlyToSubscribedTargets(t *testing.T) {
	store := snapshot.NewStore()
	loader := snapshot.NewLoader(
		store,
		&sequenceProvider{
			snapshots: map[string][]model.ServiceSnapshot{
				"default/dev/orders": {
					{
						Service: model.ServiceRef{
							Service:   "orders",
							Namespace: "default",
							Env:       "dev",
						},
						Endpoints: []model.Endpoint{{Address: "10.0.0.31", Port: 19090, Weight: 1}},
					},
					{
						Service: model.ServiceRef{
							Service:   "orders",
							Namespace: "default",
							Env:       "dev",
						},
						Endpoints: []model.Endpoint{{Address: "10.0.0.41", Port: 19090, Weight: 1}},
					},
				},
				"default/dev/payments": {
					{
						Service: model.ServiceRef{
							Service:   "payments",
							Namespace: "default",
							Env:       "dev",
						},
						Endpoints: []model.Endpoint{{Address: "10.0.0.32", Port: 19091, Weight: 1}},
					},
					{
						Service: model.ServiceRef{
							Service:   "payments",
							Namespace: "default",
							Env:       "dev",
						},
						Endpoints: []model.Endpoint{{Address: "10.0.0.42", Port: 19091, Weight: 1}},
					},
				},
			},
			counts: make(map[string]int),
		},
	)
	srv := NewWithLoader(store, loader)
	srv.TrackTarget(model.ServiceRef{Service: "orders", Namespace: "default", Env: "dev"})
	srv.TrackTarget(model.ServiceRef{Service: "payments", Namespace: "default", Env: "dev"})

	grpcServer := grpc.NewServer()
	controlv1.RegisterMeshControlPlaneServiceServer(grpcServer, srv)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		_ = grpcServer.Serve(listener)
	}()
	defer grpcServer.Stop()

	dial := func() (controlv1.MeshControlPlaneService_ConnectClient, context.CancelFunc) {
		conn, err := grpc.DialContext(
			context.Background(),
			listener.Addr().String(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })

		streamCtx, cancelStream := context.WithTimeout(context.Background(), 2*time.Second)
		stream, err := controlv1.NewMeshControlPlaneServiceClient(conn).Connect(streamCtx)
		if err != nil {
			t.Fatalf("connect failed: %v", err)
		}
		if err := stream.Send(&controlv1.ConnectRequest{
			Body: &controlv1.ConnectRequest_Register{
				Register: &controlv1.DataplaneRegister{
					Identity: &controlv1.DataplaneIdentity{
						DataplaneId: "dp-1",
						Mode:        "agent",
						NodeId:      "node-1",
						Namespace:   "default",
						Service:     "service-mesh-agent",
						Env:         "dev",
					},
				},
			},
		}); err != nil {
			t.Fatalf("send register failed: %v", err)
		}
		return stream, cancelStream
	}

	ordersStream, cancelOrders := dial()
	defer cancelOrders()
	defer ordersStream.CloseSend()
	paymentsStream, cancelPayments := dial()
	defer cancelPayments()
	defer paymentsStream.CloseSend()

	if err := ordersStream.Send(&controlv1.ConnectRequest{
		Body: &controlv1.ConnectRequest_Subscribe{
			Subscribe: &controlv1.TargetSubscription{
				Services: []*controlv1.ServiceRef{{Service: "orders", Namespace: "default", Env: "dev"}},
			},
		},
	}); err != nil {
		t.Fatalf("subscribe orders failed: %v", err)
	}
	if err := paymentsStream.Send(&controlv1.ConnectRequest{
		Body: &controlv1.ConnectRequest_Subscribe{
			Subscribe: &controlv1.TargetSubscription{
				Services: []*controlv1.ServiceRef{{Service: "payments", Namespace: "default", Env: "dev"}},
			},
		},
	}); err != nil {
		t.Fatalf("subscribe payments failed: %v", err)
	}

	drainSnapshot := func(stream controlv1.MeshControlPlaneService_ConnectClient, expectedService string) {
		deadline := time.After(time.Second)
		for {
			recvCh := make(chan *controlv1.ConnectResponse, 1)
			errCh := make(chan error, 1)
			go func() {
				resp, err := stream.Recv()
				if err != nil {
					errCh <- err
					return
				}
				recvCh <- resp
			}()
			select {
			case err := <-errCh:
				t.Fatalf("recv subscribed snapshot failed: %v", err)
			case resp := <-recvCh:
				if resp.GetServiceSnapshot() == nil {
					continue
				}
				if got, want := resp.GetServiceSnapshot().GetService().GetService(), expectedService; got == want {
					return
				}
			case <-deadline:
				t.Fatal("expected subscribed snapshot")
			}
		}
	}
	drainSnapshot(ordersStream, "orders")
	drainSnapshot(paymentsStream, "payments")

	if err := srv.RefreshTracked(context.Background()); err != nil {
		t.Fatalf("refresh tracked failed: %v", err)
	}

	recvSnapshot := func(stream controlv1.MeshControlPlaneService_ConnectClient, expectedService string) *controlv1.ServiceSnapshot {
		deadline := time.After(time.Second)
		for {
			recvCh := make(chan *controlv1.ConnectResponse, 1)
			errCh := make(chan error, 1)
			go func() {
				resp, err := stream.Recv()
				if err != nil {
					errCh <- err
					return
				}
				recvCh <- resp
			}()
			select {
			case err := <-errCh:
				t.Fatalf("recv failed: %v", err)
			case resp := <-recvCh:
				if resp.GetServiceSnapshot() == nil {
					continue
				}
				if got := resp.GetServiceSnapshot().GetService().GetService(); got == expectedService {
					return resp.GetServiceSnapshot()
				}
			case <-deadline:
				t.Fatal("expected pushed snapshot")
			}
		}
	}

	ordersSnapshot := recvSnapshot(ordersStream, "orders")
	if got, want := ordersSnapshot.GetEndpoints()[0].GetAddress(), "10.0.0.41"; got != want {
		t.Fatalf("unexpected orders snapshot address: got=%s want=%s", got, want)
	}
	paymentsSnapshot := recvSnapshot(paymentsStream, "payments")
	if got, want := paymentsSnapshot.GetEndpoints()[0].GetAddress(), "10.0.0.42"; got != want {
		t.Fatalf("unexpected payments snapshot address: got=%s want=%s", got, want)
	}
}

func TestServerUpsertRoutePolicyPushesOnlyToMatchingSubscribers(t *testing.T) {
	store := snapshot.NewStore()
	store.PutServiceSnapshot(&controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.31", Port: 19090, Weight: 1},
		},
		Revision: "v1",
	})
	srv := New(store)

	grpcServer := grpc.NewServer()
	controlv1.RegisterMeshControlPlaneServiceServer(grpcServer, srv)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		_ = grpcServer.Serve(listener)
	}()
	defer grpcServer.Stop()

	dial := func(identity *controlv1.DataplaneIdentity, timeout time.Duration) (controlv1.MeshControlPlaneService_ConnectClient, context.CancelFunc) {
		conn, err := grpc.DialContext(
			context.Background(),
			listener.Addr().String(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			t.Fatalf("dial failed: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })

		streamCtx, cancelStream := context.WithTimeout(context.Background(), timeout)
		stream, err := controlv1.NewMeshControlPlaneServiceClient(conn).Connect(streamCtx)
		if err != nil {
			t.Fatalf("connect failed: %v", err)
		}
		if err := stream.Send(&controlv1.ConnectRequest{
			Body: &controlv1.ConnectRequest_Register{
				Register: &controlv1.DataplaneRegister{
					Identity: identity,
				},
			},
		}); err != nil {
			t.Fatalf("send register failed: %v", err)
		}
		return stream, cancelStream
	}

	devStream, cancelDev := dial(&controlv1.DataplaneIdentity{
		DataplaneId: "dp-dev",
		Mode:        "agent",
		NodeId:      "node-dev",
		Namespace:   "default",
		Service:     "service-mesh-agent",
		Env:         "dev",
	}, 2*time.Second)
	defer cancelDev()
	defer devStream.CloseSend()
	prodStream, cancelProd := dial(&controlv1.DataplaneIdentity{
		DataplaneId: "dp-prod",
		Mode:        "agent",
		NodeId:      "node-prod",
		Namespace:   "default",
		Service:     "service-mesh-agent",
		Env:         "prod",
	}, 500*time.Millisecond)
	defer cancelProd()
	defer prodStream.CloseSend()

	subscribeOrders := func(stream controlv1.MeshControlPlaneService_ConnectClient) {
		if err := stream.Send(&controlv1.ConnectRequest{
			Body: &controlv1.ConnectRequest_Subscribe{
				Subscribe: &controlv1.TargetSubscription{
					Services: []*controlv1.ServiceRef{{Service: "orders", Namespace: "default", Env: "dev"}},
				},
			},
		}); err != nil {
			t.Fatalf("subscribe orders failed: %v", err)
		}
	}
	subscribeOrders(devStream)
	subscribeOrders(prodStream)

	drainSnapshot := func(stream controlv1.MeshControlPlaneService_ConnectClient) {
		deadline := time.After(time.Second)
		for {
			resp, err := stream.Recv()
			if err != nil {
				t.Fatalf("recv snapshot failed: %v", err)
			}
			if resp.GetServiceSnapshot() != nil {
				return
			}
			select {
			case <-deadline:
				t.Fatal("expected subscribed snapshot")
			default:
			}
		}
	}
	drainSnapshot(devStream)

	resultCh := make(chan *controlv1.ConnectResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := prodStream.Recv()
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- resp
	}()

	select {
	case resp := <-resultCh:
		if resp.GetServiceSnapshot() != nil || resp.GetRoutePolicy() != nil {
			t.Fatal("did not expect prod subscriber to receive subscribed state for dev target")
		}
	case <-errCh:
	case <-time.After(300 * time.Millisecond):
	}

	changed := srv.UpsertRoutePolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Retry: &controlv1.RetryPolicy{
			MaxAttempts:     3,
			PerTryTimeoutMs: 700,
		},
		TimeoutMs: 2000,
	})
	if !changed {
		t.Fatal("expected route policy upsert to be treated as change")
	}

	deadline := time.After(time.Second)
	for {
		resp, err := devStream.Recv()
		if err != nil {
			t.Fatalf("recv route policy failed: %v", err)
		}
		if resp.GetRoutePolicy() != nil {
			if got, want := resp.GetRoutePolicy().GetTimeoutMs(), uint64(2000); got != want {
				t.Fatalf("unexpected route policy timeout: got=%d want=%d", got, want)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("expected route policy push")
		default:
		}
	}

	resultCh = make(chan *controlv1.ConnectResponse, 1)
	errCh = make(chan error, 1)
	go func() {
		resp, err := prodStream.Recv()
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- resp
	}()

	select {
	case resp := <-resultCh:
		if resp.GetRoutePolicy() != nil {
			t.Fatal("did not expect prod subscriber to receive dev route policy")
		}
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected timeout or cancellation for prod subscriber")
		}
	case <-time.After(300 * time.Millisecond):
	}
}

func TestMatchesDeliveryUsesUnifiedSubscriptionAndIdentityRules(t *testing.T) {
	sub := &subscriber{
		identity: &controlv1.DataplaneIdentity{
			Namespace: "default",
			Env:       "dev",
		},
		targets: map[string]model.ServiceRef{
			"default/dev/orders": {
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
		},
	}

	if !matchesSelectors(selectorFromSubscriber(sub), resourceSelector{
		target:              model.ServiceRef{Service: "orders", Namespace: "default", Env: "dev"},
		service:             &controlv1.ServiceRef{Service: "orders", Namespace: "default", Env: "dev"},
		requireSubscription: true,
		requireIdentity:     true,
	}) {
		t.Fatal("expected matching delivery to pass")
	}

	if matchesSelectors(selectorFromSubscriber(sub), resourceSelector{
		target:              model.ServiceRef{Service: "payments", Namespace: "default", Env: "dev"},
		service:             &controlv1.ServiceRef{Service: "payments", Namespace: "default", Env: "dev"},
		requireSubscription: true,
		requireIdentity:     true,
	}) {
		t.Fatal("expected different subscribed target to fail")
	}

	if matchesSelectors(selectorFromSubscriber(sub), resourceSelector{
		target:              model.ServiceRef{Service: "orders", Namespace: "default", Env: "dev"},
		service:             &controlv1.ServiceRef{Service: "orders", Namespace: "default", Env: "prod"},
		requireSubscription: true,
		requireIdentity:     true,
	}) {
		t.Fatal("expected mismatched identity scope to fail")
	}
}

func TestEvaluateSelectorMatchExposesExactAndFallbackPriority(t *testing.T) {
	exactSubscriber := selectorFromSubscriber(&subscriber{
		identity: &controlv1.DataplaneIdentity{
			Namespace: "default",
			Env:       "dev",
		},
		targets: map[string]model.ServiceRef{
			"default/dev/orders": {
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
		},
	})

	exactMatch := evaluateSelectorMatch(exactSubscriber, resourceSelector{
		target:              model.ServiceRef{Service: "orders", Namespace: "default", Env: "dev"},
		service:             &controlv1.ServiceRef{Service: "orders", Namespace: "default", Env: "dev"},
		requireSubscription: true,
		requireIdentity:     true,
	})
	if exactMatch.subscription != matchPriorityExact {
		t.Fatalf("expected exact subscription priority, got=%d", exactMatch.subscription)
	}
	if exactMatch.identity != matchPriorityExact {
		t.Fatalf("expected exact identity priority, got=%d", exactMatch.identity)
	}

	fallbackSubscriber := selectorFromSubscriber(&subscriber{
		identity: &controlv1.DataplaneIdentity{
			Namespace: "default",
			Env:       "dev",
		},
	})
	fallbackMatch := evaluateSelectorMatch(fallbackSubscriber, resourceSelector{
		target:              model.ServiceRef{Service: "orders", Namespace: "default", Env: "dev"},
		service:             &controlv1.ServiceRef{Service: "orders", Namespace: "default"},
		requireSubscription: true,
		requireIdentity:     true,
	})
	if fallbackMatch.subscription != matchPriorityFallback {
		t.Fatalf("expected fallback subscription priority, got=%d", fallbackMatch.subscription)
	}
	if fallbackMatch.identity != matchPriorityFallback {
		t.Fatalf("expected fallback identity priority, got=%d", fallbackMatch.identity)
	}
	if !fallbackMatch.matched() {
		t.Fatal("expected fallback match to still be considered matched")
	}
}

func TestResourceArbitratorPrefersExactCandidateForTarget(t *testing.T) {
	arbitrator := newResourceArbitrator(
		[]*controlv1.ServiceSnapshot{
			{
				Service: &controlv1.ServiceRef{
					Service:   "orders",
					Namespace: "default",
				},
				Endpoints: []*controlv1.Endpoint{{Address: "10.0.0.9", Port: 18090, Weight: 1}},
			},
			{
				Service: &controlv1.ServiceRef{
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
				Endpoints: []*controlv1.Endpoint{{Address: "10.0.0.10", Port: 19090, Weight: 1}},
			},
		},
		[]*controlv1.RoutePolicy{
			{
				Service: &controlv1.ServiceRef{
					Service:   "orders",
					Namespace: "default",
				},
				TimeoutMs: 900,
			},
			{
				Service: &controlv1.ServiceRef{
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
				TimeoutMs: 1500,
			},
		},
		&controlv1.DataplaneIdentity{
			Namespace: "default",
			Env:       "dev",
		},
	)

	snapshot := arbitrator.SnapshotForTarget(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if snapshot == nil || snapshot.GetEndpoints()[0].GetAddress() != "10.0.0.10" {
		t.Fatal("expected exact snapshot candidate to win arbitration")
	}

	policy := arbitrator.PolicyForTarget(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if policy == nil || policy.GetTimeoutMs() != 1500 {
		t.Fatal("expected exact route policy candidate to win arbitration")
	}
}

func TestArbitrationCacheReusesIdentityScopedArbitrator(t *testing.T) {
	cache := newArbitrationCache(nil, nil)
	identity := &controlv1.DataplaneIdentity{
		Namespace:   "default",
		Env:         "dev",
		DataplaneId: "dp-1",
		NodeId:      "node-1",
	}

	first := cache.ForIdentity(identity)
	second := cache.ForIdentity(identity)

	if len(cache.byKey) != 1 {
		t.Fatalf("expected single cached arbitrator, got=%d", len(cache.byKey))
	}
	if len(first.snapshots) != len(second.snapshots) || len(first.policies) != len(second.policies) {
		t.Fatal("expected repeated lookup to reuse same identity-scoped arbitration result")
	}
}

func TestDeliveryCycleReusesCachedArbitrationAcrossAccessors(t *testing.T) {
	store := snapshot.NewStore()
	store.PutServiceSnapshot(&controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.10", Port: 19090, Weight: 1},
		},
	})
	store.PutRoutePolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		TimeoutMs: 1500,
	})

	cycle := newDeliveryCycle(store)
	identity := &controlv1.DataplaneIdentity{
		Namespace:   "default",
		Env:         "dev",
		DataplaneId: "dp-1",
		NodeId:      "node-1",
	}
	sub := &subscriber{identity: identity}

	_ = cycle.ForIdentity(identity)
	_ = cycle.ForSubscriber(sub)

	if got, want := len(cycle.cache.byKey), 1; got != want {
		t.Fatalf("unexpected cached arbitrator count: got=%d want=%d", got, want)
	}
}

func TestDeliveryCycleResolvesTargetScopedSnapshotAndPolicyForSubscriber(t *testing.T) {
	store := snapshot.NewStore()
	store.PutServiceSnapshot(&controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.9", Port: 18090, Weight: 1},
		},
	})
	store.PutServiceSnapshot(&controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.10", Port: 19090, Weight: 1},
		},
	})
	store.PutRoutePolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
		},
		TimeoutMs: 900,
	})
	store.PutRoutePolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		TimeoutMs: 1500,
	})

	cycle := newDeliveryCycle(store)
	sub := &subscriber{
		identity: &controlv1.DataplaneIdentity{
			Namespace: "default",
			Env:       "dev",
		},
		targets: map[string]model.ServiceRef{
			"default/dev/orders": {
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
		},
	}
	target := model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	}

	snapshot := cycle.SnapshotForSubscriberTarget(sub, target)
	if snapshot == nil || snapshot.GetEndpoints()[0].GetAddress() != "10.0.0.10" {
		t.Fatal("expected target-scoped snapshot lookup to choose exact candidate")
	}
	policy := cycle.PolicyForSubscriberTarget(sub, target)
	if policy == nil || policy.GetTimeoutMs() != 1500 {
		t.Fatal("expected target-scoped policy lookup to choose exact candidate")
	}
}

func TestDeliveryCycleSubscribeHelpersHideChangedSnapshotAndDeduplicatePolicy(t *testing.T) {
	store := snapshot.NewStore()
	exactSnapshot := &controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.10", Port: 19090, Weight: 1},
		},
		Revision: "exact",
	}
	store.PutServiceSnapshot(exactSnapshot)
	store.PutRoutePolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		TimeoutMs: 1500,
	})

	cycle := newDeliveryCycle(store)
	sub := &subscriber{
		identity: &controlv1.DataplaneIdentity{
			Namespace: "default",
			Env:       "dev",
		},
		targets: map[string]model.ServiceRef{
			"default/dev/orders": {
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
			"default/orders": {
				Service:   "orders",
				Namespace: "default",
			},
		},
	}

	target := model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	}
	cycle.RememberChangedSnapshot(exactSnapshot)
	if snapshot := cycle.SnapshotForSubscribeTarget(sub, target); snapshot != nil {
		t.Fatal("expected changed snapshot to be suppressed during subscribe delivery")
	}

	firstPolicy := cycle.PolicyForSubscribeTarget(sub, target)
	if firstPolicy == nil {
		t.Fatal("expected first subscribe target to receive policy")
	}
	secondPolicy := cycle.PolicyForSubscribeTarget(sub, model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
	})
	if secondPolicy != nil {
		t.Fatal("expected already-delivered policy to be deduplicated")
	}
}

func TestDeliveryCycleSubscribeBatchBuildsExpectedStreamPayloads(t *testing.T) {
	store := snapshot.NewStore()
	store.PutRoutePolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		TimeoutMs: 1500,
	})

	cycle := newDeliveryCycle(store)
	sub := &subscriber{
		identity: &controlv1.DataplaneIdentity{
			Namespace: "default",
			Env:       "dev",
		},
		targets: map[string]model.ServiceRef{
			"default/dev/orders": {
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
		},
	}
	changed := []*controlv1.ServiceSnapshot{
		{
			Service: &controlv1.ServiceRef{
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
			Endpoints: []*controlv1.Endpoint{
				{Address: "10.0.0.10", Port: 19090, Weight: 1},
			},
			Revision: "v1",
		},
	}

	batch := cycle.SubscribeBatch(sub, []model.ServiceRef{{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	}}, changed)
	if got, want := len(batch.streamResponses), 2; got != want {
		t.Fatalf("unexpected subscribe response count: got=%d want=%d", got, want)
	}
	if batch.streamResponses[0].GetServiceSnapshot() == nil {
		t.Fatal("expected first subscribe response to be changed snapshot")
	}
	if batch.streamResponses[1].GetRoutePolicy() == nil {
		t.Fatal("expected second subscribe response to be route policy")
	}
}

func TestDeliveryCycleRegisterBatchBuildsReplayPayloads(t *testing.T) {
	store := snapshot.NewStore()
	store.PutServiceSnapshot(&controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.10", Port: 19090, Weight: 1},
		},
	})
	store.PutRoutePolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		TimeoutMs: 1500,
	})

	batch := newDeliveryCycle(store).RegisterBatch(&controlv1.DataplaneIdentity{
		Namespace: "default",
		Env:       "dev",
	})
	if got, want := len(batch.streamResponses), 2; got != want {
		t.Fatalf("unexpected register batch response count: got=%d want=%d", got, want)
	}
	if batch.streamResponses[0].GetServiceSnapshot() == nil {
		t.Fatal("expected register batch to include snapshot replay")
	}
	if batch.streamResponses[1].GetRoutePolicy() == nil {
		t.Fatal("expected register batch to include policy replay")
	}
}

func TestDeliveryBatchBuilderSkipsNilAndPreservesOrder(t *testing.T) {
	builder := newDeliveryBatchBuilder(4, 2)
	builder.addStreamSnapshot(nil)
	builder.addStreamPolicy(nil)
	builder.addStreamSnapshot(&controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
	})
	builder.addStreamPolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		TimeoutMs: 1500,
	})
	batch := builder.build()

	if got, want := len(batch.streamResponses), 2; got != want {
		t.Fatalf("unexpected batch stream response count: got=%d want=%d", got, want)
	}
	if batch.streamResponses[0].GetServiceSnapshot() == nil {
		t.Fatal("expected snapshot response to stay first")
	}
	if batch.streamResponses[1].GetRoutePolicy() == nil {
		t.Fatal("expected policy response to stay second")
	}
}

func TestDeliveryBatchBuilderAddsCollectionsAndPushResponses(t *testing.T) {
	builder := newDeliveryBatchBuilder(4, 2)
	pushCh := make(chan *controlv1.ConnectResponse, 1)

	builder.addStreamSnapshots([]*controlv1.ServiceSnapshot{
		nil,
		{
			Service: &controlv1.ServiceRef{
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
		},
	})
	builder.addStreamPolicies([]*controlv1.RoutePolicy{
		nil,
		{
			Service: &controlv1.ServiceRef{
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
			TimeoutMs: 1500,
		},
	})
	builder.addPushResponse(pushCh, nil)
	builder.addPushResponse(pushCh, routePolicyResponse(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		TimeoutMs: 1500,
	}))

	batch := builder.build()
	if got, want := len(batch.streamResponses), 2; got != want {
		t.Fatalf("unexpected builder stream response count: got=%d want=%d", got, want)
	}
	if got, want := len(batch.deliveries), 1; got != want {
		t.Fatalf("unexpected builder delivery count: got=%d want=%d", got, want)
	}
	if batch.deliveries[0].response.GetRoutePolicy() == nil {
		t.Fatal("expected push response to keep route policy payload")
	}
}

func TestDeliveryCycleBuildsTargetedBroadcastPlan(t *testing.T) {
	store := snapshot.NewStore()
	exactSnapshot := &controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.10", Port: 19090, Weight: 1},
		},
		Revision: "exact",
	}
	store.PutServiceSnapshot(exactSnapshot)

	cycle := newDeliveryCycle(store)
	allowed := &subscriber{
		pushCh: make(chan *controlv1.ConnectResponse, 1),
		identity: &controlv1.DataplaneIdentity{
			Namespace: "default",
			Env:       "dev",
		},
		targets: map[string]model.ServiceRef{
			"default/dev/orders": {
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
		},
	}
	blocked := &subscriber{
		pushCh: make(chan *controlv1.ConnectResponse, 1),
		identity: &controlv1.DataplaneIdentity{
			Namespace: "other",
			Env:       "dev",
		},
		targets: map[string]model.ServiceRef{
			"other/dev/orders": {
				Service:   "orders",
				Namespace: "other",
				Env:       "dev",
			},
		},
	}

	batch := cycle.TargetBroadcastBatch(map[uint64]*subscriber{
		1: allowed,
		2: blocked,
	}, &controlv1.ConnectResponse{
		Body: &controlv1.ConnectResponse_ServiceSnapshot{
			ServiceSnapshot: exactSnapshot,
		},
	}, model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if got, want := len(batch.deliveries), 1; got != want {
		t.Fatalf("unexpected targeted delivery count: got=%d want=%d", got, want)
	}
}

func TestDeliveryCycleTargetBroadcastBatchBuildsDeliveries(t *testing.T) {
	store := snapshot.NewStore()
	exactSnapshot := &controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.10", Port: 19090, Weight: 1},
		},
		Revision: "exact",
	}
	store.PutServiceSnapshot(exactSnapshot)

	allowed := &subscriber{
		pushCh: make(chan *controlv1.ConnectResponse, 1),
		identity: &controlv1.DataplaneIdentity{
			Namespace: "default",
			Env:       "dev",
		},
		targets: map[string]model.ServiceRef{
			"default/dev/orders": {
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
		},
	}
	batch := newDeliveryCycle(store).TargetBroadcastBatch(map[uint64]*subscriber{
		1: allowed,
	}, &controlv1.ConnectResponse{
		Body: &controlv1.ConnectResponse_ServiceSnapshot{
			ServiceSnapshot: exactSnapshot,
		},
	}, model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if got, want := len(batch.deliveries), 1; got != want {
		t.Fatalf("unexpected target broadcast batch count: got=%d want=%d", got, want)
	}
	if batch.deliveries[0].response.GetServiceSnapshot() == nil {
		t.Fatal("expected target broadcast batch to keep snapshot response")
	}
}

func TestDeliveryCycleTargetBroadcastBatchBuildsPolicyDeliveries(t *testing.T) {
	store := snapshot.NewStore()
	exactPolicy := &controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		TimeoutMs: 1500,
	}
	fallbackPolicy := &controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
		},
		TimeoutMs: 900,
	}
	store.PutRoutePolicy(fallbackPolicy)
	store.PutRoutePolicy(exactPolicy)

	allowed := &subscriber{
		pushCh: make(chan *controlv1.ConnectResponse, 1),
		identity: &controlv1.DataplaneIdentity{
			Namespace: "default",
			Env:       "dev",
		},
		targets: map[string]model.ServiceRef{
			"default/dev/orders": {
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
		},
	}
	blocked := &subscriber{
		pushCh: make(chan *controlv1.ConnectResponse, 1),
		identity: &controlv1.DataplaneIdentity{
			Namespace: "default",
			Env:       "prod",
		},
		targets: map[string]model.ServiceRef{
			"default/prod/orders": {
				Service:   "orders",
				Namespace: "default",
				Env:       "prod",
			},
		},
	}

	batch := newDeliveryCycle(store).TargetBroadcastBatch(map[uint64]*subscriber{
		1: allowed,
		2: blocked,
	}, &controlv1.ConnectResponse{
		Body: &controlv1.ConnectResponse_RoutePolicy{
			RoutePolicy: exactPolicy,
		},
	}, model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if got, want := len(batch.deliveries), 1; got != want {
		t.Fatalf("unexpected policy broadcast batch count: got=%d want=%d", got, want)
	}
	if batch.deliveries[0].response.GetRoutePolicy() == nil {
		t.Fatal("expected policy broadcast batch to keep route policy response")
	}
	if got, want := batch.deliveries[0].response.GetRoutePolicy().GetTimeoutMs(), uint64(1500); got != want {
		t.Fatalf("unexpected policy timeout in broadcast batch: got=%d want=%d", got, want)
	}
}

func TestServerShouldPushFallbackPolicyOnlyWhenItIsBestCandidate(t *testing.T) {
	store := snapshot.NewStore()
	fallbackPolicy := &controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
		},
		TimeoutMs: 900,
	}
	exactPolicy := &controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		TimeoutMs: 1500,
	}
	store.PutRoutePolicy(fallbackPolicy)
	store.PutRoutePolicy(exactPolicy)

	cycle := newDeliveryCycle(store)
	sub := &subscriber{
		identity: &controlv1.DataplaneIdentity{
			Namespace: "default",
			Env:       "dev",
		},
		targets: map[string]model.ServiceRef{
			"default/dev/orders": {
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
		},
	}

	if cycle.AllowsPolicy(sub, fallbackPolicy) {
		t.Fatal("expected fallback policy to be skipped when exact policy exists")
	}
	if !cycle.AllowsPolicy(sub, exactPolicy) {
		t.Fatal("expected exact policy to be selected for subscriber")
	}
}

func TestServerShouldPushFallbackSnapshotOnlyWhenItIsBestCandidate(t *testing.T) {
	store := snapshot.NewStore()
	fallbackSnapshot := &controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.9", Port: 18090, Weight: 1},
		},
		Revision: "fallback",
	}
	exactSnapshot := &controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []*controlv1.Endpoint{
			{Address: "10.0.0.10", Port: 19090, Weight: 1},
		},
		Revision: "exact",
	}
	store.PutServiceSnapshot(fallbackSnapshot)
	store.PutServiceSnapshot(exactSnapshot)

	cycle := newDeliveryCycle(store)
	sub := &subscriber{
		identity: &controlv1.DataplaneIdentity{
			Namespace: "default",
			Env:       "dev",
		},
		targets: map[string]model.ServiceRef{
			"default/dev/orders": {
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
		},
	}

	if cycle.AllowsResponse(sub, &controlv1.ConnectResponse{
		Body: &controlv1.ConnectResponse_ServiceSnapshot{ServiceSnapshot: fallbackSnapshot},
	}) {
		t.Fatal("expected fallback snapshot to be skipped when exact snapshot exists")
	}
	if !cycle.AllowsResponse(sub, &controlv1.ConnectResponse{
		Body: &controlv1.ConnectResponse_ServiceSnapshot{ServiceSnapshot: exactSnapshot},
	}) {
		t.Fatal("expected exact snapshot to be selected for subscriber")
	}
}

func TestServerBackgroundRefreshPushesTrackedSnapshots(t *testing.T) {
	store := snapshot.NewStore()
	loader := snapshot.NewLoader(
		store,
		memory.New(map[string]model.ServiceSnapshot{
			"default/dev/orders": {
				Service: model.ServiceRef{
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
				Endpoints: []model.Endpoint{
					{Address: "10.0.0.22", Port: 19090, Weight: 1},
				},
			},
		}),
	)
	srv := NewWithLoader(store, loader)
	srv.TrackTarget(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})

	bgCtx, cancelBg := context.WithCancel(context.Background())
	defer cancelBg()
	srv.StartBackgroundRefresh(bgCtx, 20*time.Millisecond)

	grpcServer := grpc.NewServer()
	controlv1.RegisterMeshControlPlaneServiceServer(grpcServer, srv)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		_ = grpcServer.Serve(listener)
	}()
	defer grpcServer.Stop()

	conn, err := grpc.DialContext(
		context.Background(),
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	streamCtx, cancelStream := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelStream()
	stream, err := controlv1.NewMeshControlPlaneServiceClient(conn).Connect(streamCtx)
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	defer stream.CloseSend()

	if err := stream.Send(&controlv1.ConnectRequest{
		Body: &controlv1.ConnectRequest_Register{
			Register: &controlv1.DataplaneRegister{
				Identity: &controlv1.DataplaneIdentity{
					DataplaneId: "dp-2",
					Mode:        "agent",
					NodeId:      "node-2",
					Namespace:   "default",
					Service:     "service-mesh-agent",
					Env:         "dev",
				},
			},
		},
	}); err != nil {
		t.Fatalf("send register failed: %v", err)
	}

	deadline := time.After(time.Second)
	for {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv failed: %v", err)
		}
		if resp.GetServiceSnapshot() != nil && resp.GetServiceSnapshot().GetService().GetService() == "orders" {
			return
		}

		select {
		case <-deadline:
			t.Fatal("expected background refresh to push tracked snapshot")
		default:
		}
	}
}

func TestServerBackgroundWatchPushesSourceWatchUpdates(t *testing.T) {
	provider := memory.New(nil)
	store := snapshot.NewStore()
	loader := snapshot.NewLoader(store, provider)
	srv := NewWithLoader(store, loader)
	srv.TrackTarget(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})

	bgCtx, cancelBg := context.WithCancel(context.Background())
	defer cancelBg()
	srv.StartBackgroundWatch(bgCtx)

	grpcServer := grpc.NewServer()
	controlv1.RegisterMeshControlPlaneServiceServer(grpcServer, srv)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		_ = grpcServer.Serve(listener)
	}()
	defer grpcServer.Stop()

	conn, err := grpc.DialContext(
		context.Background(),
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	stream, err := controlv1.NewMeshControlPlaneServiceClient(conn).Connect(context.Background())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	if err := stream.Send(&controlv1.ConnectRequest{
		Body: &controlv1.ConnectRequest_Register{
			Register: &controlv1.DataplaneRegister{
				Identity: &controlv1.DataplaneIdentity{
					DataplaneId: "dp-watch",
					Mode:        "agent",
					NodeId:      "node-watch",
					Namespace:   "default",
					Service:     "service-mesh-agent",
					Env:         "dev",
				},
			},
		},
	}); err != nil {
		t.Fatalf("send register failed: %v", err)
	}

	provider.Upsert(model.ServiceSnapshot{
		Service: model.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []model.Endpoint{
			{Address: "10.0.0.23", Port: 19090, Weight: 1},
		},
	})

	deadline := time.After(time.Second)
	for {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv failed: %v", err)
		}
		if resp.GetServiceSnapshot() != nil && resp.GetServiceSnapshot().GetService().GetService() == "orders" {
			if got, want := resp.GetServiceSnapshot().GetEndpoints()[0].GetAddress(), "10.0.0.23"; got != want {
				t.Fatalf("unexpected watch-pushed snapshot address: got=%s want=%s", got, want)
			}
			return
		}

		select {
		case <-deadline:
			t.Fatal("expected watch update to push snapshot")
		default:
		}
	}
}

func TestServerTrackTargetStartsWatchAfterBackgroundWatchBegins(t *testing.T) {
	provider := memory.New(nil)
	store := snapshot.NewStore()
	loader := snapshot.NewLoader(store, provider)
	srv := NewWithLoader(store, loader)

	bgCtx, cancelBg := context.WithCancel(context.Background())
	defer cancelBg()
	srv.StartBackgroundWatch(bgCtx)

	grpcServer := grpc.NewServer()
	controlv1.RegisterMeshControlPlaneServiceServer(grpcServer, srv)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		_ = grpcServer.Serve(listener)
	}()
	defer grpcServer.Stop()

	conn, err := grpc.DialContext(
		context.Background(),
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	stream, err := controlv1.NewMeshControlPlaneServiceClient(conn).Connect(context.Background())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	if err := stream.Send(&controlv1.ConnectRequest{
		Body: &controlv1.ConnectRequest_Register{
			Register: &controlv1.DataplaneRegister{
				Identity: &controlv1.DataplaneIdentity{
					DataplaneId: "dp-watch-late",
					Mode:        "agent",
					NodeId:      "node-watch-late",
					Namespace:   "default",
					Service:     "service-mesh-agent",
					Env:         "dev",
				},
			},
		},
	}); err != nil {
		t.Fatalf("send register failed: %v", err)
	}

	srv.TrackTarget(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})

	provider.Upsert(model.ServiceSnapshot{
		Service: model.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []model.Endpoint{
			{Address: "10.0.0.24", Port: 19090, Weight: 1},
		},
	})

	deadline := time.After(time.Second)
	for {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv failed: %v", err)
		}
		if resp.GetServiceSnapshot() != nil && resp.GetServiceSnapshot().GetService().GetService() == "orders" {
			if got, want := resp.GetServiceSnapshot().GetEndpoints()[0].GetAddress(), "10.0.0.24"; got != want {
				t.Fatalf("unexpected late-watch snapshot address: got=%s want=%s", got, want)
			}
			return
		}

		select {
		case <-deadline:
			t.Fatal("expected late tracked target to start watch and push snapshot")
		default:
		}
	}
}

func TestServerBackgroundWatchPushesSnapshotDelete(t *testing.T) {
	provider := memory.New(nil)
	store := snapshot.NewStore()
	loader := snapshot.NewLoader(store, provider)
	srv := NewWithLoader(store, loader)
	srv.TrackTarget(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})

	bgCtx, cancelBg := context.WithCancel(context.Background())
	defer cancelBg()
	srv.StartBackgroundWatch(bgCtx)

	grpcServer := grpc.NewServer()
	controlv1.RegisterMeshControlPlaneServiceServer(grpcServer, srv)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		_ = grpcServer.Serve(listener)
	}()
	defer grpcServer.Stop()

	conn, err := grpc.DialContext(
		context.Background(),
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	stream, err := controlv1.NewMeshControlPlaneServiceClient(conn).Connect(context.Background())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

	if err := stream.Send(&controlv1.ConnectRequest{
		Body: &controlv1.ConnectRequest_Register{
			Register: &controlv1.DataplaneRegister{
				Identity: &controlv1.DataplaneIdentity{
					DataplaneId: "dp-delete",
					Mode:        "agent",
					NodeId:      "node-delete",
					Namespace:   "default",
					Service:     "service-mesh-agent",
					Env:         "dev",
				},
			},
		},
	}); err != nil {
		t.Fatalf("send register failed: %v", err)
	}

	provider.Upsert(model.ServiceSnapshot{
		Service: model.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []model.Endpoint{
			{Address: "10.0.0.25", Port: 19090, Weight: 1},
		},
	})

	upsertDeadline := time.After(time.Second)
	for {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv upsert failed: %v", err)
		}
		if resp.GetServiceSnapshot() != nil && resp.GetServiceSnapshot().GetService().GetService() == "orders" {
			break
		}
		select {
		case <-upsertDeadline:
			t.Fatal("expected upsert before delete")
		default:
		}
	}

	provider.Delete(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})

	deleteDeadline := time.After(time.Second)
	for {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv delete failed: %v", err)
		}
		if resp.GetServiceSnapshotDeleted() != nil && resp.GetServiceSnapshotDeleted().GetService().GetService() == "orders" {
			return
		}
		select {
		case <-deleteDeadline:
			t.Fatal("expected snapshot delete push")
		default:
		}
	}
}

func TestServerBackgroundWatchRestartsAfterWatchStreamCloses(t *testing.T) {
	provider := &restartingWatchProvider{}
	store := snapshot.NewStore()
	loader := snapshot.NewLoader(store, provider)
	updates := make(chan snapshot.WatchUpdate, 1)
	manager := newWatchManager(loader, nil, func(update snapshot.WatchUpdate) {
		select {
		case updates <- update:
		default:
		}
	})

	bgCtx, cancelBg := context.WithCancel(context.Background())
	defer cancelBg()
	manager.Start(bgCtx, nil)
	manager.Track(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})

	time.Sleep(watchRestartBackoff * 2)
	provider.Upsert(model.ServiceSnapshot{
		Service: model.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Endpoints: []model.Endpoint{
			{Address: "10.0.0.26", Port: 19090, Weight: 1},
		},
	})

	deadline := time.After(time.Second)
	for {
		select {
		case update := <-updates:
			if update.Snapshot != nil && update.Snapshot.GetService().GetService() == "orders" {
				if got, want := update.Snapshot.GetEndpoints()[0].GetAddress(), "10.0.0.26"; got != want {
					t.Fatalf("unexpected restarted-watch snapshot address: got=%s want=%s", got, want)
				}
				return
			}
		case <-deadline:
			t.Fatal("expected restarted watch to push snapshot")
		}
	}
}

func TestDeliveryCycleExplainTargetResponse(t *testing.T) {
	cycle := newDeliveryCycle(nil)
	resp := snapshotDeletedResponse(&controlv1.ServiceSnapshotDeleted{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
	})
	subscribers := map[uint64]*subscriber{
		1: {
			pushCh: make(chan *controlv1.ConnectResponse, 1),
			identity: &controlv1.DataplaneIdentity{
				DataplaneId: "dp-1",
				Namespace:   "default",
				Env:         "dev",
			},
			targets: map[string]model.ServiceRef{
				targetKey(model.ServiceRef{Service: "orders", Namespace: "default", Env: "dev"}): {
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
			},
		},
		2: {
			pushCh: make(chan *controlv1.ConnectResponse, 1),
			identity: &controlv1.DataplaneIdentity{
				DataplaneId: "dp-2",
				Namespace:   "default",
				Env:         "dev",
			},
			targets: map[string]model.ServiceRef{
				targetKey(model.ServiceRef{Service: "payments", Namespace: "default", Env: "dev"}): {
					Service:   "payments",
					Namespace: "default",
					Env:       "dev",
				},
			},
		},
	}

	summary := cycle.ExplainTargetResponse(subscribers, resp, model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})

	if got, want := summary.responseKind, "service_snapshot_deleted"; got != want {
		t.Fatalf("unexpected response kind: got=%s want=%s", got, want)
	}
	if got, want := summary.delivered, 1; got != want {
		t.Fatalf("unexpected delivered count: got=%d want=%d", got, want)
	}
	if got, want := summary.subscriptionExact, 1; got != want {
		t.Fatalf("unexpected subscription exact count: got=%d want=%d", got, want)
	}
	if got, want := summary.subscriptionFallback, 0; got != want {
		t.Fatalf("unexpected subscription fallback count: got=%d want=%d", got, want)
	}
	if got, want := summary.identityExact, 2; got != want {
		t.Fatalf("unexpected identity exact count: got=%d want=%d", got, want)
	}
	if got, want := summary.identityFallback, 0; got != want {
		t.Fatalf("unexpected identity fallback count: got=%d want=%d", got, want)
	}
	if got, want := summary.deniedSubscription, 1; got != want {
		t.Fatalf("unexpected denied subscription count: got=%d want=%d", got, want)
	}
	if got, want := summary.deniedIdentity, 0; got != want {
		t.Fatalf("unexpected denied identity count: got=%d want=%d", got, want)
	}
	if got, want := len(summary.trace), 2; got != want {
		t.Fatalf("unexpected trace length: got=%d want=%d", got, want)
	}
	if got, want := summary.trace[0].decision, "delivered"; got != want {
		t.Fatalf("unexpected first trace decision: got=%s want=%s", got, want)
	}
	if got, want := summary.trace[0].dataplaneID, "dp-1"; got != want {
		t.Fatalf("unexpected first trace dataplane: got=%s want=%s", got, want)
	}
	if got, want := summary.trace[1].decision, "denied_subscription"; got != want {
		t.Fatalf("unexpected second trace decision: got=%s want=%s", got, want)
	}
	if got, want := summary.trace[1].dataplaneID, "dp-2"; got != want {
		t.Fatalf("unexpected second trace dataplane: got=%s want=%s", got, want)
	}
	if got, want := summary.traceShownCount(1), 1; got != want {
		t.Fatalf("unexpected trace shown count: got=%d want=%d", got, want)
	}
	if got, want := summary.traceShownCount(8), 2; got != want {
		t.Fatalf("unexpected full trace shown count: got=%d want=%d", got, want)
	}
	if got := summary.traceString(2); got == "" {
		t.Fatal("expected trace string to be populated")
	}
	exported := summary.export(1)
	if got, want := exported.TraceTotal, 2; got != want {
		t.Fatalf("unexpected exported trace total: got=%d want=%d", got, want)
	}
	if got, want := exported.TraceShown, 1; got != want {
		t.Fatalf("unexpected exported trace shown: got=%d want=%d", got, want)
	}
	if got, want := len(exported.Trace), 1; got != want {
		t.Fatalf("unexpected exported trace length: got=%d want=%d", got, want)
	}
	if got, want := exported.Trace[0].DataplaneID, "dp-1"; got != want {
		t.Fatalf("unexpected exported first trace dataplane: got=%s want=%s", got, want)
	}
}

func TestResourceArbitratorExplain(t *testing.T) {
	identity := &controlv1.DataplaneIdentity{
		DataplaneId: "dp-explain",
		Namespace:   "default",
		Env:         "dev",
	}
	arbitrator := newResourceArbitrator(
		[]*controlv1.ServiceSnapshot{
			{
				Service: &controlv1.ServiceRef{
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
			},
			{
				Service: &controlv1.ServiceRef{
					Service:   "payments",
					Namespace: "default",
				},
			},
		},
		[]*controlv1.RoutePolicy{
			{
				Service: &controlv1.ServiceRef{
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
			},
			{
				Service: &controlv1.ServiceRef{
					Service:   "payments",
					Namespace: "default",
				},
			},
		},
		identity,
	)

	summary := arbitrator.Explain(identity)
	if got, want := summary.snapshotExact, 1; got != want {
		t.Fatalf("unexpected snapshot exact count: got=%d want=%d", got, want)
	}
	if got, want := summary.snapshotFallback, 1; got != want {
		t.Fatalf("unexpected snapshot fallback count: got=%d want=%d", got, want)
	}
	if got, want := summary.policyExact, 1; got != want {
		t.Fatalf("unexpected policy exact count: got=%d want=%d", got, want)
	}
	if got, want := summary.policyFallback, 1; got != want {
		t.Fatalf("unexpected policy fallback count: got=%d want=%d", got, want)
	}
}

func TestServerExplainRegisterReplay(t *testing.T) {
	store := snapshot.NewStore()
	store.PutModelSnapshot(model.ServiceSnapshot{
		Service: model.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
	})
	store.PutRoutePolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
		},
	})
	srv := New(store)

	exported := srv.ExplainRegisterReplay(&controlv1.DataplaneIdentity{
		DataplaneId: "dp-export",
		Namespace:   "default",
		Env:         "dev",
	})

	if got, want := exported.Replay.SnapshotExact, 1; got != want {
		t.Fatalf("unexpected exported snapshot exact count: got=%d want=%d", got, want)
	}
	if got, want := exported.Replay.PolicyFallback, 1; got != want {
		t.Fatalf("unexpected exported policy fallback count: got=%d want=%d", got, want)
	}
	if got, want := exported.Batch.RoutePolicies, 1; got != want {
		t.Fatalf("unexpected exported batch route policy count: got=%d want=%d", got, want)
	}
}

func TestServerExplainTargetPush(t *testing.T) {
	srv := New(snapshot.NewStore())
	id1, _ := srv.addSubscriber()
	id2, _ := srv.addSubscriber()
	srv.updateSubscriberIdentity(id1, &controlv1.DataplaneIdentity{
		DataplaneId: "dp-1",
		Namespace:   "default",
		Env:         "dev",
	})
	srv.updateSubscriberIdentity(id2, &controlv1.DataplaneIdentity{
		DataplaneId: "dp-2",
		Namespace:   "default",
		Env:         "dev",
	})
	srv.updateSubscriberTargets(id1, []model.ServiceRef{{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	}})
	srv.updateSubscriberTargets(id2, []model.ServiceRef{{
		Service:   "payments",
		Namespace: "default",
		Env:       "dev",
	}})

	exported := srv.ExplainTargetPush(snapshotDeletedResponse(&controlv1.ServiceSnapshotDeleted{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
	}), model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	}, 1)

	if got, want := exported.Delivered, 1; got != want {
		t.Fatalf("unexpected exported delivered count: got=%d want=%d", got, want)
	}
	if got, want := exported.DeniedSubscription, 1; got != want {
		t.Fatalf("unexpected exported denied subscription count: got=%d want=%d", got, want)
	}
	if got, want := exported.TraceTotal, 2; got != want {
		t.Fatalf("unexpected exported trace total: got=%d want=%d", got, want)
	}
	if got, want := exported.TraceShown, 1; got != want {
		t.Fatalf("unexpected exported trace shown: got=%d want=%d", got, want)
	}
	if got, want := exported.Trace[0].DataplaneID, "dp-1"; got != want {
		t.Fatalf("unexpected exported trace dataplane: got=%s want=%s", got, want)
	}
}

func TestServerExplainSubscribeReplay(t *testing.T) {
	store := snapshot.NewStore()
	store.PutModelSnapshot(model.ServiceSnapshot{
		Service: model.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
	})
	store.PutRoutePolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
		},
	})
	srv := New(store)
	targets := []model.ServiceRef{{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	}}

	exported := srv.ExplainSubscribeReplay(&controlv1.DataplaneIdentity{
		DataplaneId: "dp-subscribe",
		Namespace:   "default",
		Env:         "dev",
	}, targets, nil)

	if got, want := exported.TargetCount, 1; got != want {
		t.Fatalf("unexpected subscribe target count: got=%d want=%d", got, want)
	}
	if got, want := exported.Replay.SnapshotExact, 1; got != want {
		t.Fatalf("unexpected subscribe snapshot exact count: got=%d want=%d", got, want)
	}
	if got, want := exported.Replay.PolicyFallback, 1; got != want {
		t.Fatalf("unexpected subscribe policy fallback count: got=%d want=%d", got, want)
	}
	if got, want := exported.Batch.ServiceSnapshots, 1; got != want {
		t.Fatalf("unexpected subscribe snapshot batch count: got=%d want=%d", got, want)
	}
	if got, want := exported.Batch.RoutePolicies, 1; got != want {
		t.Fatalf("unexpected subscribe route policy batch count: got=%d want=%d", got, want)
	}
}

func TestServerExportDebugState(t *testing.T) {
	srv := New(snapshot.NewStore())
	srv.TrackTarget(model.ServiceRef{
		Service:   "payments",
		Namespace: "default",
		Env:       "dev",
		Port:      8081,
	})
	srv.TrackTarget(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
		Port:      8080,
	})

	id1, _ := srv.addSubscriber()
	id2, _ := srv.addSubscriber()
	srv.updateSubscriberIdentity(id1, &controlv1.DataplaneIdentity{
		DataplaneId: "dp-1",
		NodeId:      "node-1",
		Namespace:   "default",
		Env:         "dev",
	})
	srv.updateSubscriberIdentity(id2, &controlv1.DataplaneIdentity{
		DataplaneId: "dp-2",
		NodeId:      "node-2",
		Namespace:   "default",
		Env:         "dev",
	})
	srv.updateSubscriberTargets(id1, []model.ServiceRef{
		{Service: "payments", Namespace: "default", Env: "dev", Port: 8081},
		{Service: "orders", Namespace: "default", Env: "dev", Port: 8080},
	})
	srv.updateSubscriberTargets(id2, []model.ServiceRef{
		{Service: "orders", Namespace: "default", Env: "dev", Port: 8080},
	})

	exported := srv.ExportDebugState()
	if got, want := exported.SubscriberCount, 2; got != want {
		t.Fatalf("unexpected subscriber count: got=%d want=%d", got, want)
	}
	if got, want := exported.TrackedTargetCount, 2; got != want {
		t.Fatalf("unexpected tracked target count: got=%d want=%d", got, want)
	}
	if got, want := len(exported.Subscribers), 2; got != want {
		t.Fatalf("unexpected subscribers length: got=%d want=%d", got, want)
	}
	if got, want := exported.Subscribers[0].SubscriberID, id1; got != want {
		t.Fatalf("unexpected first subscriber id: got=%d want=%d", got, want)
	}
	if got, want := exported.Subscribers[0].Identity.DataplaneID, "dp-1"; got != want {
		t.Fatalf("unexpected first subscriber dataplane id: got=%s want=%s", got, want)
	}
	if got, want := exported.Subscribers[0].Targets[0].Service, "orders"; got != want {
		t.Fatalf("unexpected first subscriber first target: got=%s want=%s", got, want)
	}
	if got, want := exported.TrackedTargets[0].Service, "orders"; got != want {
		t.Fatalf("unexpected first tracked target: got=%s want=%s", got, want)
	}
}
