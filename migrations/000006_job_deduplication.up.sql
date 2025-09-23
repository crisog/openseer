ALTER TABLE app.jobs ADD CONSTRAINT jobs_unique_schedule
    UNIQUE (monitor_id, scheduled_at, region);