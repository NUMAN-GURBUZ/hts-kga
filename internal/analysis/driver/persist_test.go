package driver

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/core"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/htswire"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// Test şebekesi — ADR-20 gereği simülatör paketlerinden bağımsız kurulur.
const (
	testOriginLat = 38.6748
	testOriginLon = 39.2225
	testResM      = 250
)

var testLevels = []float64{0.50, 0.90, 0.95}

// staticSource, sabit hücre listesi döndüren envanter kaynağıdır.
type staticSource struct{ cells []redis.CellParams }

func (s staticSource) ScanCells(context.Context, uuid.UUID) ([]redis.CellParams, error) {
	return s.cells, nil
}

// testInventory, tek siteli üç sektörlü küçük bir şebeke kurar.
func testInventory(t testing.TB) *params.Inventory {
	t.Helper()

	siteID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("driver:site"))
	cells := make([]redis.CellParams, 0, 3)
	for sector := 0; sector < 3; sector++ {
		cells = append(cells, redis.CellParams{
			CellID:     uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("driver:cell:%d", sector))),
			SiteID:     siteID,
			Azimuth:    float64(sector) * 120,
			BeamWidth:  65,
			FreqMHz:    1800,
			EIRPdBm:    58,
			AntHeightM: 25,
			TiltDeg:    6,
			RMaxM:      3000,
			Morphology: "urban",
			ModelType:  "UMa",
			Lat:        testOriginLat,
			Lon:        testOriginLon,
		})
	}

	inv, err := params.Load(context.Background(), staticSource{cells: cells},
		uuid.New(), testOriginLat, testOriginLon)
	if err != nil {
		t.Fatalf("params.Load: %v", err)
	}
	return inv
}

// fakeSink, yazılan satırları bellekte toplar.
type fakeSink struct {
	mu      sync.Mutex
	rows    []postgres.EstimateRow
	batches []int
	err     error
}

func (f *fakeSink) InsertEstimates(_ context.Context, rows []postgres.EstimateRow) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.rows = append(f.rows, rows...)
	f.batches = append(f.batches, len(rows))
	return int64(len(rows)), nil
}

func (f *fakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

// setup, envanter + ızgara + motor + yazıcı zincirini kurar.
func setup(t testing.TB, flushRows int) (*Engine, *Persister, *fakeSink, *params.Inventory) {
	t.Helper()

	inv := testInventory(t)
	grid, err := density.NewGrid(testResM)
	if err != nil {
		t.Fatalf("NewGrid: %v", err)
	}

	cfg := density.Config{
		RxSensitivityDBm: -110,
		SigmaNominalDB:   7,
		Lambda:           1,
		UTHeightM:        rf.UTHeightM,
	}
	engine, err := NewEngine(EngineConfig{
		Inventory:  inv,
		Grid:       grid,
		Options:    core.DefaultOptions(cfg, 8),
		Technology: ta.LTE,
		TAEnabled:  true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	sink := &fakeSink{}
	p, err := NewPersister(PersisterConfig{
		Inventory: inv,
		Grid:      grid,
		Levels:    testLevels,
		Sink:      sink,
		FlushRows: flushRows,
	})
	if err != nil {
		t.Fatalf("NewPersister: %v", err)
	}
	return engine, p, sink, inv
}

// wireRecord, verilen hücre için tel biçiminde bir kayıt üretir.
func wireRecord(inv *params.Inventory, n int, taValue *int) htswire.Record {
	cell := inv.Cells()[n%inv.Len()]
	return htswire.Record{
		RunID:        uuid.NewSHA1(uuid.NameSpaceOID, []byte("driver:run")),
		EventID:      uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("driver:event:%d", n))),
		Time:         time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute),
		PseudoMSISDN: "pseudo",
		EventType:    "call",
		CellID:       cell.ID,
		TAValue:      taValue,
		Scenario:     "A",
	}
}

