package core

import (
	"math"
	"testing"

	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/baseline"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/geometry"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
)

// T-E03-15 — Analiz motorunun altı değişmezi (plan BÖLÜM I).
//
//	#1  |Σ mass − 1| < 1e-9
//	#2  area(M@95) ≥ area(M@90) ≥ area(M@50)
//	#3  area(B1) ≤ area(B0)
//	#4  ST_IsValid(geometry) = true
//	#5  part_count ≥ 1
//	#6  coverage_rate ∈ [0,1]
//
// Değişmezler `rapid` ile rastgele yapılandırmalarda sınanır (ADR-11):
// profil, çözünürlük, hücre ve TA değeri her koşuda yeniden çekilir. Örnek
// bazlı testler (estimate_test, contour_test) belirli durumları sabitler;
// burada aranan şey, **hiçbir** girdi kombinasyonunun değişmezi kırmamasıdır.
//
// # #4 neden burada da sınanır
//
// Geometrik geçerliliğin nihai ölçütü PostGIS'in `ST_IsValid`'idir ve o,
// uçtan uca testte her satırda koşar (`repaired` sütunu). Ama PostGIS
// altyapısı olmadan da kırılmayı yakalayabilmek gerekir: burada halkanın
// yapısal geçerliliği sınanır — kapalılık, yönelim, köşe tekrarı ve alanın
// hücre sayısıyla tam orantısı. Kenar izlemesinde bir hata bu dördünden
// en az birini bozar.

// pbtSetup, rastgele bir yapılandırma üretir.
type pbtSetup struct {
	profile    profileSpec
	inventory  *params.Inventory
	grid       *density.Grid
	record     Record
	options    Options
	resolution float64
}

// buildInventories, iki profilin envanterini önceden kurar.
//
// Envanter kurulumu pahalıdır (327 hücre) ve `rapid.T` testing.TB'yi
// karşılamaz; bu yüzden kurulum üretim döngüsünün dışında, bir kez yapılır.
// Rastgelelik hücre/TA/çözünürlük/λ üzerinden gelir.
func buildInventories(t *testing.T) map[string]*params.Inventory {
	t.Helper()
	return map[string]*params.Inventory{
		"kentsel": buildNetwork(t, urbanProfile()),
		"kırsal":  buildNetwork(t, ruralProfile()),
	}
}

// drawSetup, rapid üzerinden yapılandırma çeker.
func drawSetup(t *rapid.T, cache map[string]*params.Inventory) pbtSetup {
	profiles := []profileSpec{urbanProfile(), ruralProfile()}
	p := profiles[rapid.IntRange(0, len(profiles)-1).Draw(t, "profil")]

	inv := cache[p.name]
	if inv == nil {
		t.Fatalf("%s envanteri kurulmamış", p.name)
	}

	// Çözünürlük profile göre makul aralıkta: kentselde ince, kırsalda kaba
	// (ADR-18/6 — 100 m kırsalda olay başına 2 sa 54 dk'lık geçiş demek).
	var resolution float64
	if p.name == "kentsel" {
		resolution = rapid.SampledFrom([]float64{100, 150, 200}).Draw(t, "çözünürlük")
	} else {
		resolution = rapid.SampledFrom([]float64{250, 400, 600}).Draw(t, "çözünürlük")
	}
	grid, err := density.NewGrid(resolution)
	if err != nil {
		t.Fatalf("NewGrid: %v", err)
	}

	cellIndex := rapid.IntRange(0, inv.Len()-1).Draw(t, "hücre")
	cell := inv.Cells()[cellIndex]

	// TA değeri hücrenin r_max'ına sığmalı (PBT #7: imkânsız TA üretilemez).
	var taValue *int
	if rapid.Bool().Draw(t, "ta_var") {
		maxTA := int(cell.RMaxM / 78.12)
		if maxTA > 0 {
			v := rapid.IntRange(1, maxTA).Draw(t, "ta_değeri")
			taValue = &v
		}
	}

	lambda := rapid.Float64Range(0.5, 3.0).Draw(t, "lambda")
	cfg := testConfig(p)
	cfg.Lambda = lambda

	return pbtSetup{
		profile:    p,
		inventory:  inv,
		grid:       grid,
		record:     Record{CellID: cell.ID, TAValue: taValue, Technology: 1},
		options:    DefaultOptions(cfg, rapid.IntRange(0, 8).Draw(t, "komşu_sayısı")),
		resolution: resolution,
	}
}

