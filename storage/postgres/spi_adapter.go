package postgres

import (
	"context"
	"errors"
	"time"

	"accounting_engine/internal/posting"
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
	return reporting.TrialBalance{}, types.ErrCapabilityNotImplemented
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
	_ = eventID
	_ = details
	return types.ErrCapabilityNotImplemented
}

func (d *Driver) GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error) {
	_ = entryID
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
