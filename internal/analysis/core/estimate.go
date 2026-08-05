// Package core, analiz motorunun **saf çekirdeğidir** (G7).
//
// Tek bir HTS kaydını, şebeke envanterini ve kalibrasyon parametresini alır;
// olasılık kütlesi döndürür. Kafka, PostgreSQL, Redis, zaman damgası, günlük —
// hiçbirini tanımaz.
//
//	Estimate(kayıt, envanter, ızgara, seçenekler) → map[Axial]float64
//
// # Neden saf
//
// Motor iki ayrı sürücüden çağrılır:
//
//	(a) Kafka consumer (T-E03-01)  — canlı akış
//	(b) DB replay sürücüsü (S5)    — kalibrasyonun 12 iterasyonu, 5.000 olay
//
// Kalibrasyon döngüsü aynı olayları farklı λ ile tekrar tekrar koşar; bunu
// Kafka akışından yapmak mümkün değildir. İki sürücü aynı saf fonksiyonu
// çağırdığı için sonuçları bit düzeyinde aynıdır — K10'un S3 karşılığı budur.
//
// İmzada zaman damgası veya I/O tipi **yoktur** (İ-7): olsaydı iki sürücü
// arasında ince farklar oluşabilirdi.
//
// # Ground truth'a dokunulmaz
//
// Bu paket `ground_truth` tablosunu, `partition_key`'i ve `injected_rule`'u
// tanımaz. Kör test (K6) kod düzeyinde ADR-20 import kısıtıyla korunur.
package core

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/geometry"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// Record, motorun gördüğü HTS kaydıdır.
//
// `hts_records` satırının kütle üretimi için gereken alt kümesidir:
// takma ad, olay tipi ve zaman damgası kütleyi etkilemez, bu yüzden yoktur.
type Record struct {
	// EventID, kaydın kimliğidir (ADR-01). Yalnızca hata iletilerinde kullanılır.
	EventID uuid.UUID
	// CellID, serving hücredir.
	CellID uuid.UUID
	// TAValue, Timing Advance değeridir; nil ise kayıtta TA yoktur.
	TAValue *int
	// Technology, TA çözünürlüğünü belirler (senaryo config'inden).
	Technology ta.Technology
}

// Options, kütle üretiminin ayarlarıdır.
type Options struct {
	// Config, kalibrasyon ve radyo parametreleridir (λ dâhil — T-E03-14).
	Config density.Config
	// NeighbourMaxCount, ADR-03 komşu kümesinin üst sınırıdır
	// (analysis.neighbor_max_count). 0 ise komşu kısıtı uygulanmaz.
	NeighbourMaxCount int
	// IncludeAngular, açısal ağırlığın (w_ang) çarpıma katılıp katılmayacağıdır.
	//
	// Plan E.1 üç çarpanlı formu tanımlar. Ancak hüzme deseni w_rad'ın kapsama
	// çarpanındaki P_s ve w_nbr'ın Δ(p) marjı içinde de yer alır; w_ang'ı ayrıca
	// çarpmak deseni iki kez saymaktır (ADR-18, "Ölçülecek"). Bayrak, iki
	// varyantın aynı kod yoluyla ölçülebilmesi için vardır — karar ölçüme
	// bağlanmıştır, koda gömülü değildir.
	IncludeAngular bool
}

// DefaultOptions, plan E.1'in üç çarpanlı formunu üretir.
func DefaultOptions(cfg density.Config, neighbourMaxCount int) Options {
	return Options{Config: cfg, NeighbourMaxCount: neighbourMaxCount, IncludeAngular: true}
}

// Result, tek bir kaydın kütle çıktısıdır.
type Result struct {
	// Mass, hücre başına olasılık kütlesidir; Σ = 1 ± 1e-9 (PBT #1).
	Mass map[geo.Axial]float64
	// TAUsed, TA bilgisinin gerçekten kullanılıp kullanılmadığıdır.
	// Boş kesişim geri düşüşünde false olur (T-E03-04) → estimates.ta_used.
	TAUsed bool
	// CellCount, bölgedeki ızgara hücresi sayısıdır (başarım izlemesi).
	CellCount int
	// NeighbourCount, kısıtta kullanılan komşu sayısıdır (ADR-03 izlemesi).
	NeighbourCount int
}

