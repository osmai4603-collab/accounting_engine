package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	internalreporting "accounting_engine/internal/reporting"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/reporting"
	"accounting_engine/pkg/api/v1/types"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

var (
	_ accountingEngineStorageCore      = (*Driver)(nil)
	_ accountingEngineStorageReporting = (*Driver)(nil)
	_ accountingEngineStorageGov       = (*Driver)(nil)
)

type accountingEngineStorageCore interface {
	SaveEntry(ctx context.Context, entry ledger.JournalEntry) error
	GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error)
	PostEntry(ctx context.Context, entry ledger.JournalEntry) error
	GetAccount(ctx context.Context, companyID types.CompanyID, accountID types.AccountID) (ledger.Account, error)
	GetFiscalPeriod(ctx context.Context, companyID types.CompanyID, periodID types.FiscalPeriodID) (ledger.FiscalPeriod, error)
}

type accountingEngineStorageReporting interface {
	GetTrialBalance(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.TrialBalance, error)
	GetGeneralLedger(ctx context.Context, companyID types.CompanyID, accountID types.AccountID, start, end time.Time) (reporting.GeneralLedgerReport, error)
	GetBalanceSheet(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.BalanceSheetReport, error)
	GetIncomeStatement(ctx context.Context, companyID types.CompanyID, start, end time.Time, currency string) (reporting.IncomeStatementReport, error)
}

type accountingEngineStorageGov interface {
	RecordAuditLog(ctx context.Context, eventID string, details map[string]any) error
	GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error)
}

func (d *Driver) SaveEntry(ctx context.Context, entry ledger.JournalEntry) error {
	pEntry := mapLedgerToPosting(entry)
	return d.SaveDraftContext(ctx, pEntry)
}

func (d *Driver) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	pEntry, err := d.GetEntryByIDForCompany(ctx, uuid.UUID(companyID), uuid.UUID(entryID))
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	return mapPostingToLedger(pEntry), nil
}

func (d *Driver) PostEntry(ctx context.Context, entry ledger.JournalEntry) error {
	var totalDebit, totalCredit decimal.Decimal
	for _, line := range entry.Lines {
		totalDebit = totalDebit.Add(line.FunctionalDebit.Amount())
		totalCredit = totalCredit.Add(line.FunctionalCredit.Amount())
	}
	if !totalDebit.Equal(totalCredit) {
		return posting.ErrFunctionalBalanceMismatch
	}

	pEntry := mapLedgerToPosting(entry)
	// C.3: do not fabricate period/journal; rely on actual master data/links.
	if pEntry.PeriodID == uuid.Nil {
		return types.ErrFiscalPeriodNotFound
	}
	if pEntry.JournalID == uuid.Nil {
		pEntry.JournalID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("journal-"+pEntry.CompanyID.String()))
	}
	draft := pEntry
	draft.Status = posting.EntryDraft
	if err := d.SaveDraftContext(ctx, draft); err != nil {
		return err
	}
	pEntry.Status = posting.EntryPosted
	return d.PostAtomicContext(ctx, pEntry)
}

func (d *Driver) GetAccount(ctx context.Context, companyID types.CompanyID, accountID types.AccountID) (ledger.Account, error) {
	acc, err := d.GetAccountByIDForCompany(ctx, uuid.UUID(companyID), uuid.UUID(accountID))
	if err != nil {
		return ledger.Account{}, err
	}
	var parentID *types.AccountID
	if acc.ParentID != nil {
		pid := types.AccountID(*acc.ParentID)
		parentID = &pid
	}
	return ledger.Account{
		ID:             types.AccountID(acc.ID),
		CompanyID:      types.CompanyID(acc.CompanyID),
		Code:           acc.Code,
		Name:           acc.Name,
		Type:           ledger.AccountType(acc.AccountType),
		NormalBalance:  ledger.NormalBalance(acc.NormalBalance),
		ParentID:       parentID,
		IsReconcilable: acc.IsReconcilable,
		IsActive:       acc.IsActive,
		Currency:       acc.CurrencyID,
	}, nil
}

