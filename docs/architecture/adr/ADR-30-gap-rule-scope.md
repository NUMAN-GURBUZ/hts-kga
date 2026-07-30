# ADR-30 — Kural 4 (Kayıt Boşluğu): Olay Çıpasının İmkânsızlığı ve K7 Kapsamı

**Durum:** Kabul edildi
**Tarih:** 2026-07-30
**Sprint:** 6 (kapsam kararı) — uygulama Sprint 7
**İlgili bulgular:** ADR-09, E.6, BÖLÜM F (F.5), K7

---

## Bağlam

Plan iki şeyi birlikte söylüyor ve ikisi bir arada doğru olamaz.

**ADR-09 / E.6 — kural 4 olayı siler:**

> Kural 4 (kayıt boşluğu) olay **silme** olduğundan, silinen olay için yalnızca
> ground_truth yazılır (`injected_rule=4`), `hts.records`'a hiçbir şey gitmez.

**BÖLÜM F, F.5 — recall olay kimliğiyle eşleşir:**

```sql
LEFT JOIN integrity_findings f
       ON f.run_id=g.run_id AND f.event_id=g.event_id AND f.rule_id=g.injected_rule
```

S4'ün, var olmayan bir kaydın `event_id`'siyle bulgu yazması gerekiyor.

---

## İki bağımsız imkânsızlık

### 1. Çıpa üretilemez

`event_id = UUIDv5(namespace, run_id ‖ agent_id ‖ tick_index ‖ event_seq)`
(ADR-01).

S4'ün elindeki girdiler: `run_id` (biliyor), `agent_id` (**bilmiyor** — elinde
HMAC'li `pseudo_msisdn` var, ters çevrilemez), `tick_index` (**bilmiyor** —
kayıt yok, damga da yok), `event_seq` (**bilmiyor**).

Üç girdiden ikisi yok. S4 silinen olayın kimliğini **üretemez**. Sonuç:
`recall(4) ≡ 0` ve komşu bir olaya çıpalanan her kural-4 bulgusu F.5'e göre
yanlış pozitiftir.

Bu bir mühendislik eksiği değildir; kör testin (ADR-09, K6) doğrudan sonucudur.
S4'ün `agent_id`'yi bilmesi, kör testin çökmesi anlamına gelirdi.

### 2. Sinyal ayırt edici değil

Ölçüm (senaryo A, 1000 ajan × 30 gün):

| Boşluk tipi | n | medyan | p95 | > 12 saat |
|---|---|---|---|---|
| Silme içeren | 1.171 | 3,33 sa | 12,67 sa | 73 |
| Temiz | 295.952 | 1,33 sa | 9,25 sa | 5.114 |

12 saatlik eşikte: recall %6,2, **precision %1,4** (73 doğru / 5.187 bulgu).

Nedeni fizikseldir: olaylar Poisson akışıdır (T-E02-13), aralıklar üsteldir.
Bir olayın silinmesi iki üstel aralığı toplar → Gamma(2). Gamma(2) ile Exp(1)
kuyruklarında örtüşür; **tek bir boşluğa bakarak** hangi dağılımdan geldiği
ayırt edilemez. Ayrım ancak **popülasyon düzeyinde** yapılabilir.

---

## Karar

### 1. Kural 4 `ClassAggregate`'tir

`integrity_findings` tablosuna **olay bazlı satır yazmaz** (ADR-27 sınıf
tanımı). Yerine koşu düzeyinde bir gösterge üretir:

```
Girdi : abone başına gözlenen kayıt aralıkları (hts_records)
Çıktı : koşunun "kayıp kayıt kütlesi" tahmini + güven aralığı
Ölçüt : tahmin ↔ gerçek kural-4 sayısı (1.171) karşılaştırması,
        TOPLAM düzeyinde — olay düzeyinde değil
```

Gösterge, bir bütünlük denetim sisteminin verebileceği dürüst cevaptır:
*"bu koşuda yaklaşık N kayıt eksik görünüyor"* — hangi kayıtlar olduğunu
söyleyemeyiz, çünkü söylenemez.

### 2. K7'nin precision eşiği kural 4'e uygulanmaz

K7 "kural bazında precision ≥ %90" der. Kural 4 olay-çıpalı bulgu üretmediği
için precision tanımsızdır (bölen sıfır). `integrity_metrics`'te kural 4 satırı
`precision = NULL`, `sufficient = false` olarak durur ve K7 tablosunda
"kapsam dışı — ADR-30" olarak raporlanır.

