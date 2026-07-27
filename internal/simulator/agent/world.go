// T-E02-09 — Tick boru hattı: hareket + radyo değerlendirmesi.
//
// Sprint 2'nin bütünleşme noktasıdır. Her tick'te sırayla:
//
//  1. Evre belirlenir      (T-E02-08 rutin)
//  2. Konum güncellenir    (T-E02-09 hareket, çift tampon)
//  3. Best-server seçilir  (T-E02-12) — içinde:
//     yol kaybı (T-E02-10) · gölgeleme + LOS (T-E02-11) · anten deseni
//  4. Kapsama kararı verilir (ADR-08/3)
//
// Sprint 2 burada durur: olay üretimi, TA, HTS kaydı, ground truth ve Kafka
// **yoktur** — bunlar Sprint 3 kapsamındadır. Kapsama dışı tick'ler yalnızca
// istatistik olarak sayılır.
package agent

import (
	"fmt"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/radio"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Observation, tek bir ajanın tek bir tick'teki gözlemidir.
//
// Sprint 3'te olay üreteci bu yapıyı girdi olarak alacaktır.
type Observation struct {
	// AgentID, ajanın kimliğidir.
	AgentID int
	// Tick, gözlemin ait olduğu tick indeksidir.
	Tick int
	// Pos, ajanın konumudur (ENU, metre).
	Pos geo.Point
	// Phase, ajanın evresidir.
	Phase Phase
	// Serving, best-server sonucudur; Covered alanı kapsama kararını taşır.
	Serving radio.Serving
}

// CoverageStats, koşu boyunca biriken kapsama istatistikleridir (ADR-08/4).
type CoverageStats struct {
	// TotalTicks, değerlendirilen toplam ajan-tick sayısıdır.
	TotalTicks int64
	// UncoveredTicks, kapsama dışı kalan ajan-tick sayısıdır.
	UncoveredTicks int64
}

// NoCoverageRatio, kapsama dışı tick oranını döndürür [0,1].
//
// ADR-08/4: bu oran %5'i aşarsa senaryo config'i uyarı vermelidir
// (site yoğunluğu veya alan yarıçapı hatalı).
func (s CoverageStats) NoCoverageRatio() float64 {
	if s.TotalTicks == 0 {
		return 0
	}
	return float64(s.UncoveredTicks) / float64(s.TotalTicks)
}

// NoCoverageWarningThreshold, ADR-08/4'te belirtilen uyarı eşiğidir.
const NoCoverageWarningThreshold = 0.05

// ExceedsWarningThreshold, kapsama dışı oranının uyarı eşiğini aşıp
// aşmadığını bildirir.
func (s CoverageStats) ExceedsWarningThreshold() bool {
	return s.NoCoverageRatio() > NoCoverageWarningThreshold
}

// World, bir koşunun ajan popülasyonunu ve radyo değerlendirmesini yürütür.
type World struct {
	buf      *DoubleBuffer
	mobility *Mobility
	selector *radio.Selector

	// observations, son tick'in gözlemleridir. Kurulumda bir kez ayrılır ve
	// her tick yeniden kullanılır — tick başına ayırma yapılmaz.
	observations []Observation

	// tick, işlenmekte olan tick indeksidir. Güncelleme kapanışı (closure)
	// bunu okur; böylece kapanış tick'i yakalamak zorunda kalmaz ve
	// kurulumda bir kez oluşturulabilir (tick başına sıfır ayırma).
	tick int

	// updateFn, kurulumda bir kez oluşturulan güncelleme kapanışıdır.
	updateFn func(s State, index int) State

	stats CoverageStats
}

// NewWorld, ajan dünyasını kurar.
//
// workers ≤ 0 verilirse mevcut çekirdek sayısı kullanılır.
func NewWorld(agents []State, mobility *Mobility, selector *radio.Selector, workers int) (*World, error) {
	if mobility == nil {
		return nil, fmt.Errorf("dünya: hareket modeli zorunlu")
	}
	if selector == nil {
		return nil, fmt.Errorf("dünya: best-server seçicisi zorunlu")
	}

	buf, err := NewDoubleBuffer(agents, workers)
	if err != nil {
		return nil, fmt.Errorf("dünya: %w", err)
	}

	w := &World{
		buf:          buf,
		mobility:     mobility,
		selector:     selector,
		observations: make([]Observation, len(agents)),
	}
	// Kapanış bir kez oluşturulur ve koşu boyunca yeniden kullanılır.
	w.updateFn = func(s State, i int) State {
		next := w.mobility.Advance(s, w.tick)
		w.observations[i] = Observation{
			AgentID: next.ID,
			Tick:    w.tick,
			Pos:     next.Pos,
			Phase:   next.Phase,
			Serving: w.selector.Select(next.Pos),
		}
		return next
	}
	return w, nil
}

// AgentCount, popülasyon büyüklüğünü döndürür.
func (w *World) AgentCount() int { return w.buf.Len() }

// Agents, mevcut ajan durumlarını döndürür (salt okunur).
func (w *World) Agents() []State { return w.buf.Current() }

// Observations, son Tick çağrısının gözlemlerini döndürür (salt okunur).
func (w *World) Observations() []Observation { return w.observations }

// Stats, biriken kapsama istatistiklerini döndürür.
func (w *World) Stats() CoverageStats { return w.stats }

// Close, dünyanın işçi havuzunu kapatır.
//
// Koşu bittiğinde çağrılmalıdır; aksi hâlde işçi goroutine'leri sızar.
// Birden çok kez çağrılabilir.
func (w *World) Close() { w.buf.Close() }

// Tick, tüm ajanları verilen tick'e taşır ve radyo değerlendirmesini yapar.
//
// Ajan güncellemeleri paralel çalışır (goroutine başına ajan bloğu); her
// goroutine yalnızca kendi indeksine yazdığından kilit yoktur. Sonuç
// goroutine zamanlamasından bağımsızdır (K10).
func (w *World) Tick(tick int) {
	w.tick = tick
	w.buf.Step(w.updateFn)
	w.collectStats()
}

// TickSerial, Tick'in tek goroutine'li karşılığıdır.
//
// Paralel Tick ile **birebir aynı** sonucu üretmelidir; determinizmin
// eşzamanlılıktan bağımsızlığını doğrulamanın referans yoludur (K10).
func (w *World) TickSerial(tick int) {
	w.tick = tick
	w.buf.StepSerial(w.updateFn)
	w.collectStats()
}

// collectStats, kapsama sayaçlarını günceller.
//
// Bariyerden sonra, tek goroutine'de çalışır: paylaşılan sayaca eşzamanlı
// yazma olmaz, atomik işlem gerekmez.
func (w *World) collectStats() {
	w.stats.TotalTicks += int64(len(w.observations))
	for i := range w.observations {
		if !w.observations[i].Serving.Covered {
			w.stats.UncoveredTicks++
		}
	}
}
