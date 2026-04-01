package telemetry

import (
	"context"
	"time"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"github.com/fireflycore/service-mesh/pkg/originalidentity"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Emitter 以最小封装方式暴露 tracing 和 metrics 两类能力，
// 避免 invoke 层直接持有一组零散的 OTel 句柄。
//
// 当前只负责两类事情：
// - 为 invoke 调用创建 span
// - 记录请求数、失败数、耗时与重试次数
type Emitter struct {
	// tracer 负责创建 span。
	tracer trace.Tracer
	// 下面四个 instrument 分别统计请求、失败、重试与耗时。
	requestCounter metric.Int64Counter
	failureCounter metric.Int64Counter
	retryCounter   metric.Int64Counter
	latency        metric.Int64Histogram
}

// NewEmitter 基于全局 OTel provider 创建 telemetry emitter。
func NewEmitter() (*Emitter, error) {
	// meter/tracer 都依赖 integration/otel 预先安装的全局 provider。
	meter := otel.Meter("github.com/fireflycore/service-mesh/plane/data/telemetry")

	requestCounter, err := meter.Int64Counter("service_mesh.invoke.requests")
	if err != nil {
		return nil, err
	}
	failureCounter, err := meter.Int64Counter("service_mesh.invoke.failures")
	if err != nil {
		return nil, err
	}
	retryCounter, err := meter.Int64Counter("service_mesh.invoke.retries")
	if err != nil {
		return nil, err
	}
	latency, err := meter.Int64Histogram("service_mesh.invoke.duration_ms")
	if err != nil {
		return nil, err
	}

	return &Emitter{
		tracer:         otel.Tracer("github.com/fireflycore/service-mesh/plane/data/invoke"),
		requestCounter: requestCounter,
		failureCounter: failureCounter,
		retryCounter:   retryCounter,
		latency:        latency,
	}, nil
}

// NewNoopEmitter 返回一个可安全调用的空 emitter。
func NewNoopEmitter() *Emitter {
	return &Emitter{}
}

// StartInvoke 为一次本地 Invoke 调用创建 span。
func (e *Emitter) StartInvoke(ctx context.Context, req *invokev1.UnaryInvokeRequest) (context.Context, trace.Span) {
	if e == nil || e.tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}

	attrs := []attribute.KeyValue{
		// 这里只记录最关键的目标与协议属性，保持 span 属性简洁稳定。
		attribute.String("mesh.target.service", req.GetTarget().GetService()),
		attribute.String("mesh.target.namespace", req.GetTarget().GetNamespace()),
		attribute.String("mesh.target.env", req.GetTarget().GetEnv()),
		attribute.String("rpc.method", req.GetMethod()),
		attribute.String("rpc.codec", req.GetCodec()),
	}
	original := originalidentity.Resolve(req.GetContext())
	principal := original.Principal()
	if original.Present() {
		attrs = append(attrs,
			attribute.Bool("mesh.original_user.present", true),
			attribute.String("mesh.original_user.source", original.Source),
			attribute.String("mesh.original_user.trust", original.Trust),
			attribute.String("mesh.original_user.subject", original.Subject),
			attribute.String("mesh.original_user.issuer", original.Issuer),
		)
	}
	attrs = append(attrs,
		attribute.String("mesh.effective_principal.kind", principal.Kind),
		attribute.String("mesh.effective_principal.trust", principal.Trust),
	)
	if principal.Subject != "" {
		attrs = append(attrs, attribute.String("mesh.effective_principal.subject", principal.Subject))
	}

	return e.tracer.Start(ctx, "service-mesh.invoke", trace.WithAttributes(attrs...))
}

// RecordRequest 记录请求总数。
func (e *Emitter) RecordRequest(ctx context.Context) {
	if e == nil || e.requestCounter == nil {
		return
	}
	e.requestCounter.Add(ctx, 1)
}

// RecordFailure 记录失败总数。
func (e *Emitter) RecordFailure(ctx context.Context, reason string) {
	if e == nil || e.failureCounter == nil {
		return
	}
	// failure reason 会作为一个低基数字段，便于按失败原因聚合。
	e.failureCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("mesh.failure.reason", reason),
	))
}

// RecordRetry 记录一次重试。
func (e *Emitter) RecordRetry(ctx context.Context, attempt int64) {
	if e == nil || e.retryCounter == nil {
		return
	}
	// attempt 记录的是已经进入的重试轮次，不包含初次尝试前的状态。
	e.retryCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.Int64("mesh.retry.attempt", attempt),
	))
}

// RecordLatency 记录一次调用耗时。
func (e *Emitter) RecordLatency(ctx context.Context, duration time.Duration) {
	if e == nil || e.latency == nil {
		return
	}
	e.latency.Record(ctx, duration.Milliseconds())
}
