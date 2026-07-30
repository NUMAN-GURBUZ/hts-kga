-- ==============================================================================
-- Migration 006 — Bütünlük tespiti: idempotanslık, bastırma etiketi ve ölçüm
-- ADR-28 · ADR-30 · ADR-31 · Sprint 6 (T-E05-01)
--
-- Bu dosya dört sessiz bozulma yolunu kapatır. Hiçbiri hata mesajı üretmez;
-- hepsi K7'yi yanlış bir sayıya götürür.
--
--   1) YİNELENEN BULGU
--      Kafka at-least-once semantiğinde tüketici çökerse parti yeniden
--      işlenir. integrity_findings'te tekillik kısıtı YOKTU: aynı bulgu iki kez
--      yazılır, F.5'in precision denominatörü şişer ve ölçüm SESSİZCE düşer.
--      → UNIQUE (run_id, event_id, rule_id) + yazıcıda ON CONFLICT DO NOTHING
--
--   2) SONSUZ margin
--      Δt = 0 ve mesafe > 0 durumunda ima edilen hız sonsuzdur. Go'nun
--      json.Marshal'ı +Inf için HATA döner ve bulgu hiç yazılmaz. Kentsel
--      koşuda >300 km/h isabetlerin TAMAMI bu popülasyondadır.
--      → CHECK (margin BETWEEN 1.0 AND 1e6); uygulama kapılan değeri
--        evidence.capped = true ile işaretler
--
--   3) BASTIRMANIN BİLİMİ GİZLEMESİ
--      ADR-28 çapraz bulaşmayı öncelikle çözüyor, ama bastırılan isabet
--      atılırsa karışıklık matrisi üretilemez ve "precision'ı filtreyle
--      şişirdiniz" itirazı cevaplanamaz.
--      → suppressed_by sütunu: bastırma bir ETİKET, filtre değil.
--        F.5 kanonik ölçümü suppressed_by IS NULL kullanır (ölçümden önce beyan)
--
--   4) YARIM S4 KOŞUSU
--      Idle eşiği kısa kalırsa akış fazı ortada çıkar; düşük recall bilimsel
--      bulgu gibi görünür. Sprint 5 hatası #4 (analiz tüketicisi senaryo D'de
--      3.000 yerine 225 olay işledi) S4'te birebir tekrar edebilirdi.
--      → run_config.inspected_records + verify_integrity 5. denetimi
--
-- NEDEN integrity_findings HYPERTABLE DEĞİL: EK-02 gereği hypertable'da
-- tekillik kısıtı bölümleme sütununu (time) içermek ZORUNDADIR. Hypertable
-- olsaydı tekillik (run_id, event_id, rule_id, time) olurdu ve 1) numaralı
-- koruma kurulamazdı. Bulgular seyrektir (~4.000/koşu); zaman bölümlemesinin
-- kazancı yoktur. Normal tablo olması bilinçli bir karardır.
--
-- YENİDEN KOŞULABİLİRLİK: make migrate-up tüm *.sql dosyalarını her seferinde
-- uygular. Her adım IF NOT EXISTS ya da pg_catalog denetimiyle korunmuştur.
-- PostgreSQL'de ADD CONSTRAINT IF NOT EXISTS YOKTUR; 004_roles.sql'in DO bloğu
-- deseni kullanılır.
-- ==============================================================================

-- ==============================================================================
-- 1) integrity_findings — yeni sütunlar (ADR-28)
-- ==============================================================================
ALTER TABLE integrity_findings
    ADD COLUMN IF NOT EXISTS detected_in   VARCHAR(6) NOT NULL DEFAULT 'stream',
    ADD COLUMN IF NOT EXISTS suppressed_by INTEGER;

COMMENT ON COLUMN integrity_findings.detected_in IS
    'ADR-27: bulguyu üreten faz (stream/batch); kural sınıfı ihlali veriden de görülür';
