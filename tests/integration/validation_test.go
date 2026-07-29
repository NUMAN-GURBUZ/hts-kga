// T-E04-02..06 — Doğrulama hattının uçtan uca entegrasyon testi.
//
// Zincir: simülatör → Kafka → persister'lar → analiz motoru → estimates
//
//	→ önkoşul → λ pilotu → kalibrasyon → F.1–F.4 → metrics
//
// Bu test Sprint 5'in bütün iddialarını tek koşuda sınar ve K1–K4'ün
// ölçüldüğü yerdir. Küçük ölçekte koşar (ADR-14 örneklemesine sadık):
// tam koşu T-E04-08'in işidir.
package integration

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/core"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/driver"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/sampling"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/inventory"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/validation/calibration"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/validation/comparison"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/validation/metrics"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/validation/pipeline"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// validationRun, doğrulanmaya hazır bir koşunun tüm bileşenleridir.
type validationRun struct {
	runID    uuid.UUID
	scenario *config.Scenario
	pool     *postgres.Pool
	rdb      *redis.Client
	analyzed int64
}

// prepareRun, simülasyondan analize kadar tüm zinciri koşturur.
//
// Örnekleme modu **validation**'dır: üretimdeki yolun aynısı. Kalibrasyon
// kümesi için tahmin üretilmez — kalibrasyon kütleyi kendisi hesaplar.
func prepareRun(t *testing.T, configFile string, agents, days int) *validationRun {
	t.Helper()

	pool, rdb := requireInfra(t)
	brokers := requireKafka(t)
	admin := adminPool(t)
	ctx := context.Background()

	runID, stats := simulateSmall(t, pool, rdb, configFile, agents, days)
	t.Cleanup(func() {
		purgeRun(t, admin, runID)
		if _, err := admin.Exec(ctx, `DELETE FROM metrics WHERE run_id = $1::uuid`,
			runID.String()); err != nil {
			t.Errorf("temizlik (metrics): %v", err)
		}
		if err := pool.DeleteRun(ctx, runID); err != nil {
			t.Errorf("temizlik (postgres): %v", err)
		}
		if _, err := rdb.DeleteRunKeys(ctx, runID); err != nil {
			t.Errorf("temizlik (redis): %v", err)
		}
	})

	scn, err := config.Load(configsDir + "/" + configFile)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	scn.Simulation.Agents = agents
	scn.Simulation.DurationDays = days

	suffix := runID.String()[:8]
	drainRecords(t, pool, "val-records-"+suffix, stats.PublishedRecords, runID)
	drainGroundTruth(t, pool, "val-gt-"+suffix, stats.PublishedTruths, runID)

	analyzed := analyzeRun(t, pool, rdb, scn, runID, brokers, suffix)

	t.Logf("koşu %s hazır: %d olay, %d kayıt, %d analiz edilen",
		runID, stats.Events, stats.PublishedRecords, analyzed)

	return &validationRun{runID: runID, scenario: scn, pool: pool, rdb: rdb, analyzed: analyzed}
}

