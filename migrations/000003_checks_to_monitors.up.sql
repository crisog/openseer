-- Rename checks table to monitors and add name field
-- This migration renames all 'check' terminology to 'monitor' terminology

-- Step 1: Add name field to existing checks table
ALTER TABLE app.checks ADD COLUMN name TEXT;

-- Step 2: Populate name field with a default based on id for existing records
UPDATE app.checks SET name = 
    CASE 
        WHEN id LIKE '%-%' THEN REPLACE(INITCAP(REPLACE(id, '-', ' ')), ' ', ' ')
        ELSE INITCAP(id)
    END
WHERE name IS NULL;

-- Step 3: Make name field NOT NULL after populating
ALTER TABLE app.checks ALTER COLUMN name SET NOT NULL;

-- Step 4: Rename tables and columns
-- Rename the main table
ALTER TABLE app.checks RENAME TO monitors;

-- Step 5: Rename foreign key columns in related tables
ALTER TABLE app.jobs RENAME COLUMN check_id TO monitor_id;
ALTER TABLE ts.results_raw RENAME COLUMN check_id TO monitor_id;

-- Step 6: Drop and recreate foreign key constraint with new name
ALTER TABLE app.jobs DROP CONSTRAINT jobs_check_id_fkey;
ALTER TABLE app.jobs ADD CONSTRAINT jobs_monitor_id_fkey 
    FOREIGN KEY (monitor_id) REFERENCES app.monitors(id);

-- Step 7: Drop old indexes
DROP INDEX IF EXISTS idx_checks_next_due;
DROP INDEX IF EXISTS idx_checks_user_id;
DROP INDEX IF EXISTS idx_results_check_event;

-- Step 8: Create new indexes with monitor terminology
CREATE INDEX idx_monitors_next_due ON app.monitors(next_due_at) WHERE enabled = true;
CREATE INDEX idx_monitors_user_id ON app.monitors(user_id);
CREATE INDEX idx_results_monitor_event ON ts.results_raw(monitor_id, event_at DESC);

-- Step 9: Drop and recreate the continuous aggregate view with new column names
DROP MATERIALIZED VIEW IF EXISTS ts.results_agg_1m;

-- Recreate with monitor_id
CREATE MATERIALIZED VIEW ts.results_agg_1m
WITH (timescaledb.continuous) AS
SELECT 
    monitor_id,
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
GROUP BY monitor_id, region, bucket
WITH NO DATA;

-- Recreate the continuous aggregate policy
SELECT add_continuous_aggregate_policy('ts.results_agg_1m',
    start_offset => INTERVAL '10 minutes',
    end_offset => INTERVAL '1 minute',
    schedule_interval => INTERVAL '1 minute');

-- Step 10: Update compression policy segment key
ALTER TABLE ts.results_raw SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'monitor_id, region'
);