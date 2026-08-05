# ADR-32 — Kafka Kimlik Doğrulama ve Yetkilendirme: Kör Testin 1. Katmanı

**Durum:** Kabul edildi
**Tarih:** 2026-07-30
**Sprint:** 7 (K6 kapanışı — Sprint 5 borcu #2)
**İlgili bulgular:** K6, BÖLÜM C.2, ADR-09, ADR-15, T-E01-08

---

## Bağlam

Plan BÖLÜM C.2 kör testi üç katman olarak tanımlıyor:

```
1. Kafka ACL      : analysis/integrity principal → hts.groundtruth → DENY
2. PostgreSQL rol : analysis/integrity rolü      → ground_truth    → SELECT yok
3. Enjeksiyon etiketi (injected_rule) ground_truth'ta → S4 göremez
```

Sprint 5 raporu (borç #2) ve Sprint 6 raporu, **1. katmanın fiilen var
olmadığını** kaydetti. Ölçülen durum:

| Bulgu | Kanıt |
|---|---|
| Broker PLAINTEXT dinliyor | `KAFKA_LISTENERS: PLAINTEXT://:9092` |
| `authorizer.class.name` tanımlı değil | compose ortam değişkenlerinde yok |
| `scripts/kafka-setup.sh` ACL komutları veriyor | ama kendi yorumunda "PLAINTEXT'te zorunlu değildir" diyor |

Yetkilendirme mekanizması olmadan ACL kayıtları **uygulanmaz**. Daha temeli:
PLAINTEXT'te istemcinin bir kimliği yoktur — her bağlantı `ANONYMOUS`
principal'ıdır. `User:svc_analysis` diye bir özne yoktur, dolayısıyla ona
uygulanacak bir DENY de yoktur.

### Bu bir plan çelişkisi mi

Hayır — **uygulanmamış bir önkoşul.** ADR-15 kimliği açıkça tanımlıyor:

> Kimlik doğrulama kapsam dışı olduğundan "kim" = insan kullanıcı değil;
> sistemde zaten tanımlı olan **Kafka principal / PostgreSQL rolü**dür.

ADR-15'in "kapsam dışı" dediği şey **insan kullanıcı** kimlik doğrulamasıdır
(API'nin oturum yönetimi, JWT, kullanıcı hesapları). Servis kimliğini ise
mekanizma olarak **varsayıyor** ve denetim izinin temeline koyuyor. PostgreSQL
tarafında bu varsayım karşılanmış (dört `svc_*` rolü, migration 004); Kafka
tarafında karşılanmamış.

Dolayısıyla seçenek "kriteri gevşetmek" değil, "varsayımı gerçekleştirmek".

---

## Karar

### 1. Üç dinleyici: yayınlanan port SASL, yönetim portu iç PLAINTEXT

| Dinleyici | Protokol | Yayınlanır mı | Kullanım |
|---|---|---|---|
| `SASL://:9092` | `SASL_PLAINTEXT` | **evet** | Tüm servis istemcileri |
| `INTERNAL://:9094` | `PLAINTEXT` | **hayır** | Broker'lar arası + kurulum CLI'ı |
| `CONTROLLER://:9093` | `PLAINTEXT` | hayır | KRaft controller quorum'u |

Yetkilendirme: `org.apache.kafka.metadata.authorizer.StandardAuthorizer`
(KRaft modunun yerleşik authorizer'ı).
Süper kullanıcı: `User:ANONYMOUS` — **yalnızca 9094'ten ulaşılabilir.**

#### Neden yönetim portu SASL değil

SASL/SCRAM'da tavuk-yumurta problemi vardır: SCRAM kimlikleri broker meta
verisinde durur, meta veriyi yazmak için bağlanmak gerekir, bağlanmak için
kimlik gerekir. Standart çözüm `kafka-storage.sh format --add-scram` ile
kimliği format anında meta veri günlüğüne gömmektir — ama `apache/kafka:3.9.0`
imajının kendi format sarmalayıcısı (`KafkaDockerWrapper setup`) bu bayrağı
desteklemiyor (ölçüldü). Sarmalayıcıyı atlatmak, imajın `KAFKA_*` ortam
değişkenlerini `server.properties`'e çeviren mantığını elle yeniden yazmak
demektir; kırılgan ve bu kararın konusu değil.

Bunun yerine kimlikler broker ayağa kalktıktan sonra, **yayınlanmayan** bir iç
port üzerinden yazılır.

#### Bu, kör test iddiasını zayıflatmıyor

Katman 1'in kanıtlaması gereken şey şudur: *analiz motoru, yayınlanan porta
`svc_analysis` kimliğiyle bağlandığında `hts.groundtruth`'u okuyamaz.* 9094
konteyner içi bir arayüzdedir ve dışarıya açılmaz; ona erişmek `docker exec`
düzeyinde erişim demektir — o düzeyde saldırgan zaten disk üzerindeki ham log
segmentlerini okuyabilir. Yani iç port, kör testin koruduğu sınırın **dışında**
kalır.

Kör test servis kimliğinin uygulama topolojisindeki yetkisiyle ilgilidir,
konteyner kaçışıyla değil. ADR-15 de aynı sınırı çiziyor: denetlenen şey
"servis kimliği (principal)"dır.

**TLS kullanılmaz.** `SASL_PLAINTEXT` kimlik doğrular ama şifrelemez. Kör testin
ihtiyacı **kimlik ve yetki**dir, gizlilik değil; broker tek makinede çalışıyor.
Üretim dağıtımında değişecek tek şey dinleyicinin protokolüdür (`SASL_SSL`).

**Neden SCRAM, PLAIN değil.** SCRAM parolaları broker meta verisinde tuzlanmış
karma olarak durur ve `kafka-configs.sh` ile çalışma zamanında yönetilir; PLAIN
parolaları broker JAAS dosyasına düz metin yazmayı ve yeni principal için
broker'ı yeniden başlatmayı gerektirir.

### 2. Beş principal, en az yetki

| Principal | `hts.records` | `hts.groundtruth` | Gerekçe |
|---|---|---|---|
| `svc_simulator` | **WRITE** | **WRITE** | Tek üretici (ADR-22: simülatör iki topic'e yayınlar) |
| `svc_records_persister` | READ | — | `hts_records` tablosuna yazar (ADR-22) |
| `svc_gt_persister` | — | READ | S3a; yalnızca ground truth (ADR-04) |
| `svc_analysis` | READ | **YETKİ YOK** | **Kör test katman 1** |
| `svc_integrity` | READ | **YETKİ YOK** | **Kör test katman 1** |

Tüketiciler ayrıca kendi grupları üzerinde `READ` (group) yetkisi alır.

**Yasak, DENY kuralıyla değil yetki vermeyerek kurulur.** Kafka'nın varsayılanı
`allow.everyone.if.no.acl.found=false` olduğunda "ACL yoksa reddet"tir. Açık bir
DENY kuralı eklemek yerine ALLOW vermemek daha güçlüdür: bir gün biri geniş bir
ALLOW eklerse DENY onu ezerdi, ama yetki listesinin kendisi boşsa gözden kaçan
bir kapı da olmaz. `svc_analysis` ve `svc_integrity` için `hts.groundtruth`
üzerinde **hiçbir kayıt yoktur.**

### 3. Süper kullanıcı yalnızca iç portta ve yalnızca kurulum içindir

`User:ANONYMOUS` süper kullanıcıdır ve yalnızca yayınlanmayan 9094'ten
ulaşılabilir. Topic, ACL ve SCRAM yönetimini `make seed-kafka` bu port üzerinden
yapar. **Hiçbir servis 9094'e bağlanmaz**; servisler 9092'ye kendi SCRAM
kimlikleriyle bağlanır.

### 4. Kimlik bilgileri `.env`'den gelir, koda gömülmez

Her servis kendi principal'ını ve parolasını ortam değişkeninden okur — DSN
rolünün `svc_integrity` olarak verildiği desenin aynısı (Sprint 6). `.env`
commit edilmez (bkz. proje kuralı); `.env.example` şablonu taşır.

```
KAFKA_SASL_USER      / KAFKA_SASL_PASSWORD      (servis başına ayarlanır)
KAFKA_ADMIN_USER     / KAFKA_ADMIN_PASSWORD     (yalnızca kurulum betiği)
```

`pkg/kafka` bu değişkenleri okuyup `kgo` seçeneklerine çevirir. Değişken boşsa
istemci **SASL'sız** kurulur: geliştirme kolaylığı için değil, mevcut
entegrasyon testlerinin SASL kurulu olmayan bir broker'da da koşabilmesi için.
Bu davranış açıkça belgelenir ve izolasyon testi SASL'ın **kurulu olduğu**
ortamda gerçek reddi doğrular.

### 5. K6 katman 1 teste bağlanır

`scripts/verify-isolation.sh` genişletilir: `svc_analysis` kimliğiyle
`hts.groundtruth` topic'inden okuma denemesi yapılır ve
**`TOPIC_AUTHORIZATION_FAILED`** beklenir. Beklenen hata gelmezse betik
başarısız olur.

Katman 2'nin (PostgreSQL) zaten böyle bir regresyonu var
(`permission denied` denetimi); katman 1 aynı disipline alınır.

---

## Sonuçlar

**K6 kapanır.** Üç katmanın (artı ADR-20'nin içe alma grafiği katmanının)
hepsi kurulu ve testli olur.

**Maliyet.** Broker yapılandırması, beş SCRAM kimliği, `pkg/kafka`'da seçenek
katmanı, altı istemci çağrı noktası, kurulum betiği ve bir izolasyon testi.
Bilimsel hattın hiçbir parçası değişmez: aynı topic'ler, aynı anahtarlar, aynı
sıralama garantisi. ADR-27'nin dayandığı partition-içi sıra varsayımı da
etkilenmez (SASL taşıma katmanıdır, bölümlemeye dokunmaz).

**Mevcut koşular etkilenmez.** Veritabanındaki Sprint 5/6 ölçümleri yerinde
kalır; yeni koşular SASL üzerinden yapılır.

---

## Reddedilen alternatifler

**(A) PLAINTEXT'te kalıp K6'yı "iki katman + içe alma grafiği" olarak yeniden
tanımlamak.** Reddedildi: bu, ölçüm sonucuna göre kabul kriterinin **içeriğini**
değiştirmek olurdu. K6 üç katman diyor; katmanı kurmak, tanımı daraltmaktan
yeğdir.

**(F) Broker'lar arası dinleyiciyi de SASL yapmak (JAAS dosyasıyla).**
Reddedildi: tek düğümlü bir broker'da kimlik doğrulanacak ikinci bir broker
yoktur; JAAS dosyası yalnızca tavuk-yumurta problemini bir dosyaya taşır ve
parolayı repo'ya sokar.

**(B) `SASL_SSL` (TLS ile).** Reddedildi: sertifika üretimi ve dağıtımı getirir,
kör test iddiasına hiçbir şey eklemez. Kör testin sorusu "bu servis o veriyi
okuyabilir mi", "hat dinlenebilir mi" değil.

**(C) SASL/PLAIN mekanizması.** Reddedildi: parolalar broker JAAS dosyasında düz
metin durur ve yeni principal broker yeniden başlatması gerektirir. SCRAM
kimlikleri çalışma zamanında yönetilir.

**(D) Açık DENY kuralları.** Reddedildi: `allow.everyone.if.no.acl.found=false`
ile yetki vermemek daha güçlü bir yasaktır (madde 2).

**(E) Kafka'yı `authorizer` ile ama tek ortak principal ile çalıştırmak.**
Reddedildi: tek principal'de `svc_analysis` ile `svc_gt_persister` ayırt
edilemez ve kör test kurulamaz.

---

## İlgili

BÖLÜM C.2 (kör test üç katman) · K6 · ADR-09 (enjeksiyon etiketi) ·
ADR-15 (servis kimliği = Kafka principal / PostgreSQL rolü) ·
ADR-20 (içe alma grafiği — 4. katman) · ADR-27 (partition-içi sıra varsayımı) ·
T-E01-08 · Sprint 5 raporu borç #2
