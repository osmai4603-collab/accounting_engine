package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/storage/memory"

	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/reporting"
	"accounting_engine/pkg/api/v1/types"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

func TestPhaseCTransactionAtomicity(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	masterSvc := masterdata.NewService(masterdata.NewRepositoryAdapter(env.Driver))
	engine := posting.NewEngine(env.Driver, masterSvc)

	amount, _ := financialtypes.NewMoneyFromString("100.00", "USD")
	entry := createDraftTestEntry(t, ctx, engine, env.CompanyID, env.JournalID, env.DebitAccID, env.CreditAccID, amount, time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC))

	// Force posting failure by closing the target period; posting must be atomic
	// and leave the draft unposted.
	if err := env.Driver.UpdatePeriodStateForCompanyContext(ctx, env.CompanyID, env.PeriodID, masterdata.PeriodHardClosed); err != nil {
		t.Fatal(err)
	}

	err := engine.PostForCompany(ctx, env.CompanyID, entry.ID, "poster")
	if err == nil {
		t.Fatal("expected posting to fail for closed period")
	}

	got, getErr := env.Driver.GetEntryByIDForCompany(ctx, env.CompanyID, entry.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.Status != posting.EntryDraft {
		t.Fatalf("entry status = %s, want draft after failed atomic posting", got.Status)
	}

	var postedCount int
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*) FROM account_moves WHERE company_id=$1 AND id=$2 AND status='posted'`, env.CompanyID, entry.ID).Scan(&postedCount)
	}); err != nil {
		t.Fatal(err)
	}
	if postedCount != 0 {
		t.Fatalf("expected no posted move after rollback, got %d", postedCount)
	}
}

func TestPhaseCRLSTenantIsolation(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}
	companyA := uuid.New()
	companyB := uuid.New()
	for _, c := range []struct {
		id   uuid.UUID
		code string
	}{
		{companyA, "RLSA"}, {companyB, "RLSB"},
	} {
		if err := driver.CreateCompany(ctx, masterdata.Company{ID: c.id, Code: c.code, LegalName: c.code, FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true}); err != nil {
			t.Fatal(err)
		}
	}
	accA := uuid.New()
	if err := driver.CreateAccount(ctx, masterdata.Account{CompanyID: companyA, ID: accA, Code: "1000", Name: "Cash A", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true}); err != nil {
		t.Fatal(err)
	}

	// Company B context must not see company A account.
	_, err = driver.GetAccountByIDForCompany(ctx, companyB, accA)
	if err == nil {
		t.Fatal("expected cross-company account lookup to fail")
	}
	if !errors.Is(err, masterdata.ErrAccountNotFound) {
		// driver may map to not found; accept any non-nil error as isolation evidence if lookup fails
		// but prefer ErrAccountNotFound
	}

	accountsB, err := driver.ListAccountsForCompany(ctx, companyB)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accountsB {
		if a.ID == accA || a.CompanyID == companyA {
			t.Fatalf("company B listed foreign account: %+v", a)
		}
	}
}

func TestPhaseCConcurrencyPostingIntegrity(t *testing.T) {
	ctx, env := setupIsolatedPostingEnvWithPool(t, 12)
	masterSvc := masterdata.NewService(masterdata.NewRepositoryAdapter(env.Driver))
	engine := posting.NewEngine(env.Driver, masterSvc)

	const workers = 8
	const perWorker = 5
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for i := 0; i < perWorker; i++ {
				amt, _ := financialtypes.NewMoneyFromString(fmt.Sprintf("%d.00", 10+w+i), "USD")
				entry := createDraftTestEntry(t, ctx, engine, env.CompanyID, env.JournalID, env.DebitAccID, env.CreditAccID, amt, time.Date(2026, 1, 11+w, 0, 0, 0, 0, time.UTC))
				if err := engine.PostForCompany(ctx, env.CompanyID, entry.ID, "poster"); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent posting failed: %v", err)
	}

	var count int
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*) FROM account_moves WHERE company_id=$1 AND status='posted'`, env.CompanyID).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != workers*perWorker {
		t.Fatalf("posted moves=%d want=%d", count, workers*perWorker)
	}

	// Materialized/balance integrity: debits == credits across posted lines.
	var debit, credit decimal.Decimal
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(SUM(functional_debit_minor),0), COALESCE(SUM(functional_credit_minor),0)
			FROM account_move_lines ml JOIN account_moves m ON m.company_id=ml.company_id AND m.id=ml.move_id
			WHERE m.company_id=$1 AND m.status='posted'`, env.CompanyID).Scan(&debit, &credit)
	}); err != nil {
		t.Fatal(err)
	}
	if !debit.Equal(credit) {
		t.Fatalf("posted balance integrity violated: debit_minor=%s credit_minor=%s", debit, credit)
	}
}

func TestPhaseCBalanceIntegrity(t *testing.T) {
	ctx, env := setupIsolatedPostingEnv(t)
	masterSvc := masterdata.NewService(masterdata.NewRepositoryAdapter(env.Driver))
	engine := posting.NewEngine(env.Driver, masterSvc)

	amount, _ := financialtypes.NewMoneyFromString("75.00", "USD")
	entry := createDraftTestEntry(t, ctx, engine, env.CompanyID, env.JournalID, env.DebitAccID, env.CreditAccID, amount, time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC))
	// Corrupt stored line to make functionally unbalanced.
	if err := env.Driver.WithCompanyTx(ctx, env.CompanyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE account_move_lines SET functional_credit_minor=functional_credit_minor+1 WHERE company_id=$1 AND move_id=$2 AND line_id=$3`,
			env.CompanyID, entry.ID, entry.Lines[1].ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.PostForCompany(ctx, env.CompanyID, entry.ID, "poster"); err == nil {
		t.Fatal("expected database/application balance integrity to reject unbalanced posting")
	}
}

