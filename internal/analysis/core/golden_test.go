package core

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/baseline"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/density"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/geometry"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/analysis/params"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/rf"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/redis"
)

// Altın senaryo — analiz tarafı (plan BÖLÜM I, "Altın senaryo" satırı).
//
// Tek site, tek sektör, komşusuz, TA'sız ve σ → 0. Bu koşullarda kütle
// üretiminin **kapalı formlu** bir karşılığı vardır ve üretilen alan elle
// hesaplanan alanla karşılaştırılabilir:
//
//	σ → 0  ⇒  Φ((P_s − RxSens)/σ_eff) → 1{P_s ≥ RxSens}   (basamak fonksiyonu)
//	komşu yok  ⇒  w_nbr = 1                                (ADR-03)
//	TA yok     ⇒  w_TA  = 1                                (ADR-18)
//
// Açısal çarpan kapatıldığında (Options.IncludeAngular=false) kütle, kapsanan
// bölge üzerinde **düzgün dağılımdır**. O hâlde:
//
//	alan(M@c) ≈ c · alan(S),   S = dilim ∩ {P_s ≥ RxSens}
//
// alan(S) bağımsız olarak hesaplanır: her 0,05°'lik yönde kapsama yarıçapı
// link bütçesinden ikiye bölmeyle çözülür ve ∫ ½r²dφ ile integre edilir. Bu
// yol ızgara, kontur ve poligonlaştırma kodunun **hiçbirine** dokunmaz —
// karşılaştırma bu yüzden anlamlıdır.
//
// # σ neden tam sıfır değil
//
// density.Config.Validate σ > 0 şart koşar: sıfır σ, kalibrasyonun ölçekleyeceği
// terimi yok ederdi ve üretimde bir yapılandırma hatasıdır. Altın senaryo bunu
// 0,01 dB ile temsil eder — 0,05 dB'lik marjda Φ zaten 1'e taşar, basamak
// davranışı sayısal olarak elde edilir.

const (
	goldenSigmaDB   = 0.01 // "σ = 0"un sayısal karşılığı
	goldenResM      = 50   // ızgara çözünürlüğü (kenar etkisini küçültmek için)
	goldenAzimuth   = 0    // kuzeye bakan tek sektör
	goldenBeamWidth = 65
)

// goldenInventory, tek hücreli envanteri kurar (kentsel profil, ADR-17).
func goldenInventory(t testing.TB, p profileSpec) *params.Inventory {
	t.Helper()

	siteID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("golden:site"))
	cell := redis.CellParams{
		CellID:     uuid.NewSHA1(uuid.NameSpaceOID, []byte("golden:cell")),
		SiteID:     siteID,
		Azimuth:    goldenAzimuth,
		BeamWidth:  goldenBeamWidth,
		FreqMHz:    p.bands[0],
		EIRPdBm:    p.eirpDBm,
		AntHeightM: p.antHeightM,
		TiltDeg:    p.tiltDeg,
		RMaxM:      p.rMaxM,
		Morphology: p.name,
		ModelType:  p.model,
		Lat:        testOriginLat, // direk ENU başlangıcındadır
		Lon:        testOriginLon,
	}

	inv, err := params.Load(context.Background(), staticSource{cells: []redis.CellParams{cell}},
		uuid.New(), testOriginLat, testOriginLon)
	if err != nil {
		t.Fatalf("params.Load: %v", err)
	}
	return inv
}

// goldenConfig, σ → 0 yapılandırmasıdır.
func goldenConfig(p profileSpec) density.Config {
	return density.Config{
		RxSensitivityDBm: p.rxSensDBm,
		SigmaNominalDB:   goldenSigmaDB,
		Lambda:           1,
		UTHeightM:        utHeight,
	}
}

