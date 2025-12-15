-- name: RegisterWorker :one
INSERT INTO app.workers (
    id, region, version, last_seen_at, status
) VALUES (
    $1, $2, $3, NOW(), 'active'
)
ON CONFLICT (id)
DO UPDATE SET
    last_seen_at = NOW(),
    status = 'active'
RETURNING id, region, version, last_seen_at, registered_at, status, hostname, enrolled_at, revoked_at, revoked_reason, token_hash;

-- name: UpdateWorkerHeartbeat :exec
UPDATE app.workers
SET last_seen_at = NOW(),
    status = 'active'
WHERE id = $1;

-- name: GetActiveWorkers :many
SELECT id, region, version, last_seen_at, registered_at, status, hostname, enrolled_at, revoked_at, revoked_reason, token_hash
FROM app.workers
WHERE status = 'active'
AND last_seen_at >= NOW() - INTERVAL '1 minute'
ORDER BY region, id;

-- name: MarkWorkerInactive :exec
UPDATE app.workers
SET status = 'inactive'
WHERE status IN ('active', 'enrolled')
  AND last_seen_at < NOW() - INTERVAL '2 minutes';

-- name: EnrollWorker :one
INSERT INTO app.workers (
    id, hostname, region, version, 
    enrolled_at, last_seen_at, status
) VALUES (
    $1, $2, $3, $4, NOW(), NOW(), 'enrolled'
)
RETURNING id, region, version, last_seen_at, registered_at, status, hostname, enrolled_at, revoked_at, revoked_reason, token_hash;

-- name: GetWorkerByID :one
SELECT id, region, version, last_seen_at, registered_at, status, hostname, enrolled_at, revoked_at, revoked_reason, token_hash
FROM app.workers
WHERE id = $1;

-- name: RevokeWorker :exec
UPDATE app.workers
SET status = 'revoked', revoked_at = NOW(), revoked_reason = $1
WHERE id = $2;

-- name: ListWorkers :many
SELECT id, region, version, last_seen_at, registered_at, status, hostname, enrolled_at, revoked_at, revoked_reason, token_hash
FROM app.workers
WHERE ($1::TEXT IS NULL OR region = $1)
  AND ($2::TEXT IS NULL OR status = $2)
ORDER BY enrolled_at DESC
LIMIT $3 OFFSET $4;

-- name: CountWorkers :one
SELECT COUNT(*) FROM app.workers
WHERE ($1::TEXT IS NULL OR region = $1)
  AND ($2::TEXT IS NULL OR status = $2);

-- name: AddWorkerCapability :exec
INSERT INTO app.worker_capabilities (worker_id, capability_key, capability_value)
VALUES ($1, $2, $3)
ON CONFLICT (worker_id, capability_key)
DO UPDATE SET capability_value = EXCLUDED.capability_value;

-- name: GetWorkerCapabilities :many
SELECT capability_key, capability_value
FROM app.worker_capabilities
WHERE worker_id = $1;

-- name: DeleteWorkerCapabilities :exec
DELETE FROM app.worker_capabilities
WHERE worker_id = $1;

-- name: ListRegionHealth :many
WITH region_stats AS (
    SELECT
        region,
        COUNT(*) FILTER (
            WHERE status = 'active'
              AND last_seen_at >= NOW() - INTERVAL '1 minute'
        ) AS healthy_workers
    FROM app.workers
    GROUP BY region
)
SELECT
    region,
    healthy_workers
FROM region_stats
ORDER BY region;

-- name: GetWorkerByTokenHash :one
SELECT id, region, version, last_seen_at, registered_at, status, hostname, enrolled_at, revoked_at, revoked_reason, token_hash
FROM app.workers
WHERE token_hash = $1
  AND status IN ('enrolled', 'active');

-- name: SetWorkerToken :exec
UPDATE app.workers
SET token_hash = $1
WHERE id = $2;