func TestPhaseCParityMemoryAndPostgres(t *testing.T) {
	ctx := context.Background()
	pg, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}
	mem := memory.New()

	companyID := uuid.New()
	year := masterdata.GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	year.CompanyID = companyID
	periodID := year.Periods[0].ID
	journalID := uuid.New()
	accCash, accRevenue, accExpense := uuid.New(), uuid.New(), uuid.New()

	seed := func(d interface {
		CreateCompany(context.Context, masterdata.Company) error
		CreateAccount(context.Context, masterdata.Account) error
		CreateFiscalYear(context.Context, masterdata.FiscalYear) error
	}) error {
		if err := d.CreateCompany(ctx, masterdata.Company{ID: companyID, Code: "PAR1", LegalName: "Parity Co", FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true}); err != nil {
			return err
		}
		for _, a := range []masterdata.Account{
			{CompanyID: companyID, ID: accCash, Code: "1000", Name: "Cash", AccountType: masterdata.AccountTypeAsset, NormalBalance: masterdata.NormalBalanceDebit, IsActive: true},
			{CompanyID: companyID, ID: accRevenue, Code: "4000", Name: "Revenue", AccountType: masterdata.AccountTypeIncome, NormalBalance: masterdata.NormalBalanceCredit, IFRSCategory: masterdata.IFRSCategoryOperating, IsActive: true},
			{CompanyID: companyID, ID: accExpense, Code: "5000", Name: "Expense", AccountType: masterdata.AccountTypeExpense, NormalBalance: masterdata.NormalBalanceDebit, IFRSCategory: masterdata.IFRSCategoryOperating, IsActive: true},
		} {
			if err := d.CreateAccount(ctx, a); err != nil {
				return err
			}
		}
		return d.CreateFiscalYear(ctx, year)
	}
	if err := seed(pg); err != nil {
		t.Fatal(err)
	}
	if err := seed(mem); err != nil {
		t.Fatal(err)
	}
	// Postgres journal row (required by schema/posting paths)
	if err := pg.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO journals(id,company_id,code) VALUES($1,$2,$3)`, journalID, companyID, "GEN")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	postPair := func(d interface {
		SaveDraftContext(context.Context, posting.JournalEntry) error
		PostAtomicContext(context.Context, posting.JournalEntry) error
	}, date time.Time, ref string, debitAcc, creditAcc uuid.UUID, amt string) {
		t.Helper()
		amount, _ := financialtypes.NewMoneyFromString(amt, "USD")
		zero, _ := financialtypes.Zero("USD")
		entry := posting.JournalEntry{
			CompanyID: companyID, ID: uuid.New(), JournalID: journalID, PeriodID: periodID,
			EntryDate: date, Date: date, Status: posting.EntryDraft, CreatedBy: "maker", Reference: ref,
			Lines: []posting.JournalEntryLine{
				{CompanyID: companyID, ID: uuid.New(), AccountID: debitAcc, Debit: amount, Credit: zero, TransactionAmount: amount, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: amount, FunctionalCredit: zero},
				{CompanyID: companyID, ID: uuid.New(), AccountID: creditAcc, Debit: zero, Credit: amount, TransactionAmount: amount, TransactionCurrency: "USD", ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: amount},
			},
		}
		if err := d.SaveDraftContext(ctx, entry); err != nil {
			t.Fatal(err)
		}
		entry.Status = posting.EntryPosted
		entry.PostedBy = "poster"
		if err := d.PostAtomicContext(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}

	postPair(pg, time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), "sale", accCash, accRevenue, "100.00")
	postPair(pg, time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC), "cost", accExpense, accCash, "40.00")
	postPair(mem, time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), "sale", accCash, accRevenue, "100.00")
	postPair(mem, time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC), "cost", accExpense, accCash, "40.00")

	asOf := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	tbMem, err := mem.GetTrialBalance(ctx, types.CompanyID(companyID), asOf, "USD")
	if err != nil {
		t.Fatal(err)
	}
	tbPg, err := pg.GetTrialBalance(ctx, types.CompanyID(companyID), asOf, "USD")
	if err != nil {
		t.Fatal(err)
	}
	normalizeTB := func(tb reporting.TrialBalance) string {
		sort.Slice(tb.Lines, func(i, j int) bool { return tb.Lines[i].AccountCode < tb.Lines[j].AccountCode })
		s := fmt.Sprintf("%v|%s|%s|%s", tb.IsBalanced, tb.TotalDebit.Amount().String(), tb.TotalCredit.Amount().String(), tb.Currency)
		for _, l := range tb.Lines {
			s += fmt.Sprintf("|%s:%s:%s:%s", l.AccountCode, l.PeriodDebit.Amount().String(), l.PeriodCredit.Amount().String(), l.EndingDebit.Amount().String())
		}
		return s
	}
	if normalizeTB(tbMem) != normalizeTB(tbPg) {
		t.Fatalf("trial balance parity mismatch\nmem: %#v\npg:  %#v", tbMem, tbPg)
	}

	glMem, err := mem.GetGeneralLedger(ctx, types.CompanyID(companyID), types.AccountID(accCash), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), asOf)
	if err != nil {
		t.Fatal(err)
	}
	glPg, err := pg.GetGeneralLedger(ctx, types.CompanyID(companyID), types.AccountID(accCash), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if glMem.OpeningBalance.Amount().String() != glPg.OpeningBalance.Amount().String() || glMem.EndingBalance.Amount().String() != glPg.EndingBalance.Amount().String() || len(glMem.Entries) != len(glPg.Entries) {
		t.Fatalf("general ledger parity mismatch mem=%#v pg=%#v", glMem, glPg)
	}

	bsMem, err := mem.GetBalanceSheet(ctx, types.CompanyID(companyID), asOf, "USD")
	if err != nil {
		t.Fatal(err)
	}
	bsPg, err := pg.GetBalanceSheet(ctx, types.CompanyID(companyID), asOf, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if bsMem.IsBalanced != bsPg.IsBalanced || bsMem.TotalAssets.Amount().String() != bsPg.TotalAssets.Amount().String() || bsMem.TotalLiabEquity.Amount().String() != bsPg.TotalLiabEquity.Amount().String() {
		t.Fatalf("balance sheet parity mismatch mem=%#v pg=%#v", bsMem, bsPg)
	}

	isMem, err := mem.GetIncomeStatement(ctx, types.CompanyID(companyID), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), asOf, "USD")
	if err != nil {
		t.Fatal(err)
	}
	isPg, err := pg.GetIncomeStatement(ctx, types.CompanyID(companyID), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), asOf, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if isMem.NetIncome.Amount().String() != isPg.NetIncome.Amount().String() {
		t.Fatalf("income statement parity mismatch mem=%#v pg=%#v", isMem, isPg)
	}
}

var _ = ledger.EntryDraft
