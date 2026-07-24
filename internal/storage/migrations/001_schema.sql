-- ==============================================================================
-- Migration 001 — Şema temeli
-- T-E01-05 · ADR-05 (run_id) · BÖLÜM D (veri modeli)
--
-- Tüm non-hypertable tablolar burada oluşturulur.
-- Hypertable dönüşümleri + UNIQUE + index → 002_hypertables.sql
-- ==============================================================================

-- PostGIS ve TimescaleDB eklentilerini etkinleştir
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS timescaledb CASCADE;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";   -- uuid_generate_v4/v5 için

-- ==============================================================================
-- KOŞU KAYDI (ADR-05)
-- Normal tablo — referans noktası, diğer tablolarda FK buraya
-- ==============================================================================
CREATE TABLE IF NOT EXISTS run_config (
    run_id        UUID PRIMARY KEY,
    scenario      CHAR(1)      NOT NULL CHECK (scenario IN ('A','B','C','D')),
    morphology    VARCHAR(10)  NOT NULL CHECK (morphology IN ('urban','rural')),
    seed          BIGINT       NOT NULL,
    git_sha       VARCHAR(40)  NOT NULL,
    lambda        FLOAT,                          -- kalibre edilen λ (ADR-02); NULL = henüz kalibre edilmedi
    started_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    finished_at   TIMESTAMPTZ,
    config_yaml   JSONB        NOT NULL           -- tam senaryo config'i
);

COMMENT ON TABLE  run_config IS 'Her simülasyon koşusunun meta verisi (ADR-05)';
COMMENT ON COLUMN run_config.lambda IS 'Kalibre edilen belirsizlik ölçek katsayısı λ (ADR-02); NULL = kalibrasyon henüz yapılmadı';

-- ==============================================================================
-- HÜCRE ENVANTERİ
-- Normal tablo — run_config'e FK
-- ==============================================================================
CREATE TABLE IF NOT EXISTS cells (
    cell_id     UUID         PRIMARY KEY,
    run_id      UUID         NOT NULL REFERENCES run_config(run_id) ON DELETE CASCADE,
    site_id     UUID         NOT NULL,
    azimuth     FLOAT        NOT NULL CHECK (azimuth >= 0 AND azimuth < 360),
    beam_width  FLOAT        NOT NULL CHECK (beam_width > 0 AND beam_width <= 360),
    freq_mhz    INTEGER      NOT NULL CHECK (freq_mhz > 0),
    eirp_dbm    FLOAT        NOT NULL,
    ant_height  FLOAT        NOT NULL CHECK (ant_height > 0),
    tilt_deg    FLOAT        NOT NULL,
    r_max_m     FLOAT        NOT NULL CHECK (r_max_m > 0),  -- link budget önhesabı
    morphology  VARCHAR(10)  NOT NULL CHECK (morphology IN ('urban','rural')),
    model_type  VARCHAR(5)   NOT NULL CHECK (model_type IN ('UMa','UMi','RMa')),
    location    GEOGRAPHY(POINT, 4326) NOT NULL
);

COMMENT ON TABLE  cells IS 'Baz istasyonu sektör envanteri; koşu başında Redis''e de yüklenir';
COMMENT ON COLUMN cells.r_max_m IS 'Link budget baz alınarak hesaplanan maksimum kapsama yarıçapı (metre)';

CREATE INDEX IF NOT EXISTS cells_location_gist  ON cells USING GIST (location);
CREATE INDEX IF NOT EXISTS cells_run_site       ON cells (run_id, site_id);    -- E-04

