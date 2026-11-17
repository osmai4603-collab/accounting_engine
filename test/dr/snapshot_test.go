package drdrill

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// drillSnapshot is the exact quantitative fingerprint of the source database
// captured immediately before pg_dump. After the restore it is re-captured on
// the target database; equality proves RPO = 0 for the logical data set.
type drillSnapshot struct {
	MigrationCount   int64
	Moves            map[string]map[string]int64  // company -> status -> count
	Lines            map[string]int64             // company -> line count
	AuditLogs        map[string]int64             // company -> audit log count
	IdempotencyKeys  map[string]map[string]string // company -> key -> status
	ReversalKeys     map[string]map[string]string // company -> reversal idempotency key -> payload hash
	JournalSequence  map[string]int64             // company -> current_sequence
	MaxEntryNumbers  map[string]int64             // company -> max entry_number
	FunctionalTotals map[string][2]string         // company -> [Σdebit, Σcredit] minor
	BalanceRows      map[string]int64             // company -> account_balances rows
	FiscalYears      map[string]string            // company/year name -> state
	CapturedAt       time.Time
}

func mapKeyUUID(id uuid.UUID) string { return id.String() }

// captureSnapshot reads the full quantitative fingerprint of a drill database.
// The drill role is a PostgreSQL superuser and therefore observes every row.
func captureSnapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool) drillSnapshot {
	t.Helper()
	snapshot := drillSnapshot{
		Moves:            map[string]map[string]int64{},
		Lines:            map[string]int64{},
		AuditLogs:        map[string]int64{},
		IdempotencyKeys:  map[string]map[string]string{},
		ReversalKeys:     map[string]map[string]string{},
		JournalSequence:  map[string]int64{},
		MaxEntryNumbers:  map[string]int64{},
		FunctionalTotals: map[string][2]string{},
		BalanceRows:      map[string]int64{},
		FiscalYears:      map[string]string{},
		CapturedAt:       time.Now().UTC(),
	}

	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&snapshot.MigrationCount); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}

	// Moves grouped by company and status.
	rows, err := pool.Query(ctx, `
		SELECT company_id::text, status, count(*)
		FROM account_moves GROUP BY 1, 2 ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("snapshot account_moves: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var company, status string
		var count int64
		if err := rows.Scan(&company, &status, &count); err != nil {
			t.Fatalf("scan account_moves snapshot: %v", err)
		}
		if snapshot.Moves[company] == nil {
			snapshot.Moves[company] = map[string]int64{}
		}
		snapshot.Moves[company][status] = count
	}
	rows.Close()

	scanIntoMap := func(query string, target map[string]int64, label string) {
		t.Helper()
		rows, err := pool.Query(ctx, query)
		if err != nil {
			t.Fatalf("snapshot %s: %v", label, err)
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			var value int64
			if err := rows.Scan(&key, &value); err != nil {
				t.Fatalf("scan %s snapshot: %v", label, err)
			}
			target[key] = value
		}
	}

	scanIntoMap(`SELECT company_id::text, count(*) FROM account_move_lines GROUP BY 1`, snapshot.Lines, "move lines")
	scanIntoMap(`SELECT company_id::text, count(*) FROM financial_audit_logs GROUP BY 1`, snapshot.AuditLogs, "audit logs")
	scanIntoMap(`SELECT company_id::text, count(*) FROM account_balances GROUP BY 1`, snapshot.BalanceRows, "account balances")

	journalRows, err := pool.Query(ctx, `SELECT company_id::text, current_sequence FROM journals`)
	if err != nil {
		t.Fatalf("snapshot journals: %v", err)
	}
	for journalRows.Next() {
		var company string
		var sequence int64
		if err := journalRows.Scan(&company, &sequence); err != nil {
			t.Fatalf("scan journals snapshot: %v", err)
		}
		snapshot.JournalSequence[company] = sequence
	}
	journalRows.Close()

	numberRows, err := pool.Query(ctx, `
		SELECT company_id::text, max(entry_number) FROM account_moves
		WHERE entry_number > 0 GROUP BY 1`)
	if err != nil {
		t.Fatalf("snapshot entry numbers: %v", err)
	}
	for numberRows.Next() {
		var company string
		var maximum int64
		if err := numberRows.Scan(&company, &maximum); err != nil {
			t.Fatalf("scan entry number snapshot: %v", err)
		}
		snapshot.MaxEntryNumbers[company] = maximum
	}
	numberRows.Close()

	totalRows, err := pool.Query(ctx, `
		SELECT company_id::text, sum(functional_debit_minor)::text, sum(functional_credit_minor)::text
		FROM account_move_lines GROUP BY 1`)
	if err != nil {
		t.Fatalf("snapshot functional totals: %v", err)
	}
	for totalRows.Next() {
		var company, debit, credit string
		if err := totalRows.Scan(&company, &debit, &credit); err != nil {
			t.Fatalf("scan functional total snapshot: %v", err)
		}
		snapshot.FunctionalTotals[company] = [2]string{debit, credit}
	}
	totalRows.Close()

	keyRows, err := pool.Query(ctx, `
		SELECT company_id::text, idempotency_key, status FROM posting_idempotency ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("snapshot idempotency keys: %v", err)
	}
	for keyRows.Next() {
		var company, key, status string
		if err := keyRows.Scan(&company, &key, &status); err != nil {
			t.Fatalf("scan idempotency snapshot: %v", err)
		}
		if snapshot.IdempotencyKeys[company] == nil {
			snapshot.IdempotencyKeys[company] = map[string]string{}
		}
		snapshot.IdempotencyKeys[company][key] = status
	}
	keyRows.Close()

	// Reversal idempotency keys live inside the reversing move payload.
	reversalRows, err := pool.Query(ctx, `
		SELECT company_id::text, entry->>'idempotency_key', entry->>'idempotency_payload_hash'
		FROM account_moves
		WHERE classification = 'reversing' AND entry->>'idempotency_key' <> ''
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("snapshot reversal idempotency keys: %v", err)
	}
	for reversalRows.Next() {
		var company, key, payloadHash string
		if err := reversalRows.Scan(&company, &key, &payloadHash); err != nil {
			t.Fatalf("scan reversal idempotency snapshot: %v", err)
		}
		if snapshot.ReversalKeys[company] == nil {
			snapshot.ReversalKeys[company] = map[string]string{}
		}
		snapshot.ReversalKeys[company][key] = payloadHash
	}
	reversalRows.Close()

	yearRows, err := pool.Query(ctx, `
		SELECT companies.code, fiscal_years.code, fiscal_years.state
		FROM fiscal_years JOIN companies ON companies.id = fiscal_years.company_id
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("snapshot fiscal years: %v", err)
	}
	for yearRows.Next() {
		var code, yearName, state string
		if err := yearRows.Scan(&code, &yearName, &state); err != nil {
			t.Fatalf("scan fiscal year snapshot: %v", err)
		}
		snapshot.FiscalYears[code+"/"+yearName] = state
	}
	yearRows.Close()
	return snapshot
}

