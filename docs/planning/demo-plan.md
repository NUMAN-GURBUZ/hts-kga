# Görsel Demo Analizi ve Planı

**Tarih:** 2026-07-30
**Durum:** Analiz — **kod yazılmamıştır**
**Amaç:** Sprint 0–6 çıktısını hocaya ~5 dakikada görsel olarak anlatmak

---

## 0. Yönetici özeti

**Projede görselleştirme altyapısı fiilen yok** — ama görselleştirilecek **veri**
fazlasıyla var ve PostGIS onu **sıfır Go koduyla** harita formatına çevirebiliyor.

| Katman | Durum | Ölçüm |
|---|---|---|
| `web/` (Leaflet, E07) | **boş dizin** | 0 dosya |
| `proto/` (gRPC, E06) | **boş dizin** | 0 dosya |
| `cmd/gateway` (REST/GeoJSON, E06) | **45 satır iskelet** | Sprint 7 kapsamı |
| Grafana 11.6.1 | çalışıyor, Prometheus datasource kurulu, **0 dashboard** | provisioning dizini repo'dan mount'lu |
| Prometheus | çalışıyor, 2112–2116 portlarını scrape ediyor | **beş portun hiçbiri açık değil** |
| OTel | `MeterProvider` + prometheus exporter **kuruluyor** | ama hiçbir yere servis edilmiyor **ve hiç metrik enstrümanı kaydedilmemiş** |
| GeoJSON üretimi | kodda **hiç yok** | tek referans bir yorum satırı |
| **PostGIS geometri** | **hazır ve zengin** | `ST_AsGeoJSON` çalışıyor (ölçüldü) |

**Sonuç:** demo için yazılması gereken şey servis kodu değil, **bir sorgu
katmanı**. En iyi maliyet/etki dengesi: Grafana (nokta + zaman serisi, sıfır kod)
+ tek statik Leaflet sayfası (poligon vuruşu, ~150 satır HTML, Go kodu yok).

**Ama önce çözülmesi gereken bir veri problemi var** (Bölüm 2).

---

## 1. Bileşen bileşen: neyi görselleştirebiliriz

Ölçülmüş durum. "Efor" sütunu: **T0** = sıfır kod (SQL/config/QGIS) ·
**T1** = küçük görselleştirme kodu (statik HTML, Go yok) · **T2** = gerçek
geliştirme (Sprint 7 kapsamı).

| # | Bileşen | Veri var mı? | Görselleştirilebilir mi? | Efor |
|---|---|---|---|---|
| 1 | **Baz istasyonları** (`cells.location`) | ✅ 327 hücre/koşu | Nokta katmanı; Grafana Geomap doğrudan lat/lon okur | **T0** |
| 2 | **Sektör dilimleri** | ⚠ kısmen | `cells` azimut+hüzme+r_max taşıyor ama **poligon saklanmıyor**. Ancak **B1 tahminleri sektör diliminin kendisidir** ve `estimates.geometry`'de duruyor | **T0** (B1) / T1 (SQL'de türetme) |
| 3 | **Ajan hareketleri** (gerçek yörünge) | ✅ `ground_truth.true_location`, tüm olaylar | `ST_MakeLine(true_location ORDER BY time)` — ölçüldü: 8 nokta → 261 bayt | **T0** |
| 4 | **HTS kayıtları** | ✅ 298.117/koşu | Kaydın **konumu yok** (tasarım gereği). Serving hücreye bağlanır: kayıt → `cell_id` → `cells.location` | **T0** |
| 5 | **Timing Advance halkaları** | ⚠ türetilir | `ta_value` `hts_records`'ta; halka geometrisi **saklanmıyor** (analizde bellekte üretiliyor). `ST_Buffer(cell.location, ta·78,12)` ile türetilir — ölçüldü: 952 bayt | **T0** (SQL) |
| 6 | **Olasılık poligonları** (M@50/90/95) | ✅ 24.960 geometri (senaryo A) | `estimates.geometry` GEOGRAPHY(MULTIPOLYGON). `ST_AsGeoJSON` ölçüldü: M@90 **239 bayt**, B1 1.933, B0 10.075 | **T0** |
| 7 | **Kütle ağırlıklı merkez** | ✅ `estimates.centroid` | Nokta katmanı (58 bayt/nokta) | **T0** |
| 8 | **Ground Truth** (gerçek konum) | ✅ 299.296 satır/koşu | Nokta katmanı — poligonun **içinde mi** sorusunun görsel cevabı | **T0** |
| 9 | **Integrity Findings** | ✅ 3.154/koşu | Haritalanabilirlik kurala bağlı (aşağıda) | **T0** |
| 10 | **Olasılık haritası (ham ızgara kütlesi)** | ❌ **saklanmıyor** | ADR-10: ızgara hücre kütleleri hacim nedeniyle saklanmıyor; yalnızca kontur ve merkez kalıcı. Isı haritası için analiz motoruna çıktı eklemek gerekir | **T2** |
| 11 | **K1–K3 metrikleri** | ✅ `metrics` tablosu (5 satır/koşu) | Grafana tablo/stat paneli | **T0** |
| 12 | **K7 metrikleri** | ✅ `integrity_metrics` (5 satır/koşu) | Grafana tablo paneli | **T0** |
| 13 | **Olay yoğunluğu (diurnal λ)** | ✅ hypertable | TimescaleDB `time_bucket('1 hour', time)` → zaman serisi. **Poisson modelinin görsel kanıtı** | **T0** |
| 14 | **Kafka akış canlılığı** | ⚠ | Prometheus'ta metrik yok. PostgreSQL satır sayısı büyümesi canlı izlenebilir | **T0** (dolaylı) |
| 15 | **Prometheus metrikleri** | ❌ | Enstrüman yok + `/metrics` servis edilmiyor | **T2** |

