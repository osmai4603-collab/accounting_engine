package postgres_test

import (
	"context"
	"testing"

	"accounting_engine/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputTp := tp
			if tt.name == "empty traceparent leaves query unchanged" {
				inputTp = ""
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
