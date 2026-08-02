package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/domain"
	"github.com/German4341374/service-reliability-watchdog/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Postgres struct{ pool *pgxpool.Pool }

func OpenPostgres(ctx context.Context, dsn string, maxConnections int32) (*Postgres, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL URL: %w", err)
	}
	config.MaxConns = maxConnections
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	repository := &Postgres{pool: pool}
	if err := repository.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return repository, nil
}

func (p *Postgres) Migrate(ctx context.Context) error {
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		data, readErr := migrations.Files.ReadFile(name)
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", name, readErr)
		}
		if _, execErr := p.pool.Exec(ctx, string(data)); execErr != nil {
			return fmt.Errorf("apply migration %s: %w", name, execErr)
		}
	}
	return nil
}

func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }
func (p *Postgres) Close()                         { p.pool.Close() }

func (p *Postgres) UpsertEndpoints(ctx context.Context, endpoints []domain.Endpoint) error {
	batch := &pgx.Batch{}
	for _, endpoint := range endpoints {
		batch.Queue(`
			INSERT INTO endpoints (id, name, check_type, address)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name, check_type = EXCLUDED.check_type,
				address = EXCLUDED.address, updated_at = NOW()`,
			endpoint.ID, endpoint.Name, endpoint.Type, endpoint.Address,
		)
	}
	results := p.pool.SendBatch(ctx, batch)
	defer results.Close()
	for range endpoints {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("upsert endpoint: %w", err)
		}
	}
	return nil
}

