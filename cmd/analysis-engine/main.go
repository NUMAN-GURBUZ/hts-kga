// cmd/analysis-engine — S2 Analiz Motoru giriş noktası (T-E03-01..13).
//
// Zincir:
//
//	senaryo config → Redis envanteri → kütle ızgarası → Kafka tüketicisi
//	  → core.Estimate → core.Shapes → PostgreSQL `estimates`
//
// Servis `hts.groundtruth` topic'ine **abone olmaz** ve `ground_truth`
// tablosuna sorgu atmaz (kör test, K6). Kafka ACL'i ve PostgreSQL rolü zaten
// reddederdi; kodda denemenin hiç bulunmaması tasarımın açık ifadesidir.
//
// Yapılandırma ortam değişkenlerinden okunur (.env):
//
//	HTS_CONFIG      senaryo YAML yolu (zorunlu)
//	HTS_RUN_ID      işlenecek koşunun kimliği (zorunlu — ADR-05)
//	HTS_LAMBDA      kalibrasyon parametresi (varsayılan 1.0; S5'te λ* gelir)
//	HTS_SAMPLE_MODE validation | calibration | full (varsayılan validation)
//	POSTGRES_*      bağlantı bilgileri
//	KAFKA_BROKERS   virgülle ayrılmış broker listesi
//	REDIS_ADDR      Redis adresi
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/core"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/driver"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/sampling"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/observability"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/observability/health"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

const (
	serviceName    = "hts-analysis-engine"
	serviceVersion = "0.4.0-sprint4"
	healthAddr     = ":8082"
	consumerGroup  = "hts-analysis-engine"
)

func main() {
	if err := run(); err != nil {
		slog.Error("analysis-engine durdu", "hata", err)
		os.Exit(1)
	}
}

