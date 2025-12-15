# Control Plane Architecture

The Control Plane is a horizontally scalable Go service that orchestrates HTTP monitoring across distributed workers.

> Note: The Worker API now uses Connect/HTTP with bearer tokens and polling endpoints (`GetJobs`, `SubmitResult`, `RenewLease`). Legacy mTLS/stream references in the diagrams below are deprecated and slated for cleanup.

## Overview

```mermaid
graph TB
    WebAPI[🌐 Web API :8082<br/>Connect RPC]
    WorkerAPI[⚡ Worker API :8081<br/>gRPC + mTLS]

    subgraph "Core Services"
        Scheduler[📅 Scheduler]
        Dispatcher[🎯 Dispatcher]
        LeaseReaper[🔄 Lease Reaper]
        StreamHealth[💓 Stream Health Monitor]
        WorkerInactivity[👥 Worker Inactivity Monitor]
        Ingest[📥 Ingest]
        PKI[🔐 PKI Manager]
    end

    subgraph "Web Services"
        Dashboard[📊 Dashboard Service]
        Monitors[🖥️ Monitors Service]
        User[👤 User Service]
        Enrollment[📝 Enrollment Service]
    end

    Database[(🗄️ Database)]

    WebAPI --> Dashboard
    WebAPI --> Monitors
    WebAPI --> User
    WebAPI --> Enrollment

    WorkerAPI --> Dispatcher

    Scheduler --> Database
    Dispatcher --> Database
    LeaseReaper --> Database
    Ingest --> Database

    StreamHealth -.-> WorkerAPI
    WorkerInactivity --> Database
    PKI --> Database
```

## Dual API Architecture

### Worker API (:8081)
- **Protocol**: Connect gRPC over HTTP/2
- **Authentication**: mTLS client certificates
- **Purpose**: Bidirectional streaming with workers
- **Services**: Job distribution, result collection, enrollment

### Web API (:8082)
- **Protocol**: Connect RPC over HTTP
- **Authentication**: Session-based (cookies)
- **TLS**: Enabled by default (self-signed); can be disabled with `WEB_TLS_DISABLE=true`
- **Purpose**: Frontend integration (also serves Enrollment Service)
- **Services**: Dashboard data, monitor CRUD, user management

## Core Services

### Scheduler
**Purpose**: Creates jobs from monitor configurations

- Polls every 1 second with ~5 second look-ahead
- Uses PostgreSQL advisory locks for leader election
- Tracks cadence with `next_due_at` to prevent duplicates
- Implements adaptive jitter:
  - No jitter for intervals ≤10s (precision monitoring)
  - 1% jitter for 10s-30s intervals
  - 10% jitter for >30s intervals (thundering herd protection)

### Dispatcher
**Purpose**: Manages job leasing and worker communication

- **Pull-based model**: Workers request jobs when ready
- **Database-backed leases**: Uses `FOR UPDATE SKIP LOCKED`
- **Lease management**: 45s timeout with 10s renewal cycles
- **Region awareness**: Routes jobs to worker region; falls back to `global` jobs when needed
- **Result handling**: ACK only after durable database commit

### Lease Reaper
**Purpose**: Reclaims expired job leases

- Runs every 5 seconds using leader election
- Returns expired `leased` jobs to `ready` state
- Enables rapid failure recovery from crashed workers
- Uses advisory locks to prevent duplicate reaping

### Stream Health Monitor
**Purpose**: Detects dead worker connections

- Checks every 15 seconds; pings workers that haven't been pinged recently (≈30s)
- Expects pong responses within timeout
- Closes stalled streams to free resources
- Works with Worker Inactivity Monitor for cleanup

### Worker Inactivity Monitor
**Purpose**: Maintains worker registry health

- Monitors worker heartbeats and last-seen timestamps
- Marks inactive workers based on configurable thresholds
- Updates worker status for dashboard visibility
- Coordinates with lease reaper for job reassignment

