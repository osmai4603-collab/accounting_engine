package memory

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
)

type transactionContextKey struct{}

type memoryTransactionScope struct {
	root     *Driver
	driver   *Driver
	readOnly bool
}

var ErrReadOnlyTransaction = errors.New("memory: write attempted in read-only transaction")

func readOnlyTransaction(ctx context.Context) bool {
	scope, ok := ctx.Value(transactionContextKey{}).(memoryTransactionScope)
	return ok && scope.readOnly
}

func readOnlyDriver(ctx context.Context, driver *Driver) bool {
	return driver.readOnly || readOnlyTransaction(ctx)
}

func transactionDriver(ctx context.Context, driver *Driver) *Driver {
	scope, ok := ctx.Value(transactionContextKey{}).(memoryTransactionScope)
	if ok && scope.root == driver {
		return scope.driver
	}
	return driver
}

// transactionAwareDriver keeps ordinary SPI ports bound to the active
// transaction when callers pass the callback context to each port operation.
type transactionAwareDriver struct {
	driver *Driver
}

func (d transactionAwareDriver) SaveDraftContext(ctx context.Context, entry posting.JournalEntry) error {
	if readOnlyDriver(ctx, d.driver) {
		return ErrReadOnlyTransaction
	}
	return transactionDriver(ctx, d.driver).SaveDraftContext(ctx, entry)
}

func (d transactionAwareDriver) GetEntryByIDForCompanyContext(ctx context.Context, companyID, entryID uuid.UUID) (posting.JournalEntry, error) {
	return transactionDriver(ctx, d.driver).GetEntryByIDForCompanyContext(ctx, companyID, entryID)
}

func (d transactionAwareDriver) PostAtomicContext(ctx context.Context, entry posting.JournalEntry) error {
	if readOnlyDriver(ctx, d.driver) {
		return ErrReadOnlyTransaction
	}
	return transactionDriver(ctx, d.driver).PostAtomicContext(ctx, entry)
}

func (d transactionAwareDriver) PostReversalAtomicContext(ctx context.Context, original, reversal posting.JournalEntry) (posting.JournalEntry, error) {
	if readOnlyDriver(ctx, d.driver) {
		return posting.JournalEntry{}, ErrReadOnlyTransaction
	}
	return transactionDriver(ctx, d.driver).PostReversalAtomicContext(ctx, original, reversal)
}

func (d transactionAwareDriver) FindReversalByIdempotencyKeyContext(ctx context.Context, companyID uuid.UUID, key string) (posting.JournalEntry, bool, error) {
	return transactionDriver(ctx, d.driver).FindReversalByIdempotencyKeyContext(ctx, companyID, key)
}

func (d transactionAwareDriver) CreateAndPostReversalAtomicContext(ctx context.Context, original, reversal posting.JournalEntry) (posting.JournalEntry, error) {
	if readOnlyDriver(ctx, d.driver) {
		return posting.JournalEntry{}, ErrReadOnlyTransaction
	}
	return transactionDriver(ctx, d.driver).CreateAndPostReversalAtomicContext(ctx, original, reversal)
}

func (d transactionAwareDriver) GetNextSequenceForCompanyContext(ctx context.Context, companyID, journalID uuid.UUID) (int64, error) {
	if readOnlyDriver(ctx, d.driver) {
		return 0, ErrReadOnlyTransaction
	}
	return transactionDriver(ctx, d.driver).GetNextSequenceForCompanyContext(ctx, companyID, journalID)
}

func (d transactionAwareDriver) GetCompanyByIDContext(ctx context.Context, id uuid.UUID) (masterdata.Company, error) {
	return transactionDriver(ctx, d.driver).GetCompanyByIDContext(ctx, id)
}

func (d transactionAwareDriver) GetAccountByIDForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID) (masterdata.Account, error) {
	return transactionDriver(ctx, d.driver).GetAccountByIDForCompanyContext(ctx, companyID, accountID)
}

func (d transactionAwareDriver) GetAccountByCodeForCompany(ctx context.Context, companyID uuid.UUID, code string) (masterdata.Account, error) {
	return transactionDriver(ctx, d.driver).GetAccountByCodeForCompany(ctx, companyID, code)
}

func (d transactionAwareDriver) GetJournalByCodeForCompany(ctx context.Context, companyID uuid.UUID, code string) (masterdata.Journal, error) {
	return transactionDriver(ctx, d.driver).GetJournalByCodeForCompany(ctx, companyID, code)
}

func (d transactionAwareDriver) HasChildrenForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID) (bool, error) {
	return transactionDriver(ctx, d.driver).HasChildrenForCompanyContext(ctx, companyID, accountID)
}

func (d transactionAwareDriver) GetFiscalYearForCompany(ctx context.Context, companyID, yearID uuid.UUID) (masterdata.FiscalYear, error) {
	return transactionDriver(ctx, d.driver).GetFiscalYearForCompany(ctx, companyID, yearID)
}

func (d transactionAwareDriver) GetPeriodByDateForCompany(ctx context.Context, companyID uuid.UUID, date time.Time) (masterdata.FiscalPeriod, error) {
	return transactionDriver(ctx, d.driver).GetPeriodByDateForCompany(ctx, companyID, date)
}
