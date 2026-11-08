package masterdata

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrCycleNotFound = errors.New("cannot construct account hierarchy cycle")

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) RegisterAccount(account Account) error {
	if account.NormalBalance == "" {
		account.NormalBalance = DefaultNormalBalance(account.AccountType)
	}
	if err := account.Validate(); err != nil {
		return err
	}
	account.Code = strings.ToUpper(strings.TrimSpace(account.Code))
	if _, err := s.repo.GetAccountByCode(account.Code); err == nil {
		return errors.New("duplicate account code")
	}
	if account.ParentID != nil {
		parent, err := s.repo.GetAccountByID(*account.ParentID)
		if err != nil {
			return ErrInvalidParent
		}
		if parent.AccountType != account.AccountType {
			return ErrInvalidParent
		}
		if hasCycle, err := s.accountWouldCycle(account.ID, *account.ParentID); err != nil {
			return err
		} else if hasCycle {
			return ErrAccountCycle
		}
	}
	if account.ID == uuid.Nil {
		account.ID = uuid.New()
	}
	if account.CreatedAt.IsZero() {
		account.CreatedAt = time.Now().UTC()
	}
	account.UpdatedAt = account.CreatedAt
	return s.repo.CreateAccount(account)
}

func (s *Service) accountWouldCycle(accountID, parentID uuid.UUID) (bool, error) {
	if accountID == uuid.Nil {
		return false, nil
	}
	seen := map[uuid.UUID]bool{}
	current := parentID
	for current != uuid.Nil {
		if current == accountID {
			return true, nil
		}
		if seen[current] {
			return true, nil
		}
		seen[current] = true
		account, err := s.repo.GetAccountByID(current)
		if err != nil {
			return false, nil
		}
		if account.ParentID == nil {
			break
		}
		current = *account.ParentID
	}
	return false, nil
}

func (s *Service) UpdateAccountParent(account Account, parentID *uuid.UUID) error {
	existing, err := s.repo.GetAccountByID(account.ID)
	if err != nil {
		return err
	}
	if existing.HasPostedEntries || existing.Code != account.Code || existing.AccountType != account.AccountType {
		return errors.New("posted account code and type cannot be modified")
	}
	account.ParentID = parentID
	if parentID != nil {
		cycle, err := s.accountWouldCycle(account.ID, *parentID)
		if err != nil {
			return err
		}
		if cycle {
			return ErrAccountCycle
		}
	}
	return s.repo.CreateAccount(account)
}

func (s *Service) ValidateCanPostToAccount(accountID uuid.UUID) error {
	account, err := s.repo.GetAccountByID(accountID)
	if err != nil {
		return err
	}
	if account.IsHeader {
		return errors.New("cannot post to header account")
	}
	hasChildren, err := s.repo.HasChildren(accountID)
	if err != nil {
		return err
	}
	if hasChildren {
		return errors.New("cannot post to parent account with children")
	}
	return nil
}

func (s *Service) GetOpenPeriodForDate(date time.Time) (FiscalPeriod, error) {
	period, err := s.repo.GetPeriodByDate(date)
	if err != nil {
		return FiscalPeriod{}, err
	}
	if period.State != PeriodOpen {
		return FiscalPeriod{}, errors.New("fiscal period is closed")
	}
	return period, nil
}

func (s *Service) NextJournalNumber(journalID uuid.UUID) (int64, error) {
	return s.repo.GetNextSequenceWithLock(journalID)
}
