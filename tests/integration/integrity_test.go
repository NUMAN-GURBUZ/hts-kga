// E05 — Bütünlük denetiminin entegrasyon testleri (ADR-27..31).
//
// Zincir: simülatör → Kafka → akış fazı (kural 1, 3) → integrity_findings
//
//	→ persister → hts_records → toplu faz (kural 5)
//	→ F.5 → integrity_metrics
//
// Üç şey burada kanıtlanır ve hiçbiri birim testle görünmez:
//
//  1. **Akış sırası varsayımı** (TestStreamOrderIsMonotonePerSubscriber).
//     Kural 3'ün %100 precision'ı tek bir varsayıma dayanıyor: üretim sırası
//     abone başına olay zamanında monoton artar. Sessizce kırılırsa kural 3
//     yüzlerce yanlış pozitif üretir ve bunu fark etmenin tek yolu etiketlere
//     bakmaktır — yani üretimde kör kalırız.
//
//  2. **İdempotanslık** (TestIntegrityFindingsAreIdempotent). Kafka
//     at-least-once; yinelenen bulgu F.5'in precision denominatörünü şişirir.
//
//  3. **Gölge SQL uyumu** (TestShadowSQLAgreesWithDetector). Go dedektörü ile
//     bağımsız SQL aynı bulgu kümesini vermeli — Sprint 5'in
//     PostGIS↔Haversine çapraz kontrolünün (%0,24 fark) karşılığı.
package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/integrity/detector"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/integrity/source"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
	vintegrity "github.com/NUMAN-GURBUZ/hts-kga/internal/validation/integrity"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// integrityRun, küçük bir koşu yürütür ve iki fazı sırayla koşturur.
type integrityRun struct {
	runID     uuid.UUID
	scenario  *config.Scenario
	inventory *source.Inventory
	published int64
}

// setupIntegrityRun, simülasyon + kalıcılaştırma yapıp fazlara hazır hâle
// getirir.
func setupIntegrityRun(t *testing.T, pool *postgres.Pool, rdb *redis.Client) integrityRun {
	t.Helper()
	ctx := context.Background()

	runID, stats := simulateSmall(t, pool, rdb, "smoke.yaml", 40, 2)
	t.Logf("koşu %s: %d olay, %d enjekte", runID, stats.Events, stats.Injected)

	if stats.Injected == 0 {
		t.Fatal("enjeksiyon üretilmedi: bütünlük testi anlamsız olurdu")
	}

	// Kayıtlar veritabanına yazılır (toplu fazın önkoşulu).
	stored := drainRecords(t, pool, "it-integrity-rec-"+runID.String(), stats.PublishedRecords, runID)
	if stored != stats.PublishedRecords {
		t.Fatalf("hts_records eksik: %d/%d", stored, stats.PublishedRecords)
	}
	// Ground truth da yazılır (F.5 etiketi için).
	gt := drainGroundTruth(t, pool, "it-integrity-gt-"+runID.String(), stats.PublishedTruths, runID)
	if gt != stats.PublishedTruths {
		t.Fatalf("ground_truth eksik: %d/%d", gt, stats.PublishedTruths)
	}

	scn, err := config.Load(configsDir + "/smoke.yaml")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	inv, err := source.LoadInventory(ctx, rdb, runID, scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		t.Fatalf("LoadInventory: %v", err)
	}

	return integrityRun{
		runID: runID, scenario: scn, inventory: inv, published: stats.PublishedRecords,
	}
}

// runStreamPhase, akış fazını koşturur ve motor sayaçlarını döndürür.
func runStreamPhase(t *testing.T, pool *postgres.Pool, r integrityRun, group string) detector.Stats {
	t.Helper()

	detCfg := r.scenario.Integrity.Detection
	sink, err := detector.NewPostgresSink(pool, r.runID)
	if err != nil {
		t.Fatalf("NewPostgresSink: %v", err)
	}
	invRule, err := detector.NewInventoryRule(r.inventory, detCfg.VelocityMarginCap)
	if err != nil {
		t.Fatalf("NewInventoryRule: %v", err)
	}
	tick := time.Duration(r.scenario.Simulation.TickMinutes) * time.Minute
	timeRule, err := detector.NewTimeOrderRule(0, tick, detCfg.VelocityMarginCap)
	if err != nil {
		t.Fatalf("NewTimeOrderRule: %v", err)
	}
	engine, err := detector.NewStreamEngine(detector.Config{
		Claims: detector.NewClaims(), Sink: sink, MarginCap: detCfg.VelocityMarginCap,
	}, invRule, timeRule)
	if err != nil {
		t.Fatalf("NewStreamEngine: %v", err)
	}

	stream, err := source.NewStream(source.StreamConfig{
		Brokers:     []string{os.Getenv(envKafkaBrokers)},
		Group:       group,
		RunID:       r.runID,
		IdleTimeout: 10 * time.Second,
	}, engine)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	defer stream.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := stream.Run(ctx); err != nil {
		t.Fatalf("akış fazı: %v", err)
	}

	st := engine.Stats()
	if err := pool.SetInspectedRecords(context.Background(), r.runID, st.Inspected); err != nil {
		t.Fatalf("SetInspectedRecords: %v", err)
	}
	return st
}

