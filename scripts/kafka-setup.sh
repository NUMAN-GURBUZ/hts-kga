#!/usr/bin/env bash
# ==============================================================================
# scripts/kafka-setup.sh
# T-E01-08 — Kafka topic oluşturma + ACL (KÖR TEST KATMAN 1)
#
# KÖR TEST KATMANI 1:
#   svc_analysis  → hts.groundtruth CONSUME → DENY
#   svc_integrity → hts.groundtruth CONSUME → DENY
#   svc_gt_persister → hts.groundtruth PRODUCE/CONSUME → ALLOW
#   svc_analysis  → hts.records CONSUME → ALLOW
#
# NOT: Apache Kafka 3.9 KRaft modunda, SASL devre dışıysa ACL'ler
#      uygulanmaz (authorizer.class.name gerekli). Geliştirme ortamında
#      ACL komutları çalışır ama PLAINTEXT'te zorunlu değildir.
#      Üretim/CI ortamında SASL_PLAINTEXT + KafkaACLAuthorizer etkinleştirilmeli.
# ==============================================================================
set -euo pipefail

BOOTSTRAP="${KAFKA_BROKERS:-localhost:9092}"
CONTAINER="${KAFKA_CONTAINER:-hts-kafka}"

echo "=== HTS-KGA Kafka Kurulumu ==="
echo "Bootstrap: $BOOTSTRAP"

# ─── Topic oluşturma ──────────────────────────────────────────────────────────
create_topic() {
    local topic="$1"
    local partitions="${2:-4}"
    echo "Topic oluşturuluyor: $topic (partitions=$partitions)"
    docker exec "$CONTAINER" /opt/kafka/bin/kafka-topics.sh \
        --bootstrap-server "$BOOTSTRAP" \
        --create \
        --if-not-exists \
        --topic "$topic" \
        --partitions "$partitions" \
        --replication-factor 1 \
        --config retention.ms=604800000   # 7 gün
}

create_topic "hts.records"     4
create_topic "hts.groundtruth" 4

# ─── ACL — Kör test Katman 1 ─────────────────────────────────────────────────
# SASL etkin ortamda bu komutlar geçerli; PLAINTEXT'te log'a düşer.
echo ""
echo "=== ACL Yapılandırması (SASL etkin ortam için) ==="

# svc_analysis → hts.records → ALLOW (analiz motoru kayıtları okur)
docker exec "$CONTAINER" /opt/kafka/bin/kafka-acls.sh \
    --bootstrap-server "$BOOTSTRAP" \
    --add \
    --allow-principal "User:svc_analysis" \
    --operation Read \
    --topic hts.records \
    2>/dev/null && echo "✅ svc_analysis → hts.records ALLOW" || echo "ℹ️  ACL komutu (SASL kapalı olabilir)"

# svc_integrity → hts.records → ALLOW (bütünlük denetimi kayıtları okur)
docker exec "$CONTAINER" /opt/kafka/bin/kafka-acls.sh \
    --bootstrap-server "$BOOTSTRAP" \
    --add \
    --allow-principal "User:svc_integrity" \
    --operation Read \
    --topic hts.records \
    2>/dev/null && echo "✅ svc_integrity → hts.records ALLOW" || true

# svc_gt_persister → hts.groundtruth → ALLOW (ground truth persister)
docker exec "$CONTAINER" /opt/kafka/bin/kafka-acls.sh \
    --bootstrap-server "$BOOTSTRAP" \
    --add \
    --allow-principal "User:svc_gt_persister" \
    --operation Read \
    --topic hts.groundtruth \
    2>/dev/null && echo "✅ svc_gt_persister → hts.groundtruth ALLOW" || true

# svc_analysis → hts.groundtruth → DENY (KÖR TEST)
docker exec "$CONTAINER" /opt/kafka/bin/kafka-acls.sh \
    --bootstrap-server "$BOOTSTRAP" \
    --add \
    --deny-principal "User:svc_analysis" \
    --operation Read \
    --topic hts.groundtruth \
    2>/dev/null && echo "✅ svc_analysis → hts.groundtruth DENY (kör test)" || true

# svc_integrity → hts.groundtruth → DENY (KÖR TEST)
docker exec "$CONTAINER" /opt/kafka/bin/kafka-acls.sh \
    --bootstrap-server "$BOOTSTRAP" \
    --add \
    --deny-principal "User:svc_integrity" \
    --operation Read \
    --topic hts.groundtruth \
    2>/dev/null && echo "✅ svc_integrity → hts.groundtruth DENY (kör test)" || true

echo ""
echo "=== Topic Listesi ==="
docker exec "$CONTAINER" /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server "$BOOTSTRAP" \
    --list

echo ""
echo "✅ Kafka kurulumu tamamlandı."