// analyzeRun, analiz motorunu 'V' kümesi üzerinde koşturur.
func analyzeRun(t *testing.T, pool *postgres.Pool, rdb *redis.Client,
	scn *config.Scenario, runID uuid.UUID, brokers []string, suffix string) int64 {
	t.Helper()
	ctx := context.Background()

	inv, err := params.Load(ctx, rdb, runID, scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		t.Fatalf("params.Load: %v", err)
	}
	grid, err := density.NewGrid(scn.Analysis.GridResolutionM)
	if err != nil {
		t.Fatalf("density.NewGrid: %v", err)
	}

	var tech ta.Technology
	if scn.TimingAdvance.Enabled {
		if tech, err = ta.Parse(scn.TimingAdvance.Technology); err != nil {
			t.Fatalf("ta.Parse: %v", err)
		}
	}

	engine, err := driver.NewEngine(driver.EngineConfig{
		Inventory: inv,
		Grid:      grid,
		Options: core.DefaultOptions(density.Config{
			RxSensitivityDBm: scn.Network.RxSensitivityDBm,
			SigmaNominalDB:   scn.Radio.ShadowingSigmaDB,
			Lambda:           1,
			UTHeightM:        rf.UTHeightM,
		}, scn.Analysis.NeighborMaxCount),
		Technology: tech,
		TAEnabled:  scn.TimingAdvance.Enabled,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	persister, err := driver.NewPersister(driver.PersisterConfig{
		Inventory: inv, Grid: grid, Levels: scn.Analysis.ContourLevels,
		Sink: pool, FlushRows: 100,
	})
	if err != nil {
		t.Fatalf("NewPersister: %v", err)
	}

	sampler, err := sampling.New(sampling.Config{
		Mode:       sampling.ModeValidation,
		Seed:       scn.Run.Seed,
		SplitRatio: scn.Calibration.SplitRatio,
	})
	if err != nil {
		t.Fatalf("sampling.New: %v", err)
	}

	consumer, err := driver.NewConsumer(driver.ConsumerConfig{
		Brokers: brokers, Group: "val-analysis-" + suffix,
		Engine: engine, Handler: persister, Sampler: sampler, RunID: runID,
	})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()

	// Tüketici akışı boşaltana kadar koştur: hedef sayı önceden bilinmez
	// (örnekleme 'V' oranına bağlı), bu yüzden satır sayısı durunca durulur.
	consumeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- consumer.Run(consumeCtx) }()

	var stable, last int64
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(700 * time.Millisecond)
		n, err := pool.CountEstimates(ctx, runID)
		if err != nil {
			t.Fatalf("CountEstimates: %v", err)
		}
		if n > 0 && n == last {
			stable++
			if stable >= 3 {
				break
			}
		} else {
			stable = 0
		}
		last = n
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("tüketici: %v", err)
	}
	if err := persister.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	analyzed, _ := consumer.Stats()
	if err := pool.SetAnalyzedEvents(ctx, runID, analyzed); err != nil {
		t.Fatalf("SetAnalyzedEvents: %v", err)
	}
	return analyzed
}

// TestValidation_Preconditions, önkoşulun yarım koşuyu reddettiğini ve tam
// koşuyu kabul ettiğini doğrular (ADR-04, ADR-23).
func TestValidation_Preconditions(t *testing.T) {
	run := prepareRun(t, "urban_ta.yaml", 60, 1)
	ctx := context.Background()

	checks, err := pipeline.CheckPreconditions(ctx, run.pool, run.runID)
	if err != nil {
		t.Fatalf("tam koşu önkoşulu geçmedi: %v", err)
	}
	for _, c := range checks {
		t.Logf("önkoşul · %-24s %s", c.Name, c.Detail)
	}

	// Analiz sayacı bozulursa önkoşul reddetmeli.
	if err := run.pool.SetAnalyzedEvents(ctx, run.runID, run.analyzed+7); err != nil {
		t.Fatalf("SetAnalyzedEvents: %v", err)
	}
	if _, err := pipeline.CheckPreconditions(ctx, run.pool, run.runID); err == nil {
		t.Error("tutarsız analiz sayacı kabul edildi")
	} else {
		t.Logf("beklenen ret: %v", err)
	}

	// Geri al.
	if err := run.pool.SetAnalyzedEvents(ctx, run.runID, run.analyzed); err != nil {
		t.Fatalf("SetAnalyzedEvents: %v", err)
	}
}

