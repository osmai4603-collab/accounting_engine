package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"

	"github.com/google/uuid"
)

type failSecondContextCheck struct {
	context.Context
	checks int
	err    error
}

func (c *failSecondContextCheck) Err() error {
	c.checks++
	if c.checks == 2 {
		return c.err
	}
	return nil
}

func TestGetBalanceSheetReturnsIncomeStatementError(t *testing.T) {
	wantErr := errors.New("income statement unavailable")
	ctx := &failSecondContextCheck{Context: context.Background(), err: wantErr}
	driver := New()

	_, err := driver.GetBalanceSheet(ctx, types.CompanyID(uuid.New()), time.Now(), "USD")
	if !errors.Is(err, wantErr) {
		t.Fatalf("GetBalanceSheet error = %v, want %v", err, wantErr)
	}
}
