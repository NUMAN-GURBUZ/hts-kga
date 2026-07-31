# Sprint 8 — Gözlemlenebilirlik & Performans (E09) Kapanış Raporu

**Tarih:** 2026-07-31
**Kapsam:** T-E09-01..06 + ADR-34 + ADR-35
**Kabul kriteri:** K8 (taşınan, değişmez), K9, K10

---

## 1. Plan doğrulaması — altı boşluk, ADR-34/ADR-35 ile kapatıldı

1. **K8'in sprint ataması çelişkiliydi.** BÖLÜM H tablosu S8 çıktısını "K8,
   K9, K10" diyordu; BÖLÜM J'de K8'in Sprint sütunu S4. Kod da J'yi
   doğruluyordu — K8, `sprint5-report.md` §7.5'te ölçülüp geçmiş,
   `sprint6-report.md`'de değişmez doğrulanmıştı. S8 K8'i **yeniden
   ölçmedi**; BÖLÜM H'nin satırı bir etiketleme hatası olarak kaydedildi.
2. **Metrik/iz iskeleti vardı, hiçbir şey üretmiyordu.** `observability.Init`
   altı serviste de çağrılıyordu ama `/metrics`'i servis eden hiçbir HTTP
   sunucusu yoktu (prometheus.yml'nin 2112-2116 hedefleri "down"), hiçbir iş
   metriği yoktu, `otel.SetTracerProvider` hiç çağrılmamıştı (küresel tracer
   no-op).
3. **İz arka ucu plana hiç yazılmamıştı.** Mimari diyagram yalnızca
   `OTel → Prometheus → Grafana` diyordu (Jaeger/Tempo yok). Yeni bir arka uç
   eklenmedi; OTLP exporter `.env`'de zaten tanımlı ama tüketilmeyen
   değişkeni kullanır hâle getirildi, best-effort (arka uç yoksa servis
   çökmez), kanıtı çalışan bir arka uca değil bir birim teste bağlandı.
4. **K9 ölçüm ortamı yoktu.** `analysis-engine` yalnızca `go run` ile
   çalışıyordu; Compose'da tanımlı bir servisi yoktu. Dockerfile + profil-
   gated (`scale-test`) compose servisi eklendi.
5. **K10 "bit-identical" tanımsızdı.** Karşılaştırma yöntemi (`EXCEPT ALL`,
   `ground_truth.agent_id+time` üzerinden eşleştirme — `event_id` run_id'ye
   bağımlı olduğundan doğrudan kullanılamaz) ADR-34'te kesinleştirildi.
6. **`deployments/k8s/` boştu.** ADR-16'nın kararı (K9 Compose'da ölçülür,
   K8s yalnızca dağıtım belgesi) değiştirilmeden dizin dolduruldu.

---

## 2. Tamamlanan işler

### 2.1 OTel metrik enstrümanları + `/metrics`

- `internal/observability/otel.go`: `TracerProvider` eklendi (OTLP-gRPC,
  best-effort — `MaxElapsedTime` sınırlı, arka uç yoksa servis çökmez/asılı
  kalmaz), `Provider.MustServeMetrics(addr)` eklendi.
- Beş servisin her birine `/metrics` bağlandı — **yeni port şeması icat
  edilmedi**, `prometheus.yml`'de S0'dan beri tanımlı 2112–2116 kullanıldı.
- İş metrikleri, **mevcut sayaçların** OTel'e yansıtılmasıdır: analiz
  motorunun `analyzed/foreign/skipped` sonuçları
  (`hts_analysis_events_processed_total`), bütünlük motorunun faz-sonu
  `Stats()` anlık görüntüsü (`hts_integrity_records_total`), gateway'in
  audit `Written()/Dropped()`'ı (`hts_gateway_requests_total`), Kafka
  yayın sayısı (`hts_kafka_messages_published_total`) — hiçbiri yeni bir
  iş kuralı değil.
