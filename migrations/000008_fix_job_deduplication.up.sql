-- Fix job deduplication constraint that conflicts with idempotent job creation
-- The exact timestamp constraint conflicts with the time window approach used in CreateJobIdempotent

-- Step 1: Remove the overly strict unique constraint
ALTER TABLE app.jobs DROP CONSTRAINT IF EXISTS jobs_unique_schedule;

-- Step 2: Create a more flexible partial unique index that prevents true duplicates
-- This allows for the time window approach while still preventing exact duplicates
-- The index only applies to non-deleted jobs in ready/leased state
CREATE UNIQUE INDEX idx_jobs_dedup_active ON app.jobs (monitor_id, region, scheduled_at)
WHERE deleted_at IS NULL AND status IN ('ready', 'leased');

-- Step 3: Add a btree index for efficient time window queries used in CreateJobIdempotent
CREATE INDEX idx_jobs_monitor_region_scheduled ON app.jobs (monitor_id, region, scheduled_at)
WHERE deleted_at IS NULL AND status IN ('ready', 'leased');