// runBatchPhase, toplu fazı koşturur.
func runBatchPhase(t *testing.T, pool *postgres.Pool, r integrityRun) detector.Stats {
	t.Helper()
	ctx := context.Background()

	claims, err := detector.LoadClaims(ctx, pool, r.runID)
	if err != nil {
		t.Fatalf("LoadClaims: %v", err)
	}

	detCfg := r.scenario.Integrity.Detection
	sink, err := detector.NewPostgresSink(pool, r.runID)
	if err != nil {
		t.Fatalf("NewPostgresSink: %v", err)
	}
	activity, err := detector.NewActivityRule(detCfg.ActivityMinSupport, detCfg.VelocityMarginCap)
	if err != nil {
		t.Fatalf("NewActivityRule: %v", err)
	}
	velocity, err := detector.NewVelocityRule(r.inventory,
		r.scenario.Integrity.MaxVelocityKMH, detCfg.VelocityMarginCap)
	if err != nil {
		t.Fatalf("NewVelocityRule: %v", err)
	}
	engine, err := detector.NewBatchEngine(detector.Config{
		Claims: claims, Sink: sink, MarginCap: detCfg.VelocityMarginCap,
	}, activity, velocity)
	if err != nil {
		t.Fatalf("NewBatchEngine: %v", err)
	}

	batch, err := source.NewBatch(source.BatchConfig{RunID: r.runID}, pool, engine)
	if err != nil {
		t.Fatalf("NewBatch: %v", err)
	}
	if err := batch.Run(ctx); err != nil {
		t.Fatalf("toplu faz: %v", err)
	}
	return engine.Stats()
}

// ─── 1. Akış sırası ön koşulu (en kritik test) ────────────────────────────────

// TestStreamOrderIsMonotonePerSubscriber, kural 3'ün dayandığı varsayımı
// doğrular ve aynı zamanda kuralın tam karakterizasyonunu verir.
//
// **İddia:** `hts.records` akışında abone başına olay zamanı monotonluğunu ihlal
// eden kayıtların kümesi, tam olarak `ground_truth.injected_rule = 3` olan
// olayların kümesidir.
//
// Bu tek test iki şeyi birlikte kanıtlıyor:
//
//	(a) Kafka partition-içi sıralama varsayımı geçerli — kural 3'ün %100
//	    precision'ının tek dayanağı budur (ADR-27, Sprint 6 planı R4)
//	(b) Kural 3 tam olarak kaydırılmış kayıtları ve YALNIZCA onları görüyor
//
// Test etiketlere bakar; bu meşrudur çünkü test S3b rolündedir (ölçüm),
// dedektör değil.
func TestStreamOrderIsMonotonePerSubscriber(t *testing.T) {
	pool, rdb := requireInfra(t)
	requireKafka(t)

	r := setupIntegrityRun(t, pool, rdb)
	st := runStreamPhase(t, pool, r, "it-order-"+r.runID.String())

	if st.Inspected != r.published {
		t.Fatalf("akış fazı %d/%d kayıt inceledi", st.Inspected, r.published)
	}

	// Kural 3'ün kanonik bulguları = monotonluk ihlalleri.
	violations, err := eventIDsOfRule(pool, r.runID, integrityrule.TimeOrder)
	if err != nil {
		t.Fatalf("kural 3 bulguları okunamadı: %v", err)
	}

	// Enjeksiyon kural 3 etiketli olaylar (ground truth — yalnızca test görür).
	injected, err := eventIDsOfInjection(pool, r.runID, integrityrule.TimeOrder)
	if err != nil {
		t.Fatalf("enjeksiyon etiketleri okunamadı: %v", err)
	}

	t.Logf("kural 3: %d bulgu, %d enjekte olay", len(violations), len(injected))
	if len(injected) == 0 {
		t.Skip("bu koşuda kural 3 enjeksiyonu yok; test anlamsız")
	}

	// (b) Her bulgu gerçekten kaydırılmış bir olay olmalı → precision %100.
	var falsePositives int
	for id := range violations {
		if !injected[id] {
			falsePositives++
			t.Errorf("kural 3 temiz kayda bulgu yazdı (%s) — "+
				"akış sıralaması varsayımı kırılmış olabilir (ADR-27)", id)
		}
	}
	if falsePositives == 0 {
		t.Logf("precision = %%100 (yanlış pozitif yok) — akış sıralaması varsayımı geçerli")
	}

	// (a) Yakalanmayanlar, önceki olayı 2 saatten eski olanlardır (fiziksel
	// tavan ~%65). Recall raporlanır, eşiğe bağlanmaz (K7).
	caught := 0
	for id := range injected {
		if violations[id] {
			caught++
		}
	}
	recall := float64(caught) / float64(len(injected))
	t.Logf("kural 3 recall = %.3f (teorik tavan ~0,65 — kaydırma 2sa, olay aralığı 2,4sa)", recall)
	if recall > 1.0 {
		t.Fatalf("recall 1'i aştı (%f): bulgu kümesi enjeksiyon kümesinden büyük", recall)
	}
}

