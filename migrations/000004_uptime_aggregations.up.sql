-- Add uptime-specific aggregations for better performance
-- These aggregations will help calculate uptime percentages for different time ranges

-- Create 1-hour continuous aggregate for uptime calculations
CREATE MATERIALIZED VIEW ts.results_agg_1h
WITH (timescaledb.continuous) AS
SELECT 
    monitor_id,
    region,
    time_bucket(INTERVAL '1 hour', event_at) AS bucket,
    COUNT(*) as total_checks,
    COUNT(*) FILTER (WHERE status = 'OK') as successful_checks,
    COUNT(*) FILTER (WHERE status != 'OK') as failed_checks,
    CAST(COUNT(*) FILTER (WHERE status = 'OK') AS FLOAT) / NULLIF(COUNT(*), 0) * 100 as uptime_percentage,
    percentile_cont(0.50) WITHIN GROUP (ORDER BY total_ms) as p50_ms,
    percentile_cont(0.95) WITHIN GROUP (ORDER BY total_ms) as p95_ms,
    percentile_cont(0.99) WITHIN GROUP (ORDER BY total_ms) as p99_ms,
    MIN(total_ms) as min_ms,
    MAX(total_ms) as max_ms,
    AVG(total_ms) as avg_ms
FROM ts.results_raw
GROUP BY monitor_id, region, bucket
WITH NO DATA;

-- Add policy for 1-hour aggregate with automatic backfill of all historical data
-- Using NULL for start_offset means it will include ALL historical data
SELECT add_continuous_aggregate_policy('ts.results_agg_1h',
    start_offset => NULL,  -- Backfills all historical data
    end_offset => INTERVAL '1 minute',
    schedule_interval => INTERVAL '1 hour');

-- Create 1-day continuous aggregate for uptime calculations
CREATE MATERIALIZED VIEW ts.results_agg_1d
WITH (timescaledb.continuous) AS
SELECT 
    monitor_id,
    region,
    time_bucket(INTERVAL '1 day', event_at) AS bucket,
    COUNT(*) as total_checks,
    COUNT(*) FILTER (WHERE status = 'OK') as successful_checks,
    COUNT(*) FILTER (WHERE status != 'OK') as failed_checks,
    CAST(COUNT(*) FILTER (WHERE status = 'OK') AS FLOAT) / NULLIF(COUNT(*), 0) * 100 as uptime_percentage,
    percentile_cont(0.50) WITHIN GROUP (ORDER BY total_ms) as p50_ms,
    percentile_cont(0.95) WITHIN GROUP (ORDER BY total_ms) as p95_ms,
    percentile_cont(0.99) WITHIN GROUP (ORDER BY total_ms) as p99_ms,
    MIN(total_ms) as min_ms,
    MAX(total_ms) as max_ms,
    AVG(total_ms) as avg_ms
FROM ts.results_raw
GROUP BY monitor_id, region, bucket
WITH NO DATA;

-- Add policy for 1-day aggregate with automatic backfill of all historical data
-- Using NULL for start_offset means it will include ALL historical data
SELECT add_continuous_aggregate_policy('ts.results_agg_1d',
    start_offset => NULL,  -- Backfills all historical data
    end_offset => INTERVAL '1 hour',
    schedule_interval => INTERVAL '1 day');

-- Create indexes for faster uptime queries
CREATE INDEX idx_results_agg_1h_monitor_bucket ON ts.results_agg_1h(monitor_id, bucket DESC);
CREATE INDEX idx_results_agg_1d_monitor_bucket ON ts.results_agg_1d(monitor_id, bucket DESC);

-- Note: The continuous aggregates are created with NO DATA initially
-- But the policies with NULL start_offset will automatically backfill ALL historical data
-- on their first run (within 1 hour for hourly, within 1 day for daily).
-- No manual refresh needed!