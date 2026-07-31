# ADR-34 — Sprint 8 Gözlemlenebilirlik Mimarisi, K9/K10 Ölçüm Metodolojisi

**Durum:** Kabul edildi
**Tarih:** 2026-07-30
**Sprint:** 8 (E09 Gözlemlenebilirlik & Performans)
**İlgili bulgular:** ADR-15, ADR-16, BÖLÜM H (S8 → K8, K9, K10), BÖLÜM J, O-05

---

## Bağlam

Sprint 8'e başlamadan önce plan ve mevcut kod tarandı. Beş boşluk bulundu;
biri gerçek bir plan çelişkisi, dördü kod yazmayı bloke eden tanımsızlıklar.
Hiçbiri K8/K9/K10'un eşiğini değiştirmiyor — yalnızca nasıl ölçüleceğini
tanımlıyor.

### Boşluk 1 — K8'in sprint ataması çelişkili

BÖLÜM H tablosu S8'in çıktısını "K8, K9, K10" diye veriyor. Ama BÖLÜM J'de
K8 satırının Sprint sütunu **S4**. Kod da J'yi doğruluyor: `ADR-21`
(Go-side poligonizasyon) S4'te teslim edildi ve K8, `sprint5-report.md`
§7.5'te ADR-26 çözünürlükleriyle ölçülüp geçti (`repaired_ratio = 0`,
`p95_part_count ≤ 3`), `sprint6-report.md` bunu değişmez olarak yeniden
doğruladı.

**Karar.** K8, S4/S5'te zaten ölçülmüştür ve S6'da regresyon yoktur.
Sprint 8, geometri koduna dokunmadığı için K8'i **yeniden ölçmez**;
kapanış raporunda "değişmez, önceki ölçüm geçerli" olarak taşınır. BÖLÜM
H'nin S8 satırı bir etiketleme hatasıdır (S4'te zaten üretilen bir sonucu
S8'e de yazmış); K8'in **eşiği** ya da **anlamı** değişmez, yalnızca bu
ADR hatayı kayda geçirir.

### Boşluk 2 — Metrik/iz iskeleti var, hiçbir şey üretmiyor

`internal/observability/otel.go` bir `MeterProvider` kurar
(Prometheus exporter ile) ve `Tracer()` yardımcı işlevini dışa verir.
Altı `cmd/*/main.go` da `observability.Init` çağırıyor. Ama:

- Hiçbir HTTP sunucusu `/metrics`'i servis etmiyor — `prometheus.yml`
  zaten `hts-simulator:2112` … `hts-gateway:2116` hedeflerini tanımlamış
  (T-E01 sırasında stub olarak), ama o portlarda dinleyen hiçbir şey yok.
  Hedefler `prometheus` konteynerinde "down" görünür.
- Hiçbir iş metriği (sayaç/histogram) hiçbir yerde oluşturulmamış.
- `otel.SetTracerProvider` hiç çağrılmamış — küresel tracer no-op'tur.
  `pkg/kafka/producer.go` `propagator.Inject` çağırıyor ve
  `internal/analysis/driver/consumer.go` ile
  `internal/integrity/source/stream.go` `Propagator().Extract` çağırıyor,
  ama üretilen hiçbir span olmadığından bu şu an **plumbing var, iz yok**
  durumudur.

**Karar.** `internal/observability/otel.go`'ya bir `TracerProvider`
eklenir (karar 2 detayları aşağıda) ve `Provider`'a `/metrics`'i servis
eden bir `MustServeMetrics(addr)` metodu eklenir. Portlar **değişmez** —
zaten `prometheus.yml`'de tanımlı 2112–2116 kullanılır, böylece
provisioning dosyasına dokunulmaz.

