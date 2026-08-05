# Sprint 5 — Doğrulama & Kalibrasyon Kapanış Raporu

**Tarih:** 2026-07-29
**Kapsam:** E04 (T-E04-01..08) + plan boşlukları G1, G2, G3
**Kabul kriterleri:** K1, K2, K3, K4, K5

---

## 1. Ne yapıldı

Sprint 5, projenin **bilimsel iddiasının ilk kez uçtan uca ölçüldüğü**
sprinttir. Sprint 4'ün sonunda sistem geometri üretebiliyordu ama ürettiği
geometrinin gerçekle ne kadar örtüştüğü bilinmiyordu; ölçüm zincirinin üç
halkası eksikti (veri üreten binary, iki kalıcılaştırıcı) ve doğrulama
katmanı hiç yoktu.

Zincir şimdi tam:

```
simülatör → Kafka (2 topic) → persister'lar → PostgreSQL
                            → analiz motoru (örneklenmiş) → estimates
                                                          → kalibrasyon (λ*)
                                                          → F.1–F.4 → metrics
```

## 2. Tamamlanan tasklar

| Task | İş | Durum |
|---|---|---|
| **G1** | `cmd/simulator`'ın gerçek akışa bağlanması | ✅ |
| **G2** | `hts_records` kalıcılaştırıcısı (ADR-22) | ✅ |
| **T-E04-01** | S3a ground truth kalıcılaştırıcısı | ✅ |
| **G3** | Analiz örnekleme filtresi (ADR-24) | ✅ |
| **T-E04-07** | Örnekleme katmanı (tam N seçimi) | ✅ |
| **T-E04-02** | S3b batch iskeleti + tamamlanma önkoşulu | ✅ |
| **T-E04-05** | 80/20 enforcer (K4) | ✅ |
| **T-E04-03** | F.1–F.3 metrik sorguları | ✅ |
| **G0** | λ pilot ölçümü + tanı | ✅ |
| **T-E04-06** | Bisection kalibrasyon döngüsü | ✅ |
| **T-E04-04** | F.4 daralma hesabı (K2/K3) | ✅ |
| **T-E04-08** | 4 senaryo koşumu + karşılaştırmalı rapor | ✅ |
| **T-E04-09** | Bütünlük precision/recall (F.5) | ⏭ Sprint 6 |

T-E04-09, `integrity_findings` tablosunu dolduran servis Sprint 6'da
yazılacağı için ölçüm üretemezdi; plan K7'yi zaten S6'ya atamıştı.

## 3. Oluşturulan ve değiştirilen dosyalar

### Yeni paketler

| Yol | İş |
|---|---|
| `internal/simulator/run/` | Koşu döngüsü + envanter→radyo eşlemesi |
| `internal/persist/` | Kafka→DB tüketicisi (tip parametreli, iki rol) |
| `internal/analysis/sampling/` | Örnekleme politikası ve tam N seçimi |
| `internal/validation/metrics/` | Bölüm anahtarı tipi + F.1–F.3 + yazıcı |
| `internal/validation/comparison/` | F.4 daralma |
| `internal/validation/calibration/` | Bisection + kapsama + tanı |
| `internal/validation/pipeline/` | Önkoşul + aşama yürütücü |

### Yeni dosyalar (üretim)

```
cmd/persister/main.go
internal/simulator/run/{run.go,network.go}
internal/persist/{consumer.go,rows.go}
internal/analysis/sampling/{policy.go,exact.go}
internal/validation/metrics/{partition.go,queries.go,writer.go}
internal/validation/comparison/reduction.go
internal/validation/calibration/{bisection.go,coverage.go,diagnostics.go}
internal/validation/pipeline/{pipeline.go,precondition.go}
internal/storage/postgres/{runs.go,records.go,groundtruth.go}
internal/storage/migrations/005_run_counters.sql
scripts/{run-scenario.sh,compare-scenarios.sh}
```

### Değiştirilen dosyalar

```
cmd/simulator/main.go          Sprint 0 iskeleti → tam bağlantı
cmd/analysis-engine/main.go    örnekleme modu, run_id, analyzed_events, idle
cmd/validation/main.go         Sprint 0 iskeleti → batch iş
internal/analysis/driver/consumer.go   örnekleme + run_id süzgeci + idle
internal/analysis/density/grid.go      At() (kapsama kısayolu), Polygonize()
internal/analysis/params/inventory.go  Projector() erişimcisi
internal/simulator/event/record.go     EventCall → EventMOC/EventMTC
internal/storage/postgres/postgres.go  Querier() (metrik katmanı için)
configs/*.yaml                 ADR-26 çözünürlükleri + örneklem sınırları
```

### Yeni testler

