package otel

import (
	"context"
	"errors"

	"github.com/fireflycore/service-mesh/pkg/config"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Providers 统一管理第七版引入的 OTel provider 生命周期。
//
// 当前阶段只做最基础的事：
// - 初始化 trace provider
// - 初始化 metric provider
// - 在进程退出时统一 shutdown
type Providers struct {
	traceProvider *sdktrace.TracerProvider
	meterProvider *metric.MeterProvider
}

// New 根据 telemetry 配置安装全局 OTel provider。
func New(ctx context.Context, cfg config.TelemetryConfig, serviceName string) (*Providers, error) {
	providers := &Providers{}

	if !cfg.TraceEnabled && !cfg.MetricEnabled {
		return providers, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
		resource.WithTelemetrySDK(),
		resource.WithProcess(),
	)
	if err != nil {
		return nil, err
	}

	if cfg.TraceEnabled {
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(cfg.OTLPEndpoint))
		if err != nil {
			return nil, err
		}

		providers.traceProvider = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(res),
		)
		otelapi.SetTracerProvider(providers.traceProvider)
		otelapi.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
	}

	if cfg.MetricEnabled {
		exporter, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(cfg.OTLPEndpoint))
		if err != nil {
			return nil, err
		}

		providers.meterProvider = metric.NewMeterProvider(
			metric.WithResource(res),
			metric.WithReader(metric.NewPeriodicReader(exporter)),
		)
		otelapi.SetMeterProvider(providers.meterProvider)
	}

	return providers, nil
}

// Shutdown 在进程退出时刷新并关闭 provider。
func (p *Providers) Shutdown(ctx context.Context) error {
	var shutdownErr error

	if p.traceProvider != nil {
		shutdownErr = errors.Join(shutdownErr, p.traceProvider.Shutdown(ctx))
	}
	if p.meterProvider != nil {
		shutdownErr = errors.Join(shutdownErr, p.meterProvider.Shutdown(ctx))
	}

	return shutdownErr
}