// TestPBT_AnalysisInvariants, altı değişmezi birlikte sınar.
//
// Hepsi tek bir üretimde denetlenir: bir girdi kombinasyonu bulunduğunda
// hangi değişmezlerin birlikte kırıldığı da görülür — ayrı testlerde bu bilgi
// kaybolurdu.
func TestPBT_AnalysisInvariants(t *testing.T) {
	cache := buildInventories(t)

	rapid.Check(t, func(t *rapid.T) {
		s := drawSetup(t, cache)

		res, err := Estimate(s.record, s.inventory, s.grid, s.options)
		if err != nil {
			// Dilim hiçbir hücreyi kapsamıyorsa (çok kaba ızgara) üretim
			// başarısız olur ve bu meşrudur; değişmez iddiası yoktur.
			t.Skipf("kütle üretilemedi: %v", err)
		}

		// ── #1 Σ mass = 1 ────────────────────────────────────────────────
		total := density.TotalMass(res.Mass)
		if math.Abs(total-1) >= density.MassTolerance {
			t.Fatalf("#1 ihlal: Σ mass = %.17g (|Δ| = %.3e)", total, math.Abs(total-1))
		}

		contours, err := Contours(res.Mass, s.grid, planLevels)
		if err != nil {
			t.Fatalf("Contours: %v", err)
		}

		areas := make([]float64, len(contours))
		for i, c := range contours {
			// ── #6 kütle oranı [0,1] ve hedefi karşılıyor ────────────────
			if c.Mass < 0 || c.Mass > 1+density.MassTolerance {
				t.Fatalf("#6 ihlal: M@%.2f kütle oranı %g, [0,1] dışında", c.Level, c.Mass)
			}
			if c.Mass < c.Level-density.MassTolerance {
				t.Fatalf("#6 ihlal: M@%.2f yalnızca %g kütle topluyor", c.Level, c.Mass)
			}

			mp, err := s.grid.Polygonize(c.Cells)
			if err != nil {
				t.Fatalf("Polygonize (M@%.2f): %v", c.Level, err)
			}

			// ── #5 part_count ≥ 1 ────────────────────────────────────────
			if mp.PartCount() < 1 {
				t.Fatalf("#5 ihlal: M@%.2f part_count = %d", c.Level, mp.PartCount())
			}

			// ── #4 geometri geçerli ──────────────────────────────────────
			checkStructuralValidity(t, mp, "M@"+fmtLevel(c.Level))

			// Alan, hücre sayısıyla tam orantılı olmalı: kenar izlemesinin
			// en hassas denetimi budur.
			want := float64(len(c.Cells)) * s.grid.CellAreaM2()
			if got := mp.AreaM2(); math.Abs(got-want)/want > 1e-9 {
				t.Fatalf("#4 ihlal: M@%.2f alanı %.6f m², %.6f m² beklenir (%d hücre)",
					c.Level, got, want, len(c.Cells))
			}
			areas[i] = mp.AreaM2()
		}

		// ── #2 alan sıralaması ───────────────────────────────────────────
		for i := 1; i < len(areas); i++ {
			if areas[i] < areas[i-1] {
				t.Fatalf("#2 ihlal: area(M@%.2f) = %.3f < area(M@%.2f) = %.3f",
					contours[i].Level, areas[i], contours[i-1].Level, areas[i-1])
			}
		}

		// ── #3 area(B1) ≤ area(B0) ───────────────────────────────────────
		cell, ok := s.inventory.Cell(s.record.CellID)
		if !ok {
			t.Fatalf("hücre envanterde yok")
		}
		sector, err := geometry.NewSector(cell.Site, cell.AzimuthDeg, cell.BeamWidthDeg, cell.RMaxM)
		if err != nil {
			t.Fatalf("NewSector: %v", err)
		}
		b0, err := baseline.B0(cell.Site, cell.RMaxM)
		if err != nil {
			t.Fatalf("B0: %v", err)
		}
		b1, err := baseline.B1(sector)
		if err != nil {
			t.Fatalf("B1: %v", err)
		}
		if b1.AreaM2() > b0.AreaM2() {
			t.Fatalf("#3 ihlal: area(B1) = %.3f > area(B0) = %.3f", b1.AreaM2(), b0.AreaM2())
		}
		checkStructuralValidity(t, b0, "B0")
		checkStructuralValidity(t, b1, "B1")

		// ── #5 taban çizgileri de tek parça ──────────────────────────────
		if b0.PartCount() < 1 || b1.PartCount() < 1 {
			t.Fatalf("#5 ihlal: B0=%d B1=%d parça", b0.PartCount(), b1.PartCount())
		}
	})
}

// checkStructuralValidity, halkaların yapısal geçerliliğini denetler (#4).
//
// PostGIS'in ST_IsValid'i kesişim de arar; burada onun altyapısız karşılığı
// olan dört koşul sınanır: kapalılık, en az üç farklı köşe, doğru yönelim ve
// ardışık köşe tekrarının olmaması.
func checkStructuralValidity(t *rapid.T, mp geometry.MultiPolygon, label string) {
	for i, poly := range mp {
		checkRing(t, poly.Exterior, label, i, -1, true)
		for j, hole := range poly.Holes {
			checkRing(t, hole, label, i, j, false)
		}
	}
}

