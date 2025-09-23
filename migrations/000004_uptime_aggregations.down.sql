-- Drop uptime-specific aggregations

DROP MATERIALIZED VIEW IF EXISTS ts.results_agg_1d;
DROP MATERIALIZED VIEW IF EXISTS ts.results_agg_1h;