DROP INDEX IF EXISTS app.idx_workers_token_hash;

ALTER TABLE app.workers DROP COLUMN IF EXISTS token_hash;
