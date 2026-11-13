package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// tracedTx wraps a pgx transaction and annotates every SQL statement with the
// active W3C traceparent using the sqlcommenter convention. PostgreSQL records
// the comment in pg_stat_activity and its logs, which lets operators join
// application logs to database activity by trace-id.
type tracedTx struct {
	pgx.Tx
	traceparent string
}

func newTracedTx(tx pgx.Tx, traceparent string) pgx.Tx {
	if tx == nil || ValidateTraceparent(traceparent) != nil {
		return tx
	}
	return &tracedTx{Tx: tx, traceparent: traceparent}
}

func (t *tracedTx) annotate(query string) string {
	return AnnotateQueryWithTraceparent(query, t.traceparent)
}

func (t *tracedTx) Begin(ctx context.Context) (pgx.Tx, error) {
	nested, err := t.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return newTracedTx(nested, t.traceparent), nil
}

func (t *tracedTx) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return t.Tx.Exec(ctx, t.annotate(sql), arguments...)
}

func (t *tracedTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.Tx.Query(ctx, t.annotate(sql), args...)
}

func (t *tracedTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.Tx.QueryRow(ctx, t.annotate(sql), args...)
}

func (t *tracedTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return t.Tx.Prepare(ctx, name, t.annotate(sql))
}

// SendBatch annotates queued statements in place so any per-query callbacks
// registered by the caller are preserved.
func (t *tracedTx) SendBatch(ctx context.Context, batch *pgx.Batch) pgx.BatchResults {
	if batch != nil {
		for _, query := range batch.QueuedQueries {
			if query != nil {
				query.SQL = t.annotate(query.SQL)
			}
		}
	}
	return t.Tx.SendBatch(ctx, batch)
}
