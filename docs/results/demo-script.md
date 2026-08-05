# HTS-KGA — Final Savunma Script'i

**Bu belge, önceki demo script'inin yerine geçen nihai sürümdür.** Tüm
rakamlar bu oturumda, şu an ekranınızda açık olan koşudan (`run_id =
1928cae9-9d25-4898-b5e5-1177de2b4fac`) doğrudan sorgulanarak alındı —
uydurma değil.

---

## 1. Sunuma Başlamadan Önce Hangi Komutları Çalıştırmalıyım?

Sırayla, sunumdan **~10 dakika önce**:

```bash
# 1. Eski süreç kalmış mı kontrol edin (bugün başınıza gelen hata buradan geldi)
pgrep -f "cmd/gateway"          # bir şey listelerse: kill <pid>

# 2. Altyapı + veri — Kafka konteyneri yeniden başladıysa SASL kimliklerini
#    kaybeder (bilinen bir durum); bu komut hem onu hem migration'ı hem de
#    örnek senaryoyu tek seferde tazeler
make demo

# 3. Gateway — AYRI bir terminalde, açık bırakın
make gateway

# 4. Kontrol
curl -s -o /dev/null -w "%{http_code}\n" localhost:8080/     # 302 dönmeli
```

**Neden `pgrep` ile başlıyoruz:** Bugün tam olarak bunu yaşadınız —
`:8086` portunu tutan eski bir süreç yüzünden `make gateway` "address
already in use" ile düştü. Sunum sırasında bu hatayı görmemek için
önceden temizleyin.

**Tarayıcıda önceden açık bulundurun (üç sekme):**
`http://localhost:8080/` · `http://localhost:3000` (Grafana,
admin/admin) · `http://localhost:9090/targets`

**Kâğıda/notlara önceden yazın** (canlı aramayın):
```
run_id  : 1928cae9-9d25-4898-b5e5-1177de2b4fac
abone   : 04207e79af57b5f550025c7d586b1dd03faf0d4de37927e64f57ac85142dceb9
```

---

## 2. `make demo` Tam Olarak Ne Yapıyor?

Tek komut, beş adım (bu oturumda ölçülen gerçek süre: **~1-2 dakika**):

1. `.env` yoksa `.env.example`'dan oluşturur, `HMAC_SALT`'ı otomatik üretir.
2. Beş Docker servisini (PostgreSQL+PostGIS+TimescaleDB, Kafka, Redis,
   Prometheus, Grafana) başlatır ve **gerçekten sağlıklı olana kadar
   bekler** (`docker compose up --wait`).
3. Veritabanı migration'larını uygular (7 dosya, idempotent — ikinci kez
   çalıştırmak zararsız).
4. Kafka topic'lerini, SASL/SCRAM kimliklerini, ACL'leri kurar.
5. `configs/smoke.yaml` ile uçtan uca bir senaryo koşturur: simülasyon
   (100 ajan, 3 gün) → Kafka → kalıcılaştırma + bütünlük akış fazı →
   bütünlük toplu fazı → analiz motoru (B0/B1/M kestirimi) → doğrulama
   (F.1–F.5) → `verify_integrity` (5 denetim).

Sonunda `run_id`'yi ve bir sonraki adımı (`make gateway`) ekrana yazar.

---

## 3. Gateway Nasıl Başlatılır?

```bash
make gateway
```

**Ayrı bir terminalde**, ön planda çalışır (kapatmak için o terminalde
`Ctrl+C`). Üç uç açar:

| Uç | Port | Ne için |
|---|---|---|
| REST + statik web | `:8080` | Leaflet sayfası + API |
| gRPC | `:50051` | İç servisler (bu demoda gösterilmiyor) |
| Health/metrics | `:8086` / `:2116` | Prometheus scrape |

**Sık hata:** Zaten çalışan bir gateway varken ikinci kez başlatmaya
çalışmak `"address already in use"` verir — bu bir kod hatası değil,
Bölüm 1'deki `pgrep` kontrolüyle önlenir.

---

## 4. Tarayıcıda Hangi Adres Açılır?

**`http://localhost:8080/`** — otomatik olarak `/static/index.html`'e
yönlenir (HTTP 302, normal). Bu, projenin **tek** kullanıcı arayüzüdür.

---

## 5. Ekrandaki Her Alan Neyi Temsil Ediyor?

Ekran görüntünüzdeki sol panelden yukarıdan aşağıya:

