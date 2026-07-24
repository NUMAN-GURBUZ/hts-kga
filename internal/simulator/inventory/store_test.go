package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
)

// ─── Sahte depolar ────────────────────────────────────────────────────────────

// fakeDB, CellWriter'ın test uygulamasıdır.
type fakeDB struct {
	run      postgres.RunRow
	cells    []postgres.CellRow
	calls    int
	written  int64
	err      error
	shortBy  int64 // eksik yazma benzetimi
	callSeen bool
}

func (f *fakeDB) InsertRunWithCells(_ context.Context, run postgres.RunRow, cells []postgres.CellRow) (int64, error) {
	f.calls++
	f.callSeen = true
	if f.err != nil {
		return 0, f.err
	}
	f.run, f.cells = run, cells
	f.written = int64(len(cells)) - f.shortBy
	return f.written, nil
}

// fakeCache, CacheWriter'ın test uygulamasıdır.
type fakeCache struct {
	cells     []redis.CellParams
	sites     []redis.SiteLocation
	cellErr   error
	siteErr   error
	cellCalls int
	siteCalls int
}

func (f *fakeCache) BulkLoadCells(_ context.Context, _ uuid.UUID, cells []redis.CellParams) error {
	f.cellCalls++
	if f.cellErr != nil {
		return f.cellErr
	}
	f.cells = cells
	return nil
}

func (f *fakeCache) BulkLoadSites(_ context.Context, _ uuid.UUID, sites []redis.SiteLocation) error {
	f.siteCalls++
	if f.siteErr != nil {
		return f.siteErr
	}
	f.sites = sites
	return nil
}

// ─── Testler ──────────────────────────────────────────────────────────────────

// TestPersist_WritesBothStores, envanterin her iki depoya da tam yazıldığını sınar.
func TestPersist_WritesBothStores(t *testing.T) {
	scn, inv := mustBuild(t, "urban_ta.yaml")
	db, cache := &fakeDB{}, &fakeCache{}

	if err := (Store{DB: db, Cache: cache}).Persist(context.Background(), inv, scn, "abc123"); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	if db.calls != 1 {
		t.Errorf("DB çağrı sayısı = %d, beklenen 1 (tek işlem)", db.calls)
	}
	if len(db.cells) != inv.CellCount() {
		t.Errorf("DB'ye %d hücre yazıldı, beklenen %d", len(db.cells), inv.CellCount())
	}
	if len(cache.cells) != inv.CellCount() {
		t.Errorf("Redis'e %d hücre yazıldı, beklenen %d", len(cache.cells), inv.CellCount())
	}
	if len(cache.sites) != inv.SiteCount() {
		t.Errorf("Redis'e %d site yazıldı, beklenen %d", len(cache.sites), inv.SiteCount())
	}
}

// TestPersist_RunRowMatchesScenario, run_config satırının senaryoyu doğru
// yansıttığını sınar (ADR-05).
func TestPersist_RunRowMatchesScenario(t *testing.T) {
	scn, inv := mustBuild(t, "rural_no_ta.yaml")
	db, cache := &fakeDB{}, &fakeCache{}

	if err := (Store{DB: db, Cache: cache}).Persist(context.Background(), inv, scn, "deadbeef"); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	if db.run.RunID != inv.RunID {
		t.Errorf("run_id = %s, beklenen %s", db.run.RunID, inv.RunID)
	}
	if db.run.Scenario != "D" {
		t.Errorf("scenario = %q, beklenen \"D\"", db.run.Scenario)
	}
	if db.run.Morphology != "rural" {
		t.Errorf("morphology = %q, beklenen \"rural\"", db.run.Morphology)
	}
	if db.run.Seed != scn.Run.Seed {
		t.Errorf("seed = %d, beklenen %d", db.run.Seed, scn.Run.Seed)
	}
	if db.run.GitSHA != "deadbeef" {
		t.Errorf("git_sha = %q, beklenen \"deadbeef\"", db.run.GitSHA)
	}

	// config_yaml geçerli JSON olmalı ve senaryoyu geri verebilmeli
	var round map[string]any
	if err := json.Unmarshal(db.run.ConfigJSON, &round); err != nil {
		t.Fatalf("config_yaml geçerli JSON değil: %v", err)
	}
	if round["Morphology"] != "rural" {
		t.Errorf("config_yaml morfolojiyi taşımıyor: %v", round["Morphology"])
	}
}

