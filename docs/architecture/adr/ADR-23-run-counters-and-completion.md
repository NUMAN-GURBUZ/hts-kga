# ADR-23 — Koşu Sayaçları ve Tamamlanma Ölçütü

**Durum:** Kabul edildi
**Tarih:** 2026-07-29
**Sprint:** 5 (G1, T-E04-02)
**İlgili bulgular:** ADR-04 ↔ ADR-14 çelişkisi, ADR-01, K4

---

## Bağlam

ADR-04, doğrulamanın toplu iş olarak çalışmasını ve önce koşunun bittiğinden
emin olunmasını şart koşuyor. Tamamlanma ölçütünü şöyle veriyor:

> Koşu tamamlanma koşulu: `hts_records` sayısı = `estimates` sayısı /
> beklenen_yöntem_sayısı, ve Kafka consumer lag = 0.

ADR-14 ise her olayın analiz edilmeyeceğini söylüyor: kalibrasyon 5.000
olaylık örnekle, final ölçüm `partition_key='V'` kümesiyle (~%20) çalışır.

Bu ikisi aynı anda doğru olamaz. Örneklemeli bir koşuda `estimates` sayısı
`hts_records`'ın beş katı değil, **beşte biri mertebesindedir**. Çelişki
yalnızca metinde de kalmadı: `003_integrity_fn.sql`'deki üçüncü denetim oranı
4,5–5,5 arasında bekliyor ve örnekleme devreye girdiğinde sistem doğru
çalışırken WARN üretiyor — yani denetim, işe yaramaz hâle geliyor.

Ayrıca "Kafka consumer lag = 0" ölçütü `franz-go/pkg/kadm` bağımlılığı
gerektiriyor ve lag'in sıfır olması mesajın **işlendiğini** söyler, veritabanına
**yazıldığını** söylemez.

---

## Karar

### 1. Koşu kendi sayaçlarını beyan eder

`run_config` tablosuna iki sütun eklenir (migration 005):

| Sütun | Yazan | Anlamı |
|---|---|---|
| `published_events` | Simülatör (koşu bitiminde) | `hts.records` topic'ine yayınlanan kayıt sayısı |
| `analyzed_events` | Analiz motoru (kapanışta) | Örnekleme sonrası işlenen olay sayısı |

Sayaçlar NULL ise denetim `SKIP` döner: henüz bitmemiş bir koşu için yanlış
alarm üretilmez.

### 2. Tamamlanma ölçütü sayaçlara bağlanır

```
finished_at IS NOT NULL                       — simülasyon bitti
hts_records  sayısı = published_events        — kayıtlar yazıldı
ground_truth sayısı = published GT sayısı     — ground truth yazıldı
estimates    sayısı = 5 × analyzed_events     — analiz bitti
```

Örnekleme oranı ne olursa olsun ölçüt anlamını korur.

### 3. Kafka lag denetimi yerine sayım denkliği

`kadm` bağımlılığı eklenmez. "Yazıldı mı" sorusunun doğrudan cevabı
`hts_records` sayısının yayınlanan sayıya eşit olmasıdır; lag=0 bunun yalnızca
gerekli koşuludur, yeterli koşulu değil. Daha az bağımlılık, daha güçlü
garanti.

### 4. `verify_integrity` dört denetime çıkar

```
1. hts_records_without_ground_truth    FAIL  ise gerçek hata (ADR-01)
2. ground_truth_without_hts_records    FAIL  ise gerçek hata (kural 4 hariç)
3. published_vs_stored_records         SKIP  sayaç yoksa
4. estimates_per_analyzed_event        SKIP  sayaç yoksa
```

Denetim 2'nin durumu artık sabit `INFO` değil: kural 4 zaten sorgudan
dışlandığı için geriye kalan her satır gerçek bir eşleşme hatasıdır ve `FAIL`
olmalıdır. Sprint 4'te bu denetim her koşuda `INFO` dönüyordu; sayısı sıfır
olmadığında bile dikkat çekmezdi.

---

## Sonuçlar

- Bütünlük denetimi örneklemeyle uyumlu hâle geldi; ADR-04 ve ADR-14 artık
  çelişmiyor.
- Yarım kalan koşu `finished_at` NULL kaldığı için doğrulamayı bloke ediyor.
- `run_config` bir koşunun tüm yaşam döngüsünü taşıyor: başlangıç, bitiş,
  hacim, örneklem ve λ*.

## Reddedilen alternatifler

**`estimates` oranını olduğu gibi bırakıp örneklemeyi kapatmak.** Tüm olayları
analiz etmek D senaryosunda ~2,4 saat ve senaryo başına ~3,5 GB demek; ADR-14
bunu zaten reddetmişti.

**Denetimi tamamen kaldırmak.** Referansiyel bütünlük bu projenin K6/ADR-01
iddiasının bir parçası; ölçmemek, ölçüp raporlamaktan kötüdür.

**Kafka lag'i `kadm` ile ölçmek.** Ek bağımlılık, daha zayıf garanti; ayrıca
lag=0 tüketici çöktüğünde de doğru olabilir.
