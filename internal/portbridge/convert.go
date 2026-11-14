// Package portbridge translates between the stable public DTOs and the
// internal domain models. All conversions are explicit: no money, identifier or
// timestamp error is swallowed. Only explicit draft creation may generate
// entry and line IDs; missing journal IDs are never fabricated.
package portbridge

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
)

var (
	ErrMissingCompanyID      = errors.New("company id is required")
	ErrMissingEntryID        = errors.New("entry id is required")
	ErrMissingJournalID      = errors.New("journal id is required")
	ErrMissingPeriodID       = errors.New("fiscal period id is required")
	ErrMissingLineID         = errors.New("journal entry line id is required")
	ErrMissingAccountID      = errors.New("account id is required")
	ErrMissingCreatedAt      = errors.New("created timestamp is required")
	ErrMissingEffectiveDate  = errors.New("effective date is required")
	ErrMissingClock          = errors.New("clock is required to generate created timestamp")
	ErrMissingMoneyCurrency  = errors.New("money currency is required")
	ErrInvalidEntryStatus    = errors.New("invalid journal entry status")
	ErrInvalidClassification = errors.New("invalid journal entry classification")
)

// EntryOptions controls identity assignment during conversion.
type EntryOptions struct {
	// GenerateMissingIDs assigns entry and line IDs for a new draft. A missing
	// fiscal period remains missing; this must be false for existing entries.
	GenerateMissingIDs bool
	// Now supplies the clock used to assign a missing created timestamp in
	// draft-creation mode.
	Now func() time.Time
}

// ToInternalEntry converts a public entry into the internal domain model.
func ToInternalEntry(e ledger.JournalEntry, opts EntryOptions) (posting.JournalEntry, error) {
	if e.CompanyID == (types.CompanyID{}) || uuid.UUID(e.CompanyID) == uuid.Nil {
		return posting.JournalEntry{}, ErrMissingCompanyID
	}
	if e.JournalID == (types.JournalID{}) || uuid.UUID(e.JournalID) == uuid.Nil {
		return posting.JournalEntry{}, ErrMissingJournalID
	}
	if e.FiscalPeriodID == (types.FiscalPeriodID{}) || uuid.UUID(e.FiscalPeriodID) == uuid.Nil {
		if !opts.GenerateMissingIDs {
			return posting.JournalEntry{}, ErrMissingPeriodID
		}
	}
	if e.EffectiveDate.IsZero() {
		return posting.JournalEntry{}, ErrMissingEffectiveDate
	}
	entryID := uuid.UUID(e.ID)
	if entryID == uuid.Nil {
		if !opts.GenerateMissingIDs {
			return posting.JournalEntry{}, ErrMissingEntryID
		}
		entryID = uuid.New()
	}
	createdAt := e.CreatedAt
	if createdAt.IsZero() {
		if !opts.GenerateMissingIDs {
			return posting.JournalEntry{}, ErrMissingCreatedAt
		}
		if opts.Now == nil {
			return posting.JournalEntry{}, ErrMissingClock
		}
		createdAt = opts.Now().UTC()
	}

	status := posting.EntryStatus(e.Status)
	switch status {
	case "", posting.EntryDraft:
		status = posting.EntryDraft
	case posting.EntryPosted, posting.EntryReversed:
	default:
		return posting.JournalEntry{}, fmt.Errorf("%w: %q", ErrInvalidEntryStatus, e.Status)
	}
	classification := posting.EntryClassification(e.Classification)
	switch classification {
	case "":
		classification = posting.EntryClassRegular
	case posting.EntryClassRegular, posting.EntryClassAdjusting, posting.EntryClassClosing,
		posting.EntryClassOpening, posting.EntryClassReversing:
	default:
		return posting.JournalEntry{}, fmt.Errorf("%w: %q", ErrInvalidClassification, e.Classification)
	}

	lines := make([]posting.JournalEntryLine, len(e.Lines))
	for i, l := range e.Lines {
		lineID := uuid.UUID(l.ID)
		if lineID == uuid.Nil {
			if !opts.GenerateMissingIDs {
				return posting.JournalEntry{}, fmt.Errorf("%w: line %d", ErrMissingLineID, i)
			}
			lineID = uuid.New()
		}
		if l.AccountID == (types.AccountID{}) || uuid.UUID(l.AccountID) == uuid.Nil {
			return posting.JournalEntry{}, fmt.Errorf("%w: line %d", ErrMissingAccountID, i)
		}
		fd, err := toInternalMoney(l.FunctionalDebit)
		if err != nil {
			return posting.JournalEntry{}, fmt.Errorf("line %d functional debit: %w", i, err)
		}
		fc, err := toInternalMoney(l.FunctionalCredit)
		if err != nil {
			return posting.JournalEntry{}, fmt.Errorf("line %d functional credit: %w", i, err)
		}
		ta, err := toInternalMoney(l.TransactionAmount)
		if err != nil {
			return posting.JournalEntry{}, fmt.Errorf("line %d transaction amount: %w", i, err)
		}
		currency := l.TransactionCurrency

		lines[i] = posting.JournalEntryLine{
			CompanyID:           uuid.UUID(e.CompanyID),
			ID:                  lineID,
			AccountID:           uuid.UUID(l.AccountID),
			PartnerID:           internalID(l.PartnerID),
			CostCenterID:        internalID(l.CostCenterID),
			ProjectID:           internalID(l.ProjectID),
			SegmentID:           internalID(l.SegmentID),
			BusinessUnitID:      internalID(l.BusinessUnitID),
			MatchingNumber:      l.MatchingNumber,
			ReconciledAmount:    l.ReconciledAmount,
			TransactionAmount:   ta,
			TransactionCurrency: currency,
			ExchangeRate:        l.ExchangeRate,
			FunctionalDebit:     fd,
			FunctionalCredit:    fc,
		}
	}

	internal := posting.JournalEntry{
		CompanyID:           uuid.UUID(e.CompanyID),
		ID:                  entryID,
		JournalID:           uuid.UUID(e.JournalID),
		PeriodID:            uuid.UUID(e.FiscalPeriodID),
		EntryDate:           e.EffectiveDate.UTC(),
		Date:                e.PostingDate.UTC(),
		Status:              status,
		SourceModule:        e.SourceModule,
		DocumentReference:   e.ReferenceID,
		Reference:           e.Description,
		TransactionCurrency: e.TransactionCurrency,
		ExchangeRate:        e.ExchangeRate,
		ApprovalFingerprint: e.ApprovalFingerprint,
		CreatedBy:           e.CreatedBy,
		ApprovedBy:          e.ApprovedBy,
		ApprovedAt:          e.ApprovedAt,
		PostedBy:            e.PostedBy,
		PostedAt:            e.PostedAt,
		CreatedAt:           createdAt,
		UpdatedAt:           e.UpdatedAt,
		Classification:      classification,
		LegalNumber:         e.EntryNumber,
		SequenceNumber:      e.SequenceNumber,
		OriginalEntryID:     internalID(e.ReversalOfID),
		ReversedEntryID:     internalID(e.ReversedByID),
		ReversalReason:      e.ReversalReason,
		Lines:               lines,
	}
	return internal, nil
}

