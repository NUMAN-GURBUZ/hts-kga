# Sprint 6 (E05 — Bütünlük Denetimi) — Uygulanabilir Kesin Plan

**Tarih:** 2026-07-30
**Durum:** Plan — **kod yazılmamıştır**
**Dayanak:** `docs/planning/sprint6-analysis.md` (ölçümlü ön analiz)
**Hedef:** K7 — kural bazında precision ≥ %90, recall raporlu

---

## 1. Sprint 6'nın gerçek kapsamı

### 1.1 Kapsamı belirleyen tek ilke

> **K7, precision'ı *yapısal* olan kurallar üzerine kurulur; precision'ı
> *istatistiksel* olan kural sonradan eklenir ve sonucu ne olursa raporlanır.**

Ölçüm bu ayrımı net veriyor:

| Kural | Precision'ın kaynağı | Ayarlanabilir mi? |
|---|---|---|
| 1 envanter | küme üyeliği — eşik yok | Hayır (yapısal %100) |
| 5 aktivite | kapalı popülasyonda çoğunluk — eşik yok | Hayır (yapısal %100) |
| 3 zaman | varış sırası ↔ olay zamanı çelişkisi — eşik yok | Hayır (yapısal %100) |
| 2 hız | mesafe/süre **eşiği** + **atıf** | Evet → aşırı uydurma riski |
| 4 yörünge | istatistiksel boşluk — çıpa yok | Ölçülemez |

Sprint 6'nın kapsamı bu tabloya göre kesilir: **üç yapısal kural K7'nin
gövdesi, kural 2 eklenti, kural 4 Sprint 7.**

### 1.2 Kapsam — kesin (17 SP / 5 gün)

| Task | Ad | SP | Sınıf |
|---|---|---|---|
| T-E05-00 | `pkg/integrityrule` ortak kural sözleşmesi | 1 | Zorunlu |
| T-E05-01 | Kural motoru + bulgu yazıcısı + migration 006 | 3 | Zorunlu |
| T-E05-02 | Kural 1 — envanter tutarlılığı | 1 | Zorunlu |
| T-E05-03 | Kural 3 — zaman tutarlılığı (akış) | 2 | Zorunlu |
| T-E05-04 | Kural 5 — cihaz aktivitesi (toplu) | 1 | Zorunlu |
| T-E05-07 | `cmd/integrity` bağlantısı (akış + toplu mod) | 2 | Zorunlu |
| T-E04-09 | F.5 precision/recall + karışıklık matrisi | 2 | Zorunlu |
| T-E05-08 | Dört senaryo koşumu + K7 raporu | 2 | Zorunlu |
| | **Çekirdek toplamı** | **14** | **K7 üç kuralla ölçülebilir** |
| T-E05-05 | Kural 2 — kinematik tutarlılık + atıf | 3 | Koşullu (Gün 3 kapısı) |
| | **Toplam** | **17** | |

### 1.3 Kapsam dışı — Sprint 7

| İş | SP | Neden Sprint 7 |
|---|---|---|
| T-E05-06 kural 4 göstergesi | 2 | Olay-çıpalı bulgu üretemez (ADR-30); K7'ye katkısı yok, koşu düzeyi istatistik olarak sonradan eklenebilir |
| Kafka ACL (K6 katman 1) | 2 | Sprint 5 borcu; broker yapılandırması, E05'in yolu üzerinde değil |
| Arama bölgesi ADR'si (K1) | 5+ | Model değişikliği + dört senaryonun yeniden kalibrasyonu; E05'ten bağımsız, aynı sprinte konursa ikisi birbirini yer |
| `median_haversine_m` totolojisi | 1 | Şema değişikliği, F.5 kullanmıyor |

### 1.4 Kapsam değişikliğinin plandan farkı

| | Plan (BÖLÜM G/H) | Bu plan |
|---|---|---|
| Süre | 4 gün | **5 gün** (+ ½ gün kod yazılmayan Gün 0) |
| SP | 10 (T-E05-01..05 = 8, T-E04-09 = 2) | 17 |
| Kural sayısı | 5 | 4 (kural 4 → Sprint 7) |
| Kapsam düşürme listesi | "5 → 3: envanter, **hız**, zaman" | "5 → 3: envanter, zaman, **aktivite**" |

Son satır bilinçli bir düzeltmedir: planın feda listesi kural 5'i (1 SP,
%100/%100) atıp kural 2'yi (3 SP, %72–83) tutuyor. Ölçüm bunun tersini
söylüyor.

---

## 2. Planın hatalı ve eksik maddeleri — tek tek

> Numaralandırma: **H** = hata, **E** = eksik, **Ç** = çelişki, **B** = belirsiz.

### H-1 · BÖLÜM F, F.5 precision sorgusu iç birleştirme kullanıyor

```sql
FROM integrity_findings f
JOIN ground_truth g ON g.run_id=f.run_id AND g.event_id=f.event_id
```
`ground_truth`'ta eşi olmayan bir bulgu (uydurulmuş `event_id`, yabancı koşu,
kural 4 çıpası) **denominatörden düşer** ve precision sessizce yükselir.
Yanlış pozitifi ölçümün dışına atan bir precision formülü, ölçmediği şeyi
ölçüyor sanır.
**Düzeltme:** `LEFT JOIN`; `g.event_id IS NULL` → yanlış pozitif sayılır.
→ ADR-31

### H-2 · BÖLÜM D, `integrity_findings`'te tekillik kısıtı yok

Tabloda yalnızca `finding_id` PK ve iki normal indeks var (doğrulandı: 3 indeks,
0 UNIQUE, tablo boş). Kafka at-least-once semantiğinde tüketici çökerse parti
yeniden işlenir → **aynı bulgu iki kez yazılır** → F.5'in precision
denominatörü şişer ve ölçüm sessizce düşer.
**Düzeltme:** `UNIQUE (run_id, event_id, rule_id)` + `ON CONFLICT DO NOTHING`.
→ ADR-31

### H-3 · BÖLÜM H, kapsam düşürme listesi ölçümle çelişiyor

"S6 kural sayısı (5 → 3: envanter, hız, zaman)" — hız kuralı hem en pahalı
(3 SP) hem en zayıf (precision %72–83), aktivite kuralı hem en ucuz (1 SP) hem
kusursuz (%100/%100).
**Düzeltme:** feda listesi "envanter, zaman, aktivite" olmalı.

### Ç-1 · ADR-09 + E.6 ↔ F.5: kural 4'ün çıpası yok

ADR-09/E.6 kural 4'ün olayı **sildiğini** söylüyor; F.5 recall'ü
`f.event_id = g.event_id` ile ölçüyor. S4, silinen olayın kimliğini
(`UUIDv5(run_id‖agent_id‖tick‖seq)`) üretemez — `agent_id`, `tick` ve `seq`'in
hiçbirini görmez. İkisi bir arada doğru olamaz.
**Düzeltme:** kural 4 olay-çıpalı bulgu sınıfından çıkarılır. → ADR-30

### Ç-2 · BÖLÜM C.2 ↔ ölçüm: S4 yalnızca akış tüketicisi olarak çizilmiş

Kural 5 akışta çalışmaz (ilk-kayıt tuzağı → precision %50), kural 3 toplu modda
çalışmaz (precision %8). Tek modlu bir S4 iki kuraldan birini kaybeder.
**Düzeltme:** melez model (akış + toplu). → ADR-27

### E-1 · BÖLÜM G, E05'in 8 SP'si üç iş kalemini içermiyor

Kural motoru (öncelik, atıf, kanıt üretimi), servis bağlantısı ve dört senaryo
koşumu planlanmamış. Gerçek kapsam 17 SP.
**Düzeltme:** Bölüm 1.2'deki task ayrıştırması.

### E-2 · BÖLÜM D, `margin` ve `evidence` semantiği tanımsız

- Kural 1'de eşik yok — `margin` ne olacak?
- `Δt = 0` → hız sonsuz → Go'nun `json.Marshal`'ı `+Inf` için **hata** döner ve
  bulgu hiç yazılmaz. `margin FLOAT NOT NULL` de `Infinity` alır ama
  karşılaştırmalarda zehirlidir.
- "Adli açıklanabilirlik" iddiası şemasız, sürümsüz bir JSONB ile savunulamaz.
**Düzeltme:** kural başına birim + sonluluk üst sınırı + `{"v":1}` sürümü.
→ ADR-31

### E-3 · ADR-14/ADR-24: S4'ün örnekleme dışı olduğu hiç yazılmamış

Örnekleme analize aittir. S4'te uygulanırsa kural 2/3/5'in abone dizisi delik
deşik olur ve F.5 recall denominatörü (tüm enjekte olaylar) ile uyuşmaz.
**Düzeltme:** S4'te `sampling.Policy` alanı **hiç bulunmaz**; ADR-27'de yazılı.

### E-4 · ADR-23: S4 için sayaç simetrisi yok

`published_events` ve `analyzed_events` var, S4 için karşılığı yok. Yarım koşan
bir S4 düşük recall üretir ve bu **bilimsel bulgu gibi görünür** — Sprint 5
hatası #4'ün birebir tekrarı.
**Düzeltme:** `run_config.inspected_records` + `verify_integrity` 5. denetimi.
→ ADR-31

### E-5 · BÖLÜM D rol yetkilendirmesi: `svc_integrity` sayacı yazamaz

