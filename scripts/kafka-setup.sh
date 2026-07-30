#!/usr/bin/env bash
# ==============================================================================
# scripts/kafka-setup.sh
# T-E01-08 · ADR-32 — Topic + SCRAM kimlikleri + ACL (KÖR TEST KATMAN 1)
#
# KÖR TEST KATMAN 1 (ADR-32/2):
#   svc_analysis  → hts.groundtruth → YETKİ YOK  ← yasak burada kurulur
#   svc_integrity → hts.groundtruth → YETKİ YOK  ← yasak burada kurulur
#
# Yasak DENY kuralıyla değil **yetki vermeyerek** kurulur. Kafka'nın
# `allow.everyone.if.no.acl.found` varsayılanı false'tur: ACL yoksa reddet.
# Açık bir DENY eklemek yerine ALLOW vermemek daha güçlüdür — bir gün biri
# geniş bir ALLOW eklerse DENY onu ezerdi, ama liste boşsa gözden kaçan kapı
# da olmaz.
#
# YÖNETİM PORTU (ADR-32/1, ADR-32/3):
# Bu betik **yayınlanmayan** 9094 (PLAINTEXT) üzerinden çalışır. SCRAM'ın
# tavuk-yumurta problemi böyle çözülür: kimlikler broker meta verisinde durur,
# yazmak için bağlanmak gerekir, bağlanmak için kimlik gerekir. `User:ANONYMOUS`
# yalnızca bu iç portta süper kullanıcıdır ve port dışarıya açılmaz.
# ==============================================================================
set -euo pipefail

CONTAINER="${KAFKA_CONTAINER:-hts-kafka}"
# Yönetim işlemleri iç porttan; servisler 9092'ye SASL ile bağlanır.
ADMIN_BOOTSTRAP="${KAFKA_ADMIN_BOOTSTRAP:-localhost:9094}"

k() { docker exec "$CONTAINER" "/opt/kafka/bin/$1" "${@:2}"; }

echo "=== HTS-KGA Kafka Kurulumu (ADR-32) ==="
echo "Yönetim portu: $ADMIN_BOOTSTRAP (yayınlanmaz)"

# ─── 1. SCRAM kimlikleri ──────────────────────────────────────────────────────
# Parolalar .env'den gelir; commit edilmez. Varsayılanlar geliştirme ortamı
# içindir ve .env.example'da belgelenmiştir.
add_scram() {
    local user="$1" pass="$2"
    k kafka-configs.sh --bootstrap-server "$ADMIN_BOOTSTRAP" \
        --alter --entity-type users --entity-name "$user" \
        --add-config "SCRAM-SHA-256=[password=$pass]" >/dev/null
    echo "  ✅ SCRAM kimliği: $user"
}

echo ""
echo "--- SCRAM kimlikleri ---"
add_scram svc_simulator         "${KAFKA_PW_SIMULATOR:-simulator_dev_pw}"
add_scram svc_records_persister "${KAFKA_PW_RECORDS:-records_dev_pw}"
add_scram svc_gt_persister      "${KAFKA_PW_GT:-gtpersister_dev_pw}"
add_scram svc_analysis          "${KAFKA_PW_ANALYSIS:-analysis_dev_pw}"
add_scram svc_integrity         "${KAFKA_PW_INTEGRITY:-integrity_dev_pw}"

# svc_test — entegrasyon TEST koşumu kimliği (bir servis kimliği DEĞİL).
#
# Testler hem üretir hem tüketir ve S3b rolünü de oynar (F.5 ölçümü ground
# truth okur). Tek süreçte tek kimlik kullandıkları için iki topic'te de tam
# yetki alırlar.
#
# Bu, kör testi zayıflatmaz: K6'nın iddiası `svc_analysis` ve `svc_integrity`
# principal'lerinin ground truth'a erişemediğidir ve o iddia
# `verify-isolation.sh` tarafından bu kimliklerle sınanır. `svc_test` üretim
# topolojisinde hiçbir zaman koşmaz — hangi servisin hangi kimlikle bağlandığı
# koşum betiklerinde açıkça yazılıdır.
add_scram svc_test              "${KAFKA_PW_TEST:-test_dev_pw}"

