# Runbook: DNS failure

## Evidence

Results show `dnsResolved=false` and messages beginning with `DNS resolution failed`.

## Actions

1. Resolve the hostname from the watchdog container:
   `docker compose exec watchdog /healthcheck http://demo-target:8090/control` for the local demo,
   or use an approved DNS diagnostic image on the same network.
2. Check container DNS configuration and the host resolver.
3. Verify the YAML hostname for spelling and search-domain assumptions.
4. Confirm A/AAAA record, TTL, and recent authoritative changes from a second resolver.
5. Restore the resolver or correct the record. Do not replace a hostname with a hard-coded IP
   unless the service design explicitly supports it.

Recovery requires a new successful DNS lookup and protocol check. Confirm the transition and reset
any temporary maintenance window.

