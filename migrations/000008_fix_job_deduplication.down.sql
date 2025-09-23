-- Rollback the job deduplication constraint fix

-- Step 1: Drop the new indexes
DROP INDEX IF EXISTS idx_jobs_monitor_region_scheduled;
DROP INDEX IF EXISTS idx_jobs_dedup_active;

-- Step 2: Restore the original unique constraint (though it will have the same issue)
ALTER TABLE app.jobs ADD CONSTRAINT jobs_unique_schedule
    UNIQUE (monitor_id, scheduled_at, region);