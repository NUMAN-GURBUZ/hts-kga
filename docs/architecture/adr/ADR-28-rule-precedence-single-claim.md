# ADR-28 — Kural Motoru: Kanıt Gücü Önceliği ve Tek-Atıf

**Durum:** Kabul edildi
**Tarih:** 2026-07-30
**Sprint:** 6 (T-E05-01)
**İlgili bulgular:** BÖLÜM F (F.5), K7, ADR-09, ADR-27

---

## Bağlam

F.5 precision'ı **tam eşleşme** ile ölçer:

```sql
count(*) FILTER (WHERE g.injected_rule = f.rule_id) / count(*)  GROUP BY f.rule_id
```

Yani bir kural, *başka bir kuralla* enjekte edilmiş bir olayı yakalarsa bu
**yanlış pozitif** sayılır. Bu tanım Sprint 6 ön analizinde iki yapısal sızıntı
ortaya çıkardı ve ikisi de ölçüldü.

### Sızıntı 1 — çapraz bulaşma

Enjeksiyon kural 3 damgayı geriye kaydırır. Kaydırılmış kayıt, komşularıyla
arasında **kinematik olarak imkânsız bir geçiş** de üretir; hız kuralı tetiklenir.

| Koşu | Hız kuralı bulguları | Kural 2 | **Kural 3** | Kural 5 | Temiz |
|---|---|---|---|---|---|
| A kentsel | 159 | 58 | **21** | 1 | 79 |
| C kırsal | 349 | 147 | **24** | 1 | 177 |

Dedektör "burada bir manipülasyon var" derken doğrudur; F.5 "yanlış kuralı
söyledin" der. İkisi de haklı — çözüm ölçümü değiştirmek değil, hangi kuralın
konuşacağına karar vermektir.

### Sızıntı 2 — çift ucu işaretleme

Hız ihlali bir **geçişin** özelliğidir, tek bir kaydın değil. Kayıt bazlı bir
kural geçişin iki ucunu da işaretler; biri enjekte edilmiş, diğeri temizdir →
temiz eş **her zaman** yanlış pozitiftir. Precision yapısal olarak %50'nin
altına iner.

Kentsel koşuda birebir doğrulandı:

```
58 (kural 2 isabeti) + 21 (kural 3 isabeti) = 79 = temiz bulgu sayısı
```

Her ihlal eden çift bir enjekte + bir temiz kayıt üretiyor.

---

## Karar

### 1. Öncelik sırası kanıt gücüne göre belirlenir

Sıralama keyfi değildir; kanıtın **ne kadar yorum gerektirdiğine** göre kurulur:

| Sıra | Kural | Kanıt tipi | Eşik var mı | Atıf belirsizliği |
|---|---|---|---|---|
| 1 | **1 envanter** | küme üyeliği (`cell_id ∉ cells`) | Hayır | Yok |
| 2 | **5 aktivite** | kapalı popülasyonda çoğunluk | Hayır | Yok |
| 3 | **3 zaman** | varış sırası ↔ olay zamanı çelişkisi | Hayır | Yok |
| 4 | **2 hız** | türetilmiş oran (mesafe/süre) | **Evet** | **Evet** |

Eşiği ve atıf belirsizliği olmayan kanıt, olanın önündedir. Kural 2 en sonda
çünkü tek başına hem bir parametreye (300 km/h) hem bir karara (hangi uç?)
dayanıyor.

Kural 1'in kural 5'in önünde olması: envanterde olmayan bir hücreye işaret eden
kayıt, cihaz kimliğinden bağımsız olarak zaten geçersizdir.

### 2. Bir olay → en çok bir kanonik bulgu

Yüksek öncelikli kural olayı **talep eder** (claim). Alt sıradaki kurallar o
olayı değerlendirmez.

Gerekçe: bir kaydın nasıl bozulduğunun **tek** bir en iyi açıklaması vardır.
Kaydırılmış bir damganın yol açtığı hız anomalisi, ayrı bir manipülasyon değil
aynı manipülasyonun sonucudur; iki bulgu yazmak olayı iki kez saymaktır.

### 3. Bir geçiş → en çok bir bulgu; karar verilemezse bulgu yok

Geçiş anomalilerinde (kural 2) ihlal iki kaydı birlikte ilgilendirir. Motor
ihlali **bir** uca atfeder (ADR-29). Atıf yapılamıyorsa **hiç bulgu
üretilmez**.

Gerekçe adli bir ilkedir: kanıt tek bir kaydı göstermiyorsa suçlama yapılmaz.
Bir bütünlük denetim sisteminin çıktısı bir iddianamedir; "bu ikisinden biri"
diyen bir iddianame kullanılamaz.

### 4. Bastırma bilimi gizlemez — bir etikettir, filtre değildir

Motor **tüm** isabetleri yazar. Öncelik yarışını kaybedenler
`integrity_findings.suppressed_by` sütununda kazanan kuralın kimliğiyle
işaretlenir.

| Kullanım | Sorgu | Amaç |
|---|---|---|
| **F.5 kanonik ölçüm (K7)** | `WHERE suppressed_by IS NULL` | Kural bazında precision/recall |
| **Karışıklık matrisi** | tüm satırlar | Hangi kuralın hangi enjeksiyonu gördüğü |

Bu ayrım ölçümden **önce** beyan edilmiştir. Bastırılmış satırların da
yazılması, "precision'ı filtreyle şişirdiniz" itirazını yapısal olarak kapatır:
bastırılan her isabet veritabanında durur ve denetlenebilir.

`detected_in` sütunu ayrıca hangi fazın bulduğunu kaydeder (ADR-27 sınıf
kısıtının ihlali böylece veriden de görülür).

### 5. Kural bastırmayı bilmez

