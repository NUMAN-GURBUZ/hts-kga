package detector

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/storage/postgres"
	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// fakeWriter, InsertFindings çağrılarını kaydeder.
type fakeWriter struct {
	rows []postgres.FindingRow
}

func (w *fakeWriter) InsertFindings(_ context.Context, rows []postgres.FindingRow) (int64, error) {
	w.rows = append(w.rows, rows...)
	return int64(len(rows)), nil
}

// fakeLoader, talep defterini taklit eder.
type fakeLoader struct {
	claims map[uuid.UUID]int
	err    error
}

func (l fakeLoader) SelectCanonicalClaims(context.Context, uuid.UUID) (map[uuid.UUID]int, error) {
	return l.claims, l.err
}

var testRunID = uuid.MustParse("33333333-3333-3333-3333-333333333333")

// TestNewPostgresSink_RequiresRunID, koşu kimliği olmadan yazıcının
// kurulmadığını sınar (ADR-05).
func TestNewPostgresSink_RequiresRunID(t *testing.T) {
	if _, err := NewPostgresSink(&fakeWriter{}, uuid.Nil); err == nil {
		t.Error("run_id olmadan yazıcı kurulmamalı")
	}
	if _, err := NewPostgresSink(nil, testRunID); err == nil {
		t.Error("depolama yüzeyi olmadan yazıcı kurulmamalı")
	}
}

// TestPostgresSink_MapsFindingToRow, bulgunun satıra eksiksiz çevrildiğini
// sınar.
func TestPostgresSink_MapsFindingToRow(t *testing.T) {
	writer := &fakeWriter{}
	sink, err := NewPostgresSink(writer, testRunID)
	if err != nil {
		t.Fatalf("NewPostgresSink: %v", err)
	}

	suppressor := integrityrule.Inventory
	findings := []Finding{
		{
			Hit: Hit{
				Rule: integrityrule.Inventory, EventID: uuid.New(), Time: baseTime,
				Scenario: "A", Margin: 1.0,
				Evidence: NewEvidence(integrityrule.Inventory, DefaultMarginCap).
					Str("cell_id", "abc").MustBuild(),
			},
			DetectedIn: integrityrule.ClassStream,
		},
		{
			Hit: Hit{
				Rule: integrityrule.TimeOrder, EventID: uuid.New(), Time: baseTime,
				Scenario: "A", Margin: 24.0,
				Evidence: NewEvidence(integrityrule.TimeOrder, DefaultMarginCap).
					Float("backstep_s", 7200).MustBuild(),
			},
			DetectedIn:   integrityrule.ClassStream,
			SuppressedBy: &suppressor,
		},
	}

	n, err := sink.WriteFindings(context.Background(), findings)
	if err != nil {
		t.Fatalf("WriteFindings: %v", err)
	}
	if n != 2 {
		t.Errorf("eklenen satır %d, beklenen 2", n)
	}
	if len(writer.rows) != 2 {
		t.Fatalf("%d satır yazıldı", len(writer.rows))
	}

	// Kanonik satır.
	canonical := writer.rows[0]
	if canonical.RunID != testRunID {
		t.Errorf("run_id = %s, beklenen %s", canonical.RunID, testRunID)
	}
	if canonical.RuleID != 1 {
		t.Errorf("rule_id = %d, beklenen 1", canonical.RuleID)
	}
	if canonical.RuleName != integrityrule.Inventory.Name() {
		t.Errorf("rule_name = %q", canonical.RuleName)
	}
	if canonical.DetectedIn != "stream" {
		t.Errorf("detected_in = %q, beklenen stream", canonical.DetectedIn)
	}
	if canonical.SuppressedBy != nil {
		t.Errorf("kanonik satırda suppressed_by dolu: %d", *canonical.SuppressedBy)
	}

	// Bastırılmış satır.
	suppressed := writer.rows[1]
	if suppressed.SuppressedBy == nil || *suppressed.SuppressedBy != 1 {
		t.Errorf("suppressed_by = %v, beklenen 1", suppressed.SuppressedBy)
	}

	// Kanıt geçerli JSON ve sürümlü olmalı (şema CHECK'i).
	var probe map[string]any
	if err := json.Unmarshal(canonical.Evidence, &probe); err != nil {
		t.Fatalf("kanıt JSON değil: %v", err)
	}
	if _, ok := probe["v"]; !ok {
		t.Error("kanıtta sürüm alanı yok (evidence ? 'v' kısıtı)")
	}
}

