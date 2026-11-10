package masterdata

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidEffectivePeriod    = errors.New("invalid effective-dated account period")
	ErrOverlappingAccountVersion = errors.New("account versions have overlapping effective periods")
	ErrAccountVersionNotFound    = errors.New("account version not found for date")
)

type AccountVersion struct {
	CompanyID     uuid.UUID
	AccountID     uuid.UUID
	Name          string
	AccountType   AccountType
	IFRSCategory  string
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
}

func ValidateAccountVersions(versions []AccountVersion) error {
	if len(versions) == 0 {
		return nil
	}
	ordered := append([]AccountVersion(nil), versions...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].EffectiveFrom.Before(ordered[j].EffectiveFrom)
	})
	accountID, companyID := ordered[0].AccountID, ordered[0].CompanyID
	for i, version := range ordered {
		if version.AccountID == uuid.Nil || version.AccountID != accountID ||
			version.CompanyID == uuid.Nil || version.CompanyID != companyID || version.EffectiveFrom.IsZero() ||
			(version.EffectiveTo != nil && !version.EffectiveTo.After(version.EffectiveFrom)) {
			return ErrInvalidEffectivePeriod
		}
		if i == 0 {
			continue
		}
		previous := ordered[i-1]
		if previous.EffectiveTo == nil || version.EffectiveFrom.Before(*previous.EffectiveTo) {
			return ErrOverlappingAccountVersion
		}
	}
	return nil
}

func AccountVersionAt(versions []AccountVersion, date time.Time) (AccountVersion, error) {
	if err := ValidateAccountVersions(versions); err != nil {
		return AccountVersion{}, err
	}
	for _, version := range versions {
		if !date.Before(version.EffectiveFrom) && (version.EffectiveTo == nil || date.Before(*version.EffectiveTo)) {
			return version, nil
		}
	}
	return AccountVersion{}, ErrAccountVersionNotFound
}

type AccountVersionRepository interface {
	SaveAccountVersion(ctx context.Context, version AccountVersion) error
	GetAccountVersionForCompanyAsOf(ctx context.Context, companyID, accountID uuid.UUID, asOf time.Time) (AccountVersion, error)
}

type AccountHistoryService struct {
	repository AccountVersionRepository
}

func NewAccountHistoryService(repository AccountVersionRepository) *AccountHistoryService {
	return &AccountHistoryService{repository: repository}
}

func (s *AccountHistoryService) Save(ctx context.Context, version AccountVersion) error {
	if s == nil || s.repository == nil {
		return ErrAccountVersionNotFound
	}
	if err := ValidateAccountVersions([]AccountVersion{version}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.repository.SaveAccountVersion(ctx, version)
}

func (s *AccountHistoryService) AsOf(ctx context.Context, companyID, accountID uuid.UUID, date time.Time) (AccountVersion, error) {
	if s == nil || s.repository == nil {
		return AccountVersion{}, ErrAccountVersionNotFound
	}
	if companyID == uuid.Nil || accountID == uuid.Nil {
		return AccountVersion{}, ErrAccountVersionNotFound
	}
	if err := ctx.Err(); err != nil {
		return AccountVersion{}, err
	}
	return s.repository.GetAccountVersionForCompanyAsOf(ctx, companyID, accountID, date)
}

type MemoryAccountVersionRepository struct {
	mu       sync.RWMutex
	versions map[uuid.UUID][]AccountVersion
}

func NewMemoryAccountVersionRepository() *MemoryAccountVersionRepository {
	return &MemoryAccountVersionRepository{versions: make(map[uuid.UUID][]AccountVersion)}
}

func (r *MemoryAccountVersionRepository) SaveAccountVersion(ctx context.Context, version AccountVersion) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	versions := append([]AccountVersion(nil), r.versions[version.AccountID]...)
	for _, current := range versions {
		if current.CompanyID != version.CompanyID {
			return ErrInvalidEffectivePeriod
		}
	}
	versions = append(versions, version)
	if err := ValidateAccountVersions(versions); err != nil {
		return err
	}
	r.versions[version.AccountID] = versions
	return nil
}

func (r *MemoryAccountVersionRepository) GetAccountVersionForCompanyAsOf(ctx context.Context, companyID, accountID uuid.UUID, asOf time.Time) (AccountVersion, error) {
	if err := ctx.Err(); err != nil {
		return AccountVersion{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	versions := r.versions[accountID]
	for _, version := range versions {
		if version.CompanyID != companyID {
			return AccountVersion{}, ErrAccountVersionNotFound
		}
	}
	return AccountVersionAt(versions, asOf)
}
