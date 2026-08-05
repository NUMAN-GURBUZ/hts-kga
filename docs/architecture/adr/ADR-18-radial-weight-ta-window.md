# ADR-18 — Radyal Ağırlık ve TA Penceresi

**Durum:** Kabul edildi
**Tarih:** 2026-07-27
**Sprint:** 3 (T-E03-04, T-E03-06)
**İlgili bulgular:** G1 (plan boşluğu), İ-1, İ-2, İ-3

---

## Bağlam

Plan BÖLÜM E.1 kütle üretimini üç bileşenin çarpımı olarak tanımlar:

```
mass_raw(p) = w_ang(p) · w_rad(p) · w_nbr(p)
```

`w_ang` anten deseninden, `w_nbr` ADR-03'ten türetilmiştir. `w_rad` ise
yalnızca imzasıyla verilmiştir — `radialWeight(d, r_max, ta_ring, σ_eff)` —
sayısal tanımı yoktur. Bu ADR o boşluğu kapatır.

---

## Karar

### 1. Radyal ağırlık iki çarpandır

```
w_rad(p) = Φ( (P_s(p) − RxSens) / σ_eff ) · w_TA(p)
```

**Birinci çarpan — kapsama olasılığı.** Kaydın var olması, `p` noktasında
serving hücrenin alınan gücünün alıcı duyarlılığını aştığı anlamına gelir.
Gölgeleme rastgele olduğundan bu bir olasılıktır ve hesaplanabilir:
`P_s(p)` gölgelemesiz alınan güçtür (EIRP − PL − A_beam, ADR-19'a göre),
`Φ` standart normal dağılım fonksiyonudur. Marj büyükse w → 1, duyarlılığın
altındaysa w → 0.

Bu, ADR-03'ün komşu kısıtıyla **aynı mantıktan** türer: her ikisi de kaydın
kendisinden gelen bilgiyi olasılığa çevirir. Keyfi katsayı yoktur.

**İkinci çarpan — TA penceresi.** Aşağıda tanımlıdır; TA yoksa `w_TA ≡ 1`.

### 2. TA penceresi serttir (İ-1)

Simülatör TA'yı gürültüsüz türetir (`pkg/ta`: `floor(d2D / res)`). Gerçek konum
bu yüzden **tanım gereği** halkanın içindedir. Yumuşak bir kenar, dayanağı
olmayan bir serbest parametre (σ_TA) eklemek olurdu; plan böyle bir parametreye
izin vermez.

> Sonuç olarak simülatöre TA ölçüm gürültüsü **eklenmez**. Eklenseydi yumuşak
> kenarın genişliği türetilebilir olurdu, ama bu T-E02-15'i değiştirir ve
> modeli gereksiz yere karmaşıklaştırırdı.

### 3. Ağırlık, örtüşme oranıdır (İ-2)

```
w_TA(p) = alan( hex(p) ∩ halka ) / alan( hex(p) )        ∈ [0,1]
```

Merkez-içinde-mi ikili testi **kullanılmaz**. Gerekçe sayısaldır: LTE'de TA
adımı 78,12 m, ızgara çözünürlüğü ise 100 m'dir. Halka bir ızgara satırından
ince olduğu için ikili test halkayı çoğu olayda ya hiç yakalamaz (∅ fallback
sürekli tetiklenir, TA'nın bilimsel katkısı buharlaşır) ya da rastgele tek bir
satır yakalar. Örtüşme oranı bu sorunu yaklaşık değil **tam** çözer.

**Halka genişletilmez.** Örtüşme oranı ayrıklaştırma hatasını zaten tam olarak
karşıladığından, hücrenin çevrel yarıçapı (res/√3 ≈ 57,7 m) kadar ek bir
genişletme telafiyi çiftler: 2 km yarıçapta 78,12 m kalınlığındaki bir LTE
halkası ≈ 0,98 km²; iki yandan genişletilirse ≈ 2,43 km², yani **~2,5× alan
şişmesi**. K3'ün daralma iddiası doğrudan bu alandan ölçüldüğü için bu kabul
edilemez.

### 4. Örtüşme alanı tam hesaplanır, örneklenmez

```
alan(hex ∩ halka) = alan(hex ∩ disk(R_dış)) − alan(hex ∩ disk(R_iç))
```

Dışbükey çokgen ile dairenin kesişim alanı kenar başına kapalı formülle
hesaplanır (üçgen + dairesel kesme parçalarının toplamı). Alt-örnekleme
(hücre içinde n nokta) kasten seçilmemiştir: n bir serbest parametre olurdu
ve ağırlığa örnekleme gürültüsü katardı.

### 5. Boş kesişim → TA yok sayılır (T-E03-04)

Sektör dilimi ile halkanın kesişimi boşsa — yani ızgaradaki hiçbir hücrenin
örtüşme oranı sıfırdan büyük değilse — TA yok sayılır, `w_TA ≡ 1` alınır ve
`estimates.ta_used = false` yazılır. Bu, tahmini kaybetmek yerine bilgiyi
düşürmektir; hangi tahminlerin TA'sız üretildiği izlenebilir kalır.

### 6. Izgara çözünürlüğü: kentsel 100 m, kırsal 250 m (ölçüldü)

Gün-1 benchmark'ı koşuldu (12 çekirdek, envanter 327 hücre, 8 komşu, olay
başına tam kütle üretimi):

| Profil | Izgara | TA | Hücre/olay | Süre/olay | 'V' geçişi (60k olay) |
|---|---|---|---|---|---|
| Kentsel | 100 m | — | 2.457 | 5,95 ms | 5 dk 57 sn |
| Kentsel | 100 m | ✓ | 2.457 | 1,24 ms | 1 dk 14 sn |
| **Kırsal** | **100 m** | — | **55.456** | **174 ms** | **2 sa 54 dk** |
| Kırsal | 250 m | — | 8.872 | 28,6 ms | 28 dk 34 sn |
| Kırsal | 500 m | — | 2.217 | 7,47 ms | 7 dk 28 sn |

Kırsalda 100 m bütçeyi tutmuyor: kalibrasyon + doğrulama tek senaryoda ~5 sa
48 dk, iki kırsal senaryoda ~11,6 saat. **Karar: kırsal profilde 250 m,
kentselde plan C.1'deki 100 m.** Kırsal geçiş 28 dakikaya iner.

Bileşen maliyetleri darboğazın yerini gösterir:

| Bileşen | Hücre başına | Pay |
|---|---|---|
| `w_nbr` (8 komşu × LOS/NLOS karışımı = 16 model çağrısı) | 3.264 ns | ~%75 |
| `w_rad` | 662 ns | ~%15 |
| örtüşme oranı (tam analitik) | 248 ns | ~%6 |
| `w_ang` | 211 ns | ~%5 |

Not: örtüşme oranının tam analitik hesabı maliyetin yalnızca %6'sı. İ-2'de
"örtüşme pahalı olabilir" endişesi ölçümle **doğrulanmadı**; darboğaz komşu
kısıtıdır. Çözünürlük ölçeklemesi bu yüzden granülerlikten feragat eder,
doğruluktan değil — komşu sayısını düşürmek ADR-03'ün kendisini bozardı.

---

### 7. Açısal ağırlık **tam** anten desenini kullanır (plan E.1 tadili)

Plan E.1 `w_ang(p) = beamPattern(|θ_s(p) − azimuth_s|)` yazar — yalnızca azimut.
Analiz bunun yerine `rf.AntennaAttenuationDB`'yi tam çağırır: **azimut + eğim**.

```
w_ang(p) = 10^( −A(φ, θ) / 10 )        A = min[ A_H(φ) + A_V(θ) , A_max ]
```

Gerekçe tutarlılıktır: simülatör best-server'ı tam desenle seçti (T-E02-12).
Analiz indirgenmiş bir desen kullansaydı sistematik bir **biçim** hatası
doğardı — λ kalibrasyonu ölçek hatasını emer, biçim hatasını emmez. Eğim,
hüzme merkezini yataydan aşağı kaydırdığı için yakın mesafelerdeki ağırlığı
belirgin biçimde değiştirir; ihmal edilirse direk çevresindeki kütle olduğundan
büyük çıkar.

> **Plan BÖLÜM E.1 buna göre tadil edilmiştir:** `w_ang` satırı artık
> "tam anten deseni (azimut + eğim), simülatör tutarlılığı için" okunur.

### 8. Arama bölgesinin yarıçapı: link budget r_max (T-E02-05 revizyonu)

Dilimin yarıçapı `cells.r_max_m`'dir. O değer 2026-07-27'ye kadar profil
sabitiydi (kentsel 5 km) ve gerçek link budget'la ilgisizdi: alınan güç 5 km'de
hâlâ duyarlılığın 3,5 dB üstündeydi. Arama bölgesi bu yüzden kütlenin
kuyruğunu sistematik olarak kırpıyor ve K2/K3 daralma oranlarını **iyimser
yanlı** yapıyordu.

T-E02-05 revize edildi: r_max artık hücre başına link budget'tan çözülür ve
**aynı** log-alanı karışımını kullanır (ADR-19). Sonuç olarak r_max
mesafesinde kapsama olasılığı tam 0,5 çıkar — simülatörün kestiği yer ile
analizin baktığı yer aynı noktadır.

---

## Ölçüldü: w_ang'ın çifte sayımı — plan korunuyor

Hüzme deseni kütleye iki kez girer: `w_ang` olarak doğrudan, ve `w_rad`'ın
kapsama çarpanındaki `P_s = EIRP − PL − A_beam` içinde dolaylı olarak
(`w_nbr`'ın Δ(p) marjında da vardır). Olasılıksal okumada
`P(kayıt | p) = P(s en güçlü ∧ rx ≥ eşik | p)` ifadesini `w_nbr · w_rad` zaten
karşılar; `w_ang`'ın ayrı bir olasılıksal karşılığı yoktur.

Çifte sayım kütleyi hüzme eksenine fiziksel gerekçeden fazla sıkıştırır ve
alanı **yapay olarak** daraltır — yani K3'ü olduğundan güçlü gösterir.

**Karar ölçüme bırakılmıştır.** KAPI 2'de tek senaryoda iki varyant koşulur:

| Varyant | Kütle |
|---|---|
| Üç çarpanlı (plan E.1) | `w_ang · w_rad · w_nbr` |
| `w_ang` düşürülmüş | `w_rad · w_nbr` (desen yalnız P_s içinden) |

Medyan alan farkı **< %5** ise plan korunur; fark büyükse `w_ang` düşürülür.
Eşik ölçümden önce beyan edilmiştir.

### Sonuç (120 olay × 2 varyant, `TestAngularDoubleCounting`)

| Yapılandırma | Üç çarpanlı | `w_ang` düşürülmüş | Fark |
|---|---|---|---|
| Kentsel, TA yok | 9,4700 km² | 9,3098 km² | **−1,72 %** |
| Kentsel + TA | 0,1992 km² | 0,1992 km² | **0,00 %** |

**Karar: plan E.1'in üç çarpanlı formu korunur.** Fark eşiğin çok altındadır.

İşaretin **ters** çıkması ayrıca kayda değerdir. Endişe, `w_ang`'ın alanı
yapay olarak daraltıp K3'ü şişirmesiydi; ölçüm tersini gösteriyor — üç
çarpanlı varyant %1,72 **daha geniş** alan veriyor, yani çifte sayım iddiayı
güçlendirmiyor, hafifçe zayıflatıyor.

Nedeni anlaşılırdır: direğe yakın bölgede kapsama olasılığı Φ zaten 1'e
doyduğundan `w_rad` açıya göre neredeyse değişmez; `w_ang` ise hüzme ekseni
boyunca uzaktaki hücreleri göreli olarak yukarı çeker ve kütleyi eksen boyunca
uzatır. TA'lı senaryoda halka kütleyi zaten kilitlediği için fark tamamen
sıfırlanır.

Kod `core.Options.IncludeAngular` bayrağını korur (varsayılan `true`): ölçüm
ileride yinelenebilir olmalıdır.

---

## Bilinen basitleştirme (İ-3)

`w_rad`'ın birinci çarpanı ("serving eşiği aştı") ile `w_nbr` ("serving en
güçlüydü") **aynı** gölgeleme gerçekleşmesinden sürüklenir. Çarpım bu ikisini
bağımsız kabul eder; değildirler. Sonuç kütleyi hafifçe yanlı yapar.

Bu bilinçli bir basitleştirmedir: ortak dağılımı doğru ele almak iki boyutlu
bir koşullu olasılık hesabı gerektirir ve λ kalibrasyonu (ADR-02) kalan
sapmayı zaten emer. Belgede bu şekilde raporlanır; savunmada gizlenmez.

---

## Reddedilen alternatifler

| Alternatif | Ret gerekçesi |
|---|---|
| Yumuşak TA kenarı (σ_TA) | Gürültüsüz TA'da dayanağı yok; serbest parametre |
| Merkez-içinde-mi ikili testi | 78 m halka < 100 m ızgara; ∅ fallback'i sürekli tetikler |
| Halka genişletme + örtüşme birlikte | Telafi çiftlenir, ~2,5× alan şişmesi, K3 zayıflar |
| Hücre içi alt-örnekleme | Örnekleme yoğunluğu serbest parametre; gürültü katar |
| Mesafeye bağlı sabit ceza | ADR-03'te zaten reddedildi; fiziksel dayanağı yok |