// TestPostgresSink_RowsPassSchemaValidation, üretilen satırların şema öncesi
// doğrulamadan geçtiğini sınar.
//
// `postgres.FindingRow.validate` şema kısıtlarının aynısını uygular; motorun
// ürettiği her satır oradan geçmeli, yoksa parti reddedilir ve o partideki tüm
// bulgular kaybolur (zehirli satır).
func TestPostgresSink_RowsPassSchemaValidation(t *testing.T) {
	writer := &fakeWriter{}
	sink, _ := NewPostgresSink(writer, testRunID)

	for _, rule := range []integrityrule.ID{
		integrityrule.Inventory, integrityrule.Activity,
		integrityrule.TimeOrder, integrityrule.Velocity,
	} {
		f := Finding{
			Hit: Hit{
				Rule: rule, EventID: uuid.New(), Time: baseTime, Scenario: "A", Margin: 1.5,
				Evidence: NewEvidence(rule, DefaultMarginCap).Str("kaynak", "test").MustBuild(),
			},
			DetectedIn: rule.Class(),
		}
		if _, err := sink.WriteFindings(context.Background(), []Finding{f}); err != nil {
			t.Errorf("%s: WriteFindings: %v", rule, err)
		}
	}

	rows, err := writer.rowsOrError()
	if err != nil {
		t.Fatalf("satır doğrulaması: %v", err)
	}
	if len(rows) != 4 {
		t.Errorf("%d satır, beklenen 4", len(rows))
	}
}

// rowsOrError, kaydedilen satırları depolama katmanının doğrulamasından geçirir.
func (w *fakeWriter) rowsOrError() ([]postgres.FindingRow, error) {
	// Doğrulama InsertFindings içinde yapılıyor; burada gerçek havuz olmadan
	// aynı kapıyı geçmek için satırlar tek tek sınanır.
	for i, r := range w.rows {
		if r.RuleID < 1 || r.RuleID > 5 {
			return nil, errRow(i, "rule_id aralık dışı")
		}
		if r.Margin < 1.0 || r.Margin > 1e6 {
			return nil, errRow(i, "margin aralık dışı")
		}
		if len(r.RuleName) == 0 || len(r.RuleName) > 50 {
			return nil, errRow(i, "rule_name uzunluğu geçersiz")
		}
		if r.DetectedIn != "stream" && r.DetectedIn != "batch" {
			return nil, errRow(i, "detected_in geçersiz")
		}
	}
	return w.rows, nil
}

type rowError struct {
	index int
	msg   string
}

func (e rowError) Error() string { return e.msg }

func errRow(i int, msg string) error { return rowError{index: i, msg: msg} }

// TestLoadClaims_ParsesRuleIDs, defterin kimlik doğrulamasından geçtiğini
// sınar.
func TestLoadClaims_ParsesRuleIDs(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	claims, err := LoadClaims(context.Background(), fakeLoader{
		claims: map[uuid.UUID]int{a: 1, b: 3},
	}, testRunID)
	if err != nil {
		t.Fatalf("LoadClaims: %v", err)
	}
	if claims.Len() != 2 {
		t.Errorf("defter %d talep içeriyor, beklenen 2", claims.Len())
	}
	if rule, ok := claims.Of(a); !ok || rule != integrityrule.Inventory {
		t.Errorf("a talebi %v (ok=%v)", rule, ok)
	}
	if rule, ok := claims.Of(b); !ok || rule != integrityrule.TimeOrder {
		t.Errorf("b talebi %v (ok=%v)", rule, ok)
	}
}

// TestLoadClaims_RejectsUnknownRuleID, şema dışı bir kimliğin sessizce kabul
// edilmediğini sınar.
func TestLoadClaims_RejectsUnknownRuleID(t *testing.T) {
	_, err := LoadClaims(context.Background(), fakeLoader{
		claims: map[uuid.UUID]int{uuid.New(): 9},
	}, testRunID)
	if err == nil {
		t.Error("tanımsız kural kimliği reddedilmeliydi")
	}
}

// TestClaims_FirstClaimWins, defterin ilk talebi koruduğunu sınar (ADR-28/7).
func TestClaims_FirstClaimWins(t *testing.T) {
	c := NewClaims()
	id := uuid.New()

	if !c.Claim(id, integrityrule.TimeOrder) {
		t.Fatal("ilk talep kabul edilmeliydi")
	}
	if c.Claim(id, integrityrule.Inventory) {
		t.Error("ikinci talep — daha güçlü olsa bile — reddedilmeliydi (ADR-28/7)")
	}
	if rule, _ := c.Of(id); rule != integrityrule.TimeOrder {
		t.Errorf("talep sahibi %s, beklenen %s", rule, integrityrule.TimeOrder)
	}
}
