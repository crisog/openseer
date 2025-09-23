-- Rollback monitors to checks migration

-- Step 1: Drop the new continuous aggregate
DROP MATERIALIZED VIEW IF EXISTS ts.results_agg_1m;

-- Step 2: Drop new indexes
DROP INDEX IF EXISTS idx_monitors_next_due;
DROP INDEX IF EXISTS idx_monitors_user_id; 
DROP INDEX IF EXISTS idx_results_monitor_event;

-- Step 3: Rename columns back to check terminology
ALTER TABLE app.jobs RENAME COLUMN monitor_id TO check_id;
ALTER TABLE ts.results_raw RENAME COLUMN monitor_id TO check_id;

-- Step 4: Rename main table back
ALTER TABLE app.monitors RENAME TO checks;

-- Step 5: Drop and recreate foreign key with old name
ALTER TABLE app.jobs DROP CONSTRAINT jobs_monitor_id_fkey;
ALTER TABLE app.jobs ADD CONSTRAINT jobs_check_id_fkey 
    FOREIGN KEY (check_id) REFERENCES app.checks(id);

-- Step 6: Recreate old indexes
CREATE INDEX idx_checks_next_due ON app.checks(next_due_at) WHERE enabled = true;
CREATE INDEX idx_checks_user_id ON app.checks(user_id);
CREATE INDEX idx_results_check_event ON ts.results_raw(check_id, event_at DESC);

-- Step 7: Recreate original continuous aggregate
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

-- Step 8: Revert compression policy
ALTER TABLE ts.results_raw SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'check_id, region'
);

-- Step 9: Remove name column
ALTER TABLE app.checks DROP COLUMN name;