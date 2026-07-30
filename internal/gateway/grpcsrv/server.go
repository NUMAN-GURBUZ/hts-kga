// Package grpcsrv, iç tüketiciler için gRPC yüzeyidir (T-E06-02, ADR-12).
//
// # REST ile ilişkisi
//
// ADR-12/1: ikisi arasında kod üretimi **yoktur**. Bu paket ve
// `internal/gateway/rest` aynı `query.Service`'i çağırır; sözleşme
// `proto/hts/v1/hts.proto`, iş mantığı sorgu katmanıdır.
//
// # Hata sınıflandırması ortaktır
//
// `query.IsBadRequest` kararı verir; REST 400'e, bu paket `InvalidArgument`'a
// çevirir. İki katman aynı kuralı iki farklı protokolde konuşur.
package grpcsrv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/NUMAN-GURBUZ/hts-kga/internal/gateway/audit"
	"github.com/NUMAN-GURBUZ/hts-kga/internal/gateway/query"
	htsv1 "github.com/NUMAN-GURBUZ/hts-kga/proto/hts/v1"
)

// Server, HtsService'i uygular.
type Server struct {
	htsv1.UnimplementedHtsServiceServer

	svc     *query.Service
	auditor *audit.Logger
}

// New, gRPC sunucusunu kurar.
func New(svc *query.Service, auditor *audit.Logger) (*Server, error) {
	if svc == nil {
		return nil, fmt.Errorf("grpc: sorgu katmanı zorunlu")
	}
	if auditor == nil {
		return nil, fmt.Errorf("grpc: denetim günlüğü zorunlu (ADR-15)")
	}
	return &Server{svc: svc, auditor: auditor}, nil
}

// ─── Uçlar ────────────────────────────────────────────────────────────────────

