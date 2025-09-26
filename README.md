# OpenSeer — Distributed HTTP Monitoring

Self-hosted, horizontally scalable HTTP monitoring with a modern web interface. Multi-user support with secure authentication, real-time dashboards, and comprehensive monitoring management. Built with Go backend and TanStack Start frontend.

## Architecture

```mermaid
graph TB
    Users[👥 Users]
    WebUI[🌐 Web UI<br/>TanStack Start :3000]
    ControlPlane[🎯 Control Plane<br/>Go :8081/:8082]
    Database[(🗄️ TimescaleDB<br/>PostgreSQL)]

    subgraph "Workers - Distributed"
        WorkerUS[⚡ Worker US-East<br/>Go - us-east-1]
        WorkerEU[⚡ Worker EU-West<br/>Go - eu-west-1]
        WorkerAP[⚡ Worker AP-South<br/>Go - ap-south-1]
    end

    Users --> WebUI
    WebUI -.->|Session Auth<br/>Connect RPC| ControlPlane
    WorkerUS <-.->|mTLS gRPC<br/>Bidirectional Stream| ControlPlane
    WorkerEU <-.->|mTLS gRPC<br/>Bidirectional Stream| ControlPlane
    WorkerAP <-.->|mTLS gRPC<br/>Bidirectional Stream| ControlPlane
    ControlPlane --> Database

    subgraph "Database Schemas"
        AppSchema[📋 app schema<br/>monitors, jobs, workers, users]
        TSSchema[📊 ts schema<br/>results_raw, results_agg_*]
    end

    Database --- AppSchema
    Database --- TSSchema
```

OpenSeer consists of four main components:

- **Web Frontend (TanStack Start)** - Modern dashboard with real-time metrics visualization, multi-user support with secure session-based authentication
- **Control Plane (Go)** - Service managing workers and job scheduling
- **Workers (Go)** - Distributed agents executing HTTP checks across geographic regions, communicating via mTLS gRPC
- **Database (PostgreSQL + TimescaleDB)** - Time-series storage with automatic aggregation (1-minute, 1-hour, and 1-day intervals), P50/P95/P99 latency tracking, and uptime statistics

For detailed architecture documentation, see:
- [Control Plane Architecture](cmd/control-plane/ARCHITECTURE.md)
- [Worker Architecture](cmd/worker/ARCHITECTURE.md)

## Quick Start

Start the complete stack with one command:

```bash
task up
```

Then access:
- **Web UI**: http://localhost:3000
- **API**: https://localhost:8082

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
