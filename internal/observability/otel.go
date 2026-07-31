// Package observability, OpenTelemetry başlatma ve Kafka W3C TraceContext
// propagasyon altyapısını sağlar.
//
// T-E09 / O-05: OTel + Kafka W3C TraceContext propagation.
package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Provider, OTel MeterProvider ve TracerProvider'ı kapsar.
type Provider struct {
	meterProvider  *metric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
}

// Config, OTel başlatma yapılandırmasıdır.
type Config struct {
	ServiceName    string
	ServiceVersion string
}

// Init, Prometheus metrik dışa aktarıcısını ve OTLP-gRPC iz dışa
// aktarıcısını başlatır.
//
// # İz arka ucu yoksa da servis çökmez (ADR-34/3)
//
// `OTEL_EXPORTER_OTLP_ENDPOINT` ayarlı değilse veya karşı taraf yoksa,
// otlptracegrpc istemcisi arka planda yeniden dener ve span'ler sessizce
// atılır — bu, health/ready uçlarının zaten uyguladığı "gözlemlenebilirlik
// altyapısı olmadan da servis ayakta kalır" ilkesiyle tutarlıdır. Plan
// mimarisi (BÖLÜM C.2) bir iz arka ucu (Jaeger/Tempo) hiç önermez; bu
// exporter yalnızca `.env.example`'da zaten tanımlı ama tüketilmeyen
// değişkeni kullanılır hâle getirir.
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

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpointURL(defaultEndpoint(endpoint)),
		otlptracegrpc.WithInsecure(),
		// MaxElapsedTime SINIRLIDIR: 0 "sınırsız yeniden dene" anlamına gelir
		// ve arka uç hiç yoksa (ADR-34/3'ün beklediği normal durum) Shutdown
		// çağrısını süresiz bloke ederdi — batch job'lar (cmd/validation,
		// cmd/integrity) hiç çıkamazdı.
		otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig{Enabled: true, MaxElapsedTime: 3 * time.Second}),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return &Provider{meterProvider: mp, tracerProvider: tp}, nil
}

func defaultEndpoint(v string) string {
	if v == "" {
		return "http://localhost:4317"
	}
	return v
}

// Shutdown, OTel provider'larını düzgün biçimde kapatır.
//
// Çağıranın ctx'i ne olursa olsun burada bir üst sınır uygulanır: arka uç
// yoksa (ADR-34/3) son bir flush denemesi normaldir, ama bu deneme
// servisin çıkışını süresiz bloke etmemelidir — özellikle boşta-çıkış
// modundaki tüketiciler ve tek seferlik toplu işler (cmd/validation) için.
func (p *Provider) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := p.tracerProvider.Shutdown(ctx); err != nil {
		return err
	}
	return p.meterProvider.Shutdown(ctx)
}

// MustServeMetrics, `/metrics` ucunu servis eder ve hata durumunda panikler.
//
// Port, `prometheus.yml`'de servis başına zaten tanımlı hedef port
// olmalıdır (2112 simulator … 2116 gateway) — bu fonksiyon yeni bir port
// şeması icat etmez, var olan provisioning'i çalışır hâle getirir.
func (p *Provider) MustServeMetrics(addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	slog.Info("metrics sunucusu başlatılıyor", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic("metrics server hatası: " + err.Error())
	}
}

// Tracer, verilen servis için bir OTel tracer döndürür.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}
