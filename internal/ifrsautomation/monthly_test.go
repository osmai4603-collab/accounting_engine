package ifrsautomation

import (
	"context"
	"errors"
	"testing"
	"time"

	"accounting_engine/internal/ias12"
	"accounting_engine/internal/ifrs15"
	"accounting_engine/internal/ifrs16"
	"accounting_engine/internal/ifrs9"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type memoryPosting struct {
	entries map[uuid.UUID]posting.JournalEntry
}

func newMemoryPosting() *memoryPosting {
	return &memoryPosting{entries: make(map[uuid.UUID]posting.JournalEntry)}
}

func (m *memoryPosting) CreateDraftContext(ctx context.Context, entry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.entries[entry.ID] = entry
	return nil
}

func (m *memoryPosting) PostContext(ctx context.Context, companyID, entryID uuid.UUID, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entry, ok := m.entries[entryID]
	if !ok || entry.CompanyID != companyID {
		return posting.ErrEntryNotFound
	}
	entry.Status = posting.EntryPosted
	m.entries[entryID] = entry
	return nil
}

func (m *memoryPosting) GetEntryByIDForCompany(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	entry, ok := m.entries[entryID]
	if !ok || entry.CompanyID != companyID {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	return entry, nil
}

func TestRunMonthlyPostsAndRetriesIdempotently(t *testing.T) {
	ctx := context.Background()
	store := newMemoryPosting()
	runner, err := NewRunner(store, store, "monthly-runner")
	if err != nil {
		t.Fatal(err)
	}
	period, err := Month(2024, time.February)
	if err != nil {
		t.Fatal(err)
	}
	companyID, journalID := uuid.New(), uuid.New()
	debitID, creditID := uuid.New(), uuid.New()
	job := JobFunc{ID: "ifrs9-ecl", Execute: func(context.Context, Period) ([]posting.JournalEntry, error) {
		amount, _ := financialtypes.NewMoney(decimal.NewFromInt(25), "USD")
		return []posting.JournalEntry{{
			CompanyID: companyID, JournalID: journalID, EntryDate: period.End,
			CreatedBy: "ifrs9", Classification: posting.EntryClassAdjusting,
			Reference: "ecl:2024-02",
			Lines: []posting.JournalEntryLine{
				{AccountID: debitID, Debit: amount},
				{AccountID: creditID, Credit: amount},
			},
		}}, nil
	}}

	first, err := runner.RunMonthly(ctx, period, job)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.PostedEntryIDs) != 1 || len(first.AlreadyPosted) != 0 {
		t.Fatalf("first run result = %+v", first)
	}
	second, err := runner.RunMonthly(ctx, period, job)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.PostedEntryIDs) != 0 || len(second.AlreadyPosted) != 1 ||
		first.PostedEntryIDs[0] != second.AlreadyPosted[0] {
		t.Fatalf("retry result = %+v; first = %+v", second, first)
	}
}

func TestRunMonthlyRejectsChangedContentAndOutsidePeriod(t *testing.T) {
	ctx := context.Background()
	store := newMemoryPosting()
	runner, err := NewRunner(store, store, "monthly-runner")
	if err != nil {
		t.Fatal(err)
	}
	period, _ := Month(2025, time.January)
	companyID, journalID := uuid.New(), uuid.New()
	generate := func(value int64, date time.Time) Job {
		return JobFunc{ID: "periodic", Execute: func(context.Context, Period) ([]posting.JournalEntry, error) {
			lineAmount, _ := financialtypes.NewMoney(decimal.NewFromInt(value), "USD")
			return []posting.JournalEntry{{
				CompanyID: companyID, JournalID: journalID, EntryDate: date,
				CreatedBy: "engine", Reference: "same-reference",
				Lines: []posting.JournalEntryLine{
					{AccountID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), Debit: lineAmount},
					{AccountID: uuid.MustParse("22222222-2222-4222-8222-222222222222"), Credit: lineAmount},
				},
			}}, nil
		}}
	}
	if _, err := runner.RunMonthly(ctx, period, generate(25, period.End)); err != nil {
		t.Fatal(err)
	}
	_, err = runner.RunMonthly(ctx, period, generate(30, period.End))
	if !errors.Is(err, ErrEntryContentChanged) {
		t.Fatalf("changed content error = %v", err)
	}
	_, err = runner.RunMonthly(ctx, period, generate(25, period.End.Add(time.Second)))
	if !errors.Is(err, ErrEntryOutsidePeriod) {
		t.Fatalf("outside period error = %v", err)
	}
}

