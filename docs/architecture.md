# Architecture

## Runtime flow

The scheduler scans configured due times every 100 ms. A due endpoint is enqueued only when it is
not already in flight. A bounded channel applies backpressure; a full queue skips that interval,
increments `watchdog_scheduler_queue_dropped_total`, and logs the endpoint ID.

Workers consume the queue, then acquire a global semaphore. This separates worker count (queue
consumers) from maximum simultaneous network operations. Each endpoint has its own timeout,
interval, retry count, and circuit state.

The probe first resolves DNS and then runs its protocol check. Only Unavailable results are
retried. Backoff doubles from the configured base delay, is capped, and receives +/-20% jitter.
Degraded results are successful checks and are not retried.

After the configured number of consecutive failures, the endpoint circuit opens. Checks are
skipped during cooldown. Exactly one half-open probe is allowed afterward; success closes the
circuit and failure reopens it.

## Persistence consistency

Each check is recorded in a PostgreSQL transaction. `pg_advisory_xact_lock(hashtext(endpoint_id))`
serializes the following sequence for one endpoint:

1. Read previous state.
2. Insert result.
3. Insert transition when state changed.
4. Conditionally insert an alert when no equal dedupe key exists inside the dedupe window.
5. Commit all changes together.

Results are append-only. Configuration identity is synchronized into `endpoints` with an upsert.
Restarting the process therefore does not erase latest state, history, alerts, uptime, or SLO
samples.

## Failure modes

| Failure | Behavior |
|---|---|
| Endpoint slow/down | Retry, state update, transition, deduplicated alert, then circuit breaker |
| Scheduler queue full | Current interval skipped; metric and warning emitted |
| PostgreSQL unavailable after startup | Probes continue; writes fail visibly, readiness returns 503, storage error metric increases |
| PostgreSQL unavailable at startup | Bounded startup retry, then process exits for orchestrator restart |
| Worker panic | Not explicitly recovered; process exit/restart is preferred over unknown partial state |
| Status query database error | Generic HTTP 500; detailed error only in structured server log |
| Shutdown | Scheduling stops, workers observe cancellation, HTTP server drains within timeout |

Checks performed while PostgreSQL is down are not buffered. This avoids hidden unbounded memory
growth and duplicate replay but creates an explicit monitoring-data gap.

## Trust boundaries

YAML configuration is trusted operator input. Header values and expected text are excluded from
API JSON and never logged. The API is read-only, but has no authentication. PostgreSQL is attached
only to the internal Compose network. The watchdog and controlled demo target also join a separate
edge network so Docker can publish their ports, which remain bound exclusively to `127.0.0.1`.
