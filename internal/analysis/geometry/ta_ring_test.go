package geometry

import (
	"math"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// TestWindow_RingMatchesPkgTA, pencerenin halkasının pkg/ta ile birebir aynı
// olduğunu sınar.
//
// İkiz garantisinin testi budur: simülatör TA'yı pkg/ta ile türetir, analiz
// halkayı pkg/ta ile kurar. İki taraf ayrışırsa burada görünür.
func TestWindow_RingMatchesPkgTA(t *testing.T) {
	for _, tech := range []ta.Technology{ta.LTE, ta.GSM} {
		for _, v := range []int{0, 1, 19, 64, 256} {
			w, err := NewWindow(v, tech)
			if err != nil {
				t.Fatalf("NewWindow(%d, %v): %v", v, tech, err)
			}
			want, err := ta.NewRing(v, tech)
			if err != nil {
				t.Fatalf("ta.NewRing: %v", err)
			}
			if w.Ring() != want {
				t.Errorf("%v ta=%d: halka %v, beklenen %v", tech, v, w.Ring(), want)
			}
			if !w.Enabled() {
				t.Errorf("%v ta=%d: pencere etkin olmalı", tech, v)
			}
		}
	}
}

// TestNoWindow, TA'sız senaryonun (B, D) her yere 1 verdiğini sınar.
func TestNoWindow(t *testing.T) {
	grid, _ := geo.NewHexGrid(100)
	w := NoWindow()

	if w.Enabled() {
		t.Error("TA'sız pencere etkin görünüyor")
	}
	for _, a := range []geo.Axial{{}, {Q: 5, R: -3}, {Q: 200, R: 0}} {
		if got := w.Weight(grid.Corners(a), geo.Point{}); got != 1 {
			t.Errorf("hücre %v: ağırlık %.9f, 1 beklenir", a, got)
		}
	}
}

// TestWindow_WeightReference, tek hücrenin TA ağırlığını doğrudan örtüşme
// oranıyla karşılaştırır (ADR-18/3).
func TestWindow_WeightReference(t *testing.T) {
	grid, _ := geo.NewHexGrid(100)
	w, err := NewWindow(19, ta.LTE)
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	site := geo.Point{}
	ring := w.Ring()

	for _, center := range []geo.Point{{X: 1500, Y: 0}, {X: 1520, Y: 300}, {X: 0, Y: 1500}} {
		cell := grid.Corners(grid.At(center))
		got := w.Weight(cell, site)
		want := OverlapRatio(cell, site, ring.InnerM, ring.OuterM)
		if math.Abs(got-want) > 1e-12 {
			t.Errorf("hücre %v: ağırlık %.12f, örtüşme oranı %.12f", center, got, want)
		}
		if got <= 0 || got > 1 {
			t.Errorf("hücre %v: ağırlık %.9f (0,1] dışında", center, got)
		}
	}
}

// TestWindow_WeightsFallback, boş kesişim geri düşüşünü sınar (T-E03-04).
//
// Halka arama bölgesiyle hiç kesişmiyorsa TA yok sayılır: tüm ağırlıklar 1
// olur ve dönen pencere devre dışıdır (ta_used = false).
func TestWindow_WeightsFallback(t *testing.T) {
	grid, _ := geo.NewHexGrid(100)
	site := geo.Point{}

	// Bölge: direğin çevresinde 0–600 m arası hücreler.
	var cells [][]geo.Point
	for _, a := range grid.Cover(site, 600) {
		cells = append(cells, grid.Corners(a))
	}
	if len(cells) == 0 {
		t.Fatal("test bölgesi boş")
	}

	t.Run("halka bölgeyle kesişiyor", func(t *testing.T) {
		w, err := NewWindow(3, ta.LTE) // [234,36 , 312,48)
		if err != nil {
			t.Fatalf("NewWindow: %v", err)
		}
		weights, out := w.Weights(cells, site)

		if !out.Enabled() {
			t.Fatal("kesişim varken TA düşürüldü")
		}
		positive := 0
		for _, x := range weights {
			if x > 0 {
				positive++
			}
		}
		if positive == 0 {
			t.Fatal("kesişim var ama hiçbir hücre ağırlık almadı")
		}
		t.Logf("ta=3 (LTE): %d/%d hücre halkaya giriyor", positive, len(cells))
	})

	t.Run("halka bölgenin tamamen dışında", func(t *testing.T) {
		w, err := NewWindow(200, ta.LTE) // [15.624 , 15.702) m — 600 m'lik bölgenin çok ötesi
		if err != nil {
			t.Fatalf("NewWindow: %v", err)
		}
		weights, out := w.Weights(cells, site)

		if out.Enabled() {
			t.Fatal("boş kesişimde TA düşürülmedi (ta_used true kalmış)")
		}
		for i, x := range weights {
			if x != 1 {
				t.Fatalf("geri düşüşte hücre %d ağırlığı %.9f, 1 beklenir", i, x)
			}
		}
	})

	t.Run("TA'sız kayıt", func(t *testing.T) {
		weights, out := NoWindow().Weights(cells, site)
		if out.Enabled() {
			t.Fatal("TA'sız pencere etkin döndü")
		}
		for i, x := range weights {
			if x != 1 {
				t.Fatalf("hücre %d ağırlığı %.9f, 1 beklenir", i, x)
			}
		}
	})
}

// TestNewWindow_Validation, geçersiz girdileri kapsar.
func TestNewWindow_Validation(t *testing.T) {
	if _, err := NewWindow(-1, ta.LTE); err == nil {
		t.Error("negatif TA kabul edildi")
	}
	if _, err := NewWindow(0, ta.Unknown); err == nil {
		t.Error("tanımsız teknoloji kabul edildi")
	}
}

// TestWindow_ThinRingIsCaptured, İ-2'nin sayısal gerekçesini doğrular:
// LTE halkası (78,12 m) 100 m'lik ızgara adımından incedir; ikili
// merkez-içinde-mi testi hücrelerin çoğunu ıskalardı, örtüşme oranı ıskalamaz.
func TestWindow_ThinRingIsCaptured(t *testing.T) {
	grid, _ := geo.NewHexGrid(100)
	site := geo.Point{}
	w, _ := NewWindow(19, ta.LTE)
	ring := w.Ring()

	var cells [][]geo.Point
	var centers []geo.Point
	for _, a := range grid.Cover(site, 2000) {
		cells = append(cells, grid.Corners(a))
		centers = append(centers, grid.Center(a))
	}

	weights, _ := w.Weights(cells, site)

	overlapCount, centerCount := 0, 0
	for i := range cells {
		if weights[i] > 0 {
			overlapCount++
		}
		if d := geo.Distance(site, centers[i]); d >= ring.InnerM && d < ring.OuterM {
			centerCount++
		}
	}

	t.Logf("halka kalınlığı %.2f m, ızgara adımı 100 m → örtüşme %d hücre, ikili merkez testi %d hücre",
		ring.WidthM(), overlapCount, centerCount)

	if overlapCount <= centerCount {
		t.Errorf("örtüşme oranı ikili testten fazla hücre yakalamalı (%d ≤ %d)",
			overlapCount, centerCount)
	}
}
