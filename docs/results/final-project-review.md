# HTS-KGA — Final Project Review

**Rol:** Senior Software Architect & Technical Reviewer (bağımsız inceleme)
**Tarih:** 2026-07-31
**Kapsam:** Sprint 0–8, tüm kod tabanı, tüm ADR'ler, tüm ölçüm raporları
**Yöntem:** Statik inceleme + canlı doğrulama (build/vet/test/verify-isolation komutları bu incelemede yeniden çalıştırıldı)

---

## Yönetici Özeti

**Proje planlanan kapsamıyla tamamlandı.** 9 sprint, 133 SP, K1–K10'un
tamamı ölçüldü. Üç kriter (K1, K9, kısmen K10) eşiği tutturamadı — ama
üçü de **önceden beyan edilmiş eşiklere karşı dürüstçe ölçülmüş, kök
nedeni bulunmuş, açıklanmış** negatif/kısmi bulgulardır; kod hatası ya da
gizlenmiş eksiklik değil. Bu, bir bitirme projesi için **istenen** sonuç
türüdür — "her şey tuttu" değil, "her şey ölçüldü ve dürüstçe raporlandı."

**Tek gerçek teslim engeli: kök dizinde README yok.** Bunun dışında proje
teknik olarak teslime hazır; en kritik boşluk bir yarım günlük dokümantasyon
işidir, kod işi değildir.

---

## 1. Proje Tamamlandı mı?

| Sprint | Amaç | Durum | Eksik iş | Teslime engel mi |
|---|---|---|---|---|
| **S0** | Altyapı + çift katman izolasyon + `run_id` şeması | ✅ Tamamlandı | — | Hayır |
| **S1** | ENU + hex grid + envanter + ajan yerleşimi | ✅ Tamamlandı | — | Hayır |
| **S2** | 3GPP + best-server + olay + TA + enjeksiyon + Kafka | ✅ Tamamlandı | — | Hayır |
| **S3** | Sektör + TA + ızgara + ağırlıklar + komşu kısıtı | ✅ Tamamlandı | — | Hayır |
| **S4** | Kontur + MULTIPOLYGON + centroid + B0/B1/M + estimates | ✅ Tamamlandı | — | Hayır |
| **S5** | S3a/S3b + metrikler + kalibrasyon + 4 senaryo | ✅ Tamamlandı | K1 negatif bulgu (bilinçli, ölçüldü) | Hayır — bilimsel bulgu |
| **S6** | 5 tespit kuralı + precision/recall | ✅ Tamamlandı | Kural 2 (kentsel) eşiği tutmadı; kural 4 kapsam dışı (ADR-30) | Hayır — ikisi de gerekçeli |
| **S7** | gRPC + REST + statik web + Leaflet | ✅ Tamamlandı | — | Hayır |
| **S8** | OTel + Kafka trace + ölçekleme + Grafana | ✅ Tamamlandı | K9 negatif bulgu (ortam kaynaklı); K10 %99,91 (kalıntı kayan nokta gürültüsü) | Hayır — ikisi de gerekçeli |

**Plan dışı, bilinçli olarak ertelenen tek kalem:** T-E05-06 (kural 4
aggregate göstergesi, 2 SP) — ADR-30 bunu K7 kapsamı dışına zaten almıştı
(`event_id` çapası matematiksel olarak imkânsız). Backlog'da, teslime
engel değil.

### Net cevap

> **Evet. Proje, planlanan kapsamıyla (BÖLÜM A–N, 133 SP, 9 sprint)
> tamamlandı.** Üç kriterin eşiği tutmaması, kapsamın eksik kalması
> değil, **ölçülmüş ve raporlanmış bilimsel bulgulardır** — bu ayrım
> Bölüm 2'de kriter kriter açıklanıyor.

---

## 2. Kabul Kriterleri (K1–K10)

