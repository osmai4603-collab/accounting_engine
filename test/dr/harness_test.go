// Package drdrill implements the automated P12-01 disaster-recovery drill:
// a production-like accounting database is provisioned, backed up with
// pg_dump, restored from the backup file into a brand-new empty database,
// verified by the accounting integrity suite, and finally operated by the
// engine to prove full resumption on the restored data.
//
// The drill intentionally shells out to the real pg_dump and pg_restore
// binaries; it never rebuilds tables in-process, because the NO-GO condition
// being closed requires proof that an actual backup file restores cleanly.
package drdrill

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/shopspring/decimal"
)

// defaultAdminDSN is the local administrative DSN used when POSTGRES_URL is
// unset. The drill derives per-database DSNs from it.
const defaultAdminDSN = "postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql"

// probeRole is a dedicated NOBYPASSRLS role used to prove that Row-Level
// Security survives the restore. The drill user (osm) is a superuser and
// always bypasses RLS, so only SET ROLE makes the policies observable.
const probeRole = "dr_drill_probe"

type drillEnvironment struct {
	t *testing.T

	adminPool   *pgxpool.Pool
	adminDSN    string
	sourceDSN   string
	targetDSN   string
	sourceDB    string
	targetDB    string
	sourcePool  *pgxpool.Pool
	sourceDrive *postgres.Driver

	backupPath     string
	backupSize     int64
	backupDuration time.Duration
	restoreStart   time.Time
	restoreEnd     time.Time
	restoreOut     string
	restored       bool
	backupDir      string
}

// baseAdminDSN resolves the administrative connection string: POSTGRES_URL is
// re-pointed at the administrative `postgres` database so the drill can create
// and drop dedicated drill databases.
func baseAdminDSN(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("POSTGRES_URL")
	if raw == "" {
		raw = defaultAdminDSN
	}
	admin, err := withDatabase(raw, "postgres")
	if err != nil {
		t.Fatalf("resolve administrative DSN from %q: %v", raw, err)
	}
	return admin
}

// withDatabase rewrites a PostgreSQL connection string (URI or keyword form)
// to target a different database.
func withDatabase(raw, database string) (string, error) {
	raw = strings.TrimSpace(raw)
	if parsed, err := url.Parse(raw); err == nil &&
		(parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") && parsed.Opaque == "" {
		parsed.Path = "/" + database
		return parsed.String(), nil
	}
	fields := make([]string, 0, 6)
	replaced := false
	for _, field := range strings.Fields(raw) {
		if strings.HasPrefix(field, "dbname=") {
			field = "dbname=" + database
			replaced = true
		}
		fields = append(fields, field)
	}
	if !replaced {
		fields = append(fields, "dbname="+database)
	}
	return strings.Join(fields, " "), nil
}

// drillDSN clones the base DSN for a specific database.
func drillDSN(base, database string) string {
	dsn, err := withDatabase(base, database)
	if err != nil {
		panic(fmt.Sprintf("clone DSN for %q: %v", database, err))
	}
	return dsn
}