| Alan | Ne temsil ediyor |
|---|---|
| **"Koşu" açılır menüsü** | Veritabanındaki koşuların listesi (`scenario · run_id kısaltması · olay sayısı`). Seçmeden hiçbir katman yüklenmez. |
| **"Baz istasyonları"** (mavi noktalar) | `cells` tablosu — bu koşunun 327 sektörünün gerçek konumu. Şebekenin kendisi. |
| **"Hücre yoğunluğu" (k=5 rozetli)** | Hücre başına kaç **farklı abone** görüldüğü — ama 5'ten az abonesi olan hücreler **gizleniyor** (k-anonimlik, ADR-15). Gizlilik ilkesinin haritada görünür kanıtı. |
| **"Bütünlük bulguları" + kural seçici** (kırmızı noktalar) | **Tahmin DEĞİL.** Bütünlük denetiminin şüpheli/manipüle edilmiş bulduğu kayıtlar, o kaydı üreten baz istasyonunun konumunda işaretleniyor. Kural seçiciyle (1/2/3/5) hangi tür manipülasyon filtrelenir. |
| **"Abone" kutusu** | B0/B1/M katmanları için zorunlu tek-abone filtresi (ADR-13) — bir kişinin tüm geçmişini tek seferde göstermemek için bilinçli bir kısıt. |
| **B0 (turuncu daire)** | Naif tahmin — hücrenin **tüm** kapsama alanı. |
| **B1 (yeşil dilim)** | Sektör anteninin yönü de hesaba katılmış tahmin. |
| **M@90 (ince şerit — yakınlaşmadan görünmeyebilir)** | 3GPP yayılım + TA + komşu hücre kısıtlarıyla üretilen olasılıksal tahmin. **Bu koşuda B0'ın ~1/50'sinden küçük** — haritada görmek için o bölgeye yakınlaşmanız gerekebilir. |
| **"Ölçümler" paneli** | Seçili koşunun K7 (bütünlük precision/recall, kural başına) ve K1-K3 (kalibrasyon/daralma) sayıları — canlı SQL sorgusunun ekrana yansıması. |
| **Alt durum çubuğu** | Son işlemin sonucu ("bulgular: 15 geometri" gibi) — sessiz hata yok, her katman yükleme sonucu burada görünür. |

**Kısaca:** mavi = altyapı, turuncu/yeşil/(görünmeyen ince şerit) = konum
**tahmini**, kırmızı = şüpheli **kayıt** (konum tahmini değil, adli bulgu).

---

## 6. Hocaya Hangi Sırayla Göstermeliyim?

1. Problem (ekran yok)
2. Mimari diyagram (README)
3. Docker durumu
4. Kör test (canlı terminal)
5. Kafka canlı akış (Grafana)
6. Veritabanı (psql)
7. Leaflet — B0 → B1 → M (bilimsel iddia)
8. K1'in dürüst açıklaması
9. Bütünlük bulguları + `evidence` JSON
10. K7 tablosu
11. Grafana/Prometheus kapanışı
12. Sözlü kapanış (Bölüm 10)

---

## 7. Ekran Başına Anlatım Metni (≤1 dakika)

### Ekran 1 — Problem (ekran yok)
> "Bir operatör kaydı yalnızca 'bu abone şu baz istasyonuna bağlıydı'
> der. Buradan 'tam olarak nerede' sorusuna geçmek olasılıksal bir
> problem. Bugün bunu üç yöntemle çözüp her birini ölçtüğümüz bir
> sistemi göstereceğim."

### Ekran 2 — Mimari (README diyagramı)
> "Beş servis, aralarında Kafka. Kritik nokta: gerçek konumu taşıyan
> Kafka topic'ine analiz ve bütünlük servislerinin **erişimi yok** —
> birazdan canlı kanıtlayacağım."

### Ekran 3 — Docker
```bash
docker compose -f deployments/compose/docker-compose.yml ps
```
> "Beş altyapı servisi: PostgreSQL+PostGIS+TimescaleDB, Kafka, Redis,
> Prometheus, Grafana — hepsi sağlıklı."

### Ekran 4 — Kör test
```bash
make verify-isolation
```
> "`permission denied` ve `TOPIC_AUTHORIZATION_FAILED` satırlarına
> bakın. Bu bir iddia değil, dört bağımsız katmanda otomatik test
> edilen bir kısıt: veritabanı rolü, Kafka yetkisi, veri modeli ve kod
> seviyesinde bir bağımlılık testi."

### Ekran 5 — Kafka canlı akış
```bash
bash scripts/run-scenario.sh configs/smoke.yaml &
```
Hemen Grafana'ya geçin, "Kafka mesaj yayın hızı" ve "Analiz motoru işleme
hızı" panellerini gösterin.
> "Arka planda gerçek bir koşu başladı. Simülatör olayları Kafka'ya
> yazdıkça, analiz motoru aynı anda tüketiyor — bu panel canlı hareket
> ediyor."

### Ekran 6 — Veritabanı
```bash
psql -h localhost -U hts_admin -d hts_kga -c "\dt public.*"
```
> "Sekiz tablo. Her koşu `run_id` ile izole — koşular birbirine
> karışmıyor, bu tekrarlanabilirlik ölçümünün ön koşulu."

