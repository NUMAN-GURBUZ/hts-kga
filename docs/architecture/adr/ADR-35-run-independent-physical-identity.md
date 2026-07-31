# ADR-35 — Fiziksel Belirlenirlik İçin Kimlik Run_id'den Bağımsız Olmalı (K10 Kökeni)

**Durum:** Kabul edildi
**Tarih:** 2026-07-31
**Sprint:** 8 (K10 ölçümü sırasında bulundu)
**İlgili bulgular:** ADR-05, ADR-01, ADR-34, BÖLÜM J (K10)

---

## Bağlam

K10'u ilk kez gerçek verilerle ölçtüğümde (`configs/smoke.yaml`, aynı seed,
iki `run_id`, tam mod), 15.265 tahmin satırından **6.678'i** (%43,7) iki koşu
arasında uyuşmuyordu — B0/B1 alanları 20–43 km² farklı, merkezler farklı
hücrelerdeydi. Aynı ajan aynı tick'te iki koşuda **farklı fiziksel hücreye**
bağlanıyordu; ajan konumları ve hücre envanterinin fiziksel özellikleri
(konum/r_max/azimut/frekans) birebir aynıyken.

### Kök neden — tek bir desen, üç yerde

`cells.cell_id` ve `cells.site_id`, ADR-05 gereği **bilinçli olarak**
`run_id` içerir (aynı DB'yi paylaşan koşuların birincil anahtarı
çakışmasın diye — `internal/simulator/inventory/grid.go`:
`siteID(runID, axial)`). Bu doğru ve gerekli bir karardır.

Ama bu run-bağımlı kimlik, **DB birincil anahtarı olmanın ötesinde**, üç
ayrı yerde fiziksel/bilimsel bir kararı belirlemek için de kullanılmıştı:

| # | Dosya | Kullanım | Etki |
|---|---|---|---|
| 1 | `internal/simulator/radio/shadowing.go` (`SourceKey`) | Gölgeleme/LOS hash'i site UUID'sinden türetiliyordu | Aynı fiziksel site, aynı seed'le bile farklı gölgeleme alıyor → best-server kararı değişiyor |
| 2 | `internal/analysis/params/inventory.go`, `internal/integrity/source/inventory.go`, `internal/storage/redis/bulk.go`, `internal/storage/postgres/cells.go` | Envanter `cell_id`'ye göre sıralanıyordu ("K10 için" yorumuyla) | Kahan toplamının sırası koşudan koşuya değişiyor (küçük ölçekli kayan nokta gürültüsü) |
| 3 | `internal/simulator/event/injector/injector.go` (`farthestCell`) | Enjeksiyon envanteri `cell_id`'ye göre sıralanıyordu | Kural 2 (hız) enjeksiyonunun "en uzak hücre" seçimi, eşitlik durumunda run_id'ye bağlı hâle geliyordu |

Üçünde de **aynı kavramsal hata**: "DB'de çakışmasın" ihtiyacı ile "fiziksel
olarak run'dan bağımsız olsun" ihtiyacı aynı alanda (`cell_id`/`site_id`)
çözülmeye çalışılmış. İkisi çelişkilidir — biri run_id istiyor, öteki
istemiyor — ve kod her ikisini AYNI UUID'ye yükleyince K10 kaybetti.
İroniktir ki her üç yerde de "(K10)" diye açıkça belirtilmiş yorumlar
vardı; yazarken kimse `cell_id`'nin run_id içerdiğini bu yorumla birlikte
düşünmemiş.

## Karar

**Fiziksel kararlar için kimlik, konumdan türetilir — UUID'den değil.**

