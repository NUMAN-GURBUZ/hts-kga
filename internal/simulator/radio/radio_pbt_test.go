// T-E02-10 — Yayılım modelleri için özellik tabanlı testler (ADR-11).
//
// Tablo testleri bilinen noktaları çapalar; buradaki özellikler girdi uzayını
// rastgele tarayarak formüllerin **yapısal** doğruluğunu sınar. Bir katsayı
// yanlış işaretle yazılsaydı tablo testi kaçırabilir, monotonluk özelliği
// yakalardı.
package radio

import (
	"math"
	"testing"

	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// monotonicityTolDB, mesafe ve frekans monotonluğu için kabul edilen paydır.
//
// RMa'da standardın kırılma noktası tutarsızlığı (bkz. rma.go losPathLossDB)
// negatif bir sıçrama bırakır. Sıçramanın analitik büyüklüğü:
//
//	gap ≈ (40 − eğim) / (2·ln10) · ε²  =  4,239 · ε² ,   ε = Δh / d_BP
//
// Sıçrama hem mesafe hem **frekans** ekseninde görünür: d_BP frekansla
// doğru orantılı olduğundan, sabit bir mesafe iki komşu frekans arasında
// dal değiştirebilir (PBT bu durumu 501 ↔ 504 MHz'de yakaladı).
//
// Bu üreteçlerin taradığı uzayda en kötü durum 0,038 dB'dir
// (h_BS = 150 m, h_UT = 1 m, f = 500 MHz — d_BP'nin en küçük olduğu köşe).
// Pay bunun biraz üzerine kurulmuştur.
//
// Projenin kendi geometrisinde (h_BS = 45 m, h_UT = 1,5 m, f ≥ 800 MHz)
// artefakt ≤ 0,0063 dB'dir ve TestBreakpointContinuity bunu 0,01 dB'lik sıkı
// eşikle ayrıca doğrular. Yani gevşek pay yalnızca projenin kullanmadığı
// parametre köşelerini kapsar.
//
// Gerçek bir işaret veya katsayı hatası dB–onlarca dB mertebesinde olurdu;
// 0,05 dB'lik pay bunları yakalamayı engellemez.
const monotonicityTolDB = 0.05

// allModels, kayıtlı üç standart modeli döndürür.
func allModels() []config.PropagationModel {
	return []config.PropagationModel{config.ModelUMa, config.ModelUMi, config.ModelRMa}
}

// genModel, rastgele bir yayılım modeli üretir.
func genModel(t *rapid.T) (PathLossModel, config.PropagationModel) {
	name := rapid.SampledFrom(allModels()).Draw(t, "model")
	m, err := ModelFor(name)
	if err != nil {
		t.Fatalf("ModelFor(%q): %v", name, err)
	}
	return m, name
}

// genFreqMHz, proje ölçeğinde taşıyıcı frekans üretir.
// Aralık, ADR-17 profillerinin band dağılımını kapsar (800–2600 MHz).
func genFreqMHz(t *rapid.T) int {
	return rapid.IntRange(500, 3000).Draw(t, "freqMHz")
}

// genHeights, h_UT < h_BS koşulunu sağlayan anten yükseklikleri üretir.
func genHeights(t *rapid.T) (hBSm, hUTm float64) {
	hUTm = rapid.Float64Range(1.0, 10.0).Draw(t, "hUT")
	hBSm = rapid.Float64Range(hUTm+5, 150).Draw(t, "hBS")
	return hBSm, hUTm
}

// ─── Özellik 1: mesafeyle monotonluk ─────────────────────────────────────────

// TestPBT_PathLossMonotonicInDistance, yol kaybının mesafeyle azalmadığını
// sınar. Fiziksel olarak zorunludur: uzaklaşmak sinyali güçlendiremez.
//
// Bu özellik, iki parçalı eğrinin kırılma noktasında ters yönde sıçramadığını
// da dolaylı olarak doğrular.
func TestPBT_PathLossMonotonicInDistance(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m, name := genModel(t)
		hBSm, hUTm := genHeights(t)
		freq := genFreqMHz(t)
		los := rapid.Bool().Draw(t, "los")

		d1 := rapid.Float64Range(minLinkDistanceM, 20000).Draw(t, "d1")
		delta := rapid.Float64Range(0.001, 20000).Draw(t, "delta")
		d2 := d1 + delta

		link := func(d float64) Link {
			return Link{D2DM: d, HBSm: hBSm, HUTm: hUTm, FreqMHz: freq, LOS: los}
		}

		pl1 := m.PathLossDB(link(d1))
		pl2 := m.PathLossDB(link(d2))

		if pl2 < pl1-monotonicityTolDB {
			t.Fatalf("%s: mesafe artarken yol kaybı azaldı\n"+
				"  d=%.3f m → %.6f dB\n  d=%.3f m → %.6f dB\n"+
				"  h_BS=%.2f h_UT=%.2f f=%d MHz LOS=%v",
				name, d1, pl1, d2, pl2, hBSm, hUTm, freq, los)
		}
	})
}

