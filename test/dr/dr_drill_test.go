package drdrill

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/internal/governance"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/shopspring/decimal"
)

// TestDRDrill_FullRecovery is the P12-01 acceptance drill. It closes the
// NO-GO condition of Release Notes v0.1.0 by proving that a real pg_dump
// backup of a production-like accounting database restores into a brand-new
// empty database, passes the accounting integrity suite, and lets the engine
// resume posting, idempotent reversal, and the annual close on the restored
// data.
func TestDRDrill_FullRecovery(t *testing.T) {
	ctx := context.Background()
	env := prepareDrillEnvironment(t, ctx)

	companies := provisionProductionLikeSource(t, ctx, env)
	snapshot := captureSnapshot(t, ctx, env.sourcePool)

	t.Run("01_backup_pg_dump", func(t *testing.T) { runBackup(t, ctx, env) })
	t.Run("02_restore_pg_restore", func(t *testing.T) { runRestore(t, ctx, env) })
	t.Run("03_schema_health", func(t *testing.T) { verifySchemaHealth(t, ctx, env, snapshot) })
	t.Run("04_double_entry_balance", func(t *testing.T) { verifyDoubleEntryBalance(t, ctx, env) })
	t.Run("05_balances_match_moves", func(t *testing.T) { verifyBalancesMatchMoves(t, ctx, env, companies) })
	t.Run("06_audit_hash_chain", func(t *testing.T) { verifyAuditHashChain(t, ctx, env) })
	t.Run("07_rls_tenant_isolation", func(t *testing.T) { verifyRLSTenantIsolation(t, ctx, env, companies) })
	t.Run("08_idempotency_keys", func(t *testing.T) { verifyIdempotencyKeys(t, ctx, env) })
	t.Run("09_sequences", func(t *testing.T) { verifySequenceContinuity(t, ctx, env) })
	t.Run("10_snapshot_parity_rpo", func(t *testing.T) { verifySnapshotParity(t, ctx, env, snapshot) })
	t.Run("11_engine_resumption", func(t *testing.T) { verifyEngineResumption(t, ctx, env, companies) })

	restoreDuration := env.restoreEnd.Sub(env.restoreStart)
	t.Logf("DR drill metrics: backup_duration=%s backup_bytes=%d restore_duration=%s "+
		"rto_restore_to_verified=%s rpo=0", env.backupDuration.Round(time.Millisecond),
		env.backupSize, restoreDuration.Round(time.Millisecond),
		time.Since(env.restoreStart).Round(time.Millisecond))
}

// provisionProductionLikeSource builds the representative source database:
// two tenants with revenue, expense, settlement, adjusting and reversed
// entries, completed annual closing with opening entries, and the hashed
// audit trail written during those operations.
func provisionProductionLikeSource(t *testing.T, ctx context.Context, env *drillEnvironment) []*drillCompany {
	t.Helper()
	companies := make([]*drillCompany, 0, len(companyAmountsSet))
	for index := range companyAmountsSet {
		company := provisionCompany(t, ctx, env.sourceDrive, index)
		companies = append(companies, company)
	}
	for _, company := range companies {
		runYearEndClose(t, ctx, env.sourceDrive, company)
	}
	return companies
}

func runBackup(t *testing.T, ctx context.Context, env *drillEnvironment) {
	t.Helper()
	backupDir := t.TempDir()
	backupPath := filepath.Join(backupDir, "dr_drill_backup.dump")
	env.backupPath = backupPath

	start := time.Now()
	cmd := exec.CommandContext(ctx, "pg_dump",
		"--format=custom",
		"--file="+backupPath,
		"--dbname="+env.sourceDSN)
	output, err := cmd.CombinedOutput()
	env.backupDuration = time.Since(start)
	if err != nil {
		t.Fatalf("pg_dump failed: %v\n%s", err, output)
	}
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("stat backup file: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("pg_dump produced an empty backup file")
	}
	env.backupSize = info.Size()
	t.Logf("pg_dump produced %s (%d bytes) in %s", backupPath, info.Size(),
		env.backupDuration.Round(time.Millisecond))
}

