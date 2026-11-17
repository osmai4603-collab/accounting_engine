package subledger

import (
	"context"
	"errors"
	"time"

	apisubledger "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/subledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

var (
	// ErrARAPStoresRequired indicates incomplete dependencies for the AR/AP service.
	ErrARAPStoresRequired = errors.New("AR/AP stores and balance reader are required")
	// ErrARAPHistoryUnavailable indicates the stores cannot provide the requested historical snapshot.
	ErrARAPHistoryUnavailable = errors.New("AR/AP historical subledger snapshots are unavailable")
	// ErrPostedARAPPersistenceFailed indicates posting succeeded but updating the AR/AP store failed.
	ErrPostedARAPPersistenceFailed = errors.New("accounting entry posted but AR/AP persistence failed")
	// ErrInvalidARAPOperation indicates invalid accounting-event or inquiry input.
	ErrInvalidARAPOperation = errors.New("invalid AR/AP operation")
)

// ARAPStores is the complete public dependency set for the optional AR/AP
// capability. Balance is used only for reconciliation evidence.
type ARAPStores struct {
	Receivables ReceivableStore
	Payables    PayableStore
	Balance     storage.BalanceReader
}

// ARAPService separates accounting events from non-accounting inquiries.
// CreateInvoice, RecordPayment and WriteOff post balanced entries through the
// application layer. Read, aging and reconciliation methods are inquiry-only.
type ARAPService interface {
	CreateReceivableInvoice(context.Context, apisubledger.InvoiceCommand) (apisubledger.InvoiceResult, error)
	CreatePayableInvoice(context.Context, apisubledger.InvoiceCommand) (apisubledger.InvoiceResult, error)
	RecordPayment(context.Context, apisubledger.PaymentCommand) (apisubledger.PaymentResult, error)
	WriteOff(context.Context, apisubledger.WriteOffCommand) (types.JournalEntryID, error)
	GetReceivable(context.Context, types.CompanyID, string) (apisubledger.Receivable, error)
	ListReceivables(context.Context, types.CompanyID, string) ([]apisubledger.Receivable, error)
	GetPayable(context.Context, types.CompanyID, string) (apisubledger.Payable, error)
	ListPayables(context.Context, types.CompanyID, string) ([]apisubledger.Payable, error)
	Aging(context.Context, types.CompanyID, apisubledger.SubledgerType, string, time.Time) ([]apisubledger.AgingBucket, error)
	Reconcile(context.Context, types.CompanyID, apisubledger.SubledgerType, types.AccountID, string, time.Time) (apisubledger.ReconciliationEvidence, error)
}

// ReceivableStore defines persistence and operations for AR subledger.
type ReceivableStore interface {
	SaveReceivable(ctx context.Context, companyID types.CompanyID, r apisubledger.Receivable) error
	GetReceivable(ctx context.Context, companyID types.CompanyID, id string) (apisubledger.Receivable, error)
	ListReceivables(ctx context.Context, companyID types.CompanyID, partnerID string) ([]apisubledger.Receivable, error)
}

// PayableStore defines persistence and operations for AP subledger.
type PayableStore interface {
	SavePayable(ctx context.Context, companyID types.CompanyID, p apisubledger.Payable) error
	GetPayable(ctx context.Context, companyID types.CompanyID, id string) (apisubledger.Payable, error)
	ListPayables(ctx context.Context, companyID types.CompanyID, partnerID string) ([]apisubledger.Payable, error)
}

// FixedAssetStore defines persistence and operations for Fixed Assets subledger.
type FixedAssetStore interface {
	SaveAsset(ctx context.Context, companyID types.CompanyID, asset apisubledger.FixedAsset) error
	GetAsset(ctx context.Context, companyID types.CompanyID, id string) (apisubledger.FixedAsset, error)
	ListAssets(ctx context.Context, companyID types.CompanyID) ([]apisubledger.FixedAsset, error)
}

// DepreciationCalculator defines methods for computing asset depreciation.
type DepreciationCalculator interface {
	ComputeMonthlyDepreciation(asset apisubledger.FixedAsset) (types.Money, error)
}

// TreasuryStore defines persistence and operations for Treasury subledger.
type TreasuryStore interface {
	SaveBankAccount(ctx context.Context, companyID types.CompanyID, bank apisubledger.BankAccount) error
	GetBankAccount(ctx context.Context, companyID types.CompanyID, id string) (apisubledger.BankAccount, error)
	SaveTransaction(ctx context.Context, companyID types.CompanyID, tx apisubledger.TreasuryTransaction) error
}
