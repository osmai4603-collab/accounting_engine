package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/internal/observability"
)

func TestTraceparentValidation(t *testing.T) {
	valid := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if err := postgres.ValidateTraceparent(valid); err != nil {
		t.Fatalf("expected valid traceparent, got: %v", err)
	}

	validUnsampled := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"
	if err := postgres.ValidateTraceparent(validUnsampled); err != nil {
		t.Fatalf("expected valid unsampled traceparent, got: %v", err)
	}

	invalidCases := []struct {
		name string
		tp   string
	}{
		{"empty", ""},
		{"too short", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7"},
		{"invalid version", "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
		{"all zero trace id", "00-00000000000000000000000000000000-00f067aa0ba902b7-01"},
		{"all zero span id", "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01"},
		{"non-hex characters", "00-4bf92f3577b34da6a3ce929d0e0e47zz-00f067aa0ba902b7-01"},
		{"invalid delimiter", "00_4bf92f3577b34da6a3ce929d0e0e4736_00f067aa0ba902b7_01"},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := postgres.ValidateTraceparent(tc.tp); err == nil {
				t.Fatalf("expected error for %q, but got nil", tc.tp)
			}
		})
	}
}

func TestGenerateTraceparent(t *testing.T) {
	tpSampled, err := postgres.GenerateTraceparent(true)
	if err != nil {
		t.Fatalf("generate sampled traceparent: %v", err)
	}
	if err := postgres.ValidateTraceparent(tpSampled); err != nil {
		t.Fatalf("generated sampled traceparent is invalid: %v", err)
	}
	if tpSampled[len(tpSampled)-2:] != "01" {
		t.Fatalf("expected flags 01, got %s", tpSampled)
	}

	tpUnsampled, err := postgres.GenerateTraceparent(false)
	if err != nil {
		t.Fatalf("generate unsampled traceparent: %v", err)
	}
	if err := postgres.ValidateTraceparent(tpUnsampled); err != nil {
		t.Fatalf("generated unsampled traceparent is invalid: %v", err)
	}
	if tpUnsampled[len(tpUnsampled)-2:] != "00" {
		t.Fatalf("expected flags 00, got %s", tpUnsampled)
	}
}

func TestAnnotateQueryWithTraceparent(t *testing.T) {
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	tests := []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:     "query with semicolon",
			query:    "SELECT * FROM accounts;",
			expected: "SELECT * FROM accounts /*traceparent='00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01'*/;",
		},
		{
			name:     "query without semicolon",
			query:    "SELECT count(*) FROM journals",
			expected: "SELECT count(*) FROM journals /*traceparent='00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01'*/",
		},
		{
			name:     "empty traceparent leaves query unchanged",
			query:    "SELECT 1;",
			expected: "SELECT 1;",
		},
		{
			name:     "invalid traceparent cannot inject a comment",
			query:    "SELECT 1;",
			expected: "SELECT 1;",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputTp := tp
			if tt.name == "empty traceparent leaves query unchanged" ||
				tt.name == "invalid traceparent cannot inject a comment" {
				inputTp = ""
			}
			if tt.name == "invalid traceparent cannot inject a comment" {
				inputTp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01*/ DROP TABLE account_moves; --"
			}
			result := postgres.AnnotateQueryWithTraceparent(tt.query, inputTp)
			if result != tt.expected {
				t.Fatalf("expected:\n%s\ngot:\n%s", tt.expected, result)
			}
		})
	}
}

func TestPostgresSessionTraceparentPropagation(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}

	companyID := uuid.New()
	tp, err := postgres.GenerateTraceparent(true)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Context WITH traceparent
	tracedCtx := postgres.ContextWithTraceparent(ctx, tp)
	if extracted := postgres.TraceparentFromContext(tracedCtx); extracted != tp {
		t.Fatalf("extracted traceparent %q != expected %q", extracted, tp)
	}

	err = driver.WithCompanyTx(tracedCtx, companyID, func(tx pgx.Tx) error {
		sessionTp, err := driver.CurrentSessionTraceparent(tracedCtx, tx)
		if err != nil {
			return err
		}
		if sessionTp != tp {
			t.Fatalf("session traceparent %q != expected %q", sessionTp, tp)
		}

		// Verify annotated SQL query execution
		annotatedQuery := driver.AnnotateQuery(tracedCtx, "SELECT 1;")
		var one int
		if err := tx.QueryRow(tracedCtx, annotatedQuery).Scan(&one); err != nil {
			t.Fatalf("execute annotated sqlcommenter query: %v", err)
		}
		if one != 1 {
			t.Fatalf("expected 1, got %d", one)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Context WITHOUT traceparent
	err = driver.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		sessionTp, err := driver.CurrentSessionTraceparent(ctx, tx)
		if err != nil {
			return err
		}
		if sessionTp != "" {
			t.Fatalf("expected empty session traceparent, got: %q", sessionTp)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestTraceparentMatchesApplicationLogAndDatabaseQuery proves the Phase 6
// acceptance criterion: the trace-id emitted by the structured application log,
// the PostgreSQL session setting, and the SQL query comment are the same W3C
// trace-id.
func TestTraceparentMatchesApplicationLogAndDatabaseQuery(t *testing.T) {
	ctx := context.Background()
	driver, err := newIsolatedPostgresDriver(t, ctx)
	if err != nil {
		t.Skipf("isolated PostgreSQL schema unavailable: %v", err)
	}

	companyID, entryID := uuid.New(), uuid.New()
	tp, err := postgres.GenerateTraceparent(true)
	if err != nil {
		t.Fatal(err)
	}
	tracedCtx := observability.WithFinancialScope(ctx, companyID, entryID, tp)

	var logBuffer bytes.Buffer
	logger := observability.NewLogger(&logBuffer, nil)
	logger.InfoContext(tracedCtx, "financial query executed")

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logBuffer.Bytes()), &record); err != nil {
		t.Fatalf("decode application log: %v", err)
	}
	appTraceID, _ := record["trace_id"].(string)
	if appTraceID == "" {
		t.Fatal("application log did not include trace_id")
	}
	if record["company_id"] != companyID.String() || record["entry_id"] != entryID.String() {
		t.Fatalf("application log correlation ids = %v / %v", record["company_id"], record["entry_id"])
	}

	err = driver.WithCompanyTx(tracedCtx, companyID, func(tx pgx.Tx) error {
		sessionTraceparent, err := driver.CurrentSessionTraceparent(tracedCtx, tx)
		if err != nil {
			return err
		}
		if sessionTraceparent != tp {
			t.Fatalf("database session traceparent = %q, want %q", sessionTraceparent, tp)
		}
		if observability.TraceIDFromTraceparent(sessionTraceparent) != appTraceID {
			t.Fatalf("database trace id = %q, application trace id = %q",
				observability.TraceIDFromTraceparent(sessionTraceparent), appTraceID)
		}

		// The traced transaction rewrites the statement with a sqlcommenter
		// annotation. current_query() returns exactly what PostgreSQL received,
		// including the comment.
		var currentQuery string
		if err := tx.QueryRow(tracedCtx, "SELECT current_query()").Scan(&currentQuery); err != nil {
			return err
		}
		if !strings.Contains(currentQuery, "/*traceparent='"+tp+"'*/") {
			t.Fatalf("PostgreSQL did not receive the traceparent query comment: %q", currentQuery)
		}
		if !strings.Contains(currentQuery, appTraceID) {
			t.Fatalf("PostgreSQL query comment does not carry the application trace id: %q", currentQuery)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
