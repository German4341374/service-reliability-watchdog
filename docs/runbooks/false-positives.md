# Runbook: false positives

## Triage

1. Identify the failing expectation: DNS, timeout, status, expected text, latency, or TLS.
2. Compare direct response and response from the watchdog network.
3. Check whether retry attempts consistently failed or only the first attempt failed.
4. Verify a maintenance window boundary and UTC conversion.
5. Look for proxy, redirect, rate-limit, or allow-list behavior specific to the monitor.

## Safe tuning

- Increase timeout only when measured normal latency supports it.
- Prefer a stable machine-readable health response over matching user-facing text.
- Adjust interval and retry count within service capacity.
- Set latency target from an explicit SLO, not a value chosen to clear the dashboard.
- Scope maintenance to exact endpoints and time.

Document the evidence and add a regression test or controlled demo case when configuration or code
changes. Never disable TLS verification or hide Unavailable checks by mapping them to Maintenance.

