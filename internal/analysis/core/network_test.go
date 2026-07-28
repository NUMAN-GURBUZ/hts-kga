package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/geo"
)

// Test şebekesi, ADR-17 profillerinin analiz tarafındaki karşılığıdır.
//
// Envanter **simülatörden alınmaz**: ADR-20 gereği analiz katmanı
// internal/simulator'a bağımlı olamaz ve bu kısıt testlerde de gevşetilmez.
// Şebeke burada bağımsız olarak kurulur; parametreler Sprint 1 kararlarıyla
// (109 site, efektif ISD) ve profil değerleriyle aynıdır.
const (
	testOriginLat = 38.6748
	testOriginLon = 39.2225
)

// profileSpec, bir morfoloji profilinin analiz için gereken alanlarıdır.
type profileSpec struct {
	name         string
	model        string
	isdM         float64 // efektif ISD (Sprint 1: kentsel 912 m / kırsal 3649 m)
	areaRadiusM  float64
	antHeightM   float64
	tiltDeg      float64
	eirpDBm      float64
	rMaxM        float64 // T-E02-05 link budget çözümü
	bands        []int
	rxSensDBm    float64
	sigmaNominal float64
}

func urbanProfile() profileSpec {
	return profileSpec{
		name: "kentsel", model: "UMa",
		isdM: 912, areaRadiusM: 5000,
		antHeightM: 25, tiltDeg: 6, eirpDBm: 58,
		rMaxM: 6125.5, bands: []int{1800, 2100, 2600},
		rxSensDBm: -110, sigmaNominal: 7,
	}
}

func ruralProfile() profileSpec {
	return profileSpec{
		name: "kırsal", model: "RMa",
		isdM: 3649, areaRadiusM: 20000,
		antHeightM: 45, tiltDeg: 3, eirpDBm: 62,
		rMaxM: 29097.5, bands: []int{800, 900, 1800},
		rxSensDBm: -110, sigmaNominal: 7,
	}
}

// staticSource, sabit hücre listesi döndüren envanter kaynağıdır.
type staticSource struct{ cells []redis.CellParams }

func (s staticSource) ScanCells(context.Context, uuid.UUID) ([]redis.CellParams, error) {
	return s.cells, nil
}

// buildNetwork, profil parametreleriyle hex kafeste bir şebeke kurar.
//
// Site yerleşimi ADR-07'nin pointy-top hex kafesidir; her siteye 0/120/240
// azimutlu üç sektör konur ve bandlar site indisine göre döngüsel atanır.
func buildNetwork(t testing.TB, p profileSpec) *params.Inventory {
	t.Helper()

	projector, err := geo.NewProjector(testOriginLat, testOriginLon)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	lattice, err := geo.NewHexGrid(p.isdM)
	if err != nil {
		t.Fatalf("NewHexGrid: %v", err)
	}

	var cells []redis.CellParams
	for i, a := range lattice.Cover(geo.Point{}, p.areaRadiusM) {
		siteENU := lattice.Center(a)
		siteWGS := projector.Inverse(siteENU)
		siteID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("site:%d:%d", a.Q, a.R)))
		band := p.bands[i%len(p.bands)]

		for sector := 0; sector < 3; sector++ {
			cells = append(cells, redis.CellParams{
				CellID:     uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s:%d", siteID, sector))),
				SiteID:     siteID,
				Azimuth:    float64(sector) * 120,
				BeamWidth:  65,
				FreqMHz:    band,
				EIRPdBm:    p.eirpDBm,
				AntHeightM: p.antHeightM,
				TiltDeg:    p.tiltDeg,
				RMaxM:      p.rMaxM,
				Morphology: p.name,
				ModelType:  p.model,
				Lat:        siteWGS.Lat,
				Lon:        siteWGS.Lon,
			})
		}
	}

	inv, err := params.Load(context.Background(), staticSource{cells: cells},
		uuid.New(), testOriginLat, testOriginLon)
	if err != nil {
		t.Fatalf("params.Load: %v", err)
	}
	return inv
}

// utHeight, alıcı yüksekliğidir.
const utHeight = rf.UTHeightM
