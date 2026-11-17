package ifrs9

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	publictypes "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	"github.com/shopspring/decimal"
)

type applicationPostingRecorder struct {
	publicapplication.Service
	created publicapplication.CreateDraftCommand
	posted  publicapplication.PostEntryCommand
	entryID publictypes.JournalEntryID
}

func (r *applicationPostingRecorder) CreateDraft(
	_ context.Context,
	command publicapplication.CreateDraftCommand,
) (publicapplication.EntryResult, error) {
	r.created = command
	r.entryID = publictypes.NewJournalEntryID()
	return publicapplication.EntryResult{
		Entry: ledger.JournalEntry{ID: r.entryID},
	}, nil
}

func (r *applicationPostingRecorder) Post(
	_ context.Context,
	command publicapplication.PostEntryCommand,
) (publicapplication.EntryResult, error) {
	r.posted = command
	return publicapplication.EntryResult{
		Entry: ledger.JournalEntry{ID: command.EntryID, Status: ledger.EntryPosted},
	}, nil
}

func TestPostJournalEntryThroughApplication(t *testing.T) {
	companyID, journalID := uuid.New(), uuid.New()
	input := MeasurementInput{
		InstrumentID: uuid.New(), AsOf: testAsOf(), Classification: validHostClassification(AmortisedCost),
		Stage: Stage1, OpeningGrossCarrying: measurementMoney(t, "100"),
		ExistingLossAllowance: measurementMoney(t, "0"), PrincipalRepayments: measurementMoney(t, "0"),
		EffectiveInterestRate: decimal.RequireFromString("0.1"), Period: 365 * 24 * time.Hour,
	}
	result, err := MeasureSubsequentPeriod(input)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildMeasurementEntry(result, MeasurementAccounts{
		Asset: uuid.New(), Cash: uuid.New(), InterestIncome: uuid.New(),
	}, companyID, journalID, "risk_officer")
	if err != nil {
		t.Fatal(err)
	}
	app := &applicationPostingRecorder{}
	ctx := publicapplication.WithPrincipal(context.Background(), publicapplication.Principal{
		ActorID: "risk_officer", CompanyIDs: []publictypes.CompanyID{publictypes.CompanyID(companyID)},
	})
	posted, err := PostJournalEntryThroughApplication(ctx, app, entry)
	if err != nil {
		t.Fatal(err)
	}
	if posted.Entry.Status != ledger.EntryPosted || app.created.CompanyID != publictypes.CompanyID(companyID) ||
		app.created.JournalID != publictypes.JournalID(journalID) ||
		app.created.IdempotencyKey != entry.IdempotencyKey ||
		app.posted.EntryID != app.entryID {
		t.Fatalf("journal was not created and posted through the application service: result=%+v create=%+v post=%+v",
			posted, app.created, app.posted)
	}
	if len(app.created.Lines) != len(entry.Lines) || app.created.SourceModule != "ifrs9" {
		t.Fatalf("application draft does not preserve IFRS 9 entry details: %+v", app.created)
	}
}

func TestPostJournalEntryThroughApplicationRequiresAuthorizedPrincipal(t *testing.T) {
	app := &applicationPostingRecorder{}
	input := MeasurementInput{
		InstrumentID: uuid.New(), AsOf: testAsOf(), Classification: validHostClassification(AmortisedCost),
		Stage: Stage1, OpeningGrossCarrying: measurementMoney(t, "100"),
		ExistingLossAllowance: measurementMoney(t, "0"), PrincipalRepayments: measurementMoney(t, "0"),
		EffectiveInterestRate: decimal.RequireFromString("0.1"), Period: 365 * 24 * time.Hour,
	}
	result, err := MeasureSubsequentPeriod(input)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildMeasurementEntry(result, MeasurementAccounts{
		Asset: uuid.New(), Cash: uuid.New(), InterestIncome: uuid.New(),
	}, uuid.New(), uuid.New(), "risk_officer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PostJournalEntryThroughApplication(context.Background(), app, entry); !errors.Is(err, ErrApplicationPrincipalRequired) {
		t.Fatalf("principal error = %v, want ErrApplicationPrincipalRequired", err)
	}
}

func TestPostCategorizedVersionedECLThroughApplication(t *testing.T) {
	input := versionedGoldenInput(t)
	input.Exposures[1].Classification.MeasurementCategory = FVOCI
	result, err := EvaluateVersionedECL(input)
	if err != nil {
		t.Fatal(err)
	}
	companyID, journalID := uuid.New(), uuid.New()
	accounts := ECLCategoryAccounts{
		AmortisedCost: ECLAccounts{ImpairmentLoss: uuid.New(), Allowance: uuid.New()},
		FVOCI:         ECLAccounts{ImpairmentLoss: uuid.New(), OCIReserve: uuid.New()},
	}
	app := &applicationPostingRecorder{}
	ctx := publicapplication.WithPrincipal(context.Background(), publicapplication.Principal{
		ActorID: "risk_officer", CompanyIDs: []publictypes.CompanyID{publictypes.CompanyID(companyID)},
	})
	posted, err := PostCategorizedVersionedECLThroughApplication(
		ctx, app, result, accounts, companyID, journalID, "risk_officer",
	)
	if err != nil {
		t.Fatal(err)
	}
	if posted.Entry.Status != ledger.EntryPosted || len(app.created.Lines) != 4 ||
		app.created.IdempotencyKey == "" || app.posted.EntryID != app.entryID {
		t.Fatalf("categorized ECL was not posted as one application journal: result=%+v create=%+v", posted, app.created)
	}
	accountsUsed := make(map[publictypes.AccountID]bool, len(app.created.Lines))
	for _, line := range app.created.Lines {
		accountsUsed[line.AccountID] = true
	}
	for _, accountID := range []uuid.UUID{
		accounts.AmortisedCost.Allowance, accounts.FVOCI.OCIReserve,
	} {
		if !accountsUsed[publictypes.AccountID(accountID)] {
			t.Errorf("missing category-specific account %s from application journal", accountID)
		}
	}
}

func testAsOf() time.Time {
	return time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
}
