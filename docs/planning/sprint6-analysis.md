# Sprint 6 (E05 — Bütünlük Denetimi) Ön Analiz Raporu

**Tarih:** 2026-07-30
**Durum:** Analiz — **kod yazılmamıştır**
**Kapsam:** T-E05-01..05 (plan özeti) + T-E04-09 (Sprint 5'ten devir)
**Hedef kabul kriteri:** K7 — kural bazında precision ≥ %90, recall raporlu

---

## 0. Yönetici özeti

Sprint 6'nın altyapısı hazır: Kafka tüketici deseni, kalıcılaştırıcı çekirdeği,
envanter yükleyici, rol izolasyonu ve `integrity_findings` tablosu Sprint 0–5
boyunca yerine oturmuş. **Eksik olan altyapı değil, tespit edilebilirliğin
kendisi.**

Sprint 5 verisi üzerinde (dört koşu, ~1,2 milyon kayıt) beş enjeksiyon kuralının
her biri için tespit edilebilirliği **ölçtüm**. Sonuç, planın sessizce
varsaydığı simetriyi kırıyor:

| Enjeksiyon kuralı | Tespit sinyali | Precision | Recall | Değerlendirme |
|---|---|---|---|---|
| **1** sahte hücre | `cell_id ∉ cells` | **%100** | **%100** | Deterministik, sorun yok |
| **5** cihaz değişimi | MSISDN başına modal IMEI | **%100** | **%100** | Toplu modda kusursuz |
| **3** zaman kaydırma | akış sırasında geriye giden damga | **%100** (yapısal) | **~%65** (üst sınır) | **Yalnızca akış modunda** |
| **2** atlama | kinematik olarak imkânsız geçiş | **%36 → %72–83** | **%4,9 / %12,3** | **K7 eşiğinin altında** |
| **4** kayıt boşluğu | boşluk uzunluğu | **%1,4** | %6 | **Ölçülemez — çıpa yok** |

Üç karar Sprint 6 başlamadan alınmalı (Bölüm 5). Bunlar alınmadan kod yazmak,
Sprint 6'nın son gününde "K7 tutmadı, çünkü kural tanımı ölçülebilir değildi"
sonucuna varmak demek.

En yüksek risk teknik değil **metodolojik**: kural 2'nin precision'ı %90'ın
altında ölçüldü ve etiketli veri elimizde. Kuralı %90'ı geçene kadar
ayarlamak, test kümesine aşırı uydurma (overfitting) olur ve K1'de gösterilen
dürüstlüğü geriye dönük olarak geçersiz kılar.

---

## 1. Sprint 5 sonu mimarisi — Sprint 6'nın devraldığı taban

```
                    ┌─────────────────────────────────────────────┐
  cmd/simulator ───►│ Kafka hts.records     (4p, key=pseudo_msisdn)│──┬─► cmd/analysis-engine  ✅
   + injector       └─────────────────────────────────────────────┘  │
   (ADR-09) ───────►┌─────────────────────────────────────────────┐  └─► cmd/integrity  ◄── SPRINT 6
                    │ Kafka hts.groundtruth (4p, key=agent_id)     │──► cmd/persister (2 rol) ✅
                    └─────────────────────────────────────────────┘
  cmd/validation (S3b, batch)  ✅  ── F.1–F.4 → metrics ;  F.5 → SPRINT 6
```

**Sprint 6'da yeniden kullanılacak, çalışan bileşenler:**

| Bileşen | Yol | Sprint 6'daki rolü |
|---|---|---|
| Kafka tüketici deseni | `internal/persist/consumer.go` | Akış modunun iskeleti (offset disiplini, zehirli satır, idle) |
| Analiz tüketicisi | `internal/analysis/driver/consumer.go` | `run_id` süzgeci + idle + OTel çıkarımı deseni |
| Tel biçimi sözleşmesi | `pkg/htswire` | `Record` yapısı — `injected_rule` **taşımaz** (kör test) |
| Envanter | `internal/storage/redis.ScanCells` | Kural 1'in hücre kümesi + kural 2'nin site konumları |
| Şema | `internal/storage/migrations/001` | `integrity_findings` hazır |
| Rol izolasyonu | `migrations/004` + `scripts/verify-isolation.sh` | `svc_integrity` → `ground_truth` → **denied** (testli) |
| Koşu sayaçları | `migrations/005` (ADR-23) | `verify_integrity` deseni; S4 için 5. denetim eklenecek |
| İçe alma grafiği testi | `tests/isolation/import_graph_test.go` | S4 için genişletilecek |
| Koşum betiği | `scripts/run-scenario.sh` | Adım 4,5 olarak `cmd/integrity` eklenecek |
| Servis iskeleti | `cmd/integrity/main.go` (45 satır) | Health + OTel var, iş mantığı yok |

**Boş duran yerler:** `internal/integrity/detector/` (dizin var, dosya yok),
`internal/validation/integrity/` (dizin var, dosya yok).

---

## 2. Ölçüm: beş kuralın tespit edilebilirliği

> Bu ölçümler Sprint 5'in dört koşusu üzerinde, `ground_truth.injected_rule`
> etiketi kullanılarak yapıldı (S3b rolü — meşru). Amaç Sprint 6 kod yazmadan
> önce **hangi kuralın bilgi taşıdığını** bilmek. Ölçümün kendisi K7 değildir;
> K7'yi Sprint 6'da gerçek dedektör çıktısı üzerinden S3b hesaplayacak.

### 2.1 Kural 1 — envanter (dört koşuda da aynı)

```
bulgu = 1172 ,  doğru = 1172  →  precision %100 , recall %100
```

`cell_id`, `uuid.NewSHA1(fakeCellNamespace, eventID)` ile ayrı bir namespace'ten
üretiliyor; envantere hiçbir zaman düşmüyor. Tespit tek bir küme sorgusu.
**Tek dikkat noktası:** bu kayıtların konumu bilinmiyor, bu yüzden hız/yörünge
zincirinden **çıkarılmaları** gerekir; NULL konumla zincire girerlerse ardışık
kayıt çiftleri kırılır.

### 2.2 Kural 5 — cihaz değişimi (aktivite)

```
MSISDN başına modal IMEI referans, azınlık IMEI = bulgu
bulgu = 1171 , doğru = 1171  →  precision %100 , recall %100   (dört koşuda)
```

Enjektör `otherAgent()` ile `agentID + 1..999` seçiyor; 1000 ajanlı koşuda bu
çoğunlukla **var olmayan** bir ajanın IMEI'sidir. Dolayısıyla:

- "Bir MSISDN → birden çok IMEI" kuralı **çalışır** (ölçüldü).
- "Bir IMEI → birden çok MSISDN" kuralı **hiç tetiklenmez** (sahte IMEI başka
  yerde görünmez). Bu yön yazılmamalı; yazılırsa ölü kod olur.

**Kritik tasarım sonucu — bu kural akış modunda çalışmaz.** Akışta ilk görülen
IMEI referans alınırsa ve bir abonenin *ilk* kaydı enjekte edilmişse, o abonenin
sonraki ~293 temiz kaydı bulgu üretir. Beklenen etki: 1000 abone × %0,4 ≈ 4
abone × 293 kayıt ≈ **1170 hatalı bulgu** — precision %100'den %50'ye düşer.
Çoğunluk oyu ise geri alma (retraction) gerektirir; `integrity_findings` ekle-yalnız
bir tablodur. **Kural 5 toplu (batch) kuraldır.**

### 2.3 Kural 3 — zaman kaydırma

Enjektör damgayı **−2 saat** kaydırıyor (`DefaultTimeShift`).

| Mod | Sinyal | Ölçüm |
|---|---|---|
| **Toplu** (zamana göre sıralı) | aynı MSISDN + aynı damga, farklı hücre | 265 bulgu, yalnızca 22'si kural 3 → **precision %8,3** |
| **Akış** (üretim sırası) | abone başına en yüksek görülen damga geriye gidiyor | precision **yapısal %100**, teorik recall **%64,8** |

Toplu modda kanıt **yok olur**: zamana göre sıraladığınızda kaydırılmış kayıt
sessizce yeni yerine oturur. Akış modunda ise kanıt yapısaldır — üretim sırası
tick-major, tick içi damgalar eşit, Kafka anahtarı `pseudo_msisdn` olduğundan
abone başına sıra tek partition'da korunur. Sadece kural 3 damgaya dokunduğu
için başka hiçbir kural bu sinyali tetiklemez → yanlış pozitif üretmez.

Recall'un %64,8'de tavan yapmasının nedeni fiziksel: kaydırma 2 saat, ortalama
olay aralığı 2,4 saat. Önceki olay 2 saatten eskiyse kaydırılmış damga hâlâ
ondan sonra kalır ve sıra bozulmaz.

```
kural 3 toplam = 1140 , önceki olay < 2 saat = 739  →  teorik recall %64,8
```

### 2.4 Kural 2 — atlama (hız 300 km/h)

Bu kural planın en zayıf halkası. Üç ayrı ölçüm:

**(a) Naif kural — her kayıt için max(v_önceki, v_sonraki) > 300:**

| Koşu | bulgu | kural 2 | kural 3 | kural 5 | temiz | precision | recall |
|---|---|---|---|---|---|---|---|
| A kentsel | 159 | 58 | 21 | 1 | 79 | **%36,5** | %4,9 |
| C kırsal | 349 | 147 | 24 | 1 | 177 | **%42,1** | %12,3 |

Precision'ın %50'nin altına çakılmasının **yapısal** bir nedeni var: hız ihlali
bir **çiftin** özelliğidir, kaydın değil. Kayıt bazlı bir kural çiftin iki
ucunu da işaretler → temiz eş her zaman yanlış pozitiftir. Kentselde ölçüm
birebir tutuyor: `58 (kural2) + 21 (kural3) = 79 = temiz bulgu sayısı`.

**(b) Katı üçlü sınama — v(A,B)>300 ∧ v(B,C)>300 ∧ v(A,C)≤300:**

| Koşu | bulgu | precision | recall |
|---|---|---|---|
| A kentsel | 1 | %100 | **%0,08** |
| C kırsal | 7 | %100 | **%0,59** |

Precision kusursuz, recall yok. Tek bulgu üzerinden "%100 precision" beyan
etmek istatistiksel olarak anlamsızdır.

**(c) Yerel destek atfı — ihlal eden geçişin hangi ucunun yerel yörüngeden
uzaklaştığına bakılır (önerilen tasarım):**

| Koşu | bulgu | kural 2 | kural 3 | temiz | precision | recall |
|---|---|---|---|---|---|---|
| A kentsel | 78 | 56 | 12 | 10 | **%71,8** | %4,7 |
| C kırsal | 175 | 146 | 13 | 16 | **%83,4** | %12,3 |

Temiz yanlış pozitifler 79→10 ve 177→16'ya iniyor. Kalan hataların çoğu
**kural 3 olaylarıdır** (12–13): kaydırılmış damga da kinematik olarak imkânsız
bir geçiş üretir. Kural önceliği (Bölüm 5.2) bunların ~%65'ini temizler ve
precision kentselde ~%80, kırsalda ~%90 seviyesine çıkar — **eşiğin sınırında.**

**Recall'un kök nedeni ve neden düzeltilemeyeceği:** ortalama olay aralığı
2,4 saat. Kentsel alanda envanterin çapı ~10 km, kırsalda ~40 km. 300 km/h
eşiğini aşmak için Δt < 2 dk (kentsel) veya Δt < 8 dk (kırsal) gerekiyor;
damgalar 5 dakikalık tick ızgarasında. Yani atlamaların büyük kısmı **fiziksel
olarak mümkündür** ve hiçbir kural tasarımı bunu değiştiremez — bilgi veride yok.

**Denenen ve işe yaramayan sinyal:** TA / `r_max` tutarlılığı. Atlanan kayıt
eski hücrenin TA'sını taşıyor; yeni hücrede `ta_value · 78,12 m > r_max_m`
olması beklenebilirdi. Dört koşuda **sıfır ihlal** — `r_max` TA mesafelerine
göre çok büyük. Bu yol kapalı, Sprint 6'da denenmesin.

### 2.5 Kural 4 — kayıt boşluğu (yörünge)

**İki bağımsız nedenle F.5 ile ölçülemez.**

**Neden 1 — çıpa (event_id) yok.** F.5 recall sorgusu
`f.event_id = g.event_id` üzerinden eşleşir. Silinen olayın kimliği
`UUIDv5(run_id ‖ agent_id ‖ tick ‖ seq)`. S4 `agent_id`'yi görmez (elinde HMAC'li
`pseudo_msisdn` var), `tick` ve `seq`'i de bilemez. **S4 var olmayan bir olayın
event_id'sini üretemez** → recall(4) ≡ 0, ve komşu bir olaya çıpalanan her
kural-4 bulgusu F.5'e göre yanlış pozitiftir.

