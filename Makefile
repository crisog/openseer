.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: db-up
db-up:
	cd deployments && docker compose up -d timescaledb

.PHONY: db-down
db-down:
	cd deployments && docker compose stop timescaledb

.PHONY: db-clean
db-clean:
	cd deployments && docker compose down -v timescaledb

.PHONY: control-up
control-up: db-up
	cd deployments && docker compose --profile app up -d control-plane

.PHONY: control-down
control-down:
	cd deployments && docker compose stop control-plane

.PHONY: control-restart
control-restart:
	cd deployments && docker compose restart control-plane

.PHONY: worker-up
worker-up: control-up
	cd deployments && docker compose --profile app up -d worker

.PHONY: worker-down
worker-down:
	cd deployments && docker compose stop worker

.PHONY: worker-restart
worker-restart:
	cd deployments && docker compose restart worker

.PHONY: scale-workers
scale-workers:
	@if [ -z "$(N)" ]; then \
		echo "Usage: make scale-workers N=3"; \
		exit 1; \
	fi
	cd deployments && docker compose up -d --scale worker=$(N) --no-recreate worker
	@echo "Scaled workers to $(N) instances"

.PHONY: web-up
web-up: db-up
	cd deployments && docker compose --profile app up -d web

.PHONY: web-down
web-down:
	cd deployments && docker compose stop web

.PHONY: web-restart
web-restart:
	cd deployments && docker compose restart web

.PHONY: web-dev
web-dev: db-up
	cd web && npm run dev

.PHONY: web-build
web-build:
	cd web && npm run build

.PHONY: web-migrate
web-migrate: db-up
	cd web && DATABASE_URL="postgresql://openseer:openseer@localhost:5432/openseer" npm run auth:migrate

.PHONY: backend-up
backend-up: control-up worker-up
	@echo "Backend services started"

.PHONY: backend-down
backend-down: worker-down control-down
	@echo "Backend services stopped"

.PHONY: up
up: db-up web-migrate migrate-up control-up worker-up web-up
	@echo "All services started"

.PHONY: down
down:
	cd deployments && docker compose --profile app down

.PHONY: clean
clean:
	cd deployments && docker compose --profile app down -v

.PHONY: migrate-up
migrate-up: db-up
	cd deployments && docker compose run --rm migrate-up

.PHONY: migrate-down
migrate-down: db-up
	cd deployments && docker compose run --rm migrate-down

.PHONY: sqlc
sqlc:
	cd deployments && docker compose run --rm sqlc

.PHONY: build
build:
	cd deployments && docker compose build control-plane worker

.PHONY: build-all
build-all:
	cd deployments && docker compose build

.PHONY: build-control
build-control:
	cd deployments && docker compose build control-plane

.PHONY: build-worker
build-worker:
	cd deployments && docker compose build worker

.PHONY: build-web
build-web:
	cd deployments && docker compose build web

.PHONY: dev
dev: db-up web-migrate migrate-up sqlc
	@echo "==================================="
	@echo "Development environment ready!"
	@echo "==================================="
	@echo "Database: postgres://openseer:openseer@localhost:5432/openseer"
	@echo ""
	@echo "Quick commands:"
	@echo "  make dev-backend    - Start backend services with Air hot reload"
	@echo "  make dev-web        - Start web in dev mode (local)"
	@echo "  make dev-full       - Start everything for full-stack dev"
	@echo "  make test-local     - Run backend locally (Go run)"
	@echo ""
	@echo "Individual services:"
	@echo "  make control-up/down - Control-plane service (with Air hot reload)"
	@echo "  make worker-up/down  - Worker service(s) (with Air hot reload)"
	@echo "  make web-up/down     - Web service (container)"
	@echo "  make web-dev         - Web dev server (local)"

.PHONY: dev-backend
dev-backend: dev backend-up
	@echo "Backend development environment running with Air hot reload"
	@echo "Control-plane: http://localhost:8080"
	@echo "Enrollment: http://localhost:8079"

.PHONY: dev-web
dev-web: dev
	@echo "Web development environment ready"
	@echo "Run 'make web-dev' to start Next.js dev server"

.PHONY: dev-full
dev-full: dev backend-up web-up
	@echo "==================================="
	@echo "Full-stack environment ready!"
	@echo "==================================="
	@echo "All services running in Docker"

.PHONY: test-local
test-local: db-up web-migrate migrate-up
	go run ./cmd/control-plane &
	sleep 2
	go run ./cmd/worker


.PHONY: logs
logs:
	cd deployments && docker compose --profile app logs -f

.PHONY: logs-control
logs-control:
	cd deployments && docker compose logs -f control-plane

.PHONY: logs-worker
logs-worker:
	cd deployments && docker compose logs -f worker

.PHONY: logs-web
logs-web:
	cd deployments && docker compose logs -f web

.PHONY: logs-db
logs-db:
	cd deployments && docker compose logs -f timescaledb

.PHONY: psql
psql:
	cd deployments && docker compose exec timescaledb psql -U openseer

.PHONY: status
status:
	cd deployments && docker compose ps

.PHONY: restart
restart:
	cd deployments && docker compose --profile app restart