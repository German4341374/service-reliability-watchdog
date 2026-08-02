# Configuration reference

The decoder rejects unknown fields. Environment placeholders are expanded before decoding, so a
configuration can contain `url: ${DATABASE_URL}` without committing a password.

## Server and database

| Field | Default | Meaning |
|---|---:|---|
| `server.address` | `:8080` | HTTP listen address |
| `server.shutdownTimeout` | `10s` | HTTP graceful shutdown deadline |
| `database.url` | required | PostgreSQL URL |
| `database.connectTimeout` | `10s` | Total startup connection deadline |
| `database.maxConnections` | `10` | Pool maximum |

## Scheduler and alerts

| Field | Default | Meaning |
|---|---:|---|
| `workers` | `4` | Worker goroutines |
| `concurrencyLimit` | workers | Simultaneous probes |
| `queueSize` | `100` | Bounded pending checks |
| `retryBaseDelay` | `200ms` | First retry delay before jitter |
| `retryMaxDelay` | `5s` | Backoff cap |
| `circuitFailures` | `3` | Consecutive failures before open |
| `circuitCooldown` | `30s` | Open interval before one probe |
| `alerting.dedupeWindow` | `15m` | Minimum interval for same endpoint/state alert |

## Endpoint fields

`id`, `name`, `type`, `address`, `timeout`, `interval`, and an availability SLO are validated.
HTTP accepts only absolute `http`/`https` URLs. TCP and TLS require `host:port`. Retries are
limited to 0–10. Supported HTTP methods are passed to `net/http`; use safe idempotent methods for
synthetic monitoring.

An empty maintenance `endpoints` list applies to all endpoints. Times are RFC 3339 and end is
exclusive. Maintenance is fixed, not recurring.