func TestMonthValidatesAndHandlesLeapYear(t *testing.T) {
	period, err := Month(2024, time.February)
	if err != nil {
		t.Fatal(err)
	}
	if period.Start.Day() != 1 || period.End.Day() != 29 || period.End.Month() != time.February {
		t.Fatalf("leap-month period = %+v", period)
	}
	if _, err := Month(0, time.January); !errors.Is(err, ErrInvalidPeriod) {
		t.Fatalf("invalid month error = %v", err)
	}
}

type memoryContracts struct {
	contracts map[uuid.UUID]ifrs15.Contract
}

func (s *memoryContracts) ListContracts(ctx context.Context, _ uuid.UUID) ([]ifrs15.Contract, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	contracts := make([]ifrs15.Contract, 0, len(s.contracts))
	for _, contract := range s.contracts {
		contracts = append(contracts, contract)
	}
	return contracts, nil
}

func (s *memoryContracts) SaveContract(ctx context.Context, _ uuid.UUID, contract ifrs15.Contract) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.contracts[contract.ID] = contract
	return nil
}

func TestRevenueJobPostsAndPersistsScheduleOnce(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, time.January, 31, 0, 0, 0, 0, time.UTC)
	price := mustMoney(t, "310", "USD")
	obligationID := uuid.New()
	contract, err := ifrs15.NewContract(uuid.New(), uuid.New(), price, []ifrs15.PerformanceObligation{{
		ID: obligationID, Description: "January service", StandalonePrice: price,
		RecognitionPattern: ifrs15.OverTime, StartDate: start, EndDate: end,
	}})
	if err != nil {
		t.Fatal(err)
	}
	contract, err = contract.ApplyBilling(price)
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryContracts{contracts: map[uuid.UUID]ifrs15.Contract{contract.ID: contract}}
	job := &RevenueJob{
		ID: "ifrs15-revenue", Store: store, CompanyID: uuid.New(), JournalID: uuid.New(),
		CreatedBy: "revenue-job", RevenueAccountID: uuid.New(),
		ContractLiabilityID: uuid.New(), ContractAssetID: uuid.New(),
	}
	period, _ := Month(2025, time.January)
	entries, err := job.Generate(ctx, period)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Reference == "" {
		t.Fatalf("generated revenue entries = %+v", entries)
	}
	runnerStore := newMemoryPosting()
	runner, _ := NewRunner(runnerStore, runnerStore, "revenue-poster")
	if _, err := runner.RunMonthly(ctx, period, job); err != nil {
		t.Fatal(err)
	}
	saved := store.contracts[contract.ID]
	if !saved.RecognizedAmount.Equal(price) {
		t.Fatalf("persisted recognized revenue = %s, want %s", saved.RecognizedAmount, price)
	}
	secondRun, err := runner.RunMonthly(ctx, period, job)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondRun.PostedEntryIDs) != 0 || len(secondRun.AlreadyPosted) != 0 {
		t.Fatalf("repeat run should be a no-op, got %+v", secondRun)
	}
}

func TestLeaseJobBuildsMonthlyPaymentAndDepreciationEntries(t *testing.T) {
	date := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	zero := mustMoney(t, "0", "USD")
	payment := mustMoney(t, "100", "USD")
	contract, err := ifrs16.NewLeaseContract(date, decimal.NewFromFloat(0.12), []ifrs16.LeasePayment{{
		DueDate: time.Date(2025, time.January, 31, 0, 0, 0, 0, time.UTC), Amount: payment,
	}}, zero, zero, zero, 1)
	if err != nil {
		t.Fatal(err)
	}
	lease := LeaseSchedule{
		ID: uuid.New(), Contract: contract, CompanyID: uuid.New(), JournalID: uuid.New(),
		CreatedBy: "lease-job",
		Accounts: ifrs16.LeaseAccounts{
			LeaseLiability: uuid.New(), Cash: uuid.New(), InterestExpense: uuid.New(),
			DepreciationExpense: uuid.New(), AccumulatedDepreciation: uuid.New(),
		},
	}
	period, _ := Month(2025, time.January)
	entries, err := (LeaseJob{ID: "ifrs16-leases", Leases: []LeaseSchedule{lease}}).Generate(context.Background(), period)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("monthly lease entries = %d, want payment and depreciation", len(entries))
	}
	for _, entry := range entries {
		if entry.Reference == "" {
			t.Fatal("lease entry must have a stable reference")
		}
		if err := entry.Validate(); err != nil {
			t.Fatalf("lease entry is not balanced: %v", err)
		}
	}
}

