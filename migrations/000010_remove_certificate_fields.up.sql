DROP INDEX IF EXISTS app.idx_workers_certificate_expires;

ALTER TABLE app.workers
DROP COLUMN IF EXISTS certificate_expires_at;
