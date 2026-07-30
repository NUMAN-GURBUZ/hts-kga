// T-E05-02 — Kural 1: envanter tutarlılığı (ADR-27 ClassStream).
//
// # Kuralın iddiası
//
// Kayıt, koşunun şebekesinde **hiç var olmayan** bir hücreyi beyan ediyor.
// Adli olarak en savunulabilir bulgu tipidir: eşik yok, atıf belirsizliği yok,
// yorum yok — küme üyeliği.
//
// # Ölçüm
//
// Sprint 5 verisinde dört koşuda **precision %100 / recall %100** (1.172
// bulgu). Enjektör sahte kimliği ayrı bir UUID namespace'inden üretiyor
// (`hts-kga/injected-fake-cell`) ve o namespace envantere hiçbir zaman
// düşmüyor.
//
// # Neden öncelik sırasının başında
//
// ADR-28: kanıt gücü sıralaması. Envanterde olmayan bir hücreye işaret eden
// kayıt, cihaz kimliğinden veya zaman damgasından bağımsız olarak zaten
// geçersizdir. Ayrıca konumu bilinmediği için kural 2'nin zincirinden de
// çıkarılır (ADR-29/4).

package detector

import (
	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// CellSet, hücre kimliklerinin envanterde bulunup bulunmadığını bildiren
// yüzeydir.
//
// `internal/integrity/source.Inventory` bunu uygular. Arayüz olarak
// tanımlanması kuralın birim testinde Redis'e ihtiyaç duymamasını sağlar.
type CellSet interface {
	// Has, hücrenin envanterde olup olmadığını bildirir.
	Has(id uuid.UUID) bool
	// Len, envanterdeki hücre sayısıdır (kanıt gövdesi için).
	Len() int
}

// InventoryRule, envanterde bulunmayan hücre kimliklerini tespit eder.
type InventoryRule struct {
	cells     CellSet
	marginCap float64
}

// NewInventoryRule, kuralı kurar.
//
// Boş envanterle kurulamaz: her kayıt bulgu sayılır ve 298.117 satırlık bir
// yanlış pozitif seli oluşur. Envanter yükleyicisi de aynı denetimi yapıyor;
// iki kapı bilinçlidir, çünkü bu hatanın maliyeti tüm koşudur.
func NewInventoryRule(cells CellSet, marginCap float64) (*InventoryRule, error) {
	if cells == nil {
		return nil, errRuleSetup(integrityrule.Inventory, "hücre envanteri zorunlu")
	}
	if cells.Len() == 0 {
		return nil, errRuleSetup(integrityrule.Inventory,
			"envanter boş — her kayıt bulgu sayılırdı")
	}
	if !(marginCap > 0) {
		marginCap = DefaultMarginCap
	}
	return &InventoryRule{cells: cells, marginCap: marginCap}, nil
}

// ID, kural kimliğidir.
func (r *InventoryRule) ID() integrityrule.ID { return integrityrule.Inventory }

// Observe, kaydın hücresini envanterde arar.
//
// `margin` sabit 1,0'dır (ADR-31/4): eşik yok, ikili karar. 1,0 "kural
// tetiklendi" demektir ve şemanın `margin >= 1.0` kısıtını karşılar.
func (r *InventoryRule) Observe(rec Record) []Hit {
	if r.cells.Has(rec.CellID) {
		return nil
	}

	evidence := NewEvidence(integrityrule.Inventory, r.marginCap).
		UUID("cell_id", rec.CellID).
		Int("inventory_size", r.cells.Len()).
		MustBuild()

	return []Hit{{
		Rule:     integrityrule.Inventory,
		EventID:  rec.EventID,
		Time:     rec.Time,
		Scenario: rec.Scenario,
		Margin:   1.0,
		Evidence: evidence,
	}}
}
