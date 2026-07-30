// Package rest, elle yazılmış REST handler'larıdır (T-E06-03, ADR-12/1).
//
// # Neden elle
//
// ADR-12/1 `grpc-gateway`'i reddetti: yedi uç için kod üretim zinciri (protoc
// eklentileri, buf yapılandırması) kurmak sağladığı faydadan pahalı. Bu
// handler'lar `internal/gateway/query` katmanını çağırır — gRPC sunucusuyla
// **aynı** katmanı. İkisi arasında kod üretimi yoktur.
//
// # Hata sınıflandırması tek yerde
//
// `query.IsBadRequest` kararı verir; bu paket onu 400'e, gRPC katmanı
// `InvalidArgument`'a çevirir. Sınıflandırma iki yerde yapılsaydı biri ADR-13
// sınırını 400 döndürür, diğeri 500 döndürürdü.
package rest

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/gateway/audit"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/gateway/query"
)

// Server, REST yüzeyidir.
type Server struct {
	svc     *query.Service
	auditor *audit.Logger
	log     *slog.Logger
	static  string
}

// Config, sunucunun kurulum parametreleridir.
type Config struct {
	Service *query.Service
	Auditor *audit.Logger
	Logger  *slog.Logger
	// StaticDir, `/static` altında servis edilecek dizindir (ADR-12/3).
	// Boşsa statik servis kapalıdır.
	StaticDir string
}

// New, REST sunucusunu kurar.
func New(cfg Config) (*Server, error) {
	if cfg.Service == nil {
		return nil, fmt.Errorf("rest: sorgu katmanı zorunlu")
	}
	if cfg.Auditor == nil {
		return nil, fmt.Errorf("rest: denetim günlüğü zorunlu (ADR-15)")
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Server{svc: cfg.Service, auditor: cfg.Auditor, log: log, static: cfg.StaticDir}, nil
}

// Routes, HTTP yönlendirmelerini kurar.
//
// ADR-12/3: `cmd/visualization` kaldırıldı; `web/` altındaki statik dosyalar
// doğrudan gateway tarafından servis edilir. CORS problemi de böylece ortadan
// kalkar — sayfa ve API aynı kökenden gelir.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/runs", s.handleRuns)
	mux.HandleFunc("GET /api/v1/cells", s.handleCells)
	mux.HandleFunc("GET /api/v1/estimates", s.handleEstimates)
	mux.HandleFunc("GET /api/v1/findings", s.handleFindings)
	mux.HandleFunc("GET /api/v1/metrics", s.handleMetrics)
	mux.HandleFunc("GET /api/v1/integrity-metrics", s.handleIntegrityMetrics)
	mux.HandleFunc("GET /api/v1/aggregate/cell-activity", s.handleCellActivity)

	if s.static != "" {
		mux.Handle("GET /static/", http.StripPrefix("/static/",
			http.FileServer(http.Dir(s.static))))
		// Kök yol demo sayfasına yönlenir: hocaya "tarayıcıda şu adresi aç"
		// demek, "şu alt yolu aç" demekten kolaydır.
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/static/index.html", http.StatusFound)
		})
	}
	return mux
}

// ─── Uçlar ────────────────────────────────────────────────────────────────────

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := query.WithTimeout(r.Context())
	defer cancel()

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := s.svc.ListRuns(ctx, limit)
	if err != nil {
		s.fail(w, r, "api_runs", "run_config", uuid.Nil, err)
		return
	}

	out := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		out = append(out, map[string]any{
			"run_id":            run.RunID,
			"scenario":          run.Scenario,
			"morphology":        run.Morphology,
			"seed":              run.Seed,
			"started_at":        run.StartedAt,
			"finished_at":       run.FinishedAt,
			"published_events":  run.PublishedEvents,
			"analyzed_events":   run.AnalyzedEvents,
			"inspected_records": run.InspectedRecords,
			"lambda":            run.Lambda,
		})
	}

	s.audit(r, "api_runs", "run_config", uuid.Nil, map[string]any{"count": len(out)})
	s.writeJSON(w, http.StatusOK, map[string]any{"runs": out})
}

func (s *Server) handleCells(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := query.WithTimeout(r.Context())
	defer cancel()

	runID, err := runIDParam(r)
	if err != nil {
		s.fail(w, r, "api_cells", "cells", uuid.Nil, err)
		return
	}
	bbox, err := bboxParam(r)
	if err != nil {
		s.fail(w, r, "api_cells", "cells", runID, err)
		return
	}

	doc, err := s.svc.Cells(ctx, query.CellsFilter{RunID: runID, BBox: bbox})
	if err != nil {
		s.fail(w, r, "api_cells", "cells", runID, err)
		return
	}

	s.audit(r, "api_cells", "cells", runID,
		map[string]any{"count": doc.Count, "bbox": bbox.Set})
	s.writeGeoJSON(w, doc)
}

