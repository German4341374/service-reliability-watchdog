# Service Reliability Watchdog

[![CI](https://github.com/German4341374/service-reliability-watchdog/actions/workflows/ci.yml/badge.svg)](https://github.com/German4341374/service-reliability-watchdog/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

`service-reliability-watchdog` is a compact synthetic monitoring service for HTTP, TCP, DNS, and
TLS endpoints. It schedules checks through a bounded worker pool, stores results and state changes
in PostgreSQL, exposes a public status page and JSON API, and publishes Prometheus metrics. SLO
views turn check history into availability, latency compliance, error budget, and burn rate.

## Features

- HTTP status, response time, expected text, redirect limit, and 1 MiB inspection cap
- TCP connection and DNS resolution checks
- TLS chain/hostname validation, expiration timestamp, and warning threshold
- Independent intervals and timeouts for each endpoint
- Worker pool, global concurrency semaphore, bounded queue, and graceful shutdown
- Exponential retry with ±20% jitter
- Per-endpoint circuit breaker with a single half-open recovery probe
- PostgreSQL results, rolling uptime, transitions, and deduplicated outage/recovery alerts
- Scoped maintenance windows excluded from SLO calculations
- States: Healthy, Degraded, Unavailable, Maintenance, and Unknown
- Availability and latency SLOs, remaining error budget, and burn rate
- Responsive status page, health endpoints, JSON API, and Prometheus metrics
- Fully local controlled outage → restart → persisted outage → recovery demonstration

## Architecture

```mermaid
flowchart LR
    Y["Strict YAML configuration"] --> S["100 ms scheduler"]
    S --> Q["Bounded work queue"]
    Q --> W["Worker pool"]
    W --> C["Concurrency semaphore"]
    C --> R["Retry with jitter"]
    R --> B["Per-endpoint circuit breaker"]
    B --> P["HTTP / TCP / DNS / TLS probes"]
    P --> DB[("PostgreSQL")]
    DB --> T["Transitions and deduplicated alerts"]
    DB --> O["Uptime / SLO / error budget / burn rate"]
    T --> API["Status page and JSON API"]
    O --> API
    P --> M["Prometheus metrics"]
```

PostgreSQL advisory transaction locks serialize state changes per endpoint. This prevents two
workers from creating contradictory transitions or duplicate alerts. See
[docs/architecture.md](docs/architecture.md) for component and failure-mode details.

## Prerequisites

- Docker Engine with Docker Compose v2
- `curl`, `jq`, and Bash for the controlled demo
- Go 1.26 for native development
- Windows users: WSL2 with Docker Desktop integration, or Linux

No cloud account or paid resource is required.

## Run with Docker Compose

```bash
cp .env.example .env
docker compose up --detach --build
docker compose ps
```

Open:

- Status page: <http://127.0.0.1:8080/>
- JSON status: <http://127.0.0.1:8080/api/v1/status>
- Readiness: <http://127.0.0.1:8080/health/ready>
- Metrics: <http://127.0.0.1:8080/metrics>

Stop while preserving PostgreSQL data:

```bash
docker compose down
```

Delete the local database and generated demo certificates:

```bash
docker compose down --volumes
```

`.env.example` contains development-only values. Choose a unique secret before using a shared
machine. `.env` is ignored by Git.

## Controlled failure and restart recovery

```bash
make demo
```

The script performs and verifies this exact sequence:

1. Build the images and wait for PostgreSQL, demo target, and watchdog readiness.
2. Wait for the HTTP target to become Healthy.
3. Switch the target to controlled HTTP 503 responses.
4. Wait for Unavailable and verify only one alert is stored during repeated failures.
5. Restart the watchdog container.
6. Verify the persisted Unavailable state remains visible after restart.
7. Restore the target, wait for Healthy, and verify the recovery transition.
8. Verify Prometheus check metrics.

Set `KEEP_DEMO=1` to leave containers running after success. The detailed procedure and manual
commands are in [controlled-failure.md](docs/runbooks/controlled-failure.md).

## Native development

Start PostgreSQL separately and export a URL without putting it into shell history where possible:

```bash
export DATABASE_URL='postgres://watchdog:local_password@127.0.0.1:5432/watchdog?sslmode=disable'
cp config.example.yaml config.yaml
go run ./cmd/watchdog -config config.yaml
```

The example endpoint domains end in `.example.test` and are intentionally non-routable. Replace
them only in the ignored `config.yaml`.

## Configuration

Durations use Go notation such as `250ms`, `5s`, `30m`, or `720h`. `${VARIABLE}` placeholders are
expanded before YAML decoding. Unknown YAML fields are rejected.

```yaml
endpoints:
  - id: orders-api
    name: Orders API
    type: http
    address: https://orders.example.test/health
    method: GET
    expectedStatus: 200
    expectedText: healthy
    timeout: 5s
    interval: 30s
    retries: 2
    latencyTarget: 750ms
    tlsWarnBefore: 720h
    slo:
      availabilityTarget: 0.999
      latencyTarget: 750ms
      window: 720h
```

HTTP supports environment-backed headers, but header values are omitted from API responses and
logs. See [docs/configuration.md](docs/configuration.md).

## State calculation

| State | Meaning |
|---|---|
| Healthy | Protocol and functional expectations passed within the latency target |
| Degraded | Check passed but exceeded latency target, or TLS certificate is near expiration |
| Unavailable | DNS, connection, status, content, certificate, validation, or timeout failure |
| Maintenance | Active configured maintenance window; no probe is sent |
| Unknown | No stored result exists yet |

Maintenance results are excluded from uptime and SLO budgets. Circuit-open skipped checks remain
Unavailable because the last confirmed condition is still an outage.

## JSON API

| Method and path | Purpose |
|---|---|
| `GET /api/v1/status` | Aggregate and per-endpoint status, uptime, SLO, and transitions |
| `GET /api/v1/endpoints/{id}` | One endpoint |
| `GET /api/v1/transitions?endpoint=id&limit=100` | Transition history |
| `GET /api/v1/alerts?limit=100` | Deduplicated alert records |
| `GET /health/live` | Process liveness |
| `GET /health/ready` | PostgreSQL readiness |
| `GET /metrics` | Prometheus exposition |

```bash
curl --fail http://127.0.0.1:8080/api/v1/endpoints/demo-http | jq
curl --fail 'http://127.0.0.1:8080/api/v1/transitions?endpoint=demo-http' | jq
curl --fail http://127.0.0.1:8080/metrics | grep '^watchdog_'
```

Durations in JSON are encoded as nanoseconds by Go's `time.Duration` integer representation.

## SLO model

Availability is `(Healthy + Degraded) / eligible checks`. Latency compliance is successful checks
at or below the latency target divided by eligible checks. Error-budget burn rate is actual bad
fraction divided by allowed bad fraction. `1x` consumes budget exactly at the sustainable rate;
values above `1x` consume it too quickly. See [docs/slo.md](docs/slo.md) for formulas and caveats.

## Testing

```bash
make lint
make test
make build
```

Tests use `httptest`, local TCP/TLS listeners, fake DNS, an in-memory repository, and synthetic
states. The Compose CI job separately verifies real PostgreSQL migrations, Docker health checks,
alert deduplication, persistence across restart, recovery, and Prometheus metrics.

## Prometheus metrics

- `watchdog_checks_total{endpoint,state}`
- `watchdog_check_duration_seconds{endpoint,type}`
- `watchdog_endpoint_state{endpoint,state}`
- `watchdog_alerts_total{endpoint,state}`
- `watchdog_circuit_breaker_open{endpoint}`
- `watchdog_scheduler_queue_dropped_total`
- `watchdog_storage_errors_total`
- `watchdog_database_ready`

Endpoint labels come only from bounded configuration, not user-controlled request values.

## Operations and troubleshooting

- [Mass endpoint outage](docs/runbooks/mass-outage.md)
- [DNS failure](docs/runbooks/dns-failure.md)
- [Certificate expiration](docs/runbooks/certificate-expiration.md)
- [PostgreSQL unavailable](docs/runbooks/postgresql-unavailable.md)
- [False positives](docs/runbooks/false-positives.md)
- [Controlled failure demo](docs/runbooks/controlled-failure.md)

## Security and limitations

- The UI/API has no authentication. Bind to localhost by default and put an authenticated proxy in
  front of any production deployment.
- Configuration operators can make the service connect to arbitrary destinations. Protect write
  access to YAML and constrain egress with firewall/network policy.
- This is a single-process scheduler, not a globally coordinated monitoring cluster.
- Maintenance windows are fixed UTC intervals, not recurring calendar rules.
- Alert records are stored and exposed, but no email, paging, or webhook integration is included.
- TLS validation uses the runtime trust store. The Compose demo creates an ephemeral local CA.
- Check-frequency sampling approximates availability; it is not continuous observation.

## Project structure

```text
cmd/                 Watchdog, demo target, certificate init, healthcheck binaries
internal/checker/    HTTP, TCP, DNS, TLS probes
internal/monitor/    Scheduler, worker pool, retries, circuit breaker integration
internal/store/      PostgreSQL and in-memory repositories
internal/web/        Status page, API, health, and middleware
migrations/          Embedded PostgreSQL schema
docs/runbooks/       Incident response and recovery procedures
```

## License

[MIT](LICENSE)

