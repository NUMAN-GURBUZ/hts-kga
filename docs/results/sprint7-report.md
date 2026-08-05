# Sprint 7 — API Gateway ve Görselleştirme (E06 + E07) Kapanış Raporu

**Tarih:** 2026-07-30
**Kapsam:** T-E06-01..05, T-E07-01..06 + ADR-33
**Kabul kriteri:** KT9 (yedi madde, ADR-33'te tanımlandı)

---

## 1. Ne yapıldı

Sprint 7, platformun **beşinci** ve son uygulama servisini canlıya aldı:
`cmd/gateway` — gRPC (iç) + REST/GeoJSON (dış) + statik Leaflet sayfası.
Sprint 6 sonunda `cmd/gateway` 45 satırlık bir iskeletti; şimdi 198 satır ve
yedi uç servis ediyor.

### Plan doğrulaması — üç boşluk, üçü de ADR-33 ile kapatıldı

1. **KT9 tanımsızdı.** BÖLÜM H, S7'nin çıktısını "KT9" diye veriyordu ama
   plan içinde KT9'un ne olduğu hiçbir yerde yazmıyordu; formel kriterler
   BÖLÜM J'deki K1–K10'du ve S7'ye bir K-numarası atanmamıştı. KT9, ADR-13'ün
   zaten karara bağladığı kısıtları **yeni eşik icat etmeden** yedi ölçülebilir
   maddeye bağlandı.
2. **`svc_gateway` rolü yoktu.** BÖLÜM D `audit_log.principal`'da `'gateway'`
   yazıyordu ama migration 004 dört rol oluşturuyordu, gateway yoktu.
   Migration 007 ile eklendi — `ground_truth` erişimi **yok** (ADR-33/2).
3. **k=5'in hangi uçlarda uygulanacağı belirsizdi.** Sistemde k=5'in anlamlı
   olduğu hiçbir uç yoktu; yedinci uç (`/api/v1/aggregate/cell-activity`)
   bilinçli olarak eklendi.

---

## 2. Eklenen/değiştirilen dosyalar

```
proto/hts/v1/hts.proto (+ üretilen 2 Go dosyası)
internal/gateway/query/{filter,errors,geojson,metrics}.go
internal/gateway/{audit,rest,grpcsrv}/
internal/storage/migrations/007_gateway_role.sql
docs/architecture/adr/ADR-33-api-contract-and-kt9.md
tests/integration/gateway_test.go
web/{index.html,app.js,app.css,vendor/leaflet.*}
cmd/gateway/main.go (45 → 198 satır)
```

Yedi uç (ADR-33/3): `runs`, `cells`, `estimates`, `findings`, `metrics`,
`integrity-metrics`, `aggregate/cell-activity`.

---

## 3. Düzeltilen hatalar

1. **`cell-activity`'nin "bastırılan" sayısı yanlıştı** — kural 1'in ~1.172
   sahte hücresini de sayıyordu (1182 yerine gerçek 11). Sahte hücrelerin
   konumu yok, haritada gösterilemezler; gizlilik etkisi diye raporlamak
   abartı olurdu. `JOIN cells` eklenerek düzeltildi.
2. **KT9.6 testi düşüyordu, kusur testteydi:** `svc_gateway` `audit_log`'a
   yazar ama okuyamaz — denetlenen taraf denetim kaydını okumamalı. Test ayrı
   bir doğrulayıcı bağlantı (`HTS_TEST_GATEWAY_DSN` yerine ayrı bir admin DSN)
   kullanacak şekilde düzeltildi.
3. **Dead `pick-subscriber` düğmesi** `app.js`'ten kaldırıldı — yalnızca bir
   mesaj basıyordu, işlevi yoktu.

---

## 4. Test sonuçları

28 paket yeşil, `gofmt`/`go vet` temiz. KT9'un yedi maddesi de geçti:

| Madde | Ölçüm |
|---|---|
| KT9.1 | `run_id`'siz istek → 400 + açıklama (altı uçta) |
| KT9.2 | 3.145 geometri > 500 → 400 + daraltma önerisi (kırpma değil) |
| KT9.3 | Üç geometri ucu geçerli `FeatureCollection` |
| KT9.4 | bbox → `ST_Intersects`: 327 → 3 hücre |
| KT9.5 | k=5: 315 hücre döndü, 11 bastırıldı; her hücre ≥ 5 abone |
| KT9.6 | Her istek `audit_log`'a, `principal='gateway'` |
| KT9.7 | `svc_gateway` → `ground_truth` → permission denied |

---

## 5. Kabul kriterlerinin durumu

| Kriter | Durum |
|---|---|
| **KT9** | ✅ yedi madde de geçti |
| K6 | ✅ (S0'da kanıtlandı) — gateway beşinci uygulama noktası oldu |
| K7 | ✅ (S6'da ölçüldü, değişmedi) |
| K1 | ❌ negatif bulgu (S5, plan gereği) |
| K2–K5, K8 | ✅ değişmedi |
| K9, K10 | S8'e devredildi |

---

## 6. Sprint değerlendirmesi

En değerli sonuç: kör test API katmanına kadar uzatıldı. Yeni bir okuma
yüzeyi açılırken en kolay yol `hts_admin` ile bağlanmak olurdu; bunun yerine
`svc_gateway` rolü `ground_truth`'suz tanımlandı ve bu artık KT9.7 ile
**teste bağlı**, niyet beyanı değil.

Sprint 6'da ADR'ler ölçümden önce yazılmıştı; Sprint 7'de aynı disiplin plan
boşluklarına uygulandı: KT9 tanımsız olduğu için kod yazılmadan önce
tanımlandı, sonra ölçüldü.

**Commit:** `9dc10ef` — `feat(gateway): E06 API Gateway ve E07 görselleştirme (KT9)`