// ─── Özellik 2: NLOS ≥ LOS ───────────────────────────────────────────────────

// TestPBT_NLOSNotBelowLOS, TR 38.901 max() sözleşmesini sınar.
// Engellenmiş yol, serbest görüş hattından daha az kayıplı olamaz.
func TestPBT_NLOSNotBelowLOS(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m, name := genModel(t)
		hBSm, hUTm := genHeights(t)
		freq := genFreqMHz(t)
		d := rapid.Float64Range(minLinkDistanceM, 20000).Draw(t, "d2D")

		base := Link{D2DM: d, HBSm: hBSm, HUTm: hUTm, FreqMHz: freq}

		losLink, nlosLink := base, base
		losLink.LOS, nlosLink.LOS = true, false

		plLOS := m.PathLossDB(losLink)
		plNLOS := m.PathLossDB(nlosLink)

		if plNLOS < plLOS {
			t.Fatalf("%s: NLOS (%.6f dB) < LOS (%.6f dB) — d=%.1f m, f=%d MHz",
				name, plNLOS, plLOS, d, freq)
		}
	})
}

// ─── Özellik 3: frekansla monotonluk ─────────────────────────────────────────

// TestPBT_PathLossMonotonicInFrequency, yüksek frekansın daha çok
// zayıflatıldığını sınar. Serbest uzay bağıntısının (20·log10 f) doğrudan
// sonucudur ve ADR-17'nin "kentselde yüksek bant, kırsalda düşük bant"
// tercihinin fiziksel gerekçesidir.
func TestPBT_PathLossMonotonicInFrequency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m, name := genModel(t)
		hBSm, hUTm := genHeights(t)
		los := rapid.Bool().Draw(t, "los")
		d := rapid.Float64Range(minLinkDistanceM, 20000).Draw(t, "d2D")

		f1 := rapid.IntRange(500, 2000).Draw(t, "f1")
		f2 := f1 + rapid.IntRange(1, 1000).Draw(t, "deltaF")

		link := func(f int) Link {
			return Link{D2DM: d, HBSm: hBSm, HUTm: hUTm, FreqMHz: f, LOS: los}
		}

		pl1 := m.PathLossDB(link(f1))
		pl2 := m.PathLossDB(link(f2))

		if pl2 < pl1-monotonicityTolDB {
			t.Fatalf("%s: frekans artarken yol kaybı azaldı\n"+
				"  %d MHz → %.6f dB\n  %d MHz → %.6f dB\n  d=%.1f m LOS=%v",
				name, f1, pl1, f2, pl2, d, los)
		}
	})
}

// ─── Özellik 4: çıktı sağlığı ────────────────────────────────────────────────

