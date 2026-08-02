package store

import (
	"context"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/domain"
)

type RecordOutcome struct {
	Transition *domain.Transition
	Alert      *domain.Alert
}

type Counts struct {
	Eligible    int64
	Good        int64
	LatencyGood int64
}

type Repository interface {
	Ping(context.Context) error
	UpsertEndpoints(context.Context, []domain.Endpoint) error
	RecordResult(context.Context, domain.CheckResult, time.Duration) (RecordOutcome, error)
	LatestResults(context.Context) (map[string]domain.CheckResult, error)
	CountsSince(context.Context, string, time.Time, time.Duration) (Counts, error)
	Transitions(context.Context, string, int) ([]domain.Transition, error)
	Alerts(context.Context, int) ([]domain.Alert, error)
	Close()
}