1. `radio.AxialKey(q, r int) uint64` eklendi: sitenin hex ızgara
   koordinatından (run_id'den bağımsız, K10) bir kaynak anahtarı üretir.
   `SourceKey(id [16]byte)` (UUID hash'i) olduğu gibi kaldı ama artık
   yalnızca run_id'siz test sabitleri için genel bir yardımcıdır —
   simülasyon şebekesi (`internal/simulator/run/network.go`) `AxialKey`
   kullanır.
2. Envanter sıralamaları (dört yer) `cell_id` yerine **fiziksel konum**
   (+ gerekirse azimut) kullanacak şekilde değiştirildi: `(Site.X, Site.Y,
   AzimuthDeg)`, `(Position.X, Position.Y)`, `(Lat, Lon, Azimuth)`,
   `ORDER BY lat, lon`.
3. Enjeksiyon envanteri (`injector.go`) aynı şekilde ENU konumuna göre
   sıralanacak biçimde değiştirildi.

Hiçbiri **DB şemasını, ADR-05'in run_id-salted kimlik kararını veya
herhangi bir bilimsel eşiği/algoritmayı** değiştirmez — yalnızca "sırayı
neyle belirliyoruz" sorusunun cevabını, run'dan bağımsız olması gereken
yerlerde run'dan bağımsız hâle getirir.

## Ölçülen etki

Düzeltmeden önce/sonra, aynı seed'li iki `run_id`, `configs/smoke.yaml`,
tam mod, 15.265 tahmin satırı:

| Aşama | Uyuşmayan satır | Oran |
|---|---|---|
| Düzeltme öncesi | 6.678 | %43,7 |
| Yalnızca AxialKey (madde 1) | 19 | %0,12 |
| + envanter sıralaması (madde 2) | 86† | %0,56† |
| + enjeksiyon sıralaması (madde 3) | **14** | **%0,09** |

† Bu ara ölçüm farklı bir run çiftiyle yapıldığından madde 1'in
sonucuyla doğrudan karşılaştırılabilir değildir; yalnızca büyüklük
mertebesini göstermek için verilmiştir.

**Kalan 14 satırın tamamı** enjekte edilmemiş (`injected_rule IS NULL`)
`M` yöntemi alanlarında, en büyük fark **1,19×10⁻⁸ km² (0,01 m²)** —
`part_count` hiçbirinde değişmiyor, B0/B1'de (kural-dışı geometri) hiç
fark yok. Bu, ADR-01/PBT'nin zaten kabul ettiği kayan nokta toleransı
(`MassTolerance = 1e-9`, alan için biraz daha büyük birikmiş hata)
mertebesindedir — geometri boru hattındaki (`ST_MakeValid`/`ST_Area`)
kalıntı kayan nokta gürültüsüdür, yapısal bir kimlik hatası değildir.

**K10, katı "bit-identical" tanımıyla teknik olarak hâlâ %100 geçmiyor**
(14/15.265 satır, ~1e-8 km² farkla) — ama düzeltme öncesi durumdan
(%43,7 uyuşmazlık, 20–43 km²'lik yanlış hücre seçimleri) niteliksel
olarak farklı bir sonuçtur. Kabul kriteri **değiştirilmedi**; ölçülen
sonuç dürüstçe budur.

## Reddedilen alternatifler

**(A) `cell_id`'yi run_id'den bağımsız yapmak.** Reddedildi: ADR-05'in
kararı (aynı DB'yi paylaşan koşuların çakışmaması) hâlâ geçerli ve bu
ADR'nin kapsamı dışında bambaşka bir mimari değişiklik olurdu.

**(B) Kalan 1e-8 km²'lik kayan nokta gürültüsünü de kovalamak.**
Reddedildi: büyüklük mertebesi (0,01 m²) hiçbir ölçülen sonuç için
(K1–K9) anlamlı değil ve kaynağı muhtemelen bu kod tabanının dışında
(PostGIS/GEOS'un kendi kayan nokta davranışı). Zaman/fayda oranı bu
noktada olumsuz.

---

## İlgili

ADR-01 (deterministik `event_id`) · ADR-05 (run_id-salted kimlik) ·
ADR-34 (K10 ölçüm metodolojisi) · `internal/simulator/radio/shadowing.go`
· `internal/simulator/event/injector/injector.go`
