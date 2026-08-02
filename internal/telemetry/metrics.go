package telemetry

import (
	"net/http"

	"github.com/German4341374/service-reliability-watchdog/internal/domain"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry      *prometheus.Registry
	checks        *prometheus.CounterVec
	latency       *prometheus.HistogramVec
	state         *prometheus.GaugeVec
	alerts        *prometheus.CounterVec
	circuitOpen   *prometheus.GaugeVec
	queueDropped  prometheus.Counter
	storageErrors prometheus.Counter
	databaseReady prometheus.Gauge
}

func New() *Metrics {
	metrics := &Metrics{
		registry: prometheus.NewRegistry(),
		checks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "watchdog_checks_total", Help: "Synthetic checks by endpoint and state.",
		}, []string{"endpoint", "state"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "watchdog_check_duration_seconds", Help: "End-to-end check duration.",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 12),
		}, []string{"endpoint", "type"}),
		state: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "watchdog_endpoint_state", Help: "One-hot endpoint state gauge.",
		}, []string{"endpoint", "state"}),
		alerts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "watchdog_alerts_total", Help: "Deduplicated alert records.",
		}, []string{"endpoint", "state"}),
		circuitOpen: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "watchdog_circuit_breaker_open", Help: "Whether an endpoint circuit is open.",
		}, []string{"endpoint"}),
		queueDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "watchdog_scheduler_queue_dropped_total", Help: "Checks skipped because the work queue was full.",
		}),
		storageErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "watchdog_storage_errors_total", Help: "PostgreSQL persistence errors.",
		}),
		databaseReady: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "watchdog_database_ready", Help: "PostgreSQL readiness (1 ready, 0 unavailable).",
		}),
	}
	metrics.registry.MustRegister(
		metrics.checks, metrics.latency, metrics.state, metrics.alerts, metrics.circuitOpen,
		metrics.queueDropped, metrics.storageErrors, metrics.databaseReady,
	)
	return metrics
}

func (m *Metrics) Observe(endpoint domain.Endpoint, result domain.CheckResult) {
	m.checks.WithLabelValues(endpoint.ID, string(result.State)).Inc()
	m.latency.WithLabelValues(endpoint.ID, string(endpoint.Type)).Observe(result.Latency.Seconds())
	for _, state := range []domain.State{
		domain.StateHealthy, domain.StateDegraded, domain.StateUnavailable,
		domain.StateMaintenance, domain.StateUnknown,
	} {
		value := 0.0
		if state == result.State {
			value = 1
		}
		m.state.WithLabelValues(endpoint.ID, string(state)).Set(value)
	}
	value := 0.0
	if result.CircuitBreakerOpen {
		value = 1
	}
	m.circuitOpen.WithLabelValues(endpoint.ID).Set(value)
}

func (m *Metrics) Alert(endpointID string, state domain.State) {
	m.alerts.WithLabelValues(endpointID, string(state)).Inc()
}

func (m *Metrics) QueueDropped() { m.queueDropped.Inc() }
func (m *Metrics) StorageError() { m.storageErrors.Inc() }
func (m *Metrics) DatabaseReady(ready bool) {
	if ready {
		m.databaseReady.Set(1)
	} else {
		m.databaseReady.Set(0)
	}
}
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
