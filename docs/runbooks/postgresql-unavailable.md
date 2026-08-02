# Runbook: PostgreSQL unavailable

## Symptoms

- `/health/live` is 200 but `/health/ready` is 503.
- `watchdog_database_ready` is 0.
- `watchdog_storage_errors_total` increases.
- API/status page requests needing stored data return 500.

## Actions

1. Check `docker compose ps postgres` and PostgreSQL health logs.
2. Verify disk space, volume mount, credentials, and connection limits.
3. Confirm the internal DNS name and port without printing the full DSN.
4. Restore PostgreSQL. The existing process tests readiness every five seconds and resumes writes
   automatically when the pool can connect.
5. If credentials or URL changed, update the secret and restart watchdog.

Checks are not buffered during the outage. Record the data gap. After recovery, verify new rows,
transitions, readiness 200, and `watchdog_database_ready 1`. Do not delete the volume as a routine
recovery step.

