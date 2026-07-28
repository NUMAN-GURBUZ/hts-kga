package rf

import (
	"math"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
)

// anchorTolDB, elle hesaplanmış çapa değerleri için kabul edilen paydır.
//
// Çapaların amacı transkripsiyon hatası yakalamaktır (yanlış katsayı → dB
// mertebesinde sapma), beşinci ondalığı doğrulamak değil. Elle yapılan log10
// hesaplarının kendi belirsizliği ~1e-3 dB'dir.
const anchorTolDB = 0.1

// TestPathLoss_HandComputedAnchors, her model için bağımsız olarak **elle**
// hesaplanmış referans değerleri sınar. Katsayı veya birim hatası bu testte
// dB mertebesinde sapma üretir.
func TestPathLoss_HandComputedAnchors(t *testing.T) {
	tests := []struct {
		name       string
		model      config.PropagationModel
		link       Link
		want       float64
		derivation string
	}{
		{
			name:  "UMa LOS, 100 m, 2100 MHz",
			model: config.ModelUMa,
			link:  Link{D2DM: 100, HBSm: urbanHBSm, HUTm: UTHeightM, FreqMHz: 2100, LOS: true},
			want:  78.70,
			// d'_BP = 4·24·0.5·2.1e9/3e8 = 336 m → 100 ≤ 336, PL1 dalı
			// d_3D  = √(100² + 23.5²) = 102.724 ; log10 = 2.011673
			// PL = 28.0 + 22·2.011673 + 20·log10(2.1)
			//    = 28.0 + 44.2568 + 6.4444 = 78.7012
			derivation: "PL1 = 28.0 + 22·log10(102.724) + 20·log10(2.1)",
		},
		{
			name:  "UMa NLOS, 1000 m, 900 MHz",
			model: config.ModelUMa,
			link:  Link{D2DM: 1000, HBSm: urbanHBSm, HUTm: UTHeightM, FreqMHz: 900, LOS: false},
			want:  129.87,
			// d_3D = √(1000² + 23.5²) = 1000.2761 ; log10 = 3.0001199
			// PL' = 13.54 + 39.08·3.0001199 + 20·log10(0.9) − 0.6·(1.5−1.5)
			//     = 13.54 + 117.2447 − 0.9152 = 129.8695
			// LOS karşılığı 108.14 → max() NLOS'u seçer
			derivation: "PL' = 13.54 + 39.08·log10(1000.276) + 20·log10(0.9)",
		},
		{
			name:  "UMi LOS, 100 m, 2100 MHz",
			model: config.ModelUMi,
			link:  Link{D2DM: 100, HBSm: 10, HUTm: UTHeightM, FreqMHz: 2100, LOS: true},
			want:  80.88,
			// d'_BP = 4·9·0.5·7 = 126 m → 100 ≤ 126, PL1 dalı
			// d_3D  = √(100² + 8.5²) = 100.3606 ; log10 = 2.0015637
			// PL = 32.4 + 21·2.0015637 + 6.4444 = 80.8772
			derivation: "PL1 = 32.4 + 21·log10(100.361) + 20·log10(2.1)",
		},
		{
			name:  "RMa LOS, 1000 m, 900 MHz",
			model: config.ModelRMa,
			link:  Link{D2DM: 1000, HBSm: ruralHBSm, HUTm: UTHeightM, FreqMHz: 900, LOS: true},
			want:  93.67,
			// d_BP = 2π·45·1.5·3 = 1272.35 m → 1000 ≤ 1272, PL1 dalı
			// d_3D = √(1000² + 43.5²) = 1000.946 ; log10 = 3.0004107
			// h^1.72 = 5^1.72 = 15.930
			//   serbest uzay = 20·log10(41.8879·1000.946·0.9) = 20·4.576741 = 91.5348
			//   eğim         = min(0.03·15.930, 10)·3.0004107 = 0.4779·3.0004 = 1.4339
			//   kesme        = min(0.044·15.930, 14.77) = 0.70092
			//   doğrusal     = 0.002·log10(5)·1000.946 = 1.3993
			// PL1 = 91.5348 + 1.4339 − 0.7009 + 1.3993 = 93.6671
			derivation: "PL1 = 20·log10(40π·d₃D·f/3) + 0.4779·log10(d₃D) − 0.7009 + 1.3993",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ModelFor(tc.model)
			if err != nil {
				t.Fatalf("ModelFor: %v", err)
			}
			got := m.PathLossDB(tc.link)
			if math.Abs(got-tc.want) > anchorTolDB {
				t.Errorf("PathLossDB() = %.4f dB, elle hesaplanan %.2f dB (fark %.4f)\n  türetme: %s",
					got, tc.want, got-tc.want, tc.derivation)
			}
			t.Logf("%.4f dB  (elle: %.2f dB)", got, tc.want)
		})
	}
}

