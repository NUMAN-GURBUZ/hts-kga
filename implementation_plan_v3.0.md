# HTS-KGA — Konum Güven Alanı Analiz Platformu
## Uygulama Planı v3.0 — **BAŞLAMAYA HAZIR**
### Eksiklik analizi kapatıldı · Tüm belirsiz kararlar ADR'ye bağlandı

---

> **Durum:** v2.1'de tespit edilen 5 kritik, 8 yüksek, 6 orta, 4 kapsam ve 14 minör bulgunun
> tamamı karara bağlandı. Belirsiz bırakılan hiçbir algoritma veya mimari karar kalmadı.
> **Sprint 0 / T-E01-01 ile başlanabilir.**

---

## BÖLÜM A — BULGU DOĞRULAMA VE KARAR ÖZETİ

### A.1 Doğrulama sonucu

Eksiklik analizindeki 37 bulgunun tamamı incelendi. **33'ü geçerli ve kabul edildi.**
4 bulguda teknik düzeltme veya daha basit çözüm uygulandı:

| Bulgu | Değerlendirme |
|---|---|
| **K-01** | Geçerli, ancak teknik düzeltme: TimescaleDB'de FK **kısmen** desteklenir — hypertable'dan normal tabloya FK kurulabilir, *hypertable'a referans veren* FK kurulamaz. Ayrıca hypertable üzerinde UNIQUE constraint **bölümleme sütununu içermek zorundadır** (`time`). Bu, analizin atladığı kritik ayrıntıdır. Çözüm buna göre tasarlandı (ADR-01). |
| **K-04** | Geçerli — gerçek bir yarış durumu. Ancak önerilen çözüm (`estimates.ready` topic'i) gereğinden karmaşık. Doğrulama doğası gereği **post-hoc bir toplu iştir**; S3 iki role ayrılınca yarış tamamen ortadan kalkar (ADR-04). |
| **O-03** | Geçerli, ancak hesap düzeltmesi: olay başına 9 değil **5** geometri üretilir (B0×1 + B1×1 + M×3). 300K olay → ~1,5M geometri. Sonuç değişmiyor: Leaflet bu hacmi kaldırmaz (ADR-13). |
| **D-02** | Kısmen geçerli. Okumura-Hata/COST-231 tamamen çıkarılmıyor; **saf birim testine** indirgeniyor (3 SP → 1 SP). Akademik savunulabilirliği ucuza güçlendirir (ADR-06). |

### A.2 Analizin atladığı, ek olarak giderilen konular

| # | Konu | Çözüm |
|---|---|---|
| **EK-01** | Koşu kimliği yok — aynı DB'de tekrar eden koşular birbirine karışır, K10 (tekrarlanabilirlik) ölçülemez | Tüm tablolara `run_id UUID` eklendi (ADR-05) |
| **EK-02** | Hypertable'da UNIQUE constraint bölümleme sütununu içermek zorunda | `UNIQUE (run_id, event_id, method, confidence, time)` (ADR-01) |
| **EK-03** | 300K olay × ızgara hesabı — analiz maliyeti planlanmamış | Örnekleme stratejisi tanımlandı (ADR-14) |
| **EK-04** | Enjekte edilen kayıtların etiketi nerede tutulacak | `ground_truth`'ta tutulur → S4 göremez → bütünlük tespiti de **kör test** olur (ADR-09) |

---

## BÖLÜM B — MİMARİ KARAR KAYITLARI (ADR)

> Her ADR: karar + gerekçe + reddedilen alternatif. Sprint 0'da bu kararlar
> `docs/architecture/adr/` altına ayrı dosyalar olarak da kopyalanır.

### ADR-01 — Referansiyel bütünlük (K-01, EK-02)

**Karar.** TimescaleDB kısıtları nedeniyle klasik FK zinciri kurulmaz. Yerine üç katmanlı koruma:

1. **Hypertable UNIQUE constraint'leri** (bölümleme sütunu dâhil — zorunlu):
   - `hts_records`: `UNIQUE (run_id, event_id, time)`
   - `ground_truth`: `UNIQUE (run_id, event_id, time)`
   - `estimates`: `UNIQUE (run_id, event_id, method, confidence, time)`
   > `confidence` NULL olabildiğinden, B0/B1 için sentinel değer `-1` kullanılır (NULL'lar UNIQUE'te çakışmaz).
2. **Deterministik `event_id`** — rastgele UUID yerine:
   `event_id = UUIDv5(namespace, run_id ‖ agent_id ‖ tick_index ‖ event_seq)`
   Aynı seed → aynı `event_id`. Simülatör bir olayı iki kez yayınlarsa UNIQUE ihlali oluşur, sessiz bozulma olmaz.
3. **Bütünlük denetim sorgusu** — her koşu sonunda `make verify-integrity`:
   ```sql
   -- Eşleşmeyen kayıt olmamalı (0 dönmeli)
   SELECT count(*) FROM hts_records h
   LEFT JOIN ground_truth g USING (run_id, event_id)
   WHERE g.event_id IS NULL AND h.run_id = :run;
   ```

**Reddedilen alternatif.** Normal (non-hypertable) tablolara geçmek — zaman bölümleme kazancı kaybolurdu.

---

### ADR-02 — Kalibrasyon hedef fonksiyonu ve algoritması (K-02)

**Karar.** Tek skaler parametre üzerinde sınırlı iterasyonlu **tek boyutlu kök bulma**.

- **Kalibre edilen parametre:** `λ` — belirsizlik ölçek katsayısı, `λ ∈ [0.5, 3.0]`.
  `λ`, radyal bileşende ve komşu hücre teriminde kullanılan gölgeleme standart sapmasını ölçekler:
  `σ_eff = λ · σ_nominal`. **Tek parametre** — çok boyutlu arama yapılmaz.
- **Hedef:** `coverage@90%(λ) = 0.90` denklemini çöz.
- **Neden kök bulma, optimizasyon değil:** `coverage(λ)` λ'da **monoton artandır** (daha büyük belirsizlik → daha geniş alan → daha yüksek kapsama). Monoton tek değişkenli fonksiyonda ikili arama (bisection) garantili ve hızlı yakınsar. Alan minimizasyonu ayrıca yapılmaz; monotonluk nedeniyle kapsamayı hedefte tutan λ, o kapsamayı sağlayan **en küçük** alanı zaten verir.
- **Algoritma:**
  ```
  lo, hi = 0.5, 3.0
  for i in 1..MAX_ITER:                    # MAX_ITER = 12 (sabit üst sınır)
      mid = (lo + hi) / 2
      c   = coverage90(mid, calibration_sample)
      if |c - 0.90| < TOL:  return mid     # TOL = 0.005
      if c < 0.90: lo = mid else: hi = mid
  return (lo + hi) / 2                     # yakınsamazsa son değer + uyarı
  ```
- **İterasyon maliyeti sınırlı:** her iterasyon tam veri üzerinde değil,
  `calibration_sample` (varsayılan **5.000 olay**, `partition_key='C'` içinden seed'li örnek) üzerinde koşar.
- **Yakınsama garantisi:** 12 iterasyon × 5.000 olay = sabit, önceden kestirilebilir maliyet.
  Sprint 5 kontrolsüz uzayamaz.
- **Senaryo başına ayrı λ:** A/B/C/D için ayrı kalibre edilir ve `run_config` tablosuna yazılır.

**Reddedilen alternatif.** Çok parametreli grid search / Bayesian optimizasyon — tek geliştirici ve sınırlı sürede gereksiz karmaşıklık; monoton tek parametre bunu gereksiz kılıyor.

---

### ADR-03 — Komşu hücre kısıtı: olasılıksal türetme (K-03)

**Karar.** Keyfi "ceza" yerine, kaydın kendisinden gelen bilgiden **olasılık türetilir**.

**Mantık.** Kayıt, `s` hücresinin *best server* olduğunu söyler. Öyleyse `p` noktasında
`s`'nin alınan gücü, tüm komşuların gücünden büyük olmalıydı. Gölgeleme rastgele olduğundan
bu bir olasılıktır — ve tam olarak hesaplanabilir.

```
Her ızgara hücresi p için:
  P_s(p)   = EIRP_s − PL_s(d(p,s)) − A_beam,s(θ_s(p))          # serving, gölgelemesiz
  P_n(p)   = EIRP_n − PL_n(d(p,n)) − A_beam,n(θ_n(p))          # her komşu n için
  Δ(p)     = P_s(p) − max_n P_n(p)                              # dB cinsinden marj

  # İki bağımsız log-normal gölgeleme farkının std'si = σ_eff·√2
  w_nbr(p) = Φ( Δ(p) / (σ_eff · √2) )        # Φ = standart normal CDF
```

- **Komşu kümesi N:** `p` noktasını kapsama yarıçapı içine alan tüm hücreler
  (PostGIS/Redis üzerinden mekânsal önseçim; tipik |N| = 3–8).
- **Uygulama noktası:** normalizasyondan **önce**, açısal ve radyal bileşenlerle çarpılır:
  `mass_raw(p) = w_ang(p) · w_rad(p) · w_nbr(p)`
- **Komşu sinyali simüle edilmez** — analiz motoru envanterdeki deterministik parametrelerden
  hesaplar; gölgelemenin gerçekleşmiş değerini bilmez (bilgi asimetrisi korunur).
- **σ_eff = λ · σ_nominal** → ADR-02'deki kalibrasyon bu terimi de ölçekler.

**Neden bu doğru:** Δ(p) büyükse (serving çok baskın) `w_nbr → 1`; Δ(p) negatifse
(komşu daha güçlü görünüyor) `w_nbr → 0` — yani "burada olsaydı başka hücreye bağlanırdı"
bölgeleri doğal olarak elenir. Formül keyfi değil, best-server semantiğinden türetilmiştir.

**Reddedilen alternatif.** Mesafeye bağlı sabit ceza katsayısı — fiziksel dayanağı yok, savunulamaz.

---

### ADR-04 — Doğrulamanın toplu iş (batch) olması (K-04)

**Karar.** S3 iki ayrık role bölünür; senkronizasyon problemi ortadan kalkar.

| Rol | Tetikleyici | İş |
|---|---|---|
| **S3a — Ground truth persister** | Kafka consumer (sürekli) | `hts.groundtruth` → `ground_truth` tablosuna yazar. Başka hiçbir şey yapmaz. |
| **S3b — Validation batch job** | Koşu tamamlandıktan sonra elle/`make validate` | `estimates` ⋈ `ground_truth` join; metrikleri hesaplar, `metrics`'e yazar. |

**Neden yarış ortadan kalkıyor:** S3b, simülasyon **ve** analiz tamamlandıktan sonra çalışır.
Koşu tamamlanma koşulu: `hts_records` sayısı = `estimates` sayısı / beklenen_yöntem_sayısı,
ve Kafka consumer lag = 0. Bu koşul `make validate` içinde önkoşul olarak denetlenir.

**Reddedilen alternatif.** `estimates.ready` Kafka topic'i + event-driven bekleme — doğrulama
zaten gerçek zamanlı olmak zorunda değil; ek topic, ek ACL, ek karmaşıklık getirirdi.

---

### ADR-05 — Koşu kimliği (`run_id`) (EK-01, O-06)

**Karar.** Tüm veri tabloları `run_id UUID NOT NULL` taşır. Tek veritabanı, çoklu koşu.

- Bir koşu = bir senaryo × bir seed × bir kod sürümü.
- `run_config` tablosu koşunun tüm parametrelerini (seed, senaryo, λ, kod git SHA) saklar.
- **K10 (tekrarlanabilirlik)** ancak bu sayede ölçülebilir: aynı seed ile iki koşu → iki `run_id`
  → çıktıların bit düzeyinde karşılaştırılması.
- Sorgu indeksleri `run_id` ile başlar.

**Reddedilen alternatif.** Senaryo başına ayrı veritabanı/stack — karşılaştırmalı sorgular
(A vs B, M vs B1) tek SQL'de yapılamaz hâle gelirdi.

---

### ADR-06 — Klasik model çapraz doğrulamasının anlamı (Y-06, D-02)

**Karar.** Çapraz doğrulama = **birim test**, çalışma zamanı davranışı değil.

- 3GPP TR 38.901 tek ve yegâne çalışma zamanı modelidir. Fallback yoktur, ortalama alınmaz.
- `TestCrossValidation` birim testi: sabit bir parametre kümesi için
  (h_BS=25 m, h_UT=1.5 m, kentsel, d ∈ {100, 500, 1000, 2000} m)
  3GPP ile Okumura-Hata (900 MHz) ve COST-231 (1800 MHz) yol kaybı değerlerini hesaplar,
  farkı **≤ 10 dB** olarak doğrular ve karşılaştırma tablosunu `docs/scientific/` altına yazar.
- Efor: 3 SP → **1 SP**.
- **Akademik değeri:** "Neden bu iki model?" sorusuna cevap — bağımsız, literatürde yerleşik
  bir referansla 3GPP implementasyonumuzun makullüğünü kanıtlıyoruz; modeli kullanmıyoruz, sınıyoruz.

**Reddedilen alternatifler.** (B) İki modelin ortalamasını almak — fiziksel anlamı yok.
(C) 3GPP "yanlış görünürse" fallback — "yanlış görünmek" tanımlanamaz.

---

### ADR-07 — Hex ızgara yönelimi ve koordinat dönüşümü (Y-01, E-01)

**Karar.** **Pointy-top axial** hex; tüm hex matematiği **yerel ENU düzleminde metre** cinsinden;
WGS84'e dönüşüm yalnızca son adımda.

```
1) Origin: senaryo config'inden (origin_lat, origin_lon).
2) WGS84 → yerel ENU (metre), eşdikdörtgensel yerel yaklaşım:
     x = (lon − lon0) · cos(lat0) · 111_320
     y = (lat − lat0) · 110_540
   Ters dönüşüm simetriktir. Origin'den 10 km yarıçapta hata < 1 m
   → 100 m ızgara çözünürlüğünde ihmal edilebilir.

3) Pointy-top axial hex, merkez-merkez aralığı = resolution (100 m):
     R    = resolution / √3          # merkez→köşe ≈ 57.735 m
     x    = R · √3 · (q + r/2)
     y    = R · (3/2) · r
     Hücre alanı = (3√3/2)·R² ≈ 8.660 m²
   Ters dönüşüm: standart axial round (cube-round) algoritması.

4) Kontur poligonu ENU'da üretilir → WGS84'e çevrilir → GEOGRAPHY olarak saklanır.
5) ALAN, kütleden değil, ST_Area(GEOGRAPHY) ile gerçek yüzeyden hesaplanır.
```

**Neden ENU:** Tüm hex komşuluk ve mesafe hesapları düzlemsel metrede yapılır; enlem-boylam
ölçek farkı (boylam derecesi 40°N'de ~85 km, enlem derecesi ~111 km) dönüşüm katmanında
bir kez ele alınır, ızgara matematiğine sızmaz.

**Reddedilen alternatif.** Doğrudan lat/lon üzerinde hex üretmek — hücreler enleme göre
deforme olur, alan ve komşuluk bozulur.

---

### ADR-08 — Kapsama alanı, ajan yerleşimi ve kapsama dışı durum (Y-02, Y-03)

**Karar.**

1. **Bounding box config'e eklendi:** `origin_lat`, `origin_lon`, `area_radius_km`.
   Kentsel: 5 km yarıçap (~78 km², 100–150 site @ ~500 m ISD).
   Kırsal: 20 km yarıçap (~1.256 km², 100–150 site @ ~2.000 m ISD).
2. **Ajan ev/iş yerleşimi — reddetme örneklemesi (rejection sampling):**
   Aday nokta üretilir; o noktada `max_cell RxPower ≥ RxSensitivity` değilse reddedilir,
   yeniden üretilir. Böylece ajan **daima kapsama içinde** başlar. Üst sınır 100 deneme;
   aşılırsa senaryo config'i hatalı sayılır ve koşu başlatılmaz (fail-fast).
3. **Kapsama dışı tick yönetimi:**
   ```
   best_server(p) → (cell, rxPower)
   if rxPower < RxSensitivity:
       coverage = false
       → OLAY ÜRETİLMEZ (telefon şebekeye bağlanamaz — gerçekçi davranış)
       → ground_truth'a yalnızca konum + covered=false yazılır (istatistik için)
       → ta_value hesaplanmaz
   ```
4. **Kalite göstergesi:** koşu sonunda `no_coverage_tick_ratio` raporlanır.
   %5'i aşarsa senaryo config'i uyarı verir (site yoğunluğu veya yarıçap hatalı).

---

### ADR-09 — Manipülasyon enjeksiyon mekanizması (Y-04, D-04)

**Karar.** **Seçenek A — simülatör içi, üretim sonrası enjeksiyon aşaması.**

```
Simülatör boru hattı:
  ajan hareketi → best-server → olay üretimi
        → [ENJEKSİYON AŞAMASI]  ← yeni, config ile kontrollü
        → Kafka publish
```

- Config: `integrity.injection_rate` (varsayılan **0.02**) ve kural bazlı ağırlıklar.
- Enjekte edilen her olay, **`ground_truth` tablosuna** `injected_rule INTEGER NULL` alanıyla etiketlenir.
- **Kritik tasarım kazancı:** `ground_truth` S4'e ACL + rol ile kapalı olduğundan,
  **bütünlük tespiti de kör testtir.** S4 hangi kaydın enjekte edildiğini bilemez.
  Precision/recall'ü S3b (validation batch) hesaplar — çünkü etiketi yalnızca o görebilir.
- `integrity_findings.confidence FLOAT` **kaldırıldı**; yerine:
  - `margin FLOAT` — eşiğin ne kadar aşıldığı (örn. ölçülen hız / 300 km/h)
  - `evidence JSONB` — kuralın tetiklendiği somut değerler (adli açıklanabilirlik)

**Reddedilen alternatifler.** (B) Kafka middleware — ek bileşen, Kafka semantiğini kirletir.
(C) DB'ye doğrudan bozuk kayıt — Kafka akışını atlar, S4 gerçek yolu test etmemiş olur.

---

### ADR-10 — Kütle ağırlıklı merkez (Y-08)

**Karar.** Merkez, **tahmin üretim anında** hesaplanır ve `estimates` tablosunda saklanır.

```
centroid = Σ(mass_i · center_i) / Σ(mass_i)        # ENU'da, sonra WGS84'e
```
- `estimates.centroid GEOGRAPHY(POINT, 4326) NOT NULL` alanı eklendi.
- B0 için hücre merkezi, B1 için dilimin geometrik ağırlık merkezi kullanılır (karşılaştırılabilirlik).
- Izgara hücre kütleleri **saklanmaz** (hacim nedeniyle); merkez ve konturlar üretim anında
  türetilip saklandığı için kütleye sonradan ihtiyaç kalmaz.

---

### ADR-11 — PBT kütüphanesi (Y-07)

**Karar.** `pgregory.net/rapid` kullanılır. `gopter` reddedildi.

Gerekçe: `rapid` aktif bakımda, Go generics uyumlu, otomatik shrinking daha iyi,
API daha yalın. `gopter` 2021'den beri bakımsız ve generics ile sorunlu.
`go.mod` bağımlılığı Sprint 0'da eklenir (E-09 ile birlikte).

---

### ADR-12 — API katmanı ve GeoJSON üretimi (Y-05, D-03)

**Karar.**

1. **`grpc-gateway` kullanılmaz.** S5 içinde elle yazılmış REST handler'lar.
   Gerekçe: 6 uç için kod üretim zinciri (protoc eklentileri, buf yapılandırması) kurmak,
   sağladığı faydadan pahalı. Elle handler yazımı ~1 SP, gateway kurulumu ~3 SP + bakım.
2. **GeoJSON, veritabanında üretilir:** `ST_AsGeoJSON(geometry)::json`.
   Proto tarafında alan tipi `string` (GeoJSON metni). Uygulama katmanında geometri
   dönüşüm kodu yazılmaz.
3. **`cmd/visualization/` kaldırıldı.** `web/` altındaki statik dosyalar doğrudan
   **S5 API Gateway** tarafından servis edilir (`/static` yolu). CORS problemi de ortadan kalkar.
   Servis sayısı 6 → **5**.

---

### ADR-13 — Görselleştirme veri hacmi (O-03)

**Karar.** Görselleştirme **daima filtrelidir**; toplu yükleme yapılmaz.

- Zorunlu filtreler: `run_id` + `agent_id` + zaman aralığı (varsayılan 1 gün).
- Sunucu tarafı üst sınır: tek istekte **≤ 500 geometri** (aşılırsa 400 + açıklama).
- Harita hareketinde viewport bbox filtresi (`ST_Intersects`).
- Metrik paneli zaten toplulaştırılmış `metrics` tablosundan okur — hacim sorunu yok.
- Sunum senaryosu: tek ajan, tek gün, üç yöntem üst üste → ~50 geometri. Yeterli ve etkileyici.

---

### ADR-14 — Analiz örnekleme stratejisi (EK-03)

**Karar.** Her olay analiz edilmez; katmanlı örnekleme uygulanır.

| Amaç | Küme | Varsayılan hacim |
|---|---|---|
| Kalibrasyon iterasyonu | `partition_key='C'` içinden seed'li örnek | **5.000 olay** |
| Final ölçüm (K1–K3) | `partition_key='V'` **tamamı** | ~60.000 olay |
| Tam koşu (opsiyonel) | tüm olaylar | ~300.000 olay, `--full` bayrağı |

Gerekçe: Kalibrasyonun 12 iterasyonu 300K olay üzerinde koşamaz. Final ölçüm ise
bilimsel iddianın dayanağı olduğu için 'V' kümesinin tamamında yapılır.
Örneklem büyüklüğü config'de (`analysis.sample.*`) ayarlanabilir.

---

### ADR-15 — Denetim izi (O-04)

**Karar.** "Kullanıcı" değil, **servis kimliği (principal)** denetlenir.

Kimlik doğrulama kapsam dışı olduğundan "kim" = insan kullanıcı değil; sistemde zaten
tanımlı olan **Kafka principal / PostgreSQL rolü**dür. Bu anlamlı ve ölçülebilirdir.

```sql
CREATE TABLE audit_log (
    audit_id    BIGSERIAL PRIMARY KEY,
    time        TIMESTAMPTZ NOT NULL DEFAULT now(),
    run_id      UUID,
    principal   VARCHAR(40) NOT NULL,   -- 'analysis-engine' / 'validation' / 'integrity' / 'gateway'
    action      VARCHAR(40) NOT NULL,   -- 'read_estimates' / 'write_metrics' / 'api_query' ...
    resource    VARCHAR(60) NOT NULL,
    detail      JSONB
);
```
Kapsam çelişkisi böylece giderilir: belge içinde "kullanıcı denetimi" iddiası yer almaz;
"servis erişim günlüğü" olarak adlandırılır ve delil zinciri (chain of custody) tartışmasında
bu düzeyde savunulur.

---

### ADR-16 — Ölçekleme ölçümünün yeri (D-01)

**Karar.** **K9 ölçümü Docker Compose üzerinde** yapılır (`deploy.replicas: 1/2/4`).
K8s manifest'leri teslim edilir ancak performans kriteri onlara bağlanmaz.

Gerekçe: Tek geliştirici makinesinde minikube/kind overhead'i (etcd, API server, kubelet)
Kafka consumer ölçekleme ölçümünü gürültülendirir; ölçülen şey mimarinin ölçeklenebilirliği
değil, yerel K8s'in yükü olur. Docker Compose replika ölçümü aynı bilimsel soruyu
(consumer paralelliği doğrusal mı) gürültüsüz cevaplar.

K8s çıktıları: `deployments/k8s/` altında manifest + HPA + README (dağıtım belgesi düzeyinde).

---

### ADR-17 — Senaryo farklarının koda bağlanması (O-01)

**Karar.** `morphology` alanı bir **profil anahtarıdır**; tek başına parametre değil.
`internal/config/profile.go` içindeki `MorphologyProfile` yapısı şunları birlikte belirler:

| Alan | urban | rural |
|---|---|---|
| `area_radius_km` | 5 | 20 |
| `inter_site_distance_m` | 500 | 2000 |
| `propagation_model` | UMa (makro), UMi (mikro) | RMa |
| `ant_height_m` | 25 | 45 |
| `freq_distribution` | yüksek bantlar ağırlıklı | düşük bantlar ağırlıklı |
| `agent_home_work_km` | 1–8 | 5–25 |
| `commute_speed_kmh` | 30 | 70 |
| `d_max_km` (model geçerlilik) | 5 | 10 |

Factory (`NewSimulatorFromConfig`) bu profili tek noktadan uygular. YAML'da yalnızca
`morphology: rural` yazmak, yukarıdaki sekiz alanın tamamını tutarlı biçimde değiştirir.
Profil değerleri YAML'dan geçersiz kılınabilir (override) ama varsayılan tutarlıdır.
---

## BÖLÜM C — PARAMETRELER VE MİMARİ

### C.1 Finalize parametreler

| Parametre | Değer | Not |
|---|---|---|
| Izgara çözünürlüğü | 100 m (merkez-merkez) | pointy-top hex, hücre alanı ≈ 8.660 m² |
| Ajan sayısı | 1000 | 1 goroutine/ajan, double-buffer |
| Simülasyon süresi | 30 gün | tick = 5 dk → 8.640 tick/ajan |
| Kafka partition | 4 | key: `pseudo_msisdn` (records), `agent_id` (groundtruth) |
| Kalibrasyon/doğrulama | 80/20, **olay bazlı** | deterministik seed |
| Kalibrasyon parametresi | `λ ∈ [0.5, 3.0]` | bisection, max 12 iterasyon, tol 0.005 |
| Kalibrasyon örneklemi | 5.000 olay | ADR-14 |
| Final ölçüm kümesi | `'V'` tamamı (~60.000) | ADR-14 |
| Baz istasyonu | 100–150 site × 3 sektör | profil bazlı ISD (ADR-17) |
| Alan yarıçapı | kentsel 5 km / kırsal 20 km | ADR-08 |
| Hız üst sınırı (bütünlük) | 300 km/h | Kural 2 |
| Enjeksiyon oranı | 0.02 | ADR-09 |
| k-anonimlik | k=5, **yalnızca aggregate uçlar** | ADR-15 ile birlikte |
| Gölgeleme | σ_nominal = 7 dB | σ_eff = λ·σ_nominal |
| TA | LTE 78.12 m / GSM 550 m | mesafeden türetilir (tutarlılık) |

### C.2 Servis topolojisi (5 servis)

```
┌────────────────────────────────────────────────────────────────────────┐
│  S1 Simülatör ──► Kafka hts.records      (4p, key=pseudo_msisdn) ──┬──►│ S2 Analiz
│  (1000 ajan)  │                                                    └──►│ S4 Bütünlük
│   30 gün      │                                                        │
│   + enjeksiyon└─► Kafka hts.groundtruth  (4p, key=agent_id)  ─────────►│ S3a GT-persister
│     (ADR-09)         ACL: S2,S4 DENY  ◄── KÖR TEST                     │
│                                                                        │
│  S3b Doğrulama (BATCH — make validate)                                 │
│      estimates ⋈ ground_truth → metrics                                │
│      + bütünlük precision/recall (injected_rule etiketiyle)            │
│                                                                        │
│  S5 API Gateway  ── gRPC(iç) + REST/GeoJSON(dış) + /static (web)       │
│                     k=5 yalnızca aggregate uçlarda                     │
│                                                                        │
│  PostgreSQL 15 + PostGIS 3.4 + TimescaleDB 2.x                         │
│  Redis 7 (hücre envanteri)   ·   OTel → Prometheus → Grafana           │
└────────────────────────────────────────────────────────────────────────┘

Kör test — üç katman:
  1. Kafka ACL      : analysis/integrity principal → hts.groundtruth → DENY
  2. PostgreSQL rol : analysis/integrity rolü      → ground_truth    → SELECT yok
  3. Enjeksiyon etiketi (injected_rule) ground_truth'ta → S4 göremez → bütünlük de kör
```

> **Not:** `cmd/visualization/` kaldırıldı (ADR-12); statik web S5 tarafından servis edilir.

### C.3 Klasör yapısı (değişiklikler işaretli)

```
hts-kga/
├── cmd/  simulator/ · analysis-engine/ · validation/ · integrity/ · gateway/
│         (visualization/ KALDIRILDI — ADR-12)
├── internal/
│   ├── config/      scenario.go · profile.go        ← YENİ (ADR-17)
│   ├── simulator/
│   │   ├── inventory/  grid.go · sector.go · frequency.go · rmax.go
│   │   ├── agent/      agent.go · routine.go · mobility.go · doublebuf.go
│   │   │               placement.go                  ← YENİ (ADR-08 rejection sampling)
│   │   ├── radio/      propagation.go · uma.go · umi.go · rma.go · shadowing.go ·
│   │   │               best_server.go · crossvalidate_test.go  ← ADR-06 (test)
│   │   └── event/      generator.go · hts_record.go · ground_truth.go ·
│   │                   eventid.go   ← YENİ (ADR-01 UUIDv5) ·
│   │                   injector/    ← TAŞINDI (ADR-09: simülatör içi)
│   ├── analysis/
│   │   ├── params/     resolver.go · loader.go
│   │   ├── geometry/   sector.go · beam_pattern.go · ta_ring.go · intersection.go
│   │   ├── density/    grid.go · angular.go · radial.go ·
│   │   │               neighbor.go  ← ADR-03 (Φ tabanlı) · normalize.go
│   │   ├── contour/    cumulative.go · multipolygon.go · validator.go ·
│   │   │               centroid.go  ← YENİ (ADR-10)
│   │   └── methods/    interface.go · b0.go · b1.go · m.go
│   ├── validation/
│   │   ├── metrics/    coverage.go · area.go · haversine.go · percentile.go
│   │   ├── calibration/ splitter.go · bisection.go  ← ADR-02
│   │   ├── comparison/ reduction.go
│   │   └── integrity/  precision_recall.go          ← TAŞINDI (etiketi S3b görür)
│   ├── integrity/detector/  inventory · velocity · time · trajectory · activity
│   ├── storage/    postgres/ · redis/ · migrations/
│   └── observability/ otel.go · kafka_propagation.go ← YENİ (O-05)
├── pkg/  geo/{haversine,axial,enu}.go ← enu.go YENİ (ADR-07) · kafka/ · privacy/
├── proto/ · deployments/ · configs/ · scripts/ · tests/ · web/ · docs/adr/
```

---

## BÖLÜM D — VERİ MODELİ (nihai)

```sql
-- ============ KOŞU KAYDI (ADR-05) ============
CREATE TABLE run_config (
    run_id        UUID PRIMARY KEY,
    scenario      CHAR(1)      NOT NULL,          -- A/B/C/D
    morphology    VARCHAR(10)  NOT NULL,
    seed          BIGINT       NOT NULL,
    git_sha       VARCHAR(40)  NOT NULL,
    lambda        FLOAT,                          -- kalibre edilen λ (ADR-02)
    started_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ,
    config_yaml   JSONB        NOT NULL           -- tam senaryo config'i
);

-- ============ HÜCRE ENVANTERİ ============
CREATE TABLE cells (
    cell_id     UUID PRIMARY KEY,
    run_id      UUID NOT NULL REFERENCES run_config(run_id),
    site_id     UUID NOT NULL,
    azimuth     FLOAT NOT NULL,
    beam_width  FLOAT NOT NULL,
    freq_mhz    INTEGER NOT NULL,
    eirp_dbm    FLOAT NOT NULL,
    ant_height  FLOAT NOT NULL,
    tilt_deg    FLOAT NOT NULL,
    r_max_m     FLOAT NOT NULL,                   -- link budget önhesabı
    morphology  VARCHAR(10) NOT NULL,
    model_type  VARCHAR(5)  NOT NULL,             -- UMa/UMi/RMa
    location    GEOGRAPHY(POINT, 4326) NOT NULL
);
CREATE INDEX ON cells USING GIST (location);
CREATE INDEX ON cells (run_id, site_id);          -- E-04

-- ============ HTS KAYITLARI (hypertable) ============
CREATE TABLE hts_records (
    run_id        UUID NOT NULL,
    event_id      UUID NOT NULL,                  -- UUIDv5, deterministik (ADR-01)
    time          TIMESTAMPTZ NOT NULL,
    pseudo_msisdn VARCHAR(64) NOT NULL,
    pseudo_imei   VARCHAR(64),
    event_type    VARCHAR(8)  NOT NULL,
    cell_id       UUID NOT NULL,
    ta_value      INTEGER,                        -- NULL = TA yok
    scenario      CHAR(1) NOT NULL
);
SELECT create_hypertable('hts_records','time',chunk_time_interval=>INTERVAL '1 day');
CREATE UNIQUE INDEX ON hts_records (run_id, event_id, time);        -- ADR-01/EK-02
CREATE INDEX ON hts_records (run_id, pseudo_msisdn, time DESC);
CREATE INDEX ON hts_records (run_id, scenario, time DESC);          -- E-03

-- ============ GROUND TRUTH (hypertable) — S2/S4 ERİŞEMEZ ============
CREATE TABLE ground_truth (
    run_id        UUID NOT NULL,
    event_id      UUID NOT NULL,
    time          TIMESTAMPTZ NOT NULL,
    agent_id      INTEGER NOT NULL,
    true_location GEOGRAPHY(POINT, 4326) NOT NULL,
    covered       BOOLEAN NOT NULL DEFAULT TRUE,  -- ADR-08 kapsama dışı işareti
    partition_key CHAR(1) NOT NULL,               -- 'C' / 'V' (olay bazlı)
    injected_rule INTEGER                          -- ADR-09: NULL=temiz, 1..5=enjekte
);
SELECT create_hypertable('ground_truth','time',chunk_time_interval=>INTERVAL '1 day');
CREATE UNIQUE INDEX ON ground_truth (run_id, event_id, time);
CREATE INDEX ON ground_truth (run_id, partition_key, time DESC);
CREATE INDEX ON ground_truth USING GIST (true_location);

-- ============ TAHMİNLER (hypertable) ============
CREATE TABLE estimates (
    run_id      UUID NOT NULL,
    event_id    UUID NOT NULL,
    time        TIMESTAMPTZ NOT NULL,
    method      CHAR(2) NOT NULL,                 -- B0/B1/M
    confidence  FLOAT NOT NULL,                   -- B0/B1 = -1 sentinel; M = .50/.90/.95
    geometry    GEOGRAPHY(MULTIPOLYGON, 4326) NOT NULL,
    centroid    GEOGRAPHY(POINT, 4326) NOT NULL,  -- ADR-10 kütle ağırlıklı
    area_km2    FLOAT   NOT NULL,                 -- ST_Area(GEOGRAPHY)
    part_count  INTEGER NOT NULL,
    repaired    BOOLEAN NOT NULL DEFAULT FALSE,
    ta_used     BOOLEAN NOT NULL DEFAULT FALSE,   -- TA fallback izleme (T-E03-08b)
    scenario    CHAR(1) NOT NULL
);
SELECT create_hypertable('estimates','time',chunk_time_interval=>INTERVAL '1 day');
CREATE UNIQUE INDEX ON estimates (run_id, event_id, method, confidence, time);  -- EK-02
CREATE INDEX ON estimates USING GIST (geometry);
CREATE INDEX ON estimates (run_id, method, confidence);

-- ============ METRİKLER ============
CREATE TABLE metrics (
    metric_id       BIGSERIAL PRIMARY KEY,
    run_id          UUID NOT NULL REFERENCES run_config(run_id),
    computed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    scenario        CHAR(1) NOT NULL,
    method          CHAR(2) NOT NULL,
    confidence      FLOAT   NOT NULL,
    partition_key   CHAR(1) NOT NULL,
    n_events        INTEGER NOT NULL,
    coverage_rate   FLOAT NOT NULL,
    median_area_km2 FLOAT NOT NULL,
    p90_area_km2    FLOAT NOT NULL,
    median_haversine_m FLOAT NOT NULL,
    r50_m           FLOAT NOT NULL,
    r95_m           FLOAT NOT NULL,
    median_part_count INTEGER NOT NULL,
    p95_part_count  INTEGER NOT NULL,             -- O-02 eşiği bunun üzerinden
    repaired_ratio  FLOAT NOT NULL,
    reduction_vs_b0 FLOAT,
    reduction_vs_b1 FLOAT
);
CREATE INDEX ON metrics (run_id, scenario, method, confidence);

-- ============ BÜTÜNLÜK BULGULARI ============
CREATE TABLE integrity_findings (
    finding_id  BIGSERIAL PRIMARY KEY,
    run_id      UUID NOT NULL,
    event_id    UUID NOT NULL,
    time        TIMESTAMPTZ NOT NULL,
    rule_id     INTEGER NOT NULL,                 -- 1..5
    rule_name   VARCHAR(50) NOT NULL,
    margin      FLOAT NOT NULL,                   -- ADR-09: confidence yerine
    evidence    JSONB NOT NULL,                   -- adli açıklanabilirlik
    scenario    CHAR(1) NOT NULL
);
CREATE INDEX ON integrity_findings (run_id, rule_id);
CREATE INDEX ON integrity_findings (run_id, event_id);

-- ============ SERVİS ERİŞİM GÜNLÜĞÜ (ADR-15) ============
CREATE TABLE audit_log (
    audit_id  BIGSERIAL PRIMARY KEY,
    time      TIMESTAMPTZ NOT NULL DEFAULT now(),
    run_id    UUID,
    principal VARCHAR(40) NOT NULL,
    action    VARCHAR(40) NOT NULL,
    resource  VARCHAR(60) NOT NULL,
    detail    JSONB
);
CREATE INDEX ON audit_log (run_id, time DESC);
```

**Rol yetkilendirmesi (004_roles.sql):**
```sql
-- analysis + integrity: ground_truth GÖREMEZ
REVOKE ALL ON ground_truth FROM svc_analysis, svc_integrity;
GRANT SELECT ON cells, hts_records, run_config TO svc_analysis, svc_integrity;
GRANT SELECT, INSERT ON estimates TO svc_analysis;
GRANT SELECT, INSERT ON integrity_findings TO svc_integrity;
-- validation: her şeyi görür, tahmin üretmez
GRANT SELECT ON ground_truth, estimates, hts_records, cells, integrity_findings TO svc_validation;
GRANT SELECT, INSERT ON metrics TO svc_validation;
GRANT SELECT, INSERT ON ground_truth TO svc_gt_persister;   -- S3a
```

---

## BÖLÜM E — ALGORİTMA SPESİFİKASYONLARI

> Bu bölüm, v2.1'de "tanımsız" bırakılan her algoritmanın uygulanabilir tanımıdır.

### E.1 Olasılık kütlesi üretimi (T-E03-09..13)

```
GİRDİ : hts_record (cell_id, ta_value), cells envanteri (Redis), λ
ÇIKTI : map[AxialCoord]float64  (Σ = 1.0)

1. s ← envanterden serving cell parametreleri (r_max dâhil)
2. Kapsama bölgesi: sektör dilimi (azimut ± beam_width/2, yarıçap r_max)
   ta_value ≠ NULL ise TA halkasıyla kesiştir
   Kesişim boş ise → TA'yı yok say, ta_used=false (T-E03-08b)
3. Bölgeyi kaplayan pointy-top hex ızgara üret (ADR-07, ENU metre)
4. Her hücre merkezi p için:
     w_ang(p) = beamPattern(|θ_s(p) − azimuth_s|)            # anten deseni, 0..1
     w_rad(p) = radialWeight(d(p,s), r_max, ta_ring, σ_eff)  # link budget + TA
     w_nbr(p) = Φ( Δ(p) / (σ_eff·√2) )                       # ADR-03
     mass_raw(p) = w_ang · w_rad · w_nbr
5. Normalize: mass(p) = mass_raw(p) / Σ mass_raw
   PBT: |Σ mass − 1.0| < 1e-9
```

### E.2 Komşu kümesi seçimi (ADR-03 destek)

```
N(p) = { n ∈ cells : dist(p, n.location) ≤ n.r_max_m , n ≠ s }
Uygulama: Redis'te site konumlarına göre kaba mekânsal bucket;
          bölge merkezinden max(r_max) yarıçapında ön-filtre.
Tipik |N| = 3–8. |N| = 0 ise w_nbr(p) = 1 (komşu yok, kısıt yok).
```

### E.3 Kontur çıkarımı (T-E03-16)

```
1. Hücreleri mass'e göre AZALAN sırala
2. Kümülatif topla; hedef (0.50 / 0.90 / 0.95) aşılana kadar hücreleri seç
3. Seçilen hücre kümesini komşu birleştirmeyle poligonlaştır (ENU)
4. ENU → WGS84 dönüşümü (ADR-07)
5. ST_Union → MULTIPOLYGON
6. ST_IsValid ? değilse ST_MakeValid + repaired=true + sayaç
7. part_count = ST_NumGeometries
8. area_km2 = ST_Area(geometry)/1e6          # kütleden bağımsız, gerçek yüzey
9. centroid = Σ(mass_i·p_i)/Σmass_i → WGS84  # ADR-10
```

### E.4 Kalibrasyon döngüsü (T-E04-09, ADR-02)

```
GİRDİ : senaryo, calibration_sample (5.000 olay, partition_key='C')
ÇIKTI : λ*  → run_config.lambda

lo, hi = 0.5, 3.0 ; MAX_ITER = 12 ; TOL = 0.005
for i in 1..MAX_ITER:
    mid = (lo+hi)/2
    estimates ← analiz motorunu λ=mid ile sample üzerinde koştur (M@90% yeter)
    c ← coverage90(estimates, ground_truth['C'])
    if |c − 0.90| < TOL: λ* = mid ; break
    if c < 0.90: lo = mid else: hi = mid
λ* = λ* ?? (lo+hi)/2      # yakınsamazsa son değer + UYARI logla
```
> Final ölçüm λ* ile ve **yalnızca `partition_key='V'`** üzerinde yapılır (K4).

### E.5 Kapsama dışı ve ajan yerleşimi (ADR-08)

```
placeAgentAnchor(profile):                     # ev ve iş için ayrı çağrılır
    for attempt in 1..100:
        p ← uniform(bbox(origin, area_radius_km))
        if maxRxPower(p) ≥ RxSensitivity: return p
    fail("scenario config invalid: coverage too sparse")

onTick(agent):
    (cell, rx) ← bestServer(agent.pos)
    if rx < RxSensitivity:
        record ground_truth(covered=false); NO event
    else:
        ta ← TAFromDistance(dist(agent.pos, cell.site), tech)
        maybe emit event (Poisson λ(hour))
```

### E.6 Enjeksiyon aşaması (ADR-09)

```
for each generated event e:
    if rand() < injection_rate:
        rule ← weightedChoice(1..5)
        e' ← applyInjection(e, rule)            # sahte hücre / atlama / zaman / boşluk / sahtecilik
        publish(e' → hts.records)
        publish(gt(e, injected_rule=rule) → hts.groundtruth)
    else:
        publish(e → hts.records) ; publish(gt(e, injected_rule=NULL) → ...)
```
> Kural 4 (kayıt boşluğu) olay **silme** olduğundan, silinen olay için yalnızca
> ground_truth yazılır (`injected_rule=4`), `hts.records`'a hiçbir şey gitmez.

---

## BÖLÜM F — METRİK SQL ŞABLONLARI (K-05)

> Bu şablonlar `internal/validation/metrics/` içinde hazır sorgular olarak yer alır.

**F.1 — Kapsama oranı (yöntem ve güven seviyesi bazında)**
```sql
SELECT e.method, e.confidence,
       count(*)                                            AS n_events,
       avg( (ST_Contains(e.geometry::geometry,
                         g.true_location::geometry))::int ) AS coverage_rate
FROM estimates e
JOIN ground_truth g
  ON g.run_id = e.run_id AND g.event_id = e.event_id
WHERE e.run_id = :run
  AND g.partition_key = :pkey        -- 'C' veya 'V' — ASLA karışık (K4)
  AND g.covered = TRUE
GROUP BY e.method, e.confidence;
```

**F.2 — Alan ve parça istatistikleri**
```sql
SELECT e.method, e.confidence,
       percentile_cont(0.50) WITHIN GROUP (ORDER BY e.area_km2)   AS median_area_km2,
       percentile_cont(0.90) WITHIN GROUP (ORDER BY e.area_km2)   AS p90_area_km2,
       percentile_cont(0.50) WITHIN GROUP (ORDER BY e.part_count) AS median_part_count,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY e.part_count) AS p95_part_count,
       avg(e.repaired::int)                                       AS repaired_ratio
FROM estimates e
JOIN ground_truth g ON g.run_id=e.run_id AND g.event_id=e.event_id
WHERE e.run_id=:run AND g.partition_key=:pkey
GROUP BY e.method, e.confidence;
```

**F.3 — Haversine hata ve yüzdelikler**
```sql
WITH d AS (
  SELECT e.method, e.confidence,
         ST_Distance(e.centroid, g.true_location) AS err_m
  FROM estimates e
  JOIN ground_truth g ON g.run_id=e.run_id AND g.event_id=e.event_id
  WHERE e.run_id=:run AND g.partition_key=:pkey AND g.covered
)
SELECT method, confidence,
       percentile_cont(0.50) WITHIN GROUP (ORDER BY err_m) AS median_haversine_m,
       percentile_cont(0.50) WITHIN GROUP (ORDER BY err_m) AS r50_m,
       percentile_cont(0.95) WITHIN GROUP (ORDER BY err_m) AS r95_m
FROM d GROUP BY method, confidence;
```

**F.4 — Asıl iddia: M@90% vs B1 ve B0 daralma (K2, K3)**
```sql
WITH a AS (
  SELECT method, confidence,
         percentile_cont(0.5) WITHIN GROUP (ORDER BY area_km2) AS med_area
  FROM estimates e
  JOIN ground_truth g ON g.run_id=e.run_id AND g.event_id=e.event_id
  WHERE e.run_id=:run AND g.partition_key='V'
  GROUP BY method, confidence
)
SELECT
  (SELECT med_area FROM a WHERE method='B0')                          AS b0_area,
  (SELECT med_area FROM a WHERE method='B1')                          AS b1_area,
  (SELECT med_area FROM a WHERE method='M' AND confidence=0.90)       AS m_area,
  1 - (SELECT med_area FROM a WHERE method='M' AND confidence=0.90)
    / (SELECT med_area FROM a WHERE method='B0')                      AS reduction_vs_b0,
  1 - (SELECT med_area FROM a WHERE method='M' AND confidence=0.90)
    / (SELECT med_area FROM a WHERE method='B1')                      AS reduction_vs_b1;
```
> **Karşılaştırma kuralı:** M daima **@90%** seviyesinde B0/B1 ile karşılaştırılır.
> B0/B1'in `confidence = -1` sentinel'i yalnızca UNIQUE constraint içindir, karşılaştırmaya girmez.

**F.5 — Bütünlük precision/recall (yalnızca S3b görebilir)**
```sql
SELECT f.rule_id,
       count(*) FILTER (WHERE g.injected_rule = f.rule_id)::float
         / NULLIF(count(*),0)                                   AS precision
FROM integrity_findings f
JOIN ground_truth g ON g.run_id=f.run_id AND g.event_id=f.event_id
WHERE f.run_id = :run
GROUP BY f.rule_id;

-- recall: enjekte edilenlerin kaçı yakalandı
SELECT g.injected_rule,
       count(*) FILTER (WHERE f.event_id IS NOT NULL)::float
         / NULLIF(count(*),0)                                   AS recall
FROM ground_truth g
LEFT JOIN integrity_findings f
       ON f.run_id=g.run_id AND f.event_id=g.event_id AND f.rule_id=g.injected_rule
WHERE g.run_id=:run AND g.injected_rule IS NOT NULL
GROUP BY g.injected_rule;
```
---

## BÖLÜM G — EPIC VE TASK LİSTESİ (v3.0 farkları işaretli)

| Epic | Ad | SP | Öncelik | Risk |
|---|---|---|---|---|
| E01 | Altyapı & DevOps | 15 | P0 | Düşük |
| E02 | Şebeke Simülatörü (+ enjeksiyon) | 26 | P0 | Yüksek |
| E03 | Analiz & Belirleme Motoru | 24 | P1 | Çok Yüksek |
| E04 | Doğrulama & Kalibrasyon | 16 | P1 | Yüksek |
| E05 | Bütünlük Denetimi | 8 | P2 | Orta |
| E06 | API Gateway + statik web | 8 | P2 | Düşük |
| E07 | Görselleştirme | 8 | P2 | Düşük |
| E08 | Gizlilik & Erişim Günlüğü | 5 | P1 | Düşük |
| E09 | Gözlemlenebilirlik & Performans | 9 | P2 | Orta |
| E10 | Test Altyapısı | 14 | P1 | Orta |
| **TOPLAM** | | **133** | | |

### E01 — Altyapı (Sprint 0)

| Task | Açıklama | SP |
|---|---|---|
| T-E01-01 | `go.mod` (module adı, Go 1.22+), Makefile iskeleti — **E-09, E-13** | 2 |
| T-E01-02 | Docker Compose + `depends_on` + `healthcheck` — **E-07** | 3 |
| T-E01-03 | PostgreSQL 15 + PostGIS 3.4 + TimescaleDB | 2 |
| T-E01-04 | Kafka (KRaft) 4 partition + Redis 7 | 2 |
| T-E01-05 | Şema migration 001 (8 tablo, `run_id`, GEOGRAPHY) — **ADR-05** | 3 |
| T-E01-06 | Hypertable + UNIQUE (time dâhil) + indeksler — **ADR-01, E-03, E-04** | 2 |
| T-E01-07 | Rol yetkilendirmesi (svc_analysis/integrity/validation/gt_persister) | 2 |
| T-E01-08 | Kafka topic + ACL (S2/S4 groundtruth DENY) | 2 |
| T-E01-09 | Redis key şeması `hts:{run_id}:cell:{cell_id}` + TTL yok (koşu ömürlü) — **E-05, E-06** | 1 |
| T-E01-10 | `.env` + HMAC_SALT yönetimi — **E-08** | 1 |
| T-E01-11 | Health/ready uçları | 1 |
| T-E01-12 | `make setup / verify-isolation / verify-integrity` — **ADR-01** | 2 |

### E02 — Simülatör (Sprint 1–2)

| Task | Açıklama | SP |
|---|---|---|
| T-E02-01 | Senaryo YAML şeması + `MorphologyProfile` factory — **ADR-17, O-01** | 3 |
| T-E02-02 | ENU dönüşüm katmanı (`pkg/geo/enu.go`) — **ADR-07** | 2 |
| T-E02-03 | Pointy-top axial hex grid + site yerleşimi (bbox içinde) — **ADR-07/08** | 3 |
| T-E02-04 | Sektör/azimut/hüzme/tilt/frekans/EIRP atama | 2 |
| T-E02-05 | `r_max` link budget önhesabı → `cells` | 2 |
| T-E02-06 | Envanter → DB + Redis toplu yükleme | 1 |
| T-E02-07 | AgentState + **rejection sampling yerleşim** — **ADR-08** | 2 |
| T-E02-08 | 4-faz rutin + haftalık periyodisite (profil bazlı) | 3 |
| T-E02-09 | Hız profili + gürültü + double-buffer | 2 |
| T-E02-10 | **3GPP TR 38.901 UMa/UMi/RMa** (tam formüller, d_max kontrolü) | 4 |
| T-E02-11 | Log-normal gölgeleme (σ_nominal=7, seed'li) | 2 |
| T-E02-12 | Best-server + **kapsama dışı yönetimi** — **ADR-08** | 2 |
| T-E02-13 | Poisson olay üreteci (saate göre λ) | 2 |
| T-E02-14 | `event_id` UUIDv5 deterministik üretimi — **ADR-01** | 1 |
| T-E02-15 | HTS kaydı + HMAC-SHA256 + **TA mesafeden türetme** | 2 |
| T-E02-16 | Ground truth + `covered` + olay bazlı 80/20 `partition_key` | 1 |
| T-E02-17 | **Enjeksiyon aşaması** (5 kural, oran, `injected_rule` etiketi) — **ADR-09** | 4 |
| T-E02-18 | Kafka publisher (2 topic, doğru key'ler) | 2 |
| T-E02-19 | Altın senaryo (tek site, σ=0) + PBT | 4 |

### E03 — Analiz & Belirleme (Sprint 3–4)

| Task | Açıklama | SP |
|---|---|---|
| T-E03-01 | Kafka consumer + OTel context extraction — **O-05** | 2 |
| T-E03-02 | Redis envanter yükleyici + adaptör | 3 |
| T-E03-03 | Sektör geometrisi + hüzme deseni ağırlığı | 3 |
| T-E03-04 | TA halkası + kesişim + **∅ fallback** (`ta_used`) | 3 |
| T-E03-05 | Hex ızgara üretimi (ENU, config çözünürlük) — **E-10** | 2 |
| T-E03-06 | Açısal + radyal ağırlık bileşenleri | 3 |
| T-E03-07 | **Komşu hücre kısıtı** (Φ tabanlı, komşu seçimi) — **ADR-03** | 4 |
| T-E03-08 | Kütle normalizasyonu (Σ=1 ± 1e-9) | 1 |
| T-E03-09 | B0 (naif daire) + B1 (sektör dilimi) | 2 |
| T-E03-10 | Kümülatif kontur (%50/%90/%95) | 3 |
| T-E03-11 | MULTIPOLYGON + part_count + ST_IsValid/MakeValid | 2 |
| T-E03-12 | **Kütle ağırlıklı centroid** → estimates — **ADR-10** | 2 |
| T-E03-13 | `estimates` yazımı (sentinel confidence=-1 dâhil) | 2 |
| T-E03-14 | λ parametresinin motora enjeksiyonu (kalibrasyon arayüzü) — **ADR-02** | 2 |
| T-E03-15 | Analiz PBT (6 değişmez) | 3 |

### E04 — Doğrulama & Kalibrasyon (Sprint 5)

| Task | Açıklama | SP |
|---|---|---|
| T-E04-01 | **S3a** ground truth persister (Kafka → DB) — **ADR-04** | 2 |
| T-E04-02 | **S3b** batch job iskeleti + tamamlanma önkoşulu denetimi — **ADR-04** | 2 |
| T-E04-03 | Metrik SQL şablonları (F.1–F.3) | 3 |
| T-E04-04 | Daralma hesabı F.4 (K2/K3) | 2 |
| T-E04-05 | 80/20 enforcer (karışık sorgu → hata) | 2 |
| T-E04-06 | **Bisection kalibrasyon döngüsü** — **ADR-02** | 4 |
| T-E04-07 | Örnekleme katmanı (5.000 / tam 'V') — **ADR-14** | 2 |
| T-E04-08 | 4 senaryo koşumu + karşılaştırmalı rapor | 4 |
| T-E04-09 | Bütünlük precision/recall (F.5) — **ADR-09** | 2 |

### E05–E10 (özet)

| Task grubu | Açıklama | SP |
|---|---|---|
| T-E05-01..05 | 5 tespit kuralı (envanter, hız 300, zaman, yörünge, aktivite) + `evidence JSONB` | 8 |
| T-E06-01..05 | `.proto` + gRPC server + REST handler'lar (elle) + `ST_AsGeoJSON` + `/static` — **ADR-12** | 8 |
| T-E07-01..06 | Leaflet + 6 katman + **zorunlu filtre & 500 geometri limiti** — **ADR-13** | 8 |
| T-E08-01..04 | HMAC pseudonymization + tuz + k=5 (aggregate) + `audit_log` — **ADR-15** | 5 |
| T-E09-01..06 | OTel + **Kafka W3C TraceContext propagation** + lag + eğriler + Grafana provisioning — **O-05, E-12** | 9 |
| T-E10-01..12 | `rapid` PBT altyapısı + 6 değişmez + altın senaryo + entegrasyon + izolasyon + çapraz doğrulama testi — **ADR-06, ADR-11** | 14 |

---

## BÖLÜM H — SPRİNT PLANI

| Sprint | Süre | Hedef | Çıktı |
|---|---|---|---|
| **S0** | 4 gün | Altyapı + çift katman izolasyon + `run_id` şeması | K6 kanıtlandı |
| **S1** | 5 gün | ENU + hex grid + envanter + ajan yerleşimi (kapsama garantili) | KT2 |
| **S2** | 6 gün | 3GPP + best-server + olay + TA tutarlı + **enjeksiyon** + Kafka | KT3, KT4 |
| **S3** | 6 gün | Sektör + TA + ızgara + ağırlıklar + **komşu kısıtı** + normalizasyon | KT5 kısmen |
| **S4** | 6 gün | Kontur + MULTIPOLYGON + centroid + B0/B1/M + estimates | KT5, KT6 |
| **S5** | 6 gün | S3a/S3b + metrikler + **kalibrasyon** + 4 senaryo | **K1, K2, K3, K4, K5** |
| **S6** | 4 gün | 5 tespit kuralı + precision/recall | K7 |
| **S7** | 5 gün | gRPC + REST + statik web + Leaflet (filtreli) | KT9 |
| **S8** | 4 gün | OTel + Kafka trace propagation + ölçekleme (Compose) + Grafana | K8, K9, K10 |
| | **46 gün** | | K1–K10 |

**Öncelik kuralı (baskı altında feda sırası):**
```
1. S8 ileri özellikleri (Grafana panel zenginliği, ızgara eğrisi)
2. S7 görsel katman sayısı (6 → 4)
3. S6 kural sayısı (5 → 3: envanter, hız, zaman)
ASLA: S3, S4, S5 — bilimsel değerin tamamı burada.
S6+ hiçbir koşulda S5 bitmeden başlamaz.
```

---

## BÖLÜM I — TEST PLANI

### PBT değişmezleri (`rapid` ile)

| # | Değişmez |
|---|---|
| 1 | `\|Σ mass − 1.0\| < 1e-9` |
| 2 | `area(M@95) ≥ area(M@90) ≥ area(M@50)` |
| 3 | `area(B1) ≤ area(B0)` |
| 4 | `ST_IsValid(geometry) = true` |
| 5 | `part_count ≥ 1` |
| 6 | `coverage_rate ∈ [0,1]` |
| 7 | `ta_value · res ≤ r_max` (simülatör — imkânsız TA üretilemez) |
| 8 | `w_nbr(p) ∈ [0,1]` ve Δ→+∞ iken →1 (ADR-03 sınır davranışı) |

### Test katmanları

| Katman | Örnek |
|---|---|
| Birim | Haversine, ENU round-trip, axial↔ENU round-trip, TA dönüşümü, link budget referans |
| Çapraz doğrulama | 3GPP vs Okumura-Hata/COST-231, fark ≤ 10 dB (ADR-06) |
| Altın senaryo | Tek site, σ=0, TA yok → kapsama %100, alan elle hesapla ±%5 |
| Entegrasyon | Kafka round-trip, PostGIS GEOGRAPHY round-trip, gRPC sözleşme |
| İzolasyon | S2/S4 → `ground_truth` → denied; Kafka ACL → denied; S3 → success |
| Bütünlük | `make verify-integrity` → eşleşmeyen kayıt = 0 (ADR-01) |

---

## BÖLÜM J — BAŞARI KRİTERLERİ

> Eşikler ölçümden **önce** beyan edilmiştir; sonuca göre değiştirilmez.

| # | Kriter | Eşik | Sprint |
|---|---|---|---|
| **K1** | Kalibrasyon: kapsama@90% (4 senaryonun her biri, 'V' kümesi) | %85–95 | S5 |
| **K2** | M@90% vs B0 medyan alan daralması | ≥ %75 | S5 |
| **K3** | **M@90% vs B1 medyan alan daralması (asıl iddia)** | TA var ≥ %50 · TA yok ≥ %20 | S5 |
| **K4** | Kalibrasyon/doğrulama ayrımı kod zorunluluğu | Karışık sorgu → hata (test) | S5 |
| **K5** | 4 senaryo koşulmuş ve raporlanmış | A, B, C, D | S5 |
| **K6** | Kör test bütünlüğü (3 katman) | İzolasyon testleri geçer | S0 |
| **K7** | Bütünlük tespiti | Precision ≥ %90 (kural bazında), recall raporlu | S6 |
| **K8** | Geometri kararlılığı | `repaired_ratio < %1` **ve `p95_part_count ≤ 3`** — O-02 | S4 |
| **K9** | Ölçeklenebilirlik (**Docker Compose replika** — ADR-16) | 4 replika → verim ≥ 3,5× | S8 |
| **K10** | Tekrarlanabilirlik | Aynı seed, iki `run_id` → estimates bit-identical | S8 |

> **K3 hakkında:** Tutmama ihtimali gerçektir. Tutmazsa sonuç "olasılıksal modelleme,
> geometrik sektör dilimine göre bu senaryolarda anlamlı ek kazanç sağlamamıştır" — geçerli
> ve raporlanabilir bir negatif bulgudur. Eşiğin önceden beyanı bu dürüstlüğü mümkün kılar.

---

## BÖLÜM K — RİSK MATRİSİ

| Risk | Olasılık | Etki | Azaltma |
|---|---|---|---|
| T-E02-10 (3GPP) tahminden uzun sürer | Orta | Kritik | Önce sabit `r_max` ile uçtan uca zincir; sonra formülleri zenginleştir |
| K3 tutmuyor | Orta | Yüksek | Negatif bulgu yayınlanabilir (eşik önceden beyan) |
| Komşu kısıtı (ADR-03) beklenenden pahalı | Orta | Orta | |N| ön-filtre; gerekirse en güçlü 3 komşuyla sınırla (config) |
| Kalibrasyon yakınsamıyor | Düşük | Orta | 12 iterasyon sabit üst sınır + uyarı; λ aralığı genişletilebilir |
| Kontur parçalanması (p95_part_count > 3) | Orta | Yüksek | Izgara çözünürlüğü config; K8 eşiği erken uyarı verir |
| ENU yaklaşımı kırsalda (20 km) hata büyütür | Düşük | Orta | 20 km'de hata ~4 m; 100 m ızgarada kabul edilebilir, round-trip testiyle doğrulanır |
| S7 (API+UI) yükü | Orta | Orta | ADR-12/13 ile sadeleşti; katman sayısı feda edilebilir |
| S8 altyapı zaman çalıyor | Orta | Yüksek | S5 bitmeden S8'e geçilmez (öncelik kuralı) |
| Simülatörde sessiz hata | Orta | **Kritik** | Altın senaryo + 8 PBT + `verify-integrity` |

---

## BÖLÜM L — SENARYO CONFIG (nihai)

```yaml
# configs/urban_ta.yaml   (A senaryosu)
run:
  scenario: "A"
  name: "Kentsel + TA Var"
  seed: 42                          # deterministik tekrarlanabilirlik (K10)

morphology: "urban"                 # ADR-17: profil anahtarı — 8 alanı birden belirler

area:                               # ADR-08
  origin_lat: 38.6748               # senaryo merkez noktası
  origin_lon: 39.2225
  radius_km: 5                      # kentsel profil varsayılanı

simulation:
  agents: 1000
  duration_days: 30
  tick_minutes: 5                   # 8.640 tick/ajan

network:
  sites_min: 100
  sites_max: 150
  # inter_site_distance_m, ant_height_m, freq_distribution, propagation_model
  # → morphology profilinden gelir; buradan override edilebilir
  rx_sensitivity_dbm: -110

radio:
  shadowing_sigma_db: 7             # σ_nominal ; σ_eff = λ · σ_nominal
  d_max_km: 5                       # UMa/UMi geçerlilik sınırı (profil)

timing_advance:
  enabled: true
  technology: "LTE"                 # 78.12 m/adım

analysis:
  grid_resolution_m: 100            # ADR-07
  contour_levels: [0.50, 0.90, 0.95]
  neighbor_max_count: 8             # ADR-03 ön-filtre üst sınırı
  sample:                           # ADR-14
    calibration_events: 5000
    validation_events: 0            # 0 = 'V' kümesinin tamamı

calibration:                        # ADR-02
  split_ratio: 0.80                 # olay bazında C/V
  lambda_min: 0.5
  lambda_max: 3.0
  target_coverage: 0.90
  tolerance: 0.005
  max_iterations: 12

integrity:
  max_velocity_kmh: 300
  injection_rate: 0.02              # ADR-09
  rule_weights: {1: 0.2, 2: 0.2, 3: 0.2, 4: 0.2, 5: 0.2}

privacy:
  k_anonymity: 5                    # yalnızca aggregate uçlar
  hmac_salt_env: "HMAC_SALT"        # .env'den; config'de değil

api:
  max_geometries_per_request: 500   # ADR-13
```

---

## BÖLÜM M — BULGU İZLENEBİLİRLİK MATRİSİ

| Bulgu | Durum | Çözüm yeri |
|---|---|---|
| K-01 event_id köprüsü | ✔ Kapatıldı (+teknik düzeltme) | ADR-01, Bölüm D, `verify-integrity` |
| K-02 kalibrasyon tanımsız | ✔ Kapatıldı | ADR-02, E.4, config |
| K-03 komşu kısıt algoritması | ✔ Kapatıldı | ADR-03, E.1–E.2 |
| K-04 S3 senkronizasyon | ✔ Kapatıldı (daha basit çözümle) | ADR-04, T-E04-01/02 |
| K-05 metrik SQL yok | ✔ Kapatıldı | Bölüm F (F.1–F.5) |
| Y-01 hex→WGS84 | ✔ Kapatıldı | ADR-07, `pkg/geo/enu.go` |
| Y-02 bounding box | ✔ Kapatıldı | ADR-08, config `area:` |
| Y-03 kapsama dışı ajan | ✔ Kapatıldı | ADR-08, E.5 |
| Y-04 enjeksiyon mekanizması | ✔ Kapatıldı | ADR-09, T-E02-17 |
| Y-05 gRPC↔REST | ✔ Kapatıldı | ADR-12 |
| Y-06 çapraz doğrulama anlamı | ✔ Kapatıldı | ADR-06 |
| Y-07 gopter | ✔ Kapatıldı (`rapid`) | ADR-11 |
| Y-08 centroid kaybı | ✔ Kapatıldı | ADR-10, `estimates.centroid` |
| O-01 senaryo farkları | ✔ Kapatıldı | ADR-17, `profile.go` |
| O-02 part_count eşiği | ✔ Kapatıldı | K8: `p95_part_count ≤ 3` |
| O-03 Leaflet hacmi | ✔ Kapatıldı (+hesap düzeltmesi) | ADR-13 |
| O-04 audit vs kimlik | ✔ Kapatıldı | ADR-15, `audit_log` |
| O-05 Kafka trace | ✔ Kapatıldı | T-E09, `kafka_propagation.go` |
| O-06 aynı DB mi | ✔ Kapatıldı | ADR-05 (`run_id`) |
| D-01 K8s HPA | ✔ Kabul edildi | ADR-16 (K9 Compose'da) |
| D-02 klasik modeller | ✔ Kısmen (teste indirgendi) | ADR-06 |
| D-03 visualization servisi | ✔ Kabul edildi (kaldırıldı) | ADR-12 |
| D-04 confidence float | ✔ Kabul edildi | ADR-09 (`margin` + `evidence`) |
| E-01..E-14 minör | ✔ Tümü kapatıldı | Bölüm D/G/L içinde işaretli |
| EK-01..EK-04 (ek bulgular) | ✔ Kapatıldı | ADR-01/05/14, Bölüm D |

---

## BÖLÜM N — SPRINT 0 BAŞLAMA KONTROL LİSTESİ

Aşağıdakilerin tamamı karara bağlanmıştır; başlamak için ek karar gerekmez.

- [x] Senaryo bounding box → `area.origin_lat/lon`, `radius_km`
- [x] Axial hex yönelimi → pointy-top; dönüşüm ENU üzerinden (ADR-07)
- [x] Kalibrasyon hedef fonksiyonu → λ bisection, coverage@90 = 0.90, max 12 iter
- [x] S3 senkronizasyon → S3a stream persister + S3b batch (ADR-04)
- [x] 4 senaryo DB stratejisi → tek DB + `run_id` (ADR-05)
- [x] Enjeksiyon mekanizması → simülatör içi, `injection_rate`, etiket ground_truth'ta
- [x] PBT kütüphanesi → `pgregory.net/rapid`
- [x] Görselleştirme → statik web, S5 servis eder (`cmd/visualization` yok)
- [x] GeoJSON üretimi → DB'de `ST_AsGeoJSON`
- [x] Centroid saklama → `estimates.centroid`
- [x] Komşu kısıt formülü → `Φ(Δ/(σ_eff√2))`
- [x] `run_id` ve deterministik `event_id` (UUIDv5)
- [x] Analiz örnekleme stratejisi
- [x] K9 ölçüm ortamı → Docker Compose replika

---

> **Kod yazılmamıştır.**
> Tüm mimari ve algoritmik kararlar ADR'ye bağlanmıştır; belirsiz karar kalmamıştır.
> **"Implementasyon aşamasına geç" komutuyla Sprint 0 / T-E01-01 başlar.**
