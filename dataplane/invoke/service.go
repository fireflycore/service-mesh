package invoke

import (
	"context"
	"errors"
	"strings"
	"time"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/dataplane/authz"
	"github.com/fireflycore/service-mesh/dataplane/transport"
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Resolver interface {
	Resolve(ctx context.Context, target model.ServiceRef) (model.Endpoint, error)
}

// Options 定义 Invoke 服务在第六版引入的可靠性策略。
//
// 这些策略的目标很明确：
// - timeout：避免一次调用无限挂住
// - retry：在典型瞬时错误下做最小重试
type Options struct {
	Timeout          time.Duration
	PerTryTimeout    time.Duration
	RetryMaxAttempts uint32
	RetryBackoff     time.Duration
	RetryableCodes   map[codes.Code]struct{}
}

// Service 是本地 MeshInvokeService 的最小实现。
//
// 它负责把一条外部传入的 Invoke 请求拆成四步：
// 1. 校验输入
// 2. 调用 authz
// 3. 解析目标 endpoint
// 4. 通过 transport 转发到真正的下游 gRPC 服务
type Service struct {
	invokev1.UnimplementedMeshInvokeServiceServer

	authorizer authz.Authorizer
	resolver   Resolver
	transport  transport.Invoker
	options    Options
}

func NewService(authorizer authz.Authorizer, resolver Resolver, transport transport.Invoker, options ...Options) *Service {
	opts := defaultOptions()
	if len(options) > 0 {
		opts = normalizeOptions(options[0])
	}

	return &Service{
		authorizer: authorizer,
		resolver:   resolver,
		transport:  transport,
		options:    opts,
	}
}

// UnaryInvoke 是当前阶段最核心的本地调用入口。
//
// 当前第三版的一个重要约束是：
//   - req.Method 必须是完整的 gRPC method path
//     例如 `/acme.orders.v1.OrderService/GetOrder`
//
// 这样 transport 才能在不知道具体业务 proto Go 类型的情况下，
// 直接按原始 protobuf bytes 做通用转发。
func (s *Service) UnaryInvoke(ctx context.Context, req *invokev1.UnaryInvokeRequest) (*invokev1.UnaryInvokeResponse, error) {
	if req.GetTarget() == nil || req.GetTarget().GetService() == "" {
		return nil, status.Error(codes.InvalidArgument, "target.service is required")
	}
	if req.GetMethod() == "" {
		return nil, status.Error(codes.InvalidArgument, "method is required")
	}
	if !strings.HasPrefix(req.GetMethod(), "/") {
		return nil, status.Error(codes.InvalidArgument, "method must be a full grpc method path")
	}

	callCtx := ctx
	callTimeout := s.options.Timeout
	if req.GetContext().GetTimeoutMs() > 0 {
		callTimeout = time.Duration(req.GetContext().GetTimeoutMs()) * time.Millisecond
	}
	if callTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, callTimeout)
		defer cancel()
	}

	if err := s.authorizer.Check(callCtx, req); err != nil {
		return nil, status.Errorf(codes.PermissionDenied, "authz check failed: %v", err)
	}

	target := model.ServiceRef{
		Service:   req.GetTarget().GetService(),
		Namespace: req.GetTarget().GetNamespace(),
		Env:       req.GetTarget().GetEnv(),
		Port:      req.GetTarget().GetPort(),
	}

	attempts := s.options.RetryMaxAttempts
	if attempts == 0 {
		attempts = 1
	}

	var lastErr error
	for attempt := uint32(1); attempt <= attempts; attempt++ {
		attemptCtx := callCtx
		attemptCancel := func() {}
		if s.options.PerTryTimeout > 0 {
			attemptCtx, attemptCancel = context.WithTimeout(callCtx, s.options.PerTryTimeout)
		}

		endpoint, err := s.resolver.Resolve(attemptCtx, target)
		if err != nil {
			lastErr = err
		} else {
			resp, invokeErr := s.transport.Invoke(attemptCtx, endpoint, req)
			if invokeErr == nil {
				attemptCancel()
				return resp, nil
			}
			lastErr = invokeErr
		}
		attemptCancel()

		if !s.shouldRetry(lastErr, attempt, attempts) {
			break
		}

		if s.options.RetryBackoff > 0 {
			timer := time.NewTimer(s.options.RetryBackoff)
			select {
			case <-callCtx.Done():
				timer.Stop()
				lastErr = callCtx.Err()
				attempt = attempts
			case <-timer.C:
			}
		}
	}

	if errors.Is(lastErr, context.Canceled) {
		return nil, status.Error(codes.Canceled, lastErr.Error())
	}
	if errors.Is(lastErr, context.DeadlineExceeded) {
		return nil, status.Error(codes.DeadlineExceeded, lastErr.Error())
	}
	return nil, status.Errorf(codes.Unavailable, "invoke target failed: %v", lastErr)
}

func defaultOptions() Options {
	return normalizeOptions(Options{
		Timeout:          1500 * time.Millisecond,
		PerTryTimeout:    500 * time.Millisecond,
		RetryMaxAttempts: 2,
		RetryBackoff:     50 * time.Millisecond,
		RetryableCodes: map[codes.Code]struct{}{
			codes.Unavailable:       {},
			codes.DeadlineExceeded:  {},
			codes.ResourceExhausted: {},
		},
	})
}

func normalizeOptions(options Options) Options {
	if options.Timeout <= 0 {
		options.Timeout = 1500 * time.Millisecond
	}
	if options.PerTryTimeout <= 0 {
		options.PerTryTimeout = 500 * time.Millisecond
	}
	if options.RetryMaxAttempts == 0 {
		options.RetryMaxAttempts = 2
	}
	if options.RetryBackoff < 0 {
		options.RetryBackoff = 0
	}
	if len(options.RetryableCodes) == 0 {
		options.RetryableCodes = map[codes.Code]struct{}{
			codes.Unavailable:       {},
			codes.DeadlineExceeded:  {},
			codes.ResourceExhausted: {},
		}
	}
	return options
}

// OptionsFromConfig 把配置层的 invoke 策略转换成运行时选项。
func OptionsFromConfig(cfg config.InvokeConfig) Options {
	retryable := make(map[codes.Code]struct{}, len(cfg.RetryableCodes))
	for _, codeName := range cfg.RetryableCodes {
		switch strings.ToLower(strings.TrimSpace(codeName)) {
		case "canceled":
			retryable[codes.Canceled] = struct{}{}
		case "unknown":
			retryable[codes.Unknown] = struct{}{}
		case "deadline_exceeded":
			retryable[codes.DeadlineExceeded] = struct{}{}
		case "resource_exhausted":
			retryable[codes.ResourceExhausted] = struct{}{}
		case "aborted":
			retryable[codes.Aborted] = struct{}{}
		case "unavailable":
			retryable[codes.Unavailable] = struct{}{}
		}
	}

	return normalizeOptions(Options{
		Timeout:          time.Duration(cfg.TimeoutMS) * time.Millisecond,
		PerTryTimeout:    time.Duration(cfg.PerTryTimeoutMS) * time.Millisecond,
		RetryMaxAttempts: cfg.RetryMaxAttempts,
		RetryBackoff:     time.Duration(cfg.RetryBackoffMS) * time.Millisecond,
		RetryableCodes:   retryable,
	})
}

func (s *Service) shouldRetry(err error, attempt, attempts uint32) bool {
	if err == nil || attempt >= attempts {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		_, ok := s.options.RetryableCodes[codes.DeadlineExceeded]
		return ok
	}

	statusCode := status.Code(err)
	_, ok := s.options.RetryableCodes[statusCode]
	return ok
}