`svc_integrity`'nin `run_config` üzerinde yalnızca SELECT hakkı var.
**Düzeltme:** `GRANT UPDATE (inspected_records) ON run_config TO svc_integrity`.
→ migration 006

### E-6 · `tests/isolation/import_graph_test.go` S4'ü denetlemiyor

Yalnızca `internal/analysis` ağacı denetleniyor. `internal/integrity` →
`internal/simulator` (özellikle `injector`!) ve → `internal/analysis` yasakları
test edilmiyor.
**Düzeltme:** T-E05-07 kapsamında iki test daha.

### E-7 · Kural kimlikleri tek sözleşmede değil

Enjektör kimliklerini `internal/simulator/event/injector` tanımlıyor; dedektör
`internal/integrity` içinde olacak ve ikisi birbirini import edemez (ADR-20).
F.5 `g.injected_rule = f.rule_id` eşitliğine dayanıyor — bu bir sözleşmedir,
tesadüf olmamalı. `pkg/ta` / `pkg/split` / `pkg/htswire`'ın kurulmasına yol açan
**birebir aynı ikiz tuzağı**.
**Düzeltme:** `pkg/integrityrule`. → T-E05-00

### E-8 · BÖLÜM L config'te tespit parametreleri yok

`integrity:` bölümünde yalnızca `max_velocity_kmh`, `injection_rate`,
`rule_weights` var. Tespit tarafının hiçbir parametresi yok.
**Düzeltme:** `integrity.detection.*` bölümü, **kod yazılmadan** Gün 0'da.

### E-9 · BÖLÜM I test planı S4 için katman tanımlamıyor

"Bütünlük" satırı yalnızca `make verify-integrity`'yi anıyor; kural testleri,
gölge SQL çapraz kontrolü, akış sıralaması ön koşulu yok.
**Düzeltme:** Bölüm 12.

### E-10 · BÖLÜM C.1: 300 km/h eşiği recall hesabı yapılmadan sabitlenmiş

Eşik fiziksel olarak savunulabilir (yüksek hızlı tren mertebesi) ama
kombinasyonu ölçülmemiş: 2,4 saatlik olay aralığında 10–40 km atlama
**mümkündür**. Eşik yanlış değil, **recall tavanı hesaplanmamış**.
**Düzeltme:** eşik korunur, duyarlılık eğrisi (100/200/300 km/h) ek olarak
raporlanır.

### B-1 · BÖLÜM J, K7'nin "kural bazında" ifadesi tanımsız durumlar içeriyor

`NULLIF(count(*),0)` → sıfır bulguda precision NULL; "≥ %90" NULL üzerinde
tanımsız. Katı üçlü sınamada kural 2 **tek bulguyla %100 precision** veriyordu
— teknik olarak "geçti", bilimsel olarak boş.
**Düzeltme:** ölçümden önce beyan: 0 bulgu → "ölçülemedi"; < 30 bulgu → Wilson
%95 aralığıyla + "istatistiksel olarak yetersiz"; ≥ 30 → eşik uygulanır.
→ ADR-31

---

## 3. ADR-27..31 hangi problemleri çözüyor

### ADR-27 — Bütünlük tespiti melez çalışma modeli

**Çözdüğü problem.** Planın tek modlu S4 tasarımı (C.2) iki kuraldan birini
yapısal olarak kaybediyor: kural 3'ün kanıtı yalnızca varış sırasında var
(toplu modda precision %8), kural 5'in kanıtı yalnızca kapalı popülasyonda var
(akışta precision %50).

**Kararın içeriği.** Kural sınıfı `pkg/integrityrule`'da **tipte** ifade edilir:
`ClassStream` / `ClassBatch` / `ClassAggregate`. Motor bir kuralı yanlış fazda
koşturamaz — yorum değil, derleme/kurulum zamanı kısıtı. S4 örnekleme yapmaz;
`sampling.Policy` S4'ün yapılandırmasında **bulunmaz**.

**Kapattığı riskler.** E-3 (örnekleme sızması), Ç-2 (tek modlu tasarım),
kural 5'in akışta yazılması (precision %100 → %50).

---

### ADR-28 — Kural motoru: kanıt gücü önceliği ve tek-atıf

**Çözdüğü problem.** F.5 precision'ı **tam eşleşme** ile ölçüyor
(`g.injected_rule = f.rule_id`). Ölçüm iki yapısal sızıntı gösterdi:

1. **Çapraz bulaşma:** kural 3 olayları (kaydırılmış damga) kinematik olarak
   imkânsız geçiş de üretiyor → kural 2 tetikleniyor → F.5'e göre yanlış pozitif
   (kentsel 21, kırsal 24 olay).
2. **Çift ucu işaretleme:** hız ihlali bir *geçişin* özelliği; kayıt bazlı kural
   iki ucu da işaretliyor → precision yapısal olarak %50'nin altına iniyor.
   Kentselde birebir doğrulandı: `58 (kural2) + 21 (kural3) = 79 = temiz bulgu`.

**Kararın içeriği.**

*Öncelik sırası — kanıt gücüne göre, keyfi değil:*

| Sıra | Kural | Kanıt tipi | Eşik | Atıf belirsizliği |
|---|---|---|---|---|
| 1 | 1 envanter | küme üyeliği | yok | yok |
| 2 | 5 aktivite | kapalı popülasyonda çoğunluk | yok | yok |
| 3 | 3 zaman | varış sırası ↔ olay zamanı | yok | yok |
| 4 | 2 hız | türetilmiş oran | **var** | **var** |

*İki değişmez:*
- **Bir olay → en çok bir kanonik bulgu.** Yüksek öncelikli kural olayı
  *talep eder* (claim); alt sıradaki kurallar o olayı değerlendirmez.
- **Bir geçiş → en çok bir bulgu.** Karar verilemezse **bulgu üretilmez**:
  adli bir sistem iki tarafı birlikte suçlamaz.

*Bastırma bilimi gizlemez.* Motor **tüm** isabetleri yazar; kaybedenler
`suppressed_by` sütunuyla işaretlenir.
- F.5 kanonik ölçümü: `WHERE suppressed_by IS NULL` (ölçümden önce beyan).
- Karışıklık matrisi: tüm satırlar. Bastırma bir filtre değil, **etiket**.

**Kapattığı riskler.** Çapraz bulaşma, çift-uç işaretleme, "bastırma ile
precision şişirme" itirazı.

---

### ADR-29 — Kural 2 atıf mekanizması ve karar verilemezlik

**Çözdüğü problem.** Naif hız kuralı precision %36. Katı üçlü sınama %100 ama
recall %0,08–0,59 (1–7 bulgu — istatistiksel olarak boş). İkisinin arasında bir
tanım gerekiyor ve bu tanım **ölçümden önce donduralmalı**, yoksa etiketli veri
üzerinde %90'ı geçene kadar ayarlanır.

**Kararın içeriği (dondurulacak spesifikasyon).**

1. İhlal **geçişin** özelliğidir: `d(cell_i, cell_j) / Δt > max_velocity_kmh`.
2. `Δt = 0 ∧ d > 0` → gerçekten imkânsız; `margin` üst sınırla kapılır
   (`velocity_margin_cap`).
3. Kural 1 kayıtları zincirden çıkarılır (konumu bilinmiyor).
4. **Atıf — yerel destek:** ihlal eden geçişin hangi ucu abonenin yerel
   yörüngesinden uzaklaşıyorsa bulgu ona yazılır:
   `d(aday, dış_komşu) > d(eş, dış_komşu)`.
5. Karşılaştırma **katı eşitsizlik**tir; eşitlik/dış komşu yokluğu →
   karar verilemez → **bulgu üretilmez**.
6. Öncelik: kural 1/5/3 tarafından talep edilmiş olaylar değerlendirmeye
   girmez (ADR-28).

**Ölçülen sonuç (Sprint 5 verisi, beyan):** precision %71,8 (kentsel) /
%83,4 (kırsal); ADR-28 önceliğiyle ~%80 / ~%90. Recall %4,7 / %12,3 ve
**fiziksel tavan** — 2,4 saatlik aralıkta 10–40 km atlama mümkündür.

**Beyan.** Kural 2'nin K7 eşiğini tutmaması **beklenen** sonuçtur. Tutmazsa,
K1'de olduğu gibi önceden beyan edilmiş bir negatif bulgudur.

**Denenmiş ve kapatılmış yol:** TA/`r_max` tutarlılığı — dört koşuda sıfır
ihlal. Sprint 6'da tekrar denenmeyecek.

**Kapattığı riskler.** R1 (aşırı uydurma), R2 (precision), naif uygulama.

---

### ADR-30 — Kural 4'ün K7 kapsamı

**Çözdüğü problem.** İki bağımsız imkânsızlık:
- **Çıpa yok:** S4 silinen olayın `event_id`'sini üretemez → recall ≡ 0 ve
  komşuya çıpalanan her bulgu F.5'e göre yanlış pozitif.
- **Sinyal yok:** silme içeren boşluk medyanı 3,33 sa, temiz 1,33 sa;
  12 sa eşiğinde 73 doğru / 5114 yanlış → **precision %1,4**.

