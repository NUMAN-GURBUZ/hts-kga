# ADR-24 — Analiz Örneklemesinin Uygulanma Yeri

**Durum:** Kabul edildi
**Tarih:** 2026-07-29
**Sprint:** 5 (G3, T-E04-07)
**İlgili bulgular:** ADR-14, ADR-04, K4, K6, K9, K10

---

## Bağlam

ADR-14 her olayın analiz edilmeyeceğini söylüyor: kalibrasyon 5.000 olaylık
örneklemle, final ölçüm `partition_key='V'` kümesiyle çalışır. Ama Sprint 4'ün
analiz motoru **her kaydı** işliyor ve her kayıt için beş `estimates` satırı
yazıyordu. 300.000 olaylık gerçek bir koşuda bu:

- 1,5 milyon geometri satırı (~3,5 GB/senaryo),
- D senaryosunda ~2,4 saat CPU

demekti. Yani ADR-14 kâğıtta vardı, kodda yoktu.

İkinci sorun: örnekleme `partition_key` üzerinden tanımlı ama o sütun
`ground_truth` tablosunda ve analiz servisi o tabloyu **göremez** (K6 kör
testi). Filtre naif biçimde yazılsaydı ya kör test delinirdi ya da örnekleme
uygulanamazdı.

---

## Karar

### 1. Filtre kütle hesabından **önce** uygulanır

`driver.Consumer`, kaydı çözdükten hemen sonra politikaya danışır; elenen olay
için ne kütle üretilir ne de satır yazılır.

Yazma anında filtrelemek yalnızca diski kurtarırdı; CPU'nun tamamı yine
harcanırdı — ki maliyetin ezici kısmı kütle üretimidir (kırsalda olay başına
8.872 hücre).

### 2. C/V ayrımı `pkg/split` ile türetilir, veritabanından okunmaz

Bölüm yalnızca `event_id` ve koşu tohumundan hesaplanır. `ground_truth`
tablosuna hiç gidilmez, dolayısıyla kör test delinmez.

Bu bir sızıntı değildir: bölüm anahtarı olayın **konumu** hakkında hiçbir şey
taşımaz; hangi kümeye düştüğünü söyler, nerede olduğunu değil. Ayrım zaten
`pkg/split`'te "dondurulmuş sözleşme" olarak tanımlıdır ve simülatör de aynı
tanımı kullanır.

### 3. Üç mod

| Mod | Küme | Kullanım |
|---|---|---|
| `validation` | 'V' kümesinin tamamı | Final ölçüm (K1–K3) — **varsayılan** |
| `calibration` | 'C' içinden ~N olay | Bisection döngüsü (ADR-02) |
| `full` | her olay | K9 ölçekleme ölçümü (S8) |

Politikanın sıfır değeri geçersizdir: tüketici açık bir mod ister. Sessiz bir
"hepsini işle" varsayımı, ADR-14'ü kazara devre dışı bırakırdı.

### 4. Akışta oran, replay'de kesin sayı

Akışta gerçek olay sayısı önceden bilinemez; kalibrasyon seyreltmesi
`hedef / beklenen_C` oranıyla yapılır ve hedefi ±%5 tutturur. Beklenen hacim
**tahmin edilmez**, `run_config.published_events`'ten (ADR-23) okunur — koşu
bitmeden kalibrasyon modu çalışmayı reddeder.

Kalibrasyon döngüsü kayıtları veritabanından okur ve orada sayı kesindir;
`ExactSample` karma sırasına göre **tam N** olay seçer. Bu, 12 iterasyonun
aynı küme üzerinde koşmasını garanti eder: küme iterasyonlar arasında
değişseydi, `coverage(λ)` farkının λ'dan mı örneklem gürültüsünden mi geldiği
ayırt edilemezdi.

### 5. Seyreltme karması bölümleme karmasından ayrıdır

`pkg/split` SHA-256 kullanır çünkü ayrım bir **sözleşmedir** (SQL'de veya bir
denetçinin kendi aracında yeniden üretilebilmelidir). Seyreltme ise analiz
motorunun iç kararıdır; FNV-1a + splitmix64 sonlandırıcısı yeterlidir.

Aynı değer ikisi için kullanılsaydı seyreltme, 'C' kümesinin yalnızca
başlangıcından (uniform değeri küçük olanlardan) seçerdi — örneklem, bölümün
rastgele bir alt kümesi olmazdı.

---

## Uygulamada yakalanan hata

İlk sürümde tohum FNV-1a girdisinin **sonuna** ekleniyordu. FNV-1a'da çarpma
bitleri düşükten yükseğe taşır; sıralama üst 53 bitten türetildiği için
tohumun etkisi kayboluyordu: farklı tohumlar **aynı örneklemi** veriyordu.
`TestExactSample_Deterministic` bunu yakaladı. Düzeltme: tohum başa yazılır ve
sonuç splitmix64 ile karıştırılır. Düzeltmeden sonra iki tohumun kesişimi
20.000 olayda 1.653'ten 197'ye düştü — bağımsız iki %10'luk örneklem için
beklenen değer 200'dür.

---

## Sonuçlar

- 300K olaylık koşuda `estimates` satır sayısı 1,5 milyondan ~325 bine iner
  (~0,7 GB/senaryo).
- D senaryosunun analizi ~2,4 saatten ~30 dakikaya iner.
- Kalibrasyon ve doğrulama kümeleri kod düzeyinde ayrıktır; PBT bunu her
  rastgele yapılandırmada doğrular (K4'ün temeli).
- `run_config.analyzed_events` sayacı bütünlük denetimini besler (ADR-23).

## Reddedilen alternatifler

**Yazma anında filtreleme.** Diski kurtarır, CPU'yu kurtarmaz.

**Örneklemeyi tamamen kapatmak.** ADR-14'ün gerekçesi (kalibrasyonun 12
iterasyonu 300K olayda koşamaz) hâlâ geçerli.

**`ground_truth.partition_key` sütununu okumak.** Kör testi delerdi; ayrım
zaten olay kimliğinden türetilebilir.

**Rastgele (tohumsuz) örnekleme.** K10 tekrarlanabilirliğini imkânsız kılardı.
