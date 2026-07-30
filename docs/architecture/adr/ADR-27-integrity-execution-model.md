# ADR-27 — Bütünlük Tespitinin Çalışma Modeli: Akış + Toplu Melez

**Durum:** Kabul edildi
**Tarih:** 2026-07-30
**Sprint:** 6 (T-E05-01, T-E05-03, T-E05-04, T-E05-07)
**İlgili bulgular:** BÖLÜM C.2, ADR-09, ADR-14, ADR-24, K7

---

## Bağlam

Plan BÖLÜM C.2, S4'ü tek bir şey olarak çiziyor: `hts.records` topic'ini
tüketen bir Kafka istemcisi. Bu resim, beş tespit kuralının hepsinin aynı girdi
şeklinden beslenebileceğini varsayıyor. Sprint 6 ön analizinde bu varsayım
Sprint 5 verisi üzerinde ölçüldü ve **iki kuralda kırıldı**.

### Kural 3 (zaman) — kanıt yalnızca varış sırasında var

Enjeksiyon kural 3 damgayı 2 saat geriye kaydırır. Kayıt, `hts_records`'a
yazıldıktan sonra zamana göre sıralandığında **sessizce yeni yerine oturur**:
kaydırılmış olduğuna dair hiçbir iz kalmaz.

Toplu modda kalan tek dolaylı sinyal "aynı abone + aynı damga, farklı hücre"
durumudur ve ölçüldü:

| Mod | Bulgu | Kural 3 | Precision |
|---|---|---|---|
| Toplu (zamana göre sıralı) | 265 | 22 | **%8,3** |
| Akış (varış sırası) | — | — | **yapısal %100** |

Akış modunda kanıt yapısaldır: üretim sırası tick-major, tick içi damgalar
eşit, Kafka anahtarı `pseudo_msisdn` olduğu için abone başına sıra tek
partition'da korunur. Damgaya dokunan **tek** kural 3 olduğundan başka hiçbir
kural bu sinyali tetikleyemez.

### Kural 5 (aktivite) — kanıt yalnızca kapalı popülasyonda var

Enjeksiyon kural 5, kaydın `pseudo_imei`'sini başka bir ajanın cihazıyla
değiştirir. Tespit, abonenin **modal** IMEI'sini referans almaktır; ölçüm dört
koşuda precision %100 / recall %100 verdi.

Akış modunda ise referans belirlenemez. İlk görülen IMEI referans alınırsa ve
bir abonenin *ilk* kaydı enjekte edilmişse, o abonenin sonraki ~293 temiz kaydı
bulgu üretir. Beklenen etki 1000 abone × %0,4 ≈ 4 abone × 293 kayıt ≈ **1170
hatalı bulgu** — precision %100'den ~%50'ye düşer. Çoğunluk oyuna geçmek ise
erken yazılmış bulguların **geri alınmasını** gerektirir; `integrity_findings`
ekle-yalnız bir tablodur ve geri alma adli izlenebilirliği bozar.

### Üçüncü mesele: örnekleme

ADR-14 ve ADR-24 örnekleme filtresini tanımlar ve ADR-24 filtreyi "kütle
hesabından önce" koyar. Bu kararlar **analiz motoruna** aittir; S4 için hiçbir
yerde bir şey söylenmemiş. Söylenmemesi tehlikeli, çünkü mevcut tüketici
deseni (`internal/analysis/driver/consumer.go`) örneklemeyi zorunlu bir alan
olarak taşıyor ve kopyalanırsa S4'e sessizce sızar.

---

## Karar

### 1. Kural sınıfı tipte ifade edilir

`pkg/integrityrule` her kurala bir **sınıf** atar:

| Sınıf | Girdi | Sıra | Durum |
|---|---|---|---|
| `ClassStream` | Kafka `hts.records` | **varış** | abone başına O(1) |
| `ClassBatch` | `hts_records` tablosu | **olay zamanı**, abone bazlı | abone dizisi penceresi |
| `ClassAggregate` | `hts_records` | koşu düzeyi | koşu özeti |

Atama:

| Kural | Sınıf | Neden bu sınıf |
|---|---|---|
| 1 envanter | `ClassStream` | Durumsuz; her iki fazda çalışırdı. Akışta konur çünkü maliyeti yok ve talebi (ADR-28) toplu faza hazır olur |
| 3 zaman | `ClassStream` | Kanıt varış sırasında; toplu modda yok olur |
| 5 aktivite | `ClassBatch` | Kapalı popülasyonda çoğunluk gerekir; akışta ilk-kayıt tuzağı |
| 2 hız | `ClassBatch` | Olay zamanına göre sıralı 5'lik pencere gerekir |
| 4 yörünge | `ClassAggregate` | Olay çıpası yok (ADR-30) |

Sınıf bir yorum değil, motorun **kurulum zamanında** denetlediği bir kısıttır:
bir `ClassBatch` kuralı akış fazına verilemez. Yanlış faz bir çalışma zamanı
hatası bile değildir — kurulum başarısız olur.

### 2. Faz sırası ve devir

```
1  cmd/simulator                     → hts.records, hts.groundtruth
   ├──────────────────────────────────────────────────────────────┐
2  persister (records)  ∥  persister (gt)  ∥  integrity (stream)
   → hts_records           → ground_truth    → integrity_findings
                                              → run_config.inspected_records
   └──────────────────────────────────────────────────────────────┘
3  integrity (batch)   önkoşul: (a) hts_records tam  (b) akış fazı bitti
   → integrity_findings
4  analysis-engine     (bütünlükten bağımsız)
5  validation          F.1–F.4 → metrics ;  F.5 → integrity_metrics
6  verify_integrity    5 denetim
```

