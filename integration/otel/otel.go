package otel

import (
	"context"
	"errors"

	"github.com/fireflycore/service-mesh/pkg/config"
	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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
	// traceProvider/meterProvider 可能分别为空，取决于开关配置。
	traceProvider *sdktrace.TracerProvider
	meterProvider *metric.MeterProvider
}

// New 根据 telemetry 配置安装全局 OTel provider。
func New(ctx context.Context, cfg config.TelemetryConfig, serviceName string, attrs ...attribute.KeyValue) (*Providers, error) {
	providers := &Providers{}

	if !cfg.TraceEnabled && !cfg.MetricEnabled {
		// 两个开关都关闭时直接返回空 providers，调用方仍可安全 Shutdown。
		return providers, nil
	}

	// resource 负责给 trace/metric 统一挂上 service.name 等资源属性。
	baseAttrs := append([]attribute.KeyValue{semconv.ServiceName(serviceName)}, attrs...)
	res, err := resource.New(ctx,
		resource.WithAttributes(baseAttrs...),
		resource.WithTelemetrySDK(),
		resource.WithProcess(),
	)
	if err != nil {
		return nil, err
	}

	if cfg.TraceEnabled {
		// 当前 trace exporter 固定使用 OTLP/HTTP。
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(cfg.OTLPEndpoint))
		if err != nil {
			return nil, err
		}

		providers.traceProvider = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(res),
		)
		// 安装全局 tracer provider 后，业务层通过 otel.Tracer() 即可直接取用。
		otelapi.SetTracerProvider(providers.traceProvider)
		// 同时安装传播器，后续若接入跨服务 trace 透传会直接复用。
		otelapi.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))
	}

	if cfg.MetricEnabled {
		// metric 同样走 OTLP/HTTP，并使用周期性 reader 主动导出。
		exporter, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(cfg.OTLPEndpoint))
		if err != nil {
			return nil, err
		}

		providers.meterProvider = metric.NewMeterProvider(
			metric.WithResource(res),
			metric.WithReader(metric.NewPeriodicReader(exporter)),
		)
		// 安装全局 meter provider 后，业务层通过 otel.Meter() 即可直接取用。
		otelapi.SetMeterProvider(providers.meterProvider)
	}

	return providers, nil
}

// Shutdown 在进程退出时刷新并关闭 provider。
func (p *Providers) Shutdown(ctx context.Context) error {
	var shutdownErr error

	if p.traceProvider != nil {
		// Join 保证 trace/meter 的关闭错误都不会丢失。
		shutdownErr = errors.Join(shutdownErr, p.traceProvider.Shutdown(ctx))
	}
	if p.meterProvider != nil {
		// meter provider 也要参与统一关闭，确保周期性 reader flush 完成。
		shutdownErr = errors.Join(shutdownErr, p.meterProvider.Shutdown(ctx))
	}

	return shutdownErr
}
