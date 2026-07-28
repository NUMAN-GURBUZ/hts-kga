# ADR-21 — Poligonlaştırma, Alan Hesabı ve Merkez Tanımı

**Durum:** Kabul edildi
**Tarih:** 2026-07-28
**Sprint:** 4 (T-E03-09..13)
**İlgili bulgular:** Plan BÖLÜM E.3 (kontur çıkarımı), ADR-07, ADR-10, K8, K10

---

## Bağlam

Plan BÖLÜM E.3 kontur çıkarımını dokuz adımda tarif eder. Üç adımı
veritabanına havale eder:

```
5. ST_Union → MULTIPOLYGON
6. ST_IsValid ? değilse ST_MakeValid + repaired=true
8. area_km2 = ST_Area(geometry)/1e6
```

Plan yazıldığında bu adımların maliyeti ölçülmemişti. Sprint 3'ün gün-1
benchmark'ı (ADR-18/6) ölçtü: kırsalda olay başına bölge **8.872 hücredir**
(250 m çözünürlük), kentselde 2.457. Bir konturun binlerce hex poligonunu
veritabanına gönderip birleştirtmek, kalibrasyonun 12 iterasyonunda (ADR-02)
PostGIS'i darboğaz yapar.

Maliyetten daha ağır bir sorun var: `ST_Union` sonucu GEOS sürümüne bağlıdır.
K10 "aynı seed, iki `run_id` → estimates bit-identical" der. Geometri üretimi
kütüphane sürümüne bağlı kalırsa bu ölçüt, kodun kontrolü dışındaki bir
bileşene emanet edilmiş olur.

Ayrıca ADR-10 merkezi `Σ(mass·p)/Σmass` olarak verir ama toplamın hangi küme
üzerinde olduğunu söylemez — üç M satırı için üç farklı okuma mümkündür.

---

## Karar

### 1. Birleştirme Go tarafında kenar izlemeyle yapılır

Hücre kümesinin sınırı `internal/analysis/geometry/polygonize.go` içinde
izlenir; veritabanına hazır MULTIPOLYGON gider.

Hex kafeste birleştirme bir kesişim problemi **değildir**. Komşusu kümede
olmayan her kenar sınırdadır; sınır kenarlarını uç uca eklemek yeterlidir.
Sonuç yaklaşık değil **tamdır**, O(n)'dir ve tamamen deterministiktir.

Kilit özellik şudur: hex kafeste her köşede tam üç hücre buluşur ve bu üçü
birbirine ikişer ikişer komşudur. Dolayısıyla bir köşeye değen **sınır kenarı
sayısı daima 0 veya 2'dir** — biri giren, biri çıkan. İzleme tek yönlüdür,
seçim kuralı gerekmez ve üretilen halkalar kendine değmez. Kare ızgarada
kaçınılmaz olan "dama tahtası" köşe ikilemi burada yapısal olarak imkânsızdır.

Köşeler kayan noktada değil **tam sayı kafes kimliğiyle** eşlenir:

```
V(a, k) = ( 2·q + r + dx[k] , 3·r + dy[k] )
```

Komşu iki hücrenin paylaştığı köşe böylece bit düzeyinde tek bir sayıdır;
iki farklı merkezden hesaplansaydı son bitlerde ayrışır ve halkalar
birleşmezdi.

`ST_IsValid` / `ST_MakeValid` denetimi **kaldırılmaz**: yazım anında her
satırda koşar ve `repaired` sütununa yazılır. Geçerlilik yapı gereği
beklenir, ama K8'in `repaired_ratio < %1` ölçütü ancak ölçülerek
raporlanabilir — varsayılarak değil.

### 2. Alan, saklanan WGS84 köşelerinden küresel formülle hesaplanır

```
A = R²/2 · Σ (λ_{i+1} − λ_i)·(sin φ_i + sin φ_{i+1})     R = 6.371.007,2 m (authalic)
```

