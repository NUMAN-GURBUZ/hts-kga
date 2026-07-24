// TD-01 — Özellik tabanlı testler (Property Based Testing), ADR-11.
//
// Tablo testleri "bildiğimiz durumları" sınar; buradaki özellikler girdi
// uzayını rastgele tarayarak "aklımıza gelmeyen durumları" arar. rapid,
// bir karşı örnek bulduğunda onu otomatik küçültür (shrinking) ve
// yeniden üretilebilir bir tohumla raporlar.
//
// Bu dosya plan Bölüm I'daki birim değişmezlerini kapsar (ENU round-trip,
// axial↔ENU round-trip). Analiz motorunun 8 değişmezi T-E03-15'te,
// simülatörün altın senaryosu T-E02-19'da eklenecektir.
package geo

import (
	"math"
	"testing"

	"pgregory.net/rapid"
)

// ─── Üreteçler (generators) ───────────────────────────────────────────────────

// genResolution, hem site kafesi (~900–3600 m) hem analiz ızgarası (100 m)
// aralığını kapsayan çözünürlük üreteci.
func genResolution(t *rapid.T) float64 {
	return rapid.Float64Range(10, 5000).Draw(t, "resolution")
}

// genAxial, makul bir kafes penceresindeki eksenel koordinat üreteci.
func genAxial(t *rapid.T) Axial {
	return Axial{
		Q: rapid.IntRange(-500, 500).Draw(t, "q"),
		R: rapid.IntRange(-500, 500).Draw(t, "r"),
	}
}

// genOrigin, geçerli izdüşüm origin'i üreteci (kutup sınırının içinde).
func genOrigin(t *rapid.T) WGS84 {
	return WGS84{
		Lat: rapid.Float64Range(-80, 80).Draw(t, "originLat"),
		Lon: rapid.Float64Range(-180, 180).Draw(t, "originLon"),
	}
}

// genWGS84, yeryüzünde herhangi bir koordinat üreteci.
func genWGS84(t *rapid.T, label string) WGS84 {
	return WGS84{
		Lat: rapid.Float64Range(-90, 90).Draw(t, label+"Lat"),
		Lon: rapid.Float64Range(-180, 180).Draw(t, label+"Lon"),
	}
}

// ─── Özellik 1: axial → ENU → axial kayıpsız ─────────────────────────────────

// TestPBT_AxialENURoundTrip, plan Bölüm I birim değişmezi:
// bir hücrenin merkezinden geri dönüldüğünde aynı hücre bulunmalıdır.
//
// Bu, cubeRound'un q+r+s=0 kısıtını koruduğunun da dolaylı kanıtıdır:
// kısıt bozulsaydı geçersiz hücreye düşer, eşitlik kırılırdı.
func TestPBT_AxialENURoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		res := genResolution(t)
		a := genAxial(t)

		g, err := NewHexGrid(res)
		if err != nil {
			t.Fatalf("NewHexGrid(%g): %v", res, err)
		}

		if got := g.At(g.Center(a)); got != a {
			t.Fatalf("round-trip bozuldu: %+v → %+v (res=%g)", a, got, res)
		}
	})
}

// ─── Özellik 2: ENU → WGS84 → ENU kayıpsız ───────────────────────────────────

// TestPBT_ENURoundTrip, ADR-07 dönüşümünün tersinirliğini rastgele origin ve
// rastgele ofsetlerle sınar (tablo testi yalnızca 4 origin × 81 nokta bakıyor).
func TestPBT_ENURoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		o := genOrigin(t)
		pt := Point{
			X: rapid.Float64Range(-50000, 50000).Draw(t, "x"),
			Y: rapid.Float64Range(-50000, 50000).Draw(t, "y"),
		}

		p, err := NewProjector(o.Lat, o.Lon)
		if err != nil {
			t.Fatalf("NewProjector(%g, %g): %v", o.Lat, o.Lon, err)
		}

		back := p.Forward(p.Inverse(pt))

		// Mutlak tolerans: 50 km ölçeğinde kayan nokta gürültüsü ~1e-11 m.
		const tolM = 1e-6
		if math.Abs(back.X-pt.X) > tolM || math.Abs(back.Y-pt.Y) > tolM {
			t.Fatalf("ENU round-trip kaybı: %+v → %+v (origin %+v)", pt, back, o)
		}
	})
}