// prepareDrillEnvironment builds the isolated source and target databases and
// skips the drill when PostgreSQL or the pg_dump/pg_restore binaries are not
// available. Skipping is never counted as a passing drill.
func prepareDrillEnvironment(t *testing.T, ctx context.Context) *drillEnvironment {
	t.Helper()

	for _, binary := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("drill binary %q not on PATH: %v", binary, err)
		}
	}

	env := &drillEnvironment{t: t}
	env.adminDSN = baseAdminDSN(t)

	backupDir, err := os.MkdirTemp("", "dr_drill_backup_")
	if err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	env.backupDir = backupDir
	t.Cleanup(func() { _ = os.RemoveAll(backupDir) })

	adminPool, err := pgxpool.New(ctx, env.adminDSN)
	if err != nil {
		t.Skipf("administrative PostgreSQL connection unavailable: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Skipf("administrative PostgreSQL ping failed: %v", err)
	}
	env.adminPool = adminPool

	if err := ensureProbeRole(ctx, adminPool); err != nil {
		t.Fatalf("prepare RLS probe role: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	env.sourceDB = "dr_drill_" + suffix + "_src"
	env.targetDB = "dr_drill_" + suffix + "_tgt"
	env.sourceDSN = drillDSN(env.adminDSN, env.sourceDB)
	env.targetDSN = drillDSN(env.adminDSN, env.targetDB)

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		_ = dropDrillDatabase(cleanupCtx, env.adminPool, env.targetDB)
		_ = dropDrillDatabase(cleanupCtx, env.adminPool, env.sourceDB)
		env.adminPool.Close()
	})

	if err := dropDrillDatabase(ctx, adminPool, env.sourceDB); err != nil {
		t.Fatalf("clean pre-existing source database: %v", err)
	}
	if err := dropDrillDatabase(ctx, adminPool, env.targetDB); err != nil {
		t.Fatalf("clean pre-existing target database: %v", err)
	}
	if err := createDrillDatabase(ctx, adminPool, env.sourceDB); err != nil {
		t.Fatalf("create source database: %v", err)
	}

	sourcePool, err := pgxpool.New(ctx, env.sourceDSN)
	if err != nil {
		t.Fatalf("connect source database: %v", err)
	}
	env.sourcePool = sourcePool
	// postgres.New applies the full migration chain and seeds the currency
	// registry, exactly like a fresh production provisioning would.
	sourceDriver, err := postgres.New(ctx, env.sourceDSN)
	if err != nil {
		sourcePool.Close()
		t.Fatalf("initialize source schema: %v", err)
	}
	env.sourceDrive = sourceDriver
	return env
}

func createDrillDatabase(ctx context.Context, adminPool *pgxpool.Pool, name string) error {
	_, err := adminPool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	return err
}

func dropDrillDatabase(ctx context.Context, adminPool *pgxpool.Pool, name string) error {
	_, err := adminPool.Exec(ctx,
		"DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	return err
}

// ensureProbeRole creates (once) the non-superuser role the drill uses to
// observe RLS behaviour on the restored database.
func ensureProbeRole(ctx context.Context, adminPool *pgxpool.Pool) error {
	var exists bool
	if err := adminPool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", probeRole).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err := adminPool.Exec(ctx,
		fmt.Sprintf("CREATE ROLE %s NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS", probeRole))
	return err
}

// grantProbeAccess performs the runbook-mandated post-restore privilege
// restoration step for the probe role (dump files carry no grants for it).
func grantProbeAccess(ctx context.Context, pool *pgxpool.Pool) error {
	statements := []string{
		"GRANT USAGE ON SCHEMA public TO " + probeRole,
		"GRANT SELECT, UPDATE ON ALL TABLES IN SCHEMA public TO " + probeRole,
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("%q: %w", statement, err)
		}
	}
	return nil
}

// drillCompany holds the master-data and ledger identifiers of one provisioned
// production-like tenant.
type drillCompany struct {
	name        string
	companyID   uuid.UUID
	journalID   uuid.UUID
	cash        uuid.UUID
	receivable  uuid.UUID
	payable     uuid.UUID
	retained    uuid.UUID
	summary     uuid.UUID
	revenue     uuid.UUID
	expense     uuid.UUID
	year        masterdata.FiscalYear
	nextYear    masterdata.FiscalYear
	firstEntry  ledger.JournalEntry
	amounts     companyAmounts
	maxSequence int64
}

type companyAmounts struct {
	revenue    string
	expense    string
	accrual    string
	settlement string
	collection string
	reversal   string
}

var companyAmountsSet = []companyAmounts{
	{revenue: "1500.00", expense: "1000.00", accrual: "250.00", settlement: "150.00", collection: "300.00", reversal: "120.00"},
	{revenue: "2800.00", expense: "1900.00", accrual: "320.00", settlement: "210.00", collection: "540.00", reversal: "260.00"},
}

func expectedNetIncome(amounts companyAmounts) decimal.Decimal {
	revenue, _ := decimal.NewFromString(amounts.revenue)
	expense, _ := decimal.NewFromString(amounts.expense)
	accrual, _ := decimal.NewFromString(amounts.accrual)
	return revenue.Sub(expense).Sub(accrual)
}

