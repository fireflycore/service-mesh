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

// Invoke 把 endpoint 地址拼到 payload 前面，便于断言命中实例。
func (t *fakeTransport) Invoke(_ context.Context, endpoint model.Endpoint, req *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error) {
	return &invokev1.UnaryInvokeResponse{
		Payload: append([]byte(endpoint.Address+":"), req.GetPayload()...),
		Codec:   req.GetCodec(),
	}, nil
}

func TestServiceUnaryInvoke(t *testing.T) {
	// 先准备一份最小内存目录，让 resolver 一定能选到目标实例。
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
	// 这里走真实 gRPC server/client，验证的是完整的本地 invoke 入口，而不只是函数调用。
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

// Invoke 首次返回 Unavailable，第二次返回成功。
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

// Invoke 一直阻塞到 ctx 超时或取消。
func (t *slowTransport) Invoke(ctx context.Context, _ model.Endpoint, _ *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type fakePolicySource struct {
	policy *controlv1.RoutePolicy
}

// ResolveRoutePolicy 返回预置的控制面策略。
func (s fakePolicySource) ResolveRoutePolicy(target model.ServiceRef) (*controlv1.RoutePolicy, bool) {
	return s.policy, s.policy != nil
}

// TestServiceUnaryInvokeRejectsInvalidRequest 验证入口参数校验。
func TestServiceUnaryInvokeRejectsInvalidRequest(t *testing.T) {
	// 这里故意传空请求，验证入口参数校验发生在真正业务链路之前。
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

// TestServiceUnaryInvokeRetriesOnUnavailable 验证基础 retry 能力。
func TestServiceUnaryInvokeRetriesOnUnavailable(t *testing.T) {
	transport := &flakyTransport{}

	// 本地配置只允许 2 次尝试，因此 flakyTransport 应该正好在第二次成功。
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

// TestServiceUnaryInvokeTimeout 验证超时预算会转成 DeadlineExceeded。
func TestServiceUnaryInvokeTimeout(t *testing.T) {
	// slowTransport 永远等 ctx 结束，因此这里主要验证 timeout 到错误码的映射。
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

// TestServiceUnaryInvokeAppliesControlPlaneRetryPolicy 验证控制面策略能覆盖本地 retry。
func TestServiceUnaryInvokeAppliesControlPlaneRetryPolicy(t *testing.T) {
	transport := &flakyTransport{}

	// 本地 RetryMaxAttempts=1，但控制面策略把它提升到 2，用来验证覆盖优先级。
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

// TestServiceUnaryInvokeAppliesSidecarLocalIdentity 验证 sidecar 会补齐 caller 与 target 上下文。
func TestServiceUnaryInvokeAppliesSidecarLocalIdentity(t *testing.T) {
	// 目标请求故意不填 namespace/env/context，观察 sidecar identity 是否补齐成功。
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
		// trace_id 不要求固定值，只要求 sidecar 在缺省时自动生成。
		t.Fatal("expected trace id to be generated")
	}
	if got, want := string(resp.GetPayload()), "127.0.0.1:hello"; got != want {
		t.Fatalf("unexpected payload: got=%s want=%s", got, want)
	}
}

// TestServiceUnaryInvokeRejectsConflictingSidecarIdentity 验证冲突 caller identity 会被拒绝。
func TestServiceUnaryInvokeRejectsConflictingSidecarIdentity(t *testing.T) {
	// 这里显式构造冲突 caller.service，验证 sidecar 不会接受伪造 caller identity。
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

func TestServiceUnaryInvokeRejectsSidecarSelfTarget(t *testing.T) {
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
			Service: "config",
		},
		Method: "/acme.config.v1.ConfigService/GetConfig",
	})
	if err == nil {
		t.Fatal("expected sidecar self target to fail")
	}

	statusErr, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error: %v", err)
	}
	if statusErr.Code() != codes.InvalidArgument {
		t.Fatalf("unexpected status code: %s", statusErr.Code())
	}
}

func TestServiceUnaryInvokeAllowsSidecarSelfTargetWhenConfigured(t *testing.T) {
	svc := NewService(
		authz.NewAllowAll(),
		resolver.New(memory.New(map[string]model.ServiceSnapshot{
			"/microservice/lhdht/dev/config": {
				Service: model.ServiceRef{
					Service:   "config",
					Namespace: "/microservice/lhdht",
					Env:       "dev",
				},
				Endpoints: []model.Endpoint{{Address: "127.0.0.1", Port: 8080, Weight: 1}},
			},
		}), balancer.NewRoundRobin()),
		&fakeTransport{},
		Options{
			LocalIdentity: &LocalIdentity{
				Service:    "config",
				Namespace:  "/microservice/lhdht",
				Env:        "dev",
				TargetMode: model.SidecarTargetModeAllowSameService,
			},
		},
	)

	resp, err := svc.UnaryInvoke(context.Background(), &invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service: "config",
		},
		Method:  "/acme.config.v1.ConfigService/GetConfig",
		Payload: []byte("hello"),
	})
	if err != nil {
		t.Fatalf("expected configured same-service target to succeed: %v", err)
	}
	if got, want := string(resp.GetPayload()), "127.0.0.1:hello"; got != want {
		t.Fatalf("unexpected payload: got=%s want=%s", got, want)
	}
}

