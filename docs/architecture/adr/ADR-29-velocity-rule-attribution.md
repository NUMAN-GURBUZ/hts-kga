# ADR-29 — Kural 2: Kinematik Tutarlılık, Atıf ve Karar Verilemezlik

**Durum:** Kabul edildi — **spesifikasyon dondurulmuştur**
**Tarih:** 2026-07-30
**Sprint:** 6 (T-E05-05)
**İlgili bulgular:** BÖLÜM C.1 (300 km/h), K7, ADR-28

---

## Bağlam

Kural 2, planın "hız 300" olarak andığı tespit kuralıdır. Sprint 6 ön analizinde
üç ayrı tasarım Sprint 5 verisi üzerinde ölçüldü:

| Tasarım | Precision (kentsel / kırsal) | Recall (kentsel / kırsal) |
|---|---|---|
| (a) Naif: kayıt bazlı, `max(v_önceki, v_sonraki) > 300` | %36,5 / %42,1 | %4,9 / %12,3 |
| (b) Katı üçlü: `v(A,B)>300 ∧ v(B,C)>300 ∧ v(A,C)≤300` | %100 / %100 | **%0,08 / %0,59** |
| (c) Yerel destek atfı | **%71,8 / %83,4** | %4,7 / %12,3 |

(a) K7 eşiğinin çok altında; (b) precision'ı kusursuz ama 1–7 bulgu üretiyor —
tek bulguyla "%100 precision" beyan etmek istatistiksel olarak boştur.
(c) ikisinin arasında ve ADR-28'in öncelik kuralıyla birlikte ~%80 / ~%90'a
çıkıyor.

**Bu ADR'nin var oluş nedeni budur:** etiketli veri elimizde olduğu için kuralı
%90'ı geçene kadar ayarlamak teknik olarak kolaydır ve tanımı gereği aşırı
uydurmadır. Spesifikasyon bu yüzden **kod yazılmadan** dondurulur.

---

## Karar

### 1. İhlal geçişin özelliğidir, kaydın değil

```
ihlal(i, j)  ⟺  d(cell_i, cell_j) / Δt(i, j)  >  max_velocity_kmh
```

`i` ve `j` aynı abonenin olay zamanına göre ardışık iki kaydıdır. `d` iki
serving hücrenin site konumları arasındaki yüzey mesafesidir (`pkg/geo`).

Bir kayıt kendi başına "çok hızlı" olamaz; iki kayıt birlikte imkânsız olur.

### 2. Eşik 300 km/h — korunur, gerekçesi tamamlanır

Plan BÖLÜM C.1 eşiği 300 km/h olarak sabitlemiş. **Değiştirilmiyor**: fiziksel
olarak savunulabilir bir üst sınırdır (yüksek hızlı tren mertebesi) ve ölçümden
önce beyan edilmiştir.

Ancak planda hesaplanmamış bir sonucu vardır ve burada yazılı hâle getirilir:

```
ortalama olay aralığı  = 2,4 saat   (T-E02-13: ajan başına günde 10 olay)
kentsel envanter çapı  ≈ 10 km      (yarıçap 5 km)
kırsal envanter çapı   ≈ 40 km      (yarıçap 20 km)

300 km/h eşiğini aşmak için:  Δt < 2 dk (kentsel) · Δt < 8 dk (kırsal)
damgalar 5 dakikalık tick ızgarasındadır
```

Yani atlamaların büyük kısmı **fiziksel olarak mümkündür** ve hiçbir kural
tasarımı bunu değiştiremez — bilgi veride yoktur. Recall'un %4,7–12,3'te
kalması bir tasarım kusuru değil, ölçülmüş bir bilgi sınırıdır. K7 recall'u
eşiğe bağlamaz; raporlar.

Duyarlılık eğrisi **ek** olarak raporlanır (eşiği değiştirmez):

| Eşik | Kentsel isabet | Kırsal isabet |
|---|---|---|
| 100 km/h | 113 | 334 |
| 200 km/h | 58 | 191 |
| **300 km/h** | **58** | **147** |

### 3. `Δt = 0` gerçekten imkânsızdır

Aynı tick'in farklı olayları **aynı damgayı** taşır (`Builder.TimeAt(tick)`).
Temiz kayıtlarda bu sorun değildir: aynı tick, aynı gözlem, aynı hücre → mesafe
sıfır. Ama mesafe sıfırdan büyükse:

```
Δt = 0  ∧  d > 0   →  ihlal (sonsuz hız)
```

`margin` sonsuz olacağından üst sınırla kapılır (`velocity_margin_cap`, ADR-31)
ve `evidence.zero_interval = true` işaretlenir.

Bu popülasyon önemsiz değildir: kentsel koşuda >300 km/h isabetlerin **tamamı**
buradadır. `+Inf` koruması olmadan `json.Marshal` hata verir ve kural kentselde
sıfır bulgu üretir.

### 4. Kural 1 kayıtları zincirden çıkarılır

Envanterde olmayan bir hücreye işaret eden kaydın konumu bilinmez. Zincire NULL
konumla girerse ardışık çiftler kırılır ve komşu geçişler yanlış hesaplanır.
Çıkarılır; o kayıt zaten kural 1 tarafından talep edilmiştir (ADR-28).

### 5. Atıf — yerel destek

İhlal eden geçişin hangi ucu abonenin **yerel yörüngesinden** uzaklaşıyorsa
bulgu ona yazılır.

