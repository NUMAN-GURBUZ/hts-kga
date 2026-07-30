#!/usr/bin/env bash
# T-E04-08 — Tek bir senaryoyu uçtan uca koşturur.
#
#   simülatör → Kafka → persister'lar → analiz motoru → kalibrasyon → ölçüm
#
# Kullanım:
#   scripts/run-scenario.sh configs/urban_ta.yaml [çıktı_dizini]
#
# Tüketiciler `HTS_IDLE_TIMEOUT` ile akış bitince kendiliğinden çıkar; betik
# "ne zaman durdurayım" tahmini yapmaz.
set -euo pipefail

CONFIG="${1:?kullanım: run-scenario.sh <config.yaml> [çıktı_dizini]}"
OUTDIR="${2:-docs/results}"
IDLE="${HTS_IDLE_TIMEOUT:-20s}"

cd "$(dirname "$0")/.."
mkdir -p "$OUTDIR"

# .env yüklenir (POSTGRES_*, KAFKA_BROKERS, REDIS_ADDR, HMAC_SALT)
set -a
# shellcheck disable=SC1091
. ./.env
set +a

# ── Kafka kimlikleri (ADR-32) ────────────────────────────────────────────────
# Her servis KENDİ principal'ıyla bağlanır. Ortak bir kimlik kullanılsaydı
# svc_analysis ile svc_gt_persister ayırt edilemez ve kör test katman 1
# kurulamazdı.
export HTS_IDLE_TIMEOUT="$IDLE"

SCENARIO=$(grep -E '^\s+scenario:' "$CONFIG" | head -1 | sed 's/.*"\(.\)".*/\1/')
LABEL=$(basename "$CONFIG" .yaml)
LOG="$OUTDIR/$LABEL.log"

echo "═══ senaryo $SCENARIO ($LABEL) ═══" | tee "$LOG"

# ── 1. Simülasyon ────────────────────────────────────────────────────────────
echo "→ simülasyon" | tee -a "$LOG"
RUN_ID=$(HTS_CONFIG="$CONFIG" \
    KAFKA_SASL_USER=svc_simulator KAFKA_SASL_PASSWORD="${KAFKA_PW_SIMULATOR:-simulator_dev_pw}" \
    go run ./cmd/simulator 2>&1 \
    | tee -a "$LOG" \
    | grep -oP 'HTS_RUN_ID=\K[0-9a-f-]+' | head -1)

if [ -z "${RUN_ID:-}" ]; then
    echo "HATA: run_id alınamadı" | tee -a "$LOG"
    exit 1
fi
echo "   run_id=$RUN_ID" | tee -a "$LOG"

# ── 2. Kalıcılaştırma + bütünlük akış fazı (üç rol paralel) ──────────────────
#
# Bütünlük akış fazı persister'larla PARALEL koşar (ADR-27/2): ayrı tüketici
# grubu, aynı topic. Kazanç yalnızca duvar saati değil — canlı akış üzerinde
# bütünlük izlemek, "koşu bittikten sonra bir SQL sorgusu yazdık" iddiasından
# bilimsel olarak farklı bir şeydir.
echo "→ kalıcılaştırma + bütünlük akışı (kural 1, 3)" | tee -a "$LOG"
HTS_PERSIST_MODE=records HTS_GROUP="run-records-$RUN_ID" \
    KAFKA_SASL_USER=svc_records_persister KAFKA_SASL_PASSWORD="${KAFKA_PW_RECORDS:-records_dev_pw}" \
    go run ./cmd/persister >>"$LOG" 2>&1 &
PID_REC=$!
HTS_PERSIST_MODE=groundtruth HTS_GROUP="run-gt-$RUN_ID" \
    KAFKA_SASL_USER=svc_gt_persister KAFKA_SASL_PASSWORD="${KAFKA_PW_GT:-gtpersister_dev_pw}" \
    go run ./cmd/persister >>"$LOG" 2>&1 &
PID_GT=$!
HTS_INTEGRITY_MODE=stream HTS_CONFIG="$CONFIG" HTS_RUN_ID="$RUN_ID" \
    HTS_GROUP="run-integrity-$RUN_ID" \
    KAFKA_SASL_USER=svc_integrity KAFKA_SASL_PASSWORD="${KAFKA_PW_INTEGRITY:-integrity_dev_pw}" \
    go run ./cmd/integrity >>"$LOG" 2>&1 &
PID_INT=$!
wait $PID_REC $PID_GT $PID_INT

# ── 2b. Bütünlük toplu fazı (kural 5, 2) ─────────────────────────────────────
#
# Önkoşul: hts_records tam VE akış fazı bitti (inspected_records yazılı).
# Sağlanmazsa faz REDDEDER — eksik taleple koşmak düşük recall'u bilimsel bulgu
# gibi gösterirdi (ADR-27/3).
echo "→ bütünlük toplu fazı (kural 5, 2)" | tee -a "$LOG"
HTS_INTEGRITY_MODE=batch HTS_CONFIG="$CONFIG" HTS_RUN_ID="$RUN_ID" \
    go run ./cmd/integrity >>"$LOG" 2>&1

# ── 3. Analiz ('V' örneklemi) ────────────────────────────────────────────────
echo "→ analiz" | tee -a "$LOG"
HTS_CONFIG="$CONFIG" HTS_RUN_ID="$RUN_ID" HTS_SAMPLE_MODE=validation \
    KAFKA_SASL_USER=svc_analysis KAFKA_SASL_PASSWORD="${KAFKA_PW_ANALYSIS:-analysis_dev_pw}" \
    go run ./cmd/analysis-engine >>"$LOG" 2>&1

# ── 4. Kalibrasyon + ölçüm ───────────────────────────────────────────────────
echo "→ kalibrasyon ve ölçüm (F.1-F.5)" | tee -a "$LOG"
# HTS_CALIBRATE varsayılan olarak kapalıdır: bütünlük ölçümü λ'dan bağımsızdır
# ve düz eğride 12 iterasyon ~30 dk boşa gider (Sprint 5 borcu #4).
# Kalibrasyon istenirse HTS_CALIBRATE=true ile açılır.
HTS_CONFIG="$CONFIG" HTS_RUN_ID="$RUN_ID" \
    HTS_CALIBRATE="${HTS_CALIBRATE:-false}" HTS_RECOMPUTE=true HTS_INTEGRITY=true \
    go run ./cmd/validation 2>&1 | tee -a "$LOG"

# ── 5. Bütünlük denetimi ─────────────────────────────────────────────────────
echo "→ bütünlük denetimi" | tee -a "$LOG"
PGPASSWORD="$POSTGRES_PASSWORD" psql -h localhost -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
    -c "SELECT * FROM verify_integrity('$RUN_ID');" 2>&1 | tee -a "$LOG"

echo "$RUN_ID" > "$OUTDIR/$LABEL.run_id"
echo "✅ $LABEL tamamlandı (run_id=$RUN_ID)" | tee -a "$LOG"