// TestPBT_PathLossIsFiniteAndPositive, hiçbir girdi bileşiminin NaN, ±Inf veya
// negatif yol kaybı üretmediğini sınar. Negatif yol kaybı "sinyal yolda
// güçlendi" demek olurdu.
func TestPBT_PathLossIsFiniteAndPositive(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m, name := genModel(t)
		hBSm, hUTm := genHeights(t)
		freq := genFreqMHz(t)
		los := rapid.Bool().Draw(t, "los")
		// Sıfıra ve alt sınıra çok yakın mesafeler dâhil
		d := rapid.Float64Range(0, 50000).Draw(t, "d2D")

		pl := m.PathLossDB(Link{D2DM: d, HBSm: hBSm, HUTm: hUTm, FreqMHz: freq, LOS: los})

		switch {
		case math.IsNaN(pl):
			t.Fatalf("%s: NaN yol kaybı (d=%.3f, h_BS=%.2f, h_UT=%.2f, f=%d, LOS=%v)",
				name, d, hBSm, hUTm, freq, los)
		case math.IsInf(pl, 0):
			t.Fatalf("%s: sonsuz yol kaybı (d=%.3f, f=%d)", name, d, freq)
		case pl <= 0:
			t.Fatalf("%s: yol kaybı pozitif olmalı, bulunan %.6f dB (d=%.3f, f=%d)",
				name, pl, d, freq)
		}
	})
}

// TestPBT_PathLossDeterministic, aynı girdinin daima aynı çıktıyı verdiğini
// sınar (K10). Modeller durumsuzdur; gizli bir rastgelelik sızarsa yakalanır.
func TestPBT_PathLossDeterministic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m, name := genModel(t)
		hBSm, hUTm := genHeights(t)
		l := Link{
			D2DM:    rapid.Float64Range(minLinkDistanceM, 20000).Draw(t, "d2D"),
			HBSm:    hBSm,
			HUTm:    hUTm,
			FreqMHz: genFreqMHz(t),
			LOS:     rapid.Bool().Draw(t, "los"),
		}

		first := m.PathLossDB(l)
		for i := 0; i < 3; i++ {
			if got := m.PathLossDB(l); got != first {
				t.Fatalf("%s: deterministik değil (%v vs %v)", name, first, got)
			}
		}
	})
}

// ─── Özellik 5–6: LOS olasılığı ──────────────────────────────────────────────

// TestPBT_LOSProbabilityInUnitInterval, olasılığın [0,1] aralığında kaldığını
// sınar. TR 38.901'in yüksek UT düzeltmesi çarpanı 1'i aşabildiğinden kırpma
// gereklidir; bu özellik kırpmanın çalıştığını doğrular.
func TestPBT_LOSProbabilityInUnitInterval(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m, name := genModel(t)
		d := rapid.Float64Range(0, 50000).Draw(t, "d2D")
		hUTm := rapid.Float64Range(1, 23).Draw(t, "hUT")

		p := m.LOSProbability(d, hUTm)

		if math.IsNaN(p) || p < 0 || p > 1 {
			t.Fatalf("%s: P_LOS(%.2f m, h_UT=%.2f) = %v — [0,1] dışında", name, d, hUTm, p)
		}
	})
}

// TestPBT_LOSProbabilityDecreasesWithDistance, uzaklaştıkça görüş hattı
// olasılığının artmadığını sınar. Fiziksel olarak zorunludur: araya daha çok
// engel girer.
func TestPBT_LOSProbabilityDecreasesWithDistance(t *testing.T) {
	// Kayan nokta payı; olasılık boyutsuz olduğundan çok küçük seçilebilir.
	const tol = 1e-12

	rapid.Check(t, func(t *rapid.T) {
		m, name := genModel(t)
		// h_UT'yi projede kullanılan aralıkta tut: yüksek UT düzeltmesi
		// (yalnızca UMa'da, h_UT > 13 m) tasarım gereği eğriyi yukarı büker.
		hUTm := rapid.Float64Range(1, 13).Draw(t, "hUT")

		d1 := rapid.Float64Range(minLinkDistanceM, 20000).Draw(t, "d1")
		d2 := d1 + rapid.Float64Range(0.001, 20000).Draw(t, "delta")

		p1 := m.LOSProbability(d1, hUTm)
		p2 := m.LOSProbability(d2, hUTm)

		if p2 > p1+tol {
			t.Fatalf("%s: mesafe artarken P_LOS arttı — P(%.2f)=%.9f, P(%.2f)=%.9f",
				name, d1, p1, d2, p2)
		}
	})
}

