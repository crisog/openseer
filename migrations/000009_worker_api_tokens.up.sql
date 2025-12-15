ALTER TABLE app.workers ADD COLUMN token_hash TEXT;

CREATE INDEX idx_workers_token_hash ON app.workers(token_hash) WHERE token_hash IS NOT NULL;