// assertSnapshotsEqual compares the restored fingerprint with the source one.
func assertSnapshotsEqual(t *testing.T, source, restored drillSnapshot) {
	t.Helper()
	if source.MigrationCount != restored.MigrationCount {
		t.Fatalf("schema_migrations: source %d, restored %d", source.MigrationCount, restored.MigrationCount)
	}
	assertIntMapEqual(t, "account_move_lines", source.Lines, restored.Lines)
	assertIntMapEqual(t, "financial_audit_logs", source.AuditLogs, restored.AuditLogs)
	assertIntMapEqual(t, "account_balances", source.BalanceRows, restored.BalanceRows)
	assertIntMapEqual(t, "journals.current_sequence", source.JournalSequence, restored.JournalSequence)
	assertIntMapEqual(t, "max entry_number", source.MaxEntryNumbers, restored.MaxEntryNumbers)
	for company, totals := range source.FunctionalTotals {
		restoredTotals, ok := restored.FunctionalTotals[company]
		if !ok {
			t.Fatalf("restored database lost all move lines for company %s", company)
		}
		if restoredTotals != totals {
			t.Fatalf("functional totals for company %s: source %v, restored %v", company, totals, restoredTotals)
		}
	}
	for company, statuses := range source.Moves {
		restoredStatuses, ok := restored.Moves[company]
		if !ok {
			t.Fatalf("restored database lost account_moves for company %s", company)
		}
		for status, count := range statuses {
			if restoredStatuses[status] != count {
				t.Fatalf("moves %s/%s: source %d, restored %d", company, status, count, restoredStatuses[status])
			}
		}
	}
	for company, keys := range source.IdempotencyKeys {
		restoredKeys, ok := restored.IdempotencyKeys[company]
		if !ok || len(restoredKeys) != len(keys) {
			t.Fatalf("idempotency keys for company %s: source %d keys, restored %d",
				company, len(keys), len(restoredKeys))
		}
		for key, status := range keys {
			if restoredKeys[key] != status {
				t.Fatalf("idempotency key %s/%s: source status %q, restored %q", company, key, status, restoredKeys[key])
			}
		}
	}
	for companyYear, state := range source.FiscalYears {
		restoredState, ok := restored.FiscalYears[companyYear]
		if !ok || restoredState != state {
			t.Fatalf("fiscal year %s: source state %q, restored %q", companyYear, state, restoredState)
		}
	}
}