### Ingest
**Purpose**: Persists monitoring results

- Writes to TimescaleDB `ts.results_raw` hypertable
- Uses UPSERT by `(run_id, event_at)` for idempotency
- Enables safe retries when jobs are re-executed
- Triggers continuous aggregate updates

### PKI Manager
**Purpose**: Certificate lifecycle management

- Issues mTLS certificates for worker authentication
- Stores CA and certificates securely:
  - `~/.openseer/ca.pem` (user mode)
  - `/var/lib/openseer/ca.pem` (system mode)
- Handles certificate renewal and revocation
- Manages enrollment token validation

## Web Services

### Dashboard Service
- Aggregated metrics from continuous aggregates
- Monitor health overview and recent failures
- Real-time uptime statistics and latency percentiles
- Regional performance breakdown

### Monitors Service
- CRUD operations for monitor configurations
- Soft delete support with audit trails
- Validation of URLs, intervals, and assertions
- Region targeting and scheduling metadata

### User Service
- User profile and session management
- Integration with Better Auth for authentication
- Session-based API authentication
- Account lifecycle management

### Enrollment Service
- Worker registration and certificate issuance
- Cluster token validation
- Worker capability registration
- Bootstrap secret distribution

## Correctness Guarantees

### Exactly-Once Job Assignment
1. **Database-level row locking** with `FOR UPDATE SKIP LOCKED`
2. **Job state machine**: `ready → leased → done`
3. **Worker ID enforcement** in all lease operations
4. **Automatic lease expiry** returns jobs to ready state

### Scheduling Precision
- **Pre-scheduled jobs**: 5s look-ahead prevents delays
- **High-frequency polling**: 1s scheduler cycle
- **Per-check cadence tracking**: Prevents duplicate scheduling
- **Leader election**: Only one scheduler creates jobs

### Result Durability
- **ACK after commit**: Only acknowledge durable writes
- **Idempotent ingest**: UPSERT by `(run_id, event_at)` allows retries
- **Best-effort streaming**: Lease expiry handles failures

## Data Model

### Application Schema (`app`)

**monitors**
```sql
- id, name, user_id, url, method, interval_ms, timeout_ms
- headers (JSON), assertions (JSON), regions (array)
- last_scheduled_at, next_due_at, enabled, deleted_at
- jitter_seed (deterministic randomization)
```

**jobs**
```sql
- run_id (PK), monitor_id, region, status, scheduled_at
- lease_expires_at, worker_id, deleted_at
- States: ready → leased → done
```

**workers**
```sql
- id, hostname, region, version, status
- registered_at, enrolled_at, last_seen_at, token_hash
- revoked_at, revoked_reason
```

### Time-series Schema (`ts`)

**results_raw** (Hypertable)
```sql
- Partitioned by event_at (1-day chunks)
- Request/response timings, HTTP status, payload size
- Error messages and regional attribution
- UPSERT by run_id for idempotency
```

**results_agg_1m/1h/1d** (Continuous Aggregates)
```sql
- Pre-calculated: count, error_rate, p50/p95/p99
- Uptime percentages and success/failure counts
- Automatic refresh with configurable lag
```

## Scalability

### Horizontal Scaling
- **Stateless design**: All state in database
- **Leader election**: Advisory locks coordinate replicas
- **Load balancing**: Multiple control plane instances
- **Database connection pooling**: Shared connection management

## Security

### mTLS for Workers
- Client certificate authentication
- Certificate-based worker identity
- Secure enrollment with cluster tokens
- CA certificate distribution

### Session Authentication for Web
- Cookie-based sessions
- CSRF protection
- Secure session storage
- Token format: `tokenId.signature` (HMAC-SHA256 over tokenId with `BETTER_AUTH_SECRET`)
- Session lookup against `web/migrations/auth/schema.sql` tables (`session`, `user`)

### Certificate Storage
- Secure file permissions (0600 for private keys)
- Appropriate data directories based on user context
- Certificate expiry tracking and renewal
