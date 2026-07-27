package agent

import (
	"math"
	"testing"
)

// testClock, senaryo config'indeki tick uzunluğuyla saat kurar (5 dk).
func testClock(t *testing.T) Clock {
	t.Helper()
	c, err := NewClock(5)
	if err != nil {
		t.Fatalf("NewClock: %v", err)
	}
	return c
}

// testAgent, bilinen rutin parametreleriyle bir ajan üretir.
func testAgent() State {
	return State{
		ID:            0,
		DepartWorkMin: 8 * 60,     // 08:00
		DepartHomeMin: 17*60 + 30, // 17:30
		CommuteMin:    30,
	}
}

// TestNewClock_Validation, saat kurulumunu sınar.
func TestNewClock_Validation(t *testing.T) {
	for _, tick := range []int{0, -5, 7, 13, 100} {
		if _, err := NewClock(tick); err == nil {
			t.Errorf("tick = %d için hata bekleniyordu (günü tam bölmeli)", tick)
		}
	}
	for _, tick := range []int{1, 5, 10, 15, 30, 60} {
		if _, err := NewClock(tick); err != nil {
			t.Errorf("tick = %d kabul edilmeliydi: %v", tick, err)
		}
	}
}

// TestClock_Arithmetic, tick → takvim dönüşümlerini sınar.
func TestClock_Arithmetic(t *testing.T) {
	c := testClock(t)

	if got := c.TicksPerDay(); got != 288 {
		t.Errorf("TicksPerDay() = %d, beklenen 288 (24×60/5)", got)
	}

	tests := []struct {
		tick        int
		minuteOfDay int
		dayIndex    int
	}{
		{0, 0, 0},
		{1, 5, 0},
		{96, 480, 0},   // 08:00 gün 0
		{287, 1435, 0}, // 23:55 gün 0
		{288, 0, 1},    // gün 1 başı
		{288 + 96, 480, 1},
		{288 * 5, 0, 5}, // Cumartesi
	}
	for _, tc := range tests {
		if got := c.MinuteOfDay(tc.tick); got != tc.minuteOfDay {
			t.Errorf("MinuteOfDay(%d) = %d, beklenen %d", tc.tick, got, tc.minuteOfDay)
		}
		if got := c.DayIndex(tc.tick); got != tc.dayIndex {
			t.Errorf("DayIndex(%d) = %d, beklenen %d", tc.tick, got, tc.dayIndex)
		}
	}
}

// TestClock_WeeklyPeriodicity, haftalık periyodisiteyi sınar.
// Koşu Pazartesi başlar; gün 5 (Cumartesi) ve 6 (Pazar) hafta sonudur.
func TestClock_WeeklyPeriodicity(t *testing.T) {
	c := testClock(t)

	tests := []struct {
		day     int
		weekend bool
		name    string
	}{
		{0, false, "Pazartesi"},
		{1, false, "Salı"},
		{4, false, "Cuma"},
		{5, true, "Cumartesi"},
		{6, true, "Pazar"},
		{7, false, "sonraki Pazartesi"},
		{12, true, "sonraki Cumartesi"},
		{13, true, "sonraki Pazar"},
		{14, false, "üçüncü Pazartesi"},
	}
	for _, tc := range tests {
		tick := tc.day * c.TicksPerDay()
		if got := c.IsWeekend(tick); got != tc.weekend {
			t.Errorf("gün %d (%s): IsWeekend = %v, beklenen %v", tc.day, tc.name, got, tc.weekend)
		}
	}
}

// TestPhaseAt_WeekdaySequence, hafta içi 4 fazlı rutinin sırayla
// uygulandığını sınar.
func TestPhaseAt_WeekdaySequence(t *testing.T) {
	c := testClock(t)
	a := testAgent()

	tests := []struct {
		minuteOfDay int
		want        Phase
		label       string
	}{
		{0, PhaseHome, "gece yarısı"},
		{7 * 60, PhaseHome, "07:00 evde"},
		{8 * 60, PhaseCommuteToWork, "08:00 çıkış anı"},
		{8*60 + 15, PhaseCommuteToWork, "08:15 yolda"},
		{8*60 + 30, PhaseWork, "08:30 işe varış"},
		{12 * 60, PhaseWork, "12:00 işte"},
		{17*60 + 25, PhaseWork, "17:25 hâlâ işte"},
		{17*60 + 30, PhaseCommuteToHome, "17:30 dönüş anı"},
		{17*60 + 55, PhaseCommuteToHome, "17:55 yolda"},
		{18 * 60, PhaseHome, "18:00 eve varış"},
		{23 * 60, PhaseHome, "23:00 evde"},
	}

	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			tick := tc.minuteOfDay / c.TickMinutes()
			if got := PhaseAt(a, c, tick); got != tc.want {
				t.Errorf("%s: evre %v, beklenen %v", tc.label, got, tc.want)
			}
		})
	}
}