// TestPBT_D3DNotBelowD2D, eğik mesafenin yatay mesafeden küçük olamayacağını
// sınar (Pisagor'un doğrudan sonucu).
func TestPBT_D3DNotBelowD2D(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		hBSm, hUTm := genHeights(t)
		d2D := rapid.Float64Range(0, 50000).Draw(t, "d2D")

		l := Link{D2DM: d2D, HBSm: hBSm, HUTm: hUTm}
		if d3D := l.D3DM(); d3D < d2D {
			t.Fatalf("d_3D (%.9f) < d_2D (%.9f)", d3D, d2D)
		}
	})
}

// ─── Özellik 9–12: gölgeleme ve LOS alanı (T-E02-11) ─────────────────────────

// genPoint, senaryo alanı ölçeğinde ENU noktası üretir (kırsal yarıçap 20 km).
func genPoint(t *rapid.T, label string) geo.Point {
	return geo.Point{
		X: rapid.Float64Range(-25000, 25000).Draw(t, label+"X"),
		Y: rapid.Float64Range(-25000, 25000).Draw(t, label+"Y"),
	}
}

// genSource, rastgele bir site kaynağı üretir.
func genSource(t *rapid.T) Source {
	m, _ := genModel(t)
	return Source{
		Key:   rapid.Uint64().Draw(t, "sourceKey"),
		ENU:   genPoint(t, "src"),
		Model: m,
	}
}

// genField, rastgele tohumlu bir gölgeleme alanı üretir.
func genField(t *rapid.T) *ShadowingField {
	seed := rapid.Int64().Draw(t, "seed")
	f, err := NewShadowingField(seed, UTHeightM)
	if err != nil {
		t.Fatalf("NewShadowingField(%d): %v", seed, err)
	}
	return f
}

// TestPBT_EnvironmentDeterministic, alanın saf fonksiyon olduğunu sınar (K10).
//
// Gizli bir durum veya zamana bağlı bir bileşen sızarsa burada yakalanır —
// bu, eşzamanlı çalışmada tekrarlanabilirliğin temel güvencesidir.
func TestPBT_EnvironmentDeterministic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f := genField(t)
		src := genSource(t)
		p := genPoint(t, "agent")

		first := f.EnvironmentAt(src, p)
		for i := 0; i < 5; i++ {
			if got := f.EnvironmentAt(src, p); got != first {
				t.Fatalf("deterministik değil: %+v vs %+v", first, got)
			}
		}
	})
}

// TestPBT_ShadowingBounded, gölgelemenin sonlu ve ±8σ içinde kaldığını sınar.
// Bozuk bir hash veya Box-Muller'daki log(0) durumu burada ortaya çıkar.
func TestPBT_ShadowingBounded(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f := genField(t)
		src := genSource(t)
		p := genPoint(t, "agent")

		env := f.EnvironmentAt(src, p)
		sigma := src.Model.ShadowingSigmaDB(env.LOS)
		limit := maxAbsSigmaMultiple * sigma

		switch {
		case math.IsNaN(env.ShadowingDB):
			t.Fatalf("%s: NaN gölgeleme (kaynak %d, konum %+v)", src.Model.Name(), src.Key, p)
		case math.IsInf(env.ShadowingDB, 0):
			t.Fatalf("%s: sonsuz gölgeleme (kaynak %d)", src.Model.Name(), src.Key)
		case math.Abs(env.ShadowingDB) > limit:
			t.Fatalf("%s: |S| = %.4f dB > %.1f dB (8σ, σ=%.2f)",
				src.Model.Name(), math.Abs(env.ShadowingDB), limit, sigma)
		}
	})
}

