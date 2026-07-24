package geo

import (
	"math"
	"testing"
)

// senaryo origin'i (configs/*.yaml — Elazığ, temsili)
const (
	testOriginLat = 38.6748
	testOriginLon = 39.2225
)

// ENU yaklaşımı, küresel referans olarak Haversine (haversine.go) ile
// karşılaştırılarak sınanır. Haversine'in kendisi haversine_test.go'da
// bilinen sabit değerlerle bağımsız olarak çapalanmıştır.

func mustProjector(t *testing.T, lat, lon float64) *Projector {
	t.Helper()
	p, err := NewProjector(lat, lon)
	if err != nil {
		t.Fatalf("NewProjector(%g, %g): %v", lat, lon, err)
	}
	return p
}

// TestProjector_OriginMapsToZero, origin'in ENU başlangıcına düştüğünü sınar.
func TestProjector_OriginMapsToZero(t *testing.T) {
	p := mustProjector(t, testOriginLat, testOriginLon)
	got := p.Forward(WGS84{Lat: testOriginLat, Lon: testOriginLon})
	if got.X != 0 || got.Y != 0 {
		t.Errorf("origin (0,0)'a düşmeli, bulunan: %+v", got)
	}
	back := p.Inverse(Point{})
	if back.Lat != testOriginLat || back.Lon != testOriginLon {
		t.Errorf("Inverse(0,0) origin'e dönmeli, bulunan: %+v", back)
	}
}

// TestProjector_AxisOrientation, ENU eksen yönlerini sınar:
// doğu → +X, kuzey → +Y (batı/güney negatif).
func TestProjector_AxisOrientation(t *testing.T) {
	p := mustProjector(t, testOriginLat, testOriginLon)

	east := p.Forward(WGS84{Lat: testOriginLat, Lon: testOriginLon + 0.01})
	if east.X <= 0 {
		t.Errorf("doğu yönü +X olmalı: %+v", east)
	}
	if math.Abs(east.Y) > 1e-9 {
		t.Errorf("saf doğu hareketinde Y sıfır olmalı: %+v", east)
	}

	north := p.Forward(WGS84{Lat: testOriginLat + 0.01, Lon: testOriginLon})
	if north.Y <= 0 {
		t.Errorf("kuzey yönü +Y olmalı: %+v", north)
	}
	if math.Abs(north.X) > 1e-9 {
		t.Errorf("saf kuzey hareketinde X sıfır olmalı: %+v", north)
	}

	sw := p.Forward(WGS84{Lat: testOriginLat - 0.01, Lon: testOriginLon - 0.01})
	if sw.X >= 0 || sw.Y >= 0 {
		t.Errorf("güneybatı her iki eksende negatif olmalı: %+v", sw)
	}
}

// TestProjector_ADR07Constants, ADR-07'de yazılı ölçek katsayılarının
// koda birebir girdiğini sınar.
func TestProjector_ADR07Constants(t *testing.T) {
	p := mustProjector(t, testOriginLat, testOriginLon)

	// 1° enlem = 110_540 m
	oneDegNorth := p.Forward(WGS84{Lat: testOriginLat + 1, Lon: testOriginLon})
	if math.Abs(oneDegNorth.Y-110_540) > 1e-6 {
		t.Errorf("1° enlem = %g m, beklenen 110540", oneDegNorth.Y)
	}

	// 1° boylam = cos(lat0) · 111_320 m
	want := math.Cos(testOriginLat*math.Pi/180) * 111_320
	oneDegEast := p.Forward(WGS84{Lat: testOriginLat, Lon: testOriginLon + 1})
	if math.Abs(oneDegEast.X-want) > 1e-6 {
		t.Errorf("1° boylam = %g m, beklenen %g", oneDegEast.X, want)
	}
}

