// Package health provides HTTP health and readiness endpoints for HTS-KGA services.
//
// T-E01-11: Her servis /health ve /ready uçlarını bu paketten kullanır.
// /health : servisin ayakta olup olmadığı (liveness)
// /ready  : bağımlılıkların hazır olup olmadığı (readiness)
package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// CheckFunc, bir bağımlılık kontrolü işlevidir.
// nil döndürürse bağımlılık hazır; hata döndürürse hazır değil.
type CheckFunc func(ctx context.Context) error

// Handler, sağlık ve hazırlık uçlarını yönetir.
type Handler struct {
	mu      sync.RWMutex
	checks  map[string]CheckFunc
	service string
	version string
}

// New, yeni bir sağlık handler'ı oluşturur.
func New(service, version string) *Handler {
	return &Handler{
		checks:  make(map[string]CheckFunc),
		service: service,
		version: version,
	}
}

// Register, adlandırılmış bir hazırlık kontrolü ekler.
func (h *Handler) Register(name string, fn CheckFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[name] = fn
}

// response, sağlık uçlarının JSON yanıt yapısıdır.
type response struct {
	Service string            `json:"service"`
	Version string            `json:"version"`
	Status  string            `json:"status"`
	Checks  map[string]string `json:"checks,omitempty"`
	Time    time.Time         `json:"time"`
}

// LiveHandler, /health ucu — her zaman 200 döndürür (container canlı).
func (h *Handler) LiveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(response{
			Service: h.service,
			Version: h.version,
			Status:  "alive",
			Time:    time.Now().UTC(),
		})
	}
}

// ReadyHandler, /ready ucu — tüm kayıtlı kontroller geçerse 200, aksi 503.
func (h *Handler) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.mu.RLock()
		checks := make(map[string]CheckFunc, len(h.checks))
		for k, v := range h.checks {
			checks[k] = v
		}
		h.mu.RUnlock()

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		results := make(map[string]string, len(checks))
		allOK := true
		for name, fn := range checks {
			if err := fn(ctx); err != nil {
				results[name] = "fail: " + err.Error()
				allOK = false
				slog.Warn("readiness check failed", "check", name, "error", err)
			} else {
				results[name] = "ok"
			}
		}

		resp := response{
			Service: h.service,
			Version: h.version,
			Checks:  results,
			Time:    time.Now().UTC(),
		}

		w.Header().Set("Content-Type", "application/json")
		if allOK {
			resp.Status = "ready"
			w.WriteHeader(http.StatusOK)
		} else {
			resp.Status = "not_ready"
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// MustServe, sağlık HTTP sunucusunu başlatır ve hata durumunda panikler.
// addr örneği: ":8081"
func (h *Handler) MustServe(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.LiveHandler())
	mux.HandleFunc("/ready", h.ReadyHandler())

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	slog.Info("health server başlatılıyor", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		if errors.Is(err, syscall.EADDRINUSE) {
			// DX: çıplak panic + stack trace yerine tek satırlık, eyleme
			// geçirilebilir bir mesaj — bu servisin (h.service) başka bir
			// kopyası zaten çalışıyor olabilir (bkz. docs/results/demo-script.md).
			fmt.Fprintf(os.Stderr,
				"\n❌ HATA: %s portu zaten kullanımda — %s'in başka bir kopyası hâlâ çalışıyor olabilir.\n"+
					"   Zorla boşaltmak için (gateway ise):  make gateway-stop\n"+
					"   Genel amaçlı:  fuser -k -KILL %s/tcp   (adresteki ':' işaretini atlayın)\n\n",
				addr, h.service, strings.TrimPrefix(addr, ":"))
			os.Exit(1)
		}
		panic("health server hatası: " + err.Error())
	}
}
