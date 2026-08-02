package monitor

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/domain"
	"github.com/German4341374/service-reliability-watchdog/internal/store"
)

type executorFunc func(context.Context, domain.Endpoint) domain.CheckResult

func (f executorFunc) Check(ctx context.Context, endpoint domain.Endpoint) domain.CheckResult {
	return f(ctx, endpoint)
}

type testObserver struct {
	alerts  atomic.Int64
	dropped atomic.Int64
	storage atomic.Int64
}

func (*testObserver) Observe(domain.Endpoint, domain.CheckResult) {}
func (o *testObserver) Alert(string, domain.State)                { o.alerts.Add(1) }
func (o *testObserver) QueueDropped()                             { o.dropped.Add(1) }
func (o *testObserver) StorageError()                             { o.storage.Add(1) }

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testOptions() Options {
	return Options{Workers: 2, ConcurrencyLimit: 1, QueueSize: 10, CircuitFailures: 3,
		CircuitCooldown: time.Second, AlertDedupe: time.Hour, Now: time.Now,
		Maintenance: func(string, time.Time) bool { return false }}
}

func TestStateAndHistorySurviveMonitorRestart(t *testing.T) {
	repository := store.NewMemory()
	endpoint := domain.Endpoint{ID: "api", Name: "API", Type: domain.CheckHTTP, Interval: time.Second, SLO: domain.SLOConfig{AvailabilityTarget: .99, LatencyTarget: time.Second, Window: time.Hour}}
	unavailable := executorFunc(func(context.Context, domain.Endpoint) domain.CheckResult {
		return domain.CheckResult{EndpointID: "api", CheckedAt: time.Now().UTC(), State: domain.StateUnavailable, Message: "controlled failure"}
	})
	first := New([]domain.Endpoint{endpoint}, repository, unavailable, &testObserver{}, testLogger(), testOptions())
	first.perform(context.Background(), endpoint)

	healthy := executorFunc(func(context.Context, domain.Endpoint) domain.CheckResult {
		return domain.CheckResult{EndpointID: "api", CheckedAt: time.Now().UTC().Add(time.Second), State: domain.StateHealthy, Message: "recovered"}
	})
	second := New([]domain.Endpoint{endpoint}, repository, healthy, &testObserver{}, testLogger(), testOptions())
	before, err := second.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.Endpoints[0].LastResult.State != domain.StateUnavailable {
		t.Fatalf("restart lost outage state: %+v", before)
	}
	second.perform(context.Background(), endpoint)
	after, _ := second.Snapshot(context.Background())
	if after.Endpoints[0].LastResult.State != domain.StateHealthy {
		t.Fatalf("recovery not stored: %+v", after)
	}
	if len(after.Endpoints[0].Transitions) != 2 {
		t.Fatalf("expected outage and recovery transitions: %+v", after.Endpoints[0].Transitions)
	}
}

func TestMaintenanceSkipsExecutor(t *testing.T) {
	repository := store.NewMemory()
	endpoint := domain.Endpoint{ID: "api", Name: "API", Interval: time.Second, SLO: domain.SLOConfig{AvailabilityTarget: .99}}
	calls := atomic.Int64{}
	executor := executorFunc(func(context.Context, domain.Endpoint) domain.CheckResult { calls.Add(1); return domain.CheckResult{} })
	options := testOptions()
	options.Maintenance = func(string, time.Time) bool { return true }
	service := New([]domain.Endpoint{endpoint}, repository, executor, &testObserver{}, testLogger(), options)
	service.perform(context.Background(), endpoint)
	latest, _ := repository.LatestResults(context.Background())
	if calls.Load() != 0 || latest["api"].State != domain.StateMaintenance {
		t.Fatalf("maintenance failed: calls=%d result=%+v", calls.Load(), latest)
	}
}

func TestSchedulerHonorsConcurrencyLimit(t *testing.T) {
	repository := store.NewMemory()
	var active, maximum atomic.Int64
	executor := executorFunc(func(_ context.Context, endpoint domain.Endpoint) domain.CheckResult {
		current := active.Add(1)
		for {
			old := maximum.Load()
			if current <= old || maximum.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		active.Add(-1)
		return domain.CheckResult{EndpointID: endpoint.ID, CheckedAt: time.Now(), State: domain.StateHealthy}
	})
	endpoints := make([]domain.Endpoint, 4)
	for index := range endpoints {
		endpoints[index] = domain.Endpoint{ID: string(rune('a' + index)), Interval: 10 * time.Millisecond}
	}
	options := testOptions()
	options.Workers = 4
	options.ConcurrencyLimit = 2
	service := New(endpoints, repository, executor, &testObserver{}, testLogger(), options)
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	if err := service.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if maximum.Load() > 2 || maximum.Load() < 2 {
		t.Fatalf("expected max concurrency 2, got %d", maximum.Load())
	}
}

func TestCircuitBreakerSkipsRepeatedExecution(t *testing.T) {
	repository := store.NewMemory()
	endpoint := domain.Endpoint{ID: "api"}
	var calls atomic.Int64
	executor := executorFunc(func(context.Context, domain.Endpoint) domain.CheckResult {
		calls.Add(1)
		return domain.CheckResult{EndpointID: "api", CheckedAt: time.Now(), State: domain.StateUnavailable}
	})
	options := testOptions()
	options.CircuitFailures = 1
	options.CircuitCooldown = time.Hour
	service := New([]domain.Endpoint{endpoint}, repository, executor, &testObserver{}, testLogger(), options)
	service.perform(context.Background(), endpoint)
	service.perform(context.Background(), endpoint)
	latest, _ := repository.LatestResults(context.Background())
	if calls.Load() != 1 || !latest["api"].CircuitBreakerOpen {
		t.Fatalf("circuit did not skip: calls=%d latest=%+v", calls.Load(), latest)
	}
}

func TestSnapshotAggregateAndEndpointLookup(t *testing.T) {
	repository := store.NewMemory()
	endpoints := []domain.Endpoint{{ID: "a", SLO: domain.SLOConfig{AvailabilityTarget: .99}}, {ID: "b", SLO: domain.SLOConfig{AvailabilityTarget: .99}}}
	_ = repository.UpsertEndpoints(context.Background(), endpoints)
	_, _ = repository.RecordResult(context.Background(), domain.CheckResult{EndpointID: "a", CheckedAt: time.Now(), State: domain.StateHealthy}, time.Hour)
	service := New(endpoints, repository, executorFunc(func(context.Context, domain.Endpoint) domain.CheckResult { return domain.CheckResult{} }), &testObserver{}, testLogger(), testOptions())
	snapshot, err := service.Snapshot(context.Background())
	if err != nil || snapshot.Aggregate.Healthy != 1 || snapshot.Aggregate.Unknown != 1 {
		t.Fatalf("unexpected snapshot: %+v err=%v", snapshot, err)
	}
	if _, err := service.Endpoint(context.Background(), "missing"); err == nil {
		t.Fatal("missing endpoint should fail")
	}
}
