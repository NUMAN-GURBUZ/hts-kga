package baseline

import (
	"math"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/geometry"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// site, altın senaryonun direk konumudur (ENU başlangıcı).
var site = geo.Point{X: 1234, Y: -567}

func mustSector(t testing.TB, azimuthDeg, beamWidthDeg, rMaxM float64) geometry.Sector {
	t.Helper()
	s, err := geometry.NewSector(site, azimuthDeg, beamWidthDeg, rMaxM)
	if err != nil {
		t.Fatalf("NewSector: %v", err)
	}
	return s
}

// TestB0_AreaMatchesCircle, dairenin alanını πR² ile karşılaştırır.
//
// İçten çizilen çokgen daireden %0,005 küçüktür; tolerans buna göredir.
func TestB0_AreaMatchesCircle(t *testing.T) {
	for _, r := range []float64{500, 5000, 29000} {
		mp, err := B0(site, r)
		if err != nil {
			t.Fatalf("B0: %v", err)
		}

		if mp.PartCount() != 1 {
			t.Errorf("r=%.0f: part_count %d, 1 beklenir", r, mp.PartCount())
		}
		if a := mp[0].Exterior.SignedAreaM2(); a <= 0 {
			t.Errorf("r=%.0f: işaretli alan %.3f, pozitif (CCW) beklenir", r, a)
		}

		want := math.Pi * r * r
		got := mp.AreaM2()
		if rel := (want - got) / want; rel < 0 || rel > 1e-4 {
			t.Errorf("r=%.0f: alan %.1f m², πR²=%.1f m² (bağıl fark %.2e, [0, 1e-4] beklenir)",
				r, got, want, rel)
		}
	}
}

// TestB0_CentroidIsSite, dairenin merkezinin direk olduğunu doğrular (ADR-10).
func TestB0_CentroidIsSite(t *testing.T) {
	mp, err := B0(site, 5000)
	if err != nil {
		t.Fatalf("B0: %v", err)
	}

	c := mp.Centroid()
	if math.Abs(c.X-site.X) > 1e-6 || math.Abs(c.Y-site.Y) > 1e-6 {
		t.Errorf("merkez (%.6f, %.6f), direk (%.1f, %.1f) beklenir", c.X, c.Y, site.X, site.Y)
	}
}

// TestB1_AreaMatchesSector, dilimin alanını (span/360)·πR² ile karşılaştırır.
func TestB1_AreaMatchesSector(t *testing.T) {
	for _, tc := range []struct{ azimuth, beam, r float64 }{
		{0, 65, 5000},
		{120, 65, 5000},
		{240, 90, 20000},
		{45, 30, 1000},
		{0, 359, 5000},
	} {
		mp, err := B1(mustSector(t, tc.azimuth, tc.beam, tc.r))
		if err != nil {
			t.Fatalf("B1: %v", err)
		}

		if mp.PartCount() != 1 {
			t.Errorf("beam=%.0f: part_count %d, 1 beklenir", tc.beam, mp.PartCount())
		}
		if a := mp[0].Exterior.SignedAreaM2(); a <= 0 {
			t.Errorf("beam=%.0f: işaretli alan %.3f, pozitif (CCW) beklenir", tc.beam, a)
		}

		want := tc.beam / 360 * math.Pi * tc.r * tc.r
		got := mp.AreaM2()
		if rel := (want - got) / want; rel < 0 || rel > 1e-4 {
			t.Errorf("azimut=%.0f beam=%.0f r=%.0f: alan %.1f m², %.1f m² beklenir (bağıl fark %.2e)",
				tc.azimuth, tc.beam, tc.r, got, want, rel)
		}
	}
}

// TestB1_CentroidMatchesAnalytic, dilimin poligon merkezini kapalı formülle
// karşılaştırır: d = 2R·sin α / (3α).
//
// İki bağımsız yol aynı noktayı vermelidir; vermezse ya yay ayrıklaştırması ya
// da alan ağırlıklı merkez hesabı bozuktur.
func TestB1_CentroidMatchesAnalytic(t *testing.T) {
	for _, tc := range []struct{ azimuth, beam, r float64 }{
		{0, 65, 5000},
		{90, 65, 5000},
		{200, 120, 12000},
	} {
		sec := mustSector(t, tc.azimuth, tc.beam, tc.r)
		mp, err := B1(sec)
		if err != nil {
			t.Fatalf("B1: %v", err)
		}

		got, want := mp.Centroid(), sec.Centroid()
		if d := geo.Distance(got, want); d > tc.r*1e-4 {
			t.Errorf("azimut=%.0f beam=%.0f: merkez (%.2f, %.2f), analitik (%.2f, %.2f) — fark %.3f m",
				tc.azimuth, tc.beam, got.X, got.Y, want.X, want.Y, d)
		}
	}
}

// TestPBT3_B1AreaAtMostB0, PBT değişmezi 3'tür: area(B1) ≤ area(B0).
//
// Dilim, aynı r_max'lı dairenin alt kümesidir; hüzme 360°'ye yaklaştıkça
// eşitliğe yakınsar.
func TestPBT3_B1AreaAtMostB0(t *testing.T) {
	for _, beam := range []float64{15, 30, 65, 90, 120, 180, 270, 359, 360} {
		for _, r := range []float64{500, 5000, 29000} {
			for _, az := range []float64{0, 65, 137, 250, 359} {
				sec := mustSector(t, az, beam, r)

				b1, err := B1(sec)
				if err != nil {
					t.Fatalf("B1: %v", err)
				}
				b0, err := B0(sec.Site, sec.RMaxM)
				if err != nil {
					t.Fatalf("B0: %v", err)
				}

				if b1.AreaM2() > b0.AreaM2() {
					t.Fatalf("beam=%.0f r=%.0f az=%.0f: area(B1)=%.1f > area(B0)=%.1f",
						beam, r, az, b1.AreaM2(), b0.AreaM2())
				}
			}
		}
	}
}

// TestB1_ContainsAxisPoints, dilimin hüzme ekseni üzerindeki noktaları
// içerdiğini, dışındakileri içermediğini doğrular.
func TestB1_ContainsAxisPoints(t *testing.T) {
	const azimuth, beam, r = 0, 65, 5000
	mp, err := B1(mustSector(t, azimuth, beam, r))
	if err != nil {
		t.Fatalf("B1: %v", err)
	}

	at := func(bearingDeg, dist float64) geo.Point {
		rad := bearingDeg * math.Pi / 180
		return geo.Point{X: site.X + dist*math.Sin(rad), Y: site.Y + dist*math.Cos(rad)}
	}

	cases := []struct {
		bearing, dist float64
		want          bool
	}{
		{0, 2500, true},    // eksen üzerinde, yarıçap içinde
		{30, 2500, true},   // hüzme kenarına yakın (±32,5°)
		{-30, 2500, true},  //
		{40, 2500, false},  // hüzme dışında
		{180, 2500, false}, // arka lob yok
		{0, 6000, false},   // r_max ötesi
	}
	for _, tc := range cases {
		if got := mp.Contains(at(tc.bearing, tc.dist)); got != tc.want {
			t.Errorf("yön %.0f° mesafe %.0f m: içerir=%v, %v beklenir", tc.bearing, tc.dist, got, tc.want)
		}
	}
}

// TestBaseline_Rejects, geçersiz girdileri reddeder.
func TestBaseline_Rejects(t *testing.T) {
	if _, err := B0(site, 0); err == nil {
		t.Error("r_max=0 kabul edildi")
	}
	if _, err := B0(site, math.Inf(1)); err == nil {
		t.Error("r_max=+Inf kabul edildi")
	}
	if _, err := B1(geometry.Sector{Site: site, BeamWidthDeg: 65, RMaxM: 0}); err == nil {
		t.Error("r_max=0 dilim kabul edildi")
	}
	if _, err := B1(geometry.Sector{Site: site, BeamWidthDeg: 0, RMaxM: 1000}); err == nil {
		t.Error("hüzme=0 kabul edildi")
	}
}

// TestBaseline_Deterministic, aynı girdinin bit düzeyinde aynı halkayı
// ürettiğini doğrular (K10).
func TestBaseline_Deterministic(t *testing.T) {
	sec := mustSector(t, 137, 65, 5000)

	want, err := B1(sec)
	if err != nil {
		t.Fatalf("B1: %v", err)
	}
	for i := 0; i < 10; i++ {
		got, err := B1(sec)
		if err != nil {
			t.Fatalf("B1: %v", err)
		}
		if len(got[0].Exterior) != len(want[0].Exterior) {
			t.Fatalf("köşe sayısı ayrıştı: %d ≠ %d", len(got[0].Exterior), len(want[0].Exterior))
		}
		for j := range got[0].Exterior {
			if got[0].Exterior[j] != want[0].Exterior[j] {
				t.Fatalf("köşe %d ayrıştı: %v ≠ %v", j, got[0].Exterior[j], want[0].Exterior[j])
			}
		}
	}
}
