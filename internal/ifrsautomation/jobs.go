package ifrsautomation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/ias12"
	"github.com/osmai4603-collab/accounting_engine/internal/ifrs15"
	"github.com/osmai4603-collab/accounting_engine/internal/ifrs16"
	"github.com/osmai4603-collab/accounting_engine/internal/ifrs9"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

var (
	ErrJobConfiguration      = errors.New("invalid IFRS monthly job configuration")
	ErrContractStoreRequired = errors.New("IFRS 15 contract store is required")
	ErrRevenueScheduleState  = errors.New("IFRS 15 recognized amount does not reconcile to the schedule")
)

type ECLJob struct {
	ID     string
	Engine *ifrs9.Engine
}

func (j ECLJob) Key() string { return j.ID }

func (j ECLJob) Generate(ctx context.Context, period Period) ([]posting.JournalEntry, error) {
	if err := period.Validate(); err != nil {
		return nil, err
	}
	if j.Engine == nil {
		return nil, ErrJobConfiguration
	}
	result, err := j.Engine.Calculate(ctx, period.End)
	if err != nil {
		return nil, err
	}
	if result.JournalEntry == nil {
		return nil, nil
	}
	entry := *result.JournalEntry
	entry.Reference = "ifrs9:ecl:" + period.End.UTC().Format("2006-01")
	entry.DocumentReference = entry.Reference
	return []posting.JournalEntry{entry}, nil
}

type LeaseSchedule struct {
	ID        uuid.UUID
	Contract  ifrs16.LeaseContract
	Accounts  ifrs16.LeaseAccounts
	CompanyID uuid.UUID
	JournalID uuid.UUID
	CreatedBy string
}

type LeaseJob struct {
	ID     string
	Leases []LeaseSchedule
}

func (j LeaseJob) Key() string { return j.ID }

func (j LeaseJob) Generate(ctx context.Context, period Period) ([]posting.JournalEntry, error) {
	if err := period.Validate(); err != nil {
		return nil, err
	}
	entries := make([]posting.JournalEntry, 0)
	for _, lease := range j.Leases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if lease.ID == uuid.Nil || lease.CompanyID == uuid.Nil || lease.JournalID == uuid.Nil ||
			lease.CreatedBy == "" {
			return nil, ErrJobConfiguration
		}
		schedule, err := lease.Contract.AmortizationSchedule()
		if err != nil {
			return nil, fmt.Errorf("lease %s schedule: %w", lease.ID, err)
		}
		for _, line := range schedule {
			date := dateOnlyUTC(line.DueDate)
			if date.Before(dateOnlyUTC(period.Start)) || date.After(dateOnlyUTC(period.End)) {
				continue
			}
			payment, err := ifrs16.BuildPaymentEntry(
				line, lease.Accounts, lease.CompanyID, lease.JournalID, lease.CreatedBy,
			)
			if err != nil {
				return nil, fmt.Errorf("lease %s payment period %d: %w", lease.ID, line.Period, err)
			}
			payment.Reference = fmt.Sprintf("ifrs16:lease:%s:payment:%d", lease.ID, line.Period)
			payment.DocumentReference = payment.Reference
			entries = append(entries, payment)

			if line.ROUDepreciation.Amount().IsPositive() {
				depreciation, err := ifrs16.BuildDepreciationEntry(
					line, lease.Accounts, lease.CompanyID, lease.JournalID, lease.CreatedBy,
				)
				if err != nil {
					return nil, fmt.Errorf("lease %s depreciation period %d: %w", lease.ID, line.Period, err)
				}
				depreciation.Reference = fmt.Sprintf("ifrs16:lease:%s:depreciation:%d", lease.ID, line.Period)
				depreciation.DocumentReference = depreciation.Reference
				entries = append(entries, depreciation)
			}
		}
	}
	return entries, nil
}

type DeferredTaxJob struct {
	ID        string
	Measure   func(context.Context, Period) (ias12.Measurement, error)
	Previous  func(context.Context, Period) (ias12.DeferredTaxPosition, error)
	Accounts  ias12.DeferredTaxAccounts
	CompanyID uuid.UUID
	JournalID uuid.UUID
	CreatedBy string
}

func (j DeferredTaxJob) Key() string { return j.ID }

func (j DeferredTaxJob) Generate(ctx context.Context, period Period) ([]posting.JournalEntry, error) {
	if err := period.Validate(); err != nil {
		return nil, err
	}
	quarter := (int(period.End.Month())-1)/3 + 1
	if int(period.End.Month())%3 != 0 {
		return nil, nil
	}
	if j.Measure == nil || j.Previous == nil || j.CompanyID == uuid.Nil ||
		j.JournalID == uuid.Nil || j.CreatedBy == "" {
		return nil, ErrJobConfiguration
	}
	measurement, err := j.Measure(ctx, period)
	if err != nil {
		return nil, err
	}
	previous, err := j.Previous(ctx, period)
	if err != nil {
		return nil, err
	}
	entry, err := ias12.GenerateDeferredTaxEntry(ias12.DeferredTaxEntryInput{
		CompanyID: j.CompanyID, JournalID: j.JournalID, EntryDate: period.End,
		CreatedBy: j.CreatedBy, Previous: previous,
		Closing:  ias12.DeferredTaxPosition{DTA: measurement.DTA, DTL: measurement.DTL},
		Accounts: j.Accounts,
	})
	if errors.Is(err, ias12.ErrNoDeferredTaxMovement) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entry.Reference = fmt.Sprintf("ias12:deferred-tax:%d-Q%d", period.End.Year(), quarter)
	entry.DocumentReference = entry.Reference
	for i := range entry.Lines {
		entry.Lines[i].CompanyID = j.CompanyID
	}
	return []posting.JournalEntry{entry}, nil
}