func (s *Server) handleEstimates(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := query.WithTimeout(r.Context())
	defer cancel()

	runID, err := runIDParam(r)
	if err != nil {
		s.fail(w, r, "api_estimates", "estimates", uuid.Nil, err)
		return
	}

	q := r.URL.Query()
	window, err := windowParam(r)
	if err != nil {
		s.fail(w, r, "api_estimates", "estimates", runID, err)
		return
	}
	bbox, err := bboxParam(r)
	if err != nil {
		s.fail(w, r, "api_estimates", "estimates", runID, err)
		return
	}
	confidence, err := floatParam(q.Get("confidence"))
	if err != nil {
		s.fail(w, r, "api_estimates", "estimates", runID,
			fmt.Errorf("confidence sayı olmalı: %w", err))
		return
	}

	filter := query.EstimatesFilter{
		RunID:      runID,
		Subscriber: q.Get("subscriber"),
		Window:     window,
		Method:     strings.ToUpper(q.Get("method")),
		Confidence: confidence,
		BBox:       bbox,
	}
	doc, err := s.svc.Estimates(ctx, filter)
	if err != nil {
		s.fail(w, r, "api_estimates", "estimates", runID, err)
		return
	}

	s.audit(r, "api_estimates", "estimates", runID, map[string]any{
		"count": doc.Count, "subscriber": filter.Subscriber,
		"method": filter.Method, "bbox": bbox.Set,
	})
	s.writeGeoJSON(w, doc)
}

func (s *Server) handleFindings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := query.WithTimeout(r.Context())
	defer cancel()

	runID, err := runIDParam(r)
	if err != nil {
		s.fail(w, r, "api_findings", "integrity_findings", uuid.Nil, err)
		return
	}
	window, err := windowParam(r)
	if err != nil {
		s.fail(w, r, "api_findings", "integrity_findings", runID, err)
		return
	}
	bbox, err := bboxParam(r)
	if err != nil {
		s.fail(w, r, "api_findings", "integrity_findings", runID, err)
		return
	}
	ruleID, _ := strconv.Atoi(r.URL.Query().Get("rule_id"))

	filter := query.FindingsFilter{
		RunID: runID, RuleID: ruleID, Window: window, BBox: bbox,
		// Varsayılan kanonik: F.5 kanonik bulgular üzerinde ölçüyor
		// (ADR-28/4), harita da aynı kümeyi göstermelidir.
		CanonicalOnly: r.URL.Query().Get("all") != "true",
	}
	doc, err := s.svc.Findings(ctx, filter)
	if err != nil {
		s.fail(w, r, "api_findings", "integrity_findings", runID, err)
		return
	}

	s.audit(r, "api_findings", "integrity_findings", runID, map[string]any{
		"count": doc.Count, "rule_id": ruleID, "canonical_only": filter.CanonicalOnly,
	})
	s.writeGeoJSON(w, doc)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := query.WithTimeout(r.Context())
	defer cancel()

	runID, err := runIDParam(r)
	if err != nil {
		s.fail(w, r, "api_metrics", "metrics", uuid.Nil, err)
		return
	}
	rows, err := s.svc.Metrics(ctx, runID)
	if err != nil {
		s.fail(w, r, "api_metrics", "metrics", runID, err)
		return
	}

	s.audit(r, "api_metrics", "metrics", runID, map[string]any{"count": len(rows)})
	s.writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

func (s *Server) handleIntegrityMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := query.WithTimeout(r.Context())
	defer cancel()

	runID, err := runIDParam(r)
	if err != nil {
		s.fail(w, r, "api_integrity_metrics", "integrity_metrics", uuid.Nil, err)
		return
	}
	rows, err := s.svc.IntegrityMetrics(ctx, runID)
	if err != nil {
		s.fail(w, r, "api_integrity_metrics", "integrity_metrics", runID, err)
		return
	}

	s.audit(r, "api_integrity_metrics", "integrity_metrics", runID,
		map[string]any{"count": len(rows)})
	s.writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

