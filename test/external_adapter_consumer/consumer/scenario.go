// Package consumer is the external composition root. It constructs the
// experimental adapter, hands only its public ports to the SDK, and runs a
// complete accounting scenario through the public application service and the
// public engine.
package consumer

import (
	"context"
	"fmt"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/osmai4603-collab/accounting_engine/test/external_adapter_consumer/adapter"
	"github.com/shopspring/decimal"
)

// Result holds every intermediate value the scenario asserts on.
type Result struct {
	Fixture               adapter.Fixture
	Draft                 ledger.JournalEntry
	Posted                ledger.JournalEntry
	ReadBack              ledger.JournalEntry
	Reversal              ledger.JournalEntry
	OriginalAfterReversal ledger.JournalEntry
	TrialBalance          reporting.TrialBalance
	GeneralLedger         reporting.GeneralLedgerReport
	ExchangeRate          decimal.Decimal
}

// Run wires the SDK from the experimental adapter and executes
// draft, validate, post, get, reverse and report. It never imports an internal
// package and never implements accounting use cases itself.
func Run(ctx context.Context) (Result, error) {
	fixture := adapter.DefaultFixture()
	store, err := adapter.New(fixture)
	if err != nil {
		return Result{}, fmt.Errorf("provision experimental adapter: %w", err)
	}

	entryDate := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)
	asOf := time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)

	rate, err := store.GetRate(ctx, fixture.FunctionalCurrency, fixture.FunctionalCurrency, entryDate)
	if err != nil {
		return Result{}, fmt.Errorf("resolve exchange rate: %w", err)
	}

	// Public composition: the SDK builds the use-case layer and the engine from
	// the same adapter ports. Reporting is an optional capability of the same
	// adapter, supplied explicitly.
	ports := store.Ports()
	useCases, err := sdk.NewApplicationService(ports)
	if err != nil {
		return Result{}, fmt.Errorf("build application service: %w", err)
	}
	engine, err := sdk.NewBuilder(
		sdk.WithStoragePorts(ports),
		sdk.WithReporting(store),
	).Build(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("build engine: %w", err)
	}
	defer func() {
		_ = engine.Close()
	}()

	cashAccount, err := fixture.Account("1000")
	if err != nil {
		return Result{}, err
	}
	revenueAccount, err := fixture.Account("4000")
	if err != nil {
		return Result{}, err
	}
	amount, err := types.NewMoneyFromString("250.00", fixture.FunctionalCurrency)
	if err != nil {
		return Result{}, fmt.Errorf("create amount: %w", err)
	}
	zero, err := types.Zero(fixture.FunctionalCurrency)
	if err != nil {
		return Result{}, fmt.Errorf("create zero amount: %w", err)
	}

	commandContext := publicapplication.Context{
		Principal: publicapplication.Principal{
			ActorID:    "external-adapter-consumer",
			CompanyIDs: []types.CompanyID{fixture.Company.ID},
		},
		CorrelationID: "ext-adapter-scenario-1",
		SourceSystem:  "external-adapter-consumer",
	}

	create := publicapplication.CreateDraftCommand{
		Context:             commandContext,
		CompanyID:           fixture.Company.ID,
		JournalID:           types.JournalID(fixture.Journal.ID),
		FiscalPeriodID:      fixture.FiscalPeriod.ID,
		EffectiveDate:       entryDate,
		PostingDate:         entryDate,
		Description:         "External adapter cash sale",
		SourceModule:        "external-adapter-consumer",
		ReferenceID:         "EXT-ADAPTER-1",
		TransactionCurrency: fixture.FunctionalCurrency,
		ExchangeRate:        fixture.ExchangeRate.String(),
		Classification:      ledger.EntryClassRegular,
		Lines: []ledger.EntryLine{
			{
				ID:                  types.NewJournalEntryLineID(),
				AccountID:           cashAccount.ID,
				TransactionAmount:   amount,
				TransactionCurrency: fixture.FunctionalCurrency,
				ExchangeRate:        fixture.ExchangeRate,
				FunctionalDebit:     amount,
				FunctionalCredit:    zero,
			},
			{
				ID:                  types.NewJournalEntryLineID(),
				AccountID:           revenueAccount.ID,
				TransactionAmount:   amount,
				TransactionCurrency: fixture.FunctionalCurrency,
				ExchangeRate:        fixture.ExchangeRate,
				FunctionalDebit:     zero,
				FunctionalCredit:    amount,
			},
		},
	}

	draftResult, err := useCases.CreateDraft(ctx, create)
	if err != nil {
		return Result{}, fmt.Errorf("create draft: %w", err)
	}
	draft := draftResult.Entry

	if err := useCases.Validate(ctx, publicapplication.ValidateEntryCommand{
		Context:   commandContext,
		CompanyID: fixture.Company.ID,
		EntryID:   draft.ID,
	}); err != nil {
		return Result{}, fmt.Errorf("validate draft: %w", err)
	}

	postedResult, err := useCases.Post(ctx, publicapplication.PostEntryCommand{
		Context:   commandContext,
		CompanyID: fixture.Company.ID,
		EntryID:   draft.ID,
	})
	if err != nil {
		return Result{}, fmt.Errorf("post draft: %w", err)
	}
	posted := postedResult.Entry

	readBack, err := engine.Ledger().GetEntry(ctx, fixture.Company.ID, posted.ID)
	if err != nil {
		return Result{}, fmt.Errorf("read posted entry through the engine: %w", err)
	}

	reversalResult, err := useCases.Reverse(ctx, publicapplication.ReverseEntryCommand{
		Context:       commandContext,
		CompanyID:     fixture.Company.ID,
		EntryID:       posted.ID,
		Reason:        "external adapter scenario correction",
		EffectiveDate: entryDate,
	})
	if err != nil {
		return Result{}, fmt.Errorf("reverse posted entry: %w", err)
	}
	reversal := reversalResult.Entry

	originalAfterReversal, err := useCases.GetEntry(ctx, publicapplication.GetEntryQuery{
		Context:   commandContext,
		CompanyID: fixture.Company.ID,
		EntryID:   posted.ID,
	})
	if err != nil {
		return Result{}, fmt.Errorf("read original after reversal: %w", err)
	}

	// The original must no longer be reversible: the application layer rejects
	// a second reversal before it reaches storage.
	if _, err := useCases.Reverse(ctx, publicapplication.ReverseEntryCommand{
		Context:       commandContext,
		CompanyID:     fixture.Company.ID,
		EntryID:       posted.ID,
		Reason:        "duplicate reversal must fail",
		EffectiveDate: entryDate,
	}); err == nil {
		return Result{}, fmt.Errorf("second reversal succeeded, want a rejection")
	}

	trialBalance, err := engine.Reporting().GetTrialBalance(ctx, fixture.Company.ID, asOf, fixture.FunctionalCurrency)
	if err != nil {
		return Result{}, fmt.Errorf("get trial balance: %w", err)
	}

	generalLedger, err := engine.Reporting().GetGeneralLedger(
		ctx, fixture.Company.ID, cashAccount.ID,
		time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		return Result{}, fmt.Errorf("get general ledger: %w", err)
	}

	return Result{
		Fixture:               fixture,
		Draft:                 draft,
		Posted:                posted,
		ReadBack:              readBack,
		Reversal:              reversal,
		OriginalAfterReversal: originalAfterReversal,
		TrialBalance:          trialBalance,
		GeneralLedger:         generalLedger,
		ExchangeRate:          rate,
	}, nil
}