### 1.1 Integrity Findings'in haritalanabilirliği — ölçülmüş

| Kural | Bulgu | Haritalanabilir | Konumsuz |
|---|---|---|---|
| 1 envanter | 1172 | **0** | **1172** |
| 2 hız | 62 | 62 | 0 |
| 3 zaman | 740 | 740 | 0 |
| 5 aktivite | 1171 | 1171 | 0 |

**Kural 1 tanımı gereği haritalanamaz** ve bu demoda anlatılacak güzel bir
ayrıntıdır: bulgu "kayıt, envanterde **olmayan** bir hücreyi beyan ediyor" der —
o hücrenin konumu yoktur, çünkü hücre yoktur. Haritada "bilinmeyen hücre"
etiketiyle bir kenar listesi olarak gösterilir; iğne konulamaz.

---

## 2. Demo başlamadan çözülmesi gereken veri problemi

**Hiçbir koşuda hem geometri hem bütünlük bulgusu yok.**

| Koşu | Kayıt | Ground truth | **Estimates** | **Findings** | Metrics |
|---|---|---|---|---|---|
| Sprint 5 · A `edb59965` | 298.117 | 299.296 | **24.960** | **0** | 5 |
| Sprint 5 · B/C/D | 298.117 | 299.296 | 14–25 bin | **0** | 5 |
| Sprint 6 · A `6a67babd` | 298.117 | 299.296 | **0** | **3.154** | 0 |
| Sprint 6 · B/C/D | 298.117 | 299.296 | **0** | 3.154–3.242 | 0 |

