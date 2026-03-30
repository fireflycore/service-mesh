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
	// Resolve 把逻辑服务标识转换成当前可访问的单个实例地址。
	Resolve(ctx context.Context, target model.ServiceRef) (model.Endpoint, error)
}

// PolicySource 表示一个可选的运行时策略来源，通常来自 controlplane。
type PolicySource interface {
	ResolveRoutePolicy(target model.ServiceRef) (*controlv1.RoutePolicy, bool)
}

// LocalIdentity 描述 sidecar 绑定的本地业务服务身份。
type LocalIdentity struct {
	// AppID 通常对应业务应用标识；为空时会回退到 Service。
	AppID string
	// Service 是 sidecar 绑定的本地逻辑服务名。
	Service string
	// Namespace/Env 用于把本地调用语义和目录/控制面维度对齐。
	Namespace string
	Env       string
	// TargetMode 控制是否允许目标再次指向本地绑定服务身份。
	TargetMode string
}

// Options 定义 Invoke 服务在第六版引入的可靠性策略。
//
// 这些策略的目标很明确：
// - timeout：避免一次调用无限挂住
// - retry：在典型瞬时错误下做最小重试
type Options struct {
	// Timeout 是整次调用预算。
	Timeout time.Duration
	// PerTryTimeout 是单次尝试预算。
	PerTryTimeout time.Duration
	// RetryMaxAttempts 包含第一次尝试。
	RetryMaxAttempts uint32
	// RetryBackoff 控制两次重试之间的等待时间。
	RetryBackoff time.Duration
	// RetryableCodes 决定哪些错误允许重试。
	RetryableCodes map[codes.Code]struct{}
	// Telemetry 用于记录最小 trace/metric。
	Telemetry *meshtelemetry.Emitter
	// PolicySource 允许控制面覆盖本地 timeout/retry。
	PolicySource PolicySource
	// LocalIdentity 仅在 sidecar 模式下注入，用于补齐 caller/target 维度。
	LocalIdentity *LocalIdentity
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

	// 这三段依赖分别对应鉴权、目标解析、真实转发三个阶段。
	authorizer authz.Authorizer
	resolver   Resolver
	transport  transport.Invoker
	// options 保存当前服务实例的静态默认策略。
	options Options
}