// ToPublicEntry converts an internal entry into the stable public DTO.
// Money conversion errors are returned, never replaced with zero values.
func ToPublicEntry(e posting.JournalEntry) (ledger.JournalEntry, error) {
	if e.CompanyID == uuid.Nil {
		return ledger.JournalEntry{}, ErrMissingCompanyID
	}
	if e.ID == uuid.Nil {
		return ledger.JournalEntry{}, ErrMissingEntryID
	}
	if e.JournalID == uuid.Nil {
		return ledger.JournalEntry{}, ErrMissingJournalID
	}
	if e.PeriodID == uuid.Nil {
		return ledger.JournalEntry{}, ErrMissingPeriodID
	}
	lines := make([]ledger.EntryLine, len(e.Lines))
	for i, l := range e.Lines {
		if l.ID == uuid.Nil {
			return ledger.JournalEntry{}, fmt.Errorf("%w: line %d", ErrMissingLineID, i)
		}
		if l.AccountID == uuid.Nil {
			return ledger.JournalEntry{}, fmt.Errorf("%w: line %d", ErrMissingAccountID, i)
		}
		ta, err := toPublicMoney(l.TransactionAmount)
		if err != nil {
			return ledger.JournalEntry{}, fmt.Errorf("line %d transaction amount: %w", i, err)
		}
		fd, err := toPublicMoney(l.FunctionalDebit)
		if err != nil {
			return ledger.JournalEntry{}, fmt.Errorf("line %d functional debit: %w", i, err)
		}
		fc, err := toPublicMoney(l.FunctionalCredit)
		if err != nil {
			return ledger.JournalEntry{}, fmt.Errorf("line %d functional credit: %w", i, err)
		}
		lines[i] = ledger.EntryLine{
			ID:                  types.JournalEntryLineID(l.ID),
			AccountID:           types.AccountID(l.AccountID),
			PartnerID:           publicID[types.PartnerID](l.PartnerID),
			CostCenterID:        publicID[types.CostCenterID](l.CostCenterID),
			ProjectID:           publicID[types.ProjectID](l.ProjectID),
			SegmentID:           publicID[types.SegmentID](l.SegmentID),
			BusinessUnitID:      publicID[types.BusinessUnitID](l.BusinessUnitID),
			MatchingNumber:      l.MatchingNumber,
			ReconciledAmount:    l.ReconciledAmount,
			TransactionAmount:   ta,
			TransactionCurrency: l.TransactionCurrency,
			ExchangeRate:        l.ExchangeRate,
			FunctionalDebit:     fd,
			FunctionalCredit:    fc,
		}
	}
	entryNumber := e.LegalNumber
	if entryNumber == "" && e.SequenceNumber > 0 {
		entryNumber = strconv.FormatInt(e.SequenceNumber, 10)
	}
	return ledger.JournalEntry{
		ID:                  types.JournalEntryID(e.ID),
		CompanyID:           types.CompanyID(e.CompanyID),
		JournalID:           types.JournalID(e.JournalID),
		FiscalPeriodID:      types.FiscalPeriodID(e.PeriodID),
		EntryNumber:         entryNumber,
		SequenceNumber:      e.SequenceNumber,
		LegalNumber:         e.LegalNumber,
		Status:              ledger.EntryStatus(e.Status),
		Classification:      ledger.EntryClassification(e.Classification),
		EffectiveDate:       e.EntryDate,
		PostingDate:         e.Date,
		Description:         e.Reference,
		SourceModule:        e.SourceModule,
		ReferenceID:         e.DocumentReference,
		TransactionCurrency: e.TransactionCurrency,
		ExchangeRate:        e.ExchangeRate,
		Lines:               lines,
		CreatedBy:           e.CreatedBy,
		CreatedAt:           e.CreatedAt,
		UpdatedAt:           e.UpdatedAt,
		ApprovedBy:          e.ApprovedBy,
		ApprovedAt:          e.ApprovedAt,
		PostedBy:            e.PostedBy,
		PostedAt:            e.PostedAt,
		ApprovalFingerprint: e.ApprovalFingerprint,
		ReversalOfID:        publicID[types.JournalEntryID](e.OriginalEntryID),
		ReversedByID:        publicID[types.JournalEntryID](e.ReversedEntryID),
		ReversalReason:      e.ReversalReason,
	}, nil
}

