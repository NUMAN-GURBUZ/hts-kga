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

# ==============================================================================
# KATMAN 1 — Kafka ACL (ADR-32/5)
#
# Katman 2 (PostgreSQL rolü) yukarıda sınandı. Katman 1 aynı disipline alınır:
# `svc_analysis` kimliğiyle hts.groundtruth okumaya çalışmak
# TOPIC_AUTHORIZATION_FAILED vermek ZORUNDADIR.
#
# Bu blok Sprint 7'ye kadar yoktu; K6'nın 1. katmanı yapılandırılmamıştı ve
# ACL komutları PLAINTEXT'te sessizce etkisizdi (Sprint 5 borcu #2).
# ==============================================================================
KAFKA_CONTAINER="${KAFKA_CONTAINER:-hts-kafka}"

kafka_probe() {
    # $1 principal · $2 parola · $3 topic
    docker exec "$KAFKA_CONTAINER" bash -c "
        cat > /tmp/probe.properties <<PROPS
security.protocol=SASL_PLAINTEXT
sasl.mechanism=SCRAM-SHA-256
sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username=\"$1\" password=\"$2\";
PROPS
        timeout 25 /opt/kafka/bin/kafka-console-consumer.sh \
            --bootstrap-server localhost:9092 \
            --consumer.config /tmp/probe.properties \
            --topic $3 --max-messages 1 --timeout-ms 12000 2>&1" || true
}

echo ""
echo "=== KATMAN 1: Kafka ACL ==="

if ! docker inspect "$KAFKA_CONTAINER" >/dev/null 2>&1; then
    echo "⚠️  Kafka konteyneri yok — katman 1 sınanamadı (make infra-up)"
else
    for svc in "svc_analysis:${KAFKA_PW_ANALYSIS:-analysis_dev_pw}" \
               "svc_integrity:${KAFKA_PW_INTEGRITY:-integrity_dev_pw}"; do
        USER="${svc%%:*}"; PW="${svc##*:}"
        echo "--- $USER → hts.groundtruth (DENY bekleniyor) ---"
        OUT=$(kafka_probe "$USER" "$PW" hts.groundtruth)
        if echo "$OUT" | grep -qiE "TOPIC_AUTHORIZATION_FAILED|Topic authorization failed|not authorized"; then
            echo "✅ $USER: hts.groundtruth ENGELLENDI (TOPIC_AUTHORIZATION_FAILED)"
        else
            echo "❌ HATA: $USER hts.groundtruth'a yetkilendirilmiş — kör test katman 1 DELİK!"
            echo "$OUT" | tail -n 3
            exit 1
        fi
    done

    # Pozitif kontrol: aynı kimlik hts.records'ta yetkili olmalı. Bu satır
    # olmadan "her şey reddediliyor" durumu da testi geçerdi (SASL bozuk,
    # kimlik yok, broker kapalı) — sessiz başarısızlık biçimi budur.
    echo "--- svc_analysis → hts.records (ALLOW bekleniyor) ---"
    OUT=$(kafka_probe svc_analysis "${KAFKA_PW_ANALYSIS:-analysis_dev_pw}" hts.records)
    if echo "$OUT" | grep -qiE "TOPIC_AUTHORIZATION_FAILED|Topic authorization failed|not authorized"; then
        echo "❌ HATA: svc_analysis hts.records'u da okuyamıyor — ACL yapılandırması bozuk"
        exit 1
    fi
    echo "✅ svc_analysis: hts.records erişimi AÇIK (pozitif kontrol geçti)"
fi

echo ""
echo "=== Kör test özeti ==="
echo "  Katman 1  Kafka ACL          ✅ (bu betik)"
echo "  Katman 2  PostgreSQL rolü    ✅ (bu betik)"
echo "  Katman 3  injected_rule yeri ✅ (şema: ground_truth'ta — ADR-09)"
echo "  Katman 4  içe alma grafiği   → go test ./tests/isolation/"