func runRestore(t *testing.T, ctx context.Context, env *drillEnvironment) {
	t.Helper()
	if err := createDrillDatabase(ctx, env.adminPool, env.targetDB); err != nil {
		t.Fatalf("create pristine target database: %v", err)
	}
	env.restoreStart = time.Now()
	cmd := exec.CommandContext(ctx, "pg_restore",
		"--exit-on-error",
		"--dbname="+env.targetDSN,
		env.backupPath)
	output, err := cmd.CombinedOutput()
	env.restoreEnd = time.Now()
	env.restoreOut = string(output)
	if err != nil {
		t.Fatalf("pg_restore failed: %v\n%s", err, output)
	}
	if env.restoreOut != "" {
		t.Logf("pg_restore output: %s", env.restoreOut)
	}
	t.Logf("pg_restore completed in %s",
		env.restoreEnd.Sub(env.restoreStart).Round(time.Millisecond))
}

// restoredTargetPool connects to the restored database without mutating it.
func restoredTargetPool(t *testing.T, ctx context.Context, env *drillEnvironment) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, env.targetDSN)
	if err != nil {
		t.Fatalf("connect restored database: %v", err)
	}
	return pool
}

// verifySchemaHealth proves the restored schema is complete, recognized, and
// checksum-identical to the committed migration chain: the restored database
// needs no re-migration before serving traffic.
func verifySchemaHealth(t *testing.T, ctx context.Context, env *drillEnvironment, snapshot drillSnapshot) {
	t.Helper()
	pool := restoredTargetPool(t, ctx, env)
	defer pool.Close()

	driver := postgres.NewWithPool(pool)
	if err := driver.ValidateSchema(ctx); err != nil {
		t.Fatalf("restored schema failed health validation: %v", err)
	}
	var restoredMigrations int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&restoredMigrations); err != nil {
		t.Fatalf("count restored migrations: %v", err)
	}
	if restoredMigrations != snapshot.MigrationCount {
		t.Fatalf("migration chain: source %d entries, restored %d entries",
			snapshot.MigrationCount, restoredMigrations)
	}
	t.Logf("restored schema passed ValidateSchema with %d applied migrations; no re-migration required",
		restoredMigrations)
}

// verifyDoubleEntryBalance is integrity check 1: every restored move (posted,
// reversed, closing, opening) balances Σdebit = Σcredit in functional minor
// units, and every company totals balance as well.
func verifyDoubleEntryBalance(t *testing.T, ctx context.Context, env *drillEnvironment) {
	t.Helper()
	pool := restoredTargetPool(t, ctx, env)
	defer pool.Close()

	var unbalanced int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT m.company_id, m.id
			FROM account_moves m
			JOIN account_move_lines l ON l.company_id = m.company_id AND l.move_id = m.id
			WHERE m.status IN ('posted', 'reversed', 'draft')
			GROUP BY m.company_id, m.id
			HAVING sum(l.functional_debit_minor) <> sum(l.functional_credit_minor)
		) unbalanced`).Scan(&unbalanced); err != nil {
		t.Fatalf("count unbalanced restored moves: %v", err)
	}
	if unbalanced != 0 {
		t.Fatalf("found %d unbalanced journal entries in the restored database", unbalanced)
	}

	var unbalancedCompanies int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT company_id
			FROM account_move_lines
			GROUP BY company_id
			HAVING sum(functional_debit_minor) <> sum(functional_credit_minor)
		) unbalanced`).Scan(&unbalancedCompanies); err != nil {
		t.Fatalf("count unbalanced companies: %v", err)
	}
	if unbalancedCompanies != 0 {
		t.Fatalf("found %d companies whose restored totals do not balance", unbalancedCompanies)
	}
	t.Log("all restored moves balance: Σfunctional debit = Σfunctional credit per entry and per company")
}