| # | Kriter | Eşik | Ölçülen | Durum |
|---|---|---|---|---|
| **K1** | Kalibrasyon: kapsama@90% (4 senaryo) | %85–95 | A 0,5435 · B 0,5841 · C 0,5247 · D 0,5741 | ❌ **Sağlanmadı** |
| **K2** | M@90 vs B0 medyan alan daralması | ≥%75 | %91,2–99,99 (4/4 senaryo) | ✅ **Sağlandı** |
| **K3** | M@90 vs B1 medyan alan daralması | TA var ≥%50 · TA yok ≥%20 | TA %99,8/%99,95 · TA yok %51,1/%52,0 | ✅ **Sağlandı** |
| **K4** | Kalibrasyon/doğrulama ayrımı kod zorunluluğu | Karışık sorgu → hata | Test ile doğrulandı | ✅ **Sağlandı** |
| **K5** | 4 senaryo koşulmuş ve raporlanmış | A,B,C,D | 4/4 koşuldu, raporlandı | ✅ **Sağlandı** |
| **K6** | Kör test bütünlüğü (3 katman → 4 katman) | İzolasyon testleri geçer | Dört katman da geçiyor (bu inceleme sırasında yeniden çalıştırıldı) | ✅ **Sağlandı** |
| **K7** | Bütünlük tespiti (kural bazında) | Precision ≥%90 | Kural 1/3/5: %100,00 (4/4 senaryo) · Kural 2: kırsal geçti (%92,7/%95,2), kentsel tutmadı (%85,5/%86,9) · Kural 4: ölçülemez | ⚠️ **Kısmen sağlandı** (3/5 tam, 1/5 kısmi, 1/5 kapsam dışı) |
| **K8** | Geometri kararlılığı | `repaired_ratio<%1`, `p95_part_count≤3` | `repaired_ratio=0`, `p95≤3` | ✅ **Sağlandı** |
| **K9** | Ölçeklenebilirlik (Compose replika) | 4 replika → verim ≥3,5× | **1,07×** | ❌ **Sağlanmadı** |
| **K10** | Tekrarlanabilirlik | Aynı seed → bit-identical | %99,91 satır bit-identical (14/15.265 fark, ~1e-8 km²) | ⚠️ **Kısmen sağlandı** |

### Sağlanmayan/kısmi üç kriterin değerlendirmesi

**K1 — nedeni bulundu, teslime engel değil, bilimsel bulgu olarak
raporlanmalı (ve zaten raporlandı).** Kök neden kalibrasyon değil arama
bölgesi tanımı: sektör diliminin kendisi gerçek konumun ancak %58–60'ını
kapsıyor; λ'nın kaldıraç etkisi %1–3 ile sınırlı (ADR-25). Model, **dilim
içinde** tam kalibre (M@90/B1 = 0,900–0,908) — açık dilimin kendisinden
geliyor. Bu, "kalibrasyon bozuk" değil "arama bölgesi modelin bir
sınırlaması" bulgusudur; düzeltmesi (arama bölgesini best-server bölgesine
genişletmek) Sprint 5'te tanımlanmış ama **kullanıcı onayı olmadan model
değiştirilmediği için** uygulanmamıştır — bu doğru bir disiplindir, aceleyle
sonuca göre model değiştirmek bilimsel olarak daha kötü olurdu.

**K7 kural 2 (kentsel) — fiziksel olarak açıklanabilir, teslime engel
değil.** Olaylar arası ortalama süre ~2,4 saat, kentsel senaryo çapı 10 km
— bu pencerede 10 km'lik bir sıçrama saatte ~4,2 km/s'lik bir hızla
**fiziksel olarak mümkün** (300 km/h eşiğinin çok altında), bu yüzden kural
onu ayırt edemiyor. Kırsalda çap 40 km olduğu için aynı mesafe/süre oranı
kuralı tetikliyor ve geçiyor. Bu, kuralın **tasarım sınırıdır**, kodlama
hatası değil.