**Kararın içeriği.** Kural 4 `ClassAggregate`'tir: `integrity_findings`'e
olay bazlı satır **yazmaz**. Koşu düzeyinde bir gösterge üretir (abone başına
gözlenen sessizlik dağılımı → "kayıp kayıt kütlesi" tahmini). K7'nin precision
eşiği ona **uygulanmaz**; gerekçe bilgi teoriktir ve ölçümle belgelenir.
Uygulama **Sprint 7**'ye alınır.

**Reddedilen alternatif.** Enjektörü kural 4'ü tespit edilebilir kılacak şekilde
değiştirmek (blok silme, daha yüksek oran). Ölçüm sonucuna göre veri üretecini
değiştirmek BÖLÜM J disiplininin ihlalidir — Sprint 5'te K1 için yapılmayan şey
burada da yapılmaz.

**Kapattığı riskler.** Ç-1, R3; "K7 tutmadı" sonucunun düzeltilebilir bir
mühendislik hatası gibi görünmesi.

---

### ADR-31 — Bulgu veri modeli, idempotanslık, ölçüm sözleşmesi

**Çözdüğü problem.** Dört ayrı sessiz bozulma yolu:

| # | Bozulma | Sonuç |
|---|---|---|
| 1 | Yinelenen bulgu (at-least-once) | precision denominatörü şişer |
| 2 | F.5'in iç birleştirmesi | yanlış pozitif ölçümün dışına düşer |
| 3 | `+Inf` margin / evidence | `json.Marshal` hata verir, bulgu **hiç yazılmaz** |
| 4 | Yarım S4 koşusu | düşük recall bilimsel bulgu gibi görünür |

**Kararın içeriği.**
1. `UNIQUE (run_id, event_id, rule_id)` + `ON CONFLICT DO NOTHING`.
   `integrity_findings` **hypertable yapılmaz** — EK-02 gereği tekillik kısıtı
   bölümleme sütununu (`time`) içermek zorunda olurdu ve `(run_id, event_id,
   rule_id)` tekilliği kurulamazdı. Normal tablo olması idempotanslığın önkoşulu.
2. `margin` kural başına birim + `CHECK (margin >= 1.0 AND margin <= 1e6)`.
3. `evidence` sürümlü (`{"v":1,...}`), kural başına zorunlu alan listesi,
   **ground truth türevi alan yasak** (teste bağlanır).
4. F.5 → `LEFT JOIN` + `suppressed_by IS NULL`.
5. `run_config.inspected_records` + `verify_integrity` 5. denetimi.
6. `integrity_metrics` tablosu — `metrics` tablosunun anahtarı
   (scenario, method, confidence) kural bazlı satırları taşıyamaz.
7. K7 ölçülebilirlik kuralı: 0 bulgu → "ölçülemedi"; < 30 → Wilson %95 +
   "yetersiz"; ≥ 30 → eşik uygulanır.

**Kapattığı riskler.** H-1, H-2, E-2, E-4, E-5, B-1, R5, R6, R8.

---

## 4. Minimum riskli geliştirme sırası

### 4.1 Sıralamanın ilkesi

> **Gün 3'ün sonunda K7 üç yapısal kuralla uçtan uca ölçülebilir olmalı.**
> Bundan sonrası yukarı yönlü katkıdır; risk ondan öncesinde yoğunlaşmıştır.

Bu, sıralamayı bağımlılıktan değil **riskten** türetir:

1. **Sözleşme önce** (T-E05-00). Kural kimlikleri sabitlenmeden yazılan hiçbir
   kural F.5 ile eşleşeceğinden emin olamaz.
2. **Motor önce, kurallar sonra** (T-E05-01). Öncelik ve idempotanslık
   disiplinini beş yerde tekrarlamak, Sprint 5'te `internal/persist`'in tek
   çekirdekle çözdüğü hatanın tekrarı olurdu.
3. **En ucuz ve en kesin kural ilk** (T-E05-02, kural 1). Motoru gerçek veriyle
   doğrular; hata varsa %100 precision beklenen bir kuralda ortaya çıkar —
   teşhisi kolay.
4. **Mimariyi belirleyen kural ikinci** (T-E05-03, kural 3). Akış modunu
   zorunlu kılan tek kural; `cmd/integrity`'nin şeklini o belirliyor. Sonraya
   bırakılırsa servis yeniden yazılır.
5. **Ölçüm hattı kurallardan hemen sonra** (T-E05-07 + T-E04-09). Sprint 5'in
   dersi: "Kafka'ya yayınlamak şemayı doğrulamıyor." Ölçüm hattı kurulmadan
   hiçbir kuralın doğru çalıştığı bilinemez.
6. **Riskli kural en sonda** (T-E05-05, kural 2). Kesilebilir konumda; kesilirse
   K7 üç kuralla yine ölçülmüş olur.

### 4.2 Bağımlılık grafiği

```
        T-E05-00  pkg/integrityrule  (1)
                       │
                       ▼
        T-E05-01  motor + sink + migration 006  (3)   ◄── tüm kuralların önkoşulu
                       │
         ┌─────────────┼─────────────┐
         ▼             ▼             ▼
   T-E05-02 ★     T-E05-03 ★     T-E05-04
   kural 1 (1)    kural 3 (2)    kural 5 (1)
   envanter       zaman/akış     aktivite/toplu
         │             │             │
         └─────────────┼─────────────┘
                       ▼
        T-E05-07 ★  cmd/integrity (akış + toplu)  (2)
                       │
         ┌─────────────┴─────────────┐
         ▼                           ▼
   T-E04-09 ★                  ┌── T-E05-05  kural 2 (3)  ⚠ KESİLEBİLİR
   F.5 + matris (2)            │   (kod: 01'e bağlı, sıra: kapıya bağlı)
         │                     │
         └──────────┬──────────┘
                    ▼
        T-E05-08 ★  4 senaryo koşumu + K7 raporu  (2)

★ kritik yol (14 SP)      ⚠ koşullu
```

**Kritik yol:** `00 → 01 → 03 → 07 → 04-09 → 08` = **12 SP**
Kural 2 kritik yolda **değil**; gecikirse sprint hedefi daralır, sprint düşmez.

### 4.3 Gün 3 kapısı (kesin karar noktası)

Gün 3 sonunda şu üçü sağlanmalı:

- [ ] `configs/smoke.yaml` (100 ajan × 3 gün) ile uçtan uca koşum yeşil
- [ ] `integrity_findings` üç kuraldan bulgu içeriyor, idempotanslık testi geçti
- [ ] `integrity_metrics` dolu; F.5 kanonik ölçümü çalışıyor

**Sağlanıyorsa:** Gün 4 = kural 2.
**Sağlanmıyorsa:** kural 2 Sprint 7'ye; Gün 4 = eksik iş + Gün 5 = koşum.
K7 üç kuralla ölçülmüş olarak kapanır.

---

## 5. Sprint 6'da kesin yapılacaklar

| Task | Neden vazgeçilemez |
|---|---|
| **T-E05-00** | Kural kimliği sözleşmesi olmadan F.5'in `g.injected_rule = f.rule_id` eşitliği tesadüfe bırakılır; ikiz tuzağı |
| **T-E05-01** | Motor + migration 006 olmadan hiçbir bulgu güvenilir yazılamaz (idempotanslık, öncelik, sonluluk) |
| **T-E05-02** | K7'nin en ucuz ve en kesin ayağı (%100/%100, 1 SP); motorun gerçek veriyle ilk doğrulaması |
| **T-E05-03** | Akış modunu zorunlu kılan tek kural; mimariyi belirliyor. Sonraya kalırsa servis yeniden yazılır |
| **T-E05-04** | %100/%100, 1 SP; toplu fazı ilk kez kurar ve melez modeli doğrular |
| **T-E05-07** | Servis bağlanmadan hiçbir ölçüm üretilemez; K6 katman 2 ilk kez üretim yolunda sınanır |
| **T-E04-09** | K7'nin ölçüm aracı; Sprint 5'ten devir, kapanmalı |
| **T-E05-08** | K5 dört senaryoyu zorunlu kılıyor; kentsel/kırsal ayrımı recall'u 2,5 kat değiştiriyor |

**Kural 2 (T-E05-05) hakkında — atomiktir.** Yalnızca geçiş tespitini yazıp
atfı sonraya bırakmak, precision %36'lık bir kural üretir; bu, kuralı hiç
yazmamaktan **kötüdür** (K7 raporuna %36 precision'lı bir satır girer). Ya
ikisi birlikte ya hiç.

---

## 6. Sprint 7'ye bırakılacaklar

| İş | SP | Gerekçe |
|---|---|---|
| **T-E05-06** kural 4 göstergesi | 2 | ADR-30 ile K7 kapsamı dışı; olay-çıpalı bulgu üretemiyor. Koşu düzeyi istatistik, ölçüm hattı kurulduktan sonra ucuz |
| **Kafka ACL** (K6 katman 1) | 2 | Broker authorizer yapılandırması; E05'in yolu üzerinde değil, ama K6'yı formel olarak kapatan tek iş |
| **Arama bölgesi ADR-34** (K1) | 5+ | Analiz modeli değişikliği + 4 senaryo yeniden kalibrasyon. E05'ten tamamen bağımsız (S4 `estimates`'e dokunmuyor). Aynı sprinte konursa ikisi birbirini yer |
| Kalibrasyon erken çıkış | 1 | Sprint 5 borcu #4; S6 koşumları `HTS_CALIBRATE=false` ile zaten atlıyor |
| `median_haversine_m` totolojisi | 1 | Şema değişikliği; F.5 kullanmıyor |
| Kural 2 (kapı düşerse) | 3 | Gün 3 kapısı sağlanmazsa |

