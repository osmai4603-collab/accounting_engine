package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

func TestPostgresUnitOfWorkRollsBackAndScopesTransaction(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}

	companyID, journalID := uuid.New(), uuid.New()
	idempotencyRecord := storage.IdempotencyRecord{
		CompanyID: types.CompanyID(companyID), Key: "posting-key", Operation: "post", PayloadHash: "payload-hash",
	}
	if err := driver.CreateCompany(ctx, masterdata.Company{
		ID: companyID, Code: fmt.Sprintf("UOW-%s", companyID.String()[:8]),
		LegalName: "Unit of Work Test", FunctionalCurrency: "USD",
		PresentationCurrency: "USD", Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO journals(id,company_id,code) VALUES($1,$2,$3)`,
			journalID, companyID, "UOW-"+journalID.String()[:8])
		return err
	}); err != nil {
		t.Fatal(err)
	}

	actorID := uuid.NewString()
	txCtx := storage.WithActorContext(
		storage.WithCompanyContext(ctx, types.CompanyID(companyID)),
		actorID,
	)
	callbackErr := errors.New("force rollback")
	err = driver.Ports().UOWRunner.ExecuteWithOptions(txCtx, storage.TxOptions{
		Isolation: storage.IsolationSerializable,
	}, func(ctx context.Context, uow storage.UnitOfWork) error {
		if _, err := uow.Sequences().NextSequence(ctx, types.CompanyID(companyID), types.JournalID(journalID)); err != nil {
			return err
		}
		if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
			var activeCompany, activeActor string
			if err := tx.QueryRow(ctx, `SELECT current_setting('app.current_company_id'), current_setting('app.current_actor_id')`).
				Scan(&activeCompany, &activeActor); err != nil {
				return err
			}
			if activeCompany != companyID.String() || activeActor != actorID {
				return fmt.Errorf("transaction scope = company %q actor %q", activeCompany, activeActor)
			}
			return nil
		}); err != nil {
			return err
		}
		if _, claimed, err := uow.Idempotency().Claim(ctx, idempotencyRecord); err != nil {
			return err
		} else if !claimed {
			return errors.New("expected idempotency key to be claimed inside unit of work")
		}
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("unit of work error = %v, want callback error", err)
	}

	var sequence int64
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT current_sequence FROM journals WHERE company_id=$1 AND id=$2`,
			companyID, journalID).Scan(&sequence)
	}); err != nil {
		t.Fatal(err)
	}
	if sequence != 0 {
		t.Fatalf("sequence after rolled back unit of work = %d, want 0", sequence)
	}

	var idempotencyCount int
	if err := driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*) FROM posting_idempotency
			WHERE company_id=$1 AND idempotency_key=$2`, companyID, idempotencyRecord.Key).Scan(&idempotencyCount)
	}); err != nil {
		t.Fatal(err)
	}
	if idempotencyCount != 0 {
		t.Fatalf("idempotency rows after rolled back unit of work = %d, want 0", idempotencyCount)
	}
	if err := driver.Ports().UOWRunner.Execute(txCtx, func(ctx context.Context, uow storage.UnitOfWork) error {
		record, claimed, err := uow.Idempotency().Claim(ctx, idempotencyRecord)
		if err != nil {
			return err
		}
		if !claimed || record.Status != storage.IdempotencyStarted {
			return fmt.Errorf("new idempotency claim = claimed %v status %q", claimed, record.Status)
		}
		return uow.Idempotency().Fail(ctx, idempotencyRecord.CompanyID, idempotencyRecord.Key,
			idempotencyRecord.PayloadHash, storage.IdempotencyRetryable)
	}); err != nil {
		t.Fatal(err)
	}
	if err := driver.Ports().UOWRunner.Execute(txCtx, func(ctx context.Context, uow storage.UnitOfWork) error {
		record, claimed, err := uow.Idempotency().Claim(ctx, idempotencyRecord)
		if err != nil {
			return err
		}
		if claimed || record.Status != storage.IdempotencyRetryable {
			return fmt.Errorf("existing idempotency claim = claimed %v status %q", claimed, record.Status)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUnitOfWorkDoesNotLeakCompanyOrActorContextThroughPool(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriverWithConns(t, ctx, 2)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}
	reservedConn, err := driver.Pool().Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reservedConn.Release()

	for _, scope := range []struct {
		companyID types.CompanyID
		actorID   string
	}{
		{companyID: types.CompanyID(uuid.New()), actorID: uuid.NewString()},
		{companyID: types.CompanyID(uuid.New()), actorID: uuid.NewString()},
	} {
		txCtx := storage.WithActorContext(storage.WithCompanyContext(ctx, scope.companyID), scope.actorID)
		if err := driver.Ports().UOWRunner.Execute(txCtx, func(txCtx context.Context, _ storage.UnitOfWork) error {
			return driver.WithCompanyTx(txCtx, uuid.UUID(scope.companyID), func(tx pgx.Tx) error {
				var companyID, actorID string
				if err := tx.QueryRow(txCtx, `SELECT current_setting('app.current_company_id'),
					current_setting('app.current_actor_id')`).Scan(&companyID, &actorID); err != nil {
					return err
				}
				if companyID != uuid.UUID(scope.companyID).String() || actorID != scope.actorID {
					return fmt.Errorf("transaction scope = company %q actor %q, want company %q actor %q",
						companyID, actorID, scope.companyID, scope.actorID)
				}
				return nil
			})
		}); err != nil {
			t.Fatal(err)
		}
	}

	var companyID, actorID string
	if err := driver.Pool().QueryRow(ctx, `SELECT
		COALESCE(current_setting('app.current_company_id', true), ''),
		COALESCE(current_setting('app.current_actor_id', true), '')`).Scan(&companyID, &actorID); err != nil {
		t.Fatal(err)
	}
	if companyID != "" || actorID != "" {
		t.Fatalf("pooled connection retained tenant scope: company %q actor %q", companyID, actorID)
	}
}
