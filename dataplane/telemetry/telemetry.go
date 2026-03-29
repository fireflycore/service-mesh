package telemetry

import (
	"context"
	"time"

	invokev1 "github.com/fireflycore/service-mesh/.gen/proto/acme/invoke/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Emitter 封装第七版需要的最小 telemetry 能力。
//
// 当前只负责两类事情：
// - 为 invoke 调用创建 span
// - 记录请求数、失败数、耗时与重试次数
type Emitter struct {
	tracer         trace.Tracer
	requestCounter metric.Int64Counter
	failureCounter metric.Int64Counter
	retryCounter   metric.Int64Counter
	latency        metric.Int64Histogram
}

// NewEmitter 基于全局 OTel provider 创建 telemetry emitter。
func NewEmitter() (*Emitter, error) {
	meter := otel.Meter("github.com/fireflycore/service-mesh/dataplane/telemetry")

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
		tracer:         otel.Tracer("github.com/fireflycore/service-mesh/dataplane/invoke"),
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
		attribute.String("mesh.target.service", req.GetTarget().GetService()),
		attribute.String("mesh.target.namespace", req.GetTarget().GetNamespace()),
		attribute.String("mesh.target.env", req.GetTarget().GetEnv()),
		attribute.String("rpc.method", req.GetMethod()),
		attribute.String("rpc.codec", req.GetCodec()),
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
	e.failureCounter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("mesh.failure.reason", reason),
	))
}

// RecordRetry 记录一次重试。
func (e *Emitter) RecordRetry(ctx context.Context, attempt int64) {
	if e == nil || e.retryCounter == nil {
		return
	}
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
