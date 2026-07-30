// Kanıt üretimi ve sonluluk koruması (ADR-31/4, ADR-31/5).

package detector

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/pkg/integrityrule"
)

// EvidenceSchemaVersion, kanıt şemasının sürümüdür.
//
// `integrity_findings` üzerindeki CHECK (evidence ? 'v') kısıtı bu alanın
// varlığını zorunlu kılar. Sürümsüz bir JSONB ile "adli açıklanabilirlik"
// savunulamaz ve şema sonradan geliştirilemez.
const EvidenceSchemaVersion = 1

// Evidence, bir bulgunun kanıt gövdesidir (integrity_findings.evidence).
type Evidence map[string]any

// forbiddenKeys, ground truth türevi alan adlarıdır.
//
// # Kör testin kanıt katmanındaki ifadesi
//
// Dedektör bu değerlere zaten erişemez (rol ACL'i, ADR-09 katman 2). Ama
// erişemediğini **teste bağlamak** ayrı bir korumadır: bir gün biri hata
// ayıklama sırasında gerçek konumu kanıta yazarsa, o kanıt veritabanına gider
// ve bulgular ground truth taşımaya başlar. O noktada K7 ölçümü kirlenmiş olur
// ve bunu fark etmenin yolu kalmaz.
//
// Liste alan adlarını **alt dize** olarak arar: `true_lat`, `agent_id_hash`
// gibi türevler de yakalanır.
var forbiddenKeys = []string{
	"lat", "lon", "true_location", "location",
	"agent_id", "agent", "injected", "partition_key", "covered",
}

// reservedKeys, kurucunun kendi yazdığı anahtarlardır; kural bunları ezemez.
var reservedKeys = map[string]bool{"v": true, "rule": true, "capped": true}

// EvidenceBuilder, kanıt gövdesini sonluluk ve sızıntı denetimiyle kurar.
//
// Sıfır değeri kullanılamaz; NewEvidence ile kurulur.
type EvidenceBuilder struct {
	rule   integrityrule.ID
	cap    float64
	fields Evidence
	capped bool
	err    error
}

// NewEvidence, bir kural için kanıt kurucusu açar.
//
// marginCap, kayan nokta alanlarının mutlak üst sınırıdır
// (integrity.detection.velocity_margin_cap). Sıfır veya negatifse ADR-31'in
// varsayılanı kullanılır.
func NewEvidence(rule integrityrule.ID, marginCap float64) *EvidenceBuilder {
	if !(marginCap > 0) {
		marginCap = DefaultMarginCap
	}
	return &EvidenceBuilder{
		rule: rule,
		cap:  marginCap,
		fields: Evidence{
			"v":    EvidenceSchemaVersion,
			"rule": int(rule),
		},
	}
}

// DefaultMarginCap, ADR-31'in `margin` CHECK üst sınırıdır.
//
// Şemadaki CHECK (margin <= 1e6) ile aynı değer olmak zorundadır: küçük
// olsaydı kapılan bulgu geçerli olur ama bilgi kaybı gizli kalırdı; büyük
// olsaydı veritabanı bulguyu reddederdi.
const DefaultMarginCap = 1e6

// Str, metin alanı ekler.
func (b *EvidenceBuilder) Str(key, value string) *EvidenceBuilder {
	return b.set(key, value)
}

// Int, tam sayı alanı ekler.
func (b *EvidenceBuilder) Int(key string, value int) *EvidenceBuilder {
	return b.set(key, value)
}

// Bool, mantıksal alan ekler.
func (b *EvidenceBuilder) Bool(key string, value bool) *EvidenceBuilder {
	return b.set(key, value)
}

// UUID, kimlik alanı ekler.
func (b *EvidenceBuilder) UUID(key string, value uuid.UUID) *EvidenceBuilder {
	return b.set(key, value.String())
}

// Time, zaman alanı ekler (RFC3339, UTC).
func (b *EvidenceBuilder) Time(key string, value time.Time) *EvidenceBuilder {
	return b.set(key, value.UTC().Format(time.RFC3339Nano))
}

