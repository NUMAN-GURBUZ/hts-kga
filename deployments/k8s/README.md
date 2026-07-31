# deployments/k8s — Dağıtım Belgesi (ADR-16, ADR-34/4)

## Kapsam ve durum

Bu dizin **ADR-16**'nın kararının teslimidir: "K8s çıktıları:
`deployments/k8s/` altında manifest + HPA + README (dağıtım belgesi
düzeyinde)." Yani buradaki manifestler:

- **Çalışır ve `kubectl apply -k` ile kurulabilir hâldedir.**
- **Performans/ölçekleme kriteri (K9) bunlara bağlanmaz.** K9'un ölçülen
  sonucu Docker Compose'dadır (ADR-34/4, `deployments/compose/`,
  `scripts/measure-k9.sh`) — gerekçe ADR-16'da: tek geliştirici
  makinesinde minikube/kind overhead'i ölçümü gürültülendirir.
- Bu dosyalar bir Helm chart'ı ya da üretime hazır bir operatör
  **değildir**; "bu mimari K8s'e nasıl taşınır" sorusunun cevabıdır.

## Mimari not: PostgreSQL/Kafka/Redis kümenin DIŞINDADIR

Compose kurulumuyla tutarlı olarak, bu manifestler PostgreSQL/Kafka/Redis'i
K8s içinde çalıştırmaz — `configmap.yaml`'daki `POSTGRES_HOST` /
`KAFKA_BROKERS` / `REDIS_ADDR` harici adreslere işaret eder. Durumlu
altyapıyı (özellikle Kafka KRaft, tek-node) K8s'e taşımak ayrı bir mimari
karardır ve bu ADR'nin kapsamında değildir.

## Servisler

| Kaynak | Tür | Neden |
|---|---|---|
| `analysis-engine` | Deployment + HPA | Uzun ömürlü Kafka tüketicisi (K9'un konusu) |
| `integrity` | Deployment | Uzun ömürlü Kafka tüketicisi (akış fazı) |
| `gateway` | Deployment + Service | Uzun ömürlü REST/gRPC servisi (ADR-33) |
| `simulator` | Job | Bir koşuyu üretip çıkar |
| `validation` | Job | Bir koşuyu doğrulayıp çıkar (S3b, ADR-04) |

`persister` (S3a) ve toplu bütünlük fazı bilinçli olarak dışarıda
bırakılmıştır — bunlar tek geliştirici akışında `go run` ile tetiklenen
kısa ömürlü yardımcı süreçlerdir (bkz. `scripts/run-scenario.sh`); K8s
dağıtımında bir CronJob/init-container olarak modellenmeleri gerekir ama
bu, dağıtım belgesinin kapsamını aşan bir üretim kararıdır.

## İmaj derleme

Tek bir genel Dockerfile, `SERVICE` build-arg'ı ile hangi `cmd/*`
paketinin derleneceğini seçer:

```bash
docker build -f deployments/k8s/Dockerfile --build-arg SERVICE=analysis-engine \
  -t hts-kga/analysis-engine:latest .
docker build -f deployments/k8s/Dockerfile --build-arg SERVICE=integrity \
  -t hts-kga/integrity:latest .
docker build -f deployments/k8s/Dockerfile --build-arg SERVICE=gateway \
  -t hts-kga/gateway:latest .
docker build -f deployments/k8s/Dockerfile --build-arg SERVICE=simulator \
  -t hts-kga/simulator:latest .
docker build -f deployments/k8s/Dockerfile --build-arg SERVICE=validation \
  -t hts-kga/validation:latest .
```

## Sırlar — repo public, `secret-example.yaml`'ı ASLA doğrudan uygulamayın

`secret-example.yaml` yalnızca hangi anahtarların beklendiğini
belgeler; `CHANGE_ME` değerleriyle **kubectl apply edilmemelidir**.
Gerçek Secret'ı kendi `.env`'inizden türetin:

```bash
kubectl create namespace hts-kga
kubectl create secret generic hts-kga-secrets \
  --from-env-file=.env -n hts-kga
kubectl create configmap hts-kga-scenarios \
  --from-file=configs/ -n hts-kga
```

## Kurulum

```bash
# Sırlar ve senaryo ConfigMap'i yukarıdaki gibi elle oluşturulduktan sonra:
kubectl apply -k deployments/k8s/
```

## Bilinçli olarak yapılmayanlar

- **Ingress / TLS** — kümeye özgü, bu belgenin kapsamı dışında.
- **NetworkPolicy** (ADR-32'nin Kafka ACL'ini K8s ağ düzeyinde
  yansıtan bir politika) — gelecekte eklenebilir, K6'nın kendisi zaten
  Kafka/PostgreSQL katmanında sağlanıyor.
- **StatefulSet ile Kafka/PostgreSQL** — yukarıda açıklandığı gibi
  bilinçli olarak kümenin dışında tutuluyor.
