package postgres_test

import (
	"context"
	"errors"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresPhase0LedgerIntegrity(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, getTestPostgresConn())
	if err != nil {
		t.Skipf("Skipping PostgreSQL integrity tests: unable to connect (%v)", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("Skipping PostgreSQL integrity tests: unable to connect (%v)", err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	schemaName := "phase0_" + uuid.NewString()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schemaName}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("drop temporary schema: %v", err)
		}
	}()

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+pgx.Identifier{schemaName}.Sanitize()+", public"); err != nil {
		t.Fatal(err)
	}
	schemaSQL, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Conn().PgConn().Exec(ctx, string(schemaSQL)).ReadAll(); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	migrationSQL, err := os.ReadFile("migrations/202611100001_phase0_integrity.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Conn().PgConn().Exec(ctx, string(migrationSQL)).ReadAll(); err != nil {
		t.Fatalf("apply phase-0 migration: %v", err)
	}

	companyID, journalID := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx,
		`INSERT INTO companies(id,code,legal_name,functional_currency,presentation_currency)
		 VALUES($1,$2,'Phase 0 test','SAR','SAR')`,
		companyID, "P0-"+companyID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO journals(id,company_id,code) VALUES($1,$2,'GEN')`,
		journalID, companyID); err != nil {
		t.Fatal(err)
	}

	debitAccountID, creditAccountID := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx,
		`INSERT INTO accounts(id,company_id,code,name,account_type,normal_balance)
		 VALUES($1,$3,'1000','Debit','asset','debit'),($2,$3,'2000','Credit','liability','credit')`,
		debitAccountID, creditAccountID, companyID); err != nil {
		t.Fatal(err)
	}

	createMove := func(id uuid.UUID, status string, entryStatus string) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO account_moves(id,company_id,journal_id,entry_date,status,entry)
			 VALUES($1,$2,$3,$4,$5,jsonb_build_object('Status',$6::text))`,
			id, companyID, journalID, time.Now().UTC(), status, entryStatus)
		return err
	}
	addLine := func(moveID uuid.UUID, lineID uuid.UUID, debit, credit, functionalDebit, functionalCredit int64) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO account_move_lines
			 (move_id,company_id,line_id,account_id,debit,credit,functional_debit_minor,functional_credit_minor,line_data)
			 VALUES($1,$2,$3,$4,$5,$6,$7,$8,'{}'::jsonb)`,
			moveID, companyID, lineID, debitAccountID, debit, credit, functionalDebit, functionalCredit)
		return err
	}

	postedMoveID, draftMoveID := uuid.New(), uuid.New()
	if err := createMove(postedMoveID, "draft", "draft"); err != nil {
		t.Fatal(err)
	}
	if err := createMove(draftMoveID, "draft", "draft"); err != nil {
		t.Fatal(err)
	}
	postedLineID := uuid.New()
	if err := addLine(postedMoveID, postedLineID, 100, 0, 10000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO account_move_lines
		 (move_id,company_id,line_id,account_id,debit,credit,functional_debit_minor,functional_credit_minor,line_data)
		 VALUES($1,$2,$3,$4,0,100,0,10000,'{}'::jsonb)`,
		postedMoveID, companyID, uuid.New(), creditAccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE account_moves SET entry=jsonb_build_object('draft',true) WHERE id=$1`, draftMoveID); err != nil {
		t.Fatalf("draft header update failed: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE account_moves SET status='posted',entry=jsonb_build_object('Status','posted') WHERE id=$1`,
		postedMoveID); err != nil {
		t.Fatal(err)
	}

	assertRejected := func(name string, query string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, "SAVEPOINT "+name); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, query, args...); err == nil {
			t.Errorf("%s unexpectedly succeeded", name)
		}
		if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name); err != nil {
			t.Fatalf("%s rollback: %v", name, err)
		}
		if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT "+name); err != nil {
			t.Fatal(err)
		}
	}

	assertRejected("direct_posted_move_insert",
		`INSERT INTO account_moves(id,company_id,journal_id,entry_date,status,entry)
		 VALUES($1,$2,$3,NOW(),'posted','{"Status":"posted"}'::jsonb)`,
		uuid.New(), companyID, journalID)
	statusMismatchMoveID := uuid.New()
	if err := createMove(statusMismatchMoveID, "draft", "draft"); err != nil {
		t.Fatal(err)
	}
	if err := addLine(statusMismatchMoveID, uuid.New(), 100, 0, 10000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO account_move_lines
		 (move_id,company_id,line_id,account_id,debit,credit,functional_debit_minor,functional_credit_minor,line_data)
		 VALUES($1,$2,$3,$4,0,100,0,10000,'{}'::jsonb)`,
		statusMismatchMoveID, companyID, uuid.New(), creditAccountID); err != nil {
		t.Fatal(err)
	}
	assertRejected("posted_json_status_mismatch",
		`UPDATE account_moves SET status='posted' WHERE id=$1`, statusMismatchMoveID)
	assertRejected("posted_json_mutation",
		`UPDATE account_moves SET entry='{"tampered":true}'::jsonb WHERE id=$1`, postedMoveID)
	assertRejected("posted_line_relink",
		`UPDATE account_move_lines SET move_id=$1 WHERE move_id=$2 AND line_id=$3`,
		draftMoveID, postedMoveID, postedLineID)
	assertRejected("posted_move_delete", `DELETE FROM account_moves WHERE id=$1`, postedMoveID)
	assertRejected("posted_line_delete", `DELETE FROM account_move_lines WHERE move_id=$1 AND line_id=$2`,
		postedMoveID, postedLineID)

	reversalMoveID := uuid.New()
	if err := createMove(reversalMoveID, "draft", "draft"); err != nil {
		t.Fatal(err)
	}
	assertRejected("reversal_without_posted_entry",
		`UPDATE account_moves
		 SET status='reversed', reversed_move_id=$2,
		     entry=jsonb_build_object('Status','reversed','ReversedEntryID',$3::text)
		 WHERE id=$1`,
		postedMoveID, reversalMoveID, reversalMoveID.String())
	if _, err := tx.Exec(ctx,
		`INSERT INTO account_move_lines
		 (move_id,company_id,line_id,account_id,debit,credit,functional_debit_minor,functional_credit_minor,line_data)
		 VALUES($1,$2,$3,$4,100,0,10000,0,'{}'::jsonb),
		       ($1,$2,$5,$6,0,100,0,10000,'{}'::jsonb)`,
		reversalMoveID, companyID, uuid.New(), creditAccountID, uuid.New(), debitAccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE account_moves
		 SET status='posted',entry=jsonb_build_object('Status','posted','OriginalEntryID',$2::text)
		 WHERE id=$1`,
		reversalMoveID, postedMoveID.String()); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx,
		`UPDATE account_moves
		 SET status='reversed', reversed_move_id=$2, updated_at=NOW(),
		     entry=jsonb_build_object('Status','reversed','ReversedEntryID',$3::text,'UpdatedAt',NOW()::text)
		 WHERE id=$1`,
		postedMoveID, reversalMoveID, reversalMoveID.String())
	if err != nil {
		t.Fatalf("valid posted-to-reversed transition was rejected: %v", err)
	}

	unbalancedMoveID := uuid.New()
	if err := createMove(unbalancedMoveID, "draft", "draft"); err != nil {
		t.Fatal(err)
	}
	if err := addLine(unbalancedMoveID, uuid.New(), 100, 0, 10000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO account_move_lines
		 (move_id,company_id,line_id,account_id,debit,credit,functional_debit_minor,functional_credit_minor,line_data)
		 VALUES($1,$2,$3,$4,0,50,0,5000,'{}'::jsonb)`,
		unbalancedMoveID, companyID, uuid.New(), creditAccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SAVEPOINT unbalanced_post_attempt"); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx,
		`UPDATE account_moves SET status='posted',entry=jsonb_build_object('Status','posted') WHERE id=$1`,
		unbalancedMoveID)
	if err == nil {
		t.Fatal("unbalanced posting unexpectedly passed immediate statement validation")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("unbalanced posting error = %v, want immediate check_violation", err)
	}
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT unbalanced_post_attempt"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT unbalanced_post_attempt"); err != nil {
		t.Fatal(err)
	}

	if _, err := tx.Exec(ctx, "DROP TRIGGER trg_check_posted_move_balance_statement ON account_moves"); err != nil {
		t.Fatal(err)
	}
	runPostingBatch := func(tag string) time.Duration {
		t.Helper()
		start := time.Now()
		if _, err := tx.Exec(ctx,
			`INSERT INTO account_moves(id,company_id,journal_id,entry_date,status,ref,entry)
			 SELECT gen_random_uuid(),$1,$2,NOW(),'draft',$3,jsonb_build_object('Status','draft')
			 FROM generate_series(1,3000)`,
			companyID, journalID, tag); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO account_move_lines
			 (move_id,company_id,line_id,account_id,debit,credit,functional_debit_minor,functional_credit_minor,line_data)
			 SELECT id,$1,gen_random_uuid(),$3,100,0,10000,0,'{}'::jsonb
			 FROM account_moves WHERE company_id=$1 AND ref=$2`,
			companyID, tag, debitAccountID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO account_move_lines
			 (move_id,company_id,line_id,account_id,debit,credit,functional_debit_minor,functional_credit_minor,line_data)
			 SELECT id,$1,gen_random_uuid(),$3,0,100,0,10000,'{}'::jsonb
			 FROM account_moves WHERE company_id=$1 AND ref=$2`,
			companyID, tag, creditAccountID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE account_moves SET status='posted',entry=jsonb_build_object('Status','posted')
			 WHERE company_id=$1 AND ref=$2`,
			companyID, tag); err != nil {
			t.Fatal(err)
		}
		return time.Since(start)
	}
	overheads := make([]float64, 0, 5)
	var baselineTotal, guardedTotal time.Duration
	for range 5 {
		baselineTag := "P0-BASELINE-" + uuid.NewString()
		guardedTag := "P0-GUARDED-" + uuid.NewString()
		baselineDuration := runPostingBatch(baselineTag)
		if _, err := tx.Exec(ctx,
			`CREATE TRIGGER trg_check_posted_move_balance_statement
			 AFTER UPDATE ON account_moves
			 REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows
			 FOR EACH STATEMENT
			 EXECUTE FUNCTION check_posted_move_balance_statement()`); err != nil {
			t.Fatal(err)
		}
		guardedDuration := runPostingBatch(guardedTag)
		baselineTotal += baselineDuration
		guardedTotal += guardedDuration
		overheads = append(overheads,
			(float64(guardedDuration)-float64(baselineDuration))/float64(baselineDuration)*100)
		if _, err := tx.Exec(ctx, "DROP TRIGGER trg_check_posted_move_balance_statement ON account_moves"); err != nil {
			t.Fatal(err)
		}
	}
	sort.Float64s(overheads)
	aggregateOverhead := (float64(guardedTotal) - float64(baselineTotal)) /
		float64(baselineTotal) * 100
	t.Logf("End-to-end immediate batched balance-check overhead for 3000 moves: paired samples=%v, aggregate=%.2f%%",
		overheads, aggregateOverhead)
}