func drillMoney(t *testing.T, value string) types.Money {
	t.Helper()
	money, err := types.NewMoneyFromString(value, "USD")
	if err != nil {
		t.Fatalf("create money %q: %v", value, err)
	}
	return money
}

func drillLine(t *testing.T, accountID uuid.UUID, debit, credit string) ledger.EntryLine {
	t.Helper()
	debitAmount := drillMoney(t, debit)
	creditAmount := drillMoney(t, credit)
	transactionAmount := debitAmount
	if transactionAmount.IsZero() {
		transactionAmount = creditAmount
	}
	return ledger.EntryLine{
		ID:                  types.NewJournalEntryLineID(),
		AccountID:           types.AccountID(accountID),
		TransactionAmount:   transactionAmount,
		TransactionCurrency: "USD",
		ExchangeRate:        decimal.NewFromInt(1),
		FunctionalDebit:     debitAmount,
		FunctionalCredit:    creditAmount,
	}
}

func balancedLines(t *testing.T, debitAccount, creditAccount uuid.UUID, amount string) []ledger.EntryLine {
	t.Helper()
	return []ledger.EntryLine{
		drillLine(t, debitAccount, amount, "0"),
		drillLine(t, creditAccount, "0", amount),
	}
}

// drillPrincipal is the host-supplied verified identity for drill operations.
func drillPrincipal(company *drillCompany) publicapplication.Context {
	return publicapplication.Context{
		Principal: publicapplication.Principal{
			ActorID:    uuid.NewString(),
			CompanyIDs: []types.CompanyID{types.CompanyID(company.companyID)},
		},
		SourceSystem: "dr-drill",
	}
}

// allowAllAuthorizer mirrors the host-side authorization duty that the engine
// deliberately never implements itself.
type allowAllAuthorizer struct{}

func (allowAllAuthorizer) Authorize(context.Context, publicapplication.AuthorizationRequest) error {
	return nil
}

// buildDrillAppService builds the public application service over a driver.
func buildDrillAppService(t *testing.T, driver *postgres.Driver) publicapplication.Service {
	t.Helper()
	service, err := sdk.NewApplicationServiceWithAuthorizer(driver.Ports(), allowAllAuthorizer{})
	if err != nil {
		t.Fatalf("build application service: %v", err)
	}
	return service
}

// buildDrillEngine builds a full SDK engine (including reporting) on a driver.
func buildDrillEngine(t *testing.T, driver *postgres.Driver) sdk.Engine {
	t.Helper()
	engine, err := sdk.NewBuilder(
		sdk.WithCoreStorage(driver),
		sdk.WithReporting(driver),
	).Build(context.Background())
	if err != nil {
		t.Fatalf("build SDK engine: %v", err)
	}
	return engine
}

func postCommandFor(company *drillCompany, entryID types.JournalEntryID) publicapplication.PostEntryCommand {
	return publicapplication.PostEntryCommand{
		Context: drillPrincipal(company), CompanyID: types.CompanyID(company.companyID), EntryID: entryID,
	}
}

func reverseCommandFor(company *drillCompany, entryID types.JournalEntryID, key, reason string) publicapplication.ReverseEntryCommand {
	return reverseCommandForDate(company, entryID, key, reason,
		time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC))
}

func reverseCommandForDate(
	company *drillCompany, entryID types.JournalEntryID, key, reason string, effectiveDate time.Time,
) publicapplication.ReverseEntryCommand {
	return publicapplication.ReverseEntryCommand{
		Context: drillPrincipal(company), CompanyID: types.CompanyID(company.companyID),
		EntryID: entryID, Reason: reason,
		EffectiveDate:  effectiveDate,
		IdempotencyKey: key,
	}
}