func run() error {
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

	scn, runID, lambda, err := loadSettings()
	if err != nil {
		return err
	}

	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// ─── Envanter (Redis) ────────────────────────────────────────────────────
	rdb, err := redis.NewClient(initCtx, envOr("REDIS_ADDR", "localhost:6379"))
	if err != nil {
		return fmt.Errorf("Redis bağlantısı: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	inventory, err := params.Load(initCtx, rdb, runID, scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		return fmt.Errorf("envanter yüklenemedi (koşu %s): %w", runID, err)
	}

	// ─── Izgara ve motor ─────────────────────────────────────────────────────
	grid, err := density.NewGrid(scn.Analysis.GridResolutionM)
	if err != nil {
		return fmt.Errorf("ızgara kurulamadı: %w", err)
	}

	var tech ta.Technology
	if scn.TimingAdvance.Enabled {
		if tech, err = ta.Parse(scn.TimingAdvance.Technology); err != nil {
			return fmt.Errorf("TA teknolojisi çözülemedi: %w", err)
		}
	}

	engine, err := driver.NewEngine(driver.EngineConfig{
		Inventory: inventory,
		Grid:      grid,
		Options: core.DefaultOptions(density.Config{
			RxSensitivityDBm: scn.Network.RxSensitivityDBm,
			SigmaNominalDB:   scn.Radio.ShadowingSigmaDB,
			Lambda:           lambda,
			UTHeightM:        rf.UTHeightM,
		}, scn.Analysis.NeighborMaxCount),
		Technology: tech,
		TAEnabled:  scn.TimingAdvance.Enabled,
	})
	if err != nil {
		return fmt.Errorf("motor kurulamadı: %w", err)
	}

	// ─── Yazıcı (PostgreSQL) ─────────────────────────────────────────────────
	pool, err := postgres.NewPool(initCtx, dsnFromEnv())
	if err != nil {
		return fmt.Errorf("PostgreSQL bağlantısı: %w", err)
	}
	defer pool.Close()

	persister, err := driver.NewPersister(driver.PersisterConfig{
		Inventory: inventory,
		Grid:      grid,
		Levels:    scn.Analysis.ContourLevels,
		Sink:      pool,
	})
	if err != nil {
		return fmt.Errorf("tahmin yazıcısı kurulamadı: %w", err)
	}

	// ─── Örnekleme politikası (ADR-14, ADR-24) ───────────────────────────────
	status, err := pool.RunStatusOf(initCtx, runID)
	if err != nil {
		return fmt.Errorf("koşu durumu okunamadı: %w", err)
	}
	sampler, err := buildSampler(scn, status)
	if err != nil {
		return err
	}

	// ─── Tüketici ────────────────────────────────────────────────────────────
	consumer, err := driver.NewConsumer(driver.ConsumerConfig{
		Brokers: strings.Split(envOr("KAFKA_BROKERS", "localhost:9092"), ","),
		Group:   consumerGroup,
		Engine:  engine,
		Handler: persister,
		Sampler: sampler,
		Logger:  logger,
	})
	if err != nil {
		return fmt.Errorf("tüketici kurulamadı: %w", err)
	}
	defer consumer.Close()

	slog.Info("analysis-engine başlatıldı",
		"health", healthAddr, "run_id", runID, "senaryo", scn.Run.Scenario,
		"hücre", inventory.Len(), "çözünürlük_m", scn.Analysis.GridResolutionM,
		"lambda", lambda, "örnekleme", sampler.Mode())

	runErr := consumer.Run(ctx)

	// Kapanışta tampon boşaltılır: yarım kalan parti kaybolmaz.
	flushCtx, flushCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer flushCancel()
	if err := persister.Flush(flushCtx); err != nil {
		slog.Error("kapanışta tampon boşaltılamadı", "hata", err)
	}

	// Analiz sayacı bütünlük denetiminin girdisidir (ADR-23 denetim 4).
	analyzed, skipped := consumer.Stats()
	if err := pool.SetAnalyzedEvents(flushCtx, runID, analyzed); err != nil {
		slog.Error("analiz sayacı yazılamadı", "hata", err)
	}

	events, rows := persister.Stats()
	slog.Info("analysis-engine kapatıldı",
		"işlenen_olay", events, "tahmin_satırı", rows,
		"örneklemde", analyzed, "elenen", skipped)
	return runErr
}

// buildSampler, örnekleme politikasını senaryo config'inden kurar (ADR-14).
//
// Kalibrasyon modunda seyreltme oranı için koşunun **gerçek** olay hacmi
// gerekir; bu sayı simülatör tarafından `run_config.published_events`'e
// yazılmıştır (ADR-23). Tahmin yerine ölçülmüş değeri kullanmak, örneklem
// büyüklüğünü Poisson dalgalanmasından bağımsız kılar.
//
// Hacmi simülatör paketinden türetmek de mümkündü (ajan × gün × günlük hedef)
// ama bu, analiz ikilisinin `internal/simulator`'a bağımlı olması demekti —
// ADR-20 bunu açıkça yasaklıyor.
func buildSampler(scn *config.Scenario, status postgres.RunStatus) (sampling.Policy, error) {
	mode, err := sampling.ParseMode(envOr("HTS_SAMPLE_MODE", string(sampling.ModeValidation)))
	if err != nil {
		return sampling.Policy{}, err
	}

	cfg := sampling.Config{
		Mode:         mode,
		Seed:         scn.Run.Seed,
		SplitRatio:   scn.Calibration.SplitRatio,
		TargetEvents: scn.Analysis.Sample.CalibrationEvents,
	}

	if mode == sampling.ModeCalibration {
		if status.PublishedEvents == nil {
			return sampling.Policy{}, fmt.Errorf(
				"kalibrasyon modu koşunun bitmiş olmasını gerektirir: " +
					"run_config.published_events boş (önce simülasyonu tamamlayın)")
		}
		cfg.ExpectedTotalEvents = int(*status.PublishedEvents)
	}

	sampler, err := sampling.New(cfg)
	if err != nil {
		return sampling.Policy{}, fmt.Errorf("örnekleme politikası: %w", err)
	}
	return sampler, nil
}

// loadSettings, senaryo config'ini ve koşu parametrelerini okur.
func loadSettings() (*config.Scenario, uuid.UUID, float64, error) {
	path := os.Getenv("HTS_CONFIG")
	if path == "" {
		return nil, uuid.Nil, 0, fmt.Errorf("HTS_CONFIG tanımlı değil (senaryo YAML yolu)")
	}
	scn, err := config.Load(path)
	if err != nil {
		return nil, uuid.Nil, 0, fmt.Errorf("senaryo config'i okunamadı: %w", err)
	}

	raw := os.Getenv("HTS_RUN_ID")
	if raw == "" {
		return nil, uuid.Nil, 0, fmt.Errorf("HTS_RUN_ID tanımlı değil (ADR-05)")
	}
	runID, err := uuid.Parse(raw)
	if err != nil {
		return nil, uuid.Nil, 0, fmt.Errorf("HTS_RUN_ID geçersiz (%q): %w", raw, err)
	}

	// λ varsayılanı 1.0'dır: kalibrasyon öncesinde nominal σ kullanılır (ADR-02).
	lambda := 1.0
	if v := os.Getenv("HTS_LAMBDA"); v != "" {
		if lambda, err = strconv.ParseFloat(v, 64); err != nil {
			return nil, uuid.Nil, 0, fmt.Errorf("HTS_LAMBDA geçersiz (%q): %w", v, err)
		}
	}
	return scn, runID, lambda, nil
}

// dsnFromEnv, PostgreSQL bağlantı dizesini ortam değişkenlerinden kurar.
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