Bu bir eşik gevşetmesi **değildir**: eşik değişmedi, kuralın ölçüm sınıfı
değişti ve gerekçe ölçümle belgelendi.

### 3. Uygulama Sprint 7'ye alınır

Gösterge K7'ye katkı yapmaz ve ölçüm hattı kurulduktan sonra ucuzdur (2 SP).
Sprint 6'nın 5 günü içinde kural 4'e harcanan her saat, K7'ye katkı yapan
kurallardan alınmış olurdu.

### 4. Enjeksiyon tarafı değişmez

`injector.RuleGap` olduğu gibi kalır: olay silinir, `ground_truth`'a
`injected_rule = 4` yazılır. Enjeksiyon oranı ve ağırlıklar değişmez.

Gerekçe: Sprint 5 raporunun doğruladığı `verify_integrity` denetim 2
(`ground_truth_without_hts_records`) kural 4'ü beklenen durum olarak
tanıyor ve `hts_records` ↔ `ground_truth` fark hesabı (1.179 satır) buna
dayanıyor. Enjektöre dokunmak Sprint 5'in ölçümlerini geçersiz kılardı.

---

## Reddedilen alternatifler

**(A) Bulguyu komşu olaya çıpalamak** (boşluğu kapatan sonraki kaydın
`event_id`'si). Reddedildi: F.5'e göre o olay temizdir → her kural-4 bulgusu
yanlış pozitif olur. Ayrıca F.5'i "aralık birleştirmesi" yapacak şekilde
değiştirmek gerekirdi; ölçümden sonra ölçüm tanımı değiştirmek BÖLÜM J
disiplinini ihlal eder.

**(B) Enjektörü tespit edilebilir hâle getirmek** — blok silme (ardışık 5–10
olay) veya daha yüksek oran. Tespit edilebilirliği gerçekten artırırdı: 10
olaylık bir boşluk Gamma(10) üretir ve Exp(1)'den kolayca ayrışır.

**Reddedildi**, ve gerekçe bu ADR'nin en önemli kısmıdır: ölçüm sonucuna göre
veri üretecini değiştirmek, dedektörü veriye değil veriyi dedektöre uydurmaktır.
Sprint 5'te K1 tutmadığında model değiştirilmedi (`docs/results/sprint5-report.md`,
bölüm 8); aynı disiplin burada da geçerlidir. Blok silme akademik olarak
savunulabilir bir manipülasyon senaryosudur — ama **Sprint 2'de** seçilmeliydi,
ölçüm görüldükten sonra değil.

**(C) `hts_records`'a bir sıra numarası (`ingest_seq`) eklemek.** Eksik kaydı
numara boşluğundan bulmak. Reddedildi: bu, gerçek bir HTS kaydında bulunmayan
bir alanı veri modeline sokup manipülasyonu **tanım gereği** tespit edilebilir
kılmaktır. Silinen kayıt zaten numarasıyla birlikte silinirdi; numara
korunacaksa manipülasyon "silme" değil "işaretleme" olurdu.

**(D) Kural 4'ü tamamen kaldırmak.** Reddedildi: koşu düzeyi gösterge gerçek ve
raporlanabilir bir yetenektir; ayrıca "bu manipülasyon türü olay düzeyinde
tespit edilemez" sonucu, ölçülmüş ve gerekçelendirilmiş bir bilimsel bulgudur.
Kaldırmak o bulguyu da silerdi.

---

## Sonuç

Sprint 6, beş manipülasyon türünün üçünü yüksek kesinlikle (kural 1, 3, 5),
birini kısmi recall ile (kural 2), birini ise **"bu veride olay düzeyinde bilgi
yok"** sonucuyla (kural 4) ele alır. Son ifade bir başarısızlık değil, ölçülmüş
bir sınırdır ve beş kuralın hepsinin çalıştığını iddia etmekten daha
savunulabilirdir.

---

## İlgili

ADR-01 (deterministik `event_id`) · ADR-09 (enjeksiyon kural 4, kör test) ·
ADR-27 (kural sınıfları) · ADR-31 (`integrity_metrics`, ölçülebilirlik kuralı) ·
BÖLÜM J (eşiklerin ölçümden önce beyanı) · Sprint 5 raporu bölüm 8 (K1 emsali)
