package postgres

import (
	"path/filepath"
	"testing"
)

func TestEveryMigrationHasRollback(t *testing.T) {
	directory, err := migrationDirectory()
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := listMigrations(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		if migration.downPath == "" {
			t.Errorf("migration %s has no rollback script", filepath.Base(migration.upPath))
		}
		if _, err := embeddedMigrations.ReadFile(migration.upPath); err != nil {
			t.Errorf("embedded up migration %s: %v", migration.upPath, err)
		}
		if _, err := embeddedMigrations.ReadFile(migration.downPath); err != nil {
			t.Errorf("embedded down migration %s: %v", migration.downPath, err)
		}
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations were discovered")
	}
}
