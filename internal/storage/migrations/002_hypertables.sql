-- ==============================================================================
-- Migration 002 — Hypertable'lar + UNIQUE (time dahil) + indeksler
-- T-E01-06 · ADR-01 (referansiyel bütünlük) · EK-02 (UNIQUE bölümleme sütunu)
--
-- TimescaleDB kısıtı: hypertable UNIQUE constraint bölümleme sütununu
-- (time) içermek zorundadır. Bu dosya bu kısıtı yerine getirir.
-- ==============================================================================

-- ==============================================================================
-- HTS KAYITLARI — Hypertable (bölümleme: time, 1 günlük chunk)
-- ==============================================================================
CREATE TABLE IF NOT EXISTS hts_records (
    run_id        UUID         NOT NULL,
    event_id      UUID         NOT NULL,   -- UUIDv5, deterministik (ADR-01)
    time          TIMESTAMPTZ  NOT NULL,   -- bölümleme sütunu
    pseudo_msisdn VARCHAR(64)  NOT NULL,   -- HMAC-SHA256 (E-08)
    pseudo_imei   VARCHAR(64),             -- NULL olabilir
    event_type    VARCHAR(8)   NOT NULL CHECK (event_type IN ('MOC','MTC','SMS','DATA','IDLE')),
    cell_id       UUID         NOT NULL,
    ta_value      INTEGER,                 -- NULL = TA yok (LTE harici veya kapsama dışı)
    scenario      CHAR(1)      NOT NULL CHECK (scenario IN ('A','B','C','D'))
);

-- Hypertable dönüşümü — önceden tablo boş olmalı
SELECT create_hypertable(
    'hts_records',
    'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists       => TRUE
);

-- ADR-01 / EK-02: UNIQUE bölümleme sütununu (time) içermek zorunda
CREATE UNIQUE INDEX IF NOT EXISTS hts_records_unique
    ON hts_records (run_id, event_id, time);

-- Sorgu indeksleri — E-03
CREATE INDEX IF NOT EXISTS hts_records_msisdn_time
    ON hts_records (run_id, pseudo_msisdn, time DESC);
CREATE INDEX IF NOT EXISTS hts_records_scenario_time
    ON hts_records (run_id, scenario, time DESC);      -- E-03: senaryo bazlı sorgular

-- ==============================================================================
-- GROUND TRUTH — Hypertable (bölümleme: time, 1 günlük chunk)
-- ACL: svc_analysis, svc_integrity GÖREMEZ → 004_roles.sql
-- ==============================================================================
CREATE TABLE IF NOT EXISTS ground_truth (
    run_id        UUID         NOT NULL,
    event_id      UUID         NOT NULL,
    time          TIMESTAMPTZ  NOT NULL,   -- bölümleme sütunu
    agent_id      INTEGER      NOT NULL,
    true_location GEOGRAPHY(POINT, 4326) NOT NULL,
    covered       BOOLEAN      NOT NULL DEFAULT TRUE,   -- ADR-08 kapsama dışı işareti
    partition_key CHAR(1)      NOT NULL CHECK (partition_key IN ('C','V')),  -- 'C'=kalibrasyon, 'V'=doğrulama
    injected_rule INTEGER                              -- ADR-09: NULL=temiz, 1..5=enjekte
                  CHECK (injected_rule IS NULL OR injected_rule BETWEEN 1 AND 5)
);

SELECT create_hypertable(
    'ground_truth',
    'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists       => TRUE
);

-- ADR-01 / EK-02
CREATE UNIQUE INDEX IF NOT EXISTS ground_truth_unique
    ON ground_truth (run_id, event_id, time);

CREATE INDEX IF NOT EXISTS ground_truth_partition_time
    ON ground_truth (run_id, partition_key, time DESC);
CREATE INDEX IF NOT EXISTS ground_truth_location_gist
    ON ground_truth USING GIST (true_location);

COMMENT ON COLUMN ground_truth.partition_key IS 'C=kalibrasyon (80%), V=doğrulama (20%) — olay bazlı deterministik split (ADR-14)';
COMMENT ON COLUMN ground_truth.injected_rule IS 'ADR-09: NULL=gerçek olay, 1..5=hangi enjeksiyon kuralıyla bozuldu';

-- ==============================================================================
-- TAHMİNLER — Hypertable (bölümleme: time, 1 günlük chunk)
-- ==============================================================================
CREATE TABLE IF NOT EXISTS estimates (
    run_id      UUID         NOT NULL,
    event_id    UUID         NOT NULL,
    time        TIMESTAMPTZ  NOT NULL,   -- bölümleme sütunu
    method      CHAR(2)      NOT NULL CHECK (method IN ('B0','B1','M')),
    confidence  FLOAT        NOT NULL,   -- B0/B1 = -1 sentinel (ADR-01); M = .50/.90/.95
    geometry    GEOGRAPHY(MULTIPOLYGON, 4326) NOT NULL,
    centroid    GEOGRAPHY(POINT, 4326)        NOT NULL,  -- ADR-10 kütle ağırlıklı merkez
    area_km2    FLOAT        NOT NULL CHECK (area_km2 > 0),  -- ST_Area(GEOGRAPHY)/1e6
    part_count  INTEGER      NOT NULL CHECK (part_count >= 1),
    repaired    BOOLEAN      NOT NULL DEFAULT FALSE,         -- ST_MakeValid uygulandıysa
    ta_used     BOOLEAN      NOT NULL DEFAULT FALSE,         -- TA halkası kullanıldıysa (T-E03-08b)
    scenario    CHAR(1)      NOT NULL CHECK (scenario IN ('A','B','C','D'))
);

SELECT create_hypertable(
    'estimates',
    'time',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists       => TRUE
);

-- ADR-01 / EK-02: confidence NULL olamaz (sentinel -1); bölümleme sütunu dahil
CREATE UNIQUE INDEX IF NOT EXISTS estimates_unique
    ON estimates (run_id, event_id, method, confidence, time);

CREATE INDEX IF NOT EXISTS estimates_geometry_gist
    ON estimates USING GIST (geometry);
CREATE INDEX IF NOT EXISTS estimates_run_method
    ON estimates (run_id, method, confidence);

COMMENT ON COLUMN estimates.confidence IS 'B0/B1 için -1 sentinel (NULL UNIQUE sorunu → ADR-01/EK-02); M için 0.50/0.90/0.95';
COMMENT ON COLUMN estimates.centroid IS 'Kütle ağırlıklı merkez: Σ(mass_i·p_i)/Σmass_i → WGS84 (ADR-10)';
COMMENT ON COLUMN estimates.ta_used IS 'TA halkası kesişimi geçerliyse TRUE, aksi hâlde FALSE (fallback)';