func checkRing(t *rapid.T, r geometry.Ring, label string, poly, hole int, exterior bool) {
	where := label
	if hole >= 0 {
		where += " deliği"
	}

	if len(r) < 4 {
		t.Fatalf("#4 ihlal: %s (bileşen %d) halkası %d köşe — en az 4 gerekir", where, poly, len(r))
	}
	if r[0] != r[len(r)-1] {
		t.Fatalf("#4 ihlal: %s (bileşen %d) halkası kapalı değil: %v ≠ %v",
			where, poly, r[0], r[len(r)-1])
	}

	area := r.SignedAreaM2()
	if exterior && area <= 0 {
		t.Fatalf("#4 ihlal: %s (bileşen %d) dış halkası saat yönünde (alan %.3f)", where, poly, area)
	}
	if !exterior && area >= 0 {
		t.Fatalf("#4 ihlal: %s (bileşen %d) deliği saat yönünün tersinde (alan %.3f)", where, poly, area)
	}

	for k := 0; k < len(r)-1; k++ {
		if r[k] == r[k+1] {
			t.Fatalf("#4 ihlal: %s (bileşen %d) halkasında tekrar eden köşe (%d): %v",
				where, poly, k, r[k])
		}
	}
}

func fmtLevel(level float64) string {
	switch {
	case level < 0.55:
		return "50%"
	case level < 0.92:
		return "90%"
	default:
		return "95%"
	}
}

// TestPBT_ShapesRowInvariants, satır düzeyindeki değişmezleri sınar:
// şema kısıtlarının (002_hypertables.sql CHECK) hiçbir girdide ihlal
// edilmediğini gösterir.
func TestPBT_ShapesRowInvariants(t *testing.T) {
	cache := buildInventories(t)

	rapid.Check(t, func(t *rapid.T) {
		s := drawSetup(t, cache)

		res, err := Estimate(s.record, s.inventory, s.grid, s.options)
		if err != nil {
			t.Skipf("kütle üretilemedi: %v", err)
		}
		shapes, err := Shapes(s.record, res, s.inventory, s.grid, planLevels)
		if err != nil {
			t.Fatalf("Shapes: %v", err)
		}

		if len(shapes) != 5 {
			t.Fatalf("tahmin sayısı %d, 5 beklenir (ADR-01 oran denetimi)", len(shapes))
		}

		for _, sh := range shapes {
			if !(sh.AreaKM2 > 0) {
				t.Fatalf("%s@%g: area_km2 = %g, CHECK (area_km2 > 0) ihlali",
					sh.Method, sh.Confidence, sh.AreaKM2)
			}
			if sh.PartCount < 1 {
				t.Fatalf("%s@%g: part_count = %d, CHECK (part_count >= 1) ihlali",
					sh.Method, sh.Confidence, sh.PartCount)
			}
			if sh.Method != MethodM && sh.Confidence != SentinelConfidence {
				t.Fatalf("%s: confidence = %g, −1 sentinel beklenir", sh.Method, sh.Confidence)
			}
			if sh.Method == MethodM && !(sh.Confidence > 0 && sh.Confidence < 1) {
				t.Fatalf("M: confidence = %g, (0,1) beklenir", sh.Confidence)
			}
			if (sh.Method == MethodB0 || sh.Method == MethodB1) && sh.TAUsed {
				t.Fatalf("%s: ta_used = true — taban çizgisi TA görmez", sh.Method)
			}

			// Merkez geometrinin sınırlayıcı kutusunda kalmalı.
			checkCentroidInBounds(t, sh)

			// WKB kodu köşe sayısıyla tutarlı olmalı (hacim ölçümünün dayanağı).
			wantBytes := 9
			for _, p := range sh.Geometry {
				wantBytes += 9 + 4 + 16*len(p.Exterior)
				for _, h := range p.Holes {
					wantBytes += 4 + 16*len(h)
				}
			}
			if got := len(sh.Geometry.WKB()); got != wantBytes {
				t.Fatalf("%s@%g: WKB %d bayt, %d beklenir", sh.Method, sh.Confidence, got, wantBytes)
			}
		}
	})
}

// checkCentroidInBounds, merkezin geometrinin sınırlayıcı kutusunda olduğunu
// doğrular.
//
// Çok parçalı bir bölgede merkez parçaların arasına düşebilir; bu yüzden
// "içinde" değil "kutusunda" aranır. Kutunun dışına çıkması, izdüşüm ya da
// merkez hesabının bozukluğuna işaret eder.
func checkCentroidInBounds(t *rapid.T, sh Shape) {
	minLat, maxLat := math.Inf(1), math.Inf(-1)
	minLon, maxLon := math.Inf(1), math.Inf(-1)

	for _, p := range sh.Geometry {
		for _, v := range p.Exterior {
			minLat, maxLat = math.Min(minLat, v.Lat), math.Max(maxLat, v.Lat)
			minLon, maxLon = math.Min(minLon, v.Lon), math.Max(maxLon, v.Lon)
		}
	}

	c := sh.Centroid
	const eps = 1e-9
	if c.Lat < minLat-eps || c.Lat > maxLat+eps || c.Lon < minLon-eps || c.Lon > maxLon+eps {
		t.Fatalf("%s@%g: merkez (%.6f, %.6f) geometrinin kutusunda değil "+
			"[%.6f, %.6f] × [%.6f, %.6f]",
			sh.Method, sh.Confidence, c.Lat, c.Lon, minLat, maxLat, minLon, maxLon)
	}
}
