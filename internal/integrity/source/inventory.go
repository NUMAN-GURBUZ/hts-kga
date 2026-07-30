// Package source, bütünlük dedektörünün girdi kaynaklarıdır: hücre envanteri
// (bu dosya), Kafka akışı ve veritabanı taraması (ADR-27).
package source

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Cell, bütünlük denetiminin ihtiyaç duyduğu **en küçük** hücre görünümüdür.
//
// # Neden internal/analysis/params kullanılmıyor
//
// O paket analiz motorunun envanteridir: hüzme deseni, tilt, EIRP, yayılım
// modeli, izdüşüm. Bütünlük denetiminin bunların hiçbirine ihtiyacı yoktur —
// kural 1 için kimlik kümesi, kural 2 için site konumu yeter.
//
// Import edilse iki zarar olurdu: bütünlük servisi ızgara ve komşu seçimi
// kodunu da taşırdı (`tests/isolation` bunu yasaklıyor), ve analiz
// katmanındaki bir değişiklik bütünlük ölçümünü sessizce etkileyebilirdi.
type Cell struct {
	ID uuid.UUID
	// Position, site konumunun ENU düzlemindeki karşılığıdır (metre).
	//
	// Kural 2 mesafeleri burada hesaplar. WGS84 yerine ENU seçilmesi ADR-07'nin
	// gerekçesiyle aynıdır: düzlemsel metrede mesafe tek bir çıkarma, enlem
	// ölçek farkı dönüşüm katmanında bir kez ele alınır.
	Position geo.Point
	// RMaxM, link budget ön hesabıdır. Kural 2'nin kanıt gövdesinde yer alır.
	RMaxM float64
}

// Inventory, koşunun hücre envanteridir.
//
// Değişmezdir; kurulduktan sonra okunur. Boyut ~450 hücre (~150 site × 3
// sektör) — kayıt sayısıyla büyümez.
type Inventory struct {
	byID   map[uuid.UUID]Cell
	cells  []Cell
	origin geo.WGS84
}

// CellSource, envanteri sağlayan yüzeydir (Redis istemcisi bunu uygular).
type CellSource interface {
	ScanCells(ctx context.Context, runID uuid.UUID) ([]redis.CellParams, error)
}

