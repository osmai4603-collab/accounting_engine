package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	memoryadapter "github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/shopspring/decimal"
)

type testAuthorizer struct {
	actions  []application.Action
	requests []application.AuthorizationRequest
}

func (a *testAuthorizer) Authorize(_ context.Context, request application.AuthorizationRequest) error {
	a.actions = append(a.actions, request.Action)
	a.requests = append(a.requests, request)
	return nil
}

func TestCommandContractsSeparateCallerIdentityFromReversalReason(t *testing.T) {
	principal := application.Principal{
		ActorID:    "actor-42",
		TenantID:   "tenant-7",
		CompanyIDs: []types.CompanyID{types.NewCompanyID()},
	}
	cmd := application.ReverseEntryCommand{
		Context: application.Context{
			Principal:      principal,
			CorrelationID:  "corr-900",
			IdempotencyKey: "idem-900",
			SourceSystem:   "sdk",
		},
		CompanyID:     types.NewCompanyID(),
		EntryID:       types.NewJournalEntryID(),
		Reason:        "duplicate correction",
		EffectiveDate: time.Now().UTC(),
	}

	if cmd.Context.Principal.ActorID == cmd.Reason {
		t.Fatal("actor identity and business reason must remain distinct fields")
	}
	if cmd.Context.CorrelationID == "" || cmd.Context.IdempotencyKey == "" || cmd.Context.SourceSystem == "" {
		t.Fatal("context metadata for idempotency and correlation must be explicit")
	}
	if cmd.Context.Principal.ActorID == "" {
		t.Fatal("principal actor id is required")
	}
}

func TestRequestMetadataSurvivesContextPropagation(t *testing.T) {
	want := application.RequestMetadata{
		CorrelationID: "corr-1", IdempotencyKey: "idem-1", SourceSystem: "accounting-service",
	}
	ctx := application.WithRequestMetadata(context.Background(), want)
	got, ok := application.RequestMetadataFromContext(ctx)
	if !ok || got != want {
		t.Fatalf("request metadata = %#v, present=%t; want %#v", got, ok, want)
	}
}

func TestCreateDraftCommandCarriesFullEntryIntent(t *testing.T) {
	companyID := types.NewCompanyID()
	periodID := types.NewFiscalPeriodID()
	amount, err := types.NewMoneyFromString("100.00", "USD")
	if err != nil {
		t.Fatalf("create amount: %v", err)
	}
	zero, err := types.NewMoneyFromString("0.00", "USD")
	if err != nil {
		t.Fatalf("create zero amount: %v", err)
	}
	cmd := application.CreateDraftCommand{
		Context: application.Context{
			Principal: application.Principal{ActorID: "actor-2"},
		},
		CompanyID:           companyID,
		JournalID:           types.NewJournalID(),
		FiscalPeriodID:      periodID,
		EffectiveDate:       time.Now().UTC(),
		PostingDate:         time.Now().UTC(),
		Description:         "Cash sale",
		SourceModule:        "sdk",
		ReferenceID:         "ref-1",
		TransactionCurrency: "USD",
		ExchangeRate:        "1.0000",
		Classification:      ledger.EntryClassRegular,
		Lines: []ledger.EntryLine{{
			ID:                  types.NewJournalEntryLineID(),
			AccountID:           types.NewAccountID(),
			TransactionAmount:   amount,
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.RequireFromString("1.00"),
			FunctionalDebit:     amount,
			FunctionalCredit:    zero,
		}},
	}

	if cmd.CompanyID == (types.CompanyID{}) {
		t.Fatal("company id is required")
	}
	if cmd.JournalID == (types.JournalID{}) {
		t.Fatal("journal id is required")
	}
	if len(cmd.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(cmd.Lines))
	}
}

