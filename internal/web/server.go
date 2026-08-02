package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/domain"
	"github.com/German4341374/service-reliability-watchdog/internal/store"
)

//go:embed templates/*.html
var templates embed.FS

type StatusService interface {
	Snapshot(context.Context) (domain.StatusSnapshot, error)
	Endpoint(context.Context, string) (domain.EndpointStatus, error)
	Transitions(context.Context, string, int) ([]domain.Transition, error)
	Alerts(context.Context, int) ([]domain.Alert, error)
}

type Server struct {
	service StatusService
	repo    store.Repository
	metrics http.Handler
	logger  *slog.Logger
	page    *template.Template
	mux     *http.ServeMux
}

func New(service StatusService, repo store.Repository, metrics http.Handler, logger *slog.Logger) (*Server, error) {
	page, err := template.New("status.html").Funcs(template.FuncMap{
		"percent": func(value float64) string { return fmt.Sprintf("%.3f%%", value*100) },
		"duration": func(value time.Duration) string {
			if value <= 0 {
				return "-"
			}
			return value.Round(time.Millisecond).String()
		},
		"stateClass": func(state domain.State) string { return strings.ToLower(string(state)) },
	}).ParseFS(templates, "templates/status.html")
	if err != nil {
		return nil, fmt.Errorf("parse status template: %w", err)
	}
	server := &Server{service: service, repo: repo, metrics: metrics, logger: logger, page: page, mux: http.NewServeMux()}
	server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler { return s.securityHeaders(s.requestLog(s.mux)) }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /", s.statusPage)
	s.mux.HandleFunc("GET /health/live", s.live)
	s.mux.HandleFunc("GET /health/ready", s.ready)
	s.mux.HandleFunc("GET /api/v1/status", s.statusAPI)
	s.mux.HandleFunc("GET /api/v1/endpoints/{id}", s.endpointAPI)
	s.mux.HandleFunc("GET /api/v1/transitions", s.transitionsAPI)
	s.mux.HandleFunc("GET /api/v1/alerts", s.alertsAPI)
	s.mux.Handle("GET /metrics", s.metrics)
}

func (s *Server) statusPage(writer http.ResponseWriter, request *http.Request) {
	snapshot, err := s.service.Snapshot(request.Context())
	if err != nil {
		s.internalError(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.page.Execute(writer, snapshot); err != nil {
		s.logger.Error("render status page", "error", err)
	}
}

func (s *Server) live(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "alive"})
}

func (s *Server) ready(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	if err := s.repo.Ping(ctx); err != nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{
			"status": "not ready", "reason": "database unavailable",
		})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) statusAPI(writer http.ResponseWriter, request *http.Request) {
	snapshot, err := s.service.Snapshot(request.Context())
	if err != nil {
		s.internalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, snapshot)
}

func (s *Server) endpointAPI(writer http.ResponseWriter, request *http.Request) {
	endpoint, err := s.service.Endpoint(request.Context(), request.PathValue("id"))
	if errors.Is(err, context.Canceled) {
		return
	}
	if err != nil {
		if err.Error() == "endpoint not found" {
			writeJSON(writer, http.StatusNotFound, map[string]string{"error": "endpoint not found"})
		} else {
			s.internalError(writer, err)
		}
		return
	}
	writeJSON(writer, http.StatusOK, endpoint)
}

func (s *Server) transitionsAPI(writer http.ResponseWriter, request *http.Request) {
	items, err := s.service.Transitions(request.Context(), request.URL.Query().Get("endpoint"), parseLimit(request, 100))
	if err != nil {
		s.internalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"transitions": items})
}

func (s *Server) alertsAPI(writer http.ResponseWriter, request *http.Request) {
	items, err := s.service.Alerts(request.Context(), parseLimit(request, 100))
	if err != nil {
		s.internalError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"alerts": items})
}

func (s *Server) internalError(writer http.ResponseWriter, err error) {
	s.logger.Error("request failed", "error", err)
	writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(writer, request)
		s.logger.Info("http request", "method", request.Method, "path", request.URL.Path,
			"duration_ms", time.Since(started).Milliseconds())
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'")
		next.ServeHTTP(writer, request)
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func parseLimit(request *http.Request, fallback int) int {
	value, err := strconv.Atoi(request.URL.Query().Get("limit"))
	if err != nil || value < 1 {
		return fallback
	}
	return min(value, 500)
}