func toInternalMoney(m types.Money) (financialtypes.Money, error) {
	if m.Currency() == "" {
		return financialtypes.Money{}, ErrMissingMoneyCurrency
	}
	return financialtypes.NewMoney(m.Amount(), m.Currency())
}

func toPublicMoney(m financialtypes.Money) (types.Money, error) {
	if m.Currency() == "" {
		return types.Money{}, ErrMissingMoneyCurrency
	}
	return types.NewMoney(m.Amount(), m.Currency())
}

func internalID[T ~[16]byte](id *T) *uuid.UUID {
	if id == nil {
		return nil
	}
	value := uuid.UUID(*id)
	if value == uuid.Nil {
		return nil
	}
	return &value
}

func publicID[T ~[16]byte](id *uuid.UUID) *T {
	if id == nil || *id == uuid.Nil {
		return nil
	}
	value := T(*id)
	return &value
}

// ToInternalCompany converts public company master data.
func ToInternalCompany(c publicmasterdata.Company) masterdata.Company {
	return masterdata.Company{
		ID:                   uuid.UUID(c.ID),
		Code:                 c.Code,
		LegalName:            c.LegalName,
		FunctionalCurrency:   c.FunctionalCurrency,
		PresentationCurrency: c.PresentationCurrency,
		Active:               c.Active,
	}
}

// ToPublicCompany converts internal company master data.
func ToPublicCompany(c masterdata.Company) publicmasterdata.Company {
	return publicmasterdata.Company{
		ID:                   types.CompanyID(c.ID),
		Code:                 c.Code,
		LegalName:            c.LegalName,
		FunctionalCurrency:   c.FunctionalCurrency,
		PresentationCurrency: c.PresentationCurrency,
		Active:               c.Active,
	}
}

// ToInternalAccount converts public account master data.
func ToInternalAccount(a publicmasterdata.Account) masterdata.Account {
	account := masterdata.Account{
		CompanyID:           uuid.UUID(a.CompanyID),
		ID:                  uuid.UUID(a.ID),
		Code:                a.Code,
		Name:                a.Name,
		AccountType:         masterdata.AccountType(a.Type),
		NormalBalance:       masterdata.NormalBalance(a.NormalBalance),
		CurrencyID:          a.Currency,
		IsReconcilable:      a.IsReconcilable,
		IsControlAccount:    a.IsControlAccount,
		IsContraAccount:     a.IsContraAccount,
		IsActive:            a.IsActive,
		RequiresCostCenter:  a.RequiresCostCenter,
		IsCashEquivalent:    a.IsCashEquivalent,
		IsNonCashAdjustment: a.IsNonCashAdjustment,
		CashFlowActivity:    a.CashFlowActivity,
		SubClassification:   a.SubClassification,
		IFRSCategory:        a.IFRSCategory,
		DisplayOrder:        a.DisplayOrder,
	}
	if a.ParentID != nil {
		parent := uuid.UUID(*a.ParentID)
		account.ParentID = &parent
	}
	return account
}

