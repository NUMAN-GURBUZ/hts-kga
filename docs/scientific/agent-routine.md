# Ajan Günlük Rutini ve Hareket Modeli

**Görevler:** T-E02-08 (4 fazlı rutin + haftalık periyodisite), T-E02-09 (hız profili + gürültü)
**Uygulama:** `internal/simulator/agent/routine.go`, `internal/simulator/agent/mobility.go`

---

## 1. Neden bu belge var

Plan v3.0, T-E02-08'i tek satırla tanımlar: *"4-faz rutin + haftalık periyodisite
(profil bazlı)"*. Sayısal bir tanım — faz saatleri, sapma genlikleri, hafta sonu
davranışı — verilmez. T-E02-13'ün λ(saat) eğrisi de aynı durumdadır (Sprint 3).

Bu boşluk uygulama sırasında doldurulmak zorundaydı. Seçimlerin keyfî
görünmemesi ve tez savunmasında gerekçelendirilebilmesi için kararlar burada
kayda geçirilmiştir.

---

## 2. Günlük yapı (hafta içi)

Klasik ev–iş–ev örüntüsü, dört evreye ayrılır:

| Evre | Sabit | Süre |
|---|---|---|
| Ev | `PhaseHome` | Gece yarısından çıkışa, dönüşten gece yarısına |
| İşe gidiş | `PhaseCommuteToWork` | `CommuteMin` (türetilmiş) |
| İş | `PhaseWork` | Varıştan dönüş anına |
| Eve dönüş | `PhaseCommuteToHome` | `CommuteMin` (türetilmiş) |

```
00:00 ────────── EV ────────── T_çıkış
T_çıkış ──── İŞE GİDİŞ ──── T_çıkış + T_yol
              ────────── İŞ ────────── T_dönüş
T_dönüş ──── EVE DÖNÜŞ ──── T_dönüş + T_yol
              ────────── EV ────────── 24:00
```

### Nominal saatler

| Parametre | Değer | Gerekçe |
|---|---|---|
| Nominal işe çıkış | 08:00 | Standart mesai başlangıcı öncesi tipik çıkış |
| Nominal eve dönüş | 17:30 | Standart mesai bitişi |
| Kişisel sapma | ±45 dk | Aşağıya bakınız |

### Neden kişisel sapma zorunlu

Tüm ajanlar aynı dakikada işe çıksaydı, olay üretimi (Sprint 3) günde iki kez
keskin tepe verirdi. Gerçek şebekelerde işe geliş saatleri yaklaşık normal
dağılır. ±45 dakikalık düzgün sapma, 1,5 saatlik gerçekçi bir yayılım üretir
ve ajan kimliğinden deterministik olarak türetilir (K10 korunur).

Ölçülen dağılım (10.000 örnek): ortalama 480,0 dk, aralık ~90 dk.

---

## 3. Yolculuk süresi: sabit değil, türetilmiş

```
T_yol = ⌈ (mesafe / 1000) / hız_profil × 60 ⌉    (dakika, alt sınır 1)
```

**Neden sabit süre kullanılmadı:** ADR-17, kentsel ve kırsal profilleri hem
mesafe hem hız bakımından ayırır:

| Profil | Ev–iş mesafesi | Yolculuk hızı | Türetilen süre |
|---|---|---|---|
| Kentsel | 1–8 km | 30 km/s | 2–16 dk |
| Kırsal | 5–25 km | 70 km/s | 5–22 dk |

Sabit bir süre bu profil farkını yok ederdi: kırsal ajan uzun mesafeyi kısa
sürede almalı, kentsel ajan kısa mesafeyi görece yavaş. Türetilmiş süre bu
ilişkiyi korur ve ADR-17'nin `commute_speed_kmh` alanını anlamlı kılar.

---

## 4. Haftalık periyodisite

Koşu **Pazartesi gece yarısı** başlar (gün 0). Gün indeksi 5 (Cumartesi) ve
6 (Pazar) hafta sonudur; bu günlerde **iş evresi yoktur**, ajan gün boyu ev
çapasındadır.

### Neden hafta sonu gezisi modellenmedi

Hafta sonu boş zaman hareketliliği (alışveriş, ziyaret) üçüncü bir çapa türü
gerektirir. Plan böyle bir çapa tanımlamaz; ADR-17 profili yalnızca
`agent_home_work_km` verir. Uydurulmuş bir "boş zaman çapası" hem parametresiz
kalır hem de savunulamaz olurdu.