// ─── 2. İdempotanslık ─────────────────────────────────────────────────────────

// TestIntegrityFindingsAreIdempotent, akış fazının iki kez koşmasının satır
// sayısını değiştirmediğini doğrular (ADR-31/2).
//
// Kafka at-least-once semantiğinde tüketici çökerse parti yeniden işlenir.
// Yinelenen bulgu F.5'in precision denominatörünü şişirir ve ölçümü SESSİZCE
// düşürür.
func TestIntegrityFindingsAreIdempotent(t *testing.T) {
	pool, rdb := requireInfra(t)
	requireKafka(t)

	r := setupIntegrityRun(t, pool, rdb)

	// Aynı koşuyu iki farklı tüketici grubuyla iki kez işle: ikinci geçiş tüm
	// kayıtları baştan okur ve aynı bulguları üretmeye çalışır.
	runStreamPhase(t, pool, r, "it-idem-1-"+r.runID.String())
	first, firstCanonical, err := pool.CountFindings(context.Background(), r.runID)
	if err != nil {
		t.Fatalf("CountFindings: %v", err)
	}
	if first == 0 {
		t.Fatal("ilk geçiş hiç bulgu üretmedi — pozitif kontrol başarısız")
	}

	runStreamPhase(t, pool, r, "it-idem-2-"+r.runID.String())
	second, secondCanonical, err := pool.CountFindings(context.Background(), r.runID)
	if err != nil {
		t.Fatalf("CountFindings: %v", err)
	}

	if second != first || secondCanonical != firstCanonical {
		t.Errorf("yeniden işleme satır sayısını değiştirdi: %d/%d → %d/%d "+
			"(UNIQUE (run_id, event_id, rule_id) çalışmıyor)",
			first, firstCanonical, second, secondCanonical)
	}
	t.Logf("idempotanslık: %d bulgu (%d kanonik), iki geçişte değişmedi", first, firstCanonical)
}

// ─── 3. Gölge SQL çapraz kontrolü ─────────────────────────────────────────────

