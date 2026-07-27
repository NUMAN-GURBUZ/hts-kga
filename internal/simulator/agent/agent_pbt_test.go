// Sprint 2 — Ajan katmanı için özellik tabanlı testler (ADR-11).
package agent

import (
	"math"
	"testing"

	"pgregory.net/rapid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// genState, rutin kısıtlarını sağlayan rastgele bir ajan durumu üretir.
func genState(t *rapid.T) State {
	commute := rapid.IntRange(1, 120).Draw(t, "commuteMin")
	departWork := rapid.IntRange(0, 12*minutesPerHour).Draw(t, "departWork")
	// Dönüş anı, işe varıştan sonra ve eve varış gece yarısından önce olmalı
	minHome := departWork + commute + 1
	maxHome := minutesPerDay - commute - 1
	if minHome > maxHome {
		minHome = maxHome
	}
	departHome := rapid.IntRange(minHome, maxHome).Draw(t, "departHome")

	return State{
		ID:            rapid.IntRange(0, 999).Draw(t, "agentID"),
		Home:          genPoint(t, "home"),
		Work:          genPoint(t, "work"),
		DepartWorkMin: departWork,
		DepartHomeMin: departHome,
		CommuteMin:    commute,
	}
}

// genPoint, senaryo alanı ölçeğinde ENU noktası üretir.
func genPoint(t *rapid.T, label string) geo.Point {
	return geo.Point{
		X: rapid.Float64Range(-20000, 20000).Draw(t, label+"X"),
		Y: rapid.Float64Range(-20000, 20000).Draw(t, label+"Y"),
	}
}

// genClock, geçerli bir saat üretir (günü tam bölen tick uzunlukları).
func genClock(t *rapid.T) Clock {
	tick := rapid.SampledFrom([]int{1, 5, 10, 15, 30, 60}).Draw(t, "tickMinutes")
	c, err := NewClock(tick)
	if err != nil {
		t.Fatalf("NewClock(%d): %v", tick, err)
	}
	return c
}

// TestPBT_PhaseAlwaysValid, evre hesabının daima tanımlı bir değer
// döndürdüğünü sınar.
func TestPBT_PhaseAlwaysValid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genState(t)
		c := genClock(t)
		tick := rapid.IntRange(0, 8640*4).Draw(t, "tick")

		if p := PhaseAt(s, c, tick); !p.Valid() {
			t.Fatalf("geçersiz evre %d (tick %d)", uint8(p), tick)
		}
	})
}

// TestPBT_WeekendIsAlwaysHome, haftalık periyodisitenin değişmezini sınar.
func TestPBT_WeekendIsAlwaysHome(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genState(t)
		c := genClock(t)
		tick := rapid.IntRange(0, 8640*4).Draw(t, "tick")

		if c.IsWeekend(tick) && PhaseAt(s, c, tick) != PhaseHome {
			t.Fatalf("hafta sonu (gün %d) evre HOME değil: %v",
				c.DayIndex(tick), PhaseAt(s, c, tick))
		}
	})
}

// TestPBT_CommuteProgressInUnitInterval, ilerleme oranının [0,1] aralığında
// kaldığını sınar.
func TestPBT_CommuteProgressInUnitInterval(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := genState(t)
		c := genClock(t)
		tick := rapid.IntRange(0, 8640*4).Draw(t, "tick")

		p := CommuteProgress(s, c, tick)
		if math.IsNaN(p) || p < 0 || p > 1 {
			t.Fatalf("ilerleme %v — [0,1] dışında (tick %d, evre %v)",
				p, tick, PhaseAt(s, c, tick))
		}
		// Yolculuk dışında tam sıfır olmalı
		if !PhaseAt(s, c, tick).IsCommuting() && p != 0 {
			t.Fatalf("yolculuk dışında ilerleme %v, beklenen 0", p)
		}
	})
}

// TestPBT_PositionDeterministic, hareket hesabının saf fonksiyon olduğunu
// sınar (K10).
func TestPBT_PositionDeterministic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		seed := rapid.Int64().Draw(t, "seed")
		c := genClock(t)
		s := genState(t)
		tick := rapid.IntRange(0, 8640).Draw(t, "tick")

		m := NewMobility(seed, c)
		first := m.PositionAt(s, tick)
		for i := 0; i < 3; i++ {
			if got := m.PositionAt(s, tick); got != first {
				t.Fatalf("deterministik değil: %+v vs %+v", first, got)
			}
		}
		// Yeniden kurulan model de aynı sonucu vermeli
		if got := NewMobility(seed, c).PositionAt(s, tick); got != first {
			t.Fatalf("yeniden kurulan model farklı: %+v vs %+v", first, got)
		}
	})
}

// TestPBT_PositionFinite, konumun daima sonlu kaldığını sınar.
func TestPBT_PositionFinite(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := NewMobility(rapid.Int64().Draw(t, "seed"), genClock(t))
		s := genState(t)
		tick := rapid.IntRange(0, 8640).Draw(t, "tick")

		p := m.PositionAt(s, tick)
		if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
			t.Fatalf("konum sonlu değil: %+v", p)
		}
	})
}

