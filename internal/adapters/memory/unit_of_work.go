package memory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
	"github.com/shopspring/decimal"
)

type memoryIdempotencyKey struct {
	companyID uuid.UUID
	key       string
}

var (
	_ storage.UnitOfWorkRunner  = (*Driver)(nil)
	_ storage.TransactionRunner = (*Driver)(nil)
)

// Capabilities reports the guarantees Memory can provide within one process.
// The global transaction lock serializes unit-of-work callbacks but is not
// durable and does not provide database or cross-process isolation.
func (d *Driver) Capabilities() storage.AdapterCapabilities {
	return storage.AdapterCapabilities{
		AdapterName:            "memory",
		SupportsRepeatableRead: true,
		SupportsSerializable:   true,
		EnforcesForeignKeys:    false,
		GuaranteesDurability:   false,
		SupportsSavepoints:     false,
		DetectsDeadlocks:       false,
	}
}

func (d *Driver) Execute(ctx context.Context, fn func(context.Context, storage.UnitOfWork) error) error {
	return d.ExecuteWithOptions(ctx, storage.TxOptions{}, fn)
}

func (d *Driver) ExecuteWithOptions(ctx context.Context, opts storage.TxOptions, fn func(context.Context, storage.UnitOfWork) error) (err error) {
	if fn == nil {
		return errors.New("memory: nil unit-of-work callback")
	}
	switch opts.Isolation {
	case "", storage.IsolationReadCommitted, storage.IsolationRepeatableRead, storage.IsolationSerializable:
	default:
		return fmt.Errorf("memory: unsupported isolation level %q", opts.Isolation)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrTransactionAborted, err)
	}
	if _, nested := ctx.Value(transactionContextKey{}).(memoryTransactionScope); nested {
		return storage.ErrNestedUnitOfWork
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	tx := d.transactionSnapshotLocked(opts.ReadOnly)
	uow := &memoryUnitOfWork{driver: tx}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("memory: transaction callback panicked: %v", recovered)
		}
	}()
	txCtx := context.WithValue(ctx, transactionContextKey{}, memoryTransactionScope{
		root: d, driver: tx, readOnly: opts.ReadOnly,
	})
	if err = fn(txCtx, uow); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrTransactionAborted, err)
	}
	d.entries = tx.entries
	d.years = tx.years
	d.periods = tx.periods
	d.journalSeq = tx.journalSeq
	d.balances = tx.balances
	d.adjustingPeriods = tx.adjustingPeriods
	d.reconciliationExceptions = tx.reconciliationExceptions
	d.reconciliationExceptionLog = tx.reconciliationExceptionLog
	d.auditRecords = tx.auditRecords
	d.idempotency = tx.idempotency
	return nil
}

func (d *Driver) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return d.WithinTransactionWithOptions(ctx, storage.TxOptions{}, fn)
}

func (d *Driver) WithinTransactionWithOptions(ctx context.Context, opts storage.TxOptions, fn func(context.Context) error) error {
	return d.ExecuteWithOptions(ctx, opts, func(txCtx context.Context, _ storage.UnitOfWork) error {
		return fn(txCtx)
	})
}

func (d *Driver) transactionSnapshotLocked(readOnly bool) *Driver {
	tx := &Driver{
		readOnly:                   readOnly,
		accounts:                   d.accounts,
		companies:                  d.companies,
		journals:                   d.journals,
		years:                      cloneFiscalYears(d.years),
		periods:                    cloneMap(d.periods),
		entries:                    make(map[uuid.UUID]posting.JournalEntry, len(d.entries)),
		journalSeq:                 cloneMap(d.journalSeq),
		balances:                   cloneMap(d.balances),
		adjustingPeriods:           cloneMap(d.adjustingPeriods),
		reconciliationExceptions:   cloneMap(d.reconciliationExceptions),
		reconciliationExceptionLog: cloneReconciliationExceptionLog(d.reconciliationExceptionLog),
		auditRecords:               cloneAuditRecords(d.auditRecords),
		idempotency:                cloneIdempotency(d.idempotency),
		postingLocks:               d.postingLocks,
	}
	for id, entry := range d.entries {
		tx.entries[id] = cloneEntry(entry)
	}
	return tx
}

