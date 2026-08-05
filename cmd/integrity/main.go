// cmd/integrity — S4 Bütünlük Denetimi (E05, ADR-27..31).
//
// Tek ikili, iki **faz**:
//
//	HTS_INTEGRITY_MODE=stream   Kafka hts.records → kural 1, 3  → integrity_findings
//	                            + run_config.inspected_records
//	HTS_INTEGRITY_MODE=batch    hts_records taraması → kural 5, 2 → integrity_findings
//	HTS_INTEGRITY_MODE=all      stream sonra batch (yerel geliştirme kolaylığı)
//
// Fazların ayrılması bir kolaylık değil, ölçüm sonucudur (ADR-27): kural 3'ün
// kanıtı **varış sırasındadır** ve toplu modda yok olur (precision %8);
// kural 5'in kanıtı **kapalı popülasyondadır** ve akışta ilk-kayıt tuzağı
// precision'ı %50'ye düşürür.
//
// # Kör test
//
// Bu servis `hts.groundtruth` topic'ine abone **olmaz** ve `ground_truth`
// tablosunu sorgulamaz. DSN rolü `svc_integrity`'dir: o rolün ground_truth
// üzerinde hiçbir yetkisi yoktur (migration 004). Precision/recall'ü yalnızca
// etiketi görebilen S3b hesaplar (F.5).
//
// # Örnekleme yok
//
// ADR-27/4: kural 2/3/5 abone dizisine bağlıdır ve F.5'in recall denominatörü
// tüm enjekte olaylardır. `sampling.Policy` bu servisin yapılandırmasında
// bilinçli olarak **bulunmaz**.
//
// Ortam değişkenleri:
//
//	HTS_INTEGRITY_MODE  stream | batch | all (zorunlu)
//	HTS_CONFIG          senaryo YAML yolu (zorunlu)
//	HTS_RUN_ID          işlenecek koşu (zorunlu, ADR-05)
//	HTS_GROUP           tüketici grubu; boşsa run_id'den türetilir
//	HTS_IDLE_TIMEOUT    akış boşta kalınca çıkış süresi (örn. "20s")
//	HTS_SKIP_PRECONDITION  toplu faz önkoşulunu atlar — YALNIZCA test
//	POSTGRES_* · KAFKA_BROKERS · REDIS_ADDR
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
	"github.com/NUMAN-GURBUZ/hts-kga/internal/integrity/detector"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/integrity/source"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/observability"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/observability/health"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

const (
	serviceName    = "hts-integrity"
	serviceVersion = "0.6.0-sprint6"
	metricsAddr    = ":2115" // prometheus.yml: hts-integrity hedefi
)

func main() {
	if err := execute(); err != nil {
		slog.Error("bütünlük denetimi durdu", "hata", err)
		os.Exit(1)
	}
}

func execute() error {
	mode := os.Getenv("HTS_INTEGRITY_MODE")
	switch mode {
	case "stream", "batch", "all":
	default:
		return fmt.Errorf("HTS_INTEGRITY_MODE 'stream', 'batch' veya 'all' olmalı (%q)", mode)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfgPath := os.Getenv("HTS_CONFIG")
	if cfgPath == "" {
		return fmt.Errorf("HTS_CONFIG zorunlu (senaryo YAML yolu)")
	}
	scenario, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("senaryo config: %w", err)
	}

	runID, err := uuid.Parse(os.Getenv("HTS_RUN_ID"))
	if err != nil {
		return fmt.Errorf("HTS_RUN_ID geçerli bir UUID olmalı (ADR-05): %w", err)
	}

	prov, err := observability.Init(ctx, observability.Config{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
	})
	if err != nil {
		return fmt.Errorf("OTel başlatılamadı: %w", err)
	}
	defer func() { _ = prov.Shutdown(context.Background()) }()

	h := health.New(serviceName, serviceVersion)
	go h.MustServe(envOr("HTS_HEALTH_ADDR", ":8085"))
	go prov.MustServeMetrics(envOr("HTS_METRICS_ADDR", metricsAddr))

	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	pool, err := postgres.NewPool(initCtx, dsnFromEnv())
	if err != nil {
		return fmt.Errorf("PostgreSQL bağlantısı: %w", err)
	}
	defer pool.Close()

	sink, err := detector.NewPostgresSink(pool, runID)
	if err != nil {
		return err
	}

	detCfg := scenario.Integrity.Detection
	slog.Info("bütünlük denetimi başlatıldı",
		"mod", mode, "run_id", runID, "senaryo", scenario.Run.Scenario,
		"margin_cap", detCfg.VelocityMarginCap)

	if mode == "stream" || mode == "all" {
		if err := runStream(ctx, streamDeps{
			pool: pool, sink: sink, scenario: scenario, runID: runID, logger: logger,
		}); err != nil {
			return err
		}
	}
	if mode == "batch" || mode == "all" {
		if err := runBatch(ctx, batchDeps{
			pool: pool, sink: sink, scenario: scenario, runID: runID, logger: logger,
		}); err != nil {
			return err
		}
	}

	total, canonical, err := pool.CountFindings(context.WithoutCancel(ctx), runID)
	if err != nil {
		return err
	}
	slog.Info("bütünlük denetimi tamamlandı",
		"mod", mode, "bulgu_toplam", total, "bulgu_kanonik", canonical)
	return nil
}

