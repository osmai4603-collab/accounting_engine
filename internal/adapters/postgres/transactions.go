package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/osmai4603-collab/accounting_engine/internal/governance"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/portbridge"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
	"github.com/shopspring/decimal"
)

var (
	_ storage.TransactionRunner         = (*Driver)(nil)
	_ storage.AdvancedTransactionRunner = (*Driver)(nil)
	_ storage.UnitOfWorkRunner          = (*Driver)(nil)
)

type transactionContextKey struct{}

type transactionScope struct {
	tx        pgx.Tx
	companyID uuid.UUID
	state     *joinedTransactionState
}

type joinedTransactionState struct {
	mu           sync.Mutex
	rollbackOnly bool
}

type joinedTx struct {
	pgx.Tx
	state *joinedTransactionState
	mu    sync.Mutex
	done  bool
}

func (tx *joinedTx) Commit(context.Context) error {
	tx.mu.Lock()
	tx.done = true
	tx.mu.Unlock()
	return nil
}

func (tx *joinedTx) Rollback(context.Context) error {
	tx.mu.Lock()
	done := tx.done
	tx.done = true
	tx.mu.Unlock()
	if !done {
		tx.state.mu.Lock()
		tx.state.rollbackOnly = true
		tx.state.mu.Unlock()
	}
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

func (d *Driver) Execute(ctx context.Context, fn func(context.Context, storage.UnitOfWork) error) error {
	return d.ExecuteWithOptions(ctx, storage.TxOptions{}, fn)
}

func (d *Driver) ExecuteWithOptions(ctx context.Context, opts storage.TxOptions, fn func(context.Context, storage.UnitOfWork) error) (result error) {
	if ctx == nil {
		return errors.New("postgres: transaction context is nil")
	}
	if fn == nil {
		return errors.New("postgres: transaction callback is nil")
	}
	if _, nested := ctx.Value(transactionContextKey{}).(transactionScope); nested {
		return errors.New("postgres: nested unit of work execution is not supported")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", storage.ErrTransactionAborted, err)
	}
	companyID, ok := storage.CompanyFromContext(ctx)
	if !ok || uuid.UUID(companyID) == uuid.Nil {
		return errors.New("postgres: transaction requires company context")
	}
	pgxOptions, err := pgxTxOptions(opts)
	if err != nil {
		return err
	}
	tx, err := d.pool.BeginTx(ctx, pgxOptions)
	if err != nil {
		return translateTransactionError(err)
	}
	rollbackState := &joinedTransactionState{}
	rollback := func() {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			result = errors.Join(result, fmt.Errorf("rollback postgres unit of work: %w", rollbackErr))
		}
	}
	defer rollback()

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_company_id', $1, true)", uuid.UUID(companyID).String()); err != nil {
		return translateTransactionError(err)
	}
	if actorID, ok := storage.ActorFromContext(ctx); ok {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_actor_id', $1, true)", actorID); err != nil {
			return translateTransactionError(err)
		}
	}
	traceparent := TraceparentFromContext(ctx)
	if traceparent != "" {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.traceparent', $1, true)", traceparent); err != nil {
			return translateTransactionError(err)
		}
		tx = newTracedTx(tx, traceparent)
	}

	scope := transactionScope{tx: tx, companyID: uuid.UUID(companyID), state: rollbackState}
	txCtx := context.WithValue(ctx, transactionContextKey{}, scope)
	if err := fn(txCtx, &postgresUnitOfWork{driver: d, tx: tx}); err != nil {
		return translateTransactionError(err)
	}
	rollbackState.mu.Lock()
	rollbackOnly := rollbackState.rollbackOnly
	rollbackState.mu.Unlock()
	if rollbackOnly {
		return storage.ErrTransactionAborted
	}
	if err := tx.Commit(ctx); err != nil {
		return translateTransactionError(err)
	}
	return nil
}

func pgxTxOptions(opts storage.TxOptions) (pgx.TxOptions, error) {
	options := pgx.TxOptions{AccessMode: pgx.ReadWrite}
	if opts.ReadOnly {
		options.AccessMode = pgx.ReadOnly
	}
	switch opts.Isolation {
	case "", storage.IsolationReadCommitted:
		options.IsoLevel = pgx.ReadCommitted
	case storage.IsolationRepeatableRead:
		options.IsoLevel = pgx.RepeatableRead
	case storage.IsolationSerializable:
		options.IsoLevel = pgx.Serializable
	default:
		return pgx.TxOptions{}, fmt.Errorf("postgres: unsupported isolation level %q", opts.Isolation)
	}
	return options, nil
}

func translateTransactionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", storage.ErrTransactionAborted, err)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.Code {
	case "40001":
		return fmt.Errorf("%w: %v", storage.ErrSerializationFailure, err)
	case "40P01":
		return fmt.Errorf("%w: %v", storage.ErrDeadlockDetected, err)
	case "23505":
		return fmt.Errorf("%w: %v", storage.ErrUniqueConstraint, err)
	case "57014":
		return fmt.Errorf("%w: %v", storage.ErrTransactionAborted, err)
	default:
		return err
	}
}

func (d *Driver) beginCompanyTx(ctx context.Context, companyID uuid.UUID) (pgx.Tx, error) {
	if scope, ok := ctx.Value(transactionContextKey{}).(transactionScope); ok {
		if scope.companyID != companyID {
			return nil, errors.New("postgres: transaction company does not match requested company")
		}
		return &joinedTx{Tx: scope.tx, state: scope.state}, nil
	}
	return d.beginCompanyTxUnscoped(ctx, companyID)
}

func (d *Driver) beginCompanyTxUnscoped(ctx context.Context, companyID uuid.UUID) (pgx.Tx, error) {
	if companyID == uuid.Nil {
		return nil, errors.New("company transaction requires company id")
	}
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_company_id', $1, true)", companyID.String()); err != nil {
		if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return nil, errors.Join(err, fmt.Errorf("rollback tenant context transaction: %w", rollbackErr))
		}
		return nil, err
	}
	if actorID, ok := storage.ActorFromContext(ctx); ok {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_actor_id', $1, true)", actorID); err != nil {
			if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return nil, errors.Join(err, fmt.Errorf("rollback actor context transaction: %w", rollbackErr))
			}
			return nil, err
		}
	}
	traceparent := TraceparentFromContext(ctx)
	if traceparent != "" {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.traceparent', $1, true)", traceparent); err != nil {
			if rollbackErr := tx.Rollback(context.WithoutCancel(ctx)); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
				return nil, errors.Join(err, fmt.Errorf("rollback traceparent context transaction: %w", rollbackErr))
			}
			return nil, err
		}
	}
	return newTracedTx(tx, traceparent), nil
}

type postgresUnitOfWork struct {
	driver *Driver
	tx     pgx.Tx
}

func (u *postgresUnitOfWork) Drafts() storage.DraftStore            { return u }
func (u *postgresUnitOfWork) Entries() storage.EntryReader          { return u }
func (u *postgresUnitOfWork) Posting() storage.PostingWriter        { return u }
func (u *postgresUnitOfWork) Sequences() storage.SequenceAllocator  { return u }
func (u *postgresUnitOfWork) Audit() storage.AuditWriter            { return u }
func (u *postgresUnitOfWork) Idempotency() storage.IdempotencyStore { return u }
func (u *postgresUnitOfWork) Balance() storage.BalanceReader        { return u }

var (
	_ storage.UnitOfWork        = (*postgresUnitOfWork)(nil)
	_ storage.DraftStore        = (*postgresUnitOfWork)(nil)
	_ storage.EntryReader       = (*postgresUnitOfWork)(nil)
	_ storage.PostingWriter     = (*postgresUnitOfWork)(nil)
	_ storage.SequenceAllocator = (*postgresUnitOfWork)(nil)
	_ storage.AuditWriter       = (*postgresUnitOfWork)(nil)
	_ storage.IdempotencyStore  = (*postgresUnitOfWork)(nil)
	_ storage.BalanceReader     = (*postgresUnitOfWork)(nil)
)

func (u *postgresUnitOfWork) SaveDraft(ctx context.Context, entry ledger.JournalEntry) error {
	internal, err := portbridge.ToInternalEntry(entry, portbridge.EntryOptions{
		GenerateMissingIDs: true,
		Now:                func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return err
	}
	if err := u.driver.saveEntryTx(ctx, u.tx, internal); err != nil {
		return translateTransactionError(err)
	}
	return nil
}

func (u *postgresUnitOfWork) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	var data []byte
	err := u.tx.QueryRow(ctx, `SELECT entry FROM account_moves WHERE company_id=$1 AND id=$2`,
		uuid.UUID(companyID), uuid.UUID(entryID)).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.JournalEntry{}, posting.ErrEntryNotFound
	}
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	var internal posting.JournalEntry
	if err := json.Unmarshal(data, &internal); err != nil {
		return ledger.JournalEntry{}, err
	}
	return portbridge.ToPublicEntry(internal)
}

func (u *postgresUnitOfWork) NextSequence(ctx context.Context, companyID types.CompanyID, journalID types.JournalID) (int64, error) {
	var sequence int64
	err := u.tx.QueryRow(ctx, `UPDATE journals SET current_sequence=current_sequence+1
		WHERE company_id=$1 AND id=$2 RETURNING current_sequence`, uuid.UUID(companyID), uuid.UUID(journalID)).Scan(&sequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, masterdata.ErrJournalNotFound
	}
	return sequence, translateTransactionError(err)
}

