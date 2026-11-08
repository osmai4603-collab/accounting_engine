package masterdata

import (
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
