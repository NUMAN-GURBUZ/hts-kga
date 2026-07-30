# ADR-31 — Bulgu Veri Modeli, İdempotanslık ve K7 Ölçüm Sözleşmesi

**Durum:** Kabul edildi
**Tarih:** 2026-07-30
**Sprint:** 6 (T-E05-01, T-E04-09)
**İlgili bulgular:** BÖLÜM D, BÖLÜM F (F.5), BÖLÜM J (K7), EK-02, ADR-23, ADR-28

---

## Bağlam

Bütünlük ölçümünün dört ayrı **sessiz bozulma** yolu var. Hiçbiri hata mesajı
üretmez; hepsi K7'yi yanlış bir sayıya götürür.

| # | Bozulma | Nasıl gerçekleşir | Sonuç |
|---|---|---|---|
| 1 | Yinelenen bulgu | Kafka at-least-once; tüketici çöker, parti yeniden işlenir | Precision denominatörü şişer, ölçüm **düşer** |
| 2 | F.5'in iç birleştirmesi | `ground_truth`'ta eşi olmayan bulgu | Yanlış pozitif denominatörden **düşer**, precision yükselir |
| 3 | `+Inf` margin / evidence | `Δt = 0` → sonsuz hız | `json.Marshal` **hata** verir, bulgu hiç yazılmaz |
| 4 | Yarım S4 koşusu | Idle eşiği kısa; akış ortada kesilir | Düşük recall **bilimsel bulgu gibi** görünür |

Bunlara K7'nin kendi tanımındaki bir belirsizlik ekleniyor: `NULLIF(count(*),0)`
sıfır bulguda NULL döndürür ve "precision ≥ %90" NULL üzerinde tanımsızdır.
Ön analizde katı üçlü sınama kural 2 için **tek bulguyla %100 precision**
vermişti — teknik olarak "geçti", bilimsel olarak boş.

---

## Karar

### 1. `integrity_findings` normal tablo kalır — idempotanslığın önkoşulu

EK-02: TimescaleDB hypertable'ında tekillik kısıtı **bölümleme sütununu içermek
zorundadır**. `integrity_findings` hypertable yapılsaydı tekillik
`(run_id, event_id, rule_id, time)` olmak zorunda kalırdı ve
`(run_id, event_id, rule_id)` tekilliği kurulamazdı — yani idempotanslık
imkânsız olurdu.

Bulgular seyrektir (koşu başına ~4.000 satır); zaman bölümlemesinin kazancı
yoktur. Normal tablo olması bilinçli bir karardır, ihmal değil.

### 2. Tekillik kısıtı: `(run_id, event_id, rule_id)`

```sql
CONSTRAINT integrity_findings_uniq UNIQUE (run_id, event_id, rule_id)
```