func (p *Postgres) RecordResult(ctx context.Context, result domain.CheckResult, dedupeWindow time.Duration) (RecordOutcome, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return RecordOutcome{}, fmt.Errorf("begin result transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, result.EndpointID); err != nil {
		return RecordOutcome{}, fmt.Errorf("lock endpoint result stream: %w", err)
	}
	previous := domain.StateUnknown
	err = tx.QueryRow(ctx, `
		SELECT state FROM check_results
		WHERE endpoint_id = $1 ORDER BY checked_at DESC, id DESC LIMIT 1`, result.EndpointID,
	).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return RecordOutcome{}, fmt.Errorf("read previous state: %w", err)
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO check_results (
			endpoint_id, checked_at, state, latency_ms, http_status, dns_resolved,
			tls_valid, tls_expires_at, certificate_days, attempt_count,
			circuit_breaker_open, message, error_message
		) VALUES ($1,$2,$3,$4,NULLIF($5,0),$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id`,
		result.EndpointID, result.CheckedAt, result.State,
		float64(result.Latency)/float64(time.Millisecond), result.HTTPStatus,
		result.DNSResolved, result.TLSValid, result.TLSExpiresAt, result.CertificateDays,
		result.AttemptCount, result.CircuitBreakerOpen, result.Message, result.Error,
	).Scan(&result.ID)
	if err != nil {
		return RecordOutcome{}, fmt.Errorf("insert check result: %w", err)
	}

	outcome := RecordOutcome{}
	if previous != result.State {
		transition := domain.Transition{
			EndpointID: result.EndpointID, FromState: previous, ToState: result.State,
			ChangedAt: result.CheckedAt, Reason: result.Message,
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO state_transitions (endpoint_id, from_state, to_state, changed_at, reason)
			VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			transition.EndpointID, transition.FromState, transition.ToState,
			transition.ChangedAt, transition.Reason,
		).Scan(&transition.ID)
		if err != nil {
			return RecordOutcome{}, fmt.Errorf("insert transition: %w", err)
		}
		outcome.Transition = &transition
	}

	if alertable(result.State) || (previous != result.State && result.State == domain.StateHealthy) {
		key := result.EndpointID + ":" + string(result.State)
		alert := domain.Alert{
			EndpointID: result.EndpointID, State: result.State, DedupeKey: key,
			CreatedAt: result.CheckedAt, Message: result.Message,
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO alerts (endpoint_id, state, dedupe_key, created_at, message)
			SELECT $1,$2,$3,$4,$5
			WHERE NOT EXISTS (
				SELECT 1 FROM alerts WHERE dedupe_key = $3 AND created_at >= $4 - $6::interval
			) RETURNING id`,
			alert.EndpointID, alert.State, alert.DedupeKey, alert.CreatedAt,
			alert.Message, intervalString(dedupeWindow),
		).Scan(&alert.ID)
		if err == nil {
			outcome.Alert = &alert
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return RecordOutcome{}, fmt.Errorf("insert deduplicated alert: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return RecordOutcome{}, fmt.Errorf("commit result transaction: %w", err)
	}
	return outcome, nil
}

func (p *Postgres) LatestResults(ctx context.Context) (map[string]domain.CheckResult, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT DISTINCT ON (endpoint_id)
			id, endpoint_id, checked_at, state, latency_ms, COALESCE(http_status,0),
			dns_resolved, tls_valid, tls_expires_at, certificate_days, attempt_count,
			circuit_breaker_open, message, error_message
		FROM check_results ORDER BY endpoint_id, checked_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("query latest results: %w", err)
	}
	defer rows.Close()
	results := make(map[string]domain.CheckResult)
	for rows.Next() {
		result, scanErr := scanResult(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		results[result.EndpointID] = result
	}
	return results, rows.Err()
}

func (p *Postgres) CountsSince(ctx context.Context, endpointID string, since time.Time, latencyTarget time.Duration) (Counts, error) {
	var counts Counts
	err := p.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE state <> 'Maintenance'),
			COUNT(*) FILTER (WHERE state IN ('Healthy','Degraded')),
			COUNT(*) FILTER (
				WHERE state <> 'Maintenance' AND state <> 'Unavailable' AND latency_ms <= $3
			)
		FROM check_results WHERE endpoint_id = $1 AND checked_at >= $2`,
		endpointID, since, float64(latencyTarget)/float64(time.Millisecond),
	).Scan(&counts.Eligible, &counts.Good, &counts.LatencyGood)
	if err != nil {
		return Counts{}, fmt.Errorf("query SLO counts: %w", err)
	}
	return counts, nil
}

func (p *Postgres) Transitions(ctx context.Context, endpointID string, limit int) ([]domain.Transition, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, endpoint_id, from_state, to_state, changed_at, reason
		FROM state_transitions WHERE ($1 = '' OR endpoint_id = $1)
		ORDER BY changed_at DESC, id DESC LIMIT $2`, endpointID, limit)
	if err != nil {
		return nil, fmt.Errorf("query transitions: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Transition, 0)
	for rows.Next() {
		var item domain.Transition
		if err := rows.Scan(&item.ID, &item.EndpointID, &item.FromState, &item.ToState, &item.ChangedAt, &item.Reason); err != nil {
			return nil, fmt.Errorf("scan transition: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) Alerts(ctx context.Context, limit int) ([]domain.Alert, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, endpoint_id, state, dedupe_key, created_at, message
		FROM alerts ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query alerts: %w", err)
	}
	defer rows.Close()
	items := make([]domain.Alert, 0)
	for rows.Next() {
		var item domain.Alert
		if err := rows.Scan(&item.ID, &item.EndpointID, &item.State, &item.DedupeKey, &item.CreatedAt, &item.Message); err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanResult(row rowScanner) (domain.CheckResult, error) {
	var result domain.CheckResult
	var latencyMS float64
	if err := row.Scan(
		&result.ID, &result.EndpointID, &result.CheckedAt, &result.State, &latencyMS,
		&result.HTTPStatus, &result.DNSResolved, &result.TLSValid, &result.TLSExpiresAt,
		&result.CertificateDays, &result.AttemptCount, &result.CircuitBreakerOpen,
		&result.Message, &result.Error,
	); err != nil {
		return domain.CheckResult{}, fmt.Errorf("scan check result: %w", err)
	}
	result.Latency = time.Duration(latencyMS * float64(time.Millisecond))
	return result, nil
}

func alertable(state domain.State) bool {
	return state == domain.StateDegraded || state == domain.StateUnavailable
}

func intervalString(value time.Duration) string {
	return fmt.Sprintf("%f seconds", value.Seconds())
}
