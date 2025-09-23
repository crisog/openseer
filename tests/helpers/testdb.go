package helpers

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testPostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
)

type TestDB struct {
	Container   *testPostgres.PostgresContainer
	DB          *sql.DB
	Pool        *pgxpool.Pool
	Queries     *sqlc.Queries
	DatabaseURL string
	cleanup     func()
}

func SetupTestDB(t *testing.T) *TestDB {
	ctx := context.Background()

	var (
		container *testPostgres.PostgresContainer
		err       error
	)

	func() {
		defer func() {
			if rec := recover(); rec != nil {
				if isDockerUnavailablePanic(rec) {
					t.Skipf("skipping integration test: docker unavailable: %v", rec)
				}
				panic(rec)
			}
		}()

		container, err = testPostgres.Run(ctx,
			"timescale/timescaledb:latest-pg17",
			testPostgres.WithDatabase("openseer_test"),
			testPostgres.WithUsername("openseer"),
			testPostgres.WithPassword("openseer_test_pass"),
			testPostgres.WithSQLDriver("pgx"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(5*time.Minute),
			),
			testcontainers.WithAdditionalWaitStrategy(
				wait.ForListeningPort("5432/tcp"),
			),
		)
	}()
	if err != nil {
		if isDockerUnavailable(err) {
			t.Skipf("skipping integration test: docker unavailable: %v", err)
		}
		require.NoError(t, err)
	}

	host, err := container.Host(ctx)
	require.NoError(t, err)

	port, err := container.MappedPort(ctx, "5432")
	require.NoError(t, err)

	databaseURL := fmt.Sprintf("postgres://openseer:openseer_test_pass@%s:%s/openseer_test?sslmode=disable",
		host, port.Port())

	err = runMigrations(databaseURL)
	require.NoError(t, err)

	testDB := &TestDB{
		Container:   container,
		DatabaseURL: databaseURL,
	}

	testDB.cleanup = func() {
		if testDB.DB != nil {
			testDB.DB.Close()
		}
		if testDB.Pool != nil {
			testDB.Pool.Close()
		}
		container.Terminate(ctx)
	}

	require.NoError(t, testDB.openConnections(ctx))

	t.Cleanup(testDB.cleanup)

	return testDB
}

func isDockerUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "permission denied while trying to connect to the Docker daemon") {
		return true
	}
	if strings.Contains(msg, "Cannot connect to the Docker daemon") {
		return true
	}
	return false
}

func isDockerUnavailablePanic(rec interface{}) bool {
	switch v := rec.(type) {
	case error:
		return isDockerUnavailable(v)
	case string:
		return strings.Contains(v, "permission denied while trying to connect to the Docker daemon") ||
			strings.Contains(v, "Cannot connect to the Docker daemon")
	default:
		return false
	}
}

func (tdb *TestDB) BeginTx(t *testing.T) (*sqlc.Queries, func()) {
	tx, err := tdb.DB.Begin()
	require.NoError(t, err)

	queries := tdb.Queries.WithTx(tx)

	rollback := func() {
		tx.Rollback()
	}

	t.Cleanup(rollback)
	return queries, rollback
}

func (tdb *TestDB) TruncateAll(t *testing.T) {
	tables := []string{
		"ts.results_raw",
		"app.jobs",
		"app.worker_capabilities",
		"app.workers",
		"app.monitors",
	}

	for _, table := range tables {
		_, err := tdb.DB.Exec(fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table))
		require.NoError(t, err)
	}
}

func (tdb *TestDB) Snapshot(ctx context.Context, opts ...testPostgres.SnapshotOption) error {
	return tdb.Container.Snapshot(ctx, opts...)
}

func (tdb *TestDB) Restore(ctx context.Context, opts ...testPostgres.SnapshotOption) error {
	return tdb.Container.Restore(ctx, opts...)
}

func (tdb *TestDB) openConnections(ctx context.Context) error {
	db, err := sql.Open("postgres", tdb.DatabaseURL)
	if err != nil {
		return err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return err
	}
	pool, err := pgxpool.New(ctx, tdb.DatabaseURL)
	if err != nil {
		db.Close()
		return err
	}

	tdb.DB = db
	tdb.Pool = pool
	tdb.Queries = sqlc.New(db)

	return nil
}

func (tdb *TestDB) closeConnections() error {
	if tdb.DB != nil {
		if err := tdb.DB.Close(); err != nil {
			return err
		}
		tdb.DB = nil
	}
	if tdb.Pool != nil {
		tdb.Pool.Close()
		tdb.Pool = nil
	}
	tdb.Queries = nil
	return nil
}

func runMigrations(databaseURL string) error {
	err := runBetterAuthMigrations(databaseURL)
	if err != nil {
		return fmt.Errorf("failed to run Better Auth migrations: %w", err)
	}

	migrationPath, err := findMigrationsPath()
	if err != nil {
		return fmt.Errorf("failed to find migrations path: %w", err)
	}

	m, err := migrate.New(
		fmt.Sprintf("file://%s", migrationPath),
		databaseURL,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}
	defer m.Close()

	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}

func runBetterAuthMigrations(databaseURL string) error {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	authSchemaPath, err := findAuthSchemaPath()
	if err != nil {
		return fmt.Errorf("failed to find auth schema path: %w", err)
	}

	schemaContent, err := os.ReadFile(authSchemaPath)
	if err != nil {
		return fmt.Errorf("failed to read auth schema: %w", err)
	}

	_, err = db.Exec(string(schemaContent))
	if err != nil {
		return fmt.Errorf("failed to execute auth schema: %w", err)
	}

	return nil
}

func findMigrationsPath() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	currentDir := wd
	for {
		migrationPath := filepath.Join(currentDir, "migrations")
		if _, err := os.Stat(migrationPath); err == nil {
			return migrationPath, nil
		}

		parent := filepath.Dir(currentDir)
		if parent == currentDir {
			break
		}
		currentDir = parent
	}

	return "", fmt.Errorf("migrations directory not found")
}

func findAuthSchemaPath() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	currentDir := wd
	for {
		schemaPath := filepath.Join(currentDir, "web", "migrations", "auth", "schema.sql")
		if _, err := os.Stat(schemaPath); err == nil {
			return schemaPath, nil
		}

		parent := filepath.Dir(currentDir)
		if parent == currentDir {
			break
		}
		currentDir = parent
	}

	return "", fmt.Errorf("auth schema file not found")
}
