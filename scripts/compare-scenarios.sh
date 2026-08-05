#!/usr/bin/env bash
# T-E04-08 — Dört senaryonun karşılaştırmalı sonuç tablosu.
#
# `metrics` tablosundan K1–K3 ve K8 sayılarını çeker. Yalnızca
# partition_key='V' satırları raporlanır (K4): kalibrasyon kümesinde ölçülen
# bir sayı bilimsel iddiaya giremez.
#
# Kullanım: scripts/compare-scenarios.sh [çıktı_dizini]
set -euo pipefail

cd "$(dirname "$0")/.."
OUTDIR="${1:-docs/results}"

set -a
# shellcheck disable=SC1091
. ./.env
set +a

psql_q() {
    PGPASSWORD="$POSTGRES_PASSWORD" psql -h localhost -U "$POSTGRES_USER" \
        -d "$POSTGRES_DB" -X -q "$@"
}

RUN_IDS=()
for f in "$OUTDIR"/*.run_id; do
    [ -f "$f" ] || continue
    RUN_IDS+=("$(cat "$f")")
done

if [ ${#RUN_IDS[@]} -eq 0 ]; then
    echo "koşu bulunamadı: $OUTDIR/*.run_id"
    exit 1
fi

LIST=$(printf "'%s'," "${RUN_IDS[@]}")
LIST="${LIST%,}"

echo "═══ K1 · Kapsama (M@90, 'V' kümesi) — eşik %85–95 ═══"
psql_q -c "
SELECT r.scenario,
       round(m.coverage_rate::numeric, 4)  AS kapsama,
       m.n_events                          AS olay,
       round(r.lambda::numeric, 4)         AS lambda
  FROM metrics m JOIN run_config r USING (run_id)
 WHERE m.run_id IN ($LIST) AND m.method='M' AND m.confidence=0.90
   AND m.partition_key='V'
 ORDER BY r.scenario;"

echo "═══ K2/K3 · Alan daralması (M@90 vs B0/B1) ═══"
psql_q -c "
SELECT r.scenario,
       round(m.median_area_km2::numeric, 4)   AS m90_alan_km2,
       round((100*m.reduction_vs_b0)::numeric, 2) AS b0_daralma_yuzde,
       round((100*m.reduction_vs_b1)::numeric, 2) AS b1_daralma_yuzde
  FROM metrics m JOIN run_config r USING (run_id)
 WHERE m.run_id IN ($LIST) AND m.method='M' AND m.confidence=0.90
   AND m.partition_key='V'
 ORDER BY r.scenario;"

echo "═══ Taban çizgileri ve tüm yöntemler ═══"
psql_q -c "
SELECT r.scenario, m.method, m.confidence,
       m.n_events                             AS olay,
       round(m.coverage_rate::numeric, 4)     AS kapsama,
       round(m.median_area_km2::numeric, 4)   AS medyan_alan_km2,
       round(m.r50_m::numeric, 0)             AS r50_m,
       round(m.r95_m::numeric, 0)             AS r95_m
  FROM metrics m JOIN run_config r USING (run_id)
 WHERE m.run_id IN ($LIST) AND m.partition_key='V'
 ORDER BY r.scenario, m.method, m.confidence;"

echo "═══ K8 · Geometri kararlılığı (yöntem başına) ═══"
psql_q -c "
SELECT r.scenario, m.method, m.confidence,
       m.median_part_count AS medyan_parca,
       m.p95_part_count    AS p95_parca,
       round(m.repaired_ratio::numeric, 6) AS onarim_orani
  FROM metrics m JOIN run_config r USING (run_id)
 WHERE m.run_id IN ($LIST) AND m.partition_key='V'
 ORDER BY r.scenario, m.method, m.confidence;"

echo "═══ Koşu hacimleri ve bütünlük ═══"
psql_q -c "
SELECT r.scenario, r.seed,
       r.published_events AS yayinlanan,
       r.analyzed_events  AS analiz_edilen,
       (SELECT count(*) FROM hts_records h WHERE h.run_id=r.run_id)  AS kayit,
       (SELECT count(*) FROM ground_truth g WHERE g.run_id=r.run_id) AS ground_truth,
       (SELECT count(*) FROM estimates e WHERE e.run_id=r.run_id)    AS tahmin
  FROM run_config r
 WHERE r.run_id IN ($LIST)
 ORDER BY r.scenario;"
