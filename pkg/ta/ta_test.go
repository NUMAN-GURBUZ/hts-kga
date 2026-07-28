package ta

import (
	"math"
	"testing"

	"pgregory.net/rapid"
)

const eps = 1e-9

// TestResolutionsFrozen, plan BÖLÜM C.1'deki çözünürlükleri korur.
func TestResolutionsFrozen(t *testing.T) {
	if LTE.ResolutionM() != 78.12 {
		t.Errorf("LTE çözünürlüğü %g, beklenen 78.12", LTE.ResolutionM())
	}
	if GSM.ResolutionM() != 550.0 {
		t.Errorf("GSM çözünürlüğü %g, beklenen 550", GSM.ResolutionM())
	}
	if Unknown.ResolutionM() != 0 {
		t.Errorf("tanımsız teknoloji için çözünürlük %g, 0 olmalı", Unknown.ResolutionM())
	}
	if Unknown.Valid() {
		t.Error("tanımsız teknoloji geçerli sayıldı")
	}
}

// TestParse, config metninden dönüşümü kapsar.
func TestParse(t *testing.T) {
	valid := map[string]Technology{
		"LTE": LTE, "lte": LTE, " LTE ": LTE,
		"GSM": GSM, "gsm": GSM,
	}
	for in, want := range valid {
		got, err := Parse(in)
		if err != nil {
			t.Errorf("Parse(%q): beklenmeyen hata: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %v, beklenen %v", in, got, want)
		}
	}
	for _, in := range []string{"", "UMTS", "5G", "lte0"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) hata vermedi", in)
		}
	}
}

// TestFromDistanceReference, elle hesaplanmış referans değerlerle karşılaştırır.
//
// LTE (res = 78,12 m):  floor(d / 78,12)
// GSM (res = 550 m)  :  floor(d / 550)
func TestFromDistanceReference(t *testing.T) {
	cases := []struct {
		d    float64
		tech Technology
		want int
	}{
		{0, LTE, 0},
		{1, LTE, 0},
		{78.11, LTE, 0},   // adımın hemen altı
		{100, LTE, 1},     // 100 / 78,12 = 1,280
		{200, LTE, 2},     // 200 / 78,12 = 2,560
		{1000, LTE, 12},   // 1000 / 78,12 = 12,801
		{5000, LTE, 64},   // 5000 / 78,12 = 64,004  (kentsel r_max)
		{0, GSM, 0},       //
		{549.9, GSM, 0},   // adımın hemen altı
		{1000, GSM, 1},    // 1000 / 550 = 1,818
		{5000, GSM, 9},    // 5000 / 550 = 9,090
		{20000, GSM, 36},  // 20000 / 550 = 36,363 (kırsal r_max)
		{20000, LTE, 256}, // 20000 / 78,12 = 256,016
	}

	for _, tc := range cases {
		got, err := FromDistance(tc.d, tc.tech)
		if err != nil {
			t.Errorf("FromDistance(%g, %v): beklenmeyen hata: %v", tc.d, tc.tech, err)
			continue
		}
		if got != tc.want {
			t.Errorf("FromDistance(%g, %v) = %d, beklenen %d", tc.d, tc.tech, got, tc.want)
		}
	}
}

// TestFromDistanceExactStepBoundaries, adım sınırlarının alt uca dâhil
// olduğunu denetler.
//
// Sınır değerleri ondalık yazımla değil, n·res çarpımıyla üretilir: 78,12
// ikili tabanda tam gösterilemez, bu yüzden "156.24" yazımı 2·78,12'den bir
// ULP farklı olabilir. Simülatör ve analiz aynı çarpımı kullandığından ikisi
// arasında sapma doğmaz; test de aynı yolu izler.
func TestFromDistanceExactStepBoundaries(t *testing.T) {
	for _, tech := range []Technology{LTE, GSM} {
		res := tech.ResolutionM()
		for n := 0; n < 40; n++ {
			at := float64(n) * res
			if got, _ := FromDistance(at, tech); got != n {
				t.Errorf("%v: d = %d·res = %g → %d, beklenen %d", tech, n, at, got, n)
			}
			just := math.Nextafter(at, 0) // sınırın hemen altı
			want := n - 1
			if n == 0 {
				continue // 0'ın altı geçersiz mesafedir
			}
			if got, _ := FromDistance(just, tech); got != want {
				t.Errorf("%v: d = %g (sınırın hemen altı) → %d, beklenen %d",
					tech, just, got, want)
			}
		}
	}
}