// TestBreakpointContinuity, iki parçalı LOS eğrisinin kırılma noktasındaki
// davranışını sınar. Kabul ölçütü onaylanan test planından gelir: sıçrama
// 0,1 dB'nin altında kalmalıdır.
//
// UMa ve UMi'de süreklilik **tam**'dır ve bu, düzeltme teriminin doğru
// yazıldığının en güçlü göstergesidir:
//   - UMa: 40 − 2·9   = 22 = yakın alan üsteli ✓
//   - UMi: 40 − 2·9.5 = 21 = yakın alan üsteli ✓
//
// Katsayı yanlış yazılsaydı eğri burada dB mertebesinde sıçrardı.
//
// RMa'da standardın kendi tutarsızlığından gelen ~0,005 dB'lik bir süreksizlik
// vardır (bkz. rma.go losPathLossDB açıklaması). Bilinçli olarak korunmuştur.
//
// Sıçrama, kırılma noktasının çok yakınında (ε = 1 nm) ölçülür: böylece
// eğrinin kendi eğiminden gelen katkı ihmal edilebilir hale gelir ve ölçülen
// değer saf süreksizliktir.
func TestBreakpointContinuity(t *testing.T) {
	// approvedMaxJumpDB, onaylanan test planının kabul ölçütü.
	const approvedMaxJumpDB = 0.1

	tests := []struct {
		model config.PropagationModel
		hBSm  float64
		freqs []int
		// maxJumpDB, bu model için beklenen üst sınır. UMa/UMi tam sürekli
		// olduğundan kayan nokta gürültüsü mertebesinde; RMa standart
		// artefaktı taşır.
		maxJumpDB float64
	}{
		{config.ModelUMa, urbanHBSm, []int{800, 900, 1800, 2100, 2600}, 1e-9},
		{config.ModelUMi, 10, []int{800, 900, 1800, 2100, 2600}, 1e-9},
		{config.ModelRMa, ruralHBSm, []int{800, 900, 1800}, 0.01},
	}

	for _, tc := range tests {
		t.Run(string(tc.model), func(t *testing.T) {
			m, _ := ModelFor(tc.model)

			for _, f := range tc.freqs {
				dBP := breakpointFor(tc.model, tc.hBSm, UTHeightM, float64(f)*hzPerMHz)

				// ε = 1 nm: eğim katkısı ~1e-10 dB, süreksizlik yalıtılır
				const eps = 1e-9
				below := Link{D2DM: dBP - eps, HBSm: tc.hBSm, HUTm: UTHeightM, FreqMHz: f, LOS: true}
				above := Link{D2DM: dBP + eps, HBSm: tc.hBSm, HUTm: UTHeightM, FreqMHz: f, LOS: true}

				jump := math.Abs(m.PathLossDB(above) - m.PathLossDB(below))

				if jump > tc.maxJumpDB {
					t.Errorf("%d MHz: kırılma noktasında (%.2f m) sıçrama %.9f dB > %.9f dB",
						f, dBP, jump, tc.maxJumpDB)
				}
				if jump > approvedMaxJumpDB {
					t.Errorf("%d MHz: sıçrama %.6f dB, onaylanan ölçütü (%.1f dB) aşıyor",
						f, jump, approvedMaxJumpDB)
				}
				t.Logf("%d MHz: d_BP = %8.2f m, sıçrama = %.9f dB", f, dBP, jump)
			}
		})
	}
}