# ─── 2. Topic'ler ─────────────────────────────────────────────────────────────
create_topic() {
    local topic="$1" partitions="${2:-4}"
    k kafka-topics.sh --bootstrap-server "$ADMIN_BOOTSTRAP" \
        --create --if-not-exists --topic "$topic" \
        --partitions "$partitions" --replication-factor 1 \
        --config retention.ms=604800000 >/dev/null
    echo "  ✅ topic: $topic ($partitions partition)"
}

echo ""
echo "--- Topic'ler ---"
create_topic "hts.records"     4
create_topic "hts.groundtruth" 4

# ─── 3. ACL — en az yetki (ADR-32/2) ─────────────────────────────────────────
acl_topic() {
    local user="$1" op="$2" topic="$3"
    k kafka-acls.sh --bootstrap-server "$ADMIN_BOOTSTRAP" \
        --add --allow-principal "User:$user" \
        --operation "$op" --topic "$topic" >/dev/null 2>&1
    echo "  ✅ $user → $topic → $op"
}

# Tüketiciler kendi grupları üzerinde de READ ister. Grup adları koşu
# kimliğini taşıdığı için önek (prefixed) eşleşmesi kullanılır.
acl_group_all() {
    local user="$1"
    k kafka-acls.sh --bootstrap-server "$ADMIN_BOOTSTRAP" \
        --add --allow-principal "User:$user" \
        --operation Read --group '*' >/dev/null 2>&1
    echo "  ✅ $user → tüketici grupları → Read"
}

echo ""
echo "--- ACL: üretici ---"
acl_topic svc_simulator Write    hts.records
acl_topic svc_simulator Write    hts.groundtruth
acl_topic svc_simulator Describe hts.records
acl_topic svc_simulator Describe hts.groundtruth

echo ""
echo "--- ACL: kalıcılaştırıcılar ---"
acl_topic svc_records_persister Read     hts.records
acl_topic svc_records_persister Describe hts.records
acl_group_all svc_records_persister
acl_topic svc_gt_persister Read     hts.groundtruth
acl_topic svc_gt_persister Describe hts.groundtruth
acl_group_all svc_gt_persister

echo ""
echo "--- ACL: analiz ve bütünlük (hts.records YALNIZCA) ---"
acl_topic svc_analysis  Read     hts.records
acl_topic svc_analysis  Describe hts.records
acl_group_all svc_analysis
acl_topic svc_integrity Read     hts.records
acl_topic svc_integrity Describe hts.records
acl_group_all svc_integrity

echo ""
echo "--- ACL: entegrasyon testi kimliği (üretim topolojisinde koşmaz) ---"
for op in Read Write Describe; do
    acl_topic svc_test "$op" hts.records
    acl_topic svc_test "$op" hts.groundtruth
done
acl_group_all svc_test

echo ""
echo "  ⛔ svc_analysis  → hts.groundtruth : KAYIT YOK (kör test katman 1)"
echo "  ⛔ svc_integrity → hts.groundtruth : KAYIT YOK (kör test katman 1)"

# ─── 4. Doğrulama ─────────────────────────────────────────────────────────────
echo ""
echo "=== hts.groundtruth üzerindeki ACL listesi ==="
k kafka-acls.sh --bootstrap-server "$ADMIN_BOOTSTRAP" --list --topic hts.groundtruth 2>/dev/null || true

echo ""
echo "=== Topic listesi ==="
k kafka-topics.sh --bootstrap-server "$ADMIN_BOOTSTRAP" --list

echo ""
echo "✅ Kafka kurulumu tamamlandı. Kör test katman 1 kurulu."
echo "   Doğrulama: make verify-isolation"
