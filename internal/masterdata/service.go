package masterdata

import (
	"context"
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
	if account.CompanyID != uuid.Nil {
		repository, ok := s.repo.(CompanyScopedRepository)
		if !ok {
			return ErrCompanyScopeRequired
		}
		if _, err := repository.GetAccountByCodeForCompany(account.CompanyID, account.Code); err == nil {
			return errors.New("duplicate account code")
		} else if !errors.Is(err, ErrAccountNotFound) {
			return err
		}
	} else if _, err := s.repo.GetAccountByCode(account.Code); err == nil {
		return errors.New("duplicate account code")
	} else if !errors.Is(err, ErrAccountNotFound) {
		return err
	}
	if account.ParentID != nil {
		var parent Account
		var err error
		if account.CompanyID != uuid.Nil {
			repository, ok := s.repo.(CompanyScopedRepository)
			if !ok {
				return ErrCompanyScopeRequired
			}
			parent, err = repository.GetAccountByIDForCompany(account.CompanyID, *account.ParentID)
		} else {
			parent, err = s.repo.GetAccountByID(*account.ParentID)
		}
		if err != nil {
			return ErrInvalidParent
		}
		if parent.AccountType != account.AccountType {
			return ErrInvalidParent
		}
		if hasCycle, err := s.accountWouldCycle(context.Background(), account.CompanyID, account.ID, *account.ParentID); err != nil {
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

func (s *Service) accountWouldCycle(ctx context.Context, companyID, accountID, parentID uuid.UUID) (bool, error) {
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
		if err := ctx.Err(); err != nil {
			return false, err
		}
		seen[current] = true
		account, err := s.getAccountForCompany(ctx, companyID, current)
		if err != nil {
			return false, err
		}
		if account.ParentID == nil {
			break
		}
		current = *account.ParentID
	}
	return false, nil
}

func (s *Service) UpdateAccountParent(account Account, parentID *uuid.UUID) error {
	return s.UpdateAccountParentContext(context.Background(), account, parentID)
}

func (s *Service) UpdateAccountParentContext(ctx context.Context, account Account, parentID *uuid.UUID) error {
	if ctx == nil {
		return errors.New("account update context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	existing, err := s.getAccountForCompany(ctx, account.CompanyID, account.ID)
	if err != nil {
		return err
	}
	if account.CompanyID != existing.CompanyID {
		return ErrInvalidParent
	}
	if existing.HasPostedEntries || existing.Code != account.Code || existing.AccountType != account.AccountType {
		return errors.New("posted account code and type cannot be modified")
	}
	account.ParentID = parentID
	if parentID != nil {
		parent, err := s.getAccountForCompany(ctx, account.CompanyID, *parentID)
		if err != nil {
			if errors.Is(err, ErrAccountNotFound) {
				return ErrInvalidParent
			}
			return err
		}
		if parent.AccountType != account.AccountType {
			return ErrInvalidParent
		}
		cycle, err := s.accountWouldCycle(ctx, account.CompanyID, account.ID, *parentID)
		if err != nil {
			return err
		}
		if cycle {
			return ErrAccountCycle
		}
	}
	return s.createAccountContext(ctx, account)
}

func (s *Service) getAccountForCompany(ctx context.Context, companyID, accountID uuid.UUID) (Account, error) {
	if companyID == uuid.Nil {
		return s.repo.GetAccountByID(accountID)
	}
	if repository, ok := s.repo.(ContextCompanyScopedRepository); ok {
		return repository.GetAccountByIDForCompanyContext(ctx, companyID, accountID)
	}
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	if repository, ok := s.repo.(CompanyScopedRepository); ok {
		return repository.GetAccountByIDForCompany(companyID, accountID)
	}
	return Account{}, ErrCompanyScopeRequired
}

func (s *Service) createAccountContext(ctx context.Context, account Account) error {
	if repository, ok := s.repo.(interface {
		CreateAccountContext(context.Context, Account) error
	}); ok {
		return repository.CreateAccountContext(ctx, account)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.repo.CreateAccount(account)
}

func (s *Service) ValidateCanPostToAccount(accountID uuid.UUID) error {
	account, err := s.repo.GetAccountByID(accountID)
	if err != nil {
		return err
	}
	return validatePostableAccount(s.repo, accountID, account)
}

func (s *Service) GetCompanyByID(companyID uuid.UUID) (Company, error) {
	repository, ok := s.repo.(CompanyRepository)
	if !ok {
		return Company{}, ErrCompanyScopeRequired
	}
	return repository.GetCompanyByID(companyID)
}

func (s *Service) GetCompanyByIDContext(ctx context.Context, companyID uuid.UUID) (Company, error) {
	if err := ctx.Err(); err != nil {
		return Company{}, err
	}
	repository, ok := s.repo.(ContextCompanyScopedRepository)
	if !ok {
		return Company{}, ErrCompanyScopeRequired
	}
	return repository.GetCompanyByIDContext(ctx, companyID)
}

func (s *Service) ValidateCanPostToAccountForCompany(companyID, accountID uuid.UUID) error {
	repository, ok := s.repo.(CompanyScopedRepository)
	if !ok {
		return ErrCompanyScopeRequired
	}
	account, err := repository.GetAccountByIDForCompany(companyID, accountID)
	if err != nil {
		return err
	}
	return validatePostableAccount(s.repo, accountID, account)
}

func (s *Service) ValidateCanPostToAccountForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	repository, ok := s.repo.(ContextCompanyScopedRepository)
	if !ok {
		return ErrCompanyScopeRequired
	}
	account, err := repository.GetAccountByIDForCompanyContext(ctx, companyID, accountID)
	if err != nil {
		return err
	}
	if account.IsHeader {
		return errors.New("cannot post to header account")
	}
	hasChildren, err := repository.HasChildrenForCompanyContext(ctx, companyID, accountID)
	if err != nil {
		return err
	}
	if hasChildren {
		return errors.New("cannot post to parent account with children")
	}
	return nil
}

func validatePostableAccount(repository Repository, accountID uuid.UUID, account Account) error {
	if account.IsHeader {
		return errors.New("cannot post to header account")
	}
	hasChildren, err := repository.HasChildren(accountID)
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

func (s *Service) GetOpenPeriodForCompanyDate(companyID uuid.UUID, date time.Time) (FiscalPeriod, error) {
	repository, ok := s.repo.(CompanyScopedRepository)
	if !ok {
		return FiscalPeriod{}, ErrCompanyScopeRequired
	}
	period, err := repository.GetPeriodByDateForCompany(companyID, date)
	if err != nil {
		return FiscalPeriod{}, err
	}
	if period.State != PeriodOpen {
		return FiscalPeriod{}, ErrFiscalPeriodClosed
	}
	return period, nil
}

func (s *Service) GetOpenPeriodForCompanyDateContext(ctx context.Context, companyID uuid.UUID, date time.Time) (FiscalPeriod, error) {
	if err := ctx.Err(); err != nil {
		return FiscalPeriod{}, err
	}
	repository, ok := s.repo.(ContextCompanyScopedRepository)
	if !ok {
		return FiscalPeriod{}, ErrCompanyScopeRequired
	}
	period, err := repository.GetPeriodByDateForCompanyContext(ctx, companyID, date)
	if err != nil {
		return FiscalPeriod{}, err
	}
	if period.State != PeriodOpen {
		return FiscalPeriod{}, ErrFiscalPeriodClosed
	}
	return period, nil
}

func (s *Service) GetOpenAdjustmentPeriod(fiscalYearID uuid.UUID, periodNumber int) (FiscalPeriod, error) {
	if periodNumber != 13 && periodNumber != 14 {
		return FiscalPeriod{}, ErrFiscalPeriodNotFound
	}
	expectedType := FiscalPeriodTypeYearEndAdjustment
	if periodNumber == 14 {
		expectedType = FiscalPeriodTypeAuditAdjustment
	}
	fiscalYear, err := s.repo.GetFiscalYear(fiscalYearID)
	if err != nil {
		return FiscalPeriod{}, err
	}
	if fiscalYear.CompanyID != uuid.Nil {
		repository, ok := s.repo.(CompanyScopedRepository)
		if !ok {
			return FiscalPeriod{}, ErrCompanyScopeRequired
		}
		period, err := repository.GetPeriodByNumberForCompany(fiscalYear.CompanyID, fiscalYearID, periodNumber)
		if err != nil {
			return FiscalPeriod{}, err
		}
		if period.Type != expectedType {
			return FiscalPeriod{}, ErrFiscalPeriodNotFound
		}
		if period.State != PeriodOpen {
			return FiscalPeriod{}, ErrFiscalPeriodClosed
		}
		return period, nil
	}
	for _, period := range fiscalYear.Periods {
		if period.Number == periodNumber {
			if period.Type != expectedType {
				return FiscalPeriod{}, ErrFiscalPeriodNotFound
			}
			if period.State != PeriodOpen {
				return FiscalPeriod{}, ErrFiscalPeriodClosed
			}
			return period, nil
		}
	}
	return FiscalPeriod{}, ErrFiscalPeriodNotFound
}

func (s *Service) GetOpenAdjustmentPeriodForDate(date time.Time, periodNumber int) (FiscalPeriod, error) {
	regularPeriod, err := s.repo.GetPeriodByDate(date)
	if err != nil {
		return FiscalPeriod{}, err
	}
	return s.GetOpenAdjustmentPeriod(regularPeriod.FiscalYearID, periodNumber)
}

func (s *Service) GetOpenAdjustmentPeriodForCompanyDate(companyID uuid.UUID, date time.Time, periodNumber int) (FiscalPeriod, error) {
	if periodNumber != 13 && periodNumber != 14 {
		return FiscalPeriod{}, ErrFiscalPeriodNotFound
	}
	repository, ok := s.repo.(CompanyScopedRepository)
	if !ok {
		return FiscalPeriod{}, ErrCompanyScopeRequired
	}
	regularPeriod, err := repository.GetPeriodByDateForCompany(companyID, date)
	if err != nil {
		return FiscalPeriod{}, err
	}
	period, err := repository.GetPeriodByNumberForCompany(companyID, regularPeriod.FiscalYearID, periodNumber)
	if err != nil {
		return FiscalPeriod{}, err
	}
	expectedType := FiscalPeriodTypeYearEndAdjustment
	if periodNumber == 14 {
		expectedType = FiscalPeriodTypeAuditAdjustment
	}
	if period.Type != expectedType {
		return FiscalPeriod{}, ErrFiscalPeriodNotFound
	}
	if period.State != PeriodOpen {
		return FiscalPeriod{}, ErrFiscalPeriodClosed
	}
	return period, nil
}

func (s *Service) GetOpenAdjustmentPeriodForCompanyDateContext(ctx context.Context, companyID uuid.UUID, date time.Time, periodNumber int) (FiscalPeriod, error) {
	if periodNumber != 13 && periodNumber != 14 {
		return FiscalPeriod{}, ErrFiscalPeriodNotFound
	}
	if err := ctx.Err(); err != nil {
		return FiscalPeriod{}, err
	}
	repository, ok := s.repo.(ContextCompanyScopedRepository)
	if !ok {
		return FiscalPeriod{}, ErrCompanyScopeRequired
	}
	regularPeriod, err := repository.GetPeriodByDateForCompanyContext(ctx, companyID, date)
	if err != nil {
		return FiscalPeriod{}, err
	}
	period, err := repository.GetPeriodByNumberForCompanyContext(ctx, companyID, regularPeriod.FiscalYearID, periodNumber)
	if err != nil {
		return FiscalPeriod{}, err
	}
	expectedType := FiscalPeriodTypeYearEndAdjustment
	if periodNumber == 14 {
		expectedType = FiscalPeriodTypeAuditAdjustment
	}
	if period.Type != expectedType {
		return FiscalPeriod{}, ErrFiscalPeriodNotFound
	}
	if period.State != PeriodOpen {
		return FiscalPeriod{}, ErrFiscalPeriodClosed
	}
	return period, nil
}

func (s *Service) NextJournalNumber(journalID uuid.UUID) (int64, error) {
	return s.repo.GetNextSequenceWithLock(journalID)
}