// TestFromDistanceRejectsInvalid, geçersiz girdileri kapsar.
func TestFromDistanceRejectsInvalid(t *testing.T) {
	bad := []struct {
		d    float64
		tech Technology
	}{
		{-1, LTE},
		{math.NaN(), LTE},
		{math.Inf(1), LTE},
		{100, Unknown},
		{100, Technology(9)},
	}
	for _, tc := range bad {
		if _, err := FromDistance(tc.d, tc.tech); err == nil {
			t.Errorf("FromDistance(%g, %v) hata vermedi", tc.d, tc.tech)
		}
	}
}

// TestNewRingReference, halka sınırlarını elle hesaplanmış değerlerle
// karşılaştırır.
func TestNewRingReference(t *testing.T) {
	cases := []struct {
		ta                 int
		tech               Technology
		wantInner, wantOut float64
	}{
		{0, LTE, 0, 78.12},
		{1, LTE, 78.12, 156.24},
		{64, LTE, 4999.68, 5077.80},
		{0, GSM, 0, 550},
		{36, GSM, 19800, 20350},
	}

	for _, tc := range cases {
		r, err := NewRing(tc.ta, tc.tech)
		if err != nil {
			t.Errorf("NewRing(%d, %v): beklenmeyen hata: %v", tc.ta, tc.tech, err)
			continue
		}
		if math.Abs(r.InnerM-tc.wantInner) > eps || math.Abs(r.OuterM-tc.wantOut) > eps {
			t.Errorf("NewRing(%d, %v) = [%g, %g), beklenen [%g, %g)",
				tc.ta, tc.tech, r.InnerM, r.OuterM, tc.wantInner, tc.wantOut)
		}
		if w := r.WidthM(); math.Abs(w-tc.tech.ResolutionM()) > eps {
			t.Errorf("NewRing(%d, %v) kalınlığı %g, beklenen %g",
				tc.ta, tc.tech, w, tc.tech.ResolutionM())
		}
	}
}

// TestNewRingRejectsInvalid, geçersiz girdileri kapsar.
func TestNewRingRejectsInvalid(t *testing.T) {
	if _, err := NewRing(-1, LTE); err == nil {
		t.Error("negatif TA kabul edildi")
	}
	if _, err := NewRing(0, Unknown); err == nil {
		t.Error("tanımsız teknoloji kabul edildi")
	}
}

// TestRingContainsBoundaries, alt ucun dâhil, üst ucun hariç olduğunu
// denetler — floor tanımının geometrik karşılığı.
func TestRingContainsBoundaries(t *testing.T) {
	r, err := NewRing(3, LTE)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}

	if !r.Contains(r.InnerM) {
		t.Error("iç sınır halkaya dâhil olmalı")
	}
	if r.Contains(r.OuterM) {
		t.Error("dış sınır halkaya dâhil olmamalı")
	}
	if !r.Contains(math.Nextafter(r.OuterM, 0)) {
		t.Error("dış sınırın hemen altı halkada olmalı")
	}
	if r.Contains(math.Nextafter(r.InnerM, 0)) {
		t.Error("iç sınırın hemen altı halkada olmamalı")
	}
}

// TestRoundTrip, iki yönün birbirinin tersi olduğunu denetler: simülatörün
// bir mesafeden ürettiği TA'nın halkası, o mesafeyi **her zaman** içerir.
//
// Bu, paketin var oluş nedeninin doğrudan testidir. Düşerse simülatör ile
// analiz TA konusunda ayrışmış demektir.
func TestRoundTrip(t *testing.T) {
	for _, tech := range []Technology{LTE, GSM} {
		for d := 0.0; d <= 20000.0; d += 3.7 {
			taValue, err := FromDistance(d, tech)
			if err != nil {
				t.Fatalf("FromDistance(%g, %v): %v", d, tech, err)
			}
			r, err := NewRing(taValue, tech)
			if err != nil {
				t.Fatalf("NewRing(%d, %v): %v", taValue, tech, err)
			}
			if !r.Contains(d) {
				t.Fatalf("%v: d = %g, ta = %d, halka = [%g, %g) — mesafe halkada değil",
					tech, d, taValue, r.InnerM, r.OuterM)
			}
		}
	}
}

// TestMaxValueReference, kapsama yarıçapındaki üst sınırı denetler.
func TestMaxValueReference(t *testing.T) {
	cases := []struct {
		rMax float64
		tech Technology
		want int
	}{
		{5000, LTE, 64},   // kentsel
		{20000, GSM, 36},  // kırsal
		{5000, GSM, 9},    //
		{20000, LTE, 256}, //
	}
	for _, tc := range cases {
		got, err := MaxValue(tc.rMax, tc.tech)
		if err != nil {
			t.Errorf("MaxValue(%g, %v): beklenmeyen hata: %v", tc.rMax, tc.tech, err)
			continue
		}
		if got != tc.want {
			t.Errorf("MaxValue(%g, %v) = %d, beklenen %d", tc.rMax, tc.tech, got, tc.want)
		}
	}
	if _, err := MaxValue(-1, LTE); err == nil {
		t.Error("negatif yarıçap kabul edildi")
	}
}

