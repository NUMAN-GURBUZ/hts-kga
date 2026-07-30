#!/usr/bin/env bash
# T-E05-08 — Bir senaryoda K7 ölçümünü uçtan uca koşturur.
#
#   simülatör → Kafka → persister'lar ∥ bütünlük akışı → bütünlük toplu → F.5
#
# ANALİZ MOTORU VE KALİBRASYON KOŞULMAZ. Gerekçe: K7, `estimates` tablosuna
# hiç dokunmaz — bütünlük denetimi kütle modelinden, kontur çıkarımından ve
# λ'dan bağımsızdır. Kalibrasyonun düz eğride 12 iterasyonu senaryo başına ~30
# dk boşa gider (Sprint 5 borcu #4) ve K7'ye hiçbir katkısı yoktur.
#
# K1–K3 ölçümü için `run-scenario.sh` kullanılır; o betik bütünlük fazlarını da
# içerir.
#
# Kullanım:
#   scripts/run-integrity.sh configs/urban_ta.yaml [çıktı_dizini]
set -euo pipefail

CONFIG="${1:?kullanım: run-integrity.sh <config.yaml> [çıktı_dizini]}"
OUTDIR="${2:-docs/results}"
IDLE="${HTS_IDLE_TIMEOUT:-30s}"

cd "$(dirname "$0")/.."
mkdir -p "$OUTDIR"

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
LOG="$OUTDIR/$LABEL.integrity.log"

echo "═══ K7 ölçümü — senaryo $SCENARIO ($LABEL) ═══" | tee "$LOG"

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

# ── 2. Kalıcılaştırma + bütünlük akış fazı (üç rol paralel, ADR-27/2) ────────
echo "→ kalıcılaştırma + bütünlük akışı (kural 1, 3)" | tee -a "$LOG"
HTS_PERSIST_MODE=records HTS_GROUP="ri-records-$RUN_ID" \
    KAFKA_SASL_USER=svc_records_persister KAFKA_SASL_PASSWORD="${KAFKA_PW_RECORDS:-records_dev_pw}" \
    go run ./cmd/persister >>"$LOG" 2>&1 &
PID_REC=$!
HTS_PERSIST_MODE=groundtruth HTS_GROUP="ri-gt-$RUN_ID" \
    KAFKA_SASL_USER=svc_gt_persister KAFKA_SASL_PASSWORD="${KAFKA_PW_GT:-gtpersister_dev_pw}" \
    go run ./cmd/persister >>"$LOG" 2>&1 &
PID_GT=$!
HTS_INTEGRITY_MODE=stream HTS_CONFIG="$CONFIG" HTS_RUN_ID="$RUN_ID" \
    HTS_GROUP="ri-integrity-$RUN_ID" \
    KAFKA_SASL_USER=svc_integrity KAFKA_SASL_PASSWORD="${KAFKA_PW_INTEGRITY:-integrity_dev_pw}" \
    go run ./cmd/integrity >>"$LOG" 2>&1 &
PID_INT=$!
wait $PID_REC $PID_GT $PID_INT

# ── 3. Bütünlük toplu fazı (kural 5, 2) ──────────────────────────────────────
echo "→ bütünlük toplu fazı (kural 5, 2)" | tee -a "$LOG"
HTS_INTEGRITY_MODE=batch HTS_CONFIG="$CONFIG" HTS_RUN_ID="$RUN_ID" \
    go run ./cmd/integrity >>"$LOG" 2>&1

# ── 4. F.5 ölçümü (K7) ───────────────────────────────────────────────────────
echo "→ F.5 precision/recall" | tee -a "$LOG"
# HTS_INTEGRITY_ONLY: K1-K3 hattı atlanır. F.5, `estimates` tablosuna hiç
# dokunmaz — analiz motorunun "tamamlandı" önkoşulu buraya uygulanmaz.
HTS_CONFIG="$CONFIG" HTS_RUN_ID="$RUN_ID" \
    HTS_CALIBRATE=false HTS_INTEGRITY_ONLY=true \
    go run ./cmd/validation 2>&1 | tee -a "$LOG"

# ── 5. Bütünlük denetimi (5 denetim) ─────────────────────────────────────────
echo "→ verify_integrity" | tee -a "$LOG"
PGPASSWORD="$POSTGRES_PASSWORD" psql -h localhost -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
    -c "SELECT * FROM verify_integrity('$RUN_ID');" 2>&1 | tee -a "$LOG"

# ── 6. K7 tablosu ────────────────────────────────────────────────────────────
PGPASSWORD="$POSTGRES_PASSWORD" psql -h localhost -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "
    SELECT rule_id, rule_name, findings, true_positives, injected,
           round(precision::numeric,4)    AS precision,
           round(precision_lo::numeric,3) AS wilson_lo,
           round(recall::numeric,4)       AS recall, sufficient,
           CASE WHEN precision IS NULL THEN 'ölçülemedi'
                WHEN NOT sufficient    THEN 'yetersiz'
                WHEN precision >= 0.90 THEN 'geçti'
                ELSE 'tutmadı' END AS k7
      FROM integrity_metrics WHERE run_id = '$RUN_ID' ORDER BY rule_id;" 2>&1 | tee -a "$LOG"

echo "$RUN_ID" > "$OUTDIR/$LABEL.integrity.run_id"
echo "✅ $LABEL K7 ölçümü tamamlandı (run_id=$RUN_ID)" | tee -a "$LOG"
