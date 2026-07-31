#!/usr/bin/env bash
# T-E09 / K9 — Docker Compose replika ölçeklemesi (ADR-16, ADR-34/4).
#
# Kullanım:
#   scripts/measure-k9.sh <replika_sayısı> <run_id> <yayınlanan_olay_sayısı>
#
# Her çağrıdan önce `hts-analysis-engine` consumer group'u en baştan
# okunacak şekilde sıfırlanır — bu, ardışık ölçümlerin hepsinin AYNI toplam
# mesaj hacmini (yabancı koşuların atlanması dâhil) taraması anlamına gelir;
# aksi hâlde sonraki ölçümler önceki ölçümlerin zaten ilerlettiği offset'ten
# başlar ve daha az iş yaparak haksız yere hızlı görünürdü.
set -euo pipefail

N="${1:?kullanım: measure-k9.sh <replika> <run_id> <yayınlanan_olay>}"
RUN_ID="${2:?run_id zorunlu}"
PUBLISHED="${3:?yayınlanan olay sayısı zorunlu}"

cd "$(dirname "$0")/.."
DC="docker compose -f deployments/compose/docker-compose.yml"

echo "→ consumer group sıfırlanıyor (en baştan)"
docker exec hts-kafka /opt/kafka/bin/kafka-consumer-groups.sh \
    --bootstrap-server localhost:9094 --group hts-analysis-engine \
    --topic hts.records --reset-offsets --to-earliest --execute >/dev/null 2>&1 || true

echo "→ $N replika, run_id=$RUN_ID"
START=$(date +%s.%N)
HTS_RUN_ID="$RUN_ID" $DC --profile scale-test up \
    --scale analysis-engine="$N" analysis-engine
END=$(date +%s.%N)

$DC --profile scale-test rm -f analysis-engine >/dev/null 2>&1 || true

ELAPSED=$(echo "$END - $START" | bc)
THROUGHPUT=$(echo "scale=2; $PUBLISHED / $ELAPSED" | bc)
echo "REPLICAS=$N RUN_ID=$RUN_ID ELAPSED_S=$ELAPSED PUBLISHED=$PUBLISHED THROUGHPUT_EPS=$THROUGHPUT"
