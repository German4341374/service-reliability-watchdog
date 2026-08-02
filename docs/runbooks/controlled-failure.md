# Runbook: controlled failure and restart recovery

The demo is local and uses only synthetic targets. It requires Docker Compose, Bash, curl, and jq.

```bash
bash scripts/controlled-failure-demo.sh
```

To inspect the running system afterward:

```bash
KEEP_DEMO=1 bash scripts/controlled-failure-demo.sh
docker compose logs watchdog
docker compose down --volumes
```

## Manual sequence

```bash
cp .env.example .env
docker compose up -d --build
curl -fsS http://127.0.0.1:8080/health/ready
curl -fsS -X POST 'http://127.0.0.1:18090/control?mode=fail'
curl -fsS http://127.0.0.1:8080/api/v1/endpoints/demo-http | jq
docker compose restart watchdog
curl -fsS -X POST 'http://127.0.0.1:18090/control?mode=healthy'
curl -fsS 'http://127.0.0.1:8080/api/v1/transitions?endpoint=demo-http' | jq
```

Success means Healthy → Unavailable → Healthy transitions are persisted, one Unavailable alert is
stored inside the dedupe window, Unavailable remains visible after restart, and metrics contain
check samples.