// TestPBT7_ImpossibleTANeverProduced, plan BÖLÜM I'deki 7 numaralı değişmezi
// denetler: ta_value · res ≤ r_max.
//
// floor tanımı gereği ta·res ≤ d; d ≤ r_max olduğundan sonuç doğrudan gelir.
// Test bunu ispat olarak değil, tanımın ileride değişmediğinin güvencesi
// olarak tutar (ör. round'a geçilirse burada düşer).
func TestPBT7_ImpossibleTANeverProduced(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		tech := []Technology{LTE, GSM}[rapid.IntRange(0, 1).Draw(rt, "tech")]
		rMax := rapid.Float64Range(100, 20000).Draw(rt, "rmax")
		d := rapid.Float64Range(0, rMax).Draw(rt, "d")

		taValue, err := FromDistance(d, tech)
		if err != nil {
			rt.Fatalf("FromDistance(%g, %v): %v", d, tech, err)
		}

		reach := float64(taValue) * tech.ResolutionM()
		if reach > rMax {
			rt.Fatalf("%v: d = %g, r_max = %g, ta = %d → ta·res = %g > r_max",
				tech, d, rMax, taValue, reach)
		}
		if reach > d {
			rt.Fatalf("%v: ta·res = %g > d = %g (floor bozuldu)", tech, reach, d)
		}

		maxTA, err := MaxValue(rMax, tech)
		if err != nil {
			rt.Fatalf("MaxValue(%g, %v): %v", rMax, tech, err)
		}
		if taValue > maxTA {
			rt.Fatalf("%v: ta = %d > MaxValue(%g) = %d", tech, taValue, rMax, maxTA)
		}
	})
}

// TestPBT_RingRoundTripAtBoundaries, tur dönüşünü adım sınırlarının **bir ULP
// yakınında** denetler.
//
// Kayan nokta bölmesinin sınırda yukarı yuvarlanması bu paketin tek gerçek
// tuzağıdır: düzeltme olmadan FromDistance bir adım fazla verir ve halka
// mesafeyi içermez. Rastgele mesafeler bu noktalara pratikte hiç düşmediği
// için sınır özellikle hedeflenir.
func TestPBT_RingRoundTripAtBoundaries(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		tech := []Technology{LTE, GSM}[rapid.IntRange(0, 1).Draw(rt, "tech")]
		n := rapid.IntRange(0, 300).Draw(rt, "n")
		at := float64(n) * tech.ResolutionM()

		probes := []float64{
			at,
			math.Nextafter(at, math.Inf(1)),
			math.Nextafter(math.Nextafter(at, math.Inf(1)), math.Inf(1)),
		}
		if n > 0 {
			probes = append(probes,
				math.Nextafter(at, 0),
				math.Nextafter(math.Nextafter(at, 0), 0),
			)
		}

		for _, d := range probes {
			taValue, err := FromDistance(d, tech)
			if err != nil {
				rt.Fatalf("FromDistance(%g, %v): %v", d, tech, err)
			}
			r, err := NewRing(taValue, tech)
			if err != nil {
				rt.Fatalf("NewRing(%d, %v): %v", taValue, tech, err)
			}
			if !r.Contains(d) {
				rt.Fatalf("%v: d = %.17g (n = %d sınırı yakını), ta = %d, halka = [%.17g, %.17g)",
					tech, d, n, taValue, r.InnerM, r.OuterM)
			}
		}
	})
}

// TestPBT_RingRoundTrip, tur dönüşünü rastgele mesafelerde denetler.
func TestPBT_RingRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		tech := []Technology{LTE, GSM}[rapid.IntRange(0, 1).Draw(rt, "tech")]
		d := rapid.Float64Range(0, 50000).Draw(rt, "d")

		taValue, err := FromDistance(d, tech)
		if err != nil {
			rt.Fatalf("FromDistance: %v", err)
		}
		r, err := NewRing(taValue, tech)
		if err != nil {
			rt.Fatalf("NewRing: %v", err)
		}
		if !r.Contains(d) {
			rt.Fatalf("%v: d = %g, ta = %d, halka = [%g, %g)",
				tech, d, taValue, r.InnerM, r.OuterM)
		}
		// Komşu halkalar ne örtüşür ne boşluk bırakır.
		next, err := NewRing(taValue+1, tech)
		if err != nil {
			rt.Fatalf("NewRing(+1): %v", err)
		}
		if math.Abs(next.InnerM-r.OuterM) > eps {
			rt.Fatalf("%v: halka %d ile %d arasında boşluk/örtüşme (%g ≠ %g)",
				tech, taValue, taValue+1, next.InnerM, r.OuterM)
		}
	})
}
