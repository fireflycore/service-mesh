package invoke

import (
	"context"
	"net"
	"testing"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/dataplane/authz"
	"github.com/fireflycore/service-mesh/dataplane/balancer"
	"github.com/fireflycore/service-mesh/dataplane/resolver"
	"github.com/fireflycore/service-mesh/pkg/model"
	"github.com/fireflycore/service-mesh/source/memory"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type fakeTransport struct{}

func (t *fakeTransport) Invoke(_ context.Context, endpoint model.Endpoint, req *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error) {
	return &invokev1.UnaryInvokeResponse{
		Payload: append([]byte(endpoint.Address+":"), req.GetPayload()...),
		Codec:   req.GetCodec(),
	}, nil
}

func TestServiceUnaryInvoke(t *testing.T) {
	provider := memory.New(map[string]model.ServiceSnapshot{
		"default/dev/orders": {
			Service: model.ServiceRef{
				Service:   "orders",
				Namespace: "default",
				Env:       "dev",
			},
			Endpoints: []model.Endpoint{
				{
					Address: "127.0.0.1",
					Port:    8080,
					Weight:  1,
				},
			},
			Revision: "v1",
		},
	})

	svc := NewService(
		authz.NewAllowAll(),
		resolver.New(provider, balancer.NewRoundRobin()),
		&fakeTransport{},
	)

	server := grpc.NewServer()
	invokev1.RegisterMeshInvokeServiceServer(server, svc)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	conn, err := grpc.DialContext(
		context.Background(),
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	client := invokev1.NewMeshInvokeServiceClient(conn)
	resp, err := client.UnaryInvoke(context.Background(), &invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Method:  "GetOrder",
		Payload: []byte("hello"),
		Codec:   "json",
	})
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}

	if got, want := string(resp.GetPayload()), "127.0.0.1:hello"; got != want {
		t.Fatalf("unexpected payload: got=%s want=%s", got, want)
	}
	if got, want := resp.GetCodec(), "json"; got != want {
		t.Fatalf("unexpected codec: got=%s want=%s", got, want)
	}
}

func TestServiceUnaryInvokeRejectsInvalidRequest(t *testing.T) {
	svc := NewService(
		authz.NewAllowAll(),
		resolver.New(memory.New(nil), balancer.NewRoundRobin()),
		&fakeTransport{},
	)

	_, err := svc.UnaryInvoke(context.Background(), &invokev1.UnaryInvokeRequest{})
	if err == nil {
		t.Fatal("expected invalid request to fail")
	}

	statusErr, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error: %v", err)
	}
	if statusErr.Code() != codes.InvalidArgument {
		t.Fatalf("unexpected status code: %s", statusErr.Code())
	}
}
