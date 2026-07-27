# 3GPP TR 38.901 — Klasik Model Çapraz Doğrulaması

> Bu dosya `TestCrossValidation` tarafından üretilir; elle düzenlemeyin.

**Karar:** ADR-06 — Çapraz doğrulama bir **birim testtir**, çalışma zamanı
davranışı değil. TR 38.901 tek ve yegâne çalışma zamanı modelidir; fallback
yoktur, iki modelin ortalaması alınmaz.

**Amaç:** Literatürde yerleşik, bağımsız türetilmiş ampirik modellerle
uygulamamızın makullüğünü kanıtlamak.

## Parametreler

| Parametre | Değer |
|---|---|
| Model | UMa NLOS (kentsel makro, engellenmiş) |
| h_BS | 25.0 m |
| h_UT | 1.5 m |
| Kabul eşiği | ≤ 10 dB (ADR-06) |

## Sonuçlar

| Referans | f (MHz) | d (m) | TR 38.901 (dB) | Referans (dB) | Fark (dB) |
|---|---:|---:|---:|---:|---:|
| Okumura-Hata (kentsel, orta şehir) | 900 | 100 | 91.24 | 91.75 | -0.51 |
| Okumura-Hata (kentsel, orta şehir) | 900 | 500 | 118.12 | 116.74 | +1.38 |
| Okumura-Hata (kentsel, orta şehir) | 900 | 1000 | 129.87 | 127.50 | +2.37 |
| Okumura-Hata (kentsel, orta şehir) | 900 | 2000 | 141.63 | 138.26 | +3.37 |
| COST-231 Hata (C_m = 0 dB) | 1800 | 100 | 97.26 | 101.55 | -4.29 |
| COST-231 Hata (C_m = 0 dB) | 1800 | 500 | 124.14 | 126.53 | -2.39 |
| COST-231 Hata (C_m = 0 dB) | 1800 | 1000 | 135.89 | 137.29 | -1.40 |
| COST-231 Hata (C_m = 0 dB) | 1800 | 2000 | 147.65 | 148.05 | -0.40 |

**En büyük mutlak fark: 4.29 dB** → ADR-06 eşiğini (10 dB) sağlıyor ✅

## Referans modellerin geçerlilik sınırları

| Model | Frekans | Mesafe | h_b | h_m |
|---|---|---|---|---|
| Okumura-Hata | 150–1500 MHz | 1–20 km | 30–200 m | 1–10 m |
| COST-231 Hata | 1500–2000 MHz | 1–20 km | 30–200 m | 1–10 m |

> **Not:** 100 m ve 500 m ölçümleri, klasik modellerin 1 km alt sınırının
> altındadır; bu noktalarda referans dışdeğerlemedir. ADR-06 mesafe kümesi
> plandan alınmıştır ve karşılaştırma yine de bilgi vericidir — farkın
> yakın mesafede büyümemesi, TR 38.901'in yakın alan davranışının makul
> olduğunu gösterir.

> **Not:** h_BS = 25 m, Okumura-Hata'nın 30 m alt sınırının biraz altındadır.
> Bu, ADR-17 kentsel profilinin (UMa nominal 25 m) gereğidir.