### Ekran 7 — Leaflet: B0 → B1 → M (en önemli ekran, ~1,5 dk)
Koşuyu seçin → abone kutusuna yapıştırın → sırayla B0, B1, M@90'ı açın.
> "B0: hücrenin tüm kapsama alanı, 95 km². B1: sektör bilgisini
> eklersek 17 km². M@90: 3GPP yayılım modeli + timing advance ile
> **0,017 km²** — B0'a göre **yaklaşık 5.500 kat daralma**, ve gerçek
> konum hâlâ bu şeridin içinde."

### Ekran 8 — Bilimsel dürüstlük
> "Modelin '%90 güvenle içindedir' iddiasını da ayrıca ölçtük — bu
> koşuda kapsama %53 çıktı, hedefimiz %85-95'ti, **bu kriter tutmadı**.
> Nedenini bulduk: sorun kalibrasyon değil, aradığımız bölgenin tanımı
> — sektör dilimi gerçek konumun ancak %58-60'ını kapsıyor. Bunu ölçüp
> yazdık, sonucu değiştirmedik."

### Ekran 9 — Bütünlük bulguları
Kural 3'ü seçin, kırmızı bir noktaya tıklayın.
> "Bu kaydın zaman damgası, aynı abonenin önceki kaydından mantıksız
> şekilde geriye gidiyor. Sistem bunu, kaydın manipüle edildiğini
> **bilmeden** buldu — az önce gösterdiğim kör test yüzünden."

### Ekran 10 — K7 tablosu
Sol paneldeki "Ölçümler" veya:
```bash
make verify-k7 RUN_ID=1928cae9-9d25-4898-b5e5-1177de2b4fac
```
> "Üç kural %100 kesinlikte. Kural 2 bu küçük örneklemde bir yanlış
> pozitif verdi — tam ölçekli koşularımızda (298.000 kayıt) kırsalda
> %92,7 ile geçiyor, yalnızca kentselde, fiziksel bir sınır yüzünden
> tutmuyor."

### Ekran 11 — Grafana/Prometheus kapanışı
> "Beş servisin metrikleri OpenTelemetry ile toplanıyor — sistem
> üretim ortamında izlenebilir."

### Ekran 12 — Sözlü kapanış
Bölüm 10'daki özeti kullanın.

---

## 8. Hocanın Sorabileceği Sorular ve Cevaplar

**S: Neden Kafka kullandınız, doğrudan veritabanına yazsanız olmaz mıydı?**
C: Üç bağımsız tüketici (analiz, bütünlük akışı, kalıcılaştırma) aynı
veriyi birbirini bloklamadan paralel işliyor. Ayrıca Kafka ACL'i kör
testin ilk savunma hattı — mesajlaşma seviyesinde bile kim neyi
okuyabilir kısıtlanıyor.

**S: Kör test gerçekten garanti mi, yoksa siz mi "erişmiyoruz" diyorsunuz?**
C: Dört bağımsız, otomatik test edilen katman: Kafka ACL (broker
reddediyor), PostgreSQL rolü (yetki yok), veri modeli (etiket yalnızca
`ground_truth`'ta), ve kod seviyesinde bir içe alma grafiği testi (analiz
servisinin simülatör paketini derleme zamanında import edemeyeceği
kanıtlanıyor). Dördü de her build'de otomatik çalışıyor.

**S: K1 neden tutmadı, düzeltilebilir miydi?**
C: Kalibrasyon değil arama-bölgesi tanımı sorunu — sektör dilimi gerçek
konumun ancak %58-60'ını kapsıyor. Düzeltmesi biliniyor (arama bölgesini
genişletmek) ama ölçüm sonucuna göre modeli değiştirmek bilimsel
disiplini bozardı; bulgu olarak raporladık.

**S: K9 (ölçeklenebilirlik) neden tutmadı?**
C: 4 replikada ölçülen verim artışı 1,07× (hedef ≥3,5×). Kök nedeni ölçüm
sırasında makinenin (paylaşımlı geliştirici masaüstü) belleğinin dolu
olmasıydı — mimarinin kendisinin doğru çalıştığını ayrıca ölçtük:
partition'lar 4 replika arasında neredeyse eşit dağıldı.

**S: K10 (tekrarlanabilirlik) tam sağlanmıyor, ciddi bir sorun değil mi?**
C: Bu ölçüm sırasında gerçek bir mimari hata bulduk: bir veritabanı
kimliğinin, aynı zamanda fiziksel hesaplamalar için de kullanıldığını
tespit ettik. Düzeltmeden önce %43,7 fark vardı; kök nedeniyle düzelttik,
şimdi %0,09 (kalıntı kayan nokta gürültüsü, 0,01 m² mertebesinde).