**Neden 2 — sinyal yok.** Silmenin yarattığı boşluk ile doğal sessizlik
ayrışmıyor:

| Boşluk tipi | n | medyan | p95 | >12 saat |
|---|---|---|---|---|
| Silme içeren | 1171 | 3,33 sa | 12,67 sa | 73 |
| Temiz | 295 952 | 1,33 sa | 9,25 sa | 5114 |

12 saatlik eşikte: recall %6,2, precision **73/5187 = %1,4**. Poisson akışında
uzun sessizlik normaldir; iki üstel aralığın toplamı ile tek aralık ayırt
edilemez.

---

## 3. Task task analiz

> Planın G bölümü E05'i tek satırda özetliyor: *"T-E05-01..05 — 5 tespit kuralı
> (envanter, hız 300, zaman, yörünge, aktivite) + `evidence JSONB` — 8 SP"*.
> Aşağıdaki ayrıştırma benim önerimdir; kural motoru, servis bağlantısı ve
> idempotanslık planın 8 SP'sinde **yer almıyor** (Bölüm 4.3).

---

### T-E05-00 — `pkg/integrityrule` ortak kural sözleşmesi  · 1 SP · **YENİ**

- **Amaç.** Kural kimlikleri (1..5), kanonik adlar (`integrity_findings.rule_name`)
  ve kural sınıfı (olay-çıpalı / toplulaştırılmış) için bağımlılıksız tek bir
  tanım noktası.
- **Neden gerekli?** Enjektör kimlikleri `internal/simulator/event/injector`
  içinde tanımlı; dedektör `internal/integrity/detector` içinde olacak. İkisi
  birbirini import **edemez** (izolasyon testi ADR-20). Kimlikler iki yerde
  ayrı yazılırsa bir numaralandırma değişikliği derleme hatası vermez, sessizce
  precision'ı sıfırlar — `pkg/ta`, `pkg/split`, `pkg/htswire`'ın kurulmasına yol
  açan **birebir aynı ikiz tuzağı**. F.5 `g.injected_rule = f.rule_id`
  eşitliğine dayanıyor; bu eşitlik bir sözleşmedir, tesadüf olmamalı.
  > Kör test delinmiyor: S4 hangi kuralı *uyguladığını* bilmek zorundadır;
  > hangi kaydın enjekte edildiğini öğrenmiyor.
- **Mevcut bileşenler.** `pkg/ta` / `pkg/split` deseni (stdlib + uuid dışı
  bağımlılık yok).
- **Yeni dosyalar.** `pkg/integrityrule/rule.go`, `rule_test.go`
- **Değişecek dosyalar.** `internal/simulator/event/injector/injector.go`
  (kendi `Rule` tipini bırakır, ortak tipe geçer),
  `tests/isolation/import_graph_test.go` (`TestSharedContractsAreDependencyFree`
  listesine eklenir).
- **Riskler.** Düşük. Enjektörün davranışı değişmemeli — mevcut
  `injector_test.go` regresyon güvencesi. Kural adlarının `VARCHAR(50)`'e
  sığması denetlenmeli.
- **Test planı.** Kimlik↔ad çift yönlü eşleme; `Valid()` sınırları; ad uzunluğu
  ≤ 50; bağımlılıksızlık testi; enjektör testleri değişmeden geçer.
- **Tamamlanma.** `go list -deps pkg/integrityrule` içinde hiç `internal/` yok;
  enjektör ve dedektör aynı sabitleri kullanıyor.

---

### T-E05-01 — Kural motoru + bulgu yazıcısı + migration 006 · 3 SP

- **Amaç.** Kuralların takılacağı çekirdek: kural arayüzü, abone başına durum,
  öncelik/tek-atıf mantığı, `margin`+`evidence` üretimi, idempotent yazıcı.
- **Neden gerekli?** Beş kural beş ayrı geçici çözüm olarak yazılırsa öncelik
  disiplini (ADR-28) her kuralda tekrarlanır ve zamanla ayrışır — Sprint 5'te
  `internal/persist`'in tek çekirdekle çözdüğü problemin aynısı. Ayrıca F.5
  precision'ı bulgu **sayısına** bölüyor: yinelenen tek bir bulgu ölçümü
  sessizce bozar.
- **Mevcut bileşenler.** `internal/persist` offset/tampon deseni;
  `internal/storage/postgres` yazıcı deseni (`ON CONFLICT DO NOTHING`).
- **Yeni dosyalar.**
  ```
  internal/integrity/detector/{engine.go,rule.go,state.go,evidence.go,precedence.go}
  internal/storage/postgres/findings.go
  internal/storage/migrations/006_integrity_findings.sql
  ```
