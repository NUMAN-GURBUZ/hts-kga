// T-E02-08 — 4 fazlı günlük rutin ve haftalık periyodisite.
//
// Plan bu görev için sayısal bir tanım vermez ("4-faz rutin + haftalık
// periyodisite (profil bazlı)"). Aşağıdaki model, yerleşik ulaşım
// literatüründeki klasik ev–iş–ev örüntüsünü izler ve gerekçesi
// docs/scientific/agent-routine.md belgesinde kayıtlıdır.
//
// # Günlük yapı (hafta içi)
//
//	00:00 ────────── EV ────────── T_çıkış
//	T_çıkış ──── İŞE GİDİŞ ──── T_çıkış + T_yol
//	              ────────── İŞ ────────── T_dönüş
//	T_dönüş ──── EVE DÖNÜŞ ──── T_dönüş + T_yol
//	              ────────── EV ────────── 24:00
//
// # Haftalık periyodisite
//
// Hafta sonları iş evresi yoktur; ajan gün boyu ev çapasındadır. Bu, planın
// "haftalık periyodisite" gereksinimini karşılayan en küçük ve savunulabilir
// modeldir — hafta sonu boş zaman gezileri kasten modellenmemiştir, çünkü
// üçüncü bir çapa türü planda tanımlı değildir ve uydurmak bilimsel
// savunulabilirliği zayıflatırdı.
//
// # Kişiselleştirme
//
// Herkesin aynı anda işe çıkması gerçekdışı bir olay tepesi yaratırdı.
// Çıkış ve dönüş anları ajan başına ±45 dakikalık deterministik sapma taşır.
package agent

import (
	"fmt"
	"math"
)

// Rutin zaman sabitleri (dakika, gün başından itibaren).
const (
	// nominalDepartWorkMin, ortalama işe çıkış anıdır (08:00).
	nominalDepartWorkMin = 8 * minutesPerHour
	// nominalDepartHomeMin, ortalama eve dönüş anıdır (17:30).
	nominalDepartHomeMin = 17*minutesPerHour + 30

	// departJitterMin, çıkış/dönüş anlarının ajan başına sapma genliğidir.
	// ±45 dakika, işe geliş saatlerinin gerçekçi yayılımını verir.
	departJitterMin = 45

	// daysPerWeek ve haftanın ilk iş günü tanımı.
	daysPerWeek = 7
	// weekendStartDay, hafta sonunun başladığı gün indeksidir.
	// Koşu Pazartesi (gün 0) başlar; 5 = Cumartesi, 6 = Pazar.
	weekendStartDay = 5
)

// Clock, tick indeksini takvim bilgisine çeviren zaman modelidir.
//
// Koşu, gün 0'ın gece yarısında ve **Pazartesi** başlar; bu, haftalık
// periyodisitenin referans noktasıdır.
type Clock struct {
	tickMinutes int
}

// NewClock, tick uzunluğundan bir zaman modeli kurar.
// tickMinutes senaryo config'inden gelir (simulation.tick_minutes = 5).
func NewClock(tickMinutes int) (Clock, error) {
	if tickMinutes <= 0 {
		return Clock{}, fmt.Errorf("saat: tick uzunluğu pozitif olmalı (%d dk)", tickMinutes)
	}
	if minutesPerDay%tickMinutes != 0 {
		return Clock{}, fmt.Errorf(
			"saat: tick uzunluğu günü tam bölmeli (%d dk, 1440'ın böleni olmalı)", tickMinutes)
	}
	return Clock{tickMinutes: tickMinutes}, nil
}

// TickMinutes, tick uzunluğunu döndürür (dakika).
func (c Clock) TickMinutes() int { return c.tickMinutes }

// TicksPerDay, bir gündeki tick sayısını döndürür (5 dk için 288).
func (c Clock) TicksPerDay() int { return minutesPerDay / c.tickMinutes }

