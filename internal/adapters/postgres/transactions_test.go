package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

func TestPGXTransactionOptions(t *testing.T) {
	tests := []struct {
		name       string
		options    storage.TxOptions
		isolation  pgx.TxIsoLevel
		accessMode pgx.TxAccessMode
		wantErr    bool
	}{
		{name: "defaults", isolation: pgx.ReadCommitted, accessMode: pgx.ReadWrite},
		{name: "read only repeatable read", options: storage.TxOptions{Isolation: storage.IsolationRepeatableRead, ReadOnly: true}, isolation: pgx.RepeatableRead, accessMode: pgx.ReadOnly},
		{name: "serializable", options: storage.TxOptions{Isolation: storage.IsolationSerializable}, isolation: pgx.Serializable, accessMode: pgx.ReadWrite},
		{name: "rejects unknown isolation", options: storage.TxOptions{Isolation: "snapshot"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := pgxTxOptions(test.options)
			if (err != nil) != test.wantErr {
				t.Fatalf("pgxTxOptions() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && (got.IsoLevel != test.isolation || got.AccessMode != test.accessMode) {
				t.Fatalf("pgxTxOptions() = %+v, want isolation %q access %q", got, test.isolation, test.accessMode)
			}
		})
	}
}

func TestTranslateTransactionError(t *testing.T) {
	tests := []struct {
		code string
		want error
	}{
		{code: "40001", want: storage.ErrSerializationFailure},
		{code: "40P01", want: storage.ErrDeadlockDetected},
		{code: "23505", want: storage.ErrUniqueConstraint},
		{code: "23503", want: nil},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			err := translateTransactionError(&pgconn.PgError{Code: test.code, Message: "database error"})
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("translated error = %v, want errors.Is(_, %v)", err, test.want)
			}
			if test.want == nil && err == nil {
				t.Fatal("expected unmapped database error to be preserved")
			}
		})
	}
}

func TestPostgresCapabilitiesAndTransactionPorts(t *testing.T) {
	driver := NewWithPool(nil)
	capabilities := driver.Capabilities()
	if capabilities.AdapterName != "postgresql" || !capabilities.GuaranteesDurability ||
		!capabilities.EnforcesForeignKeys || !capabilities.SupportsSerializable {
		t.Fatalf("unexpected PostgreSQL capabilities: %+v", capabilities)
	}
	ports := driver.Ports()
	if ports.TxRunner != driver || ports.UOWRunner != driver {
		t.Fatal("PostgreSQL SPI ports do not expose their transaction runners")
	}
}
