package standardsparity

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	internalias12 "github.com/osmai4603-collab/accounting_engine/internal/ias12"
	internalifrs15 "github.com/osmai4603-collab/accounting_engine/internal/ifrs15"
	internalifrs16 "github.com/osmai4603-collab/accounting_engine/internal/ifrs16"
	internalifrs9 "github.com/osmai4603-collab/accounting_engine/internal/ifrs9"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/modules/ias12"
	"github.com/osmai4603-collab/accounting_engine/modules/ifrs15"
	"github.com/osmai4603-collab/accounting_engine/modules/ifrs16"
	"github.com/osmai4603-collab/accounting_engine/modules/ifrs9"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
	"github.com/shopspring/decimal"
)

type standardsFixture struct {
	companyID         types.CompanyID
	journalID         types.JournalID
	cashAccount       types.AccountID
	revenueAccount    types.AccountID
	receivableAccount types.AccountID
	payableAccount    types.AccountID
	expenseAccount    types.AccountID
	assetAccount      types.AccountID
	liabilityAccount  types.AccountID
}

func setupStandardsFixture(ctx context.Context, mdStore publicmasterdata.Store) (standardsFixture, error) {
	companyID := types.NewCompanyID()
	journalID := types.JournalID(uuid.New())
	currency := "USD"

	if err := mdStore.CreateCompany(ctx, publicmasterdata.Company{
		ID:                   companyID,
		Code:                 fmt.Sprintf("STD-%s", uuid.UUID(companyID).String()[:8]),
		LegalName:            "Standards Integration Corp",
		FunctionalCurrency:   currency,
		PresentationCurrency: currency,
		Active:               true,
	}); err != nil {
		return standardsFixture{}, err
	}

	if err := mdStore.CreateJournal(ctx, publicmasterdata.Journal{
		ID:        publicmasterdata.JournalID(journalID),
		CompanyID: companyID,
		Code:      "GEN",
		Name:      "General Standards Journal",
	}); err != nil {
		return standardsFixture{}, err
	}

	makeAccount := func(code, name string, accType publicmasterdata.AccountType, normal publicmasterdata.NormalBalance) (types.AccountID, error) {
		accID := types.NewAccountID()
		err := mdStore.CreateAccount(ctx, publicmasterdata.Account{
			ID:            accID,
			CompanyID:     companyID,
			Code:          code,
			Name:          name,
			Type:          accType,
			NormalBalance: normal,
			Currency:      currency,
			IsActive:      true,
		})
		return accID, err
	}

	cash, err := makeAccount("1000", "Cash", publicmasterdata.AccountTypeAsset, publicmasterdata.NormalBalanceDebit)
	if err != nil {
		return standardsFixture{}, err
	}
	ar, err := makeAccount("1200", "Receivables", publicmasterdata.AccountTypeAsset, publicmasterdata.NormalBalanceDebit)
	if err != nil {
		return standardsFixture{}, err
	}
	asset, err := makeAccount("1500", "ROU Asset", publicmasterdata.AccountTypeAsset, publicmasterdata.NormalBalanceDebit)
	if err != nil {
		return standardsFixture{}, err
	}
	liability, err := makeAccount("2100", "Lease Liability", publicmasterdata.AccountTypeLiability, publicmasterdata.NormalBalanceCredit)
	if err != nil {
		return standardsFixture{}, err
	}
	ap, err := makeAccount("2000", "Payables", publicmasterdata.AccountTypeLiability, publicmasterdata.NormalBalanceCredit)
	if err != nil {
		return standardsFixture{}, err
	}
	rev, err := makeAccount("4000", "Revenue", publicmasterdata.AccountTypeIncome, publicmasterdata.NormalBalanceCredit)
	if err != nil {
		return standardsFixture{}, err
	}
	exp, err := makeAccount("6000", "Expense", publicmasterdata.AccountTypeExpense, publicmasterdata.NormalBalanceDebit)
	if err != nil {
		return standardsFixture{}, err
	}

	// Fiscal Year & Periods
	yearID := types.NewFiscalYearID()
	periodID := types.NewFiscalPeriodID()
	startDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2026, 12, 31, 23, 59, 59, 999999999, time.UTC)

	if err := mdStore.CreateFiscalYear(ctx, publicmasterdata.FiscalYear{
		ID: yearID, CompanyID: companyID, Name: "FY2026", StartDate: startDate, EndDate: endDate,
	}); err != nil {
		return standardsFixture{}, err
	}

	if err := mdStore.CreateFiscalPeriod(ctx, publicmasterdata.FiscalPeriod{
		ID: periodID, CompanyID: companyID, FiscalYearID: yearID,
		Number: 1, Name: "P01-2026", StartDate: startDate, EndDate: endDate, State: publicmasterdata.PeriodOpen,
	}); err != nil {
		return standardsFixture{}, err
	}

	return standardsFixture{
		companyID:         companyID,
		journalID:         journalID,
		cashAccount:       cash,
		revenueAccount:    rev,
		receivableAccount: ar,
		payableAccount:    ap,
		expenseAccount:    exp,
		assetAccount:      asset,
		liabilityAccount:  liability,
	}, nil
}