- **Değişecek dosyalar.** `internal/storage/migrations/004_roles.sql` üzerine
  006 ek grant'ları (aşağıda).
- **Migration 006 içeriği (gerekçeleriyle).**
  1. `CREATE UNIQUE INDEX ON integrity_findings (run_id, event_id, rule_id)` —
     **eksik ve kritik.** Kafka at-least-once; tüketici çökerse parti yeniden
     işlenir. Şu anda hiçbir kısıt yinelenen bulguyu engellemiyor; F.5'in
     precision denominatörü şişer ve ölçüm **sessizce** düşer.
  2. `integrity_metrics` tablosu (`run_id, rule_id, findings, true_positives,
     injected, precision, recall, computed_at`) — `metrics` tablosu
     (scenario, method, confidence) anahtarıyla kural bazlı satırları taşıyamaz.
     K7'nin çıktısı tekrarlanabilir biçimde saklanmalı.
  3. `GRANT UPDATE (inspected_records) ON run_config TO svc_integrity` +
     `ALTER TABLE run_config ADD COLUMN inspected_records BIGINT` — S4'ün kaç
     kayıt incelediğini beyan etmesi için (Bölüm 5.4).
  4. `verify_integrity`'ye 5. denetim: `inspected_records = published_events`.
- **Riskler.**
  - `evidence JSONB` içine `+Inf` sızması: Δt = 0 durumunda hız sonsuzdur;
    Go'nun `json.Marshal` fonksiyonu `+Inf`/`NaN` için **hata** döner ve bulgu
    hiç yazılmaz. `margin` de `FLOAT NOT NULL`. Üst sınır (örn. 1e6) ve
    "Δt=0 → ayrı kanıt alanı" sözleşmesi şart (ADR-32).
  - Öncelik mantığı yanlış yerde kurulursa (kural içinde, motorda değil)
    çapraz bastırma test edilemez hâle gelir.
- **Test planı.** Birim: öncelik tablosu, tek-atıf değişmezi, margin sonluluğu,
  evidence serileşmesi. PBT: aynı olay için ≤ 1 bulgu; `Marshal` hiçbir girdide
  hata vermez. Entegrasyon: aynı partiyi iki kez işle → satır sayısı değişmez
  (idempotanslık).
- **Tamancal ölçüt.** Sahte iki kuralla motor uçtan uca çalışıyor; yeniden işleme
  satır üretmiyor; `verify_integrity` 5 denetim döndürüyor.

---

### T-E05-02 — Kural 1: envanter tutarlılığı · 1 SP

- **Amaç.** `hts_records.cell_id` koşunun envanterinde yoksa bulgu üret.
- **Neden gerekli?** K7'nin en sağlam ayağı (ölçüm: %100/%100) ve adli olarak
  en savunulabilir bulgu tipi: "kayıt, şebekede hiç var olmayan bir hücreyi
  beyan ediyor."
