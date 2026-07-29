// cmd/simulator — S1 Simülatör giriş noktası (G1).
//
// Akış:
//
//	senaryo config → envanter (PostgreSQL + Redis) → koşu döngüsü
//	  → Kafka hts.records + hts.groundtruth → run_config.finished_at
//
// Ortam değişkenleri (.env):
//
//	HTS_CONFIG      senaryo YAML yolu (zorunlu)
//	HTS_RUN_ID      koşu kimliği; boşsa yeni üretilir (ADR-05)
//	HMAC_SALT       takma ad tuzu (E-08, en az 32 bayt)
//	POSTGRES_*      · KAFKA_BROKERS · REDIS_ADDR
//
// Koşu kimliği stderr'e `HTS_RUN_ID=<uuid>` olarak yazılır: sonraki adımlar
// (analiz, doğrulama) onu buradan alır.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/observability"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/observability/health"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/event"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/inventory"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/run"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/kafka"
)

const (
	serviceName    = "hts-simulator"
	serviceVersion = "0.5.0-sprint5"
	healthAddr     = ":8081"
)

// runStartAnchor, tick 0'ın takvim karşılığıdır.
//
// Pazartesi 00:00 seçilir: haftalık periyodisitenin (T-E02-08) referans günü
// budur ve koşular arasında sabit kalması karşılaştırılabilirliğin koşuludur.
var runStartAnchor = time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)

func main() {
	if err := execute(); err != nil {
		slog.Error("simülatör durdu", "hata", err)
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

	h := health.New(serviceName, serviceVersion)
	go h.MustServe(healthAddr)

	// ─── Yapılandırma ────────────────────────────────────────────────────────
	path := os.Getenv("HTS_CONFIG")
	if path == "" {
		return fmt.Errorf("HTS_CONFIG tanımlı değil (senaryo YAML yolu)")
	}
	scn, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("senaryo config'i okunamadı: %w", err)
	}

	runID := uuid.New()
	if raw := os.Getenv("HTS_RUN_ID"); raw != "" {
		if runID, err = uuid.Parse(raw); err != nil {
			return fmt.Errorf("HTS_RUN_ID geçersiz (%q): %w", raw, err)
		}
	}

	pseudo, err := event.NewPseudonymizerFromEnv()
	if err != nil {
		return fmt.Errorf("takma ad üreticisi: %w", err)
	}

	projector, err := geo.NewProjector(scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		return fmt.Errorf("ENU izdüşümü: %w", err)
	}

	initCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// ─── Envanter ────────────────────────────────────────────────────────────
	pool, err := postgres.NewPool(initCtx, dsnFromEnv())
	if err != nil {
		return fmt.Errorf("PostgreSQL bağlantısı: %w", err)
	}
	defer pool.Close()

	rdb, err := redis.NewClient(initCtx, envOr("REDIS_ADDR", "localhost:6379"))
	if err != nil {
		return fmt.Errorf("Redis bağlantısı: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	inv, err := inventory.Build(runID, scn, projector)
	if err != nil {
		return fmt.Errorf("envanter kurulamadı: %w", err)
	}
	store := inventory.Store{DB: pool, Cache: rdb}
	if err := store.Persist(initCtx, inv, scn, postgres.GitSHA()); err != nil {
		return fmt.Errorf("envanter yazılamadı: %w", err)
	}

	// ─── Koşu ────────────────────────────────────────────────────────────────
	producer, err := kafka.NewProducer(strings.Split(envOr("KAFKA_BROKERS", "localhost:9092"), ","))
	if err != nil {
		return fmt.Errorf("Kafka üreticisi: %w", err)
	}
	defer producer.Close()

	runner, err := run.New(run.Config{
		RunID:         runID,
		Scenario:      scn,
		Inventory:     inv,
		Projector:     projector,
		Pseudonymizer: pseudo,
		Sink:          producer,
		RunStart:      runStartAnchor,
		Logger:        logger,
	})
	if err != nil {
		return fmt.Errorf("koşu kurulamadı: %w", err)
	}

	slog.Info("simülasyon başlıyor",
		"run_id", runID, "senaryo", scn.Run.Scenario, "seed", scn.Run.Seed,
		"ajan", runner.AgentCount(), "tick", runner.Ticks(),
		"site", inv.SiteCount(), "hücre", inv.CellCount())
	fmt.Fprintf(os.Stderr, "HTS_RUN_ID=%s\n", runID)

	stats, err := runner.Run(ctx)
	if err != nil {
		return fmt.Errorf("koşu tamamlanamadı: %w", err)
	}

	// ─── Tamamlanma işareti (ADR-04, ADR-23) ─────────────────────────────────
	doneCtx, doneCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer doneCancel()
	if err := pool.CompleteRun(doneCtx, runID, stats.PublishedRecords); err != nil {
		return fmt.Errorf("koşu tamamlanma işareti: %w", err)
	}

	slog.Info("simülasyon tamamlandı",
		"run_id", runID,
		"olay", stats.Events,
		"kayıt", stats.PublishedRecords,
		"ground_truth", stats.PublishedTruths,
		"enjeksiyon", stats.Injected,
		"kapsama_dışı_olay", stats.Uncovered,
		"kapsama_dışı_oran", stats.Coverage.NoCoverageRatio(),
		"süre", stats.Duration.Round(time.Second))

	if stats.Coverage.ExceedsWarningThreshold() {
		slog.Warn("kapsama dışı oranı eşiği aşıyor (ADR-08/4)",
			"oran", stats.Coverage.NoCoverageRatio())
	}
	return nil
}

func dsnFromEnv() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		envOr("POSTGRES_USER", "hts_admin"),
		envOr("POSTGRES_PASSWORD", ""),
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
