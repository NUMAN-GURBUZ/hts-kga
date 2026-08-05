# Sprint 6 — Bütünlük Denetimi (E05) Kapanış Raporu

**Tarih:** 2026-07-30
**Kapsam:** T-E05-00..05, T-E05-07, T-E05-08 + T-E04-09 (Sprint 5'ten devir)
**Kabul kriteri:** K7 — kural bazında precision ≥ %90, recall raporlu

---

## 1. Ne yapıldı

Sprint 6, platformun **beşinci servisini** canlıya aldı: `hts.records` akışını
gerçek zamanlı denetleyen ve koşu sonunda toplu geçiş yapan bir bütünlük
izleyicisi. Sprint 5'in sonunda `internal/integrity/detector/` ve
`internal/validation/integrity/` dizinleri boştu; `cmd/integrity` 45 satırlık
bir iskeletti.

Zincir şimdi tam:

```
simülatör (+ enjeksiyon) → Kafka hts.records
    ├─► persister ─────────────────────────────► hts_records
    └─► BÜTÜNLÜK AKIŞ FAZI (kural 1, 3) ───────► integrity_findings
                                                 run_config.inspected_records
        BÜTÜNLÜK TOPLU FAZI (kural 5, 2) ──────► integrity_findings
                                                        │
        doğrulama (F.5, etiketi yalnızca o görür) ◄──────┘
                                                 └─────► integrity_metrics
```

**Sprint 6'nın en önemli sonucu metodolojiktir:** ön analizde ölçülen
tespit-edilebilirlik değerleri ADR-27..31'e **kod yazılmadan önce** yazıldı ve
tam ölçekli ölçümde birebir doğrulandı. Aşağıdaki tabloda öngörü ile ölçüm yan
yanadır.

---

## 2. Tamamlanan tasklar

| Task | İş | SP | Durum |
|---|---|---|---|
| **Gün 0** | ADR-27, 28, 29, 30, 31 + config anahtarları + `smoke.yaml` | 0 | ✅ |
| **T-E05-00** | `pkg/integrityrule` ortak kural sözleşmesi | 1 | ✅ |
| **T-E05-01** | Kural motoru + bulgu yazıcısı + migration 006 | 3 | ✅ |
| **T-E05-02** | Kural 1 — envanter tutarlılığı (akış) | 1 | ✅ |
| **T-E05-03** | Kural 3 — zaman tutarlılığı (akış) | 2 | ✅ |
| **T-E05-04** | Kural 5 — cihaz aktivitesi (toplu) | 1 | ✅ |
| **T-E05-05** | Kural 2 — kinematik tutarlılık + atıf (toplu) | 3 | ✅ |
| **T-E05-07** | `cmd/integrity` iki faz + entegrasyon testleri | 2 | ✅ |
| **T-E04-09** | F.5 precision/recall + karışıklık matrisi + Wilson | 2 | ✅ |
| **T-E05-08** | Dört senaryo K7 ölçümü + rapor | 2 | ✅ |
| | **Toplam** | **17** | |
| T-E05-06 | Kural 4 göstergesi | 2 | ⏭ Sprint 7 (ADR-30) |

**Gün 3 kapısı sağlandı:** küçük koşu (`smoke.yaml`) uçtan uca yeşil, üç kural
bulgu üretti, `integrity_metrics` doldu → kural 2 planlandığı gibi Gün 4'te
yazıldı.

---

## 3. Oluşturulan ve değiştirilen dosyalar

### Yeni paketler

| Yol | İş |
|---|---|
| `pkg/integrityrule/` | Kural kimliği, adı ve **sınıfı** — bağımlılıksız sözleşme |
| `internal/integrity/detector/` | Kural motoru, öncelik, kanıt kurucusu, dört kural |
| `internal/integrity/source/` | Envanter, Kafka akışı, veritabanı taraması |
| `internal/validation/integrity/` | F.5 precision/recall + Wilson + `integrity_metrics` |

### Yeni dosyalar (üretim)

```
pkg/integrityrule/rule.go
internal/integrity/detector/{rule.go,engine.go,claims.go,evidence.go,sink.go}
internal/integrity/detector/{inventory.go,timeorder.go,activity.go,velocity.go}
internal/integrity/source/{inventory.go,stream.go,batch.go}
internal/validation/integrity/{precision_recall.go,writer.go}
internal/storage/postgres/findings.go
internal/storage/migrations/006_integrity_detection.sql
configs/smoke.yaml
scripts/run-integrity.sh
docs/architecture/adr/ADR-27..31 (5 dosya)
```

### Değiştirilen dosyalar

```
cmd/integrity/main.go                  45 satırlık iskelet → iki fazlı servis (390 satır)
cmd/validation/main.go                 HTS_INTEGRITY / HTS_INTEGRITY_ONLY
internal/config/scenario.go            integrity.detection.* bölümü + doğrulama
internal/simulator/event/injector/     kural kimlikleri ortak sözleşmeye taşındı
internal/storage/postgres/runs.go      inspected_records, morphology
internal/storage/postgres/cells.go     SelectCellLocations (envanter yedeği)
internal/validation/pipeline/          F.5 aşaması + yalnızca-bütünlük önkoşulu
configs/*.yaml                         detection bölümü (4 senaryo)
scripts/run-scenario.sh                akış fazı paralel + toplu faz + F.5
Makefile                               integrity-stream / integrity-batch / verify-k7
tests/isolation/import_graph_test.go   S4 içe alma yasakları
```

### Yeni testler

```
pkg/integrityrule/rule_test.go                     kimlik/sınıf/öncelik + 2 PBT
internal/integrity/detector/engine_test.go         öncelik, tek-atıf, idempotanslık + 3 PBT
internal/integrity/detector/evidence_test.go       sızıntı koruması, sonluluk + 2 PBT
internal/integrity/detector/rules_test.go          kural 1, 3, 5 + 3 PBT
internal/integrity/detector/velocity_test.go       kural 2 sınır durumları + 2 PBT
internal/integrity/detector/sink_test.go           satır eşlemesi, talep defteri
internal/integrity/source/batch_test.go            iki önkoşulun her ihlali
internal/validation/integrity/precision_recall_test.go  Wilson, K7 verdict + 1 PBT
tests/integration/integrity_test.go                akış sırası, idempotanslık, gölge SQL, F.5
```

Toplam ~7.000 satır (üretim + test).

---

## 4. Eklenen ADR'ler

| ADR | Karar | Ölçüm dayanağı |
|---|---|---|
| **ADR-27** | Melez çalışma modeli: kural sınıfı **tipte** ifade edilir; motor yanlış fazda kurulmaz | Kural 3 toplu modda precision %8; kural 5 akışta %50 |
| **ADR-28** | Kanıt gücü önceliği (1→5→3→2) + tek-atıf + `suppressed_by` **etiketi**; fazlar arası "ilk talep kazanır" | Naif kural precision %36,5; çift-uç işaretleme yapısal |
| **ADR-29** | Kural 2 spesifikasyonu **dondurulmuş**: yerel destek atfı, karar verilemezse bulgu yok | Naif %36,5 → atıflı %71,8–83,4 (ön analiz) |
| **ADR-30** | Kural 4 `ClassAggregate`, K7 kapsamı dışı; uygulama Sprint 7 | Çıpa üretilemez + boşluk sinyali precision %1,4 |
| **ADR-31** | Veri modeli, idempotanslık, F.5 düzeltmesi, K7 ölçülebilirlik kuralı | Dört sessiz bozulma yolu |

ADR-28'e implementasyon sırasında **bir madde eklendi** (madde 7): fazlar arası
öncelik çakışması. Kural 5 (öncelik 1) kural 3'ten (öncelik 2) güçlüdür ama
sonraki fazda koşar; karar "ilk talep kazanır" olarak yazıldı, gerekçesi
belgelendi (ekle-yalnız bulgu tablosu + ölçülen çakışma kümesinin boş olması) ve
motor bu durumu `demoted` sayacıyla raporlar — sessiz kalmaz.

---

## 5. K7 ölçümü — dört senaryo, tam ölçek

Koşu hacmi senaryo başına: **298.117 kayıt** yayınlandı, **298.117** incelendi
(`verify_integrity` denetim 5: OK). Enjeksiyon oranı 0,02.

### 5.1 Öngörü ↔ ölçüm

Öngörüler ADR-27..31'de **kod yazılmadan önce** beyan edilmişti.

| Kural | ADR öngörüsü | Ölçülen (A/B kentsel · C/D kırsal) | Uyum |
|---|---|---|---|
| 1 envanter | precision %100 · recall %100 | **1,0000 · 1,0000** (dört senaryo) | ✅ birebir |
| 3 zaman | precision %100 · recall tavanı **%64,8** | **1,0000 · 0,6491** (dört senaryo) | ✅ birebir |
| 5 aktivite | precision %100 · recall %100 | **1,0000 · 1,0000** (dört senaryo) | ✅ birebir |
| 2 hız | precision ~%80 kentsel / ~%90 kırsal · recall %4,7 / %12,3 | **0,8548 / 0,8689** · **0,9272 / 0,9521** · recall 0,0445 / 0,1174 | ✅ bantta |
| 4 yörünge | ölçülemez (ADR-30) | precision NULL, kapsam dışı | ✅ |

Kural 3'ün recall'u dikkat çekicidir: ön analizde "kaydırma 2 saat, ortalama
olay aralığı 2,4 saat → önceki olay 2 saat içindeyse yakalanır → **%64,8**"
diye hesaplanmıştı. Ölçülen değer **%64,91**.

### 5.2 Dört senaryo, kural bazında

| Senaryo | Kural | Bulgu | Doğru | Enjekte | Precision | Wilson %95 alt | Recall | K7 |
|---|---|---|---|---|---|---|---|---|
| **A** kentsel TA | 1 | 1172 | 1172 | 1172 | **1,0000** | 0,997 | 1,0000 | ✅ |
| | 2 | 62 | 53 | 1192 | 0,8548 | 0,747 | 0,0445 | ❌ |
| | 3 | 740 | 740 | 1140 | **1,0000** | 0,995 | 0,6491 | ✅ |
| | 4 | 0 | 0 | 1179 | — | — | 0,0000 | ⊘ |
| | 5 | 1171 | 1171 | 1171 | **1,0000** | 0,997 | 1,0000 | ✅ |
| **B** kentsel TA'sız | 1 | 1172 | 1172 | 1172 | **1,0000** | 0,997 | 1,0000 | ✅ |
| | 2 | 61 | 53 | 1192 | 0,8689 | 0,762 | 0,0445 | ❌ |
| | 3 | 740 | 740 | 1140 | **1,0000** | 0,995 | 0,6491 | ✅ |
| | 4 | 0 | 0 | 1179 | — | — | 0,0000 | ⊘ |
| | 5 | 1171 | 1171 | 1171 | **1,0000** | 0,997 | 1,0000 | ✅ |
| **C** kırsal TA | 1 | 1172 | 1172 | 1172 | **1,0000** | 0,997 | 1,0000 | ✅ |
| | 2 | 151 | 140 | 1192 | **0,9272** | 0,874 | 0,1174 | ✅ |
| | 3 | 740 | 740 | 1140 | **1,0000** | 0,995 | 0,6491 | ✅ |
| | 4 | 0 | 0 | 1179 | — | — | 0,0000 | ⊘ |
| | 5 | 1171 | 1171 | 1171 | **1,0000** | 0,997 | 1,0000 | ✅ |
| **D** kırsal TA'sız | 1 | 1172 | 1172 | 1172 | **1,0000** | 0,997 | 1,0000 | ✅ |
| | 2 | 146 | 139 | 1192 | **0,9521** | 0,904 | 0,1166 | ✅ |
| | 3 | 740 | 740 | 1140 | **1,0000** | 0,995 | 0,6491 | ✅ |
| | 4 | 0 | 0 | 1179 | — | — | 0,0000 | ⊘ |
| | 5 | 1171 | 1171 | 1171 | **1,0000** | 0,997 | 1,0000 | ✅ |

Dört senaryoda da bulgu sayıları ≥ 30 (kural 4 hariç), yani K7 eşiği
uygulanabilir durumda (`sufficient = true`, ADR-31/9).

### 5.3 Karışıklık matrisi

Satır = tespit eden kural, sütun = gerçek enjeksiyon. Bastırılmış satırlar
ayrıca gösterilir (ADR-28/4 — bastırma bir etiket, filtre değil).

| Senaryo | Tespit eden | Gerçek | Kanonik | Bastırılmış |
|---|---|---|---|---|
| A | kural 1 | kural 1 | 1172 | 0 |
| | kural 2 | kural 2 | 53 | 0 |
| | kural 2 | **kural 3** | 3 | **9** |
| | kural 2 | temiz | 6 | 0 |
| | kural 3 | kural 3 | 740 | 0 |
| | kural 5 | kural 5 | 1171 | 0 |
| B | kural 2 | kural 3 | 2 | **10** |
| | kural 2 | temiz | 6 | 0 |
| C | kural 2 | kural 3 | 3 | **8** |
| | kural 2 | temiz | 8 | 0 |
| D | kural 2 | kural 3 | 2 | **10** |
| | kural 2 | temiz | 5 | 0 |

(Kural 1, 3, 5 satırları dört senaryoda da aynıdır ve tam köşegendir.)

**Matris iki şeyi kanıtlıyor:**

1. **Kural 1, 3 ve 5 hiçbir yanlış pozitif üretmiyor** — precision %100 bir
   ayar sonucu değil, tasarımın sonucudur (eşik yok, atıf yok).

2. **ADR-28'in öncelik mekanizması ölçülebilir iş yapıyor.** Kaydırılmış damga
   (kural 3) kinematik olarak imkânsız bir geçiş de üretiyor; kural 2 bunu
   görüyor. Öncelik, bu isabetlerin **%77–83'ünü** (9/12, 10/12, 8/11, 10/12)
   bastırıyor. Bastırılmadan kural 2'nin precision'ı A'da 53/71 = %74,6
   olurdu; öncelikle %85,5.

Kural 2'nin kalan hataları iki gruptur: bastırılamayan kural-3 isabetleri
(kural 3'ün recall'u %64,9 olduğu için o olaylar talep edilmemiş) ve 5–8 temiz
yanlış pozitif. Ön analizde naif tasarımın temiz yanlış pozitifi **79** idi;
atıf mekanizması onu **5–8**'e indirdi.

### 5.4 Kural 2 — kentsel/kırsal ayrımı ve K7 sonucu

| | Kentsel (A, B) | Kırsal (C, D) |
|---|---|---|
| Envanter çapı | ~10 km | ~40 km |
| 300 km/h için gereken Δt | < 2 dk | < 8 dk |
| Recall | %4,45 | %11,66–11,74 |
| Precision | %85,5–86,9 ❌ | **%92,7–95,2** ✅ |

Kural 2 **kırsalda K7 eşiğini geçti, kentselde geçmedi.** Neden fizikseldir ve
ADR-29/2'de ölçümden önce yazılıydı: damgalar 5 dakikalık tick ızgarasındadır,
kentsel alanda 10 km'lik bir atlamanın 300 km/h'yi aşması için Δt < 2 dakika
olması gerekir — yani yalnızca aynı tick'in olayları. Kırsalda 40 km'lik atlama
5 dakikada 480 km/h eder ve eşiği rahatça aşar; bu da hem daha çok bulgu (151
vs 62) hem daha yüksek precision demektir.

**Kentselde eşiğin tutmaması önceden beyan edilmiş bir negatif bulgudur.**
Spesifikasyon ölçümden sonra değiştirilmedi.

### 5.5 Duyarlılık eğrisi (ek bilgi — eşiği değiştirmez)

Ön analizde ölçülen (Sprint 5 verisi, naif kural):

| Eşik | Kentsel isabet | Kırsal isabet |
|---|---|---|
| 100 km/h | 113 | 334 |
| 200 km/h | 58 | 191 |
| **300 km/h** (beyan edilen) | **58** | **147** |

Eşiği düşürmek recall'u yükseltirdi ama BÖLÜM C.1'de ölçümden önce beyan edilmiş
bir parametreyi sonuca göre değiştirmek olurdu (ADR-29, reddedilen alternatif C).

---

## 6. Test sonuçları

Tüm birim, PBT ve entegrasyon testleri yeşil; `gofmt` ve `go vet` temiz.

```
28 paket ok  ·  go test -race -count=1 ./...
```

| Katman | Kapsam |
|---|---|
| **Birim** | Kural sınır durumları: Δt=0, zincir başı/sonu, 299/301 km/h, simetrik atıf, tek kayıtlı abone, NULL IMEI, tick-içi eşit damga |
| **PBT (`rapid`)** | 8 yeni değişmez (#9–#16), aşağıda |
| **Entegrasyon** | Akış sırası ön koşulu · idempotanslık · gölge SQL · faz önkoşulu · uçtan uca F.5 |
| **İzolasyon** | `svc_integrity` → `ground_truth` → denied · `internal/integrity` ↛ `internal/simulator` · ↛ `internal/analysis` · `pkg/integrityrule` bağımlılıksız |

### 6.1 Yeni PBT değişmezleri

| # | Değişmez | Sonuç |
|---|---|---|
| 9 | Bir olay için en çok bir **kanonik** bulgu | ✅ |
| 10 | `margin` sonlu, NaN değil, ≥ 1,0 ve ≤ cap | ✅ |
| 11 | `evidence` her girdide `json.Marshal`'dan hatasız geçer | ✅ |
| 12 | Öncelik katı tam sıralamadır (yansımasız, antisimetrik, geçişli, tam) | ✅ |
| 13 | `cell_id ∈ envanter ⇔ kural 1 bulgusu yok` (tam karakterizasyon) | ✅ |
| 14 | Monoton artan akışta kural 3 hiç bulgu üretmez | ✅ |
| 15 | Bulgu sayısı ihlal eden geçiş sayısını aşamaz; aynı kayda iki bulgu yazılamaz | ✅ |
| 16 | Kanıt anahtarları yasaklı listede değil (ground truth sızıntısı yok) | ✅ |

### 6.2 En kritik test: akış sırası ön koşulu

`TestStreamOrderIsMonotonePerSubscriber` — kural 3'ün %100 precision'ı tek bir
varsayıma dayanıyordu: üretim sırası abone başına olay zamanında monoton artar
(tick-major döngü + Kafka anahtarı `pseudo_msisdn` + idempotent üretici).

Test bu varsayımı gerçek Kafka akışında doğruladı: **monotonluk ihlallerinin
kümesi tam olarak `injected_rule = 3` kümesidir**, yanlış pozitif sıfır.
Varsayım sessizce kırılsaydı kural 3, %100 precision'lı bir kuraldan yüzlerce
yanlış pozitif üreten bir kurala dönüşür ve bunu fark etmenin tek yolu
etiketlere bakmak olurdu — yani üretimde kör kalırdık.

### 6.3 Gölge SQL çapraz kontrolü

Kural 1 ve kural 5'in Go uygulaması ile bağımsız SQL sorguları **birebir aynı**
bulgu kümesini verdi. Sprint 5'in PostGIS↔Haversine çapraz kontrolünün
(%0,24 fark) karşılığı: bir uygulama hatasının "bilimsel bulgu" olarak
raporlanmasını engeller.

### 6.4 Sessiz başarısızlık kontrolü

`TestIntegrityPipeline_ProducesMeasurableFindings` — "hiç bulgu yok" durumu tüm
yanlış-pozitif testlerini geçer. Bu test pozitif kontrol koyuyor: her kanonik
kural en az bir bulgu üretmeli. `tests/isolation`'daki
`TestDetectorSeesKnownDependency` ile aynı felsefe.

---

## 7. Geliştirme sırasında bulunan hatalar

Sprint 5'te beş hata gerçek koşuda ortaya çıkmıştı. Sprint 6'da üç kusur
**PBT ve test tarafından**, üretim koşumundan önce yakalandı.

| # | Kusur | Nasıl bulundu | Etkisi olurdu |
|---|---|---|---|
| 1 | Kural 2, Δt=0 durumunda ham `+Inf` margin üretiyordu | PBT #15 | Kuralın çıktısı şema-geçersiz; motorun güvenlik ağına bağımlı kalırdı |
| 2 | Doğrulama önkoşulu F.5'e analiz motorunu şart koşuyordu | Tam ölçekli koşum | K7 ölçümü için gereksiz saatler; ADR-27'nin faz bağımsızlığı ihlali |
| 3 | Envanter yalnızca Redis'ten okunuyordu (koşu ömürlü anahtarlar) | Geçmiş koşuda ölçüm denemesi | Geçmiş bir koşuda bütünlük denetimi yeniden koşturulamazdı |

Ayrıca bir **test iddiası** PBT tarafından çürütüldü: "komşu iki kayıt asla
birlikte işaretlenmez" ADR-28/3'ten daha güçlü bir iddiaydı. Ardışık iki geçiş
ayrı ayrı ihlal edip sırasıyla i ve i+1'i suçlayabilir; her geçiş yine tek bulgu
üretmiştir. Değişmez geçiş başına düzeltildi ve gerekçesi teste yazıldı.

---

## 8. K7 ve diğer kabul kriterleri

### K7 — kural bazında ayrıştırılmış sonuç

> **Kural 1 (envanter), kural 3 (zaman) ve kural 5 (aktivite) dört senaryoda da
> precision ≥ %90 eşiğini karşıladı — üçü de %100,00 ile ve Wilson %95 alt
> sınırı 0,995'in üzerinde. Kural 2 (kinematik) kırsal senaryolarda eşiği geçti
> (%92,7 ve %95,2), kentsel senaryolarda geçmedi (%85,5 ve %86,9); nedeni olay
> aralığının (2,4 saat) atlama mesafesine (~10 km kentsel) göre büyük olmasından
> kaynaklanan fiziksel bir sınırdır ve ADR-29/2'de ölçümden önce beyan
> edilmiştir. Kural 4 (yörünge boşluğu), silinen olayın `event_id`'si S4
> tarafından üretilemediği için olay-çıpalı ölçüme bilgi teorik olarak kapalıdır
> (ADR-30) ve K7 kapsamı dışında bırakılmıştır.**

Recall dört kuralda raporlandı ve eşiğe bağlanmadı (K7'nin kendi ifadesi):
kural 1 ve 5'te %100, kural 3'te %64,91 (teorik tavan %64,8), kural 2'de
%4,45 (kentsel) / %11,7 (kırsal).

### Diğer kriterler

| # | Kriter | Durum |
|---|---|---|
| **K7** | Bütünlük tespiti | ✅ **kısmen** — 3 kural dört senaryoda, kural 2 kırsalda geçti; kentsel kural 2 önceden beyan edilmiş negatif bulgu; kural 4 kapsam dışı |
| **K6** | Kör test (3 katman) | ⚠ **güçlendi, kapanmadı** — katman 2 (PostgreSQL rolü) ve katman 4 (içe alma grafiği) ilk kez **üretim yolunda** sınandı: servis `svc_integrity` DSN'i ile bağlanıyor. Katman 1 (Kafka ACL) borcu açık → Sprint 7 |
| K1 | Kapsama@90 | Değişmez (❌, Sprint 5) — S4 `estimates`'e dokunmuyor |
| K2, K3 | Alan daralması | Değişmez (✅) |
| K4, K5 | Ayrım + 4 senaryo | Değişmez (✅) |
| K8 | Geometri kararlılığı | Değişmez (✅) |
| K9, K10 | Ölçeklenebilirlik, tekrarlanabilirlik | Sprint 8 |

`verify_integrity` dört koşuda da **4 OK + 1 SKIP**:
`hts_records_without_ground_truth` OK · `ground_truth_without_hts_records` OK ·
`published_vs_stored_records` OK (298.117) ·
`estimates_per_analyzed_event` SKIP (analiz motoru koşmadı — K7 ona ihtiyaç
duymaz) · `inspected_vs_published_records` **OK (298.117)**.

---

## 9. Performans ve ölçeklenebilirlik

| Faz | İş | Süre (298.117 kayıt) |
|---|---|---|
| Akış | Kafka tüketimi + kural 1, 3 | ~35 sn (yabancı koşu kayıtları dâhil ~600K mesaj) |
| Toplu | 1000 abone dizisi + kural 5, 2 | **~1,4 sn** |
| F.5 | precision/recall + matris + yazım | ~0,2 sn |

Bellek: hiçbir durum olay sayısıyla büyümüyor.

| Durum | Boyut |
|---|---|
Abone su işareti (kural 3) | 1000 × ~40 B ≈ 40 KB |
Hücre kimliği/konumu (kural 1, 2) | ~330 × ~56 B ≈ 18 KB |
Abone dizisi penceresi (toplu) | ~300 kayıt ≈ 30 KB |
Talep defteri | ~3.100 bulgu ≈ 124 KB |

Toplam < 1 MB. 300.000 olaylık koşu ile 3.000.000 olaylık koşu aynı belleği
kullanır.

**K9'a hazır yapı:** akış kuralları abone anahtarına göre durumludur ve Kafka
anahtarı `pseudo_msisdn` olduğundan bir abonenin tüm kayıtları tek partition'a
düşer → 4 partition, 4 replika, replikalar arası durum paylaşımı **gerekmez**.
Bu, `pkg/kafka`'daki anahtar seçiminin zaten beyan edilmiş gerekçesiydi.

---

## 10. Kalan teknik borçlar

| # | Borç | Etki | Sprint |
|---|---|---|---|
| 1 | **Kural 4 göstergesi** (T-E05-06) | K7'ye katkısı yok; koşu düzeyi "kayıp kayıt kütlesi" tahmini eksik | 7 |
| 2 | **Kafka ACL** (K6 katman 1) | K6 formel olarak eksik kalıyor | 7 |
| 3 | **Arama bölgesi tanımı** (K1) | Sprint 5'ten devir; E05'ten bağımsız | 7+ |
| 4 | Sağlık portları paralel koşumda çakışıyor (:8083/:8084/:8085) | İki senaryo aynı anda koşturulamaz; betik sıralı koşuyor | 7 |
| 5 | Kafka topic'i koşular arası birikiyor | Akış fazı yabancı kayıtları da okuyor (~2× süre); `run_id` süzgeci doğruluğu koruyor | 7 |
| 6 | `median_haversine_m` = `r50_m` totolojisi | Sprint 5'ten devir; F.5 kullanmıyor | 7+ |
| 7 | Kural 2 kentselde precision %85–87 | Fiziksel sınır; eşik değiştirilmedi | — (bulgu) |

Borç #5'in bir sonucu kayda değer: Sprint 5 koşularının Kafka kayıtları topic
tazelenmesi nedeniyle silinmiş olduğu için o koşuların akış fazı **yeniden
koşturulamadı**; K7 ölçümü için dört senaryo baştan koşuldu. Toplu faz ise
`hts_records`'tan okuduğu için geçmiş koşularda çalışabilir (envanter yedeği
sayesinde).

---

## 11. Sprint 6 değerlendirmesi

### Ne iyi gitti

**Beyan-sonra-ölç disiplini işledi.** Ön analizin ölçümleri ADR'lere kod
yazılmadan önce yazıldı ve tam ölçekte birebir doğrulandı — kural 3'ün recall
tavanı %64,8 hesaplanıp %64,91 ölçüldü. Kural 2'nin precision'ı için beyan
edilen %80–90 bandı da tuttu. Bu, "sonuca göre tanım değiştirme" riskini
yapısal olarak kapattı: değiştirilecek bir şey kalmadı, çünkü tahmin doğruydu.

**Ölçüm tasarımı yönlendirdi, tersi değil.** Kural 5'in `ClassBatch` olması,
kural 3'ün `ClassStream` olması, kural 2'nin atıf mekanizması ve kural 4'ün
kapsam dışı bırakılması — dördü de ön analizdeki ölçümlerden çıktı. Tasarımı
önce yapıp sonra ölçmek, kural 5'te precision'ı %50'ye, kural 3'te %8'e
düşürecekti.

**PBT üretimden önce üç kusur yakaladı.** Sprint 5'te beş hata gerçek koşuda
ortaya çıkmıştı; Sprint 6'da benzer kusurlar test aşamasında görüldü.

**Kapı mekanizması işe yaradı.** Gün 3 sonunda K7 üç yapısal kuralla ölçülebilir
hâldeydi; kural 2 ondan sonra, kesilebilir konumda yazıldı. Kesilmesi
gerekmedi, ama gerekseydi sprint hedefsiz kalmayacaktı.

### Ne zorladı

**Efor planın 1,7 katı çıktı** (17 SP vs planın 10 SP'si) — ama bu ön analizde
öngörülmüştü ve plan ona göre 5 güne çıkarılmıştı. Planın 8 SP'si yalnızca kural
mantığını kapsıyordu; motor, servis bağlantısı ve koşum planlanmamıştı.

**Doğrulama önkoşulu F.5'i bloke etti.** K1–K3'ün "analiz tamamlandı" önkoşulu
F.5'e uygulanıyordu; F.5 `estimates`'e hiç dokunmadığı için bu yanlıştı ve
`HTS_INTEGRITY_ONLY` modu eklenerek düzeltildi. Ön analizde bu bağımlılık
görülmemişti.

**Paralel koşum sağlık portu çakışması üretti** — Sprint 5 hatası #5'in aynısı,
bu kez senaryolar arasında. Betik sıralı koşuyor; borç olarak kaydedildi.

### Dürüst özet

Platform beş manipülasyon türünün **üçünü** kusursuz kesinlikle (%100 precision,
sıfır yanlış pozitif), birini kırsalda eşik üstü / kentselde eşik altı
kesinlikle, birini ise "bu veride olay düzeyinde bilgi yok" sonucuyla ele
alıyor. Son ifade bir başarısızlık değil, ölçülmüş ve ADR ile gerekçelendirilmiş
bir sınırdır — beş kuralın hepsinin çalıştığını iddia etmekten daha
savunulabilir.

---

## 12. Sprint 7'ye devredilen işler

1. **T-E05-06** — kural 4 koşu düzeyi göstergesi (ADR-30 kapsamında, 2 SP).
2. **Kafka ACL** — K6'nın birinci katmanı; K6'yı formel olarak kapatan tek iş (2 SP).
3. **Sağlık portu çakışması** — paralel senaryo koşumu için port ayrımı (0,5 SP).
4. **Topic tazeleme otomasyonu** — koşum betiğine (0,5 SP).
5. **Arama bölgesi kararı (K1)** — E05'ten bağımsız, ayrı bir çalışma olarak.
6. **E06 (S7)** — gRPC + REST + statik web + Leaflet; plan BÖLÜM H.

---

## 13. Yeniden üretim

```bash
make migrate-up                              # migration 006 dâhil
scripts/run-integrity.sh configs/urban_ta.yaml     # senaryo A
scripts/run-integrity.sh configs/urban_no_ta.yaml  # senaryo B
scripts/run-integrity.sh configs/rural_ta.yaml     # senaryo C
scripts/run-integrity.sh configs/rural_no_ta.yaml  # senaryo D
make verify-k7 RUN_ID=<run_id>
```

Koşu kimlikleri: `docs/results/*.integrity.run_id`
Koşum günlükleri: `docs/results/*.integrity.log`

| Senaryo | run_id |
|---|---|
| A kentsel TA | `6a67babd-ce37-429c-9d39-0ed343d8c4c2` |
| B kentsel TA'sız | `43c8003a-ef4b-4e8a-89a0-d43c945aa1b1` |
| C kırsal TA | `901d19e9-def4-427e-a0e5-d0abdc9feec5` |
| D kırsal TA'sız | `e6b0f067-ccab-4810-a3e2-d3c87915ca16` |
