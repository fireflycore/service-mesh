package server

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/controlplane/snapshot"
	"github.com/fireflycore/service-mesh/pkg/model"
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