COMMENT ON COLUMN integrity_findings.suppressed_by IS
    'ADR-28: öncelik yarışını kazanan kuralın kimliği. NULL = kanonik bulgu. '
    'Bastırma bir etikettir, filtre değil: F.5 kanonik ölçümü IS NULL süzer, '
    'karışıklık matrisi tüm satırları kullanır.';

-- ==============================================================================
-- 2) Kısıtlar (ADR-31)
-- ==============================================================================
DO $$
BEGIN
    -- Tekillik: bir kural, bir olay için en çok bir isabet üretir.
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'integrity_findings'::regclass
          AND conname  = 'integrity_findings_uniq'
    ) THEN
        ALTER TABLE integrity_findings
            ADD CONSTRAINT integrity_findings_uniq
            UNIQUE (run_id, event_id, rule_id);
    END IF;

    -- margin sözleşmesi: alt sınır 1,0 (eşik aşılmadan bulgu yazılmaz),
    -- üst sınır 1e6 (Δt=0 → sonsuz hız kapılır).
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'integrity_findings'::regclass
          AND conname  = 'integrity_findings_margin_range'
    ) THEN
        ALTER TABLE integrity_findings
            ADD CONSTRAINT integrity_findings_margin_range
            CHECK (margin >= 1.0 AND margin <= 1e6);
    END IF;

    -- Bastırma tutarlılığı: bir kural kendisini bastıramaz.
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'integrity_findings'::regclass
          AND conname  = 'integrity_findings_suppression_sane'
    ) THEN
        ALTER TABLE integrity_findings
            ADD CONSTRAINT integrity_findings_suppression_sane
            CHECK (suppressed_by IS NULL
                   OR (suppressed_by BETWEEN 1 AND 5 AND suppressed_by <> rule_id));
    END IF;

    -- Kanıt sürümlü olmalı: şemasız JSONB ile "adli açıklanabilirlik"
    -- savunulamaz ve şema sonradan geliştirilemez.
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'integrity_findings'::regclass
          AND conname  = 'integrity_findings_evidence_versioned'
    ) THEN
        ALTER TABLE integrity_findings
            ADD CONSTRAINT integrity_findings_evidence_versioned
            CHECK (evidence ? 'v');
    END IF;

    -- detected_in alan kümesi.
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'integrity_findings'::regclass
          AND conname  = 'integrity_findings_detected_in_valid'
    ) THEN
        ALTER TABLE integrity_findings
            ADD CONSTRAINT integrity_findings_detected_in_valid
            CHECK (detected_in IN ('stream','batch'));
    END IF;
END
$$;

-- F.5 kanonik sorgusunun sıcak yolu (ADR-28).
CREATE INDEX IF NOT EXISTS integrity_findings_canonical
    ON integrity_findings (run_id, rule_id)
    WHERE suppressed_by IS NULL;

-- ==============================================================================
-- 3) Tamlık sayacı (ADR-31/7, ADR-23 simetrisi)
-- ==============================================================================
ALTER TABLE run_config
    ADD COLUMN IF NOT EXISTS inspected_records BIGINT;

COMMENT ON COLUMN run_config.inspected_records IS
    'ADR-31: bütünlük akış fazının incelediği hts_records sayısı; koşu bitiminde '
    'yazılır. published_events''e eşit olmalı — yarım koşan bir S4''ün düşük '
    'recall''u böylece bilimsel bulgu gibi görünemez.';

