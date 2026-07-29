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
export HTS_IDLE_TIMEOUT="$IDLE"

SCENARIO=$(grep -E '^\s+scenario:' "$CONFIG" | head -1 | sed 's/.*"\(.\)".*/\1/')
LABEL=$(basename "$CONFIG" .yaml)
LOG="$OUTDIR/$LABEL.log"

echo "═══ senaryo $SCENARIO ($LABEL) ═══" | tee "$LOG"

# ── 1. Simülasyon ────────────────────────────────────────────────────────────
echo "→ simülasyon" | tee -a "$LOG"
RUN_ID=$(HTS_CONFIG="$CONFIG" go run ./cmd/simulator 2>&1 \
    | tee -a "$LOG" \
    | grep -oP 'HTS_RUN_ID=\K[0-9a-f-]+' | head -1)

if [ -z "${RUN_ID:-}" ]; then
    echo "HATA: run_id alınamadı" | tee -a "$LOG"
    exit 1
fi
echo "   run_id=$RUN_ID" | tee -a "$LOG"

# ── 2. Kalıcılaştırma (iki rol paralel) ──────────────────────────────────────
echo "→ kalıcılaştırma" | tee -a "$LOG"
HTS_PERSIST_MODE=records HTS_GROUP="run-records-$RUN_ID" \
    go run ./cmd/persister >>"$LOG" 2>&1 &
PID_REC=$!
HTS_PERSIST_MODE=groundtruth HTS_GROUP="run-gt-$RUN_ID" \
    go run ./cmd/persister >>"$LOG" 2>&1 &
PID_GT=$!
wait $PID_REC $PID_GT

# ── 3. Analiz ('V' örneklemi) ────────────────────────────────────────────────
echo "→ analiz" | tee -a "$LOG"
HTS_CONFIG="$CONFIG" HTS_RUN_ID="$RUN_ID" HTS_SAMPLE_MODE=validation \
    go run ./cmd/analysis-engine >>"$LOG" 2>&1

# ── 4. Kalibrasyon + ölçüm ───────────────────────────────────────────────────
echo "→ kalibrasyon ve ölçüm" | tee -a "$LOG"
HTS_CONFIG="$CONFIG" HTS_RUN_ID="$RUN_ID" HTS_CALIBRATE=true HTS_RECOMPUTE=true \
    go run ./cmd/validation 2>&1 | tee -a "$LOG"

# ── 5. Bütünlük denetimi ─────────────────────────────────────────────────────
echo "→ bütünlük denetimi" | tee -a "$LOG"
PGPASSWORD="$POSTGRES_PASSWORD" psql -h localhost -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
    -c "SELECT * FROM verify_integrity('$RUN_ID');" 2>&1 | tee -a "$LOG"

echo "$RUN_ID" > "$OUTDIR/$LABEL.run_id"
echo "✅ $LABEL tamamlandı (run_id=$RUN_ID)" | tee -a "$LOG"
