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

// Deprecated: use RegisterAccountContext.
func (s *Service) RegisterAccount(account Account) error {
	return s.RegisterAccountContext(context.Background(), account)
}

func (s *Service) RegisterAccountContext(ctx context.Context, account Account) error {
	if ctx == nil {
		return errors.New("account registration context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
		if _, err := s.getAccountByCodeForCompany(ctx, repository, account.CompanyID, account.Code); err == nil {
			return errors.New("duplicate account code")
		} else if !errors.Is(err, ErrAccountNotFound) {
			return err
		}
	} else if _, err := s.getAccountByCode(ctx, account.Code); err == nil {
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
			parent, err = s.getAccountByIDForCompany(ctx, repository, account.CompanyID, *account.ParentID)
		} else {
			parent, err = s.getAccountByID(ctx, *account.ParentID)
		}
		if err != nil {
			return ErrInvalidParent
		}
		if parent.AccountType != account.AccountType {
			return ErrInvalidParent
		}
		if hasCycle, err := s.accountWouldCycle(ctx, account.CompanyID, account.ID, *account.ParentID); err != nil {
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
	return s.createAccountContext(ctx, account)
}

func (s *Service) getAccountByCode(ctx context.Context, code string) (Account, error) {
	if repository, ok := s.repo.(interface {
		GetAccountByCodeContext(context.Context, string) (Account, error)
	}); ok {
		return repository.GetAccountByCodeContext(ctx, code)
	}
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	return s.repo.GetAccountByCode(code)
}

func (s *Service) getAccountByID(ctx context.Context, id uuid.UUID) (Account, error) {
	if repository, ok := s.repo.(interface {
		GetAccountByIDContext(context.Context, uuid.UUID) (Account, error)
	}); ok {
		return repository.GetAccountByIDContext(ctx, id)
	}
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	return s.repo.GetAccountByID(id)
}

func (s *Service) getAccountByCodeForCompany(
	ctx context.Context,
	repository CompanyScopedRepository,
	companyID uuid.UUID,
	code string,
) (Account, error) {
	if contextual, ok := s.repo.(interface {
		GetAccountByCodeForCompanyContext(context.Context, uuid.UUID, string) (Account, error)
	}); ok {
		return contextual.GetAccountByCodeForCompanyContext(ctx, companyID, code)
	}
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	return repository.GetAccountByCodeForCompany(companyID, code)
}

func (s *Service) getAccountByIDForCompany(
	ctx context.Context,
	repository CompanyScopedRepository,
	companyID, accountID uuid.UUID,
) (Account, error) {
	if contextual, ok := s.repo.(ContextCompanyScopedRepository); ok {
		return contextual.GetAccountByIDForCompanyContext(ctx, companyID, accountID)
	}
	if err := ctx.Err(); err != nil {
		return Account{}, err
	}
	return repository.GetAccountByIDForCompany(companyID, accountID)
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

// Deprecated: use UpdateAccountParentContext.
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

// Deprecated: use ValidateCanPostToAccountContext.
func (s *Service) ValidateCanPostToAccount(accountID uuid.UUID) error {
	return s.ValidateCanPostToAccountContext(context.Background(), accountID)
}

func (s *Service) ValidateCanPostToAccountContext(ctx context.Context, accountID uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	account, err := s.getAccountByID(ctx, accountID)
	if err != nil {
		return err
	}
	if account.IsHeader {
		return errors.New("cannot post to header account")
	}
	var hasChildren bool
	if repository, ok := s.repo.(interface {
		HasChildrenContext(context.Context, uuid.UUID) (bool, error)
	}); ok {
		hasChildren, err = repository.HasChildrenContext(ctx, accountID)
		if err != nil {
			return err
		}
		if hasChildren {
			return errors.New("cannot post to parent account with children")
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return validatePostableAccount(s.repo, accountID, account)
}

// Deprecated: use GetCompanyByIDContext.
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

// Deprecated: use ValidateCanPostToAccountForCompanyContext.
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

// Deprecated: use GetOpenPeriodForDateContext.
func (s *Service) GetOpenPeriodForDate(date time.Time) (FiscalPeriod, error) {
	return s.GetOpenPeriodForDateContext(context.Background(), date)
}

func (s *Service) GetOpenPeriodForDateContext(ctx context.Context, date time.Time) (FiscalPeriod, error) {
	if err := ctx.Err(); err != nil {
		return FiscalPeriod{}, err
	}
	period, err := s.getPeriodByDate(ctx, date)
	if err != nil {
		return FiscalPeriod{}, err
	}
	if period.State != PeriodOpen {
		return FiscalPeriod{}, errors.New("fiscal period is closed")
	}
	return period, nil
}

// Deprecated: use GetOpenPeriodForCompanyDateContext.
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

// Deprecated: use GetOpenAdjustmentPeriodContext.
func (s *Service) GetOpenAdjustmentPeriod(fiscalYearID uuid.UUID, periodNumber int) (FiscalPeriod, error) {
	return s.GetOpenAdjustmentPeriodContext(context.Background(), fiscalYearID, periodNumber)
}

func (s *Service) GetOpenAdjustmentPeriodContext(ctx context.Context, fiscalYearID uuid.UUID, periodNumber int) (FiscalPeriod, error) {
	if periodNumber != 13 && periodNumber != 14 {
		return FiscalPeriod{}, ErrFiscalPeriodNotFound
	}
	if err := ctx.Err(); err != nil {
		return FiscalPeriod{}, err
	}
	expectedType := FiscalPeriodTypeYearEndAdjustment
	if periodNumber == 14 {
		expectedType = FiscalPeriodTypeAuditAdjustment
	}
	fiscalYear, err := s.getFiscalYear(ctx, fiscalYearID)
	if err != nil {
		return FiscalPeriod{}, err
	}
	if fiscalYear.CompanyID != uuid.Nil {
		repository, ok := s.repo.(CompanyScopedRepository)
		if !ok {
			return FiscalPeriod{}, ErrCompanyScopeRequired
		}
		period, err := s.getPeriodByNumberForCompany(ctx, repository, fiscalYear.CompanyID, fiscalYearID, periodNumber)
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

// Deprecated: use GetOpenAdjustmentPeriodForDateContext.
func (s *Service) GetOpenAdjustmentPeriodForDate(date time.Time, periodNumber int) (FiscalPeriod, error) {
	return s.GetOpenAdjustmentPeriodForDateContext(context.Background(), date, periodNumber)
}

func (s *Service) GetOpenAdjustmentPeriodForDateContext(ctx context.Context, date time.Time, periodNumber int) (FiscalPeriod, error) {
	if err := ctx.Err(); err != nil {
		return FiscalPeriod{}, err
	}
	regularPeriod, err := s.getPeriodByDate(ctx, date)
	if err != nil {
		return FiscalPeriod{}, err
	}
	return s.GetOpenAdjustmentPeriodContext(ctx, regularPeriod.FiscalYearID, periodNumber)
}

// Deprecated: use GetOpenAdjustmentPeriodForCompanyDateContext.
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

// Deprecated: use NextJournalNumberContext.
func (s *Service) NextJournalNumber(journalID uuid.UUID) (int64, error) {
	return s.NextJournalNumberContext(context.Background(), journalID)
}

func (s *Service) NextJournalNumberContext(ctx context.Context, journalID uuid.UUID) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if repository, ok := s.repo.(interface {
		GetNextSequenceWithLockContext(context.Context, uuid.UUID) (int64, error)
	}); ok {
		return repository.GetNextSequenceWithLockContext(ctx, journalID)
	}
	return s.repo.GetNextSequenceWithLock(journalID)
}

func (s *Service) getPeriodByDate(ctx context.Context, date time.Time) (FiscalPeriod, error) {
	if repository, ok := s.repo.(interface {
		GetPeriodByDateContext(context.Context, time.Time) (FiscalPeriod, error)
	}); ok {
		return repository.GetPeriodByDateContext(ctx, date)
	}
	if err := ctx.Err(); err != nil {
		return FiscalPeriod{}, err
	}
	return s.repo.GetPeriodByDate(date)
}

func (s *Service) getFiscalYear(ctx context.Context, fiscalYearID uuid.UUID) (FiscalYear, error) {
	if repository, ok := s.repo.(interface {
		GetFiscalYearContext(context.Context, uuid.UUID) (FiscalYear, error)
	}); ok {
		return repository.GetFiscalYearContext(ctx, fiscalYearID)
	}
	if err := ctx.Err(); err != nil {
		return FiscalYear{}, err
	}
	return s.repo.GetFiscalYear(fiscalYearID)
}

func (s *Service) getPeriodByNumberForCompany(
	ctx context.Context,
	repository CompanyScopedRepository,
	companyID, fiscalYearID uuid.UUID,
	number int,
) (FiscalPeriod, error) {
	if contextual, ok := s.repo.(ContextCompanyScopedRepository); ok {
		return contextual.GetPeriodByNumberForCompanyContext(ctx, companyID, fiscalYearID, number)
	}
	if err := ctx.Err(); err != nil {
		return FiscalPeriod{}, err
	}
	return repository.GetPeriodByNumberForCompany(companyID, fiscalYearID, number)
}
