package event

import (
	"math"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/agent"
)

// testTickMinutes, senaryo config'lerindeki tick uzunluğudur.
const testTickMinutes = 5

// newTestGenerator, sabit tohumlu bir üreteç kurar.
func newTestGenerator(t *testing.T, seed int64) *Generator {
	t.Helper()
	clock, err := agent.NewClock(testTickMinutes)
	if err != nil {
		t.Fatalf("agent.NewClock: %v", err)
	}
	g, err := NewGenerator(seed, clock)
	if err != nil {
		t.Fatalf("NewGenerator: %v", err)
	}
	return g
}

// TestDiurnalProfileNormalisation, profilin ajan başına günde tam olarak
// DailyEventTarget olay ima ettiğini denetler.
//
// Bu, profilin varlık koşuludur: ADR-14'ün ~300.000 olay hacmi buradan gelir.
// Vektör elle yazıldığı için toplamın doğruluğu tesadüfe bırakılamaz.
func TestDiurnalProfileNormalisation(t *testing.T) {
	profile := DiurnalProfile()

	total := 0.0
	for _, lambda := range profile {
		total += lambda
	}
	if math.Abs(total-DailyEventTarget) > normalisationTolerance {
		t.Fatalf("Σ λ(h) = %.12f, beklenen %.2f (tolerans %g)",
			total, DailyEventTarget, normalisationTolerance)
	}

	// Aynı özdeşliğin tick düzeyindeki hâli: her saat tickPerHour tick içerir
	// ve her tick λ(h)·Δt bekler. Toplam yine günlük hedeftir.
	g := newTestGenerator(t, 42)
	ticksPerHour := minutesPerHour / testTickMinutes
	tickTotal := 0.0
	for h := 0; h < len(profile); h++ {
		tickTotal += float64(ticksPerHour) * g.LambdaPerTick(h)
	}
	if math.Abs(tickTotal-DailyEventTarget) > normalisationTolerance {
		t.Fatalf("Σ tickPerHour·λ(h)·Δt = %.12f, beklenen %.2f",
			tickTotal, DailyEventTarget)
	}
}

// TestDiurnalProfileShape, profilin ilan edilen günlük örüntüyü taşıdığını
// denetler: her saat pozitif, tepeler 08 ve 18'de, dip 03'te.
func TestDiurnalProfileShape(t *testing.T) {
	profile := DiurnalProfile()

	peakHour, dipHour := 0, 0
	for h, lambda := range profile {
		if lambda <= 0 {
			t.Fatalf("λ(%d) = %g, pozitif olmalı", h, lambda)
		}
		if lambda > profile[peakHour] {
			peakHour = h
		}
		if lambda < profile[dipHour] {
			dipHour = h
		}
	}

	if profile[8] != profile[peakHour] || profile[18] != profile[peakHour] {
		t.Errorf("tepe saatleri 08 ve 18 olmalı (bulunan tepe: %02d = %g)",
			peakHour, profile[peakHour])
	}
	if dipHour != 3 {
		t.Errorf("dip saati 03 olmalı (bulunan: %02d)", dipHour)
	}
	if ratio := profile[peakHour] / profile[dipHour]; math.Abs(ratio-16.0) > 1e-9 {
		t.Errorf("tepe/dip oranı = %g, beklenen 16.0", ratio)
	}

	// Gece 00–05 penceresi günlük hacmin küçük bir kısmını taşımalı.
	night := 0.0
	for h := 0; h < 6; h++ {
		night += profile[h]
	}
	if night/DailyEventTarget > 0.10 {
		t.Errorf("gece payı %.3f, %%10'un altında beklenir", night/DailyEventTarget)
	}
}

// TestPoissonCountInverseCDF, ters CDF'i uygulamadan bağımsız hesaplanmış
// kümülatif eşiklere karşı denetler (λ = 1 için Poisson CDF).
//
// Eşikler: P(0)=0.367879, P(≤1)=0.735759, P(≤2)=0.919699, P(≤3)=0.981012,
// P(≤4)=0.996340.
func TestPoissonCountInverseCDF(t *testing.T) {
	const lambda = 1.0

	cases := []struct {
		u    float64
		want int
	}{
		{0.100000, 0},
		{0.367878, 0}, // P(0)'ın hemen altı
		{0.367880, 1}, // P(0)'ın hemen üstü
		{0.500000, 1},
		{0.735758, 1},
		{0.735760, 2},
		{0.800000, 2},
		{0.919700, 3},
		{0.950000, 3},
		{0.981013, 4},
		{0.990000, 4},
	}

	for _, tc := range cases {
		if got := poissonCount(lambda, tc.u); got != tc.want {
			t.Errorf("poissonCount(%.1f, %.6f) = %d, beklenen %d", lambda, tc.u, got, tc.want)
		}
	}
}

