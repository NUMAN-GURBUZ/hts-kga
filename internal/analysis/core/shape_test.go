package core

import (
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// shapesFor, verilen profilde bir kaydın beş tahminini üretir.
func shapesFor(t testing.TB, p profileSpec, resolutionM float64, taValue *int) ([]Shape, *params.Inventory) {
	t.Helper()

	inv := buildNetwork(t, p)
	grid := mustGrid(t, resolutionM)
	rec := record(inv, 0, taValue)

	res, err := Estimate(rec, inv, grid, DefaultOptions(testConfig(p), 8))
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	shapes, err := Shapes(rec, res, inv, grid, planLevels)
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}
	return shapes, inv
}

// TestShapes_FiveRowsPerRecord, olay başına tam beş satır üretildiğini ve
// yöntem/güven eşleşmesinin şemaya uyduğunu doğrular (ADR-01, 002 migration).
func TestShapes_FiveRowsPerRecord(t *testing.T) {
	shapes, _ := shapesFor(t, urbanProfile(), 100, nil)

	if len(shapes) != 5 {
		t.Fatalf("tahmin sayısı %d, 5 beklenir", len(shapes))
	}

	want := []struct {
		method     Method
		confidence float64
	}{
		{MethodB0, SentinelConfidence},
		{MethodB1, SentinelConfidence},
		{MethodM, 0.50},
		{MethodM, 0.90},
		{MethodM, 0.95},
	}
	for i, w := range want {
		if shapes[i].Method != w.method || shapes[i].Confidence != w.confidence {
			t.Errorf("%d. satır (%s, %g), (%s, %g) beklenir",
				i, shapes[i].Method, shapes[i].Confidence, w.method, w.confidence)
		}
	}

	// Şema kısıtları: area_km2 > 0, part_count ≥ 1 (002_hypertables.sql CHECK)
	for _, s := range shapes {
		if !(s.AreaKM2 > 0) {
			t.Errorf("%s@%g: area_km2 %g, pozitif olmalı", s.Method, s.Confidence, s.AreaKM2)
		}
		if s.PartCount < 1 {
			t.Errorf("%s@%g: part_count %d, ≥1 olmalı", s.Method, s.Confidence, s.PartCount)
		}
		if len(s.Geometry) != s.PartCount {
			t.Errorf("%s@%g: geometri %d bileşen, part_count %d",
				s.Method, s.Confidence, len(s.Geometry), s.PartCount)
		}
	}
}

// TestShapes_TAUsedOnlyForModel, taban çizgilerinin TA'yı hiç kullanmadığını
// doğrular.
func TestShapes_TAUsedOnlyForModel(t *testing.T) {
	shapes, _ := shapesFor(t, urbanProfile(), 100, intPtr(12))

	for _, s := range shapes {
		switch s.Method {
		case MethodB0, MethodB1:
			if s.TAUsed {
				t.Errorf("%s satırında ta_used=true — taban çizgisi TA görmez", s.Method)
			}
		case MethodM:
			if !s.TAUsed {
				t.Errorf("M@%g satırında ta_used=false — geçerli TA'lı kayıt beklenirdi", s.Confidence)
			}
		}
	}
}

