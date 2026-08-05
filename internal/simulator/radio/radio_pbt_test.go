// T-E02-11, T-E02-12 — Gölgeleme alanı ve best-server için özellik tabanlı
// testler (ADR-11).
//
// Yol kaybı ve anten deseni değişmezleri burada değildir: ADR-20 gereği o
// hesaplar internal/rf paketine taşınmıştır ve testleri de oradadır. Bu
// dosyada kalanlar rastgele gerçekleşmeye — gölgeleme ve LOS çekilişine —
// bağlı olan, yani yalnızca simülatörde var olan değişmezlerdir.
package radio

import (
	"math"
	"testing"

	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// genModel, rastgele bir yayılım modeli üretir.
//
// internal/rf kayıt defterinden okur; model seçimi deterministik parametredir,
// gölgelemeden bağımsızdır.
func genModel(t *rapid.T) rf.PathLossModel {
	name := rapid.SampledFrom([]config.PropagationModel{
		config.ModelUMa, config.ModelUMi, config.ModelRMa,
	}).Draw(t, "model")
	m, err := rf.ModelFor(name)
	if err != nil {
		t.Fatalf("rf.ModelFor(%q): %v", name, err)
	}
	return m
}

// genPoint, senaryo alanı ölçeğinde ENU noktası üretir (kırsal yarıçap 20 km).
func genPoint(t *rapid.T, label string) geo.Point {
	return geo.Point{
		X: rapid.Float64Range(-25000, 25000).Draw(t, label+"X"),
		Y: rapid.Float64Range(-25000, 25000).Draw(t, label+"Y"),
	}
}

// genSource, rastgele bir site kaynağı üretir.
func genSource(t *rapid.T) Source {
	m := genModel(t)
	return Source{
		Key:   rapid.Uint64().Draw(t, "sourceKey"),
		ENU:   genPoint(t, "src"),
		Model: m,
	}
}

// genField, rastgele tohumlu bir gölgeleme alanı üretir.
func genField(t *rapid.T) *ShadowingField {
	seed := rapid.Int64().Draw(t, "seed")
	f, err := NewShadowingField(seed, rf.UTHeightM)
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

// TestPBT_SelectCoverageConsistent, kapsama kararının eşikle tutarlı olduğunu
// ve seçimin deterministik kaldığını sınar (ADR-08/3, K10).
func TestPBT_SelectCoverageConsistent(t *testing.T) {
	m, err := rf.ModelFor(config.ModelUMa)
	if err != nil {
		t.Fatalf("rf.ModelFor: %v", err)
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
	field, err := NewShadowingField(7, rf.UTHeightM)
	if err != nil {
		t.Fatalf("NewShadowingField: %v", err)
	}
	sel, err := NewSelector(net, field, -110, rf.UTHeightM)
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