---

## 7. K7'yi sağlayacak en güvenli mimari

Yedi savunma katmanı — her biri belirli bir sessiz bozulma yolunu kapatıyor:

### K-1 · Precision'ı yapısal kurallar üzerine kur
Kural 1, 3, 5'in precision'ı **tasarımdan** %100: eşik yok, atıf yok,
ayarlanacak parametre yok. K7'nin gövdesi bunlar. Kural 2 ölçülür ve raporlanır,
K7'yi tek başına taşımaz.

### K-2 · Kanıt gücü önceliği + tek-atıf (ADR-28)
Çapraz bulaşmayı ve çift-uç işaretlemeyi **yapısal olarak** ortadan kaldırır —
eşik ayarıyla değil. Ölçüm: kentselde 79 temiz yanlış pozitifin tamamı bu iki
mekanizmadan geliyordu.

### K-3 · Karar verilemezlik → bulgu yok (ADR-29)
Adli ilke: kanıt tek bir kaydı göstermiyorsa suçlama yapılmaz. Precision'ı
recall pahasına korur ve bu değiş-tokuş **önceden beyan edilmiştir**.

### K-4 · Veritabanı düzeyinde idempotanslık (ADR-31)
`UNIQUE (run_id, event_id, rule_id)`. Uygulama hatası, çökme veya yeniden
işleme precision denominatörünü **bozamaz**. Doğruluk kod disiplinine değil
şema kısıtına bağlanır.

### K-5 · Tamlık sayacı (ADR-31)
`inspected_records = published_events`. Yarım koşan bir S4'ün düşük recall'u
bilimsel bulgu gibi görünemez — `verify_integrity` FAIL verir.

### K-6 · Kural sınıfının tipte ifadesi (ADR-27)
Kural 5 akışta koşulamaz, S4'e örnekleme takılamaz. Yorumla değil,
kurulum zamanı hatasıyla.

### K-7 · Gölge SQL çapraz kontrolü
Her toplu kural iki kez uygulanır: Go dedektörü + SQL sorgusu. İkisi aynı bulgu
kümesini vermeli. Sprint 5'in PostGIS↔Haversine çapraz kontrolünün (%0,24 fark)
karşılığı; uygulama hatasının "bilimsel bulgu" olarak raporlanmasını engeller.

### Ve mimari olmayan sekizinci katman: beyan-sonra-ölç disiplini
Tüm eşikler Gün 0'da ADR'lere yazılır. F.5 bir kez hesaplanır. Sonuç ne olursa
raporlanır. **En büyük risk teknik değil; etiketli veri elimizde olduğu için
kuralı %90'ı geçene kadar ayarlamak teknik olarak kolay ve bilimsel olarak
geçersizdir.**

---

## 8. Rule Engine mimarisi

### 8.1 Paket yerleşimi

```
pkg/integrityrule/                     ← bağımlılıksız sözleşme (stdlib + uuid)
    rule.go        ID, Name, Class, All()

internal/integrity/
    detector/
        rule.go        Rule arayüzleri, Finding, Hit
        engine.go      faz yürütücü, öncelik uygulaması
        claims.go      olay talep defteri (claim ledger)
        state.go       abone başına akış durumu (watermark)
        window.go      abone dizisi kayan penceresi (toplu)
        evidence.go    kanıt kurucusu + sonluluk koruması
        inventory.go   kural 1   (ClassStream)
        timeorder.go   kural 3   (ClassStream)
        activity.go    kural 5   (ClassBatch)
        velocity.go    kural 2   (ClassBatch)
        attribution.go kural 2 atıf mantığı
    source/
        stream.go      Kafka tüketicisi → akış kuralları
        batch.go       PostgreSQL sıralı tarama → toplu kurallar
        inventory.go   Redis'ten minimal hücre görünümü
    sink/
        findings.go    tamponlu, idempotent yazıcı
```

**İçe alma yasakları (teste bağlanır):**
`internal/integrity` ↛ `internal/simulator` (özellikle `injector`)
`internal/integrity` ↛ `internal/analysis`
`pkg/integrityrule` ↛ `internal/*`

> **Kural kimliğini paylaşmak kör testi delmiyor.** S4 hangi kuralı
> *uyguladığını* bilmek zorundadır; hangi kaydın enjekte edildiğini öğrenmez.
> Etiket `ground_truth`'ta kalır ve S4 o tabloya erişemez (katman 2), o topic'e
> abone olmaz (katman 1), o kodu import edemez (katman 4).

### 8.2 Sözleşme tipleri

```
ID     : 1..5
Class  : ClassStream | ClassBatch | ClassAggregate
Name   : kanonik ad (≤ 50 karakter — integrity_findings.rule_name)

Hit    : { RuleID, EventID, Time, Scenario, Margin, Evidence }
Finding: Hit + { DetectedIn, SuppressedBy *ID }
```

`Hit` kuralın ürettiği ham isabet; `Finding` motorun öncelik uyguladıktan sonra
yazdığı satır. Ayrım önemli: kural bastırmayı **bilmez**, motor uygular.

### 8.3 Kural arayüzleri

İki arayüz, çünkü iki farklı girdi şekli var:

```
StreamRule:                          BatchRule:
  ID()    ID                           ID()      ID
  Observe(rec) []Hit                   Evaluate(seq SubscriberSequence) []Hit
  ── varış sırasında tek kayıt         ── bir abonenin olay-zamanı sıralı dizisi
  ── abone başına O(1) durum           ── kayan pencere (5 kayıt) + dizi özeti
```

`ClassAggregate` (kural 4, Sprint 7) üçüncü bir arayüz alır: `Summarize(run)
→ RunIndicator`; `integrity_findings`'e yazmaz.

### 8.4 Öncelik ve talep defteri (claim ledger)

```
öncelik sırası (ADR-28):  1 envanter  →  5 aktivite  →  3 zaman  →  2 hız
```

Motor akışı:

```
FAZ 1 (akış)
  her kayıt için:
      hits ← [kural 1, kural 3] sırayla Observe(rec)
      motor: hits'i öncelik sırasına dizer
             ilk isabet   → kanonik  (suppressed_by = NULL)
             sonrakiler   → bastırılmış (suppressed_by = kanonik kural)
      defter[event_id] = kanonik kural
      sink.Write(findings)

FAZ 2 (toplu)
  başlangıç:
      defter ← integrity_findings'ten YÜKLENİR (run_id, event_id, rule_id
                WHERE suppressed_by IS NULL)
  her abone dizisi için:
      hits ← [kural 5, kural 2] sırayla Evaluate(seq)
      motor: defterde kaydı olan olayın isabeti bastırılmış yazılır
             defterde yoksa öncelik içi sıralama uygulanır
      sink.Write(findings)
```

**Defterin veritabanından yüklenmesi bilinçli bir karardır.** Bellekte
taşınsaydı toplu fazı tek başına yeniden koşmak (hata ayıklamada normal ihtiyaç)
öncelik bilgisini kaybederdi. Veritabanı üzerinden devir, toplu fazı bağımsız
olarak yeniden koşulabilir ve **denetlenebilir** kılar.

**Önkoşul:** toplu faz, akış fazının bu koşu için tamamlandığını denetler
(`run_config.inspected_records IS NOT NULL`). Aksi hâlde kural 1'in dışlamaları
eksik olur ve kural 2 bilinmeyen konumlu kayıtları zincire alır. Reddedilir,
sessizce koşulmaz — ADR-04'ün önkoşul disiplini.

### 8.5 Durum yönetimi ve bellek sınırları

| Faz | Durum | Boyut | Sınırlı mı |
|---|---|---|---|
| Akış | `map[pseudo_msisdn]watermark` | 1000 abone × ~40 bayt ≈ **40 KB** | Evet, abone sayısıyla |
| Akış | hücre kimliği kümesi (kural 1) | ~450 hücre × 16 bayt ≈ **7 KB** | Evet |
| Toplu | tek abonenin dizisi + 5'lik pencere | ~300 kayıt ≈ **30 KB** | Evet, abone başına |
| Toplu | hücre konum haritası (kural 2) | ~450 × 40 bayt ≈ **18 KB** | Evet |
| Motor | talep defteri | ~4.000 bulgu × 40 bayt ≈ **160 KB** | Evet, bulgu sayısıyla |

Toplam < 1 MB. **Hiçbir durum olay sayısıyla büyümüyor** — 300.000 olaylık koşu
ile 3.000.000 olaylık koşu aynı belleği kullanır.

### 8.6 Ölçeklenebilirlik

Akış kuralları abone anahtarına göre durumludur ve Kafka anahtarı
`pseudo_msisdn`'dir → **bir abonenin tüm kayıtları tek partition'a düşer**.
Dolayısıyla replikalar arası durum paylaşımı gerekmez: 4 partition → 4 replika,
konuşmasız. Bu, K9 (S8, Docker Compose replika ölçümü) için hazır bir yapıdır
ve tesadüf değil — `pkg/kafka`'daki anahtar seçimi zaten bu gerekçeyle
yapılmıştı.