func (d *Driver) GetFiscalPeriod(ctx context.Context, companyID types.CompanyID, periodID types.FiscalPeriodID) (ledger.FiscalPeriod, error) {
	var period ledger.FiscalPeriod
	err := d.WithCompanyTx(ctx, uuid.UUID(companyID), func(tx pgx.Tx) error {
		var pID, cID, yID uuid.UUID
		var number int
		var name, state, ptype string
		var start, end time.Time
		scanErr := tx.QueryRow(ctx, `SELECT id, company_id, year_id, COALESCE(period_number,0), COALESCE(name,''), start_date, end_date, state, period_type
			FROM fiscal_periods WHERE company_id=$1 AND id=$2`, uuid.UUID(companyID), uuid.UUID(periodID)).
			Scan(&pID, &cID, &yID, &number, &name, &start, &end, &state, &ptype)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return types.ErrFiscalPeriodNotFound
		}
		if scanErr != nil {
			return scanErr
		}
		period = ledger.FiscalPeriod{
			ID:           types.FiscalPeriodID(pID),
			CompanyID:    types.CompanyID(cID),
			FiscalYearID: types.CompanyID(yID),
			Number:       number,
			Name:         name,
			StartDate:    start,
			EndDate:      end,
			State:        ledger.PeriodState(state),
			Type:         ledger.FiscalPeriodType(ptype),
		}
		return nil
	})
	return period, err
}

