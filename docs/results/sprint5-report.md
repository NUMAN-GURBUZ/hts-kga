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

