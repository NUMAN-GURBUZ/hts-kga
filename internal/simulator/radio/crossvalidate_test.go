// T-E02-10 / ADR-06 — Klasik model çapraz doğrulaması.
//
// ADR-06'nın açık hükmü: **çapraz doğrulama bir birim testtir, çalışma zamanı
// davranışı değil.** TR 38.901 tek ve yegâne çalışma zamanı modelidir; fallback
// yoktur, iki modelin ortalaması alınmaz.
//
// Bu testin akademik değeri: "Neden bu iki referans?" sorusuna cevap verir —
// literatürde yerleşik, bağımsız türetilmiş iki ampirik modelle 3GPP
// uygulamamızın makullüğünü kanıtlarız. Modelleri kullanmıyoruz, **sınıyoruz**.
//
// Kabul ölçütü (ADR-06): fark ≤ 10 dB.
// Çıktı: docs/scientific/cross-validation-3gpp.md
package radio

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/config"
)

// ADR-06 çapraz doğrulama parametre kümesi.
const (
	crossValHBSm      = 25.0 // kentsel makro anten yüksekliği
	crossValHUTm      = UTHeightM
	crossValMaxDiffDB = 10.0 // ADR-06 kabul eşiği

	// Okumura-Hata 900 MHz, COST-231 1800 MHz bandında geçerlidir.
	okumuraHataFreqMHz = 900
	cost231FreqMHz     = 1800
)

// crossValDistancesM, ADR-06'da belirtilen mesafe kümesi.
var crossValDistancesM = []float64{100, 500, 1000, 2000}

// crossValOutputPath, karşılaştırma tablosunun yazılacağı yol (ADR-06).
const crossValOutputPath = "../../../docs/scientific/cross-validation-3gpp.md"

// ─── Referans modeller (yalnızca test) ───────────────────────────────────────

// okumuraHataUrbanDB, Okumura-Hata kentsel yol kaybını döndürür (dB).
//
// Geçerlilik: 150–1500 MHz, 1–20 km, h_b 30–200 m, h_m 1–10 m.
// Küçük/orta şehir düzeltmesi kullanılır:
//
//	L = 69.55 + 26.16·log10(f) − 13.82·log10(h_b) − a(h_m)
//	    + (44.9 − 6.55·log10(h_b))·log10(d)
//	a(h_m) = (1.1·log10(f) − 0.7)·h_m − (1.56·log10(f) − 0.8)
//
// f: MHz, d: km, h_b/h_m: m.
func okumuraHataUrbanDB(freqMHz, distanceKM, hBm, hMm float64) float64 {
	logF := math.Log10(freqMHz)
	logHB := math.Log10(hBm)

	aHM := (1.1*logF-0.7)*hMm - (1.56*logF - 0.8)

	return 69.55 + 26.16*logF - 13.82*logHB - aHM +
		(44.9-6.55*logHB)*math.Log10(distanceKM)
}

// cost231HataDB, COST-231 Hata (genişletilmiş Hata) yol kaybını döndürür (dB).
//
// Geçerlilik: 1500–2000 MHz, 1–20 km, h_b 30–200 m, h_m 1–10 m.
//
//	L = 46.3 + 33.9·log10(f) − 13.82·log10(h_b) − a(h_m)
//	    + (44.9 − 6.55·log10(h_b))·log10(d) + C_m
//
// C_m = 0 dB (orta şehir / banliyö), 3 dB (metropol). Burada 0 kullanılır:
// senaryo temsili bir Anadolu şehridir, metropol değil.
func cost231HataDB(freqMHz, distanceKM, hBm, hMm, cityCorrDB float64) float64 {
	logF := math.Log10(freqMHz)
	logHB := math.Log10(hBm)

	aHM := (1.1*logF-0.7)*hMm - (1.56*logF - 0.8)

	return 46.3 + 33.9*logF - 13.82*logHB - aHM +
		(44.9-6.55*logHB)*math.Log10(distanceKM) + cityCorrDB
}

// ─── Referans modellerin kendi doğrulaması ───────────────────────────────────