ENU düzleminde shoelace **kullanılmaz**: ADR-07 izdüşümü sabit ölçek
katsayıları taşır (Sprint 1'de ölçülen sapma kuzeyde %0,59, doğuda %0,11) ve
düzlemsel alan bu sapmayı iki eksenin çarpımı olarak `area_km2` sütununa
sistematik yanlılık diye yazardı. Ne saklanıyorsa onun alanı ölçülür.

Yarıçap olarak ortalama değil **authalic** (eşit alanlı) yarıçap kullanılır:
küresel formülün elipsoit alanına en yakın sonucu vermesi için doğru sabit
budur.

### 3. Merkez, M için konturun **kendi** kütlesi üzerinden hesaplanır

```
centroid(M@c) = Σ_{i ∈ C(c)} mass_i · p_i / Σ_{i ∈ C(c)} mass_i
```

ADR-10'un formülü korunur; toplamın kümesi konturun kendi hücreleridir, tüm
dağılım değil.

**Gerekçe:** merkez satır başına saklanır ve satır bir **bölgeyi** tanımlar;
o bölgenin merkezi kendi kütlesinden gelmelidir. Tüm dağılım kullanılsaydı üç
M satırı aynı merkezi taşırdı ve %50 konturunun merkezi, kendi geometrisinin
dışına düşebilirdi (çok tepeli kütlede gerçekten olur). Pratikte fark
küçüktür: %95 konturu kütlenin neredeyse tamamını içerdiği için merkezi tam
dağılımın merkezine yakınsar.

Taban çizgilerinde olasılık dağılımı yoktur; B0 ve B1'in merkezi **alan
ağırlıklı geometrik merkezdir** (ADR-10'un "B0 için hücre merkezi, B1 için
dilimin ağırlık merkezi" ifadesinin uygulaması). B0'ın merkezi simetri gereği
direğin kendisidir, B1'inki kapalı formdaki `d = 2R·sinα/(3α)` noktasına
yakınsar — test iki yolu karşılaştırır.

### 4. Geometri WKB olarak taşınır, WKT olarak değil

`estimates.geometry` sütununa OGC WKB gider ve SRID sunucuda
(`ST_GeomFromWKB(…, 4326)`) verilir. WKT, float64'ü ondalığa çevirir; yaygın
`%.7f` kullanımı ~1 cm kayma bırakır ve K10'un bit düzeyi ölçütü veritabanına
gidip dönen geometride kırılırdı. Ayrıca WKB köşe başına 16 bayttır, tam
duyarlıklı WKT ~45.

### 5. Yazma `unnest` ile toplu INSERT'tir, COPY değil

COPY sunucu tarafında fonksiyon çağıramaz; `ST_IsValid`/`ST_MakeValid` ve
SRID ataması ancak ara tablo üzerinden yapılabilirdi (iki kat yazma, ek DDL,
eşzamanlı koşularda ad çakışması). `cells` yazımı (T-E02-06) da aynı deseni
kullanır.

### 6. Sadeleştirme S4'te uygulanmaz

Geometri hacmi **ölçüldü** (aşağıda); tahmin edilenden bir mertebe küçük
çıktı. Sadeleştirme (`ST_SimplifyPreserveTopology`) alan ölçümüne bilinmeyen
bir yanlılık sokar ve K2/K3 iddiası alan oranlarına dayanır. Gerek olmadığı
ölçüldüğü için uygulanmaz; gerekirse ayrı bir ADR ile ve ölçümle gelir.

---

## Ölçümler (tahmin değil)

Dört senaryonun tamamında, senaryo başına 120 olay, gerçek altyapıda
(`tests/integration/analysis_pipeline_test.go`):

### Alan çapraz kontrolü — Go küresel formülü vs PostGIS `ST_Area(geography)`

| Senaryo | Satır | Ortalama fark | En büyük fark |
|---|---|---|---|
| A kentsel TA'lı | 600 | %0,0733 | %0,0771 |
| B kentsel TA'sız | 600 | %0,0734 | %0,0773 |
| C kırsal TA'lı | 600 | %0,0726 | %0,0826 |
| D kırsal TA'sız | 600 | %0,0730 | %0,0833 |

Fark küre–elipsoit farkından ibarettir ve %1 toleransının on katı altındadır.

### Geometrik geçerlilik

`repaired_ratio = %0,0000` (0/2400 satır). Kenar izlemesinin ürettiği hiçbir
geometri `ST_MakeValid` gerektirmedi — bölüm 1'deki yapısal iddia ölçümle
doğrulandı.

### Hacim

| Yöntem | Ortalama köşe | Ortalama bayt |
|---|---|---|
| B0 | 361 | 5.798 |
| B1 | 68 | 1.110 |
| M@50 (kentsel TA'lı) | 60 | 1.021 |
| M@90 (kırsal TA'sız) | 795 | 12.748 |
| M@95 (kırsal TA'sız) | 937 | 15.032 |

Olay başına toplam ~11 KB (kentsel) – ~38 KB (kırsal). ADR-14 örneklemesiyle
('V' kümesi ~60.000 olay) senaryo başına **0,7–2,3 GB**. Sprint 4 planlamasında
korkulan 10 GB'lık mertebe gerçekleşmedi; sadeleştirme bu yüzden gereksizdir.

> B0'ın 361 köşesi 1°'lik yay ayrıklaştırmasından gelir ve tüm yöntemler
> içinde en büyüğüdür. Taban çizgisinin kendisi ucuz olduğu için bu kabul
> edilmiştir; gerekirse adım büyütülebilir.

### Altın senaryo — bağımsız hesapla karşılaştırma

Tek site, σ→0, komşusuz, TA'sız, açısal çarpan kapalı: kütle kapsanan bölgede
düzgün dağılır ve `alan(M@c) ≈ c · alan(S)` kapalı formu geçerlidir. Kapsanan
alan, ızgara/kontur/poligon kodunun hiçbirine dokunmadan, 0,05°'lik yönlerde
link bütçesinden ikiye bölmeyle çözülüp `∫½r²dφ` ile integre edildi:

| | üretilen | bağımsız hesap | fark |
|---|---|---|---|
| M@50% | 10,3490 km² | 10,3386 km² | **+0,10%** |
| M@90% | 18,6260 km² | 18,6095 km² | **+0,09%** |
| M@95% | 19,6609 km² | 19,6434 km² | **+0,09%** |

Plan BÖLÜM I'nın altın senaryo toleransı ±%5'tir.

---

## Sonuçlar

- Plan BÖLÜM E.3'ün 3–5. adımları (poligonlaştırma, birleştirme) Go tarafına
  taşınmıştır; 6. adım (geçerlilik denetimi) yerinde kalmıştır.
- 8. adım (`ST_Area`) yazımdan çıkarılmış, **çapraz kontrole** dönüşmüştür:
  saklanan değer Go'da hesaplanır, PostGIS bağımsız doğrular.
- Geometri üretimi GEOS sürümünden bağımsızdır → K10 kod içinde kalır.
- `estimates` satırları PostGIS olmadan üretilebildiği için kalibrasyon
  döngüsü (S5) ve PBT'ler altyapısız koşar.

## Açık kalan

K8'in ikinci yarısı (`p95_part_count ≤ 3`) **bu ADR'de kapatılmamıştır.**
Ölçüm, TA'lı senaryolarda eşiğin aşıldığını gösterdi (kentsel M@90 p95=4,
kırsal M@90 p95=7). Tanı ölçümü (`internal/analysis/core/partcount_test.go`)
nedenin alt-çözünürlük halkası olduğunu gösteriyor: LTE'de TA halkası
78,12 m'dir, ızgara adımı kentselde 100 m / kırsalda 250 m — halka hücreden
incedir ve yüksek kütleli bant kesik bir zincire dönüşür. Çözünürlük
düşürüldüğünde parçalanma da düşüyor (kentsel M@50: 100 m'de p95=6 → 39 m'de
p95=2).

Çözünürlük kararı ayrı bir ADR'ye bırakılmıştır; komşu kısıtı (ADR-03) bu
karar kapsamında **zayıflatılmayacaktır**.

---

## Reddedilen alternatifler

**PostGIS `ST_Union` (plan metni).** Olay başına binlerce hex poligonunun
gidiş-dönüşü; kalibrasyonun 12 iterasyonunda darboğaz; sonucun GEOS sürümüne
bağlanması. Ölçülen bölge büyüklüğü (8.872 hücre/olay) bu maliyeti kabul
edilemez kılıyor.

**ENU'da düzlemsel alan (shoelace).** Ucuz ve basit, ama ADR-07'nin ölçek
sapmasını `area_km2` sütununa sistematik yanlılık olarak taşır. K2/K3 alan
oranlarına dayandığı için yanlılık pay ve paydada tam sadeleşmez.

**Merkezin tüm dağılımdan hesaplanması.** Üç M satırı aynı merkezi taşırdı;
%50 konturunun merkezi kendi geometrisinin dışına düşebilirdi.

**WKT ile taşıma.** Okunabilir ve hata ayıklaması kolay, ama tam duyarlık
için `%.17g` gerekir ve o hâlde WKB'den ~3 kat büyüktür; kısaltılmış duyarlık
K10'u kırar.

**Baştan sadeleştirme.** Disk endişesiyle önerilmişti; ölçüm hacmin bir
mertebe küçük olduğunu gösterdi. Ölçülmeden uygulansaydı K2/K3'e bilinmeyen
bir yanlılık girecekti.