```
Abone dizisi:  … p2 , p1 , b , n1 , n2 …          (olay zamanına göre)

Gelen kenar (p1 → b) ihlal ediyorsa:
    p2 mevcut  ∧  d(b, p2) > d(p1, p2)      →  b suçlanır

Giden kenar (b → n1) ihlal ediyorsa:
    n2 mevcut  ∧  d(b, n2) > d(n1, n2)      →  b suçlanır
```

Sezgi: atlanan kayıt envanterin en uzak hücresine taşınmıştır; abonenin
komşu kayıtları gerçek konumun etrafındadır. Dolayısıyla dış komşuya olan
mesafe, suçlu uçta belirgin biçimde büyüktür.

### 6. Karar verilemezlik → bulgu üretilmez

Karşılaştırma **katı eşitsizliktir**. Aşağıdaki durumlarda bulgu yoktur:

| Durum | Neden |
|---|---|
| Dış komşu yok (`p2` veya `n2` mevcut değil) | Referans noktası yok — hangi uç olduğu bilinemez |
| `d(b, dış) = d(eş, dış)` | Simetrik; kanıt tek kaydı göstermiyor |
| `d(b, dış) < d(eş, dış)` | Eş suçlu görünüyor; o kayıt kendi sırası geldiğinde değerlendirilir |
| Abonenin tek kaydı var | Geçiş yok |

Bu, ADR-28/3'ün uygulanışıdır: adli bir sistem iki tarafı birlikte suçlamaz.
Precision recall pahasına korunur ve bu değiş-tokuş **önceden beyan edilmiştir**.

### 7. Öncelik

Kural 1, 5 veya 3 tarafından talep edilmiş olaylar değerlendirmeye girmez
(ADR-28). Kaydırılmış bir damganın yol açtığı kinematik anomali ayrı bir
manipülasyon değil, aynı manipülasyonun sonucudur.

### 8. Kural 2 atomiktir

Geçiş tespiti (madde 1–4) atıf (madde 5–7) olmadan **devreye alınmaz**. Atıfsız
kural precision %36 üretir; K7 raporuna böyle bir satır girmesi, kuralı hiç
yazmamaktan kötüdür. Uygulama yarım kalırsa kural tamamen devre dışı bırakılır
ve K7 üç yapısal kuralla kapanır.

---

## Beyan (ölçümden önce)

**Kural 2'nin K7 eşiğini (%90) tutmaması beklenen sonuçtur.** Sprint 5 verisi
üzerindeki tasarım ölçümü precision'ı %71,8–83,4 (öncelikle ~%80–90) olarak
verdi. Tutmazsa, K1'de olduğu gibi **önceden beyan edilmiş bir negatif
bulgudur** ve nedeni ölçülmüş bir bilgi sınırıdır (madde 2).

**Spesifikasyon ölçümden sonra değiştirilmeyecektir.** Değiştirilirse Sprint 6
raporu bunu açıkça belirtir ve önceki ölçümü de gösterir.

---

## Kapatılmış yol: TA / `r_max` tutarlılığı

Atlanan kayıt eski hücrenin `ta_value`'sunu taşır. Yeni hücrede
`ta_value · 78,12 m > r_max_m` olması beklenebilirdi — statik, tek kayıtlık,
Δt'den bağımsız bir sinyal.

Dört koşuda **sıfır ihlal** ölçüldü: `r_max` (kentsel ~1–2 km, kırsal daha
büyük) TA mesafelerine göre çok büyük. Bu yol kapalıdır ve Sprint 6'da
denenmeyecektir.

---

## Reddedilen alternatifler

**(A) Naif kayıt bazlı kural.** Precision %36,5 / %42,1 ölçüldü. Reddedildi.

**(B) Katı üçlü sınama.** Precision %100 ama recall %0,08–0,59 (1–7 bulgu).
Reddedildi: ADR-31'in ölçülebilirlik kuralına göre 30'un altındaki bulgu sayısı
"istatistiksel olarak yetersiz" sayılır; kural K7'ye anlamlı bir katkı
yapamazdı.

**(C) Eşiği düşürmek (100 veya 200 km/h).** Reddedildi: eşik BÖLÜM C.1'de
ölçümden önce beyan edilmiş bir parametredir. Recall'u yükseltmek için sonradan
düşürmek, sonuca göre tanım değiştirmektir. Duyarlılık eğrisi ek bilgi olarak
raporlanır.

**(D) Morfolojiye göre eşik (kentsel 100, kırsal 300).** Reddedildi: senaryo
karşılaştırmasına confound sokar — "kırsalda recall daha yüksek" ifadesi
morfolojinin mi eşiğin mi etkisi olduğu ayırt edilemez hâle gelir. ADR-26'nın
"morfoloji içinde tek çözünürlük" gerekçesiyle aynı mantık.

**(E) Enjektörü daha tespit edilebilir hâle getirmek** (atlama hedefini Δt'ye
göre seçmek). Reddedildi: ölçüm sonucuna göre veri üretecini değiştirmek BÖLÜM J
disiplininin ihlalidir.

---

## İlgili

BÖLÜM C.1 (300 km/h) · ADR-09 (enjeksiyon kural 2) · ADR-28 (öncelik, tek-atıf) ·
ADR-31 (`margin` üst sınırı, ölçülebilirlik kuralı) · T-E02-13 (olay yoğunluğu)