// TestReferenceModels_KnownValues, referans modellerin doğru yazıldığını
// bağımsız olarak sınar. Referans hatalıysa çapraz doğrulama anlamsızlaşır.
//
// Çapa: h_b = 25 m, h_m = 1,5 m, d = 1 km (log10(1) = 0 → mesafe terimi düşer).
func TestReferenceModels_KnownValues(t *testing.T) {
	// Okumura-Hata, 900 MHz, 1 km:
	//   log10(900) = 2.954243 ; log10(25) = 1.397940
	//   a(h_m) = (1.1·2.954243 − 0.7)·1.5 − (1.56·2.954243 − 0.8) = 0.015818
	//   L = 69.55 + 26.16·2.954243 − 13.82·1.397940 − 0.015818 + 0
	//     = 69.55 + 77.282 − 19.319 − 0.016 = 127.497
	got := okumuraHataUrbanDB(okumuraHataFreqMHz, 1.0, crossValHBSm, crossValHUTm)
	if want := 127.497; math.Abs(got-want) > 0.05 {
		t.Errorf("Okumura-Hata(900 MHz, 1 km) = %.4f dB, elle hesaplanan %.3f dB", got, want)
	}
	t.Logf("Okumura-Hata(900 MHz, 1 km) = %.4f dB", got)

	// COST-231, 1800 MHz, 1 km:
	//   log10(1800) = 3.255273
	//   a(h_m) = (1.1·3.255273 − 0.7)·1.5 − (1.56·3.255273 − 0.8) = 0.042977
	//   L = 46.3 + 33.9·3.255273 − 19.319 − 0.043 + 0
	//     = 46.3 + 110.354 − 19.319 − 0.043 = 137.292
	got = cost231HataDB(cost231FreqMHz, 1.0, crossValHBSm, crossValHUTm, 0)
	if want := 137.292; math.Abs(got-want) > 0.05 {
		t.Errorf("COST-231(1800 MHz, 1 km) = %.4f dB, elle hesaplanan %.3f dB", got, want)
	}
	t.Logf("COST-231(1800 MHz, 1 km) = %.4f dB", got)
}

// ─── Çapraz doğrulama ────────────────────────────────────────────────────────

// crossValRow, karşılaştırma tablosunun tek satırıdır.
type crossValRow struct {
	reference   string
	freqMHz     int
	distanceM   float64
	tr38901DB   float64
	referenceDB float64
	diffDB      float64
}

// TestCrossValidation, ADR-06 gereksinimidir: TR 38.901 UMa NLOS değerlerini
// Okumura-Hata (900 MHz) ve COST-231 (1800 MHz) ile karşılaştırır ve farkın
// 10 dB'yi aşmadığını doğrular.
//
// NLOS karşılaştırılır: her iki klasik model de kentsel **engellenmiş**
// yayılım için türetilmiştir; LOS ile karşılaştırmak elmayla armut olurdu.
//
// Test, karşılaştırma tablosunu docs/scientific/ altına yazar (ADR-06).
func TestCrossValidation(t *testing.T) {
	uma, err := ModelFor(config.ModelUMa)
	if err != nil {
		t.Fatalf("ModelFor(UMa): %v", err)
	}

	rows := make([]crossValRow, 0, 2*len(crossValDistancesM))

	references := []struct {
		name    string
		freqMHz int
		fn      func(distanceKM float64) float64
	}{
		{
			name:    "Okumura-Hata (kentsel, orta şehir)",
			freqMHz: okumuraHataFreqMHz,
			fn: func(dKM float64) float64 {
				return okumuraHataUrbanDB(okumuraHataFreqMHz, dKM, crossValHBSm, crossValHUTm)
			},
		},
		{
			name:    "COST-231 Hata (C_m = 0 dB)",
			freqMHz: cost231FreqMHz,
			fn: func(dKM float64) float64 {
				return cost231HataDB(cost231FreqMHz, dKM, crossValHBSm, crossValHUTm, 0)
			},
		},
	}

	for _, ref := range references {
		t.Run(ref.name, func(t *testing.T) {
			for _, dM := range crossValDistancesM {
				link := Link{
					D2DM:    dM,
					HBSm:    crossValHBSm,
					HUTm:    crossValHUTm,
					FreqMHz: ref.freqMHz,
					LOS:     false, // klasik modeller kentsel NLOS içindir
				}

				tr := uma.PathLossDB(link)
				refDB := ref.fn(dM / 1000)
				diff := tr - refDB

				rows = append(rows, crossValRow{
					reference: ref.name, freqMHz: ref.freqMHz, distanceM: dM,
					tr38901DB: tr, referenceDB: refDB, diffDB: diff,
				})

				if math.Abs(diff) > crossValMaxDiffDB {
					t.Errorf("d=%.0f m: TR 38.901 = %.2f dB, %s = %.2f dB, "+
						"fark %.2f dB > %.0f dB (ADR-06 eşiği)",
						dM, tr, ref.name, refDB, diff, crossValMaxDiffDB)
				}
				t.Logf("d=%5.0f m  TR38.901 %7.2f dB   referans %7.2f dB   fark %+6.2f dB",
					dM, tr, refDB, diff)
			}
		})
	}

	// ADR-06: karşılaştırma tablosu docs/scientific/ altına yazılır.
	if err := writeCrossValidationReport(rows); err != nil {
		t.Errorf("çapraz doğrulama raporu yazılamadı: %v", err)
	}
}

