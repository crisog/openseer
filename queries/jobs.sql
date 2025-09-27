-- name: CreateJob :one
INSERT INTO app.jobs (
    run_id, monitor_id, region, status, scheduled_at
) VALUES (
    $1, $2, $3, 'ready', $4
)
RETURNING *;

-- name: CreateJobIdempotent :one
INSERT INTO app.jobs (
    run_id, monitor_id, region, status, scheduled_at
)
SELECT $1, $2, $3, 'ready', $4
WHERE NOT EXISTS (
    SELECT 1 FROM app.jobs j
    WHERE j.monitor_id = $2
    AND j.region = $3
    AND j.deleted_at IS NULL
    AND j.status IN ('ready', 'leased')
    AND j.scheduled_at BETWEEN $5 AND $6  -- Time window parameters
)
RETURNING *;

-- name: LeaseJobs :many
WITH leased_jobs AS (
    UPDATE app.jobs
    SET
        status = 'leased',
        lease_expires_at = $4,
        worker_id = $1
    WHERE run_id IN (
        SELECT run_id
        FROM app.jobs
        WHERE status = 'ready'
        AND deleted_at IS NULL
        AND scheduled_at <= NOW()
        AND app.jobs.region = $3  -- Filter by worker region
        ORDER BY scheduled_at
        LIMIT $2
        FOR UPDATE SKIP LOCKED
    )
    RETURNING *
)
SELECT * FROM leased_jobs;

-- name: LeaseJobsWithFallback :many
WITH leased_jobs AS (
    UPDATE app.jobs
    SET
        status = 'leased',
        lease_expires_at = $4,
        worker_id = $1
    WHERE run_id IN (
        SELECT run_id
        FROM app.jobs
        WHERE status = 'ready'
        AND deleted_at IS NULL
        AND scheduled_at <= NOW()
        AND app.jobs.region IN ($3, 'global')  -- Try worker region first, then global
        ORDER BY
            CASE WHEN app.jobs.region = $3 THEN 0 ELSE 1 END,  -- Prioritize exact region match
            scheduled_at
        LIMIT $2
        FOR UPDATE SKIP LOCKED
    )
    RETURNING *
)
SELECT * FROM leased_jobs;

-- name: RenewLease :one
UPDATE app.jobs
SET lease_expires_at = $3
WHERE run_id = $1 
AND status = 'leased'
AND worker_id = $2
RETURNING *;

-- name: CompleteJob :one
UPDATE app.jobs
SET status = 'done',
    lease_expires_at = NULL
WHERE run_id = $1
AND status = 'leased'
AND worker_id = $2
RETURNING *;

-- name: GetJobByRunID :one
SELECT * FROM app.jobs
WHERE run_id = $1
LIMIT 1;

-- name: ReclaimExpiredLeases :exec
UPDATE app.jobs
SET 
    status = 'ready',
    lease_expires_at = NULL,
    worker_id = NULL
WHERE status = 'leased'
AND deleted_at IS NULL
AND lease_expires_at < NOW();

-- name: GetReadyJobsCount :one
SELECT COUNT(*) as count
FROM app.jobs
WHERE status = 'ready'
AND deleted_at IS NULL
AND scheduled_at <= NOW();

-- name: CheckJobExists :one
SELECT EXISTS(
    SELECT 1 FROM app.jobs
    WHERE monitor_id = $1
    AND deleted_at IS NULL
    AND status IN ('ready', 'leased')
    AND scheduled_at BETWEEN $2 AND $3
) as exists;

-- name: LeaseJobsWithMonitorData :many
-- Lease jobs with monitor data
WITH leased_jobs AS (
    UPDATE app.jobs
    SET
        status = 'leased',
        lease_expires_at = $4,
        worker_id = $1
    WHERE run_id IN (
        SELECT run_id
        FROM app.jobs
        WHERE status = 'ready'
        AND deleted_at IS NULL
        AND scheduled_at <= NOW()
        AND app.jobs.region IN ($3, 'global')  -- Try worker region first, then global
        ORDER BY
            CASE WHEN app.jobs.region = $3 THEN 0 ELSE 1 END,  -- Prioritize exact region match
            scheduled_at
        LIMIT $2
        FOR UPDATE SKIP LOCKED
    )
    RETURNING *
)
SELECT
    j.run_id,
    j.monitor_id,
    j.region,
    j.status,
    j.scheduled_at,
    j.lease_expires_at,
    j.worker_id,
    j.created_at,
    j.deleted_at,
    m.id as monitor_id_2,
    m.url,
    m.method,
    m.timeout_ms,
    m.interval_ms,
    m.headers,
    m.regions
FROM leased_jobs j
JOIN app.monitors m ON j.monitor_id = m.id
WHERE m.deleted_at IS NULL;

-- name: CountJobsForMonitor :one
SELECT COUNT(*) as count
FROM app.jobs
WHERE monitor_id = $1
AND deleted_at IS NULL;

-- name: GetJobsForMonitor :many
SELECT * FROM app.jobs
WHERE monitor_id = $1
AND deleted_at IS NULL
ORDER BY scheduled_at DESC;

-- name: GetCompletedJobsByMonitor :many
SELECT * FROM app.jobs
WHERE monitor_id = $1
AND status = 'done'
AND deleted_at IS NULL
ORDER BY scheduled_at DESC;

-- name: CountDeletedJobsForMonitor :one
SELECT COUNT(*)
FROM app.jobs
WHERE monitor_id = $1
AND deleted_at IS NOT NULL;

-- name: ForceExpireJobLease :exec
UPDATE app.jobs
SET lease_expires_at = $2
WHERE run_id = $1;
