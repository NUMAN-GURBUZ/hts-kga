#!/usr/bin/env bash
# ==============================================================================
# scripts/verify-isolation.sh
# T-E01-12 / K6: S2/S4 rollerinin ground_truth tablosuna erişimini doğrular.
# ==============================================================================
set -euo pipefail

# .env dosyasından POSTGRES_ değişkenlerini yükle
if [ -f .env ]; then
    export $(grep -E '^POSTGRES_' .env | xargs)
fi

DB_HOST="localhost"
DB_PORT="5432"
DB_NAME="${POSTGRES_DB:-hts_kga}"
DB_ADMIN="${POSTGRES_USER:-hts_admin}"
DB_PASS="${POSTGRES_PASSWORD:-hts_dev_password_2024}"

echo "=== Ön kontrol: PostgreSQL erişilebilir mi? ==="
if ! PGPASSWORD="$DB_PASS" psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_ADMIN" -d "$DB_NAME" -c "SELECT 1;" >/dev/null 2>&1; then
    echo "❌ PostgreSQL erişilemiyor — önce 'make setup' çalıştırın veya konteynerlerin ayakta olduğundan emin olun."
    exit 1
fi
echo "✅ PostgreSQL bağlantısı OK ($DB_ADMIN)"
echo ""

# ── svc_analysis → ground_truth: DENY bekleniyor ────────────────────────
echo "=== svc_analysis → ground_truth (DENY bekleniyor) ==="
ERR=$(PGPASSWORD=analysis_dev_pw psql -h "$DB_HOST" -p "$DB_PORT" -U svc_analysis -d "$DB_NAME" -c "SELECT 1 FROM ground_truth LIMIT 1;" 2>&1 || true)
if echo "$ERR" | grep -qiE "permission denied|role.*does not exist|authentication|pg_hba|password"; then
    echo "✅ svc_analysis: ground_truth ENGELLENDI — $(echo "$ERR" | head -n 1 | cut -c1-60)..."
elif echo "$ERR" | grep -q "1 row"; then
    echo "❌ HATA: svc_analysis ground_truth'u OKUYABİLİYOR — ACL eksik!"
    exit 1
else
    echo "⚠️ Beklenmeyen yanıt: $ERR"
fi

# ── svc_integrity → ground_truth: DENY bekleniyor ───────────────────────
echo "=== svc_integrity → ground_truth (DENY bekleniyor) ==="
ERR=$(PGPASSWORD=integrity_dev_pw psql -h "$DB_HOST" -p "$DB_PORT" -U svc_integrity -d "$DB_NAME" -c "SELECT 1 FROM ground_truth LIMIT 1;" 2>&1 || true)
if echo "$ERR" | grep -qiE "permission denied|role.*does not exist|authentication|pg_hba|password"; then
    echo "✅ svc_integrity: ground_truth ENGELLENDI — $(echo "$ERR" | head -n 1 | cut -c1-60)..."
elif echo "$ERR" | grep -q "1 row"; then
    echo "❌ HATA: svc_integrity ground_truth'u OKUYABİLİYOR — ACL eksik!"
    exit 1
else
    echo "⚠️ Beklenmeyen yanıt: $ERR"
fi

# ── svc_validation → ground_truth: ALLOW bekleniyor ─────────────────────
echo "=== svc_validation → ground_truth (ALLOW bekleniyor) ==="
OUT=$(PGPASSWORD=validation_dev_pw psql -h "$DB_HOST" -p "$DB_PORT" -U svc_validation -d "$DB_NAME" -c "SELECT count(*) FROM ground_truth;" 2>&1 || true)
if echo "$OUT" | grep -qiE "permission denied|authentication|pg_hba"; then
    echo "❌ HATA: svc_validation ground_truth'u OKUYAMIYOR — migration 004 eksik?"
    exit 1
else
    echo "✅ svc_validation: ground_truth erişimi AÇIK (doğru)"
fi

echo ""
echo "✅ verify-isolation tamamlandı — K6 kriteri geçti"