```
internal/simulator/run/run_test.go                 determinizm, hacim, enjeksiyon
internal/persist/persist_test.go                   dönüşüm, doğrulama
internal/analysis/sampling/sampling_test.go        C∩V=∅, tam N, PBT
internal/validation/metrics/partition_test.go      K4 enforcer
internal/validation/calibration/bisection_test.go  kök bulma, düz eğri, PBT
tests/integration/persist_test.go                  uçtan uca + kör test regresyonu
tests/integration/validation_test.go               önkoşul, λ pilotu, tam hat
```

## 4. Eklenen ADR'ler

| ADR | Karar |
|---|---|
| **ADR-22** | `hts_records` ayrı bir kalıcılaştırıcı ile yazılır (simülatör değil): Kafka tek doğruluk kaynağı kalır, sapma ölçülebilir olur |
| **ADR-23** | Koşu sayaçları (`published_events`, `analyzed_events`) — ADR-04 ↔ ADR-14 çelişkisini kapatır; `verify_integrity` dört denetime çıkar |
| **ADR-24** | Örnekleme filtresi kütle hesabından **önce**; C/V ayrımı `pkg/split` ile türetilir, kör test delinmez |
| **ADR-25** | Kalibrasyon, ground truth'tan motora giden tek kanaldır ve genişliği **bir skalerdir**; λ'nın kaldıraç sınırı ölçüldü |
| **ADR-26** | Izgara çözünürlüğü: kentsel 50 m, kırsal 150 m (morfoloji içinde tek değer) |

## 5. Düzeltilen hatalar

Beşi de **gerçek koşuda** ortaya çıktı; hiçbiri birim testlerle görünmüyordu.

| # | Hata | Nasıl bulundu | Etkisi |
|---|---|---|---|
| 1 | Simülatör `CALL` üretiyor, şema `MOC/MTC` bekliyor | G2 entegrasyon testi | Sprint 2'den beri gizliydi; Kafka'ya yayınlamak şemayı doğrulamıyor |
| 2 | Örnekleme tohumu karma girdisinin **sonunda** | `TestExactSample_Deterministic` | Farklı tohum aynı örneklemi veriyordu (K10 ihlali) |
| 3 | Tüketicide `run_id` süzgeci yok | Doğrulama entegrasyon testi | Başka koşuların kayıtları işleniyor, sayaçlar bozuluyordu |
| 4 | Analiz sayacı başarısız olayları da sayıyor | Önkoşul denetimi | Enjeksiyon kural 1 kayıtları analiz edilemez; bütünlük denetimi tutmuyordu |
| 5 | İki persister aynı health portunu tutuyor | Senaryo A koşumu | İkinci rol sessizce düşüyordu |

Ayrıca iki dayanıklılık açığı kapatıldı: **zehirli satır** (tek bozuk kayıt
tüm partiyi ve dolayısıyla koşuyu bloke ediyordu) ve **döngü içinde biriken
`defer cancel()`**.

## 6. Test sonuçları

Tüm birim, PBT ve entegrasyon testleri yeşil (`go vet` ve `gofmt` temiz).

| Katman | Kapsam |
|---|---|
| Birim | 23 paket; bisection kök bulma, örnekleme oranları, K4 tip güvenliği, WKB, dönüşümler |
| PBT (`rapid`) | Örnekleme değişmezleri (C∩V=∅, tam mod, alt küme, determinizm) · bisection (monoton eğride yakınsama, aralık daralması) · Sprint 4'ün 6 analiz değişmezi |
| Entegrasyon | Persister round-trip + idempotans · kör test regresyonu · önkoşul reddi · λ pilotu · tam doğrulama hattı · F.3 çapraz kontrolü |
| Çapraz kontrol | PostGIS `ST_Distance` ↔ `pkg/geo.Haversine`: en büyük fark **%0,24** |
| Kör test | `svc_analysis` → `ground_truth` → **permission denied** (regresyona bağlandı) |

## 7. Senaryo sonuçları

Dört senaryo da tam ölçekte koşuldu: 1000 ajan × 30 gün ≈ 300.000 olay.
Analiz ADR-14 örneklemesiyle yapıldı (kentsel 5.000 / kırsal 3.000 'V' olayı;
kalibrasyon kentsel 5.000 / kırsal 2.000). Tüm ölçümler `partition_key='V'`
kümesindedir (K4).

### 7.1 Koşu hacimleri