// TestPersist_EmptyGitSHAFallback, git_sha NOT NULL kısıtının korunduğunu sınar.
func TestPersist_EmptyGitSHAFallback(t *testing.T) {
	scn, inv := mustBuild(t, "urban_ta.yaml")
	db, cache := &fakeDB{}, &fakeCache{}

	if err := (Store{DB: db, Cache: cache}).Persist(context.Background(), inv, scn, ""); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	if db.run.GitSHA == "" {
		t.Error("git_sha boş kaldı — NOT NULL kısıtı ihlal edilir")
	}
}

// TestPersist_DBFailureSkipsCache, PostgreSQL yazımı başarısızsa Redis'e
// dokunulmadığını sınar (kalıcı kaynak önce).
func TestPersist_DBFailureSkipsCache(t *testing.T) {
	scn, inv := mustBuild(t, "urban_ta.yaml")
	db := &fakeDB{err: errors.New("bağlantı koptu")}
	cache := &fakeCache{}

	err := (Store{DB: db, Cache: cache}).Persist(context.Background(), inv, scn, "sha")
	if err == nil {
		t.Fatal("DB hatası için hata bekleniyordu")
	}
	if cache.cellCalls != 0 || cache.siteCalls != 0 {
		t.Errorf("DB başarısızken Redis'e yazıldı (cells=%d, sites=%d)",
			cache.cellCalls, cache.siteCalls)
	}
}

// TestPersist_PartialWriteDetected, eksik satır yazımının yakalandığını sınar.
func TestPersist_PartialWriteDetected(t *testing.T) {
	scn, inv := mustBuild(t, "urban_ta.yaml")
	db := &fakeDB{shortBy: 1}
	cache := &fakeCache{}

	err := (Store{DB: db, Cache: cache}).Persist(context.Background(), inv, scn, "sha")
	if err == nil {
		t.Fatal("eksik yazma için hata bekleniyordu")
	}
	if cache.cellCalls != 0 {
		t.Error("eksik yazmada Redis'e geçilmemeliydi")
	}
}

// TestPersist_CacheFailurePropagates, Redis hatasının yutulmadığını sınar.
func TestPersist_CacheFailurePropagates(t *testing.T) {
	scn, inv := mustBuild(t, "urban_ta.yaml")

	t.Run("hücre yüklemesi", func(t *testing.T) {
		db := &fakeDB{}
		cache := &fakeCache{cellErr: errors.New("redis kapalı")}
		if err := (Store{DB: db, Cache: cache}).Persist(context.Background(), inv, scn, "sha"); err == nil {
			t.Fatal("hata bekleniyordu")
		}
	})

	t.Run("site indeksi", func(t *testing.T) {
		db := &fakeDB{}
		cache := &fakeCache{siteErr: errors.New("redis kapalı")}
		if err := (Store{DB: db, Cache: cache}).Persist(context.Background(), inv, scn, "sha"); err == nil {
			t.Fatal("hata bekleniyordu")
		}
	})
}

// TestPersist_NilDependencies, eksik bağımlılıkların reddedildiğini sınar.
func TestPersist_NilDependencies(t *testing.T) {
	scn, inv := mustBuild(t, "urban_ta.yaml")

	tests := []struct {
		name  string
		store Store
		inv   *Inventory
	}{
		{"DB yok", Store{Cache: &fakeCache{}}, inv},
		{"önbellek yok", Store{DB: &fakeDB{}}, inv},
		{"envanter yok", Store{DB: &fakeDB{}, Cache: &fakeCache{}}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.store.Persist(context.Background(), tc.inv, scn, "sha"); err == nil {
				t.Error("hata bekleniyordu")
			}
		})
	}
}