Toplu kurallar koşu başına bir kez çalışır; paralellik gerektirmez
(ölçüldü: tek sıralı tarama, 300K satır, saniyeler mertebesinde).

### 8.7 Kanıt üretimi ve sonluluk koruması

```
evidence.Build(ruleID, fields) → (JSONB, margin)
    her sayısal alan için:  IsInf ∨ IsNaN → cap (1e6) + "capped": true
    şema sürümü:            {"v": 1, "rule": N, ...}
    yasak alan denetimi:    ground truth türevi anahtar adları reddedilir
```

`+Inf` koruması isteğe bağlı bir incelik değil: `Δt = 0` durumunda hız
sonsuzdur, Go'nun `json.Marshal`'ı `+Inf` için **hata döner** ve bulgu hiç
yazılmaz. Kentselde >300 km/h isabetlerin **tamamı** bu popülasyondadır —
koruma olmadan kural 2 kentselde sıfır bulgu üretir.

---

## 9. Streaming ve Batch birlikte nasıl işler

### 9.1 Faz dağılımı ve gerekçeleri

| Faz | Girdi | Sıra | Kurallar | Neden bu fazda |
|---|---|---|---|---|
| **Akış** | Kafka `hts.records` | **varış** sırası | 1 envanter, 3 zaman | Kural 3'ün kanıtı varış sırası ↔ olay zamanı çelişkisidir; toplu modda kanıt **yok olur** (precision %8) |
| **Toplu** | `hts_records` tablosu | **olay zamanı**, abone bazlı | 5 aktivite, 2 hız | Kural 5 kapalı popülasyon gerektirir (akışta ilk-kayıt tuzağı precision'ı %50'ye düşürür); kural 2 zaman-sıralı 5'lik pencere ister |
| *Toplulaştırma* | `hts_records` | koşu düzeyi | *4 yörünge (Sprint 7)* | Olay çıpası yok; koşu istatistiği |

Kural 1 akış fazındadır: durumsuz olduğu için maliyeti yok, talebi hemen
kullanılabilir ve gerçek zamanlı yeteneği gösterir.

### 9.2 Zamanlama ve koşum sırası

```
1  cmd/simulator                     yayınlar → hts.records, hts.groundtruth
   ├─────────────────────────────────────────────────────────┐
2  cmd/persister (records)      ∥   cmd/persister (gt)   ∥   cmd/integrity (stream)
   grup: run-records-$RUN            grup: run-gt-$RUN       grup: run-integrity-$RUN
   → hts_records                     → ground_truth          → integrity_findings
   └─────────────────────────────────────────────────────────┘
                     ↓ hepsi idle-çıkış ile biter
3  cmd/integrity (batch)             önkoşul: hts_records tam + inspected_records yazılı
                                     → integrity_findings (kural 5, 2)
4  cmd/analysis-engine               (bütünlükten bağımsız — paralel de olabilir)
5  cmd/validation  (F.1–F.4 + F.5)   → metrics, integrity_metrics
6  verify_integrity                  5 denetim
```

**Akış fazı persister ile paralel koşar.** Ayrı tüketici grubu, aynı topic —
Kafka semantiği bunu doğrudan destekler. Kazanç sadece duvar saati değil:
gerçek topolojiyi (canlı akış üzerinde bütünlük izleme) test ediyor olmak,
"post-hoc bir SQL sorgusu yazdık" iddiasından bilimsel olarak farklıdır.

**Toplu faz iki önkoşulu denetler:**
1. `count(hts_records WHERE run_id) = run_config.published_events`
   → records persister bitti (kayıtlar tam).
2. `run_config.inspected_records IS NOT NULL`
   → akış fazı bitti (kural 1/3 talepleri yazılı).

İkisinden biri sağlanmazsa toplu faz **reddeder**. Sessizce eksik veriyle
koşmak, düşük recall'u bilimsel bulgu gibi gösterir.

### 9.3 Servis modları

`cmd/integrity`, `cmd/persister`'ın kurulmuş desenini izler:

| `HTS_INTEGRITY_MODE` | Davranış |
|---|---|
| `stream` | Kafka tüketicisi; kural 1 + 3; `inspected_records` yazar; `HTS_IDLE_TIMEOUT` ile çıkar |
| `batch` | Önkoşul denetimi → `hts_records` sıralı tarama; kural 5 + 2 |
| `all` | `stream` sonra `batch` (yerel geliştirme kolaylığı) |

