# Multi-Cloud Production Guidance

This guide is for deployments where the control plane and workers do not share a single private network (for example, multiple VPCs, multiple cloud providers, or internet-routed links).

## Baseline Assumptions

- OpenSeer defaults are optimized for private-network self-hosting.
- In private networks, single-port HTTP (`:8080`) with cluster-token enrollment and worker API tokens is usually sufficient.
- In multi-cloud environments, treat all inter-service links as untrusted unless explicitly protected.

## Recommended Architecture

1. Place the control plane behind a production reverse proxy or load balancer.
2. Terminate TLS at the edge proxy (or use passthrough TLS with a mesh).
3. Restrict worker API and enrollment API with network policies, firewall rules, and allowlists.
4. Keep PostgreSQL/TimescaleDB private; do not expose it publicly.
5. Run at least two control-plane replicas behind the same load balancer.

## Network and Transport

1. Use HTTPS for all worker-to-control-plane traffic.
2. Use stable DNS names for control plane endpoints.
3. Set strict ingress rules so only worker source ranges can hit worker APIs.
4. Set strict egress rules so workers can only reach configured monitor destinations and control plane.
5. Add request timeouts and connection limits at the edge proxy.

## Worker Configuration

Use explicit worker settings instead of defaults:

```bash
ENROLLMENT_URL=https://control-plane.example.com
CLUSTER_TOKEN=<managed-secret>
REGION=us-east-1
MAX_CONCURRENCY=5
POLL_BASE_INTERVAL=1s
POLL_MAX_INTERVAL=15s
RESULT_SUBMIT_MAX_ATTEMPTS=6
RESULT_SUBMIT_RETRY_INTERVAL=5s
RESULT_SUBMIT_TIMEOUT=10s
```

## Control Plane Configuration

Use explicit control-plane settings:

```bash
PORT=8080
API_ENDPOINT=https://control-plane.example.com
CORS_ORIGIN=https://app.example.com
CLUSTER_TOKEN=<managed-secret>
BETTER_AUTH_SECRET=<managed-secret>
WORKER_HEARTBEAT_MIN_UPDATE_INTERVAL=15s
WORKER_AUTH_CACHE_TTL=30s
WORKER_AUTH_CACHE_MAX_ENTRIES=50000
JOB_CLEANUP_INTERVAL=1m
JOB_RETENTION_PERIOD=168h
JOB_CLEANUP_BATCH_SIZE=1000
DB_MAX_OPEN_CONNS=100
DB_MAX_IDLE_CONNS=25
DB_CONN_MAX_LIFETIME=30m
DB_CONN_MAX_IDLE_TIME=5m
```

## Token and Secret Operations

1. Store `CLUSTER_TOKEN` and `BETTER_AUTH_SECRET` in a secret manager.
2. Rotate cluster tokens periodically with staged rollout.
3. Revoke compromised workers via `RevokeEnrollment`.
4. Audit enrollment and worker authentication events in centralized logs.

## Scale and Reliability Notes

1. Increase `POLL_MAX_INTERVAL` as worker fleet size grows to reduce idle load.
2. Keep `POLL_BASE_INTERVAL` low enough to maintain acceptable pickup latency.
3. Monitor lease reaper lag and scheduler lag.
4. Alert on spikes in uncommitted results and repeated lease reclaims.
5. Budget DB connections per replica: `replicas * DB_MAX_OPEN_CONNS` should stay below PostgreSQL/pooler limits with headroom.
6. Prefer a PostgreSQL connection pooler for multi-replica fleets and keep app-level pool limits explicit.
7. Keep retention cleanup enabled so `app.jobs` growth does not degrade lease/dispatch query latency over time.

## Operational Checklist

1. TLS enabled for all cross-network traffic.
2. No public database exposure.
3. Secrets sourced from managed secret storage.
4. Control-plane replicas behind health-checked load balancer.
5. Centralized logs and metrics for worker auth failures, lease renewals, and scheduler throughput.