func TestServiceUnaryInvokeAllowsCrossScopeSameServiceTarget(t *testing.T) {
	svc := NewService(
		authz.NewAllowAll(),
		resolver.New(memory.New(map[string]model.ServiceSnapshot{
			"/microservice/lhdht/prod/config": {
				Service: model.ServiceRef{
					Service:   "config",
					Namespace: "/microservice/lhdht",
					Env:       "prod",
				},
				Endpoints: []model.Endpoint{{Address: "127.0.0.1", Port: 8080, Weight: 1}},
			},
		}), balancer.NewRoundRobin()),
		&fakeTransport{},
		Options{
			LocalIdentity: &LocalIdentity{
				Service:    "config",
				Namespace:  "/microservice/lhdht",
				Env:        "dev",
				TargetMode: model.SidecarTargetModeAllowCrossScopeSameService,
			},
		},
	)

	resp, err := svc.UnaryInvoke(context.Background(), &invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service: "config",
			Env:     "prod",
		},
		Method:  "/acme.config.v1.ConfigService/GetConfig",
		Payload: []byte("hello"),
	})
	if err != nil {
		t.Fatalf("expected cross-scope same-service target to succeed: %v", err)
	}
	if got, want := string(resp.GetPayload()), "127.0.0.1:hello"; got != want {
		t.Fatalf("unexpected payload: got=%s want=%s", got, want)
	}
}

func TestServiceUnaryInvokeRejectsDegradedSnapshot(t *testing.T) {
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
				Status:       model.SnapshotStatusDegraded,
				StatusReason: "all endpoints unhealthy",
			},
		}), balancer.NewRoundRobin()),
		&fakeTransport{},
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
		t.Fatal("expected degraded snapshot invoke to fail")
	}

	statusErr, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error: %v", err)
	}
	if got, want := statusErr.Code(), codes.FailedPrecondition; got != want {
		t.Fatalf("unexpected status code: got=%s want=%s", got, want)
	}
}

func TestServiceUnaryInvokeRejectsSameScopeTargetUnderCrossScopeMode(t *testing.T) {
	svc := NewService(
		authz.NewAllowAll(),
		resolver.New(memory.New(nil), balancer.NewRoundRobin()),
		&fakeTransport{},
		Options{
			LocalIdentity: &LocalIdentity{
				Service:    "config",
				Namespace:  "/microservice/lhdht",
				Env:        "dev",
				TargetMode: model.SidecarTargetModeAllowCrossScopeSameService,
			},
		},
	)

	_, err := svc.UnaryInvoke(context.Background(), &invokev1.UnaryInvokeRequest{
		Target: &invokev1.ServiceRef{
			Service: "config",
		},
		Method: "/acme.config.v1.ConfigService/GetConfig",
	})
	if err == nil {
		t.Fatal("expected same-scope target to fail under cross-scope mode")
	}

	statusErr, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error: %v", err)
	}
	if statusErr.Code() != codes.InvalidArgument {
		t.Fatalf("unexpected status code: %s", statusErr.Code())
	}
}