-- ==============================================================================
-- METRİKLER (toplulaştırılmış doğrulama çıktıları)
-- Normal tablo — run_config'e FK
-- ==============================================================================
CREATE TABLE IF NOT EXISTS metrics (
    metric_id           BIGSERIAL    PRIMARY KEY,
    run_id              UUID         NOT NULL REFERENCES run_config(run_id) ON DELETE CASCADE,
    computed_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    scenario            CHAR(1)      NOT NULL CHECK (scenario IN ('A','B','C','D')),
    method              CHAR(2)      NOT NULL CHECK (method IN ('B0','B1','M')),
    confidence          FLOAT        NOT NULL,
    partition_key       CHAR(1)      NOT NULL CHECK (partition_key IN ('C','V')),
    n_events            INTEGER      NOT NULL CHECK (n_events > 0),
    coverage_rate       FLOAT        NOT NULL CHECK (coverage_rate BETWEEN 0 AND 1),
    median_area_km2     FLOAT        NOT NULL CHECK (median_area_km2 > 0),
    p90_area_km2        FLOAT        NOT NULL CHECK (p90_area_km2 > 0),
    median_haversine_m  FLOAT        NOT NULL CHECK (median_haversine_m >= 0),
    r50_m               FLOAT        NOT NULL CHECK (r50_m >= 0),
    r95_m               FLOAT        NOT NULL CHECK (r95_m >= 0),
    median_part_count   INTEGER      NOT NULL CHECK (median_part_count >= 1),
    p95_part_count      INTEGER      NOT NULL CHECK (p95_part_count >= 1),  -- K8 eşiği: ≤ 3
    repaired_ratio      FLOAT        NOT NULL CHECK (repaired_ratio BETWEEN 0 AND 1),
    reduction_vs_b0     FLOAT,       -- NULL = karşılaştırma yok (B0 kendisiyle karşılaştırılmaz)
    reduction_vs_b1     FLOAT
);

COMMENT ON TABLE  metrics IS 'S3b toplu doğrulama çıktıları; K1–K5 kriterlerinin dayanağı';
COMMENT ON COLUMN metrics.p95_part_count IS 'K8 kriteri: p95_part_count ≤ 3 olmalı';

CREATE INDEX IF NOT EXISTS metrics_run_method ON metrics (run_id, scenario, method, confidence);

-- ==============================================================================
-- BÜTÜNLÜK BULGULARI
-- Normal tablo — run_config'e FK
-- ==============================================================================
CREATE TABLE IF NOT EXISTS integrity_findings (
    finding_id  BIGSERIAL    PRIMARY KEY,
    run_id      UUID         NOT NULL,     -- run_config'e FK, cascade gereksiz (bulgular korunabilir)
    event_id    UUID         NOT NULL,
    time        TIMESTAMPTZ  NOT NULL,
    rule_id     INTEGER      NOT NULL CHECK (rule_id BETWEEN 1 AND 5),
    rule_name   VARCHAR(50)  NOT NULL,
    margin      FLOAT        NOT NULL,     -- ADR-09: eşiğin aşım miktarı
    evidence    JSONB        NOT NULL,     -- adli açıklanabilirlik
    scenario    CHAR(1)      NOT NULL CHECK (scenario IN ('A','B','C','D'))
);

COMMENT ON TABLE  integrity_findings IS 'S4 bütünlük denetim bulguları; ground_truth ile S3b karşılaştırır';
COMMENT ON COLUMN integrity_findings.margin IS 'Kural eşiğinin aşım miktarı (örn: gerçek hız / 300 km/h)';
COMMENT ON COLUMN integrity_findings.evidence IS 'Kuralı tetikleyen somut değerler; adli açıklanabilirlik için';

CREATE INDEX IF NOT EXISTS integrity_findings_run_rule  ON integrity_findings (run_id, rule_id);
CREATE INDEX IF NOT EXISTS integrity_findings_run_event ON integrity_findings (run_id, event_id);

-- ==============================================================================
-- SERVİS ERİŞİM GÜNLÜĞÜ (ADR-15)
-- Normal tablo
-- ==============================================================================
CREATE TABLE IF NOT EXISTS audit_log (
    audit_id  BIGSERIAL    PRIMARY KEY,
    time      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    run_id    UUID,                        -- NULL olabilir (başlangıç olayları için)
    principal VARCHAR(40)  NOT NULL,       -- 'analysis-engine' / 'validation' / 'integrity' / 'gateway'
    action    VARCHAR(40)  NOT NULL,       -- 'read_estimates' / 'write_metrics' / 'api_query' ...
    resource  VARCHAR(60)  NOT NULL,
    detail    JSONB
);

COMMENT ON TABLE  audit_log IS 'Servis kimliği bazlı erişim günlüğü (ADR-15); kullanıcı değil principal takibi';
COMMENT ON COLUMN audit_log.principal IS 'PostgreSQL rolü veya Kafka principal adı';

CREATE INDEX IF NOT EXISTS audit_log_run_time ON audit_log (run_id, time DESC);
