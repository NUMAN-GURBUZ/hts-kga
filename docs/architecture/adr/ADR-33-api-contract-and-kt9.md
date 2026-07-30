# ADR-33 — API Sözleşmesi, k=5 Kapsamı ve KT9'un Tanımı

**Durum:** Kabul edildi
**Tarih:** 2026-07-30
**Sprint:** 7 (E06 + E07 + E08 kalanı)
**İlgili bulgular:** ADR-12, ADR-13, ADR-15, BÖLÜM D, BÖLÜM H (S7 → KT9)

---

## Bağlam

Sprint 7 planı üç boşluk taşıyor. Üçü de geliştirme başlamadan karara
bağlanmalı, çünkü ikisi kod yazmayı bloke ediyor.

### Boşluk 1 — KT9 tanımsız

BÖLÜM H, S7'nin çıktısını `KT9` olarak veriyor. Plan içinde **KT9'un ne olduğu
hiçbir yerde yazmıyor**; KT2, KT3, KT4, KT5, KT6 da aynı durumda — yalnızca
sprint tablosunda etiket olarak geçiyorlar. Formel kabul kriterleri
BÖLÜM J'deki **K1–K10**'dur ve orada bir K-numarası S7'ye atanmamıştır.

Yani S7'nin ölçülebilir bir kabul ölçütü yok.

### Boşluk 2 — `svc_gateway` rolü yok

BÖLÜM D, `audit_log.principal` sütununu şöyle belgeliyor:

```sql
principal VARCHAR(40) NOT NULL,   -- 'analysis-engine' / 'validation' / 'integrity' / 'gateway'
```

Ama migration 004 dört rol oluşturuyor: `svc_analysis`, `svc_integrity`,
`svc_validation`, `svc_gt_persister`. **`svc_gateway` yok.** Gateway'in hangi
tablolara erişebileceği tanımlı değil.

### Boşluk 3 — k=5 hangi uçlarda

ADR-15 ve BÖLÜM C.1 "k=5, **yalnızca aggregate uçlar**" diyor ama hangi uçların
aggregate sayıldığını saymıyor. `metrics` tablosu zaten koşu düzeyinde
toplulaştırılmış (satır başına `n_events` binlerce); orada k=5 uygulamak
işlemsiz bir denetim olurdu.

---

## Karar

### 1. KT9 = ADR-13'ün ölçülebilir kuralları

KT9, S7'nin kabul testidir ve **yeni bir eşik icat etmez** — ADR-13'ün zaten
karara bağladığı kısıtları ölçülebilir hâle getirir:

| # | Ölçüt | Beklenen |
|---|---|---|
| **KT9.1** | Zorunlu filtre yokluğu | `run_id` verilmeyen istek → **400** |
| **KT9.2** | Geometri üst sınırı | Tek istekte > 500 geometri → **400 + açıklama** |
| **KT9.3** | GeoJSON geçerliliği | Her geometri ucu geçerli `FeatureCollection` döndürür |
| **KT9.4** | Viewport filtresi | `bbox` verildiğinde `ST_Intersects` uygulanır, sonuç küçülür |
| **KT9.5** | k-anonimlik | Aggregate uçta `n < 5` olan grup **döndürülmez** |
| **KT9.6** | Denetim izi | Her API isteği `audit_log`'a bir satır yazar (ADR-15) |
| **KT9.7** | Kör test korunur | Gateway `ground_truth`'u **yalnızca** `estimates` ile birlikte ve yalnızca doğrulama amaçlı okur; `injected_rule` hiçbir uçta dışarı verilmez |

KT9.7 vazgeçilmezdir: gateway `svc_validation`'a yakın bir okuma yüzeyi olur ve
dikkatsiz bir uç, altı sprint boyunca korunan kör testi tek satırda delebilir.

### 2. `svc_gateway` rolü — salt okuma, ground truth **hariç**

Migration 007:

```sql
GRANT SELECT ON cells, hts_records, estimates, metrics,
                integrity_findings, integrity_metrics, run_config TO svc_gateway;
GRANT INSERT ON audit_log TO svc_gateway;
-- ground_truth: erişim YOK
```

**Gateway `ground_truth`'u göremez.** Bu, planın "gateway görselleştirme için
gerçek konumu göstermeli mi" sorusuna verilen cevaptır: **göstermez.**

Gerekçe: gerçek konum, modelin doğruluğunu **ölçmek** için vardır ve o ölçüm
`metrics` tablosunda toplulaştırılmış hâlde zaten yayınlanıyor. Tek tek gerçek
konumları API'den servis etmek, sistemin dışarıya "bu abone tam olarak
buradaydı" demesi olurdu — ki bu, çalışmanın modellediği HTS verisinde **var
olmayan** bir bilgidir.

> **Demo sonucu:** demo haritasında ground truth katmanı göstermek isteniyorsa
> `psql` ile dışa aktarılır (bkz. `docs/planning/demo-plan.md`), API'den değil.
> Bu, kör testin gateway'e kadar uzatılmasıdır.

### 3. Altı uç (ADR-12'nin "6 uç"u sabitlenir)

| # | Uç | Döndürür | Filtre |
|---|---|---|---|
| 1 | `GET /api/v1/runs` | Koşu listesi (`run_config` + sayaçlar) | — |
| 2 | `GET /api/v1/cells` | Baz istasyonları, GeoJSON | `run_id` (zorunlu), `bbox` |
| 3 | `GET /api/v1/estimates` | Olasılık geometrileri, GeoJSON | `run_id`+`agent`+zaman (zorunlu), `method`, `confidence`, `bbox` |
| 4 | `GET /api/v1/findings` | Bütünlük bulguları, GeoJSON | `run_id` (zorunlu), `rule_id`, zaman, `bbox` |
| 5 | `GET /api/v1/metrics` | K1–K3 ölçümleri (aggregate) | `run_id` (zorunlu) |
| 6 | `GET /api/v1/integrity-metrics` | K7 ölçümleri (aggregate) | `run_id` (zorunlu) |