// verifyBalancesMatchMoves is integrity check 2: the materialized perpetual
// account_balances match the sum of posted move lines for every company and
// period, using the engine's own reconciliation routine.
func verifyBalancesMatchMoves(
	t *testing.T, ctx context.Context, env *drillEnvironment, companies []*drillCompany,
) {
	t.Helper()
	pool := restoredTargetPool(t, ctx, env)
	defer pool.Close()
	driver := postgres.NewWithPool(pool)

	periods, err := pool.Query(ctx, `
		SELECT DISTINCT m.company_id, m.period_id
		FROM account_moves m WHERE m.period_id IS NOT NULL ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("list restored periods: %v", err)
	}
	type companyPeriod struct {
		companyID, periodID uuid.UUID
	}
	var companyPeriods []companyPeriod
	for periods.Next() {
		var cp companyPeriod
		if err := periods.Scan(&cp.companyID, &cp.periodID); err != nil {
			t.Fatalf("scan restored period: %v", err)
		}
		companyPeriods = append(companyPeriods, cp)
	}
	periods.Close()

	for _, cp := range companyPeriods {
		if err := driver.AssertBalancesMatchMoves(ctx, cp.companyID, cp.periodID, "USD"); err != nil {
			t.Fatalf("restored balances diverge from moves for company %s period %s: %v",
				cp.companyID, cp.periodID, err)
		}
	}
	t.Logf("account_balances reconcile with posted moves for %d company/period combinations on the restored database",
		len(companyPeriods))
}

// verifyAuditHashChain is integrity check 3: the tamper-evident audit hash
// chain survived the restore with no gaps or recomputed-hash mismatches.
func verifyAuditHashChain(t *testing.T, ctx context.Context, env *drillEnvironment) {
	t.Helper()
	pool := restoredTargetPool(t, ctx, env)
	defer pool.Close()
	driver := postgres.NewWithPool(pool)

	var auditLogs int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM financial_audit_logs").Scan(&auditLogs); err != nil {
		t.Fatalf("count restored audit logs: %v", err)
	}
	if auditLogs == 0 {
		t.Fatal("restored database contains no audit logs to verify")
	}
	verified, err := governance.VerifyAuditIntegrity(ctx, driver)
	if err != nil {
		t.Fatalf("restored audit chain verification failed: %v", err)
	}
	if !verified {
		t.Fatal("restored audit chain is broken")
	}
	t.Logf("audit hash chain verified across %d restored audit logs", auditLogs)
}

// verifyRLSTenantIsolation is integrity check 4: after the restore, Row-Level
// Security still prevents any cross-company access. The probe role is a
// NOBYPASSRLS non-superuser, so the policies actually apply to it.
func verifyRLSTenantIsolation(
	t *testing.T, ctx context.Context, env *drillEnvironment, companies []*drillCompany,
) {
	t.Helper()
	if len(companies) < 2 {
		t.Fatal("RLS isolation check requires at least two provisioned companies")
	}
	pool := restoredTargetPool(t, ctx, env)
	defer pool.Close()
	companyA, companyB := companies[0], companies[1]

	// A tenant-scoped policy table must show only the rows of the context
	// company and hide the other tenant completely.
	for _, table := range []string{"posting_idempotency", "account_balances"} {
		visibleToA := countUnderRLSContext(t, ctx, pool, companyA.companyID, table, "company_id = $1", companyA.companyID.String())
		visibleToB := countUnderRLSContext(t, ctx, pool, companyB.companyID, table, "company_id = $1", companyB.companyID.String())
		crossView := countUnderRLSContext(t, ctx, pool, companyB.companyID, table, "company_id = $1", companyA.companyID.String())
		noContext := countUnderRLSContext(t, ctx, pool, companyA.companyID, table, "company_id = $1", "")
		t.Logf("%s under RLS: A sees %d rows, B sees %d rows, A sees %d of B's rows, no-context sees %d rows",
			table, visibleToA, visibleToB, crossView, noContext)
		if crossView != 0 {
			t.Fatalf("RLS leak: company A can observe %d rows of company B in %s", crossView, table)
		}
		if noContext != 0 {
			t.Fatalf("RLS leak: %d rows visible in %s without a company context", noContext, table)
		}
	}

	// The core ledger tables carry FORCE ROW LEVEL SECURITY with no permissive
	// policy: the probe role must see zero rows even under a company context.
	coreTables := []string{"account_moves", "account_move_lines", "financial_audit_logs", "journals"}
	for _, table := range coreTables {
		visible := countUnderRLSContext(t, ctx, pool, companyA.companyID, table, "company_id = $1", companyA.companyID.String())
		if visible != 0 {
			t.Fatalf("RLS leak: probe role observed %d rows in core table %s", visible, table)
		}
	}
	t.Log("core ledger tables (account_moves, account_move_lines, financial_audit_logs, journals) deny every " +
		"non-privileged role; ledger access stays behind the service account")

	// Cross-tenant writes must be filtered to zero affected rows.
	if err := grantProbeAccess(ctx, pool); err != nil {
		t.Fatalf("grant probe privileges on restored database: %v", err)
	}
	var originalName string
	if err := pool.QueryRow(ctx,
		"SELECT legal_name FROM companies WHERE id = $1", companyB.companyID).Scan(&originalName); err != nil {
		t.Fatalf("read company B legal name: %v", err)
	}
	if affected := filteredUpdateUnderRLSContext(t, ctx, pool, companyB.companyID); affected != 0 {
		t.Fatalf("cross-tenant UPDATE touched %d rows of company B while scoped to company A", affected)
	}
	var afterName string
	if err := pool.QueryRow(ctx,
		"SELECT legal_name FROM companies WHERE id = $1", companyB.companyID).Scan(&afterName); err != nil {
		t.Fatalf("re-read company B legal name: %v", err)
	}
	if afterName != originalName {
		t.Fatalf("cross-tenant UPDATE mutated company B data: %q -> %q", originalName, afterName)
	}
	t.Log("cross-tenant read and write attempts are fully filtered by restored RLS policies")
}

// verifyIdempotencyKeys is integrity check 5: no idempotency key was lost and
// every key keeps its recorded operation status after the restore.
func verifyIdempotencyKeys(t *testing.T, ctx context.Context, env *drillEnvironment) {
	t.Helper()
	pool := restoredTargetPool(t, ctx, env)
	defer pool.Close()

	var keys int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM posting_idempotency").Scan(&keys); err != nil {
		t.Fatalf("count restored idempotency keys: %v", err)
	}
	if keys == 0 {
		t.Fatal("restored database lost all posting idempotency keys")
	}
	var duplicates int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT company_id, idempotency_key FROM posting_idempotency
			GROUP BY company_id, idempotency_key HAVING count(*) > 1
		) duplicated`).Scan(&duplicates); err != nil {
		t.Fatalf("check idempotency key uniqueness: %v", err)
	}
	if duplicates != 0 {
		t.Fatalf("found %d duplicated idempotency keys after restore", duplicates)
	}
	var completed, inFlight int64
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM posting_idempotency WHERE status = 'completed'").Scan(&completed); err != nil {
		t.Fatalf("count completed idempotency rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM posting_idempotency WHERE status IN ('started', 'retryable_failure')`).Scan(&inFlight); err != nil {
		t.Fatalf("count in-flight idempotency rows: %v", err)
	}
	t.Logf("restored database holds %d idempotency keys (%d completed, %d in-flight); all preserved and unique",
		keys, completed, inFlight)
}

