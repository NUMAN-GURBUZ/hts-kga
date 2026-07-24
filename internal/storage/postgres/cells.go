// T-E02-06 — `run_config` ve `cells` tablolarına toplu yazma.
//
// Toplu ekleme, dizi parametrelerinin `unnest` ile satırlara açılmasıyla tek
// gidiş-dönüşte yapılır. GEOGRAPHY sütunu sunucu tarafında
// ST_SetSRID(ST_MakePoint(lon, lat), 4326) ile üretilir; istemci tarafında
// WKB kodlaması yazılmaz.
package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// RunRow, `run_config` tablosunun bir satırıdır (ADR-05).
type RunRow struct {
	RunID      uuid.UUID
	Scenario   string // A/B/C/D
	Morphology string // urban/rural
	Seed       int64
	GitSHA     string
	ConfigJSON []byte // config_yaml JSONB — tam senaryo config'i
}

// CellRow, `cells` tablosunun bir satırıdır.
type CellRow struct {
	CellID     uuid.UUID
	RunID      uuid.UUID
	SiteID     uuid.UUID
	Azimuth    float64
	BeamWidth  float64
	FreqMHz    int
	EIRPdBm    float64
	AntHeightM float64
	TiltDeg    float64
	RMaxM      float64
	Morphology string
	ModelType  string
	Lat        float64
	Lon        float64
}

// insertRunSQL, koşu kaydını ekler. Aynı run_id yeniden yazılmaz (yeniden
// çalıştırmada koşu meta verisi korunur).
const insertRunSQL = `
INSERT INTO run_config (run_id, scenario, morphology, seed, git_sha, config_yaml)
VALUES ($1::uuid, $2, $3, $4, $5, $6)
ON CONFLICT (run_id) DO NOTHING`

// insertCellsSQL, hücre envanterini tek sorguda toplu ekler.
const insertCellsSQL = `
INSERT INTO cells (
    cell_id, run_id, site_id, azimuth, beam_width, freq_mhz, eirp_dbm,
    ant_height, tilt_deg, r_max_m, morphology, model_type, location
)
SELECT
    c.cell_id, c.run_id, c.site_id, c.azimuth, c.beam_width, c.freq_mhz,
    c.eirp_dbm, c.ant_height, c.tilt_deg, c.r_max_m, c.morphology, c.model_type,
    ST_SetSRID(ST_MakePoint(c.lon, c.lat), 4326)::geography
FROM unnest(
    $1::uuid[], $2::uuid[], $3::uuid[], $4::float8[], $5::float8[], $6::int[],
    $7::float8[], $8::float8[], $9::float8[], $10::float8[], $11::varchar[],
    $12::varchar[], $13::float8[], $14::float8[]
) AS c(
    cell_id, run_id, site_id, azimuth, beam_width, freq_mhz, eirp_dbm,
    ant_height, tilt_deg, r_max_m, morphology, model_type, lon, lat
)`

// execer, hem havuzun hem de işlemin (transaction) karşıladığı Exec arayüzüdür.
// Aynı yazma kodunun ikisiyle de çalışmasını sağlar.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// InsertRun, koşu kaydını yazar. `cells` bu satıra FK ile bağlı olduğundan
// envanter yüklemesinden önce çağrılmalıdır.
func (p *Pool) InsertRun(ctx context.Context, r RunRow) error {
	return insertRun(ctx, p.pool, r)
}

// InsertCells, hücre envanterini toplu yazar ve eklenen satır sayısını döndürür.
func (p *Pool) InsertCells(ctx context.Context, rows []CellRow) (int64, error) {
	return insertCells(ctx, p.pool, rows)
}

// InsertRunWithCells, koşu kaydını ve envanteri **tek işlemde** yazar.
//
// Envanter yüklemesinin atomik yolu budur: hata durumunda run_config satırı da
// geri alınır, yarım envanterli koşu kaydı ortada kalmaz.
func (p *Pool) InsertRunWithCells(ctx context.Context, run RunRow, rows []CellRow) (int64, error) {
	var affected int64
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		if err := insertRun(ctx, tx, run); err != nil {
			return err
		}
		n, err := insertCells(ctx, tx, rows)
		if err != nil {
			return err
		}
		affected = n
		return nil
	})
	return affected, err
}

func insertRun(ctx context.Context, q execer, r RunRow) error {
	if r.RunID == uuid.Nil {
		return fmt.Errorf("run_config: run_id boş (ADR-05)")
	}
	if len(r.ConfigJSON) == 0 {
		return fmt.Errorf("run_config: config_yaml boş olamaz")
	}

	_, err := q.Exec(ctx, insertRunSQL,
		r.RunID.String(), r.Scenario, r.Morphology, r.Seed, r.GitSHA, r.ConfigJSON)
	if err != nil {
		return fmt.Errorf("run_config yazılamadı (%s): %w", r.RunID, err)
	}
	return nil
}

