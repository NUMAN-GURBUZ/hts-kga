// KT9 — API Gateway kabul testi (S7, ADR-33/1).
//
// ADR-33 KT9'u yedi ölçülebilir maddeye bağladı; bu dosya onları sınar.
// Yeni eşik icat edilmez: hepsi ADR-13'ün zaten karara bağladığı kısıtlardır.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/gateway/audit"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/gateway/query"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/gateway/rest"
)

// envGatewayDSN, gateway'in KENDİ rolüyle bağlanacağı DSN'dir.
//
// Testin `hts_admin` ile koşması KT9.7'yi anlamsız kılardı: kör testin
// gateway'e kadar uzadığını kanıtlamak, gateway'in rolüyle denemeyi gerektirir.
const envGatewayDSN = "HTS_TEST_GATEWAY_DSN"

// gatewayFixture, testin ihtiyaç duyduğu ortamdır.
type gatewayFixture struct {
	srv *httptest.Server
	// db, gateway'in KENDİ rolüdür (svc_gateway).
	db *pgxpool.Pool
	// auditDB, denetim izini DOĞRULAMAK için ayrı bir bağlantıdır.
	//
	// svc_gateway `audit_log`'a INSERT eder ama SELECT **edemez** — denetlenen
	// taraf denetim kaydını okuyamamalıdır (en az yetki). Testin doğrulayıcı
	// rolü ayrı bir kimlikle bağlanır; bu ayrım tasarımın kanıtıdır, engeli
	// değil.
	auditDB *pgxpool.Pool
	runID   uuid.UUID
	subject string
}

// setupGateway, gateway'i svc_gateway rolüyle ayağa kaldırır ve içinde veri
// bulunan bir koşu seçer.
func setupGateway(t *testing.T) gatewayFixture {
	t.Helper()

	dsn := os.Getenv(envGatewayDSN)
	if dsn == "" {
		t.Skipf("gateway testi atlandı: %s tanımlı değil", envGatewayDSN)
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("svc_gateway bağlantısı: %v", err)
	}
	t.Cleanup(db.Close)

	// Verisi olan bir koşu seç: bulgusu ve tahmini olan koşular farklı
	// olabilir, bu yüzden en çok bulgusu olan seçilir (KT9.2 sınır aşımı
	// oradan sınanır).
	var runID uuid.UUID
	err = db.QueryRow(ctx, `
        SELECT run_id FROM integrity_findings
         GROUP BY run_id ORDER BY count(*) DESC LIMIT 1`).Scan(&runID)
	if err != nil {
		t.Skipf("bulgu içeren koşu yok: %v", err)
	}

	var subject string
	if err := db.QueryRow(ctx,
		`SELECT pseudo_msisdn FROM hts_records WHERE run_id = $1 LIMIT 1`,
		runID).Scan(&subject); err != nil {
		t.Fatalf("abone seçilemedi: %v", err)
	}

	svc, err := query.New(db)
	if err != nil {
		t.Fatalf("query.New: %v", err)
	}
	auditor, err := audit.New(db, nil)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	handler, err := rest.New(rest.Config{Service: svc, Auditor: auditor})
	if err != nil {
		t.Fatalf("rest.New: %v", err)
	}

	srv := httptest.NewServer(handler.Routes())
	t.Cleanup(srv.Close)

	// Denetim izini okuyacak ayrı bağlantı (bkz. gatewayFixture.auditDB).
	var auditDB *pgxpool.Pool
	if adminDSN := os.Getenv(envPGDSN); adminDSN != "" {
		if pool, err := pgxpool.New(ctx, adminDSN); err == nil {
			auditDB = pool
			t.Cleanup(pool.Close)
		}
	}

	return gatewayFixture{srv: srv, db: db, auditDB: auditDB, runID: runID, subject: subject}
}

