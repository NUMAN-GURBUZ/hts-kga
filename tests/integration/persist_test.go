// G2 · T-E04-01 — Kalıcılaştırıcıların entegrasyon testi.
//
// Zincir: simülatör → Kafka (iki topic) → iki persister → PostgreSQL.
//
// Test ayrıca kör testin (K6) 2. katmanını regresyona bağlar: `svc_analysis`
// kimliğiyle `ground_truth` okumaya çalışmak **reddedilmelidir**. Bu satır
// silinirse kör test sessizce delinebilirdi.
package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/persist"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/event"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/inventory"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/run"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/kafka"
)

// persistTestSalt, HMAC takma adı için 32 baytlık test tuzudur.
var persistTestSalt = []byte("hts-kga-test-salt-0123456789abcd")

// simulateSmall, küçük bir koşu yürütür ve Kafka'ya yayınlar.
//
// Dönen değerler: koşu kimliği, yayınlanan kayıt ve ground truth sayısı.
func simulateSmall(t *testing.T, pool *postgres.Pool, rdb cacheWriter,
	configFile string, agents, days int) (uuid.UUID, run.Stats) {
	t.Helper()
	ctx := context.Background()

	scn, err := config.Load(configsDir + "/" + configFile)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	scn.Simulation.Agents = agents
	scn.Simulation.DurationDays = days

	proj, err := geo.NewProjector(scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}

	runID := uuid.New()
	inv, err := inventory.Build(runID, scn, proj)
	if err != nil {
		t.Fatalf("inventory.Build: %v", err)
	}
	store := inventory.Store{DB: pool, Cache: rdb}
	if err := store.Persist(ctx, inv, scn, postgres.GitSHA()); err != nil {
		t.Fatalf("envanter yazılamadı: %v", err)
	}

	pseudo, err := event.NewPseudonymizer(persistTestSalt)
	if err != nil {
		t.Fatalf("NewPseudonymizer: %v", err)
	}
	producer, err := kafka.NewProducer([]string{os.Getenv(envKafkaBrokers)})
	if err != nil {
		t.Fatalf("kafka.NewProducer: %v", err)
	}
	defer producer.Close()

	runner, err := run.New(run.Config{
		RunID:         runID,
		Scenario:      scn,
		Inventory:     inv,
		Projector:     proj,
		Pseudonymizer: pseudo,
		Sink:          producer,
		RunStart:      time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("run.New: %v", err)
	}

	stats, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("koşu: %v", err)
	}
	if err := pool.CompleteRun(ctx, runID, stats.PublishedRecords); err != nil {
		t.Fatalf("CompleteRun: %v", err)
	}
	return runID, stats
}

// cacheWriter, envanter deposunun Redis arayüzüdür (test kolaylığı).
type cacheWriter = inventory.CacheWriter

// drainRecords, `hts.records` topic'ini boşaltıp veritabanına yazar.
func drainRecords(t *testing.T, pool *postgres.Pool, group string, want int64, runID uuid.UUID) int64 {
	t.Helper()

	consumer, err := persist.New(persist.Config[postgres.HTSRecordRow]{
		Brokers: []string{os.Getenv(envKafkaBrokers)},
		Topic:   persist.TopicRecords,
		Group:   group,
		Decode:  persist.DecodeRecordRow,
		Write:   pool.InsertHTSRecords,
	})
	if err != nil {
		t.Fatalf("persist.New (records): %v", err)
	}
	defer consumer.Close()

	return drainUntil(t, consumer.Run, func() int64 {
		n, err := pool.CountHTSRecords(context.Background(), runID)
		if err != nil {
			t.Fatalf("CountHTSRecords: %v", err)
		}
		return n
	}, want)
}

// drainGroundTruth, `hts.groundtruth` topic'ini boşaltıp veritabanına yazar.
func drainGroundTruth(t *testing.T, pool *postgres.Pool, group string, want int64, runID uuid.UUID) int64 {
	t.Helper()

	consumer, err := persist.New(persist.Config[postgres.GroundTruthRow]{
		Brokers: []string{os.Getenv(envKafkaBrokers)},
		Topic:   persist.TopicGroundTruth,
		Group:   group,
		Decode:  persist.DecodeGroundTruthRow,
		Write:   pool.InsertGroundTruth,
	})
	if err != nil {
		t.Fatalf("persist.New (groundtruth): %v", err)
	}
	defer consumer.Close()

	return drainUntil(t, consumer.Run, func() int64 {
		n, err := pool.CountGroundTruth(context.Background(), runID)
		if err != nil {
			t.Fatalf("CountGroundTruth: %v", err)
		}
		return n
	}, want)
}

