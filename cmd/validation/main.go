// cmd/validation — S3b Doğrulama toplu işi (T-E04-02, ADR-04).
//
// Uzun ömürlü bir servis **değildir**: bir koşuyu doğrular ve çıkar
// (`make validate RUN_ID=...`). Doğrulama gerçek zamanlı olmak zorunda
// değildir; koşu bittikten sonra bir kez çalışır.
//
// Ortam değişkenleri:
//
//	HTS_CONFIG        senaryo YAML yolu (zorunlu — senaryo harfi ve λ aralığı)
//	HTS_RUN_ID        doğrulanacak koşu (zorunlu)
//	HTS_CALIBRATE     "true" ise λ kalibrasyonu çalıştırılır (ADR-02)
//	HTS_RECOMPUTE     "true" ise koşunun mevcut ölçümleri silinip yeniden yazılır
//	HTS_CALIB_METRICS "true" ise 'C' kümesi için de ölçüm yazılır (bilgi amaçlı)
//	POSTGRES_* · REDIS_ADDR
//
// Çıkış kodu: önkoşul karşılanmazsa ya da ölçüm başarısız olursa 1.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/observability"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/validation/calibration"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/validation/pipeline"
)

const (
	serviceName    = "hts-validation"
	serviceVersion = "0.5.0-sprint5"
)

func main() {
	if err := execute(); err != nil {
		slog.Error("doğrulama başarısız", "hata", err)
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

	scn, runID, err := loadSettings()
	if err != nil {
		return err
	}

	initCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	pool, err := postgres.NewPool(initCtx, dsnFromEnv())
	if err != nil {
		return fmt.Errorf("PostgreSQL bağlantısı: %w", err)
	}
	defer pool.Close()

	// ─── Kalibrasyon (ADR-02) ────────────────────────────────────────────────
	if envBool("HTS_CALIBRATE") {
		rdb, err := redis.NewClient(initCtx, envOr("REDIS_ADDR", "localhost:6379"))
		if err != nil {
			return fmt.Errorf("Redis bağlantısı: %w", err)
		}
		defer func() { _ = rdb.Close() }()

		runner, err := calibration.New(calibration.Config{
			Pool:      pool,
			Inventory: rdb,
			Scenario:  scn,
			RunID:     runID,
			Logger:    logger,
		})
		if err != nil {
			return err
		}

		result, err := runner.Run(ctx)
		if err != nil {
			return fmt.Errorf("kalibrasyon: %w", err)
		}
		fmt.Println(result.Summary())
	}

	// ─── Ölçüm ───────────────────────────────────────────────────────────────
	pipe, err := pipeline.New(pool, logger)
	if err != nil {
		return err
	}

	report, err := pipe.Run(ctx, pipeline.Options{
		RunID:                     runID,
		Scenario:                  scn.Run.Scenario,
		IncludeCalibrationMetrics: envBool("HTS_CALIB_METRICS"),
		Recompute:                 envBool("HTS_RECOMPUTE"),
	})
	if err != nil {
		return err
	}

	fmt.Print(report.Summary())
	slog.Info("doğrulama tamamlandı",
		"run_id", runID, "senaryo", report.Scenario,
		"satır", len(report.Validation), "süre", report.Duration.Round(time.Millisecond))
	return nil
}

// loadSettings, senaryo config'ini ve koşu kimliğini okur.
func loadSettings() (*config.Scenario, uuid.UUID, error) {
	path := os.Getenv("HTS_CONFIG")
	if path == "" {
		return nil, uuid.Nil, fmt.Errorf("HTS_CONFIG tanımlı değil (senaryo YAML yolu)")
	}
	scn, err := config.Load(path)
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("senaryo config'i okunamadı: %w", err)
	}

	raw := os.Getenv("HTS_RUN_ID")
	if raw == "" {
		return nil, uuid.Nil, fmt.Errorf("HTS_RUN_ID tanımlı değil (ADR-05)")
	}
	runID, err := uuid.Parse(raw)
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("HTS_RUN_ID geçersiz (%q): %w", raw, err)
	}
	return scn, runID, nil
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

func envBool(key string) bool {
	v := os.Getenv(key)
	return v == "1" || v == "true" || v == "TRUE"
}
