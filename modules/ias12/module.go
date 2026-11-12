package ias12

import (
	"context"
	"errors"

	"accounting_engine/pkg/spi/standards"
	"accounting_engine/pkg/spi/storage"
)

type Module struct{}

var _ standards.StandardModule = (*Module)(nil)

func (m *Module) Name() string {
	return "IAS 12"
}

func (m *Module) ValidateDependencies(ports storage.CoreLedgerStore) error {
	if ports == nil {
		return errors.New("core ledger store cannot be nil for IAS 12")
	}
	return nil
}

func (m *Module) RegisterRules(router interface{}) error {
	return nil
}

func (m *Module) RunScheduledJobs(ctx context.Context, runner interface{}) error {
	return nil
}