// drainUntil, tüketiciyi hedef satır sayısına ulaşana kadar çalıştırır.
func drainUntil(t *testing.T, runFn func(context.Context) error, count func() int64, want int64) int64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- runFn(ctx) }()

	deadline := time.Now().Add(2 * time.Minute)
	var got int64
	for time.Now().Before(deadline) {
		if got = count(); got >= want {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("tüketici hatası: %v", err)
	}
	return count()
}

// TestPersisters_EndToEnd, iki kalıcılaştırıcının koşuyu eksiksiz yazdığını
// ve bütünlük denetiminin temiz geçtiğini doğrular.
func TestPersisters_EndToEnd(t *testing.T) {
	pool, rdb := requireInfra(t)
	requireKafka(t)
	admin := adminPool(t)
	ctx := context.Background()

	runID, stats := simulateSmall(t, pool, rdb, "urban_ta.yaml", 40, 1)
	t.Cleanup(func() {
		purgeRun(t, admin, runID)
		if err := pool.DeleteRun(ctx, runID); err != nil {
			t.Errorf("temizlik (postgres): %v", err)
		}
		if _, err := rdb.DeleteRunKeys(ctx, runID); err != nil {
			t.Errorf("temizlik (redis): %v", err)
		}
	})

	t.Logf("koşu %s: %d olay, %d kayıt, %d ground truth",
		runID, stats.Events, stats.PublishedRecords, stats.PublishedTruths)

	suffix := runID.String()[:8]
	records := drainRecords(t, pool, "test-records-"+suffix, stats.PublishedRecords, runID)
	truths := drainGroundTruth(t, pool, "test-gt-"+suffix, stats.PublishedTruths, runID)

	if records != stats.PublishedRecords {
		t.Errorf("hts_records %d satır, %d yayınlanmıştı", records, stats.PublishedRecords)
	}
	if truths != stats.PublishedTruths {
		t.Errorf("ground_truth %d satır, %d yayınlanmıştı", truths, stats.PublishedTruths)
	}

	// ─── Bütünlük denetimi (ADR-01, ADR-23) ─────────────────────────────────
	checkIntegrity(t, admin, runID)

	// ─── Kural 4: kayıtsız ground truth beklenen bir durumdur ───────────────
	var deleted int64
	err := admin.QueryRow(ctx, `
        SELECT count(*) FROM ground_truth
         WHERE run_id = $1::uuid AND injected_rule = 4`, runID.String()).Scan(&deleted)
	if err != nil {
		t.Fatalf("kural 4 sorgusu: %v", err)
	}
	if truths-records != deleted+int64(stats.Uncovered) {
		t.Errorf("kayıtsız GT %d, kural4(%d) + kapsama dışı(%d) beklenir",
			truths-records, deleted, stats.Uncovered)
	}
	t.Logf("kural 4 ile silinen kayıt: %d · kapsama dışı: %d", deleted, stats.Uncovered)

	// ─── Takma ad sızıntısı olmamalı (E-08) ──────────────────────────────────
	//
	// Takma ad HMAC-SHA256'nın 64 karakterlik onaltılık gösterimidir. Ham
	// MSISDN biçimi "9050" + 8 hanedir (12 rakam); ham bir değer sızsaydı
	// onaltılık desene uymazdı.
	var leaked int64
	err = admin.QueryRow(ctx, `
        SELECT count(*) FROM hts_records
         WHERE run_id = $1::uuid
           AND (pseudo_msisdn !~ '^[0-9a-f]{64}$' OR pseudo_imei !~ '^[0-9a-f]{64}$')`,
		runID.String()).Scan(&leaked)
	if err != nil {
		t.Fatalf("takma ad sorgusu: %v", err)
	}
	if leaked > 0 {
		t.Errorf("%d kayıtta takma ad HMAC deseninde değil — ham değer sızmış olabilir", leaked)
	}

	// Aynı abonenin kayıtları aynı takma adı taşımalı (S4 hız/yörünge
	// kurallarının sıra görebilmesi buna bağlıdır).
	var distinctPseudos int64
	err = admin.QueryRow(ctx, `
        SELECT count(DISTINCT pseudo_msisdn) FROM hts_records WHERE run_id = $1::uuid`,
		runID.String()).Scan(&distinctPseudos)
	if err != nil {
		t.Fatalf("takma ad sayımı: %v", err)
	}
	if distinctPseudos > 40 {
		t.Errorf("%d farklı takma ad, en çok 40 ajan var — takma ad deterministik değil",
			distinctPseudos)
	}
	t.Logf("takma ad: %d farklı değer (40 ajan)", distinctPseudos)
}