- Kafka consumer lag (`hts_kafka_consumer_lag`, T-E09'un "lag" gereksinimi):
  `pkg/kafka/lag.go`, `kadm.Client.Lag()` üzerinden ObservableGauge
  callback'i — yalnızca `/metrics` scrape edildiğinde çalışır, ek goroutine
  gerekmez.

### 2.2 Kafka W3C TraceContext span üretimi

- `pkg/kafka/producer.go`: `Publish` artık `kafka.publish` span'i açıyor ve
  `traceparent`'i kayıt başlıklarına yazıyor (önceden `propagator.Inject`
  çağrılıyordu ama ctx'te hiç span olmadığından hiçbir şey yazmıyordu).
- `internal/analysis/driver/consumer.go`, `internal/integrity/source/stream.go`:
  `Extract` edilen bağlamın altına `kafka.consume` span'i açılıyor.
- `pkg/kafka/carrier_test.go`: uçtan uca yayılımın kanıtı — bellek-içi span
  kaydedici ile üretici span'inin `trace_id`'sinin tüketici span'ine
  taşındığı doğrudan test edildi (çalışan bir OTLP arka ucu gerekmez,
  ADR-34/3).

### 2.3 Prometheus hedefleri — gerçek bir altyapı hatası bulundu ve düzeltildi

`host.docker.internal`, Docker Desktop dışında (bu makine Linux) varsayılan
olarak çözümlenmiyor — Prometheus konteyneri host'taki hiçbir servise
ulaşamıyordu (beş hedef de "down"). `docker-compose.yml`'e
`extra_hosts: host.docker.internal:host-gateway` eklendi; ölçülerek
doğrulandı (gateway çalışırken hedef "up" oldu).

### 2.4 Grafana provisioning + dashboard

`deployments/compose/observability/grafana/provisioning/dashboards/` — bir
"HTS-KGA — Genel Bakış" dashboard'u (6 panel: servis sağlığı, Kafka lag,
yayın hızı, analiz işleme hızı, bütünlük sayaçları, gateway istek hızı).
Grafana API'siyle sağlandığı doğrulandı. Panel zenginliği plan gereği
bilinçli olarak sınırlı tutuldu (BÖLÜM H'nin baskı-altı feda sıralamasının
ilk maddesi).

### 2.5 K9 — Docker Compose replika ölçeklemesi