| Senaryo | Yayınlanan olay | `hts_records` | `ground_truth` | Analiz edilen | `estimates` |
|---|---|---|---|---|---|
| A kentsel TA'lı | 298.117 | 298.117 | 299.296 | 4.992 | 24.960 |
| B kentsel TA'sız | 298.117 | 298.117 | 299.296 | 5.045 | 25.225 |
| C kırsal TA'lı | 298.117 | 298.117 | 299.296 | 2.937 | 14.685 |
| D kırsal TA'sız | 298.117 | 298.117 | 299.296 | 3.045 | 15.225 |

Dört senaryoda yayınlanan olay sayısı **birebir aynıdır**: Poisson üreteci
(tohum, ajan, tick) üçlüsünden beslenir, morfolojiden bağımsızdır. Senaryoları
ayıran şey olay sayısı değil, o olayların nerede geçtiği ve TA'nın olup
olmadığıdır — karşılaştırma bu sayede tek değişkenli kalır.

`ground_truth` ile `hts_records` farkı (1.179 satır) enjeksiyon kural 4'ün
sildiği kayıtlardır (ADR-09): olay gerçekleşti, kayıt "kayboldu".

`verify_integrity` dört koşuda da dört denetimin tamamında **OK**.

### 7.2 K1 — Kapsama (M@90, 'V' kümesi)

| Senaryo | M@90 kapsama | B1 (dilim) kapsaması | M@90 / B1 | λ* | Eşik %85–95 |
|---|---|---|---|---|---|
| A | 0,5435 | 0,6038 | **0,900** | 2,9997 | ✗ |
| B | 0,5841 | 0,5980 | 0,977 | 2,9997 | ✗ |
| C | 0,5247 | 0,5778 | **0,908** | 2,9997 | ✗ |
| D | 0,5741 | 0,5773 | 0,994 | 2,9997 | ✗ |

**K1 dört senaryoda da tutmadı** ve nedeni ölçüldü.

Kritik sayı üçüncü sütundur. TA'lı senaryolarda M@90'ın kapsaması, sektör
diliminin kapsamasının **%90,0 ve %90,8'idir** — yani model, *arama bölgesi
içinde* tam olarak beyan ettiği gibi %90 kalibredir. Açığın tamamı bölgenin
kendisinden gelir: gerçek konumun %40–42'si serving hücrenin ±32,5°'lik
diliminin dışındadır ve model oraya hiç kütle koymaz.

Kök neden Sprint 3'ün arama bölgesi tanımıdır (T-E03-03): simülatörün
best-server'ı tüm yönlerde çalışır (gölgeleme ve anten deseni birlikte),
analiz ise bölgeyi 3 dB hüzme genişliğiyle sınırlar. Bir hücre, hüzmesinin
dışındaki bir noktada da pekâlâ en güçlü sunucu olabilir.