// TestPhaseAt_WeekendIsAlwaysHome, hafta sonu iş evresi olmadığını sınar.
func TestPhaseAt_WeekendIsAlwaysHome(t *testing.T) {
	c := testClock(t)
	a := testAgent()

	for _, day := range []int{5, 6, 12, 13} {
		for tick := day * c.TicksPerDay(); tick < (day+1)*c.TicksPerDay(); tick++ {
			if got := PhaseAt(a, c, tick); got != PhaseHome {
				t.Fatalf("gün %d tick %d: evre %v, hafta sonu HOME olmalı", day, tick, got)
			}
		}
	}
}

// TestPhaseAt_AllPhasesReachedOnWeekday, hafta içi dört evrenin de
// gerçekleştiğini sınar.
func TestPhaseAt_AllPhasesReachedOnWeekday(t *testing.T) {
	c := testClock(t)
	a := testAgent()

	seen := make(map[Phase]int)
	for tick := 0; tick < c.TicksPerDay(); tick++ {
		seen[PhaseAt(a, c, tick)]++
	}

	for _, p := range []Phase{PhaseHome, PhaseCommuteToWork, PhaseWork, PhaseCommuteToHome} {
		if seen[p] == 0 {
			t.Errorf("evre %v hiç gerçekleşmedi", p)
		}
	}
	t.Logf("hafta içi tick dağılımı: EV %d, GİDİŞ %d, İŞ %d, DÖNÜŞ %d",
		seen[PhaseHome], seen[PhaseCommuteToWork], seen[PhaseWork], seen[PhaseCommuteToHome])
}

// TestCommuteProgress, yolculuk ilerlemesini sınar.
func TestCommuteProgress(t *testing.T) {
	c := testClock(t)
	a := testAgent()

	t.Run("yolculuk dışında sıfır", func(t *testing.T) {
		for _, minute := range []int{0, 7 * 60, 12 * 60, 23 * 60} {
			if got := CommuteProgress(a, c, minute/c.TickMinutes()); got != 0 {
				t.Errorf("dakika %d: ilerleme %g, beklenen 0", minute, got)
			}
		}
	})

	t.Run("yolculuk boyunca artan", func(t *testing.T) {
		prev := -1.0
		for minute := 8 * 60; minute < 8*60+30; minute += 5 {
			got := CommuteProgress(a, c, minute/c.TickMinutes())
			if got < 0 || got > 1 {
				t.Fatalf("dakika %d: ilerleme %g, [0,1] dışında", minute, got)
			}
			if got < prev {
				t.Errorf("dakika %d: ilerleme geriledi (%g < %g)", minute, got, prev)
			}
			prev = got
		}
	})

	t.Run("çıkış anında sıfır", func(t *testing.T) {
		if got := CommuteProgress(a, c, (8*60)/c.TickMinutes()); got != 0 {
			t.Errorf("çıkış anında ilerleme %g, beklenen 0", got)
		}
	})
}

// TestPersonalizeRoutine_Distribution, kişiselleştirmenin çıkış anlarını
// yaydığını ve tutarlı kaldığını sınar.
//
// Herkesin aynı anda işe çıkması gerçekdışı bir olay tepesi yaratırdı.
func TestPersonalizeRoutine_Distribution(t *testing.T) {
	const samples = 10_000
	rng := newRNG(42, streamRoutine)

	var sum float64
	minDepart, maxDepart := math.MaxInt32, 0

	for i := 0; i < samples; i++ {
		departWork, departHome := personalizeRoutine(rng, 30)

		if departWork < 0 || departWork >= minutesPerDay {
			t.Fatalf("çıkış anı gün dışında: %d", departWork)
		}
		if departHome+30 >= minutesPerDay {
			t.Fatalf("eve varış gece yarısını aşıyor: %d + 30", departHome)
		}
		if departHome <= departWork+30 {
			t.Fatalf("dönüş anı işe varıştan önce: %d ≤ %d+30", departHome, departWork)
		}

		sum += float64(departWork)
		if departWork < minDepart {
			minDepart = departWork
		}
		if departWork > maxDepart {
			maxDepart = departWork
		}
	}

	mean := sum / samples
	if math.Abs(mean-nominalDepartWorkMin) > 2 {
		t.Errorf("ortalama çıkış anı %.1f dk, beklenen ≈%d dk", mean, nominalDepartWorkMin)
	}
	// Sapma genliği ±45 dk olmalı
	if maxDepart-minDepart < 80 {
		t.Errorf("çıkış anları yeterince yayılmamış: aralık %d dk", maxDepart-minDepart)
	}
	t.Logf("çıkış anı: min %d, ort %.1f, max %d dakika (nominal %d ± %d)",
		minDepart, mean, maxDepart, nominalDepartWorkMin, departJitterMin)
}

// TestPhaseAt_Deterministic, evre hesabının saf fonksiyon olduğunu sınar.
func TestPhaseAt_Deterministic(t *testing.T) {
	c := testClock(t)
	a := testAgent()

	for tick := 0; tick < 3*c.TicksPerDay(); tick += 7 {
		first := PhaseAt(a, c, tick)
		for i := 0; i < 5; i++ {
			if got := PhaseAt(a, c, tick); got != first {
				t.Fatalf("tick %d: deterministik değil (%v vs %v)", tick, first, got)
			}
		}
	}
}
