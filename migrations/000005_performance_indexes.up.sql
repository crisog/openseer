CREATE INDEX idx_jobs_ready_lease ON app.jobs (status, region, scheduled_at);

CREATE INDEX idx_jobs_monitor_status_scheduled ON app.jobs (monitor_id, status, scheduled_at);

CREATE INDEX idx_monitors_due ON app.monitors (enabled, next_due_at) WHERE enabled = true;

CREATE INDEX idx_results_raw_monitor_time ON ts.results_raw (monitor_id, event_at DESC);

CREATE INDEX idx_jobs_cleanup ON app.jobs (status, scheduled_at) WHERE status = 'done';

CREATE INDEX idx_workers_activity ON app.workers (status, last_seen_at) WHERE status = 'active';