// Float, kayan nokta alanı ekler ve sonluluğu garanti eder.
//
// ±Inf ve NaN kapılır ve `capped` işareti konur. Koruma isteğe bağlı bir
// incelik değildir: Go'nun json.Marshal fonksiyonu +Inf ve NaN için **hata**
// döner. Δt = 0 durumunda ima edilen hız sonsuzdur ve kentsel koşuda
// >300 km/h isabetlerin tamamı bu popülasyondadır — koruma olmadan kural 2
// kentselde sıfır bulgu üretir.
func (b *EvidenceBuilder) Float(key string, value float64) *EvidenceBuilder {
	clamped, capped := ClampFinite(value, b.cap)
	if capped {
		b.capped = true
	}
	return b.set(key, clamped)
}

// set, anahtarı doğrular ve ekler.
func (b *EvidenceBuilder) set(key string, value any) *EvidenceBuilder {
	if b.err != nil {
		return b
	}
	if err := checkEvidenceKey(key); err != nil {
		b.err = err
		return b
	}
	b.fields[key] = value
	return b
}

// checkEvidenceKey, anahtarın ayrılmış veya yasaklı olmadığını denetler.
func checkEvidenceKey(key string) error {
	if key == "" {
		return fmt.Errorf("kanıt: anahtar boş olamaz")
	}
	lower := strings.ToLower(key)
	if reservedKeys[lower] {
		return fmt.Errorf("kanıt: %q anahtarı kurucuya ayrılmıştır", key)
	}
	for _, banned := range forbiddenKeys {
		if strings.Contains(lower, banned) {
			return fmt.Errorf(
				"kanıt: %q anahtarı ground truth türevi (%q içeriyor); "+
					"bütünlük bulguları gerçek konum veya enjeksiyon etiketi taşıyamaz (ADR-31/5)",
				key, banned)
		}
	}
	return nil
}

// Build, kanıt gövdesini kapatır.
//
// Serileşebilirlik burada **sınanır**, yazma anında değil: yazma sırasında
// ortaya çıkan bir Marshal hatası partinin tamamını reddederdi ve bulgu
// kaybolurdu.
func (b *EvidenceBuilder) Build() (Evidence, error) {
	if b.err != nil {
		return nil, b.err
	}
	if b.capped {
		b.fields["capped"] = true
	}
	if _, err := json.Marshal(b.fields); err != nil {
		return nil, fmt.Errorf("kanıt (kural %s) serileştirilemedi: %w", b.rule, err)
	}
	return b.fields, nil
}

// MustBuild, Build'in hata döndürmeyeceği bilinen çağrı noktaları içindir.
//
// Kural kodu sabit anahtar kümeleri kullanır; hata ancak programlama
// hatasıyla oluşur. Yine de sessizce geçilmez: panik yerine kanıt gövdesi
// hata bilgisiyle döner ve motorun geçerlilik denetimi bulguyu reddeder.
func (b *EvidenceBuilder) MustBuild() Evidence {
	ev, err := b.Build()
	if err != nil {
		return Evidence{
			"v":     EvidenceSchemaVersion,
			"rule":  int(b.rule),
			"error": err.Error(),
		}
	}
	return ev
}

// ClampFinite, değeri sonlu ve [-cap, cap] aralığında döndürür.
//
// İkinci dönüş değeri kapılıp kapılmadığıdır. NaN, 0 değil **cap** olarak
// kapılır: NaN bir "değer yok" değil, "hesap bozuk" işaretidir ve sıfıra
// çevrilmesi bulguyu eşiğin altına düşürüp sessizce yok ederdi.
func ClampFinite(v, cap float64) (float64, bool) {
	switch {
	case math.IsNaN(v):
		return cap, true
	case math.IsInf(v, 1) || v > cap:
		return cap, true
	case math.IsInf(v, -1) || v < -cap:
		return -cap, true
	default:
		return v, false
	}
}

// ClampMargin, `margin` alanını şema kısıtına uygun hâle getirir.
//
// integrity_findings CHECK (margin >= 1.0 AND margin <= 1e6). Alt sınır
// önemlidir: eşik aşılmadan bulgu yazılmaz, dolayısıyla margin < 1 anlamsızdır
// ve şema onu reddeder. 1'in altındaki bir değer bir hesap hatasıdır ve 1,0'a
// yuvarlanır — bulguyu düşürmek yerine kaydetmek yeğdir, çünkü kural zaten
// tetiklenmiştir.
func ClampMargin(v, cap float64) (float64, bool) {
	clamped, capped := ClampFinite(v, cap)
	if clamped < 1.0 {
		return 1.0, capped || clamped != 1.0
	}
	return clamped, capped
}
