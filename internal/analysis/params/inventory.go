// Package params, analiz motorunun şebeke envanterini yükler ve kendi alan
// tiplerine çevirir (T-E03-02).
//
// # Neden ayrı bir envanter tipi
//
// Simülatörün envanter tipleri (internal/simulator/inventory) analiz
// tarafından **kullanılamaz**: ADR-20 gereği analiz katmanı
// internal/simulator altındaki hiçbir pakete bağımlı olamaz. Kısıt keyfi
// değildir — o paketlerden birine açılan kapı, gölgeleme alanına da açılır ve
// kör testin kod katmanı delinir.
//
// Bu yüzden envanter Redis'ten **nötr** depolama tipleriyle okunur
// (internal/storage/redis) ve burada analiz tiplerine dönüştürülür. Dönüşüm
// sırasında WGS84 site konumları ENU metrik düzlemine taşınır (ADR-07):
// analizin tüm geometrisi metre cinsindendir.
//
// # Neden envanterin tamamı bellekte
//
// Plan, komşu ön-filtresi için Redis mekânsal bucket önerir (E.2). Bu boyutta
// gereksizdir: ~110 site × 3 sektör ≈ 330 hücre, birkaç yüz kilobayt. Koşu
// başında bir kez okunur, sonrasında ağ gidiş-dönüşü yoktur. Doğrusal tarama
// 330 eleman üzerinde her olayda mikrosaniyeler alır; bir Redis GEO sorgusu
// bundan pahalıdır.
package params

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Cell, analiz motorunun gördüğü sektördür.
//
// Değer tipidir. Simülatörün hücre tipinden farkı, konumun ENU metre olarak
// çözülmüş ve yayılım modelinin bağlanmış olmasıdır; morfoloji metni gibi
// yalnızca veritabanı için anlamlı alanlar taşınmaz.
type Cell struct {
	// ID, sektör kimliğidir (cells.cell_id).
	ID uuid.UUID
	// SiteID, sektörün bağlı olduğu direktir.
	SiteID uuid.UUID
	// Site, direğin ENU konumudur (metre).
	Site geo.Point
	// AzimuthDeg, sektör azimutudur (0 / 120 / 240).
	AzimuthDeg float64
	// BeamWidthDeg, yatay 3 dB hüzme genişliğidir.
	BeamWidthDeg float64
	// TiltDeg, elektriksel aşağı eğimdir.
	TiltDeg float64
	// EIRPdBm, yayılan izotropik güçtür.
	EIRPdBm float64
	// FreqMHz, taşıyıcı frekanstır.
	FreqMHz int
	// AntHeightM, direk anten yüksekliğidir.
	AntHeightM float64
	// RMaxM, kapsama yarıçapı önhesabıdır (T-E02-05).
	RMaxM float64
	// Model, sektörün yayılım modelidir (deterministik, gölgelemesiz).
	Model rf.PathLossModel
}

// Inventory, bir koşunun şebeke envanterinin analiz görünümüdür.
//
// Değişmezdir; eşzamanlı kullanımda güvenlidir.
type Inventory struct {
	cells     []Cell
	index     map[uuid.UUID]int
	maxRMaxM  float64
	origin    geo.WGS84
	projector *geo.Projector
}

// CellSource, envanterin okunacağı kaynaktır.
//
// Arayüz tüketici tarafında tanımlanır: analiz paketi Redis istemcisine değil,
// bu daracık sözleşmeye bağımlıdır ve testler altyapı olmadan koşar.
type CellSource interface {
	ScanCells(ctx context.Context, runID uuid.UUID) ([]redis.CellParams, error)
}

// Load, koşunun envanterini okur ve analiz tiplerine çevirir.
//
// originLat/originLon senaryonun ENU başlangıcıdır (area.origin_*); tüm
// koşuda tek bir başlangıç kullanılır, böylece ızgara koşu boyunca aynı
// noktalara demirlenir (K10).
func Load(ctx context.Context, src CellSource, runID uuid.UUID, originLat, originLon float64) (*Inventory, error) {
	if src == nil {
		return nil, fmt.Errorf("envanter yükleme: kaynak zorunlu")
	}
	if runID == uuid.Nil {
		return nil, fmt.Errorf("envanter yükleme: run_id boş (ADR-05)")
	}

	projector, err := geo.NewProjector(originLat, originLon)
	if err != nil {
		return nil, fmt.Errorf("envanter yükleme: ENU başlangıcı geçersiz: %w", err)
	}

	raw, err := src.ScanCells(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("envanter yükleme: %w", err)
	}
	return build(raw, projector)
}

