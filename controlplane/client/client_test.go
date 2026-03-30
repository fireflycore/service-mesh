package client

import (
	"context"
	"net"
	"testing"
	"time"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/controlplane/server"
	"github.com/fireflycore/service-mesh/controlplane/snapshot"
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source/memory"
	"google.golang.org/grpc"
)

// TestClientReceivesSnapshotAndPolicy 验证 client 能消费并缓存控制面下发状态。
func TestClientReceivesSnapshotAndPolicy(t *testing.T) {
	store := snapshot.NewStore()
	// 先准备一份控制面已有状态，验证 client 启动后能否同步到本地缓存。
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
			{Address: "10.0.0.20", Port: 29090, Weight: 1},
		},
		Revision: "v2",
	})

	grpcServer := grpc.NewServer()
	controlv1.RegisterMeshControlPlaneServiceServer(grpcServer, server.New(store))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		_ = grpcServer.Serve(listener)
	}()
	defer grpcServer.Stop()

	client := New(config.ControlPlaneConfig{
		Target:              listener.Addr().String(),
		HeartbeatIntervalMS: 100,
		ConnectTimeoutMS:    200,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.Run(ctx, &controlv1.DataplaneIdentity{
			DataplaneId: "dp-1",
			Mode:        "agent",
			NodeId:      "node-1",
			Namespace:   "default",
			Service:     "service-mesh-agent",
			Env:         "dev",
		})
	}()

	deadline := time.After(500 * time.Millisecond)
	for {
		// 这里轮询等待异步 recvLoop 把状态写进 client.State。
		if client.State().Snapshot() != nil && client.State().RoutePolicy() != nil {
			cancel()
			break
		}

		select {
		case <-deadline:
			t.Fatal("expected controlplane client state to be populated")
		case <-time.After(20 * time.Millisecond):
		}
	}

	if err := <-errCh; err != nil && err != context.Canceled {
		t.Fatalf("unexpected run error: %v", err)
	}

	snapshotValue, ok := client.State().ResolveSnapshot(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if !ok {
		t.Fatal("expected state snapshot lookup to succeed")
	}
	if got, want := snapshotValue.Endpoints[0].Address, "10.0.0.10"; got != want {
		t.Fatalf("unexpected snapshot address: got=%s want=%s", got, want)
	}

	paymentsSnapshot, ok := client.State().ResolveSnapshot(model.ServiceRef{
		Service:   "payments",
		Namespace: "default",
		Env:       "dev",
	})
	if !ok {
		t.Fatal("expected second service snapshot lookup to succeed")
	}
	if got, want := paymentsSnapshot.Endpoints[0].Address, "10.0.0.20"; got != want {
		t.Fatalf("unexpected second snapshot address: got=%s want=%s", got, want)
	}

	// 除了缓存“最后一条消息”，client 还需要支持按 service 维度解析当前策略。
	policy, ok := client.State().ResolveRoutePolicy(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})
	if !ok || policy == nil {
		t.Fatal("expected state route policy lookup to succeed")
	}
	if got, want := policy.GetTimeoutMs(), uint64(1500); got != want {
		t.Fatalf("unexpected route timeout: got=%d want=%d", got, want)
	}
}

func TestClientReceivesPushedSnapshotAfterRefresh(t *testing.T) {
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
					{Address: "10.0.0.30", Port: 19090, Weight: 1},
				},
			},
		}),
	)
	srv := server.NewWithLoader(store, loader)
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

	client := New(config.ControlPlaneConfig{
		Target:              listener.Addr().String(),
		HeartbeatIntervalMS: 100,
		ConnectTimeoutMS:    200,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.Run(ctx, &controlv1.DataplaneIdentity{
			DataplaneId: "dp-1",
			Mode:        "agent",
			NodeId:      "node-1",
			Namespace:   "default",
			Service:     "service-mesh-agent",
			Env:         "dev",
		})
	}()

	time.Sleep(150 * time.Millisecond)

	if err := srv.RefreshTracked(context.Background()); err != nil {
		t.Fatalf("refresh tracked failed: %v", err)
	}

	deadline := time.After(time.Second)
	for {
		snapshotValue, ok := client.State().ResolveSnapshot(model.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		})
		if ok {
			if got, want := snapshotValue.Endpoints[0].Address, "10.0.0.30"; got != want {
				t.Fatalf("unexpected pushed snapshot address: got=%s want=%s", got, want)
			}
			cancel()
			break
		}

		select {
		case <-deadline:
			t.Fatal("expected pushed snapshot to reach client state")
		case <-time.After(20 * time.Millisecond):
		}
	}

	if err := <-errCh; err != nil && err != context.Canceled {
		t.Fatalf("unexpected run error: %v", err)
	}
}

func TestClientTrackTargetTriggersSubscribeAndSnapshotSync(t *testing.T) {
	store := snapshot.NewStore()
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
					{Address: "10.0.0.40", Port: 19090, Weight: 1},
				},
			},
		}),
	)
	srv := server.NewWithLoader(store, loader)

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

	client := New(config.ControlPlaneConfig{
		Target:              listener.Addr().String(),
		HeartbeatIntervalMS: 100,
		ConnectTimeoutMS:    200,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.Run(ctx, &controlv1.DataplaneIdentity{
			DataplaneId: "dp-1",
			Mode:        "agent",
			NodeId:      "node-1",
			Namespace:   "default",
			Service:     "service-mesh-agent",
			Env:         "dev",
		})
	}()

	time.Sleep(150 * time.Millisecond)
	client.TrackTarget(model.ServiceRef{
		Service:   "orders",
		Namespace: "default",
		Env:       "dev",
	})

	deadline := time.After(time.Second)
	for {
		snapshotValue, ok := client.State().ResolveSnapshot(model.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		})
		if ok {
			if got, want := snapshotValue.Endpoints[0].Address, "10.0.0.40"; got != want {
				t.Fatalf("unexpected subscribed snapshot address: got=%s want=%s", got, want)
			}
		}

		policy, policyOK := client.State().ResolveRoutePolicy(model.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		})
		if ok && policyOK {
			if got, want := policy.GetTimeoutMs(), uint64(1500); got != want {
				t.Fatalf("unexpected subscribed route policy timeout: got=%d want=%d", got, want)
			}
			break
		}

		select {
		case <-deadline:
			t.Fatal("expected subscribed snapshot and route policy to reach client state")
		case <-time.After(20 * time.Millisecond):
		}
	}

	if changed := srv.UpsertRoutePolicy(&controlv1.RoutePolicy{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Retry: &controlv1.RetryPolicy{
			MaxAttempts:     4,
			PerTryTimeoutMs: 800,
		},
		TimeoutMs: 2500,
	}); !changed {
		t.Fatal("expected route policy update to be broadcast")
	}

	updateDeadline := time.After(time.Second)
	for {
		policy, policyOK := client.State().ResolveRoutePolicy(model.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		})
		if policyOK && policy.GetTimeoutMs() == 2500 {
			cancel()
			break
		}

		select {
		case <-updateDeadline:
			t.Fatal("expected pushed route policy update to reach client state")
		case <-time.After(20 * time.Millisecond):
		}
	}

	if err := <-errCh; err != nil && err != context.Canceled {
		t.Fatalf("unexpected run error: %v", err)
	}
}