// TestPBT_PositionBoundedByPath, ajanın çapalarının tanımladığı bölgeden
// gürültü payı dışında çıkmadığını sınar.
//
// Ajan ev ile iş arasındaki doğru parçasının etrafında kalmalıdır; başka bir
// yere ışınlanmamalıdır.
func TestPBT_PositionBoundedByPath(t *testing.T) {
	// En büyük olası sapma: yol gürültüsü iki eksende birden.
	maxDeviation := pathNoiseM*math.Sqrt2 + 1e-6

	rapid.Check(t, func(t *rapid.T) {
		m := NewMobility(rapid.Int64().Draw(t, "seed"), genClock(t))
		s := genState(t)
		tick := rapid.IntRange(0, 8640).Draw(t, "tick")

		p := m.PositionAt(s, tick)

		// Doğru parçasına uzaklık
		if d := distanceToSegment(p, s.Home, s.Work); d > maxDeviation {
			t.Fatalf("konum ev–iş güzergâhından %.3f m sapmış (üst sınır %.3f m)\n"+
				"  konum %+v, ev %+v, iş %+v, evre %v",
				d, maxDeviation, p, s.Home, s.Work, PhaseAt(s, m.Clock(), tick))
		}
	})
}

// distanceToSegment, noktanın doğru parçasına en kısa uzaklığını döndürür.
func distanceToSegment(p, a, b geo.Point) float64 {
	abx, aby := b.X-a.X, b.Y-a.Y
	lenSq := abx*abx + aby*aby
	if lenSq == 0 {
		return geo.Distance(p, a)
	}

	// Nokta izdüşümünün doğru parçası üzerindeki konumu [0,1]
	tt := ((p.X-a.X)*abx + (p.Y-a.Y)*aby) / lenSq
	tt = math.Max(0, math.Min(1, tt))

	proj := geo.Point{X: a.X + tt*abx, Y: a.Y + tt*aby}
	return geo.Distance(p, proj)
}

// TestPBT_AdvanceDoesNotMutateInput, Advance'in girdi durumunu
// değiştirmediğini sınar — çift tampon sözleşmesinin ön koşulu.
func TestPBT_AdvanceDoesNotMutateInput(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := NewMobility(rapid.Int64().Draw(t, "seed"), genClock(t))
		s := genState(t)
		tick := rapid.IntRange(0, 8640).Draw(t, "tick")

		before := s
		next := m.Advance(s, tick)

		if s != before {
			t.Fatalf("Advance girdiyi değiştirdi:\n  önce: %+v\n  sonra: %+v", before, s)
		}
		// Değişmez alanlar korunmalı
		if next.ID != before.ID || next.Home != before.Home || next.Work != before.Work ||
			next.CommuteMin != before.CommuteMin {
			t.Fatalf("Advance değişmez alanları bozdu:\n  %+v\n  %+v", before, next)
		}
	})
}

// TestPBT_ClockArithmetic, saat aritmetiğinin değişmezlerini sınar.
func TestPBT_ClockArithmetic(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		c := genClock(t)
		tick := rapid.IntRange(0, 1_000_000).Draw(t, "tick")

		minute := c.MinuteOfDay(tick)
		if minute < 0 || minute >= minutesPerDay {
			t.Fatalf("MinuteOfDay(%d) = %d — [0, 1440) dışında", tick, minute)
		}

		day := c.DayIndex(tick)
		if day < 0 {
			t.Fatalf("DayIndex(%d) = %d — negatif olamaz", tick, day)
		}

		// Bir gün ileri gitmek gün indeksini tam olarak 1 artırmalı
		if next := c.DayIndex(tick + c.TicksPerDay()); next != day+1 {
			t.Fatalf("gün geçişi tutarsız: DayIndex(%d) = %d, DayIndex(+1 gün) = %d",
				tick, day, next)
		}
		// Ve gün içi dakikayı değiştirmemeli
		if m2 := c.MinuteOfDay(tick + c.TicksPerDay()); m2 != minute {
			t.Fatalf("bir gün sonra gün içi dakika değişti: %d → %d", minute, m2)
		}
	})
}

// TestPBT_SpeedNonNegative, hız hesabının negatif değer üretmediğini sınar.
func TestPBT_SpeedNonNegative(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		m := NewMobility(rapid.Int64().Draw(t, "seed"), genClock(t))
		s := genState(t)
		from := rapid.IntRange(0, 8000).Draw(t, "fromTick")
		to := from + rapid.IntRange(1, 100).Draw(t, "delta")

		v := m.SpeedKMH(s, from, to)
		if math.IsNaN(v) || v < 0 {
			t.Fatalf("hız %v — negatif olamaz", v)
		}
		// Geriye doğru veya sıfır aralık → 0
		if got := m.SpeedKMH(s, to, from); got != 0 {
			t.Fatalf("ters aralıkta hız %v, beklenen 0", got)
		}
	})
}