// TestValidation_LambdaPilotSweep, λ eğrisinin şeklini ölçer (G0 pilotu).
//
// ADR-02 `coverage(λ)`'nın monoton artan olduğunu **varsayar**; bu test o
// varsayımı gerçek veriyle sınar. Eğri düzse (TA'lı senaryolarda beklenen
// risk, ADR-25) bisection hedefi tutturamaz ve bu, kalibrasyon kodunun
// hatası değil modelin sınırıdır.
func TestValidation_LambdaPilotSweep(t *testing.T) {
	for _, tc := range []struct {
		file   string
		agents int
	}{
		{"urban_ta.yaml", 60},
		{"urban_no_ta.yaml", 60},
	} {
		t.Run(tc.file, func(t *testing.T) {
			run := prepareRun(t, tc.file, tc.agents, 1)

			runner, err := calibration.New(calibration.Config{
				Pool: run.pool, Inventory: run.rdb,
				Scenario: run.scenario, RunID: run.runID,
			})
			if err != nil {
				t.Fatalf("calibration.New: %v", err)
			}

			// Tanı: kapsamanın üst sınırı (gerçek konum dilim içinde mi)
			diag, err := runner.Diagnose()
			if err != nil {
				t.Fatalf("Diagnose: %v", err)
			}
			t.Logf("%s · %s", tc.file, diag.Summary())

			lambdas := []float64{0.5, 1.0, 1.5, 2.0, 3.0}
			points, err := runner.Sweep(context.Background(), lambdas)
			if err != nil {
				t.Fatalf("Sweep: %v", err)
			}

			t.Logf("λ taraması (%s, %d olay):", tc.file, runner.SampleSize())
			for _, p := range points {
				t.Logf("  λ=%.2f → kapsama %.4f", p.Lambda, p.Coverage)
			}

			// Monotonluk: ADR-02'nin varsayımı. İhlal ediliyorsa bisection
			// sessizce yanlış kök verebilir — bu bir bulgudur, testi
			// düşürmez ama görünür olmalıdır.
			for i := 1; i < len(points); i++ {
				if points[i].Coverage < points[i-1].Coverage-1e-9 {
					t.Errorf("MONOTONLUK İHLALİ: λ=%.2f → %.4f, λ=%.2f → %.4f "+
						"(ADR-02 varsayımı gerçek veride tutmuyor)",
						points[i-1].Lambda, points[i-1].Coverage,
						points[i].Lambda, points[i].Coverage)
				}
			}

			span := points[len(points)-1].Coverage - points[0].Coverage
			maxCoverage := points[len(points)-1].Coverage
			t.Logf("  eğri açıklığı: %.4f (λ=0,5 → λ=3,0)", span)
			t.Logf("  kapsama/üst sınır oranı: %.3f (λ=3,0'da %.4f / %.4f)",
				maxCoverage/diag.WedgeRatio(), maxCoverage, diag.WedgeRatio())

			if maxCoverage > diag.WedgeRatio()+1e-9 {
				t.Errorf("kapsama (%.4f) üst sınırı (%.4f) aştı — tanı ölçümü tutarsız",
					maxCoverage, diag.WedgeRatio())
			}
			if span < 0.01 {
				t.Logf("  UYARI: eğri neredeyse düz — λ'nın kaldıracı yok (ADR-25). " +
					"Bisection hedefi tutturamayabilir; bu modelin sınırıdır, kodun değil.")
			}
		})
	}
}

