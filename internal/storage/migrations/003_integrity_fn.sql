-- ==============================================================================
-- Migration 003 — Bütünlük doğrulama fonksiyonu
-- T-E01-12 · ADR-01: verify-integrity make hedefinin SQL tarafı
-- ==============================================================================

-- hts_records ↔ ground_truth eşleşme denetimi
-- Her koşun sonunda `make verify-integrity` bu fonksiyonu çağırır.
-- Dönüş: 0 = OK, >0 = eşleşmeyen kayıt sayısı (hata)
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
        'INFO'::TEXT AS status,   -- enjeksiyon kural 4 → beklenen
        count(*) AS count
    FROM ground_truth g
    LEFT JOIN hts_records h USING (run_id, event_id)
    WHERE g.run_id = p_run_id
      AND h.event_id IS NULL
      AND g.injected_rule IS DISTINCT FROM 4;  -- kural 4 dışındakiler gerçek hata

    -- Denetim 3: estimates sayısı beklenti kontrolü (her olaya 5 tahmin: B0+B1+M@50/90/95)
    RETURN QUERY
    WITH hts_count AS (
        SELECT count(*) AS n FROM hts_records WHERE run_id = p_run_id
    ),
    est_count AS (
        SELECT count(*) AS n FROM estimates WHERE run_id = p_run_id
    )
    SELECT
        'estimates_count_ratio'::TEXT AS check_name,
        CASE
            WHEN hts_count.n = 0 THEN 'SKIP'
            WHEN est_count.n::FLOAT / hts_count.n BETWEEN 4.5 AND 5.5 THEN 'OK'
            ELSE 'WARN'
        END AS status,
        est_count.n AS count
    FROM hts_count, est_count;
END;
$$;

COMMENT ON FUNCTION verify_integrity IS
    'ADR-01 bütünlük denetimi: hts_records ↔ ground_truth eşleşme + estimates oran kontrolü. '
    'make verify-integrity tarafından çağrılır.';