Nedeni: Sprint 5 koşuları bütünlük servisi yazılmadan önce yapıldı; Sprint 6
koşumları K7 için analiz motorunu bilinçli olarak atladı (`run-integrity.sh`,
K7 `estimates`'e dokunmuyor).

**Çözüm — sıfır kod:** `run-scenario.sh` (Sprint 6'da bütünlük fazlarını içerecek
şekilde güncellendi) tek komutta hepsini üretir:
simülasyon → persister'lar ∥ bütünlük akışı → bütünlük toplu → analiz →
F.1–F.5. Kalibrasyon varsayılan olarak kapalı (`HTS_CALIBRATE=false`), süre
~15–30 dk.

### 2.1 İkinci problem: örnekleme demo yörüngelerini seyrelttiyor

ADR-14 gereği analiz yalnızca örneklenmiş 'V' kümesini işliyor: 298.117 olayın
**4.992'si**. Sonuç, abone başına çok az geometri:

| Ölçüm (senaryo A) | Değer |
|---|---|
| En zengin abonenin tahminli olayı (30 gün) | **14** |
| Bir günde ≥3 tahminli olayı olan abone | var: `a390b5cdafde`, 2026-01-20, 4 olay |

Planın ADR-13'te öngördüğü demo sahnesi ("tek ajan, tek gün, üç yöntem üst üste
→ ~50 geometri") mevcut veriyle **ancak 20 geometri** olur.

**Çözüm — sıfır kod, tek config dosyası:** `configs/demo.yaml` — `urban_ta`'nın
kopyası, yalnızca ölçek küçültülmüş:

```yaml
simulation: { agents: 50, duration_days: 7 }      # ~3.500 olay
analysis:   { sample: { validation_events: 3000 } }  # olayların ~%85'i analiz edilir
```

Böylece bir abonenin 7 günlük izinin **neredeyse her olayı** için üç yöntemin
geometrisi bulunur → abone başına ~70 olay × 5 geometri. Koşu süresi de birkaç
dakikaya iner. Bilimsel iddia (K1–K3) bu config'le **ölçülmez**; demo config'i
olduğu açıkça yazılır ve tam ölçek koşuları raporda durmaya devam eder.

---

## 3. Soruların cevapları

### 1. En etkileyici demo senaryosu ne olur?

**"Bir telefon kaydından konuma — ve o kaydın yalan söylediğini nasıl anlarız."**

Üç perdelik bir anlatı; her perdenin tek bir görsel vuruşu var:

**Perde 1 — Daralma (bilimsel iddia).** Tek bir HTS kaydı seçilir. Haritada üst
üste üç geometri:

| Yöntem | Alan (ölçülmüş, senaryo A medyan) | Görsel |
|---|---|---|
| B0 naif daire | **118,46 km²** | şehri yutan dev daire |
| B1 sektör dilimi | **21,37 km²** | pasta dilimi |
| M@90 olasılıksal | **0,0392 km²** | TA yayı üzerinde ince şerit |

Ve şeridin **içinde** ground truth noktası. Tek bakışta anlaşılan mesaj:
**3.000 kat daralma ve gerçek konum hâlâ içinde.** (K2 %99,97 · K3 %99,82.)

**Perde 2 — Kalibrasyon dürüstlüğü.** Aynı haritada 30 olayın M@90 poligonu ve
ground truth noktaları. Bazı noktalar poligonun dışında. "Model %90 diyor,
ölçtük: dilim içinde %90,0 — ama dilimin kendisi gerçek konumun %58'ini kapsıyor,
bu yüzden K1 tutmadı ve bunu sakladığımız yerde değil raporda yazdık."

**Perde 3 — Bütünlük (adli vuruş).** Manipüle edilmiş bir kayıt seçilir.
`integrity_findings` satırının `evidence` JSON'u ekrana:

```json
{"v":1,"rule":3,"record_time":"...","watermark":"...","backstep_s":7200,"prev_event_id":"..."}
```

"Bu kayıt, aynı abonenin daha önce görülmüş en yüksek zamanından 7200 saniye
geriye gidiyor. Sistem bunu, kaydın enjekte edildiğini **bilmeden** buldu."
Ardından K7 tablosu: üç kural %100,00 precision, 0 yanlış pozitif.

**Neden bu senaryo:** projenin üç ayağını (olasılıksal konumlama, bilimsel
dürüstlük, adli bütünlük) sırayla ve her birini tek bir görselle veriyor.
Perde 1 etkileyici, Perde 2 güvenilir yapıyor, Perde 3 farklılaştırıyor.

### 2. Simülasyon sırasında hangi veriler gerçek zamanlı gösterilebilir?

Prometheus boş olduğu için canlılık **PostgreSQL'den** okunur (Grafana'nın
5 sn'lik yenileme aralığıyla):

| Panel | Sorgu kaynağı | Ne anlatır |
|---|---|---|
| **Kayıt sayacı** | `count(*) FROM hts_records WHERE run_id=…` | Kafka→DB akışı canlı |
| **Ground truth sayacı** | `count(*) FROM ground_truth` | İki topic paralel akıyor |
| **Fark = enjeksiyon kural 4** | `gt − records` | 1.179 satır: "silinen kayıtlar" |
| **Bulgu sayacı, kurala göre** | `integrity_findings GROUP BY rule_id` | Bütünlük **akış fazı** canlı çalışıyor |
| **Olay yoğunluğu** | `time_bucket('1 hour', time)` | Diurnal Poisson profili gözle görünür (gece dibi, sabah/akşam tepeleri) |
| **Abone başına kayıt** | `GROUP BY pseudo_msisdn` | 1000 ajan gerçekten üretiyor |
| **`verify_integrity`** | fonksiyon çağrısı | 5 denetim: 4 OK + 1 SKIP |
| **Kapsama dışı oranı** | `covered = false` oranı | ADR-08 kalite göstergesi |

Simülatör ~2 dk sürüyor (8.640 tick), persister'lar ve bütünlük akışı ona
paralel. Grafana panelleri gözle görülür biçimde artar — "canlı sistem" hissi
buradan gelir.

### 3. Harita üzerinde neleri gösterebiliriz?

| Katman | Kaynak | Hazır mı |
|---|---|---|
| Baz istasyonları (327 nokta) | `cells.location` | ✅ T0 |
| Sektör dilimleri | `estimates` B1 satırları (poligon olarak saklı) | ✅ T0 |
| Kapsama yarıçapı | `ST_Buffer(cells.location, r_max_m)` | ✅ T0 |
| Ajan gerçek yörüngesi | `ST_MakeLine(ground_truth.true_location ORDER BY time)` | ✅ T0 |
| HTS kayıtlarının serving hücresi | `hts_records → cells` join | ✅ T0 |
| TA halkaları | `ST_Buffer(cell.location, ta_value·78,12)` (dış) − (iç) | ✅ T0 |
| Olasılık poligonları M@50/90/95 | `estimates.geometry` | ✅ T0 |
| B0 dairesi / B1 dilimi | `estimates.geometry` | ✅ T0 |
| Kütle ağırlıklı merkez | `estimates.centroid` | ✅ T0 |
| Ground truth noktası | `ground_truth.true_location` | ✅ T0 |
| Bütünlük bulguları (kural 2, 3, 5) | `findings → hts_records → cells` | ✅ T0 |
| Bütünlük bulguları (kural 1) | **konumsuz — tanım gereği** | liste olarak |
| Ham olasılık ızgarası (ısı haritası) | **saklanmıyor** (ADR-10) | ❌ T2 |

### 4. Projede bunlardan hangileri zaten mevcut?

**Veri olarak: 12/13 katman mevcut.** Eksik olan tek şey ham ızgara kütlesi
(ADR-10 gereği bilinçli olarak saklanmıyor).

**Görselleştirme kodu olarak: sıfırı.** `web/` boş, `proto/` boş, gateway
iskelet, Grafana'da dashboard yok, GeoJSON üretilmiyor.

Ama **PostGIS'in kendisi bir görselleştirme sunucusudur** ve bu ölçüldü:
`ST_AsGeoJSON`, `ST_MakeLine`, `ST_Buffer`, `json_build_object` ile tam bir
GeoJSON `FeatureCollection` üretiliyor — **34.881 bayt, 5 geometri, sıfır Go
kodu.**

### 5. Hangileri yalnızca küçük bir görselleştirme koduyla gösterilebilir?

**T0 — hiç kod yazmadan (yalnızca SQL + config):**
- Grafana: tüm sayaçlar, K1–K3 ve K7 tabloları, diurnal profil, `verify_integrity`
- Grafana Geomap: baz istasyonları, ground truth noktaları, merkezler, bulgular
  (nokta katmanları — Geomap lat/lon sütunlarını doğrudan okur)
- QGIS: PostGIS'e doğrudan bağlanıp **poligonlar dâhil her katman**
- `psql` ile `.geojson` dosyası dışa aktarma

**T1 — tek statik HTML (Leaflet, ~150 satır, Go kodu yok):**
- Üç yöntemin poligonlarının üst üste bindirilmesi (Perde 1'in vuruşu)
- Katman seçici (B0/B1/M@50/M@90/M@95/TA/ground truth/yörünge)
- Bulguya tıklayınca `evidence` JSON'unun gösterilmesi

**T2 — gerçek geliştirme (Sprint 7 / E06–E07, planda 16 SP):**
- `cmd/gateway` REST + `ST_AsGeoJSON` uçları, ADR-12/13 filtreleri
- Prometheus metrik enstrümanları + `/metrics` servisi
- Ham olasılık ızgarası ısı haritası (analiz motoruna çıktı eklenmesi)

### 6. Mevcut projede GeoJSON veya benzeri harita çıktıları üretiliyor mu?

**Hayır.** Kodda `ST_AsGeoJSON` çağrısı yok; tek geçtiği yer
`internal/analysis/geometry/multipolygon.go` içinde bir yorum (halka yönü için
GeoJSON RFC 7946'ya atıf).

**Ama üretilebiliyor ve bu ölçüldü** (Bölüm 4). Geometri PostGIS'te
GEOGRAPHY olarak saklı; ADR-12 zaten "GeoJSON veritabanında üretilir"
(`ST_AsGeoJSON`) diye karara bağlanmış — yalnızca çağıracak katman yazılmamış.

### 7. PostgreSQL + PostGIS ile QGIS / Leaflet / OpenLayers canlı harita yapılabilir mi?

**Evet, üçü de — farklı efor ve farklı "canlılık" seviyesiyle.**

| Araç | Nasıl | Canlılık | Efor | Poligon |
|---|---|---|---|---|
| **QGIS** | PostGIS bağlantısı → katmanları sürükle | Yenile tuşu | **T0** | ✅ tam |
| **Grafana Geomap** | PostgreSQL datasource + dashboard JSON | **otomatik 5 sn** | **T0** | ⚠ yalnızca nokta/çizgi |
| **Leaflet (statik)** | `psql` → `.geojson` → `python3 -m http.server` | elle yeniden aktarma | **T1** | ✅ tam |
| **Leaflet (canlı)** | `cmd/gateway` GeoJSON ucu | otomatik | T2 | ✅ tam |

**Önemli uyarı:** Grafana 11'in Geomap paneli sorgudan gelen **poligonu
çizemez** — GeoJSON katmanı dosya/URL'den yüklenir, marker/route katmanları ise
lat/lon sütunlarından. Yani "B0 vs B1 vs M@90 üst üste" vuruşu Grafana'da
**yapılamaz**; o vuruş için QGIS (T0) veya statik Leaflet (T1) gerekir.

Bu, demoyu iki araca bölmeyi gerektiriyor ve aslında iyi bir şey: Grafana
"sistem çalışıyor" perdesini, harita "bilim burada" perdesini üstlenir.

### 8. Grafana üzerinden hangi metrikler canlı gösterilebilir?

**Prometheus üzerinden: hiçbiri.** Enstrüman yok, `/metrics` servis edilmiyor,
2112–2116 portları kapalı. Bunu düzeltmek T2 işidir (Sprint 8 / E09'da planlı).

**PostgreSQL datasource üzerinden: çok şey** (Grafana'nın PostgreSQL datasource'u
yerleşiktir, TimescaleDB desteği kutudan çıkıyor):

*Canlı akış panelleri (koşu sırasında artan):*
`hts_records` / `ground_truth` / `estimates` / `integrity_findings` sayaçları ·
saatlik olay yoğunluğu (`time_bucket`) · kurala göre bulgu dağılımı ·
kapsama dışı oranı · abone sayısı

*Sonuç panelleri (koşu sonrası):*
K1–K3 (`metrics`: `coverage_rate`, `median_area_km2`, `reduction_vs_b0/b1`,
`p95_part_count`, `repaired_ratio`) · K7 (`integrity_metrics`: precision,
recall, Wilson aralığı, `sufficient`) · `verify_integrity()` 5 denetimi ·
karışıklık matrisi (findings ⋈ ground_truth)

*En etkileyici tek panel:* **saatlik olay yoğunluğu.** Diurnal λ profili
(gece dibi 0,05 → sabah tepesi 0,80 → akşam tepesi 0,80) grafikte gözle
görünür ve "1000 ajanın davranışı modellendi" iddiasını tek bakışta kanıtlar.

### 9. Demo sırasında hangi komutlar sırayla çalıştırılmalı?

Bölüm 5'te dakika dakika verilmiştir.

### 10. Hocanın ~5 dakikada anlayacağı en iyi akış?

Bölüm 5.

### 11. Mevcut görsel çıktı üretme altyapısının tamamı

Dürüst ve kısa liste:

| Var olan | Ne işe yarıyor |
|---|---|
| Grafana 11.6.1 (`:3000`, admin/admin) | Çalışıyor; provisioning dizini repo'dan mount'lu → dashboard eklemek dosya kopyalamak |
| Prometheus (`:9090`) | Çalışıyor; **hedefleri boş** |
| PostgreSQL + PostGIS 3.4 + TimescaleDB | **Gerçek görselleştirme kaynağı**: GEOGRAPHY geometriler + hypertable zaman serileri |
| `health.MustServe` (`:8083–8085`) | `/health`, `/ready` — JSON, görsel değil |
| `docs/results/*.log` | Koşum günlükleri (metin) |
| `docs/results/sprint5-report.md`, `sprint6-report.md` | Markdown tablolar — hazır, sunumda doğrudan kullanılabilir |
| `make verify-k7`, `verify-integrity`, `verify-isolation` | Terminal çıktısı; **demoda etkili** (izolasyonun "permission denied" vermesi) |

Bunun dışında görsel çıktı üreten hiçbir bileşen yok.

### 12. Yoksa en az kod değişikliğiyle demo çözümü

**Önerilen: iki araçlı hibrit — toplam ~yarım gün, sıfır Go kodu.**

| Adım | İş | Efor | Kod |
|---|---|---|---|
| **A** | `configs/demo.yaml` (50 ajan × 7 gün, örneklem 3000) | 10 dk | config |
| **B** | `run-scenario.sh configs/demo.yaml` — tek koşuda estimates + findings + metrics | ~5 dk koşum | yok |
| **C** | Grafana PostgreSQL datasource (provisioning YAML) | 15 dk | YAML |
| **D** | Grafana dashboard JSON: 8 panel (sayaçlar, diurnal, K1–K3, K7, verify) | 2 sa | JSON |
| **E** | `scripts/export-geojson.sh` — `psql` ile katmanları `.geojson` dosyalarına aktarır | 45 dk | bash + SQL |
| **F** | `web/demo.html` — tek Leaflet sayfası, katman seçici, `evidence` popup'ı | 2 sa | HTML/JS |
| **G** | Kuru prova + yedek ekran görüntüleri | 45 dk | — |

**Neden `web/demo.html` T1 sayılıyor:** Go kodu yok, servis yok, proto yok,
gateway'e dokunulmuyor. Sayfa `.geojson` dosyalarını `python3 -m http.server`
üzerinden okur. E06/E07'nin (16 SP) hiçbir parçası öne çekilmez; Sprint 7
planlandığı gibi yazılır ve bu sayfa o zaman atılır.

**Reddedilen alternatif — gateway'i öne çekmek.** ADR-12'nin REST handler'ları
+ `ST_AsGeoJSON` uçları demoyu "canlı" yapardı, ama 8 SP'lik planlı bir sprint
işini demo baskısı altında yazmak demektir; ADR-13'ün filtre ve 500-geometri
sınırı da o zaman aceleye gelir. Statik sayfa aynı görseli verir ve Sprint 7'yi
kirletmez.

---

## 4. Ölçülmüş kanıtlar

Bu analizdeki her iddia çalışan sistemde doğrulandı.

```
ST_AsGeoJSON(estimates.geometry)     B0 10.075 · B1 1.933 · M@90 239 bayt
Tam GeoJSON FeatureCollection        34.881 bayt / 5 geometri (saf SQL)
ST_MakeLine(ground_truth)            8 nokta → 261 bayt (ajan yörüngesi)
ST_Buffer(cell, ta·78,12)            952 bayt (TA halkası)
Bulgu haritalanabilirliği            kural 2/3/5: %100 · kural 1: %0 (tanım gereği)
Prometheus hedefleri (2112–2116)     beşi de kapalı
Metrik enstrümanı                    0 (grep: Counter/Histogram/Gauge yok)
Grafana dashboard                    0 dosya
web/ · proto/                        0 dosya
En zengin abone (senaryo A)          14 tahminli olay / 30 gün
Bir günde ≥3 tahminli olay           a390b5cdafde · 2026-01-20 · 4 olay
```

---

## 5. Demo akışı — 5 dakika, dakika dakika

**Hazırlık (hoca gelmeden):** `make infra-up` · Grafana `:3000` açık, dashboard
yüklü · `python3 -m http.server 8000 -d web/` çalışıyor · terminal ve tarayıcı
sekmeleri hazır · `demo.yaml` koşusunun `run_id`'si elde.

### 0:00–0:45 · Mimari ve canlı akış

```bash
make infra-up                        # 5 konteyner: postgres, kafka, redis, prometheus, grafana
scripts/run-scenario.sh configs/demo.yaml &   # arka planda başlat
```

Grafana'ya geç. **Sayaç panelleri artmaya başlar.** Söylenecek:

> "Beş servis var. Simülatör 50 sanal telefon aboneliğini 7 gün boyunca
> yürütüyor, 3GPP yayılım modeliyle hangi baz istasyonuna bağlandıklarını
> hesaplıyor ve iki Kafka topic'ine yazıyor: operatör kayıtları ve **gerçek
> konum**. Gerçek konumu analiz motoru hiçbir zaman görmüyor — buna sonra
> döneceğim."

Diurnal panelini göster: **"Bu eğri modellenmiş insan davranışı: gece dibi,
sabah ve akşam tepeleri."**

### 0:45–1:30 · Kör test (projenin omurgası)

```bash
make verify-isolation
```

Çıktı: `svc_analysis: ground_truth ENGELLENDI — permission denied` ·
`svc_integrity: ENGELLENDI` · `svc_validation: erişim AÇIK`

```bash
go test ./tests/isolation/ -run TestIntegrity -v
```

> "Analiz ve bütünlük servisleri gerçek konumu **veritabanı rolüyle**
> göremiyor, Kafka ACL'iyle o topic'e abone olamıyor ve **import grafiği testiyle**
> simülatör kodunu çağıramıyor. Dört katman. Ölçümlerin kör test olduğunu
> iddia etmiyoruz, zorunlu kılıyoruz."

### 1:30–3:00 · Harita: bilimsel iddia (en uzun perde)

Leaflet sayfasını aç. Katmanları **sırayla** açarak anlat:

1. **Baz istasyonları** (327 nokta) — "operatörün şebekesi"
2. **Tek bir HTS kaydı seç** — "elimizde yalnızca bu var: abone, zaman, hücre kimliği, bir TA değeri"
3. **B0 dairesi** → `118,46 km²` — "naif yaklaşım: hücrenin tüm kapsama alanı"
4. **B1 dilimi** → `21,37 km²` — "sektör anteni: 5,5 kat daralma"
5. **TA halkası** — "cihazın baz istasyonuna uzaklığı, 78 metrelik adımlarla"
6. **M@90 poligonu** → `0,0392 km²` — "olasılıksal model: **B0'a göre 3.000 kat**"
7. **Ground truth noktası** — poligonun içinde

> "K2 kriteri %75 daralma istiyordu, %99,97 ölçtük. K3 %50 istiyordu, %99,82."

Ardından **30 olayın** poligonlarını + ground truth noktalarını aç. Bazıları
dışarıda:

> "Model '%90 güvenle bu alanın içinde' diyor. Ölçtük: sektör diliminin **içinde**
> %90,0 kalibre. Ama dilimin kendisi gerçek konumun yalnızca %58'ini kapsıyor,
> çünkü baz istasyonu hüzmesinin dışında da en güçlü sunucu olabiliyor. Bu
> yüzden K1 tutmadı. Eşiği ölçümden önce beyan ettik, sonra değiştirmedik —
> nedenini ölçüp rapora yazdık."

### 3:00–4:15 · Bütünlük tespiti (adli perde)

Haritada bulgu katmanını aç — bulgular kırmızı iğnelerle görünür.

Bir kural-3 bulgusuna tıkla, `evidence` popup'ı:

```json
{"v":1,"rule":3,"backstep_s":7200,"watermark":"...","record_time":"..."}
```

> "Bu kaydın zaman damgası, aynı abonenin daha önce görülmüş en yüksek
> zamanından **7200 saniye geriye** gidiyor. Sistem bunu, o kaydın manipüle
> edildiğini **bilmeden** buldu — enjeksiyon etiketi gerçek konum tablosunda ve
> bütünlük servisi o tabloyu göremiyor."

```bash
make verify-k7 RUN_ID=<demo_run_id>
```

| kural | precision | recall | K7 |
|---|---|---|---|
| 1 envanter | 1,0000 | 1,0000 | geçti |
| 2 hız | 0,8548 | 0,0445 | tutmadı |
| 3 zaman | 1,0000 | 0,6491 | geçti |
| 4 yörünge | — | — | kapsam dışı |
| 5 aktivite | 1,0000 | 1,0000 | geçti |

> "Üç kural %100,00 kesinlikte, sıfır yanlış pozitif. Kural 2 kentselde eşiği
> tutmadı: olaylar ortalama 2,4 saat aralıklı, şehrin çapı 10 km — 10 km'lik bir
> atlama 2,4 saatte **fiziksel olarak mümkün**. Kırsalda çap 40 km olduğu için
> aynı kural %92,7 ile geçiyor. Kural 4 ise ölçülemez: silinen kaydın kimliğini
> üretmek matematiksel olarak imkânsız, bunu ADR-30'da gerekçesiyle yazdık."

### 4:15–5:00 · Bilimsel disiplin (kapanış)

```bash
psql -c "SELECT * FROM verify_integrity('<demo_run_id>');"
```

5 denetim: 4 OK + 1 SKIP.

```bash
ls docs/architecture/adr/          # 31 ADR
go test ./... | tail -3            # 28 paket yeşil
```

> "31 mimari karar kaydı, her biri gerekçesi ve **reddedilen alternatifiyle**.
> 16 property-based test değişmezi. Ölçüm eşikleri ölçümden önce beyan edildi ve
> tutmayan iki kriterde eşik değil, açıklama yazıldı. Sprint 6'nın en önemli
> sonucu şu: ADR'lere kod yazılmadan önce yazdığımız tahminler tam ölçekte
> birebir doğrulandı — kural 3'ün recall tavanını %64,8 hesaplamıştık, %64,91
> ölçtük."

---

## 6. Risk ve yedekler

| Risk | Azaltma |
|---|---|
| Canlı koşum demo sırasında hata verir | Koşuyu **önceden** yap, `run_id` elde; canlı koşum yalnızca ilk 45 sn'de "akış artıyor" göstermek için |
| Grafana paneli boş görünür | Datasource'u önceden test et; yedek olarak ekran görüntüsü |
| Leaflet sayfası GeoJSON'u yükleyemez (CORS) | `python3 -m http.server` zorunlu, `file://` çalışmaz — kuru provada doğrula |
| Sağlık portu çakışması (Sprint 6 borcu #4) | Demo öncesi `pgrep` ile artık süreçleri temizle |
| 5 dakika aşılır | Perde 2'yi (kalibrasyon dürüstlüğü) kısalt; Perde 1 ve 3 vazgeçilmez |
| Hoca ham olasılık ısı haritası ister | ADR-10 gereği saklanmıyor; "kontur ve merkez saklanıyor, ızgara kütlesi hacim nedeniyle atılıyor" — karar gerekçeli |

---

## 7. Öneri özeti

1. **Önce veri problemi çözülür** (Bölüm 2): `configs/demo.yaml` + tek
   `run-scenario.sh` koşumu → estimates + findings + metrics aynı `run_id`'de.
   *Sıfır kod.*
2. **Grafana** sistemin canlı olduğunu gösterir (sayaçlar + diurnal profil +
   K1–K3/K7 tabloları). *Datasource YAML + dashboard JSON, sıfır Go kodu.*
3. **Statik Leaflet sayfası** bilimsel vuruşu yapar (üç yöntemin poligonları +
   ground truth + bulgu popup'ı). *~150 satır HTML, Go kodu yok.*
4. **Terminal** disiplini gösterir (`verify-isolation`, `verify-k7`,
   `verify_integrity`, 31 ADR, 28 paket yeşil).
5. **E06/E07 öne çekilmez.** Sprint 7 planlandığı gibi yazılır; demo sayfası o
   zaman atılır.

**Toplam efor: ~yarım gün. Değişen Go kodu: yok.**

---

> **Kod yazılmamıştır.** Bu belge mevcut durumun ölçülmüş envanteri ve demo
> önerisidir; uygulama onay sonrası yapılacaktır.
