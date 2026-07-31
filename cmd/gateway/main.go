// cmd/gateway — S5 API Gateway (E06, ADR-12, ADR-33).
//
// İki yüzey, tek çekirdek:
//
//	gRPC  :50051  iç tüketiciler          (proto/hts/v1)
//	REST  :8080   dış istemciler + /static (elle yazılmış handler'lar)
//
// ADR-12/1: `grpc-gateway` kullanılmaz; REST handler'ları elle yazılır ve gRPC
// ile **aynı** `internal/gateway/query` katmanını çağırır.
// ADR-12/3: `cmd/visualization` kaldırıldı; `web/` doğrudan buradan servis
// edilir ve CORS problemi ortadan kalkar.
//
// # Kör test API katmanına kadar uzar
//
// DSN rolü `svc_gateway`'dir ve o rolün `ground_truth` üzerinde **hiçbir
// yetkisi yoktur** (migration 007, ADR-33/2). Gerçek konum, modelin
// doğruluğunu ölçmek için vardır ve o ölçüm `metrics` tablosunda
// toplulaştırılmış hâlde yayınlanır; tek tek gerçek konumları API'den servis
// etmek, sistemin dışarıya "bu abone tam olarak buradaydı" demesi olurdu.
//
// Ortam değişkenleri:
//
//	HTS_REST_ADDR    REST dinleme adresi (varsayılan :8080)
//	HTS_GRPC_ADDR    gRPC dinleme adresi (varsayılan :50051 — :9090 Prometheusta)
//	HTS_HEALTH_ADDR  sağlık ucu (varsayılan :8086 — ADR-33/6)
//	HTS_STATIC_DIR   statik dosya dizini (varsayılan ./web)
//	HTS_GATEWAY_DB_USER / HTS_GATEWAY_DB_PASSWORD · POSTGRES_*
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/gateway/audit"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/gateway/grpcsrv"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/gateway/query"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/gateway/rest"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/observability"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/observability/health"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	htsv1 "github.com/NUMAN-GURBUZ/hts-kga/proto/hts/v1"
)

const (
	serviceName    = "hts-gateway"
	serviceVersion = "0.7.0-sprint7"
	metricsAddr    = ":2116" // prometheus.yml: hts-gateway hedefi
)

func main() {
	if err := execute(); err != nil {
		slog.Error("gateway durdu", "hata", err)
		os.Exit(1)
	}
}

func execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	prov, err := observability.Init(ctx, observability.Config{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
	})
	if err != nil {
		return fmt.Errorf("OTel başlatılamadı: %w", err)
	}
	defer func() { _ = prov.Shutdown(context.Background()) }()

	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	pool, err := postgres.NewPool(initCtx, dsnFromEnv())
	if err != nil {
		return fmt.Errorf("PostgreSQL bağlantısı (svc_gateway): %w", err)
	}
	defer pool.Close()

	svc, err := query.New(pool.Querier())
	if err != nil {
		return err
	}
	auditor, err := audit.New(pool.Querier(), logger)
	if err != nil {
		return err
	}

	// Sağlık ucu :8086 — cmd/integrity :8085 kullanıyor (ADR-33/6,
	// Sprint 6 borcu #4).
	h := health.New(serviceName, serviceVersion)
	h.Register("postgres", pool.Ping)
	go h.MustServe(envOr("HTS_HEALTH_ADDR", ":8086"))
	go prov.MustServeMetrics(envOr("HTS_METRICS_ADDR", metricsAddr))

	staticDir := envOr("HTS_STATIC_DIR", "./web")
	restSrv, err := rest.New(rest.Config{
		Service:   svc,
		Auditor:   auditor,
		Logger:    logger,
		StaticDir: staticDir,
	})
	if err != nil {
		return err
	}

	grpcImpl, err := grpcsrv.New(svc, auditor)
	if err != nil {
		return err
	}

	restAddr := envOr("HTS_REST_ADDR", ":8080")
	grpcAddr := envOr("HTS_GRPC_ADDR", ":50051")

	httpSrv := &http.Server{
		Addr:              restAddr,
		Handler:           restSrv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	grpcSrv := grpc.NewServer()
	htsv1.RegisterHtsServiceServer(grpcSrv, grpcImpl)

	errCh := make(chan error, 2)

	go func() {
		slog.Info("REST dinleniyor", "addr", restAddr, "static", staticDir)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("REST sunucusu: %w", err)
		}
	}()

	go func() {
		lis, err := net.Listen("tcp", grpcAddr)
		if err != nil {
			errCh <- fmt.Errorf("gRPC dinleyicisi: %w", err)
			return
		}
		slog.Info("gRPC dinleniyor", "addr", grpcAddr)
		if err := grpcSrv.Serve(lis); err != nil {
			errCh <- fmt.Errorf("gRPC sunucusu: %w", err)
		}
	}()

	slog.Info("gateway başlatıldı",
		"rest", restAddr, "grpc", grpcAddr,
		"rol", envOr("HTS_GATEWAY_DB_USER", "svc_gateway"),
		"not", "ground_truth erişimi YOK (ADR-33/2)")

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
	}

	// Kapanış: bekleyen istekler tamamlansın, denetim satırları yazılsın.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	grpcSrv.GracefulStop()

	slog.Info("gateway kapatıldı",
		"denetim_yazılan", auditor.Written(), "denetim_düşen", auditor.Dropped())
	if auditor.Dropped() > 0 {
		slog.Error("denetim satırı kaybı var — istekler servis edildi ama izi eksik",
			"adet", auditor.Dropped())
	}
	return runErr
}

// dsnFromEnv, `svc_gateway` rolüyle bağlanır (ADR-33/2).
//
// Rol seçimi kritiktir: `hts_admin` ile bağlanılsaydı gateway `ground_truth`'u
// okuyabilir ve kör test API katmanında delinmiş olurdu.
func dsnFromEnv() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		envOr("HTS_GATEWAY_DB_USER", "svc_gateway"),
		envOr("HTS_GATEWAY_DB_PASSWORD", "gateway_dev_pw"),
		envOr("POSTGRES_HOST", "localhost"),
		envOr("POSTGRES_PORT", "5432"),
		envOr("POSTGRES_DB", "hts_kga"))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