func (u *postgresUnitOfWork) CommitPosting(ctx context.Context, commit storage.PostingCommit) (ledger.JournalEntry, error) {
	internal, err := portbridge.ToInternalEntry(commit.Entry, portbridge.EntryOptions{})
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	if internal.Status != posting.EntryPosted {
		return ledger.JournalEntry{}, posting.ErrInvalidEntry
	}
	if commit.ActorID != "" {
		internal.PostedBy = commit.ActorID
	}
	if commit.Reason != "" {
		internal.ReversalReason = commit.Reason
	}
	if internal.SequenceNumber == 0 {
		sequence, err := u.NextSequence(ctx, commit.Entry.CompanyID, commit.Entry.JournalID)
		if err != nil {
			return ledger.JournalEntry{}, err
		}
		internal.SequenceNumber = sequence
	}
	if commit.Operation == storage.OperationReverse {
		if commit.OriginalEntryID == nil {
			return ledger.JournalEntry{}, errors.New("postgres: reversal requires original entry id")
		}
		return u.commitReversal(ctx, internal, uuid.UUID(*commit.OriginalEntryID))
	}
	if commit.Operation != "" && commit.Operation != storage.OperationPost {
		return ledger.JournalEntry{}, fmt.Errorf("postgres: unsupported posting operation %q", commit.Operation)
	}
	if err := u.driver.saveEntryTx(ctx, u.tx, internal); err != nil {
		return ledger.JournalEntry{}, translateTransactionError(err)
	}
	result, err := portbridge.ToPublicEntry(internal)
	return result, err
}

func (u *postgresUnitOfWork) commitReversal(ctx context.Context, reversal posting.JournalEntry, originalID uuid.UUID) (ledger.JournalEntry, error) {
	var originalData []byte
	if err := u.tx.QueryRow(ctx, `SELECT entry FROM account_moves WHERE company_id=$1 AND id=$2 FOR UPDATE`,
		reversal.CompanyID, originalID).Scan(&originalData); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ledger.JournalEntry{}, posting.ErrEntryNotFound
		}
		return ledger.JournalEntry{}, err
	}
	var original posting.JournalEntry
	if err := json.Unmarshal(originalData, &original); err != nil {
		return ledger.JournalEntry{}, err
	}
	if original.Status != posting.EntryPosted || original.ReversedEntryID != nil {
		return ledger.JournalEntry{}, posting.ErrEntryAlreadyReversed
	}
	if err := posting.ValidateReversalLines(original, reversal); err != nil {
		return ledger.JournalEntry{}, err
	}
	reversal.Classification = posting.EntryClassReversing
	draft := reversal
	draft.Status = posting.EntryDraft
	draft.PostedBy = ""
	draft.PostedAt = nil
	if err := u.driver.saveEntryTx(ctx, u.tx, draft); err != nil {
		return ledger.JournalEntry{}, err
	}
	if err := u.driver.saveEntryTx(ctx, u.tx, reversal); err != nil {
		return ledger.JournalEntry{}, err
	}
	original.ReversedEntryID = &reversal.ID
	original.Status = posting.EntryReversed
	original.UpdatedAt = time.Now().UTC()
	if err := u.driver.saveEntryTx(ctx, u.tx, original); err != nil {
		return ledger.JournalEntry{}, err
	}
	return portbridge.ToPublicEntry(reversal)
}

func (u *postgresUnitOfWork) GetAccountBalance(ctx context.Context, companyID types.CompanyID, accountID types.AccountID, asOf time.Time, currency string) (decimal.Decimal, error) {
	var balance decimal.Decimal
	err := u.tx.QueryRow(ctx, `SELECT COALESCE(SUM(balance.ending_debit-balance.ending_credit),0)
		FROM account_balances balance
		JOIN fiscal_periods period ON period.company_id=balance.company_id AND period.id=balance.fiscal_period_id
		WHERE balance.company_id=$1 AND balance.account_id=$2 AND balance.currency_id=$3 AND period.end_date <= $4`,
		uuid.UUID(companyID), uuid.UUID(accountID), currency, asOf).Scan(&balance)
	return balance, err
}

func (u *postgresUnitOfWork) AppendAudit(ctx context.Context, record storage.AuditRecord) error {
	entityID, err := uuid.Parse(record.EntityID)
	if err != nil {
		return fmt.Errorf("postgres: invalid audit entity id: %w", err)
	}
	occurredAt := record.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	payload := append(json.RawMessage(nil), record.Payload...)
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	_, err = appendAuditLogTx(ctx, u.tx, governance.FinancialAuditLog{
		CompanyID: uuid.UUID(record.CompanyID), EntityType: record.EntityType, EntityID: entityID,
		Action: record.Action, ActorID: auditActorID(record.ActorID), PayloadAfter: payload, Timestamp: occurredAt,
	})
	return translateTransactionError(err)
}

