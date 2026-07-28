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

---

## Ek A — `event_id` UUIDv5 Sözleşmesi (T-E02-14, 2026-07-27)

**Namespace (dondurulmuş):**

```
29b15779-62b7-4b8d-889e-417227237e61
```

Bir kez rastgele (v4) üretilmiştir. Platformun sabitidir; senaryoya, ortama
veya koşuya göre değişmez. Kodda `internal/simulator/event`
(`namespaceLiteral`) içinde tutulur ve yeniden atanamaz.

**Ad kodlaması (dondurulmuş, 32 bayt, ayırıcısız):**

| Konum | Genişlik | Alan | Biçim |
|---|---|---|---|
| 0–15 | 16 | `run_id` | UUID ham baytlar |
| 16–19 | 4 | `agent_id` | uint32 big endian |
| 20–27 | 8 | `tick_index` | uint64 big endian |
| 28–31 | 4 | `event_seq` | uint32 big endian |

Sabit genişlik zorunludur: değişken genişlikli bir kodlamada (ör. ondalık
metin) tick 0'daki ajan 1 ile tick 10'daki ajan 0 aynı adı üretir ve kimlik
tekliği çöker.

**Altın vektörler** (`run_id = 11111111-2222-4333-8444-555555555555`):

| agent | tick | seq | `event_id` |
|---|---|---|---|
| 42 | 8639 | 0 | `6e89a177-fb74-5d45-aa75-cd6d47b2a41f` |
| 42 | 8639 | 1 | `3afa79dc-78e3-51c6-a136-7fde9d5c7220` |
| 1 | 0 | 0 | `f97a725f-773d-522a-a4f3-19799e145606` |
| 0 | 1 | 0 | `e2445eb0-ef03-5bae-8784-d8c6a6320579` |

Bu tablonun değişmesi, üretilmiş her olayın kimliğinin değişmesi demektir:
`estimates ⋈ ground_truth` bağı kopar, K10 tekrarlanabilirliği geçersiz olur.
`TestEventIDGoldenVector` bekçilik eder.