func assertIntMapEqual(t *testing.T, label string, source, restored map[string]int64) {
	t.Helper()
	for key, expected := range source {
		if restored[key] != expected {
			t.Fatalf("%s[%s]: source %d, restored %d", label, key, expected, restored[key])
		}
	}
	for key := range restored {
		if _, ok := source[key]; !ok {
			t.Fatalf("%s[%s] exists in restored database but not in the source snapshot", label, key)
		}
	}
}

// countUnderRLSContext executes a count(*) through a probe session applying
// Row-Level Security. The context value (empty string) is set before the query
// so company-scoped policies can be exercised.
func countUnderRLSContext(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool, companyID uuid.UUID, table, where string, contextValue string,
) int64 {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire probe connection: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET ROLE %s", probeRole)); err != nil {
		t.Fatalf("SET ROLE %s: %v", probeRole, err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "RESET ROLE")
	}()
	if _, err := conn.Exec(ctx,
		"SELECT set_config('app.current_company_id', $1, false)", contextValue); err != nil {
		t.Fatalf("set RLS context: %v", err)
	}
	query := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", table, where)
	var count int64
	if err := conn.QueryRow(ctx, query, companyID).Scan(&count); err != nil {
		t.Fatalf("RLS probe query on %s: %v", table, err)
	}
	return count
}

// filteredUpdateUnderRLSContext runs an UPDATE under the probe role and returns
// how many rows it actually touched; cross-tenant writes must touch zero rows.
func filteredUpdateUnderRLSContext(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool, contextCompanyID, targetCompanyID uuid.UUID,
) int64 {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire probe connection: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET ROLE %s", probeRole)); err != nil {
		t.Fatalf("SET ROLE %s: %v", probeRole, err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "RESET ROLE")
	}()
	if _, err := conn.Exec(ctx,
		"SELECT set_config('app.current_company_id', $1, false)", contextCompanyID.String()); err != nil {
		t.Fatalf("set RLS context: %v", err)
	}
	tag, err := conn.Exec(ctx,
		`UPDATE companies SET legal_name = legal_name || '-tampered' WHERE id = $1`, targetCompanyID)
	if err != nil {
		t.Fatalf("cross-tenant UPDATE probe: %v", err)
	}
	return tag.RowsAffected()
}
