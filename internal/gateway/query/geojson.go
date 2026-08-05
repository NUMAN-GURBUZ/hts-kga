// GeoJSON üreten sorgular (T-E06-04, ADR-12/2, ADR-13).
//
// # GeoJSON veritabanında üretilir
//
// ADR-12/2: `ST_AsGeoJSON` + `json_build_object`. Uygulama katmanında geometri
// dönüşüm kodu yazılmaz — PostGIS'in ürettiği metni çözüp yeniden kurmak, hem
// gereksiz hem hataya açık olurdu.
//
// # Sınır önce sayılır, sonra çekilir
//
// Her uç iki sorgu koşar: önce `count(*)`, sonra (sınır geçilirse) veri.
// Tersi de yapılabilirdi (LIMIT 501 ile çekip saymak), ama o zaman 500
// geometrilik bir yükü boşuna ağa çıkarmış olurduk — B0 geometrisi tek başına
// ~10 KB (ölçüldü).

package query

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// GeoJSON, bir harita ucunun yanıtıdır.
type GeoJSON struct {
	// FeatureCollection, PostGIS'ten gelen GeoJSON metnidir.
	FeatureCollection string
	// Count, koleksiyondaki geometri sayısıdır.
	Count int
}

// emptyCollection, hiç geometri bulunmadığında döndürülür.
//
// `json_agg` boş kümede NULL döndürür; NULL'ı istemciye vermek yerine geçerli
// bir boş koleksiyon döndürülür — istemci tarafında özel durum kodu gerekmez.
const emptyCollection = `{"type":"FeatureCollection","features":[]}`

// Service, okuma katmanıdır.
type Service struct {
	db *pgxpool.Pool
}

// New, servisi kurar.
func New(db *pgxpool.Pool) (*Service, error) {
	if db == nil {
		return nil, fmt.Errorf("gateway sorgu katmanı: veritabanı bağlantısı zorunlu")
	}
	return &Service{db: db}, nil
}

// ─── Baz istasyonları ─────────────────────────────────────────────────────────

// Cells, koşunun hücre envanterini GeoJSON olarak döndürür.
//
// Hücreler şebeke altyapısıdır, kişi verisi değildir: k-anonimlik uygulanmaz
// (ADR-33/5).
func (s *Service) Cells(ctx context.Context, f CellsFilter) (GeoJSON, error) {
	if err := f.Validate(); err != nil {
		return GeoJSON{}, err
	}

	where, args := "c.run_id = $1::uuid", []any{f.RunID.String()}
	if f.BBox.Set {
		where += fmt.Sprintf(
			" AND ST_Intersects(c.location::geometry, ST_MakeEnvelope($%d,$%d,$%d,$%d,4326))",
			len(args)+1, len(args)+2, len(args)+3, len(args)+4)
		args = append(args, f.BBox.MinLon, f.BBox.MinLat, f.BBox.MaxLon, f.BBox.MaxLat)
	}

	n, err := s.count(ctx, "cells c", where, args)
	if err != nil {
		return GeoJSON{}, err
	}
	if err := s.checkLimit(n, "bbox ile daraltın"); err != nil {
		return GeoJSON{}, err
	}

	sql := `
        SELECT coalesce(json_build_object(
            'type','FeatureCollection',
            'features', coalesce(json_agg(json_build_object(
                'type','Feature',
                'geometry', ST_AsGeoJSON(c.location)::json,
                'properties', json_build_object(
                    'cell_id',    c.cell_id,
                    'site_id',    c.site_id,
                    'azimuth',    c.azimuth,
                    'beam_width', c.beam_width,
                    'freq_mhz',   c.freq_mhz,
                    'r_max_m',    round(c.r_max_m::numeric, 1),
                    'model_type', c.model_type))), '[]'::json)
        )::text, '` + emptyCollection + `')
        FROM cells c WHERE ` + where

	return s.collect(ctx, sql, args, n)
}

// ─── Olasılık geometrileri ────────────────────────────────────────────────────