// TestPersister_FiveRowsPerEvent, olay başına beş satır yazıldığını ve
// alanların şema kısıtlarını karşıladığını doğrular.
func TestPersister_FiveRowsPerEvent(t *testing.T) {
	engine, persister, sink, inv := setup(t, 5)
	ctx := context.Background()

	rec := wireRecord(inv, 0, intPtr(6))
	res, err := engine.Process(rec)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if err := persister.Handle(ctx, rec, res); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if got := sink.count(); got != 5 {
		t.Fatalf("%d satır yazıldı, 5 beklenir", got)
	}

	methods := map[string]int{}
	for _, r := range sink.rows {
		methods[r.Method]++

		if r.RunID != rec.RunID || r.EventID != rec.EventID {
			t.Errorf("%s: kimlik alanları kayıtla eşleşmiyor", r.Method)
		}
		if !r.Time.Equal(rec.Time) {
			t.Errorf("%s: zaman %v, %v beklenir", r.Method, r.Time, rec.Time)
		}
		if r.Scenario != "A" {
			t.Errorf("%s: senaryo %q, \"A\" beklenir", r.Method, r.Scenario)
		}
		if len(r.GeometryWKB) == 0 || len(r.CentroidWKB) != 21 {
			t.Errorf("%s: geometri %d bayt, merkez %d bayt (21 beklenir)",
				r.Method, len(r.GeometryWKB), len(r.CentroidWKB))
		}
		if !(r.AreaKM2 > 0) || r.PartCount < 1 {
			t.Errorf("%s: area_km2=%g part_count=%d", r.Method, r.AreaKM2, r.PartCount)
		}
		if r.Method == "M" {
			if !(r.Confidence > 0 && r.Confidence < 1) {
				t.Errorf("M: confidence %g, (0,1) beklenir", r.Confidence)
			}
			if !r.TAUsed {
				t.Errorf("M@%g: ta_used=false, TA'lı kayıtta true beklenir", r.Confidence)
			}
		} else if r.Confidence != -1 {
			t.Errorf("%s: confidence %g, -1 sentinel beklenir", r.Method, r.Confidence)
		}
	}

	if methods["B0"] != 1 || methods["B1"] != 1 || methods["M"] != 3 {
		t.Errorf("yöntem dağılımı %v, B0=1 B1=1 M=3 beklenir", methods)
	}
}

// TestPersister_BuffersUntilThreshold, tamponun eşiğe kadar yazmadığını
// doğrular.
func TestPersister_BuffersUntilThreshold(t *testing.T) {
	engine, persister, sink, inv := setup(t, 20) // 4 olay = 20 satır
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		rec := wireRecord(inv, i, nil)
		res, err := engine.Process(rec)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if err := persister.Handle(ctx, rec, res); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}
	if got := sink.count(); got != 0 {
		t.Errorf("eşik altında %d satır yazıldı, 0 beklenir", got)
	}

	rec := wireRecord(inv, 3, nil)
	res, err := engine.Process(rec)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if err := persister.Handle(ctx, rec, res); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := sink.count(); got != 20 {
		t.Errorf("eşikte %d satır yazıldı, 20 beklenir", got)
	}

	// Kalan tampon boş: Flush hiçbir şey yazmamalı.
	if err := persister.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := sink.count(); got != 20 {
		t.Errorf("boş tampon boşaltılınca %d satır, 20 beklenir", got)
	}
}

// TestPersister_FlushWritesRemainder, kapanışta kalan satırların yazıldığını
// doğrular — Kafka offset'i ilerlemeden önce çağrılan yol budur.
func TestPersister_FlushWritesRemainder(t *testing.T) {
	engine, persister, sink, inv := setup(t, 1000)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		rec := wireRecord(inv, i, nil)
		res, err := engine.Process(rec)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if err := persister.Handle(ctx, rec, res); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}
	if got := sink.count(); got != 0 {
		t.Fatalf("eşik altında %d satır yazıldı", got)
	}

	if err := persister.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := sink.count(); got != 15 {
		t.Errorf("%d satır yazıldı, 15 beklenir (3 olay × 5)", got)
	}

	events, rows := persister.Stats()
	if events != 3 || rows != 15 {
		t.Errorf("istatistik (%d olay, %d satır), (3, 15) beklenir", events, rows)
	}
}

