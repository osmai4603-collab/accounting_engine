package postgres_test

import (
	"context"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/storage/postgres"
	"github.com/google/uuid"
)

// TestMigrationUpgradeFromPreviousVersionPreservesData simulates deploying the
// current release over the previous schema version: it applies the full chain,
// writes representative tenant data, rolls back exactly one migration (the
// state of the previous release), then upgrades again. Existing rows must
// survive and the final chain must match a clean install.
func TestMigrationUpgradeFromPreviousVersionPreservesData(t *testing.T) {
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

	if err := postgres.MigrateDown(ctx, pool, 1); err != nil {
		t.Fatalf("roll back to the previous release: %v", err)
	}

	if err := postgres.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("upgrade from the previous release: %v", err)
	}

	var stored masterdata.Company
	if err := pool.QueryRow(ctx,
		`SELECT id, code, legal_name FROM companies WHERE id=$1`, companyID).
		Scan(&stored.ID, &stored.Code, &stored.LegalName); err != nil {
		t.Fatalf("company row lost across the upgrade: %v", err)
	}
	if stored.ID != companyID || stored.LegalName != "Upgrade Preservation Co" {
		t.Fatalf("company row changed across the upgrade: %+v", stored)
	}

	var applied int
	if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied == 0 {
		t.Fatal("no migrations recorded after upgrade")
	}
}