// TestPBT_StationaryAgentIsConstant, durağan ajan değişmezini sınar:
// konum değişmedikçe ortam da değişmez.
//
// Tasarımın ana gerekçesidir — i.i.d. gölgeleme bu özelliği sağlamazdı ve
// masadaki telefon her tick'te başka hücreye bağlanırdı.
func TestPBT_StationaryAgentIsConstant(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f := genField(t)
		src := genSource(t)
		home := genPoint(t, "home")

		want := f.EnvironmentAt(src, home)
		ticks := rapid.IntRange(2, 200).Draw(t, "ticks")

		for i := 0; i < ticks; i++ {
			if got := f.EnvironmentAt(src, home); got != want {
				t.Fatalf("tick %d: durağan ajanın ortamı değişti %+v → %+v", i, want, got)
			}
		}
	})
}

// TestPBT_UnitOpenInterval, hash → (0,1) dönüşümünün uç değer üretmediğini
// sınar. u = 0 olsaydı Box-Muller'daki log(u) −Inf verirdi.
func TestPBT_UnitOpenInterval(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		h := rapid.Uint64().Draw(t, "hash")
		u := unitOpen(h)

		if !(u > 0 && u < 1) {
			t.Fatalf("unitOpen(%d) = %v — (0,1) aralığı dışında", h, u)
		}
	})
}

// TestPBT_StandardNormalFinite, normal dönüşümünün her hash için sonlu değer
// ürettiğini sınar.
func TestPBT_StandardNormalFinite(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		h := rapid.Uint64().Draw(t, "hash")
		z := standardNormal(h)

		if math.IsNaN(z) || math.IsInf(z, 0) {
			t.Fatalf("standardNormal(%d) = %v", h, z)
		}
	})
}

// TestPBT_SourceKeyDeterministic, site anahtarı türetmesinin kararlı olduğunu
// sınar.
func TestPBT_SourceKeyDeterministic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var id [16]byte
		for i := range id {
			id[i] = rapid.ByteMax(255).Draw(t, "b")
		}

		first := SourceKey(id)
		if got := SourceKey(id); got != first {
			t.Fatalf("SourceKey deterministik değil: %d vs %d", first, got)
		}
	})
}

// ─── Özellik 15–18: anten deseni ve best-server (T-E02-12) ───────────────────

// TestPBT_AntennaAttenuationBounded, anten zayıflamasının [0, A_max]
// aralığında kaldığını sınar. Negatif zayıflama "anten güç üretiyor" demek
// olurdu; A_max üstü ise standardın ön-arka bastırma sınırını ihlal ederdi.
func TestPBT_AntennaAttenuationBounded(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		phi := rapid.Float64Range(-720, 720).Draw(t, "deltaAzimuth")
		elev := rapid.Float64Range(-90, 90).Draw(t, "elevation")
		beam := rapid.Float64Range(10, 180).Draw(t, "beamWidth")
		tilt := rapid.Float64Range(0, 20).Draw(t, "tilt")

		a := AntennaAttenuationDB(phi, elev, beam, tilt)

		if math.IsNaN(a) || a < 0 || a > frontToBackDB {
			t.Fatalf("A(φ=%.2f, θ=%.2f, beam=%.2f, tilt=%.2f) = %v — [0, %g] dışında",
				phi, elev, beam, tilt, a, frontToBackDB)
		}
	})
}

