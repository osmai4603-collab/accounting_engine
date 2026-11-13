package sdk

import (
	"strings"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/types"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func toInternalMoney(m types.Money) (financialtypes.Money, error) {
	if m.Currency() == "" {
		return financialtypes.Money{}, nil
	}
	return financialtypes.NewMoney(m.Amount(), m.Currency())
}

func toPublicMoney(m financialtypes.Money) (types.Money, error) {
	if m.Currency() == "" {
		return types.Money{}, nil
	}
	return types.NewMoney(m.Amount(), m.Currency())
}

func mustPublicMoney(m financialtypes.Money) types.Money {
	v, err := toPublicMoney(m)
	if err != nil {
		return types.Money{}
	}
	return v
}

func toInternalEntry(e ledger.JournalEntry) (posting.JournalEntry, error) {
	lines := make([]posting.JournalEntryLine, len(e.Lines))
	for i, l := range e.Lines {
		fd, err := toInternalMoney(l.FunctionalDebit)
		if err != nil {
			return posting.JournalEntry{}, err
		}
		fc, err := toInternalMoney(l.FunctionalCredit)
		if err != nil {
			return posting.JournalEntry{}, err
		}
		ta, err := toInternalMoney(l.TransactionAmount)
		if err != nil {
			return posting.JournalEntry{}, err
		}
		cur := strings.TrimSpace(l.TransactionCurrency)
		if cur == "" {
			cur = ta.Currency()
		}
		if cur == "" {
			cur = fd.Currency()
		}
		if cur == "" {
			cur = fc.Currency()
		}
		if ta.Currency() == "" && cur != "" {
			ta, _ = financialtypes.NewMoney(decimal.Zero, cur)
		}
		if fd.Currency() == "" && cur != "" {
			fd, _ = financialtypes.NewMoney(decimal.Zero, cur)
		}
		if fc.Currency() == "" && cur != "" {
			fc, _ = financialtypes.NewMoney(decimal.Zero, cur)
		}

		lineID := uuid.UUID(l.ID)
		if lineID == uuid.Nil {
			lineID = uuid.New()
		}
		var partnerID, costCenterID, projectID, segmentID, businessUnitID *uuid.UUID
		if l.PartnerID != nil {
			id := uuid.UUID(*l.PartnerID)
			partnerID = &id
		}
		if l.CostCenterID != nil {
			id := uuid.UUID(*l.CostCenterID)
			costCenterID = &id
		}
		if l.ProjectID != nil {
			id := uuid.UUID(*l.ProjectID)
			projectID = &id
		}
		if l.SegmentID != nil {
			id := uuid.UUID(*l.SegmentID)
			segmentID = &id
		}
		if l.BusinessUnitID != nil {
			id := uuid.UUID(*l.BusinessUnitID)
			businessUnitID = &id
		}
		lines[i] = posting.JournalEntryLine{
			CompanyID:           uuid.UUID(e.CompanyID),
			ID:                  lineID,
			AccountID:           uuid.UUID(l.AccountID),
			PartnerID:           partnerID,
			CostCenterID:        costCenterID,
			ProjectID:           projectID,
			SegmentID:           segmentID,
			BusinessUnitID:      businessUnitID,
			MatchingNumber:      l.MatchingNumber,
			ReconciledAmount:    l.ReconciledAmount,
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
	var sourceEventID *uuid.UUID
	if e.ReferenceID != "" {
		if id, err := uuid.Parse(e.ReferenceID); err == nil {
			sourceEventID = &id
		}
	}

	class := posting.EntryClassRegular
	if e.Classification != "" {
		class = posting.EntryClassification(e.Classification)
	}
	status := posting.EntryDraft
	switch e.Status {
	case ledger.EntryPosted:
		status = posting.EntryPosted
	case ledger.EntryReversed:
		status = posting.EntryReversed
	case ledger.EntryDraft, "":
		status = posting.EntryDraft
	default:
		status = posting.EntryStatus(e.Status)
	}

	effective := e.EffectiveDate
	if effective.IsZero() {
		effective = e.PostingDate
	}
	if effective.IsZero() {
		effective = time.Now().UTC()
	}
	postingDate := e.PostingDate
	if postingDate.IsZero() {
		postingDate = effective
	}
	createdAt := effective

	journalID := uuid.UUID(e.JournalID)
	if journalID == uuid.Nil && e.SourceModule != "" {
		journalID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("journal-"+uuid.UUID(e.CompanyID).String()+"-"+e.SourceModule))
	}
	if journalID == uuid.Nil {
		journalID = uuid.NewSHA1(uuid.NameSpaceOID, []byte("journal-"+uuid.UUID(e.CompanyID).String()))
	}
	periodID := uuid.UUID(e.FiscalPeriodID)

	entryID := uuid.UUID(e.ID)
	if entryID == uuid.Nil {
		entryID = uuid.New()
	}

	return posting.JournalEntry{
		CompanyID:           uuid.UUID(e.CompanyID),
		ID:                  entryID,
		JournalID:           journalID,
		PeriodID:            periodID,
		EntryDate:           effective,
		Date:                postingDate,
		Status:              status,
		DocumentReference:   e.ReferenceID,
		Reference:           e.Description,
		TransactionCurrency: e.TransactionCurrency,
		ExchangeRate:        e.ExchangeRate,
		CreatedBy:           e.CreatedBy,
		CreatedAt:           createdAt,
		UpdatedAt:           createdAt,
		PostedBy:            e.ApprovedBy,
		OriginalEntryID:     revID,
		SourceEventID:       sourceEventID,
		SourceModule:        e.SourceModule,
		LegalNumber:         e.EntryNumber,
		ApprovalFingerprint: e.ApprovalFingerprint,
		Classification:      class,
		Lines:               lines,
	}, nil
}

func fromInternalEntry(e posting.JournalEntry) ledger.JournalEntry {
	lines := make([]ledger.EntryLine, len(e.Lines))
	for i, l := range e.Lines {
		var partnerID *types.PartnerID
		var costCenterID *types.CostCenterID
		var projectID *types.ProjectID
		var segmentID *types.SegmentID
		var businessUnitID *types.BusinessUnitID
		if l.PartnerID != nil {
			id := types.PartnerID(*l.PartnerID)
			partnerID = &id
		}
		if l.CostCenterID != nil {
			id := types.CostCenterID(*l.CostCenterID)
			costCenterID = &id
		}
		if l.ProjectID != nil {
			id := types.ProjectID(*l.ProjectID)
			projectID = &id
		}
		if l.SegmentID != nil {
			id := types.SegmentID(*l.SegmentID)
			segmentID = &id
		}
		if l.BusinessUnitID != nil {
			id := types.BusinessUnitID(*l.BusinessUnitID)
			businessUnitID = &id
		}
		lines[i] = ledger.EntryLine{
			ID:                  types.JournalEntryLineID(l.ID),
			AccountID:           types.AccountID(l.AccountID),
			PartnerID:           partnerID,
			CostCenterID:        costCenterID,
			ProjectID:           projectID,
			SegmentID:           segmentID,
			BusinessUnitID:      businessUnitID,
			MatchingNumber:      l.MatchingNumber,
			ReconciledAmount:    l.ReconciledAmount,
			TransactionAmount:   mustPublicMoney(l.TransactionAmount),
			TransactionCurrency: l.TransactionCurrency,
			ExchangeRate:        l.ExchangeRate,
			FunctionalDebit:     mustPublicMoney(l.FunctionalDebit),
			FunctionalCredit:    mustPublicMoney(l.FunctionalCredit),
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
	entryNumber := e.LegalNumber
	if entryNumber == "" && e.SequenceNumber > 0 {
		entryNumber = uuid.UUID(e.JournalID).String()
	}

	return ledger.JournalEntry{
		ID:                  types.JournalEntryID(e.ID),
		CompanyID:           types.CompanyID(e.CompanyID),
		JournalID:           types.JournalID(e.JournalID),
		FiscalPeriodID:      types.FiscalPeriodID(e.PeriodID),
		EntryNumber:         entryNumber,
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
		ApprovedBy:          e.PostedBy,
		ApprovalFingerprint: e.ApprovalFingerprint,
		ReversalOfID:        revID,
	}
}

func toInternalCompanyID(id types.CompanyID) uuid.UUID     { return uuid.UUID(id) }
func toInternalAccountID(id types.AccountID) uuid.UUID     { return uuid.UUID(id) }
func toInternalEntryID(id types.JournalEntryID) uuid.UUID  { return uuid.UUID(id) }
func toInternalPeriodID(id types.FiscalPeriodID) uuid.UUID { return uuid.UUID(id) }
func toPublicCompanyID(id uuid.UUID) types.CompanyID       { return types.CompanyID(id) }
func toPublicAccountID(id uuid.UUID) types.AccountID       { return types.AccountID(id) }
func toPublicEntryID(id uuid.UUID) types.JournalEntryID    { return types.JournalEntryID(id) }
func toPublicPeriodID(id uuid.UUID) types.FiscalPeriodID   { return types.FiscalPeriodID(id) }

func zeroPublicMoney(currency string) types.Money {
	if currency == "" {
		currency = "USD"
	}
	m, _ := types.NewMoney(decimal.Zero, currency)
	return m
}
