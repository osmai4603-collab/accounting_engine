package types

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestIdentifierTypesAreDistinct(t *testing.T) {
	ids := []any{
		CompanyID(uuid.UUID{}),
		AccountID(uuid.UUID{}),
		FiscalYearID(uuid.UUID{}),
		FiscalPeriodID(uuid.UUID{}),
		JournalEntryID(uuid.UUID{}),
		JournalEntryLineID(uuid.UUID{}),
		PartnerID(uuid.UUID{}),
		CostCenterID(uuid.UUID{}),
		ProjectID(uuid.UUID{}),
		SegmentID(uuid.UUID{}),
		BusinessUnitID(uuid.UUID{}),
	}

	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			if reflect.TypeOf(ids[i]) == reflect.TypeOf(ids[j]) {
				t.Errorf("%T and %T unexpectedly share a type", ids[i], ids[j])
			}
		}
	}
}