// MinuteOfDay, tick indeksinin gün içindeki dakikasını döndürür [0, 1440).
func (c Clock) MinuteOfDay(tick int) int {
	return (tick * c.tickMinutes) % minutesPerDay
}

// DayIndex, tick indeksinin koşu içindeki gün numarasını döndürür (0 tabanlı).
func (c Clock) DayIndex(tick int) int {
	return tick * c.tickMinutes / minutesPerDay
}

// IsWeekend, tick'in hafta sonuna düşüp düşmediğini bildirir.
// Koşu Pazartesi başlar; gün 5 ve 6 hafta sonudur.
func (c Clock) IsWeekend(tick int) bool {
	return c.DayIndex(tick)%daysPerWeek >= weekendStartDay
}

// personalizeRoutine, ajana özgü çıkış ve dönüş anlarını üretir.
//
// Sapma simetriktir (±departJitterMin) ve dönüş anının işe varıştan sonra
// kalmasını garanti eder; aksi hâlde State.Validate reddederdi.
func personalizeRoutine(rng randSource, commuteMin int) (departWorkMin, departHomeMin int) {
	jitter := func() int {
		return int(math.Round((rng.Float64()*2 - 1) * departJitterMin))
	}

	departWorkMin = clampMinuteOfDay(nominalDepartWorkMin + jitter())
	departHomeMin = clampMinuteOfDay(nominalDepartHomeMin + jitter())

	// İş günü en az bir tam yolculuk süresi kadar sürmelidir.
	if minEnd := departWorkMin + commuteMin + minCommuteMin; departHomeMin <= minEnd {
		departHomeMin = clampMinuteOfDay(minEnd + 1)
	}
	// Eve varış gece yarısını aşmamalıdır: model günlük döngü varsayar.
	if arrival := departHomeMin + commuteMin; arrival >= minutesPerDay {
		departHomeMin = minutesPerDay - commuteMin - 1
	}
	return departWorkMin, departHomeMin
}

// clampMinuteOfDay, dakikayı gün sınırlarına kırpar.
func clampMinuteOfDay(m int) int {
	switch {
	case m < 0:
		return 0
	case m >= minutesPerDay:
		return minutesPerDay - 1
	default:
		return m
	}
}

// PhaseAt, ajanın verilen tick'teki evresini döndürür.
//
// Saf fonksiyondur: ajan durumu ve tick dışında hiçbir girdiye bakmaz, bu
// yüzden herhangi bir tick'e doğrudan atlanabilir (K10).
func PhaseAt(s State, c Clock, tick int) Phase {
	// Haftalık periyodisite: hafta sonu iş evresi yoktur.
	if c.IsWeekend(tick) {
		return PhaseHome
	}

	t := c.MinuteOfDay(tick)
	switch {
	case t < s.DepartWorkMin:
		return PhaseHome
	case t < s.DepartWorkMin+s.CommuteMin:
		return PhaseCommuteToWork
	case t < s.DepartHomeMin:
		return PhaseWork
	case t < s.DepartHomeMin+s.CommuteMin:
		return PhaseCommuteToHome
	default:
		return PhaseHome
	}
}

// CommuteProgress, yolculuk evresindeki ilerlemeyi [0,1] aralığında döndürür.
//
// Yolculuk evresinde değilse 0 döner. Hareket katmanı (T-E02-09) konumu bu
// oranla ev ve iş çapaları arasında ara değerler.
func CommuteProgress(s State, c Clock, tick int) float64 {
	phase := PhaseAt(s, c, tick)
	if !phase.IsCommuting() {
		return 0
	}

	t := c.MinuteOfDay(tick)
	var elapsed int
	if phase == PhaseCommuteToWork {
		elapsed = t - s.DepartWorkMin
	} else {
		elapsed = t - s.DepartHomeMin
	}

	progress := float64(elapsed) / float64(s.CommuteMin)
	switch {
	case progress < 0:
		return 0
	case progress > 1:
		return 1
	default:
		return progress
	}
}