// TestShapes_AreaOrdering, PBT #2 ve #3'ü satır düzeyinde doğrular.
func TestShapes_AreaOrdering(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    profileSpec
		res  float64
		ta   *int
	}{
		{"kentsel TA'sız", urbanProfile(), 100, nil},
		{"kentsel TA'lı", urbanProfile(), 100, intPtr(12)},
		{"kırsal TA'sız", ruralProfile(), 250, nil},
		{"kırsal TA'lı", ruralProfile(), 250, intPtr(30)},
	} {
		shapes, _ := shapesFor(t, tc.p, tc.res, tc.ta)

		b0, b1 := shapes[0].AreaKM2, shapes[1].AreaKM2
		if b1 > b0 {
			t.Errorf("%s: area(B1)=%.4f > area(B0)=%.4f — PBT #3", tc.name, b1, b0)
		}

		m := shapes[2:]
		for i := 1; i < len(m); i++ {
			if m[i].AreaKM2 < m[i-1].AreaKM2 {
				t.Errorf("%s: area(M@%g)=%.4f < area(M@%g)=%.4f — PBT #2",
					tc.name, m[i].Confidence, m[i].AreaKM2, m[i-1].Confidence, m[i-1].AreaKM2)
			}
		}

		t.Logf("%s: B0 %.4f km² · B1 %.4f km² · M@50 %.4f · M@90 %.4f · M@95 %.4f km²",
			tc.name, b0, b1, m[0].AreaKM2, m[1].AreaKM2, m[2].AreaKM2)
	}
}

// TestShapes_SphericalAreaMatchesPlanar, küresel alan ile ENU düzlemsel
// alanının ölçek sapması kadar (≈%1) ayrıştığını doğrular.
//
// Bu, PostGIS `ST_Area(geography)` çapraz kontrolünün (KAPI 2) Go tarafındaki
// önizlemesidir: iki hesap **aynı köşelerden** farklı yollarla alan üretir.
// Fark ADR-07'nin ölçek sabitlerinden gelir ve %1'i aşmamalıdır; aşarsa ya
// izdüşüm ya da küresel formül bozuktur.
func TestShapes_SphericalAreaMatchesPlanar(t *testing.T) {
	p := urbanProfile()
	inv := buildNetwork(t, p)
	grid := mustGrid(t, 100)
	rec := record(inv, 0, nil)

	res, err := Estimate(rec, inv, grid, DefaultOptions(testConfig(p), 8))
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	shapes, err := Shapes(rec, res, inv, grid, planLevels)
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}

	contours, err := Contours(res.Mass, grid, planLevels)
	if err != nil {
		t.Fatalf("Contours: %v", err)
	}

	for i, c := range contours {
		mp, err := grid.Polygonize(c.Cells)
		if err != nil {
			t.Fatalf("Polygonize: %v", err)
		}
		planar := mp.AreaM2() / 1e6
		spherical := shapes[2+i].AreaKM2
		rel := (spherical - planar) / planar

		t.Logf("M@%.0f%%: düzlemsel %.5f km², küresel %.5f km² (fark %+.3f%%)",
			c.Level*100, planar, spherical, rel*100)

		if math.Abs(rel) > 0.01 {
			t.Errorf("M@%.0f%%: küresel/düzlemsel alan farkı %+.3f%%, ±%%1 beklenir",
				c.Level*100, rel*100)
		}
	}
}

// TestShapes_CentroidNearRegion, merkezlerin makul yerde olduğunu doğrular:
// B0'ın merkezi direk, B1'inki hüzme ekseni üzerinde, M'ninki kütle bölgesinde.
func TestShapes_CentroidNearRegion(t *testing.T) {
	shapes, inv := shapesFor(t, urbanProfile(), 100, nil)

	cell := inv.Cells()[0] // record() envanterin ilk hücresini kullanır
	projector := inv.Projector()
	siteWGS := projector.Inverse(cell.Site)

	if d := geo.Haversine(shapes[0].Centroid, siteWGS); d > 1 {
		t.Errorf("B0 merkezi direkten %.3f m uzakta, ≤1 m beklenir", d)
	}

	// B1'in merkezi eksen üzerinde, 0,63·r_max mesafede (kapalı form).
	b1Dist := geo.Haversine(shapes[1].Centroid, siteWGS)
	if b1Dist < 0.5*cell.RMaxM || b1Dist > 0.75*cell.RMaxM {
		t.Errorf("B1 merkezi direkten %.1f m uzakta, ~%.1f m beklenir",
			b1Dist, 0.63*cell.RMaxM)
	}

	// M merkezleri dilim içinde kalmalı.
	for _, s := range shapes[2:] {
		if d := geo.Haversine(s.Centroid, siteWGS); d > cell.RMaxM {
			t.Errorf("M@%g merkezi direkten %.1f m uzakta, r_max=%.1f aşıldı",
				s.Confidence, d, cell.RMaxM)
		}
	}
}

