-- Add soft delete support to monitors
ALTER TABLE app.monitors ADD COLUMN deleted_at TIMESTAMPTZ;

-- Add index for active monitors (not deleted)
CREATE INDEX idx_monitors_active ON app.monitors(user_id, deleted_at) WHERE deleted_at IS NULL;

-- Update existing indexes to exclude soft-deleted records
DROP INDEX IF EXISTS app.idx_monitors_due;
CREATE INDEX idx_monitors_due ON app.monitors(enabled, next_due_at)
    WHERE enabled = true AND deleted_at IS NULL;

-- Add soft delete support to jobs for audit trail
ALTER TABLE app.jobs ADD COLUMN deleted_at TIMESTAMPTZ;

-- Update jobs indexes to work with soft deletes
CREATE INDEX idx_jobs_active ON app.jobs(status, scheduled_at)
    WHERE deleted_at IS NULL;

-- Create a view for active monitors (for easier querying)
CREATE VIEW app.active_monitors AS
SELECT * FROM app.monitors WHERE deleted_at IS NULL;

-- Create a view for active jobs
CREATE VIEW app.active_jobs AS
SELECT * FROM app.jobs WHERE deleted_at IS NULL;