// T-E03-13 — Kütleden `estimates` satırlarına.
//
// Sürücü zincirinin son halkasıdır:
//
//	Kafka/replay → Engine.Process → core.Shapes → EstimateRow → PostgreSQL
//
// # Neden tamponlanır
//
// Olay başına beş satır yazılır. Tek tek INSERT, 300.000 olayda 1,5 milyon
// gidiş-dönüş demektir. Tampon, satırları partiler hâlinde toplayıp tek
// sorguda yazar; boyut olay değil **satır** üzerinden sayılır, çünkü
// veritabanı sorgusunun büyüklüğünü belirleyen odur.
//
// # Tampon Kafka offset'inden önce boşaltılır
//
// Tüketici, her getirmeden sonra offset commit eder. Tampon o an dolu
// kalsaydı, commit'ten sonraki bir çökme yazılmamış tahminleri sessizce
// kaybederdi — `verify_integrity` bunu "eksik estimates" olarak görürdü ama
// veri geri getirilemezdi. `Flush`, tüketici tarafından commit'ten **önce**
// çağrılır.

package driver

import (
	"context"
	"fmt"
	"sync"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/core"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/geometry"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/htswire"
)

// defaultFlushRows, tamponun boşaltılma eşiğidir (satır).
const defaultFlushRows = 200

// EstimateSink, tahminlerin yazıldığı yerdir.
//
// Arayüz tüketici tarafında dar tutulur: testler PostgreSQL olmadan koşar ve
// yazıcı değiştirilebilir (S5'te ölçüm koşusu aynı arayüzü kullanır).
type EstimateSink interface {
	InsertEstimates(ctx context.Context, rows []postgres.EstimateRow) (int64, error)
}

// Persister, üretilen kütleyi `estimates` satırlarına çevirip yazar.
//
// Handler arayüzünü uygular; eşzamanlı tüketici goroutine'lerinden güvenle
// çağrılabilir.
type Persister struct {
	inventory *params.Inventory
	grid      *density.Grid
	levels    []float64
	sink      EstimateSink
	flushRows int

	mu      sync.Mutex
	buffer  []postgres.EstimateRow
	written int64
	events  int64
}

// PersisterConfig, yazıcının kurulum parametreleridir.
type PersisterConfig struct {
	// Inventory, koşunun envanteridir (B0/B1 için sektör parametreleri).
	Inventory *params.Inventory
	// Grid, kütle ızgarasıdır (kontur poligonlaştırması).
	Grid *density.Grid
	// Levels, güven seviyeleridir (analysis.contour_levels).
	Levels []float64
	// Sink, yazma hedefidir.
	Sink EstimateSink
	// FlushRows, tampon eşiğidir; 0 ise varsayılan kullanılır.
	FlushRows int
}

// NewPersister, yazıcıyı kurar.
func NewPersister(cfg PersisterConfig) (*Persister, error) {
	switch {
	case cfg.Inventory == nil:
		return nil, fmt.Errorf("tahmin yazıcısı: envanter zorunlu")
	case cfg.Grid == nil:
		return nil, fmt.Errorf("tahmin yazıcısı: ızgara zorunlu")
	case cfg.Sink == nil:
		return nil, fmt.Errorf("tahmin yazıcısı: yazma hedefi zorunlu")
	case len(cfg.Levels) == 0:
		return nil, fmt.Errorf("tahmin yazıcısı: güven seviyesi listesi boş")
	}

	flush := cfg.FlushRows
	if flush <= 0 {
		flush = defaultFlushRows
	}
	return &Persister{
		inventory: cfg.Inventory,
		grid:      cfg.Grid,
		levels:    append([]float64(nil), cfg.Levels...),
		sink:      cfg.Sink,
		flushRows: flush,
		buffer:    make([]postgres.EstimateRow, 0, flush),
	}, nil
}

// Handle, bir kaydın kütlesini satırlara çevirir ve tampona alır.
func (p *Persister) Handle(ctx context.Context, rec htswire.Record, result core.Result) error {
	// Shapes yalnızca kimliği ve serving hücreyi kullanır; TA'nın etkisi
	// kütleye (result) çoktan işlenmiştir ve ta_used oradan gelir.
	shapes, err := core.Shapes(core.Record{
		EventID: rec.EventID,
		CellID:  rec.CellID,
	}, result, p.inventory, p.grid, p.levels)
	if err != nil {
		return fmt.Errorf("tahmin üretimi: %w", err)
	}

	rows := make([]postgres.EstimateRow, 0, len(shapes))
	for _, s := range shapes {
		rows = append(rows, postgres.EstimateRow{
			RunID:       rec.RunID,
			EventID:     rec.EventID,
			Time:        rec.Time,
			Method:      string(s.Method),
			Confidence:  s.Confidence,
			GeometryWKB: s.Geometry.WKB(),
			CentroidWKB: geometry.PointWKB(s.Centroid),
			AreaKM2:     s.AreaKM2,
			PartCount:   s.PartCount,
			TAUsed:      s.TAUsed,
			Scenario:    rec.Scenario,
		})
	}

	p.mu.Lock()
	p.buffer = append(p.buffer, rows...)
	p.events++
	full := len(p.buffer) >= p.flushRows
	p.mu.Unlock()

	if full {
		return p.Flush(ctx)
	}
	return nil
}

// Flush, tamponu boşaltır. Tüketici bunu offset commit'inden önce çağırır.
func (p *Persister) Flush(ctx context.Context) error {
	p.mu.Lock()
	if len(p.buffer) == 0 {
		p.mu.Unlock()
		return nil
	}
	batch := p.buffer
	p.buffer = make([]postgres.EstimateRow, 0, p.flushRows)
	p.mu.Unlock()

	n, err := p.sink.InsertEstimates(ctx, batch)
	if err != nil {
		return fmt.Errorf("tahmin yazımı (%d satır): %w", len(batch), err)
	}

	p.mu.Lock()
	p.written += n
	p.mu.Unlock()
	return nil
}

// Stats, yazılan satır ve işlenen olay sayısını döndürür.
func (p *Persister) Stats() (events, rows int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.events, p.written
}
