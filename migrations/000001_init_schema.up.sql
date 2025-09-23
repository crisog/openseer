-- Enable TimescaleDB extension
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Create schemas
CREATE SCHEMA IF NOT EXISTS app;
CREATE SCHEMA IF NOT EXISTS ts;

-- Checks definition table
CREATE TABLE app.checks (
    id TEXT PRIMARY KEY,
    url TEXT NOT NULL,
    interval_ms INT NOT NULL DEFAULT 60000,
    timeout_ms INT NOT NULL DEFAULT 5000,
    regions TEXT[] NOT NULL DEFAULT '{global}',
    jitter_seed INT NOT NULL DEFAULT 42,
    rate_limit_key TEXT,
    method TEXT NOT NULL DEFAULT 'GET',
    headers JSONB DEFAULT '{}',
    assertions JSONB DEFAULT '[]',
    enabled BOOLEAN DEFAULT true,
    last_scheduled_at TIMESTAMPTZ,
    next_due_at TIMESTAMPTZ,
    user_id TEXT REFERENCES "user"(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Jobs table for lease-based dispatch
CREATE TABLE app.jobs (
    run_id TEXT PRIMARY KEY,
    check_id TEXT NOT NULL REFERENCES app.checks(id),
    region TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ready', -- ready, leased, done
    scheduled_at TIMESTAMPTZ NOT NULL,
    lease_expires_at TIMESTAMPTZ,
    worker_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT jobs_status_check CHECK (status IN ('ready', 'leased', 'done'))
);

CREATE INDEX idx_jobs_status_scheduled ON app.jobs(status, scheduled_at) WHERE status = 'ready';
CREATE INDEX idx_jobs_lease_expires ON app.jobs(lease_expires_at) WHERE status = 'leased';

-- Index for due checks
CREATE INDEX idx_checks_next_due ON app.checks(next_due_at) WHERE enabled = true;

-- Results table (will become hypertable)
CREATE TABLE ts.results_raw (
    run_id TEXT NOT NULL,
    check_id TEXT NOT NULL,
    region TEXT NOT NULL,
    event_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL, -- OK, FAIL, ERROR
    http_code INT,
    dns_ms INT,
    connect_ms INT,
    tls_ms INT,
    ttfb_ms INT,
    download_ms INT,
    total_ms INT,
    size_bytes BIGINT,
    error_message TEXT,
    CONSTRAINT results_pkey PRIMARY KEY (run_id, event_at),
    CONSTRAINT results_status_check CHECK (status IN ('OK', 'FAIL', 'ERROR'))
);

-- Convert results to hypertable
SELECT create_hypertable('ts.results_raw', 'event_at', chunk_time_interval => INTERVAL '1 day');

-- Create index for queries
CREATE INDEX idx_results_check_event ON ts.results_raw(check_id, event_at DESC);
CREATE UNIQUE INDEX idx_results_run_id ON ts.results_raw(run_id, event_at);

-- BRIN index for time scans
CREATE INDEX idx_results_event_at_brin ON ts.results_raw USING BRIN (event_at);

-- Partial index on failures
CREATE INDEX idx_results_failures ON ts.results_raw(status) WHERE status <> 'OK';

-- Continuous aggregate for metrics
CREATE MATERIALIZED VIEW ts.results_agg_1m
WITH (timescaledb.continuous) AS
SELECT 
    check_id,
    region,
    time_bucket(INTERVAL '1 minute', event_at) AS bucket,
    COUNT(*) as count,
    COUNT(*) FILTER (WHERE status != 'OK') as error_count,
    CAST(COUNT(*) FILTER (WHERE status != 'OK') AS FLOAT) / COUNT(*) as error_rate,
    percentile_cont(0.50) WITHIN GROUP (ORDER BY total_ms) as p50_ms,
    percentile_cont(0.95) WITHIN GROUP (ORDER BY total_ms) as p95_ms,
    percentile_cont(0.99) WITHIN GROUP (ORDER BY total_ms) as p99_ms,
    MIN(total_ms) as min_ms,
    MAX(total_ms) as max_ms,
    AVG(total_ms) as avg_ms
FROM ts.results_raw
GROUP BY check_id, region, bucket
WITH NO DATA;

SELECT add_continuous_aggregate_policy('ts.results_agg_1m',
    start_offset => INTERVAL '10 minutes',
    end_offset => INTERVAL '1 minute',
    schedule_interval => INTERVAL '1 minute');

-- Compression and retention policies
ALTER TABLE ts.results_raw SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'check_id, region'
);
SELECT add_compression_policy('ts.results_raw', INTERVAL '24 hours');

-- User indexing
CREATE INDEX idx_checks_user_id ON app.checks(user_id);

-- Workers registration table
CREATE TABLE app.workers (
    id TEXT PRIMARY KEY,
    region TEXT NOT NULL,
    version TEXT NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status TEXT NOT NULL DEFAULT 'active'
);