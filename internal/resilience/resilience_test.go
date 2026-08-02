package resilience

import (
	"context"
	"testing"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/domain"
)

func TestRetryRecoversAndCountsAttempts(t *testing.T) {
	calls := 0
	result := Retry(context.Background(), 2, 0, 0, func(context.Context) domain.CheckResult {
		calls++
		if calls < 3 {
			return domain.CheckResult{State: domain.StateUnavailable}
		}
		return domain.CheckResult{State: domain.StateHealthy}
	})
	if result.State != domain.StateHealthy || result.AttemptCount != 3 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestRetryStopsForDegraded(t *testing.T) {
	result := Retry(context.Background(), 3, 0, 0, func(context.Context) domain.CheckResult { return domain.CheckResult{State: domain.StateDegraded} })
	if result.AttemptCount != 1 {
		t.Fatalf("degraded result was retried: %+v", result)
	}
}

func TestCircuitBreakerOpensHalfOpensAndRecovers(t *testing.T) {
	now := time.Now()
	breaker := NewCircuitBreaker(2, time.Second)
	if breaker.Failure("api", now) {
		t.Fatal("opened too early")
	}
	if !breaker.Failure("api", now) || breaker.Allow("api", now) {
		t.Fatal("circuit should be open")
	}
	if !breaker.Allow("api", now.Add(2*time.Second)) {
		t.Fatal("half-open probe should be allowed")
	}
	if breaker.Allow("api", now.Add(2*time.Second)) {
		t.Fatal("only one half-open probe is allowed")
	}
	breaker.Success("api")
	if !breaker.Allow("api", now) {
		t.Fatal("success should close circuit")
	}
}

func TestRetryDelayIsJitteredWithinBounds(t *testing.T) {
	for range 20 {
		delay := retryDelay(2, 100*time.Millisecond, time.Second)
		if delay < 320*time.Millisecond || delay > 480*time.Millisecond {
			t.Fatalf("delay outside jitter range: %s", delay)
		}
	}
}
