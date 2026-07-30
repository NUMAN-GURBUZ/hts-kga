// Bulgu → veritabanı satırı eşlemesi (T-E05-01c).
//
// Eşleme bu pakette durur, `internal/storage/postgres` içinde değil: depolama
// katmanı alan (domain) paketlerine bağımlı olmamalıdır (postgres paketinin
// kendi kuralı). Ters yön serbesttir.

package detector

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// FindingWriter, bulgu satırlarını yazan depolama yüzeyidir.
//
// Arayüz olarak tanımlanır ki motor testleri veritabanı olmadan koşabilsin;
// `*postgres.Pool` bunu uygular.
type FindingWriter interface {
	InsertFindings(ctx context.Context, rows []postgres.FindingRow) (int64, error)
}

// ClaimLoader, faz devrinde talep defterini okuyan yüzeydir (ADR-28/6).
type ClaimLoader interface {
	SelectCanonicalClaims(ctx context.Context, runID uuid.UUID) (map[uuid.UUID]int, error)
}

// PostgresSink, bulguları `integrity_findings` tablosuna yazar.
type PostgresSink struct {
	writer FindingWriter
	runID  uuid.UUID
}

// NewPostgresSink, veritabanı yazıcısını kurar.
//
// runID satırlara buradan yazılır, kuralın isabetinden değil: kural koşunun
// kimliğini bilmek zorunda olmasın diye. Böylece kural testleri run_id
// kurgulamak zorunda kalmaz.
func NewPostgresSink(writer FindingWriter, runID uuid.UUID) (*PostgresSink, error) {
	if writer == nil {
		return nil, fmt.Errorf("bulgu yazıcısı: depolama yüzeyi zorunlu")
	}
	if runID == uuid.Nil {
		return nil, fmt.Errorf("bulgu yazıcısı: run_id zorunlu (ADR-05)")
	}
	return &PostgresSink{writer: writer, runID: runID}, nil
}

// WriteFindings, bulguları satıra çevirip yazar.
func (s *PostgresSink) WriteFindings(ctx context.Context, findings []Finding) (int64, error) {
	if len(findings) == 0 {
		return 0, nil
	}

	rows := make([]postgres.FindingRow, 0, len(findings))
	for i, f := range findings {
		row, err := s.toRow(f)
		if err != nil {
			return 0, fmt.Errorf("bulgu[%d] satıra çevrilemedi: %w", i, err)
		}
		rows = append(rows, row)
	}
	return s.writer.InsertFindings(ctx, rows)
}

// toRow, bulguyu veritabanı satırına çevirir.
func (s *PostgresSink) toRow(f Finding) (postgres.FindingRow, error) {
	evidence, err := json.Marshal(f.Evidence)
	if err != nil {
		// Motorun geçerlilik denetimi bunu zaten yakalamış olmalı; buraya
		// düşmesi kanıt kurucusunun atlandığı anlamına gelir.
		return postgres.FindingRow{}, fmt.Errorf("kanıt serileştirilemedi (kural %s): %w", f.Rule, err)
	}

	row := postgres.FindingRow{
		RunID:      s.runID,
		EventID:    f.EventID,
		Time:       f.Time,
		RuleID:     int(f.Rule),
		RuleName:   f.RuleName(),
		Margin:     f.Margin,
		Evidence:   evidence,
		Scenario:   f.Scenario,
		DetectedIn: string(f.DetectedIn),
	}
	if f.SuppressedBy != nil {
		v := int(*f.SuppressedBy)
		row.SuppressedBy = &v
	}
	return row, nil
}

// LoadClaims, koşunun kanonik taleplerini okuyup defter kurar (ADR-28/6).
//
// Tanımsız bir kural kimliği okunursa hata verilir: `pkg/integrityrule.Parse`
// kapısından geçmeyen bir kimlik karışıklık matrisinde açıklanamayan bir satır
// üretirdi.
func LoadClaims(ctx context.Context, loader ClaimLoader, runID uuid.UUID) (*Claims, error) {
	raw, err := loader.SelectCanonicalClaims(ctx, runID)
	if err != nil {
		return nil, err
	}

	parsed := make(map[uuid.UUID]integrityrule.ID, len(raw))
	for eventID, ruleID := range raw {
		id, err := integrityrule.Parse(ruleID)
		if err != nil {
			return nil, fmt.Errorf("talep defteri (olay %s): %w", eventID, err)
		}
		parsed[eventID] = id
	}
	return NewClaimsFrom(parsed), nil
}