func closeCommandFor(company *drillCompany, withOpening bool) publicapplication.ExecuteYearEndClosingCommand {
	command := publicapplication.ExecuteYearEndClosingCommand{
		Context: drillPrincipal(company), CompanyID: types.CompanyID(company.companyID),
		FiscalYearID:              types.FiscalYearID(company.year.ID),
		JournalID:                 types.JournalID(company.journalID),
		RetainedEarningsAccountID: types.AccountID(company.retained),
		Policy:                    publicapplication.YearEndClosingDirectToRetainedEarnings,
		Currency:                  "USD", Reason: "Approved annual close",
	}
	if withOpening {
		command.NextFiscalYearID = types.FiscalYearID(company.nextYear.ID)
	}
	return command
}

// postDrillEntry drives the public draft → validate → post workflow.
func postDrillEntry(
	t *testing.T, ctx context.Context, appService publicapplication.Service,
	company *drillCompany, date time.Time, description string, lines []ledger.EntryLine,
) ledger.JournalEntry {
	t.Helper()
	draft, err := appService.CreateDraft(ctx, publicapplication.CreateDraftCommand{
		Context:             drillPrincipal(company),
		CompanyID:           types.CompanyID(company.companyID),
		JournalID:           types.JournalID(company.journalID),
		FiscalPeriodID:      types.FiscalPeriodID(company.year.Periods[date.Month()-1].ID),
		EffectiveDate:       date,
		PostingDate:         date,
		Description:         description,
		SourceModule:        "dr-drill",
		TransactionCurrency: "USD",
		ExchangeRate:        "1",
		Classification:      ledger.EntryClassRegular,
		Lines:               lines,
	})
	if err != nil {
		t.Fatalf("create draft %q for %s: %v", description, company.name, err)
	}
	if err := appService.Validate(ctx, publicapplication.ValidateEntryCommand{
		Context: drillPrincipal(company), CompanyID: types.CompanyID(company.companyID), EntryID: draft.Entry.ID,
	}); err != nil {
		t.Fatalf("validate draft %q for %s: %v", description, company.name, err)
	}
	posted, err := appService.Post(ctx, postCommandFor(company, draft.Entry.ID))
	if err != nil {
		t.Fatalf("post draft %q for %s: %v", description, company.name, err)
	}
	if posted.Entry.Status != ledger.EntryPosted {
		t.Fatalf("entry %q status = %q, want posted", description, posted.Entry.Status)
	}
	return posted.Entry
}

