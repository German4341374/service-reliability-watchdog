# Runbook: certificate expiration

## Trigger

An endpoint becomes Degraded inside `tlsWarnBefore`, or Unavailable because the certificate is
expired, untrusted, has the wrong hostname, or has an invalid chain.

## Actions

1. Read `tlsExpiresAt`, `certificateDaysRemaining`, and `tlsValid` from the endpoint API.
2. Confirm system UTC time and hostname/SNI configuration.
3. Inspect the served chain from a trusted workstation; ensure intermediates are present.
4. Renew through the owning certificate process. Never disable verification as mitigation.
5. Deploy the renewed certificate and confirm every load-balancer node serves it.

## Verification

Wait for Healthy, confirm the new expiration date, verify the recovery transition, and close the
change only after independent browser or TLS-tool validation.