// breakpointFor, test amaçlı kırılma mesafesi yardımcısıdır.
func breakpointFor(model config.PropagationModel, hBSm, hUTm, freqHz float64) float64 {
	if model == config.ModelRMa {
		return rmaBreakpointDistanceM(hBSm, hUTm, freqHz)
	}
	return breakpointDistanceUrbanM(hBSm, hUTm, freqHz)
}

// TestBreakpointDistance_ReferenceValues, kırılma mesafelerini elle
// hesaplanmış değerlerle sınar.
func TestBreakpointDistance_ReferenceValues(t *testing.T) {
	tests := []struct {
		name    string
		got     float64
		want    float64
		formula string
	}{
		{
			name:    "UMa 2100 MHz",
			got:     breakpointDistanceUrbanM(urbanHBSm, UTHeightM, 2100*hzPerMHz),
			want:    336,
			formula: "4·(25−1)·(1.5−1)·2.1e9/3e8",
		},
		{
			name:    "UMa 900 MHz",
			got:     breakpointDistanceUrbanM(urbanHBSm, UTHeightM, 900*hzPerMHz),
			want:    144,
			formula: "4·24·0.5·0.9e9/3e8",
		},
		{
			name:    "UMi 2100 MHz",
			got:     breakpointDistanceUrbanM(10, UTHeightM, 2100*hzPerMHz),
			want:    126,
			formula: "4·(10−1)·(1.5−1)·2.1e9/3e8",
		},
		{
			name:    "RMa 900 MHz",
			got:     rmaBreakpointDistanceM(ruralHBSm, UTHeightM, 900*hzPerMHz),
			want:    2 * math.Pi * 45 * 1.5 * 3,
			formula: "2π·45·1.5·0.9e9/3e8",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if math.Abs(tc.got-tc.want) > 1e-6 {
				t.Errorf("d_BP = %.6f m, beklenen %.6f m  (%s)", tc.got, tc.want, tc.formula)
			}
		})
	}
}

// TestNLOSNotBelowLOS, TR 38.901 max() sözleşmesini sınar:
// engellenmiş bir yol, serbest görüş hattından daha az kayıplı olamaz.
func TestNLOSNotBelowLOS(t *testing.T) {
	for _, name := range []config.PropagationModel{config.ModelUMa, config.ModelUMi, config.ModelRMa} {
		t.Run(string(name), func(t *testing.T) {
			m, _ := ModelFor(name)

			hBS := urbanHBSm
			if name == config.ModelRMa {
				hBS = ruralHBSm
			}
			if name == config.ModelUMi {
				hBS = 10
			}

			for _, d := range []float64{10, 50, 100, 336, 500, 1000, 2000, 5000, 10000} {
				for _, f := range []int{800, 900, 1800, 2100, 2600} {
					los := Link{D2DM: d, HBSm: hBS, HUTm: UTHeightM, FreqMHz: f, LOS: true}
					nlos := los
					nlos.LOS = false

					if plN, plL := m.PathLossDB(nlos), m.PathLossDB(los); plN < plL {
						t.Errorf("d=%.0f m, f=%d MHz: NLOS (%.3f) < LOS (%.3f)", d, f, plN, plL)
					}
				}
			}
		})
	}
}