// verifySequenceContinuity is integrity check 6: journal sequence counters and
// the highest issued entry numbers survived the restore, so resumed posting
// cannot reuse or duplicate numbers.
func verifySequenceContinuity(t *testing.T, ctx context.Context, env *drillEnvironment) {
	t.Helper()
	pool := restoredTargetPool(t, ctx, env)
	defer pool.Close()

	rows, err := pool.Query(ctx, `
		SELECT j.company_id::text, j.current_sequence, max(m.entry_number)
		FROM journals j
		LEFT JOIN account_moves m ON m.company_id = j.company_id AND m.journal_id = j.id AND m.entry_number > 0
		GROUP BY j.company_id, j.current_sequence ORDER BY 1`)
	if err != nil {
		t.Fatalf("read restored sequences: %v", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var company string
		var sequence, maxNumber int64
		if err := rows.Scan(&company, &sequence, &maxNumber); err != nil {
			t.Fatalf("scan restored sequence row: %v", err)
		}
		if maxNumber > sequence {
			t.Fatalf("sequence inconsistency for company %s: current_sequence %d < max entry_number %d",
				company, sequence, maxNumber)
		}
		count++
	}
	if count == 0 {
		t.Fatal("no journals found in the restored database")
	}
	t.Logf("journal sequence counters verified for %d companies: next posting number continues after the backup",
		count)
}

// verifySnapshotParity re-captures the fingerprint on the restored database
// and compares it with the pre-backup fingerprint: this is the RPO = 0
// evidence for the logical data set.
func verifySnapshotParity(
	t *testing.T, ctx context.Context, env *drillEnvironment, source drillSnapshot,
) {
	t.Helper()
	pool := restoredTargetPool(t, ctx, env)
	defer pool.Close()
	restored := captureSnapshot(t, ctx, pool)
	assertSnapshotsEqual(t, source, restored)
	t.Log("restored database fingerprint matches the pre-backup fingerprint exactly: RPO = 0")
}

// verifyEngineResumption proves the engine fully resumes on the restored
// database: reading restored history, posting a brand-new draft, continuing
// the sequence, replaying an idempotent reversal, generating a balanced trial
// balance, and executing another annual close.
func verifyEngineResumption(
	t *testing.T, ctx context.Context, env *drillEnvironment, companies []*drillCompany,
) {
	t.Helper()
	pool := restoredTargetPool(t, ctx, env)
	defer pool.Close()
	driver := postgres.NewWithPool(pool)
	appService := buildDrillAppService(t, driver)
	engine := buildDrillEngine(t, driver)
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close resumed engine: %v", err)
		}
	}()

	company := companies[0]

	// 1. Historical read path: a restored entry is fully readable.
	historical, err := appService.GetEntry(ctx, publicapplication.GetEntryQuery{
		Context: drillPrincipal(company), CompanyID: types.CompanyID(company.companyID),
		EntryID: types.JournalEntryID(company.firstEntry.ID),
	})
	if err != nil {
		t.Fatalf("read restored historical entry: %v", err)
	}
	if historical.Status != ledger.EntryPosted {
		t.Fatalf("restored historical entry status = %q, want posted", historical.Status)
	}

	// 2. New draft on the restored database, in the first period of FY2027.
	date := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)
	amount := "111.00"
	draft, err := appService.CreateDraft(ctx, publicapplication.CreateDraftCommand{
		Context:             drillPrincipal(company),
		CompanyID:           types.CompanyID(company.companyID),
		JournalID:           types.JournalID(company.journalID),
		FiscalPeriodID:      types.FiscalPeriodID(company.nextYear.Periods[0].ID),
		EffectiveDate:       date,
		PostingDate:         date,
		Description:         "DR-07 post-restore revenue receipt",
		SourceModule:        "dr-drill-resumption",
		TransactionCurrency: "USD",
		ExchangeRate:        "1",
		Classification:      ledger.EntryClassRegular,
		Lines:               balancedLines(t, company.cash, company.revenue, amount),
	})
	if err != nil {
		t.Fatalf("create draft on restored database: %v", err)
	}
	if _, err := appService.Post(ctx, postCommandFor(company, draft.Entry.ID)); err != nil {
		t.Fatalf("post draft on restored database: %v", err)
	}
	if draft.Entry.SequenceNumber != company.maxSequence+1 {
		t.Fatalf("post-restore sequence number = %d, want %d (continuation of the pre-backup sequence)",
			draft.Entry.SequenceNumber, company.maxSequence+1)
	}
	company.maxSequence = draft.Entry.SequenceNumber

	// 3. Idempotent reversal replays identically on the restored database.
	reversalKey := "dr-drill-resume-reversal"
	reversed, err := appService.Reverse(ctx, reverseCommandFor(company, draft.Entry.ID, reversalKey,
		"Approved post-restore correction"))
	if err != nil {
		t.Fatalf("reverse post-restore entry: %v", err)
	}
	replayed, err := appService.Reverse(ctx, reverseCommandFor(company, draft.Entry.ID, reversalKey,
		"Approved post-restore correction"))
	if err != nil {
		t.Fatalf("replay idempotent reversal: %v", err)
	}
	if !replayed.IdempotentReplay {
		t.Fatal("idempotency replay flag not raised for repeated reversal on the restored database")
	}
	if replayed.Entry.ID != reversed.Entry.ID {
		t.Fatalf("idempotent replay returned entry %s, want the original reversal %s",
			replayed.Entry.ID, reversed.Entry.ID)
	}
	company.maxSequence++ // the reversal consumed one sequence number

	// 4. Reporting resumes on the restored database.
	trialBalance, err := engine.Reporting().GetTrialBalance(ctx,
		types.CompanyID(company.companyID), time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC), "USD")
	if err != nil {
		t.Fatalf("generate trial balance on restored database: %v", err)
	}
	if !trialBalance.IsBalanced {
		t.Fatalf("post-restore trial balance is not balanced: total debit %s, total credit %s",
			trialBalance.TotalDebit.Amount(), trialBalance.TotalCredit.Amount())
	}
	if !trialBalance.TotalDebit.Amount().Equal(trialBalance.TotalCredit.Amount()) {
		t.Fatalf("trial balance totals differ: %s vs %s",
			trialBalance.TotalDebit.Amount(), trialBalance.TotalCredit.Amount())
	}

	// 5. Another annual close runs on the restored database (no opening entry
	// because no successor year is supplied).
	for _, period := range company.nextYear.Periods[:12] {
		if err := driver.TransitionPeriodStateContext(ctx, company.companyID, period.ID,
			masterdata.PeriodSoftClosed, uuid.New(), "DR drill FY2027 soft close"); err != nil {
			t.Fatalf("soft close FY2027 period %d: %v", period.Number, err)
		}
		if err := driver.TransitionPeriodStateContext(ctx, company.companyID, period.ID,
			masterdata.PeriodHardClosed, uuid.New(), "DR drill FY2027 hard close"); err != nil {
			t.Fatalf("hard close FY2027 period %d: %v", period.Number, err)
		}
	}
	closer, err := sdk.YearEndClosing(engine)
	if err != nil {
		t.Fatalf("year-end closing capability on restored database: %v", err)
	}
	command := closeCommandFor(company, false)
	command.FiscalYearID = types.FiscalYearID(company.nextYear.ID)
	command.NextFiscalYearID = types.FiscalYearID(uuid.Nil)
	result, err := closer.ExecuteYearEndClosing(ctx, command)
	if err != nil {
		t.Fatalf("execute FY2027 close on restored database: %v", err)
	}
	if len(result.ClosingEntryIDs) == 0 {
		t.Fatalf("FY2027 close produced no closing entries: %+v", result)
	}
	if result.OpeningEntryID != (types.JournalEntryID{}) {
		t.Fatalf("FY2027 close unexpectedly generated an opening entry %s", result.OpeningEntryID)
	}
	expectedNet := decimal.RequireFromString(amount)
	actualNet, err := decimal.NewFromString(result.NetIncome)
	if err != nil || !actualNet.Equal(expectedNet) {
		t.Fatalf("FY2027 net income = %q, want %s", result.NetIncome, expectedNet)
	}

	closedYear, err := driver.GetFiscalYearForCompanyContext(ctx, company.companyID, company.nextYear.ID)
	if err != nil {
		t.Fatalf("reload FY2027 after close: %v", err)
	}
	if closedYear.State != masterdata.FiscalYearHardClosed {
		t.Fatalf("FY2027 state = %s, want hard-closed after post-restore close", closedYear.State)
	}

	// 6. The ledger still balances after all post-restore activity.
	if err := driver.AssertBalancesMatchMoves(ctx, company.companyID, company.nextYear.Periods[0].ID, "USD"); err != nil {
		t.Fatalf("balances diverged from moves after post-restore activity: %v", err)
	}
	t.Log("engine fully resumed on the restored database: read, draft, post, sequence continuation, " +
		"idempotent reversal, trial balance, and annual close all succeeded")
}