func insertCells(ctx context.Context, q execer, rows []CellRow) (int64, error) {
	if len(rows) == 0 {
		return 0, fmt.Errorf("cells: yazılacak hücre yok")
	}

	a := newCellArrays(len(rows))
	for i, r := range rows {
		if err := a.set(i, r); err != nil {
			return 0, err
		}
	}

	tag, err := q.Exec(ctx, insertCellsSQL,
		a.cellID, a.runID, a.siteID, a.azimuth, a.beamWidth, a.freqMHz,
		a.eirp, a.antHeight, a.tilt, a.rMax, a.morphology, a.modelType,
		a.lon, a.lat)
	if err != nil {
		return 0, fmt.Errorf("cells toplu yazma başarısız (%d satır): %w", len(rows), err)
	}
	return tag.RowsAffected(), nil
}

// CountCells, bir koşuya ait hücre sayısını döndürür (doğrulama için).
func (p *Pool) CountCells(ctx context.Context, runID uuid.UUID) (int64, error) {
	var n int64
	err := p.pool.QueryRow(ctx,
		`SELECT count(*) FROM cells WHERE run_id = $1::uuid`, runID.String()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("hücre sayımı başarısız (%s): %w", runID, err)
	}
	return n, nil
}

// DeleteRun, koşuyu ve ona bağlı hücreleri siler (ON DELETE CASCADE).
// Testlerin ve yeniden yüklemenin temizlik yoludur.
func (p *Pool) DeleteRun(ctx context.Context, runID uuid.UUID) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM run_config WHERE run_id = $1::uuid`, runID.String())
	if err != nil {
		return fmt.Errorf("koşu silinemedi (%s): %w", runID, err)
	}
	return nil
}

// ─── Dizi parametreleri ───────────────────────────────────────────────────────

// cellArrays, unnest parametrelerini sütun dizileri olarak tutar.
//
// UUID sütunları dize olarak taşınır: sürücünün adlandırılmış [16]byte
// tiplerini dizi içinde yorumlamasına güvenmek yerine, `::uuid[]` dönüşümü
// sunucuda yapılır — davranış sürücü sürümünden bağımsızdır.
type cellArrays struct {
	cellID     []string
	runID      []string
	siteID     []string
	azimuth    []float64
	beamWidth  []float64
	freqMHz    []int32
	eirp       []float64
	antHeight  []float64
	tilt       []float64
	rMax       []float64
	morphology []string
	modelType  []string
	lon        []float64
	lat        []float64
}

func newCellArrays(n int) *cellArrays {
	return &cellArrays{
		cellID: make([]string, n), runID: make([]string, n),
		siteID: make([]string, n), azimuth: make([]float64, n),
		beamWidth: make([]float64, n), freqMHz: make([]int32, n),
		eirp: make([]float64, n), antHeight: make([]float64, n),
		tilt: make([]float64, n), rMax: make([]float64, n),
		morphology: make([]string, n), modelType: make([]string, n),
		lon: make([]float64, n), lat: make([]float64, n),
	}
}

// set, i'inci satırı dizilere yerleştirir ve temel kısıtları denetler.
//
// Denetim burada da yapılır: veritabanı CHECK ihlali tüm toplu yazmayı
// düşürür, hangi satırın suçlu olduğunu söylemez.
func (a *cellArrays) set(i int, r CellRow) error {
	if r.CellID == uuid.Nil || r.RunID == uuid.Nil || r.SiteID == uuid.Nil {
		return fmt.Errorf("cells[%d]: kimlik alanları boş olamaz", i)
	}
	if !(r.Azimuth >= 0 && r.Azimuth < 360) {
		return fmt.Errorf("cells[%d]: azimuth [0,360) olmalı (%g)", i, r.Azimuth)
	}
	if !(r.BeamWidth > 0 && r.BeamWidth <= 360) {
		return fmt.Errorf("cells[%d]: beam_width (0,360] olmalı (%g)", i, r.BeamWidth)
	}
	if r.FreqMHz <= 0 {
		return fmt.Errorf("cells[%d]: freq_mhz pozitif olmalı (%d)", i, r.FreqMHz)
	}
	if !(r.AntHeightM > 0) {
		return fmt.Errorf("cells[%d]: ant_height pozitif olmalı (%g)", i, r.AntHeightM)
	}
	if !(r.RMaxM > 0) {
		return fmt.Errorf("cells[%d]: r_max_m pozitif olmalı (%g)", i, r.RMaxM)
	}
	if r.Lat < -90 || r.Lat > 90 || r.Lon < -180 || r.Lon > 180 {
		return fmt.Errorf("cells[%d]: geçersiz konum (%g, %g)", i, r.Lat, r.Lon)
	}

	a.cellID[i] = r.CellID.String()
	a.runID[i] = r.RunID.String()
	a.siteID[i] = r.SiteID.String()
	a.azimuth[i], a.beamWidth[i] = r.Azimuth, r.BeamWidth
	a.freqMHz[i] = int32(r.FreqMHz)
	a.eirp[i], a.antHeight[i], a.tilt[i], a.rMax[i] = r.EIRPdBm, r.AntHeightM, r.TiltDeg, r.RMaxM
	a.morphology[i], a.modelType[i] = r.Morphology, r.ModelType
	a.lon[i], a.lat[i] = r.Lon, r.Lat
	return nil
}