// TestProjector_RoundTrip, ADR-07 doğrulama gereksinimidir (Bölüm K, birim test
// listesi): WGS84 → ENU → WGS84 ve ENU → WGS84 → ENU çevrimleri kayıpsız olmalı.
//
// Kırsal senaryonun 20 km yarıçapı dâhil taranır.
func TestProjector_RoundTrip(t *testing.T) {
	origins := []WGS84{
		{Lat: testOriginLat, Lon: testOriginLon}, // senaryo origin'i
		{Lat: 0, Lon: 0},                         // ekvator
		{Lat: 60, Lon: -120},                     // yüksek enlem, negatif boylam
		{Lat: -33.9, Lon: 151.2},                 // güney yarımküre
	}

	// −20 km … +20 km aralığında tarama (kırsal profil yarıçapı)
	offsets := []float64{-20000, -5000, -100, -0.5, 0, 0.5, 100, 5000, 20000}

	const tolM = 1e-6 // mikrometre — kayan nokta gürültüsü dışında kayıp yok

	for _, o := range origins {
		p := mustProjector(t, o.Lat, o.Lon)

		for _, dx := range offsets {
			for _, dy := range offsets {
				start := Point{X: dx, Y: dy}

				// ENU → WGS84 → ENU
				back := p.Forward(p.Inverse(start))
				if math.Abs(back.X-start.X) > tolM || math.Abs(back.Y-start.Y) > tolM {
					t.Errorf("origin %+v: ENU round-trip kaybı %+v → %+v", o, start, back)
				}

				// WGS84 → ENU → WGS84 (aynı noktanın coğrafi karşılığından)
				g := p.Inverse(start)
				g2 := p.Inverse(p.Forward(g))
				if math.Abs(g2.Lat-g.Lat) > 1e-12 || math.Abs(g2.Lon-g.Lon) > 1e-12 {
					t.Errorf("origin %+v: WGS84 round-trip kaybı %+v → %+v", o, g, g2)
				}
			}
		}
	}
}

// TestProjector_LinearizationError, ADR-07'nin doğruluk iddiasını sınar.
//
// Eşdikdörtgensel yaklaşımda iki ayrı hata bileşeni vardır:
//
//	(a) sabit eksen ölçek sapması — ADR-07 sabitleri (110540 / 111320) ile
//	    küresel referans arasındaki fark; mesafeden bağımsızdır ve tüm
//	    hesaplarda aynı yönde uygulandığı için iç tutarlılığı bozmaz.
//	(b) doğrusallaştırma (linearization) hatası — origin'den uzaklaştıkça
//	    büyüyen gerçek bozulma. ADR-07 ve risk tablosunun "20 km'de birkaç
//	    metre" iddiası budur.
//
// Test (b) bileşenini yalıtır: aynı azimutta oran mesafeden bağımsız kalmalıdır.
//
// Kabul eşiği ADR-07'nin kendi gerekçesinden türetilir: hata, analiz ızgara
// çözünürlüğünün (100 m) onda birinin altında kalmalıdır. Ölçülen en kötü
// durum, 20 km'de köşegen azimutlarda ~9 m'dir (mesafenin karesiyle büyür);
// eksen yönlerinde sapma sıfırdır.
func TestProjector_LinearizationError(t *testing.T) {
	p := mustProjector(t, testOriginLat, testOriginLon)
	origin := WGS84{Lat: testOriginLat, Lon: testOriginLon}

	// gridResolutionM: configs/*.yaml → analysis.grid_resolution_m
	const gridResolutionM = 100.0
	const maxDeviationM = gridResolutionM / 10 // 10 m

	var worst float64
	var worstAz, worstD float64

	// Her azimut için 1 km'deki ölçek oranı referans alınır; 20 km'ye kadar
	// bu orandan sapma, doğrusallaştırma hatasının ta kendisidir.
	for az := 0.0; az < 360; az += 15 {
		sin, cos := math.Sincos(az * math.Pi / 180)

		ratioAt := func(d float64) float64 {
			pt := Point{X: d * sin, Y: d * cos}
			return Haversine(origin, p.Inverse(pt)) / d
		}

		ref := ratioAt(1000)
		for _, d := range []float64{5000, 10000, 20000} {
			// mutlak sapma (metre) = |oran farkı| · d
			deviationM := math.Abs(ratioAt(d)-ref) * d
			if deviationM > maxDeviationM {
				t.Errorf("azimut %.0f°, d=%.0f m: doğrusallaştırma sapması %.3f m > %.0f m",
					az, d, deviationM, maxDeviationM)
			}
			if deviationM > worst {
				worst, worstAz, worstD = deviationM, az, d
			}
		}
	}

	t.Logf("en kötü doğrusallaştırma sapması: %.3f m (azimut %.0f°, d=%.0f m)",
		worst, worstAz, worstD)
}

// TestProjector_AxisScaleBias, (a) bileşenini görünür kılar: ADR-07 sabitleri
// küresel referansa göre eksen başına sabit bir ölçek sapması taşır.
// Sapma %1'i aşarsa sabitler gözden geçirilmelidir.
func TestProjector_AxisScaleBias(t *testing.T) {
	p := mustProjector(t, testOriginLat, testOriginLon)
	origin := WGS84{Lat: testOriginLat, Lon: testOriginLon}

	const d = 10000
	for _, tc := range []struct {
		name string
		pt   Point
	}{
		{"kuzey", Point{X: 0, Y: d}},
		{"doğu", Point{X: d, Y: 0}},
	} {
		bias := math.Abs(Haversine(origin, p.Inverse(tc.pt))/d - 1)
		if bias > 0.01 {
			t.Errorf("%s ekseni ölçek sapması %%%.3f > %%1", tc.name, bias*100)
		}
		t.Logf("%s ekseni ölçek sapması: %%%.4f", tc.name, bias*100)
	}
}