func TestServiceCreateDraftAndPost(t *testing.T) {
	ctx := context.Background()
	driver := memoryadapter.New()
	companyID := types.CompanyID(uuid.New())
	journalID := publicmasterdata.NewJournalID()
	periodID := types.FiscalPeriodID(uuid.New())
	yearID := types.FiscalYearID(uuid.New())
	cashAccountID := types.AccountID(uuid.New())
	revenueAccountID := types.AccountID(uuid.New())
	masterData := driver.MasterDataStore()

	if err := masterData.CreateCompany(ctx, publicmasterdata.Company{
		ID: companyID, Code: "PORTS", LegalName: "Ports Ltd",
		FunctionalCurrency: "USD", PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatalf("create company: %v", err)
	}
	if err := masterData.CreateJournal(ctx, publicmasterdata.Journal{
		ID: journalID, CompanyID: companyID, Code: "GENERAL", Name: "General Journal",
	}); err != nil {
		t.Fatalf("create journal: %v", err)
	}
	if err := masterData.CreateAccount(ctx, publicmasterdata.Account{
		ID: cashAccountID, CompanyID: companyID, Code: "1000", Name: "Cash",
		Type: publicmasterdata.AccountTypeAsset, NormalBalance: publicmasterdata.NormalBalanceDebit,
		Currency: "USD", IsActive: true,
	}); err != nil {
		t.Fatalf("create cash account: %v", err)
	}
	if err := masterData.CreateAccount(ctx, publicmasterdata.Account{
		ID: revenueAccountID, CompanyID: companyID, Code: "4000", Name: "Revenue",
		Type: publicmasterdata.AccountTypeIncome, NormalBalance: publicmasterdata.NormalBalanceCredit,
		Currency: "USD", IsActive: true,
	}); err != nil {
		t.Fatalf("create revenue account: %v", err)
	}
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := masterData.CreateFiscalYear(ctx, publicmasterdata.FiscalYear{
		ID: yearID, CompanyID: companyID, Name: "FY2026",
		StartDate: start, EndDate: time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC),
		State: publicmasterdata.FiscalYearOpen,
	}); err != nil {
		t.Fatalf("create fiscal year: %v", err)
	}
	if err := masterData.CreateFiscalPeriod(ctx, publicmasterdata.FiscalPeriod{
		ID: types.FiscalPeriodID(periodID), CompanyID: companyID, FiscalYearID: yearID,
		Number: 1, Name: "January 2026", StartDate: start,
		EndDate: time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
		State:   publicmasterdata.PeriodOpen, Type: publicmasterdata.FiscalPeriodTypeNormal,
	}); err != nil {
		t.Fatalf("create fiscal period: %v", err)
	}

	authorizer := &testAuthorizer{}
	service, err := sdk.NewApplicationServiceWithAuthorizer(driver.Ports(), authorizer)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if _, err := service.CreateDraft(ctx, application.CreateDraftCommand{
		Context:   application.Context{Principal: application.Principal{ActorID: "maker"}},
		CompanyID: companyID,
	}); !errors.Is(err, application.ErrForbidden) {
		t.Fatalf("create draft without company membership error = %v, want %v", err, application.ErrForbidden)
	}

	amount, err := types.NewMoneyFromString("42.00", "USD")
	if err != nil {
		t.Fatalf("money: %v", err)
	}
	zero, err := types.NewMoneyFromString("0.00", "USD")
	if err != nil {
		t.Fatalf("zero money: %v", err)
	}
	adjustingService, ok := service.(application.AdjustingEntryService)
	if !ok {
		t.Fatal("application service does not expose adjusting-entry creation")
	}
	adjustingCommand := application.CreateAdjustingEntryCommand{
		Draft: application.CreateDraftCommand{
			Context: application.Context{Principal: application.Principal{
				ActorID: "adjuster", CompanyIDs: []types.CompanyID{companyID},
			}},
			CompanyID: companyID, JournalID: types.JournalID(journalID),
			FiscalPeriodID: types.FiscalPeriodID(periodID),
			EffectiveDate:  start.AddDate(0, 0, 3),
			PostingDate:    start.AddDate(0, 0, 3),
			Description:    "Month-end accrual", TransactionCurrency: "USD",
			ExchangeRate: "1.0",
			Lines: []ledger.EntryLine{{
				ID: types.NewJournalEntryLineID(), AccountID: cashAccountID,
				TransactionAmount: amount, TransactionCurrency: "USD",
				ExchangeRate: decimal.RequireFromString("1.00"), FunctionalDebit: amount, FunctionalCredit: zero,
			}, {
				ID: types.NewJournalEntryLineID(), AccountID: revenueAccountID,
				TransactionAmount: amount, TransactionCurrency: "USD",
				ExchangeRate: decimal.RequireFromString("1.00"), FunctionalDebit: zero, FunctionalCredit: amount,
			}},
		},
		Type: ledger.AdjustingEntryAccrual, Reason: "Services received before invoice",
		ApprovalReference: "APP-2026-03",
	}
	adjusting, err := adjustingService.CreateAdjustingEntry(ctx, adjustingCommand)
	if err != nil {
		t.Fatalf("create adjusting entry: %v", err)
	}
	if adjusting.Entry.Classification != ledger.EntryClassAdjusting ||
		adjusting.Entry.AdjustingEntryType != ledger.AdjustingEntryAccrual ||
		adjusting.Entry.AdjustmentReason != adjustingCommand.Reason ||
		adjusting.Entry.AdjustmentApprovalReference != adjustingCommand.ApprovalReference ||
		!adjusting.Entry.AdjustmentAuthorized || adjusting.Entry.ApprovedBy != "adjuster" ||
		adjusting.Entry.ApprovedAt == nil {
		t.Fatalf("adjusting-entry approval metadata missing: %+v", adjusting.Entry)
	}
	if len(authorizer.actions) == 0 || authorizer.actions[len(authorizer.actions)-1] != application.ActionCreateAdjustment {
		t.Fatalf("adjustment action was not delegated to host authorizer: %v", authorizer.actions)
	}
	if authorizer.requests[0].ApprovalReference != adjustingCommand.ApprovalReference {
		t.Fatalf("host authorizer received approval reference %q, want %q",
			authorizer.requests[0].ApprovalReference, adjustingCommand.ApprovalReference)
	}
	withoutAuthorizer, err := sdk.NewApplicationService(driver.Ports())
	if err != nil {
		t.Fatalf("new service without authorizer: %v", err)
	}
	withoutHostAuthorization, ok := withoutAuthorizer.(application.AdjustingEntryService)
	if !ok {
		t.Fatal("application service does not expose adjusting-entry creation")
	}
	if _, err := withoutHostAuthorization.CreateAdjustingEntry(ctx, adjustingCommand); !errors.Is(err, application.ErrForbidden) {
		t.Fatalf("adjusting entry without host authorizer error = %v, want forbidden", err)
	}
	if _, err := service.CreateDraft(ctx, application.CreateDraftCommand{
		Context: application.Context{Principal: application.Principal{
			ActorID: "adjuster", CompanyIDs: []types.CompanyID{companyID},
		}},
		CompanyID: companyID, Classification: ledger.EntryClassAdjusting,
	}); !errors.Is(err, application.ErrForbidden) {
		t.Fatalf("generic adjusting-entry creation error = %v, want forbidden", err)
	}

	created, err := service.CreateDraft(ctx, application.CreateDraftCommand{
		Context:             application.Context{Principal: application.Principal{ActorID: "maker", CompanyIDs: []types.CompanyID{companyID}}},
		CompanyID:           companyID,
		JournalID:           types.JournalID(journalID),
		FiscalPeriodID:      types.FiscalPeriodID(periodID),
		EffectiveDate:       start.AddDate(0, 0, 2),
		PostingDate:         start.AddDate(0, 0, 2),
		Description:         "Cash sale",
		SourceModule:        "sdk",
		ReferenceID:         "ref-42",
		TransactionCurrency: "USD",
		ExchangeRate:        "1.0",
		Classification:      ledger.EntryClassRegular,
		Lines: []ledger.EntryLine{{
			ID:                  types.NewJournalEntryLineID(),
			AccountID:           cashAccountID,
			TransactionAmount:   amount,
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.RequireFromString("1.00"),
			FunctionalDebit:     amount,
			FunctionalCredit:    zero,
		}, {
			ID:                  types.NewJournalEntryLineID(),
			AccountID:           revenueAccountID,
			TransactionAmount:   amount,
			TransactionCurrency: "USD",
			ExchangeRate:        decimal.RequireFromString("1.00"),
			FunctionalDebit:     zero,
			FunctionalCredit:    amount,
		}},
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if created.Entry.ID == (types.JournalEntryID{}) {
		t.Fatal("draft entry id is required")
	}
	if err := service.Validate(ctx, application.ValidateEntryCommand{
		Context:   application.Context{Principal: application.Principal{ActorID: "maker", CompanyIDs: []types.CompanyID{companyID}}},
		CompanyID: companyID,
		EntryID:   created.Entry.ID,
	}); err != nil {
		t.Fatalf("validate draft: %v", err)
	}
	posted, err := service.Post(ctx, application.PostEntryCommand{
		Context:   application.Context{Principal: application.Principal{ActorID: "checker", CompanyIDs: []types.CompanyID{companyID}}},
		CompanyID: companyID,
		EntryID:   created.Entry.ID,
	})
	if err != nil {
		t.Fatalf("post entry: %v", err)
	}
	if posted.Entry.Status != ledger.EntryPosted {
		t.Fatalf("expected posted entry status, got %s", posted.Entry.Status)
	}
	reversalCommand := application.ReverseEntryCommand{
		Context: application.Context{
			Principal:      application.Principal{ActorID: "reverser", CompanyIDs: []types.CompanyID{companyID}},
			CorrelationID:  "case-42",
			IdempotencyKey: "reverse-42",
			SourceSystem:   "billing",
		},
		CompanyID:     companyID,
		EntryID:       posted.Entry.ID,
		Reason:        "duplicate invoice",
		EffectiveDate: start.AddDate(0, 0, 3),
	}
	invalidReason := reversalCommand
	invalidReason.Reason = "\n"
	if _, err := service.Reverse(ctx, invalidReason); err == nil {
		t.Fatal("reversal accepted a reason containing control characters")
	}
	reversed, err := service.Reverse(ctx, reversalCommand)
	if err != nil {
		t.Fatalf("reverse entry: %v", err)
	}
	if reversed.Entry.CreatedBy != "reverser" || reversed.Entry.PostedBy != "reverser" ||
		reversed.Entry.ReversalReason != "duplicate invoice" || reversed.Entry.PostedAt == nil ||
		reversed.Entry.Description != posted.Entry.Description || reversed.Entry.ReferenceID != posted.Entry.ReferenceID ||
		reversed.IdempotentReplay {
		t.Fatalf("reversal actor/reason metadata is incorrect: %+v", reversed)
	}
	replayed, err := service.Reverse(ctx, reversalCommand)
	if err != nil {
		t.Fatalf("replay reversal: %v", err)
	}
	if replayed.Entry.ID != reversed.Entry.ID || !replayed.IdempotentReplay {
		t.Fatalf("replay result = %+v, want same reversal marked as replay", replayed)
	}
	conflictingCommand := reversalCommand
	conflictingCommand.Reason = "incorrect invoice"
	if _, err := service.Reverse(ctx, conflictingCommand); !errors.Is(err, posting.ErrIdempotencyConflict) {
		t.Fatalf("same idempotency key with changed reason error = %v, want conflict", err)
	}
	if _, err := service.GetEntry(ctx, application.GetEntryQuery{
		Context:   application.Context{Principal: application.Principal{ActorID: "reader", CompanyIDs: []types.CompanyID{companyID}}},
		CompanyID: companyID,
		EntryID:   posted.Entry.ID,
	}); err != nil {
		t.Fatalf("get posted entry: %v", err)
	}
	wantActions := []application.Action{
		application.ActionCreateAdjustment,
		application.ActionCreate,
		application.ActionValidate,
		application.ActionPost,
		application.ActionReverse,
		application.ActionReverse,
		application.ActionReverse,
		application.ActionReverse,
		application.ActionRead,
	}
	if len(authorizer.actions) != len(wantActions) {
		t.Fatalf("authorizer calls = %v, want %v", authorizer.actions, wantActions)
	}
	for i := range wantActions {
		if authorizer.actions[i] != wantActions[i] {
			t.Fatalf("authorizer action[%d] = %q, want %q", i, authorizer.actions[i], wantActions[i])
		}
	}
}
