// T-E03-13 — Analiz hattının uçtan uca entegrasyon testi (KAPI 2).
//
// Zincir gerçek altyapıdan geçer:
//
//	envanter → PostgreSQL + Redis
//	olay     → Kafka `hts.records`
//	analiz   → core.Estimate → core.Shapes → PostgreSQL `estimates`
//	denetim  → verify_integrity, ST_Area çapraz kontrolü, K8 ölçümü
//
// # hts_records ve ground_truth satırlarını test yazar
//
// Bu iki tablonun yazıcısı henüz yoktur: ground truth persister ADR-04'e göre
// S3a'dır (T-E04-01, Sprint 5) ve `hts_records`'ın DB yolu da simülatörün
// bağlanmasıyla gelir. Test, o yazıcıların yerine geçer — böylece ADR-01
// referansiyel bütünlük denetimi bugün koşulabilir.
//
// Bu, kör testi (K6) bozmaz: satırları **test** yazar, analiz kodu değil.
// Analiz paketi ground_truth'u ne okur ne de tanır; ADR-20 import testi bunu
// kod düzeyinde ayrıca doğrular.
package integration

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/core"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/driver"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/inventory"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/htswire"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/kafka"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

const (
	envKafkaBrokers = "HTS_TEST_KAFKA_BROKERS"

	// pipelineEvents, duman testinde üretilen olay sayısıdır.
	//
	// ADR-14 örneklemesine sadık kalınır: K8 ve alan ölçümleri için 'V'
	// kümesinin tamamına gerek yoktur. Tam koşu (10 GB mertebesi) S5'in işidir.
	pipelineEvents = 120

	// pipelineTimeout, tüketicinin tüm olayları işlemesi için verilen süredir.
	pipelineTimeout = 4 * time.Minute
)

// requireKafka, Kafka adresini döndürür; tanımlı değilse testi atlar.
func requireKafka(t *testing.T) []string {
	t.Helper()

	addr := os.Getenv(envKafkaBrokers)
	if addr == "" {
		t.Skipf("Kafka testi atlandı: %s tanımlı değil", envKafkaBrokers)
	}
	return []string{addr}
}