- **Mevcut bileşenler.** `internal/storage/redis.ScanCells` (koşu kapsamlı
  envanter, `hts:{run_id}:cell:{cell_id}`), alternatif olarak `cells` tablosu
  (`svc_integrity`'nin SELECT hakkı var).
- **Yeni dosyalar.** `internal/integrity/detector/inventory.go`, `_test.go`
- **Değişecek dosyalar.** yok
- **Riskler.**
  - `internal/analysis/params.Inventory` cazip ama **kullanılmamalı**: S4 →
    analiz bağımlılığı izolasyon grafiğini kirletir ve S4'ün ihtiyacı yok
    (yalnızca kimlik kümesi + site konumu + `r_max`). Kendi minimal görünümü
    kurulmalı.
  - Envanter boş yüklenirse **her kayıt** bulgu üretir (1172 yerine 298 117).
    Yükleme sonrası `len(cells) > 0` fail-fast denetimi zorunlu.
- **Test planı.** Birim: bilinen/bilinmeyen kimlik. PBT: `cell_id ∈ envanter ⇔
  bulgu yok` (tam karakterizasyon). Gölge SQL çapraz kontrolü: Go dedektörünün
  bulgu sayısı = `LEFT JOIN cells ... WHERE c.cell_id IS NULL` sayısı.
- **Tamamlanma.** Dört koşuda bulgu sayısı = 1172, precision %100.

---

### T-E05-03 — Kural 3: zaman tutarlılığı (akış) · 2 SP · **kritik yolda**

- **Amaç.** Abone başına "şimdiye kadar görülen en yüksek olay zamanı"
  (watermark) tutulur; gelen kaydın damgası bundan geriyse bulgu üretilir.
- **Neden gerekli?** Kanıt **yalnızca akış sırasında** var (ölçüm: toplu modda
  precision %8,3, akışta yapısal %100). Bu kural, S4'ün neden bir Kafka
  tüketicisi olmak zorunda olduğunun tek gerçek gerekçesidir — mimari kararı bu
  task belirliyor, bu yüzden kritik yolda.
- **Mevcut bileşenler.** `internal/analysis/driver/consumer.go` (poll döngüsü,
  `run_id` süzgeci, idle), `pkg/kafka` (anahtar = `pseudo_msisdn`).
- **Yeni dosyalar.** `internal/integrity/detector/timeorder.go`, `_test.go`
- **Değişecek dosyalar.** yok (motor T-E05-01'de kuruldu)
- **Riskler.**
  - **Yük taşıyan varsayım:** üretim sırası abone başına zamanda monoton
    artmalı. Simülatör tick-major döngü kuruyor (`run.go`), tick içi damgalar
    eşit, `ProduceSync` idempotent üreticiyle partition içi sırayı koruyor.
    Bu varsayım **teste bağlanmalı** — kırılırsa kural sessizce temiz kayıtlara
    bulgu yazar ve precision çöker. Sprint 5'in beş gizli hatasının hepsi bu
    türdendi.
  - Yeniden işleme (offset geri sarma): watermark bellekte olduğu için yeniden
    başlatmada sıfırlanır; ilk kayıtlar bulgu üretmez (recall düşer, precision
    bozulmaz). Kabul edilebilir, ama raporlanmalı.
  - `>` mi `≥` mi: tick içi eşit damgalar **ihlal değildir**. `<` katı olmalı.
- **Test planı.** Birim: elle kurulmuş 10 kayıtlık akış (eşit damga, artan
  damga, 2 saat geri). PBT: monoton artan akışta hiç bulgu yok. Entegrasyon
  (ön koşul testi): küçük bir koşuda tüketim sırasında abone başına damga
  monotonluğu — kural 3 kayıtları hariç.
- **Tamamlanma.** Dört koşuda precision %100, recall %55–65 bandında
  (teorik tavan %64,8).

---

### T-E05-04 — Kural 5: cihaz aktivitesi (toplu) · 1 SP

- **Amaç.** Koşu sonunda MSISDN başına modal IMEI belirlenir; azınlık IMEI'li
  kayıtlar bulgu üretir.
- **Neden gerekli?** %100/%100 ölçüldü — K7'nin ikinci sağlam ayağı.
- **Mevcut bileşenler.** `internal/storage/postgres` sorgu deseni.
- **Yeni dosyalar.** `internal/integrity/detector/activity.go`, `_test.go`
- **Değişecek dosyalar.** yok
- **Riskler.**
  - **Akış modunda yazılırsa precision %50'ye düşer** (Bölüm 2.2). Toplu mod
    zorunlu; bu kısıt kodda yorumla değil, tipte ifade edilmeli (kural sınıfı
    `pkg/integrityrule`'da).
  - `pseudo_imei` şemada NULL'a izin veriyor (`VARCHAR(64)`, NOT NULL yok).
    NULL IMEI modal hesabını bozmamalı.
  - Ters yön ("bir IMEI → çok MSISDN") yazılmamalı: enjektör var olmayan ajanın
    IMEI'sini kullanıyor, o kural hiç tetiklenmez.
- **Test planı.** Birim: tek IMEI, iki IMEI (azınlık atfı), üç IMEI, NULL.
  Gölge SQL çapraz kontrolü. PBT: modal IMEI hiçbir zaman bulgu üretmez.
- **Tamamlanma.** Dört koşuda bulgu = 1171, precision %100.

---

### T-E05-05 — Kural 2: kinematik tutarlılık (hız) · 3 SP · **en yüksek risk**

- **Amaç.** Ardışık kayıt çiftleri arasındaki ima edilen hız
  `max_velocity_kmh` (300) eşiğini aşarsa, ihlali **çiftin bir ucuna atfederek**
  tek bulgu üret.
- **Neden gerekli?** Planın adıyla andığı beş kuraldan biri ve K7'nin geçip
  geçmemesini belirleyecek olan. Aynı zamanda tasarımı en çok kanıt gerektiren.
- **Mevcut bileşenler.** `pkg/geo` (ENU/Haversine), envanter site konumları,
  `internal/storage/postgres` pencere sorgusu.
- **Yeni dosyalar.** `internal/integrity/detector/velocity.go`,
  `attribution.go`, `_test.go`
- **Değişecek dosyalar.** `configs/*.yaml` (`integrity.detection.*` bölümü)
- **Tasarım (ölçümle gerekçelendirilmiş, Sprint 6 başında donduralacak).**
  1. Kural 1 kayıtları zincirden çıkarılır (konum bilinmiyor).
  2. İhlal **geçişin** özelliğidir: `d(cell_i, cell_j) / Δt > 300 km/h`.
     `Δt = 0` ve `d > 0` → gerçekten imkânsız (sonsuz hız), `margin` üst sınırla
     kapılır.
  3. **Atıf — yerel destek:** ihlal eden geçişin hangi ucu abonenin yerel
     yörüngesinden uzaklaşıyorsa bulgu ona yazılır
     (`d(aday, dış_komşu) > d(eş, dış_komşu)`).
  4. **Bir geçiş → en çok bir bulgu.** Karar verilemezse (iki uç simetrik)
     **bulgu üretilmez** — adli bir sistem iki tarafı birlikte suçlamaz.
  5. Öncelik: kural 1, 3 ve 5 tarafından talep edilmiş olaylar değerlendirmeye
     girmez (ADR-28).
- **Riskler.**
  - **Precision K7 eşiğinin altında ölçüldü** (%71,8 kentsel / %83,4 kırsal;
    öncelikle ~%80 / ~%90). En yüksek teknik ve metodolojik risk. Bölüm 5.2.
  - Recall %4,7–12,3 — fiziksel tavan, tasarımla yükseltilemez.
  - Naif kayıt-bazlı uygulama precision'ı %36'ya düşürür; atıf mekanizması
    isteğe bağlı bir iyileştirme değil, kuralın **tanımının parçası**.
  - Çapraz bulaşma: kural 3 olayları da imkânsız geçiş üretir. Bu bir dedektör
    hatası değil, F.5'in tam-eşleşme tanımının sonucu — raporda ayrıştırılmalı.
- **Test planı.** Birim: elle kurulmuş üçlüler (temiz, atlama, Δt=0, sınırda
  299/301 km/h, zincir başı/sonu). PBT: bir geçiş → ≤ 1 bulgu; simetrik girdide
  bulgu yok; `margin` sonlu ve ≥ 1. Gölge SQL: bu raporun ölçüm sorgusuyla
  bulgu kümesi karşılaştırması. Altın senaryo: 20 kayıtlık tek abone dizisi.
- **Tamamlanma.** Dedektör gölge SQL ile aynı bulgu kümesini üretiyor;
  precision/recall dört koşuda ölçülmüş ve **eşiğe uysun ya da uymasın**
  raporlanmış.

---

### T-E05-06 — Kural 4: yörünge sürekliliği · 2 SP · **kapsam kararı bekliyor**

- **Amaç.** Abone kayıt akışındaki eksik halkanın raporlanması.
- **Neden gerekli?** Planın beşinci kuralı. Ancak Bölüm 2.5'teki iki nedenle
  **olay-çıpalı bir bulgu olarak var olamaz.**
- **Önerilen kapsam (ADR-30).** Kural 4, `integrity_findings`'e olay bazlı
  satır yazmaz; koşu düzeyinde bir **gösterge** üretir: abone başına gözlenen
  sessizlik dağılımı, diurnal λ'dan beklenen dağılımla karşılaştırılır ve
  koşunun "kayıp kayıt kütlesi" tahmini raporlanır. K7'nin precision eşiği bu
  kurala uygulanmaz; gerekçe bilgi teoriktir, ölçümle belgelenir.
- **Mevcut bileşenler.** `internal/simulator/event.DiurnalProfile()` beklenen
  yoğunluğu veriyor — ama S4 bunu import **edemez** (ADR-20). λ profili S4
  tarafında ayrı beyan edilmeli veya beklenti verinin kendisinden
  (abone bazlı ampirik dağılım) türetilmeli. **İkincisi tercih edilmeli.**
- **Yeni dosyalar.** `internal/integrity/detector/trajectory.go`, `_test.go`
- **Riskler.** Kapsam kararı alınmazsa: 5187 bulgu, precision %1,4 → K7 kural
  bazında **kesin olarak tutmaz** ve bu, düzeltilebilir bir mühendislik hatası
  gibi görünür; değildir.
- **Test planı.** Birim: sentetik boşluklu diziler. Ölçüm: raporlanan kayıp
  kütle tahmini ile gerçek kural-4 sayısı (1171) karşılaştırması — **toplam
  düzeyinde** doğruluk, olay düzeyinde değil.
- **Tamamlanma.** Kural 4 göstergesi koşu başına raporlanıyor; K7 kapsamı ADR
  ile yazılı olarak dışlanmış.

---

### T-E05-07 — `cmd/integrity` bağlanması (akış + toplu) · 2 SP · **kritik yolda**

- **Amaç.** Servisi gerçek akışa ve veritabanına bağlamak: Kafka tüketicisi
  (akış kuralları), koşu sonu toplu geçiş (toplu kurallar), `svc_integrity`
  DSN'i, `run_id` süzgeci, idle-çıkış, `inspected_records` sayacı, OTel,
  `audit_log`.
- **Neden gerekli?** Sprint 5'in dersi: "Kafka'ya yayınlamak şemayı
  doğrulamıyor." Kural mantığı birim testte yeşil olabilir; servis
  bağlanmadan hiçbir ölçüm üretilemez.
- **Mevcut bileşenler.** `internal/persist/consumer.go`,
  `internal/analysis/driver/consumer.go`, `internal/observability/health`,
  `cmd/persister/main.go` (env ile mod seçme deseni).
- **Yeni dosyalar.** —
- **Değişecek dosyalar.** `cmd/integrity/main.go`, `scripts/run-scenario.sh`
  (adım 4,5), `Makefile` (`verify-integrity` çıktısına K7 özeti),
  `configs/*.yaml`
- **Riskler.**
  - **S4 örnekleme yapmamalı.** ADR-14/ADR-24 örneklemesi analize aittir.
    Örnekleme uygulanırsa kural 2/3/5 abone dizisi delik deşik olur ve recall
    ölçümü anlamsızlaşır. Bu, `sampling.Policy` alanının S4'te **hiç
    bulunmaması** ile ifade edilmeli.
  - **Yarım koşu sessizce düşük recall üretir.** Sprint 5 hatası #4 birebir
    burada tekrar edebilir: idle eşiği kısa kalırsa S4 akışın ortasında çıkar,
    recall %30 çıkar ve bu "bilimsel bulgu" gibi görünür.
    `inspected_records = published_events` denetimi (migration 006) bu yüzden
    isteğe bağlı değil.
  - İki persister'ın health portu çakışması (Sprint 5 hatası #5) — S4 için
    `:8084` zaten ayrı, ama toplu koşumda paralel çalışırsa denetlenmeli.
  - `svc_integrity` yerine `hts_admin` DSN'i kullanılırsa K6 katman 2 üretim
    yolunda hiç sınanmaz. DSN rolü açıkça `svc_integrity` olmalı.
- **Test planı.** Entegrasyon: Kafka → dedektör → `integrity_findings`
  round-trip + idempotanslık; `run_id` süzgeci (yabancı koşu kayıtları
  sayılmıyor); idle-çıkış; `svc_integrity` ile `ground_truth` sorgusu →
  permission denied (regresyon). İçe alma grafiği: `internal/integrity` →
  `internal/simulator` ve → `internal/analysis` yasak.
- **Tamamlanma.** `scripts/run-scenario.sh` tek komutta bütünlük denetimini de
  koşuyor; `verify_integrity` 5 denetimde OK.

---

### T-E04-09 — F.5 precision/recall + karışıklık matrisi · 2 SP (Sprint 5 devri)

- **Amaç.** Kural bazında precision/recall hesabı ve `integrity_metrics`'e
  yazımı; ek olarak 5×5 karışıklık matrisi.
- **Neden gerekli?** K7'nin ölçüm aracı. Etiketi yalnızca S3b görebilir
  (ADR-09).
- **Mevcut bileşenler.** `internal/validation/pipeline` (aşama yürütücü),
  `internal/validation/metrics/writer.go`, `cmd/validation`.
- **Yeni dosyalar.** `internal/validation/integrity/precision_recall.go`,
  `_test.go` (dizin Sprint 0'da açılmış, boş)
- **Değişecek dosyalar.** `cmd/validation/main.go`,
  `internal/validation/pipeline/pipeline.go`
- **Planda düzeltilmesi gereken iki nokta.**
  1. **F.5 precision sorgusu `JOIN` kullanıyor, `LEFT JOIN` olmalı.**
     `ground_truth`'ta eşi olmayan bir bulgu (uydurulmuş `event_id`, yabancı
     koşu, kural 4 çıpası) iç birleştirmede **denominatörden düşer** ve
     precision sessizce yükselir. Yanlış pozitifi ölçümün dışına atan bir
     precision formülü, ölçmediği şeyi ölçüyor sanır.
  2. **Bölüm anahtarı (C/V) süzgeci uygulanmamalı.** Bütünlük tespitinde
     kalibre edilen bir şey yok; K4 enforcer'ının tip zorunluluğu bu hatta
     **taşınmamalı**, yoksa recall denominatörü %20'ye iner.
- **Riskler.** Sıfır bulgulu kuralda `NULLIF` → precision NULL. K7 "≥ %90"
  ifadesi NULL üzerinde tanımsız; önceden beyan edilmiş bir kural gerekiyor
  (Bölüm 5.5).
- **Test planı.** Birim: elle kurulmuş bulgu/etiket kümeleri üzerinde
  precision/recall (0, 1, kısmi, yinelenen). Entegrasyon: gerçek koşuda
  `integrity_metrics` satırları + SQL'in elle doğrulanması.
- **Tamamlanma.** Dört koşu için kural bazında precision/recall +
  karışıklık matrisi `integrity_metrics`'te ve raporda.

---

### T-E05-08 — Dört senaryo bütünlük koşumu + K7 raporu · 2 SP

- **Amaç.** A/B/C/D koşularında bütünlük hattını uçtan uca çalıştırmak, K7'yi
  ölçmek, `docs/results/sprint6-report.md` yazmak.
- **Neden gerekli?** K5 dört senaryoyu zorunlu kılıyor; kentsel/kırsal ayrımı
  kural 2'nin recall'unu 2,5 kat değiştiriyor (ölçüldü) — tek senaryo yanıltıcı
  olurdu.
- **Mevcut bileşenler.** `scripts/run-scenario.sh`, `compare-scenarios.sh`,
  `docs/results/*.run_id` (mevcut koşular yeniden kullanılabilir mi? **Hayır** —
  Kafka retention 7 gün, koşular 2026-07-29; akış kuralları için topic'in canlı
  olması gerekir. Yeni koşum şart.)
- **Yeni dosyalar.** `docs/results/sprint6-report.md`
- **Değişecek dosyalar.** `scripts/compare-scenarios.sh`
- **Riskler.** Topic'ler koşular arasında birikiyor (Sprint 5 borcu #5);
  akış kuralları için bu **daha tehlikeli**: eski koşuların kayıtları
  watermark'ı bozar. `run_id` süzgeci zorunlu ve topic tazeleme otomatikleşmeli.
  Dört tam koşum ~4–6 saat (Sprint 5 tecrübesi) — 4 günlük sprintte tek
  deneme hakkı var gibi planlanmalı.
- **Test planı.** Koşum sonrası `verify_integrity` 5/5 OK; `integrity_metrics`
  dolu; K7 tablosu raporda.
- **Tamamlanma.** Rapor yazılmış, K7 durumu (tuttu/tutmadı, kural bazında)
  gerekçeleriyle beyan edilmiş.

---

## 4. Bağımlılık grafiği, sıra ve kritik yol

```
T-E05-00  pkg/integrityrule
    │
    ▼
T-E05-01  kural motoru + migration 006  ◄── tüm kuralların önkoşulu
    │
    ├──────────────┬──────────────┬──────────────┬──────────────┐
    ▼              ▼              ▼              ▼              ▼
T-E05-02       T-E05-03       T-E05-04       T-E05-05       T-E05-06
kural 1        kural 3        kural 5        kural 2        kural 4
envanter       zaman ★        aktivite       hız ⚠          yörünge ?
(1 SP)         (2 SP)         (1 SP)         (3 SP)         (2 SP)
    │              │              │              │              │
    └──────────────┴──────────────┴──────────────┴──────────────┘
                                  │
                                  ▼
                          T-E05-07  cmd/integrity ★
                          (akış + toplu bağlantı)
                                  │
                                  ▼
                          T-E05-08  4 senaryo koşumu ★
                                  │
                                  ▼
                          T-E04-09  F.5 ölçümü ★
                          (kod paralel yazılabilir,
                           ölçüm 08'e bağlı)

★ kritik yol      ⚠ en yüksek risk      ? kapsam kararı bekliyor
```

**Kritik yol:** `T-E05-00 → T-E05-01 → T-E05-03 → T-E05-07 → T-E05-08 → T-E04-09`
(1 + 3 + 2 + 2 + 2 + 2 = **12 SP**)

Kural 3 kritik yolda çünkü **akış modunu zorunlu kılan tek kuraldır** ve
`cmd/integrity`'nin mimarisini o belirliyor. Kural 1, 4, 5 toplu ve bağımsız;
kural 2 kritik yolda değil ama **K7'nin kapısı** — gecikirse sprint hedefi
düşer, kritik yol düşmez.

**T-E04-09'un kodu kritik yolun dışında yazılabilir** (girdi şeması T-E05-01'de
sabitlenince). Ölçüm T-E05-08'i bekler. Bu paralelleştirme, dört senaryo
koşumunun uzun sürmesi nedeniyle önemli.

---

### 4.1 Efor karşılaştırması — plan 10 SP, gerçek 19 SP

| | Plan | Bu analiz |
|---|---|---|
| 5 tespit kuralı | 8 SP | 9 SP (02+03+04+05+06) |
| Ortak kural sözleşmesi | — | 1 SP |
| Kural motoru + migration | — | 3 SP |
| Servis bağlantısı | — | 2 SP |
| 4 senaryo koşumu + rapor | — | 2 SP |
| F.5 (T-E04-09) | 2 SP | 2 SP |
| **Toplam** | **10 SP** | **19 SP** |

Planın 8 SP'si yalnızca kural mantığını kapsıyor; motor, servis bağlantısı ve
koşum planlanmamış. Sprint 5 (16 SP planlı) fiilen 13 task + 5 hata
düzeltmesi olarak koştu ve 6 gün sürdü. **4 günde 19 SP gerçekçi değil.**

**Planın kendi kapsam düşürme kuralı (BÖLÜM H) uygulanmalı:**
> `3. S6 kural sayısı (5 → 3: envanter, hız, zaman)`

Ölçümlere göre bu listeyi **düzeltmeyi öneriyorum**: kural 5 (%100/%100, 1 SP)
kural 2'den (%72–83, 3 SP) hem daha ucuz hem daha değerli.

| Öneri | Kurallar | SP | K7 sonucu |
|---|---|---|---|
| **Çekirdek (öneri)** | 1, 3, 5 + motor + servis + ölçüm | 12 | Üç kuralda precision ≥ %90 kanıtlanır |
| Tam | + kural 2 | 15 | Kural 2 precision %80–90 aralığında raporlanır |
| Plan | + kural 4 | 19 | Kural 4 K7 kapsamı dışı beyan edilir |

---

## 5. Eksik, çelişkili ve belirsiz noktalar

### 5.1 Kural 4'ün F.5 ile ölçülemezliği — **çelişki, karar gerekiyor**

Plan iki şeyi birlikte söylüyor ve ikisi bir arada doğru olamaz:
- ADR-09/E.6: kural 4 olayı **siler**, `hts.records`'a hiçbir şey gitmez.
- F.5: recall `f.event_id = g.event_id` eşleşmesiyle ölçülür.

S4, var olmayan bir kaydın `event_id`'sini üretemez (girdileri `agent_id`,
`tick`, `seq` — hiçbirini görmüyor). Buna ölçülmüş sinyal yokluğu ekleniyor
(precision %1,4).

**Öneri:** ADR-30 ile kural 4, olay-çıpalı bulgu sınıfından çıkarılıp koşu
düzeyinde gösterge yapılsın; K7'nin precision eşiği ona uygulanmasın; gerekçe
(bilgi teorik + ölçüm) yazılı olarak belgelenmiş olsun.
**Reddedilen alternatif:** enjektörü kural 4'ü tespit edilebilir hâle
getirecek şekilde değiştirmek (örn. blok silme). Ölçüm sonucuna göre veri
üretecini değiştirmek, BÖLÜM J disiplininin ihlalidir — Sprint 5'te K1 için
yapılmayan şey burada da yapılmamalı.

### 5.2 Kural 2'nin precision'ı eşiğin altında — **en yüksek risk**

En iyi ölçülen tasarım: %71,8 (kentsel) / %83,4 (kırsal); kural önceliğiyle
~%80 / ~%90. K7 eşiği %90.

**Öneri:** kural 2'nin tam spesifikasyonu (eşik 300 km/h, atıf mekanizması,
öncelik, karar verilemezlik → bulgu yok) **Sprint 6 başlamadan yazılıp
dondurulsun**, sonra ölçülsün ve çıkan sonuç raporlansın. Ek olarak, bilimsel
ek katman:
- Duyarlılık eğrisi (eşik 100/200/300 km/h → recall) — ölçüm hazır:
  kentsel 113/58/58, kırsal 334/191/147.
