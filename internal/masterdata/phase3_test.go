package masterdata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAccountHistoryPersistsEffectiveDatedVersions(t *testing.T) {
	repository := NewMemoryAccountVersionRepository()
	service := NewAccountHistoryService(repository)
	companyID, accountID := uuid.New(), uuid.New()
	firstFrom := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	secondFrom := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	firstEnd := secondFrom
	for _, version := range []AccountVersion{
		{CompanyID: companyID, AccountID: accountID, Name: "Legacy", EffectiveFrom: firstFrom, EffectiveTo: &firstEnd},
		{CompanyID: companyID, AccountID: accountID, Name: "Current", EffectiveFrom: secondFrom},
	} {
		if err := service.Save(context.Background(), version); err != nil {
			t.Fatal(err)
		}
	}
	version, err := service.AsOf(context.Background(), companyID, accountID, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || version.Name != "Legacy" {
		t.Fatalf("as-of account version = %#v, %v", version, err)
	}
	if err := service.Save(context.Background(), AccountVersion{
		CompanyID: companyID, AccountID: accountID, Name: "Overlap", EffectiveFrom: firstFrom.AddDate(0, 6, 0),
	}); !errors.Is(err, ErrOverlappingAccountVersion) {
		t.Fatalf("overlap save error = %v", err)
	}
}

func TestAccountVersionsResolveAsOfDateAndRejectOverlaps(t *testing.T) {
	companyID, accountID := uuid.New(), uuid.New()
	firstEnd := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	versions := []AccountVersion{
		{CompanyID: companyID, AccountID: accountID, Name: "Legacy", EffectiveFrom: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), EffectiveTo: &firstEnd},
		{CompanyID: companyID, AccountID: accountID, Name: "Current", EffectiveFrom: firstEnd},
	}
	got, err := AccountVersionAt(versions, time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || got.Name != "Legacy" {
		t.Fatalf("historical version = %#v, %v", got, err)
	}
	got, err = AccountVersionAt(versions, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || got.Name != "Current" {
		t.Fatalf("current version = %#v, %v", got, err)
	}
	overlapEnd := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	versions[0].EffectiveTo = &overlapEnd
	if err := ValidateAccountVersions(versions); !errors.Is(err, ErrOverlappingAccountVersion) {
		t.Fatalf("overlapping versions error = %v", err)
	}
}

func TestCostCenterHierarchyRejectsCrossCompanyParent(t *testing.T) {
	parentID, childID := uuid.New(), uuid.New()
	centers := []CostCenter{
		{ID: parentID, CompanyID: uuid.New(), Code: "HQ", Name: "Head office", Active: true},
		{ID: childID, CompanyID: uuid.New(), ParentID: &parentID, Code: "OPS", Name: "Operations", Active: true},
	}
	if err := ValidateCostCenterHierarchy(centers); !errors.Is(err, ErrInvalidCostCenter) {
		t.Fatalf("cross-company hierarchy error = %v, want invalid center", err)
	}
}
