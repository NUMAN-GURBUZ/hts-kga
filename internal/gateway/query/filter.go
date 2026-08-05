// Package query, API'nin okuma katmanıdır (T-E06, ADR-12, ADR-13, ADR-33).
//
// # Neden ayrı paket
//
// gRPC sunucusu ve elle yazılmış REST handler'ları (ADR-12/1) **aynı** servis
// katmanını çağırır. İş mantığı handler'ların içine yazılsaydı iki yerde
// tekrarlanır ve zamanla ayrışırdı — biri ADR-13 sınırını uygular, diğeri
// unuturdu.
//
// # ground_truth burada yok
//
// ADR-33/2: `svc_gateway` rolünün o tabloda yetkisi yoktur ve bu paket ona
// referans veren tek bir sorgu içermez. Kör test (K6) API katmanına kadar
// uzar.
package query

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// MaxGeometries, tek istekte döndürülebilecek geometri sayısıdır (ADR-13).
//
// Aşılırsa istek **reddedilir** (400 + açıklama); sessizce kırpılmaz. Kırpma,
// kullanıcıya eksik bir haritayı tam sanmasına yol açardı — ADR-13'ün
// "daima filtreli" kuralının amacı tam da budur.
const MaxGeometries = 500

// DefaultTimeWindow, zaman aralığı verilmediğinde uygulanan penceredir
// (ADR-13: "varsayılan 1 gün").
const DefaultTimeWindow = 24 * time.Hour

// KAnonymity, aggregate uçlarda uygulanan eşiktir (ADR-15, ADR-33/5).
const KAnonymity = 5

// BBox, viewport süzgecidir (ADR-13: ST_Intersects).
type BBox struct {
	MinLon, MinLat, MaxLon, MaxLat float64
	// Set false ise süzgeç uygulanmaz.
	Set bool
}

// Validate, kutunun coğrafi olarak geçerli olduğunu denetler.
func (b BBox) Validate() error {
	if !b.Set {
		return nil
	}
	switch {
	case b.MinLon < -180 || b.MaxLon > 180:
		return fmt.Errorf("bbox: boylam [-180, 180] aralığında olmalı")
	case b.MinLat < -90 || b.MaxLat > 90:
		return fmt.Errorf("bbox: enlem [-90, 90] aralığında olmalı")
	case b.MinLon >= b.MaxLon:
		return fmt.Errorf("bbox: min_lon < max_lon olmalı")
	case b.MinLat >= b.MaxLat:
		return fmt.Errorf("bbox: min_lat < max_lat olmalı")
	}
	return nil
}

// TimeWindow, zaman aralığı süzgecidir.
type TimeWindow struct {
	From, To time.Time
}

// Resolve, eksik uçları ADR-13'ün varsayılanıyla tamamlar.
//
// # Neden varsayılan pencere zorunlu
//
// ADR-13 görselleştirmenin **daima filtreli** olmasını istiyor. Zaman aralığı
// boş bırakılabilseydi tek istek 30 günün tamamını çekerdi; 500 geometri
// sınırı bunu reddederdi ama kullanıcı nedenini anlamazdı. Varsayılan pencere,
// sınırın anlamlı bir istekle karşılanmasını sağlar.
func (w TimeWindow) Resolve() (TimeWindow, error) {
	switch {
	case w.From.IsZero() && w.To.IsZero():
		// Her iki uç da boş: çağıran koşunun zaman aralığını bilmiyor.
		// Süzgeç uygulanmaz; geometri sınırı yine de korur.
		return TimeWindow{}, nil
	case w.From.IsZero():
		return TimeWindow{From: w.To.Add(-DefaultTimeWindow), To: w.To}, nil
	case w.To.IsZero():
		return TimeWindow{From: w.From, To: w.From.Add(DefaultTimeWindow)}, nil
	case !w.To.After(w.From):
		return TimeWindow{}, fmt.Errorf("zaman aralığı: 'to' > 'from' olmalı")
	}
	return w, nil
}

// Active, süzgecin uygulanıp uygulanmayacağını bildirir.
func (w TimeWindow) Active() bool { return !w.From.IsZero() && !w.To.IsZero() }

// CellsFilter, baz istasyonu ucunun süzgecidir.
type CellsFilter struct {
	RunID uuid.UUID
	BBox  BBox
}

// Validate, zorunlu alanları denetler (ADR-13: run_id daima zorunlu).
func (f CellsFilter) Validate() error {
	if f.RunID == uuid.Nil {
		return ErrRunIDRequired
	}
	return f.BBox.Validate()
}

// EstimatesFilter, olasılık geometrisi ucunun süzgecidir.
//
// ADR-13 üç zorunlu süzgeç istiyor: run_id + abone + zaman aralığı. Abone
// filtresi burada `pseudo_msisdn`'dir (ADR-33/3).
type EstimatesFilter struct {
	RunID      uuid.UUID
	Subscriber string
	Window     TimeWindow
	Method     string
	Confidence float64
	BBox       BBox
}

// Validate, ADR-13'ün zorunlu süzgeç kuralını uygular.
func (f EstimatesFilter) Validate() error {
	if f.RunID == uuid.Nil {
		return ErrRunIDRequired
	}
	if f.Subscriber == "" {
		return ErrSubscriberRequired
	}
	switch f.Method {
	case "", "B0", "B1", "M":
	default:
		return fmt.Errorf("method 'B0', 'B1' veya 'M' olmalı (%q)", f.Method)
	}
	if f.Confidence != 0 {
		switch f.Confidence {
		case 0.50, 0.90, 0.95, -1:
		default:
			return fmt.Errorf("confidence 0.50, 0.90 veya 0.95 olmalı (%g)", f.Confidence)
		}
	}
	return f.BBox.Validate()
}

// FindingsFilter, bütünlük bulgusu ucunun süzgecidir.
type FindingsFilter struct {
	RunID         uuid.UUID
	RuleID        int
	Window        TimeWindow
	BBox          BBox
	CanonicalOnly bool
}

// Validate, zorunlu alanları denetler.
func (f FindingsFilter) Validate() error {
	if f.RunID == uuid.Nil {
		return ErrRunIDRequired
	}
	if f.RuleID != 0 && (f.RuleID < 1 || f.RuleID > 5) {
		return fmt.Errorf("rule_id 1..5 olmalı (%d)", f.RuleID)
	}
	return f.BBox.Validate()
}