// TestPBT_AntennaSymmetric, yatay desenin hüzme ekseni etrafında simetrik
// olduğunu sınar: A(+φ) = A(−φ).
func TestPBT_AntennaSymmetric(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		phi := rapid.Float64Range(0, 180).Draw(t, "phi")
		beam := rapid.Float64Range(10, 180).Draw(t, "beamWidth")

		left := horizontalAttenuationDB(phi, beam)
		right := horizontalAttenuationDB(-phi, beam)

		if math.Abs(left-right) > 1e-9 {
			t.Fatalf("yatay desen simetrik değil: A(+%.3f) = %.9f, A(−%.3f) = %.9f",
				phi, left, phi, right)
		}
	})
}

// TestPBT_WrapAngleRange, açı indirgemesinin (−180, 180] aralığına
// düşürdüğünü ve tam turların değeri korumasını sınar.
func TestPBT_WrapAngleRange(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		deg := rapid.Float64Range(-100000, 100000).Draw(t, "deg")
		w := WrapAngleDeg(deg)

		if math.IsNaN(w) || w <= -180 || w > 180 {
			t.Fatalf("WrapAngleDeg(%v) = %v — (−180, 180] dışında", deg, w)
		}
		// Tam tur eklemek sonucu değiştirmemeli
		if again := WrapAngleDeg(deg + 360); math.Abs(WrapAngleDeg(again-w)) > 1e-6 {
			t.Fatalf("360° eklemek sonucu değiştirdi: %v vs %v", w, again)
		}
	})
}

// TestPBT_SelectCoverageConsistent, kapsama kararının eşikle tutarlı olduğunu
// ve seçimin deterministik kaldığını sınar (ADR-08/3, K10).
func TestPBT_SelectCoverageConsistent(t *testing.T) {
	m, err := ModelFor(config.ModelUMa)
	if err != nil {
		t.Fatalf("ModelFor: %v", err)
	}
	sites := []Site{
		{
			Source:     Source{Key: 1, ENU: geo.Point{}, Model: m},
			AntHeightM: 25,
			Cells: []Cell{
				{ID: [16]byte{1}, AzimuthDeg: 0, BeamWidthDeg: 65, TiltDeg: 6, EIRPdBm: 58, FreqMHz: 2100, RMaxM: 5000},
				{ID: [16]byte{2}, AzimuthDeg: 120, BeamWidthDeg: 65, TiltDeg: 6, EIRPdBm: 58, FreqMHz: 2100, RMaxM: 5000},
				{ID: [16]byte{3}, AzimuthDeg: 240, BeamWidthDeg: 65, TiltDeg: 6, EIRPdBm: 58, FreqMHz: 2100, RMaxM: 5000},
			},
		},
	}
	net, err := NewNetwork(sites)
	if err != nil {
		t.Fatalf("NewNetwork: %v", err)
	}
	field, err := NewShadowingField(7, UTHeightM)
	if err != nil {
		t.Fatalf("NewShadowingField: %v", err)
	}
	sel, err := NewSelector(net, field, -110, UTHeightM)
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}

	rapid.Check(t, func(t *rapid.T) {
		p := geo.Point{
			X: rapid.Float64Range(-8000, 8000).Draw(t, "x"),
			Y: rapid.Float64Range(-8000, 8000).Draw(t, "y"),
		}

		got := sel.Select(p)

		// Kapsama kararı eşikle tutarlı olmalı
		if want := got.RxDBm >= sel.RxSensitivityDBm(); got.Covered != want {
			t.Fatalf("covered=%v ama rx=%.4f eşik=%.1f", got.Covered, got.RxDBm, sel.RxSensitivityDBm())
		}
		// Kapsama varsa alınan güç sonlu olmalı
		if got.Covered && (math.IsNaN(got.RxDBm) || math.IsInf(got.RxDBm, 0)) {
			t.Fatalf("kapsama var ama rx = %v", got.RxDBm)
		}
		// Determinizm
		if again := sel.Select(p); again != got {
			t.Fatalf("deterministik değil: %+v vs %+v", got, again)
		}
	})
}
