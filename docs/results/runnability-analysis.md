# HTS-KGA — Çalıştırılabilirlik Analizi (Sıfır Bilgiyle Klonlama Senaryosu)

**Tarih:** 2026-07-31
**Yöntem:** İddia değil, ölçüm. Bu belgedeki her bulgu bu oturumda gerçek
komutlar çalıştırılarak doğrulandı (aşağıda kanıtlarıyla).
**Amaç:** "Bu projeyi ilk kez klonlayan, hiçbir şey bilmeyen bir geliştirici
gerçekten ayağa kaldırabilir mi?" sorusuna kanıtlı cevap.

**Kod yazılmadı, README yazılmadı — yalnızca analiz.**

---

## 1. Kurulum Analizi

### Host makinede önceden kurulu olması gerekenler (otomatik DEĞİL)

| Araç | Gerekli mi | Sürüm (bu makinede ölçülen) | Nerede kullanılıyor |
|---|---|---|---|
| **Go** | ✅ Zorunlu | 1.26.5 (`go.mod`'da sabitlenmiş) | Tüm `cmd/*` servisleri `go run` ile çalıştırılıyor |
| **Docker** | ✅ Zorunlu | 29.1.3 | Compose altyapısı |
| **Docker Compose (v2)** | ✅ Zorunlu | 2.40.3 | `docker compose` (tire biçiminde, eski `docker-compose` değil) |
| **`psql` (PostgreSQL istemcisi)** | ✅ Zorunlu — **belgelenmemiş** | 14.23 (sunucu 15, istemci uyumlu) | `make migrate-up/migrate-down/migrate-status/verify-integrity/verify-k7`, `scripts/verify-k10.sh`, `scripts/run-scenario.sh` — hepsi host'ta kurulu `psql` binary'sine shell-out yapıyor |
| **`protoc` + Go eklentileri** | ⚠️ Yalnızca `make proto` için | kuruluysa | `.pb.go` dosyaları **zaten commit'li**; sıfırdan çalıştırmak için gerekmiyor |
| **`golangci-lint`** | ❌ Yalnızca `make lint` için | **bu makinede kurulu değil** ve proje yine de tam çalışıyor | CI'da zorunlu, yerel çalıştırma için değil |
| **`migrate` (golang-migrate CLI)** | ❌ **Gerekmiyor** | kurulu ama kullanılmıyor | Makefile hedefi `migrate-up` ismine rağmen bu CLI'yi **çağırmıyor** — ham `psql -f` döngüsü. Yanıltıcı isimlendirme, gerçek bir bağımlılık değil. |

### Docker Compose ile otomatik kurulan (`deployments/compose/docker-compose.yml`)

| Servis | İmaj (pinned) | Otomatik mi |
|---|---|---|
| PostgreSQL + PostGIS + TimescaleDB | `timescale/timescaledb-ha:pg15-latest` | ✅ |
| Kafka (KRaft) | `apache/kafka:3.9.0` | ✅ |
| Redis | `redis:7-alpine` | ✅ |
| Prometheus | `prom/prometheus:v3.4.1` | ✅ |
| Grafana | `grafana/grafana:11.6.1` | ✅ |

**Beş servis de `make setup`/`docker compose up` ile otomatik iner** — kurulum
gerektirmez, yalnızca image indirme (ilk seferde internet gerekir).

### Otomatik OLMAYAN, host'ta manuel kurulması gerekenler

**Go, Docker, Docker Compose, `psql`.** Bunlardan **`psql` hiçbir yerde
(plan, ADR, .env.example) bir önkoşul olarak yazılmıyor** — bu belgeyi
okuyana kadar bilinmeyen, sessiz bir bağımlılıktır.

---

## 2. Çalıştırma Analizi

### Gerekli komutlar (sırayla, gerçek)

| # | Komut | Zorunlu mu | Ne yapar |
|---|---|---|---|
| 1 | `cp .env.example .env` | ✅ Zorunlu | `.env` yoksa **`make` hiçbir hedefte çalışmaz** (aşağıda kanıtlandı) |
| 2 | `.env` içinde `HMAC_SALT`'ı gerçek bir değerle değiştir | ✅ **Sert zorunlu** | Örnek değer 30 karakter, kod ≥32 karakter istiyor — placeholder ile simülatör **fail-fast** hata verir |
| 3 | `.env` içinde `POSTGRES_PASSWORD`'ı değiştir | ⚠️ Yumuşak öneri | Placeholder değer bile **teknik olarak çalışır** (Postgres onu literal parola sanır) — güvenlik hijyeni, işlevsel engel değil |
| 4 | `make setup` | ✅ Zorunlu | `infra-up` + `migrate-up` + `seed-kafka` |
| 5 | Bir senaryo koşusu (`scripts/run-scenario.sh configs/<x>.yaml` **veya** `make simulate` + elle diğer adımlar) | ✅ Zorunlu (veri olmadan gösterilecek bir şey yok) | Uçtan uca: simülasyon → persist → bütünlük → analiz → doğrulama |
| 6 | `make gateway` | ✅ Görselleştirme için zorunlu | REST/gRPC/Leaflet |

### Opsiyonel komutlar

`make test`, `make test-integration`, `make lint`, `make proto`,
`make verify-integrity`, `make verify-isolation`, `make verify-k7`,
`make verify-k10`, `make kafka-list`, `make migrate-status`,
`scripts/measure-k9.sh` — hiçbiri projeyi ayağa kaldırmak için gerekli
değil, doğrulama/tanı amaçlı.

---

## 3. Tek Komut Analizi

### Şu anda mümkün mü?

**Hayır.** En az **4 ayrı komut** gerekiyor (yukarıdaki 1-2, 4, 5, 6) ve
ilk ikisi (`.env` oluşturma + `HMAC_SALT` düzenleme) **hiçbir şekilde tek
komuta indirgenemez** çünkü Makefile'ın kendisi `.env` olmadan
**parse bile edilemiyor**:

```
$ mv .env /tmp && make help
Makefile:14: .env: No such file or directory
make: *** No rule to make target '.env'.  Stop.
```

*(Bu oturumda gerçek komutla doğrulandı — `.env` dosyası test için geçici
olarak kaldırılıp geri yüklendi, projeye kalıcı bir değişiklik yapılmadı.)*

### `make setup` bile tek başına güvenilir değil — race condition

`infra-up` hedefi:
```make
infra-up:
	$(DC) up -d
	@echo "Servisler ayağa kalkıyor, sağlık kontrolü bekleniyor..."
	@$(DC) ps
```
`docker compose up -d` konteynerler **başlatılır başlatılmaz** döner —
sağlıklı olmalarını **beklemez** (`--wait` bayrağı kullanılmıyor). `echo`
satırı "bekleniyor" diyor ama fiilen hiçbir şey beklemiyor, yalnızca anlık
`ps` çıktısı basıyor. Hemen ardından çalışan `migrate-up`, Postgres henüz
`start_period: 20s` içindeyken (özellikle **ilk soğuk açılışta**, imaj
init'i normal çalışmadan uzun sürer) bağlanmaya çalışabilir ve
başarısız olabilir. Bu makinede (sıcak Docker cache, önceden çekilmiş
imajlar) sorun gözlenmedi çünkü altyapı zaten aylardır bu makinede
çalışıyor — **gerçek bir "sıfırdan klonlanmış makine" senaryosunda bu risk
gerçektir.**

### Eksik olan tam olarak nedir?

1. **`scripts/bootstrap.sh` (ya da `make demo`) diye bir orkestrasyon
   scripti yok.** Var olan en yakın şey `scripts/run-scenario.sh` ama o da
   yalnızca simülasyon zincirini yapıyor — altyapıyı ayağa kaldırmıyor,
   gateway'i başlatmıyor.
2. **`infra-up`, `docker compose up -d --wait` kullanmıyor** (Compose v2'de
   zaten var olan bir bayrak — sağlıklı olana kadar gerçekten bekler).
3. **`.env` oluşturma otomatikleştirilemez** (sır içerdiği için haklı olarak
   elle yapılıyor) ama en azından `HMAC_SALT`'ı `openssl rand -hex 32` ile
   otomatik üreten bir `make env-init` gibi bir kolaylık hedefi yok.

### Bu ne kadar geliştirme ister?

**Yarım günden az** ve **uygulama koduna hiç dokunulmaz** — yalnızca:
- `infra-up`'a `--wait` bayrağı eklemek (1 satır).
- Opsiyonel bir `scripts/bootstrap.sh` (mevcut `run-scenario.sh` + `make
  gateway`'i arka planda başlatan ~20 satırlık bir orkestrasyon).
- Opsiyonel bir `make env-init` (`openssl rand -hex 32` ile `HMAC_SALT`'ı
  otomatik dolduran bir `sed`/`envsubst` çağrısı).

Bu üçü de **isteğe bağlı iyileştirmedir** — mevcut hâliyle proje 4-5
komutla ve ~2-3 dakikada tam çalışır duruma geliyor (Bölüm 4'te ölçüldü);
"çalışmıyor" değil, "tek komut değil" durumudur.

---

## 4. Demo Analizi — Sıfırdan Çalıştırma (Numaralı, Gerçek Süreyle)

**Sistem bu 5 adımdan sonra tam çalışır durumda olacak** (5. adımdan sonra
tarayıcıda harita görünür):

1. **`cp .env.example .env`** — sonra `.env` içinde `HMAC_SALT`'ı
   `openssl rand -hex 32` çıktısıyla değiştirin (zorunlu — placeholder ile
   simülatör hata verir).

2. **`make setup`** — beş Docker servisini indirir/başlatır + migration'ları
   uygular + Kafka topic/ACL/SCRAM kimliklerini kurar. İlk seferde imaj
   indirme dahil birkaç dakika; sonraki seferlerde ~30-40 saniye. (Race
   riski için Bölüm 3'e bakın — pratikte birkaç saniye bekleyip devam etmek
   yeterli oluyor, ama garanti değil.)

3. **`bash scripts/run-scenario.sh configs/smoke.yaml`** — uçtan uca:
   simülasyon → persister'lar ∥ bütünlük akışı → bütünlük toplu fazı →
   analiz → doğrulama (F.1–F.5) → `verify_integrity`.
   **Ölçülen gerçek süre: 1 dakika 1 saniye** (bu oturumda çalıştırıldı,
   `run_id=f479411d-...` üretti, 5/5 bütünlük denetimi OK döndü).
   *(Not: tam bilimsel senaryolar — `urban_ta.yaml` vb., 298.117 olay —
   ~15-30 dakika sürer; demo için `smoke.yaml` veya `configs/demo.yaml`
   gibi küçültülmüş bir config kullanılmalı, tam senaryo koşusu **canlı
   demo'da değil önceden** yapılmalı.)*

4. **`make gateway`** (ayrı terminalde, arka planda bırakın) — REST `:8080`,
   gRPC `:50051`, health `:8086`. Bu oturumda başlatılıp gerçek veriyle
   sınandı: `curl localhost:8080/api/v1/runs` az önceki koşuyu döndürdü.

5. **Tarayıcıda `http://localhost:8080/`** — koşuyu seçin, katmanları açın.

**Toplam: 4 komut, ~2-4 dakika (smoke.yaml ile), demo için tam yeterli.**

---

## 5. Demo Akışı — Hangi Ekranlar Gerçekten Gösterilebilir

| Ekran | Gösterilebilir mi | Kanıt / Not |
|---|---|---|
| **Docker** (`docker compose ps`) | ✅ Evet | Beş servis, sağlıklı |
| **Grafana** | ✅ Evet | "HTS-KGA — Genel Bakış" dashboard'u provisioning ile otomatik yüklü (`localhost:3000`, admin/admin) |
| **Leaflet** | ✅ Evet | Bu oturumda canlı veriyle sınandı — gerçek, çalışan bir arayüz |
| **REST API** | ✅ Evet | `curl`/tarayıcı — bu oturumda `/api/v1/runs` gerçek veri döndürdü |
| **gRPC** | ⚠️ **Var ama demo'ya hazır değil** | Port `:50051` açık ve kod çalışıyor ama **`grpcurl` bu makinede kurulu değil** ve projede hazır bir örnek istemci yok. Canlı gRPC çağrısı göstermek için ya `grpcurl` kurulmalı ya da küçük bir Go istemci yazılmalı (ikisi de demo öncesi yapılmalı, canlı değil). |
| **PostgreSQL** | ✅ Evet | `psql` ile `\dt`, `SELECT * FROM metrics`, vb. |
| **Kafka** | ⚠️ Gösterilebilir ama sönük | `kafka-console-consumer.sh` ham JSON basar — görsel olarak etkileyici değil. **Daha iyi seçenek:** Grafana'da `hts_records` sayacının canlı arttığını göstermek. |
| **Integrity** | ✅ Evet | `make verify-k7 RUN_ID=...` tablosu, bulguya tıklayınca `evidence` JSON popup'ı (Leaflet) |
| **Probability (B0/B1/M)** | ✅ Evet | Leaflet katmanları, gerçek geometrilerle |
| **Harita** | ✅ Evet | Yukarıdakilerin toplamı |

---

## 6. Eksikler (Çalıştırmayı Zorlaştıran)

| # | Eksik | Etki | Ciddiyet |
|---|---|---|---|
| 1 | **README yok** | Yeni klonlayan biri nereden başlayacağını bilmiyor | 🔴 Kritik (önceki incelemede de tespit edildi) |
| 2 | **`psql` bağımlılığı hiçbir yerde belgelenmiyor** | `make migrate-up`/`verify-*` host'ta `psql` yoksa sessizce "command not found" ile başarısız olur | 🟠 Orta — belgelenmesi 1 satır |
| 3 | **`infra-up` sağlık kontrolünü gerçekten beklemiyor** | Soğuk/yavaş makinede `make setup` ilk denemede başarısız olabilir | 🟠 Orta — `--wait` bayrağı 1 satırlık düzeltme |
| 4 | **Tek komutla kurulum/demo scripti yok** | Kullanıcı 4-6 komutu doğru sırada elle çalıştırmalı | 🟡 Düşük — mevcut hâliyle çalışıyor, kolaylık meselesi |
| 5 | **`scripts/run-scenario.sh` gateway'i başlatmıyor** | Demo için ayrı bir 4. komut (`make gateway`) gerekiyor | 🟡 Düşük |
| 6 | **`demo-plan.md` Sprint 7 öncesi yazılmış, güncel değil** | Statik Leaflet sayfası öneriyor, artık gerçek gateway var | 🟡 Düşük — bu belge + önceki review onu fiilen güncelliyor |
| 7 | **gRPC için hazır bir demo aracı/istemci yok** | Canlı gRPC çağrısı gösterilemez, yalnızca REST | 🟡 Düşük — demo REST üzerinden zaten tam |
| 8 | **Kafka konteyneri restart'ta SASL kimliklerini kaybediyor** (Sprint 8'de tespit edildi) | Yalnızca *restart* senaryosunda; **ilk kurulumda sorun değil** | 🟢 Bilgi amaçlı — sıfırdan kuruluma engel değil |

---

## 7. Çözüm Planı (Öncelik Sırasıyla)

| Sıra | İş | Efor | Neden bu sırada |
|---|---|---|---|
| **1** | README yaz (kurulum + `psql` önkoşulu + demo akışı) | ~yarım gün | Diğer her şeyin görünürlüğü buna bağlı; kod değişikliği gerektirmez |
| **2** | `.env.example`'a `psql`'in bir önkoşul olduğuna dair bir satır not düş | 2 dakika | Sessiz bağımlılığı görünür kılar |
| **3** | `infra-up`'a `docker compose up -d --wait` bayrağını ekle | 1 satır | Race condition'ı ortadan kaldırır, "tek komut" iddiasını gerçek kılar |
| **4** | (İsteğe bağlı) `scripts/bootstrap.sh` — setup + smoke senaryosu + gateway'i tek script'te zincirle | ~1 saat | Demo hazırlığını 4 komuttan 1'e indirir |
| **5** | (İsteğe bağlı) `make env-init` — `HMAC_SALT`'ı otomatik üretir | ~15 dakika | Kolaylık, zorunlu değil |
| **6** | (İsteğe bağlı) `demo-plan.md`'yi güncelle ya da bu belgeye yönlendiren bir not ekle | ~10 dakika | Tutarlılık |

**Maddeler 1–3 önerilir, 4–6 isteğe bağlıdır.** Hiçbiri uygulama koduna
dokunmaz; hepsi dokümantasyon + Makefile/script düzeyinde, toplam bir
günden az.

---

## Sonuç

**Proje şu anda 4 komutla ve ölçülen ~2-4 dakikada gerçekten ayağa
kalkıyor** (bu oturumda uçtan uca doğrulandı: `.env` kurulumu →
`make setup` → `run-scenario.sh` → `make gateway` → tarayıcıda gerçek
veri). **Tek komut değil ama çalışıyor.** Tek komuta indirgemek ve
soğuk-başlangıç risk(ler)ini kapatmak istenirse Bölüm 7'deki 1-3 numaralı
maddeler yeterlidir.