func (u *postgresUnitOfWork) Claim(ctx context.Context, record storage.IdempotencyRecord) (storage.IdempotencyRecord, bool, error) {
	if record.Status == "" {
		record.Status = storage.IdempotencyStarted
	}
	if record.Key == "" || record.Operation == "" || record.PayloadHash == "" {
		return storage.IdempotencyRecord{}, false, errors.New("postgres: idempotency key, operation, and payload hash are required")
	}
	var existing storage.IdempotencyRecord
	err := u.tx.QueryRow(ctx, `INSERT INTO posting_idempotency(company_id,idempotency_key,operation,payload_hash,status,entry_id,response,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,COALESCE($8,now()),COALESCE($9,now()))
		ON CONFLICT(company_id,idempotency_key) DO NOTHING
		RETURNING company_id,idempotency_key,operation,payload_hash,status,entry_id,response,created_at,updated_at`,
		uuid.UUID(record.CompanyID), record.Key, record.Operation, record.PayloadHash, record.Status,
		entryIDValue(record.EntryID), record.Response, nullableTime(record.CreatedAt), nullableTime(record.UpdatedAt)).
		Scan(&existing.CompanyID, &existing.Key, &existing.Operation, &existing.PayloadHash, &existing.Status,
			&existing.EntryID, &existing.Response, &existing.CreatedAt, &existing.UpdatedAt)
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return storage.IdempotencyRecord{}, false, translateTransactionError(err)
	}
	err = u.tx.QueryRow(ctx, `SELECT company_id,idempotency_key,operation,payload_hash,status,entry_id,response,created_at,updated_at
		FROM posting_idempotency WHERE company_id=$1 AND idempotency_key=$2 FOR UPDATE`,
		uuid.UUID(record.CompanyID), record.Key).
		Scan(&existing.CompanyID, &existing.Key, &existing.Operation, &existing.PayloadHash, &existing.Status,
			&existing.EntryID, &existing.Response, &existing.CreatedAt, &existing.UpdatedAt)
	if err != nil {
		return storage.IdempotencyRecord{}, false, translateTransactionError(err)
	}
	if existing.PayloadHash != record.PayloadHash || existing.Operation != record.Operation {
		return existing, false, storage.ErrUniqueConstraint
	}
	return existing, false, nil
}

func (u *postgresUnitOfWork) Complete(ctx context.Context, companyID types.CompanyID, key, payloadHash string, entryID types.JournalEntryID, response []byte) error {
	tag, err := u.tx.Exec(ctx, `UPDATE posting_idempotency SET status=$5,entry_id=$4,response=$6,updated_at=now()
		WHERE company_id=$1 AND idempotency_key=$2 AND payload_hash=$3 AND status IN ($7,$8)`,
		uuid.UUID(companyID), key, payloadHash, uuid.UUID(entryID), storage.IdempotencyCompleted, response,
		storage.IdempotencyStarted, storage.IdempotencyRetryable)
	if err != nil {
		return translateTransactionError(err)
	}
	if tag.RowsAffected() != 1 {
		return storage.ErrUniqueConstraint
	}
	return nil
}

func (u *postgresUnitOfWork) Fail(ctx context.Context, companyID types.CompanyID, key, payloadHash, status string) error {
	switch status {
	case storage.IdempotencyRetryable, storage.IdempotencyTerminal:
	default:
		return fmt.Errorf("postgres: invalid idempotency failure status %q", status)
	}
	tag, err := u.tx.Exec(ctx, `UPDATE posting_idempotency SET status=$4,updated_at=now()
		WHERE company_id=$1 AND idempotency_key=$2 AND payload_hash=$3 AND status=$5`,
		uuid.UUID(companyID), key, payloadHash, status, storage.IdempotencyStarted)
	if err != nil {
		return translateTransactionError(err)
	}
	if tag.RowsAffected() != 1 {
		return storage.ErrUniqueConstraint
	}
	return nil
}

func entryIDValue(entryID *types.JournalEntryID) any {
	if entryID == nil {
		return nil
	}
	return uuid.UUID(*entryID)
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func (d *Driver) Capabilities() storage.AdapterCapabilities {
	return storage.AdapterCapabilities{
		AdapterName: "postgresql", SupportsRepeatableRead: true, SupportsSerializable: true,
		EnforcesForeignKeys: true, GuaranteesDurability: true, SupportsSavepoints: true, DetectsDeadlocks: true,
	}
}