// ToPublicAccount converts internal account master data.
func ToPublicAccount(a masterdata.Account) publicmasterdata.Account {
	account := publicmasterdata.Account{
		ID:                  types.AccountID(a.ID),
		CompanyID:           types.CompanyID(a.CompanyID),
		Code:                a.Code,
		Name:                a.Name,
		Type:                publicmasterdata.AccountType(a.AccountType),
		NormalBalance:       publicmasterdata.NormalBalance(a.NormalBalance),
		Currency:            a.CurrencyID,
		IsReconcilable:      a.IsReconcilable,
		IsControlAccount:    a.IsControlAccount,
		IsContraAccount:     a.IsContraAccount,
		IsActive:            a.IsActive,
		RequiresCostCenter:  a.RequiresCostCenter,
		IsCashEquivalent:    a.IsCashEquivalent,
		IsNonCashAdjustment: a.IsNonCashAdjustment,
		CashFlowActivity:    a.CashFlowActivity,
		SubClassification:   a.SubClassification,
		IFRSCategory:        a.IFRSCategory,
		DisplayOrder:        a.DisplayOrder,
	}
	if a.ParentID != nil {
		parent := types.AccountID(*a.ParentID)
		account.ParentID = &parent
	}
	return account
}

// ToInternalJournal converts public journal master data.
func ToInternalJournal(j publicmasterdata.Journal) masterdata.Journal {
	return masterdata.Journal{
		CompanyID: uuid.UUID(j.CompanyID),
		ID:        uuid.UUID(j.ID),
		Code:      j.Code,
		Name:      j.Name,
	}
}

// ToPublicJournal converts internal journal master data.
func ToPublicJournal(j masterdata.Journal) publicmasterdata.Journal {
	return publicmasterdata.Journal{
		CompanyID: types.CompanyID(j.CompanyID),
		ID:        publicmasterdata.JournalID(j.ID),
		Code:      j.Code,
		Name:      j.Name,
	}
}

// ToInternalFiscalYear converts public fiscal year master data.
func ToInternalFiscalYear(y publicmasterdata.FiscalYear) masterdata.FiscalYear {
	result := masterdata.FiscalYear{
		CompanyID: uuid.UUID(y.CompanyID),
		ID:        uuid.UUID(y.ID),
		Name:      y.Name,
		StartDate: y.StartDate,
		EndDate:   y.EndDate,
		State:     masterdata.FiscalYearState(y.State),
	}
	return result
}

// ToPublicFiscalYear converts internal fiscal year master data.
func ToPublicFiscalYear(y masterdata.FiscalYear) publicmasterdata.FiscalYear {
	result := publicmasterdata.FiscalYear{
		CompanyID: types.CompanyID(y.CompanyID),
		ID:        types.FiscalYearID(y.ID),
		Name:      y.Name,
		StartDate: y.StartDate,
		EndDate:   y.EndDate,
		State:     publicmasterdata.FiscalYearState(y.State),
	}
	return result
}

// ToInternalFiscalPeriod converts public fiscal period master data.
func ToInternalFiscalPeriod(p publicmasterdata.FiscalPeriod) masterdata.FiscalPeriod {
	return masterdata.FiscalPeriod{
		CompanyID:    uuid.UUID(p.CompanyID),
		ID:           uuid.UUID(p.ID),
		FiscalYearID: uuid.UUID(p.FiscalYearID),
		Number:       p.Number,
		Name:         p.Name,
		StartDate:    p.StartDate,
		EndDate:      p.EndDate,
		State:        masterdata.PeriodState(p.State),
		Type:         masterdata.FiscalPeriodType(p.Type),
	}
}

// ToPublicFiscalPeriod converts internal fiscal period master data.
func ToPublicFiscalPeriod(p masterdata.FiscalPeriod) publicmasterdata.FiscalPeriod {
	return publicmasterdata.FiscalPeriod{
		ID:           types.FiscalPeriodID(p.ID),
		CompanyID:    types.CompanyID(p.CompanyID),
		FiscalYearID: types.FiscalYearID(p.FiscalYearID),
		Number:       p.Number,
		Name:         p.Name,
		StartDate:    p.StartDate,
		EndDate:      p.EndDate,
		State:        publicmasterdata.PeriodState(p.State),
		Type:         publicmasterdata.FiscalPeriodType(p.Type),
	}
}
