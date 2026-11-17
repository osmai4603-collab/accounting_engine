package sdk_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/subledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicsubledger "github.com/osmai4603-collab/accounting_engine/pkg/spi/subledger"
	"github.com/shopspring/decimal"
)

func TestP1001PublicARAPContractsAreConsumableAndOptional(t *testing.T) {
	stores := publicsubledger.ARAPStores{
		Receivables: p1001ExternalReceivableStore{},
		Payables:    p1001ExternalPayableStore{},
		Balance:     p1001ExternalBalanceReader{},
	}
	if _, err := sdk.NewBuilder(sdk.WithSubledgerARAP(stores)).Build(context.Background()); err == nil ||
		strings.Contains(err.Error(), "AR/AP requires") {
		t.Fatalf("valid AR/AP dependencies were rejected: %v", err)
	}
	if _, err := sdk.NewBuilder(sdk.WithSubledgerARAP(publicsubledger.ARAPStores{})).Build(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "AR/AP requires") {
		t.Fatal("builder accepted missing AR/AP stores")
	}
	var typedNil *p1001ExternalReceivableStore
	if _, err := sdk.NewBuilder(sdk.WithSubledgerARAP(publicsubledger.ARAPStores{
		Receivables: typedNil, Payables: p1001ExternalPayableStore{},
		Balance: p1001ExternalBalanceReader{},
	})).Build(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "AR/AP requires") {
		t.Fatal("builder accepted a typed-nil AR/AP dependency")
	}
	if _, err := sdk.SubledgerARAP(nil); !errors.Is(err, types.ErrCapabilityNotImplemented) {
		t.Fatalf("disabled optional capability error = %v", err)
	}

	// Verify that public request and response DTOs can be constructed without
	// importing any internal implementation package.
	_ = subledger.InvoiceCommand{Amount: types.Money{}}
	_ = subledger.PaymentCommand{Allocations: []subledger.PaymentAllocation{}}
	_ = subledger.WriteOffCommand{}
}

type p1001ExternalReceivableStore struct{}

func (p1001ExternalReceivableStore) SaveReceivable(context.Context, types.CompanyID, subledger.Receivable) error {
	return nil
}
func (p1001ExternalReceivableStore) GetReceivable(context.Context, types.CompanyID, string) (subledger.Receivable, error) {
	return subledger.Receivable{}, nil
}
func (p1001ExternalReceivableStore) ListReceivables(context.Context, types.CompanyID, string) ([]subledger.Receivable, error) {
	return nil, nil
}

type p1001ExternalPayableStore struct{}

func (p1001ExternalPayableStore) SavePayable(context.Context, types.CompanyID, subledger.Payable) error {
	return nil
}
func (p1001ExternalPayableStore) GetPayable(context.Context, types.CompanyID, string) (subledger.Payable, error) {
	return subledger.Payable{}, nil
}
func (p1001ExternalPayableStore) ListPayables(context.Context, types.CompanyID, string) ([]subledger.Payable, error) {
	return nil, nil
}

type p1001ExternalBalanceReader struct{}

func (p1001ExternalBalanceReader) GetAccountBalance(context.Context, types.CompanyID, types.AccountID, time.Time, string) (decimal.Decimal, error) {
	return decimal.Zero, nil
}