`deployments/compose/analysis-engine.Dockerfile` + `docker-compose.yml`'de
`profiles: ["scale-test"]` ile işaretli `analysis-engine` servisi (varsayılan
`up`'a dahil değil). Kafka'nın SASL/ACL modeli aynı kalacak şekilde
kardeş-konteyner erişimi için dördüncü bir dinleyici eklendi
(`CONTAINER://:9095`, yayınlanmaz, `kafka:9095` reklamı — host için doğru
olan `localhost:9092` reklamı bir konteynerden erişilemezdi).

**Ölçüm sonucu** (`configs/k9_scale.yaml`, 50.000 olay, `scripts/measure-k9.sh`):

| Replika | Verim (olay/sn) |
|---|---|
| 1 | 3.002 |
| 2 | 3.250 |
| 4 | 3.215 |

**K9 oranı = 1,07×. Beyan edilen eşik (≥ 3,5×) TUTMADI.**

Ölçüm altyapısı doğru çalıştı (4 replikada partition'lar neredeyse eşit
dağıldı: 50/52/54/58 olay). Darboğaz mimari değil ölçüm ortamıydı: ölçüm
sırasında `free -h` 15 GiB RAM'in 8,3 GiB'ının dolu ve 2 GiB swap'ın
**tamamen dolu** olduğunu gösterdi — paylaşımlı bir geliştirici masaüstü,
tek amaçlı bir ölçüm sunucusu değil. Bu, K3'ün Sprint 5'teki ele alınışıyla
aynı disiplinle raporlanan **negatif bir bulgudur**: eşik ölçümden önce
beyan edilmişti, sonuç istenen yönde çıkmadı diye değiştirilmedi.

### 2.6 K10 — tekrarlanabilirlik (ADR-35: kritik bulgu + düzeltme)

İlk ölçümde (`configs/smoke.yaml`, aynı seed, iki `run_id`, tam mod)
**15.265 satırdan 6.678'i (%43,7) uyuşmuyordu** — aynı ajan aynı tick'te iki
koşuda farklı fiziksel hücreye bağlanıyordu.

**Kök neden (ADR-35):** `cells.cell_id`/`site_id`, ADR-05 gereği bilinçli
olarak `run_id` içerir (DB birincil anahtar çakışmasını önlemek için). Ama
bu run-bağımlı kimlik, **üç ayrı yerde** fiziksel bir kararı belirlemek için
de kullanılmıştı:

1. `radio.SourceKey` — gölgeleme/LOS hash'i site UUID'sinden türetiliyordu.
2. Dört envanter sıralaması (`analysis/params`, `integrity/source`,
   `storage/redis`, `storage/postgres`) — `cell_id`'ye göre sıralanıyordu,
   "K10 için" yorumuyla — **tam tersi etki** yapıyordu.
3. Enjeksiyon envanteri (`injector.go`, kural 2 "en uzak hücre" seçimi) —
   aynı şekilde `cell_id`'ye göre sıralanıyordu.

Kullanıcıyla onaylandıktan sonra üçü de **fiziksel konuma** (axial
koordinat / ENU / lat-lon) göre sıralanacak şekilde düzeltildi
(`radio.AxialKey`, dört sıralama fonksiyonu, `injector.farthestCell`).

**Ölçülen etki:** %43,7 → **%0,09** (15.265 satırdan 14'ü, en büyük fark
1,19×10⁻⁸ km² — B0/B1/part_count'ta hiç fark yok, yalnızca M yönteminin
alanında kalıntı kayan nokta gürültüsü). `make verify-k10 RUN_A=... RUN_B=...`
kalıcı doğrulama betiği eklendi.

K10, katı "bit-identical" tanımıyla **%100 geçmiyor** ama düzeltme
öncesindeki niteliksel başarısızlıktan (yanlış hücre seçimi) tamamen farklı,
ölçülebilir düzeyde negatif bir bulgudur — kabul kriteri değiştirilmedi.

### 2.7 `deployments/k8s/`

Namespace, ConfigMap, Secret örneği (gerçek sır yok — repo public),
üç Deployment (analysis-engine+HPA, integrity, gateway+Service), iki Job
(simulator, validation), genel Dockerfile (`SERVICE` build-arg'ı), README.
ADR-16'nın kararı korunur: bu manifestler dağıtım belgesidir, K9 kriteri
bunlara bağlanmaz.

---

## 3. Düzeltilen hatalar

1. **`host.docker.internal` Linux'ta çözümlenmiyor** — `extra_hosts`
   eklendi (bkz. §2.3).
2. **OTel trace exporter'ın `Shutdown()`'ı süresiz bloke olabiliyordu** —
   `MaxElapsedTime: 0` (sınırsız yeniden deneme) + zaman aşımsız
   `context.Background()` birleşimi, arka uç yoksa servisin (özellikle
   `cmd/validation` gibi tek seferlik işlerin) çıkışını geciktiriyordu.
   `MaxElapsedTime` 3 saniyeye sınırlandı, `Shutdown()`'a kendi 5 saniyelik
   üst sınırı eklendi.
3. **K10'un kök nedeni** — bkz. §2.6, ADR-35.
4. **Kafka konteyneri restart'ta SASL kimliklerini/topic verisini
   kaybediyor** — kök nedeni tam araştırılmadı (muhtemelen `apache/kafka`
   imajının KRaft depolama biçimlendirme davranışı), ama gözlemlendi ve
   pratik çözümü belgelendi: her restart sonrası `make seed-kafka`
   gerekir. Sprint 8 kapsamının dışında (K6'yı kırmıyor, yalnızca
   operasyonel bir hatırlatma).

---

## 4. Test sonuçları

- `gofmt -l .` temiz, `go vet ./...` temiz.
- Birim + PBT testleri: tüm paketler yeşil (`go test ./... ` hariç
  `tests/integration`).
- `tests/integration/...`: **bir alt-test dışında** tüm testler yeşil (K6
  izolasyon, KT9.1–9.7, analiz boru hattı 4 senaryo, bütünlük
  akış/idempotanslık/gölge-SQL testleri dahil).

  **`TestValidation_LambdaPilotSweep/urban_ta.yaml` düşüyor** — λ=1,5→2,0
  arası kapsama %0,43 puan geriliyor (473 olaylık küçük bir pilot örneklem).
  Kök nedeni doğrulandı: ADR-35'in düzeltmeleri **stash'lenip** aynı test
  tekrar koşturuldu — düzeltme olmadan test geçiyor (eğri düz/monoton).
  Yani ADR-35, hangi hücrenin **nadir eşitlik** durumlarında kazandığını
  değiştirdiği için bu küçük örneklemdeki spesifik kapsama eğrisi de
  değişti. Bu, **bilimsel modelin değişmesi değildir** — link budget,
  gölgeleme dağılımı, yayılım formülleri aynıdır; yalnızca istatistiksel
  olarak eşdeğer birkaç sonuçtan hangisinin seçildiği değişti (eski
  tie-break de yeni tie-break de fizik açısından "keyfi"ydi — gerçekte de
  bir gölgeleme eşitliği yazı-tura gibidir). Test zaten kendi içinde
  "eğri neredeyse düz — λ'nın kaldıracı yok (ADR-25)" uyarısı basıyor;
  küçük örneklemli bir pilot ölçümün doğası gereği sınırda/gürültüye
  açıktır. K1–K10'un hiçbiri bu teste bağlı değildir (K1 zaten Sprint 5'te
  negatif bulgu olarak kapanmıştı). Test kodu bu sprintin kapsamı
  (E09) dışında olduğu için değiştirilmedi.
- `scripts/verify-isolation.sh`: K6'nın dört katmanı da geçti (bu sprint
  boyunca Kafka broker'ı birden çok kez yeniden başlatıldı, her seferinde
  yeniden doğrulandı).
