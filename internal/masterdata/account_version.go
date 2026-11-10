package masterdata

import (
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidEffectivePeriod = errors.New("invalid effective-dated account period")
	ErrOverlappingAccountVersion = errors.New("account versions have overlapping effective periods")
	ErrAccountVersionNotFound = errors.New("account version not found for date")
)

type AccountVersion struct {
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
	accountID := ordered[0].AccountID
	for i, version := range ordered {
		if version.AccountID == uuid.Nil || version.AccountID != accountID || version.EffectiveFrom.IsZero() ||
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
