-- Remove policies and aggregates
SELECT remove_continuous_aggregate_policy('ts.results_agg_1m', if_exists => true);
SELECT remove_compression_policy('ts.results_raw', if_exists => true);
DROP MATERIALIZED VIEW IF EXISTS ts.results_agg_1m;

-- Drop indexes added in up
DROP INDEX IF EXISTS app.idx_checks_user_id;
DROP INDEX IF EXISTS app.idx_checks_next_due;
DROP INDEX IF EXISTS ts.idx_results_failures;
DROP INDEX IF EXISTS ts.idx_results_event_at_brin;
DROP INDEX IF EXISTS ts.idx_results_run_id;
DROP INDEX IF EXISTS ts.idx_results_check_event;
DROP INDEX IF EXISTS app.idx_jobs_lease_expires;
DROP INDEX IF EXISTS app.idx_jobs_status_scheduled;

DROP TABLE IF EXISTS app.workers;
DROP TABLE IF EXISTS ts.results_raw;
DROP TABLE IF EXISTS app.jobs;
DROP TABLE IF EXISTS app.checks;
DROP SCHEMA IF EXISTS ts CASCADE;
DROP SCHEMA IF EXISTS app CASCADE;
DROP EXTENSION IF EXISTS timescaledb;