Kural arayüzü ham isabet (`Hit`) döndürür; öncelik ve bastırma **motorda**
uygulanır ve `Finding`'e dönüşür.

Gerekçe: öncelik mantığı kuralların içine yazılırsa beş yerde tekrarlanır ve
zamanla ayrışır — `internal/persist`'in tek çekirdekle iki rolü çözmesiyle aynı
gerekçe. Ayrıca kural birim testleri o zaman öncelik durumunu kurmak zorunda
kalır ve kuralın kendisini sınamaz.

### 6. Talep defteri fazlar arasında veritabanı üzerinden devredilir

Toplu faz başlangıçta defteri `integrity_findings`'ten yükler:

```sql
SELECT event_id, rule_id FROM integrity_findings
WHERE run_id = :run AND suppressed_by IS NULL;
```

Bellekte taşınsaydı toplu fazı tek başına yeniden koşmak (hata ayıklamada
normal ihtiyaç) öncelik bilgisini kaybederdi. Veritabanı üzerinden devir, toplu
fazı bağımsız yeniden koşulabilir ve denetlenebilir kılar. Maliyet önemsizdir:
koşu başına ~4.000 satır.

### 7. Fazlar arasında **ilk talep kazanır**; faz içinde öncelik uygulanır

Öncelik sırası ile faz sırası birbirine denk değildir:

| Kural | Öncelik | Faz |
|---|---|---|
| 1 envanter | 0 | akış |
| 5 aktivite | **1** | **toplu** |
| 3 zaman | **2** | **akış** |
| 2 hız | 3 | toplu |

Kural 5, kural 3'ten güçlüdür ama **sonraki** fazda koşar. Aynı olay her ikisi
tarafından da yakalanırsa, güçlü olan geldiğinde zayıf olanın bulgusu **zaten
yazılmıştır**.

**Karar:** bir olay bir kez talep edildiyse, sonraki fazın isabeti — öncelik
sırasında daha güçlü olsa bile — bastırılmış yazılır.

Üç gerekçe:

1. **Ekle-yalnız kısıt.** Aksi hâlde yazılmış bulgunun `suppressed_by`
   sütununu güncellemek gerekirdi. `svc_integrity`'nin `integrity_findings`
   üzerinde UPDATE yetkisi yoktur ve verilmeyecektir (ADR-27, reddedilen
   alternatif B): bulgu tablosu adli bir kayıttır.
2. **Çakışma kümesi ölçüldü ve boştur.** Enjektör olay başına **tek** kural
   uygular (`switch rule`); bir olay ya kural 3 ya kural 5 ile bozulur, ikisi
   birden olmaz. Kural 3 ve kural 5'in precision'ı yapısal olarak %100
   olduğundan yanlış pozitif üzerinden çakışma da beklenmez.
3. **Sessiz kalmaz.** Motor, daha güçlü bir isabetin bastırıldığı durumu
   `Error` seviyesinde günlüğe geçirir ve sayar. Sayaç sıfırdan farklıysa bu bir
   ölçüm notudur, gizlenmez.

Faz **içinde** öncelik tam olarak uygulanır: akış fazında kural 1 kural 3'ü,
toplu fazda kural 5 kural 2'yi bastırır.

---

## Sonuçlar

**Ölçülen etki (Sprint 5 verisi).** Yerel destek atfı (ADR-29) temiz yanlış
pozitifleri 79→10 (kentsel) ve 177→16 (kırsal) indiriyor; öncelik kuralı buna ek
olarak kural 3 isabetlerini (12–13) kural 2'nin denominatöründen çıkarıyor.
Kural 2 precision'ı ~%80 (kentsel) / ~%90 (kırsal) seviyesine çıkıyor.

**Kural 1, 3, 5 etkilenmiyor.** Üçü de öncelik sırasının üstünde ve birbirinden
ayrık popülasyonları görüyor; ölçümde hiçbir çakışma yok. Öncelik mekanizması
onların %100 precision'ını değiştirmiyor, kural 2'yi koruyor.

**K7 kapanış ifadesi bu ADR'ye dayanıyor.** "Kural bazında precision" artık
"kanonik bulgular üzerinde, kanıt gücü önceliği uygulanmış precision" anlamına
gelir ve bu tanım ölçümden önce yazılıdır.

---

## Reddedilen alternatifler

**(A) Bastırma yok, tüm isabetler kanonik.** Ölçüldü: kural 2 precision'ı
%36–42. Reddedildi.

**(B) Bastırılan isabetler hiç yazılmaz.** Reddedildi: karışıklık matrisi
üretilemez ve "hangi kuralın hangi enjeksiyonu gördüğü" bilgisi kaybolur. O
bilgi bu çalışmanın en ilginç bulgularından biridir (kaydırılmış damga
kinematik anomali üretir).

**(C) F.5'i "herhangi bir enjeksiyon" precision'ına çevirmek.** Reddedildi:
F.5 plan BÖLÜM F'de yazılı ve K7 ona dayanıyor; ölçümden sonra tanım
değiştirmek BÖLÜM J disiplinini ihlal eder. "Herhangi bir enjeksiyon"
precision'ı **ek** metrik olarak raporlanır, K7'nin yerine geçmez.

**(D) Öncelik sırasını ölçülen precision'a göre belirlemek.** Reddedildi:
sıralamayı sonuca göre seçmek, etiketli veriye aşırı uydurmanın ta kendisidir.
Sıra kanıt tipinden türetilir ve ölçümden önce dondurulur.

---

## İlgili

ADR-09 (enjeksiyon, `injected_rule`) · ADR-27 (faz modeli, devir) ·
ADR-29 (kural 2 atfı) · ADR-30 (kural 4 kapsamı) · ADR-31 (veri modeli, F.5)