// Estimates, bir abonenin tahmin geometrilerini GeoJSON olarak döndürür.
//
// ADR-13'ün zorunlu süzgeçleri (run_id + abone + zaman) `Validate` içinde
// denetlenir. `hts_records` ile join, abone süzgecini uygulamak içindir:
// `estimates` tablosu takma ad taşımaz.
func (s *Service) Estimates(ctx context.Context, f EstimatesFilter) (GeoJSON, error) {
	if err := f.Validate(); err != nil {
		return GeoJSON{}, err
	}
	window, err := f.Window.Resolve()
	if err != nil {
		return GeoJSON{}, err
	}

	from := `estimates e JOIN hts_records h
                 ON h.run_id = e.run_id AND h.event_id = e.event_id`
	where := "e.run_id = $1::uuid AND h.pseudo_msisdn = $2"
	args := []any{f.RunID.String(), f.Subscriber}

	if window.Active() {
		where += fmt.Sprintf(" AND h.time >= $%d AND h.time < $%d", len(args)+1, len(args)+2)
		args = append(args, window.From, window.To)
	}
	if f.Method != "" {
		where += fmt.Sprintf(" AND e.method = $%d", len(args)+1)
		args = append(args, f.Method)
	}
	if f.Confidence != 0 {
		where += fmt.Sprintf(" AND e.confidence = $%d", len(args)+1)
		args = append(args, f.Confidence)
	}
	if f.BBox.Set {
		where += fmt.Sprintf(
			" AND ST_Intersects(e.geometry::geometry, ST_MakeEnvelope($%d,$%d,$%d,$%d,4326))",
			len(args)+1, len(args)+2, len(args)+3, len(args)+4)
		args = append(args, f.BBox.MinLon, f.BBox.MinLat, f.BBox.MaxLon, f.BBox.MaxLat)
	}

	n, err := s.count(ctx, from, where, args)
	if err != nil {
		return GeoJSON{}, err
	}
	if err := s.checkLimit(n,
		"zaman aralığını daraltın, method/confidence süzgeci ekleyin ya da bbox verin"); err != nil {
		return GeoJSON{}, err
	}

	sql := `
        SELECT coalesce(json_build_object(
            'type','FeatureCollection',
            'features', coalesce(json_agg(json_build_object(
                'type','Feature',
                'geometry', ST_AsGeoJSON(e.geometry)::json,
                'properties', json_build_object(
                    'event_id',   e.event_id,
                    'method',     e.method,
                    'confidence', e.confidence,
                    'area_km2',   round(e.area_km2::numeric, 6),
                    'part_count', e.part_count,
                    'repaired',   e.repaired,
                    'ta_used',    e.ta_used,
                    'time',       h.time,
                    'centroid',   ST_AsGeoJSON(e.centroid)::json))), '[]'::json)
        )::text, '` + emptyCollection + `')
        FROM ` + from + ` WHERE ` + where

	return s.collect(ctx, sql, args, n)
}

// ─── Bütünlük bulguları ───────────────────────────────────────────────────────

