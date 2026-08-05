#!/usr/bin/env bash
# K10 — tekrarlanabilirlik doğrulaması (ADR-34/K10, ADR-35).
#
# Kullanım:
#   scripts/verify-k10.sh <run_id_a> <run_id_b>
#
# İki run_id, `ground_truth.agent_id + time` üzerinden eşleştirilir
# (event_id run_id'ye bağımlı olduğundan doğrudan karşılaştırılamaz —
# ADR-01). `EXCEPT ALL` çokküme farkı alır; float alanlar dahil satır-tam
# eşitlik ister.
set -euo pipefail

RUN_A="${1:?kullanım: verify-k10.sh <run_id_a> <run_id_b>}"
RUN_B="${2:?kullanım: verify-k10.sh <run_id_a> <run_id_b>}"

cd "$(dirname "$0")/.."
set -a
# shellcheck disable=SC1091
. ./.env
set +a

PGPASSWORD="$POSTGRES_PASSWORD" psql -h localhost -U "$POSTGRES_USER" -d "$POSTGRES_DB" <<SQL
WITH a AS (
  SELECT g.agent_id, g.time, e.method, e.confidence, e.area_km2, e.part_count,
         ST_AsText(e.centroid) AS centroid_wkt, ST_AsText(e.geometry) AS geom_wkt
  FROM ground_truth g JOIN estimates e ON e.run_id=g.run_id AND e.event_id=g.event_id
  WHERE g.run_id = '$RUN_A'
),
b AS (
  SELECT g.agent_id, g.time, e.method, e.confidence, e.area_km2, e.part_count,
         ST_AsText(e.centroid) AS centroid_wkt, ST_AsText(e.geometry) AS geom_wkt
  FROM ground_truth g JOIN estimates e ON e.run_id=g.run_id AND e.event_id=g.event_id
  WHERE g.run_id = '$RUN_B'
)
SELECT
  (SELECT count(*) FROM a) AS a_rows,
  (SELECT count(*) FROM b) AS b_rows,
  (SELECT count(*) FROM (SELECT * FROM a EXCEPT ALL SELECT * FROM b) d1) AS only_in_a,
  (SELECT count(*) FROM (SELECT * FROM b EXCEPT ALL SELECT * FROM a) d2) AS only_in_b,
  CASE WHEN (SELECT count(*) FROM (SELECT * FROM a EXCEPT ALL SELECT * FROM b) x)
          + (SELECT count(*) FROM (SELECT * FROM b EXCEPT ALL SELECT * FROM a) y) = 0
       THEN '✅ K10: bit-identical'
       ELSE '⚠️  K10: fark var (ADR-35, kalıntı kayan nokta gürültüsü bekleniyor — büyüklüğü kontrol edin)'
  END AS sonuc;
SQL