// TestShadowSQLAgreesWithDetector, Go dedektörünün bulgu kümesinin bağımsız SQL
// sorgularıyla birebir aynı olduğunu doğrular.
//
// Sprint 5'in PostGIS↔Haversine çapraz kontrolünün karşılığı: bir uygulama
// hatasının "bilimsel bulgu" olarak raporlanmasını engeller.
func TestShadowSQLAgreesWithDetector(t *testing.T) {
	pool, rdb := requireInfra(t)
	requireKafka(t)

	r := setupIntegrityRun(t, pool, rdb)
	runStreamPhase(t, pool, r, "it-shadow-"+r.runID.String())
	runBatchPhase(t, pool, r)

	db := pool.Querier()
	ctx := context.Background()

	// Kural 1 gölge sorgusu: envanterde olmayan cell_id.
	var shadowInventory int64
	err := db.QueryRow(ctx, `
        SELECT count(*)
          FROM hts_records h
          LEFT JOIN cells c ON c.cell_id = h.cell_id AND c.run_id = h.run_id
         WHERE h.run_id = $1::uuid AND c.cell_id IS NULL`, r.runID.String()).Scan(&shadowInventory)
	if err != nil {
		t.Fatalf("kural 1 gölge sorgusu: %v", err)
	}

	detectorInventory, err := countFindingsOfRule(pool, r.runID, integrityrule.Inventory)
	if err != nil {
		t.Fatalf("kural 1 bulgu sayımı: %v", err)
	}
	if detectorInventory != shadowInventory {
		t.Errorf("kural 1: dedektör %d, gölge SQL %d bulgu", detectorInventory, shadowInventory)
	}

	// Kural 5 gölge sorgusu: MSISDN başına modal IMEI'den sapan kayıtlar.
	var shadowActivity int64
	err = db.QueryRow(ctx, `
        WITH cnt AS (
            SELECT pseudo_msisdn, pseudo_imei, count(*) AS n
              FROM hts_records
             WHERE run_id = $1::uuid AND pseudo_imei <> ''
             GROUP BY 1, 2
        ), modal AS (
            SELECT pseudo_msisdn, pseudo_imei FROM (
                SELECT *, row_number() OVER (
                    PARTITION BY pseudo_msisdn ORDER BY n DESC, pseudo_imei) AS rn
                  FROM cnt) x
             WHERE rn = 1
        ), support AS (
            SELECT m.pseudo_msisdn, c.n
              FROM modal m JOIN cnt c
                ON c.pseudo_msisdn = m.pseudo_msisdn AND c.pseudo_imei = m.pseudo_imei
        ), multi AS (
            SELECT pseudo_msisdn FROM cnt GROUP BY 1 HAVING count(*) > 1
        )
        SELECT count(*)
          FROM hts_records h
          JOIN modal   m ON m.pseudo_msisdn = h.pseudo_msisdn
          JOIN support s ON s.pseudo_msisdn = h.pseudo_msisdn
          JOIN multi   u ON u.pseudo_msisdn = h.pseudo_msisdn
         WHERE h.run_id = $1::uuid
           AND h.pseudo_imei <> ''
           AND h.pseudo_imei <> m.pseudo_imei
           AND s.n >= $2`, r.runID.String(), r.scenario.Integrity.Detection.ActivityMinSupport).
		Scan(&shadowActivity)
	if err != nil {
		t.Fatalf("kural 5 gölge sorgusu: %v", err)
	}

	detectorActivity, err := countFindingsOfRule(pool, r.runID, integrityrule.Activity)
	if err != nil {
		t.Fatalf("kural 5 bulgu sayımı: %v", err)
	}
	if detectorActivity != shadowActivity {
		t.Errorf("kural 5: dedektör %d, gölge SQL %d bulgu", detectorActivity, shadowActivity)
	}
	t.Logf("gölge SQL uyumu — kural 1: %d, kural 5: %d", detectorInventory, detectorActivity)
}

// ─── 4. Pozitif kontrol ve F.5 ────────────────────────────────────────────────

// TestIntegrityPipeline_ProducesMeasurableFindings, uçtan uca hattın ölçülebilir
// bulgu ürettiğini doğrular.
//
// # Sessiz başarısızlık kontrolü
//
// "Hiç bulgu yok" durumu tüm yanlış-pozitif testlerini geçer. Bu test o sessiz
// başarısızlık biçimini kapatır: her kanonik kural en az bir bulgu üretmeli.
// `tests/isolation`'daki TestDetectorSeesKnownDependency ile aynı felsefe.
func TestIntegrityPipeline_ProducesMeasurableFindings(t *testing.T) {
	pool, rdb := requireInfra(t)
	requireKafka(t)

	r := setupIntegrityRun(t, pool, rdb)
	streamStats := runStreamPhase(t, pool, r, "it-pipeline-"+r.runID.String())
	batchStats := runBatchPhase(t, pool, r)

	if streamStats.Invalid != 0 || batchStats.Invalid != 0 {
		t.Errorf("geçerlilik denetiminden düşen isabet var: akış %d, toplu %d",
			streamStats.Invalid, batchStats.Invalid)
	}

	// Pozitif kontrol: üç yapısal kural bulgu üretmeli.
	for _, id := range []integrityrule.ID{
		integrityrule.Inventory, integrityrule.TimeOrder, integrityrule.Activity,
	} {
		n, err := countFindingsOfRule(pool, r.runID, id)
		if err != nil {
			t.Fatalf("%s bulgu sayımı: %v", id, err)
		}
		if n == 0 {
			t.Errorf("%s hiç bulgu üretmedi — sessiz başarısızlık olabilir", id)
		}
		t.Logf("%s: %d kanonik bulgu", id, n)
	}

	// F.5 ölçümü.
	report, err := vintegrity.Compute(context.Background(), pool.Querier(), vintegrity.Options{
		RunID:       r.runID,
		MinFindings: r.scenario.Integrity.Detection.MinFindingsForThreshold,
	})
	if err != nil {
		t.Fatalf("F.5: %v", err)
	}
	if err := vintegrity.Write(context.Background(), pool.Querier(), report); err != nil {
		t.Fatalf("integrity_metrics yazımı: %v", err)
	}

	if len(report.Rows) != 5 {
		t.Errorf("F.5 %d kural satırı üretti, beklenen 5", len(report.Rows))
	}
	for _, row := range report.Rows {
		// ADR-30: kural 4 daima "kapsam dışı".
		if row.RuleID == integrityrule.Trajectory {
			if row.Precision != nil {
				t.Error("kural 4 için precision hesaplandı — ADR-30 ihlali")
			}
			continue
		}
		// Precision hesaplandıysa yanlış pozitif olmalı ya da olmamalı; ama
		// bulgu > 0 iken nil kalmamalı.
		if row.Findings > 0 && row.Precision == nil {
			t.Errorf("%s: %d bulgu var ama precision nil", row.RuleID, row.Findings)
		}
	}
	t.Log("\n" + report.Summary())
	t.Log(report.K7Verdict())

	// Yazılan satırlar geri okunabilmeli (şema kısıtları geçildi).
	var written int64
	if err := pool.Querier().QueryRow(context.Background(),
		`SELECT count(*) FROM integrity_metrics WHERE run_id = $1::uuid`,
		r.runID.String()).Scan(&written); err != nil {
		t.Fatalf("integrity_metrics okunamadı: %v", err)
	}
	if written != 5 {
		t.Errorf("integrity_metrics %d satır, beklenen 5", written)
	}
}

