# Five-minute employer demonstration

## 0:00–1:00 — architecture

Open the README Mermaid diagram. Explain the strict YAML input, bounded queue, worker pool,
concurrency semaphore, retry/jitter, circuit breaker, transactional PostgreSQL history, API, and
Prometheus output.

## 1:00–2:00 — start locally

```bash
cp .env.example .env
docker compose up -d --build
docker compose ps
```

Open <http://127.0.0.1:8080> and show HTTP, TCP, and TLS cards plus SLO/error-budget fields.

## 2:00–4:00 — prove failure and recovery

```bash
KEEP_DEMO=1 bash scripts/controlled-failure-demo.sh
```

Narrate that the script causes real HTTP 503 responses, waits for Unavailable, verifies alert
deduplication, restarts only the monitoring process, proves PostgreSQL retained state, restores the
target, and verifies the recovery transition.

## 4:00–5:00 — operations evidence

```bash
curl -fsS http://127.0.0.1:8080/api/v1/status | jq
curl -fsS http://127.0.0.1:8080/metrics | grep '^watchdog_'
docker compose logs --tail=20 watchdog
docker compose down --volumes
```

Close with the runbooks, race-enabled tests, non-root/read-only containers, internal network, and
CI Compose recovery job.