// TestShapes_Deterministic, aynı girdinin bit düzeyinde aynı satırları
// ürettiğini doğrular (K10).
func TestShapes_Deterministic(t *testing.T) {
	p := urbanProfile()
	inv := buildNetwork(t, p)
	grid := mustGrid(t, 100)
	rec := record(inv, 3, intPtr(9))

	res, err := Estimate(rec, inv, grid, DefaultOptions(testConfig(p), 8))
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	want, err := Shapes(rec, res, inv, grid, planLevels)
	if err != nil {
		t.Fatalf("Shapes: %v", err)
	}

	for iter := 0; iter < 5; iter++ {
		res2, err := Estimate(rec, inv, grid, DefaultOptions(testConfig(p), 8))
		if err != nil {
			t.Fatalf("iter %d: Estimate: %v", iter, err)
		}
		got, err := Shapes(rec, res2, inv, grid, planLevels)
		if err != nil {
			t.Fatalf("iter %d: Shapes: %v", iter, err)
		}

		for i := range want {
			if got[i].AreaKM2 != want[i].AreaKM2 {
				t.Fatalf("iter %d: %s@%g alanı ayrıştı: %.17g ≠ %.17g",
					iter, got[i].Method, got[i].Confidence, got[i].AreaKM2, want[i].AreaKM2)
			}
			if got[i].Centroid != want[i].Centroid {
				t.Fatalf("iter %d: %s@%g merkezi ayrıştı: %v ≠ %v",
					iter, got[i].Method, got[i].Confidence, got[i].Centroid, want[i].Centroid)
			}
			if got[i].PartCount != want[i].PartCount {
				t.Fatalf("iter %d: %s@%g part_count ayrıştı: %d ≠ %d",
					iter, got[i].Method, got[i].Confidence, got[i].PartCount, want[i].PartCount)
			}
			if len(got[i].Geometry) != len(want[i].Geometry) {
				t.Fatalf("iter %d: %s@%g bileşen sayısı ayrıştı", iter, got[i].Method, got[i].Confidence)
			}
			for c := range got[i].Geometry {
				gr, wr := got[i].Geometry[c].Exterior, want[i].Geometry[c].Exterior
				if len(gr) != len(wr) {
					t.Fatalf("iter %d: %s@%g halka uzunluğu ayrıştı", iter, got[i].Method, got[i].Confidence)
				}
				for v := range gr {
					if gr[v] != wr[v] {
						t.Fatalf("iter %d: %s@%g köşe %d ayrıştı: %v ≠ %v",
							iter, got[i].Method, got[i].Confidence, v, gr[v], wr[v])
					}
				}
			}
		}
	}
}

// TestShapes_Rejects, geçersiz girdileri reddeder.
func TestShapes_Rejects(t *testing.T) {
	p := urbanProfile()
	inv := buildNetwork(t, p)
	grid := mustGrid(t, 100)
	rec := record(inv, 0, nil)

	res, err := Estimate(rec, inv, grid, DefaultOptions(testConfig(p), 8))
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}

	if _, err := Shapes(rec, res, nil, grid, planLevels); err == nil {
		t.Error("nil envanter kabul edildi")
	}
	if _, err := Shapes(rec, res, inv, nil, planLevels); err == nil {
		t.Error("nil ızgara kabul edildi")
	}
	if _, err := Shapes(rec, res, inv, grid, nil); err == nil {
		t.Error("boş seviye listesi kabul edildi")
	}

	unknown := rec
	unknown.CellID = uuid.New()
	if _, err := Shapes(unknown, res, inv, grid, planLevels); err == nil {
		t.Error("envanterde olmayan hücre kabul edildi")
	}
}
