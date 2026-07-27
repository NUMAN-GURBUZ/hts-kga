// T-E02-07 — Ajan yerleşimi: reddetme örneklemesi (ADR-08/2).
//
// Karar (ADR-08): ajanların ev ve iş çapaları **daima kapsama içinde**
// olmalıdır. Aday nokta üretilir; o noktada en yüksek alınan güç alıcı
// duyarlılığının altındaysa reddedilip yeniden üretilir. Üst sınır 100
// denemedir; aşılırsa senaryo config'i hatalı sayılır ve koşu başlatılmaz
// (fail-fast).
//
// Neden fail-fast: kapsama dışında başlayan bir ajan hiç olay üretemez.
// Sessizce kabul etmek, sonuçları fark edilmeden bozardı — koşuyu hiç
// başlatmamak, yanlış sonuç üretmekten iyidir.
package agent

import (
	"fmt"
	"math"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/simulator/radio"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

const (
	// maxPlacementAttempts, ADR-08/2'de belirtilen deneme üst sınırıdır.
	maxPlacementAttempts = 100

	// metersPerKM, profil mesafelerinin birim dönüşümüdür.
	metersPerKM = 1000.0

	// minutesPerHour / minutesPerDay, rutin zaman hesapları için.
	minutesPerHour = 60
	minutesPerDay  = 24 * minutesPerHour

	// minCommuteMin, yolculuk süresinin alt sınırıdır: çok kısa mesafelerde
	// süre sıfıra düşerse evre geçişi anlamsızlaşır.
	minCommuteMin = 1
)

// CoverageProbe, bir noktanın kapsama içinde olup olmadığını bildirir.
//
// Arayüz tüketici tarafında tanımlanır: yerleşim, radyo katmanının tamamına
// değil yalnızca bu tek yeteneğe bağlıdır. Testlerde gerçek şebeke kurmadan
// sahte uygulama verilebilir.
type CoverageProbe interface {
	// MaxRxDBm, verilen noktadaki en yüksek alınan gücü döndürür (dBm).
	MaxRxDBm(p geo.Point) float64
	// RxSensitivityDBm, kapsama eşiğini döndürür (dBm, negatif).
	RxSensitivityDBm() float64
}

// Derleme zamanı denetimi: radyo seçicisi sondayı karşılamalıdır.
var _ CoverageProbe = (*radio.Selector)(nil)

// PlacementConfig, ajan yerleşiminin girdileridir.
type PlacementConfig struct {
	// Count, üretilecek ajan sayısıdır (simulation.agents).
	Count int
	// Seed, koşu tohumudur (run.seed).
	Seed int64
	// AreaRadiusM, kapsama alanı yarıçapıdır (ADR-08).
	AreaRadiusM float64
	// HomeWorkMinM / HomeWorkMaxM, ev–iş mesafe aralığıdır (ADR-17 profili).
	HomeWorkMinM float64
	HomeWorkMaxM float64
	// CommuteSpeedKMH, yolculuk hızıdır (ADR-17 profili).
	CommuteSpeedKMH float64
	// Probe, kapsama sondasıdır.
	Probe CoverageProbe
}

// PlacementConfigFrom, senaryo config'inden yerleşim girdilerini türetir.
//
// ADR-17 profilinin ev–iş mesafesi ve yolculuk hızı alanları burada
// kilometreden metreye çevrilir; başka hiçbir yerde birim dönüşümü yapılmaz.
func PlacementConfigFrom(scn *config.Scenario, probe CoverageProbe) (PlacementConfig, error) {
	if scn == nil {
		return PlacementConfig{}, fmt.Errorf("ajan yerleşimi: senaryo zorunlu")
	}
	p := scn.Profile
	return PlacementConfig{
		Count:           scn.Simulation.Agents,
		Seed:            scn.Run.Seed,
		AreaRadiusM:     p.AreaRadiusM(),
		HomeWorkMinM:    p.AgentHomeWorkMinKM * metersPerKM,
		HomeWorkMaxM:    p.AgentHomeWorkMaxKM * metersPerKM,
		CommuteSpeedKMH: p.CommuteSpeedKMH,
		Probe:           probe,
	}, nil
}

// validate, yerleşim girdilerinin tutarlılığını denetler.
func (c PlacementConfig) validate() error {
	switch {
	case c.Count <= 0:
		return fmt.Errorf("ajan yerleşimi: ajan sayısı pozitif olmalı (%d)", c.Count)
	case !(c.AreaRadiusM > 0):
		return fmt.Errorf("ajan yerleşimi: alan yarıçapı pozitif olmalı (%g m)", c.AreaRadiusM)
	case !(c.HomeWorkMinM > 0) || c.HomeWorkMaxM <= c.HomeWorkMinM:
		return fmt.Errorf("ajan yerleşimi: ev–iş aralığı geçersiz (%g–%g m)",
			c.HomeWorkMinM, c.HomeWorkMaxM)
	case !(c.CommuteSpeedKMH > 0):
		return fmt.Errorf("ajan yerleşimi: yolculuk hızı pozitif olmalı (%g km/s)", c.CommuteSpeedKMH)
	case c.Probe == nil:
		return fmt.Errorf("ajan yerleşimi: kapsama sondası zorunlu")
	}

	// Ev–iş alt sınırı alan çapını aşarsa hiçbir çift bulunamaz.
	if c.HomeWorkMinM > 2*c.AreaRadiusM {
		return fmt.Errorf(
			"ajan yerleşimi: ev–iş alt sınırı (%g m) alan çapından (%g m) büyük — "+
				"senaryo config'i tutarsız", c.HomeWorkMinM, 2*c.AreaRadiusM)
	}
	return nil
}

// PlaceAgents, kapsama garantili ajan kümesi üretir.
//
// Her ajan için sırayla:
//  1. Ev çapası — alan içinde düzgün dağılım + reddetme örneklemesi
//  2. İş çapası — ev çevresinde profil mesafe aralığında + reddetme örneklemesi
//  3. Rutin kişiselleştirmesi (T-E02-08) ve başlangıç konumu
//
// Deterministiktir: aynı tohum daima aynı ajan kümesini verir (K10).
func PlaceAgents(cfg PlacementConfig) ([]State, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	rng := newRNG(cfg.Seed, streamAgentPlacement)
	routineRNG := newRNG(cfg.Seed, streamRoutine)
	threshold := cfg.Probe.RxSensitivityDBm()

	agents := make([]State, cfg.Count)
	for i := range agents {
		home, err := placeAnchorInArea(rng, cfg, threshold)
		if err != nil {
			return nil, fmt.Errorf("ajan %d ev çapası: %w", i, err)
		}

		work, err := placeWorkAnchor(rng, cfg, threshold, home)
		if err != nil {
			return nil, fmt.Errorf("ajan %d iş çapası: %w", i, err)
		}

		s := State{
			ID:    i,
			Home:  home,
			Work:  work,
			Pos:   home, // koşu gece yarısı başlar → ajan evdedir
			Phase: PhaseHome,
		}
		s.CommuteMin = commuteMinutes(geo.Distance(home, work), cfg.CommuteSpeedKMH)
		s.DepartWorkMin, s.DepartHomeMin = personalizeRoutine(routineRNG, s.CommuteMin)

		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("ajan yerleşimi: %w", err)
		}
		agents[i] = s
	}
	return agents, nil
}

