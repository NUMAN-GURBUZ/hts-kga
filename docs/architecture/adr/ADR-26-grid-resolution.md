# ADR-26 — Izgara Çözünürlüğü ve K8 Parçalanması

**Durum:** Kabul edildi
**Tarih:** 2026-07-29
**Sprint:** 5 (Sprint 4'ten devir)
**İlgili bulgular:** K8, ADR-18/6, ADR-21 açık kalemi

---

## Bağlam

Sprint 4, K8'in iki yarısından birini karşıladı (`repaired_ratio = %0,0000`)
ama diğerini karşılayamadı: `p95_part_count ≤ 3` TA'lı senaryolarda tutmadı
(kentsel M@90 p95=4, kırsal M@90 p95=7).

Tanı ölçümü nedenin **alt-çözünürlük halkası** olduğunu gösterdi: LTE'de TA
halkasının genişliği 78,12 m'dir, ızgara adımı kentselde 100 m / kırsalda
250 m. Halka hücreden ince olduğu için yüksek kütleli bant kesik bir zincire
dönüşür.

| Adım | halka/adım | M@50 p95 | M@90 p95 | hücre/olay |
|---|---|---|---|---|
| 100 m | 0,78 | 6 | 4 | 2.457 |
| 78 m | 1,00 | 6 | 3 | 4.039 |
| 50 m | 1,56 | 3 | 3 | 9.830 |
| 39 m | 2,00 | 2 | 3 | 16.158 |
| 25 m | 3,12 | 2 | 3 | 39.320 |

TA'sız senaryoda parçalanma çözünürlükten **bağımsızdır** (M@90 p95 = 3–4,
her adımda): orada parçalanma sayısal değil fizikseldir — kütle gerçekten
çok tepelidir (komşu kısıtının böldüğü loblar).

---

## Karar

### 1. Morfoloji içinde tek çözünürlük

| Senaryo | Eski | Yeni |
|---|---|---|
| A kentsel TA'lı | 100 m | **50 m** |
| B kentsel TA'sız | 100 m | **50 m** |
| C kırsal TA'lı | 250 m | **150 m** |
| D kırsal TA'sız | 250 m | **150 m** |

A ile B'yi farklı adımda koşmak, senaryo karşılaştırmasına çözünürlük
confound'u sokardı: "TA daraltıyor" ifadesi, TA'nın mı yoksa daha ince
ızgaranın mı etkisi olduğu ayırt edilemez hâle gelirdi. Aynı gerekçe C ve D
için geçerlidir.

### 2. Kentselde 50 m, kırsalda 150 m — neden bu değerler

Kentsel: 50 m'de halka/adım = 1,56 ve M@90 p95 = 3 (K8 eşiği). 39 m'ye inmek
M@50'yi 3'ten 2'ye düşürüyor ama M@90'ı değiştirmiyor; maliyet 1,6 kat
artıyor. Eşiği karşılayan en ucuz nokta 50 m'dir.

Kırsal: 29 km yarıçaplı dilimde adım karesiyle büyüyen bir maliyet var.
78 m (halka/adım = 1,00) 91.147 hücre/olay ve 60K olayda ~1 saat demek.
150 m, hücre sayısını 24.645'te tutarken M@90 p95'i 3'e indiriyor (12 olaylık
ön ölçüm; tam koşuda doğrulanacak).

### 3. Kalan parçalanma bulgu olarak raporlanır

Çözünürlük düzeltmesi sayısal eseri kaldırır; TA'sız senaryolardaki 3–4'lük
parçalanma fizikseldir ve **eşik ölçümden sonra değiştirilmez** (plan BÖLÜM J).
Tutmazsa negatif bulgu olarak raporlanır.

---

## Sonuçlar

- Analiz maliyeti kentselde 4 kat, kırsalda ~2,8 kat artar; ADR-14
  örneklemesiyle birlikte toplam bütçe kabul edilebilir kalır.
- `configs/*.yaml` dosyalarındaki `analysis.grid_resolution_m` güncellendi.
- Sprint 4'ün ADR-21'de açık bıraktığı kalem kapandı.

## Reddedilen alternatifler

**Nyquist kuralını (adım ≤ halka/2 = 39 m) her yerde uygulamak.** Kırsalda
364.000 hücre/olay ve ~9,5 saat demek; ölçümün değeriyle orantısız.

**Yalnızca TA'lı senaryoların çözünürlüğünü düşürmek.** Senaryo
karşılaştırmasına confound sokar.

**Komşu kısıtını (ADR-03) zayıflatarak parçalanmayı azaltmak.** Kısıt
çalışmanın bilimsel iddiasının parçasıdır; parçalanmayı azaltmak için
gevşetmek, ölçülen şeyi ölçüme uydurmak olurdu.
