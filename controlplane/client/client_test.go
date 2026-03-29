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
	"google.golang.org/grpc"
)

func TestClientReceivesSnapshotAndPolicy(t *testing.T) {
	store := snapshot.NewStore()
	store.PutServiceSnapshot(&controlv1.ServiceSnapshot{
		Service: &controlv1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
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
			MaxAttempts:     2,
			PerTryTimeoutMs: 500,
		},
		TimeoutMs: 1500,
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
			Service:     "orders",
		})
	}()

	deadline := time.After(500 * time.Millisecond)
	for {
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
}