-- ==============================================================================
-- 4) K7 ölçüm tablosu (ADR-31/8)
--
-- metrics tablosunun anahtarı (scenario, method, confidence, partition_key) —
-- kural bazlı satırları taşıyamaz. Ayrı tablo gerekiyor.
-- ==============================================================================
CREATE TABLE IF NOT EXISTS integrity_metrics (
    metric_id      BIGSERIAL   PRIMARY KEY,
    run_id         UUID        NOT NULL,
    computed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    scenario       CHAR(1)     NOT NULL CHECK (scenario IN ('A','B','C','D')),
    rule_id        INTEGER     NOT NULL CHECK (rule_id BETWEEN 1 AND 5),
    rule_name      VARCHAR(50) NOT NULL,

    findings       BIGINT      NOT NULL CHECK (findings       >= 0),
    true_positives BIGINT      NOT NULL CHECK (true_positives >= 0),
    injected       BIGINT      NOT NULL CHECK (injected       >= 0),

    -- NULL = ölçülemedi (kanonik bulgu yok, ya da kural olay-çıpalı değil).
    -- ADR-30: kural 4 daima NULL.
    precision      FLOAT       CHECK (precision    IS NULL OR precision    BETWEEN 0 AND 1),
    recall         FLOAT       NOT NULL CHECK (recall BETWEEN 0 AND 1),
    precision_lo   FLOAT       CHECK (precision_lo IS NULL OR precision_lo BETWEEN 0 AND 1),
    precision_hi   FLOAT       CHECK (precision_hi IS NULL OR precision_hi BETWEEN 0 AND 1),

    -- ADR-31/9: findings >= min_findings_for_threshold (30). false ise K7
    -- eşiği UYGULANMAZ; precision Wilson aralığıyla ve "istatistiksel olarak
    -- yetersiz" etiketiyle raporlanır.
    sufficient     BOOLEAN     NOT NULL,

    CONSTRAINT integrity_metrics_uniq UNIQUE (run_id, rule_id),
    CONSTRAINT integrity_metrics_tp_le_findings CHECK (true_positives <= findings)
);

COMMENT ON TABLE integrity_metrics IS
    'ADR-31: F.5 çıktısı — kural bazında precision/recall. Kanonik bulgular '
    'üzerinde hesaplanır (integrity_findings.suppressed_by IS NULL).';

CREATE INDEX IF NOT EXISTS integrity_metrics_run ON integrity_metrics (run_id, rule_id);

-- ==============================================================================
-- 5) Rol yetkileri (ADR-31, BÖLÜM D eksiği E-5)
-- ==============================================================================
-- svc_integrity: kendi bulgularını okur (ADR-28 talep defteri devri) ve
-- tamlık sayacını yazar. UPDATE yalnızca TEK SÜTUNDA verilir: bütünlük
-- servisinin koşu yapılandırmasını değiştirmesi için hiçbir gerekçe yok.
GRANT UPDATE (inspected_records) ON run_config TO svc_integrity;

-- svc_validation: F.5'i hesaplar ve yazar (etiketi yalnızca o görebilir).
GRANT SELECT, INSERT, UPDATE ON integrity_metrics TO svc_validation;
GRANT USAGE  ON SEQUENCE integrity_metrics_metric_id_seq TO svc_validation;

-- DİKKAT: svc_integrity'ye integrity_findings üzerinde UPDATE/DELETE
-- VERİLMEZ. Bulgu tablosu ekle-yalnız bir adli kayıttır; yinelenenler
-- ON CONFLICT DO NOTHING ile önlenir, silinerek değil.

