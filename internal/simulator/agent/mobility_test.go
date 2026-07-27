package agent

import (
	"math"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// mobilityAgent, hareket testleri için bilinen çapalı bir ajan üretir.
func mobilityAgent() State {
	return State{
		ID:            7,
		Home:          geo.Point{X: 0, Y: 0},
		Work:          geo.Point{X: 4000, Y: 3000}, // 5 km
		Pos:           geo.Point{},
		Phase:         PhaseHome,
		DepartWorkMin: 8 * 60,
		DepartHomeMin: 17*60 + 30,
		CommuteMin:    30,
	}
}

// TestMobility_StaysNearAnchors, durağan evrelerde ajanın çapasından fazla
// uzaklaşmadığını sınar.
func TestMobility_StaysNearAnchors(t *testing.T) {
	c := testClock(t)
	m := NewMobility(42, c)
	a := mobilityAgent()

	for tick := 0; tick < c.TicksPerDay(); tick++ {
		phase := PhaseAt(a, c, tick)
		pos := m.PositionAt(a, tick)

		switch phase {
		case PhaseHome:
			if d := geo.Distance(pos, a.Home); d > anchorNoiseM*math.Sqrt2+1e-9 {
				t.Errorf("tick %d: evden %.1f m uzakta (üst sınır %.1f m)",
					tick, d, anchorNoiseM*math.Sqrt2)
			}
		case PhaseWork:
			if d := geo.Distance(pos, a.Work); d > anchorNoiseM*math.Sqrt2+1e-9 {
				t.Errorf("tick %d: işten %.1f m uzakta (üst sınır %.1f m)",
					tick, d, anchorNoiseM*math.Sqrt2)
			}
		}
	}
}

// TestMobility_CommutePathConnectsAnchors, yolculuğun çapalar arasında
// ilerlediğini sınar: başlangıçta kaynağa, sonunda hedefe yakın olmalı.
func TestMobility_CommutePathConnectsAnchors(t *testing.T) {
	c := testClock(t)
	m := NewMobility(42, c)
	a := mobilityAgent()

	// Yolculuğun uçlarında yanal sapma sıfıra gider (taper), bu yüzden
	// ajan çapasının yanına değil üstüne varır.
	startTick := a.DepartWorkMin / c.TickMinutes()
	endTick := (a.DepartWorkMin + a.CommuteMin) / c.TickMinutes()

	startPos := m.PositionAt(a, startTick)
	if d := geo.Distance(startPos, a.Home); d > pathNoiseM {
		t.Errorf("yolculuk başında evden %.1f m uzakta", d)
	}

	// Son yolculuk tick'i (varış tick'i artık İŞ evresidir)
	lastCommuteTick := endTick - 1
	if PhaseAt(a, c, lastCommuteTick) != PhaseCommuteToWork {
		t.Fatalf("tick %d yolculuk evresinde olmalıydı", lastCommuteTick)
	}
	lastPos := m.PositionAt(a, lastCommuteTick)
	if d := geo.Distance(lastPos, a.Work); d > 0.3*geo.Distance(a.Home, a.Work) {
		t.Errorf("yolculuk sonunda işe %.1f m uzaklıkta, daha yakın olmalıydı", d)
	}
}

// TestMobility_SpeedWithinIntegrityLimit, üretilen hareketin bütünlük
// kuralının (Kural 2) 300 km/s sınırını aşmadığını sınar.
//
// Simülatörün imkânsız hız üretmemesi, S4'ün yakaladığı her ihlalin gerçekten
// enjeksiyon kaynaklı olmasını garanti eder.
func TestMobility_SpeedWithinIntegrityLimit(t *testing.T) {
	const maxVelocityKMH = 300.0 // integrity.max_velocity_kmh

	c := testClock(t)
	m := NewMobility(42, c)

	// En zorlu durum: kırsal profil, en uzun ev-iş mesafesi, en kısa yolculuk
	a := State{
		ID:            1,
		Home:          geo.Point{X: 0, Y: 0},
		Work:          geo.Point{X: 25000, Y: 0}, // 25 km, kırsal üst sınır
		DepartWorkMin: 8 * 60,
		DepartHomeMin: 17*60 + 30,
		CommuteMin:    commuteMinutes(25000, 70), // 70 km/s → 22 dk
	}

	var maxObserved float64
	for tick := 0; tick < 2*c.TicksPerDay(); tick++ {
		v := m.SpeedKMH(a, tick, tick+1)
		if v > maxObserved {
			maxObserved = v
		}
		if v > maxVelocityKMH {
			t.Errorf("tick %d: hız %.1f km/s, bütünlük sınırı %.0f km/s aşıldı",
				tick, v, maxVelocityKMH)
		}
	}
	t.Logf("en yüksek gözlenen hız: %.1f km/s (sınır %.0f km/s)", maxObserved, maxVelocityKMH)
}

// TestMobility_Deterministic, K10 gereksinimini sınar.
func TestMobility_Deterministic(t *testing.T) {
	c := testClock(t)
	a := mobilityAgent()

	m1 := NewMobility(42, c)
	m2 := NewMobility(42, c)

	for tick := 0; tick < 3*c.TicksPerDay(); tick += 3 {
		p1 := m1.PositionAt(a, tick)
		p2 := m2.PositionAt(a, tick)
		if p1 != p2 {
			t.Fatalf("tick %d: farklı örnekler farklı konum verdi %+v vs %+v", tick, p1, p2)
		}
		// Aynı örnek, tekrar çağrı
		if again := m1.PositionAt(a, tick); again != p1 {
			t.Fatalf("tick %d: deterministik değil", tick)
		}
	}
}

// TestMobility_SeedSeparation, farklı tohumun farklı gürültü ürettiğini sınar.
func TestMobility_SeedSeparation(t *testing.T) {
	c := testClock(t)
	a := mobilityAgent()

	p1 := NewMobility(1, c).PositionAt(a, 10)
	p2 := NewMobility(2, c).PositionAt(a, 10)

	if p1 == p2 {
		t.Error("farklı tohum aynı konumu üretti")
	}
}

// TestMobility_AgentSeparation, farklı ajanların bağımsız gürültü aldığını
// sınar (aynı çapada bile).
func TestMobility_AgentSeparation(t *testing.T) {
	c := testClock(t)
	m := NewMobility(42, c)

	a1 := mobilityAgent()
	a2 := mobilityAgent()
	a2.ID = 99

	if m.PositionAt(a1, 10) == m.PositionAt(a2, 10) {
		t.Error("farklı ajanlar aynı gürültüyü aldı")
	}
}

// TestMobility_Advance_DoesNotMutate, Advance'in girdi durumunu
// değiştirmediğini sınar — çift tampon sözleşmesinin ön koşuludur.
func TestMobility_Advance_DoesNotMutate(t *testing.T) {
	c := testClock(t)
	m := NewMobility(42, c)

	original := mobilityAgent()
	input := original

	next := m.Advance(input, 100)

	if input != original {
		t.Errorf("Advance girdi durumunu değiştirdi:\n  önce: %+v\n  sonra: %+v", original, input)
	}
	if next.Pos == original.Pos && next.Phase == original.Phase {
		t.Log("not: bu tick'te konum ve evre değişmemiş olabilir (normal)")
	}
	// Çapalar korunmalı
	if next.Home != original.Home || next.Work != original.Work || next.ID != original.ID {
		t.Error("Advance değişmez alanları bozdu")
	}
}

// TestMobility_NoAllocations, sıcak yolun ayırma yapmadığını sınar.
func TestMobility_NoAllocations(t *testing.T) {
	c := testClock(t)
	m := NewMobility(42, c)
	a := mobilityAgent()

	allocs := testing.AllocsPerRun(1000, func() {
		_ = m.PositionAt(a, 42)
	})
	if allocs != 0 {
		t.Errorf("PositionAt %g ayırma yaptı, 0 bekleniyordu", allocs)
	}
}

// BenchmarkPositionAt, hareket hesabının tick başına maliyetini ölçer.
func BenchmarkPositionAt(b *testing.B) {
	c, err := NewClock(5)
	if err != nil {
		b.Fatalf("NewClock: %v", err)
	}
	m := NewMobility(42, c)
	a := mobilityAgent()

	b.ReportAllocs()
	b.ResetTimer()

	var sink geo.Point
	for i := 0; i < b.N; i++ {
		sink = m.PositionAt(a, i)
	}
	_ = sink
}
