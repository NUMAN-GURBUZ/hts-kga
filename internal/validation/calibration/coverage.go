// T-E04-06 — Kalibrasyon kümesi ve kapsama hesabı (ADR-02).
//
// # Kapsama neden poligonsuz ölçülür
//
// F.1 kapsamayı `ST_Covers(geometry, true_location)` ile ölçer. Kalibrasyon
// döngüsü ise aynı soruyu 12 kez, 5.000 olayda sorar; her seferinde
// MULTIPOLYGON üretip veritabanına yazmak (ve okumak) gerekseydi tek bir λ
// denemesi dakikalar sürerdi.
//
// Kısayol matematiksel olarak **eşdeğerdir**: kontur, seçilen hex hücrelerin
// birleşimidir; bir nokta o birleşimin içindeyse, noktayı içeren hücre
// seçilmiş hücreler kümesindedir. Yani
//
//	ST_Covers(∪ hücreler, p)  ⟺  hücre(p) ∈ seçilen hücreler
//
// Tek fark hücre sınırındaki noktalardır: `grid.At` her noktayı tam bir
// hücreye atar, dolayısıyla sınır belirsizliği yoktur — F.1'in `ST_Covers`
// tercihiyle (sınır dâhil) aynı yönde davranır.
//
// # Kör testle ilişkisi
//
// Bu paket ground truth okur ve okumak **zorundadır**: kalibrasyonun tanımı
// budur (ADR-02). Kör test analiz motorunu korur, doğrulama servisini değil;
// `svc_validation` rolünün ground_truth'a SELECT yetkisi vardır (004_roles).
// Motora giden tek bilgi kanalı λ skaleridir (ADR-25).

package calibration

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/core"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// sample, kalibrasyon kümesindeki tek bir olaydır.
//
// Kayıt tarafı (hücre, TA) ile gerçek konum bir arada tutulur; motor yalnızca
// kayıt tarafını görür, gerçek konum sadece skorlamada kullanılır.
type sample struct {
	eventID uuid.UUID
	cellID  uuid.UUID
	taValue *int
	truth   geo.WGS84
}

// loadSetSQL, kalibrasyon kümesini okur.
//
// `partition_key = 'C'` sorguya gömülüdür: kalibrasyonun doğrulama kümesine
// bakmasının geçerli bir kullanımı yoktur (K4). `covered = TRUE` süzgeci
// F.1 ile aynıdır — kapsama dışı olay için kayıt zaten üretilmez.
const loadSetSQL = `
SELECT h.event_id, h.cell_id, h.ta_value,
       ST_Y(g.true_location::geometry) AS lat,
       ST_X(g.true_location::geometry) AS lon
  FROM hts_records h
  JOIN ground_truth g ON g.run_id = h.run_id AND g.event_id = h.event_id
 WHERE h.run_id = $1::uuid
   AND g.partition_key = 'C'
   AND g.covered = TRUE
 ORDER BY h.time, h.event_id`

// loadCalibrationSet, koşunun 'C' kümesini okur.
//
// Sıra `(time, event_id)`'dir: 12 iterasyon aynı kayıtları aynı sırada
// işlemelidir, yoksa kayan nokta toplamları iterasyonlar arasında ayrışır
// ve λ* sıraya bağlı olurdu (K10).
func loadCalibrationSet(ctx context.Context, db *pgxpool.Pool, runID uuid.UUID) ([]sample, error) {
	rows, err := db.Query(ctx, loadSetSQL, runID.String())
	if err != nil {
		return nil, fmt.Errorf("kalibrasyon kümesi okunamadı (%s): %w", runID, err)
	}
	defer rows.Close()

	var out []sample
	for rows.Next() {
		var s sample
		var taValue *int32
		if err := rows.Scan(&s.eventID, &s.cellID, &taValue, &s.truth.Lat, &s.truth.Lon); err != nil {
			return nil, fmt.Errorf("kalibrasyon satırı okunamadı: %w", err)
		}
		if taValue != nil {
			v := int(*taValue)
			s.taValue = &v
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// coverageAt, verilen λ için kapsama oranını hesaplar.
//
// Her olayda kütle üretilir, hedef güven seviyesinde kontur çıkarılır ve
// gerçek konumun hücresi o konturda mı diye bakılır. Poligon üretilmez.
func (r *Runner) coverageAt(lambda float64) (float64, error) {
	cfg := r.baseConfig
	cfg.Lambda = lambda
	if err := cfg.Validate(); err != nil {
		return 0, fmt.Errorf("λ=%g: %w", lambda, err)
	}

	opts := core.DefaultOptions(cfg, r.scenario.Analysis.NeighborMaxCount)
	covered, evaluated := 0, 0

	for _, s := range r.samples {
		rec := core.Record{
			EventID:    s.eventID,
			CellID:     s.cellID,
			Technology: r.technology,
		}
		if r.taEnabled {
			rec.TAValue = s.taValue
		}

		res, err := core.Estimate(rec, r.inventory, r.grid, opts)
		if err != nil {
			// Bölge boş kalabilir (çok kaba ızgara ya da tutarsız envanter);
			// olay atlanır ama sessizce değil — sayaç farkı raporlanır.
			continue
		}

		contours, err := core.Contours(res.Mass, r.grid, []float64{r.targetConfidence})
		if err != nil {
			continue
		}

		evaluated++
		if containsTruth(contours[0].Cells, r.grid, r.projector.Forward(s.truth)) {
			covered++
		}
	}

	if evaluated == 0 {
		return 0, fmt.Errorf("λ=%g: hiçbir olay değerlendirilemedi", lambda)
	}
	return float64(covered) / float64(evaluated), nil
}

// containsTruth, gerçek konumun konturda olup olmadığını bildirir.
func containsTruth(cells []geo.Axial, grid *density.Grid, truth geo.Point) bool {
	target := grid.At(truth)
	for _, c := range cells {
		if c == target {
			return true
		}
	}
	return false
}

// technologyOf, senaryonun TA teknolojisini çözer.
func technologyOf(enabled bool, name string) (ta.Technology, error) {
	if !enabled {
		return 0, nil
	}
	tech, err := ta.Parse(name)
	if err != nil {
		return 0, fmt.Errorf("kalibrasyon: TA teknolojisi: %w", err)
	}
	return tech, nil
}
