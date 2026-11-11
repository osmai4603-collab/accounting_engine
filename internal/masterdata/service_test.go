package masterdata

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newAccount(code string, accountType AccountType, parent *uuid.UUID) Account {
	return Account{
		ID: uuid.New(), Code: code, Name: code, AccountType: accountType,
		NormalBalance: DefaultNormalBalance(accountType), ParentID: parent,
	}
}

func TestUpdateAccountParentUsesCompanyScopeAndRejectsCycles(t *testing.T) {
	repository := NewMockRepository()
	service := NewService(repository)
	companyID := uuid.New()

	firstRoot := newAccount("1000", AccountTypeAsset, nil)
	firstRoot.CompanyID = companyID
	secondRoot := newAccount("1100", AccountTypeAsset, nil)
	secondRoot.CompanyID = companyID
	child := newAccount("1001", AccountTypeAsset, &firstRoot.ID)
	child.CompanyID = companyID
	for _, account := range []Account{firstRoot, secondRoot, child} {
		if err := service.RegisterAccount(account); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	if err := service.UpdateAccountParentContext(ctx, child, &secondRoot.ID); err != nil {
		t.Fatalf("update account parent: %v", err)
	}
	updated, err := repository.GetAccountByIDForCompany(companyID, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ParentID == nil || *updated.ParentID != secondRoot.ID {
		t.Fatalf("updated parent = %v, want %s", updated.ParentID, secondRoot.ID)
	}

	if err := service.UpdateAccountParentContext(ctx, secondRoot, &child.ID); !errors.Is(err, ErrAccountCycle) {
		t.Fatalf("cyclic parent update error = %v, want ErrAccountCycle", err)
	}

	otherCompanyParent := newAccount("2000", AccountTypeAsset, nil)
	otherCompanyParent.CompanyID = uuid.New()
	if err := service.RegisterAccount(otherCompanyParent); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateAccountParentContext(ctx, child, &otherCompanyParent.ID); !errors.Is(err, ErrInvalidParent) {
		t.Fatalf("cross-company parent error = %v, want ErrInvalidParent", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := service.UpdateAccountParentContext(canceled, child, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled update error = %v, want context.Canceled", err)
	}
}

func TestRegisterAccountAndRejectDuplicateCode(t *testing.T) {
	repo := NewMockRepository()
	service := NewService(repo)
	if err := service.RegisterAccount(newAccount("1001", AccountTypeAsset, nil)); err != nil {
		t.Fatal(err)
	}
	if err := service.RegisterAccount(newAccount("1001", AccountTypeAsset, nil)); err == nil {
		t.Fatal("expected duplicate account code error")
	}
}

func TestCannotPostToParentAccount(t *testing.T) {
	repo := NewMockRepository()
	service := NewService(repo)
	parent := newAccount("1000", AccountTypeAsset, nil)
	parent.IsHeader = true
	if err := service.RegisterAccount(parent); err != nil {
		t.Fatal(err)
	}
	childID := uuid.New()
	childParentID := parent.ID
	child := newAccount("1001", AccountTypeAsset, &childParentID)
	child.ID = childID
	if err := service.RegisterAccount(child); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateCanPostToAccount(parent.ID); err == nil {
		t.Fatal("expected parent account posting to be rejected")
	}
}

func TestFiscalPeriodStates(t *testing.T) {
	repo := NewMockRepository()
	year := GenerateFiscalYear(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if len(year.Periods) != 14 {
		t.Fatalf("got %d periods, want 14", len(year.Periods))
	}
	if err := repo.CreateFiscalYear(year); err != nil {
		t.Fatal(err)
	}
	service := NewService(repo)
	period, err := service.GetOpenPeriodForDate(time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdatePeriodState(period.ID, PeriodSoftClosed); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetOpenPeriodForDate(time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("expected soft closed period to be closed for direct posting")
	}
}

func TestSubClassificationValidation(t *testing.T) {
	repo := NewMockRepository()
	service := NewService(repo)

	// Valid matching classifications
	validAsset := Account{
		ID: uuid.New(), Code: "1001", Name: "Cash Account", AccountType: AccountTypeAsset,
		SubClassification: SubClassCashAndEquivalents,
	}
	if err := service.RegisterAccount(validAsset); err != nil {
		t.Fatalf("expected valid asset subclassification to succeed, got %v", err)
	}

	validExpense := Account{
		ID: uuid.New(), Code: "5001", Name: "COGS Account", AccountType: AccountTypeExpense,
		SubClassification: SubClassCostOfSales,
	}
	if err := service.RegisterAccount(validExpense); err != nil {
		t.Fatalf("expected valid expense subclassification to succeed, got %v", err)
	}

	// Incompatible classification: assigning revenue classification to an asset account
	invalidAccount := Account{
		ID: uuid.New(), Code: "1002", Name: "Mismatched Account", AccountType: AccountTypeAsset,
		SubClassification: SubClassOperatingRevenue,
	}
	if err := service.RegisterAccount(invalidAccount); err != ErrInvalidSubClassificationForType {
		t.Fatalf("expected ErrInvalidSubClassificationForType, got %v", err)
	}
}

func TestGaplessConcurrentSequences(t *testing.T) {
	repo := NewMockRepository()
	journalID := uuid.New()
	const count = 100
	numbers := make(chan int64, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			number, err := repo.GetNextSequenceWithLock(journalID)
			if err != nil {
				t.Error(err)
				return
			}
			numbers <- number
		}()
	}
	wg.Wait()
	close(numbers)
	seen := make(map[int64]bool, count)
	for number := range numbers {
		if seen[number] {
			t.Fatalf("duplicate sequence %d", number)
		}
		seen[number] = true
	}
	if len(seen) != count {
		t.Fatalf("got %d sequences, want %d", len(seen), count)
	}
}

func TestCompanyScopedFiscalPeriods(t *testing.T) {
	repository := NewMockRepository()
	service := NewService(repository)
	companyA, companyB := uuid.New(), uuid.New()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	yearA := GenerateFiscalYear(start)
	yearA.CompanyID = companyA
	yearB := GenerateFiscalYear(start)
	yearB.CompanyID = companyB
	if err := repository.CreateFiscalYear(yearA); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateFiscalYear(yearB); err != nil {
		t.Fatal(err)
	}

	date := start.AddDate(0, 0, 14)
	periodA, err := service.GetOpenPeriodForCompanyDate(companyA, date)
	if err != nil {
		t.Fatal(err)
	}
	periodB, err := service.GetOpenPeriodForCompanyDate(companyB, date)
	if err != nil {
		t.Fatal(err)
	}
	if periodA.ID == periodB.ID || periodA.CompanyID != companyA || periodB.CompanyID != companyB {
		t.Fatalf("company-scoped periods crossed: A=%+v B=%+v", periodA, periodB)
	}

	period13A := yearA.Periods[12]
	if err := repository.UpdatePeriodState(period13A.ID, PeriodSoftClosed); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdatePeriodState(period13A.ID, PeriodHardClosed); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetOpenAdjustmentPeriodForCompanyDate(companyA, date, 13); err != ErrFiscalPeriodClosed {
		t.Fatalf("closed adjustment period error = %v, want ErrFiscalPeriodClosed", err)
	}
	openPeriod, err := service.GetOpenAdjustmentPeriodForCompanyDate(companyB, date, 13)
	if err != nil {
		t.Fatal(err)
	}
	if openPeriod.CompanyID != companyB {
		t.Fatalf("adjustment period belongs to company %s, want %s", openPeriod.CompanyID, companyB)
	}
}