Ortak: `HTS_RUN_ID` zorunlu (`run_id` süzgeci — Sprint 5 hatası #3),
DSN rolü **`svc_integrity`** (K6 katman 2'nin üretim yolunda sınanması),
health `:8084`, OTel + `traceparent` çıkarımı, `audit_log` yazımı.

### 9.4 Faz sınırındaki kenar durumlar

| Durum | Davranış | Gerekçe |
|---|---|---|
| Akış fazı yeniden başlar | watermark sıfırlanır; ilk kayıtlar bulgu üretmez | Recall düşer, **precision bozulmaz**; raporlanır |
| Aynı parti iki kez işlenir | `ON CONFLICT DO NOTHING` | Idempotanslık şemada |
| Toplu faz iki kez koşar | Aynı bulgular, satır sayısı değişmez | Defter DB'den yüklendiği için öncelik de aynı |
| Yabancı koşunun kaydı gelir | `run_id` süzgeci atlar, `foreign` sayacı artar | Topic koşular arası paylaşılıyor |
| Kural 1 kaydı toplu faza gelir | Kural 2 zincirinden çıkarılır | Konumu bilinmiyor; NULL konum zinciri kırardı |
| Tek kayıtlı abone | Kural 2/3 bulgu üretmez, kural 5 üretmez | Komşusu yok — karar verilemez |

---

## 10. `integrity_findings` son veri modeli

```sql
-- 001_schema.sql'deki tanım + 006 eklentileri (birleştirilmiş son hâli)
CREATE TABLE integrity_findings (
    finding_id    BIGSERIAL    PRIMARY KEY,
    run_id        UUID         NOT NULL,
    event_id      UUID         NOT NULL,
    time          TIMESTAMPTZ  NOT NULL,
    rule_id       INTEGER      NOT NULL CHECK (rule_id BETWEEN 1 AND 5),
    rule_name     VARCHAR(50)  NOT NULL,
    margin        FLOAT        NOT NULL,
    evidence      JSONB        NOT NULL,
    scenario      CHAR(1)      NOT NULL CHECK (scenario IN ('A','B','C','D')),

    -- ── 006 (ADR-28, ADR-31) ────────────────────────────────────────────
    detected_in   VARCHAR(6)   NOT NULL DEFAULT 'stream'
                               CHECK (detected_in IN ('stream','batch')),
    suppressed_by INTEGER      NULL CHECK (suppressed_by BETWEEN 1 AND 5),

    CONSTRAINT integrity_findings_margin_range
        CHECK (margin >= 1.0 AND margin <= 1e6),
    CONSTRAINT integrity_findings_suppression_sane
        CHECK (suppressed_by IS NULL OR suppressed_by <> rule_id),
    CONSTRAINT integrity_findings_evidence_versioned
        CHECK (evidence ? 'v'),
    CONSTRAINT integrity_findings_uniq
        UNIQUE (run_id, event_id, rule_id)
);

CREATE INDEX integrity_findings_run_rule   ON integrity_findings (run_id, rule_id);
CREATE INDEX integrity_findings_run_event  ON integrity_findings (run_id, event_id);
CREATE INDEX integrity_findings_canonical  ON integrity_findings (run_id, rule_id)
                                            WHERE suppressed_by IS NULL;   -- F.5
```

### 10.1 Alan gerekçeleri

| Alan | Karar | Neden |
|---|---|---|
| `finding_id BIGSERIAL` | korundu | Bulgu doğal anahtarı (run, event, rule) zaten tekil; yüzey anahtarı adli referans için pratik |
| **normal tablo, hypertable değil** | korundu | EK-02: hypertable'da tekillik kısıtı `time`'ı içermek **zorunda**; o zaman `(run_id, event_id, rule_id)` tekilliği kurulamaz ve idempotanslık imkânsız olur. Bulgular seyrek (~4.000/koşu) — zaman bölümlemesinin kazancı yok |
| `time` | korundu | Kaydın olay zamanı (bulgunun yazılma zamanı değil) — adli zaman çizelgesi |
| `margin` + CHECK | **yeni kısıt** | `+Inf` zehirliliği; alt sınır 1,0 (eşik aşılmadan bulgu olmaz) |
| `evidence` + `? 'v'` | **yeni kısıt** | Sürümsüz JSONB ile "adli açıklanabilirlik" savunulamaz |
| `detected_in` | **yeni** | Hangi fazın bulduğu; akış/toplu ayrımının denetlenebilirliği ve kural sınıfı ihlalinin yakalanması |
| `suppressed_by` | **yeni** | ADR-28: bastırma bir **etiket**, filtre değil. Karışıklık matrisi tüm satırları, F.5 `IS NULL` olanları kullanır |
| `UNIQUE (run_id, event_id, rule_id)` | **yeni** | Idempotanslık; her kural bir olay için en çok bir isabet üretir |
| kısmi indeks | **yeni** | F.5 kanonik sorgusunun sıcak yolu |

### 10.2 `margin` sözleşmesi (kural başına)

| Kural | `margin` | Değer aralığı | Not |
|---|---|---|---|
| 1 envanter | sabit `1.0` | 1,0 | Eşik yok, ikili karar — 1,0 "kural tetiklendi" demektir |
| 2 hız | `v_ölçülen / max_velocity_kmh` | 1,0 – 1e6 | `Δt = 0` → cap; `evidence.zero_interval = true` |
| 3 zaman | `geri_gidiş_saniye / tick_saniye` | 1,0 – 1e6 | 2 saat geri, 5 dk tick → 24,0 |
| 5 aktivite | abonenin farklı IMEI sayısı | 2,0 – … | Tek IMEI'de bulgu olmaz |
| *4 yörünge* | *bulgu üretmez* | — | ADR-30 |

### 10.3 `evidence` şeması (v1, kural başına zorunlu alanlar)

```
kural 1: {"v":1,"rule":1,"cell_id":UUID,"inventory_size":int}
kural 2: {"v":1,"rule":2,"from_cell":UUID,"to_cell":UUID,"distance_m":float,
          "delta_s":float,"velocity_kmh":float,"threshold_kmh":300,
          "attribution":"local_support","peer_event_id":UUID,
          "zero_interval":bool,"capped":bool}
kural 3: {"v":1,"rule":3,"record_time":RFC3339,"watermark":RFC3339,
          "backstep_s":float,"prev_event_id":UUID}
kural 5: {"v":1,"rule":5,"imei":string,"modal_imei":string,
          "imei_count":int,"support":int}
```

**Değişmez:** hiçbir kanıt alanı ground truth türevi olamaz (gerçek konum,
`agent_id`, `injected_rule`, `partition_key`, `covered`). Yasaklı anahtar
listesi teste bağlanır — kör testin kanıt katmanındaki ifadesi.

### 10.4 `integrity_metrics` (K7 çıktısı)

```sql
CREATE TABLE integrity_metrics (
    metric_id      BIGSERIAL   PRIMARY KEY,
    run_id         UUID        NOT NULL,
    computed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    scenario       CHAR(1)     NOT NULL,
    rule_id        INTEGER     NOT NULL CHECK (rule_id BETWEEN 1 AND 5),
    rule_name      VARCHAR(50) NOT NULL,
    findings       BIGINT      NOT NULL,   -- kanonik bulgu sayısı
    true_positives BIGINT      NOT NULL,   -- injected_rule = rule_id
    injected       BIGINT      NOT NULL,   -- recall denominatörü
    precision      FLOAT,                  -- NULL = ölçülemedi (findings = 0)
    recall         FLOAT       NOT NULL,
    precision_lo   FLOAT,                  -- Wilson %95 alt sınırı
    precision_hi   FLOAT,                  -- Wilson %95 üst sınırı
    sufficient     BOOLEAN     NOT NULL,   -- findings >= 30 (B-1 kuralı)
    UNIQUE (run_id, rule_id)
);
```

`precision` NULL kalabildiği için "≥ %90" karşılaştırması `sufficient` ile
birlikte yorumlanır — B-1'in çözümü şemada da görünür.

---

## 11. Migration planı

### 11.1 Ön koşul doğrulaması (yapıldı)

```
integrity_findings satır sayısı : 0
mevcut indeksler                : pkey, run_rule, run_event  (UNIQUE yok)
run_config sütunları            : ... published_events, analyzed_events (005)
```
Tablo boş olduğu için `UNIQUE` kısıtı **çakışma riski olmadan** eklenebilir.

### 11.2 `006_integrity_detection.sql` — tek dosya, yeniden koşulabilir

`make migrate-up` tüm `*.sql`'leri sırayla uyguluyor; 006 da idempotent olmak
zorunda (mevcut dosyaların deseni: `IF NOT EXISTS` + `DO $$ ... pg_catalog
kontrolü $$`).

| Adım | İş | Yeniden koşulabilirlik |
|---|---|---|
| 1 | `ALTER TABLE integrity_findings ADD COLUMN IF NOT EXISTS detected_in`, `suppressed_by` | `IF NOT EXISTS` |
| 2 | `UNIQUE (run_id, event_id, rule_id)` | `DO` bloğu + `pg_constraint` kontrolü (PG'de `ADD CONSTRAINT IF NOT EXISTS` **yok** — 004'ün rol deseni) |
| 3 | 3 CHECK kısıtı (margin, suppression, evidence sürümü) | Aynı `DO` deseni |
| 4 | Kısmi indeks `... WHERE suppressed_by IS NULL` | `CREATE INDEX IF NOT EXISTS` |
| 5 | `ALTER TABLE run_config ADD COLUMN IF NOT EXISTS inspected_records BIGINT` + COMMENT | `IF NOT EXISTS` |
| 6 | `CREATE TABLE IF NOT EXISTS integrity_metrics` + indeks | `IF NOT EXISTS` |
| 7 | Grant'lar: `UPDATE (inspected_records) ON run_config → svc_integrity`; `SELECT, INSERT ON integrity_metrics → svc_validation`; `USAGE ON SEQUENCE integrity_metrics_metric_id_seq → svc_validation` | GRANT idempotenttir |
| 8 | `CREATE OR REPLACE FUNCTION verify_integrity` — 5. denetim eklenir | `OR REPLACE` |

### 11.3 `verify_integrity` 5. denetimi

```
inspected_vs_published_records:
    inspected_records IS NULL              → SKIP   (S4 bu koşuda koşmadı)
    inspected_records = published_events   → OK
    aksi hâlde                             → FAIL
```

ADR-23'ün simetrik tamamlanması. `SKIP` semantiği korunur: Sprint 5'in dört
koşusu S4 koşmadan yapıldı, denetim onlarda yanlış alarm üretmemeli.

### 11.4 Geri alma ve risk

- Projede down-migration yok (`migrate-down` = `DROP SCHEMA`). 006 **dolu bir
  veritabanında güvenli** olmak zorunda: yalnızca ekleme yapıyor, hiçbir sütun
  veya kısıt kaldırmıyor.
- Tek gerçek risk: 2. adımdaki `UNIQUE` kısıtı, tabloda yinelenen satır varsa
  başarısız olur. Şu an tablo boş; ilk S4 koşumundan **önce** uygulanmalı.
  Sıra plana yazıldı (Gün 1, T-E05-01).
- Eski koşular etkilenmez: `inspected_records` NULL → denetim SKIP;
  `suppressed_by` NULL → mevcut F.5 davranışı korunur.

### 11.5 Sprint 7 için migration 007 (ön not)

Kural 4 göstergesi `integrity_findings`'e yazmayacağı için ya
`integrity_metrics`'e bir satır (`rule_id = 4`, `precision NULL`,
`sufficient false`) ya da ayrı bir `integrity_indicators` tablosu gerekir.
Karar Sprint 7'ye bırakılıyor; 006 bu yüzden `integrity_metrics.rule_id`
kısıtını 1..5 olarak bırakıyor (4 dâhil).

---

## 12. Test stratejisi

### 12.1 Katmanlar ve sorumlulukları

| Katman | Ne kanıtlar | Nerede |
|---|---|---|
| **Birim** | Kural mantığı sınır durumlarda doğru | `internal/integrity/detector/*_test.go` |
| **PBT (`rapid`)** | Değişmezler tüm girdi uzayında geçerli | aynı yer |
| **Altın senaryo** | Bilinen enjeksiyonlar tam olarak beklenen bulguları üretir | `tests/golden/integrity_test.go` |
| **Gölge SQL** | Go uygulaması ile bağımsız SQL aynı sonucu verir | `tests/integration/integrity_shadow_test.go` |
| **Entegrasyon** | Kafka→dedektör→DB hattı, idempotanslık, önkoşullar | `tests/integration/integrity_test.go` |
| **İzolasyon** | Kör testin dört katmanı S4 için de geçerli | `tests/isolation/` + `scripts/verify-isolation.sh` |
| **Ön koşul** | Kural 3'ün dayandığı sıralama varsayımı gerçekten geçerli | `tests/integration/stream_order_test.go` |
| **Sessiz başarısızlık** | "0 bulgu" ile "her şey yolunda" karıştırılamaz | pozitif kontroller |

### 12.2 Yeni PBT değişmezleri (planın 8'ine ek)

| # | Değişmez | Hangi hatayı yakalar |
|---|---|---|
| 9 | Bir olay için en çok bir **kanonik** bulgu | Öncelik mantığının kural içine sızması |
| 10 | `margin` sonlu, `NaN` değil, ≥ 1,0 | `+Inf` zehirliliği, CHECK ihlali |
| 11 | `evidence` her girdide `json.Marshal`'dan hatasız geçer | Bulgunun sessizce yazılmaması |
| 12 | Aynı girdi akışı → aynı bulgu kümesi | Determinizm (K10) |
| 13 | `cell_id ∈ envanter ⇔ kural 1 bulgusu yok` | Tam karakterizasyon — hem FP hem FN |
| 14 | Zamanda monoton artan akışta kural 3 hiç bulgu üretmez | Kural 3'ün yanlış pozitif üretmesi |
| 15 | Simetrik geçişte (atıf belirsiz) kural 2 bulgu üretmez | Çift-uç işaretleme nüksü |
| 16 | `evidence` anahtarları yasaklı listede değil | Ground truth sızıntısı |

### 12.3 Kritik testler — ayrıntılı

**`TestStreamOrderIsMonotonePerSubscriber`** *(en önemli test)*
Küçük koşu (`configs/smoke.yaml`), `hts.records` tüketilir, abone başına olay
zamanı monotonluğu denetlenir. **İddia:** monotonluk ihlallerinin kümesi, tam
olarak `ground_truth.injected_rule = 3` olan olayların kümesidir.

Bu tek test iki şeyi birlikte kanıtlıyor: (a) Kafka partition-içi sıralama
varsayımı geçerli (R4 — kural 3'ün %100 precision'ının tek dayanağı),
(b) kural 3 tam olarak kaydırılmış kayıtları ve yalnızca onları görüyor.
Varsayım sessizce kırılırsa kural 3 yüzlerce yanlış pozitif üretir ve bunu fark
etmenin tek yolu etiketlere bakmaktır — yani üretimde kör kalırız.

**`TestFindingsAreIdempotent`**
Aynı parti iki kez işlenir; `integrity_findings` satır sayısı değişmez.
`ON CONFLICT DO NOTHING` + UNIQUE kısıtı birlikte doğrulanır.

**`TestBatchPhaseRequiresStreamPhase`**
`inspected_records` NULL iken toplu faz **reddeder**. Sessizce eksik öncelikle
koşmanın imkânsızlığı.

**`TestShadowSQLAgreesWithDetector`**
Kural 1, 2, 5 için Go dedektörünün bulgu kümesi ile bağımsız SQL sorgusunun
sonucu **birebir** aynı olmalı (`event_id` kümesi karşılaştırması).
Sprint 5'in PostGIS↔Haversine çapraz kontrolünün (%0,24 fark) karşılığı.

**`TestDetectorProducesFindingsOnSeededRun`** *(pozitif kontrol)*
Küçük koşuda her kanonik kural **en az bir** bulgu üretmeli.
`tests/isolation/import_graph_test.go`'daki `TestDetectorSeesKnownDependency`
ile aynı felsefe: "hiç bulgu yok" durumu tüm yanlış-pozitif testlerini geçer;
o sessiz başarısızlık biçimi ayrıca kapatılmalı.

**`TestEvidenceHasNoGroundTruthFields`**
Üretilen tüm kanıt JSON'larının anahtarları yasaklı listeyle kesişmemeli.

**İzolasyon eklentileri**
- `TestIntegrityDoesNotDependOnSimulator` — özellikle `injector`
- `TestIntegrityDoesNotDependOnAnalysis`
- `pkg/integrityrule` → `TestSharedContractsAreDependencyFree` listesine eklenir
- `scripts/verify-isolation.sh` — `svc_integrity` denetimi zaten var, korunur

### 12.4 Ölçüm disiplini (beyan-sonra-ölç)

```
Gün 0   Tüm eşikler ve kural spesifikasyonları ADR'lere yazılır. DONDURULUR.
Gün 3   Küçük koşu ile hat doğrulanır — precision/recall'a BAKILMAZ.
Gün 5   Dört senaryo koşulur. F.5 BİR KEZ hesaplanır.
        Sonuç ne olursa raporlanır.
Sonra   Spesifikasyon değişirse rapor bunu açıkça belirtir ve ÖNCEKİ ölçümü de
        gösterir.
```

Gün 3'te precision'a bakmama kuralı önemli: bakılırsa Gün 4'ün kural 2
tasarımı o sayıya göre şekillenir ve bu tanımı gereği aşırı uydurmadır.

---

## 13. Sprint 6 sonunda beklenen kabul kriterleri

### 13.1 K7 — kural bazında öngörü

| Kural | Sınıf | Öngörülen precision | Öngörülen recall | K7 |
|---|---|---|---|---|
| 1 envanter | yapısal | **%100** | %100 | ✅ |
| 3 zaman | yapısal | **%100** | %55–65 | ✅ |
| 5 aktivite | yapısal | **%100** | %100 | ✅ |
| 2 hız *(yapılırsa)* | istatistiksel | **%80–90** | %4,7–12,3 | ⚠ muhtemelen ✗ |
| 4 yörünge | — | Sprint 7, K7 kapsamı dışı (ADR-30) | — | ⊘ |

**K7 kapanış ifadesi (önceden beyan):** *"Üç yapısal kuralda precision ≥ %90
sağlanmıştır. Kural 2'de precision %80–90 aralığında ölçülmüştür ve eşiği
tutmaması, olay aralığının (2,4 sa) atlama mesafesine (10–40 km) göre büyük
olmasından kaynaklanan fiziksel bir sınırdır — önceden beyan edilmiş bir negatif
bulgudur. Kural 4, bilgi teorik olarak olay-çıpalı ölçüme kapalıdır (ADR-30)."*

### 13.2 Diğer kriterler

| # | Kriter | Sprint 6 sonu |
|---|---|---|
| **K7** | Bütünlük tespiti | **Ölçülmüş, kural bazında ayrıştırılmış** (yukarıdaki tablo) |
| **K6** | Kör test bütünlüğü (3 katman) | Katman 2 ve 4 ilk kez **üretim yolunda** sınanmış olur. Katman 1 (Kafka ACL) borcu açık → **formel olarak eksik**, Sprint 7 |
| K1 | Kapsama@90 | Değişmez (❌, Sprint 5'te ölçüldü) — S4 `estimates`'e dokunmuyor |
| K2, K3 | Alan daralması | Değişmez (✅) |
| K4, K5 | Ayrım + 4 senaryo | Değişmez (✅) |
| K8 | Geometri kararlılığı | Değişmez (✅) |
| K9, K10 | Ölçeklenebilirlik, tekrarlanabilirlik | Sprint 8 |

### 13.3 Yeni yetenekler

- Beşinci servis canlı: `hts.records` akışını **gerçek zamanlı** denetleyen ve
  koşu sonunda toplu geçiş yapan bütünlük izleyicisi.
- Manipüle edilmiş kayıtların **kör** tespiti — dört katmanlı izolasyon üretim
  yolunda çalışıyor.
- Adli açıklanabilirlik: sürümlü `evidence` + kural başına tanımlı `margin`.
- Kural bazında precision/recall + 5×5 karışıklık matrisi, `integrity_metrics`
  ile tekrarlanabilir.
- K9'a hazır yapı: akış kuralları abone anahtarına göre durumlu, replikalar
  arası konuşma gerekmiyor.

---

## 14. Story Point hesabı

### 14.1 Ölçek çıpası

Sprint 5 fiili: 16 SP planlı, 13 task, 6 gün, 5 gerçek hata düzeltmesi.
1 SP ≈ yarım günün altı, testleriyle birlikte tamamlanmış iş.

### 14.2 Task bazında hesap

| Task | Yeni dosya | Değişen | Test | Yeni kavram | SP | Gerekçe |
|---|---|---|---|---|---|---|
| **T-E05-00** | 2 | 2 | birim + izolasyon | yok | **1** | Mekanik sözleşme paketi; `pkg/ta`/`pkg/split` deseni aynen. Risk enjektör regresyonunda |
| **T-E05-01** | 6 + 1 SQL | 1 | birim + PBT + entegrasyon | öncelik, talep defteri, idempotanslık, kanıt sonluluğu | **3** | Sprint'in en yoğun kavramsal işi. Migration 006 (8 adım) dâhil. Sahte kurallarla uçtan uca doğrulama gerekiyor |
| **T-E05-02** | 2 | 0 | birim + PBT + gölge SQL | yok | **1** | Küme üyeliği; en ucuz kural. Envanter yükleyicisi `redis.ScanCells`'ten hazır |
| **T-E05-03** | 2 | 0 | birim + PBT + **ön koşul entegrasyonu** | abone watermark'ı, varış sırası semantiği | **2** | Mantık basit (watermark karşılaştırması) ama sıralama ön koşul testi ve akış fazı semantiği maliyetin yarısı |
| **T-E05-04** | 2 | 0 | birim + PBT + gölge SQL | modal seçim | **1** | Tek geçişli çoğunluk hesabı; toplu fazın ilk müşterisi |
| **T-E05-05** | 3 | 1 (config) | birim + 2 PBT + gölge SQL + altın | 5'lik pencere, yerel destek atfı, karar verilemezlik | **3** | En çok sınır durumu: Δt=0, zincir başı/sonu, 299/301 km/h, simetrik atıf, tek kayıtlı abone. Gölge SQL karşılaştırması zorunlu |
| **T-E05-07** | 0 | 4 | 4 entegrasyon + 2 izolasyon | iki modlu servis, faz önkoşulu | **2** | Kod çoğunlukla mevcut desenlerin birleştirilmesi; maliyet entegrasyon testlerinde ve `smoke.yaml` + betik değişikliğinde |
| **T-E04-09** | 2 | 2 | birim + entegrasyon | Wilson aralığı, karışıklık matrisi | **2** | F.5'in iki sorgusu + `LEFT JOIN` düzeltmesi + `suppressed_by` filtresi + matris + yazıcı |
| **T-E05-08** | 1 (rapor) | 1 (betik) | — | yok | **2** | Kod az, duvar saati çok: 4 tam koşum (kalibrasyon atlanarak ~2–3 sa) + rapor yazımı |

| | SP |
|---|---|
| Çekirdek (00, 01, 02, 03, 04, 07, 04-09, 08) | **14** |
| Kural 2 (05) | **3** |
| **Toplam** | **17** |
| *Sprint 7'ye* (T-E05-06 + Kafka ACL) | *4* |

### 14.3 Kritik yol SP'si

`00 (1) → 01 (3) → 03 (2) → 07 (2) → 04-09 (2) → 08 (2)` = **12 SP**
Kural 1, 4 (aktivite) ve 2 kritik yol dışında; paralel/kesilebilir.

### 14.4 Hız varsayımı ve dürüst uyarı

17 SP / 5 gün = 3,4 SP/gün. Sprint 5'in fiili hızı 16 SP / 6 gün = 2,7 SP/gün
idi — ama o hız beş gizli hatanın teşhis ve düzeltmesini içeriyordu ve altyapı
sıfırdan kuruluyordu. Sprint 6'da altyapı hazır, tasklar küçük ve bağımsız.
3,4 SP/gün **iyimser ama ulaşılabilir**; Gün 3 kapısı tam olarak bu iyimserliğin
sigortasıdır. Kapı düşerse 14 SP / 5 gün = 2,8 SP/gün — Sprint 5 hızı.

---

## 15. Gün gün uygulama planı

### Gün 0 — ½ gün · kod yazılmaz

| # | İş | Çıktı |
|---|---|---|
| 0.1 | **ADR-27** melez çalışma modeli | `docs/architecture/adr/ADR-27-integrity-execution-model.md` |
| 0.2 | **ADR-28** öncelik + tek-atıf + `suppressed_by` semantiği | ADR-28 dosyası |
| 0.3 | **ADR-29** kural 2 spesifikasyonu — **dondurulur** | ADR-29 dosyası |
| 0.4 | **ADR-30** kural 4 kapsamı | ADR-30 dosyası |
| 0.5 | **ADR-31** veri modeli + idempotanslık + F.5 düzeltmesi + K7 ölçülebilirlik kuralı | ADR-31 dosyası |
| 0.6 | `configs/*.yaml` → `integrity.detection.*` anahtarları (kullanılmadan) | 4 config + `smoke.yaml` |
| 0.7 | Kapsam onayı: 17 SP / 5 gün, kural 4 → Sprint 7 | onay |

**Çıkış ölçütü:** beş ADR yazılı ve onaylı; hiçbir eşik kod yazıldıktan sonra
değişmeyecek.

---

### Gün 1 — Temel · 4 SP

| # | Task | İş | Bitiş ölçütü |
|---|---|---|---|
| 1.1 | T-E05-00 | `pkg/integrityrule`; enjektör ortak tipe geçer | `go list -deps` temiz; enjektör testleri değişmeden yeşil |
| 1.2 | T-E05-01a | Migration 006 (8 adım) + `make migrate-up` | 5 denetim döner; `UNIQUE` kısıtı kurulu (tablo boşken) |
| 1.3 | T-E05-01b | Motor: `Rule` arayüzleri, `Hit`/`Finding`, öncelik, talep defteri | Sahte iki kuralla öncelik testi geçer |
| 1.4 | T-E05-01c | `sink/findings.go` idempotent yazıcı + `evidence.go` sonluluk koruması | Aynı parti iki kez → satır sayısı sabit |

**Gün sonu:** motor sahte kurallarla uçtan uca çalışıyor; şema hazır.

---

### Gün 2 — Üç yapısal kural · 4 SP

| # | Task | İş | Bitiş ölçütü |
|---|---|---|---|
| 2.1 | T-E05-02 | Kural 1 + `source/inventory.go` (Redis minimal görünüm) | PBT #13 geçer; envanter boşsa fail-fast |
| 2.2 | T-E05-03a | Kural 3: abone watermark'ı | PBT #14 geçer; tick-içi eşit damga ihlal değil |
| 2.3 | T-E05-03b | `TestStreamOrderIsMonotonePerSubscriber` (ön koşul) | İhlal kümesi = `injected_rule = 3` kümesi |
| 2.4 | T-E05-04 | Kural 5: modal IMEI (toplu) | Gölge SQL ile birebir; NULL IMEI güvenli |

**Gün sonu:** üç kural birim + PBT düzeyinde doğrulanmış; R4 varsayımı kanıtlı.

---

### Gün 3 — Servis ve ölçüm hattı · 4 SP · **KAPI**

| # | Task | İş | Bitiş ölçütü |
|---|---|---|---|
| 3.1 | T-E05-07a | `cmd/integrity`: `stream` modu (`svc_integrity` DSN, `run_id`, idle, `inspected_records`, OTel, `audit_log`) | Kafka→DB round-trip entegrasyon testi |
| 3.2 | T-E05-07b | `batch` modu + iki önkoşul denetimi | `TestBatchPhaseRequiresStreamPhase` |
| 3.3 | T-E05-07c | İzolasyon testleri + `scripts/run-scenario.sh` adım 2'ye paralel akış, adım 3'e toplu faz | `verify-isolation` + içe alma grafiği yeşil |
| 3.4 | T-E04-09 | F.5 (`LEFT JOIN` + `suppressed_by IS NULL`) + karışıklık matrisi + Wilson + `integrity_metrics` yazıcısı | Elle kurulmuş kümelerde birim testler geçer |
| 3.5 | — | **Küçük koşu:** `smoke.yaml` (100 ajan × 3 gün) uçtan uca | `verify_integrity` 5/5 OK; `integrity_metrics` dolu |

> **KAPI — Gün 3 sonu.** Üç ölçüt (Bölüm 4.3) sağlanıyorsa Gün 4 = kural 2.
> Sağlanmıyorsa kural 2 → Sprint 7; Gün 4 = eksik iş, Gün 5 = koşum.
> **Küçük koşuda precision/recall değerlerine bakılmaz** (Bölüm 12.4).

---

### Gün 4 — Kural 2 · 3 SP · *koşullu*

| # | Task | İş | Bitiş ölçütü |
|---|---|---|---|
| 4.1 | T-E05-05a | `window.go` 5'lik kayan pencere + geçiş hızı hesabı | Δt=0, zincir başı/sonu, 299/301 km/h birim testleri |
| 4.2 | T-E05-05b | `attribution.go` yerel destek + karar verilemezlik | PBT #15 (simetrik girdide bulgu yok) |
| 4.3 | T-E05-05c | Öncelik entegrasyonu (kural 1/5/3 talepleri dışlanır) | PBT #9 kural 2 dâhil geçer |
| 4.4 | T-E05-05d | Gölge SQL karşılaştırması + altın senaryo (20 kayıtlık abone) | Bulgu kümeleri birebir aynı |

**Kural 2 atomiktir:** 4.1 tamamlanıp 4.2 yarım kalırsa kural 2 **tamamen
devre dışı bırakılır**. Atıfsız kural precision %36 üretir; K7 raporuna böyle
bir satır girmesi, kuralı hiç yazmamaktan kötüdür.

---

### Gün 5 — Ölçüm ve rapor · 2 SP

| # | İş | Not |
|---|---|---|
| 5.1 | Topic tazeleme + 4 senaryo koşumu (`HTS_CALIBRATE=false`) | Kalibrasyon atlanarak ~2–3 sa; Sprint 5 borcu #4 böyle kazanca dönüyor |
| 5.2 | F.5 **bir kez** hesaplanır → `integrity_metrics` | Sonuç ne olursa raporlanır |
| 5.3 | Karışıklık matrisi + duyarlılık eğrisi (100/200/300 km/h) | Bilimsel ek katman; K7 eşiğini değiştirmez |
| 5.4 | `docs/results/sprint6-report.md` | K7 kural bazında; kapanış ifadesi Bölüm 13.1 |
| 5.5 | Sprint 7 devir listesi + teknik borç güncellemesi | Kural 4, Kafka ACL, arama bölgesi |

---

## 16. Kesin yol haritası — özet tablo

| Gün | SP | Tasklar | Kapanan risk |
|---|---|---|---|
| **0** (½) | 0 | ADR-27..31, config anahtarları, kapsam onayı | R1 aşırı uydurma, Ç-1, Ç-2, B-1 |
| **1** | 4 | T-E05-00, T-E05-01 (+migration 006) | H-2 idempotanslık, E-2 sonluluk, E-7 ikiz tuzağı |
| **2** | 4 | T-E05-02, T-E05-03, T-E05-04 | R4 sıralama varsayımı, kural 5'in akış tuzağı |
| **3** | 4 | T-E05-07, T-E04-09, küçük koşu → **KAPI** | H-1 F.5, E-4 tamlık sayacı, E-6 izolasyon |
| **4** | 3 | T-E05-05 *(koşullu, atomik)* | R2 kural 2 precision'ı — ölçülür, beyan edilir |
| **5** | 2 | 4 senaryo koşumu, F.5, rapor | R10 duvar saati, R9 topic birikmesi |
| | **17** | | |

**Kritik yol:** `00 → 01 → 03 → 07 → 04-09 → 08` (12 SP)
**Kesilebilir:** T-E05-05 (3 SP) — kesilirse K7 üç yapısal kuralla kapanır
**Sprint 7:** T-E05-06 (2), Kafka ACL (2), arama bölgesi ADR-34 (5+)

---

> **Kod yazılmamıştır.** Kod yazımına Gün 0 çıktıları (ADR-27..31) onaylandıktan
> sonra, T-E05-00 ile başlanacaktır.
