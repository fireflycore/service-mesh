package server

import (
	"context"
	"net"
	"testing"
	"time"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	"github.com/fireflycore/service-mesh/controlplane/snapshot"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source/memory"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

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

	stream, err := controlv1.NewMeshControlPlaneServiceClient(conn).Connect(context.Background())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

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
	for i := 0; i < 3; i++ {
		resp, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv replay response failed: %v", err)
		}
		if resp.GetServiceSnapshot() != nil {
			snapshotCount++
			continue
		}
		if resp.GetRoutePolicy() != nil {
			policyCount++
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

	stream, err := controlv1.NewMeshControlPlaneServiceClient(conn).Connect(context.Background())
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

	stream, err := controlv1.NewMeshControlPlaneServiceClient(conn).Connect(context.Background())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}

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