// coverageRadiusM, verilen yönde kapsama yarıçapını ikiye bölmeyle çözer.
//
// Link bütçesi doğrudan internal/rf'ten kurulur; density paketinin ağırlık
// kodu kullanılmaz.
func coverageRadiusM(cell params.Cell, bearingDeg, rxSensDBm, rMaxM float64) float64 {
	power := func(d float64) float64 {
		link := rf.Link{D2DM: d, HBSm: cell.AntHeightM, HUTm: utHeight, FreqMHz: cell.FreqMHz}
		pathLoss := rf.MixedPathLossDB(cell.Model, link, utHeight)
		offAxis := rf.WrapAngleDeg(bearingDeg - cell.AzimuthDeg)
		elevation := rf.ElevationDeg(cell.AntHeightM-utHeight, d)
		beamLoss := rf.AntennaAttenuationDB(offAxis, elevation, cell.BeamWidthDeg, cell.TiltDeg)
		return cell.EIRPdBm - pathLoss - beamLoss
	}

	lo, hi := 1.0, rMaxM
	if power(lo) < rxSensDBm {
		return 0
	}
	if power(hi) >= rxSensDBm {
		return hi // dilim, kapsamadan önce r_max ile kesiliyor
	}
	for i := 0; i < 60; i++ {
		mid := (lo + hi) / 2
		if power(mid) >= rxSensDBm {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// coveredAreaM2, kapsanan bölgenin alanını kutupsal integralle hesaplar:
//
//	A = ∫ ½ · r_cov(φ)² dφ
func coveredAreaM2(cell params.Cell, rxSensDBm, rMaxM float64) float64 {
	const stepDeg = 0.05

	half := cell.BeamWidthDeg / 2
	steps := int(math.Round(cell.BeamWidthDeg / stepDeg))
	stepRad := (stepDeg * math.Pi / 180)

	area := 0.0
	for i := 0; i < steps; i++ {
		// Dilim ortası: yamuk yerine orta nokta kuralı (yay üzerinde düzgün)
		bearing := cell.AzimuthDeg - half + (float64(i)+0.5)*stepDeg
		r := coverageRadiusM(cell, bearing, rxSensDBm, rMaxM)
		area += 0.5 * r * r * stepRad
	}
	return area
}

// TestGolden_ContourAreaMatchesClosedForm, altın senaryoda üretilen kontur
// alanlarını bağımsız hesapla karşılaştırır (tolerans ±%5).
func TestGolden_ContourAreaMatchesClosedForm(t *testing.T) {
	p := urbanProfile()
	inv := goldenInventory(t, p)
	grid := mustGrid(t, goldenResM)
	cfg := goldenConfig(p)

	opts := Options{Config: cfg, NeighbourMaxCount: 8, IncludeAngular: false}

	cell := inv.Cells()[0]
	res, err := Estimate(Record{CellID: cell.ID}, inv, grid, opts)
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if res.NeighbourCount != 0 {
		t.Fatalf("komşu sayısı %d, tek hücreli envanterde 0 beklenir", res.NeighbourCount)
	}
	if res.TAUsed {
		t.Fatal("TA'sız kayıtta ta_used=true")
	}

	wantTotal := coveredAreaM2(cell, cfg.RxSensitivityDBm, cell.RMaxM)
	t.Logf("bağımsız hesap: kapsanan alan %.4f km² (r_max=%.1f m, hüzme=%.0f°)",
		wantTotal/1e6, cell.RMaxM, cell.BeamWidthDeg)

	contours, err := Contours(res.Mass, grid, planLevels)
	if err != nil {
		t.Fatalf("Contours: %v", err)
	}

	for _, c := range contours {
		mp, err := grid.Polygonize(c.Cells)
		if err != nil {
			t.Fatalf("%.2f: Polygonize: %v", c.Level, err)
		}

		got := mp.AreaM2()
		want := c.Level * wantTotal
		rel := (got - want) / want

		t.Logf("M@%.0f%%: üretilen %.4f km², beklenen %.4f km² (fark %+.2f%%, %d parça, %d hücre)",
			c.Level*100, got/1e6, want/1e6, rel*100, mp.PartCount(), len(c.Cells))

		if math.Abs(rel) > 0.05 {
			t.Errorf("M@%.0f%%: alan farkı %+.2f%%, ±%%5 beklenir", c.Level*100, rel*100)
		}
	}
}

// TestGolden_ShrinkageAgainstBaselines, altın senaryoda M@90'ın B0 ve B1'e
// göre daralmasını ölçer (K2/K3'ün mekanik önizlemesi).
//
// Bu bir kabul testi **değildir**: K2/K3 dört senaryonun tam koşusunda,
// kalibre edilmiş λ ile ve 'V' kümesinde ölçülür (S5). Buradaki amaç
// zincirin — kütle, kontur, poligon, taban çizgisi — birlikte anlamlı bir
// sayı ürettiğini görmek ve daralmanın işaretinin doğru olduğunu doğrulamak.
func TestGolden_ShrinkageAgainstBaselines(t *testing.T) {
	p := urbanProfile()
	inv := goldenInventory(t, p)
	grid := mustGrid(t, goldenResM)
	cell := inv.Cells()[0]

	res, err := Estimate(Record{CellID: cell.ID}, inv, grid, DefaultOptions(goldenConfig(p), 8))
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	contours, err := Contours(res.Mass, grid, planLevels)
	if err != nil {
		t.Fatalf("Contours: %v", err)
	}

	sector, err := geometry.NewSector(cell.Site, cell.AzimuthDeg, cell.BeamWidthDeg, cell.RMaxM)
	if err != nil {
		t.Fatalf("NewSector: %v", err)
	}
	b0, err := baseline.B0(cell.Site, cell.RMaxM)
	if err != nil {
		t.Fatalf("B0: %v", err)
	}
	b1, err := baseline.B1(sector)
	if err != nil {
		t.Fatalf("B1: %v", err)
	}

	// PBT #3, altın senaryo örneğinde
	if b1.AreaM2() > b0.AreaM2() {
		t.Fatalf("area(B1)=%.1f > area(B0)=%.1f", b1.AreaM2(), b0.AreaM2())
	}

	for _, c := range contours {
		mp, err := grid.Polygonize(c.Cells)
		if err != nil {
			t.Fatalf("%.2f: Polygonize: %v", c.Level, err)
		}
		area := mp.AreaM2()
		t.Logf("M@%.0f%%: %.4f km² · B0 %.4f km² (daralma %%%.1f) · B1 %.4f km² (daralma %%%.1f)",
			c.Level*100, area/1e6, b0.AreaM2()/1e6, 100*(1-area/b0.AreaM2()),
			b1.AreaM2()/1e6, 100*(1-area/b1.AreaM2()))

		if area > b0.AreaM2() {
			t.Errorf("M@%.0f%% alanı B0'dan büyük", c.Level*100)
		}
	}
}

// TestGolden_MassIsUniformWithoutAngular, açısal çarpan kapalıyken kütlenin
// kapsanan bölgede düzgün dağıldığını doğrular.
//
// Kapalı form karşılaştırmasının (yukarıdaki test) dayandığı varsayım budur;
// sessizce bozulursa alan testi yanlış bir gerekçeyle geçebilirdi.
func TestGolden_MassIsUniformWithoutAngular(t *testing.T) {
	p := urbanProfile()
	inv := goldenInventory(t, p)
	grid := mustGrid(t, goldenResM)

	cell := inv.Cells()[0]
	res, err := Estimate(Record{CellID: cell.ID}, inv, grid,
		Options{Config: goldenConfig(p), NeighbourMaxCount: 8, IncludeAngular: false})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}

	var min, max float64 = math.Inf(1), 0
	var nonZero int
	for _, m := range res.Mass {
		if m <= 0 {
			continue
		}
		nonZero++
		min, max = math.Min(min, m), math.Max(max, m)
	}
	if nonZero == 0 {
		t.Fatal("hiç pozitif kütleli hücre yok")
	}

	// Sınırdaki birkaç hücre Φ ≈ 0,5 alabilir; ezici çoğunluk eşit olmalıdır.
	uniform := 0
	for _, m := range res.Mass {
		if m > 0 && math.Abs(m-max)/max < 1e-9 {
			uniform++
		}
	}
	ratio := float64(uniform) / float64(nonZero)
	t.Logf("düzgün kütleli hücre oranı %%%.2f (min %.3e, maks %.3e, pozitif %d)",
		ratio*100, min, max, nonZero)

	if ratio < 0.98 {
		t.Errorf("kütle düzgün değil: hücrelerin yalnızca %%%.2f'i eşit kütlede", ratio*100)
	}
}

// TestGolden_SingleCellSanity, altın envanterin gerçekten tek hücreli
// olduğunu ve dilimin beklenen yönde durduğunu doğrular.
func TestGolden_SingleCellSanity(t *testing.T) {
	inv := goldenInventory(t, urbanProfile())

	if inv.Len() != 1 {
		t.Fatalf("envanter %d hücre içeriyor, 1 beklenir", inv.Len())
	}
	cell := inv.Cells()[0]
	if d := cell.Site.Norm(); d > 1e-6 {
		t.Errorf("direk başlangıçtan %.6f m uzakta, 0 beklenir", d)
	}
	if cell.AzimuthDeg != goldenAzimuth || cell.BeamWidthDeg != goldenBeamWidth {
		t.Errorf("sektör (%g°, %g°), (%g°, %g°) beklenir",
			cell.AzimuthDeg, cell.BeamWidthDeg, float64(goldenAzimuth), float64(goldenBeamWidth))
	}
	if err := cell.Validate(); err != nil {
		t.Errorf("hücre geçersiz: %v", err)
	}
	_ = fmt.Sprint(cell.ID) // kimlik üretimi deterministik olmalı (UUIDv5)
}
