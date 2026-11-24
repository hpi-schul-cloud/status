package integrationtests

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/hpi-schul-cloud/status/internal/store"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPostgresStatusUpdatePersistsAcrossConnections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("status"),
		postgres.WithUsername("status"),
		postgres.WithPassword("integration-test-password"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get postgres connection string: %v", err)
	}
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Seed(ctx, store.SeedConfig{
		AdminUser: "admin", AdminPassword: "integration-admin-password",
		PageTitle:  "Integration Status",
		Groups:     []store.SeedGroup{{Name: "Platform"}},
		Components: []store.SeedService{{ID: "api", Name: "API", ComponentGroup: "Platform"}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	message := "Requests are delayed\nWe are investigating"
	if err := db.SetStatus(ctx, "api", "partial_outage", message, "operator"); err != nil {
		t.Fatalf("set status: %v", err)
	}

	// Opening a new store models a separate replica reading shared durable state.
	secondReplica, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open second store: %v", err)
	}
	defer secondReplica.Close()
	settings, groups, err := secondReplica.PublicData(ctx)
	if err != nil {
		t.Fatalf("read public data: %v", err)
	}
	if settings["page_title"] != "Integration Status" {
		t.Fatalf("page title = %q", settings["page_title"])
	}
	if len(groups) != 1 || len(groups[0].Items) != 1 {
		t.Fatalf("unexpected groups/components: %#v", groups)
	}
	component := groups[0].Items[0]
	if component.Status != "partial_outage" || component.Message != message {
		t.Fatalf("persisted component = %#v", component)
	}
	history, err := secondReplica.StatusHistory(ctx, 50)
	if err != nil {
		t.Fatalf("read incident history: %v", err)
	}
	if len(history) != 1 || history[0].ComponentName != "API" || history[0].NewStatus != "partial_outage" || history[0].Message != message {
		t.Fatalf("unexpected incident history: %#v", history)
	}

	// Even a token table left by an older schema is removed by the migration.
	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open migration probe: %v", err)
	}
	defer adminDB.Close()
	if _, err := adminDB.ExecContext(ctx, `CREATE TABLE api_tokens (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create legacy token table: %v", err)
	}
	if _, err := adminDB.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=2`); err != nil {
		t.Fatalf("prepare token removal migration: %v", err)
	}
	if err := secondReplica.Migrate(ctx); err != nil {
		t.Fatalf("rerun token removal migration: %v", err)
	}
	var tokenTable *string
	if err := adminDB.QueryRowContext(ctx, `SELECT to_regclass('public.api_tokens')`).Scan(&tokenTable); err != nil {
		t.Fatalf("check token table: %v", err)
	}
	if tokenTable != nil {
		t.Fatalf("legacy API token table still exists: %q", *tokenTable)
	}
}
