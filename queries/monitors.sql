-- name: GetMonitor :one
SELECT * FROM app.monitors
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetMonitorByUser :one
SELECT * FROM app.monitors
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- name: ListAllMonitors :many
SELECT * FROM app.monitors
WHERE deleted_at IS NULL
ORDER BY id;

-- name: ListMonitorsByUser :many
SELECT * FROM app.monitors
WHERE user_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: ListEnabledMonitors :many
SELECT * FROM app.monitors
WHERE enabled = true AND deleted_at IS NULL
ORDER BY id;

-- name: ListEnabledMonitorsByUser :many
SELECT * FROM app.monitors
WHERE enabled = true AND user_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC;

-- name: ListDueMonitors :many
-- List monitors that need jobs scheduled (look ahead by schedule window)
-- Uses FOR UPDATE SKIP LOCKED to prevent race conditions between scheduler instances
SELECT * FROM app.monitors
WHERE enabled = true
  AND deleted_at IS NULL
  AND (next_due_at IS NULL OR next_due_at <= $1)
ORDER BY next_due_at NULLS FIRST
FOR UPDATE SKIP LOCKED;

-- name: UpdateMonitorSchedulingTime :one
UPDATE app.monitors SET
    last_scheduled_at = $2,
    next_due_at = $3,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: CreateMonitor :one
INSERT INTO app.monitors (
    id, name, url, interval_ms, timeout_ms, regions,
    jitter_seed, rate_limit_key, method, headers, assertions, enabled, user_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)
RETURNING *;

-- name: UpdateMonitor :one
UPDATE app.monitors SET
    name = $2,
    url = $3,
    interval_ms = $4,
    timeout_ms = $5,
    regions = $6,
    jitter_seed = $7,
    rate_limit_key = $8,
    method = $9,
    headers = $10,
    assertions = $11,
    enabled = $12,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateMonitorByUser :one
UPDATE app.monitors SET
    name = $3,
    url = $4,
    interval_ms = $5,
    timeout_ms = $6,
    regions = $7,
    jitter_seed = $8,
    rate_limit_key = $9,
    method = $10,
    headers = $11,
    assertions = $12,
    enabled = $13,
    updated_at = NOW()
WHERE id = $1 AND user_id = $2
RETURNING *;

-- name: DeleteMonitor :exec
UPDATE app.monitors
SET deleted_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;

-- name: DeleteMonitorByUser :exec
UPDATE app.monitors
SET deleted_at = NOW()
WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL;

-- name: DeleteMonitorJobs :exec
-- Delete all pending jobs for a monitor (called when monitor is deleted)
UPDATE app.jobs
SET deleted_at = NOW()
WHERE monitor_id = $1
AND status IN ('ready', 'leased')
AND deleted_at IS NULL;

-- name: GetMonitorIncludingDeleted :one
SELECT * FROM app.monitors
WHERE id = $1
LIMIT 1;

-- name: CountActiveMonitorsByID :one
SELECT COUNT(*)
FROM app.active_monitors
WHERE id = $1;