λ bu açığı kapatamaz: kalibrasyon λ = 3,0 üst sınırına dayandı ve kapsama
λ ∈ [0,5, 3,0] boyunca yalnızca %1–3 değişti (ADR-25'te ölçüldü). λ yalnızca
σ_eff'i ölçekler; arama bölgesinin sert sınırını genişletmez.

### 7.3 K2 / K3 — Alan daralması ('V' kümesi, M@90)

| Senaryo | M@90 alan | B0 alan | B1 alan | K2 (vs B0) | K3 (vs B1) | Eşik |
|---|---|---|---|---|---|---|
| A kentsel TA'lı | 0,0392 km² | 118,46 km² | 21,39 km² | **%99,97** | **%99,82** | K2 ≥75 · K3 ≥50 ✓ |
| B kentsel TA'sız | 10,47 km² | 118,46 km² | 21,39 km² | **%91,16** | **%51,05** | K2 ≥75 · K3 ≥20 ✓ |
| C kırsal TA'lı | 0,2737 km² | 3018,70 km² | 545,03 km² | **%99,99** | **%99,95** | K2 ≥75 · K3 ≥50 ✓ |
| D kırsal TA'sız | 261,36 km² | 3017,49 km² | 544,59 km² | **%91,34** | **%52,01** | K2 ≥75 · K3 ≥20 ✓ |

**K2 ve K3 dört senaryoda da tuttu**, TA'sız senaryolarda eşiğin iki buçuk
katıyla (%51–52 vs %20 eşiği).

TA'nın katkısı çarpıcıdır: kentselde alan 10,47 km²'den 0,0392 km²'ye,
kırsalda 261 km²'den 0,27 km²'ye iner — **267× ve 955× daralma**. Timing
Advance'ın 78 metrelik halkası, sektör dilimini ince bir yaya indirger.

### 7.4 Konum hatası (F.3, merkez ↔ gerçek konum)

| Senaryo | M@90 r50 | M@90 r95 | B1 r50 | Kazanç (r50) |
|---|---|---|---|---|
| A | 150 m | 676 m | 3.569 m | **24×** |
| B | 1.721 m | 3.755 m | 3.590 m | 2,1× |
| C | 590 m | 2.117 m | 17.871 m | **30×** |
| D | 9.189 m | 18.544 m | 17.757 m | 1,9× |

Nokta kestirimi, bölge kapsaması tutmasa bile taban çizgisinden belirgin
biçimde iyidir. TA'lı senaryolarda 24–30 kat; TA'sızlarda ~2 kat.

### 7.5 K8 — Geometri kararlılığı (ADR-26 çözünürlükleriyle)

| Senaryo | M@50 p95 | M@90 p95 | M@95 p95 | `repaired_ratio` |
|---|---|---|---|---|
| A | 2 | **1** | 1 | 0,000000 |
| B | 2 | **3** | 3 | 0,000000 |
| C | 5 | **2** | 1 | 0,000000 |
| D | 2 | **3** | 4 | 0,000000 |

ADR-26 kararı (kentsel 50 m, kırsal 150 m) işe yaradı: Sprint 4'te 100/250 m
ile M@90 p95 değerleri 4 ve 7 iken şimdi **dört senaryoda da ≤ 3**. Onarım
oranı sıfırdır — kenar izlemesi (ADR-21) hiçbir koşuda geçersiz geometri
üretmedi (2.400 satırdan 80.095 satıra çıkan ölçekte de).

M@50'de kırsal TA'lı senaryonun p95'i 5'tir; kütlenin en yoğun yarısı ince TA
yayı üzerinde birkaç parçaya bölünüyor. Bu seviye kriterin konusu değildir
(karşılaştırma seviyesi %90'dır) ve bilgi amaçlı raporlanır.

## 8. K1–K5 durumu

| Kriter | Eşik | Sonuç | Durum |
|---|---|---|---|
| **K1** | Kapsama@90 ∈ [%85, %95], 4 senaryo | 0,52–0,58 | ❌ **Tutmadı** — nedeni ölçüldü (arama bölgesi tavanı), model dilim içinde %90,0–90,8 kalibre |
| **K2** | M@90 vs B0 daralma ≥ %75 | %91,2–99,99 | ✅ Dört senaryoda tuttu |
| **K3** | M@90 vs B1: TA var ≥%50 · TA yok ≥%20 | %99,8/%99,95 · %51,1/%52,0 | ✅ Dört senaryoda tuttu |
| **K4** | Karışık sorgu → hata | Tip düzeyinde imkânsız + test | ✅ |
| **K5** | 4 senaryo koşulmuş ve raporlanmış | A, B, C, D | ✅ |

K8 (S4 kriteri) da bu koşumlarla yeniden ölçüldü: `repaired_ratio` = 0 ve
M@90 `p95_part_count` ≤ 3 → ✅.

**K1 hakkında.** Plan BÖLÜM J eşiklerin ölçümden önce beyan edildiğini ve
sonuca göre değiştirilmeyeceğini söyler. K1 tutmadı; eşik değiştirilmedi,
model de sonucu kurtarmak için değiştirilmedi. Bunun yerine açığın nereden
geldiği ölçülerek gösterildi. Bu, planın K3 için öngördüğü "negatif bulgu da
geçerli bir sonuçtur" duruşunun K1'e uygulanmasıdır.

## 9. Kalan teknik borçlar

| # | Borç | Etki |
|---|---|---|
| 1 | **Arama bölgesi tanımı** — hüzme genişliği yerine "bu hücrenin best-server olabileceği bölge" | K1'in tek engeli; Sprint 6'da karar bekliyor |
| 2 | Kafka ACL yapılandırılmamış (broker'da authorizer yok) | K6'nın 1. katmanı fiilen yok; 2. ve 3. katman çalışıyor ve testli |
| 3 | `median_haversine_m` = `r50_m` tautolojisi (plandan devralındı) | Şema değişikliği gerektirir; ölçüm anlamı bozulmasın diye korundu |
| 4 | Kalibrasyon 12 iterasyon boyunca düz eğride koşuyor | ~30 dk/senaryo boşa; erken çıkış eşiği eklenebilir |
| 5 | Senaryolar arası topic tazeleme elle yapılıyor | Koşum betiği otomatikleştirebilir |

## 10. Sprint 6'ya devredilen işler

1. **T-E04-09** — bütünlük precision/recall (F.5); `integrity_findings`
   dolduğunda ölçülebilir.
2. **Arama bölgesi kararı** — K1'in kaderi buna bağlı. Ölçüm hazır: bölge
   genişletilirse kapsama tavanı da yükselir, K1 yeniden ölçülebilir.
3. **E05 — beş tespit kuralı** (envanter, hız 300, zaman, yörünge, aktivite)
   ve K7.
