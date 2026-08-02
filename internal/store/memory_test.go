package store

import (
	"context"
	"testing"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/domain"
)

func TestMemoryRecordsTransitionsAndDeduplicatesAlerts(t *testing.T) {
	repository := NewMemory()
	now := time.Now().UTC()
	results := []domain.CheckResult{
		{EndpointID: "api", CheckedAt: now, State: domain.StateUnavailable, Message: "down"},
		{EndpointID: "api", CheckedAt: now.Add(time.Minute), State: domain.StateUnavailable, Message: "still down"},
		{EndpointID: "api", CheckedAt: now.Add(2 * time.Minute), State: domain.StateHealthy, Message: "recovered"},
	}
	for _, result := range results {
		if _, err := repository.RecordResult(context.Background(), result, 15*time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	transitions, _ := repository.Transitions(context.Background(), "api", 10)
	alerts, _ := repository.Alerts(context.Background(), 10)
	if len(transitions) != 2 {
		t.Fatalf("expected two transitions, got %+v", transitions)
	}
	if len(alerts) != 2 {
		t.Fatalf("expected outage and recovery alerts, got %+v", alerts)
	}
	if transitions[0].FromState != domain.StateUnavailable || transitions[0].ToState != domain.StateHealthy {
		t.Fatalf("unexpected latest transition: %+v", transitions[0])
	}
}

func TestMemoryCountsExcludeMaintenance(t *testing.T) {
	repository := NewMemory()
	now := time.Now().UTC()
	for _, state := range []domain.State{domain.StateHealthy, domain.StateDegraded, domain.StateUnavailable, domain.StateMaintenance} {
		_, _ = repository.RecordResult(context.Background(), domain.CheckResult{EndpointID: "api", CheckedAt: now, State: state, Latency: 100 * time.Millisecond}, time.Hour)
	}
	counts, _ := repository.CountsSince(context.Background(), "api", now.Add(-time.Hour), 200*time.Millisecond)
	if counts.Eligible != 3 || counts.Good != 2 || counts.LatencyGood != 2 {
		t.Fatalf("unexpected counts: %+v", counts)
	}
}

func TestMemoryPersistsAcrossServiceInstances(t *testing.T) {
	repository := NewMemory()
	result := domain.CheckResult{EndpointID: "api", CheckedAt: time.Now(), State: domain.StateUnavailable}
	_, _ = repository.RecordResult(context.Background(), result, time.Hour)
	latest, _ := repository.LatestResults(context.Background())
	if latest["api"].State != domain.StateUnavailable {
		t.Fatal("persisted state was lost")
	}
}