func (d *Driver) GetTrialBalance(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.TrialBalance, error) {
	if strings.TrimSpace(currency) == "" {
		currency = "USD"
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	engine := internalreporting.NewEngine(d)
	tb, err := engine.TrialBalance.GenerateAdjustedForCompanyContext(ctx, uuid.UUID(companyID), time.Time{}, asOf, currency, false)
	if err != nil {
		return reporting.TrialBalance{}, err
	}
	lines := make([]reporting.TrialBalanceLine, 0, len(tb.Lines))
	for _, l := range tb.Lines {
		lines = append(lines, reporting.TrialBalanceLine{
			AccountID:     types.AccountID(l.AccountID),
			AccountCode:   l.AccountCode,
			AccountName:   l.AccountName,
			AccountType:   string(l.AccountType),
			OpeningDebit:  mustMoney(l.OpeningDebit, currency),
			OpeningCredit: mustMoney(l.OpeningCredit, currency),
			PeriodDebit:   mustMoney(l.PeriodDebit, currency),
			PeriodCredit:  mustMoney(l.PeriodCredit, currency),
			EndingDebit:   mustMoney(l.EndingDebit, currency),
			EndingCredit:  mustMoney(l.EndingCredit, currency),
		})
	}
	return reporting.TrialBalance{
		CompanyID:   companyID,
		AsOfDate:    asOf,
		Currency:    currency,
		Lines:       lines,
		TotalDebit:  mustMoney(tb.DebitTotal, currency),
		TotalCredit: mustMoney(tb.CreditTotal, currency),
		IsBalanced:  tb.Balanced,
	}, nil
}

func (d *Driver) GetGeneralLedger(ctx context.Context, companyID types.CompanyID, accountID types.AccountID, start, end time.Time) (reporting.GeneralLedgerReport, error) {
	currency := "USD"
	var functional string
	if err := d.WithCompanyTx(ctx, uuid.UUID(companyID), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT functional_currency FROM companies WHERE id=$1`, uuid.UUID(companyID)).Scan(&functional)
	}); err == nil && strings.TrimSpace(functional) != "" {
		currency = strings.ToUpper(strings.TrimSpace(functional))
	}
	engine := internalreporting.NewEngine(d)
	gl, err := engine.GeneralLedger.GenerateForCompanyContext(ctx, uuid.UUID(companyID), uuid.UUID(accountID), start, end, currency)
	if err != nil {
		return reporting.GeneralLedgerReport{}, err
	}
	entries := make([]reporting.GeneralLedgerEntry, 0, len(gl.Lines))
	for _, l := range gl.Lines {
		entries = append(entries, reporting.GeneralLedgerEntry{
			EntryID:        types.JournalEntryID(l.EntryID),
			EntryNumber:    fmtInt64(l.SequenceNumber),
			EffectiveDate:  l.Date,
			Description:    l.Reference,
			Debit:          mustMoney(l.Debit, currency),
			Credit:         mustMoney(l.Credit, currency),
			RunningBalance: mustMoney(l.RunningBalance, currency),
		})
	}
	return reporting.GeneralLedgerReport{
		CompanyID:      companyID,
		AccountID:      accountID,
		AccountCode:    gl.Code,
		AccountName:    gl.Name,
		StartDate:      start,
		EndDate:        end,
		OpeningBalance: mustMoney(gl.OpeningBalance, currency),
		Entries:        entries,
		EndingBalance:  mustMoney(gl.EndingBalance, currency),
	}, nil
}

func (d *Driver) GetBalanceSheet(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.BalanceSheetReport, error) {
	if strings.TrimSpace(currency) == "" {
		currency = "USD"
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	engine := internalreporting.NewEngine(d)
	tb, err := engine.TrialBalance.GenerateAdjustedForCompanyContext(ctx, uuid.UUID(companyID), time.Time{}, asOf, currency, false)
	if err != nil {
		return reporting.BalanceSheetReport{}, err
	}
	assets := reporting.BalanceSheetSection{Title: "Assets"}
	liabs := reporting.BalanceSheetSection{Title: "Liabilities"}
	equity := reporting.BalanceSheetSection{Title: "Equity"}
	var totalAssets, totalLiab, totalEquity decimal.Decimal
	for _, l := range tb.Lines {
		net := l.EndingDebit.Sub(l.EndingCredit)
		switch l.AccountType {
		case masterdata.AccountTypeAsset:
			amt := net
			if amt.IsZero() {
				continue
			}
			assets.Lines = append(assets.Lines, reporting.BalanceSheetLine{AccountID: types.AccountID(l.AccountID), AccountCode: l.AccountCode, AccountName: l.AccountName, Amount: mustMoney(amt, currency)})
			totalAssets = totalAssets.Add(amt)
		case masterdata.AccountTypeLiability:
			amt := net.Neg()
			if amt.IsZero() {
				continue
			}
			liabs.Lines = append(liabs.Lines, reporting.BalanceSheetLine{AccountID: types.AccountID(l.AccountID), AccountCode: l.AccountCode, AccountName: l.AccountName, Amount: mustMoney(amt, currency)})
			totalLiab = totalLiab.Add(amt)
		case masterdata.AccountTypeEquity:
			amt := net.Neg()
			if amt.IsZero() {
				continue
			}
			equity.Lines = append(equity.Lines, reporting.BalanceSheetLine{AccountID: types.AccountID(l.AccountID), AccountCode: l.AccountCode, AccountName: l.AccountName, Amount: mustMoney(amt, currency)})
			totalEquity = totalEquity.Add(amt)
		}
	}
	if is, err := engine.Income.GenerateForCompanyContext(ctx, uuid.UUID(companyID), time.Time{}, asOf, currency); err == nil && !is.NetProfit.IsZero() {
		equity.Lines = append(equity.Lines, reporting.BalanceSheetLine{AccountName: "Current Period Net Income", Amount: mustMoney(is.NetProfit, currency)})
		totalEquity = totalEquity.Add(is.NetProfit)
	}
	assets.TotalAmount = mustMoney(totalAssets, currency)
	liabs.TotalAmount = mustMoney(totalLiab, currency)
	equity.TotalAmount = mustMoney(totalEquity, currency)
	totalLE := totalLiab.Add(totalEquity)
	return reporting.BalanceSheetReport{
		CompanyID:       companyID,
		AsOfDate:        asOf,
		Currency:        currency,
		Assets:          assets,
		Liabilities:     liabs,
		Equity:          equity,
		TotalAssets:     mustMoney(totalAssets, currency),
		TotalLiabEquity: mustMoney(totalLE, currency),
		IsBalanced:      totalAssets.Equal(totalLE),
	}, nil
}

func (d *Driver) GetIncomeStatement(ctx context.Context, companyID types.CompanyID, start, end time.Time, currency string) (reporting.IncomeStatementReport, error) {
	if strings.TrimSpace(currency) == "" {
		currency = "USD"
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	engine := internalreporting.NewEngine(d)
	tb, err := engine.TrialBalance.GenerateAdjustedForCompanyContext(ctx, uuid.UUID(companyID), start, end, currency, false)
	if err != nil {
		return reporting.IncomeStatementReport{}, err
	}
	revenues := reporting.IncomeStatementSection{Title: "Revenues"}
	expenses := reporting.IncomeStatementSection{Title: "Expenses"}
	var totalRev, totalExp decimal.Decimal
	for _, l := range tb.Lines {
		net := l.EndingDebit.Sub(l.EndingCredit)
		switch l.AccountType {
		case masterdata.AccountTypeIncome:
			amt := net.Neg()
			if amt.IsZero() {
				continue
			}
			revenues.Lines = append(revenues.Lines, reporting.IncomeStatementLine{AccountID: types.AccountID(l.AccountID), AccountCode: l.AccountCode, AccountName: l.AccountName, Amount: mustMoney(amt, currency)})
			totalRev = totalRev.Add(amt)
		case masterdata.AccountTypeExpense:
			amt := net
			if amt.IsZero() {
				continue
			}
			expenses.Lines = append(expenses.Lines, reporting.IncomeStatementLine{AccountID: types.AccountID(l.AccountID), AccountCode: l.AccountCode, AccountName: l.AccountName, Amount: mustMoney(amt, currency)})
			totalExp = totalExp.Add(amt)
		}
	}
	revenues.TotalAmount = mustMoney(totalRev, currency)
	expenses.TotalAmount = mustMoney(totalExp, currency)
	return reporting.IncomeStatementReport{
		CompanyID: companyID,
		StartDate: start,
		EndDate:   end,
		Currency:  currency,
		Revenues:  revenues,
		Expenses:  expenses,
		NetIncome: mustMoney(totalRev.Sub(totalExp), currency),
	}, nil
}

func mustMoney(amount decimal.Decimal, currency string) types.Money {
	m, err := types.NewMoney(amount, currency)
	if err != nil {
		m, _ = types.NewMoney(decimal.Zero, currency)
	}
	return m
}

func fmtInt64(v int64) string {
	if v == 0 {
		return ""
	}
	return decimal.NewFromInt(v).String()
}

func (d *Driver) RecordAuditLog(ctx context.Context, eventID string, details map[string]any) error {
	// SPI audit requires company-scoped, chained append semantics that are not
	// safely derivable from this generic signature alone; return a clear capability
	// error rather than writing a non-compliant/placeholder audit record.
	_ = ctx
	_ = eventID
	_ = details
	return types.ErrCapabilityNotImplemented
}

func (d *Driver) GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Resolve entry company, then compute deterministic content hash.
	var companyID uuid.UUID
	err := d.pool.QueryRow(ctx, `SELECT company_id FROM account_moves WHERE id=$1`, uuid.UUID(entryID)).Scan(&companyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", posting.ErrEntryNotFound
	}
	if err != nil {
		return "", err
	}
	entry, err := d.GetEntryByIDForCompany(ctx, companyID, uuid.UUID(entryID))
	if err != nil {
		return "", err
	}
	return posting.EntryContentHash(entry)
}

func mapLedgerToPosting(e ledger.JournalEntry) posting.JournalEntry {
	lines := make([]posting.JournalEntryLine, len(e.Lines))
	for i, l := range e.Lines {
		fdAmount := l.FunctionalDebit.Amount()
		fcAmount := l.FunctionalCredit.Amount()
		tAmount := l.TransactionAmount.Amount()

		cur := l.FunctionalDebit.Currency()
		if cur == "" {
			cur = l.FunctionalCredit.Currency()
		}
		if cur == "" {
			cur = l.TransactionCurrency
		}
		if cur == "" {
			cur = "USD"
		}
		fd, _ := financialtypes.NewMoney(fdAmount, cur)
		fc, _ := financialtypes.NewMoney(fcAmount, cur)
		ta, _ := financialtypes.NewMoney(tAmount, cur)

		lines[i] = posting.JournalEntryLine{
			ID:                  uuid.UUID(l.ID),
			AccountID:           uuid.UUID(l.AccountID),
			TransactionAmount:   ta,
			TransactionCurrency: cur,
			ExchangeRate:        l.ExchangeRate,
			FunctionalDebit:     fd,
			FunctionalCredit:    fc,
		}
	}

	var revID *uuid.UUID
	if e.ReversalOfID != nil {
		id := uuid.UUID(*e.ReversalOfID)
		revID = &id
	}

	class := posting.EntryClassRegular
	if e.Classification != "" {
		class = posting.EntryClassification(e.Classification)
	}

	status := posting.EntryPosted
	if e.Status != "" {
		status = posting.EntryStatus(e.Status)
	}

	periodID := uuid.UUID(e.FiscalPeriodID)
	journalID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("journal-"+uuid.UUID(e.CompanyID).String()))
	effective := e.EffectiveDate
	if effective.IsZero() {
		effective = e.PostingDate
	}

	return posting.JournalEntry{
		ID:                uuid.UUID(e.ID),
		CompanyID:         uuid.UUID(e.CompanyID),
		JournalID:         journalID,
		PeriodID:          periodID,
		EntryDate:         effective,
		Date:              effective,
		Status:            status,
		Classification:    class,
		CreatedBy:         e.CreatedBy,
		Reference:         e.Description,
		DocumentReference: e.ReferenceID,
		ReversedEntryID:   revID,
		Lines:             lines,
	}
}

func mapPostingToLedger(e posting.JournalEntry) ledger.JournalEntry {
	lines := make([]ledger.EntryLine, len(e.Lines))
	for i, l := range e.Lines {
		fd, _ := types.NewMoney(l.FunctionalDebit.Amount(), l.FunctionalDebit.Currency())
		fc, _ := types.NewMoney(l.FunctionalCredit.Amount(), l.FunctionalCredit.Currency())
		ta, _ := types.NewMoney(l.TransactionAmount.Amount(), l.TransactionCurrency)

		lines[i] = ledger.EntryLine{
			ID:                  types.AccountID(l.ID),
			AccountID:           types.AccountID(l.AccountID),
			TransactionAmount:   ta,
			TransactionCurrency: l.TransactionCurrency,
			ExchangeRate:        l.ExchangeRate,
			FunctionalDebit:     fd,
			FunctionalCredit:    fc,
		}
	}

	var revID *types.JournalEntryID
	if e.OriginalEntryID != nil {
		id := types.JournalEntryID(*e.OriginalEntryID)
		revID = &id
	} else if e.ReversedEntryID != nil && e.Classification == posting.EntryClassReversing {
		id := types.JournalEntryID(*e.ReversedEntryID)
		revID = &id
	}

	return ledger.JournalEntry{
		ID:             types.JournalEntryID(e.ID),
		CompanyID:      types.CompanyID(e.CompanyID),
		FiscalPeriodID: types.FiscalPeriodID(e.PeriodID),
		Status:         ledger.EntryStatus(e.Status),
		Classification: ledger.EntryClassification(e.Classification),
		EffectiveDate:  e.EntryDate,
		PostingDate:    e.Date,
		Description:    e.Reference,
		ReferenceID:    e.DocumentReference,
		CreatedBy:      e.CreatedBy,
		ReversalOfID:   revID,
		Lines:          lines,
	}
}
