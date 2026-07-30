-- ==============================================================================
-- Migration 007 — API Gateway rolü (S5)
-- T-E06 · ADR-33/2 · BÖLÜM D
--
-- BOŞLUK: BÖLÜM D, audit_log.principal sütununu
--   'analysis-engine' / 'validation' / 'integrity' / 'gateway'
-- diye belgeliyor ama migration 004 yalnızca DÖRT rol oluşturuyor —
-- svc_gateway yok. Gateway'in hangi tablolara erişebileceği tanımsızdı.
--
-- KARAR (ADR-33/2): salt okuma, ground_truth HARİÇ.
--
-- Gateway ground_truth'u GÖREMEZ. Gerçek konum, modelin doğruluğunu ÖLÇMEK
-- için vardır ve o ölçüm `metrics` tablosunda toplulaştırılmış hâlde zaten
-- yayınlanıyor. Tek tek gerçek konumları API'den servis etmek, sistemin
-- dışarıya "bu abone tam olarak buradaydı" demesi olurdu — ki bu, çalışmanın
-- modellediği HTS verisinde VAR OLMAYAN bir bilgidir.
--
-- Böylece kör test (K6) gateway'e kadar uzar: altı sprintlik yatırım, yeni bir
-- okuma yüzeyi açılırken korunur.
-- ==============================================================================

DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'svc_gateway') THEN
        CREATE ROLE svc_gateway LOGIN PASSWORD 'gateway_dev_pw';
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO svc_gateway;

-- Önce her şeyi geri al: rol yeniden çalıştırmada birikmiş yetki taşımasın.
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM svc_gateway;

-- Salt okuma yüzeyi (ADR-33/3'teki altı + bir uç bunlardan beslenir).
GRANT SELECT ON
    run_config,
    cells,
    hts_records,
    estimates,
    metrics,
    integrity_findings,
    integrity_metrics   TO svc_gateway;

-- Denetim izi (ADR-15): her API isteği bir satır yazar.
GRANT INSERT ON audit_log TO svc_gateway;
GRANT USAGE  ON SEQUENCE audit_log_audit_id_seq TO svc_gateway;

-- ground_truth: YETKİ YOK (ADR-33/2, kör test)
--   Açık bir REVOKE yazılmaz — yukarıdaki REVOKE ALL yeterlidir ve yetki
--   listesinde hiç kayıt olmaması, bir DENY satırından daha güçlüdür
--   (ADR-32/2 ile aynı gerekçe).

COMMENT ON TABLE audit_log IS
    'Servis kimliği bazlı erişim günlüğü (ADR-15). Gateway her API isteğini '
    'buraya yazar (ADR-33/1 KT9.6); principal sütunu servis adını taşır.';