// TestPoissonCountEdgeCases, sınır davranışlarını denetler.
func TestPoissonCountEdgeCases(t *testing.T) {
	if got := poissonCount(0, 0.999999); got != 0 {
		t.Errorf("λ=0 için sayım %d, 0 olmalı", got)
	}
	if got := poissonCount(-1, 0.5); got != 0 {
		t.Errorf("negatif λ için sayım %d, 0 olmalı", got)
	}
	// Güvenlik sınırı: gerçekçi olmayan λ'da bile döngü sınırlıdır.
	if got := poissonCount(1000, 0.9999999999); got > maxEventsPerTick {
		t.Errorf("sayım %d, üst sınır %d aşıldı", got, maxEventsPerTick)
	}
	// Monotonluk: u büyüdükçe sayım azalamaz.
	prev := 0
	for u := 0.01; u < 1.0; u += 0.01 {
		got := poissonCount(0.5, u)
		if got < prev {
			t.Fatalf("u=%.2f'te sayım düştü (%d → %d)", u, prev, got)
		}
		prev = got
	}
}

// TestGeneratorDeterministic, aynı ajan-tick'in her zaman aynı sayımı
// verdiğini denetler (K10).
func TestGeneratorDeterministic(t *testing.T) {
	g1 := newTestGenerator(t, 42)
	g2 := newTestGenerator(t, 42)

	for agentID := 0; agentID < 50; agentID++ {
		for tick := 0; tick < 288; tick++ {
			a := g1.Count(agentID, tick)
			b := g2.Count(agentID, tick)
			if a != b {
				t.Fatalf("ajan %d tick %d: %d ≠ %d", agentID, tick, a, b)
			}
			if c := g1.Count(agentID, tick); c != a {
				t.Fatalf("ajan %d tick %d: tekrar çağrı %d verdi, %d bekleniyordu",
					agentID, tick, c, a)
			}
		}
	}
}

// TestGeneratorSeedSensitivity, farklı tohumların farklı olay dizisi verdiğini
// denetler; aksi hâlde tohum çıktıyı etkilemiyor demektir.
func TestGeneratorSeedSensitivity(t *testing.T) {
	g1 := newTestGenerator(t, 42)
	g2 := newTestGenerator(t, 43)

	diff := 0
	for agentID := 0; agentID < 100; agentID++ {
		for tick := 0; tick < 288; tick++ {
			if g1.Count(agentID, tick) != g2.Count(agentID, tick) {
				diff++
			}
		}
	}
	if diff == 0 {
		t.Fatal("iki tohum aynı olay dizisini üretti")
	}
}

// TestGeneratorRawRateMatchesLambda, üretecin **ham** çıktısının saatlik
// yoğunlukla uyuştuğunu denetler.
//
// Kapsama filtresi kasten dışarıdadır (ADR-08 ayrı katmandır): burada ölçülen
// yalnızca Poisson doğruluğudur. İkisi aynı testte karışsaydı, gerçekleşen
// olay sayısı beklenenden düşük çıktığında hatanın hangi katmanda olduğu
// ayırt edilemezdi.
func TestGeneratorRawRateMatchesLambda(t *testing.T) {
	g := newTestGenerator(t, 42)
	clock, _ := agent.NewClock(testTickMinutes)
	ticksPerDay := clock.TicksPerDay()
	ticksPerHour := minutesPerHour / testTickMinutes

	// Tepe (18) ve dip (03) saatleri ayrı ayrı ölçülür.
	for _, hour := range []int{3, 18} {
		const agents, days = 1000, 100
		total, samples := 0, 0
		for agentID := 0; agentID < agents; agentID++ {
			for day := 0; day < days; day++ {
				base := day*ticksPerDay + hour*ticksPerHour
				for slot := 0; slot < ticksPerHour; slot++ {
					total += g.Count(agentID, base+slot)
					samples++
				}
			}
		}

		got := float64(total) / float64(samples)
		want := g.LambdaPerTick(hour)
		t.Logf("saat %02d: n=%d ölçülen=%.6f beklenen=%.6f bağıl fark=%%%.3f",
			hour, samples, got, want, math.Abs(got-want)/want*100)
		// Örneklem hatası: σ/√n = √(λ/n). n ≈ 1.2e6 için tepe saatte
		// bağıl hata ~%0.4; %2 sınırı bunun beş katıdır ve sabit tohumla
		// deterministiktir (rastgele düşme riski yoktur).
		if rel := math.Abs(got-want) / want; rel > 0.02 {
			t.Errorf("saat %02d: ölçülen ortalama %.6f, beklenen %.6f (bağıl fark %%%.2f, n=%d)",
				hour, got, want, rel*100, samples)
		}
	}
}