- Karışıklık matrisi + "herhangi bir enjeksiyon" precision'ı: kural 3
  olaylarının kural 2'yi tetiklemesi bir dedektör hatası değil, F.5'in
  tam-eşleşme tanımının sonucudur. İki metrik ayrı raporlanmalı.

**Yapılmaması gereken:** precision %90'ı geçene kadar eşik/atıf ayarlamak.
Etiketli veri elimizde olduğu için bu teknik olarak kolay ve bilimsel olarak
geçersiz. Sprint 6'nın en büyük riski budur.

### 5.3 Akış mı toplu mu — **planda belirsiz, ADR gerekiyor**

Plan C.2'de S4 `hts.records` tüketicisi olarak çizilmiş, ama toplu iş
ihtiyacına hiç değinmiyor. Ölçüm net bir bölünme veriyor:

| Kural | Mod | Gerekçe |
|---|---|---|
| 1 envanter | ikisi de | durumsuz |
| 3 zaman | **yalnızca akış** | kanıt varış sırasında; toplu modda precision %8 |
| 2 hız | toplu (akış da olur) | olay zamanına göre sıralı 3'lü pencere |
| 5 aktivite | **yalnızca toplu** | akışta ilk-kayıt tuzağı precision'ı %50'ye düşürür |
| 4 yörünge | toplu (toplulaştırılmış) | koşu düzeyi istatistik |