// Estimate, tek bir kayıttan olasılık kütlesi üretir.
//
// Adımlar (plan BÖLÜM E.1):
//
//  1. Serving hücre envanterden çözülür
//  2. Sektör dilimi kurulur (azimut ± hüzme/2, yarıçap r_max)
//  3. Dilimi kaplayan ızgara üretilir (koşuya demirli)
//  4. TA halkasıyla örtüşme ağırlıkları hesaplanır (∅ ise TA düşer)
//  5. Komşu kümesi seçilir (bölge başına bir kez)
//  6. Her hücrede w_ang · w_rad · w_nbr çarpılır
//  7. Toplam 1'e normalize edilir
func Estimate(rec Record, inv *params.Inventory, grid *density.Grid, opts Options) (Result, error) {
	if inv == nil || grid == nil {
		return Result{}, fmt.Errorf("kütle üretimi: envanter ve ızgara zorunlu")
	}
	if err := opts.Config.Validate(); err != nil {
		return Result{}, fmt.Errorf("kütle üretimi (olay %s): %w", rec.EventID, err)
	}

	serving, ok := inv.Cell(rec.CellID)
	if !ok {
		return Result{}, fmt.Errorf("kütle üretimi (olay %s): hücre %s envanterde yok",
			rec.EventID, rec.CellID)
	}

	sector, err := geometry.NewSector(serving.Site, serving.AzimuthDeg, serving.BeamWidthDeg, serving.RMaxM)
	if err != nil {
		return Result{}, fmt.Errorf("kütle üretimi (olay %s): %w", rec.EventID, err)
	}

	region := grid.Region(sector)
	if len(region) == 0 {
		return Result{}, fmt.Errorf("kütle üretimi (olay %s): dilim hiçbir ızgara hücresi kapsamıyor "+
			"(r_max=%.1f m, çözünürlük=%.1f m)", rec.EventID, serving.RMaxM, grid.ResolutionM())
	}

	// 4. TA penceresi ve örtüşme ağırlıkları
	window, err := newWindow(rec)
	if err != nil {
		return Result{}, fmt.Errorf("kütle üretimi (olay %s): %w", rec.EventID, err)
	}
	polygons := make([][]geo.Point, len(region))
	for i, a := range region {
		polygons[i] = grid.Corners(a)
	}
	taWeights, effective := window.Weights(polygons, serving.Site)

	// 5. Komşu kümesi — bölge başına bir kez (T-E03-07)
	neighbours := density.SelectNeighbours(inv, serving, sector.Centroid(),
		opts.NeighbourMaxCount, opts.Config.UTHeightM)

	// 6. Ağırlık çarpımı
	weights := make([]float64, len(region))
	for i, a := range region {
		p := grid.Center(a)

		w := density.Radial(serving, p, taWeights[i], opts.Config)
		if w == 0 {
			continue
		}
		if opts.IncludeAngular {
			w *= density.Angular(serving, p, opts.Config.UTHeightM)
		}
		w *= density.Neighbour(serving, neighbours, p, opts.Config)
		weights[i] = w
	}

	// 7. Normalizasyon
	mass, err := density.Normalize(region, weights)
	if err != nil {
		return Result{}, fmt.Errorf("kütle üretimi (olay %s): %w", rec.EventID, err)
	}

	return Result{
		Mass:           mass,
		TAUsed:         effective.Enabled(),
		CellCount:      len(region),
		NeighbourCount: len(neighbours),
	}, nil
}

// newWindow, kayıttaki TA bilgisinden pencere kurar.
func newWindow(rec Record) (geometry.Window, error) {
	if rec.TAValue == nil {
		return geometry.NoWindow(), nil
	}
	return geometry.NewWindow(*rec.TAValue, rec.Technology)
}