// Findings, bütünlük bulgularını GeoJSON olarak döndürür.
//
// # Kural 1 bulguları neden konumsuz
//
// Kural 1, "kayıt envanterde **olmayan** bir hücreyi beyan ediyor" der; o
// hücrenin konumu yoktur çünkü hücre yoktur. Bu bulgular `geometry: null`
// ile döner — GeoJSON RFC 7946 bunu açıkça destekler ve istemci onları liste
// olarak gösterir.
func (s *Service) Findings(ctx context.Context, f FindingsFilter) (GeoJSON, error) {
	if err := f.Validate(); err != nil {
		return GeoJSON{}, err
	}
	window, err := f.Window.Resolve()
	if err != nil {
		return GeoJSON{}, err
	}

	from := `integrity_findings f
                 JOIN hts_records h ON h.run_id = f.run_id AND h.event_id = f.event_id
                 LEFT JOIN cells c  ON c.run_id = f.run_id AND c.cell_id  = h.cell_id`
	where := "f.run_id = $1::uuid"
	args := []any{f.RunID.String()}

	if f.CanonicalOnly {
		where += " AND f.suppressed_by IS NULL"
	}
	if f.RuleID != 0 {
		where += fmt.Sprintf(" AND f.rule_id = $%d", len(args)+1)
		args = append(args, f.RuleID)
	}
	if window.Active() {
		where += fmt.Sprintf(" AND f.time >= $%d AND f.time < $%d", len(args)+1, len(args)+2)
		args = append(args, window.From, window.To)
	}
	if f.BBox.Set {
		where += fmt.Sprintf(
			" AND ST_Intersects(c.location::geometry, ST_MakeEnvelope($%d,$%d,$%d,$%d,4326))",
			len(args)+1, len(args)+2, len(args)+3, len(args)+4)
		args = append(args, f.BBox.MinLon, f.BBox.MinLat, f.BBox.MaxLon, f.BBox.MaxLat)
	}

	n, err := s.count(ctx, from, where, args)
	if err != nil {
		return GeoJSON{}, err
	}
	if err := s.checkLimit(n, "rule_id veya zaman aralığı süzgeci ekleyin"); err != nil {
		return GeoJSON{}, err
	}

	sql := `
        SELECT coalesce(json_build_object(
            'type','FeatureCollection',
            'features', coalesce(json_agg(json_build_object(
                'type','Feature',
                'geometry', CASE WHEN c.location IS NULL THEN NULL
                                 ELSE ST_AsGeoJSON(c.location)::json END,
                'properties', json_build_object(
                    'event_id',      f.event_id,
                    'rule_id',       f.rule_id,
                    'rule_name',     f.rule_name,
                    'margin',        round(f.margin::numeric, 4),
                    'evidence',      f.evidence,
                    'detected_in',   f.detected_in,
                    'suppressed_by', f.suppressed_by,
                    'time',          f.time,
                    'cell_known',    (c.location IS NOT NULL)))), '[]'::json)
        )::text, '` + emptyCollection + `')
        FROM ` + from + ` WHERE ` + where

	return s.collect(ctx, sql, args, n)
}

// ─── k-anonim hücre yoğunluğu (ADR-33/5) ──────────────────────────────────────

// CellActivity, hücre başına kaç farklı abonenin görüldüğünü döndürür.
type CellActivity struct {
	GeoJSON
	// Suppressed, k eşiği nedeniyle gizlenen hücre sayısıdır.
	//
	// Gizleme **sessiz olmaz**: kaç grubun bastırıldığı raporlanır. Aksi hâlde
	// seyrek bölgeler haritada "aktivite yok" gibi görünür ve bu yanıltıcıdır.
	Suppressed int
	// K, uygulanan eşiktir.
	K int
}

// cellActivitySQL, k-anonim hücre yoğunluğunu hesaplar.
//
// `HAVING count(DISTINCT h.pseudo_msisdn) >= $k`: 5'ten az abonenin görüldüğü
// hücre **döndürülmez**. Bu, ADR-15'in "k=5, yalnızca aggregate uçlar"
// kuralının sistemdeki tek anlamlı uygulama noktasıdır (ADR-33/5).
const cellActivitySQL = `
WITH activity AS (
    SELECT h.cell_id,
           count(*)                             AS records,
           count(DISTINCT h.pseudo_msisdn)      AS subscribers
      FROM hts_records h
     WHERE h.run_id = $1::uuid
     GROUP BY h.cell_id
), kept AS (
    SELECT a.*, c.location
      FROM activity a
      JOIN cells c ON c.run_id = $1::uuid AND c.cell_id = a.cell_id
     WHERE a.subscribers >= $2
       AND ($3::boolean IS NOT TRUE
            OR ST_Intersects(c.location::geometry, ST_MakeEnvelope($4,$5,$6,$7,4326)))
)
SELECT
  coalesce((SELECT json_build_object(
      'type','FeatureCollection',
      'features', coalesce(json_agg(json_build_object(
          'type','Feature',
          'geometry', ST_AsGeoJSON(k.location)::json,
          'properties', json_build_object(
              'cell_id',     k.cell_id,
              'records',     k.records,
              'subscribers', k.subscribers))), '[]'::json))
    FROM kept k)::text, '` + emptyCollection + `'),
  (SELECT count(*) FROM kept),
  (SELECT count(*) FROM activity a
     JOIN cells c ON c.run_id = $1::uuid AND c.cell_id = a.cell_id
    WHERE a.subscribers < $2)`

