package domain

import (
	"math"
	"testing"
	"time"
)

func TestCalculateSLOExcludesMaintenance(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	endpoint := Endpoint{LatencyTarget: time.Second, SLO: SLOConfig{
		AvailabilityTarget: 0.99, LatencyTarget: 500 * time.Millisecond, Window: 24 * time.Hour,
	}}
	results := []CheckResult{
		{CheckedAt: now.Add(-time.Hour), State: StateHealthy, Latency: 100 * time.Millisecond},
		{CheckedAt: now.Add(-2 * time.Hour), State: StateUnavailable, Latency: 100 * time.Millisecond},
		{CheckedAt: now.Add(-3 * time.Hour), State: StateMaintenance},
		{CheckedAt: now.Add(-48 * time.Hour), State: StateUnavailable},
	}
	status := CalculateSLO(endpoint, results, now)
	if status.SampleCount != 2 || status.Availability != 0.5 {
		t.Fatalf("unexpected SLO: %+v", status)
	}
	if math.Abs(status.BurnRate-50) > 0.001 {
		t.Fatalf("expected 50x burn rate, got %f", status.BurnRate)
	}
	if status.ErrorBudgetRemain != 0 {
		t.Fatalf("expected exhausted budget, got %f", status.ErrorBudgetRemain)
	}
}

func TestCalculateSLOCountsDefaults(t *testing.T) {
	status := CalculateSLOCounts(Endpoint{LatencyTarget: time.Second}, 0, 0, 0)
	if status.AvailabilityTarget != 0.999 || status.Window != 30*24*time.Hour {
		t.Fatalf("unexpected defaults: %+v", status)
	}
	if status.ErrorBudgetRemain != 1 {
		t.Fatalf("empty window should retain budget: %+v", status)
	}
}