**S: Neden Go dilini seçtiniz?**
C: Goroutine'lerle doğal eşzamanlı Kafka tüketimi, statik tip güvenliği,
Kafka/gRPC/PostGIS istemcileri için olgun kütüphane desteği.

**S: Gizlilik/KVKK için ne yaptınız?**
C: Abone kimlikleri HMAC-SHA256 ile pseudonimleştiriliyor (ham numara
hiçbir yerde saklanmıyor), toplu uçlarda k-anonimlik (k=5) uygulanıyor,
her API isteği denetim izine yazılıyor.

**S: Test kapsamınız nedir?**
C: 70 test dosyası; çekirdek algoritma paketlerinde %85-98 satır
kapsamı; 19 dosyada property-based test — rastgele girdilerle
matematiksel değişmezler (kütle toplamı=1, geometri iç içeliği vb.)
sınanıyor.

**S: 35 mimari karardan en önemlisi hangisi?**
C: K10 ölçümü sırasında bulduğumuz mimari hatayı belgeleyen karar —
hem bulgu hem düzeltme süreci, projenin genel disiplininin küçük bir
örneği.

---

## 9. Sunum Sırasında Yapılmaması Gereken Hatalar

1. **Tam ölçekli senaryoyu (298.000 olay, ~15-30 dk) canlı çalıştırmaya
   kalkışmayın** — `smoke.yaml` zaten hazır, sonuçlar aynı disiplinle
   ölçülmüş.
2. **Gateway'i başlatmadan önce eski süreç kontrolünü atlamayın** —
   bugün tam olarak bu yüzden hata aldınız (Bölüm 1).
3. **Negatif bulguları (K1, K9, K10) gizlemeyin veya savunmacı bir dille
   sunmayın.** Kendinizden önce siz söyleyin, güvenle: "bu kriter
   tutmadı, nedenini bulduk" — bu bir zayıflık değil, olgunluk kanıtı.
4. **Abone kimliğini veya `run_id`'yi canlı SQL ile aramayın** — önceden
   not alın (Bölüm 1).
5. **Kafka'nın SASL kimliklerini kaybetmiş olabileceğini unutmayın** —
   konteyner yeniden başladıysa `make demo`/`make setup`'ı sunumdan
   hemen önce bir kez çalıştırın.
6. **Kod satırlarına dalıp süre kaybetmeyin** — yüksek seviyede kalın,
   "isterseniz koda bakabiliriz" deyin, sormadıkça açmayın.
7. **Bilmediğiniz bir soruya "bilmiyorum" demeyin** — "bunu ölçmedik,
   iyi bir gelecek çalışma konusu" gibi çerçeveleyin; proje zaten bu
   dille yazıldı.
8. **Aynı anda çok fazla katman açıp haritayı karmaşıklaştırmayın** —
   B0 → B1 → M sırasını koruyun, tek tek açın.
9. **Tarayıcı sekmelerini/terminal düzenini son dakikada hazırlamayın** —
   Bölüm 1'i sunumdan 10 dakika önce bitirin.

---

## 10. Kapanışta Teknik ve Bilimsel Katkı Özeti

**Teknik katkı** (~30 saniye):
> "Beş mikroservisli, olay güdümlü bir mimari kurduk. En önemlisi: kör
> test tasarımını dört bağımsız katmanda **otomatik testle** garanti
> altına aldık — bu yalnızca bir tasarım tercihi değil, her build'de
> doğrulanan bir kısıt. Kafka, PostGIS, TimescaleDB, OpenTelemetry,
> Prometheus, Grafana'yı uçtan uca entegre ettik. 35 mimari kararın
> her biri gerekçeli ve reddedilen alternatifleriyle kayıtlı — bunlardan
> biri, geliştirme sırasında bulduğumuz gerçek bir hatayı belgeliyor."

**Bilimsel katkı** (~30 saniye):
> "Olasılıksal konum kestirimi, sektör diliminden ölçülen ~%99,8 daha
> küçük bir alana daralıyor. 3GPP yayılım modelimizi literatürle çapraz
> doğruladık. Kalibrasyonun neden tutmadığını izole ettik — kök neden
> arama bölgesi tanımı, model hatası değil. Beş kurallı bütünlük
> tespitinin precision/recall'unu Wilson güven aralığıyla ölçtük. En
> önemlisi: on kabul kriterinden tutmayan üçünün **hiçbiri gizlenmedi**
> — hepsi kök nedeniyle birlikte, ölçümden önce beyan edilmiş eşiklere
> karşı dürüstçe raporlandı. Bilimsel yöntemde bu, tutan bir sonuçtan
> daha değerlidir."

**Son cümle:**
> "Sorularınızı alabilirim."
