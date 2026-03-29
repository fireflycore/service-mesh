package invoke

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
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
		Method:  "/acme.orders.v1.OrderService/GetOrder",
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

type flakyTransport struct {
	mu       sync.Mutex
	attempts int
}

func (t *flakyTransport) Invoke(_ context.Context, endpoint model.Endpoint, req *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.attempts++
	if t.attempts == 1 {
		return nil, status.Error(codes.Unavailable, "temporary unavailable")
	}

	return &invokev1.UnaryInvokeResponse{
		Payload: append([]byte(endpoint.Address+":"), req.GetPayload()...),
		Codec:   req.GetCodec(),
	}, nil
}

type slowTransport struct{}

func (t *slowTransport) Invoke(ctx context.Context, _ model.Endpoint, _ *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type fakePolicySource struct {
	policy *controlv1.RoutePolicy
}

func (s fakePolicySource) ResolveRoutePolicy(target model.ServiceRef) (*controlv1.RoutePolicy, bool) {
	return s.policy, s.policy != nil
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

func TestServiceUnaryInvokeRetriesOnUnavailable(t *testing.T) {
	transport := &flakyTransport{}

	svc := NewService(
		authz.NewAllowAll(),
		resolver.New(memory.New(map[string]model.ServiceSnapshot{
			"default/dev/orders": {
				Service: model.ServiceRef{
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
				Endpoints: []model.Endpoint{{Address: "127.0.0.1", Port: 8080, Weight: 1}},
			},
		}), balancer.NewRoundRobin()),
		transport,
		Options{
			Timeout:          time.Second,
			PerTryTimeout:    200 * time.Millisecond,
			RetryMaxAttempts: 2,
			RetryBackoff:     0,
			RetryableCodes: map[codes.Code]struct{}{
				codes.Unavailable: {},
			},
		},
	)

	resp, err := svc.UnaryInvoke(context.Background(), &invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Method:  "/acme.orders.v1.OrderService/GetOrder",
		Payload: []byte("hello"),
		Codec:   "json",
	})
	if err != nil {
		t.Fatalf("expected retry to succeed: %v", err)
	}
	if got, want := transport.attempts, 2; got != want {
		t.Fatalf("unexpected retry count: got=%d want=%d", got, want)
	}
	if got, want := string(resp.GetPayload()), "127.0.0.1:hello"; got != want {
		t.Fatalf("unexpected payload: got=%s want=%s", got, want)
	}
}

func TestServiceUnaryInvokeTimeout(t *testing.T) {
	svc := NewService(
		authz.NewAllowAll(),
		resolver.New(memory.New(map[string]model.ServiceSnapshot{
			"default/dev/orders": {
				Service: model.ServiceRef{
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
				Endpoints: []model.Endpoint{{Address: "127.0.0.1", Port: 8080, Weight: 1}},
			},
		}), balancer.NewRoundRobin()),
		&slowTransport{},
		Options{
			Timeout:          50 * time.Millisecond,
			PerTryTimeout:    20 * time.Millisecond,
			RetryMaxAttempts: 1,
			RetryBackoff:     0,
			RetryableCodes: map[codes.Code]struct{}{
				codes.DeadlineExceeded: {},
			},
		},
	)

	_, err := svc.UnaryInvoke(context.Background(), &invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Method: "/acme.orders.v1.OrderService/GetOrder",
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}

	statusErr, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error: %v", err)
	}
	if !errors.Is(statusErr.Err(), context.DeadlineExceeded) && statusErr.Code() != codes.DeadlineExceeded {
		t.Fatalf("unexpected status code: %s", statusErr.Code())
	}
}

func TestServiceUnaryInvokeAppliesControlPlaneRetryPolicy(t *testing.T) {
	transport := &flakyTransport{}

	svc := NewService(
		authz.NewAllowAll(),
		resolver.New(memory.New(map[string]model.ServiceSnapshot{
			"default/dev/orders": {
				Service: model.ServiceRef{
					Service:   "orders",
					Namespace: "default",
					Env:       "dev",
				},
				Endpoints: []model.Endpoint{{Address: "10.0.0.2", Port: 8080, Weight: 1}},
			},
		}), balancer.NewRoundRobin()),
		transport,
		Options{
			Timeout:          time.Second,
			PerTryTimeout:    200 * time.Millisecond,
			RetryMaxAttempts: 1,
			RetryBackoff:     0,
			RetryableCodes: map[codes.Code]struct{}{
				codes.Unavailable: {},
			},
			PolicySource: fakePolicySource{
				policy: &controlv1.RoutePolicy{
					Service: &controlv1.ServiceRef{
						Service:   "orders",
						Namespace: "default",
						Env:       "dev",
					},
					Retry: &controlv1.RetryPolicy{
						MaxAttempts:     2,
						PerTryTimeoutMs: 200,
					},
				},
			},
		},
	)

	resp, err := svc.UnaryInvoke(context.Background(), &invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service:   "orders",
			Namespace: "default",
			Env:       "dev",
		},
		Method:  "/acme.orders.v1.OrderService/GetOrder",
		Payload: []byte("hello"),
		Codec:   "json",
	})
	if err != nil {
		t.Fatalf("expected policy-driven retry to succeed: %v", err)
	}
	if got, want := transport.attempts, 2; got != want {
		t.Fatalf("unexpected retry count after controlplane policy: got=%d want=%d", got, want)
	}
	if got, want := string(resp.GetPayload()), "10.0.0.2:hello"; got != want {
		t.Fatalf("unexpected payload: got=%s want=%s", got, want)
	}
}

