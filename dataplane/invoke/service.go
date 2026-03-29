package invoke

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	controlv1 "github.com/fireflycore/service-mesh/.gen/proto/acme/control/v1"
	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/dataplane/authz"
	meshtelemetry "github.com/fireflycore/service-mesh/dataplane/telemetry"
	"github.com/fireflycore/service-mesh/dataplane/transport"
	"github.com/fireflycore/service-mesh/pkg/config"
	"github.com/fireflycore/service-mesh/pkg/model"
	otelcodes "go.opentelemetry.io/otel/codes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Resolver interface {
	Resolve(ctx context.Context, target model.ServiceRef) (model.Endpoint, error)
}

type PolicySource interface {
	ResolveRoutePolicy(target model.ServiceRef) (*controlv1.RoutePolicy, bool)
}

// LocalIdentity 描述 sidecar 绑定的本地业务服务身份。
type LocalIdentity struct {
	AppID     string
	Service   string
	Namespace string
	Env       string
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
	Telemetry        *meshtelemetry.Emitter
	PolicySource     PolicySource
	LocalIdentity    *LocalIdentity
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
	if err := applyLocalIdentity(req, s.options.LocalIdentity); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	target := model.ServiceRef{
		Service:   req.GetTarget().GetService(),
		Namespace: req.GetTarget().GetNamespace(),
		Env:       req.GetTarget().GetEnv(),
		Port:      req.GetTarget().GetPort(),
	}

	effectiveOptions := s.options
	if s.options.PolicySource != nil {
		if policy, ok := s.options.PolicySource.ResolveRoutePolicy(target); ok {
			effectiveOptions = mergeOptionsWithPolicy(s.options, policy)
		}
	}

	callCtx := ctx
	callTimeout := effectiveOptions.Timeout
	if req.GetContext().GetTimeoutMs() > 0 {
		callTimeout = time.Duration(req.GetContext().GetTimeoutMs()) * time.Millisecond
	}
	if callTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, callTimeout)
		defer cancel()
	}

	effectiveOptions.Telemetry.RecordRequest(callCtx)
	spanCtx, span := effectiveOptions.Telemetry.StartInvoke(callCtx, req)
	defer span.End()
	start := time.Now()
	defer func() {
		effectiveOptions.Telemetry.RecordLatency(spanCtx, time.Since(start))
	}()

	if err := s.authorizer.Check(spanCtx, req); err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "authz failed")
		effectiveOptions.Telemetry.RecordFailure(spanCtx, "authz")
		return nil, status.Errorf(codes.PermissionDenied, "authz check failed: %v", err)
	}

	attempts := effectiveOptions.RetryMaxAttempts
	if attempts == 0 {
		attempts = 1
	}

	var lastErr error
	for attempt := uint32(1); attempt <= attempts; attempt++ {
		attemptCtx := spanCtx
		attemptCancel := func() {}
		if effectiveOptions.PerTryTimeout > 0 {
			attemptCtx, attemptCancel = context.WithTimeout(callCtx, effectiveOptions.PerTryTimeout)
		}

		endpoint, err := s.resolver.Resolve(attemptCtx, target)
		if err != nil {
			lastErr = err
		} else {
			resp, invokeErr := s.transport.Invoke(attemptCtx, endpoint, req)
			if invokeErr == nil {
				attemptCancel()
				span.SetStatus(otelcodes.Ok, "invoke succeeded")
				return resp, nil
			}
			span.RecordError(invokeErr)
			lastErr = invokeErr
		}
		attemptCancel()

		if !shouldRetry(lastErr, attempt, attempts, effectiveOptions) {
			break
		}
		effectiveOptions.Telemetry.RecordRetry(spanCtx, int64(attempt))

		if effectiveOptions.RetryBackoff > 0 {
			timer := time.NewTimer(effectiveOptions.RetryBackoff)
			select {
			case <-spanCtx.Done():
				timer.Stop()
				lastErr = spanCtx.Err()
				attempt = attempts
			case <-timer.C:
			}
		}
	}

	if errors.Is(lastErr, context.Canceled) {
		span.RecordError(lastErr)
		span.SetStatus(otelcodes.Error, "invoke canceled")
		effectiveOptions.Telemetry.RecordFailure(spanCtx, "canceled")
		return nil, status.Error(codes.Canceled, lastErr.Error())
	}
	if errors.Is(lastErr, context.DeadlineExceeded) {
		span.RecordError(lastErr)
		span.SetStatus(otelcodes.Error, "invoke deadline exceeded")
		effectiveOptions.Telemetry.RecordFailure(spanCtx, "deadline_exceeded")
		return nil, status.Error(codes.DeadlineExceeded, lastErr.Error())
	}
	span.RecordError(lastErr)
	span.SetStatus(otelcodes.Error, "invoke failed")
	effectiveOptions.Telemetry.RecordFailure(spanCtx, "invoke")
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
	if options.Telemetry == nil {
		options.Telemetry = meshtelemetry.NewNoopEmitter()
	}
	if options.LocalIdentity != nil {
		options.LocalIdentity = normalizeLocalIdentity(*options.LocalIdentity)
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

func shouldRetry(err error, attempt, attempts uint32, options Options) bool {
	if err == nil || attempt >= attempts {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		_, ok := options.RetryableCodes[codes.DeadlineExceeded]
		return ok
	}

	statusCode := status.Code(err)
	_, ok := options.RetryableCodes[statusCode]
	return ok
}

func mergeOptionsWithPolicy(base Options, policy *controlv1.RoutePolicy) Options {
	if policy == nil {
		return base
	}

	merged := base
	if policy.GetTimeoutMs() > 0 {
		merged.Timeout = time.Duration(policy.GetTimeoutMs()) * time.Millisecond
	}
	if policy.GetRetry() != nil {
		if policy.GetRetry().GetMaxAttempts() > 0 {
			merged.RetryMaxAttempts = policy.GetRetry().GetMaxAttempts()
		}
		if policy.GetRetry().GetPerTryTimeoutMs() > 0 {
			merged.PerTryTimeout = time.Duration(policy.GetRetry().GetPerTryTimeoutMs()) * time.Millisecond
		}
	}

	return normalizeOptions(merged)
}

// applyLocalIdentity 在 sidecar 模式下补齐本地 caller 身份与默认目标域。
func applyLocalIdentity(req *invokev1.UnaryInvokeRequest, identity *LocalIdentity) error {
	if identity == nil {
		return nil
	}
	if req.Context == nil {
		req.Context = &invokev1.InvocationContext{}
	}
	if req.Context.Caller == nil {
		req.Context.Caller = &invokev1.Caller{}
	}

	if err := ensureMatch("context.caller.service", req.Context.Caller.Service, identity.Service); err != nil {
		return err
	}
	if err := ensureMatch("context.caller.namespace", req.Context.Caller.Namespace, identity.Namespace); err != nil {
		return err
	}
	if err := ensureMatch("context.caller.env", req.Context.Caller.Env, identity.Env); err != nil {
		return err
	}

	if strings.TrimSpace(req.Context.Caller.AppId) == "" {
		req.Context.Caller.AppId = identity.AppID
	}
	if strings.TrimSpace(req.Context.Caller.Service) == "" {
		req.Context.Caller.Service = identity.Service
	}
	if strings.TrimSpace(req.Context.Caller.Namespace) == "" {
		req.Context.Caller.Namespace = identity.Namespace
	}
	if strings.TrimSpace(req.Context.Caller.Env) == "" {
		req.Context.Caller.Env = identity.Env
	}

	if strings.TrimSpace(req.Target.Namespace) == "" {
		req.Target.Namespace = identity.Namespace
	}
	if strings.TrimSpace(req.Target.Env) == "" {
		req.Target.Env = identity.Env
	}
	if strings.TrimSpace(req.Context.TraceId) == "" {
		req.Context.TraceId = defaultTraceID(identity)
	}

	return nil
}

// normalizeLocalIdentity 统一修整 sidecar 本地身份字段。
func normalizeLocalIdentity(identity LocalIdentity) *LocalIdentity {
	identity.AppID = strings.TrimSpace(identity.AppID)
	identity.Service = strings.TrimSpace(identity.Service)
	identity.Namespace = strings.TrimSpace(identity.Namespace)
	identity.Env = strings.TrimSpace(identity.Env)
	if identity.AppID == "" {
		identity.AppID = identity.Service
	}
	return &identity
}

// ensureMatch 用于拒绝与 sidecar 绑定身份冲突的显式调用上下文。
func ensureMatch(field, actual, expected string) error {
	actual = strings.TrimSpace(actual)
	expected = strings.TrimSpace(expected)
	if actual == "" || expected == "" {
		return nil
	}
	if actual != expected {
		return errors.New(field + " conflicts with sidecar local identity")
	}
	return nil
}

// defaultTraceID 在请求未带 trace_id 时生成一个最小可用值。
func defaultTraceID(identity *LocalIdentity) string {
	base := "mesh"
	if identity != nil && strings.TrimSpace(identity.Service) != "" {
		base = strings.TrimSpace(identity.Service)
	}
	return base + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}