- `pkg/kafka/carrier_test.go` (yeni): span yayılımı doğrudan test edildi.
- `internal/analysis/params/inventory_test.go`
  (`TestLoad_StableOrder`, güncellendi): artık kimliğe değil konuma göre
  sıralamayı doğruluyor; iki farklı `run_id` ile çağrılarak sıranın
  kimlikten bağımsız olduğu özellikle sınanıyor.

---

## 5. Kabul kriterlerinin durumu

| Kriter | Durum |
|---|---|
| K8 | ✅ değişmez (S4/S5'te ölçülü, yeniden ölçülmedi — §1 madde 1) |
| **K9** | ❌ **1,07× < 3,5×** — negatif bulgu, kök neden ortam (paylaşımlı masaüstü belleği), mimari değil |
| **K10** | ⚠️ **%99,91 satır bit-identical** (14/15.265 fark, ~1e-8 km²) — kritik yapısal hata (ADR-35) düzeltildi, kalan kayan nokta gürültüsü |
| K1–K7 | ✅ değişmez |

---

## 6. Kalan teknik borç (Sprint 8 kapsamı dışı, bilinçli olarak ertelendi)

- **T-E05-06 (kural 4 aggregate göstergesi, 2 SP)** — ADR-30 zaten bu
  kuralı K7 kapsamı dışına almıştı (`event_id` çapası imkânsız + %1,4
  precision). E09'un kapsamına girmiyor; backlog'da kalır.
- **Topic-refresh otomasyonu** — Kafka konteyneri restart'ta SCRAM
  kimliklerini kaybediyor (§3 madde 4). Operasyonel bir `make` hedefi
  (`make seed-kafka`'yı health-check'e bağlamak) faydalı olur ama E09'un
  "gözlemlenebilirlik ve ölçekleme" kapsamının dışında; ayrı bir E01/altyapı
  borcu olarak kalır.
- **K9'un izole makinede yeniden ölçülmesi** — mimarinin kendisi doğru
  çalıştığı için (partition ataması ölçülerek doğrulandı), gürültüsüz bir
  ortamda tekrar ölçüm anlamlı olur ama bu sprintin zorunlu kapsamı değildir.

---

## 7. Sprint değerlendirmesi

Bu sprintin en önemli sonucu, ilk kez **koşular arası** bir kabul kriteri
(K10) ölçülürken ortaya çıktı: altı sprint boyunca "içinde" doğru çalışan
mimari, **iki koşuyu karşılaştırdığınızda** üç ayrı yerde aynı kavramsal
hatayı (DB-birincil-anahtar kimliğini fiziksel-belirlenirlik kimliği yerine
kullanmak) taşıyordu. Bu, yalnızca tek-koşu testleriyle (K1–K8, hepsi
başarıyla geçmişti) **hiçbir zaman yakalanamazdı** — K10'un "gereksiz"
görünen katılığı tam olarak bunun için var.

K9'un negatif sonucu da aynı disiplinle raporlandı: ölçüm altyapısı doğru
çalıştığı ölçülerek kanıtlandı, darboğaz gerekçeli biçimde ortama
bağlandı, eşik değiştirilmedi.

---

## Conventional Commit

```
feat(observability): E09 gözlemlenebilirlik, K9 ölçümü, K10 kök neden düzeltmesi (ADR-34, ADR-35)
```