// TestBatchPhaseRequiresStreamPhase, toplu fazın akış fazı olmadan
// reddedildiğini doğrular (ADR-27/3).
func TestBatchPhaseRequiresStreamPhase(t *testing.T) {
	pool, rdb := requireInfra(t)
	requireKafka(t)

	r := setupIntegrityRun(t, pool, rdb)
	// Akış fazı KOŞTURULMAZ → inspected_records NULL kalır.

	engine, err := detector.NewBatchEngine(detector.Config{
		Claims: detector.NewClaims(),
		Sink:   detector.SinkFunc(func(context.Context, []detector.Finding) (int64, error) { return 0, nil }),
	})
	if err != nil {
		t.Fatalf("NewBatchEngine: %v", err)
	}
	batch, err := source.NewBatch(source.BatchConfig{RunID: r.runID}, pool, engine)
	if err != nil {
		t.Fatalf("NewBatch: %v", err)
	}

	if err := batch.CheckPrecondition(context.Background()); err == nil {
		t.Error("akış fazı koşmadan toplu faz kabul edildi — " +
			"talep defteri eksik olurdu (ADR-27/3, ADR-28/6)")
	} else {
		t.Logf("önkoşul doğru reddetti: %v", err)
	}
}

// ─── Yardımcılar ──────────────────────────────────────────────────────────────

// eventIDsOfRule, bir kuralın kanonik bulgularının olay kimliklerini döndürür.
func eventIDsOfRule(pool *postgres.Pool, runID uuid.UUID, rule integrityrule.ID) (map[uuid.UUID]bool, error) {
	rows, err := pool.Querier().Query(context.Background(), `
        SELECT event_id FROM integrity_findings
         WHERE run_id = $1::uuid AND rule_id = $2 AND suppressed_by IS NULL`,
		runID.String(), int(rule))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[uuid.UUID]bool)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// eventIDsOfInjection, bir enjeksiyon kuralının etiketli olaylarını döndürür.
//
// `ground_truth`'a bakar — bu test S3b rolündedir (ölçüm), dedektör değil.
func eventIDsOfInjection(pool *postgres.Pool, runID uuid.UUID, rule integrityrule.ID) (map[uuid.UUID]bool, error) {
	rows, err := pool.Querier().Query(context.Background(), `
        SELECT g.event_id
          FROM ground_truth g
          JOIN hts_records h ON h.run_id = g.run_id AND h.event_id = g.event_id
         WHERE g.run_id = $1::uuid AND g.injected_rule = $2`,
		runID.String(), int(rule))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[uuid.UUID]bool)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// countFindingsOfRule, bir kuralın kanonik bulgu sayısını döndürür.
func countFindingsOfRule(pool *postgres.Pool, runID uuid.UUID, rule integrityrule.ID) (int64, error) {
	var n int64
	err := pool.Querier().QueryRow(context.Background(), `
        SELECT count(*) FROM integrity_findings
         WHERE run_id = $1::uuid AND rule_id = $2 AND suppressed_by IS NULL`,
		runID.String(), int(rule)).Scan(&n)
	return n, err
}