func TestServiceUnaryInvokeAppliesSidecarLocalIdentity(t *testing.T) {
	svc := NewService(
		authz.NewAllowAll(),
		resolver.New(memory.New(map[string]model.ServiceSnapshot{
			"/microservice/lhdht/dev/orders": {
				Service: model.ServiceRef{
					Service:   "orders",
					Namespace: "/microservice/lhdht",
					Env:       "dev",
				},
				Endpoints: []model.Endpoint{{Address: "127.0.0.1", Port: 8080, Weight: 1}},
			},
		}), balancer.NewRoundRobin()),
		&fakeTransport{},
		Options{
			LocalIdentity: &LocalIdentity{
				AppID:     "config",
				Service:   "config",
				Namespace: "/microservice/lhdht",
				Env:       "dev",
			},
		},
	)

	req := &invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service: "orders",
		},
		Method:  "/acme.orders.v1.OrderService/GetOrder",
		Payload: []byte("hello"),
		Codec:   "json",
	}

	resp, err := svc.UnaryInvoke(context.Background(), req)
	if err != nil {
		t.Fatalf("expected local identity enrichment to succeed: %v", err)
	}
	if got, want := req.GetContext().GetCaller().GetService(), "config"; got != want {
		t.Fatalf("unexpected caller service: got=%s want=%s", got, want)
	}
	if got, want := req.GetTarget().GetNamespace(), "/microservice/lhdht"; got != want {
		t.Fatalf("unexpected target namespace: got=%s want=%s", got, want)
	}
	if got, want := req.GetTarget().GetEnv(), "dev"; got != want {
		t.Fatalf("unexpected target env: got=%s want=%s", got, want)
	}
	if req.GetContext().GetTraceId() == "" {
		t.Fatal("expected trace id to be generated")
	}
	if got, want := string(resp.GetPayload()), "127.0.0.1:hello"; got != want {
		t.Fatalf("unexpected payload: got=%s want=%s", got, want)
	}
}

func TestServiceUnaryInvokeRejectsConflictingSidecarIdentity(t *testing.T) {
	svc := NewService(
		authz.NewAllowAll(),
		resolver.New(memory.New(nil), balancer.NewRoundRobin()),
		&fakeTransport{},
		Options{
			LocalIdentity: &LocalIdentity{
				Service:   "config",
				Namespace: "/microservice/lhdht",
				Env:       "dev",
			},
		},
	)

	_, err := svc.UnaryInvoke(context.Background(), &invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service: "orders",
		},
		Method: "/acme.orders.v1.OrderService/GetOrder",
		Context: &invokev1.InvocationContext{
			Caller: &invokev1.Caller{
				Service: "other-service",
			},
		},
	})
	if err == nil {
		t.Fatal("expected conflicting sidecar identity to fail")
	}

	statusErr, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error: %v", err)
	}
	if statusErr.Code() != codes.InvalidArgument {
		t.Fatalf("unexpected status code: %s", statusErr.Code())
	}
}