// TestPersister_ImplementsFlusher, tüketicinin tamponu offset commit'inden
// önce boşaltabildiğini doğrular.
//
// Arayüz uyumu derleme zamanında görünmez (tüketici tip iddiası kullanır);
// bu test bağın kopmadığını garanti eder.
func TestPersister_ImplementsFlusher(t *testing.T) {
	_, persister, _, _ := setup(t, 10)

	var h Handler = persister
	if _, ok := h.(Flusher); !ok {
		t.Fatal("Persister artık Flusher değil — tüketici tamponu boşaltamaz, " +
			"offset commit'i yazılmamış satırların önüne geçer")
	}
}

// TestPersister_SinkErrorPropagates, yazma hatasının yutulmadığını doğrular.
func TestPersister_SinkErrorPropagates(t *testing.T) {
	engine, persister, sink, inv := setup(t, 5)
	ctx := context.Background()
	sink.err = fmt.Errorf("bağlantı koptu")

	rec := wireRecord(inv, 0, nil)
	res, err := engine.Process(rec)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if err := persister.Handle(ctx, rec, res); err == nil {
		t.Fatal("yazma hatası yutuldu")
	}
}

// TestPersister_Rejects, eksik yapılandırmayı reddeder.
func TestPersister_Rejects(t *testing.T) {
	inv := testInventory(t)
	grid, err := density.NewGrid(testResM)
	if err != nil {
		t.Fatalf("NewGrid: %v", err)
	}
	sink := &fakeSink{}

	cases := []struct {
		name string
		cfg  PersisterConfig
	}{
		{"envantersiz", PersisterConfig{Grid: grid, Levels: testLevels, Sink: sink}},
		{"ızgarasız", PersisterConfig{Inventory: inv, Levels: testLevels, Sink: sink}},
		{"hedefsiz", PersisterConfig{Inventory: inv, Grid: grid, Levels: testLevels}},
		{"seviyesiz", PersisterConfig{Inventory: inv, Grid: grid, Sink: sink}},
	}
	for _, tc := range cases {
		if _, err := NewPersister(tc.cfg); err == nil {
			t.Errorf("%s: hata beklenirdi", tc.name)
		}
	}
}

// TestDriverDeterminism, iki sürücünün (replay) aynı kayıtlarda bit düzeyinde
// aynı sonucu ürettiğini doğrular — K10'un S3 karşılığı.
func TestDriverDeterminism(t *testing.T) {
	engine, _, _, inv := setup(t, 100)
	ctx := context.Background()

	records := make([]htswire.Record, 0, 30)
	for i := 0; i < 30; i++ {
		var taValue *int
		if i%2 == 0 {
			taValue = intPtr(1 + i%20)
		}
		records = append(records, wireRecord(inv, i, taValue))
	}

	replayA, err := NewReplay(engine, records)
	if err != nil {
		t.Fatalf("NewReplay: %v", err)
	}
	replayB, err := NewReplay(engine, records)
	if err != nil {
		t.Fatalf("NewReplay: %v", err)
	}

	got, err := replayA.Collect(ctx)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want, err := replayB.Collect(ctx)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	for id, a := range got {
		b := want[id]
		if len(a.Mass) != len(b.Mass) {
			t.Fatalf("olay %s: hücre sayısı %d ≠ %d", id, len(a.Mass), len(b.Mass))
		}
		for cell, m := range a.Mass {
			if b.Mass[cell] != m {
				t.Fatalf("olay %s hücre %v: kütle ayrıştı %.17g ≠ %.17g", id, cell, m, b.Mass[cell])
			}
		}
		if a.TAUsed != b.TAUsed {
			t.Fatalf("olay %s: ta_used ayrıştı", id)
		}
	}
}

func intPtr(v int) *int { return &v }
