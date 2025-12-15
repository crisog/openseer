ALTER TABLE app.workers
ADD COLUMN IF NOT EXISTS certificate_expires_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_workers_certificate_expires
ON app.workers(certificate_expires_at);