// placeAnchorInArea, alan diski içinde kapsama garantili bir çapa üretir
// (ADR-08/2 sözde kodunun birebir karşılığı).
func placeAnchorInArea(rng randSource, cfg PlacementConfig, thresholdDBm float64) (geo.Point, error) {
	for attempt := 0; attempt < maxPlacementAttempts; attempt++ {
		p := uniformInDisk(rng, cfg.AreaRadiusM)
		if cfg.Probe.MaxRxDBm(p) >= thresholdDBm {
			return p, nil
		}
	}
	return geo.Point{}, fmt.Errorf(
		"%d denemede kapsama içinde nokta bulunamadı — senaryo config'i geçersiz "+
			"(site yoğunluğu veya alan yarıçapı hatalı olabilir)", maxPlacementAttempts)
}

// placeWorkAnchor, ev çapasından profil mesafe aralığında, alan içinde ve
// kapsama garantili bir iş çapası üretir.
func placeWorkAnchor(rng randSource, cfg PlacementConfig, thresholdDBm float64,
	home geo.Point) (geo.Point, error) {

	for attempt := 0; attempt < maxPlacementAttempts; attempt++ {
		p := uniformInAnnulus(rng, home, cfg.HomeWorkMinM, cfg.HomeWorkMaxM)

		// Alan diskinin dışına taşan adaylar elenir (ADR-08 sınırlayıcı kutu).
		if p.Norm() > cfg.AreaRadiusM {
			continue
		}
		if cfg.Probe.MaxRxDBm(p) >= thresholdDBm {
			return p, nil
		}
	}
	return geo.Point{}, fmt.Errorf(
		"%d denemede uygun iş çapası bulunamadı (ev–iş aralığı %g–%g m, alan yarıçapı %g m) — "+
			"senaryo config'i geçersiz",
		maxPlacementAttempts, cfg.HomeWorkMinM, cfg.HomeWorkMaxM, cfg.AreaRadiusM)
}

// randSource, yerleşimin ihtiyaç duyduğu en küçük üreteç arayüzüdür.
type randSource interface {
	Float64() float64
}

// uniformInDisk, yarıçapı radiusM olan disk içinde **alan bakımından düzgün**
// dağılmış bir nokta üretir.
//
// r = R·√u dönüşümü zorunludur: r'yi doğrudan düzgün seçmek noktaları merkeze
// yığardı (halka alanı yarıçapla doğrusal büyür).
func uniformInDisk(rng randSource, radiusM float64) geo.Point {
	r := radiusM * math.Sqrt(rng.Float64())
	theta := 2 * math.Pi * rng.Float64()
	sin, cos := math.Sincos(theta)
	return geo.Point{X: r * cos, Y: r * sin}
}

// uniformInAnnulus, merkez çevresinde [minM, maxM] halkasında alan bakımından
// düzgün dağılmış bir nokta üretir.
//
//	r = √( r_min² + u·(r_max² − r_min²) )
func uniformInAnnulus(rng randSource, center geo.Point, minM, maxM float64) geo.Point {
	rMin2, rMax2 := minM*minM, maxM*maxM
	r := math.Sqrt(rMin2 + rng.Float64()*(rMax2-rMin2))
	theta := 2 * math.Pi * rng.Float64()
	sin, cos := math.Sincos(theta)
	return geo.Point{X: center.X + r*cos, Y: center.Y + r*sin}
}

// commuteMinutes, mesafe ve profil hızından tek yön yolculuk süresini
// dakika cinsinden türetir.
//
// Süre sabit değil türetilmiştir: kırsal ajan hem daha uzağa gider hem daha
// hızlı gider (ADR-17: 5–25 km / 70 km/s), kentsel ajan tersine (1–8 km /
// 30 km/s). Sabit bir süre bu profil farkını yok ederdi.
func commuteMinutes(distanceM, speedKMH float64) int {
	hours := (distanceM / metersPerKM) / speedKMH
	minutes := int(math.Ceil(hours * minutesPerHour))
	if minutes < minCommuteMin {
		return minCommuteMin
	}
	return minutes
}