-- ==============================================================================
-- 6) verify_integrity — 5. denetim (ADR-31/7)
-- ==============================================================================
CREATE OR REPLACE FUNCTION verify_integrity(p_run_id UUID)
RETURNS TABLE (
    check_name  TEXT,
    status      TEXT,
    count       BIGINT
) LANGUAGE plpgsql AS $$
BEGIN
    -- Denetim 1: hts_records'ta olup ground_truth'ta olmayan event_id'ler
    RETURN QUERY
    SELECT
        'hts_records_without_ground_truth'::TEXT AS check_name,
        CASE WHEN count(*) = 0 THEN 'OK' ELSE 'FAIL' END AS status,
        count(*) AS count
    FROM hts_records h
    LEFT JOIN ground_truth g USING (run_id, event_id)
    WHERE h.run_id = p_run_id
      AND g.event_id IS NULL;

    -- Denetim 2: ground_truth'ta olup hts_records'ta olmayan event_id'ler
    -- (kural 4 enjeksiyonu: olay silindi, sadece GT var — beklenen durum)
    RETURN QUERY
    SELECT
        'ground_truth_without_hts_records'::TEXT AS check_name,
        CASE WHEN count(*) = 0 THEN 'OK' ELSE 'FAIL' END AS status,
        count(*) AS count
    FROM ground_truth g
    LEFT JOIN hts_records h USING (run_id, event_id)
    WHERE g.run_id = p_run_id
      AND h.event_id IS NULL
      AND g.injected_rule IS DISTINCT FROM 4;

    -- Denetim 3: yayınlanan kayıt sayısı = yazılan kayıt sayısı (ADR-23)
    RETURN QUERY
    WITH declared AS (
        SELECT published_events FROM run_config WHERE run_id = p_run_id
    ),
    stored AS (
        SELECT count(*) AS n FROM hts_records WHERE run_id = p_run_id
    )
    SELECT
        'published_vs_stored_records'::TEXT AS check_name,
        CASE
            WHEN declared.published_events IS NULL THEN 'SKIP'
            WHEN declared.published_events = stored.n THEN 'OK'
            ELSE 'FAIL'
        END AS status,
        stored.n AS count
    FROM declared, stored;

    -- Denetim 4: estimates sayısı = 5 × analiz edilen olay (ADR-14 örneklemesi)
    RETURN QUERY
    WITH declared AS (
        SELECT analyzed_events FROM run_config WHERE run_id = p_run_id
    ),
    est AS (
        SELECT count(*) AS n FROM estimates WHERE run_id = p_run_id
    )
    SELECT
        'estimates_per_analyzed_event'::TEXT AS check_name,
        CASE
            WHEN declared.analyzed_events IS NULL OR declared.analyzed_events = 0 THEN 'SKIP'
            WHEN est.n = 5 * declared.analyzed_events THEN 'OK'
            ELSE 'FAIL'
        END AS status,
        est.n AS count
    FROM declared, est;

    -- Denetim 5: bütünlük akış fazı tüm kayıtları inceledi mi (ADR-31)
    --
    -- S4 örnekleme YAPMAZ (ADR-27/4): kural 2/3/5 abone dizisine bağlıdır ve
    -- F.5'in recall denominatörü tüm enjekte olaylardır. Dolayısıyla incelenen
    -- kayıt sayısı yayınlanan sayıya EŞİT olmalıdır — az olması yarım koşudur.
    --
    -- SKIP: S4 bu koşuda hiç koşmadı (Sprint 5'in dört koşusu böyledir;
    -- denetim onlarda yanlış alarm üretmemeli).
    RETURN QUERY
    WITH declared AS (
        SELECT published_events, inspected_records FROM run_config WHERE run_id = p_run_id
    )
    SELECT
        'inspected_vs_published_records'::TEXT AS check_name,
        CASE
            WHEN declared.inspected_records IS NULL THEN 'SKIP'
            WHEN declared.published_events  IS NULL THEN 'SKIP'
            WHEN declared.inspected_records = declared.published_events THEN 'OK'
            ELSE 'FAIL'
        END AS status,
        coalesce(declared.inspected_records, 0) AS count
    FROM declared;
END;
$$;

COMMENT ON FUNCTION verify_integrity IS
    'ADR-01 + ADR-23 + ADR-31 bütünlük denetimi: hts_records ↔ ground_truth '
    'eşleşmesi, yayınlanan/yazılan kayıt denkliği, örnekleme farkındalı '
    'estimates oranı ve bütünlük akış fazının tamlığı. '
    'FAIL = gerçek hata; SKIP = ilgili sayaç henüz yazılmamış.';
