package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/domain"
	"github.com/German4341374/service-reliability-watchdog/internal/store"
)

type stubService struct{ snapshot domain.StatusSnapshot }

func (s stubService) Snapshot(context.Context) (domain.StatusSnapshot, error) { return s.snapshot, nil }
func (s stubService) Endpoint(_ context.Context, id string) (domain.EndpointStatus, error) {
	for _, item := range s.snapshot.Endpoints {
		if item.Endpoint.ID == id {
			return item, nil
		}
	}
	return domain.EndpointStatus{}, context.Canceled
}
func (stubService) Transitions(context.Context, string, int) ([]domain.Transition, error) {
	return []domain.Transition{{ID: 1}}, nil
}
func (stubService) Alerts(context.Context, int) ([]domain.Alert, error) {
	return []domain.Alert{{ID: 1}}, nil
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	state := domain.StateHealthy
	service := stubService{snapshot: domain.StatusSnapshot{GeneratedAt: time.Now(), Aggregate: domain.Aggregate{Total: 1, Healthy: 1}, Endpoints: []domain.EndpointStatus{{Endpoint: domain.Endpoint{ID: "api", Name: "API", Type: domain.CheckHTTP}, LastResult: &domain.CheckResult{State: state, CheckedAt: time.Now()}, Uptime: 1, SLO: domain.SLOStatus{AvailabilityTarget: .999, ErrorBudgetRemain: 1}}}}}
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("metric 1\n")) })
	server, err := New(service, store.NewMemory(), metrics, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler()
}

func TestStatusPageAndSecurityHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	newTestHandler(t).ServeHTTP(recorder, request)
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "Service Reliability Watchdog") {
		t.Fatalf("unexpected page: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("security header missing")
	}
}

func TestJSONAPIAndHealth(t *testing.T) {
	handler := newTestHandler(t)
	for _, path := range []string{"/health/live", "/health/ready", "/api/v1/status", "/api/v1/endpoints/api", "/api/v1/transitions", "/api/v1/alerts", "/metrics"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != 200 {
			t.Errorf("%s returned %d: %s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestLimitIsBounded(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/?limit=9999", nil)
	if parseLimit(request, 100) != 500 {
		t.Fatal("limit was not capped")
	}
	request = httptest.NewRequest(http.MethodGet, "/?limit=bad", nil)
	if parseLimit(request, 100) != 100 {
		t.Fatal("fallback not applied")
	}
}