// TestPersisters_Idempotent, aynı akışın yeniden tüketilmesinin satır
// yinelemediğini doğrular (at-least-once + ON CONFLICT DO NOTHING).
func TestPersisters_Idempotent(t *testing.T) {
	pool, rdb := requireInfra(t)
	requireKafka(t)
	admin := adminPool(t)
	ctx := context.Background()

	runID, stats := simulateSmall(t, pool, rdb, "urban_no_ta.yaml", 20, 1)
	t.Cleanup(func() {
		purgeRun(t, admin, runID)
		if err := pool.DeleteRun(ctx, runID); err != nil {
			t.Errorf("temizlik (postgres): %v", err)
		}
		if _, err := rdb.DeleteRunKeys(ctx, runID); err != nil {
			t.Errorf("temizlik (redis): %v", err)
		}
	})

	suffix := runID.String()[:8]
	first := drainRecords(t, pool, "test-idem-a-"+suffix, stats.PublishedRecords, runID)

	// Farklı grup: akış baştan tüketilir, satırlar yeniden yazılmaya çalışılır.
	second := drainRecords(t, pool, "test-idem-b-"+suffix, stats.PublishedRecords, runID)

	if first != second {
		t.Errorf("ikinci tüketimde satır sayısı %d → %d değişti (yineleme)", first, second)
	}
	t.Logf("iki kez tüketildi, satır sayısı sabit: %d", second)
}

// TestBlindTest_AnalysisCannotReadGroundTruth, kör testin 2. katmanını
// regresyona bağlar (K6).
//
// `svc_analysis` rolü `ground_truth` tablosunu **görmemelidir**. Bu test
// başarısız olursa çalışmanın ana iddiası (analiz gerçek konumu bilmez)
// veritabanı düzeyinde delinmiş demektir.
func TestBlindTest_AnalysisCannotReadGroundTruth(t *testing.T) {
	requireInfra(t)
	ctx := context.Background()

	dsn := analysisDSN(t)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("svc_analysis bağlantısı: %v", err)
	}
	defer pool.Close()

	var n int64
	err = pool.QueryRow(ctx, `SELECT count(*) FROM ground_truth`).Scan(&n)
	if err == nil {
		t.Fatal("svc_analysis ground_truth tablosunu okuyabildi — kör test (K6) delinmiş")
	}
	t.Logf("beklenen ret: %v", err)

	// Aynı rol `hts_records` ve `estimates`'i okuyabilmelidir; aksi hâlde
	// yetkilendirme fazla dar demektir ve analiz servisi çalışamaz.
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hts_records`).Scan(&n); err != nil {
		t.Errorf("svc_analysis hts_records okuyamadı: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM estimates`).Scan(&n); err != nil {
		t.Errorf("svc_analysis estimates okuyamadı: %v", err)
	}
}

// analysisDSN, svc_analysis rolünün bağlantı dizesini kurar.
//
// Parola 004_roles.sql'de tanımlı geliştirme parolasıdır; üretimde ortam
// değişkeninden gelir.
func analysisDSN(t *testing.T) string {
	t.Helper()

	base := os.Getenv(envPGDSN)
	if base == "" {
		t.Skipf("altyapı testi atlandı: %s tanımlı değil", envPGDSN)
	}
	db := "hts_kga"
	if v := os.Getenv("POSTGRES_DB"); v != "" {
		db = v
	}
	return fmt.Sprintf("postgres://svc_analysis:analysis_dev_pw@localhost:5432/%s?sslmode=disable", db)
}
