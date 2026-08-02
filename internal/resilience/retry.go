package resilience

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/domain"
)

type CheckFunc func(context.Context) domain.CheckResult

func Retry(ctx context.Context, retries int, baseDelay, maxDelay time.Duration, check CheckFunc) domain.CheckResult {
	var result domain.CheckResult
	for attempt := 0; attempt <= retries; attempt++ {
		result = check(ctx)
		result.AttemptCount = attempt + 1
		if result.State != domain.StateUnavailable || attempt == retries {
			return result
		}
		delay := retryDelay(attempt, baseDelay, maxDelay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			result.Error = ctx.Err().Error()
			result.Message = "retry cancelled: " + ctx.Err().Error()
			return result
		case <-timer.C:
		}
	}
	return result
}

func retryDelay(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	if baseDelay <= 0 {
		return 0
	}
	delay := baseDelay
	for range attempt {
		if delay >= maxDelay/2 && maxDelay > 0 {
			delay = maxDelay
			break
		}
		delay *= 2
	}
	if maxDelay > 0 && delay > maxDelay {
		delay = maxDelay
	}
	jitter := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(delay) * jitter)
}