İş metrikleri, her servisin zaten saydığı sayaçların (analysis-engine'in
`analyzed`/`failed`, integrity'nin `inspected/written/suppressed`,
gateway'in audit `Written()/Dropped()`) OTel enstrümanlarına
**yansıtılmasıdır** — yeni bir iş kuralı icat edilmez, mevcut sayaçlar
görünür kılınır.

### Boşluk 3 — İz (trace) arka ucu plana yazılmamış

Mimari diyagram (BÖLÜM C.2) `OTel → Prometheus → Grafana` diyor —
Jaeger/Tempo/OTel-Collector **hiçbir yerde yok**. `.env.example` bir
`OTEL_EXPORTER_OTLP_ENDPOINT` taşıyor ama onu tüketen hiçbir kod yok ve
compose'da o adrese cevap verecek bir servis yok. Ayrıca geliştirme
makinesi tek kişilik ve kaynak sınırlı (ROS2/PX4 ile paylaşılıyor).

**Karar.** Yeni bir iz arka ucu (Jaeger/Tempo/Collector) **eklenmez** —
plan mimarisi bunu hiç önermemiş, eklemek kapsam dışı bir altyapı
kararı olurdu. Bunun yerine:

1. `TracerProvider`, OTLP-gRPC exporter'ı `OTEL_EXPORTER_OTLP_ENDPOINT`
   değişkenine bağlar (zaten `.env.example`'da tanımlı, kullanılmayan bir
   değişken artık kullanılır).
2. Bağlantı kurulamazsa (collector çalışmıyorsa) servis **çökmez** —
   `otlptracegrpc` istemcisi arka planda yeniden dener; bu, health/ready
   uçlarının zaten uyguladığı "gözlemlenebilirlik altyapısı olmadan da
   servis ayakta kalır" ilkesiyle tutarlıdır.
3. Uçtan uca yayılımın **çalıştığının kanıtı**, çalışan bir arka uca değil
   entegrasyon testine bağlanır: bellek-içi span kaydedici
   (`sdktrace.NewTracerProvider` + `tracetest.InMemoryExporter`) ile
   üretici span'inin `trace_id`'si tüketici span'inin `trace_id`'siyle
   eşleşiyor mu diye doğrudan ölçülür. Bu, "collector çalışıyor mu"
   sorusundan bağımsız, mimarinin kendisini test eder.

Span'ler iki sınırda açılır: Kafka üretimi (`pkg/kafka` üretici) ve
Kafka tüketimi + işleme (`analysis-engine`, `integrity` — zaten
`Extract` çağrılan iki nokta). Yeni span sınırı icat edilmez.

### Boşluk 4 — K9 ölçüm ortamı yok

ADR-16: "K9 ölçümü Docker Compose üzerinde yapılır (`deploy.replicas:
1/2/4`)". Ama `docker-compose.yml`'de `analysis-engine` diye bir servis
**yok** — beş uygulama servisi de yerel `go run` ile çalıştırılıyor
(Sprint 6/7 kapanışlarında ölçülen tüm koşumlar böyle yapıldı). Docker
Compose'un `deploy.replicas`'ı ölçeklemek için önce servisin compose
dosyasında tanımlı olması gerekir.

**Karar.** ADR-16'nın kararı **değiştirilmez** — Compose üzerinde ölçüm
yapılır, K8s'e gidilmez. Eksik olan yalnızca uygulama: `analysis-engine`
için bir `Dockerfile` (çok aşamalı: `golang:1.26` derleme + dağıtık ikili
çalışma zamanı) ve compose'a bir `analysis-engine` servisi eklenir.

Bu servis **varsayılan `docker compose up`'a dahil değildir** (Compose
`profiles: ["scale-test"]` ile işaretlenir) — ölçüm dışında hiçbir
zaman gereksiz yere ayağa kalkmaz, geliştiricinin günlük `make infra-up`
akışını değiştirmez veya kaynak tüketmez.

**"Verim" tanımı (ölçümden önce beyan edilir):**

- Sabit bir yayın: `configs/smoke.yaml` (Sprint 6 Gün 3 kapısında da
  kullanılan, urban_ta'nın küçültülmüş kopyası — 100 ajan × 3 gün ≈ 3.000
  olay). Tam üretim senaryosu (`urban_no_ta`, ~298.000 olay) K9'un ölçtüğü
  soruyu (consumer paralelliği doğrusal mı) yanıtlamak için gerekli
  değildir — ölçüm tek bir dev makinede üç kez (1/2/4 replika) koşacağı
  için ölçek, toplam çalışma süresini pratikte tutacak kadar küçük,
  başlangıç maliyetlerini (Redis envanteri, Kafka bağlantısı) steady-state
  tüketimin altında bırakacak kadar büyük seçilmiştir. Analiz motoru
  **varsayılan** `HTS_SAMPLE_MODE=validation` ile çalışır
  (`scripts/run-scenario.sh`'nin de kullandığı, `analysis-engine` paket
  belgesindeki varsayılan mod) — yeni bir mod icat edilmez.
- `analysis-engine`, `HTS_IDLE_TIMEOUT` ile boşta-çıkış modunda
  (Sprint 6'da S4 aggregate-göstergesi ölçümü için zaten kurulmuş
  mekanizma — `scripts/run-scenario.sh`) çalıştırılır: kuyruk boşalınca
  süreç kendiliğinden sonlanır.
- **Verim = toplam işlenen olay / (son mesaj tüketiminden ilk mesaj
  tüketimine kadar geçen duvar saati, saniye)**.
- Ölçüm `replicas ∈ {1,2,4}` için tekrarlanır (partition sayısı 4
  olduğundan 4 replika üst sınırdır — ADR-16 rasyonelinin doğrudan
  sonucu: consumer group paralelliği partition sayısını aşamaz).
- **K9 oranı = verim(4 replika) / verim(1 replika)**; eşik ≥ 3,5 —
  BÖLÜM J'den değişmeden alınır.

### K9 — ölçüm sonucu (bu ADR'nin kararlarıyla, Sprint 8 sırasında yapıldı)

`configs/k9_scale.yaml` (250 ajan × 20 gün ≈ 50.000 olay, `smoke.yaml`'ın
~15 katı — sabit maliyetlerin payını azaltmak için amaca özel oluşturuldu;
üç ölçüm de aynı seed'in üç ayrı `run_id`'si), `HTS_IDLE_TIMEOUT=4s`:

| Replika | Süre (s) | Verim (olay/sn) |
|---|---|---|
| 1 | 16,65 | 3.002 |
| 2 | 15,39 | 3.250 |
| 4 | 15,55 | 3.215 |

**K9 oranı = 3.215 / 3.002 = 1,07×. Beyan edilen eşik (≥ 3,5×) TUTMADI.**

Ölçüm altyapısının kendisi doğru çalıştığı ölçülerek doğrulandı: 4
replikalı denemede her replika sırasıyla 50/52/54/58 olay işledi —
partition'ların (4 tane) replikalar arasında neredeyse eşit dağıldığını,
yani Kafka consumer group rebalance'ının doğru çalıştığını gösterir.
Darboğaz mimari değil, **ölçüm ortamı**: `free -h` ölçüm sırasında
15 GiB RAM'in 8,3 GiB'ının dolu ve 2 GiB swap'ın **tamamen dolu**
olduğunu gösterdi (`ps aux` bunu paylaşımlı geliştirici masaüstündeki
çok sayıda Chrome/VS Code sürecine bağladı). Bu makine tek amaçlı bir
ölçüm sunucusu değil — [[hts-dev-environment]] belleğinde zaten not
edilen disk/RAM sınırının somut bir tezahürü. Ek replikalar gerçek
paralellik yerine kıt host belleği için birbirleriyle yarışıyor
olabilir; 2→4 replika arasında verimin **gerilemesi** (3.250→3.215)
bunu destekliyor.

**Bu, K3'ün Sprint 5'teki ele alınışıyla aynı disiplinle raporlanan
negatif bir bulgudur:** eşik ölçümden önce beyan edilmişti (bu ADR'nin
kendisinde), sonuç istenen yönde çıkmadı diye değiştirilmedi. K9,
**bu ölçümde**, **bu makinede** tutmuyor. Mimarinin kendisi (partition
başına tüketici ataması) doğru çalıştığı için, izole/özel bir makinede
tekrar ölçülmesi sonraki bir adım olarak önerilir — ama bu, Sprint 8
kapsamında zorunlu tutulmaz (ADR-16 zaten K9'un Compose'da ölçüleceğini,
K8s'te değil, tam da yerel makine gürültüsünü en aza indirmek için
kararlaştırmıştı; o karar host'un kendisinin dolu olma ihtimalini
öngörmüyordu).

### Boşluk 5 — K10 "bit-identical" tanımsız

BÖLÜM J: "Aynı seed, iki `run_id` → estimates bit-identical." Hangi
kolonlar, hangi tolerans (float eşitliği!) belirtilmemiş.

Kod incelemesi K10'un mimari ön koşullarının **zaten** S3/S4'te
karşılandığını gösteriyor:
- `site_id`/`cell_id` `uuid.NewSHA1` ile site/sektör adından türetiliyor
  (`internal/simulator/inventory/{grid,sector}.go`) — rastgele değil,
  aynı seed aynı kimlikleri üretir.
- `event_id` UUIDv5 deterministik (ADR-01).
- RNG `math/rand/v2` `PCG` ile seed'den kurulu (`inventory/rng.go`,
  `agent/rng.go`).
- Kütle toplamı **Kahan toplaması** ile ve ızgara sırası (r,q) sabit
  tutularak yapılıyor (`internal/analysis/density/normalize.go`) —
  harita yinelemesinin rastgeleliğine rağmen toplam sırası sabit.
- Kontur sıralaması eşit kütlede (r,q) ile kırılıyor
  (`internal/analysis/core/contour.go: rankByMass`) — Go'nun kararsız
  `sort.Slice`'ı harita düzeninden etkilenmiyor.

Yani K10'un düşme riski kodda değil, yalnızca **ölçüm yönteminde**:
karşılaştırma run_id, `estimate_id` gibi koşuya özgü kolonları es
geçmeli, geri kalan her şeyi tam eşleştirmeli.

**Karar.** İki koşu aynı seed'li senaryo ile farklı `run_id`'lerle
çalıştırılır. Karşılaştırma SQL'i:

```sql
(SELECT agent_id, method, confidence, area_km2, part_count,
        centroid, ST_AsText(geom) AS geom_wkt
   FROM estimates WHERE run_id = :run_a
 EXCEPT
 SELECT agent_id, method, confidence, area_km2, part_count,
        centroid, ST_AsText(geom) AS geom_wkt
   FROM estimates WHERE run_id = :run_b)
UNION ALL
(SELECT agent_id, method, confidence, area_km2, part_count,
        centroid, ST_AsText(geom) AS geom_wkt
   FROM estimates WHERE run_id = :run_b
 EXCEPT
 SELECT agent_id, method, confidence, area_km2, part_count,
        centroid, ST_AsText(geom) AS geom_wkt
   FROM estimates WHERE run_id = :run_a);
```

**K10 geçer ⟺ bu sorgu 0 satır döndürür.** `EXCEPT`, Postgres'te satır
düzeyinde tam eşitlik ister (float dahil, IEEE754 bit kalıbı eşitliği —
"bit-identical" ifadesinin karşılığı budur, ondalık yuvarlama toleransı
yoktur). `run_id`, `estimate_id`, `created_at` gibi koşuya özgü kolonlar
karşılaştırmaya girmez çünkü bunların eşit olmayacağı zaten bilinir ve
karşılaştırmanın konusu değildir.

### Boşluk 6 — k8s kapsamı

ADR-16 zaten netti: "K8s çıktıları: `deployments/k8s/` altında manifest
+ HPA + README (dağıtım belgesi düzeyinde)." **Bu ADR o kararı
değiştirmez** — yalnızca dizin hâlâ boş olduğundan teslimi tamamlar.
Performans kriteri K8s'e **bağlanmaz** (ADR-16'nın gerekçesi: yerel
minikube/kind overhead'i ölçümü gürültülendirir).

---

## Karar özeti

| # | Boşluk | Çözüm | Eşik değişti mi |
|---|---|---|---|
| 1 | K8 sprint ataması | S4/S5'te zaten ölçülü; S8'de yeniden ölçülmez | Hayır |
| 2 | Metrik iskeleti boş | `/metrics` servisi (mevcut portlarda) + mevcut sayaçların OTel'e yansıtılması | Hayır |
| 3 | İz arka ucu yok | Yeni backend eklenmez; OTLP best-effort + in-memory testle kanıt | Hayır |
| 4 | K9 ortamı yok | `analysis-engine` Dockerfile + profil-gated compose servisi; verim tanımı | Hayır |
| 5 | K10 tanımsız | `EXCEPT`/`EXCEPT` karşılaştırması, koşuya özgü kolonlar hariç | Hayır |
| 6 | k8s boş | ADR-16 aynen uygulanır, dizin doldurulur | Hayır |

**Yeni bilimsel iddia yoktur.** Bu ADR hiçbir eşik, model veya algoritma
değiştirmez; yalnızca zaten var olan sayaçları görünür kılar ve K9/K10'un
nasıl ölçüleceğini kesinleştirir.

---

## Reddedilen alternatifler

**(A) K9'u da K8s'te ölçmek.** Reddedildi — ADR-16 zaten bu tartışmayı
kapattı; yeni bilgi yok, kararı geri almak için gerekçe yok.

**(B) Trace arka ucu olarak Jaeger/Tempo eklemek.** Reddedildi: plan
mimari diyagramında yok, tek geliştirici makinesinde ek bir konteyner
daha (bellek/CPU baskısı — ROS2/PX4 ile paylaşılan makine) kapsam dışı
fayda için kapsam içi maliyet olurdu. OTLP uç noktası zaten `.env`'de
tanımlıydı; kullanılmaması onu kaldırmayı değil, best-effort tüketmeyi
gerektirir.

**(C) `analysis-engine` compose servisini varsayılan `up`'a dahil
etmek.** Reddedildi: günlük geliştirme akışı zaten `go run` ile
çalışıyor (health portları, log formatı, debugger erişimi hep buna göre
kuruldu); varsayılanı değiştirmek Sprint 8 kapsamında olmayan bir
geliştirici deneyimi kararı olurdu. `profiles:` ile izole edildi.

**(D) K10 karşılaştırmasını Go kodunda yapmak (SQL yerine).**
Reddedildi: `EXCEPT` zaten satır-tam eşitliği PostgreSQL'in kendi
karşılaştırma semantiğiyle verir; Go'da yeniden yazmak ekstra
serileştirme adımı ekler ve karşılaştırmanın kendisini test edilmesi
gereken bir yüzey hâline getirirdi.

---

## İlgili

ADR-01 (deterministik `event_id`) · ADR-15 (audit/servis kimliği) ·
ADR-16 (K9 Compose kararı) · ADR-21 (Go poligonizasyon) · ADR-26 (ızgara
çözünürlüğü, K8) · BÖLÜM H (S8) · BÖLÜM J (K8–K10)