**Öneri:** ADR-27 — melez model. Akış geçişi (kural 1 + 3) Kafka tüketicisi
olarak, toplu geçiş (kural 2 + 4 + 5) koşu sonunda tek `cmd/integrity`
ikilisinin iki modu olarak.
**Reddedilen alternatif:** `hts_records`'a `ingest_seq` (Kafka offset) sütunu
ekleyip her şeyi toplu yapmak. Kanıtı S4'ün kendi gözleminden persister'ın
yazdığı bir alana taşır; gerçek zamanlı bütünlük izleyicisi fikrini de bozar.

### 5.4 S4 örnekleme yapmamalı + yarım koşu koruması — **eksik**

ADR-14/24 örneklemesi analize aittir. S4 için:
- Kural 2/3/5 abone dizisine bağlı → örneklem diziyi yok eder.
- F.5 recall denominatörü tüm enjekte olaylar → örneklem recall'u anlamsız kılar.

Ayrıca yarım koşan bir S4 sessizce düşük recall üretir ve bu bir bulgu gibi
görünür (Sprint 5 hatası #4'ün birebir tekrarı). **Öneri:**
`run_config.inspected_records` sayacı + `verify_integrity` 5. denetimi
(`inspected_records = published_events`). ADR-23'ün simetrik tamamlanması.

### 5.5 K7'nin sıfır-bulgu ve düşük-sayı durumu — **belirsiz**

`NULLIF(count(*),0)` bir kural hiç bulgu üretmezse NULL döndürüyor. "Precision
≥ %90" NULL üzerinde tanımsız. Katı üçlü sınama ölçümünde kural 2 tek bulguyla
%100 precision veriyordu — teknik olarak "geçti", bilimsel olarak boş.

**Öneri (ölçümden önce beyan):**
- Bulgu sayısı 0 → kural "ölçülemedi", geçmedi de kalmadı da.
- Bulgu sayısı < 30 → precision Wilson %95 güven aralığıyla raporlanır ve
  "istatistiksel olarak yetersiz" etiketlenir.
- Bulgu sayısı ≥ 30 → K7 eşiği uygulanır.

### 5.6 F.5 precision sorgusundaki `JOIN` — **planda hata**

`FROM integrity_findings f JOIN ground_truth g ON ...` — eşi olmayan bulgu
denominatörden düşer, precision sessizce şişer. `LEFT JOIN` olmalı ve
`g.event_id IS NULL` durumu **yanlış pozitif** sayılmalı.

### 5.7 `integrity_findings` idempotanslığı yok — **eksik**

Tabloda `(run_id, event_id, rule_id)` üzerinde kısıt yok, `svc_integrity`'nin
UPDATE/DELETE hakkı da yok. Kafka at-least-once → yeniden işleme yinelenen
bulgu → precision denominatörü şişer. Migration 006 zorunlu.

### 5.8 `margin` / `evidence` sözleşmesi tanımsız — **belirsiz**

- `margin FLOAT NOT NULL` her kural için ne demek? (Kural 1'de eşik yok.)
- `Δt = 0` → hız sonsuz → Go `json.Marshal` **hata** verir, bulgu hiç yazılmaz.
- `evidence` şeması sürümlenmemiş; adli açıklanabilirlik iddiası şemasız JSONB
  ile savunulamaz.

**Öneri:** ADR-32 — kural başına `margin` birimi (ölçülen/eşik, boyutsuz,
≥ 1,0), sonluluk üst sınırı, `evidence` için `{"v":1, ...}` sürüm alanı ve
kural başına zorunlu alan listesi.

### 5.9 Ortak kural sözleşmesi yok — **eksik** (bkz. T-E05-00)

### 5.10 İçe alma grafiği S4 için genişletilmemiş — **eksik**

`tests/isolation/import_graph_test.go` yalnızca `internal/analysis` ağacını
denetliyor. `internal/integrity` → `internal/simulator` (özellikle `injector`!)
ve → `internal/analysis` yasakları test edilmiyor.

### 5.11 Compose'da servis tanımı yok — **minör**

`deployments/compose/docker-compose.yml` yalnızca altyapıyı içeriyor
(postgres, kafka, redis, prometheus, grafana). Uygulama servisleri betikle
koşuyor. S6 aynı deseni sürdürebilir; K9 (S8) için önemli olacak.

---

## 6. Gerekli yeni ADR'ler

| ADR | Karar | Neden zorunlu |
|---|---|---|
| **ADR-27** | Bütünlük tespiti melez çalışma modeli (akış + toplu; hangi kural nerede) | Kural 3 yalnızca akışta, kural 5 yalnızca toplu modda çalışıyor — ölçülü |
| **ADR-28** | Kural motoru: öncelik sırası ve tek-atıf (bir olay → ≤1 bulgu, bir geçiş → ≤1 bulgu) | Çapraz bulaşma precision'ı %50'nin altına indiriyor — ölçülü |
| **ADR-29** | Kural 2 atıf mekanizması (yerel destek) ve karar verilemezlikte bulgu üretmeme | Naif kural %36, atıflı %72–83 — tanımın parçası, iyileştirme değil |
| **ADR-30** | Kural 4'ün K7 kapsamı: olay-çıpalı bulgu değil, koşu düzeyi gösterge | `event_id` çıpası imkânsız + precision %1,4 |
| **ADR-31** | `integrity_findings` idempotanslığı, `integrity_metrics` tablosu, F.5'in `LEFT JOIN` düzeltmesi (migration 006) | Yinelenen bulgu ve iç birleştirme ölçümü sessizce bozuyor |
| **ADR-32** | `margin` ve `evidence` sözleşmesi (birim, sonluluk, şema sürümü) | `+Inf` bulgunun hiç yazılmamasına yol açar |
| **ADR-33** *(ops)* | S4 örnekleme yapmaz; `inspected_records` sayacı ve `verify_integrity` 5. denetimi | Yarım koşu düşük recall'u bilimsel bulgu gibi gösterir |

ADR-33, ADR-31'in içine katlanabilir. **Asgari yazılması gerekenler: ADR-27,
ADR-28, ADR-29, ADR-30, ADR-31.**

> **Ayrıca:** Sprint 5'ten devreden **arama bölgesi kararı** (K1) da bir ADR
> gerektiriyor (ADR-34 olur), ama E05'ten bağımsızdır — Bölüm 8.

---

## 7. Teknik risk değerlendirmesi

| # | Risk | Olasılık | Etki | Azaltma |
|---|---|---|---|---|
| R1 | **Etiketli veriye aşırı uydurma** — kural 2 %90'ı geçene kadar ayarlanır | Yüksek | **Kritik** (bilimsel geçersizlik) | Spesifikasyonu Sprint 6 başında dondur, ADR'ye yaz, sonucu olduğu gibi raporla |
| R2 | Kural 2 precision K7'yi tutmaz | **Yüksek** (ölçüldü: %72–83) | Yüksek | Önceden beyan; K1 gibi negatif bulgu olarak raporlanabilir |
| R3 | Kural 4 K7'yi tutmaz | **Kesin** (%1,4) | Yüksek | ADR-30 ile kapsam dışı; gerekçe ölçümle belgeli |
| R4 | Kafka partition-içi sıra varsayımı kırılır → kural 3 temiz kayıtlara bulgu yazar | Düşük | **Kritik** (precision %100 → çöküş) | Ön koşul entegrasyon testi: abone başına damga monotonluğu |
| R5 | Yinelenen bulgu (at-least-once) precision'ı sessizce düşürür | Orta | Yüksek | Migration 006 UNIQUE + `ON CONFLICT DO NOTHING` + idempotanslık testi |
| R6 | Yarım S4 koşusu düşük recall'u bulgu gibi gösterir | **Orta** (Sprint 5'te oldu) | Yüksek | `inspected_records` sayacı + 5. denetim |
| R7 | 4 günde 19 SP yetmez | **Yüksek** | Orta | Çekirdek kapsam (1, 3, 5) — 12 SP; kapsam düşürme kuralı önceden onaylı |
| R8 | `+Inf` / `NaN` bulguların hiç yazılmaması | Orta | Orta | ADR-32 sonluluk üst sınırı + PBT (`Marshal` hata vermez) |
| R9 | Topic birikmesi akış kurallarında watermark'ı bozar | Orta | Orta | `run_id` süzgeci (zorunlu) + koşum betiğinde topic tazeleme |
| R10 | Dört tam koşum 4–6 saat; tek deneme hakkı | Orta | Orta | Önce küçük koşu (100 ajan × 3 gün) ile hattı doğrula |
| R11 | S4 → analiz/simülatör bağımlılığı sızar | Düşük | Yüksek | İçe alma grafiği testini S4 için genişlet |

**En yüksek teknik risk: R4.** Kural 3, K7'nin en sağlam ayağı olacak
(yapısal %100 precision) ama bu güvence tek bir varsayıma dayanıyor:
`ProduceSync` + idempotent üretici + anahtar `pseudo_msisdn` → partition içi
sıra korunur. Varsayım sessizce kırılırsa kural 3, %100 precision'lı bir
kuraldan yüzlerce yanlış pozitif üreten bir kurala dönüşür ve bunu fark
etmenin tek yolu etiketlere bakmaktır — yani üretimde kör kalırız.

**En yüksek genel risk: R1**, ve o teknik değil metodolojik.

---

## 8. Sprint 5'ten devralınan teknik borçların etkisi

| # | Borç | Sprint 6'yı etkiler mi? |
|---|---|---|
| 1 | **Arama bölgesi tanımı** (K1'in tek engeli) | **Hayır.** S4 `estimates`'i hiç okumuyor; kütle modeli, kontur ve λ bütünlük tespitine girmiyor. K7, K1'den tamamen bağımsız ölçülür. Karar Sprint 6'ya sokulmamalı: model değişikliği dört senaryonun yeniden koşulmasını ve yeniden kalibrasyonunu gerektirir (~6 saat) ve 4 günlük bir sprintte E05 ile aynı pakete konursa ikisi birbirini yer. |
| 2 | **Kafka ACL yapılandırılmamış** (K6 katman 1 fiilen yok) | **Evet, dolaylı.** Sprint 6, `svc_integrity`'yi ilk kez üretim yolunda kullanan sprint. Katman 2 (PostgreSQL rolü) ve katman 4 (içe alma grafiği) çalışıyor; katman 1 hâlâ yok. K6 "kanıtlandı" (S0) beyanı bu boşlukla eksik kalıyor. S4'ün `hts.groundtruth`'a hiç abone olmaması kodda ifade edilmeli (analiz tüketicisindeki desen), ACL borcu ayrıca kapatılmalı. |
| 3 | `median_haversine_m` = `r50_m` totolojisi | Hayır — F.5 bu sütunları kullanmıyor. |
| 4 | Kalibrasyon 12 iterasyon düz eğride koşuyor (~30 dk/senaryo boşa) | **Evet, zaman olarak.** T-E05-08 dört senaryoyu baştan koşacaksa 2 saati kalibrasyonda boşa geçer. Bütünlük ölçümü λ'dan bağımsız olduğundan koşum betiğine `HTS_CALIBRATE=false` seçeneği eklenmeli — S6 koşumları kalibrasyonu atlayabilir. **Somut ve ucuz kazanç.** |
| 5 | Senaryolar arası topic tazeleme elle | **Evet, ve daha kritik.** Akış kuralları için eski koşuların kayıtları watermark'ı ve abone durumunu kirletir. `run_id` süzgeci koruyor ama tazeleme otomatikleşmeli. |

---

## 9. Test stratejisi

### 9.1 Katmanlar

| Katman | Kapsam |
|---|---|
| **Birim** | Her kural için elle kurulmuş kayıt dizileri: temiz, sınır (299/301 km/h), Δt=0, zincir başı/sonu, tek kayıtlı abone, NULL IMEI |
| **PBT (`rapid`)** | Değişmez #9–#14, aşağıda |
| **Altın senaryo** | Tek abone, 20 kayıt, her kuraldan bir enjeksiyon; beklenen bulgu kümesi **tam olarak** yazılı |
| **Gölge SQL çapraz kontrolü** | Toplu kuralların Go uygulaması ile bu raporun SQL sorguları aynı bulgu kümesini vermeli (Sprint 5'in PostGIS↔Haversine çapraz kontrolünün karşılığı) |
| **Entegrasyon** | Kafka → dedektör → `integrity_findings` round-trip; idempotanslık; `run_id` süzgeci; idle-çıkış; `inspected_records` denetimi |
| **İzolasyon (regresyon)** | `svc_integrity` → `ground_truth` → denied; içe alma grafiği: `internal/integrity` ↛ `internal/simulator`, ↛ `internal/analysis`; `pkg/integrityrule` bağımlılıksız |
| **Ön koşul** | Kafka partition-içi abone sıralaması monoton (R4'ün güvencesi) |

### 9.2 Yeni PBT değişmezleri (planın 8'ine ek)

| # | Değişmez |
|---|---|
| 9 | Bir olay için en çok bir bulgu üretilir (öncelik değişmezi, ADR-28) |
| 10 | `margin` sonlu, `NaN` değil ve tetiklenmiş kuralda ≥ 1,0 |
| 11 | `evidence` her girdi için `json.Marshal`'dan hatasız geçer |
| 12 | Aynı girdi akışı → aynı bulgu kümesi (determinizm, K10) |
| 13 | `cell_id ∈ envanter ⇔ kural 1 bulgusu yok` (tam karakterizasyon) |
| 14 | Zamanda monoton artan akışta kural 3 hiç bulgu üretmez |

### 9.3 Ölçüm doğrulama disiplini

1. Kural spesifikasyonları ve tüm eşikler **kod yazılmadan** ADR'lere yazılır.
2. Küçük koşu (100 ajan × 3 gün) ile hat doğrulanır — precision/recall'a
   **bakılmadan**.
3. Dört senaryo tam koşulur.
4. F.5 bir kez hesaplanır; sonuç ne olursa olsun raporlanır.
5. Spesifikasyon 4. adımdan sonra değiştirilirse, rapor bunu açıkça belirtir
   ve önceki ölçümü de gösterir.

---

## 10. Sprint 6 sonunda sistemin yetenekleri

**Yeni yetenekler:**
- Beşinci servis (S4) canlı: `hts.records` akışını gerçek zamanlı denetleyen ve
  koşu sonunda toplu geçiş yapan bir bütünlük izleyicisi.
- Manipüle edilmiş HTS kayıtlarının **kör** tespiti: S4 ne gerçek konumu ne de
  enjeksiyon etiketini görür; dört katmanlı izolasyon üretim yolunda çalışır.
- Adli açıklanabilirlik: her bulgu `margin` + sürümlü `evidence` ile
  "neden tetiklendi" sorusunu somut değerlerle cevaplar.
- Kural bazında precision/recall ölçümü ve 5×5 karışıklık matrisi;
  `integrity_metrics` ile tekrarlanabilir.
- Ölçeklenebilirlik özelliği: akış kuralları abone anahtarına göre durumlu,
  Kafka anahtarı `pseudo_msisdn` olduğundan replikalar arası konuşma
  gerektirmez — K9 (S8) için hazır bir yapı.

**Kapanan kabul kriterleri:**

| Kriter | Sprint 6 sonu durumu |
|---|---|
| **K7** | **Ölçülür.** Öngörü: kural 1 ve 5 → precision %100 (eşik ✓); kural 3 → %100 precision, recall ~%60 (✓); kural 2 → %80–90 (**muhtemelen ✗**, önceden beyan edilmiş negatif bulgu); kural 4 → ADR-30 ile kapsam dışı |
| **K6** | Katman 2 ve 4 **üretim yolunda** sınanmış olur (şu ana kadar yalnızca testte). Katman 1 (Kafka ACL) borcu kapanmazsa K6 formel olarak eksik kalır |
| K1–K5 | Değişmez (S4 `estimates`'e dokunmuyor) |
| K8 | Değişmez |
| K9, K10 | S8 |

**Sprint 6'nın dürüst özeti:** platform, beş manipülasyon türünün **üçünü**
yüksek kesinlikle, birini kısmi recall ile tespit edebilir; beşinci için
"bu veride bilgi yok" sonucunu ölçümle gösterir. Bu, beş kuralın hepsinin
çalıştığını iddia etmekten daha savunulabilir bir sonuçtur.

---

## 11. Sorulara doğrudan cevaplar

### Sprint 6 başlamadan çözülmesi gereken kritik bir problem var mı?

**Evet, üç karar + iki şema düzeltmesi.**

1. **Kural 4'ün kapsamı** (ADR-30). Karar alınmazsa K7 kesin olarak tutmaz ve
   bu bir mühendislik hatası gibi görünür.
2. **Kural 2'nin spesifikasyonunun dondurulması** (ADR-29). Şimdi
   dondurulmazsa Sprint 6 boyunca etikete bakarak ayarlama baskısı sürekli olur.
3. **Akış/toplu melez modelin onaylanması** (ADR-27). `cmd/integrity`'nin
   mimarisi buna bağlı; sonradan değiştirmek yeniden yazmak demek.
4. **Migration 006** — `integrity_findings` UNIQUE kısıtı (idempotanslık) ve
   `integrity_metrics` tablosu.
5. **F.5'in `LEFT JOIN` düzeltmesi** — mevcut sorgu yanlış pozitifleri
   denominatörden düşürüyor.

### Sprint 6 sırasında en yüksek teknik risk nedir?

İki katmanlı cevap:

- **Metodolojik (en yüksek):** kural 2'yi etiketli veri üzerinde %90'ı geçene
  kadar ayarlama baskısı. Teknik olarak kolay, bilimsel olarak geçersiz ve
  Sprint 5'te K1 için gösterilen dürüstlüğü geriye dönük olarak yok eder.
- **Teknik (en yüksek):** Kafka partition-içi abone sıralaması varsayımı (R4).
  Kural 3'ün %100 precision'ı tamamen buna dayanıyor ve kırıldığında kör
  kalırız. Ön koşul testi bu yüzden pazarlık konusu değil.

### Mimaride değiştirilmesi gereken bir karar görüyor musun?

Evet, üç tanesi — hiçbiri temel mimariyi değil, **E05'in eksik kalmış
varsayımlarını** düzeltiyor:

1. **ADR-09'un örtük simetrisi.** "Beş kural enjekte edilir, beş kural tespit
   edilir" varsayımı ölçümle çürüdü: kural 4'ün olay çıpası yok, kural 2'nin
   fiziksel recall tavanı %12. ADR-09 yanlış değil; **eksik** — tespit
   edilebilirlik simetrisi hiç tartışılmamış.
2. **F.5'in iç birleştirmesi** → `LEFT JOIN`.
3. **S4'ün "sadece bir Kafka tüketicisi" olarak çizilmesi** (C.2) → melez model.

Değiştirilmesi gerekmeyen ama **netleştirilmesi** gereken: K7'nin "kural
bazında" ifadesi (sıfır ve düşük bulgu sayısı durumları tanımsız).

### Yeni ADR yazılması gereken noktalar var mı?

Beş zorunlu: **ADR-27** (melez çalışma modeli), **ADR-28** (öncelik/tek-atıf),
**ADR-29** (kural 2 atıf mekanizması), **ADR-30** (kural 4 kapsamı),
**ADR-31** (idempotanslık + `integrity_metrics` + F.5 düzeltmesi).
İki opsiyonel: **ADR-32** (`margin`/`evidence` sözleşmesi — ADR-31'e
katlanabilir), **ADR-33** (S4 örneklemesizliği + `inspected_records` — ADR-31'e
katlanabilir).

Ayrıca Sprint 5'ten devreden **arama bölgesi kararı** bir ADR gerektiriyor
(ADR-34), ama E05'ten bağımsız ve Sprint 6'ya sokulmamalı.

### Sprint 6 sonunda hangi kabul kriterleri tamamlanmış olacak?

- **K7 — ölçülmüş olacak, kısmen tutacak.** Kural 1, 3, 5 eşiği geçer; kural 2
  büyük olasılıkla geçmez (önceden beyan edilmiş negatif bulgu); kural 4 ADR ile
  kapsam dışı.
- **K6 — güçlenir ama kapanmaz.** Katman 2 ve 4 ilk kez üretim yolunda
  sınanır; katman 1 (Kafka ACL) borcu açık kalır.
- **K1–K5** değişmez (Sprint 5 durumu: K2, K3, K4, K5 ✓ · K1 ✗ ölçülü),
  **K8** değişmez (✓), **K9/K10** Sprint 8'de.

Yani Sprint 6 tek bir yeni kriteri (K7) hedefliyor ve onu tamamen değil
**kural bazında ayrıştırılmış** olarak kapatacak.

---

## 12. Önerilen geliştirme sırası (yol haritası)

### Gün 0 — kod yazmadan (½ gün)

| Sıra | İş |
|---|---|
| 0.1 | **ADR-27, 28, 29, 30, 31** yazılır ve onaylanır. Tüm tespit eşikleri (300 km/h, atıf toleransı, minimum bulgu sayısı) burada dondurulur. |
| 0.2 | `configs/*.yaml` içine `integrity.detection.*` bölümü eklenir (henüz kullanılmadan). |
| 0.3 | Kapsam kararı: çekirdek (1, 3, 5) mi tam (+2) mi. Öneri: çekirdek + zaman kalırsa kural 2. |

### Gün 1 — temel (4 SP)

| Sıra | Task | Not |
|---|---|---|
| 1.1 | **T-E05-00** `pkg/integrityrule` | Enjektör ortak tipe geçirilir; mevcut testler yeşil kalmalı |
| 1.2 | **T-E05-01** motor + migration 006 | Sahte iki kuralla uçtan uca; idempotanslık testi burada yazılır |

### Gün 2 — yüksek kesinlikli kurallar (4 SP)

| Sıra | Task | Not |
|---|---|---|
| 2.1 | **T-E05-02** kural 1 (envanter) | En ucuz, en sağlam; motoru gerçek veriyle doğrular |
| 2.2 | **T-E05-03** kural 3 (zaman, akış) | **Kritik yol.** Kafka sıralama ön koşul testi burada yazılır |
| 2.3 | **T-E05-04** kural 5 (aktivite, toplu) | Toplu geçişi ilk kez kurar |

### Gün 3 — servis ve ölçüm hattı (4 SP)

| Sıra | Task | Not |
|---|---|---|
| 3.1 | **T-E05-07** `cmd/integrity` bağlantısı | `svc_integrity` DSN, `run_id` süzgeci, idle, `inspected_records`; koşum betiği |
| 3.2 | **T-E04-09** F.5 + karışıklık matrisi | `LEFT JOIN` düzeltmeli; kodu paralel yazılabilir |
| 3.3 | **Küçük koşu doğrulaması** (100 ajan × 3 gün) | Hat doğrulanır; precision/recall'a **bakılmaz** |

### Gün 4 — ölçüm ve rapor (3 SP)

| Sıra | Task | Not |
|---|---|---|
| 4.1 | **T-E05-08** dört senaryo koşumu | `HTS_CALIBRATE=false` ile ~2 saat kazanılır; topic tazeleme otomatik |
| 4.2 | F.5 hesabı, `integrity_metrics` yazımı | Bir kez; sonuç ne olursa raporlanır |
| 4.3 | `docs/results/sprint6-report.md` | K7 kural bazında; duyarlılık eğrisi ve karışıklık matrisi ek olarak |

### Zaman kalırsa (öncelik sırasıyla)

| Sıra | İş |
|---|---|
| E.1 | **T-E05-05** kural 2 (hız + atıf) — 3 SP, K7'nin dördüncü ayağı |
| E.2 | **T-E05-06** kural 4 göstergesi — 2 SP, ADR-30 kapsamında |
| E.3 | Kafka ACL borcu (K6 katman 1) — Sprint 5 borç #2 |
| E.4 | Arama bölgesi ADR'si (K1) — **ayrı bir çalışma olarak**, Sprint 6 içinde değil |

---

> **Kod yazılmamıştır.** Bu belge Sprint 6'nın ön analizidir; Bölüm 2'deki
> ölçümler Sprint 5 verisi üzerinde salt-okunur sorgularla yapılmıştır.
