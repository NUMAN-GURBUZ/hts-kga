# ADR-01 — Referansiyel Bütünlük (TimescaleDB Kısıtları)

**Durum:** Kabul edildi  
**Tarih:** 2026-07-22  
**Sprint:** 0 (T-E01-05, T-E01-06, T-E01-12)  
**İlgili bulgular:** K-01, EK-02

---

## Karar

TimescaleDB kısıtları nedeniyle klasik FK zinciri kurulmaz. Üç katmanlı koruma:

### 1. Hypertable UNIQUE Constraint'leri

TimescaleDB zorunlu kısıtı: **UNIQUE constraint bölümleme sütununu içermek zorundadır.**

| Tablo | UNIQUE İndeksi |
|---|---|
| `hts_records` | `(run_id, event_id, time)` |
| `ground_truth` | `(run_id, event_id, time)` |
| `estimates` | `(run_id, event_id, method, confidence, time)` |

> `confidence` NULL olamaz; B0/B1 için **sentinel değer `-1`** kullanılır.  
> (NULL'lar UNIQUE'te çakışmaz → veri bozulmasına yol açar)

### 2. Deterministik `event_id` (UUIDv5)

Rastgele UUID yerine:
```
event_id = UUIDv5(namespace, run_id ‖ agent_id ‖ tick_index ‖ event_seq)
```
Aynı seed → aynı `event_id`. Simülatör bir olayı iki kez yayınlarsa UNIQUE ihlali oluşur; sessiz bozulma olmaz.

### 3. Bütünlük Denetim Sorgusu

Her koşun sonunda `make verify-integrity` çalıştırılır:
```sql
SELECT count(*) FROM hts_records h
LEFT JOIN ground_truth g USING (run_id, event_id)
WHERE g.event_id IS NULL AND h.run_id = :run;
-- 0 dönmeli
```

## Teknik Düzeltme (analizin atladığı ayrıntı)

- Hypertable'dan **normal tabloya FK kurulabilir** (örn. `cells.run_id → run_config.run_id`)
- **Hypertable'a referans veren FK kurulamaz**  
- UNIQUE constraint **mutlaka bölümleme sütununu (time) içermelidir**

## Reddedilen Alternatif

Normal (non-hypertable) tablolara geçmek — zaman bölümleme kazancı kaybolurdu.