// adminPool, ham SQL için doğrudan bağlantı açar.
//
// `postgres.Pool` bilinçli olarak genel Exec sunmaz; test, yazıcısı henüz
// olmayan tablolara (hts_records, ground_truth) yazmak için kendi bağlantısını
// kullanır.
func adminPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), os.Getenv(envPGDSN))
	if err != nil {
		t.Fatalf("yönetici bağlantısı: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// pipelineFixture, bir koşunun uçtan uca kurulumudur.
type pipelineFixture struct {
	runID      uuid.UUID
	scenario   *config.Scenario
	inventory  *params.Inventory
	grid       *density.Grid
	records    []htswire.Record
	truth      []htswire.GroundTruth
	technology ta.Technology
	pool       *postgres.Pool
	admin      *pgxpool.Pool
}

// setupPipeline, envanteri kurar, kayıtları üretir ve altyapıya yükler.
func setupPipeline(t *testing.T, configFile string) *pipelineFixture {
	t.Helper()

	pool, rdb := requireInfra(t)
	admin := adminPool(t)
	ctx := context.Background()

	scn, err := config.Load(configsDir + "/" + configFile)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	proj, err := geo.NewProjector(scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		t.Fatalf("geo.NewProjector: %v", err)
	}

	runID := uuid.New()
	t.Cleanup(func() {
		purgeRun(t, admin, runID)
		if err := pool.DeleteRun(ctx, runID); err != nil {
			t.Errorf("temizlik (postgres): %v", err)
		}
		if _, err := rdb.DeleteRunKeys(ctx, runID); err != nil {
			t.Errorf("temizlik (redis): %v", err)
		}
	})

	// Envanter → PostgreSQL + Redis
	simInv, err := inventory.Build(runID, scn, proj)
	if err != nil {
		t.Fatalf("inventory.Build: %v", err)
	}
	store := inventory.Store{DB: pool, Cache: rdb}
	if err := store.Persist(ctx, simInv, scn, postgres.GitSHA()); err != nil {
		t.Fatalf("envanter yazılamadı: %v", err)
	}

	// Envanterin analiz görünümü (Redis'ten — üretimdeki yolun aynısı)
	invView, err := params.Load(ctx, rdb, runID, scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		t.Fatalf("params.Load: %v", err)
	}
	grid, err := density.NewGrid(scn.Analysis.GridResolutionM)
	if err != nil {
		t.Fatalf("density.NewGrid: %v", err)
	}

	records, truth, tech := buildEvents(t, scn, invView, runID)

	return &pipelineFixture{
		runID: runID, scenario: scn, inventory: invView, grid: grid,
		records: records, truth: truth, technology: tech, pool: pool, admin: admin,
	}
}

// buildEvents, senaryo hücrelerine dağılmış olaylar üretir.
//
// Gerçek konum, hücrenin hüzme ekseni üzerinde r_max'ın yarısındadır: TA buna
// göre türetilir, böylece kayıt kendi içinde tutarlıdır (PBT #7).
func buildEvents(t *testing.T, scn *config.Scenario, inv *params.Inventory,
	runID uuid.UUID) ([]htswire.Record, []htswire.GroundTruth, ta.Technology) {
	t.Helper()

	var tech ta.Technology
	if scn.TimingAdvance.Enabled {
		var err error
		if tech, err = ta.Parse(scn.TimingAdvance.Technology); err != nil {
			t.Fatalf("ta.Parse: %v", err)
		}
	}
	projector := inv.Projector()
	base := time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC)

	records := make([]htswire.Record, 0, pipelineEvents)
	truth := make([]htswire.GroundTruth, 0, pipelineEvents)

	for i := 0; i < pipelineEvents; i++ {
		cell := inv.Cells()[(i*7)%inv.Len()] // hücreler arasında yayıl
		distance := cell.RMaxM / 2
		bearing := cell.AzimuthDeg * math.Pi / 180
		truePos := geo.Point{
			X: cell.Site.X + distance*math.Sin(bearing),
			Y: cell.Site.Y + distance*math.Cos(bearing),
		}
		trueWGS := projector.Inverse(truePos)

		var taValue *int
		if scn.TimingAdvance.Enabled {
			v, err := ta.FromDistance(distance, tech)
			if err != nil {
				t.Fatalf("ta.FromDistance: %v", err)
			}
			taValue = &v
		}

		eventID := uuid.NewSHA1(uuid.NameSpaceOID,
			[]byte(fmt.Sprintf("pipeline:%s:%d", runID, i)))
		at := base.Add(time.Duration(i) * time.Minute)

		records = append(records, htswire.Record{
			RunID:        runID,
			EventID:      eventID,
			Time:         at,
			PseudoMSISDN: fmt.Sprintf("pseudo-%04d", i),
			PseudoIMEI:   fmt.Sprintf("imei-%04d", i),
			EventType:    "MOC",
			CellID:       cell.ID,
			TAValue:      taValue,
			Scenario:     scn.Run.Scenario,
		})
		truth = append(truth, htswire.GroundTruth{
			RunID:        runID,
			EventID:      eventID,
			Time:         at,
			AgentID:      i,
			Lat:          trueWGS.Lat,
			Lon:          trueWGS.Lon,
			Covered:      true,
			PartitionKey: partitionKey(i),
		})
	}
	return records, truth, tech
}

// partitionKey, olay bazlı 80/20 ayrımının test karşılığıdır (pkg/split).
func partitionKey(i int) string {
	if i%5 == 0 {
		return "V"
	}
	return "C"
}

// TestAnalysisPipeline_EndToEnd, olaydan `estimates` satırına kadar tüm
// zinciri doğrular ve KAPI 2 ölçümlerini raporlar.
//
// Dört senaryonun tamamı koşulur: K8'in parçalanma ölçümü TA'ya ve ızgara
// çözünürlüğüne duyarlıdır, tek senaryodan çıkarılan sonuç yanıltıcı olurdu.
// Olay sayısı ADR-14 örneklemesine sadık kalır (senaryo başına 120) — tam
// koşu S5'in işidir.
func TestAnalysisPipeline_EndToEnd(t *testing.T) {
	for _, file := range []string{
		"urban_ta.yaml", "urban_no_ta.yaml", "rural_ta.yaml", "rural_no_ta.yaml",
	} {
		t.Run(file, func(t *testing.T) { runPipeline(t, file) })
	}
}

func runPipeline(t *testing.T, configFile string) {
	brokers := requireKafka(t)
	fx := setupPipeline(t, configFile)
	ctx := context.Background()

	// ─── 1. Olaylar Kafka'ya ─────────────────────────────────────────────────
	producer, err := kafka.NewProducer(brokers)
	if err != nil {
		t.Fatalf("kafka.NewProducer: %v", err)
	}
	defer producer.Close()

	messages := make([]kafka.Message, 0, len(fx.records))
	for _, rec := range fx.records {
		value, err := htswire.EncodeRecord(rec)
		if err != nil {
			t.Fatalf("EncodeRecord: %v", err)
		}
		messages = append(messages, kafka.Message{
			Topic: kafka.TopicRecords,
			Key:   []byte(rec.CellID.String()),
			Value: value,
		})
	}
	if err := producer.Publish(ctx, messages...); err != nil {
		t.Fatalf("Kafka'ya yayın: %v", err)
	}
	t.Logf("Kafka'ya %d olay yayınlandı", len(messages))

	// ─── 2. Analiz motoru tüketir ────────────────────────────────────────────
	engine, err := driver.NewEngine(driver.EngineConfig{
		Inventory: fx.inventory,
		Grid:      fx.grid,
		Options: core.DefaultOptions(density.Config{
			RxSensitivityDBm: fx.scenario.Network.RxSensitivityDBm,
			SigmaNominalDB:   fx.scenario.Radio.ShadowingSigmaDB,
			Lambda:           1,
			UTHeightM:        rf.UTHeightM,
		}, fx.scenario.Analysis.NeighborMaxCount),
		Technology: fx.technology,
		TAEnabled:  fx.scenario.TimingAdvance.Enabled,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	persister, err := driver.NewPersister(driver.PersisterConfig{
		Inventory: fx.inventory,
		Grid:      fx.grid,
		Levels:    fx.scenario.Analysis.ContourLevels,
		Sink:      fx.pool,
		FlushRows: 50,
	})
	if err != nil {
		t.Fatalf("NewPersister: %v", err)
	}

	consumer, err := driver.NewConsumer(driver.ConsumerConfig{
		Brokers: brokers,
		Group:   "hts-analysis-test-" + fx.runID.String()[:8],
		Engine:  engine,
		Handler: persister,
	})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()

	consumeCtx, cancel := context.WithTimeout(ctx, pipelineTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- consumer.Run(consumeCtx) }()

	wantRows := int64(len(fx.records) * 5)
	start := time.Now()
	deadline := time.Now().Add(pipelineTimeout)
	var rows int64
	for time.Now().Before(deadline) {
		rows, err = fx.pool.CountEstimates(ctx, fx.runID)
		if err != nil {
			t.Fatalf("CountEstimates: %v", err)
		}
		if rows >= wantRows {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("tüketici hatası: %v", err)
	}
	if err := persister.Flush(ctx); err != nil {
		t.Fatalf("kapanış Flush: %v", err)
	}

	rows, err = fx.pool.CountEstimates(ctx, fx.runID)
	if err != nil {
		t.Fatalf("CountEstimates: %v", err)
	}
	events, written := persister.Stats()
	t.Logf("%d olay işlendi, %d satır yazıldı (%s)", events, written, time.Since(start).Round(time.Second))

	if rows != wantRows {
		t.Fatalf("`estimates` %d satır, %d beklenir (olay başına 5)", rows, wantRows)
	}

	// ─── 3. Referansiyel bütünlük (ADR-01) ───────────────────────────────────
	insertRecords(t, fx.admin, fx.records)
	insertTruth(t, fx.admin, fx.truth)

	checkIntegrity(t, fx.admin, fx.runID)
	checkEstimateJoins(t, fx.admin, fx.runID, len(fx.records))

	// ─── 4. ST_Area çapraz kontrolü (ADR-21) ─────────────────────────────────
	area, err := fx.pool.CheckArea(ctx, fx.runID)
	if err != nil {
		t.Fatalf("CheckArea: %v", err)
	}
	t.Logf("alan çapraz kontrolü: %d satır, ortalama fark %%%.4f, en büyük fark %%%.4f",
		area.Rows, area.MeanRelDiff*100, area.MaxRelDiff*100)
	if area.Rows != rows {
		t.Errorf("çapraz kontrol %d satırı kapsadı, %d beklenir", area.Rows, rows)
	}
	if area.MaxRelDiff > 0.01 {
		t.Errorf("Go alanı ile ST_Area farkı %%%.4f, %%1 toleransı aşıyor", area.MaxRelDiff*100)
	}

	// ─── 5. K8 — geometri kararlılığı, YÖNTEM BAŞINA ─────────────────────────
	stats, err := fx.pool.GeometryStatsByMethod(ctx, fx.runID)
	if err != nil {
		t.Fatalf("GeometryStatsByMethod: %v", err)
	}

	var totalRepaired int64
	for _, s := range stats {
		label := s.Method
		if s.Method == "M" {
			label = fmt.Sprintf("M@%.0f%%", s.Confidence*100)
		}
		t.Logf("K8 · %-7s satır=%3d onarılan=%d p95_part_count=%.1f maks=%d "+
			"ort_köşe=%.0f ort_bayt=%.0f",
			label, s.Rows, s.Repaired, s.P95PartCount, s.MaxPartCount,
			s.AvgVertexCount, s.AvgBytes)
		totalRepaired += s.Repaired
	}

	repairedRatio := float64(totalRepaired) / float64(rows)
	t.Logf("K8 · repaired_ratio = %%%.4f (%d/%d)", repairedRatio*100, totalRepaired, rows)
	if repairedRatio >= 0.01 {
		t.Errorf("repaired_ratio %%%.4f, K8 eşiği %%1'in üstünde", repairedRatio*100)
	}
}

// insertRecords, `hts_records` satırlarını yazar (simülatörün DB yolunun yerine).
func insertRecords(t *testing.T, pool *pgxpool.Pool, records []htswire.Record) {
	t.Helper()
	ctx := context.Background()

	const sql = `
INSERT INTO hts_records (run_id, event_id, time, pseudo_msisdn, pseudo_imei,
                         event_type, cell_id, ta_value, scenario)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7::uuid, $8, $9)`

	for i, r := range records {
		if _, err := pool.Exec(ctx, sql, r.RunID.String(), r.EventID.String(), r.Time,
			r.PseudoMSISDN, r.PseudoIMEI, r.EventType, r.CellID.String(), r.TAValue,
			r.Scenario); err != nil {
			t.Fatalf("hts_records[%d]: %v", i, err)
		}
	}
}

// insertTruth, `ground_truth` satırlarını yazar (S3a persister'ın yerine).
func insertTruth(t *testing.T, pool *pgxpool.Pool, truth []htswire.GroundTruth) {
	t.Helper()
	ctx := context.Background()

	const sql = `
INSERT INTO ground_truth (run_id, event_id, time, agent_id, true_location,
                          covered, partition_key, injected_rule)
VALUES ($1::uuid, $2::uuid, $3, $4,
        ST_SetSRID(ST_MakePoint($5, $6), 4326)::geography, $7, $8, NULL)`

	for i, g := range truth {
		if _, err := pool.Exec(ctx, sql, g.RunID.String(), g.EventID.String(), g.Time,
			g.AgentID, g.Lon, g.Lat, g.Covered, g.PartitionKey); err != nil {
			t.Fatalf("ground_truth[%d]: %v", i, err)
		}
	}
}

// checkIntegrity, verify_integrity() fonksiyonunu koşar (ADR-01).
func checkIntegrity(t *testing.T, pool *pgxpool.Pool, runID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	rows, err := pool.Query(ctx, `SELECT check_name, status, count FROM verify_integrity($1::uuid)`,
		runID.String())
	if err != nil {
		t.Fatalf("verify_integrity: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var name, status string
		var count int64
		if err := rows.Scan(&name, &status, &count); err != nil {
			t.Fatalf("verify_integrity okunamadı: %v", err)
		}
		seen++
		t.Logf("verify_integrity · %-32s %s (%d)", name, status, count)

		// 'INFO' bilgilendirmedir (enjeksiyon kural 4 beklenen bir durumdur),
		// ama sayısı sıfırdan büyükse gerçek bir eşleşme hatasıdır — 003
		// migration'ının kendi yorumu böyle diyor.
		switch status {
		case "OK", "SKIP":
		case "INFO":
			if count > 0 {
				t.Errorf("bütünlük denetimi: %s = %d (INFO ama sıfır olmalı)", name, count)
			}
		default:
			t.Errorf("bütünlük denetimi başarısız: %s = %s (%d)", name, status, count)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("verify_integrity: %v", err)
	}
	if seen == 0 {
		t.Error("verify_integrity hiç denetim döndürmedi")
	}
}

// checkEstimateJoins, her tahmin satırının ground truth ile event_id üzerinden
// eşleştiğini doğrular — duman testinin asıl iddiası budur.
func checkEstimateJoins(t *testing.T, pool *pgxpool.Pool, runID uuid.UUID, wantEvents int) {
	t.Helper()
	ctx := context.Background()

	var orphans int64
	err := pool.QueryRow(ctx, `
        SELECT count(*)
        FROM estimates e
        LEFT JOIN ground_truth g USING (run_id, event_id)
        WHERE e.run_id = $1::uuid AND g.event_id IS NULL`, runID.String()).Scan(&orphans)
	if err != nil {
		t.Fatalf("eşleşme sorgusu: %v", err)
	}
	if orphans != 0 {
		t.Errorf("%d tahmin satırı ground_truth ile eşleşmiyor (ADR-01)", orphans)
	}

	var matched int64
	err = pool.QueryRow(ctx, `
        SELECT count(DISTINCT e.event_id)
        FROM estimates e
        JOIN ground_truth g USING (run_id, event_id)
        JOIN hts_records h USING (run_id, event_id)
        WHERE e.run_id = $1::uuid`, runID.String()).Scan(&matched)
	if err != nil {
		t.Fatalf("üçlü eşleşme sorgusu: %v", err)
	}
	if matched != int64(wantEvents) {
		t.Errorf("üç tablonun eşleştiği olay sayısı %d, %d beklenir", matched, wantEvents)
	}
	t.Logf("referansiyel bütünlük: %d olay, estimates ⋈ hts_records ⋈ ground_truth tam eşleşti", matched)
}

// purgeRun, koşuya ait tüm satırları siler.
//
// `estimates`, `hts_records` ve `ground_truth` hypertable'dır ve run_config'e
// FK ile bağlı değildir (bölümlenmiş tablolarda FK maliyeti kabul edilmedi);
// bu yüzden temizlik açıkça yapılır.
func purgeRun(t *testing.T, pool *pgxpool.Pool, runID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	for _, table := range []string{"estimates", "ground_truth", "hts_records"} {
		if _, err := pool.Exec(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE run_id = $1::uuid", table), runID.String()); err != nil {
			t.Errorf("temizlik (%s): %v", table, err)
		}
	}
}