// NewService 创建一个本地 MeshInvokeService。
func NewService(authorizer authz.Authorizer, resolver Resolver, transport transport.Invoker, options ...Options) *Service {
	// 先拿默认值，只有调用者显式传入时才覆盖。
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
	// 入口先做最小参数校验，避免后续 resolver/transport 收到脏数据。
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
		// sidecar 入口若检测到 caller identity 冲突，会在这里直接拒绝。
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// 从 proto target 收敛成内部统一目标结构，方便 resolver/source 共用。
	target := model.ServiceRef{
		Service:   req.GetTarget().GetService(),
		Namespace: req.GetTarget().GetNamespace(),
		Env:       req.GetTarget().GetEnv(),
		Port:      req.GetTarget().GetPort(),
	}
	if err := validateSidecarTarget(target, s.options.LocalIdentity); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	effectiveOptions := s.options
	if s.options.PolicySource != nil {
		// 控制面若下发了 RoutePolicy，则在当前调用维度覆盖本地静态配置。
		if policy, ok := s.options.PolicySource.ResolveRoutePolicy(target); ok {
			// 这里不直接改写 s.options，避免一次策略命中污染后续其他请求。
			effectiveOptions = mergeOptionsWithPolicy(s.options, policy)
		}
	}

	callCtx := ctx
	callTimeout := effectiveOptions.Timeout
	if req.GetContext().GetTimeoutMs() > 0 {
		// 请求上下文里的 timeout_ms 优先级高于本地默认值。
		callTimeout = time.Duration(req.GetContext().GetTimeoutMs()) * time.Millisecond
	}
	if callTimeout > 0 {
		// 这层 timeout 覆盖整次调用生命周期，包含鉴权、解析、重试与下游转发。
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, callTimeout)
		defer cancel()
	}

	// telemetry 从真正进入业务链路前开始统计。
	effectiveOptions.Telemetry.RecordRequest(callCtx)
	// spanCtx 可能在 StartInvoke 内挂上 trace/span 元数据，后续各阶段都沿用它。
	spanCtx, span := effectiveOptions.Telemetry.StartInvoke(callCtx, req)
	defer span.End()
	start := time.Now()
	defer func() {
		// 无论成功失败都记录 latency，便于后续看尾延迟。
		effectiveOptions.Telemetry.RecordLatency(spanCtx, time.Since(start))
	}()

	// authz 放在 resolver 之前，避免未授权请求也去访问目录和下游。
	if err := s.authorizer.Check(spanCtx, req); err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "authz failed")
		effectiveOptions.Telemetry.RecordFailure(spanCtx, "authz")
		return nil, status.Errorf(codes.PermissionDenied, "authz check failed: %v", err)
	}

	attempts := effectiveOptions.RetryMaxAttempts
	if attempts == 0 {
		// 兜底保证至少执行一次。
		attempts = 1
	}
	// attempts 的语义是“总尝试次数”，因此 2 代表“首次 + 1 次重试”。

	var lastErr error
	for attempt := uint32(1); attempt <= attempts; attempt++ {
		// 默认每一轮尝试沿用总调用上下文；只有配置了 per-try timeout 才再切子 ctx。
		attemptCtx := spanCtx
		attemptCancel := func() {}
		if effectiveOptions.PerTryTimeout > 0 {
			// 每次尝试都在总预算内再切一层子超时，避免单次重试吃满全部预算。
			attemptCtx, attemptCancel = context.WithTimeout(callCtx, effectiveOptions.PerTryTimeout)
		}

		// 先解析出本次调用真正命中的实例。
		endpoint, err := s.resolver.Resolve(attemptCtx, target)
		if err != nil {
			// resolver 失败通常意味着目录不可用、无健康实例，或 balancer 无法选点。
			lastErr = err
		} else {
			// 只有解析成功后才真正发起下游网络请求。
			// transport 只负责把请求真正发给下游业务实例。
			resp, invokeErr := s.transport.Invoke(attemptCtx, endpoint, req)
			if invokeErr == nil {
				attemptCancel()
				span.SetStatus(otelcodes.Ok, "invoke succeeded")
				return resp, nil
			}
			span.RecordError(invokeErr)
			// transport 失败后继续看是否命中 retry 策略。
			lastErr = invokeErr
		}
		attemptCancel()

		// 当前错误若不符合 retry 策略，就立即结束重试循环。
		if !shouldRetry(lastErr, attempt, attempts, effectiveOptions) {
			break
		}
		// 记录的是“已经决定继续下一轮”的重试事件，而不是简单的失败次数。
		effectiveOptions.Telemetry.RecordRetry(spanCtx, int64(attempt))

		if effectiveOptions.RetryBackoff > 0 {
			// backoff 期间也要响应 ctx 取消，避免退出时还卡在 sleep。
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
		// Canceled 和 DeadlineExceeded 会被精确映射到 gRPC 语义错误码。
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
	// 其余错误统一按 Unavailable 返回，表示这次目标调用未成功完成。
	span.RecordError(lastErr)
	span.SetStatus(otelcodes.Error, "invoke failed")
	effectiveOptions.Telemetry.RecordFailure(spanCtx, "invoke")
	return nil, status.Errorf(codes.Unavailable, "invoke target failed: %v", lastErr)
}

func defaultOptions() Options {
	return normalizeOptions(Options{
		// 默认值与配置默认值保持一致，保证直接构造 Service 时语义稳定。
		Timeout:          1500 * time.Millisecond,
		PerTryTimeout:    500 * time.Millisecond,
		RetryMaxAttempts: 2,
		RetryBackoff:     50 * time.Millisecond,
		RetryableCodes: map[codes.Code]struct{}{
			// 默认只对常见瞬时错误打开重试。
			codes.Unavailable:       {},
			codes.DeadlineExceeded:  {},
			codes.ResourceExhausted: {},
		},
	})
}

// normalizeOptions 把外部传入的可选项修整成运行时稳定可用的形态。
func normalizeOptions(options Options) Options {
	// 下面依次补齐每个运行时选项的兜底值。
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
			// 这里和 defaultOptions 保持相同集合，避免两条默认路径出现漂移。
			codes.Unavailable:       {},
			codes.DeadlineExceeded:  {},
			codes.ResourceExhausted: {},
		}
	}
	if options.Telemetry == nil {
		// 没有 telemetry 时用 noop emitter，避免调用方处处判空。
		options.Telemetry = meshtelemetry.NewNoopEmitter()
	}
	if options.LocalIdentity != nil {
		// sidecar identity 也统一做 trim，避免字符串空白造成伪冲突。
		options.LocalIdentity = normalizeLocalIdentity(*options.LocalIdentity)
	}
	return options
}

