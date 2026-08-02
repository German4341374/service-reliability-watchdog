# Runbook: mass endpoint outage

## Trigger

Many unrelated endpoints become Unavailable in the same few minutes, or alert volume increases
across several services.

## Triage

1. Check process and database readiness: `curl -fsS localhost:8080/health/ready`.
2. Inspect `watchdog_database_ready`, `watchdog_storage_errors_total`, and queue-drop metrics.
3. Compare DNS errors, connection errors, and HTTP failures in structured logs.
4. Test one endpoint from a second network location to distinguish monitor egress from service
   failure.
5. Check shared dependencies: DNS, proxy, firewall, certificate trust store, and routing.

## Mitigation

- If a planned shared change is confirmed, add a narrowly scoped maintenance window and restart
  after review.
- If monitor egress is broken, restore routing/proxy/DNS before changing endpoint thresholds.
- Do not increase all timeouts or disable TLS validation to silence the symptom.

## Recovery verification

Confirm circuits half-open and close, endpoints transition to Healthy/Degraded, the recovery alert
is recorded once, readiness remains 200, and queue-drop growth stops. Preserve logs and the
transition timeline for review.