Akış fazı persister'larla **paralel** koşar: ayrı tüketici grubu, aynı topic —
Kafka semantiği bunu doğrudan destekler. Kazanç yalnızca duvar saati değildir:
canlı akış üzerinde bütünlük izlemek, "koşu bittikten sonra bir SQL sorgusu
yazdık" iddiasından bilimsel olarak farklı bir şeydir.

### 3. Toplu faz iki önkoşulu denetler ve sağlanmazsa reddeder

```
(a) count(hts_records WHERE run_id) = run_config.published_events
    → kayıtlar tam (records persister bitti)
(b) run_config.inspected_records IS NOT NULL
    → akış fazı bitti (kural 1/3 talepleri yazılı)
```

(b) olmadan koşulursa kural 1'in dışlamaları eksik olur ve kural 2 bilinmeyen
konumlu kayıtları zincire alır. Sessizce eksik veriyle koşmak, düşük recall'u
bilimsel bulgu gibi gösterir — ADR-04'ün önkoşul disiplini burada da geçerlidir.

### 4. S4 örnekleme yapmaz

`cmd/integrity` yapılandırmasında `sampling.Policy` alanı **bulunmaz**.
İki gerekçe:

- Kural 2/3/5 abone dizisine bağlıdır; örneklem diziyi delik deşik eder.
- F.5'in recall denominatörü `injected_rule IS NOT NULL` olan **tüm** ground
  truth satırlarıdır. Örneklenmiş bir dedektör bu denominatörle uyuşmaz ve
  recall yapay olarak düşer.

ADR-14/ADR-24 ile çelişmiyoruz: o kararlar analiz motorunun ızgara maliyetini
sınırlamak içindir. S4'ün olay başına maliyeti sabittir (hash aramaları ve bir
karşılaştırma), 300.000 olay örneklemeye gerek bırakmaz.

### 5. Servis modları

`cmd/persister`'ın kurulmuş desenini izler (env ile mod seçme):

| `HTS_INTEGRITY_MODE` | Davranış |
|---|---|
| `stream` | Kafka tüketicisi; `ClassStream` kuralları; `inspected_records` yazar; `HTS_IDLE_TIMEOUT` ile çıkar |
| `batch` | Önkoşul denetimi → `hts_records` sıralı tarama; `ClassBatch` kuralları |
| `all` | `stream` sonra `batch` (yerel geliştirme kolaylığı) |

Ortak: `HTS_RUN_ID` zorunlu, DSN rolü `svc_integrity`, health `:8084`,
OTel + `traceparent` çıkarımı.

---

## Sonuçlar

**Kazanç.** Her kural kanıtının bulunduğu fazda koşar. Kural 3 %100 precision'a,
kural 5 %100/%100'e ulaşır; ikisi de tek modlu bir tasarımda kaybedilirdi.

**Maliyet.** İki faz, iki girdi kaynağı, bir devir noktası (ADR-28'in talep
defteri) ve iki önkoşul denetimi. Tek modlu bir tasarımdan karmaşık; ama
karmaşıklık ölçülmüş bir kazanç karşılığında alınıyor.

**Kör test bozulmuyor.** İki faz da yalnızca `hts_records`/`hts.records` ve
`cells` okur. `ground_truth`'a ne akış fazı ne toplu faz erişir; rol
(`svc_integrity`) zaten reddeder.

**Ölçeklenebilirlik.** Akış kuralları abone anahtarına göre durumludur ve Kafka
anahtarı `pseudo_msisdn` olduğundan bir abonenin tüm kayıtları tek partition'a
düşer: 4 partition → 4 replika, replikalar arası durum paylaşımı **gerekmez**.
Bu, `pkg/kafka`'daki anahtar seçiminin zaten beyan edilmiş gerekçesiydi.

---

## Reddedilen alternatifler

**(A) Her şeyi toplu yapmak, `hts_records`'a `ingest_seq` (Kafka offset) sütunu
ekleyerek.** Varış sırasını veriye yazmak kural 3'ü toplu modda tespit edilebilir
kılardı. Reddedildi: kanıtı S4'ün kendi gözleminden persister'ın yazdığı bir
alana taşır — S4 artık bir şey *gözlemlemiyor*, başka bir servisin beyanına
güveniyor olurdu. Gerçek zamanlı bütünlük izleyicisi fikri de ortadan kalkardı.

**(B) Her şeyi akışta yapmak, kural 5 için geri alma (retraction) ile.**
Reddedildi: `integrity_findings` ekle-yalnız bir adli kayıttır; bulgu silmek
veya güncellemek delil zinciri tartışmasında savunulamaz. Ayrıca
`svc_integrity`'nin UPDATE/DELETE yetkisi yoktur ve verilmemelidir.

**(C) İki ayrı servis (S4a akış, S4b toplu).** Reddedildi: ikisi aynı kural
motorunu, aynı envanteri ve aynı yazıcıyı kullanıyor. `internal/persist`'in tek
çekirdekle iki rolü çözmesiyle aynı gerekçe — ayrı yazılsalar öncelik disiplini
iki yerde tekrarlanır ve zamanla ayrışır.

---

## İlgili

ADR-04 (batch önkoşul disiplini) · ADR-09 (enjeksiyon, kör test) ·
ADR-14 / ADR-24 (örnekleme — analiz motoruna özgü) · ADR-23 (koşu sayaçları) ·
ADR-28 (öncelik ve talep defteri) · ADR-31 (veri modeli ve sayaç)