// ─── Akış fazı ────────────────────────────────────────────────────────────────

type streamDeps struct {
	pool     *postgres.Pool
	sink     detector.Sink
	scenario *config.Scenario
	runID    uuid.UUID
	logger   *slog.Logger
}

// runStream, akış fazını koşturur: kural 1 (envanter) ve kural 3 (zaman).
//
// Envanter Redis'ten yüklenir; boşsa koşu **başlamaz** (kural 1 her kaydı bulgu
// sayardı).
func runStream(ctx context.Context, d streamDeps) error {
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	rdb, err := redis.NewClient(initCtx, envOr("REDIS_ADDR", "localhost:6379"))
	if err != nil {
		return fmt.Errorf("Redis bağlantısı: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	inventory, err := source.LoadInventoryWithFallback(initCtx, rdb,
		source.NewPostgresCellSource(d.pool), d.runID,
		d.scenario.Area.OriginLat, d.scenario.Area.OriginLon, d.logger)
	if err != nil {
		return err
	}

	detCfg := d.scenario.Integrity.Detection
	invRule, err := detector.NewInventoryRule(inventory, detCfg.VelocityMarginCap)
	if err != nil {
		return err
	}
	tickLength := time.Duration(d.scenario.Simulation.TickMinutes) * time.Minute
	timeRule, err := detector.NewTimeOrderRule(
		time.Duration(detCfg.TimeBackstepToleranceS*float64(time.Second)),
		tickLength, detCfg.VelocityMarginCap)
	if err != nil {
		return err
	}

	// Akış fazı defteri boş başlar: bu koşunun ilk fazıdır.
	engine, err := detector.NewStreamEngine(detector.Config{
		Claims:    detector.NewClaims(),
		Sink:      d.sink,
		MarginCap: detCfg.VelocityMarginCap,
		Logger:    d.logger,
	}, invRule, timeRule)
	if err != nil {
		return err
	}

	idle, err := idleFromEnv()
	if err != nil {
		return err
	}

	stream, err := source.NewStream(source.StreamConfig{
		Brokers:     strings.Split(envOr("KAFKA_BROKERS", "localhost:9092"), ","),
		Group:       envOr("HTS_GROUP", "hts-integrity-"+d.runID.String()),
		RunID:       d.runID,
		IdleTimeout: idle,
		Logger:      d.logger,
	}, engine)
	if err != nil {
		return err
	}
	defer stream.Close()

	runErr := stream.Run(ctx)
	st := engine.Stats()
	recordStats(context.WithoutCancel(ctx), "stream", st)

	// Tamlık sayacı, akış hata verse bile yazılır: yarım koşu FAIL olarak
	// görünmelidir, hiç yazılmaması SKIP verir ve sorunu gizler (ADR-31/7).
	writeCtx, writeCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer writeCancel()
	if err := d.pool.SetInspectedRecords(writeCtx, d.runID, st.Inspected); err != nil {
		if runErr != nil {
			return fmt.Errorf("%w (ayrıca inspected_records yazılamadı: %v)", runErr, err)
		}
		return err
	}

	slog.Info("akış fazı tamamlandı",
		"incelenen", st.Inspected, "kanonik", st.Canonical, "bastırılmış", st.Suppressed,
		"geçersiz", st.Invalid, "yabancı_koşu", stream.Foreign(), "çözümlenemeyen", stream.Failed(),
		"izlenen_abone", timeRule.Subscribers())
	if st.Invalid > 0 {
		slog.Error("geçerlilik denetiminden düşen isabet var — bulgular eksik",
			"adet", st.Invalid)
	}
	return runErr
}

// ─── Toplu faz ────────────────────────────────────────────────────────────────

type batchDeps struct {
	pool     *postgres.Pool
	sink     detector.Sink
	scenario *config.Scenario
	runID    uuid.UUID
	logger   *slog.Logger
}

// batchRuleSet, toplu faz kurallarını kurar.
//
// Envanter kural 2 için gereklidir (hücre konumları). Kural 1 kayıtları
// zincirden düşer: konumu bilinmeyen hücre `Position` çağrısında bulunamaz
// (ADR-29/4).
func batchRuleSet(inv detector.CellPositions, scn *config.Scenario) ([]detector.BatchRule, error) {
	detCfg := scn.Integrity.Detection

	activity, err := detector.NewActivityRule(detCfg.ActivityMinSupport, detCfg.VelocityMarginCap)
	if err != nil {
		return nil, err
	}
	velocity, err := detector.NewVelocityRule(inv, scn.Integrity.MaxVelocityKMH, detCfg.VelocityMarginCap)
	if err != nil {
		return nil, err
	}
	// Sıra motorda öncelikle yeniden dizilir; burada okunabilirlik için
	// öncelik sırasında verilir (kural 5 → kural 2).
	return []detector.BatchRule{activity, velocity}, nil
}

// runBatch, toplu fazı koşturur: kural 5 (aktivite) ve kural 2 (hız).
//
// Kural 2, ADR-29 gereği atıf mekanizmasıyla **birlikte** devreye alınır:
// atıfsız kural precision %36 üretir ve K7 raporuna böyle bir satır girmesi
// kuralı hiç yazmamaktan kötüdür (ADR-29/8, atomiklik).
func runBatch(ctx context.Context, d batchDeps) error {
	// Talep defteri veritabanından yüklenir (ADR-28/6): toplu faz bağımsız
	// yeniden koşulabilir olmalıdır.
	loadCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	claims, err := detector.LoadClaims(loadCtx, d.pool, d.runID)
	if err != nil {
		return err
	}
	slog.Info("talep defteri yüklendi", "talep", claims.Len())

	// Envanter kural 2 için gerekli (hücre konumları).
	rdb, err := redis.NewClient(loadCtx, envOr("REDIS_ADDR", "localhost:6379"))
	if err != nil {
		return fmt.Errorf("Redis bağlantısı: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	inventory, err := source.LoadInventoryWithFallback(loadCtx, rdb,
		source.NewPostgresCellSource(d.pool), d.runID,
		d.scenario.Area.OriginLat, d.scenario.Area.OriginLon, d.logger)
	if err != nil {
		return err
	}

	detCfg := d.scenario.Integrity.Detection
	rules, err := batchRuleSet(inventory, d.scenario)
	if err != nil {
		return err
	}

	engine, err := detector.NewBatchEngine(detector.Config{
		Claims:    claims,
		Sink:      d.sink,
		MarginCap: detCfg.VelocityMarginCap,
		Logger:    d.logger,
	}, rules...)
	if err != nil {
		return err
	}

	batch, err := source.NewBatch(source.BatchConfig{
		RunID:            d.runID,
		SkipPrecondition: os.Getenv("HTS_SKIP_PRECONDITION") == "true",
		Logger:           d.logger,
	}, d.pool, engine)
	if err != nil {
		return err
	}

	if err := batch.Run(ctx); err != nil {
		return err
	}

	st := engine.Stats()
	recordStats(context.WithoutCancel(ctx), "batch", st)
	if st.Invalid > 0 {
		slog.Error("geçerlilik denetiminden düşen isabet var — bulgular eksik", "adet", st.Invalid)
	}
	if st.Demoted > 0 {
		slog.Warn("daha güçlü isabet önceki talep nedeniyle bastırıldı (ADR-28/7)",
			"adet", st.Demoted)
	}
	slog.Info("toplu faz kuralları", "kurallar", integrityrule.InClass(integrityrule.ClassBatch))
	return nil
}

// ─── Ortam ────────────────────────────────────────────────────────────────────

func idleFromEnv() (time.Duration, error) {
	v := os.Getenv("HTS_IDLE_TIMEOUT")
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("HTS_IDLE_TIMEOUT geçersiz (%q): %w", v, err)
	}
	return d, nil
}

// dsnFromEnv, `svc_integrity` rolüyle bağlanır.
//
// Rol seçimi kritiktir: `hts_admin` ile bağlanılsaydı K6 katman 2
// (PostgreSQL rol izolasyonu) üretim yolunda **hiç sınanmamış** olurdu.
// `svc_integrity`'nin ground_truth üzerinde yetkisi yoktur; bu servis o tabloyu
// sorgulamaya çalışırsa veritabanı reddeder.
func dsnFromEnv() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		envOr("HTS_INTEGRITY_DB_USER", "svc_integrity"),
		envOr("HTS_INTEGRITY_DB_PASSWORD", "integrity_dev_pw"),
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
