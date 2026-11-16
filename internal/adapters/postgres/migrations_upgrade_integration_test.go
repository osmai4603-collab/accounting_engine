package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
)

// TestMigrationUpgradeFromOldestSupportedSchemaPreservesData exercises the
// full supported upgrade path from the baseline schema to the current chain.
func TestMigrationUpgradeFromOldestSupportedSchemaPreservesData(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pool, _ := freshMigrationPool(t, ctx)
	if err := postgres.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("initial migrate-up: %v", err)
	}

	companyID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO companies(id, code, legal_name, functional_currency, presentation_currency, is_active)
		VALUES($1,$2,$3,'USD','USD',TRUE)`,
		companyID, "UPG-"+companyID.String()[:8], "Upgrade Preservation Co"); err != nil {
		t.Fatalf("seed company before rollback: %v", err)
	}

	if err := postgres.MigrateDown(ctx, pool, countUpMigrations(t)-1); err != nil {
		t.Fatalf("roll back to the oldest supported baseline schema: %v", err)
	}
	if err := postgres.ValidateSchemaHealth(ctx, pool); err == nil {
		t.Fatal("baseline schema unexpectedly passed current schema readiness check")
	}

	if err := postgres.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("upgrade from the baseline schema: %v", err)
	}

	var stored masterdata.Company
	if err := pool.QueryRow(ctx,
		`SELECT id, code, legal_name FROM companies WHERE id=$1`, companyID).
		Scan(&stored.ID, &stored.Code, &stored.LegalName); err != nil {
		t.Fatalf("company row lost across the baseline-to-current upgrade: %v", err)
	}
	if stored.ID != companyID || stored.LegalName != "Upgrade Preservation Co" {
		t.Fatalf("company row changed across the baseline-to-current upgrade: %+v", stored)
	}
	if err := postgres.ValidateSchemaHealth(ctx, pool); err != nil {
		t.Fatalf("current schema health after upgrade: %v", err)
	}

	var applied int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied == 0 {
		t.Fatal("no migrations recorded after upgrade")
	}
}
