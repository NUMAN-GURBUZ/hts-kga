# ADR-22 — `hts_records` Kalıcılaştırma Mimarisi

**Durum:** Kabul edildi
**Tarih:** 2026-07-29
**Sprint:** 5 (G2, T-E04-01)
**İlgili bulgular:** Plan boşluğu (E02/E04'te task yok), ADR-01, ADR-04, K6, K9

---

## Bağlam

`hts_records` tablosu şemada var (001_schema.sql), `verify_integrity` ona
bakıyor, Sprint 6'nın bütünlük kuralları onu okuyacak ve kalibrasyon döngüsü
kayıtları oradan çekecek — **ama onu dolduran hiçbir task yok.** E02 Kafka'ya
yayınlamayla bitiyor; E04 yalnızca ground truth persister'ını (T-E04-01,
ADR-04'ün S3a rolü) tanımlıyor. Sprint 4'te bu boşluk entegrasyon testinin
elle yazdığı satırlarla geçici olarak kapatılmıştı.

İki seçenek vardı: simülatör Kafka'ya yayınlarken **aynı zamanda** veritabanına
yazsın, ya da `hts.records` topic'ini tüketip yazan ayrı bir rol olsun.

---

## Karar

### 1. Ayrı kalıcılaştırıcı rolü (S1b)

`hts.records` → `hts_records` yazımı, `hts.groundtruth` → `ground_truth`
yazımıyla **aynı desenle** çalışan ayrı bir tüketici tarafından yapılır.

Gerekçeler:

- **Tek doğruluk kaynağı.** Simülatör hem Kafka'ya hem veritabanına yazsaydı
  aynı verinin iki üretim yolu olurdu. Aralarındaki bir sapmayı ADR-01
  bütünlük denetimi **göremezdi**: iki çıktı da aynı süreçten, aynı bellek
  durumundan üretildiği için birlikte hatalı olurlardı. Kafka'dan okuyan ayrı
  bir yol, yayınlananla yazılanı bağımsız olarak karşılaştırılabilir kılar
  (ADR-23 denetim 3).
- **Simetri.** S3a ile aynı offset disiplini, aynı hata yönetimi, aynı
  idempotans stratejisi. İki ayrı uygulama zamanla ayrışırdı.
- **Koşu süresi.** Simülatör 8.640 tick'i 39 saniyede yürütüyor; araya
  senkron veritabanı yazımı koymak bu süreyi veritabanının hızına bağlardı.
- **K9 hazırlığı.** Ölçekleme ölçümü (S8) için ikinci bir tüketici örneği.

### 2. Tek ikili, iki mod

`cmd/persister`, `HTS_PERSIST_MODE=records|groundtruth` ile çalışır. Roller
ayrı süreçler olarak, ayrı veritabanı kimlikleriyle dağıtılır
(`svc_gt_persister` yalnızca ground truth yazabilir).

Kör test (K6) bundan zarar görmez: mod, abone olunacak topic'i belirler ve
`records` modunda `hts.groundtruth`'a **hiç** abone olunmaz. Yetki sınırı
koddaki `switch` değil, veritabanı rolü ve Kafka ACL'idir.

### 3. Ortak çekirdek, tip parametresiyle

`internal/persist.Consumer[T]` çöz–tamponla–yaz–commit döngüsünü bir kez
uygular; roller yalnızca çözümleyici ve yazıcı fonksiyonlarını verir. Offset
disiplini (tampon **daima** commit'ten önce boşaltılır) böylece tek yerde
tanımlıdır.

### 4. Zehirli satır tek bir kaydı düşürür, koşuyu değil

Bir satır CHECK kısıtını ihlal ederse PostgreSQL tüm partiyi reddeder. Hata
yukarı verilip servis düşseydi, yeniden başlayan tüketici aynı partiyi okur ve
aynı yerde düşerdi — 300.000 kaydın tamamı tek bozuk satır yüzünden
yazılamazdı.

Karar: parti düşerse satır satır yeniden denenir; hâlâ reddedilen satırlar tam
ayrıntısıyla günlüğe geçer ve `Failed()` sayacına eklenir. **Kayıp sessiz
değildir:** ADR-23'ün `published_vs_stored_records` denetimi farkı FAIL olarak
raporlar.

### 5. İdempotans `ON CONFLICT DO NOTHING` ile

Tüketici at-least-once çalışır (offset yalnızca yazma bittikten sonra
ilerler). Yeniden işlenen kayıt yinelenen satır üretmez; UNIQUE indeks
`(run_id, event_id, time)` tekilliği zaten garanti eder.

---

## Bu kararın ortaya çıkardığı hata

G2 entegrasyon testi ilk koşuşunda `hts_records_event_type_check` ihlali
verdi: simülatör `CALL` üretiyordu, şema (plan BÖLÜM D) telekom standardı
`MOC`/`MTC` bekliyordu. Uyumsuzluk Sprint 2'den beri kodda duruyordu ve
**yalnızca gerçekten veritabanına yazıldığında** görünür oldu — Kafka'ya
yayınlamak şemayı doğrulamaz.

Düzeltme simülatör tarafında yapıldı (`EventMOC`, `EventMTC`); şema plandan
geldiği için değiştirilmedi.

---

## Sonuçlar

- `hts_records` artık gerçek koşularda doluyor; `verify_integrity` dört
  denetimin üçünü çalıştırabiliyor (dördüncüsü analiz sayacını bekliyor).
- Kalibrasyon replay'i (T-E04-06) kayıtları veritabanından çekebilecek.
- Bütünlük servisi (S6) için veri hazır.

## Reddedilen alternatifler

**Simülatörün doğrudan yazması.** Daha az hareketli parça, ama iki üretim yolu
arasındaki sapmayı ölçülemez kılıyordu ve koşu süresini veritabanına
bağlıyordu.

**Ayrı iki ikili (`cmd/records-persister`, `cmd/gt-persister`).** Plan C.3'ün
klasör yapısına iki yeni girdi eklerdi; rol ayrımı zaten dağıtım düzeyinde
(farklı kimlik, farklı süreç) yapılıyor.

**Hatalı partide servisi düşürmek.** "Sessiz veri kaybı olmasın" iyi niyeti,
tek bozuk satırın tüm koşuyu bloke etmesiyle sonuçlanırdı. Görünür kayıp,
görünmez tıkanmadan iyidir.