// TestProjector_InvalidOrigin, geçersiz origin'lerin fail-fast reddedildiğini sınar.
func TestProjector_InvalidOrigin(t *testing.T) {
	tests := []struct {
		name     string
		lat, lon float64
	}{
		{"kuzey kutbu", 90, 0},
		{"kutba yakın", 85.1, 0},
		{"güney kutbu", -90, 0},
		{"NaN enlem", math.NaN(), 0},
		{"NaN boylam", 38, math.NaN()},
		{"boylam > 180", 38, 181},
		{"boylam < -180", 38, -181},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewProjector(tc.lat, tc.lon); err == nil {
				t.Errorf("NewProjector(%g, %g) hata döndürmeliydi", tc.lat, tc.lon)
			}
		})
	}
}

// TestProjector_ValidOriginBoundary, sınır değerlerin kabul edildiğini sınar.
func TestProjector_ValidOriginBoundary(t *testing.T) {
	for _, tc := range []struct{ lat, lon float64 }{
		{85, 180}, {-85, -180}, {0, 0},
	} {
		if _, err := NewProjector(tc.lat, tc.lon); err != nil {
			t.Errorf("NewProjector(%g, %g) kabul edilmeliydi: %v", tc.lat, tc.lon, err)
		}
	}
}

// TestPointOps, düzlemsel yardımcı işlemleri sınar.
func TestPointOps(t *testing.T) {
	a := Point{X: 3, Y: 4}
	b := Point{X: 0, Y: 0}

	if got := a.Norm(); got != 5 {
		t.Errorf("Norm() = %g, beklenen 5 (3-4-5 üçgeni)", got)
	}
	if got := Distance(a, b); got != 5 {
		t.Errorf("Distance() = %g, beklenen 5", got)
	}
	if got := Distance(b, a); got != 5 {
		t.Errorf("Distance simetrik olmalı: %g", got)
	}
	if got := a.Sub(Point{X: 1, Y: 1}); got != (Point{X: 2, Y: 3}) {
		t.Errorf("Sub() = %+v, beklenen {2 3}", got)
	}
	if got := Distance(a, a); got != 0 {
		t.Errorf("aynı noktanın mesafesi 0 olmalı: %g", got)
	}
}

// TestProjector_BatchMatchesSingle, toplu dönüşümlerin tekil dönüşümle
// birebir aynı sonucu verdiğini sınar.
func TestProjector_BatchMatchesSingle(t *testing.T) {
	p := mustProjector(t, testOriginLat, testOriginLon)

	gs := []WGS84{
		{Lat: testOriginLat, Lon: testOriginLon},
		{Lat: testOriginLat + 0.05, Lon: testOriginLon - 0.05},
		{Lat: testOriginLat - 0.1, Lon: testOriginLon + 0.1},
	}

	pts := p.ForwardAll(gs)
	if len(pts) != len(gs) {
		t.Fatalf("ForwardAll uzunluğu = %d, beklenen %d", len(pts), len(gs))
	}
	for i, g := range gs {
		if pts[i] != p.Forward(g) {
			t.Errorf("ForwardAll[%d] tekil Forward'dan farklı", i)
		}
	}

	back := p.InverseAll(pts)
	for i, pt := range pts {
		if back[i] != p.Inverse(pt) {
			t.Errorf("InverseAll[%d] tekil Inverse'ten farklı", i)
		}
	}

	// boş dilim → boş sonuç, panik yok
	if got := p.ForwardAll(nil); len(got) != 0 {
		t.Errorf("ForwardAll(nil) boş dönmeli: %v", got)
	}
	if got := p.InverseAll(nil); len(got) != 0 {
		t.Errorf("InverseAll(nil) boş dönmeli: %v", got)
	}
}

// TestProjector_Origin, origin erişimcisinin config değerini koruduğunu sınar.
func TestProjector_Origin(t *testing.T) {
	p := mustProjector(t, testOriginLat, testOriginLon)
	if got := p.Origin(); got.Lat != testOriginLat || got.Lon != testOriginLon {
		t.Errorf("Origin() = %+v, beklenen {%g %g}", got, testOriginLat, testOriginLon)
	}
}