Artı `/health`, `/ready` (mevcut) ve `/static/*` (ADR-12/3).

**Abone kimliği filtresi `agent` adını taşır ama değeri `pseudo_msisdn`'dir.**
ADR-13 "agent_id" diyor; veritabanında olay-kayıt eşlemesi takma ad üzerinden
kurulu ve `agent_id` yalnızca `ground_truth`'ta. Gateway o tabloyu göremediği
için (karar 2) filtre takma addan yürür — bu, ADR-13'ün amacını (tek abone,
tek gün) birebir karşılar.

### 4. gRPC iç, REST dış — ikisi de elle

ADR-12/1: `grpc-gateway` kullanılmaz. Karar korunur ve şöyle uygulanır:

- `proto/hts/v1/*.proto` sözleşmeyi tanımlar; `protoc` ile Go tipleri ve gRPC
  sunucu iskeleti üretilir.
- gRPC sunucusu iç tüketiciler içindir (ileride başka servisler).
- REST handler'ları **elle** yazılır ve **aynı** servis katmanını çağırır.
  İkisi arasında kod üretimi yoktur.

**Geometri alanı `string`'dir** (ADR-12/2): GeoJSON metni veritabanında
`ST_AsGeoJSON` ile üretilir, uygulama katmanında geometri dönüşüm kodu
yazılmaz.

### 5. k=5 yalnızca **abone bazlı toplulaştırmada**

k-anonimlik, bir grubun 5'ten az bireyi temsil ettiği yerde anlamlıdır.
Uygulama noktası:

| Uç | k=5 uygulanır mı | Gerekçe |
|---|---|---|
| `/metrics`, `/integrity-metrics` | **Hayır** | Zaten koşu düzeyinde; `n_events` binlerce. İşlemsiz denetim olurdu |
| `/cells` | Hayır | Şebeke altyapısı, kişi verisi değil |
| `/estimates`, `/findings` | **Hayır — ama zorunlu abone filtresi var** | Tek abonenin kendi kayıtları; toplulaştırma yok |
| **Yeni: `/api/v1/aggregate/cell-activity`** | **Evet** | Hücre başına **kaç farklı abone** görüldüğü. `count(DISTINCT pseudo_msisdn) < 5` olan hücreler **döndürülmez** |

Yedinci uç bilinçli olarak eklenmiştir: ADR-15 "k=5 yalnızca aggregate uçlarda"
diyor ama sistemde k=5'in anlamlı olduğu **hiçbir uç yoktu**. Kuralı
uygulanabilir kılacak en küçük uç budur ve görselleştirmede gerçek işi vardır
(hücre yoğunluk katmanı).

### 6. Sağlık portu `:8086`

Gateway iskeleti `:8085` kullanıyordu; Sprint 6'da `cmd/integrity` de `:8085`
aldı. Çakışma (Sprint 6 borcu #4) burada kapanır:

| Servis | Sağlık portu |
|---|---|
| persister (records) | 8083 |
| persister (groundtruth) | 8084 |
| integrity | 8085 |
| **gateway** | **8086** |

Hepsi `HTS_HEALTH_ADDR` ile geçersiz kılınabilir.

Servis portları da çakışmayacak biçimde seçilir:

| Yüzey | Port | Not |
|---|---|---|
| REST + `/static` | **8080** | dış istemciler |
| gRPC | **50051** | iç tüketiciler. `:9090` **kullanılamaz** — Prometheus orada (compose) |

---

## Sonuçlar

**S7 ölçülebilir hâle gelir.** KT9'un yedi maddesi entegrasyon testine bağlanır.

**Kör test gateway'e kadar uzar.** Yeni bir okuma yüzeyi açılırken `ground_truth`
dışarıda tutulur; K6'nın altı sprintlik yatırımı korunur.

**Yeni bilimsel iddia yoktur.** Bu ADR ölçüm, model veya eşik değiştirmez;
yalnızca bir sunum katmanı ve onun sınırlarını tanımlar.

---

## Reddedilen alternatifler

**(A) KT9'u "API çalışıyor" diye yorumlamak.** Reddedildi: ölçülemez. ADR-13
zaten sayısal kısıtlar koymuş; kabul testi onları ölçmelidir.

**(B) Gateway'e `ground_truth` okuma yetkisi vermek** (haritada gerçek konum
göstermek için). Reddedildi: karar 2'deki gerekçe. Demo ihtiyacı `psql` dışa
aktarımıyla karşılanır ve o, API'nin sözleşmesi değildir.

**(C) `grpc-gateway` ile REST'i üretmek.** Reddedildi: ADR-12/1 bunu zaten
gerekçesiyle reddetti; kararı Sprint 7'de geri almak için yeni bir bilgi yok.

**(D) k=5'i `/metrics` uçlarına da uygulamak.** Reddedildi: koşu düzeyi
toplulaştırmada `n_events` binlerce; denetim hiçbir zaman tetiklenmez ve
"k-anonimlik uygulanıyor" iddiası boşa çıkar.

---

## İlgili

ADR-12 (grpc-gateway yok, GeoJSON DB'de) · ADR-13 (zorunlu filtre, 500 sınırı,
bbox) · ADR-15 (servis kimliği, `audit_log`, k=5) · ADR-32 (servis kimliği
deseni) · BÖLÜM D (rol yetkilendirmesi) · BÖLÜM H (S7 → KT9)
