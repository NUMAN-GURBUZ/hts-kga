# ADR-25 — Kalibrasyonun Bilgi Kanalı ve λ'nın Kaldıraç Sınırı

**Durum:** Kabul edildi
**Tarih:** 2026-07-29
**Sprint:** 5 (T-E04-06, G0 pilotu)
**İlgili bulgular:** ADR-02, ADR-18, K1, K4, K6

---

## Bağlam

Kalibrasyon, ground truth'a bakan **tek** meşru bileşendir: λ'yı bulmak için
gerçek konumları görmek zorundadır (ADR-02). Bu, kör testle (K6) çelişir gibi
görünür ve sınırın nerede olduğu açıkça yazılmalıdır.

İkinci mesele λ'nın gerçekten ne kadar iş yapabildiğidir. ADR-02 `coverage(λ)`
fonksiyonunun monoton artan olduğunu varsayar ve hedefin (0,90) aralık içinde
bulunduğunu ima eder. İkisi de **ölçülmemişti**.

---

## Karar

### 1. Kalibrasyon tek yönlü ve bir skaler genişliğinde bir kanaldır

Ground truth → analiz motoru yönünde akan bilgi **yalnızca λ**'dır: tek bir
kayan nokta sayısı. Motor ne gerçek konumu, ne partition anahtarını, ne de
enjeksiyon etiketini görür.

Kanalın dar olması tesadüf değil, tasarımdır: λ'nın taşıyabileceği bilgi
miktarı bir sayıyla sınırlıdır ve bu sayı **tüm koşu için ortaktır** — tek bir
olayın gerçek konumu hakkında hiçbir şey söyleyemez.

Kör testin koruduğu şey analiz motorudur, doğrulama servisi değil.
`svc_validation` rolünün `ground_truth`'a SELECT yetkisi vardır (004_roles);
`svc_analysis` ve `svc_integrity` rollerinin yoktur.

### 2. λ yalnızca σ_eff'i ölçekler; TA penceresi ondan bağımsızdır

```
w_rad(p) = Φ((P_s(p) − RxSens) / σ_eff) · w_TA(p)      σ_eff = λ · σ_nominal
```

`w_TA` geometrik örtüşme oranıdır (ADR-18/3) ve λ'yı görmez. TA'lı
senaryolarda kütlenin ezici kısmı halkanın içinde kaldığından λ'nın kapsama
üzerindeki etkisi küçüktür.

### 3. Yakınsamama bir bulgudur, gizlenmez

Döngü hedefi tutturamazsa:

- λ* olarak son aralık ortası yazılır (`run_config.lambda` boş kalmaz),
- `Converged=false` işaretlenir,
- tüm (λ, kapsama) izlemesi raporda kalır.

"Kalibre edildi" denmez. Eğrinin düz olduğu izlemeden okunur.

---

## Ölçüm (Sprint 5 pilotu, G0)

Kentsel senaryolar, ~480 olaylık kalibrasyon örneklemi:

| Senaryo | λ=0,5 | λ=1,0 | λ=1,5 | λ=2,0 | λ=3,0 | açıklık |
|---|---|---|---|---|---|---|
| A kentsel TA'lı | 0,5096 | 0,5203 | 0,5203 | 0,5203 | 0,5225 | **0,0128** |
| B kentsel TA'sız | 0,5114 | 0,5197 | 0,5197 | 0,5280 | 0,5383 | **0,0269** |

İki gözlem:

1. **Eğri monotondur** — ADR-02'nin varsayımı tutuyor (ihlal ölçülmedi).
2. **Eğri neredeyse düzdür ve hedefin çok altındadır.** λ'yı altı kat
   büyütmek kapsamayı %1–3 artırıyor; hedef 0,90'a hiçbir λ ile ulaşılmıyor.

### Nedeni: arama bölgesi, λ değil

Tanı ölçümü (`Runner.Diagnose`) kapsamanın **üst sınırını** verdi:

| Senaryo | Gerçek konum dilim içinde | Ölçülen kapsama (λ=3) | Tavana oran |
|---|---|---|---|
| A kentsel TA'lı | %58,9 | 0,5225 | **%89** |
| B kentsel TA'sız | %55,9 | 0,5383 | **%96** |

Yani model, arama bölgesinin izin verdiği kapsamanın %89–96'sına zaten
ulaşıyor. Kalan boşluk λ ile kapatılamaz: gerçek konumun **%41–44'ü serving
hücrenin diliminin dışındadır** ve orada kütle tanımlı bile değildir.

Sebep yapısaldır: simülatörün best-server seçimi tüm yönlerde çalışır (anten
deseninin yan lobu ve gölgeleme sayesinde 60° sapmadaki bir ajan da o hücreye
bağlanabilir), analiz ise arama bölgesini azimut ± hüzme/2 = ±32,5° ile
sınırlar (T-E03-03). Gerçek konumun eksenden medyan sapması **24,8°**'dir ve
dağılımın kuyruğu 32,5°'yi aşar.

---

## Sonuçlar

- K1 (kapsama %85–95) mevcut model tanımıyla **ulaşılamazdır** ve bunun nedeni
  kalibrasyonun başarısızlığı değildir.
- Kalibrasyon kodu doğru çalışmaktadır: monoton eğride kökü bulur (birim
  testler), düz eğride yakınsamadığını bildirir.
- Düzeltme yolu arama bölgesinin tanımındadır (T-E03-03), λ'da değil. Öneri
  ayrı bir ADR'ye bırakılmıştır: bölge, sert dilim yerine "bu hücrenin
  best-server olabileceği bölge" olarak tanımlanmalı ve ağırlığı anten
  deseni vermelidir — Sprint 3'ün kendi belgelenmiş tasarım niyeti de budur
  ("dilim kütlenin taşıyıcısı değil, arama bölgesidir").

## Reddedilen alternatifler

**`w_TA`'ya λ bağımlılığı eklemek.** TA penceresi ölçüm belirsizliği değil,
protokol nicemlemesidir (78,12 m'lik adım). Onu kalibrasyon parametresine
bağlamak, fiziksel olarak tanımlı bir büyüklüğü serbest parametreye çevirirdi.

**λ aralığını genişletmek (örn. [0,5, 20]).** Eğri düz olduğu için hedefe yine
ulaşılmazdı; yalnızca alanları şişirir ve K2/K3'ü yapay olarak kötüleştirirdi.

**Ölçümü kalibrasyon kümesinde yapmak.** Kapsama 'C' üzerinde ölçülüp 'V'
üzerinde raporlansaydı sayı yükselmezdi — küme değişikliği yapısal sorunu
çözmez, yalnızca K4'ü ihlal ederdi.
