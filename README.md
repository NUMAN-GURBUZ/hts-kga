# HTS-KGA — Konum Güven Alanı Analiz Platformu

**HTS-KGA**, operatör tarafında tutulan HTS/CDR (Hücresel Trafik Sinyali /
Call Detail Record) kayıtlarından bir abonenin olası konumunu **olasılıksal
bir güven alanı** olarak kestiren, uçtan uca çalışan bir bilimsel simülasyon
ve analiz platformudur. Beş mikroservis, 3GPP TR 38.901 tabanlı bir radyo
yayılım simülatörü, akış + toplu bütünlük denetimi, ölçülebilir bir kör test
mimarisi ve 35 mimari karar kaydından (ADR) oluşur.

> Bu proje bir bitirme (lisans) projesi kapsamında geliştirilmiştir. Kod ve
> bulgular akademik değerlendirme amaçlıdır.

---

## İçindekiler

1. [Projenin Amacı](#projenin-amacı)
2. [Kullanılan Teknolojiler](#kullanılan-teknolojiler)
3. [Sistem Mimarisi](#sistem-mimarisi)
4. [Gereksinimler](#gereksinimler)
5. [Kurulum](#kurulum)
6. [Çalıştırma](#çalıştırma)
7. [Demo Senaryosu](#demo-senaryosu)
8. [Testlerin Çalıştırılması](#testlerin-çalıştırılması)
9. [Proje Yapısı](#proje-yapısı)
10. [Sprint Özeti](#sprint-özeti)
11. [Kabul Kriterleri (K1–K10)](#kabul-kriterleri-k1k10)
12. [Bilimsel Katkılar](#bilimsel-katkılar)
13. [Dokümantasyon](#dokümantasyon)
14. [Lisans](#lisans)

---

## Projenin Amacı

Bir HTS kaydı yalnızca şunu söyler: *"bu abone, şu anda, şu baz istasyonu
sektörüne bağlıydı, timing advance değeri şuydu."* Bundan **"abone tam olarak
nerede?"** sorusuna geçmek, tek doğru cevabı olmayan, olasılıksal bir çıkarım
problemidir. HTS-KGA bu problemi üç artan karmaşıklıkta yöntemle çözer ve
her birinin gerçek konuma göre ne kadar isabetli olduğunu **ölçer**:

| Yöntem | Tanım | Yaklaşık alan (ölçülen medyan, senaryo A) |
|---|---|---|
| **B0** | Naif daire — hücrenin tüm kapsama alanı | ~118 km² |
| **B1** | Sektör dilimi — antenin yatay hüzmesiyle sınırlı | ~21 km² |
| **M** | Olasılıksal model — 3GPP yayılım + TA + komşu hücre kısıtlarıyla ağırlıklandırılmış yoğunluk ızgarasının kümülatif konturu (%50/%90/%95 güven) | ~0,04 km² (M@90) |

Bunun ötesinde platform iki soruyu daha, aynı bilimsel titizlikle
cevaplamaya çalışır:

- **Bu kayıt gerçek mi, yoksa manipüle mi edilmiş?** Beş kural (envanter
  tutarlılığı, kinematik imkânsızlık, zaman sıralaması, yörünge bütünlüğü,
  cihaz kimliği) ile **precision/recall ölçülerek** tespit edilir.
- **Bu sonuçlara nasıl güvenebiliriz?** Modelin "doğruluğunu" ölçen her
  bileşen, ölçtüğü şeyin **gerçek konumunu asla görmeyen** ayrı bir
  servistir — dört katmanlı, otomatik test edilen bir "kör test" mimarisiyle
  garanti altına alınır (bkz. [Sistem Mimarisi](#sistem-mimarisi)).

---

## Kullanılan Teknolojiler

| Katman | Teknoloji |
|---|---|
| Dil | Go 1.26.5 |
| Mesajlaşma | Apache Kafka 3.9 (KRaft modu, Zookeeper'sız), `franz-go` istemcisi |
| Veritabanı | PostgreSQL 15 + PostGIS 3.4 (coğrafi geometri) + TimescaleDB 2.x (zaman serisi hypertable) |
| Önbellek | Redis 7 (koşu ömürlü hücre envanteri) |
| API | gRPC (iç tüketiciler) + elle yazılmış REST/GeoJSON (dış istemciler) |
| Görselleştirme | Leaflet.js (statik, sunucusuz, harici CDN yok) |
| Gözlemlenebilirlik | OpenTelemetry (metrik + W3C TraceContext) → Prometheus 3.4 → Grafana 11.6 |
| Test | Go `testing` + `pgregory.net/rapid` (property-based testing) |
| Altyapı | Docker Compose (geliştirme) + Kubernetes manifestleri (dağıtım belgesi) |
| Konteynerleşme | Çok aşamalı Docker build (`golang:1.26-alpine` → `alpine:3.20`) |

---

## Sistem Mimarisi

```
┌──────────────────────────────────────────────────────────────────────────┐
│  Simülatör (1000 ajan, 30 gün, 3GPP TR 38.901 UMa/UMi/RMa)                │
│    │                                                                      │
│    ├──► Kafka hts.records      (4 partition, key=pseudo_msisdn) ──┬─────► │ Analiz Motoru
│    │      (operatör kaydı, HMAC-pseudonimleştirilmiş)             │       │ (B0/B1/M kestirimi)
│    │      + bilinçli enjekte edilmiş manipülasyonlar (K10)        └─────► │ Bütünlük Denetimi
│    │                                                                      │ (5 kural, akış+toplu)
│    └──► Kafka hts.groundtruth  (gerçek konum + manipülasyon etiketi)      │
│           ACL: Analiz & Bütünlük servisleri buraya ERİŞEMEZ ◄── KÖR TEST  │
│                                                                            │
│  Doğrulama (S3b, toplu) ── estimates ⋈ ground_truth → metrics + K1–K10   │
│                                                                            │
│  API Gateway ── gRPC (iç) + REST/GeoJSON (dış) + statik Leaflet sayfası  │
│                 svc_gateway rolü de ground_truth'u GÖREMEZ                │
│                                                                            │
│  PostgreSQL 15 + PostGIS 3.4 + TimescaleDB  ·  Redis 7                   │
│  OpenTelemetry → Prometheus → Grafana                                    │
└──────────────────────────────────────────────────────────────────────────┘
```

### Kör test — projenin mimari omurgası

Bir konum-kestirim sisteminin "doğruluğunu" ölçen kod, test edilen gerçek
konumu **görebiliyorsa** o ölçüm değersizdir — model, cevabı bilerek
"doğru" tahmin üretebilir. HTS-KGA bunu **dört bağımsız, otomatik test
edilen katmanla** engeller:

| Katman | Mekanizma |
|---|---|
| 1 — Kafka ACL | `svc_analysis`/`svc_integrity` principal'leri `hts.groundtruth` topic'ine **yetkisiz** (SASL/SCRAM + StandardAuthorizer, ACL yoksa reddet) |
| 2 — PostgreSQL rolü | Aynı roller `ground_truth` tablosunda **SELECT yetkisine sahip değil** |
| 3 — Veri modeli | Manipülasyon etiketi (`injected_rule`) yalnızca `ground_truth`'ta yazılı; kayıtların kendisinde hiçbir iz yok |
| 4 — İçe alma grafiği | `internal/simulator/*` paketlerinin analiz/bütünlük servislerince import edilemeyeceği **derleme zamanı testiyle** kanıtlanıyor (`tests/isolation`) |

```bash
make verify-isolation   # dört katmanı da canlı olarak sınar
```

Beş servisin tamamı (`simulator`, `analysis-engine`, `integrity`,
`validation`, `gateway`) bu ilkeye göre tasarlanmıştır — `svc_gateway`
rolü bile `ground_truth`'u göremez (ADR-33).

---

## Gereksinimler

| Araç | Zorunlu mu | Not |
|---|---|---|
| **Go ≥ 1.26** | ✅ | `go.mod`'da sabitlenmiş |
| **Docker** | ✅ | Beş altyapı servisi (Postgres/Kafka/Redis/Prometheus/Grafana) konteynerde çalışır |
| **Docker Compose v2** (`docker compose`, tireli değil) | ✅ | |
| **`psql`** (PostgreSQL istemcisi) | ✅ | Sunucu Docker'da çalışır, istemci host'ta kurulu olmalı. `make check-psql` denetler ve eksikse kurulum komutunu gösterir. |
| `protoc` | ❌ Opsiyonel | Yalnızca `make proto` (gRPC kodu yeniden üretme) için; üretilmiş dosyalar zaten commit'li |
| `golangci-lint` | ❌ Opsiyonel | Yalnızca `make lint` için |

Beş Docker servisinin hiçbiri elle kurulmaz — sürümleri
`deployments/compose/docker-compose.yml`'de sabitlenmiştir
(`timescale/timescaledb-ha:pg15-latest`, `apache/kafka:3.9.0`,
`redis:7-alpine`, `prom/prometheus:v3.4.1`, `grafana/grafana:11.6.1`).

---

## Kurulum

```bash
git clone <repo-url>
cd hts-kga
make demo
```

Bu tek komut:

1. `.env` yoksa `.env.example`'dan oluşturur ve `HMAC_SALT`'ı otomatik
   üretir (kriptografik olarak rastgele, 32 bayt).
2. Beş Docker servisini indirir/başlatır ve **gerçekten sağlıklı olana
   kadar bekler** (`docker compose up --wait`).
3. Veritabanı migration'larını uygular (7 dosya, idempotent).
4. Kafka topic'lerini, SASL/SCRAM kimliklerini ve ACL'leri kurar.
5. Küçük bir örnek senaryo (`configs/smoke.yaml`) koşturur — simülasyon →
   Kafka → analiz → bütünlük denetimi → doğrulama, uçtan uca.
6. Bir sonraki adımı ekrana yazar.

**Ölçülen gerçek süre: ~1-2 dakika** (ilk çalıştırmada Docker imaj
indirmesi hariç).

`.env`'in tek elle kontrol edilmesi gereken alanı `POSTGRES_PASSWORD`'dur
— varsayılan değerle de çalışır, ama gerçek bir dağıtımda değiştirilmelidir.
`.env` asla Git'e eklenmez (`.gitignore`).

### Elle, adım adım (ne olduğunu görmek isterseniz)

```bash
make env-init      # .env oluştur (zaten varsa dokunmaz)
make setup         # infra-up + migrate-up + seed-kafka
make simulate CONFIG=configs/urban_ta.yaml   # yalnızca simülasyon
```

---

## Çalıştırma

`make demo`'dan sonra veri hazırdır. Görselleştirmeyi başlatmak için:

```bash
make gateway
```

REST `:8080`, gRPC `:50051`, health `:8086`. Tarayıcıda
**`http://localhost:8080/`** — bir koşu seçin, katmanları (baz
istasyonları, hücre yoğunluğu, B0/B1/olasılık poligonları, bütünlük
bulguları) tek tek açın.

| Arayüz | Adres |
|---|---|
| Leaflet harita | http://localhost:8080/ |
| REST API | http://localhost:8080/api/v1/... |
| Grafana | http://localhost:3000 (admin/admin) |
| Prometheus | http://localhost:9090/targets |

Başka bir senaryo koşturmak için: `bash scripts/run-scenario.sh
configs/<senaryo>.yaml` (`urban_ta`, `urban_no_ta`, `rural_ta`,
`rural_no_ta` — dört bilimsel senaryo; `smoke` — hızlı doğrulama).

---

## Demo Senaryosu

**Hocaya ~10-15 dakikada gösterim için önerilen akış** (tüm komutlar
çalışır durumda, bu oturumda uçtan uca doğrulanmıştır):

1. **Mimari** (1,5 dk) — beş servis diyagramı, kör test vurgusu.
2. **Kör test canlı** (1,5 dk) — `make verify-isolation`; "permission
   denied" çıktısı.
3. **Simülasyon + Kafka** (1,5 dk) — Grafana'da canlı artan sayaçlar.
4. **Veritabanı** (1 dk) — `psql \dt`, `metrics`/`integrity_metrics`
   tabloları.
5. **Olasılık poligonları** (3 dk) — Leaflet'te tek kayıt: B0 → B1 → M@90,
   ~3.000× daralma (K2/K3).
6. **Bilimsel dürüstlük** (2 dk) — K1'in neden tutmadığının açıklanması
   (arama bölgesi tanımı — bkz. [Bilimsel Katkılar](#bilimsel-katkılar)).
7. **Bütünlük tespiti** (2,5 dk) — bir bulguya tıkla, `evidence` JSON'u;
   `make verify-k7 RUN_ID=...`.
8. **Dashboard** (1 dk) — Grafana genel bakış, Prometheus hedefleri.
9. **Kapanış** (1,5 dk) — 35 ADR, K1–K10 tablosu, "eşikler ölçümden önce
   beyan edildi, tutmayanlar açıklandı."

Ayrıntılı, dakika dakika senaryo ve her ekranın ne gösterdiği:
[`docs/results/final-project-review.md`](docs/results/final-project-review.md#6-hocaya-canlı-demo).

---

## Testlerin Çalıştırılması

```bash
make test               # birim + property-based testler (altyapı gerekmez)
make test-integration   # altyapı gerektiren testler — önce: make setup
```

- **70 test dosyası**, çekirdek bilimsel paketlerde (radyo yayılımı,
  geometri, coğrafi dönüşümler, bütünlük kuralları) **%85–98 satır
  kapsamı**.
- **19 dosyada property-based test** (`pgregory.net/rapid`) — kütle
  toplamının 1'e eşitliği, geometri iç içeliği (%50⊆%90⊆%95), TA'nın
  fiziksel imkânsızlığı gibi matematiksel değişmezler rastgele girdilerle
  sınanır.
- `tests/isolation/` — kör testin 4. katmanı (içe alma grafiği).
- `tests/integration/` — Kafka/Postgres/Redis gerektiren uçtan uca
  senaryolar (KT9.1–9.7 API sözleşmesi dahil).

---

## Proje Yapısı

```
hts-kga/
├── cmd/                        Beş servisin giriş noktaları
│   ├── simulator/              Ağ + ajan + olay + enjeksiyon simülasyonu
│   ├── analysis-engine/        Kafka tüketici → B0/B1/M kestirimi
│   ├── integrity/              Bütünlük denetimi (akış + toplu faz)
│   ├── validation/             Doğrulama + kalibrasyon (S3b, tek seferlik)
│   ├── gateway/                gRPC + REST + statik web sunucusu
│   └── persister/              Kafka → PostgreSQL kalıcılaştırma (S3a)
├── internal/
│   ├── simulator/              radio (3GPP yayılım) · agent · event · inventory · run
│   ├── analysis/               core (B0/B1/M) · density · geometry · params · driver
│   ├── integrity/              detector (5 kural) · source
│   ├── validation/             calibration · metrics · comparison · pipeline
│   ├── gateway/                query (GeoJSON) · rest · grpcsrv · audit
│   ├── storage/                postgres · redis · migrations (7 dosya)
│   ├── observability/          OTel init + health
│   ├── config/                 Senaryo YAML şeması + morfoloji profilleri
│   ├── persist/                Kafka → tablo kalıcılaştırma mantığı
│   └── rf/                     Link budget / path-loss modelleri
├── pkg/                        Bağımlılıksız, saf yardımcı paketler
│   ├── geo/                    Haversine, ENU/axial dönüşümleri
│   ├── kafka/                  SASL istemci, üretici, trace propagation, lag
│   ├── integrityrule/          Kural kimlik/öncelik tanımları (tek doğruluk kaynağı)
│   ├── privacy/ · split/ · ta/ · htswire/
├── proto/hts/v1/                gRPC sözleşmesi
├── deployments/
│   ├── compose/                 Docker Compose (geliştirme)
│   └── k8s/                     Kubernetes manifestleri (dağıtım belgesi)
├── configs/                      6 senaryo YAML'ı (4 bilimsel + smoke + k9_scale)
├── scripts/                      Kurulum/doğrulama/ölçüm kabuk betikleri
├── tests/{golden,integration,isolation}/
├── web/                          Leaflet statik sayfası
└── docs/
    ├── architecture/adr/         35 Mimari Karar Kaydı
    ├── planning/                 Sprint planları, demo analizi
    ├── results/                  Sprint kapanış raporları, ölçüm kanıtları
    └── scientific/                Çapraz doğrulama, ajan rutini notları
```

---

## Sprint Özeti

Proje 9 sprintte (S0–S8), plan BÖLÜM A–N'de tanımlanan 133 SP kapsamıyla
tamamlanmıştır.

| Sprint | Kapsam | Kapanış |
|---|---|---|
| S0 | Altyapı + kör test temeli + `run_id` şeması | ✅ |
| S1 | Hex ızgara + hücre envanteri + ajan yerleşimi | ✅ |
| S2 | 3GPP yayılım + best-server + olay üretimi + enjeksiyon + Kafka | ✅ |
| S3 | Sektör/TA ağırlıkları + yoğunluk ızgarası + komşu kısıtı | ✅ |
| S4 | Kümülatif kontur + MULTIPOLYGON + B0/B1/M kestirimi | ✅ |
| S5 | Doğrulama hattı + kalibrasyon + 4 bilimsel senaryo | ✅ (K1 negatif bulgu) |
| S6 | 5 bütünlük kuralı + precision/recall ölçümü | ✅ (kural 2 kısmi) |
| S7 | gRPC + REST + statik Leaflet arayüzü | ✅ |
| S8 | OpenTelemetry + Kafka trace + Compose ölçekleme + Grafana | ✅ (K9 negatif, K10 kritik hata bulundu+düzeltildi) |

Her sprintin ayrıntılı kapanış raporu `docs/results/sprint{5,6,7,8}-report.md`
altındadır (S0–S4'ün ölçüm kanıtları ilgili ADR'lerin içindedir).
Bağımsız bir mimari inceleme: [`docs/results/final-project-review.md`](docs/results/final-project-review.md).

---

## Kabul Kriterleri (K1–K10)

| # | Kriter | Eşik | Sonuç |
|---|---|---|---|
| K1 | Kalibrasyon kapsaması@90% | %85–95 | ❌ %52–58 (negatif bulgu — kök nedeni bulundu) |
| K2 | M@90 vs B0 alan daralması | ≥%75 | ✅ %91–99,99 |
| K3 | M@90 vs B1 alan daralması | TA:≥%50 · TA yok:≥%20 | ✅ %51–99,95 |
| K4 | Kalibrasyon/doğrulama ayrımı | Karışık sorgu → hata | ✅ |
| K5 | 4 senaryo koşulmuş | A,B,C,D | ✅ |
| K6 | Kör test bütünlüğü | 4 katman geçer | ✅ |
| K7 | Bütünlük tespiti precision | ≥%90 (kural bazında) | ⚠️ 3/5 tam, 1/5 kısmi, 1/5 kapsam dışı |
| K8 | Geometri kararlılığı | `repaired<%1`, `p95≤3` | ✅ |
| K9 | Ölçeklenebilirlik (4 replika) | ≥3,5× verim | ❌ 1,07× (ortam kaynaklı) |
| K10 | Tekrarlanabilirlik | Bit-identical | ⚠️ %99,91 (kalıntı kayan nokta gürültüsü) |

Tutmayan/kısmi kriterlerin **hiçbiri kod hatası değildir** — her biri kök
nedeniyle birlikte ölçülmüş ve raporlanmıştır. Ayrıntı:
[`docs/results/final-project-review.md`](docs/results/final-project-review.md#2-kabul-kriterleri).

---

## Bilimsel Katkılar

- **3GPP TR 38.901 tabanlı, çapraz doğrulanmış yayılım simülasyonu**
  (UMa/UMi/RMa), Okumura-Hata/COST-231 ile ≤10 dB fark içinde doğrulandı
  (`docs/scientific/cross-validation-3gpp.md`).
- **HPD (highest posterior density) kontur yöntemi**: kümülatif kütle
  ızgarasından üretilen %50/%90/%95 güven bölgeleri, aynı kütleyi kaplayan
  bölgeler arasında **alanı en küçük** olandır — sektör dilimine göre
  ölçülen ≥%50–99,95 daralma bu tanımın doğrudan sonucudur (K2/K3).
- **K1'in negatif bulgusunun kök nedeni**: kalibrasyon değil, arama bölgesi
  tanımı. Sektör diliminin kendisi gerçek konumun ancak %58–60'ını
  kapsıyor; model **dilim içinde** tam kalibre (M@90/B1 ≈ %90). λ'nın
  kaldıraç etkisi %1–3 ile sınırlı olduğu ölçüldü (ADR-25).
- **Beş kurallı bütünlük tespiti**, precision/recall'u Wilson güven
  aralığıyla birlikte ölçülmüş: üç kural %100,00 precision, kural 2
  (kinematik) fiziksel olarak açıklanabilir bir sınırla kentselde tutmuyor
  (olaylar arası ortalama süre × eşik hız < şehir çapı).
- **Dört katmanlı, otomatik test edilen kör test mimarisi** — Kafka ACL +
  PostgreSQL rolü + veri modeli + derleme zamanı içe alma grafiği testi.
  API katmanına kadar uzatıldı (`svc_gateway` de `ground_truth`'u göremez).
- **K10 ölçümü sırasında bulunan ve düzeltilen mimari hata (ADR-35)**:
  run_id-salted bir veritabanı kimliğinin üç ayrı yerde yanlışlıkla
  fiziksel belirlenirlik için de kullanıldığı tespit edildi (%43,7 satır
  uyuşmazlığı), kök nedeniyle birlikte düzeltildi (%0,09'a indirildi).
  Bu bulgunun kendisi de bir ADR olarak belgelendi — "hata bulundu,
  kanıtlandı, düzeltildi" disiplini projenin geneline yayılmıştır (35 ADR).

Tüm ölçümler **ölçümden önce beyan edilmiş eşiklere karşı** yapılmıştır ve
sonuca göre değiştirilmemiştir (plan BÖLÜM J disiplini).

---

## Dokümantasyon

| Belge | İçerik |
|---|---|
| `docs/architecture/adr/` | 35 Mimari Karar Kaydı — her biri Bağlam/Karar/Sonuç/Reddedilen Alternatifler yapısında |
| `docs/results/sprint{5,6,7,8}-report.md` | Sprint kapanış raporları, ölçüm kanıtları |
| `docs/results/final-project-review.md` | Bağımsız mimari inceleme (Senior Architect gözüyle) |
| `docs/results/runnability-analysis.md` | Sıfırdan çalıştırılabilirlik analizi |
| `docs/planning/` | Sprint planları, demo analizi |
| `docs/scientific/` | Çapraz doğrulama, ajan rutini modellemesi notları |
| `make help` | Tüm komutların listesi |

---

## Lisans

Bu proje [MIT Lisansı](LICENSE) ile lisanslanmıştır.
