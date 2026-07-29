// cmd/persister — Kafka → PostgreSQL kalıcılaştırıcısı (G2 · T-E04-01).
//
// Tek ikili, iki **rol**:
//
//	HTS_PERSIST_MODE=records      S1b — hts.records     → hts_records
//	HTS_PERSIST_MODE=groundtruth  S3a — hts.groundtruth → ground_truth
//
// Roller ayrı çalıştırılır ve ayrı veritabanı kimlikleriyle bağlanır
// (`svc_gt_persister` yalnızca ground truth yazabilir). Aynı ikilinin iki
// modu olması kör testi zayıflatmaz: mod, hangi topic'e abone olunacağını
// belirler ve `records` modunda `hts.groundtruth`'a **hiç** abone olunmaz.
// Yetki sınırı kod değil, veritabanı rolü ve Kafka ACL'i tarafından çizilir.
//
// Ortam değişkenleri:
//
//	HTS_PERSIST_MODE  records | groundtruth (zorunlu)
//	HTS_GROUP         tüketici grubu; boşsa moddan türetilir
//	HTS_IDLE_TIMEOUT  akış boşta kalınca çıkış süresi (örn. "20s"); boşsa süresiz
//	POSTGRES_* · KAFKA_BROKERS
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

	"github.com/NUMAN-GURBUZ/hts-kga/internal/observability"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/observability/health"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/persist"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
)

const serviceVersion = "0.5.0-sprint5"

// healthAddrFor, rol başına sağlık ucunu döndürür.
//
// İki rol aynı makinede birlikte çalışır (toplu koşum); tek bir sabit port
// ikincisinin bağlanamayıp düşmesine yol açardı. HTS_HEALTH_ADDR ile
// geçersiz kılınabilir.
func healthAddrFor(mode string) string {
	if v := os.Getenv("HTS_HEALTH_ADDR"); v != "" {
		return v
	}
	if mode == "groundtruth" {
		return ":8084"
	}
	return ":8083"
}

func main() {
	if err := execute(); err != nil {
		slog.Error("persister durdu", "hata", err)
		os.Exit(1)
	}
}

func execute() error {
	mode := os.Getenv("HTS_PERSIST_MODE")
	if mode != "records" && mode != "groundtruth" {
		return fmt.Errorf("HTS_PERSIST_MODE 'records' veya 'groundtruth' olmalı (%q)", mode)
	}
	serviceName := "hts-persister-" + mode

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

	healthAddr := healthAddrFor(mode)
	h := health.New(serviceName, serviceVersion)
	go h.MustServe(healthAddr)

	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	pool, err := postgres.NewPool(initCtx, dsnFromEnv())
	if err != nil {
		return fmt.Errorf("PostgreSQL bağlantısı: %w", err)
	}
	defer pool.Close()

	brokers := strings.Split(envOr("KAFKA_BROKERS", "localhost:9092"), ",")
	group := envOr("HTS_GROUP", "hts-persister-"+mode)

	var idle time.Duration
	if v := os.Getenv("HTS_IDLE_TIMEOUT"); v != "" {
		if idle, err = time.ParseDuration(v); err != nil {
			return fmt.Errorf("HTS_IDLE_TIMEOUT geçersiz (%q): %w", v, err)
		}
	}

	slog.Info("persister başlatıldı", "mod", mode, "grup", group, "health", healthAddr)

	switch mode {
	case "records":
		consumer, err := persist.New(persist.Config[postgres.HTSRecordRow]{
			Brokers:     brokers,
			Topic:       persist.TopicRecords,
			Group:       group,
			Decode:      persist.DecodeRecordRow,
			Write:       pool.InsertHTSRecords,
			IdleTimeout: idle,
			Logger:      logger,
		})
		if err != nil {
			return err
		}
		defer consumer.Close()

		runErr := consumer.Run(ctx)
		slog.Info("persister kapatıldı", "mod", mode,
			"yazılan", consumer.Written(), "çözümlenemeyen", consumer.Failed())
		return runErr

	default: // groundtruth
		consumer, err := persist.New(persist.Config[postgres.GroundTruthRow]{
			Brokers:     brokers,
			Topic:       persist.TopicGroundTruth,
			Group:       group,
			Decode:      persist.DecodeGroundTruthRow,
			Write:       pool.InsertGroundTruth,
			IdleTimeout: idle,
			Logger:      logger,
		})
		if err != nil {
			return err
		}
		defer consumer.Close()

		runErr := consumer.Run(ctx)
		slog.Info("persister kapatıldı", "mod", mode,
			"yazılan", consumer.Written(), "çözümlenemeyen", consumer.Failed())
		return runErr
	}
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