// NOT: bastırılan sayısı `cells` ile JOIN edilir. JOIN'siz sayım, enjeksiyon
// kural 1'in ürettiği ~1.172 SAHTE hücreyi de sayardı (her biri tek aboneli,
// hepsi k eşiğinin altında) ve "1.182 hücre gizlendi" gibi bir sayı çıkardı —
// oysa gerçekte gizlenen gerçek hücre sayısı bunun yüzde biri kadar.
//
// Sahte hücrelerin konumu zaten yoktur ve haritada gösterilemezler; onları
// gizlilik etkisi olarak raporlamak, k-anonimliğin ne kadar iş yaptığını
// abartmak olurdu.

// CellActivity, k-anonim hücre yoğunluğunu döndürür.
func (s *Service) CellActivity(ctx context.Context, runID uuid.UUID, bbox BBox) (CellActivity, error) {
	if runID == uuid.Nil {
		return CellActivity{}, ErrRunIDRequired
	}
	if err := bbox.Validate(); err != nil {
		return CellActivity{}, err
	}

	var out CellActivity
	var kept, suppressed int64
	err := s.db.QueryRow(ctx, cellActivitySQL,
		runID.String(), KAnonymity, bbox.Set,
		bbox.MinLon, bbox.MinLat, bbox.MaxLon, bbox.MaxLat,
	).Scan(&out.FeatureCollection, &kept, &suppressed)
	if err != nil {
		return CellActivity{}, fmt.Errorf("hücre yoğunluğu sorgusu: %w", err)
	}

	out.Count = int(kept)
	out.Suppressed = int(suppressed)
	out.K = KAnonymity

	if err := s.checkLimit(out.Count, "bbox ile daraltın"); err != nil {
		return CellActivity{}, err
	}
	return out, nil
}

// ─── Ortak yardımcılar ────────────────────────────────────────────────────────

// count, süzgeçten geçen satır sayısını döndürür.
func (s *Service) count(ctx context.Context, from, where string, args []any) (int, error) {
	var n int64
	sql := "SELECT count(*) FROM " + from + " WHERE " + where
	if err := s.db.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("sayım sorgusu: %w", err)
	}
	return int(n), nil
}

// checkLimit, ADR-13'ün 500 geometri sınırını uygular.
func (s *Service) checkLimit(n int, hint string) error {
	if n > MaxGeometries {
		return &TooManyGeometriesError{Requested: n, Limit: MaxGeometries, Hint: hint}
	}
	return nil
}

// collect, GeoJSON metnini çeker.
func (s *Service) collect(ctx context.Context, sql string, args []any, n int) (GeoJSON, error) {
	var doc string
	if err := s.db.QueryRow(ctx, sql, args...).Scan(&doc); err != nil {
		return GeoJSON{}, fmt.Errorf("geojson sorgusu: %w", err)
	}
	return GeoJSON{FeatureCollection: doc, Count: n}, nil
}

// queryTimeout, tek bir API sorgusunun üst sınırıdır.
//
// Gateway kullanıcı isteği servis eder; yavaş bir sorgu istemciyi bekletmek
// yerine hata döndürmelidir.
const queryTimeout = 15 * time.Second

// WithTimeout, sorgu bağlamına üst sınır ekler.
func WithTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, queryTimeout)
}