// ListRuns, koşuları döndürür.
func (s *Server) ListRuns(ctx context.Context, req *htsv1.ListRunsRequest) (*htsv1.ListRunsResponse, error) {
	runs, err := s.svc.ListRuns(ctx, int(req.GetLimit()))
	if err != nil {
		return nil, s.fail(ctx, "grpc_runs", "run_config", uuid.Nil, err)
	}

	out := &htsv1.ListRunsResponse{Runs: make([]*htsv1.Run, 0, len(runs))}
	for _, r := range runs {
		item := &htsv1.Run{
			RunId:      r.RunID.String(),
			Scenario:   r.Scenario,
			Morphology: r.Morphology,
			Seed:       r.Seed,
			StartedAt:  r.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		if r.FinishedAt != nil {
			item.FinishedAt = r.FinishedAt.Format("2006-01-02T15:04:05Z07:00")
		}
		item.PublishedEvents = deref(r.PublishedEvents)
		item.AnalyzedEvents = deref(r.AnalyzedEvents)
		item.InspectedRecords = deref(r.InspectedRecords)
		if r.Lambda != nil {
			item.Lambda, item.HasLambda = *r.Lambda, true
		}
		out.Runs = append(out.Runs, item)
	}

	s.record(ctx, "grpc_runs", "run_config", uuid.Nil, map[string]any{"count": len(out.Runs)})
	return out, nil
}

// GetCells, baz istasyonlarını GeoJSON olarak döndürür.
func (s *Server) GetCells(ctx context.Context, req *htsv1.GetCellsRequest) (*htsv1.GeoJsonResponse, error) {
	runID, err := parseRunID(req.GetRunId())
	if err != nil {
		return nil, s.fail(ctx, "grpc_cells", "cells", uuid.Nil, err)
	}

	doc, err := s.svc.Cells(ctx, query.CellsFilter{RunID: runID, BBox: toBBox(req.GetBbox())})
	if err != nil {
		return nil, s.fail(ctx, "grpc_cells", "cells", runID, err)
	}

	s.record(ctx, "grpc_cells", "cells", runID, map[string]any{"count": doc.Count})
	return geoResponse(doc), nil
}

// GetEstimates, olasılık geometrilerini döndürür.
func (s *Server) GetEstimates(ctx context.Context, req *htsv1.GetEstimatesRequest) (*htsv1.GeoJsonResponse, error) {
	runID, err := parseRunID(req.GetRunId())
	if err != nil {
		return nil, s.fail(ctx, "grpc_estimates", "estimates", uuid.Nil, err)
	}
	window, err := toWindow(req.GetTimeRange())
	if err != nil {
		return nil, s.fail(ctx, "grpc_estimates", "estimates", runID, err)
	}

	doc, err := s.svc.Estimates(ctx, query.EstimatesFilter{
		RunID:      runID,
		Subscriber: req.GetSubscriber(),
		Window:     window,
		Method:     req.GetMethod(),
		Confidence: req.GetConfidence(),
		BBox:       toBBox(req.GetBbox()),
	})
	if err != nil {
		return nil, s.fail(ctx, "grpc_estimates", "estimates", runID, err)
	}

	s.record(ctx, "grpc_estimates", "estimates", runID, map[string]any{"count": doc.Count})
	return geoResponse(doc), nil
}

// GetFindings, bütünlük bulgularını döndürür.
func (s *Server) GetFindings(ctx context.Context, req *htsv1.GetFindingsRequest) (*htsv1.GeoJsonResponse, error) {
	runID, err := parseRunID(req.GetRunId())
	if err != nil {
		return nil, s.fail(ctx, "grpc_findings", "integrity_findings", uuid.Nil, err)
	}
	window, err := toWindow(req.GetTimeRange())
	if err != nil {
		return nil, s.fail(ctx, "grpc_findings", "integrity_findings", runID, err)
	}

	doc, err := s.svc.Findings(ctx, query.FindingsFilter{
		RunID:         runID,
		RuleID:        int(req.GetRuleId()),
		Window:        window,
		BBox:          toBBox(req.GetBbox()),
		CanonicalOnly: req.GetCanonicalOnly(),
	})
	if err != nil {
		return nil, s.fail(ctx, "grpc_findings", "integrity_findings", runID, err)
	}

	s.record(ctx, "grpc_findings", "integrity_findings", runID, map[string]any{"count": doc.Count})
	return geoResponse(doc), nil
}

// GetMetrics, K1–K3 ölçümlerini döndürür.
func (s *Server) GetMetrics(ctx context.Context, req *htsv1.GetMetricsRequest) (*htsv1.GetMetricsResponse, error) {
	runID, err := parseRunID(req.GetRunId())
	if err != nil {
		return nil, s.fail(ctx, "grpc_metrics", "metrics", uuid.Nil, err)
	}
	rows, err := s.svc.Metrics(ctx, runID)
	if err != nil {
		return nil, s.fail(ctx, "grpc_metrics", "metrics", runID, err)
	}

	out := &htsv1.GetMetricsResponse{Rows: make([]*htsv1.MetricRow, 0, len(rows))}
	for _, m := range rows {
		row := &htsv1.MetricRow{
			Scenario: m.Scenario, Method: m.Method, Confidence: m.Confidence,
			PartitionKey: m.PartitionKey, NEvents: m.NEvents,
			CoverageRate: m.CoverageRate, MedianAreaKm2: m.MedianAreaKM2,
			P90AreaKm2: m.P90AreaKM2, R50M: m.R50M, R95M: m.R95M,
			P95PartCount: int32(m.P95PartCount), RepairedRatio: m.RepairedRatio,
		}
		if m.ReductionVsB0 != nil {
			row.ReductionVsB0 = *m.ReductionVsB0
		}
		if m.ReductionVsB1 != nil {
			row.ReductionVsB1 = *m.ReductionVsB1
		}
		out.Rows = append(out.Rows, row)
	}

	s.record(ctx, "grpc_metrics", "metrics", runID, map[string]any{"count": len(out.Rows)})
	return out, nil
}

// GetIntegrityMetrics, K7 ölçümlerini döndürür.
func (s *Server) GetIntegrityMetrics(ctx context.Context, req *htsv1.GetIntegrityMetricsRequest) (*htsv1.GetIntegrityMetricsResponse, error) {
	runID, err := parseRunID(req.GetRunId())
	if err != nil {
		return nil, s.fail(ctx, "grpc_integrity_metrics", "integrity_metrics", uuid.Nil, err)
	}
	rows, err := s.svc.IntegrityMetrics(ctx, runID)
	if err != nil {
		return nil, s.fail(ctx, "grpc_integrity_metrics", "integrity_metrics", runID, err)
	}

	out := &htsv1.GetIntegrityMetricsResponse{Rows: make([]*htsv1.IntegrityMetricRow, 0, len(rows))}
	for _, m := range rows {
		row := &htsv1.IntegrityMetricRow{
			RuleId: int32(m.RuleID), RuleName: m.RuleName,
			Findings: m.Findings, TruePositives: m.TruePositives,
			Injected: m.Injected, Recall: m.Recall, Sufficient: m.Sufficient,
		}
		// ADR-30: kural 4'te precision NULL'dır ve bu "ölçülemedi" demektir,
		// "sıfır" değil. Ayrım `has_precision` ile taşınır.
		if m.Precision != nil {
			row.Precision, row.HasPrecision = *m.Precision, true
		}
		if m.PrecisionLo != nil {
			row.PrecisionLo = *m.PrecisionLo
		}
		if m.PrecisionHi != nil {
			row.PrecisionHi = *m.PrecisionHi
		}
		out.Rows = append(out.Rows, row)
	}

	s.record(ctx, "grpc_integrity_metrics", "integrity_metrics", runID,
		map[string]any{"count": len(out.Rows)})
	return out, nil
}

// GetCellActivity, k-anonim hücre yoğunluğunu döndürür (ADR-33/5).
func (s *Server) GetCellActivity(ctx context.Context, req *htsv1.GetCellActivityRequest) (*htsv1.GetCellActivityResponse, error) {
	runID, err := parseRunID(req.GetRunId())
	if err != nil {
		return nil, s.fail(ctx, "grpc_cell_activity", "hts_records", uuid.Nil, err)
	}

	act, err := s.svc.CellActivity(ctx, runID, toBBox(req.GetBbox()))
	if err != nil {
		return nil, s.fail(ctx, "grpc_cell_activity", "hts_records", runID, err)
	}

	s.record(ctx, "grpc_cell_activity", "hts_records", runID, map[string]any{
		"count": act.Count, "suppressed": act.Suppressed, "k": act.K,
	})
	return &htsv1.GetCellActivityResponse{
		FeatureCollection: act.FeatureCollection,
		Count:             int32(act.Count),
		SuppressedCells:   int32(act.Suppressed),
		K:                 int32(act.K),
	}, nil
}

// ─── Yardımcılar ──────────────────────────────────────────────────────────────

func parseRunID(raw string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, query.ErrRunIDRequired
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("run_id geçerli bir UUID olmalı: %w", err)
	}
	return id, nil
}