// provisionCompany creates one production-like tenant: master data, two fiscal
// years, and a full transaction mix (revenue receipt, expense payment, AR/AP
// settlement, an approved adjusting accrual, and a reversed entry).
func provisionCompany(t *testing.T, ctx context.Context, driver *postgres.Driver, index int) *drillCompany {
	t.Helper()
	name := fmt.Sprintf("DR-CO-%d", index+1)
	amounts := companyAmountsSet[index]

	companyID, journalID := uuid.New(), uuid.New()
	cashID, receivableID, payableID := uuid.New(), uuid.New(), uuid.New()
	retainedID, summaryID, revenueID, expenseID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: name, LegalName: name + " Production-like Co",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatalf("create company %s: %v", name, err)
	}
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO journals(id, company_id, code, name) VALUES($1,$2,$3,$4)`,
			journalID, companyID, "GEN", "General journal")
		return err
	}); err != nil {
		t.Fatalf("create journal for %s: %v", name, err)
	}

	accounts := []masterdata.Account{
		{
			CompanyID: companyID, ID: cashID, Code: "1000", Name: "Cash and equivalents",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
			IsActive: true, IsCashEquivalent: true, CashFlowActivity: masterdata.CashFlowCashEquivalent,
			IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual,
		},
		{
			CompanyID: companyID, ID: receivableID, Code: "1200", Name: "Accounts receivable",
			AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit,
			IsActive: true, CashFlowActivity: masterdata.CashFlowOperating,
			IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual,
		},
		{
			CompanyID: companyID, ID: payableID, Code: "2000", Name: "Accounts payable",
			AccountType: masterdata.AccountTypeLiability, NormalBalance: masterdata.NormalBalanceCredit,
			IsActive: true, CashFlowActivity: masterdata.CashFlowOperating,
			IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual,
		},
		{
			CompanyID: companyID, ID: retainedID, Code: "3300", Name: "Retained earnings",
			AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit,
			IsActive: true, SubClassification: masterdata.SubClassRetainedEarnings,
			CashFlowActivity: masterdata.CashFlowFinancing, IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual,
		},
		{
			CompanyID: companyID, ID: summaryID, Code: "3990", Name: "Income summary",
			AccountType: masterdata.AccountTypeEquity, NormalBalance: masterdata.NormalBalanceCredit,
			IsActive: true, SubClassification: "income_summary",
			CashFlowActivity: masterdata.CashFlowFinancing, IFRSCategory: masterdata.IFRSCategoryBalanceSheetPerpetual,
		},
		{
			CompanyID: companyID, ID: revenueID, Code: "4000", Name: "Operating revenue",
			AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit,
			IsActive: true, SubClassification: masterdata.SubClassOperatingRevenue,
			CashFlowActivity: masterdata.CashFlowOperating, IFRSCategory: masterdata.IFRSCategoryOperating,
		},
		{
			CompanyID: companyID, ID: expenseID, Code: "5000", Name: "Operating expense",
			AccountType: masterdata.AccountTypeExpense, NormalBalance: masterdata.NormalBalanceDebit,
			IsActive: true, SubClassification: masterdata.SubClassCostOfSales,
			CashFlowActivity: masterdata.CashFlowOperating, IFRSCategory: masterdata.IFRSCategoryOperating,
		},
	}
	for _, account := range accounts {
		if err := driver.CreateAccount(ctx, account); err != nil {
			t.Fatalf("create account %s/%s: %v", name, account.Code, err)
		}
	}

	year := masterdata.GenerateFiscalYear(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, year); err != nil {
		t.Fatalf("create FY2026 for %s: %v", name, err)
	}
	nextYear := masterdata.GenerateFiscalYear(time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC))
	nextYear.CompanyID = companyID
	if err := driver.CreateFiscalYear(ctx, nextYear); err != nil {
		t.Fatalf("create FY2027 for %s: %v", name, err)
	}

	company := &drillCompany{
		name: name, companyID: companyID, journalID: journalID,
		cash: cashID, receivable: receivableID, payable: payableID,
		retained: retainedID, summary: summaryID, revenue: revenueID, expense: expenseID,
		year: year, nextYear: nextYear, amounts: amounts,
	}

	appService := buildDrillAppService(t, driver)
	adjustingService, ok := appService.(publicapplication.AdjustingEntryService)
	if !ok {
		t.Fatal("application service does not expose the adjusting-entry capability")
	}

	// TX-01 revenue receipt in January.
	company.firstEntry = postDrillEntry(t, ctx, appService, company, time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		"DR-01 revenue receipt", balancedLines(t, cashID, revenueID, amounts.revenue))
	// TX-02 expense payment in February.
	postDrillEntry(t, ctx, appService, company, time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC),
		"DR-02 expense payment", balancedLines(t, expenseID, cashID, amounts.expense))
	// TX-03 payable settlement in March.
	postDrillEntry(t, ctx, appService, company, time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
		"DR-03 payable settlement", balancedLines(t, payableID, cashID, amounts.settlement))
	// TX-04 receivable collection in April.
	postDrillEntry(t, ctx, appService, company, time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC),
		"DR-04 receivable collection", balancedLines(t, cashID, receivableID, amounts.collection))

	// TX-05 approved adjusting accrual (adjusting/settlement entry mix).
	adjustingDraft, err := adjustingService.CreateAdjustingEntry(ctx, publicapplication.CreateAdjustingEntryCommand{
		Draft: publicapplication.CreateDraftCommand{
			Context:             drillPrincipal(company),
			CompanyID:           types.CompanyID(company.companyID),
			JournalID:           types.JournalID(company.journalID),
			FiscalPeriodID:      types.FiscalPeriodID(company.year.Periods[2].ID),
			EffectiveDate:       time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
			PostingDate:         time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
			Description:         "DR-05 accrued operating expense",
			SourceModule:        "dr-drill",
			TransactionCurrency: "USD",
			ExchangeRate:        "1",
			Lines:               balancedLines(t, expenseID, payableID, amounts.accrual),
		},
		Type:              ledger.AdjustingEntryAccrual,
		Reason:            "Accrue March operating expense",
		ApprovalReference: "APPROVAL-DR-" + name,
	})
	if err != nil {
		t.Fatalf("create adjusting accrual for %s: %v", name, err)
	}
	if _, err := appService.Post(ctx, postCommandFor(company, adjustingDraft.Entry.ID)); err != nil {
		t.Fatalf("post adjusting accrual for %s: %v", name, err)
	}

	// TX-06 posted entry reversed under a strict idempotency key.
	reversedEntry := postDrillEntry(t, ctx, appService, company, time.Date(2026, 4, 20, 0, 0, 0, 0, time.UTC),
		"DR-06 erroneous expense to reverse", balancedLines(t, expenseID, cashID, amounts.reversal))
	reversal, err := appService.Reverse(ctx, reverseCommandFor(company, reversedEntry.ID,
		"dr-reversal-"+name, "Approved correction: duplicated expense booking"))
	if err != nil {
		t.Fatalf("reverse entry for %s: %v", name, err)
	}
	if reversal.Entry.Classification != ledger.EntryClassReversing {
		t.Fatalf("reversal classification = %q, want %q", reversal.Entry.Classification, ledger.EntryClassReversing)
	}
	return company
}

// runYearEndClose hard-closes the twelve regular periods of FY2026 and executes
// the atomic annual close with an opening entry into FY2027.
func runYearEndClose(t *testing.T, ctx context.Context, driver *postgres.Driver, company *drillCompany) {
	t.Helper()
	for _, period := range company.year.Periods[:12] {
		if err := driver.TransitionPeriodStateContext(ctx, company.companyID, period.ID,
			masterdata.PeriodSoftClosed, uuid.New(), "DR drill month-end soft close"); err != nil {
			t.Fatalf("soft close period %d for %s: %v", period.Number, company.name, err)
		}
		if err := driver.TransitionPeriodStateContext(ctx, company.companyID, period.ID,
			masterdata.PeriodHardClosed, uuid.New(), "DR drill month-end hard close"); err != nil {
			t.Fatalf("hard close period %d for %s: %v", period.Number, company.name, err)
		}
	}

	engine := buildDrillEngine(t, driver)
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close engine for %s: %v", company.name, err)
		}
	}()
	closer, err := sdk.YearEndClosing(engine)
	if err != nil {
		t.Fatalf("year-end closing capability for %s: %v", company.name, err)
	}
	result, err := closer.ExecuteYearEndClosing(ctx, closeCommandFor(company, true))
	if err != nil {
		t.Fatalf("execute FY2026 close for %s: %v", company.name, err)
	}
	if len(result.ClosingEntryIDs) == 0 || result.OpeningEntryID == (types.JournalEntryID{}) {
		t.Fatalf("FY2026 close for %s produced %+v, want closing and opening entries", company.name, result)
	}
	netIncome, err := decimal.NewFromString(result.NetIncome)
	if err != nil {
		t.Fatalf("parse FY2026 net income %q for %s: %v", result.NetIncome, company.name, err)
	}
	if !netIncome.Equal(expectedNetIncome(company.amounts)) {
		t.Fatalf("FY2026 net income for %s = %s, want %s", company.name, result.NetIncome,
			expectedNetIncome(company.amounts))
	}
	company.maxSequence = int64(len(result.ClosingEntryIDs)) + 1

	closedYear, err := driver.GetFiscalYearForCompanyContext(ctx, company.companyID, company.year.ID)
	if err != nil {
		t.Fatalf("reload FY2026 for %s: %v", company.name, err)
	}
	if closedYear.State != masterdata.FiscalYearHardClosed {
		t.Fatalf("FY2026 state for %s = %s, want hard-closed", company.name, closedYear.State)
	}
}