// TestValidation_FullPipeline, kalibrasyon + ölçüm zincirini uçtan uca
// koşturur ve K1–K3 sayılarını raporlar.
func TestValidation_FullPipeline(t *testing.T) {
	run := prepareRun(t, "urban_ta.yaml", 80, 1)
	ctx := context.Background()

	// ─── Kalibrasyon (ADR-02) ────────────────────────────────────────────────
	runner, err := calibration.New(calibration.Config{
		Pool: run.pool, Inventory: run.rdb,
		Scenario: run.scenario, RunID: run.runID,
	})
	if err != nil {
		t.Fatalf("calibration.New: %v", err)
	}

	result, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("kalibrasyon: %v", err)
	}
	t.Log(result.Summary())

	if result.Lambda < run.scenario.Calibration.LambdaMin ||
		result.Lambda > run.scenario.Calibration.LambdaMax {
		t.Errorf("λ* %.4f, [%.2f, %.2f] aralığında olmalı",
			result.Lambda, run.scenario.Calibration.LambdaMin, run.scenario.Calibration.LambdaMax)
	}

	// λ* veritabanına yazılmış olmalı.
	status, err := run.pool.RunStatusOf(ctx, run.runID)
	if err != nil {
		t.Fatalf("RunStatusOf: %v", err)
	}
	if status.Lambda == nil {
		t.Fatal("λ* run_config'e yazılmadı")
	}
	if *status.Lambda != result.Lambda {
		t.Errorf("yazılan λ %.6f, hesaplanan %.6f", *status.Lambda, result.Lambda)
	}

	// ─── Ölçüm hattı ─────────────────────────────────────────────────────────
	pipe, err := pipeline.New(run.pool, nil)
	if err != nil {
		t.Fatalf("pipeline.New: %v", err)
	}

	report, err := pipe.Run(ctx, pipeline.Options{
		RunID:     run.runID,
		Scenario:  run.scenario.Run.Scenario,
		Recompute: true,
	})
	if err != nil {
		t.Fatalf("doğrulama hattı: %v", err)
	}
	t.Log("\n" + report.Summary())

	if len(report.Validation) != 5 {
		t.Errorf("%d ölçüm satırı, 5 beklenir (B0, B1, M@50/90/95)", len(report.Validation))
	}
	if !report.EqualSets {
		t.Error("yöntemler farklı olay kümelerinde ölçüldü")
	}

	// ─── K1: kapsama @90 ─────────────────────────────────────────────────────
	for _, row := range report.Validation {
		if row.Method == "M" && row.Confidence == comparison.ModelConfidence {
			t.Logf("K1 · kapsama@90%% = %.4f (eşik %%85–95)", row.CoverageRate)
			if row.CoverageRate < 0 || row.CoverageRate > 1 {
				t.Errorf("kapsama oranı [0,1] dışında: %g", row.CoverageRate)
			}
		}
		if row.NEvents <= 0 {
			t.Errorf("%s: n_events sıfır", row.Label())
		}
	}

	// ─── K2/K3: daralma ──────────────────────────────────────────────────────
	t.Logf("K2 · M@90 vs B0 daralma = %%%.1f (eşik ≥ %%75)", report.Reduction.ReductionVsB0*100)
	t.Logf("K3 · M@90 vs B1 daralma = %%%.1f (eşik: TA var ≥ %%50)",
		report.Reduction.ReductionVsB1*100)

	if report.Reduction.B0AreaKM2 <= report.Reduction.B1AreaKM2 {
		t.Errorf("B0 alanı (%.4f) B1'den (%.4f) büyük olmalı — PBT #3",
			report.Reduction.B0AreaKM2, report.Reduction.B1AreaKM2)
	}

	// ─── metrics tablosu yazıldı mı ──────────────────────────────────────────
	admin := adminPool(t)
	var rows int64
	err = admin.QueryRow(ctx, `
        SELECT count(*) FROM metrics WHERE run_id = $1::uuid`, run.runID.String()).Scan(&rows)
	if err != nil {
		t.Fatalf("metrics sayımı: %v", err)
	}
	if rows != 5 {
		t.Errorf("metrics tablosunda %d satır, 5 beklenir", rows)
	}

	// ─── K4: yalnızca 'V' kümesi yazılmış olmalı ─────────────────────────────
	var partitions int64
	err = admin.QueryRow(ctx, `
        SELECT count(DISTINCT partition_key) FROM metrics WHERE run_id = $1::uuid`,
		run.runID.String()).Scan(&partitions)
	if err != nil {
		t.Fatalf("partition sayımı: %v", err)
	}
	if partitions != 1 {
		t.Errorf("metrics tablosunda %d farklı partition var — K4 ölçümü karışmış", partitions)
	}
}

