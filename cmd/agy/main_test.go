package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"accounting_engine/internal/ifrs15"
	"accounting_engine/internal/ifrs16"
	"accounting_engine/internal/ifrsautomation"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestRunRejectsInvalidMigrationArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "unknown command", args: []string{"version"}, want: "usage:"},
		{name: "invalid direction", args: []string{"db", "migrate", "sideways"}, want: "usage:"},
		{name: "zero steps", args: []string{"db", "migrate", "down", "--steps", "0"}, want: "positive integer"},
		{name: "malformed steps", args: []string{"db", "migrate", "--steps"}, want: "usage:"},
		{name: "invalid ifrs command", args: []string{"ifrs", "start"}, want: "usage:"},
		{name: "missing config value", args: []string{"ifrs", "run", "--config"}, want: "usage:"},
		{name: "invalid interval", args: []string{"ifrs", "daemon", "--interval", "0s"}, want: "invalid daemon interval"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := run(test.args)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run(%v) error = %v, want text %q", test.args, err, test.want)
			}
		})
	}
}

func TestIFRSConfigDefaultsAndGracefulSkip(t *testing.T) {
	t.Setenv("AGY_IFRS_CONFIG_PATH", "")
	t.Setenv("DATABASE_URL", "")

	if err := runIFRS([]string{"run"}); err != nil {
		t.Fatalf("runIFRS without config = %v, want graceful skip", err)
	}

	emptyPath := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(emptyPath, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, configured, err := loadAutomationConfig(emptyPath); err != nil || configured {
		t.Fatalf("empty config = configured %v, error %v", configured, err)
	}
	if _, configured, err := loadAutomationConfig(filepath.Join(t.TempDir(), "missing.json")); err != nil || configured {
		t.Fatalf("missing config = configured %v, error %v", configured, err)
	}
	invalidPath := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalidPath, []byte(`{"companies":[],"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadAutomationConfig(invalidPath); err == nil {
		t.Fatal("expected unknown config fields to be rejected")
	}

	t.Setenv("AGY_IFRS_CONFIG_PATH", "environment.json")
	_, _, path, err := parseIFRSArgs([]string{"run"})
	if err != nil || path != "environment.json" {
		t.Fatalf("environment config path = %q, error %v", path, err)
	}
	_, _, path, err = parseIFRSArgs([]string{"run", "--config", "argument.json"})
	if err != nil || path != "argument.json" {
		t.Fatalf("explicit config path = %q, error %v", path, err)
	}
}

func TestCLIAutomationExecutionWithJobs(t *testing.T) {
	ctx := context.Background()
	period, err := ifrsautomation.Month(2025, time.January)
	if err != nil {
		t.Fatal(err)
	}
	companyID, journalID := uuid.New(), uuid.New()
	store := &cliAutomationStore{
		contracts: make(map[uuid.UUID]ifrs15.Contract),
		balances:  make(map[uuid.UUID]financialtypes.Money),
		entries:   make(map[uuid.UUID]posting.JournalEntry),
	}

	price := cliMoney(t, "310", "USD")
	contract, err := ifrs15.NewContract(uuid.New(), uuid.New(), price, []ifrs15.PerformanceObligation{{
		ID: uuid.New(), Description: "January service", StandalonePrice: price,
		RecognitionPattern: ifrs15.OverTime,
		StartDate:          period.Start, EndDate: period.End,
	}})
	if err != nil {
		t.Fatal(err)
	}
	contract, err = contract.ApplyBilling(price)
	if err != nil {
		t.Fatal(err)
	}
	store.contracts[contract.ID] = contract

	receivable := subledgers.OpenLine{
		ID: uuid.New(), CompanyID: companyID, Kind: subledgers.LineReceivable,
		DueDate: period.Start, OriginalAmount: cliMoney(t, "1000", "USD"),
		ResidualAmount: cliMoney(t, "1000", "USD"),
	}
	store.lines = []subledgers.OpenLine{receivable}
	provisionMatrix := make(map[subledgers.AgingBucket]decimal.Decimal)
	for _, bucket := range []subledgers.AgingBucket{
		subledgers.AgingCurrent, subledgers.Aging1To30, subledgers.Aging31To60,
		subledgers.Aging61To90, subledgers.AgingOver90,
	} {
		provisionMatrix[bucket] = decimal.RequireFromString("0.1")
	}

	zero := cliMoney(t, "0", "USD")
	leaseAccounts := ifrs16.LeaseAccounts{
		ROUAsset: uuid.New(), LeaseLiability: uuid.New(), Cash: uuid.New(),
		InterestExpense: uuid.New(), DepreciationExpense: uuid.New(),
		AccumulatedDepreciation: uuid.New(),
	}
	config := ifrsautomation.AutomationConfig{Companies: []ifrsautomation.CompanyAutomationConfig{{
		CompanyID: companyID, JournalID: journalID, CreatedBy: "agy-test",
		IFRS9: ifrsautomation.IFRS9AutomationConfig{
			ImpairmentAccountID: uuid.New(), AllowanceAccountID: uuid.New(),
			Currency: "USD", ProvisionMatrix: provisionMatrix,
		},
		IFRS15: ifrsautomation.IFRS15AutomationConfig{
			RevenueAccountID: uuid.New(), ContractLiabilityID: uuid.New(), ContractAssetID: uuid.New(),
		},
		IFRS16: ifrsautomation.IFRS16AutomationConfig{Leases: []ifrsautomation.LeaseAutomationConfig{{
			ID: uuid.New(),
			Contract: ifrsautomation.LeaseTerms{
				CommencementDate: period.Start, AnnualDiscountRate: decimal.RequireFromString("0.12"),
				Payments:           []ifrs16.LeasePayment{{DueDate: period.End, Amount: cliMoney(t, "100", "USD")}},
				InitialDirectCosts: zero, Prepayments: zero, LeaseIncentives: zero,
				UsefulLifePeriods: 1,
			},
			Accounts: leaseAccounts,
		}}},
	}}}

	configPath := filepath.Join(t.TempDir(), "agy-ifrs.json")
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, configured, err := loadAutomationConfig(configPath)
	if err != nil || !configured {
		t.Fatalf("load config = configured %v, error %v", configured, err)
	}
	jobs, err := ifrsautomation.BuildStandardJobs(loaded, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatalf("standard job count = %d, want 3", len(jobs))
	}
	runner, err := ifrsautomation.NewRunner(store, store, "agy-cli-test")
	if err != nil {
		t.Fatal(err)
	}
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logOutput, nil))
	scheduler, err := ifrsautomation.NewSchedulerWithLogger(runner, logger, jobs...)
	if err != nil {
		t.Fatal(err)
	}
	if err := runIFRSPeriod(ctx, scheduler, "run", time.Hour, time.Date(2025, time.February, 10, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if len(store.entries) != 4 {
		t.Fatalf("posted journal count = %d, want 4 (ECL, revenue, lease payment, depreciation)", len(store.entries))
	}
	if !store.balanceAsOf.Equal(period.End) {
		t.Fatalf("existing ECL allowance read at %s, want period end %s", store.balanceAsOf, period.End)
	}
	for id, entry := range store.entries {
		if entry.Status != posting.EntryPosted {
			t.Errorf("entry %s status = %s, want posted", id, entry.Status)
		}
	}
	for _, jobKey := range []string{"ifrs9-ecl:", "ifrs15-revenue:", "ifrs16-leases:"} {
		if !strings.Contains(logOutput.String(), jobKey) {
			t.Errorf("structured log output does not include job %q: %s", jobKey, logOutput.String())
		}
	}
}

type cliAutomationStore struct {
	lines       []subledgers.OpenLine
	contracts   map[uuid.UUID]ifrs15.Contract
	balances    map[uuid.UUID]financialtypes.Money
	entries     map[uuid.UUID]posting.JournalEntry
	balanceAsOf time.Time
}

func (s *cliAutomationStore) ListOpenLinesForCompany(
	ctx context.Context, companyID uuid.UUID, _ string,
) ([]subledgers.OpenLine, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make([]subledgers.OpenLine, 0, len(s.lines))
	for _, line := range s.lines {
		if line.CompanyID == companyID {
			result = append(result, line)
		}
	}
	return result, nil
}

func (s *cliAutomationStore) GetAccountBalanceAsOf(
	ctx context.Context, _ uuid.UUID, accountID uuid.UUID, asOf time.Time,
) (financialtypes.Money, error) {
	if err := ctx.Err(); err != nil {
		return financialtypes.Money{}, err
	}
	s.balanceAsOf = asOf
	if balance, ok := s.balances[accountID]; ok {
		return balance, nil
	}
	return financialtypes.Zero("USD")
}

func (s *cliAutomationStore) ListContracts(
	ctx context.Context, _ uuid.UUID,
) ([]ifrs15.Contract, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make([]ifrs15.Contract, 0, len(s.contracts))
	for _, contract := range s.contracts {
		result = append(result, contract)
	}
	return result, nil
}

func (s *cliAutomationStore) SaveContract(
	ctx context.Context, _ uuid.UUID, contract ifrs15.Contract,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.contracts[contract.ID] = contract
	return nil
}

func (s *cliAutomationStore) CreateDraftContext(ctx context.Context, entry posting.JournalEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.entries[entry.ID] = entry
	return nil
}

func (s *cliAutomationStore) PostContext(
	ctx context.Context, companyID, entryID uuid.UUID, _ string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entry, ok := s.entries[entryID]
	if !ok || entry.CompanyID != companyID {
		return posting.ErrEntryNotFound
	}
	entry.Status = posting.EntryPosted
	s.entries[entryID] = entry
	return nil
}

func (s *cliAutomationStore) GetEntryByIDForCompany(
	ctx context.Context, companyID, entryID uuid.UUID,
) (posting.JournalEntry, error) {
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	entry, ok := s.entries[entryID]
	if !ok || entry.CompanyID != companyID {
		return posting.JournalEntry{}, posting.ErrEntryNotFound
	}
	return entry, nil
}

func cliMoney(t *testing.T, amount, currency string) financialtypes.Money {
	t.Helper()
	value, err := financialtypes.NewMoneyFromString(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