// LoadInventory, koşunun envanterini yükler.
//
// Boş envanter **hata**dır: kural 1 her kaydı bulgu sayar ve 298.117 satırlık
// bir yanlış pozitif seli üretir. Fail-fast, ADR-08'in disiplini.
func LoadInventory(
	ctx context.Context, src CellSource, runID uuid.UUID, originLat, originLon float64,
) (*Inventory, error) {
	if src == nil {
		return nil, fmt.Errorf("envanter: kaynak zorunlu")
	}
	if runID == uuid.Nil {
		return nil, fmt.Errorf("envanter: run_id zorunlu (ADR-05)")
	}

	origin := geo.WGS84{Lat: originLat, Lon: originLon}
	projector, err := geo.NewProjector(originLat, originLon)
	if err != nil {
		return nil, fmt.Errorf("envanter: izdüşüm kurulamadı: %w", err)
	}

	raw, err := src.ScanCells(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("envanter yüklenemedi (%s): %w", runID, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf(
			"envanter: koşu %s için hücre yok — kural 1 her kaydı bulgu sayardı", runID)
	}

	inv := &Inventory{
		byID:   make(map[uuid.UUID]Cell, len(raw)),
		cells:  make([]Cell, 0, len(raw)),
		origin: origin,
	}
	for _, c := range raw {
		if c.CellID == uuid.Nil {
			return nil, fmt.Errorf("envanter: hücre kimliği boş")
		}
		if _, dup := inv.byID[c.CellID]; dup {
			return nil, fmt.Errorf("envanter: hücre kimliği yinelendi (%s)", c.CellID)
		}
		cell := Cell{
			ID:       c.CellID,
			Position: projector.Forward(geo.WGS84{Lat: c.Lat, Lon: c.Lon}),
			RMaxM:    c.RMaxM,
		}
		inv.byID[cell.ID] = cell
		inv.cells = append(inv.cells, cell)
	}

	// Kimliğe göre sıralı: envanterin bellek düzeni koşudan koşuya aynı
	// olmalıdır (K10). ScanCells zaten sıralı döndürüyor; burada da garanti
	// edilir ki başka bir kaynak takıldığında değişmez korunsun.
	sort.Slice(inv.cells, func(i, j int) bool {
		return inv.cells[i].ID.String() < inv.cells[j].ID.String()
	})
	return inv, nil
}

// NewInventory, doğrudan hücre listesinden envanter kurar (test ve toplu faz).
func NewInventory(cells []Cell) (*Inventory, error) {
	if len(cells) == 0 {
		return nil, fmt.Errorf("envanter: hücre listesi boş")
	}
	inv := &Inventory{
		byID:  make(map[uuid.UUID]Cell, len(cells)),
		cells: make([]Cell, 0, len(cells)),
	}
	for _, c := range cells {
		if c.ID == uuid.Nil {
			return nil, fmt.Errorf("envanter: hücre kimliği boş")
		}
		if _, dup := inv.byID[c.ID]; dup {
			return nil, fmt.Errorf("envanter: hücre kimliği yinelendi (%s)", c.ID)
		}
		inv.byID[c.ID] = c
		inv.cells = append(inv.cells, c)
	}
	sort.Slice(inv.cells, func(i, j int) bool {
		return inv.cells[i].ID.String() < inv.cells[j].ID.String()
	})
	return inv, nil
}

// Has, hücrenin envanterde bulunup bulunmadığını bildirir (kural 1).
func (inv *Inventory) Has(id uuid.UUID) bool {
	_, ok := inv.byID[id]
	return ok
}

// Cell, hücreyi döndürür (kural 2).
func (inv *Inventory) Cell(id uuid.UUID) (Cell, bool) {
	c, ok := inv.byID[id]
	return c, ok
}

// Len, envanterdeki hücre sayısıdır. Kural 1'in kanıt gövdesine yazılır:
// "kayıt, N hücreli envanterde bulunmayan bir hücreyi beyan ediyor".
func (inv *Inventory) Len() int { return len(inv.cells) }

// Cells, envanterin kimliğe göre sıralı kopyasını döndürür.
func (inv *Inventory) Cells() []Cell {
	out := make([]Cell, len(inv.cells))
	copy(out, inv.cells)
	return out
}

// Origin, izdüşüm başlangıcıdır.
func (inv *Inventory) Origin() geo.WGS84 { return inv.origin }

// Position, hücrenin ENU konumunu döndürür (kural 2).
//
// `detector.CellPositions` arayüzünü uygular.
func (inv *Inventory) Position(id uuid.UUID) (geo.Point, bool) {
	c, ok := inv.byID[id]
	if !ok {
		return geo.Point{}, false
	}
	return c.Position, true
}

// PostgresCellSource, `cells` tablosunu envanter kaynağı olarak sunar.
//
// Redis anahtarları koşu ömürlüdür; `cells` kalıcıdır. Bütünlük denetiminin
// geçmiş bir koşu üzerinde yeniden koşturulabilmesi hata ayıklamada ve ölçüm
// tekrarında normal bir ihtiyaçtır.
type PostgresCellSource struct {
	pool interface {
		SelectCellLocations(ctx context.Context, runID uuid.UUID) ([]postgres.CellRow, error)
	}
}

// NewPostgresCellSource, veritabanı kaynağını sarar.
func NewPostgresCellSource(pool interface {
	SelectCellLocations(ctx context.Context, runID uuid.UUID) ([]postgres.CellRow, error)
}) *PostgresCellSource {
	return &PostgresCellSource{pool: pool}
}

// ScanCells, CellSource arayüzünü uygular.
func (s *PostgresCellSource) ScanCells(ctx context.Context, runID uuid.UUID) ([]redis.CellParams, error) {
	rows, err := s.pool.SelectCellLocations(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := make([]redis.CellParams, 0, len(rows))
	for _, r := range rows {
		out = append(out, redis.CellParams{
			CellID: r.CellID,
			RMaxM:  r.RMaxM,
			Lat:    r.Lat,
			Lon:    r.Lon,
		})
	}
	return out, nil
}

// LoadInventoryWithFallback, envanteri önce Redis'ten, olmazsa PostgreSQL'den
// yükler.
//
// Redis sıcak yoldur (koşu sırasında); veritabanı geçmiş koşular içindir.
// Sıra bilinçlidir: canlı koşuda Redis'i atlamak gereksiz veritabanı yükü olur.
func LoadInventoryWithFallback(
	ctx context.Context, primary, fallback CellSource,
	runID uuid.UUID, originLat, originLon float64, log *slog.Logger,
) (*Inventory, error) {
	if log == nil {
		log = slog.Default()
	}

	inv, err := LoadInventory(ctx, primary, runID, originLat, originLon)
	if err == nil {
		return inv, nil
	}
	if fallback == nil {
		return nil, err
	}

	log.Warn("envanter Redis'te bulunamadı, veritabanına düşülüyor",
		"run_id", runID, "redis_hatası", err)
	return LoadInventory(ctx, fallback, runID, originLat, originLon)
}