// get, bir uca istek atar ve durum kodu + gövdeyi döndürür.
func (f gatewayFixture) get(t *testing.T, path string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(f.srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// ─── KT9.1 — zorunlu süzgeç ───────────────────────────────────────────────────

// TestKT9_1_RunIDRequired, ADR-13'ün "görselleştirme daima filtrelidir"
// kuralını sınar.
func TestKT9_1_RunIDRequired(t *testing.T) {
	f := setupGateway(t)

	for _, path := range []string{
		"/api/v1/cells",
		"/api/v1/estimates?subscriber=x",
		"/api/v1/findings",
		"/api/v1/metrics",
		"/api/v1/integrity-metrics",
		"/api/v1/aggregate/cell-activity",
	} {
		t.Run(path, func(t *testing.T) {
			status, body := f.get(t, path)
			if status != http.StatusBadRequest {
				t.Errorf("HTTP %d, beklenen 400 (%s)", status, body)
			}
			if !strings.Contains(string(body), "run_id") {
				t.Errorf("hata mesajı run_id'yi işaret etmeli: %s", body)
			}
		})
	}

	// Tahmin ucunda abone de zorunludur (ADR-13: tek abone, tek aralık).
	status, body := f.get(t, "/api/v1/estimates?run_id="+f.runID.String())
	if status != http.StatusBadRequest || !strings.Contains(string(body), "subscriber") {
		t.Errorf("abonesiz estimates 400 vermeli: HTTP %d %s", status, body)
	}
}

// ─── KT9.2 — 500 geometri sınırı ──────────────────────────────────────────────

// TestKT9_2_GeometryLimit, ADR-13'ün üst sınırının **reddederek** uygulandığını
// sınar.
//
// Sessizce kırpmak, kullanıcıya eksik bir haritayı tam sanmasına yol açardı;
// ADR-13 açıkça "aşılırsa 400 + açıklama" diyor ve açıklama nasıl
// daraltılacağını söylemelidir.
func TestKT9_2_GeometryLimit(t *testing.T) {
	f := setupGateway(t)

	status, body := f.get(t, "/api/v1/findings?run_id="+f.runID.String())
	if status != http.StatusBadRequest {
		t.Fatalf("sınır aşımı 400 vermeliydi: HTTP %d (%s)", status, body)
	}

	msg := string(body)
	for _, want := range []string{"500", "ADR-13"} {
		if !strings.Contains(msg, want) {
			t.Errorf("açıklama %q içermeli: %s", want, msg)
		}
	}
	// Açıklama eyleme dönüştürülebilir olmalı.
	if !strings.Contains(msg, "rule_id") && !strings.Contains(msg, "zaman") {
		t.Errorf("açıklama nasıl daraltılacağını söylemeli: %s", msg)
	}

	// Daraltılmış istek geçmeli — sınırın aşılamaz değil, filtrelenebilir
	// olduğunu gösterir.
	status, body = f.get(t, "/api/v1/findings?run_id="+f.runID.String()+"&rule_id=2")
	if status != http.StatusOK {
		t.Errorf("daraltılmış istek 200 vermeliydi: HTTP %d (%s)", status, body)
	}
}

// ─── KT9.3 — GeoJSON geçerliliği ──────────────────────────────────────────────

// TestKT9_3_ValidGeoJSON, her geometri ucunun geçerli FeatureCollection
// döndürdüğünü sınar.
func TestKT9_3_ValidGeoJSON(t *testing.T) {
	f := setupGateway(t)

	paths := []string{
		"/api/v1/cells?run_id=" + f.runID.String(),
		"/api/v1/findings?run_id=" + f.runID.String() + "&rule_id=2",
		"/api/v1/estimates?run_id=" + f.runID.String() + "&subscriber=" + f.subject,
	}

	for _, path := range paths {
		t.Run(path[:strings.Index(path, "?")], func(t *testing.T) {
			status, body := f.get(t, path)
			if status != http.StatusOK {
				t.Fatalf("HTTP %d: %s", status, body)
			}

			var doc struct {
				Type     string `json:"type"`
				Features []struct {
					Type     string          `json:"type"`
					Geometry json.RawMessage `json:"geometry"`
				} `json:"features"`
			}
			if err := json.Unmarshal(body, &doc); err != nil {
				t.Fatalf("geçerli JSON değil: %v", err)
			}
			if doc.Type != "FeatureCollection" {
				t.Errorf("type = %q, beklenen FeatureCollection", doc.Type)
			}
			for i, feat := range doc.Features {
				if feat.Type != "Feature" {
					t.Errorf("features[%d].type = %q", i, feat.Type)
				}
			}
		})
	}
}

// ─── KT9.4 — viewport süzgeci ─────────────────────────────────────────────────

// TestKT9_4_BBoxNarrowsResult, bbox'ın ST_Intersects ile uygulandığını sınar.
func TestKT9_4_BBoxNarrowsResult(t *testing.T) {
	f := setupGateway(t)
	base := "/api/v1/cells?run_id=" + f.runID.String()

	status, body := f.get(t, base)
	if status != http.StatusOK {
		t.Fatalf("HTTP %d: %s", status, body)
	}
	full := countFeatures(t, body)
	if full == 0 {
		t.Skip("koşuda hücre yok")
	}

	// Koşunun merkezini bul ve çevresinde küçük bir kutu iste.
	var lon, lat float64
	if err := f.db.QueryRow(context.Background(), `
        SELECT ST_X(ST_Centroid(ST_Collect(location::geometry))),
               ST_Y(ST_Centroid(ST_Collect(location::geometry)))
          FROM cells WHERE run_id = $1`, f.runID).Scan(&lon, &lat); err != nil {
		t.Fatalf("merkez hesaplanamadı: %v", err)
	}

	const d = 0.01 // ~1 km
	narrow := fmt.Sprintf("%s&bbox=%f,%f,%f,%f", base, lon-d, lat-d, lon+d, lat+d)
	status, body = f.get(t, narrow)
	if status != http.StatusOK {
		t.Fatalf("bbox isteği HTTP %d: %s", status, body)
	}
	clipped := countFeatures(t, body)

	if clipped >= full {
		t.Errorf("bbox sonucu daraltmadı: %d → %d", full, clipped)
	}
	t.Logf("bbox süzgeci: %d → %d hücre", full, clipped)

	// Geçersiz bbox reddedilmeli.
	status, _ = f.get(t, base+"&bbox=1,2,3")
	if status != http.StatusBadRequest {
		t.Errorf("eksik bbox 400 vermeliydi: HTTP %d", status)
	}
}

// ─── KT9.5 — k-anonimlik ──────────────────────────────────────────────────────

// TestKT9_5_KAnonymity, aggregate uçta k=5 eşiğinin uygulandığını sınar
// (ADR-15, ADR-33/5).
func TestKT9_5_KAnonymity(t *testing.T) {
	f := setupGateway(t)

	status, body := f.get(t, "/api/v1/aggregate/cell-activity?run_id="+f.runID.String())
	if status != http.StatusOK {
		t.Fatalf("HTTP %d: %s", status, body)
	}

	var resp struct {
		FeatureCollection json.RawMessage `json:"feature_collection"`
		Count             int             `json:"count"`
		SuppressedCells   int             `json:"suppressed_cells"`
		K                 int             `json:"k"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("yanıt çözümlenemedi: %v — %s", err, body)
	}
	if resp.K != query.KAnonymity {
		t.Errorf("k = %d, beklenen %d", resp.K, query.KAnonymity)
	}

	// Döndürülen HER hücrenin abone sayısı k'nın üstünde olmalı — kuralın
	// gerçekten uygulandığının kanıtı.
	var coll struct {
		Features []struct {
			Properties struct {
				Subscribers int `json:"subscribers"`
			} `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(resp.FeatureCollection, &coll); err != nil {
		t.Fatalf("koleksiyon çözümlenemedi: %v", err)
	}
	for i, feat := range coll.Features {
		if feat.Properties.Subscribers < query.KAnonymity {
			t.Errorf("features[%d]: %d abone < k=%d — k-anonimlik delinmiş",
				i, feat.Properties.Subscribers, query.KAnonymity)
		}
	}

	// Bastırma sessiz olmamalı.
	var belowK int64
	if err := f.db.QueryRow(context.Background(), `
        SELECT count(*) FROM (
            SELECT h.cell_id FROM hts_records h
              JOIN cells c ON c.run_id = h.run_id AND c.cell_id = h.cell_id
             WHERE h.run_id = $1
             GROUP BY h.cell_id
            HAVING count(DISTINCT h.pseudo_msisdn) < $2) x`,
		f.runID, query.KAnonymity).Scan(&belowK); err != nil {
		t.Fatalf("bastırılan sayımı: %v", err)
	}
	if int64(resp.SuppressedCells) != belowK {
		t.Errorf("suppressed_cells = %d, veritabanı %d diyor", resp.SuppressedCells, belowK)
	}
	t.Logf("k=%d · döndürülen %d hücre · bastırılan %d", resp.K, resp.Count, resp.SuppressedCells)
}

// ─── KT9.6 — denetim izi ──────────────────────────────────────────────────────

// TestKT9_6_AuditTrail, her API isteğinin audit_log'a yazıldığını sınar
// (ADR-15).
func TestKT9_6_AuditTrail(t *testing.T) {
	f := setupGateway(t)
	if f.auditDB == nil {
		t.Skipf("denetim doğrulaması atlandı: %s tanımlı değil", envPGDSN)
	}
	ctx := context.Background()

	// svc_gateway audit_log'u OKUYAMAZ — bu, en az yetkinin kanıtıdır.
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(new(int64)); err == nil {
		t.Error("gateway kendi denetim kaydını okuyabildi — en az yetki ihlali")
	}

	before := f.auditCount(t, ctx)

	if status, body := f.get(t, "/api/v1/cells?run_id="+f.runID.String()); status != http.StatusOK {
		t.Fatalf("HTTP %d: %s", status, body)
	}
	// Hatalı istek de iz bırakmalı: reddedilen erişim de bir erişim denemesidir.
	f.get(t, "/api/v1/cells")

	after := f.auditCount(t, ctx)
	if after < before+2 {
		t.Errorf("denetim satırı yazılmadı: %d → %d (en az +2 bekleniyordu)", before, after)
	}

	var principal string
	if err := f.auditDB.QueryRow(ctx, `
        SELECT principal FROM audit_log ORDER BY audit_id DESC LIMIT 1`).Scan(&principal); err != nil {
		t.Fatalf("son denetim satırı okunamadı: %v", err)
	}
	if principal != audit.Principal {
		t.Errorf("principal = %q, beklenen %q (ADR-15)", principal, audit.Principal)
	}
}

func (f gatewayFixture) auditCount(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	var n int64
	if err := f.auditDB.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE principal = $1`, audit.Principal).Scan(&n); err != nil {
		t.Fatalf("denetim sayımı: %v", err)
	}
	return n
}

// ─── KT9.7 — kör test API katmanına kadar uzar ────────────────────────────────

// TestKT9_7_BlindTestHoldsAtAPI, gateway'in ground_truth'u göremediğini sınar
// (ADR-33/2).
//
// Bu, K6'nın beşinci uygulama noktasıdır: yeni bir okuma yüzeyi açılırken
// altı sprintlik kör test yatırımı korunuyor mu?
func TestKT9_7_BlindTestHoldsAtAPI(t *testing.T) {
	f := setupGateway(t)
	ctx := context.Background()

	// Rol düzeyinde: svc_gateway ground_truth'a erişemez.
	var n int64
	err := f.db.QueryRow(ctx, `SELECT count(*) FROM ground_truth`).Scan(&n)
	if err == nil {
		t.Fatal("gateway ground_truth'u OKUYABİLDİ — kör test API katmanında delik (ADR-33/2)")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
		t.Errorf("beklenen 'permission denied', gelen: %v", err)
	}
	t.Logf("kör test korunuyor: %v", err)

	// Yanıt düzeyinde: hiçbir uç enjeksiyon etiketi ya da gerçek konum
	// sızdırmamalı.
	paths := []string{
		"/api/v1/cells?run_id=" + f.runID.String(),
		"/api/v1/findings?run_id=" + f.runID.String() + "&rule_id=2",
		"/api/v1/integrity-metrics?run_id=" + f.runID.String(),
	}
	banned := []string{"injected_rule", "true_location", "agent_id", "partition_key"}

	for _, path := range paths {
		_, body := f.get(t, path)
		lower := strings.ToLower(string(body))
		for _, word := range banned {
			if strings.Contains(lower, word) {
				t.Errorf("%s yanıtında yasaklı alan %q var — kör test sızıntısı", path, word)
			}
		}
	}
}

// countFeatures, FeatureCollection'daki geometri sayısını döndürür.
func countFeatures(t *testing.T, body []byte) int {
	t.Helper()
	var doc struct {
		Features []json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("koleksiyon çözümlenemedi: %v", err)
	}
	return len(doc.Features)
}
