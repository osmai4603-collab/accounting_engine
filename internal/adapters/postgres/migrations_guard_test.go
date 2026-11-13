package postgres

import (
	"regexp"
	"strings"
	"testing"
)

// grandfatheredDangerousMigrations lists forward migrations that contain a
// destructive or type-changing statement approved before the migration policy
// took effect. New migrations must not be added here without an ADR and a
// reviewed Expand/Contract plan; see docs/migration_policy.md.
var grandfatheredDangerousMigrations = map[string]string{
	"202611110005_audit_payload_bytes": "approved one-time jsonb->bytea payload conversion (see docs/migration_policy.md)",
}

var (
	dangerousMigrationPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bdrop\s+column\b`),
		regexp.MustCompile(`(?i)\bdrop\s+table\b`),
		regexp.MustCompile(`(?i)\balter\s+column\s+\w+\s+type\b`),
		regexp.MustCompile(`(?i)\bdelete\s+from\b`),
		regexp.MustCompile(`(?im)^\s*truncate\s+[a-z_"` + "`" + `]`),
	}
	migrationNamePattern = regexp.MustCompile(`^[0-9]{12}_[a-z0-9_]+$`)
)

// TestMigrationsHaveReversiblePairs enforces the first rule of the migration
// policy: every forward migration ships with a reviewed down migration so a
// release can always be rolled back.
func TestMigrationsHaveReversiblePairs(t *testing.T) {
	migrations, err := listMigrations("migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations found")
	}
	for _, migration := range migrations {
		if !migrationNamePattern.MatchString(migration.name) {
			t.Errorf("migration %q does not match <12-digit version>_<snake_case_name>", migration.name)
		}
		if migration.downPath == "" {
			t.Errorf("migration %s has no .down.sql counterpart", migration.name)
			continue
		}
		content, err := embeddedMigrations.ReadFile(migration.downPath)
		if err != nil {
			t.Errorf("read down migration %s: %v", migration.name, err)
			continue
		}
		if strings.TrimSpace(string(content)) == "" {
			t.Errorf("down migration %s is empty", migration.downPath)
		}
	}
}

// TestDangerousMigrationsRequireApproval rejects destructive DDL in forward
// migrations unless the migration is explicitly grandfathered with a reason.
func TestDangerousMigrationsRequireApproval(t *testing.T) {
	migrations, err := listMigrations("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations {
		if migration.name == "202611010001_baseline_schema" {
			// The bootstrap schema is the documented starting point and has no
			// prior data to protect.
			continue
		}
		content, err := embeddedMigrations.ReadFile(migration.upPath)
		if err != nil {
			t.Fatalf("read migration %s: %v", migration.name, err)
		}
		if _, approved := grandfatheredDangerousMigrations[migration.name]; approved {
			continue
		}
		for _, pattern := range dangerousMigrationPatterns {
			if match := pattern.FindString(string(content)); match != "" {
				t.Errorf("migration %s contains dangerous statement %q; use Expand/Contract or document approval in grandfatheredDangerousMigrations",
					migration.name, strings.TrimSpace(match))
			}
		}
	}
}
