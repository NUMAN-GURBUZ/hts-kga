-- ==============================================================================
-- Migration 005 — Koşu sayaçları ve örnekleme farkındalı bütünlük denetimi
-- ADR-23 · Sprint 5 (G1, T-E04-02)
--
-- SORUN: ADR-04'ün tamamlanma ölçütü ile ADR-14'ün örneklemesi çelişiyordu.
--
--   ADR-04: "hts_records sayısı = estimates sayısı / beklenen_yöntem_sayısı"
--   ADR-14: olayların yalnızca ~%20'si analiz edilir ('V' kümesi + kalibrasyon
--           örneği); geri kalanı için estimates satırı ÜRETİLMEZ.
--
-- İkisi aynı anda doğru olamaz. 003_integrity_fn.sql'deki denetim 3 bu
-- çelişkiyi kodlamıştı: estimates/hts_records oranını 4,5–5,5 arasında
-- bekliyordu. Örneklemeli bir koşuda oran ~1,0 çıkar ve denetim, sistem doğru
-- çalışırken WARN verir — yani yararsız hâle gelir.
--
-- KARAR (ADR-23): ölçüt, koşunun kendi beyan ettiği sayaçlara bağlanır.
--   published_events : simülatörün Kafka'ya yayınladığı hts_records sayısı
--   analyzed_events  : analiz motorunun örnekleme sonrası işlediği olay sayısı
--
-- Böylece denetim "estimates = 5 × analyzed_events" olur ve örnekleme oranı
-- ne olursa olsun anlamını korur. Sayaçlar NULL ise (eski koşular, ya da
-- henüz bitmemiş koşu) denetim SKIP döner — yanlış alarm üretmez.
-- ==============================================================================

ALTER TABLE run_config
    ADD COLUMN IF NOT EXISTS published_events BIGINT,
    ADD COLUMN IF NOT EXISTS analyzed_events  BIGINT;

COMMENT ON COLUMN run_config.published_events IS
    'ADR-23: simülatörün hts.records topic''ine yayınladığı kayıt sayısı; koşu bitiminde yazılır';
COMMENT ON COLUMN run_config.analyzed_events IS
    'ADR-23: analiz motorunun örnekleme (ADR-14) sonrası işlediği olay sayısı';

-- ==============================================================================
-- verify_integrity — denetim 3 örnekleme farkındalı hâle getirildi
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
    -- (kural 4 enjeksiyonu: olay silindi, sadece GT var — bu beklenen bir durum)
    RETURN QUERY
    SELECT
        'ground_truth_without_hts_records'::TEXT AS check_name,
        CASE WHEN count(*) = 0 THEN 'OK' ELSE 'FAIL' END AS status,
        count(*) AS count
    FROM ground_truth g
    LEFT JOIN hts_records h USING (run_id, event_id)
    WHERE g.run_id = p_run_id
      AND h.event_id IS NULL
      AND g.injected_rule IS DISTINCT FROM 4;  -- kural 4 dışındakiler gerçek hata

    -- Denetim 3: yayınlanan kayıt sayısı ile yazılan kayıt sayısı (ADR-23)
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
END;
$$;

COMMENT ON FUNCTION verify_integrity IS
    'ADR-01 + ADR-23 bütünlük denetimi: hts_records ↔ ground_truth eşleşmesi, '
    'yayınlanan/yazılan kayıt denkliği ve örnekleme farkındalı estimates oranı. '
    'FAIL = gerçek hata; SKIP = koşu sayaçları henüz yazılmamış.';
