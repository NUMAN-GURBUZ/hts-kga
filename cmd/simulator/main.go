// cmd/simulator — S1 Simülatör giriş noktası
// Sprint 1–2'de T-E02-* task'ları bu paketi doldurur.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/observability"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/observability/health"
)

const (
	serviceName    = "hts-simulator"
	serviceVersion = "0.1.0-sprint0"
	healthAddr     = ":8081"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	// OTel başlat
	prov, err := observability.Init(ctx, observability.Config{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
	})
	if err != nil {
		slog.Error("OTel başlatılamadı", "error", err)
		os.Exit(1)
	}
	defer func() { _ = prov.Shutdown(context.Background()) }()

	// Health/Ready sunucusu
	h := health.New(serviceName, serviceVersion)
	go h.MustServe(healthAddr)

	slog.Info("simülatör başlatıldı (Sprint 1–2 bekleniyor)", "health", healthAddr)
	<-ctx.Done()
	slog.Info("simülatör kapatılıyor")
}
