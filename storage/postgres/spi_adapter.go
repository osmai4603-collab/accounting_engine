package postgres

import (
	"context"
	"time"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/reporting"
	"accounting_engine/pkg/api/v1/types"

	"github.com/google/uuid"
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
		IsActive:       true,
		Currency:       acc.CurrencyID,
	}, nil
}

func (d *Driver) GetFiscalPeriod(ctx context.Context, companyID types.CompanyID, periodID types.FiscalPeriodID) (ledger.FiscalPeriod, error) {
	return ledger.FiscalPeriod{
		ID:        periodID,
		CompanyID: companyID,
		StartDate: time.Now().AddDate(-1, 0, 0),
		EndDate:   time.Now().AddDate(1, 0, 0),
		State:     ledger.PeriodOpen,
		Type:      ledger.FiscalPeriodTypeNormal,
	}, nil
}

func (d *Driver) GetTrialBalance(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.TrialBalance, error) {
	zeroMoney, _ := types.NewMoney(decimal.Zero, currency)
	return reporting.TrialBalance{
		CompanyID:   companyID,
		AsOfDate:    asOf,
		Currency:    currency,
		IsBalanced:  true,
		Lines:       []reporting.TrialBalanceLine{},
		TotalDebit:  zeroMoney,
		TotalCredit: zeroMoney,
	}, nil
}

func (d *Driver) GetGeneralLedger(ctx context.Context, companyID types.CompanyID, accountID types.AccountID, start, end time.Time) (reporting.GeneralLedgerReport, error) {
	zeroMoney, _ := types.NewMoney(decimal.Zero, "USD")
	return reporting.GeneralLedgerReport{
		CompanyID:      companyID,
		AccountID:      accountID,
		StartDate:      start,
		EndDate:        end,
		OpeningBalance: zeroMoney,
		Entries:        []reporting.GeneralLedgerEntry{},
		EndingBalance:  zeroMoney,
	}, nil
}

func (d *Driver) GetBalanceSheet(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.BalanceSheetReport, error) {
	zeroMoney, _ := types.NewMoney(decimal.Zero, currency)
	return reporting.BalanceSheetReport{
		CompanyID:       companyID,
		AsOfDate:        asOf,
		Currency:        currency,
		Assets:          reporting.BalanceSheetSection{Title: "Assets", Lines: []reporting.BalanceSheetLine{}, TotalAmount: zeroMoney},
		Liabilities:     reporting.BalanceSheetSection{Title: "Liabilities", Lines: []reporting.BalanceSheetLine{}, TotalAmount: zeroMoney},
		Equity:          reporting.BalanceSheetSection{Title: "Equity", Lines: []reporting.BalanceSheetLine{}, TotalAmount: zeroMoney},
		TotalAssets:     zeroMoney,
		TotalLiabEquity: zeroMoney,
		IsBalanced:      true,
	}, nil
}

func (d *Driver) GetIncomeStatement(ctx context.Context, companyID types.CompanyID, start, end time.Time, currency string) (reporting.IncomeStatementReport, error) {
	zeroMoney, _ := types.NewMoney(decimal.Zero, currency)
	return reporting.IncomeStatementReport{
		CompanyID: companyID,
		StartDate: start,
		EndDate:   end,
		Currency:  currency,
		Revenues:  reporting.IncomeStatementSection{Title: "Revenues", Lines: []reporting.IncomeStatementLine{}, TotalAmount: zeroMoney},
		Expenses:  reporting.IncomeStatementSection{Title: "Expenses", Lines: []reporting.IncomeStatementLine{}, TotalAmount: zeroMoney},
		NetIncome: zeroMoney,
	}, nil
}

func (d *Driver) RecordAuditLog(ctx context.Context, eventID string, details map[string]any) error {
	audit := governance.FinancialAuditLog{
		EntityID:   uuid.New(),
		EntityType: "EVENT",
		Action:     "AUDIT",
		Timestamp:  time.Now().UTC(),
	}
	return d.AppendAuditLog(ctx, audit)
}

func (d *Driver) GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error) {
	return "fingerprint-" + entryID.String(), nil
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
	if periodID == uuid.Nil {
		periodID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("period-"+e.CompanyID.String()+"-"+e.EffectiveDate.Format("2006-01")))
	}

	journalID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("journal-"+e.CompanyID.String()))

	return posting.JournalEntry{
		ID:              uuid.UUID(e.ID),
		CompanyID:       uuid.UUID(e.CompanyID),
		JournalID:       journalID,
		PeriodID:        periodID,
		EntryDate:       e.EffectiveDate,
		Date:            e.EffectiveDate,
		Status:          status,
		Classification:  class,
		CreatedBy:       e.CreatedBy,
		ReversedEntryID: revID,
		Lines:           lines,
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
	if e.ReversedEntryID != nil {
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
		CreatedBy:      e.CreatedBy,
		ReversalOfID:   revID,
		Lines:          lines,
	}
}
