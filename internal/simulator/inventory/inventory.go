// Envanter üretim boru hattı — T-E02-03 → T-E02-04 → T-E02-05 birleşimi.
//
// Build, koşu başında bir kez çağrılır ve sonucu T-E02-06 ile PostgreSQL +
// Redis'e yüklenir.
package inventory

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Inventory, bir koşunun tam şebeke envanteridir.
type Inventory struct {
	RunID  uuid.UUID
	Layout *Layout
	Cells  []Cell
}

// SiteCount, envanterdeki site sayısıdır.
func (inv *Inventory) SiteCount() int { return len(inv.Layout.Sites) }

// CellCount, envanterdeki sektör (hücre) sayısıdır.
func (inv *Inventory) CellCount() int { return len(inv.Cells) }

// MaxRMaxM, envanterdeki en büyük kapsama yarıçapıdır.
// Komşu ön-filtresinde mekânsal sorgu yarıçapı olarak kullanılır (ADR-03).
func (inv *Inventory) MaxRMaxM() float64 {
	var maxR float64
	for _, c := range inv.Cells {
		if c.RMaxM > maxR {
			maxR = c.RMaxM
		}
	}
	return maxR
}

// Build, senaryodan tam envanteri üretir:
//
//	site yerleşimi (T-E02-03) → 3 sektör (T-E02-04) → r_max (T-E02-05) → doğrulama
//
// Üretim baştan sona deterministiktir: aynı run_id + run.seed aynı envanteri
// verir (K10).
func Build(runID uuid.UUID, scn *config.Scenario, proj *geo.Projector) (*Inventory, error) {
	if runID == uuid.Nil {
		return nil, fmt.Errorf("envanter üretimi: run_id zorunlu (ADR-05)")
	}

	layout, err := PlaceSites(runID, scn, proj)
	if err != nil {
		return nil, err
	}

	cells, err := BuildSectors(layout, scn)
	if err != nil {
		return nil, err
	}

	if err := AssignRMax(cells, scn); err != nil {
		return nil, err
	}

	// Şema kısıtları üretim anında denetlenir; toplu yüklemede değil (T-E02-06).
	for _, c := range cells {
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("envanter doğrulama: %w", err)
		}
	}

	return &Inventory{RunID: runID, Layout: layout, Cells: cells}, nil
}