// OptionsFromConfig 把配置层的 invoke 策略转换成运行时选项。
func OptionsFromConfig(cfg config.InvokeConfig) Options {
	retryable := make(map[codes.Code]struct{}, len(cfg.RetryableCodes))
	for _, codeName := range cfg.RetryableCodes {
		// 这里只接受少量明确支持的字符串，未知值会被静默忽略。
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
	// 静默忽略未知 code 的目的是兼容未来配置扩展，而不是在加载阶段直接失败。

	// 配置层用毫秒和字符串表达，运行时层统一转成 Duration 和枚举 map。
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
		// 成功或已经达到最大尝试次数，都不再重试。
		return false
	}
	if errors.Is(err, context.Canceled) {
		// 用户主动取消时不应该再做任何重试。
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// DeadlineExceeded 既可能来自总超时，也可能来自 per-try timeout，
		// 是否重试交给 RetryableCodes 明确控制。
		_, ok := options.RetryableCodes[codes.DeadlineExceeded]
		return ok
	}

	statusCode := status.Code(err)
	// 对普通 gRPC error，直接按 status code 是否出现在 allow-list 决定。
	_, ok := options.RetryableCodes[statusCode]
	return ok
}

// mergeOptionsWithPolicy 把控制面策略覆盖到当前调用生效选项上。
func mergeOptionsWithPolicy(base Options, policy *controlv1.RoutePolicy) Options {
	if policy == nil {
		return base
	}

	merged := base
	if policy.GetTimeoutMs() > 0 {
		// 控制面 timeout 一旦下发，就覆盖本地静态超时预算。
		merged.Timeout = time.Duration(policy.GetTimeoutMs()) * time.Millisecond
	}
	if policy.GetRetry() != nil {
		if policy.GetRetry().GetMaxAttempts() > 0 {
			// max_attempts 同样包含第一次尝试，和本地语义保持一致。
			merged.RetryMaxAttempts = policy.GetRetry().GetMaxAttempts()
		}
		if policy.GetRetry().GetPerTryTimeoutMs() > 0 {
			// 当前控制面只覆盖 max_attempts 和 per_try_timeout 两个最关键项。
			merged.PerTryTimeout = time.Duration(policy.GetRetry().GetPerTryTimeoutMs()) * time.Millisecond
		}
	}

	// 覆盖后再次走 normalize，确保最终选项仍然完整可用。
	return normalizeOptions(merged)
}

// applyLocalIdentity 在 sidecar 模式下补齐本地 caller 身份与默认目标域。
func applyLocalIdentity(req *invokev1.UnaryInvokeRequest, identity *LocalIdentity) error {
	if identity == nil {
		// agent 模式没有本地服务身份补齐需求，因此直接跳过。
		return nil
	}
	if req.Context == nil {
		// sidecar 允许调用方省略 context，这里会自动补一个空壳。
		req.Context = &invokev1.InvocationContext{}
	}
	if req.Context.Caller == nil {
		req.Context.Caller = &invokev1.Caller{}
	}

	// 如果调用方显式传了 caller 维度，就要求它和 sidecar 绑定身份一致。
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
		// AppId 缺省时默认与 Service 对齐，避免本地调用方必须重复填写。
		req.Context.Caller.AppId = identity.AppID
	}
	if strings.TrimSpace(req.Context.Caller.Service) == "" {
		// sidecar 默认把 caller.service 绑定到自己的本地业务服务名。
		req.Context.Caller.Service = identity.Service
	}
	if strings.TrimSpace(req.Context.Caller.Namespace) == "" {
		req.Context.Caller.Namespace = identity.Namespace
	}
	if strings.TrimSpace(req.Context.Caller.Env) == "" {
		req.Context.Caller.Env = identity.Env
	}

	if strings.TrimSpace(req.Target.Namespace) == "" {
		// target 缺省时回退到 sidecar 绑定的 namespace / env。
		req.Target.Namespace = identity.Namespace
	}
	if strings.TrimSpace(req.Target.Env) == "" {
		req.Target.Env = identity.Env
	}
	if strings.TrimSpace(req.Context.TraceId) == "" {
		// 当前阶段还没有完整 trace 上下文透传时，先生成一个最小 trace_id。
		req.Context.TraceId = defaultTraceID(identity)
	}

	return nil
}