// build, ham Redis kayıtlarını envantere çevirir.
//
// Ayrı tutulur: testler Redis'e gitmeden bu dönüşümü doğrudan sınayabilir.
func build(raw []redis.CellParams, projector *geo.Projector) (*Inventory, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("envanter yükleme: hücre yok")
	}

	inv := &Inventory{
		cells:     make([]Cell, 0, len(raw)),
		index:     make(map[uuid.UUID]int, len(raw)),
		origin:    projector.Origin(),
		projector: projector,
	}

	for i, p := range raw {
		model, err := rf.ModelFor(config.PropagationModel(p.ModelType))
		if err != nil {
			return nil, fmt.Errorf("envanter yükleme: hücre[%d] (%s) modeli çözülemedi: %w",
				i, p.CellID, err)
		}
		cell := Cell{
			ID:           p.CellID,
			SiteID:       p.SiteID,
			Site:         projector.Forward(geo.WGS84{Lat: p.Lat, Lon: p.Lon}),
			AzimuthDeg:   p.Azimuth,
			BeamWidthDeg: p.BeamWidth,
			TiltDeg:      p.TiltDeg,
			EIRPdBm:      p.EIRPdBm,
			FreqMHz:      p.FreqMHz,
			AntHeightM:   p.AntHeightM,
			RMaxM:        p.RMaxM,
			Model:        model,
		}
		if err := cell.Validate(); err != nil {
			return nil, fmt.Errorf("envanter yükleme: hücre[%d]: %w", i, err)
		}
		if _, dup := inv.index[cell.ID]; dup {
			return nil, fmt.Errorf("envanter yükleme: yinelenen hücre kimliği %s", cell.ID)
		}
		inv.index[cell.ID] = len(inv.cells)
		inv.cells = append(inv.cells, cell)
		if cell.RMaxM > inv.maxRMaxM {
			inv.maxRMaxM = cell.RMaxM
		}
	}

	// Kimliğe göre kararlı sıra: kayan nokta toplama sırası koşudan koşuya
	// değişmesin (K10). ScanCells zaten sıralı döndürür; burada garanti
	// kaynaktan bağımsız hâle gelir.
	sort.Slice(inv.cells, func(i, j int) bool {
		return inv.cells[i].ID.String() < inv.cells[j].ID.String()
	})
	for i := range inv.cells {
		inv.index[inv.cells[i].ID] = i
	}
	return inv, nil
}

// Validate, hücre parametrelerinin analiz için kullanılabilir olduğunu
// denetler.
func (c Cell) Validate() error {
	switch {
	case c.ID == uuid.Nil:
		return fmt.Errorf("hücre kimliği boş")
	case c.Model == nil:
		return fmt.Errorf("hücre %s: yayılım modeli yok", c.ID)
	case !(c.RMaxM > 0):
		return fmt.Errorf("hücre %s: r_max pozitif olmalı (%g)", c.ID, c.RMaxM)
	case !(c.BeamWidthDeg > 0 && c.BeamWidthDeg <= 360):
		return fmt.Errorf("hücre %s: hüzme genişliği (0,360] olmalı (%g)", c.ID, c.BeamWidthDeg)
	case c.FreqMHz <= 0:
		return fmt.Errorf("hücre %s: frekans pozitif olmalı (%d)", c.ID, c.FreqMHz)
	case !(c.AntHeightM > 0):
		return fmt.Errorf("hücre %s: anten yüksekliği pozitif olmalı (%g)", c.ID, c.AntHeightM)
	}
	return nil
}

// Len, envanterdeki sektör sayısını döndürür.
func (inv *Inventory) Len() int { return len(inv.cells) }

// MaxRMaxM, envanterdeki en büyük kapsama yarıçapıdır (komşu ön-filtresi).
func (inv *Inventory) MaxRMaxM() float64 { return inv.maxRMaxM }

// Origin, ENU başlangıcını döndürür.
func (inv *Inventory) Origin() geo.WGS84 { return inv.origin }

// Projector, envanterin ENU izdüşümüdür.
//
// Direk konumları bu izdüşümle ENU'ya taşındı; üretilen geometri de **aynı**
// izdüşümle WGS84'e döndürülmelidir. İkinci bir izdüşüm kurulsaydı (aynı
// başlangıçla bile) sabitlerdeki en küçük fark, saklanan geometriyi envanterle
// tutarsız yapardı.
func (inv *Inventory) Projector() *geo.Projector { return inv.projector }

// Cell, kimliğe göre sektörü döndürür.
func (inv *Inventory) Cell(id uuid.UUID) (Cell, bool) {
	i, ok := inv.index[id]
	if !ok {
		return Cell{}, false
	}
	return inv.cells[i], true
}

// Cells, envanterin kararlı sıralı görünümünü döndürür.
//
// Dönen dilim çağıran tarafından değiştirilmemelidir; envanter değişmezdir.
func (inv *Inventory) Cells() []Cell { return inv.cells }