func toBBox(b *htsv1.BoundingBox) query.BBox {
	if b == nil {
		return query.BBox{}
	}
	return query.BBox{
		MinLon: b.GetMinLon(), MinLat: b.GetMinLat(),
		MaxLon: b.GetMaxLon(), MaxLat: b.GetMaxLat(), Set: true,
	}
}

func toWindow(tr *htsv1.TimeRange) (query.TimeWindow, error) {
	var w query.TimeWindow
	if tr == nil {
		return w, nil
	}
	const layout = "2006-01-02T15:04:05Z07:00"
	if tr.GetFrom() != "" {
		t, err := parseTime(layout, tr.GetFrom())
		if err != nil {
			return w, fmt.Errorf("from RFC3339 olmalı: %w", err)
		}
		w.From = t
	}
	if tr.GetTo() != "" {
		t, err := parseTime(layout, tr.GetTo())
		if err != nil {
			return w, fmt.Errorf("to RFC3339 olmalı: %w", err)
		}
		w.To = t
	}
	return w, nil
}

// geoResponse, sorgu çıktısını proto yanıtına çevirir.
//
// `Truncated` daima false'tur: ADR-13 sınırı aşıldığında istek REDDEDİLİR,
// sessizce kırpılmaz. Alan sözleşmede bu davranışı beyan etmek için var.
func geoResponse(doc query.GeoJSON) *htsv1.GeoJsonResponse {
	return &htsv1.GeoJsonResponse{
		FeatureCollection: doc.FeatureCollection,
		Count:             int32(doc.Count),
		Truncated:         false,
	}
}

func deref(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

// fail, hatayı gRPC koduna çevirir ve denetim izine yazar.
func (s *Server) fail(ctx context.Context, action, resource string,
	runID uuid.UUID, err error) error {

	code := codes.Internal
	var tooMany *query.TooManyGeometriesError
	switch {
	case errors.As(err, &tooMany), query.IsBadRequest(err):
		code = codes.InvalidArgument
	case errors.Is(err, query.ErrNotFound):
		code = codes.NotFound
	}

	s.record(ctx, action+"_error", resource, runID,
		map[string]any{"code": code.String(), "error": err.Error()})
	return status.Error(code, err.Error())
}

func (s *Server) record(ctx context.Context, action, resource string,
	runID uuid.UUID, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	detail["transport"] = "grpc"
	s.auditor.Record(ctx, audit.Entry{
		RunID: runID, Action: action, Resource: resource, Detail: detail,
	})
}

// parseTime, RFC3339 damgasını çözer.
func parseTime(layout, raw string) (time.Time, error) {
	return time.Parse(layout, raw)
}
