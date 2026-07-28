package core

import (
	"fmt"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/ta"
)

// K8 tanı ölçümü — parçalanma sayısal mı, fiziksel mi?
//
// Uçtan uca koşuda (KAPI 2) TA'lı senaryolar TA'sızlardan belirgin biçimde
// daha çok parçalanıyor:
//
//	kentsel  TA'lı  M@50 p95 = 8    TA'sız M@50 p95 = 3
//	kırsal   TA'lı  M@50 p95 = 14   TA'sız M@50 p95 = 2
//
// İki açıklama mümkündür ve ayırt edilmeleri şarttır:
//
//	(a) Fiziksel  — TA halkası kütleyi gerçekten birden çok tepeye böler;
//	                parçalanma kütlenin gerçek şeklidir, kriter yanlıştır.
//	(b) Sayısal   — LTE'de TA halkasının genişliği 78,12 m'dir; ızgara adımı
//	                kentselde 100 m, kırsalda 250 m. Halka **hücreden ince**
//	                olduğu için yüksek kütleli bant kesik bir zincire dönüşür;
//	                parçalanma çözünürlük eseridir.
//
// Ayrım testi doğrudandır: çözünürlük düşürülünce parçalanma da düşüyorsa
// neden (b)'dir. Ölçüm, çözünürlük kararının (ADR-18/6) girdisidir; komşu
// kısıtı (ADR-03) bu ölçümde **değiştirilmez**.

// partCountStats, bir yapılandırmanın parçalanma özetidir.
type partCountStats struct {
	resolutionM float64
	level       float64
	p50, p95    int
	max         int
	cellsPerEvt int
	usPerEvent  float64
}

// measurePartCount, verilen çözünürlükte parçalanmayı ölçer.
func measurePartCount(t testing.TB, p profileSpec, resolutionM float64,
	taValue *int, events int) []partCountStats {
	t.Helper()

	inv := buildNetwork(t, p)
	grid := mustGrid(t, resolutionM)
	opts := DefaultOptions(testConfig(p), 8)

	counts := map[float64][]int{}
	cells := 0

	// Isınma: ilk çağrı ayırmaları yapar, ölçüme girmemeli.
	if _, err := Estimate(record(inv, 0, taValue), inv, grid, opts); err != nil {
		t.Fatalf("ısınma: %v", err)
	}
	started := time.Now()

	for i := 0; i < events; i++ {
		res, err := Estimate(record(inv, i, taValue), inv, grid, opts)
		if err != nil {
			t.Fatalf("Estimate: %v", err)
		}
		cells += res.CellCount

		contours, err := Contours(res.Mass, grid, planLevels)
		if err != nil {
			t.Fatalf("Contours: %v", err)
		}
		for _, c := range contours {
			mp, err := grid.Polygonize(c.Cells)
			if err != nil {
				t.Fatalf("Polygonize: %v", err)
			}
			counts[c.Level] = append(counts[c.Level], mp.PartCount())
		}
	}

	perEvent := float64(time.Since(started).Microseconds()) / float64(events)

	out := make([]partCountStats, 0, len(planLevels))
	for _, level := range planLevels {
		v := counts[level]
		sort.Ints(v)
		out = append(out, partCountStats{
			resolutionM: resolutionM,
			level:       level,
			p50:         v[len(v)/2],
			p95:         v[(len(v)*95)/100],
			max:         v[len(v)-1],
			cellsPerEvt: cells / events,
			usPerEvent:  perEvent,
		})
	}
	return out
}

// TestK8_PartCountVsResolution, parçalanmanın çözünürlükle nasıl değiştiğini
// ölçer.
//
// TA halkası genişliği (LTE 78,12 m) ile ızgara adımı arasındaki oran
// belirleyiciyse, adım halkanın altına indiğinde p95 düşmelidir.
func TestK8_PartCountVsResolution(t *testing.T) {
	if testing.Short() {
		t.Skip("çözünürlük taraması kısa modda atlanır")
	}

	p := urbanProfile()
	const events = 40

	ringWidth := ta.LTE.ResolutionM()
	t.Logf("LTE TA halka genişliği: %.2f m", ringWidth)

	for _, tc := range []struct {
		name string
		ta   *int
	}{
		{"TA'lı", intPtr(20)},
		{"TA'sız", nil},
	} {
		t.Logf("── %s ───────────────────────────────────────────────", tc.name)
		for _, res := range []float64{100, 78, 50, 39, 25} {
			stats := measurePartCount(t, p, res, tc.ta, events)
			for _, s := range stats {
				if s.level != 0.50 && s.level != 0.90 {
					continue
				}
				t.Logf("  çözünürlük %3.0f m (halka/adım %.2f) · M@%.0f%%: "+
					"p50=%d p95=%2d maks=%2d · %d hücre/olay · %.1f ms/olay",
					res, ringWidth/res, s.level*100, s.p50, s.p95, s.max,
					s.cellsPerEvt, s.usPerEvent/1000)
			}
		}
	}
}

