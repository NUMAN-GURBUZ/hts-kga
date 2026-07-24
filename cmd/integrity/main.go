// cmd/integrity — HTS-KGA integrity giriş noktası
// Sprint ilgili task'lar bu paketi doldurur.
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
	serviceName    = "hts-integrity"
	serviceVersion = "0.1.0-sprint0"
	healthAddr     = ":8084"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	prov, err := observability.Init(ctx, observability.Config{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
	})
	if err != nil {
		slog.Error("OTel başlatılamadı", "error", err)
		os.Exit(1)
	}
	defer func() { _ = prov.Shutdown(context.Background()) }()

	h := health.New(serviceName, serviceVersion)
	go h.MustServe(healthAddr)

	slog.Info("integrity başlatıldı", "health", healthAddr)
	<-ctx.Done()
	slog.Info("integrity kapatılıyor")
}