Anlamı: **bir kural, bir olay için en çok bir isabet üretir.** Yazıcı
`ON CONFLICT DO NOTHING` kullanır; yeniden işleme bir hata değil, beklenen
durumdur (`internal/persist`'in kurulmuş deseni).

Doğruluk kod disiplinine değil **şema kısıtına** bağlanır: uygulama hatası,
çökme veya offset geri sarma precision denominatörünü bozamaz.

### 3. `suppressed_by` ve `detected_in` sütunları

```sql
detected_in   VARCHAR(6) NOT NULL DEFAULT 'stream'
                         CHECK (detected_in IN ('stream','batch'))
suppressed_by INTEGER    NULL CHECK (suppressed_by BETWEEN 1 AND 5)
CHECK (suppressed_by IS NULL OR suppressed_by <> rule_id)
```

`suppressed_by` ADR-28'in uygulanışıdır: bastırma bir **etiket**, filtre değil.
`detected_in` hangi fazın bulduğunu kaydeder — ADR-27'nin sınıf kısıtının
ihlali böylece veriden de görülür.

### 4. `margin` sözleşmesi ve sonluluk

```sql
CONSTRAINT integrity_findings_margin_range CHECK (margin >= 1.0 AND margin <= 1e6)
```

| Kural | `margin` | Örnek |
|---|---|---|
| 1 envanter | sabit `1.0` — eşik yok, ikili karar | 1,0 |
| 2 hız | `v_ölçülen / max_velocity_kmh` | 480/300 = 1,6 |
| 3 zaman | `geri_gidiş_saniye / tick_saniye` | 7200/300 = 24,0 |
| 5 aktivite | abonenin farklı IMEI sayısı | 2,0 |

Alt sınır 1,0: eşik aşılmadan bulgu yazılmaz, dolayısıyla `margin < 1` anlamsızdır.
Kural 1'de eşik olmadığı için 1,0 "kural tetiklendi" demektir.

Üst sınır `1e6` (`velocity_margin_cap`): `Δt = 0` durumunda hız sonsuzdur.
Koruma isteğe bağlı bir incelik değildir — kentsel koşuda >300 km/h isabetlerin
**tamamı** bu popülasyondadır; koruma olmadan kural 2 kentselde sıfır bulgu
üretir. Kapılan değerlerde `evidence.capped = true` işaretlenir.

### 5. `evidence` sürümlü ve kural başına şemalı

```sql
CONSTRAINT integrity_findings_evidence_versioned CHECK (evidence ? 'v')
```

```
kural 1: {"v":1,"rule":1,"cell_id":UUID,"inventory_size":int}
kural 2: {"v":1,"rule":2,"from_cell":UUID,"to_cell":UUID,"distance_m":float,
          "delta_s":float,"velocity_kmh":float,"threshold_kmh":float,
          "attribution":"local_support","peer_event_id":UUID,
          "zero_interval":bool,"capped":bool}
kural 3: {"v":1,"rule":3,"record_time":RFC3339,"watermark":RFC3339,
          "backstep_s":float,"prev_event_id":UUID}
kural 5: {"v":1,"rule":5,"imei":string,"modal_imei":string,
          "imei_count":int,"support":int}
```

**Değişmez — kanıt katmanında kör test:** hiçbir kanıt alanı ground truth
türevi olamaz. Yasaklı anahtarlar: `lat`, `lon`, `true_location`, `agent_id`,
`injected_rule`, `partition_key`, `covered`. Liste teste bağlanır.

"Adli açıklanabilirlik" iddiası sürümsüz, şemasız bir JSONB ile savunulamaz;
`v` alanı şemanın sonradan geliştirilmesini de mümkün kılar.

### 6. F.5 düzeltilir: `LEFT JOIN` + kanonik süzgeç

**Plandaki hâli (hatalı):**
```sql
FROM integrity_findings f
JOIN ground_truth g ON g.run_id=f.run_id AND g.event_id=f.event_id
```
`ground_truth`'ta eşi olmayan bir bulgu iç birleştirmede **denominatörden
düşer** ve precision sessizce yükselir. Yanlış pozitifi ölçümün dışına atan bir
precision formülü, ölçmediği şeyi ölçüyor sanır.

**Düzeltilmiş hâli:**
```sql
SELECT f.rule_id,
       count(*)                                                    AS findings,
       count(*) FILTER (WHERE g.injected_rule = f.rule_id)         AS true_positives,
       count(*) FILTER (WHERE g.injected_rule = f.rule_id)::float
         / NULLIF(count(*),0)                                      AS precision
FROM integrity_findings f
LEFT JOIN ground_truth g
       ON g.run_id = f.run_id AND g.event_id = f.event_id
WHERE f.run_id = :run
  AND f.suppressed_by IS NULL          -- ADR-28: kanonik bulgular
GROUP BY f.rule_id;
```

`g.event_id IS NULL` durumu artık **yanlış pozitif** sayılır (filtre onu
saymaz, ama denominatörde durur).

Recall sorgusu da kanonik süzgeç alır:
```sql
LEFT JOIN integrity_findings f
       ON f.run_id = g.run_id AND f.event_id = g.event_id
      AND f.rule_id = g.injected_rule AND f.suppressed_by IS NULL
```

**Bölüm anahtarı (C/V) süzgeci uygulanmaz.** Bütünlük tespitinde kalibre edilen
bir şey yoktur; K4 enforcer'ının tip zorunluluğu bu hatta taşınmaz. Taşınsaydı
recall denominatörü %20'ye inerdi.

### 7. Tamlık sayacı: `inspected_records`

```sql
ALTER TABLE run_config ADD COLUMN inspected_records BIGINT;
```

Akış fazı, incelediği kayıt sayısını koşu bitiminde yazar.
`verify_integrity` beşinci denetim:

```
inspected_records IS NULL              → SKIP   (S4 bu koşuda koşmadı)
inspected_records = published_events   → OK
aksi hâlde                             → FAIL
```

ADR-23'ün simetrik tamamlanması. Yarım koşan bir S4'ün düşük recall'u artık
bilimsel bulgu gibi görünemez: denetim FAIL verir. Sprint 5 hatası #4 (analiz
tüketicisi akışın ortasında boşta sanıp çıkıyordu, senaryo D'de 3.000 yerine
225 olay analiz etti) bu mekanizma olmadan S4'te birebir tekrar edebilirdi.

`SKIP` semantiği korunur: Sprint 5'in dört koşusu S4 koşmadan yapıldı, denetim
onlarda yanlış alarm üretmemeli.

### 8. `integrity_metrics` tablosu

`metrics` tablosunun anahtarı `(scenario, method, confidence, partition_key)`
— kural bazlı satırları taşıyamaz. Ayrı tablo:

```sql
CREATE TABLE integrity_metrics (
    metric_id      BIGSERIAL   PRIMARY KEY,
    run_id         UUID        NOT NULL,
    computed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    scenario       CHAR(1)     NOT NULL,
    rule_id        INTEGER     NOT NULL CHECK (rule_id BETWEEN 1 AND 5),
    rule_name      VARCHAR(50) NOT NULL,
    findings       BIGINT      NOT NULL,
    true_positives BIGINT      NOT NULL,
    injected       BIGINT      NOT NULL,
    precision      FLOAT,                 -- NULL = ölçülemedi
    recall         FLOAT       NOT NULL,
    precision_lo   FLOAT,                 -- Wilson %95 alt
    precision_hi   FLOAT,                 -- Wilson %95 üst
    sufficient     BOOLEAN     NOT NULL,  -- findings >= 30
    UNIQUE (run_id, rule_id)
);
```

`UNIQUE (run_id, rule_id)` yeniden hesaplamayı idempotent kılar
(`ON CONFLICT ... DO UPDATE`).

### 9. K7 ölçülebilirlik kuralı — ölçümden önce beyan

| Kanonik bulgu sayısı | Karar |
|---|---|
| 0 | **"Ölçülemedi"** — ne geçti ne kaldı; `precision = NULL`, `sufficient = false` |
| 1 – 29 | Precision **Wilson %95 güven aralığıyla** raporlanır, `sufficient = false`, "istatistiksel olarak yetersiz" etiketlenir |
| ≥ 30 | K7 eşiği (%90) uygulanır, `sufficient = true` |

Wilson aralığı seçimi: küçük örneklem ve uç oranlarda (p ≈ 1) normal yaklaşım
çöker; Wilson bu iki durumda da davranışlıdır ve tek taraflı yorumlanabilir.

Eşik 30: küçük örneklem sınırının yerleşik pratiği. Ön analizde katı üçlü sınama
1–7 bulgu üretiyordu; bu kural o tasarımın "%100 precision" beyanını
engelleyecek biçimde **ölçümden önce** konulmuştur.

---

## Sonuçlar

Dört sessiz bozulma yolu kapanıyor:

| # | Kapatan karar | Nasıl |
|---|---|---|
| 1 Yinelenen bulgu | Karar 1 + 2 | Şema kısıtı; koda güvenilmiyor |
| 2 F.5 iç birleştirme | Karar 6 | `LEFT JOIN`; yanlış pozitif denominatörde kalıyor |
| 3 `+Inf` | Karar 4 | Üst sınır + `capped` işareti + PBT |
| 4 Yarım koşu | Karar 7 | `verify_integrity` FAIL |

Ayrıca K7'nin tanımsız durumları (0 ve düşük bulgu) ölçümden önce karara
bağlanıyor (karar 9) ve kanıt katmanında kör test bir teste dönüşüyor
(karar 5).

**Migration 006** bu kararların tamamını tek dosyada uygular; adımların hepsi
yeniden koşulabilirdir (`make migrate-up` tüm dosyaları her seferinde uyguluyor).

---

## Reddedilen alternatifler

**(A) `integrity_findings`'i hypertable yapmak.** EK-02 nedeniyle idempotanslık
kısıtı kurulamaz. Reddedildi.

**(B) Tekilliği `(run_id, event_id)` yapmak** (olay başına tek bulgu).
Reddedildi: ADR-28 bastırılmış isabetleri de yazıyor; aynı olay için birden çok
kural satırı olabilir (biri kanonik, diğerleri `suppressed_by` etiketli).

**(C) `margin`'i `NUMERIC` yapmak** (`Infinity` desteklemez, hata verir).
Reddedildi: hata koşuyu durdurur ve bulgu kaybolur; kapılan değer + `capped`
işareti bilgiyi korur.

**(D) Yinelenenleri sonradan `DELETE` ile temizlemek.** Reddedildi:
`svc_integrity`'nin DELETE yetkisi yoktur ve verilmemelidir — bulgu tablosu
ekle-yalnız bir adli kayıttır.

**(E) K7 için eşiği "en az bir kuralda ≥ %90" olarak yorumlamak.** Reddedildi:
plan "kural bazında" diyor; yorumu gevşetmek ölçümden sonra tanım değiştirmek
olurdu.

---

## İlgili

BÖLÜM D (veri modeli) · BÖLÜM F (F.5) · BÖLÜM J (K7) · EK-02 (hypertable
tekilliği) · ADR-01 (bütünlük denetimi) · ADR-23 (koşu sayaçları) ·
ADR-27 (fazlar) · ADR-28 (`suppressed_by`) · ADR-29 (`margin` üst sınırı) ·
ADR-30 (kural 4 → `precision = NULL`)
