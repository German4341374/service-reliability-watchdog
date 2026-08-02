package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/config"
	"github.com/German4341374/service-reliability-watchdog/internal/domain"
	"github.com/German4341374/service-reliability-watchdog/internal/resilience"
	"github.com/German4341374/service-reliability-watchdog/internal/store"
)

type Executor interface {
	Check(context.Context, domain.Endpoint) domain.CheckResult
}

type Observer interface {
	Observe(domain.Endpoint, domain.CheckResult)
	Alert(string, domain.State)
	QueueDropped()
	StorageError()
}

type Options struct {
	Workers          int
	ConcurrencyLimit int
	QueueSize        int
	RetryBaseDelay   time.Duration
	RetryMaxDelay    time.Duration
	CircuitFailures  int
	CircuitCooldown  time.Duration
	AlertDedupe      time.Duration
	Maintenance      func(string, time.Time) bool
	Now              func() time.Time
}

type Service struct {
	endpoints []domain.Endpoint
	byID      map[string]domain.Endpoint
	repo      store.Repository
	executor  Executor
	observer  Observer
	logger    *slog.Logger
	options   Options
	breaker   *resilience.CircuitBreaker
}

func New(endpoints []domain.Endpoint, repo store.Repository, executor Executor, observer Observer, logger *slog.Logger, options Options) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Maintenance == nil {
		options.Maintenance = func(string, time.Time) bool { return false }
	}
	byID := make(map[string]domain.Endpoint, len(endpoints))
	for _, endpoint := range endpoints {
		byID[endpoint.ID] = endpoint
	}
	return &Service{
		endpoints: endpoints, byID: byID, repo: repo, executor: executor,
		observer: observer, logger: logger, options: options,
		breaker: resilience.NewCircuitBreaker(options.CircuitFailures, options.CircuitCooldown),
	}
}

