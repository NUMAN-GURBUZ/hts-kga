# ADR-20 — Deterministik RF Paketinin Ayrılması ve Import Kısıtı

**Durum:** Kabul edildi
**Tarih:** 2026-07-27
**Sprint:** 3 (T-E03-03, T-E03-06, T-E03-07)
**İlgili bulgular:** G3 (plan boşluğu), K6 (kör test)

---

## Bağlam

Analiz motoru, kütle üretimi için yol kaybı ve anten deseni hesaplamak
zorundadır (ADR-18, ADR-19). Bu hesaplar bugün `internal/simulator/radio`
paketindedir — gölgeleme alanıyla (`ShadowingField`) aynı pakette.

Sorun mimari değil, **bilimsel**tir. Kör testin üç katmanı (Kafka ACL,
PostgreSQL rolü, enjeksiyon etiketi) analizin *veriye* erişmesini engeller.
Ama analiz, simülatörün gölgeleme gerçekleşmesini **kod yoluyla** okuyabilseydi
— aynı tohumdan aynı alanı yeniden üreterek — bilgi asimetrisi kod düzeyinden
delinirdi. Bu, çalışmanın en değerli iddiasını (ADR-03: analiz gerçekleşmiş
gölgelemeyi bilmez) sessizce geçersiz kılardı.

---

## Karar

### 1. Deterministik kısım ayrı pakete taşınır

**`internal/rf`** paketi kurulur ve şunları içerir:

| Taşınan | İçerik |
|---|---|
| `propagation.go` | `Link`, `PathLossModel`, kayıt defteri, `ValidityRange`, `UTHeightM` |
| `uma.go` · `umi.go` · `rma.go` | 3GPP TR 38.901 yol kaybı modelleri |
| `antenna.go` | `AntennaAttenuationDB`, `BearingDeg`, `ElevationDeg`, `WrapAngleDeg` |

**`internal/simulator/radio`** paketinde kalanlar:

| Kalan | Neden |
|---|---|
| `shadowing.go` | `ShadowingField`, `Source` — rastgele gerçekleşme |
| `best_server.go` | `Selector` gölgeleme alanı taşır; simülatöre özgüdür |

Bağımlılık yönü tek yönlüdür: `internal/simulator/radio → internal/rf`.

### 2. `pkg/` değil `internal/rf`

`pkg/rf` seçilseydi, paket `internal/config`'e (yayılım modeli enum'u) bağımlı
kalacak ve modül dışından import edilemeyen bir `pkg/` paketi doğacaktı — yani
`pkg/` semantiğinin ihlali. Enum'u taşıyıp `internal/config`'de tip alias
bırakmak da mümkündü, ama `internal/rf` bu tartışmayı tamamen kapatır ve
ADR'nin asıl şartını (analiz `ShadowingField`'e ulaşamaz) aynen sağlar.

### 3. Import kısıtı testle zorlanır — kör testin 4. katmanı

`internal/analysis/...` altındaki hiçbir paket, doğrudan veya dolaylı olarak
`internal/simulator/...` altındaki hiçbir pakete bağımlı olamaz. Kural bir
testle zorlanır (`go list` bağımlılık kapanışı üzerinden):

```
K6 kör testi — dört katman:
  1. Kafka ACL      : analysis principal → hts.groundtruth → DENY
  2. PostgreSQL rol : analysis rolü      → ground_truth    → SELECT yok
  3. Enjeksiyon etiketi: injected_rule ground_truth'ta, S4 göremez
  4. Import grafiği : analysis → simulator bağımlılığı yok  ← YENİ
```

Testin düşmesi, birinin analiz katmanından simülatörün rastgeleliğine erişim
açtığı anlamına gelir; bu bir derleme hatası kadar bağlayıcıdır.

### 4. Plan BÖLÜM C.3 tadil edilmiştir

Plan v3.0'ın klasör şeması `radio/` altında altı dosyayı birlikte listeler ve
`pkg/` içeriğini `geo/ · kafka/ · privacy/` ile sınırlar. Aşağıdaki üç ekleme
bilinçli tadildir:

| Ekleme | Gerekçe |
|---|---|
| `internal/rf/` | Bu ADR — kör testin kod katmanı |
| `pkg/ta/` | G4 — TA türetme ile TA halkası ikizdir, tek tanım gerekir |
| `pkg/split/` | İ-5 — 80/20 ayrımı simülatör ve S5 replay sürücüsünde ikizdir |

`pkg/ta` ve `pkg/split` `pkg/` altındadır çünkü iç bağımlılıkları yoktur
(stdlib + `github.com/google/uuid`); `internal/rf` `internal/` altındadır
çünkü `internal/config`'e bağımlıdır.

---

## Etki analizi (ölçülmüş, tahmin değil)

Taşımanın kırılma yüzeyi:

- `best_server.go`: 5 sembol `rf.` önekine geçer (`BearingDeg`,
  `ElevationDeg`, `Link`, `PathLossDB`, `AntennaAttenuationDB`).
  `isPositiveFinite` yardımcısı `internal/rf`'e gittiği için `radio`'da yerel
  bir kopya gerekir.
- `shadowing.go`: yalnızca tip referansı değişir (`rf.PathLossModel`, 2 yer).
  Arayüz metodu çağrıları (`DecorrelationM`, `LOSProbability`,
  `ShadowingSigmaDB`) dokunulmadan kalır.
- `internal/simulator/agent`: **üretim kodu etkilenmez**. `placement.go` ve
  `world.go` yalnızca `radio.Selector/Site/Source/NewNetwork/NewShadowingField`
  kullanır — hepsi yerinde kalır. Yalnızca `agent_test.go`'daki 2 adet
  `radio.ModelFor` çağrısı `rf.ModelFor` olur.
- Test dosyaları: `models_test.go`, `propagation_test.go`,
  `crossvalidate_test.go` birlikte taşınır. `radio_pbt_test.go` **bölünür**:
  yol kaybı/LOS değişmezleri `internal/rf`'e, gölgeleme değişmezleri yerinde.

---

## Reddedilen alternatifler

| Alternatif | Ret gerekçesi |
|---|---|
| Analizin `internal/simulator/radio`'yu import etmesi | `ShadowingField` erişilebilir kalır; kör test kod düzeyinde delinir |
| `pkg/rf` | `internal/config` bağımlılığı `pkg/` semantiğini ihlal eder |
| Enum'u taşıyıp `config`'de tip alias | Çalışır ama gereksiz; `internal/rf` tartışmayı kapatır |
| Kodu kopyalamak (analiz kendi yol kaybını yazsın) | İkiz sorunu: iki uygulama sessizce ayrışır (bkz. `pkg/ta` gerekçesi) |
| Kısıtı yalnızca kod incelemesiyle korumak | İnsan denetimi regresyona karşı garanti vermez; test bağlayıcıdır |
