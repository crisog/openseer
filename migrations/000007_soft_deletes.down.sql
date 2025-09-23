-- Drop views
DROP VIEW IF EXISTS app.active_jobs;
DROP VIEW IF EXISTS app.active_monitors;

-- Remove soft delete indexes
DROP INDEX IF EXISTS idx_jobs_active;
DROP INDEX IF EXISTS idx_monitors_active;

-- Restore original index
DROP INDEX IF EXISTS idx_monitors_due;
CREATE INDEX idx_monitors_due ON app.monitors(enabled, next_due_at) WHERE enabled = true;

-- Remove soft delete columns
ALTER TABLE app.jobs DROP COLUMN deleted_at;
ALTER TABLE app.monitors DROP COLUMN deleted_at;