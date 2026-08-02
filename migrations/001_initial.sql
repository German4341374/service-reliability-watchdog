CREATE TABLE IF NOT EXISTS endpoints (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    check_type TEXT NOT NULL CHECK (check_type IN ('http', 'tcp', 'tls')),
    address TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS check_results (
    id BIGSERIAL PRIMARY KEY,
    endpoint_id TEXT NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    checked_at TIMESTAMPTZ NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('Healthy', 'Degraded', 'Unavailable', 'Maintenance', 'Unknown')),
    latency_ms DOUBLE PRECISION NOT NULL CHECK (latency_ms >= 0),
    http_status INTEGER,
    dns_resolved BOOLEAN NOT NULL,
    tls_valid BOOLEAN,
    tls_expires_at TIMESTAMPTZ,
    certificate_days INTEGER,
    attempt_count INTEGER NOT NULL CHECK (attempt_count >= 0),
    circuit_breaker_open BOOLEAN NOT NULL DEFAULT FALSE,
    message TEXT NOT NULL,
    error_message TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_check_results_endpoint_time
    ON check_results(endpoint_id, checked_at DESC);
CREATE INDEX IF NOT EXISTS idx_check_results_state_time
    ON check_results(state, checked_at DESC);

CREATE TABLE IF NOT EXISTS state_transitions (
    id BIGSERIAL PRIMARY KEY,
    endpoint_id TEXT NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    from_state TEXT NOT NULL,
    to_state TEXT NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL,
    reason TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_transitions_endpoint_time
    ON state_transitions(endpoint_id, changed_at DESC);

CREATE TABLE IF NOT EXISTS alerts (
    id BIGSERIAL PRIMARY KEY,
    endpoint_id TEXT NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    dedupe_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    message TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_alerts_dedupe_time
    ON alerts(dedupe_key, created_at DESC);