// TestLOSProbability_ReferenceBehaviour, TR 38.901 Tablo 7.4.2-1 davranışını sınar.
func TestLOSProbability_ReferenceBehaviour(t *testing.T) {
	uma, _ := ModelFor(config.ModelUMa)
	umi, _ := ModelFor(config.ModelUMi)
	rma, _ := ModelFor(config.ModelRMa)

	t.Run("yakın mesafede kesin LOS", func(t *testing.T) {
		if p := uma.LOSProbability(18, UTHeightM); p != 1 {
			t.Errorf("UMa P_LOS(18 m) = %g, beklenen 1", p)
		}
		if p := umi.LOSProbability(18, UTHeightM); p != 1 {
			t.Errorf("UMi P_LOS(18 m) = %g, beklenen 1", p)
		}
		if p := rma.LOSProbability(10, UTHeightM); p != 1 {
			t.Errorf("RMa P_LOS(10 m) = %g, beklenen 1", p)
		}
	})

	// RMa: P_LOS(d) = e^(−(d−10)/1000) → kapalı formla doğrudan karşılaştırılabilir
	t.Run("RMa kapalı form", func(t *testing.T) {
		for _, d := range []float64{100, 500, 1000, 5000} {
			want := math.Exp(-(d - 10) / 1000)
			if got := rma.LOSProbability(d, UTHeightM); math.Abs(got-want) > 1e-12 {
				t.Errorf("RMa P_LOS(%.0f) = %g, beklenen %g", d, got, want)
			}
		}
	})

	// Kırsalda engel seyrek → aynı mesafede LOS olasılığı kentselden yüksek
	t.Run("kırsal > kentsel", func(t *testing.T) {
		for _, d := range []float64{100, 500, 1000} {
			pRMa := rma.LOSProbability(d, UTHeightM)
			pUMa := uma.LOSProbability(d, UTHeightM)
			if pRMa <= pUMa {
				t.Errorf("d=%.0f m: RMa P_LOS (%.4f) ≤ UMa (%.4f)", d, pRMa, pUMa)
			}
		}
	})

	// Sokak kanyonu makro hücreden daha hızlı kapanır (36 m'ye karşı 63 m)
	t.Run("UMi < UMa", func(t *testing.T) {
		for _, d := range []float64{50, 100, 200} {
			if pUMi, pUMa := umi.LOSProbability(d, UTHeightM), uma.LOSProbability(d, UTHeightM); pUMi >= pUMa {
				t.Errorf("d=%.0f m: UMi P_LOS (%.4f) ≥ UMa (%.4f)", d, pUMi, pUMa)
			}
		}
	})
}

// TestUMaHUTCorrection, yüksek UT düzeltmesi C'(h_UT)'yi sınar.
// Projede h_UT = 1,5 m olduğundan düzeltme daima 0'dır; dal ileride kullanılabilir.
func TestUMaHUTCorrection(t *testing.T) {
	tests := []struct {
		hUTm float64
		want float64
	}{
		{1.5, 0},
		{13, 0},
		{23, math.Pow(1.0, 1.5)}, // ((23−13)/10)^1.5 = 1
		{18, math.Pow(0.5, 1.5)},
	}
	for _, tc := range tests {
		if got := umaHUTCorrection(tc.hUTm); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("C'(%g) = %g, beklenen %g", tc.hUTm, got, tc.want)
		}
	}
}

// TestPathLoss_ScenarioParameters, Sprint 1 envanterinden gelen gerçek
// parametrelerle yol kaybının makul aralıkta kaldığını sınar.
//
// Beklenen: kapsama kenarında (r_max) yol kaybı, EIRP ile alıcı duyarlılığı
// arasındaki bütçeyi aşırı zorlamamalı. EIRP 58/62 dBm, duyarlılık −110 dBm
// → kabul edilebilir üst sınır ≈ 168/172 dB.
func TestPathLoss_ScenarioParameters(t *testing.T) {
	tests := []struct {
		name      string
		model     config.PropagationModel
		hBSm      float64
		freqMHz   int
		rMaxM     float64
		eirpDBm   float64
		maxBudget float64
	}{
		{"kentsel", config.ModelUMa, urbanHBSm, 2100, 5000, 58, 58 + 110},
		{"kırsal", config.ModelRMa, ruralHBSm, 900, 20000, 62, 62 + 110},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := ModelFor(tc.model)
			l := Link{D2DM: tc.rMaxM, HBSm: tc.hBSm, HUTm: UTHeightM, FreqMHz: tc.freqMHz, LOS: false}

			pl := m.PathLossDB(l)
			rx := tc.eirpDBm - pl

			t.Logf("%s: d=%.0f m, PL=%.2f dB, RX=%.2f dBm (bütçe %.0f dB)",
				tc.name, tc.rMaxM, pl, rx, tc.maxBudget)

			if pl <= 0 {
				t.Errorf("yol kaybı pozitif olmalı: %.2f dB", pl)
			}
			if pl > tc.maxBudget {
				t.Errorf("r_max'ta yol kaybı (%.2f dB) link bütçesini (%.0f dB) aşıyor — "+
					"kapsama yarıçapı gerçekçi değil", pl, tc.maxBudget)
			}
		})
	}
}