// TestCellRows_MappingComplete, alan → satır eşlemesinde alan kaybı olmadığını
// sınar (şemanın her sütunu dolmalı).
func TestCellRows_MappingComplete(t *testing.T) {
	_, inv := mustBuild(t, "rural_ta.yaml")
	rows := CellRows(inv)

	if len(rows) != len(inv.Cells) {
		t.Fatalf("satır sayısı = %d, beklenen %d", len(rows), len(inv.Cells))
	}

	for i, c := range inv.Cells {
		r := rows[i]
		switch {
		case r.CellID != c.ID:
			t.Fatalf("satır[%d]: cell_id eşleşmiyor", i)
		case r.RunID != c.RunID:
			t.Fatalf("satır[%d]: run_id eşleşmiyor", i)
		case r.SiteID != c.SiteID:
			t.Fatalf("satır[%d]: site_id eşleşmiyor", i)
		case r.Azimuth != c.Azimuth:
			t.Fatalf("satır[%d]: azimuth eşleşmiyor", i)
		case r.BeamWidth != c.BeamWidth:
			t.Fatalf("satır[%d]: beam_width eşleşmiyor", i)
		case r.FreqMHz != c.FreqMHz:
			t.Fatalf("satır[%d]: freq_mhz eşleşmiyor", i)
		case r.EIRPdBm != c.EIRPdBm:
			t.Fatalf("satır[%d]: eirp_dbm eşleşmiyor", i)
		case r.AntHeightM != c.AntHeightM:
			t.Fatalf("satır[%d]: ant_height eşleşmiyor", i)
		case r.TiltDeg != c.TiltDeg:
			t.Fatalf("satır[%d]: tilt_deg eşleşmiyor", i)
		case r.RMaxM != c.RMaxM:
			t.Fatalf("satır[%d]: r_max_m eşleşmiyor", i)
		case r.Morphology != string(c.Morphology):
			t.Fatalf("satır[%d]: morphology eşleşmiyor", i)
		case r.ModelType != string(c.ModelType):
			t.Fatalf("satır[%d]: model_type eşleşmiyor", i)
		case r.Lat != c.Location.Lat || r.Lon != c.Location.Lon:
			t.Fatalf("satır[%d]: konum eşleşmiyor", i)
		}
	}
}

// TestCellParams_MappingComplete, Redis DTO eşlemesini sınar.
func TestCellParams_MappingComplete(t *testing.T) {
	_, inv := mustBuild(t, "urban_ta.yaml")
	params := CellParams(inv)

	if len(params) != len(inv.Cells) {
		t.Fatalf("parametre sayısı = %d, beklenen %d", len(params), len(inv.Cells))
	}
	for i, c := range inv.Cells {
		p := params[i]
		if p.CellID != c.ID || p.SiteID != c.SiteID || p.RMaxM != c.RMaxM ||
			p.FreqMHz != c.FreqMHz || p.Azimuth != c.Azimuth ||
			p.Lat != c.Location.Lat || p.Lon != c.Location.Lon {
			t.Fatalf("parametre[%d] eşleşmiyor:\n  %+v\n  %+v", i, p, c)
		}
	}
}

// TestCellParams_JSONRoundTrip, Redis'te saklanan gösterimin kayıpsız
// okunabildiğini sınar (S2 analiz motoru bu alanları okuyacak).
func TestCellParams_JSONRoundTrip(t *testing.T) {
	_, inv := mustBuild(t, "urban_ta.yaml")
	original := CellParams(inv)[0]

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back redis.CellParams
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != original {
		t.Errorf("JSON round-trip kaybı:\n  %+v\n  %+v", original, back)
	}
}

// TestSiteLocations_Mapping, GEO indeksi eşlemesini sınar.
func TestSiteLocations_Mapping(t *testing.T) {
	_, inv := mustBuild(t, "rural_ta.yaml")
	locs := SiteLocations(inv)

	if len(locs) != inv.SiteCount() {
		t.Fatalf("konum sayısı = %d, beklenen %d", len(locs), inv.SiteCount())
	}
	for i, s := range inv.Layout.Sites {
		if locs[i].SiteID != s.ID || locs[i].Lat != s.WGS84.Lat || locs[i].Lon != s.WGS84.Lon {
			t.Fatalf("konum[%d] eşleşmiyor", i)
		}
	}
}

// TestRunRowFor_RequiresValidConfig, run satırı üretimini sınar.
func TestRunRowFor_RequiresValidConfig(t *testing.T) {
	scn, inv := mustBuild(t, "urban_ta.yaml")

	row, err := RunRowFor(inv, scn, "sha1")
	if err != nil {
		t.Fatalf("RunRowFor: %v", err)
	}
	if len(row.ConfigJSON) == 0 {
		t.Error("config_yaml boş üretildi")
	}
	if row.RunID != inv.RunID {
		t.Error("run_id eşleşmiyor")
	}
}
