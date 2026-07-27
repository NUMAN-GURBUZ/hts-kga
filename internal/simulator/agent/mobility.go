// T-E02-09 — Hareket: hız profili ve konum gürültüsü.
//
// Konum, ajanın evresinden **türetilir**; adım adım biriktirilmez:
//
//	EV / İŞ evresi   → çapa + küçük yerel gürültü
//	YOLCULUK evresi  → çapalar arası doğrusal ara değer + yol gürültüsü
//
// # Neden türetilmiş konum, biriktirilmiş değil
//
// Biriktirmeli (p += v·Δt) bir model üç sorun doğururdu:
//   - Kayan nokta hatası 8.640 tick boyunca birikir, ajan çapasını ıskalar
//   - Herhangi bir tick'e doğrudan atlanamaz (test ve hata ayıklama zorlaşır)
//   - Tick başına çekilen rastgele sayı adedi değişirse akış kayar (K10 riski)
//
// Türetilmiş konumda her tick bağımsız hesaplanır: aynı tick daima aynı
// konumu verir, goroutine sırası önemsizdir.
//
// # Hız profili
//
// Yolculuk hızı doğrudan uygulanmaz; ADR-17 profil hızı ile mesafeden
// türetilen yolculuk **süresi** (T-E02-07) üzerinden dolaylı olarak etkir.
// Anlık hız, ilerleme oranındaki değişimden doğar ve gürültü ile modüle edilir.
package agent

import (
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Hareket gürültüsü ölçekleri (metre).
const (
	// anchorNoiseM, ev veya iş çapası çevresindeki yerel dolaşma yarıçapıdır.
	// Bina içi/çevresi hareketi temsil eder; hücre değiştirecek kadar büyük
	// değildir.
	anchorNoiseM = 25.0

	// pathNoiseM, yolculuk güzergâhındaki sapma genliğidir. Gerçek yollar
	// düz çizgi olmadığından ara değer eğrisi bu ölçekte bozulur.
	pathNoiseM = 60.0

	// speedJitterFraction, anlık hızdaki bağıl dalgalanmadır (±%25):
	// trafik, sinyalizasyon ve yürüyüş parçalarını temsil eder.
	speedJitterFraction = 0.25
)

// Gürültü akışı ayraçları (aynı tick'te bağımsız çekimler için).
const (
	purposeNoiseX uint64 = 0x91
	purposeNoiseY uint64 = 0x92
	purposeSpeed  uint64 = 0x93
)

// Mobility, ajan konumlarını hesaplayan hareket modelidir.
//
// Değişmezdir; 1000 goroutine tarafından kilitsiz paylaşılabilir.
type Mobility struct {
	seed  uint64
	clock Clock
}

// NewMobility, hareket modelini kurar.
func NewMobility(seed int64, clock Clock) *Mobility {
	return &Mobility{
		seed:  mix64(uint64(seed), streamMobility),
		clock: clock,
	}
}

// Clock, hareket modelinin zaman modelini döndürür.
func (m *Mobility) Clock() Clock { return m.clock }

// PositionAt, ajanın verilen tick'teki konumunu döndürür (ENU, metre).
//
// Saf fonksiyondur ve yığın ayırma yapmaz: tick başına ajan sayısı kadar
// çağrılır (8,64 milyon çağrı).
func (m *Mobility) PositionAt(s State, tick int) geo.Point {
	phase := PhaseAt(s, m.clock, tick)

	switch phase {
	case PhaseHome:
		return m.jitterAround(s.Home, s.ID, tick, anchorNoiseM)

	case PhaseWork:
		return m.jitterAround(s.Work, s.ID, tick, anchorNoiseM)

	case PhaseCommuteToWork:
		return m.alongPath(s, tick, s.Home, s.Work)

	case PhaseCommuteToHome:
		return m.alongPath(s, tick, s.Work, s.Home)
	}
	return s.Home
}

// Advance, ajanın durumunu bir sonraki tick'e taşır.
//
// Girdi durumunu **değiştirmez**; güncellenmiş kopyayı döndürür. Çift
// tamponlu güncellemenin (doublebuf.go) temel yapı taşıdır: okuma ve yazma
// farklı tamponlarda olduğundan kilit gerekmez.
func (m *Mobility) Advance(s State, tick int) State {
	next := s
	next.Phase = PhaseAt(s, m.clock, tick)
	next.Pos = m.PositionAt(s, tick)
	return next
}

// jitterAround, çapa çevresinde deterministik yerel gürültü uygular.
//
// Gürültü, konumdan değil (ajan, tick) çiftinden türetilir: ajan çapasında
// dururken bile küçük hareketler yapar, ama bu hareketler tekrarlanabilirdir.
func (m *Mobility) jitterAround(anchor geo.Point, agentID, tick int, scaleM float64) geo.Point {
	// (0,1) → (−1,1) dönüşümü; iki bağımsız eksen.
	nx := hashUnit(m.seed, agentID, tick, purposeNoiseX)*2 - 1
	ny := hashUnit(m.seed, agentID, tick, purposeNoiseY)*2 - 1

	return geo.Point{
		X: anchor.X + nx*scaleM,
		Y: anchor.Y + ny*scaleM,
	}
}

// alongPath, yolculuk güzergâhındaki konumu hesaplar.
//
// Temel konum, ilerleme oranıyla doğrusal ara değerdir. Üzerine iki bileşen
// eklenir:
//   - hız dalgalanması: ilerleme oranı ±%25 modüle edilir (trafik, duraklar)
//   - güzergâh sapması: düz çizgiden yanal kayma (gerçek yollar düz değildir)
func (m *Mobility) alongPath(s State, tick int, from, to geo.Point) geo.Point {
	progress := CommuteProgress(s, m.clock, tick)

	// Hız dalgalanması ilerlemeyi öne/arkaya kaydırır, [0,1] dışına taşmaz.
	jitter := (hashUnit(m.seed, s.ID, tick, purposeSpeed)*2 - 1) * speedJitterFraction
	progress = clampUnit(progress * (1 + jitter))

	base := geo.Point{
		X: from.X + (to.X-from.X)*progress,
		Y: from.Y + (to.Y-from.Y)*progress,
	}

	// Yanal sapma: güzergâhın ortasında en büyük, uçlarda sıfır olmalıdır —
	// aksi hâlde ajan çapasının üstüne değil yanına varırdı.
	taper := 4 * progress * (1 - progress) // [0,1], tepe noktası progress=0.5
	return m.jitterAround(base, s.ID, tick, pathNoiseM*taper)
}

// SpeedKMH, iki tick arasındaki ortalama yer değiştirme hızını döndürür (km/s).
//
// Bütünlük denetiminin (Kural 2, 300 km/s üst sınırı) simülatör tarafındaki
// karşılığıdır: üretilen hareket bu sınırı aşmamalıdır.
func (m *Mobility) SpeedKMH(s State, fromTick, toTick int) float64 {
	if toTick <= fromTick {
		return 0
	}
	distanceM := geo.Distance(m.PositionAt(s, fromTick), m.PositionAt(s, toTick))
	hours := float64(toTick-fromTick) * float64(m.clock.TickMinutes()) / minutesPerHour
	return (distanceM / metersPerKM) / hours
}

// clampUnit, değeri [0,1] aralığına kırpar.
func clampUnit(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	case math.IsNaN(v):
		return 0
	default:
		return v
	}
}