// handleCellActivity, k-anonim hücre yoğunluğudur (ADR-33/5).
//
// Yanıt `suppressed_cells` alanını **daima** taşır: k eşiği nedeniyle gizlenen
// hücre sayısı raporlanır. Sessiz gizleme, seyrek bölgeleri haritada "aktivite
// yok" gibi gösterirdi.
func (s *Server) handleCellActivity(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := query.WithTimeout(r.Context())
	defer cancel()

	runID, err := runIDParam(r)
	if err != nil {
		s.fail(w, r, "api_cell_activity", "hts_records", uuid.Nil, err)
		return
	}
	bbox, err := bboxParam(r)
	if err != nil {
		s.fail(w, r, "api_cell_activity", "hts_records", runID, err)
		return
	}

	act, err := s.svc.CellActivity(ctx, runID, bbox)
	if err != nil {
		s.fail(w, r, "api_cell_activity", "hts_records", runID, err)
		return
	}

	s.audit(r, "api_cell_activity", "hts_records", runID, map[string]any{
		"count": act.Count, "suppressed": act.Suppressed, "k": act.K,
	})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-HTS-Count", strconv.Itoa(act.Count))
	w.Header().Set("X-HTS-Suppressed", strconv.Itoa(act.Suppressed))
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"feature_collection":%s,"count":%d,"suppressed_cells":%d,"k":%d}`,
		act.FeatureCollection, act.Count, act.Suppressed, act.K)
}

// ─── Parametre çözümleme ──────────────────────────────────────────────────────

// runIDParam, zorunlu `run_id` süzgecini çözer (ADR-13).
func runIDParam(r *http.Request) (uuid.UUID, error) {
	raw := r.URL.Query().Get("run_id")
	if raw == "" {
		return uuid.Nil, query.ErrRunIDRequired
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("run_id geçerli bir UUID olmalı: %w", err)
	}
	return id, nil
}

// bboxParam, `bbox=minLon,minLat,maxLon,maxLat` biçimini çözer (ADR-13).
func bboxParam(r *http.Request) (query.BBox, error) {
	raw := r.URL.Query().Get("bbox")
	if raw == "" {
		return query.BBox{}, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return query.BBox{}, fmt.Errorf(
			"bbox 'minLon,minLat,maxLon,maxLat' biçiminde olmalı (%q)", raw)
	}
	var v [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return query.BBox{}, fmt.Errorf("bbox[%d] sayı olmalı: %w", i, err)
		}
		v[i] = f
	}
	b := query.BBox{MinLon: v[0], MinLat: v[1], MaxLon: v[2], MaxLat: v[3], Set: true}
	return b, b.Validate()
}

// windowParam, `from`/`to` (RFC3339) zaman aralığını çözer.
func windowParam(r *http.Request) (query.TimeWindow, error) {
	q := r.URL.Query()
	var w query.TimeWindow

	if raw := q.Get("from"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return w, fmt.Errorf("from RFC3339 olmalı: %w", err)
		}
		w.From = t
	}
	if raw := q.Get("to"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return w, fmt.Errorf("to RFC3339 olmalı: %w", err)
		}
		w.To = t
	}
	return w, nil
}

// floatParam, boş olabilen bir sayı parametresini çözer.
func floatParam(raw string) (float64, error) {
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseFloat(raw, 64)
}

// ─── Yanıt yazımı ─────────────────────────────────────────────────────────────

// writeGeoJSON, GeoJSON koleksiyonunu doğrudan yazar.
//
// Metin PostGIS'ten geldiği için yeniden serileştirilmez (ADR-12/2): çözüp
// yeniden kurmak hem maliyet hem hata kaynağı olurdu.
func (s *Server) writeGeoJSON(w http.ResponseWriter, doc query.GeoJSON) {
	w.Header().Set("Content-Type", "application/geo+json; charset=utf-8")
	w.Header().Set("X-HTS-Count", strconv.Itoa(doc.Count))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(doc.FeatureCollection)); err != nil {
		s.log.Error("yanıt yazılamadı", "hata", err)
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.log.Error("yanıt serileştirilemedi", "hata", err)
	}
}

// fail, hatayı sınıflandırır, denetim izine yazar ve yanıtlar.
//
// ADR-13'ün 500 geometri sınırı ve zorunlu süzgeçleri **400** döndürür;
// açıklama gövdededir (ADR-13: "aşılırsa 400 + açıklama").
func (s *Server) fail(w http.ResponseWriter, r *http.Request,
	action, resource string, runID uuid.UUID, err error) {

	status := http.StatusInternalServerError
	var tooMany *query.TooManyGeometriesError

	switch {
	case errors.As(err, &tooMany), query.IsBadRequest(err):
		status = http.StatusBadRequest
	case errors.Is(err, query.ErrNotFound):
		status = http.StatusNotFound
	default:
		// Süzgeç doğrulama hataları sentinel değildir (mesajları alan adını
		// taşır); istemci hatası olduklarını buradan anlarız.
		if isValidationError(err) {
			status = http.StatusBadRequest
		}
	}

	if status >= 500 {
		s.log.Error("istek başarısız", "action", action, "hata", err)
	}
	s.audit(r, action+"_error", resource, runID,
		map[string]any{"status": status, "error": err.Error()})

	s.writeJSON(w, status, map[string]any{
		"error":  err.Error(),
		"status": status,
	})
}

// isValidationError, hatanın süzgeç doğrulamasından gelip gelmediğini bildirir.
//
// Sorgu katmanı bu hataları `fmt.Errorf` ile üretiyor ve alan adını mesaja
// koyuyor; sentinel'e çevirmek her alan için bir değişken demekti. Ön ek
// eşleşmesi, yüzeyin küçük olduğu bu noktada yeterli ve okunaklı.
func isValidationError(err error) bool {
	msg := err.Error()
	for _, prefix := range []string{
		"bbox", "method", "confidence", "rule_id", "zaman aralığı",
		"run_id", "from", "to", "subscriber",
	} {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}

// audit, denetim satırı yazar (ADR-15, KT9.6).
func (s *Server) audit(r *http.Request, action, resource string,
	runID uuid.UUID, detail map[string]any) {

	if detail == nil {
		detail = map[string]any{}
	}
	detail["path"] = r.URL.Path
	detail["query"] = r.URL.RawQuery

	s.auditor.Record(r.Context(), audit.Entry{
		RunID: runID, Action: action, Resource: resource, Detail: detail,
	})
}