// validateSidecarTarget 拒绝把本地 sidecar 当成“调自己”的上游代理使用。
func validateSidecarTarget(target model.ServiceRef, identity *LocalIdentity) error {
	if identity == nil {
		return nil
	}
	switch strings.TrimSpace(identity.TargetMode) {
	case model.SidecarTargetModeAllowSameService:
		return nil
	case model.SidecarTargetModeAllowCrossScopeSameService:
		return validateCrossScopeSameServiceTarget(target, identity)
	}

	targetService := strings.TrimSpace(target.Service)
	identityService := strings.TrimSpace(identity.Service)
	if targetService == "" || identityService == "" {
		return nil
	}
	if targetService != identityService {
		return nil
	}
	if !matchesLocalDimension(target.Namespace, identity.Namespace) {
		return nil
	}
	if !matchesLocalDimension(target.Env, identity.Env) {
		return nil
	}
	return errors.New("target conflicts with sidecar local service identity")
}

func validateCrossScopeSameServiceTarget(target model.ServiceRef, identity *LocalIdentity) error {
	targetService := strings.TrimSpace(target.Service)
	identityService := strings.TrimSpace(identity.Service)
	if targetService == "" || identityService == "" || targetService != identityService {
		return nil
	}

	targetNamespace := strings.TrimSpace(target.Namespace)
	targetEnv := strings.TrimSpace(target.Env)
	localNamespace := strings.TrimSpace(identity.Namespace)
	localEnv := strings.TrimSpace(identity.Env)

	if targetNamespace != "" && localNamespace != "" && targetNamespace != localNamespace {
		return nil
	}
	if targetEnv != "" && localEnv != "" && targetEnv != localEnv {
		return nil
	}
	return errors.New("target conflicts with sidecar local service identity under cross-scope target mode")
}

// normalizeLocalIdentity 统一修整 sidecar 本地身份字段。
func normalizeLocalIdentity(identity LocalIdentity) *LocalIdentity {
	identity.AppID = strings.TrimSpace(identity.AppID)
	identity.Service = strings.TrimSpace(identity.Service)
	identity.Namespace = strings.TrimSpace(identity.Namespace)
	identity.Env = strings.TrimSpace(identity.Env)
	identity.TargetMode = strings.TrimSpace(strings.ToLower(identity.TargetMode))
	if identity.AppID == "" {
		// AppID 缺失时回退到 Service，保证 caller identity 始终是完整的。
		identity.AppID = identity.Service
	}
	if identity.TargetMode == "" {
		identity.TargetMode = model.SidecarTargetModeUpstreamOnly
	}
	return &identity
}

func matchesLocalDimension(target, local string) bool {
	target = strings.TrimSpace(target)
	local = strings.TrimSpace(local)
	return target == "" || local == "" || target == local
}

// ensureMatch 用于拒绝与 sidecar 绑定身份冲突的显式调用上下文。
func ensureMatch(field, actual, expected string) error {
	actual = strings.TrimSpace(actual)
	expected = strings.TrimSpace(expected)
	if actual == "" || expected == "" {
		// 任意一侧为空都视为“没有显式冲突”，留给后续补齐逻辑处理。
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
		// 有本地服务名时，把 trace 前缀设成 service，更利于日志肉眼识别。
		base = strings.TrimSpace(identity.Service)
	}
	return base + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}