// TestGeneratorDailyTotal, bir günlük ham hacmin günlük hedefe oturduğunu
// denetler: 1000 ajan × 1 gün ≈ 10.000 olay.
func TestGeneratorDailyTotal(t *testing.T) {
	g := newTestGenerator(t, 42)
	clock, _ := agent.NewClock(testTickMinutes)
	ticksPerDay := clock.TicksPerDay()

	const agents = 1000
	total := 0
	for agentID := 0; agentID < agents; agentID++ {
		for tick := 0; tick < ticksPerDay; tick++ {
			total += g.Count(agentID, tick)
		}
	}

	want := agents * DailyEventTarget
	t.Logf("günlük ham hacim: %d olay, beklenen %.0f (bağıl fark %%%.2f)",
		total, want, math.Abs(float64(total)-want)/want*100)
	// σ = √10000 = 100, yani %1. %4 sınırı 4σ'dır.
	if rel := math.Abs(float64(total)-want) / want; rel > 0.04 {
		t.Errorf("günlük hacim %d, beklenen %.0f (bağıl fark %%%.2f)", total, want, rel*100)
	}
}

// TestGeneratorHourlyShapeReproduced, ham çıktının saatlik dağılımının profil
// şeklini yeniden ürettiğini denetler: en yoğun saat 08 veya 18, en seyrek 03.
func TestGeneratorHourlyShapeReproduced(t *testing.T) {
	g := newTestGenerator(t, 42)
	clock, _ := agent.NewClock(testTickMinutes)
	ticksPerDay := clock.TicksPerDay()
	ticksPerHour := minutesPerHour / testTickMinutes

	var perHour [24]int
	const agents, days = 500, 10
	for agentID := 0; agentID < agents; agentID++ {
		for day := 0; day < days; day++ {
			for tick := 0; tick < ticksPerDay; tick++ {
				n := g.Count(agentID, day*ticksPerDay+tick)
				if n > 0 {
					perHour[(tick/ticksPerHour)%24] += n
				}
			}
		}
	}

	peak, dip := 0, 0
	for h := 1; h < 24; h++ {
		if perHour[h] > perHour[peak] {
			peak = h
		}
		if perHour[h] < perHour[dip] {
			dip = h
		}
	}
	if peak != 8 && peak != 18 {
		t.Errorf("ölçülen tepe saati %02d, 08 veya 18 beklenir (%v)", peak, perHour)
	}
	if dip != 3 {
		t.Errorf("ölçülen dip saati %02d, 03 beklenir (%v)", dip, perHour)
	}
}

// TestNewGeneratorRejectsInvalidClock, kurulum denetimlerini kapsar.
func TestNewGeneratorRejectsInvalidClock(t *testing.T) {
	if _, err := NewGenerator(42, agent.Clock{}); err == nil {
		t.Error("sıfır değerli saat kabul edildi")
	}
}

// TestValidateProfile, normalizasyon denetiminin bozuk vektörleri yakaladığını
// gösterir.
func TestValidateProfile(t *testing.T) {
	if err := validateProfile(diurnalLambda); err != nil {
		t.Fatalf("geçerli profil reddedildi: %v", err)
	}

	scaled := diurnalLambda
	scaled[0] += 0.01 // toplam 10.01
	if err := validateProfile(scaled); err == nil {
		t.Error("normalize olmayan profil kabul edildi")
	}

	zeroed := diurnalLambda
	zeroed[3] = 0
	if err := validateProfile(zeroed); err == nil {
		t.Error("sıfır yoğunluklu saat kabul edildi")
	}
}

// BenchmarkGeneratorCount, ajan-tick başına maliyeti ölçer.
func BenchmarkGeneratorCount(b *testing.B) {
	clock, err := agent.NewClock(testTickMinutes)
	if err != nil {
		b.Fatal(err)
	}
	g, err := NewGenerator(42, clock)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		g.Count(i%1000, i%288)
	}
}
