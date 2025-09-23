-- Add enrollment fields to workers table
ALTER TABLE app.workers
ADD COLUMN hostname TEXT,
ADD COLUMN enrolled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
ADD COLUMN certificate_expires_at TIMESTAMPTZ,
ADD COLUMN revoked_at TIMESTAMPTZ,
ADD COLUMN revoked_reason TEXT;

-- Add constraints for worker status
ALTER TABLE app.workers
DROP CONSTRAINT IF EXISTS workers_status_check;

ALTER TABLE app.workers
ADD CONSTRAINT workers_status_check CHECK (status IN ('enrolled', 'active', 'inactive', 'revoked'));

-- Create worker capabilities table
CREATE TABLE app.worker_capabilities (
    worker_id TEXT REFERENCES app.workers(id) ON DELETE CASCADE,
    capability_key TEXT NOT NULL,
    capability_value TEXT NOT NULL,
    PRIMARY KEY (worker_id, capability_key)
);

-- Create indexes for efficient querying
CREATE INDEX idx_workers_status ON app.workers(status);
CREATE INDEX idx_workers_region ON app.workers(region);
CREATE INDEX idx_workers_certificate_expires ON app.workers(certificate_expires_at);
CREATE INDEX idx_worker_capabilities_worker_id ON app.worker_capabilities(worker_id);