// ─── Özellik 3: hex kafes komşu mesafesi ─────────────────────────────────────

// TestPBT_NeighborSpacing, pointy-top hex kafesin tanımlayıcı özelliğini
// sınar: altı komşunun **tamamına** merkez-merkez mesafe çözünürlüğe eşittir.
// Kare ızgarada bu özellik sağlanmaz (köşegen komşu √2 kat uzaktır).
func TestPBT_NeighborSpacing(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		res := genResolution(t)
		a := genAxial(t)

		g, err := NewHexGrid(res)
		if err != nil {
			t.Fatalf("NewHexGrid(%g): %v", res, err)
		}

		center := g.Center(a)
		for i, n := range a.Neighbors() {
			d := Distance(center, g.Center(n))
			// Bağıl tolerans: res 10 m ile 5000 m arasında değişiyor.
			if rel := math.Abs(d/res - 1); rel > 1e-12 {
				t.Fatalf("%+v komşu[%d]: mesafe %.9f m, beklenen %.9f m (bağıl %g)",
					a, i, d, res, rel)
			}
		}
	})
}

// ─── Özellik 4: Cover() sözleşmesi ───────────────────────────────────────────

// coverBoundaryTol, Cover'ın sınır karşılaştırması için bağıl paydır.
//
// Cover içeride `dx² + dy² ≤ r²`, test ise `Distance() ≤ r` (math.Hypot)
// kullanır. İkisi matematiksel olarak denk, kayan noktada değil: tam sınıra
// düşen bir hücre birinde geçip diğerinde kalabilir. Ölçüm: 2.000.000 sınır
// noktasında %6 uyuşmazlık, bağıl fark ~3e-16 mertebesinde.
//
// Bu bir kusur değil, iki denk karşılaştırmanın yuvarlama farkıdır; sınırdaki
// hücrenin dâhil olup olmaması zaten keyfîdir. 1e-12 bağıl pay, gözlenen
// farkın ~3500 katıdır — gerçek bir sözleşme ihlali yine de yakalanır.
const coverBoundaryTol = 1e-12

// TestPBT_CoverContract, Cover'ın iki taahhüdünü sınar:
//   - dönen her hücrenin merkezi disk içindedir (sınır payıyla)
//   - hiçbir hücre tekrarlanmaz
//
// Yarıçap, çözünürlüğün katı olarak üretilir: hücre sayısı sınırlı kalır,
// özellik hızlı çalışır.
func TestPBT_CoverContract(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		res := rapid.Float64Range(50, 2000).Draw(t, "resolution")
		ratio := rapid.Float64Range(1, 20).Draw(t, "radius/resolution")
		radius := res * ratio

		center := Point{
			X: rapid.Float64Range(-10000, 10000).Draw(t, "cx"),
			Y: rapid.Float64Range(-10000, 10000).Draw(t, "cy"),
		}

		g, err := NewHexGrid(res)
		if err != nil {
			t.Fatalf("NewHexGrid(%g): %v", res, err)
		}

		cells := g.Cover(center, radius)
		seen := make(map[Axial]bool, len(cells))

		for _, a := range cells {
			if d := Distance(center, g.Center(a)); d > radius*(1+coverBoundaryTol) {
				t.Fatalf("%+v merkezi disk dışında: d=%.9f > r=%.9f (bağıl %.3e)",
					a, d, radius, d/radius-1)
			}
			if seen[a] {
				t.Fatalf("%+v tekrarlandı", a)
			}
			seen[a] = true
		}
	})
}

// ─── Özellik 5–6: Haversine bir metriktir ────────────────────────────────────

