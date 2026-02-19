# OpenSeer — Distributed HTTP Monitoring

Self-hosted, horizontally scalable HTTP monitoring with a modern web interface. Multi-user support with secure authentication, real-time dashboards, and comprehensive monitoring management. Built with Go backend and Next.js frontend.

## Architecture

```mermaid
graph TB
    Users[👥 Users]
    WebUI[🌐 Web UI<br/>Next.js :3000]
    ControlPlane[🎯 Control Plane<br/>Go :8080]
    Database[(🗄️ TimescaleDB<br/>PostgreSQL)]

    subgraph "Workers - Distributed"
        WorkerUS[⚡ Worker US-East<br/>Go - us-east-1]
        WorkerEU[⚡ Worker EU-West<br/>Go - eu-west-1]
        WorkerAP[⚡ Worker AP-South<br/>Go - ap-south-1]
    end

    Users --> WebUI
    WebUI -.->|Session Auth<br/>Connect RPC| ControlPlane
    WorkerUS -->|Token Auth<br/>HTTP Polling| ControlPlane
    WorkerEU -->|Token Auth<br/>HTTP Polling| ControlPlane
    WorkerAP -->|Token Auth<br/>HTTP Polling| ControlPlane
    ControlPlane --> Database

    subgraph "Database Schemas"
        AppSchema[📋 app schema<br/>monitors, jobs, workers, users]
        TSSchema[📊 ts schema<br/>results_raw, results_agg_*]
    end

    Database --- AppSchema
    Database --- TSSchema
```

OpenSeer consists of four main components:

- **Web Frontend (Next.js)** - Modern dashboard with real-time metrics visualization, multi-user support with secure session-based authentication
- **Control Plane (Go)** - Service managing workers and job scheduling via HTTP APIs
- **Workers (Go)** - Distributed agents executing HTTP checks across geographic regions, using HTTP polling with token-based authentication
- **Database (PostgreSQL + TimescaleDB)** - Time-series storage with automatic aggregation (1-minute, 1-hour, and 1-day intervals), P50/P95/P99 latency tracking, and uptime statistics

## Worker Communication

Workers communicate with the Control Plane using a simple HTTP polling model:

1. **Enrollment**: Workers register using a cluster token and receive an API token (`ostk_...`)
2. **Job Polling**: Workers poll `GetJobs` endpoint to fetch available jobs for their region
3. **Result Submission**: After executing checks, workers submit results via `SubmitResult`
4. **Lease Renewal**: For long-running checks, workers can extend job leases via `RenewLease`

This polling-based architecture provides:
- **Simplicity**: No persistent connections to maintain
- **Scalability**: Stateless control plane, easy horizontal scaling
- **Resilience**: Workers automatically reconnect on network issues

For detailed architecture documentation, see:
- [Control Plane Architecture](cmd/control-plane/README.md)
- [Worker Architecture](cmd/worker/README.md)
- [Multi-Cloud Production Guidance](docs/production-multicloud.md)

## Quick Start

Start the complete stack with one command:

```bash
task up
```

Then access:
- **Web UI**: http://localhost:3000
- **API**: http://localhost:8080

### Step-by-Step Setup

1. **Start Database**
   ```bash
   task db-up       # PostgreSQL + TimescaleDB
   task migrate-up  # Database migrations
   ```

2. **Start Services**
   ```bash
   task backend-up  # Control plane + workers
   task web-up      # Web interface
   ```

3. **Create Monitors**
   - Open http://localhost:3000
   - Sign up/sign in
   - Add monitors via the web interface

### Development Mode

For development with hot-reload:

```bash
task dev-full    # Full stack with live reload
```

### Common Commands

```bash
task logs              # View all service logs
task scale-workers N=3 # Scale workers
task psql              # Database CLI
task build             # Build images
task --list            # Show all available tasks
```

## Contributing

Contributions are welcome! Please see the detailed architecture documentation in the `cmd/` directories for implementation details.

## License

MIT
