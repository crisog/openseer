-- Drop indexes
DROP INDEX IF EXISTS app.idx_worker_capabilities_worker_id;
DROP INDEX IF EXISTS app.idx_workers_certificate_expires;
DROP INDEX IF EXISTS app.idx_workers_region;
DROP INDEX IF EXISTS app.idx_workers_status;

-- Drop worker capabilities table
DROP TABLE IF EXISTS app.worker_capabilities;

-- Remove enrollment fields from workers table
ALTER TABLE app.workers
DROP COLUMN IF EXISTS hostname,
DROP COLUMN IF EXISTS enrolled_at,
DROP COLUMN IF EXISTS certificate_expires_at,
DROP COLUMN IF EXISTS revoked_at,
DROP COLUMN IF EXISTS revoked_reason;

-- Restore original status constraint
ALTER TABLE app.workers
DROP CONSTRAINT IF EXISTS workers_status_check;

ALTER TABLE app.workers
ADD CONSTRAINT workers_status_check CHECK (status IN ('active', 'inactive'));