// TestPBT_HaversineIsMetric, Haversine'in metrik aksiyomlarını sınar:
// negatif olmama, özdeşlik, simetri. Üçgen eşitsizliği ayrı testte.
func TestPBT_HaversineIsMetric(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genWGS84(t, "a")
		b := genWGS84(t, "b")

		d := Haversine(a, b)

		if math.IsNaN(d) {
			t.Fatalf("NaN mesafe: %+v ↔ %+v", a, b)
		}
		if d < 0 {
			t.Fatalf("negatif mesafe %g: %+v ↔ %+v", d, a, b)
		}
		// Üst sınır: yarım büyük çember
		if max := EarthRadiusM * math.Pi; d > max+1e-6 {
			t.Fatalf("mesafe yarım çemberi aşıyor: %g > %g", d, max)
		}
		// Simetri
		if rev := Haversine(b, a); math.Abs(d-rev) > 1e-9 {
			t.Fatalf("simetri bozuk: H(a,b)=%g, H(b,a)=%g", d, rev)
		}
		// Özdeşlik
		if same := Haversine(a, a); same != 0 {
			t.Fatalf("H(a,a) = %g, beklenen 0", same)
		}
	})
}

// haversineGlobalTol, küresel üçgen eşitsizliği için bağıl paydır.
//
// Haversine formülü antipodal noktalara yakınken kötü koşulludur (h → 1
// olurken asin'in türevi sonsuza gider). Bu, formülün bilinen bir özelliğidir,
// uygulamamızın kusuru değil — düzeltmek Vincenty'ye geçmeyi gerektirirdi.
//
// Ölçüm (3.000.000 hedefli antipodal örnek): en kötü ihlal 0,195 m, bağıl
// 9,7e-09. Pay bunun ~10 katı seçildi. Yani metrik özelliği, 20.015 km'lik
// mesafede 7 anlamlı basamak doğrulukla kanıtlanıyor.
//
// Proje açısından önemsiz: en uzun gerçek mesafemiz kırsal senaryonun 20 km'si.
// Bölgesel ölçekteki sıkı kontrol için aşağıdaki *Regional testine bakınız.
const haversineGlobalTol = 1e-7

// TestPBT_HaversineTriangleInequality, üçgen eşitsizliğini **küresel** ölçekte
// sınar: H(a,c) ≤ H(a,b) + H(b,c). Büyük daire mesafesinin gerçek bir metrik
// olduğunun en güçlü göstergesidir.
func TestPBT_HaversineTriangleInequality(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genWGS84(t, "a")
		b := genWGS84(t, "b")
		c := genWGS84(t, "c")

		direct := Haversine(a, c)
		viaB := Haversine(a, b) + Haversine(b, c)

		if direct > viaB+direct*haversineGlobalTol {
			t.Fatalf("üçgen eşitsizliği ihlali: H(a,c)=%.6f > H(a,b)+H(b,c)=%.6f "+
				"(bağıl %.3e)\n  a=%+v\n  b=%+v\n  c=%+v",
				direct, viaB, (direct-viaB)/direct, a, b, c)
		}
	})
}

// TestPBT_HaversineTriangleInequalityRegional, aynı özelliği **senaryo
// ölçeğinde** (origin çevresinde ~±50 km) sıkı toleransla sınar.
//
// Projenin gerçekte kullandığı aralık budur: bütünlük hız denetimi (Kural 2),
// doğrulama metrikleri ve ENU çapraz kontrolü hep bu ölçekte çalışır. Ölçüm
// 3.000.000 örnekte **tam sıfır** ihlal gösterdi; bu yüzden pay mikrometre
// mertebesinde tutuldu.
func TestPBT_HaversineTriangleInequalityRegional(t *testing.T) {
	// configs/*.yaml origin'i çevresinde ~±0,2° ≈ ±22 km
	const spanDeg = 0.2

	regional := func(t *rapid.T, label string) WGS84 {
		return WGS84{
			Lat: testOriginLat + rapid.Float64Range(-spanDeg, spanDeg).Draw(t, label+"Lat"),
			Lon: testOriginLon + rapid.Float64Range(-spanDeg, spanDeg).Draw(t, label+"Lon"),
		}
	}

	rapid.Check(t, func(t *rapid.T) {
		a, b, c := regional(t, "a"), regional(t, "b"), regional(t, "c")

		direct := Haversine(a, c)
		viaB := Haversine(a, b) + Haversine(b, c)

		const tolM = 1e-6 // mikrometre
		if direct > viaB+tolM {
			t.Fatalf("bölgesel üçgen eşitsizliği ihlali: H(a,c)=%.9f > %.9f\n"+
				"  a=%+v\n  b=%+v\n  c=%+v", direct, viaB, a, b, c)
		}
	})
}
