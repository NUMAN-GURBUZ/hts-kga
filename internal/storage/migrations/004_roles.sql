-- ==============================================================================
-- Migration 004 — Rol yetkilendirmesi
-- T-E01-07 · ADR-09 (kör test) · BÖLÜM D (rol yetkilendirmesi)
--
-- ÜÇ KATMANLI KÖR TEST (K6):
--   Katman 1: Kafka ACL → S2/S4 hts.groundtruth topic'ini okuyamaz (T-E01-08)
--   Katman 2: PostgreSQL rol → svc_analysis/svc_integrity ground_truth tablosuna erişemez (BU DOSYA)
--   Katman 3: injected_rule kolonu ground_truth'ta → S4 zaten tabloya erişemiyor, göremez
-- ==============================================================================

-- Rolleri oluştur (varsa atla)
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'svc_analysis') THEN
        CREATE ROLE svc_analysis LOGIN PASSWORD 'analysis_dev_pw';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'svc_integrity') THEN
        CREATE ROLE svc_integrity LOGIN PASSWORD 'integrity_dev_pw';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'svc_validation') THEN
        CREATE ROLE svc_validation LOGIN PASSWORD 'validation_dev_pw';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'svc_gt_persister') THEN
        CREATE ROLE svc_gt_persister LOGIN PASSWORD 'gtpersister_dev_pw';
    END IF;
END
$$;

-- ==============================================================================
-- 1) Şema seviyesi yetkiler (PG 15+ için zorunlu)
GRANT USAGE ON SCHEMA public TO svc_analysis;
GRANT USAGE ON SCHEMA public TO svc_integrity;
GRANT USAGE ON SCHEMA public TO svc_validation;
GRANT USAGE ON SCHEMA public TO svc_gt_persister;

-- ==============================================================================
-- 2) svc_analysis: Analiz Motoru (S2)
-- ground_truth GÖREMEZ — KÖR TEST KATMANI 2
-- ==============================================================================
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM svc_analysis;
GRANT SELECT          ON cells, hts_records, run_config TO svc_analysis;
GRANT SELECT, INSERT  ON estimates                       TO svc_analysis;
GRANT INSERT          ON audit_log                       TO svc_analysis;
-- ground_truth: erişim YOK (REVOKE ALL yeterli)

-- ==============================================================================
-- svc_integrity: Bütünlük Denetimi (S4)
-- ground_truth GÖREMEZ — KÖR TEST KATMANI 2
-- ==============================================================================
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM svc_integrity;
GRANT SELECT          ON cells, hts_records, run_config TO svc_integrity;
GRANT SELECT, INSERT  ON integrity_findings              TO svc_integrity;
GRANT INSERT          ON audit_log                       TO svc_integrity;
-- ground_truth: erişim YOK (REVOKE ALL yeterli)

-- ==============================================================================
-- svc_validation: Doğrulama (S3b)
-- Tüm tabloları görebilir (injected_rule dahil) — kör test bu rolde kırılmaz
-- Çünkü S3b, bilerek ground_truth'u görebilmeli (precision/recall hesabı)
-- ==============================================================================
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM svc_validation;
GRANT SELECT ON
    ground_truth,
    estimates,
    hts_records,
    cells,
    integrity_findings,
    run_config              TO svc_validation;
GRANT SELECT, INSERT ON metrics TO svc_validation;
GRANT INSERT         ON audit_log TO svc_validation;

-- ==============================================================================
-- svc_gt_persister: Ground Truth Persister (S3a)
-- Yalnızca ground_truth'a yazar; başka tabloya dokunmaz
-- ==============================================================================
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM svc_gt_persister;
GRANT SELECT, INSERT ON ground_truth TO svc_gt_persister;
GRANT SELECT         ON run_config   TO svc_gt_persister;   -- koşun var olup olmadığını kontrol eder
GRANT INSERT         ON audit_log    TO svc_gt_persister;

-- SEQUENCE erişimleri (BIGSERIAL olan tablolar için)
GRANT USAGE ON SEQUENCE metrics_metric_id_seq           TO svc_validation;
GRANT USAGE ON SEQUENCE integrity_findings_finding_id_seq TO svc_integrity;
GRANT USAGE ON SEQUENCE audit_log_audit_id_seq          TO svc_analysis, svc_integrity, svc_validation, svc_gt_persister;

-- Doğrulama: hangi rolün ne görebildiği
-- make verify-isolation bu kontrolü çalıştırır
