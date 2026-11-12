package postgres

import (
	"context"
	"errors"
	"time"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
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
	if pEntry.Status == "" {
		pEntry.Status = posting.EntryPosted
	}
	if err := d.SaveDraftContext(ctx, pEntry); err != nil {
		return err
	}
	return d.PostAtomicContext(ctx, pEntry)
}

func (d *Driver) GetAccount(ctx context.Context, companyID types.CompanyID, accountID types.AccountID) (ledger.Account, error) {
	acc, err := d.GetAccountByIDForCompany(ctx, uuid.UUID(companyID), uuid.UUID(accountID))
	if err != nil {
		return ledger.Account{}, err
	}
	return ledger.Account{
		ID:                types.AccountID(acc.ID),
		CompanyID:         types.CompanyID(acc.CompanyID),
		Code:              acc.Code,
		Name:              acc.Name,
		Type:              ledger.AccountType(acc.Type),
		SubClassification: acc.SubClassification,
		NormalBalance:     ledger.NormalBalance(acc.NormalBalance),
		ParentID:          (*types.AccountID)(acc.ParentID),
		IsReconcilable:    acc.IsReconcilable,
		IsActive:          acc.IsActive,
		Currency:          acc.Currency,
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
	return reporting.TrialBalance{
		CompanyID:   companyID,
		AsOf:        asOf,
		Currency:    currency,
		IsBalanced:  true,
		Lines:       []reporting.TrialBalanceLine{},
	}, nil
}

func (d *Driver) GetGeneralLedger(ctx context.Context, companyID types.CompanyID, accountID types.AccountID, start, end time.Time) (reporting.GeneralLedgerReport, error) {
	return reporting.GeneralLedgerReport{
		CompanyID: companyID,
		AccountID: accountID,
		Start:     start,
		End:       end,
		Lines:     []reporting.GeneralLedgerLine{},
	}, nil
}

func (d *Driver) GetBalanceSheet(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.BalanceSheetReport, error) {
	return reporting.BalanceSheetReport{
		CompanyID: companyID,
		AsOf:      asOf,
		Currency:  currency,
	}, nil
}

func (d *Driver) GetIncomeStatement(ctx context.Context, companyID types.CompanyID, start, end time.Time, currency string) (reporting.IncomeStatementReport, error) {
	return reporting.IncomeStatementReport{
		CompanyID: companyID,
		Start:     start,
		End:       end,
		Currency:  currency,
	}, nil
}

func (d *Driver) RecordAuditLog(ctx context.Context, eventID string, details map[string]any) error {
	return d.AppendAuditLog(ctx, governance.FinancialAuditLog{
		Action: eventID,
	})
}

func (d *Driver) GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error) {
	return "fp-" + entryID.String(), nil
}

func mapLedgerToPosting(e ledger.JournalEntry) posting.JournalEntry {
	lines := make([]posting.JournalEntryLine, len(e.Lines))
	for i, l := range e.Lines {
		fdAmount := l.FunctionalDebit.Amount()
		fcAmount := l.FunctionalCredit.Amount()
		tAmount := l.TransactionAmount.Amount()

		fd, _ := masterdata.NewMoney(fdAmount, l.FunctionalDebit.Currency())
		fc, _ := masterdata.NewMoney(fcAmount, l.FunctionalCredit.Currency())
		ta, _ := masterdata.NewMoney(tAmount, l.TransactionCurrency)

		lines[i] = posting.JournalEntryLine{
			ID:                  uuid.UUID(l.ID),
			AccountID:           uuid.UUID(l.AccountID),
			PartnerID:           (*uuid.UUID)(l.PartnerID),
			CostCenterID:        (*uuid.UUID)(l.CostCenterID),
			ProjectID:           (*uuid.UUID)(l.ProjectID),
			SegmentID:           (*uuid.UUID)(l.SegmentID),
			BusinessUnitID:      (*uuid.UUID)(l.BusinessUnitID),
			MatchingNumber:      l.MatchingNumber,
			ReconciledAmount:    l.ReconciledAmount,
			TransactionAmount:   ta,
			TransactionCurrency: l.TransactionCurrency,
			ExchangeRate:        l.ExchangeRate,
			FunctionalDebit:     fd,
			FunctionalCredit:    fc,
		}
	}

	var revID uuid.UUID
	if e.ReversalOfID != nil {
		revID = uuid.UUID(*e.ReversalOfID)
	}

	return posting.JournalEntry{
		ID:                  uuid.UUID(e.ID),
		CompanyID:           uuid.UUID(e.CompanyID),
		FiscalPeriodID:      uuid.UUID(e.FiscalPeriodID),
		EntryNumber:         e.EntryNumber,
		Status:              posting.EntryStatus(e.Status),
		Classification:      posting.EntryClassification(e.Classification),
		EffectiveDate:       e.EffectiveDate,
		EntryDate:           e.EffectiveDate,
		PostingDate:         e.PostingDate,
		Description:         e.Description,
		SourceModule:        e.SourceModule,
		ReferenceID:         e.ReferenceID,
		TransactionCurrency: e.TransactionCurrency,
		ExchangeRate:        e.ExchangeRate,
		Lines:               lines,
		CreatedBy:           e.CreatedBy,
		ApprovedBy:          e.ApprovedBy,
		ApprovalFingerprint: e.ApprovalFingerprint,
		ReversalOfID:        revID,
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
			PartnerID:           (*types.CompanyID)(l.PartnerID),
			CostCenterID:        (*types.CompanyID)(l.CostCenterID),
			ProjectID:           (*types.CompanyID)(l.ProjectID),
			SegmentID:           (*types.CompanyID)(l.SegmentID),
			BusinessUnitID:      (*types.CompanyID)(l.BusinessUnitID),
			MatchingNumber:      l.MatchingNumber,
			ReconciledAmount:    l.ReconciledAmount,
			TransactionAmount:   ta,
			TransactionCurrency: l.TransactionCurrency,
			ExchangeRate:        l.ExchangeRate,
			FunctionalDebit:     fd,
			FunctionalCredit:    fc,
		}
	}

	var revID *types.JournalEntryID
	if e.ReversalOfID != uuid.Nil {
		id := types.JournalEntryID(e.ReversalOfID)
		revID = &id
	}

	return ledger.JournalEntry{
		ID:                  types.JournalEntryID(e.ID),
		CompanyID:           types.CompanyID(e.CompanyID),
		FiscalPeriodID:      types.FiscalPeriodID(e.FiscalPeriodID),
		EntryNumber:         e.EntryNumber,
		Status:              ledger.EntryStatus(e.Status),
		Classification:      ledger.EntryClassification(e.Classification),
		EffectiveDate:       e.EffectiveDate,
		PostingDate:         e.PostingDate,
		Description:         e.Description,
		SourceModule:        e.SourceModule,
		ReferenceID:         e.ReferenceID,
		TransactionCurrency: e.TransactionCurrency,
		ExchangeRate:        e.ExchangeRate,
		Lines:               lines,
		CreatedBy:           e.CreatedBy,
		ApprovedBy:          e.ApprovedBy,
		ApprovalFingerprint: e.ApprovalFingerprint,
		ReversalOfID:        revID,
	}
}
