// T-E02-04 — Site başına 3 sektör üretimi (azimut / hüzme / tilt / frekans / EIRP).
//
// Karar (kesin): sektör azimutları 0°, 120°, 240°'dir. Rastgele sapma yoktur —
// yerleşim düzenli hex kafes olduğu için azimut sapması eklemek, kapsama
// simetrisini bozmaktan başka bir şey yapmaz ve tekrarlanabilirliği zorlaştırır.
//
// Hüzme genişliği, tilt, EIRP ve anten yüksekliği MorphologyProfile'dan gelir
// (ADR-17); frekans, profil band dağılımından ağırlıklı seçilir (frequency.go).
package inventory

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// SectorAzimuths, site başına üç sektörün azimut açılarıdır (derece, kuzeyden
// saat yönünde). Sabittir — senaryolar arası fark yoktur.
var SectorAzimuths = [3]float64{0, 120, 240}

// SectorsPerSite, site başına sektör sayısı (Bölüm C.1: 100–150 site × 3 sektör).
const SectorsPerSite = len(SectorAzimuths)

// Cell, bir sektörü temsil eder — `cells` tablosunun koddaki karşılığıdır
// (internal/storage/migrations/001_schema.sql).
//
// ENU alanı veritabanına yazılmaz; simülasyon içi mesafe ve açı hesaplarında
// tekrar tekrar izdüşüm yapmamak için taşınır.
type Cell struct {
	ID         uuid.UUID
	RunID      uuid.UUID
	SiteID     uuid.UUID
	Azimuth    float64 // derece [0, 360)
	BeamWidth  float64 // derece (0, 360]
	FreqMHz    int
	EIRPdBm    float64
	AntHeightM float64
	TiltDeg    float64

	// RMaxM, link budget önhesabıdır ve T-E02-05'te (rmax.go) atanır.
	// BuildSectors bu alanı sıfır bırakır.
	RMaxM float64

	Morphology config.Morphology
	ModelType  config.PropagationModel

	Location geo.WGS84 // cells.location (GEOGRAPHY POINT 4326)
	ENU      geo.Point // yerel düzlem konumu (saklanmaz)
}

// cellNamespace, sektör UUID'leri için proje sabiti (ADR-01 deterministik kimlik).
var cellNamespace = uuid.NewSHA1(uuid.NameSpaceOID, []byte("hts-kga/cell"))

// cellID, site kimliği ve sektör indisinden deterministik UUIDv5 üretir.
func cellID(siteID uuid.UUID, sectorIdx int) uuid.UUID {
	return uuid.NewSHA1(cellNamespace, []byte(fmt.Sprintf("%s:%d", siteID, sectorIdx)))
}

// BuildSectors, yerleşimdeki her site için 3 sektör üretir.
//
// Dönen dilim deterministik sıradadır: site sırası korunur, her site içinde
// sektörler azimut sırasıyla (0°, 120°, 240°) dizilir. Toplam hücre sayısı
// daima len(sites) · 3'tür.
//
// r_max alanı burada doldurulmaz; AssignRMax ile atanır (T-E02-05).
func BuildSectors(layout *Layout, scn *config.Scenario) ([]Cell, error) {
	if layout == nil || scn == nil {
		return nil, fmt.Errorf("sektör üretimi: yerleşim ve senaryo zorunlu")
	}
	if len(layout.Sites) == 0 {
		return nil, fmt.Errorf("sektör üretimi: yerleşimde site yok")
	}

	p := scn.Profile
	selector, err := newBandSelector(p.FreqDistribution)
	if err != nil {
		return nil, err
	}
	rng := newRNG(scn.Run.Seed, streamFrequency)

	cells := make([]Cell, 0, len(layout.Sites)*SectorsPerSite)
	for _, site := range layout.Sites {
		for idx, az := range SectorAzimuths {
			cells = append(cells, Cell{
				ID:         cellID(site.ID, idx),
				RunID:      layout.RunID,
				SiteID:     site.ID,
				Azimuth:    az,
				BeamWidth:  p.BeamWidthDeg,
				FreqMHz:    selector.pick(rng.Float64()),
				EIRPdBm:    p.EIRPdBm,
				AntHeightM: p.AntHeightM,
				TiltDeg:    p.TiltDeg,
				Morphology: p.Morphology,
				ModelType:  p.PropagationModel,
				Location:   site.WGS84,
				ENU:        site.ENU,
			})
		}
	}
	return cells, nil
}

// Validate, hücrenin `cells` tablosu CHECK kısıtlarını sağladığını denetler.
//
// Veritabanına yazmadan önce çağrılır: kısıt ihlalini toplu yükleme sırasında
// değil, üretim anında yakalamak hata ayıklamayı kolaylaştırır (T-E02-06).
func (c Cell) Validate() error {
	if c.ID == uuid.Nil {
		return fmt.Errorf("hücre kimliği boş")
	}
	if c.RunID == uuid.Nil {
		return fmt.Errorf("hücre %s: run_id boş", c.ID)
	}
	if c.SiteID == uuid.Nil {
		return fmt.Errorf("hücre %s: site_id boş", c.ID)
	}
	if !(c.Azimuth >= 0 && c.Azimuth < 360) {
		return fmt.Errorf("hücre %s: azimuth [0,360) olmalı (%g)", c.ID, c.Azimuth)
	}
	if !(c.BeamWidth > 0 && c.BeamWidth <= 360) {
		return fmt.Errorf("hücre %s: beam_width (0,360] olmalı (%g)", c.ID, c.BeamWidth)
	}
	if c.FreqMHz <= 0 {
		return fmt.Errorf("hücre %s: freq_mhz pozitif olmalı (%d)", c.ID, c.FreqMHz)
	}
	if !(c.AntHeightM > 0) {
		return fmt.Errorf("hücre %s: ant_height pozitif olmalı (%g)", c.ID, c.AntHeightM)
	}
	if !(c.RMaxM > 0) {
		return fmt.Errorf("hücre %s: r_max_m pozitif olmalı (%g) — AssignRMax çağrıldı mı?",
			c.ID, c.RMaxM)
	}
	if !c.Morphology.Valid() {
		return fmt.Errorf("hücre %s: geçersiz morphology %q", c.ID, c.Morphology)
	}
	if !c.ModelType.Valid() {
		return fmt.Errorf("hücre %s: geçersiz model_type %q", c.ID, c.ModelType)
	}
	if c.Location.Lat < -90 || c.Location.Lat > 90 {
		return fmt.Errorf("hücre %s: geçersiz enlem (%g)", c.ID, c.Location.Lat)
	}
	if c.Location.Lon < -180 || c.Location.Lon > 180 {
		return fmt.Errorf("hücre %s: geçersiz boylam (%g)", c.ID, c.Location.Lon)
	}
	return nil
}