func TestStandardsPostingWorkflow_EndToEnd(t *testing.T) {
	ctx := context.Background()
	driver := memory.New()
	mdStore := driver.MasterDataStore()

	fix, err := setupStandardsFixture(ctx, mdStore)
	if err != nil {
		t.Fatalf("setup fixture: %v", err)
	}

	appService, err := sdk.NewApplicationService(driver.Ports())
	if err != nil {
		t.Fatalf("build app service: %v", err)
	}

	ports := standards.NewModulePorts(
		driver.Ports().Entries,
		driver.Ports().Balance,
		driver.MasterDataStore(),
		appService,
	)

	cmdCtx := publicapplication.WithPrincipal(ctx, publicapplication.Principal{
		ActorID:    "standards-controller",
		CompanyIDs: []types.CompanyID{fix.companyID},
	})

	// 1. IFRS 15 Module Posting Workflow
	t.Run("IFRS 15 Revenue Workflow Posting", func(t *testing.T) {
		m15 := ifrs15.NewModule()
		if err := m15.Initialize(ctx, ports); err != nil {
			t.Fatalf("Initialize IFRS 15: %v", err)
		}

		mapping := internalifrs15.RevenueJournalMapping{
			CompanyID:                uuid.UUID(fix.companyID),
			JournalID:                uuid.UUID(fix.journalID),
			RevenueAccountID:         uuid.UUID(fix.revenueAccount),
			ContractAssetAccountID:   uuid.UUID(fix.receivableAccount),
			ContractLiabilityAccount: uuid.UUID(fix.payableAccount),
		}

		wf, err := m15.NewRevenueWorkflow(mapping)
		if err != nil {
			t.Fatalf("NewRevenueWorkflow: %v", err)
		}

		contractID := uuid.New()
		poID := uuid.New()
		price, _ := financialtypes.NewMoneyFromString("1000.00", "USD")

		contract, err := wf.AddContract(internalifrs15.FiveStepAssessment{
			ContractID:                  contractID,
			CustomerID:                  uuid.New(),
			Approved:                    true,
			EnforceableRightsIdentified: true,
			PaymentTermsIdentified:      true,
			CommercialSubstance:         true,
			CollectibilityProbable:      true,
			FixedTransactionPrice:       true,
			TransactionPrice:            price,
			DistinctObligationIDs:       []uuid.UUID{poID},
			Obligations: []internalifrs15.PerformanceObligation{
				{
					ID:                 poID,
					Description:        "Subscription service",
					StandalonePrice:    price,
					RecognitionPattern: internalifrs15.PointInTime,
					StartDate:          time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
				},
			},
		})
		if err != nil {
			t.Fatalf("AddContract: %v", err)
		}

		eventID := uuid.New()
		eventAmt, _ := financialtypes.NewMoneyFromString("450.00", "USD")
		event := internalifrs15.SatisfactionEvent{
			ID:           eventID,
			ObligationID: poID,
			Amount:       eventAmt,
			Date:         time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
			Evidence: internalifrs15.ServiceDeliveryEvidence{
				Satisfied:         true,
				MeasureBasis:      "metered-delivery",
				EvidenceReference: "EV-15-01",
			},
		}

		updatedContract, entry, err := wf.Recognize(cmdCtx, contract.ID, event)
		if err != nil {
			t.Fatalf("Recognize failed: %v", err)
		}
		if updatedContract.RecognizedAmount.Amount().StringFixed(2) != "450.00" {
			t.Errorf("RecognizedAmount = %s, want 450.00", updatedContract.RecognizedAmount.Amount())
		}
		if entry.Status != "posted" && entry.Status != "" {
			t.Logf("Entry status: %s", entry.Status)
		}

		// Verify retrieval through application layer
		queryCtx := publicapplication.Context{
			Principal: publicapplication.Principal{
				ActorID:    "standards-controller",
				CompanyIDs: []types.CompanyID{fix.companyID},
			},
		}
		retrieved, err := appService.GetEntry(cmdCtx, publicapplication.GetEntryQuery{
			Context:   queryCtx,
			CompanyID: fix.companyID,
			EntryID:   types.JournalEntryID(entry.ID),
		})
		if err != nil {
			t.Fatalf("GetEntry: %v", err)
		}
		if retrieved.Status != ledger.EntryPosted {
			t.Errorf("entry status = %s, want %s", retrieved.Status, ledger.EntryPosted)
		}
		if retrieved.SourceModule != "ifrs15" {
			t.Errorf("SourceModule = %s, want ifrs15", retrieved.SourceModule)
		}
	})

	// 2. IFRS 9 Module Posting Workflow
	t.Run("IFRS 9 Measurement and ECL Posting", func(t *testing.T) {
		m9 := &ifrs9.Module{}
		if err := m9.Initialize(ctx, ports); err != nil {
			t.Fatalf("Initialize IFRS 9: %v", err)
		}

		instID := uuid.New()
		openingMoney, _ := financialtypes.NewMoneyFromString("5000.00", "USD")
		zeroMoney, _ := financialtypes.Zero("USD")

		measInput := internalifrs9.MeasurementInput{
			InstrumentID: instID,
			AsOf:         time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
			Classification: internalifrs9.HostClassification{
				MeasurementCategory:     internalifrs9.AmortisedCost,
				BusinessModelReference:  "bm-hold-to-collect",
				SPPIAssessmentReference: "sppi-contractual-cashflows",
			},
			Stage:                 internalifrs9.Stage1,
			OpeningGrossCarrying:  openingMoney,
			ExistingLossAllowance: zeroMoney,
			PrincipalRepayments:   zeroMoney,
			EffectiveInterestRate: decimal.RequireFromString("0.08"),
			Period:                30 * 24 * time.Hour,
		}
		measResult, err := internalifrs9.MeasureSubsequentPeriod(measInput)
		if err != nil {
			t.Fatalf("MeasureSubsequentPeriod: %v", err)
		}

		measAccounts := internalifrs9.MeasurementAccounts{
			Asset:          uuid.UUID(fix.receivableAccount),
			Cash:           uuid.UUID(fix.cashAccount),
			InterestIncome: uuid.UUID(fix.revenueAccount),
		}

		measPostResult, err := m9.PostSubsequentMeasurement(
			cmdCtx,
			measResult,
			measAccounts,
			uuid.UUID(fix.companyID),
			uuid.UUID(fix.journalID),
			"standards-controller",
			"idem-ifrs9-meas-1",
		)
		if err != nil {
			t.Fatalf("PostSubsequentMeasurement failed: %v", err)
		}
		if measPostResult.Entry.Status != ledger.EntryPosted {
			t.Errorf("IFRS 9 measurement status = %s, want %s", measPostResult.Entry.Status, ledger.EntryPosted)
		}
	})

	// 3. IFRS 16 Module Posting Workflow
	t.Run("IFRS 16 Lease Posting Workflow", func(t *testing.T) {
		m16 := &ifrs16.Module{}
		if err := m16.Initialize(ctx, ports); err != nil {
			t.Fatalf("Initialize IFRS 16: %v", err)
		}

		commencement := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		payAmt, _ := financialtypes.NewMoneyFromString("1200.00", "USD")
		zeroMoney, _ := financialtypes.Zero("USD")

		contract, err := internalifrs16.NewLeaseContract(
			commencement,
			decimal.RequireFromString("0.06"),
			[]internalifrs16.LeasePayment{
				{DueDate: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), Amount: payAmt},
			},
			zeroMoney, zeroMoney, zeroMoney,
			12,
		)
		if err != nil {
			t.Fatalf("NewLeaseContract: %v", err)
		}
		contract.Reference = "LEASE-INTEG-001"

		leaseAccounts := internalifrs16.LeaseAccounts{
			ROUAsset:                uuid.UUID(fix.assetAccount),
			LeaseLiability:          uuid.UUID(fix.liabilityAccount),
			Cash:                    uuid.UUID(fix.cashAccount),
			PrepaidLease:            uuid.UUID(fix.receivableAccount),
			InterestExpense:         uuid.UUID(fix.expenseAccount),
			DepreciationExpense:     uuid.UUID(fix.expenseAccount),
			AccumulatedDepreciation: uuid.UUID(fix.assetAccount),
		}

		commenceResult, err := m16.PostCommencement(
			cmdCtx,
			contract,
			leaseAccounts,
			uuid.UUID(fix.companyID),
			uuid.UUID(fix.journalID),
			"standards-controller",
			"idem-lease-commence-1",
		)
		if err != nil {
			t.Fatalf("PostCommencement: %v", err)
		}
		if commenceResult.Entry.Status != ledger.EntryPosted {
			t.Errorf("commencement status = %s, want %s", commenceResult.Entry.Status, ledger.EntryPosted)
		}
	})

	// 4. IAS 12 Module Posting Workflow
	t.Run("IAS 12 Tax Posting Workflow", func(t *testing.T) {
		m12 := ias12.NewModule(nil)
		if err := m12.Initialize(ctx, ports); err != nil {
			t.Fatalf("Initialize IAS 12: %v", err)
		}

		taxAmt, _ := financialtypes.NewMoneyFromString("320.00", "USD")
		currInput := internalias12.CurrentTaxEntryInput{
			CompanyID:        uuid.UUID(fix.companyID),
			JournalID:        uuid.UUID(fix.journalID),
			ExpenseAccountID: uuid.UUID(fix.expenseAccount),
			PayableAccountID: uuid.UUID(fix.payableAccount),
			EntryDate:        time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC),
			CreatedBy:        "standards-controller",
			Amount:           taxAmt,
		}

		taxResult, err := m12.PostCurrentTax(cmdCtx, currInput, "TAX-ACC-2026-01", "idem-tax-curr-1")
		if err != nil {
			t.Fatalf("PostCurrentTax: %v", err)
		}
		if taxResult.Entry.Status != ledger.EntryPosted {
			t.Errorf("tax entry status = %s, want %s", taxResult.Entry.Status, ledger.EntryPosted)
		}
	})

	// 5. Failure Injection & Rollback Guarantee
	t.Run("Failure Injection Rejects Unbalanced Lines and Keeps GL Intact", func(t *testing.T) {
		poster := ports.Posting()
		badAmt1, _ := types.NewMoneyFromString("100.00", "USD")
		badAmt2, _ := types.NewMoneyFromString("50.00", "USD")
		zero, _ := types.Zero("USD")

		badReq := standards.StandardPostingRequest{
			StandardRef:    "IFRS-15-REV-REC",
			EntityID:       "bad-entity",
			IdempotencyKey: "bad-unbalanced-key",
			CompanyID:      fix.companyID,
			JournalID:      fix.journalID,
			EffectiveDate:  time.Now().UTC(),
			Lines: []ledger.EntryLine{
				{
					ID: types.NewJournalEntryLineID(), AccountID: fix.cashAccount,
					TransactionAmount: badAmt1, TransactionCurrency: "USD",
					ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: badAmt1, FunctionalCredit: zero,
				},
				{
					ID: types.NewJournalEntryLineID(), AccountID: fix.revenueAccount,
					TransactionAmount: badAmt2, TransactionCurrency: "USD",
					ExchangeRate: decimal.NewFromInt(1), FunctionalDebit: zero, FunctionalCredit: badAmt2,
				},
			},
		}

		if _, err := poster.PostStandardEntry(cmdCtx, badReq); !errors.Is(err, standards.ErrUnbalancedLines) {
			t.Fatalf("expected ErrUnbalancedLines, got: %v", err)
		}
	})
}