func TestDeferredTaxJobRunsAtQuarterEnd(t *testing.T) {
	ctx := context.Background()
	dta, _ := financialtypes.NewMoneyFromString("20", "USD")
	dtl, _ := financialtypes.NewMoneyFromString("0", "USD")
	zero, _ := financialtypes.Zero("USD")
	job := DeferredTaxJob{
		ID: "ias12-quarterly", CompanyID: uuid.New(), JournalID: uuid.New(), CreatedBy: "tax-job",
		Accounts: ias12.DeferredTaxAccounts{Expense: uuid.New(), DTA: uuid.New(), DTL: uuid.New()},
		Measure: func(context.Context, Period) (ias12.Measurement, error) {
			return ias12.Measurement{DTA: dta, DTL: dtl}, nil
		},
		Previous: func(context.Context, Period) (ias12.DeferredTaxPosition, error) {
			return ias12.DeferredTaxPosition{DTA: zero, DTL: zero}, nil
		},
	}
	april, _ := Month(2025, time.April)
	entries, err := job.Generate(ctx, april)
	if err != nil || len(entries) != 0 {
		t.Fatalf("non-quarter month entries = %v, error = %v", entries, err)
	}
	june, _ := Month(2025, time.June)
	entries, err = job.Generate(ctx, june)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Reference != "ias12:deferred-tax:2025-Q2" {
		t.Fatalf("quarter-end tax entries = %+v", entries)
	}
}

type receivablesSource []ifrs9.Exposure

func (s receivablesSource) ListReceivables(ctx context.Context, _ uuid.UUID) ([]ifrs9.Exposure, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]ifrs9.Exposure(nil), s...), nil
}

type allowanceSource struct{ allowance financialtypes.Money }

func (s allowanceSource) ExistingAllowance(context.Context, uuid.UUID, uuid.UUID, string) (financialtypes.Money, error) {
	return s.allowance, nil
}

func TestECLJobGeneratesMonthlyAdjustment(t *testing.T) {
	companyID, journalID := uuid.New(), uuid.New()
	exposureAmount := mustMoney(t, "1000", "USD")
	zero := mustMoney(t, "0", "USD")
	dueDate := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	exposure := ifrs9.Exposure{Receivable: subledgers.OpenLine{
		ID: uuid.New(), CompanyID: companyID, Kind: subledgers.LineReceivable,
		DueDate: dueDate, OriginalAmount: exposureAmount, ResidualAmount: exposureAmount,
	}}
	config := ifrs9.Config{
		CompanyID: companyID, JournalID: journalID, ImpairmentAccountID: uuid.New(),
		AllowanceAccountID: uuid.New(), Currency: "USD",
		ProvisionMatrix: map[subledgers.AgingBucket]decimal.Decimal{
			subledgers.AgingCurrent: decimal.NewFromFloat(0.01),
			subledgers.Aging1To30:   decimal.NewFromFloat(0.03),
			subledgers.Aging31To60:  decimal.NewFromFloat(0.08),
			subledgers.Aging61To90:  decimal.NewFromFloat(0.2),
			subledgers.AgingOver90:  decimal.NewFromFloat(0.5),
		},
	}
	engine, err := ifrs9.NewEngine(config, receivablesSource{exposure}, allowanceSource{allowance: zero})
	if err != nil {
		t.Fatal(err)
	}
	period, _ := Month(2025, time.January)
	entries, err := (ECLJob{ID: "ifrs9-ecl-monthly", Engine: engine}).Generate(context.Background(), period)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Reference != "ifrs9:ecl:2025-01" {
		t.Fatalf("ECL monthly entries = %+v", entries)
	}
}

func mustMoney(t *testing.T, value, currency string) financialtypes.Money {
	t.Helper()
	money, err := financialtypes.NewMoneyFromString(value, currency)
	if err != nil {
		t.Fatal(err)
	}
	return money
}
