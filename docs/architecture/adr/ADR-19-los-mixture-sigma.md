# ADR-19 — LOS Bilgi Asimetrisi ve σ_eff'in Kaynağı

**Durum:** Kabul edildi
**Tarih:** 2026-07-27
**Sprint:** 3 (T-E03-03, T-E03-06, T-E03-07)
**İlgili bulgular:** G2 (plan boşluğu), İ-4

---

## Bağlam

3GPP TR 38.901 yol kaybı modelleri (UMa/UMi/RMa) **LOS durumuna bağlıdır**:
`PathLossDB(link, los bool)`. Simülatörde LOS, site başına bir kez rastgele
çekilir (T-E02-11) ve gölgeleme ile birlikte gerçekleşir.

Analiz motoru bu çekilişi **bilemez**. Kayıt yalnızca hangi hücrenin serving
olduğunu söyler; ortamın hangi gerçekleşmesinde olduğunu söylemez. Bilgi
asimetrisi ADR-03'ün ve kör testin temelidir: analiz, envanterdeki
deterministik parametrelerden hesaplar, gerçekleşmiş rastgeleliği görmez.

Plan bu durumu tanımlamaz. Analiz `PathLossDB`'yi hangi LOS değeriyle
çağıracaktır?

---

## Karar

### 1. Log-alanı LOS/NLOS karışımı

```
PL_analiz(d) = p_LOS(d) · PL_LOS(d) + (1 − p_LOS(d)) · PL_NLOS(d)
```

`p_LOS` her modelin kendi LOS olasılığı fonksiyonudur (`LOSProbability`,
T-E02-10'da zaten uygulanmıştır).

**Adlandırma önemlidir:** bu bir "beklenen değer" **değildir**. Beklenen alınan
güç lineer güç alanında hesaplanırdı; yukarıdaki ifade dB alanında, yani
logaritmik alanda bir karışımdır. İki alan aynı sonucu vermez (Jensen). Terim
belgede ve kodda **"log-alanı LOS/NLOS karışımı"** olarak geçer.

Bunun tercih edilmesinin nedeni pratiktir: TR 38.901 modelleri zaten dB
cinsinden medyan yol kaybı olarak tanımlıdır; karışımı aynı alanda yapmak
modelin kendi tanım alanında kalmak demektir.

### 2. Muhafazakâr (NLOS-only) yaklaşım reddedilmiştir

Alternatif, analizin daima NLOS varsayması olurdu — "en kötü durum" anlamında
güvenli görünür. Reddedilmiştir çünkü:

- λ kalibrasyonu (ADR-02) kalan belirsizliği zaten emer; muhafazakârlık
  kalibrasyonun işini tekrarlar.
- Sistematik olarak fazla yol kaybı varsaymak kapsama olasılığını (ADR-18,
  birinci çarpan) düşürür ve kütleyi geniş bir alana yayar. Bu, **alanı
  gereksiz şişirir** ve K3'ün daralma iddiasını doğrudan zayıflatır.
- Yanlılık yönü tek taraflıdır: model yanlış tarafa kaymış olur, kalibrasyon
  bunu ancak λ'yı sınır değerine iterek telafi edebilir.

### 3. Analiz modelin kendi σ'sını kullanmaz

TR 38.901 modelleri LOS/NLOS durumuna göre farklı gölgeleme sapmaları tanımlar
(ör. UMa'da σ_LOS ≈ 4 dB, σ_NLOS ≈ 6 dB). Analiz bunların **hiçbirini
kullanmaz**. Tek kaynak:

```
σ_eff = λ · σ_nominal            σ_nominal = radio.shadowing_sigma_db = 7 dB
```

Bu değer hem ADR-18'in kapsama çarpanında hem ADR-03'ün komşu kısıtında
(`σ_eff·√2`) geçer. Gerekçe: ADR-02 kalibrasyonu tam olarak bu terimi ölçekler.
Analiz modele özgü σ kullansaydı λ'nın neyi ölçeklediği belirsizleşir, S3 ile
S5 arasında sessiz bir tutarsızlık doğardı.

> LOS/NLOS karışımının kendisi de ek bir varyans yaratır (karışım dağılımının
> varyansı bileşenlerin varyanslarının ağırlıklı ortalamasından büyüktür).
> Bu ek varyans ayrıca modellenmez; λ'nın emdiği belirsizliğin bir parçasıdır.
> Bilinen ve kabul edilen basitleştirmedir.

---

## Sonuçlar

- Analiz tarafında LOS bir **boolean değil, bir olasılıktır**; kod `los bool`
  parametresini iki kez çağırıp ağırlıklı toplar.
- Yol kaybı hesabı analiz tarafında simülatörünkinin iki katı maliyetlidir
  (LOS ve NLOS ayrı ayrı). Gün 1 benchmark'ı bunu ölçer.
- Simülatör tarafı değişmez: orada LOS gerçekleşmiş bir olaydır.

---

## Reddedilen alternatifler

| Alternatif | Ret gerekçesi |
|---|---|
| NLOS-only (muhafazakâr) | Alanı şişirir, K3'ü zayıflatır, kalibrasyonun işini tekrarlar |
| Lineer güç alanında beklenen değer | Modeller dB medyan olarak tanımlı; alan değiştirmek yeni varsayım gerektirir |
| Analizin modele özgü σ kullanması | λ'nın neyi ölçeklediğini belirsizleştirir; S3/S5 tutarsızlığı |
| LOS'u envanterde saklamak | Bilgi asimetrisini yıkar; kör testin anlamı kalmaz |
