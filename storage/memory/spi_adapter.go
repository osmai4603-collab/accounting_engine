package memory

import (
	"context"
	"errors"
	"time"

	"accounting_engine/internal/masterdata"
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

	// P0-1/P0-5: do not fabricate master data. Require referenced entities to exist.
	d.mu.RLock()
	_, companyOK := d.companies[pEntry.CompanyID]
	_, periodOK := d.periods[pEntry.PeriodID]
	accountsOK := true
	for _, l := range pEntry.Lines {
		acc, ok := d.accounts[l.AccountID]
		if !ok || (pEntry.CompanyID != uuid.Nil && acc.CompanyID != pEntry.CompanyID) {
			accountsOK = false
			break
		}
	}
	d.mu.RUnlock()
	if pEntry.CompanyID != uuid.Nil && !companyOK {
		return errors.New("company not found")
	}
	if pEntry.PeriodID != uuid.Nil && !periodOK {
		return types.ErrFiscalPeriodNotFound
	}
	if !accountsOK {
		return types.ErrAccountNotFound
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
	d.mu.RLock()
	defer d.mu.RUnlock()
	acc, ok := d.accounts[uuid.UUID(accountID)]
	if !ok || acc.CompanyID != uuid.UUID(companyID) {
		return ledger.Account{}, masterdata.ErrAccountNotFound
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
	d.mu.RLock()
	defer d.mu.RUnlock()
	p, ok := d.periods[uuid.UUID(periodID)]
	if !ok || p.CompanyID != uuid.UUID(companyID) {
		return ledger.FiscalPeriod{}, types.ErrFiscalPeriodNotFound
	}
	return ledger.FiscalPeriod{
		ID:           types.FiscalPeriodID(p.ID),
		CompanyID:    types.CompanyID(p.CompanyID),
		FiscalYearID: types.CompanyID(p.FiscalYearID),
		Number:       p.Number,
		Name:         p.Name,
		StartDate:    p.StartDate,
		EndDate:      p.EndDate,
		State:        ledger.PeriodState(p.State),
		Type:         ledger.FiscalPeriodType(p.Type),
	}, nil
}

func (d *Driver) GetTrialBalance(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.TrialBalance, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	lines := make([]reporting.TrialBalanceLine, 0)
	var totD, totC decimal.Decimal
	for k, b := range d.balances {
		if k.companyID == uuid.UUID(companyID) {
			if currency == "" || k.currency == currency {
				acc := d.accounts[k.accountID]
				dMoney, _ := types.NewMoney(b.periodDebit, k.currency)
				cMoney, _ := types.NewMoney(b.periodCredit, k.currency)
				lines = append(lines, reporting.TrialBalanceLine{
					AccountID:    types.AccountID(k.accountID),
					AccountCode:  acc.Code,
					AccountName:  acc.Name,
					AccountType:  string(acc.AccountType),
					PeriodDebit:  dMoney,
					PeriodCredit: cMoney,
					EndingDebit:  dMoney,
					EndingCredit: cMoney,
				})
				totD = totD.Add(b.periodDebit)
				totC = totC.Add(b.periodCredit)
			}
		}
	}
	totDMoney, _ := types.NewMoney(totD, currency)
	totCMoney, _ := types.NewMoney(totC, currency)
	return reporting.TrialBalance{
		CompanyID:   companyID,
		AsOfDate:    asOf,
		Currency:    currency,
		Lines:       lines,
		TotalDebit:  totDMoney,
		TotalCredit: totCMoney,
		IsBalanced:  totD.Equal(totC),
	}, nil
}

func (d *Driver) GetGeneralLedger(ctx context.Context, companyID types.CompanyID, accountID types.AccountID, start, end time.Time) (reporting.GeneralLedgerReport, error) {
	return reporting.GeneralLedgerReport{}, types.ErrCapabilityNotImplemented
}

func (d *Driver) GetBalanceSheet(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.BalanceSheetReport, error) {
	return reporting.BalanceSheetReport{}, types.ErrCapabilityNotImplemented
}

func (d *Driver) GetIncomeStatement(ctx context.Context, companyID types.CompanyID, start, end time.Time, currency string) (reporting.IncomeStatementReport, error) {
	return reporting.IncomeStatementReport{}, types.ErrCapabilityNotImplemented
}

func (d *Driver) RecordAuditLog(ctx context.Context, eventID string, details map[string]any) error {
	return types.ErrCapabilityNotImplemented
}

func (d *Driver) GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error) {
	return "", types.ErrCapabilityNotImplemented
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
