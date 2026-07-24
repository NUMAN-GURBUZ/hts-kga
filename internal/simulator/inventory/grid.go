// T-E02-03 — Pointy-top axial hex ızgara üzerinde site yerleşimi (ADR-07, ADR-08).
//
// Yerleşim kararı: profil ISD'si (urban 500 m / rural 2000 m) **nominal**
// değerdir; alanın tamamını kaplayan hex kafes bu aralıkla kurulduğunda site
// sayısı config'teki [sites_min, sites_max] aralığını aşar. Bu yüzden efektif
// ISD, alan ve hedef site sayısından türetilir:
//
//	N_hedef = seed'li rand[sites_min, sites_max]
//	A       = π · R²
//	ISD_eff = √( A / (0.866 · N_hedef) )          # hex kafes hücre alanı katsayısı
//
// Analitik başlangıçtan sonra ikiye bölme (bisection) ile site sayısının
// aralığa düştüğü ISD bulunur. Alanın tamamı kaplanır; dış halka boş kalmaz.
package inventory

import (
	"fmt"
	"math"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// hexPackingFactor, hex kafeste hücre alanı / ISD² oranıdır: √3/2 ≈ 0.866.
// Kafes yoğunluğu = 1 / (hexPackingFactor · ISD²).
var hexPackingFactor = math.Sqrt(3) / 2

// maxISDIterations, efektif ISD arayışındaki üst sınır (fail-fast).
const maxISDIterations = 64

// Site, bir baz istasyonu direğidir. Her site 3 sektör (hücre) taşır (T-E02-04).
type Site struct {
	ID    uuid.UUID // deterministik UUIDv5 — run_id + axial koordinat
	Axial geo.Axial // hex kafesteki konumu
	ENU   geo.Point // yerel düzlem konumu (metre)
	WGS84 geo.WGS84 // coğrafi konum (cells.location için)
}

// Layout, tamamlanmış site yerleşimidir.
type Layout struct {
	RunID uuid.UUID
	Sites []Site

	// NominalISDM, profilden gelen ISD'dir (ADR-17). Yerleşimde doğrudan
	// kullanılmaz; yayılım/mobilite modellerinde referans olarak kalır.
	NominalISDM float64

	// EffectiveISDM, kafesin gerçek merkez-merkez aralığıdır.
	EffectiveISDM float64

	// AreaRadiusM, kapsama alanı yarıçapıdır (ADR-08).
	AreaRadiusM float64
}

// siteNamespace, site UUID'leri için proje sabiti (ADR-01 deterministik kimlik).
var siteNamespace = uuid.NewSHA1(uuid.NameSpaceOID, []byte("hts-kga/site"))

// siteID, run_id ve hücre koordinatından deterministik UUIDv5 üretir.
// Aynı koşu aynı site kimliklerini verir; farklı koşular çakışmaz (ADR-05).
func siteID(runID uuid.UUID, a geo.Axial) uuid.UUID {
	name := fmt.Sprintf("%s:%d,%d", runID, a.Q, a.R)
	return uuid.NewSHA1(siteNamespace, []byte(name))
}

// PlaceSites, senaryo alanına hex kafes üzerinde site yerleştirir.
//
// Dönen dilim deterministik sıradadır (hex ızgara tarama sırası); aynı
// run.seed her koşuda aynı yerleşimi üretir (K10).
func PlaceSites(runID uuid.UUID, scn *config.Scenario, proj *geo.Projector) (*Layout, error) {
	if scn == nil || proj == nil {
		return nil, fmt.Errorf("site yerleşimi: senaryo ve izdüşüm zorunlu")
	}

	radiusM := scn.Profile.AreaRadiusM()
	minN, maxN := scn.Network.SitesMin, scn.Network.SitesMax

	// Hedef site sayısı — tohumdan türetilir, aralık içinde kalır.
	rng := newRNG(scn.Run.Seed, streamSitePlacement)
	targetN := minN + rng.IntN(maxN-minN+1)

	isd, cells, err := solveEffectiveISD(radiusM, targetN, minN, maxN)
	if err != nil {
		return nil, err
	}

	grid, err := geo.NewHexGrid(isd)
	if err != nil {
		return nil, fmt.Errorf("site yerleşimi: %w", err)
	}

	sites := make([]Site, len(cells))
	for i, a := range cells {
		enu := grid.Center(a)
		sites[i] = Site{
			ID:    siteID(runID, a),
			Axial: a,
			ENU:   enu,
			WGS84: proj.Inverse(enu),
		}
	}

	return &Layout{
		RunID:         runID,
		Sites:         sites,
		NominalISDM:   scn.Profile.InterSiteDistanceM,
		EffectiveISDM: isd,
		AreaRadiusM:   radiusM,
	}, nil
}

// solveEffectiveISD, site sayısını [minN, maxN] aralığına düşüren merkez-merkez
// aralığı bulur ve kaplanan hücreleri döndürür.
//
// Site sayısı ISD'ye göre monoton azalır; bu yüzden ikiye bölme uygulanabilir.
// Analitik tahmin başlangıç noktasıdır, aralık onun etrafında kurulur.
func solveEffectiveISD(radiusM float64, targetN, minN, maxN int) (float64, []geo.Axial, error) {
	if !(radiusM > 0) {
		return 0, nil, fmt.Errorf("site yerleşimi: alan yarıçapı pozitif olmalı (%g m)", radiusM)
	}
	if minN < 1 || maxN < minN {
		return 0, nil, fmt.Errorf("site yerleşimi: geçersiz site aralığı [%d, %d]", minN, maxN)
	}

	area := math.Pi * radiusM * radiusM
	analytic := math.Sqrt(area / (hexPackingFactor * float64(targetN)))

	// Arama aralığı: küçük ISD → çok site, büyük ISD → az site.
	lo, hi := analytic/4, analytic*4

	countAt := func(isd float64) ([]geo.Axial, error) {
		g, err := geo.NewHexGrid(isd)
		if err != nil {
			return nil, err
		}
		return g.Cover(geo.Point{}, radiusM), nil
	}

	// Analitik tahmin zaten aralıktaysa doğrudan kullan.
	if cells, err := countAt(analytic); err == nil && len(cells) >= minN && len(cells) <= maxN {
		return analytic, cells, nil
	}

	for i := 0; i < maxISDIterations; i++ {
		mid := (lo + hi) / 2
		cells, err := countAt(mid)
		if err != nil {
			return 0, nil, fmt.Errorf("site yerleşimi: %w", err)
		}

		n := len(cells)
		if n >= minN && n <= maxN {
			return mid, cells, nil
		}
		if n > targetN {
			lo = mid // daha seyrek kafes gerek → ISD büyüsün
		} else {
			hi = mid // daha sık kafes gerek → ISD küçülsün
		}
	}

	return 0, nil, fmt.Errorf(
		"site yerleşimi: %d iterasyonda [%d, %d] aralığına ulaşılamadı "+
			"(alan yarıçapı %.0f m, hedef %d site) — senaryo config'i gözden geçirin",
		maxISDIterations, minN, maxN, radiusM, targetN)
}