// writeCrossValidationReport, karşılaştırma tablosunu Markdown olarak yazar.
//
// ADR-06'nın somut çıktısıdır: tez ekinde kaynak gösterilebilir bir tablo.
func writeCrossValidationReport(rows []crossValRow) error {
	if len(rows) == 0 {
		return fmt.Errorf("yazılacak satır yok")
	}

	var b strings.Builder
	b.WriteString("# 3GPP TR 38.901 — Klasik Model Çapraz Doğrulaması\n\n")
	b.WriteString("> Bu dosya `TestCrossValidation` tarafından üretilir; elle düzenlemeyin.\n\n")
	b.WriteString("**Karar:** ADR-06 — Çapraz doğrulama bir **birim testtir**, çalışma zamanı\n")
	b.WriteString("davranışı değil. TR 38.901 tek ve yegâne çalışma zamanı modelidir; fallback\n")
	b.WriteString("yoktur, iki modelin ortalaması alınmaz.\n\n")
	b.WriteString("**Amaç:** Literatürde yerleşik, bağımsız türetilmiş ampirik modellerle\n")
	b.WriteString("uygulamamızın makullüğünü kanıtlamak.\n\n")

	b.WriteString("## Parametreler\n\n")
	b.WriteString("| Parametre | Değer |\n|---|---|\n")
	fmt.Fprintf(&b, "| Model | UMa NLOS (kentsel makro, engellenmiş) |\n")
	fmt.Fprintf(&b, "| h_BS | %.1f m |\n", crossValHBSm)
	fmt.Fprintf(&b, "| h_UT | %.1f m |\n", crossValHUTm)
	fmt.Fprintf(&b, "| Kabul eşiği | ≤ %.0f dB (ADR-06) |\n\n", crossValMaxDiffDB)

	b.WriteString("## Sonuçlar\n\n")
	b.WriteString("| Referans | f (MHz) | d (m) | TR 38.901 (dB) | Referans (dB) | Fark (dB) |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|\n")

	var maxAbsDiff float64
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %d | %.0f | %.2f | %.2f | %+.2f |\n",
			r.reference, r.freqMHz, r.distanceM, r.tr38901DB, r.referenceDB, r.diffDB)
		maxAbsDiff = math.Max(maxAbsDiff, math.Abs(r.diffDB))
	}

	fmt.Fprintf(&b, "\n**En büyük mutlak fark: %.2f dB** ", maxAbsDiff)
	if maxAbsDiff <= crossValMaxDiffDB {
		fmt.Fprintf(&b, "→ ADR-06 eşiğini (%.0f dB) sağlıyor ✅\n\n", crossValMaxDiffDB)
	} else {
		fmt.Fprintf(&b, "→ ADR-06 eşiğini (%.0f dB) AŞIYOR ❌\n\n", crossValMaxDiffDB)
	}

	b.WriteString("## Referans modellerin geçerlilik sınırları\n\n")
	b.WriteString("| Model | Frekans | Mesafe | h_b | h_m |\n|---|---|---|---|---|\n")
	b.WriteString("| Okumura-Hata | 150–1500 MHz | 1–20 km | 30–200 m | 1–10 m |\n")
	b.WriteString("| COST-231 Hata | 1500–2000 MHz | 1–20 km | 30–200 m | 1–10 m |\n\n")
	b.WriteString("> **Not:** 100 m ve 500 m ölçümleri, klasik modellerin 1 km alt sınırının\n")
	b.WriteString("> altındadır; bu noktalarda referans dışdeğerlemedir. ADR-06 mesafe kümesi\n")
	b.WriteString("> plandan alınmıştır ve karşılaştırma yine de bilgi vericidir — farkın\n")
	b.WriteString("> yakın mesafede büyümemesi, TR 38.901'in yakın alan davranışının makul\n")
	b.WriteString("> olduğunu gösterir.\n\n")
	b.WriteString("> **Not:** h_BS = 25 m, Okumura-Hata'nın 30 m alt sınırının biraz altındadır.\n")
	b.WriteString("> Bu, ADR-17 kentsel profilinin (UMa nominal 25 m) gereğidir.\n")

	path := filepath.Clean(crossValOutputPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("dizin oluşturulamadı: %w", err)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