func (s *Service) Run(ctx context.Context) error {
	if err := s.repo.UpsertEndpoints(ctx, s.endpoints); err != nil {
		return fmt.Errorf("synchronize endpoints: %w", err)
	}
	jobs := make(chan domain.Endpoint, s.options.QueueSize)
	done := make(chan string, len(s.endpoints)+s.options.Workers)
	semaphore := make(chan struct{}, s.options.ConcurrencyLimit)
	var workers sync.WaitGroup
	for workerID := range s.options.Workers {
		workers.Add(1)
		go s.worker(ctx, workerID, jobs, done, semaphore, &workers)
	}

	next := make(map[string]time.Time, len(s.endpoints))
	inFlight := make(map[string]bool, len(s.endpoints))
	now := s.options.Now()
	for _, endpoint := range s.endpoints {
		next[endpoint.ID] = now
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	defer func() { close(jobs); workers.Wait() }()

	for {
		select {
		case <-ctx.Done():
			return nil
		case endpointID := <-done:
			inFlight[endpointID] = false
			next[endpointID] = s.options.Now().Add(s.byID[endpointID].Interval)
		case current := <-ticker.C:
			for _, endpoint := range s.endpoints {
				if inFlight[endpoint.ID] || current.Before(next[endpoint.ID]) {
					continue
				}
				select {
				case jobs <- endpoint:
					inFlight[endpoint.ID] = true
				case <-ctx.Done():
					return nil
				default:
					s.observer.QueueDropped()
					next[endpoint.ID] = current.Add(endpoint.Interval)
					s.logger.Warn("scheduler queue full", "endpoint", endpoint.ID)
				}
			}
		}
	}
}

func (s *Service) worker(ctx context.Context, workerID int, jobs <-chan domain.Endpoint, done chan<- string, semaphore chan struct{}, group *sync.WaitGroup) {
	defer group.Done()
	for endpoint := range jobs {
		select {
		case semaphore <- struct{}{}:
			s.perform(ctx, endpoint)
			<-semaphore
		case <-ctx.Done():
		}
		select {
		case done <- endpoint.ID:
		case <-ctx.Done():
			return
		}
		s.logger.Debug("worker completed check", "worker", workerID, "endpoint", endpoint.ID)
	}
}

func (s *Service) perform(ctx context.Context, endpoint domain.Endpoint) {
	now := s.options.Now()
	var result domain.CheckResult
	if s.options.Maintenance(endpoint.ID, now) {
		result = domain.CheckResult{
			EndpointID: endpoint.ID, CheckedAt: now.UTC(), State: domain.StateMaintenance,
			Message: "endpoint is in a configured maintenance window",
		}
	} else if !s.breaker.Allow(endpoint.ID, now) {
		result = domain.CheckResult{
			EndpointID: endpoint.ID, CheckedAt: now.UTC(), State: domain.StateUnavailable,
			CircuitBreakerOpen: true, Message: "circuit breaker is open",
			Error: "check skipped while circuit breaker is open",
		}
	} else {
		result = resilience.Retry(ctx, endpoint.Retries, s.options.RetryBaseDelay, s.options.RetryMaxDelay,
			func(checkCtx context.Context) domain.CheckResult { return s.executor.Check(checkCtx, endpoint) },
		)
		if result.State == domain.StateUnavailable {
			result.CircuitBreakerOpen = s.breaker.Failure(endpoint.ID, s.options.Now())
		} else {
			s.breaker.Success(endpoint.ID)
		}
	}
	s.observer.Observe(endpoint, result)
	outcome, err := s.repo.RecordResult(ctx, result, s.options.AlertDedupe)
	if err != nil {
		s.observer.StorageError()
		s.logger.Error("persist check result", "endpoint", endpoint.ID, "error", err)
		return
	}
	if outcome.Transition != nil {
		s.logger.Info("endpoint state transition", "endpoint", endpoint.ID,
			"from", outcome.Transition.FromState, "to", outcome.Transition.ToState)
	}
	if outcome.Alert != nil {
		s.observer.Alert(endpoint.ID, outcome.Alert.State)
		s.logger.Warn("deduplicated alert recorded", "endpoint", endpoint.ID,
			"state", outcome.Alert.State, "alert_id", outcome.Alert.ID)
	}
}

func (s *Service) Snapshot(ctx context.Context) (domain.StatusSnapshot, error) {
	latest, err := s.repo.LatestResults(ctx)
	if err != nil {
		return domain.StatusSnapshot{}, err
	}
	now := s.options.Now().UTC()
	snapshot := domain.StatusSnapshot{GeneratedAt: now, Endpoints: make([]domain.EndpointStatus, 0, len(s.endpoints))}
	for _, endpoint := range s.endpoints {
		latencyTarget := endpoint.SLO.LatencyTarget
		if latencyTarget <= 0 {
			latencyTarget = endpoint.LatencyTarget
		}
		window := endpoint.SLO.Window
		if window <= 0 {
			window = 30 * 24 * time.Hour
		}
		counts, countErr := s.repo.CountsSince(ctx, endpoint.ID, now.Add(-window), latencyTarget)
		if countErr != nil {
			return domain.StatusSnapshot{}, countErr
		}
		transitions, transitionErr := s.repo.Transitions(ctx, endpoint.ID, 10)
		if transitionErr != nil {
			return domain.StatusSnapshot{}, transitionErr
		}
		status := domain.EndpointStatus{
			Endpoint: endpoint, Uptime: ratio(counts.Good, counts.Eligible),
			SLO:         domain.CalculateSLOCounts(endpoint, counts.Eligible, counts.Good, counts.LatencyGood),
			Transitions: transitions,
		}
		if result, exists := latest[endpoint.ID]; exists {
			status.LastResult = &result
		}
		snapshot.Endpoints = append(snapshot.Endpoints, status)
		addState(&snapshot.Aggregate, status.LastResult)
	}
	snapshot.Aggregate.Total = len(snapshot.Endpoints)
	return snapshot, nil
}

func (s *Service) Endpoint(ctx context.Context, endpointID string) (domain.EndpointStatus, error) {
	if _, exists := s.byID[endpointID]; !exists {
		return domain.EndpointStatus{}, errors.New("endpoint not found")
	}
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return domain.EndpointStatus{}, err
	}
	for _, endpoint := range snapshot.Endpoints {
		if endpoint.Endpoint.ID == endpointID {
			return endpoint, nil
		}
	}
	return domain.EndpointStatus{}, errors.New("endpoint not found")
}

func (s *Service) Transitions(ctx context.Context, endpointID string, limit int) ([]domain.Transition, error) {
	return s.repo.Transitions(ctx, endpointID, limit)
}
func (s *Service) Alerts(ctx context.Context, limit int) ([]domain.Alert, error) {
	return s.repo.Alerts(ctx, limit)
}

func addState(aggregate *domain.Aggregate, result *domain.CheckResult) {
	if result == nil {
		aggregate.Unknown++
		return
	}
	switch result.State {
	case domain.StateHealthy:
		aggregate.Healthy++
	case domain.StateDegraded:
		aggregate.Degraded++
	case domain.StateUnavailable:
		aggregate.Unavailable++
	case domain.StateMaintenance:
		aggregate.Maintenance++
	default:
		aggregate.Unknown++
	}
}

func ratio(numerator, denominator int64) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func OptionsFromConfig(cfg config.Config) Options {
	return Options{
		Workers: cfg.Scheduler.Workers, ConcurrencyLimit: cfg.Scheduler.ConcurrencyLimit,
		QueueSize: cfg.Scheduler.QueueSize, RetryBaseDelay: cfg.Scheduler.RetryBaseDelay.Duration,
		RetryMaxDelay:   cfg.Scheduler.RetryMaxDelay.Duration,
		CircuitFailures: cfg.Scheduler.CircuitFailures,
		CircuitCooldown: cfg.Scheduler.CircuitCooldown.Duration,
		AlertDedupe:     cfg.Alerting.DedupeWindow.Duration,
		Maintenance:     cfg.InMaintenance, Now: time.Now,
	}
}