Seçilen model, planın "haftalık periyodisite" gereksinimini karşılayan **en
küçük** ve tamamen gerekçelendirilebilir varsayımdır: hafta sonu iş yolculuğu
yoktur. Sonuç olarak olay dağılımı hafta içi/hafta sonu farkı gösterir, ki
periyodisitenin amacı da budur.

**Bilinen sınır:** hafta sonu hareketliliği gerçekte sıfır değildir. Bu, bilinçli
bir sadeleştirmedir ve teknik borç olarak kayıtlıdır.

---

## 5. Hareket modeli

### Türetilmiş konum, biriktirilmiş değil

Konum her tick'te evreden **yeniden hesaplanır**; adım adım biriktirilmez
(`p += v·Δt` değil).

| Biriktirmeli modelin sorunu | Türetilmiş modelin çözümü |
|---|---|
| Kayan nokta hatası 8.640 tick boyunca birikir | Her tick bağımsız, hata birikmez |
| Herhangi bir tick'e atlanamaz | `PositionAt(s, tick)` doğrudan çağrılabilir |
| Tick başına çekim sayısı değişirse akış kayar | Hash tabanlı, çekim sırası yok |

Son madde K10 açısından kritiktir: Sprint 3'te Poisson olay üreteci eklendiğinde
tick başına çekilen rastgele sayı adedi değişecektir. Durum tutan bir üreteç
kullanılsaydı hareket akışı kayar ve Sprint 2 çıktıları değişirdi.

### Gürültü bileşenleri

| Bileşen | Ölçek | Temsil ettiği |
|---|---|---|
| Çapa gürültüsü | 25 m | Bina içi/çevresi hareket |
| Güzergâh sapması | 60 m (uçlarda 0'a iner) | Yolların düz olmaması |
| Hız dalgalanması | ±%25 | Trafik, sinyalizasyon, yürüyüş parçaları |

Güzergâh sapması `4·p·(1−p)` çarpanıyla ölçeklenir: yolculuğun ortasında en
büyük, uçlarda sıfırdır. Aksi hâlde ajan çapasının **üstüne** değil yanına
varırdı ve kapsama garantisi (ADR-08/2) bozulurdu.

### Hız üst sınırı doğrulaması

Bütünlük Kural 2, 300 km/s üzerini manipülasyon sayar. Simülatörün imkânsız hız
üretmemesi, S4'ün yakaladığı her ihlalin gerçekten enjeksiyon kaynaklı olmasını
garanti eder.

En zorlu durumda (kırsal, 25 km ev–iş, 22 dk yolculuk) ölçülen en yüksek anlık
hız **120,7 km/s**'dir — sınırın %40'ı. `TestMobility_SpeedWithinIntegrityLimit`
bunu her koşuda doğrular.

---

## 6. Determinizm (K10)

Tüm rastgelelik `(tohum, ajan kimliği, tick, amaç)` dörtlüsünden hash ile
türetilir; paylaşılan üreteç durumu yoktur.

Sonuç: 1000 goroutine eşzamanlı çalışsa da çıktı goroutine zamanlamasından
bağımsızdır. `TestWorld_ParallelMatchesSerial` ve
`TestWorld_WorkerCountDoesNotAffectResult`, 1/2/3/7/8/16/64 goroutine
yapılandırmalarının **birebir aynı** sonucu ürettiğini doğrular.

---

## 7. Kaynak parametreler özeti

| Parametre | Değer | Kaynak |
|---|---|---|
| Nominal işe çıkış | 08:00 | Bu belge |
| Nominal eve dönüş | 17:30 | Bu belge |
| Kişisel sapma | ±45 dk | Bu belge |
| Hafta sonu | Gün 5–6, iş yok | Bu belge |
| Ev–iş mesafesi | Kentsel 1–8 km, kırsal 5–25 km | ADR-17 |
| Yolculuk hızı | Kentsel 30, kırsal 70 km/s | ADR-17 |
| Tick uzunluğu | 5 dk | `configs/*.yaml` |
| Ajan sayısı | 1000 | `configs/*.yaml` |
| Hız üst sınırı | 300 km/s | `configs/*.yaml` (integrity) |
