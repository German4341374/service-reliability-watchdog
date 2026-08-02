package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/domain"
)

type Memory struct {
	mu          sync.RWMutex
	endpoints   map[string]domain.Endpoint
	results     map[string][]domain.CheckResult
	transitions []domain.Transition
	alerts      []domain.Alert
	nextID      int64
}

func NewMemory() *Memory {
	return &Memory{
		endpoints: make(map[string]domain.Endpoint),
		results:   make(map[string][]domain.CheckResult),
		nextID:    1,
	}
}

func (m *Memory) Ping(context.Context) error { return nil }
func (m *Memory) Close()                     {}

func (m *Memory) UpsertEndpoints(_ context.Context, endpoints []domain.Endpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, endpoint := range endpoints {
		m.endpoints[endpoint.ID] = endpoint
	}
	return nil
}

func (m *Memory) RecordResult(_ context.Context, result domain.CheckResult, dedupeWindow time.Duration) (RecordOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := domain.StateUnknown
	items := m.results[result.EndpointID]
	if len(items) > 0 {
		previous = items[len(items)-1].State
	}
	result.ID = m.next()
	m.results[result.EndpointID] = append(items, result)
	outcome := RecordOutcome{}
	if previous != result.State {
		transition := domain.Transition{
			ID: m.next(), EndpointID: result.EndpointID, FromState: previous,
			ToState: result.State, ChangedAt: result.CheckedAt, Reason: result.Message,
		}
		m.transitions = append(m.transitions, transition)
		outcome.Transition = &transition
	}
	if alertable(result.State) || (previous != result.State && result.State == domain.StateHealthy) {
		key := result.EndpointID + ":" + string(result.State)
		duplicate := false
		for index := len(m.alerts) - 1; index >= 0; index-- {
			if m.alerts[index].DedupeKey == key && result.CheckedAt.Sub(m.alerts[index].CreatedAt) < dedupeWindow {
				duplicate = true
				break
			}
		}
		if !duplicate {
			alert := domain.Alert{
				ID: m.next(), EndpointID: result.EndpointID, State: result.State,
				DedupeKey: key, CreatedAt: result.CheckedAt, Message: result.Message,
			}
			m.alerts = append(m.alerts, alert)
			outcome.Alert = &alert
		}
	}
	return outcome, nil
}

func (m *Memory) LatestResults(context.Context) (map[string]domain.CheckResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	latest := make(map[string]domain.CheckResult)
	for id, results := range m.results {
		if len(results) > 0 {
			latest[id] = results[len(results)-1]
		}
	}
	return latest, nil
}

func (m *Memory) CountsSince(_ context.Context, endpointID string, since time.Time, latencyTarget time.Duration) (Counts, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var counts Counts
	for _, result := range m.results[endpointID] {
		if result.CheckedAt.Before(since) || result.State == domain.StateMaintenance {
			continue
		}
		counts.Eligible++
		if result.State == domain.StateHealthy || result.State == domain.StateDegraded {
			counts.Good++
		}
		if result.State != domain.StateUnavailable && result.Latency <= latencyTarget {
			counts.LatencyGood++
		}
	}
	return counts, nil
}

func (m *Memory) Transitions(_ context.Context, endpointID string, limit int) ([]domain.Transition, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]domain.Transition, 0, limit)
	for index := len(m.transitions) - 1; index >= 0 && len(items) < limit; index-- {
		if endpointID == "" || m.transitions[index].EndpointID == endpointID {
			items = append(items, m.transitions[index])
		}
	}
	return items, nil
}

func (m *Memory) Alerts(_ context.Context, limit int) ([]domain.Alert, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := append([]domain.Alert(nil), m.alerts...)
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (m *Memory) next() int64 {
	value := m.nextID
	m.nextID++
	return value
}