// TestValidation_HaversineCrossCheck, F.3'ün PostGIS mesafesini bağımsız bir
// hesapla doğrular.
//
// `ST_Distance(geography)` jeodeziktir; argümanlar `::geometry`'ye
// düşürülseydi sonuç derece cinsinden anlamsız bir sayı olurdu ve **hata
// verilmezdi**. Bu test o sessiz hatayı yakalar.
func TestValidation_HaversineCrossCheck(t *testing.T) {
	run := prepareRun(t, "urban_ta.yaml", 50, 1)
	ctx := context.Background()
	admin := adminPool(t)

	rows, err := admin.Query(ctx, `
        SELECT ST_Y(e.centroid::geometry), ST_X(e.centroid::geometry),
               ST_Y(g.true_location::geometry), ST_X(g.true_location::geometry),
               ST_Distance(e.centroid, g.true_location)
          FROM estimates e
          JOIN ground_truth g ON g.run_id = e.run_id AND g.event_id = e.event_id
         WHERE e.run_id = $1::uuid AND e.method = 'M' AND e.confidence = 0.90
         LIMIT 200`, run.runID.String())
	if err != nil {
		t.Fatalf("çapraz kontrol sorgusu: %v", err)
	}
	defer rows.Close()

	var checked int
	var maxRel float64
	for rows.Next() {
		var cLat, cLon, tLat, tLon, postgis float64
		if err := rows.Scan(&cLat, &cLon, &tLat, &tLon, &postgis); err != nil {
			t.Fatalf("satır okunamadı: %v", err)
		}

		own := geo.Haversine(geo.WGS84{Lat: cLat, Lon: cLon}, geo.WGS84{Lat: tLat, Lon: tLon})
		if own < 1 {
			continue // çok küçük mesafede bağıl fark anlamsız
		}
		rel := math.Abs(own-postgis) / postgis
		if rel > maxRel {
			maxRel = rel
		}
		checked++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("çapraz kontrol: %v", err)
	}
	if checked == 0 {
		t.Skip("karşılaştırılacak satır yok")
	}

	t.Logf("F.3 çapraz kontrolü: %d satır, en büyük bağıl fark %%%.4f "+
		"(PostGIS elipsoit ↔ Haversine küre)", checked, maxRel*100)

	// Haversine küre, ST_Distance elipsoit üzerindedir; fark %0,5'i aşmamalı.
	if maxRel > 0.005 {
		t.Errorf("F.3 mesafe farkı %%%.4f — jeodezik hesap şüpheli "+
			"(::geometry kastı derece cinsinden sonuç üretir)", maxRel*100)
	}
}

// TestValidation_MetricsRejectMixedPartition, K4'ün çalışma zamanındaki
// karşılığını doğrular: ölçüm yalnızca tek bir küme üzerinde yapılabilir.
func TestValidation_MetricsRejectMixedPartition(t *testing.T) {
	requireInfra(t)

	// Karışık küme ifadeleri tip düzeyinde üretilemez; metinden çözüm de
	// reddeder. Bu, K4'ün "karışık sorgu → hata" ölçütüdür.
	for _, mixed := range []string{"C,V", "both", "%", ""} {
		if _, err := metrics.ParsePartition(mixed); err == nil {
			t.Errorf("%q kabul edildi — karışık küme reddedilmeliydi", mixed)
		}
	}

	// Ölçüm fonksiyonları belirtilmemiş anahtarı reddeder.
	var unset metrics.PartitionKey
	if _, err := metrics.Compute(context.Background(), nil, uuid.New(), "A", unset); err == nil {
		t.Error("belirtilmemiş bölüm anahtarıyla ölçüm yapıldı")
	}
}

// TestValidation_RejectsUnfinishedRun, tamamlanmamış koşuda doğrulamanın
// çalışmayı reddettiğini doğrular.
func TestValidation_RejectsUnfinishedRun(t *testing.T) {
	pool, rdb := requireInfra(t)
	ctx := context.Background()
	admin := adminPool(t)

	// Envanteri yaz ama koşuyu kapatma.
	scn, err := config.Load(configsDir + "/urban_ta.yaml")
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	scn.Simulation.Agents = 5
	scn.Simulation.DurationDays = 1

	runID := uuid.New()
	t.Cleanup(func() {
		purgeRun(t, admin, runID)
		_ = pool.DeleteRun(ctx, runID)
		_, _ = rdb.DeleteRunKeys(ctx, runID)
	})

	proj, err := geo.NewProjector(scn.Area.OriginLat, scn.Area.OriginLon)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	inv, err := inventory.Build(runID, scn, proj)
	if err != nil {
		t.Fatalf("inventory.Build: %v", err)
	}
	store := inventory.Store{DB: pool, Cache: rdb}
	if err := store.Persist(ctx, inv, scn, postgres.GitSHA()); err != nil {
		t.Fatalf("envanter: %v", err)
	}

	if _, err := pipeline.CheckPreconditions(ctx, pool, runID); err == nil {
		t.Fatal("tamamlanmamış koşu doğrulamaya kabul edildi")
	} else {
		t.Logf("beklenen ret: %v", err)
	}

	pipe, err := pipeline.New(pool, nil)
	if err != nil {
		t.Fatalf("pipeline.New: %v", err)
	}
	if _, err := pipe.Run(ctx, pipeline.Options{RunID: runID, Scenario: "A"}); err == nil {
		t.Fatal("tamamlanmamış koşuda ölçüm yapıldı")
	}
}
