-- name: UpsertResult :one
INSERT INTO ts.results_raw (
    run_id, monitor_id, region, event_at, status,
    http_code, dns_ms, connect_ms, tls_ms, ttfb_ms,
    download_ms, total_ms, size_bytes, error_message
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
ON CONFLICT (run_id, event_at) 
DO UPDATE SET
    status = EXCLUDED.status,
    http_code = EXCLUDED.http_code,
    dns_ms = EXCLUDED.dns_ms,
    connect_ms = EXCLUDED.connect_ms,
    tls_ms = EXCLUDED.tls_ms,
    ttfb_ms = EXCLUDED.ttfb_ms,
    download_ms = EXCLUDED.download_ms,
    total_ms = EXCLUDED.total_ms,
    size_bytes = EXCLUDED.size_bytes,
    error_message = EXCLUDED.error_message
RETURNING *;

-- name: GetRecentResults :many
SELECT * FROM ts.results_raw
WHERE monitor_id = $1
AND event_at >= NOW() - INTERVAL '1 hour'
ORDER BY event_at DESC
LIMIT $2;

-- name: GetLatestResultPerMonitor :many
SELECT DISTINCT ON (monitor_id) 
    monitor_id, region, event_at, status, http_code, total_ms
FROM ts.results_raw
WHERE event_at >= NOW() - INTERVAL '5 minutes'
ORDER BY monitor_id, event_at DESC;

-- name: GetAggregatedMetrics :many
SELECT * FROM ts.results_agg_1m
WHERE monitor_id = $1
AND bucket >= $2
AND bucket <= $3
ORDER BY bucket DESC;

-- name: GetUptimeData24h :one
SELECT 
    COALESCE(CAST(SUM(total_checks) AS BIGINT), 0) as total_checks,
    COALESCE(CAST(SUM(successful_checks) AS BIGINT), 0) as successful_checks,
    COALESCE(CAST(SUM(failed_checks) AS BIGINT), 0) as failed_checks,
    COALESCE(CAST(SUM(successful_checks) AS FLOAT) / NULLIF(SUM(total_checks), 0) * 100, 0)::int as uptime_percentage
FROM ts.results_agg_1h
WHERE monitor_id = $1
AND bucket >= NOW() - INTERVAL '24 hours';

-- name: GetUptimeData24hRaw :one
SELECT 
    COALESCE(COUNT(*), 0) as total_checks,
    COALESCE(COUNT(*) FILTER (WHERE status = 'OK'), 0) as successful_checks,
    COALESCE(COUNT(*) FILTER (WHERE status != 'OK'), 0) as failed_checks,
    COALESCE(CAST(COUNT(*) FILTER (WHERE status = 'OK') AS FLOAT) / NULLIF(COUNT(*), 0) * 100, 0)::int as uptime_percentage
FROM ts.results_raw
WHERE monitor_id = $1
AND event_at >= NOW() - INTERVAL '24 hours';

-- name: GetUptimeData7d :one
SELECT 
    COALESCE(CAST(SUM(total_checks) AS BIGINT), 0) as total_checks,
    COALESCE(CAST(SUM(successful_checks) AS BIGINT), 0) as successful_checks,
    COALESCE(CAST(SUM(failed_checks) AS BIGINT), 0) as failed_checks,
    COALESCE(CAST(SUM(successful_checks) AS FLOAT) / NULLIF(SUM(total_checks), 0) * 100, 0)::int as uptime_percentage
FROM ts.results_agg_1h
WHERE monitor_id = $1
AND bucket >= NOW() - INTERVAL '7 days';

-- name: GetUptimeData30d :one
SELECT 
    COALESCE(CAST(SUM(total_checks) AS BIGINT), 0) as total_checks,
    COALESCE(CAST(SUM(successful_checks) AS BIGINT), 0) as successful_checks,
    COALESCE(CAST(SUM(failed_checks) AS BIGINT), 0) as failed_checks,
    COALESCE(CAST(SUM(successful_checks) AS FLOAT) / NULLIF(SUM(total_checks), 0) * 100, 0)::int as uptime_percentage
FROM ts.results_agg_1d
WHERE monitor_id = $1
AND bucket >= NOW() - INTERVAL '30 days';

-- name: GetUptimeTimeline24h :many
SELECT 
    time_bucket(INTERVAL '15 minutes', event_at) AS bucket,
    COUNT(*) as total_checks,
    COUNT(*) FILTER (WHERE status = 'OK') as successful_checks,
    COALESCE(CAST(COUNT(*) FILTER (WHERE status = 'OK') AS FLOAT) / NULLIF(COUNT(*), 0) * 100, 0)::int as uptime_percentage
FROM ts.results_raw
WHERE monitor_id = $1
AND event_at >= NOW() - INTERVAL '24 hours'
GROUP BY bucket
ORDER BY bucket ASC;

-- name: GetUptimeTimeline7d :many
SELECT 
    time_bucket(INTERVAL '2 hours', bucket) AS bucket,
    SUM(total_checks) as total_checks,
    SUM(successful_checks) as successful_checks,
    COALESCE(CAST(SUM(successful_checks) AS FLOAT) / NULLIF(SUM(total_checks), 0) * 100, 0)::int as uptime_percentage
FROM ts.results_agg_1h
WHERE monitor_id = $1
AND bucket >= NOW() - INTERVAL '7 days'
GROUP BY time_bucket(INTERVAL '2 hours', bucket)
ORDER BY bucket ASC;

-- name: GetUptimeTimeline30d :many
SELECT
    bucket,
    total_checks,
    successful_checks,
    uptime_percentage
FROM ts.results_agg_1d
WHERE monitor_id = $1
AND bucket >= NOW() - INTERVAL '30 days'
ORDER BY bucket ASC;

-- name: CountResultsByRunID :one
SELECT COUNT(*) as count
FROM ts.results_raw
WHERE run_id = $1;

-- name: GetResultByRunIDAndTime :one
SELECT status, http_code, total_ms
FROM ts.results_raw
WHERE run_id = $1
AND event_at = $2;