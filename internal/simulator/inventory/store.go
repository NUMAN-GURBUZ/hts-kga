// T-E02-06 — Envanterin PostgreSQL ve Redis'e toplu yüklenmesi.
//
// Bu dosya eşleme (mapping) ve sıralama katmanıdır: depolama paketleri alan
// tiplerini tanımaz, alan paketi de sürücü ayrıntılarını bilmez.
//
// Yükleme sırası önemlidir:
//  1. PostgreSQL — run_config + cells, tek işlemde (kalıcı kaynak)
//  2. Redis      — hücre parametreleri + site GEO indeksi (sıcak yol önbelleği)
//
// Redis kaybolursa PostgreSQL'den yeniden kurulabilir; tersi doğru değildir.
package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
)

// CellWriter, envanterin kalıcı deposudur (PostgreSQL).
// Arayüz burada tanımlanır: tüketici tarafında tanımlamak, sahte (fake)
// uygulamalarla test etmeyi altyapı olmadan mümkün kılar.
type CellWriter interface {
	InsertRunWithCells(ctx context.Context, run postgres.RunRow, cells []postgres.CellRow) (int64, error)
}

// CacheWriter, envanterin sıcak yol önbelleğidir (Redis).
type CacheWriter interface {
	BulkLoadCells(ctx context.Context, runID uuid.UUID, cells []redis.CellParams) error
	BulkLoadSites(ctx context.Context, runID uuid.UUID, sites []redis.SiteLocation) error
}

// Store, envanteri yazacak iki depoyu birlikte tutar.
type Store struct {
	DB    CellWriter
	Cache CacheWriter
}

// Persist, envanteri her iki depoya yazar.
//
// PostgreSQL yazımı başarısız olursa Redis'e hiç dokunulmaz. Redis yazımı
// başarısız olursa PostgreSQL kaydı yerinde kalır (kalıcı kaynak bozulmaz) ve
// hata döner — çağıran koşuyu başlatmamalıdır (fail-fast).
func (s Store) Persist(ctx context.Context, inv *Inventory, scn *config.Scenario, gitSHA string) error {
	if s.DB == nil || s.Cache == nil {
		return fmt.Errorf("envanter yükleme: DB ve önbellek yazıcıları zorunlu")
	}
	if inv == nil || scn == nil {
		return fmt.Errorf("envanter yükleme: envanter ve senaryo zorunlu")
	}

	runRow, err := RunRowFor(inv, scn, gitSHA)
	if err != nil {
		return err
	}

	written, err := s.DB.InsertRunWithCells(ctx, runRow, CellRows(inv))
	if err != nil {
		return fmt.Errorf("envanter PostgreSQL'e yazılamadı: %w", err)
	}
	if written != int64(len(inv.Cells)) {
		return fmt.Errorf("envanter eksik yazıldı: %d/%d satır", written, len(inv.Cells))
	}

	if err := s.Cache.BulkLoadCells(ctx, inv.RunID, CellParams(inv)); err != nil {
		return fmt.Errorf("envanter Redis'e yazılamadı: %w", err)
	}
	if err := s.Cache.BulkLoadSites(ctx, inv.RunID, SiteLocations(inv)); err != nil {
		return fmt.Errorf("site indeksi Redis'e yazılamadı: %w", err)
	}

	slog.Info("envanter yüklendi",
		"run_id", inv.RunID.String(),
		"scenario", scn.Run.Scenario,
		"morphology", string(scn.Profile.Morphology),
		"sites", inv.SiteCount(),
		"cells", inv.CellCount(),
		"effective_isd_m", inv.Layout.EffectiveISDM,
		"r_max_m", inv.MaxRMaxM(),
	)
	return nil
}

// ─── Eşleme (mapping) ─────────────────────────────────────────────────────────

// RunRowFor, koşu meta verisini `run_config` satırına dönüştürür (ADR-05).
// Tam senaryo config'i JSONB olarak saklanır: koşu sonradan yeniden kurulabilir.
func RunRowFor(inv *Inventory, scn *config.Scenario, gitSHA string) (postgres.RunRow, error) {
	configJSON, err := json.Marshal(scn)
	if err != nil {
		return postgres.RunRow{}, fmt.Errorf("senaryo config JSON'a çevrilemedi: %w", err)
	}
	if gitSHA == "" {
		gitSHA = "unknown" // run_config.git_sha NOT NULL
	}

	return postgres.RunRow{
		RunID:      inv.RunID,
		Scenario:   scn.Run.Scenario,
		Morphology: string(scn.Profile.Morphology),
		Seed:       scn.Run.Seed,
		GitSHA:     gitSHA,
		ConfigJSON: configJSON,
	}, nil
}

// CellRows, envanteri `cells` tablosu satırlarına dönüştürür.
func CellRows(inv *Inventory) []postgres.CellRow {
	rows := make([]postgres.CellRow, len(inv.Cells))
	for i, c := range inv.Cells {
		rows[i] = postgres.CellRow{
			CellID:     c.ID,
			RunID:      c.RunID,
			SiteID:     c.SiteID,
			Azimuth:    c.Azimuth,
			BeamWidth:  c.BeamWidth,
			FreqMHz:    c.FreqMHz,
			EIRPdBm:    c.EIRPdBm,
			AntHeightM: c.AntHeightM,
			TiltDeg:    c.TiltDeg,
			RMaxM:      c.RMaxM,
			Morphology: string(c.Morphology),
			ModelType:  string(c.ModelType),
			Lat:        c.Location.Lat,
			Lon:        c.Location.Lon,
		}
	}
	return rows
}

// CellParams, envanteri Redis hücre parametrelerine dönüştürür.
func CellParams(inv *Inventory) []redis.CellParams {
	params := make([]redis.CellParams, len(inv.Cells))
	for i, c := range inv.Cells {
		params[i] = redis.CellParams{
			CellID:     c.ID,
			SiteID:     c.SiteID,
			Azimuth:    c.Azimuth,
			BeamWidth:  c.BeamWidth,
			FreqMHz:    c.FreqMHz,
			EIRPdBm:    c.EIRPdBm,
			AntHeightM: c.AntHeightM,
			TiltDeg:    c.TiltDeg,
			RMaxM:      c.RMaxM,
			Morphology: string(c.Morphology),
			ModelType:  string(c.ModelType),
			Lat:        c.Location.Lat,
			Lon:        c.Location.Lon,
		}
	}
	return params
}

// SiteLocations, site konumlarını Redis GEO indeksi girdilerine dönüştürür.
func SiteLocations(inv *Inventory) []redis.SiteLocation {
	locs := make([]redis.SiteLocation, len(inv.Layout.Sites))
	for i, s := range inv.Layout.Sites {
		locs[i] = redis.SiteLocation{
			SiteID: s.ID,
			Lat:    s.WGS84.Lat,
			Lon:    s.WGS84.Lon,
		}
	}
	return locs
}
