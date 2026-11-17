package ifrs9

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	publictypes "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/shopspring/decimal"
)

var (
	ErrApplicationPostingUnavailable  = errors.New("IFRS 9 application posting service is required")
	ErrApplicationPrincipalRequired   = errors.New("IFRS 9 application posting requires an authorized principal")
	ErrApplicationIdempotencyRequired = errors.New("IFRS 9 application posting requires an idempotency key")
)

// PostJournalEntryThroughApplication creates and posts an IFRS 9 draft through
// the public application service. The principal must already be present in
// ctx and authorized for the entry's company.
func PostJournalEntryThroughApplication(
	ctx context.Context,
	app publicapplication.Service,
	entry posting.JournalEntry,
) (publicapplication.EntryResult, error) {
	if app == nil {
		return publicapplication.EntryResult{}, ErrApplicationPostingUnavailable
	}
	if ctx == nil {
		return publicapplication.EntryResult{}, ErrApplicationPrincipalRequired
	}
	if entry.IdempotencyKey == "" {
		return publicapplication.EntryResult{}, ErrApplicationIdempotencyRequired
	}
	if err := entry.Validate(); err != nil {
		return publicapplication.EntryResult{}, err
	}
	principal, ok := publicapplication.PrincipalFromContext(ctx)
	if !ok || principal.ActorID == "" {
		return publicapplication.EntryResult{}, ErrApplicationPrincipalRequired
	}
	companyID := publictypes.CompanyID(entry.CompanyID)
	authorizedCompany := false
	for _, company := range principal.CompanyIDs {
		if company == companyID {
			authorizedCompany = true
			break
		}
	}
	if !authorizedCompany {
		return publicapplication.EntryResult{}, ErrApplicationPrincipalRequired
	}

	metadata, _ := publicapplication.RequestMetadataFromContext(ctx)
	commandContext := publicapplication.Context{
		Principal: principal, CorrelationID: metadata.CorrelationID,
		IdempotencyKey: entry.IdempotencyKey, SourceSystem: "ifrs9",
	}
	lines := make([]ledger.EntryLine, 0, len(entry.Lines))
	currency := ""
	for _, line := range entry.Lines {
		transactionAmount := line.TransactionAmount
		if transactionAmount.Currency() == "" {
			transactionAmount = line.Debit
			if transactionAmount.Currency() == "" || transactionAmount.Amount().IsZero() {
				transactionAmount = line.Credit
			}
		}
		transactionCurrency := line.TransactionCurrency
		if transactionCurrency == "" {
			transactionCurrency = transactionAmount.Currency()
		}
		if transactionAmount.Currency() == "" || transactionCurrency == "" ||
			transactionAmount.Currency() != transactionCurrency {
			return publicapplication.EntryResult{}, posting.ErrInvalidEntry
		}
		if currency == "" {
			currency = transactionCurrency
		} else if currency != transactionCurrency {
			return publicapplication.EntryResult{}, posting.ErrInvalidEntry
		}
		functionalDebit := line.FunctionalDebit
		if functionalDebit.Currency() == "" {
			functionalDebit = line.Debit
		}
		functionalCredit := line.FunctionalCredit
		if functionalCredit.Currency() == "" {
			functionalCredit = line.Credit
		}
		if functionalDebit.Currency() != transactionCurrency ||
			functionalCredit.Currency() != transactionCurrency {
			return publicapplication.EntryResult{}, posting.ErrInvalidEntry
		}
		publicTransactionAmount, err := publictypes.NewMoney(transactionAmount.Amount(), transactionCurrency)
		if err != nil {
			return publicapplication.EntryResult{}, err
		}
		publicFunctionalDebit, err := publictypes.NewMoney(functionalDebit.Amount(), functionalDebit.Currency())
		if err != nil {
			return publicapplication.EntryResult{}, err
		}
		publicFunctionalCredit, err := publictypes.NewMoney(functionalCredit.Amount(), functionalCredit.Currency())
		if err != nil {
			return publicapplication.EntryResult{}, err
		}
		exchangeRate := line.ExchangeRate
		if !exchangeRate.IsPositive() {
			exchangeRate = decimal.NewFromInt(1)
		}
		lines = append(lines, ledger.EntryLine{
			ID: publictypes.JournalEntryLineID(line.ID), AccountID: publictypes.AccountID(line.AccountID),
			TransactionAmount: publicTransactionAmount, TransactionCurrency: transactionCurrency,
			ExchangeRate: exchangeRate, FunctionalDebit: publicFunctionalDebit,
			FunctionalCredit: publicFunctionalCredit,
		})
	}
	if currency == "" {
		return publicapplication.EntryResult{}, posting.ErrInvalidEntry
	}
	created, err := app.CreateDraft(ctx, publicapplication.CreateDraftCommand{
		Context: commandContext, CompanyID: companyID,
		JournalID:     publictypes.JournalID(entry.JournalID),
		EffectiveDate: entry.EntryDate, PostingDate: entry.EntryDate,
		Description: entry.Reference, SourceModule: "ifrs9",
		ReferenceID: entry.DocumentReference, TransactionCurrency: currency,
		ExchangeRate: "1", Classification: ledger.EntryClassRegular, Lines: lines,
	})
	if err != nil {
		return publicapplication.EntryResult{}, fmt.Errorf("create IFRS 9 draft: %w", err)
	}
	posted, err := app.Post(ctx, publicapplication.PostEntryCommand{
		Context: commandContext, CompanyID: companyID, EntryID: created.Entry.ID,
	})
	if err != nil {
		return publicapplication.EntryResult{}, fmt.Errorf("post IFRS 9 draft: %w", err)
	}
	return posted, nil
}

// PostVersionedECLThroughApplication builds and posts a single-category ECL
// adjustment through the public application service.
func PostVersionedECLThroughApplication(
	ctx context.Context,
	app publicapplication.Service,
	result VersionedECLResult,
	accounts ECLAccounts,
	companyID, journalID uuid.UUID,
	createdBy string,
) (publicapplication.EntryResult, error) {
	entry, err := BuildVersionedECLEntry(result, accounts, companyID, journalID, createdBy)
	if err != nil {
		return publicapplication.EntryResult{}, err
	}
	return PostJournalEntryThroughApplication(ctx, app, entry)
}

// PostCategorizedVersionedECLThroughApplication posts the AC and FVOCI
// allowance movements together as one journal through the public application
// service.
func PostCategorizedVersionedECLThroughApplication(
	ctx context.Context,
	app publicapplication.Service,
	result VersionedECLResult,
	accounts ECLCategoryAccounts,
	companyID, journalID uuid.UUID,
	createdBy string,
) (publicapplication.EntryResult, error) {
	entry, err := BuildVersionedECLApplicationEntry(
		result, accounts, companyID, journalID, createdBy,
	)
	if err != nil {
		return publicapplication.EntryResult{}, err
	}
	return PostJournalEntryThroughApplication(ctx, app, entry)
}
