// Package observability, OpenTelemetry başlatma ve Kafka W3C TraceContext
// propagasyon altyapısını sağlar.
//
// T-E09 / O-05: OTel + Kafka W3C TraceContext propagation
// Sprint 0'da stub; Sprint 8'de Grafana provisioning ile genişletilir.
package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Provider, OTel MeterProvider ve TracerProvider'ı kapsar.
type Provider struct {
	meterProvider *metric.MeterProvider
}

// Config, OTel başlatma yapılandırmasıdır.
type Config struct {
	ServiceName    string
	ServiceVersion string
}

// Init, Prometheus exporter üzerinden OTel metric provider'ı başlatır.
// Trace exporter Sprint 8'de OTLP ile eklenir.
func Init(ctx context.Context, cfg Config) (*Provider, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	// Prometheus exporter — /metrics ucu üzerinden scrape edilir
	promExporter, err := prometheus.New()
	if err != nil {
		return nil, fmt.Errorf("prometheus exporter: %w", err)
	}

	mp := metric.NewMeterProvider(
		metric.WithReader(promExporter),
		metric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	return &Provider{meterProvider: mp}, nil
}

// Shutdown, OTel provider'ı düzgün biçimde kapatır.
func (p *Provider) Shutdown(ctx context.Context) error {
	return p.meterProvider.Shutdown(ctx)
}

// Tracer, verilen servis için bir OTel tracer döndürür.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}