func cloneReconciliationExceptionLog(
	source map[uuid.UUID][]subledgers.ReconciliationExceptionTransition,
) map[uuid.UUID][]subledgers.ReconciliationExceptionTransition {
	result := make(map[uuid.UUID][]subledgers.ReconciliationExceptionTransition, len(source))
	for id, transitions := range source {
		result[id] = append([]subledgers.ReconciliationExceptionTransition(nil), transitions...)
	}
	return result
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	result := make(map[K]V, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneFiscalYears(source map[uuid.UUID]masterdata.FiscalYear) map[uuid.UUID]masterdata.FiscalYear {
	result := make(map[uuid.UUID]masterdata.FiscalYear, len(source))
	for id, year := range source {
		year.Periods = append([]masterdata.FiscalPeriod(nil), year.Periods...)
		result[id] = year
	}
	return result
}

func cloneAuditRecords(records []storage.AuditRecord) []storage.AuditRecord {
	result := make([]storage.AuditRecord, len(records))
	for i, record := range records {
		record.Payload = append([]byte(nil), record.Payload...)
		result[i] = record
	}
	return result
}

func cloneIdempotency(records map[memoryIdempotencyKey]storage.IdempotencyRecord) map[memoryIdempotencyKey]storage.IdempotencyRecord {
	result := make(map[memoryIdempotencyKey]storage.IdempotencyRecord, len(records))
	for key, record := range records {
		record.Response = append([]byte(nil), record.Response...)
		if record.EntryID != nil {
			id := *record.EntryID
			record.EntryID = &id
		}
		result[key] = record
	}
	return result
}

type memoryUnitOfWork struct {
	driver *Driver
}

func (u *memoryUnitOfWork) Drafts() storage.DraftStore     { return u.driver.Ports().Drafts }
func (u *memoryUnitOfWork) Entries() storage.EntryReader   { return u.driver.Ports().Entries }
func (u *memoryUnitOfWork) Posting() storage.PostingWriter { return u.driver.Ports().Posting }
func (u *memoryUnitOfWork) Sequences() storage.SequenceAllocator {
	return u.driver.Ports().Sequences
}
func (u *memoryUnitOfWork) Audit() storage.AuditWriter { return memoryAuditWriter{driver: u.driver} }
func (u *memoryUnitOfWork) Idempotency() storage.IdempotencyStore {
	return memoryIdempotencyStore{driver: u.driver}
}
func (u *memoryUnitOfWork) Balance() storage.BalanceReader {
	return memoryBalanceReader{driver: u.driver}
}

type memoryAuditWriter struct {
	driver *Driver
}

func (w memoryAuditWriter) AppendAudit(ctx context.Context, record storage.AuditRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if readOnlyDriver(ctx, w.driver) {
		return ErrReadOnlyTransaction
	}
	if record.CompanyID == types.CompanyID(uuid.Nil) || record.Action == "" {
		return errors.New("memory: audit company and action are required")
	}
	driver := transactionDriver(ctx, w.driver)
	driver.mu.Lock()
	defer driver.mu.Unlock()
	if record.OccurredAt.IsZero() {
		record.OccurredAt = time.Now().UTC()
	}
	record.Payload = append([]byte(nil), record.Payload...)
	driver.auditRecords = append(driver.auditRecords, record)
	return nil
}

type memoryIdempotencyStore struct {
	driver *Driver
}

func (s memoryIdempotencyStore) Claim(ctx context.Context, record storage.IdempotencyRecord) (storage.IdempotencyRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return storage.IdempotencyRecord{}, false, err
	}
	if readOnlyDriver(ctx, s.driver) {
		return storage.IdempotencyRecord{}, false, ErrReadOnlyTransaction
	}
	if record.CompanyID == types.CompanyID(uuid.Nil) || record.Key == "" || record.PayloadHash == "" {
		return storage.IdempotencyRecord{}, false, errors.New("memory: idempotency company, key, and payload hash are required")
	}
	driver := transactionDriver(ctx, s.driver)
	driver.mu.Lock()
	defer driver.mu.Unlock()
	key := memoryIdempotencyKey{companyID: uuid.UUID(record.CompanyID), key: record.Key}
	if existing, ok := driver.idempotency[key]; ok {
		if existing.PayloadHash != record.PayloadHash || existing.Operation != record.Operation {
			return storage.IdempotencyRecord{}, false, storage.ErrUniqueConstraint
		}
		return cloneIdempotency(map[memoryIdempotencyKey]storage.IdempotencyRecord{key: existing})[key], false, nil
	}
	record.Status = storage.IdempotencyStarted
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = record.CreatedAt
	if record.EntryID != nil {
		id := *record.EntryID
		record.EntryID = &id
	}
	record.Response = append([]byte(nil), record.Response...)
	driver.idempotency[key] = record
	return record, true, nil
}

func (s memoryIdempotencyStore) Complete(ctx context.Context, companyID types.CompanyID, key, payloadHash string, entryID types.JournalEntryID, response []byte) error {
	return s.update(ctx, companyID, key, payloadHash, func(record *storage.IdempotencyRecord) {
		record.Status = storage.IdempotencyCompleted
		id := entryID
		record.EntryID = &id
		record.Response = append([]byte(nil), response...)
	})
}

func (s memoryIdempotencyStore) Fail(ctx context.Context, companyID types.CompanyID, key, payloadHash, status string) error {
	if status != storage.IdempotencyRetryable && status != storage.IdempotencyTerminal {
		return errors.New("memory: idempotency failure status must be retryable_failure or terminal_failure")
	}
	return s.update(ctx, companyID, key, payloadHash, func(record *storage.IdempotencyRecord) {
		record.Status = status
	})
}

func (s memoryIdempotencyStore) update(ctx context.Context, companyID types.CompanyID, key, payloadHash string, apply func(*storage.IdempotencyRecord)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if readOnlyDriver(ctx, s.driver) {
		return ErrReadOnlyTransaction
	}
	driver := transactionDriver(ctx, s.driver)
	id := memoryIdempotencyKey{companyID: uuid.UUID(companyID), key: key}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	record, ok := driver.idempotency[id]
	if !ok || record.PayloadHash != payloadHash {
		return storage.ErrUniqueConstraint
	}
	apply(&record)
	record.UpdatedAt = time.Now().UTC()
	driver.idempotency[id] = record
	return nil
}

type memoryBalanceReader struct {
	driver *Driver
}

func (r memoryBalanceReader) GetAccountBalance(ctx context.Context, companyID types.CompanyID, accountID types.AccountID, asOf time.Time, currency string) (decimal.Decimal, error) {
	if err := ctx.Err(); err != nil {
		return decimal.Zero, err
	}
	d := transactionDriver(ctx, r.driver)
	d.mu.RLock()
	defer d.mu.RUnlock()
	total := decimal.Zero
	for _, entry := range d.entries {
		if entry.CompanyID != uuid.UUID(companyID) || entry.Status != posting.EntryPosted {
			continue
		}
		date := entry.EntryDate
		if date.IsZero() {
			date = entry.Date
		}
		if !asOf.IsZero() && date.After(asOf) {
			continue
		}
		for _, line := range entry.Lines {
			if line.AccountID != uuid.UUID(accountID) {
				continue
			}
			debit, credit, functionalCurrency := posting.LineFunctionalAmounts(line)
			if currency != "" && functionalCurrency != currency {
				continue
			}
			total = total.Add(debit).Sub(credit)
		}
	}
	return total, nil
}