// TestK8_FragmentationIsSubResolutionRing, tanının kendisini bir iddiaya
// bağlar: TA'lı parçalanma, ızgara adımı halka genişliğinin altına indiğinde
// **azalmalıdır**.
//
// Azalmıyorsa neden sayısal değil fizikseldir ve K8 tartışması çözünürlükle
// değil kriterin tanımıyla ilgilidir. Bu test o ayrımı kayda geçirir.
func TestK8_FragmentationIsSubResolutionRing(t *testing.T) {
	if testing.Short() {
		t.Skip("çözünürlük taraması kısa modda atlanır")
	}

	p := urbanProfile()
	const events = 40
	taValue := intPtr(20)

	coarse := measurePartCount(t, p, 100, taValue, events) // halka < adım
	fine := measurePartCount(t, p, 39, taValue, events)    // halka > adım

	find := func(stats []partCountStats, level float64) partCountStats {
		for _, s := range stats {
			if s.level == level {
				return s
			}
		}
		t.Fatalf("%.2f seviyesi ölçülmedi", level)
		return partCountStats{}
	}

	for _, level := range []float64{0.50, 0.90} {
		c, f := find(coarse, level), find(fine, level)
		t.Logf("M@%.0f%%: 100 m → p95=%d | 39 m → p95=%d (%+.0f%%)",
			level*100, c.p95, f.p95, 100*(float64(f.p95-c.p95)/math.Max(1, float64(c.p95))))

		if f.p95 > c.p95 {
			t.Errorf("M@%.0f%%: çözünürlük iyileştirilince parçalanma arttı "+
				"(100 m: %d → 39 m: %d) — neden alt-çözünürlük halkası değil, "+
				"K8 tartışması kriterin tanımına taşınmalı",
				level*100, c.p95, f.p95)
		}
	}
}

// TestK8_RingWidthVersusGrid, dört senaryonun halka/adım oranını belgeler.
//
// Sayı, çözünürlük kararının doğrudan girdisidir: oran 1'in altındaysa TA
// bandı ızgarada temsil edilemiyor demektir.
func TestK8_RingWidthVersusGrid(t *testing.T) {
	cases := []struct {
		scenario    string
		tech        ta.Technology
		resolutionM float64
		taEnabled   bool
	}{
		{"A kentsel TA'lı", ta.LTE, 100, true},
		{"B kentsel TA'sız", ta.LTE, 100, false},
		{"C kırsal TA'lı", ta.LTE, 250, true},
		{"D kırsal TA'sız", ta.LTE, 250, false},
	}

	for _, c := range cases {
		if !c.taEnabled {
			t.Logf("%-18s ızgara %3.0f m · TA yok", c.scenario, c.resolutionM)
			continue
		}
		ratio := c.tech.ResolutionM() / c.resolutionM
		verdict := "halka ızgarada temsil ediliyor"
		if ratio < 1 {
			verdict = fmt.Sprintf("halka hücreden İNCE (%.2f×) — bant kesik zincire dönüşür", ratio)
		}
		t.Logf("%-18s ızgara %3.0f m · halka %.2f m · oran %.2f · %s",
			c.scenario, c.resolutionM, c.tech.ResolutionM(), ratio, verdict)
	}
}

// TestK8_RuralResolutionCost, kırsal senaryoda çözünürlük düşürmenin bedelini
// ölçer.
//
// Karar tek başına parçalanmaya bakarak verilemez: kırsalda dilim 29 km
// yarıçaplıdır ve hücre sayısı adımın karesiyle büyür. Aşağıdaki tablo,
// "halka ızgarada temsil edilsin" hedefinin (adım ≤ 78 m) 60.000 olaylık 'V'
// kümesinde kaç saate mal olduğunu gösterir.
func TestK8_RuralResolutionCost(t *testing.T) {
	if testing.Short() {
		t.Skip("kırsal maliyet taraması kısa modda atlanır")
	}

	p := ruralProfile()
	const events = 12
	const validationEvents = 60_000

	for _, res := range []float64{250, 150, 100, 78} {
		stats := measurePartCount(t, p, res, intPtr(60), events)
		s := stats[1] // M@90
		total := time.Duration(s.usPerEvent*validationEvents) * time.Microsecond
		t.Logf("kırsal TA'lı · adım %3.0f m (halka/adım %.2f) · M@90%%: p95=%d maks=%d · "+
			"%d hücre/olay · %.0f ms/olay → 60K olay ≈ %s",
			res, ta.LTE.ResolutionM()/res, s.p95, s.max, s.cellsPerEvt,
			s.usPerEvent/1000, total.Round(time.Minute))
	}
}