type RevenueContractStore interface {
	ListContracts(context.Context, uuid.UUID) ([]ifrs15.Contract, error)
	SaveContract(context.Context, uuid.UUID, ifrs15.Contract) error
}

type RevenueJob struct {
	ID                     string
	Store                  RevenueContractStore
	CompanyID              uuid.UUID
	JournalID              uuid.UUID
	CreatedBy              string
	RevenueAccountID       uuid.UUID
	ContractLiabilityID    uuid.UUID
	ContractAssetID        uuid.UUID
	pendingContractUpdates []ifrs15.Contract
}

func (j *RevenueJob) Key() string {
	if j == nil {
		return ""
	}
	return j.ID
}

func (j *RevenueJob) Generate(ctx context.Context, period Period) ([]posting.JournalEntry, error) {
	if err := period.Validate(); err != nil {
		return nil, err
	}
	if j == nil || j.Store == nil || j.CompanyID == uuid.Nil || j.JournalID == uuid.Nil ||
		j.CreatedBy == "" || j.RevenueAccountID == uuid.Nil ||
		j.ContractLiabilityID == uuid.Nil || j.ContractAssetID == uuid.Nil {
		return nil, ErrContractStoreRequired
	}
	j.pendingContractUpdates = nil
	contracts, err := j.Store.ListContracts(ctx, j.CompanyID)
	if err != nil {
		return nil, err
	}
	entries := make([]posting.JournalEntry, 0)
	seen := make(map[uuid.UUID]struct{}, len(contracts))
	for _, contract := range contracts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := contract.Validate(); err != nil {
			return nil, fmt.Errorf("contract %s: %w", contract.ID, err)
		}
		if _, exists := seen[contract.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate contract %s", ErrJobConfiguration, contract.ID)
		}
		seen[contract.ID] = struct{}{}
		priorRecognized, err := zeroForCurrency(contract.Currency)
		if err != nil {
			return nil, err
		}
		currentAmount, err := zeroForCurrency(contract.Currency)
		if err != nil {
			return nil, err
		}
		type scheduledItem struct {
			obligationID uuid.UUID
			entry        ifrs15.RevenueScheduleEntry
		}
		currentEntries := make([]scheduledItem, 0)
		for _, obligation := range contract.Obligations {
			schedule, err := contract.Schedule(obligation.ID, ifrs15.Monthly)
			if err != nil {
				return nil, fmt.Errorf("contract %s obligation %s: %w", contract.ID, obligation.ID, err)
			}
			for _, scheduled := range schedule {
				date := dateOnlyUTC(scheduled.RecognitionDate)
				if date.Before(dateOnlyUTC(period.Start)) {
					priorRecognized, err = priorRecognized.Add(scheduled.AmountToRecognize)
					if err != nil {
						return nil, err
					}
					continue
				}
				if date.After(dateOnlyUTC(period.End)) {
					continue
				}
				currentAmount, err = currentAmount.Add(scheduled.AmountToRecognize)
				if err != nil {
					return nil, err
				}
				currentEntries = append(currentEntries, scheduledItem{
					obligationID: obligation.ID, entry: scheduled,
				})
			}
		}
		if !contract.RecognizedAmount.Equal(priorRecognized) {
			alreadyRecognized, err := priorRecognized.Add(currentAmount)
			if err != nil {
				return nil, err
			}
			if contract.RecognizedAmount.Equal(alreadyRecognized) {
				continue
			}
			return nil, fmt.Errorf("%w: contract %s", ErrRevenueScheduleState, contract.ID)
		}
		updated := contract
		changed := false
		for _, scheduled := range currentEntries {
			date := dateOnlyUTC(scheduled.entry.RecognitionDate)
			entry, err := ifrs15.BuildRecognitionJournal(
				updated, scheduled.entry.AmountToRecognize, j.JournalID, date,
				j.RevenueAccountID, j.ContractLiabilityID, j.ContractAssetID,
			)
			if err != nil {
				return nil, fmt.Errorf("contract %s obligation %s recognition: %w", contract.ID, scheduled.obligationID, err)
			}
			entry.CompanyID = j.CompanyID
			entry.CreatedBy = j.CreatedBy
			entry.Reference = fmt.Sprintf(
				"ifrs15:contract:%s:obligation:%s:%s",
				contract.ID, scheduled.obligationID, date.Format("2006-01-02"),
			)
			entry.DocumentReference = entry.Reference
			for i := range entry.Lines {
				entry.Lines[i].CompanyID = j.CompanyID
			}
			entries = append(entries, entry)
			updated, err = updated.ApplyRecognition(scheduled.entry.AmountToRecognize)
			if err != nil {
				return nil, err
			}
			changed = true
		}
		if changed {
			j.pendingContractUpdates = append(j.pendingContractUpdates, updated)
		}
	}
	return entries, nil
}

func (j *RevenueJob) Complete(ctx context.Context, _ Period) error {
	if j == nil || j.Store == nil {
		return ErrContractStoreRequired
	}
	for _, contract := range j.pendingContractUpdates {
		if err := j.Store.SaveContract(ctx, j.CompanyID, contract); err != nil {
			return fmt.Errorf("save contract %s recognition state: %w", contract.ID, err)
		}
	}
	j.pendingContractUpdates = nil
	return nil
}

func dateOnlyUTC(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func zeroForCurrency(currency string) (financialtypes.Money, error) {
	return financialtypes.Zero(currency)
}