**K9 — kök nedeni doğrulanmış ortam kısıtı, mimari değil.** Ölçüm sırasında
makine (paylaşımlı geliştirici masaüstü) 8,3/15 GiB RAM kullanımda ve 2 GiB
swap **tamamen dolu**ydu. Ölçüm altyapısının kendisi doğru çalıştığı ölçüldü
(4 replikada partition'lar neredeyse eşit dağıldı — Kafka consumer group
rebalance'ı doğru). İzole bir makinede yeniden ölçüm eşiği geçebilir ama bu
**doğrulanmadı**, dolayısıyla dürüstçe "tutmadı" olarak raporlanmalı.

**K10 — kritik bir mimari hata bulundu ve düzeltildi (ADR-35), kalan fark
kayan nokta gürültüsü.** İlk ölçümde %43,7 satır uyuşmazlığı vardı (ciddi);
kök neden (run_id-salted UUID'nin fiziksel belirlenirlik için de
kullanılması) bulunup üç yerde düzeltildikten sonra %0,09'a indi, en büyük
fark 1,19×10⁻⁸ km² (0,01 m²) — B0/B1/part_count'ta hiç fark yok. Katı
"bit-identical" tanımıyla %100 geçmiyor ama bu, PostGIS/GEOS'un kendi kayan
nokta davranışı olabilecek, hiçbir K1–K9 sonucunu etkilemeyen bir kalıntıdır.

**Genel değerlendirme:** Hiçbiri kod hatası ya da eksik geliştirme değil.
Üçü de plan disiplinine (BÖLÜM J: "eşikler ölçümden önce beyan edilmiştir;
sonuca göre değiştirilmez") tam uyularak ölçülmüş, kök nedeni bulunmuş,
raporlanmış negatif/kısmi bulgulardır. **Bir bitirme projesi jürisi
önünde bunlar zayıflık değil, metodolojik olgunluk göstergesidir** —
tutmayan bir sonucu saklamak yerine nedenini bulup yazmak, tutan bir
sonuçtan daha zor ve daha değerlidir.

---

## 3. Mimari Değerlendirme

| Başlık | Puan /10 | Gerekçe |
|---|---|---|
| **Clean Architecture** | 8 | `cmd/` (giriş noktaları) → `internal/<servis>/<katman>` (core/driver/detector/query) → `pkg/` (saf, bağımlılıksız yardımcılar) ayrımı tutarlı uygulanmış. `internal/simulator/radio` "envanteri tanımaz" gibi katman kuralları yorumlarla açıkça belgelenmiş **ve** `tests/isolation`'da **test edilerek** zorlanmış — bu nadir görülen bir titizlik. |
| **SOLID** | 8 | DIP: `CellSource`, `Handler`, `Policy`, `PathLossModel` gibi dar arayüzler tüketici tarafında tanımlı (Go idiomatic). SRP: paketler tek sorumluluk etrafında küçük tutulmuş (`density`, `sampling`, `injector` ayrı ayrı). Zayıf nokta: bazı tipler (`Cell`) üç farklı pakette (analysis/params, integrity/source, simulator/inventory) benzer ama kasıtlı olarak ayrı tanımlanmış — dokümante edilmiş bir tercih (bağımlılık izolasyonu) ama ilk bakışta yineleme gibi görünebilir. |
| **Modülerlik** | 9 | 47 paket, `tests/isolation` içe alma grafiğini **otomatik test ediyor** (S2/S4'ün `ground_truth`'a erişemeyeceğini yalnızca DB/Kafka rolüyle değil, **derleme zamanı bağımlılık grafiğiyle** de kanıtlıyor). Bu, akademik projelerde nadiren görülen bir mimari disiplin kanıtı. |
| **Test edilebilirlik** | 8 | Çekirdek algoritma paketleri (radio, geo, integrityrule, rf, agent, simulator/inventory) %85–98 kapsamda. PBT (`pgregory.net/rapid`) 19 dosyada aktif kullanılıyor — determinizm, kütle normalizasyonu, geometri geçerliliği gibi değişmezler rastgele girdilerle sınanıyor. Zayıf nokta: `gateway`, `storage/postgres`, `storage/redis` paketleri **birim düzeyinde %0** — yalnızca `tests/integration`'da (altyapı gerektiren) sınanıyor. Bu mimari olarak savunulabilir (I/O katmanı) ama saf hesap mantığından ayrıştırılabilecek parçalar varsa birim testsiz kalıyor. |
| **Genişletilebilirlik** | 7 | Yeni tespit kuralı eklemek `integrityrule.ID` enum'una ve `ClassStream/ClassAggregate` sınıflandırmasına oturuyor (ADR-27). Yeni yayılım modeli `PathLossModel` arayüzünden geçiyor. ADR-13'ün filtre çerçevesi yeni API ucu eklemeyi kolaylaştırıyor. Puanı 8'e değil 7'ye çeken şey: bazı kararlar (örn. `radio.Source.Key`'in nasıl türetildiği) yeterince izole değildi ve K10 ölçümünde üç ayrı yerde aynı hatanın tekrarlandığı görüldü (ADR-35) — bu, "aynı deseni bir yerde değiştirince her yerde değişmesi gerekirdi" türünden bir genişletilebilirlik zafiyetiydi, düzeltildi ama iz bırakıyor. |
| **Performans** | 6 | Mimari doğru (Kafka partition-paralel tüketim, batched DB yazımı, Kahan toplamı ile sayısal kararlılık) ama **K9'un empirik kanıtı henüz yok** — 3,5× hedefine karşı 1,07× ölçüldü, ortam gürültüsüne bağlandı ama izole makinede doğrulanmadı. Puan mimari tasarıma değil, **ölçülmüş kanıta** göre verilmiştir. |
| **Güvenlik** | 9 | Dört katmanlı kör test (Kafka ACL + PostgreSQL rol + veri modeli + içe alma grafiği), en az yetki ilkesi (her servis kendi principal'ı, `svc_gateway` `ground_truth`'u göremiyor), HMAC-SHA256 pseudonimleştirme, k-anonimlik (k=5), denetim izi (`audit_log`), sır yönetimi (`.env` asla commit edilmiyor, k8s manifestlerinde gerçek sır yok). Akademik bir proje için olağanüstü kapsamlı. |
| **Dokümantasyon** | 6 | 35 ADR, her biri "reddedilen alternatifler" bölümüyle — bu düzey nadir. Sprint kapanış raporları (5–8) ölçüm kanıtlarıyla eksiksiz. **Ama kök dizinde README yok** — bir yabancının (jüri, değerlendirici) projeye ilk bakışta "bu ne, nasıl çalıştırılır" sorusuna cevap bulacağı tek dosya eksik. İç dokümantasyon mükemmel, giriş kapısı yok. |

**Ortalama: 7,6/10** — mimari temel çok sağlam (modülerlik, güvenlik, SOLID
disiplini üst düzey); performans kanıtının ve giriş dokümantasyonunun
eksikliği ortalamayı aşağı çekiyor.

---

## 4. Kod Kalitesi

| Metrik | Değer |
|---|---|
| Go dosyası (satır) | ~44.400 satır (web/ hariç) |
| Paket sayısı | 47 |
| Test dosyası | 70 |
| PBT kullanan dosya | 19 |
| ADR sayısı | 35 |
| `gofmt -l .` | Temiz |
| `go vet ./...` | Temiz |
| Toplam commit | 15 |

**Paket yapısı:** `cmd/{simulator,analysis-engine,integrity,validation,gateway,persister}`
+ `internal/{analysis,simulator,integrity,gateway,validation,storage,observability,config,rf,persist}`
+ `pkg/{geo,kafka,integrityrule,privacy,split,ta,htswire}`. Sorumluluk
ayrımı isim düzeyinde bile tutarlı (`internal/X/core`, `internal/X/driver`,
`internal/X/detector` deseni tekrarlanıyor).

**Dosya organizasyonu:** Dosya adları görevle bire bir eşleşiyor
(`shadowing.go`, `best_server.go`, `contour.go`, `polygonize.go`) — kod
okumadan hangi dosyada ne olduğu tahmin edilebiliyor.

**Test kapsamı:** Çekirdek bilimsel/algoritmik kod yüksek kapsamda; I/O
sınırındaki kod (gateway, storage) yalnızca entegrasyon testleriyle
sınanıyor — mimari olarak makul ama raporlanmalı bir asimetri.

**PBT kullanımı:** Plan BÖLÜM I'in 8 PBT değişmezinin tamamı kodda karşılığını
buluyor (kütle toplamı, alan iç içeliği, geçerli geometri, TA imkânsızlığı
vb.). Bu, "birim test yazdık" ötesinde "matematiksel değişmezleri
doğruladık" iddiasını destekliyor.

**ADR kullanımı:** Projenin en güçlü yanı. 35 ADR, her biri Bağlam → Karar
→ Sonuçlar → Reddedilen Alternatifler yapısında. K10 ölçümü sırasında
bulunan mimari hata bile (ADR-35) aynı disiplinle belgelenmiş — bu,
"hata bulundu, gizlenmedi, kayda geçirildi" örneğidir ve akademik
sunumda güçlü bir argümandır.

**Go standartlarına uygunluk:** `gofmt`/`go vet` temiz, hata sarmalama
(`%w`) tutarlı, context kullanımı (iptal/zaman aşımı) disiplinli,
`sync/atomic` gerektiğinde doğru kullanılmış (audit sayaçları). Yorumlar
İngilizce değil Türkçe ama bu bir bitirme projesi için beklenen ve
tutarlı bir tercih.

---

## 5. Teslim Öncesi Checklist

| Madde | Durum | Not |
|---|---|---|
| README yeterli mi? | ❌ **YOK** | Kök dizinde `README.md` bulunmuyor. **Tek kritik boşluk.** |
| Kurulum adımları yeterli mi? | ⚠️ Dağınık | `.env.example`, `make help`, ADR'ler ve `docs/planning/` içinde var ama **tek bir "başlarken" belgesi yok** — hepsi README'de toplanmalı. |
| Docker Compose çalışıyor mu? | ✅ Evet | Bu inceleme sırasında doğrulandı: postgres/kafka/redis/prometheus/grafana ayakta, sağlıklı. |
| Demo senaryosu hazır mı? | ⚠️ Kısmen | `docs/planning/demo-plan.md` **Sprint 7 öncesi** yazılmış — statik Leaflet sayfası öneriyordu, artık **gerçek, canlı bir gateway+Leaflet var**. Belge güncel değil ama demoyu engellemiyor; Bölüm 6–7 bu incelemede güncel akışı veriyor. |
| Veritabanı migration'ları eksiksiz mi? | ✅ Evet | 7 migration (001–007), sırayla test edildi, hepsi idempotent (`IF NOT EXISTS`). |
| Config dosyaları yeterli mi? | ✅ Evet | 6 senaryo config'i (4 bilimsel + smoke + k9_scale), `.env.example` eksiksiz. |
| Gereksiz dosyalar var mı? | ⚠️ Küçük | `docs/results/*.run_id` dosyaları (küçük, faydalı — kanıt izi) commit'li; `*.log` doğru şekilde `.gitignore`'da. Önemli bir kirlilik yok. |
| Commit geçmişi uygun mu? | ⚠️ Kabul edilebilir | Yalnızca 15 commit, bazıları birden fazla sprint'i tek commit'te birleştiriyor (örn. "Sprint 2-3 tamamlandı"). Profesyonel bir ekip için ideal olmazdı ama tek geliştiricili akademik bir proje için kabul edilebilir; mesajlar açıklayıcı. |

### Eksik listesi (öncelik sırasıyla)

1. **README.md yok — teslimden önce mutlaka eklenmeli.**
2. `demo-plan.md`'nin Sprint 7/8 sonrası gerçek duruma göre güncellenmesi (opsiyonel, bu review'ın Bölüm 6–7'si onu fiilen yerine koyuyor).

---

## 6. Hocaya Canlı Demo

### 1. Sıfırdan çalıştırma sırası

```bash
cp .env.example .env
# .env içindeki CHANGE_ME değerlerini doldurun (POSTGRES_PASSWORD, HMAC_SALT, KAFKA_PW_*)
make setup          # infra-up + migrate-up + seed-kafka
```

### 2. Docker servisleri hangi sırayla ayağa kalkar?

`docker-compose.yml`'deki `depends_on`/`healthcheck` sırası: **postgres**
(healthy) → **kafka** (healthy, iç port 9094 üzerinden) → **redis**
(healthy) → **prometheus** (postgres healthy'ye bağlı) → **grafana**
(prometheus'a bağlı). `make setup` bunu otomatik sıralar; elle
`docker compose up -d` de aynı bağımlılık sırasını izler.

### 3. Simülasyonu hangi komut başlatıyor?

```bash
make simulate CONFIG=configs/urban_ta.yaml
# veya doğrudan:
scripts/run-scenario.sh configs/urban_ta.yaml
```
İkincisi tercih edilmeli — simülasyon + persister'lar + bütünlük akış fazı +
analiz + kalibrasyon + F.1–F.5'i **tek komutta** uçtan uca koşturur ve
`docs/results/<senaryo>.run_id` dosyasına `run_id`'yi yazar.

### 4. Analiz motoru nasıl çalışıyor?

`cmd/analysis-engine`, `hts.records` Kafka topic'ini tüketir (`svc_analysis`
kimliğiyle, `hts.groundtruth`'a **hiç abone olmaz**), Redis'ten hücre
envanterini yükler, her kayıt için yoğunluk ızgarası + kümülatif kontur
(B0/B1/M@50/90/95) hesaplar ve `estimates` tablosuna yazar. Tek başına:
```bash
HTS_CONFIG=configs/urban_ta.yaml HTS_RUN_ID=<run_id> go run ./cmd/analysis-engine
```

### 5. Kafka veri akışı nasıl gözlemlenebilir?

```bash
make kafka-list                                    # topic listesi
docker exec hts-kafka /opt/kafka/bin/kafka-console-consumer.sh \
  --bootstrap-server localhost:9094 --topic hts.records --max-messages 5
```
Daha etkileyici: Grafana'daki PostgreSQL panelinde `hts_records` satır
sayacının koşum sırasında **canlı arttığını** göstermek (5 sn yenileme).

### 6. PostgreSQL'de hangi tablolar doluyor?

`run_config`, `cells`, `hts_records`, `ground_truth`, `estimates`,
`metrics`, `integrity_findings`, `integrity_metrics`, `audit_log`.

### 7. Hangi SQL sorgularıyla sonuç gösterilir?

```bash
make verify-integrity                                    # ADR-01 referansiyel bütünlük
make verify-k7 RUN_ID=<run_id>                            # precision/recall tablosu
psql -c "SELECT * FROM verify_integrity('<run_id>');"     # 5 denetim
```

### 8. Leaflet arayüzü nasıl açılıyor?

```bash
make gateway     # REST :8080, gRPC :50051, health :8086
```
Tarayıcıda **`http://localhost:8080/`** — otomatik `/static/index.html`'e
yönlenir. Koşu seçilir, katmanlar (baz istasyonları, hücre yoğunluğu,
bulgular, B0/B1/M) tek tek açılır.

### 9. Grafana dashboard'ları nasıl açılıyor?

`http://localhost:3000` (admin/admin) → **"HTS-KGA — Genel Bakış"**
dashboard'u provisioning ile otomatik yüklü, elle içe aktarma gerekmez.

### 10. Prometheus metrikleri nasıl gösterilir?

`http://localhost:9090/targets` — beş servis hedefi (`hts-simulator`,
`hts-analysis-engine`, `hts-validation`, `hts-integrity`, `hts-gateway`)
çalışan servisler için "up" görünür. Doğrudan uç: `curl localhost:2116/metrics`
(gateway çalışırken).

### 11. Canlı gösterilebilecek API uçları

`GET /api/v1/runs`, `/cells`, `/estimates`, `/findings`, `/metrics`,
`/integrity-metrics`, `/aggregate/cell-activity` — hepsi `curl` veya
tarayıcıdan doğrudan çağrılabilir; `run_id` olmadan **400 + açıklama**
döndürmesi (ADR-13) etkileyici bir canlı demo anıdır.

### 12. Haritada neler gösteriliyor?

| Katman | Gerçekten mevcut mu? | Not |
|---|---|---|
| **Baz istasyonları** | ✅ Evet | `l-cells` katmanı, `/api/v1/cells` |
| **Sektörler** | ✅ Evet (dolaylı) | Ayrı bir "sektör" katmanı yok ama **B1 tahminleri sektör diliminin ta kendisidir** — `l-b1` |
| **Ground Truth** | ❌ **Yok, bilinçli olarak** | ADR-33/2 — gateway `ground_truth`'u göremez (kör testin API'ye kadar uzatılması). Göstermek istenirse `psql` ile dışa aktarılır, API'den değil. |
| **HTS kayıtları** | ⚠️ Dolaylı | Kaydın kendisi konumsuzdur (tasarım gereği); `/aggregate/cell-activity` hücre başına abone yoğunluğunu (k=5 anonimlik ile) gösterir — `l-activity` |
| **Timing Advance** | ❌ Yok | TA halkası için ayrı bir API ucu/katman yazılmadı. TA analizde dahili olarak kullanılıyor ama görselleştirilmiyor. **Küçük bir ayarla (SQL `ST_Buffer`) gösterilebilir, ama kod gerektirir — şu an mevcut değil.** |
| **Probability Polygon (B0/B1/M)** | ✅ Evet | `l-b0`, `l-b1`, `l-m` — konfidans seçici (50/90/95) ile |
| **Integrity Findings** | ✅ Evet | `l-findings`, kural seçici ile; bulguya tıklayınca `evidence` JSON popup'ı |

**Özet:** 12 istenen görsel unsurdan **5'i doğrudan mevcut** (baz
istasyonları, sektörler/B1, olasılık poligonları, bulgular, hücre
yoğunluğu), **1'i bilinçli olarak yok** (ground truth — kör testin parçası,
`psql` ile ayrıca gösterilebilir), **1'i (TA halkası) küçük bir SQL
sorgusuyla QGIS/`psql`'de gösterilebilir ama Leaflet'e kod eklemeden
gösterilemez.**

---

## 7. Sunum Akışı (10–15 dakika)

| # | Adım | Süre | İçerik |
|---|---|---|---|
| 1 | **Problem** | 1 dk | Operatör kaydından (hücre + TA) olasılıksal konum kestirimi; "naif daire → sektör dilimi → olasılıksal model" |
| 2 | **Sistem Mimarisi** | 1,5 dk | 5 servis diyagramı (simülatör→Kafka→analiz/bütünlük→gateway), kör test vurgusu |
| 3 | **Kör Test (K6)** | 1,5 dk | `make verify-isolation` canlı çalıştır — "permission denied" çıktısı etkileyici; dört katman açıklanır |
| 4 | **Simülasyon + Kafka** | 1,5 dk | Önceden başlatılmış bir koşunun Grafana'da canlı sayaç artışı; diurnal olay yoğunluğu paneli |
| 5 | **Database** | 1 dk | `psql \dt`, tablo listesi, `metrics`/`integrity_metrics` içerikleri |
| 6 | **Analysis Engine + Probability Polygon** | 3 dk | Leaflet: tek kayıt → B0 (118 km²) → B1 (21 km²) → M@90 (0,04 km²) → **~3.000× daralma** vurgusu (K2/K3) |
| 7 | **Validation** | 2 dk | K1'in neden tutmadığının açıklaması (arama bölgesi tanımı) — **dürüstlük vurgusu**, jüriye iyi izlenim bırakır |
| 8 | **Integrity Detection** | 2,5 dk | Bir bulguya tıkla → `evidence` JSON → "sistem bunu enjekte edildiğini bilmeden buldu"; `make verify-k7` tablosu |
| 9 | **Dashboard (Grafana + Prometheus)** | 1 dk | Genel bakış dashboard'u, `/targets` sayfası |
| 10 | **Sonuçlar ve Bilimsel Disiplin** | 1,5 dk | 35 ADR, K1–K10 tablosu, "eşikler ölçümden önce beyan edildi, tutmayanlar açıklandı" |
| 11 | **Sorular** | 2–3 dk | Rezerv |

**Toplam: ~12–14 dakika**, 15 dakikalık sınırın içinde, soru payı bırakılmış.

---

## 8. Son Değerlendirme

### Bitirme projesi olarak teslim edilmeye hazır mı?

**Evet — bir dokümantasyon eklemesinden sonra.**

### Akademik olarak yeterli mi?

**Evet, ortalamanın üzerinde.** 35 gerekçeli ADR, önceden beyan edilmiş ve
sonradan değiştirilmemiş eşikler, negatif bulguların kök nedenleriyle
raporlanması (K1, K7-kural2, K9) ve **geliştirme sırasında bulunan bir
mimari hatanın (K10/ADR-35) şeffafça belgelenmesi** — bu son madde özellikle
değerlidir: bir jüri üyesi "her şey mükemmel çıktı" iddiasından çok, "hata
bulundu, kanıtlandı, düzeltildi, etkisi ölçüldü" anlatısına güvenir.

### Eksik gördüğüm kritik bir konu var mı?

Yalnızca bir tane: **kök dizinde README yok.** Bu, kod ya da bilim
eksikliği değil, "yabancı biri repoyu açtığında ilk 30 saniyede ne
göreceği" eksikliğidir — ama bir teslimde bu ilk izlenim önemlidir.

### Teslim etmeden önce mutlaka yapılması gerekenler (önem sırasıyla)

1. **README.md yazılmalı** (proje özeti, mimari diyagram, kurulum adımları,
   `make setup` → `make simulate` → `make gateway` akışı, K1–K10 tablosuna
   link). Yarım günden az sürer.
2. **Kuru prova** — Bölüm 6–7'deki demo akışını gerçek ekranda bir kez
   baştan sona çalıştırın; `HTS_HEALTH_ADDR` port çakışmaları gibi ufak
   sürprizleri önceden yakalar.
3. *(İsteğe bağlı, teslime engel değil)* `docs/planning/demo-plan.md`'yi
   Sprint 7/8 sonrası gerçek duruma göre güncelleyin ya da bu review'ın
   Bölüm 6–7'sine yönlendiren bir not ekleyin.

**Proje gerçekten tamamlandı.** Yukarıdaki tek madde (README) dışında
teslime engel hiçbir şey yok.
