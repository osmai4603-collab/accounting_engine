package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/shopspring/decimal"
)

type stubDrafts struct{}

func (stubDrafts) SaveDraft(context.Context, ledger.JournalEntry) error { return nil }

type stubEntries struct{}

func (stubEntries) GetEntry(context.Context, types.CompanyID, types.JournalEntryID) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

type stubPosting struct{}

func (stubPosting) CommitPosting(context.Context, PostingCommit) (ledger.JournalEntry, error) {
	return ledger.JournalEntry{}, nil
}

type stubSequences struct{}

func (stubSequences) NextSequence(context.Context, types.CompanyID, types.JournalID) (int64, error) {
	return 0, nil
}

type stubMasterData struct{}

func (stubMasterData) GetCompany(context.Context, types.CompanyID) (masterdata.Company, error) {
	return masterdata.Company{}, nil
}
func (stubMasterData) GetAccount(context.Context, types.CompanyID, types.AccountID) (masterdata.Account, error) {
	return masterdata.Account{}, nil
}
func (stubMasterData) GetAccountByCode(context.Context, types.CompanyID, string) (masterdata.Account, error) {
	return masterdata.Account{}, nil
}
func (stubMasterData) GetJournalByCode(context.Context, types.CompanyID, string) (masterdata.Journal, error) {
	return masterdata.Journal{}, nil
}
func (stubMasterData) HasAccountChildren(context.Context, types.CompanyID, types.AccountID) (bool, error) {
	return false, nil
}
func (stubMasterData) GetFiscalYear(context.Context, types.CompanyID, types.FiscalYearID) (masterdata.FiscalYear, error) {
	return masterdata.FiscalYear{}, nil
}
func (stubMasterData) GetPeriodForDate(context.Context, types.CompanyID, time.Time) (masterdata.FiscalPeriod, error) {
	return masterdata.FiscalPeriod{}, nil
}

type stubBalance struct{}

func (stubBalance) GetAccountBalance(context.Context, types.CompanyID, types.AccountID, time.Time, string) (decimal.Decimal, error) {
	return decimal.Zero, nil
}

func TestPortsValidateReportsEveryRequiredPort(t *testing.T) {
	err := (Ports{}).Validate()
	if err == nil {
		t.Fatal("expected validation error for empty ports")
	}
	for _, capability := range []string{"draft store", "entry reader", "posting writer", "sequence allocator", "master data reader"} {
		if !strings.Contains(err.Error(), capability) {
			t.Errorf("validation error does not mention %q: %v", capability, err)
		}
	}
}

func TestPortsValidateAcceptsRequiredPortsOnly(t *testing.T) {
	ports := Ports{
		Drafts:     stubDrafts{},
		Entries:    stubEntries{},
		Posting:    stubPosting{},
		Sequences:  stubSequences{},
		MasterData: stubMasterData{},
	}
	if err := ports.Validate(); err != nil {
		t.Fatalf("required ports rejected: %v", err)
	}
	if (Ports{}).Validate() == nil {
		t.Fatal("empty ports accepted")
	}
}

func TestOptionalCapabilitiesAreNilByDefault(t *testing.T) {
	ports := Ports{}
	if ports.Balance != nil || ports.Audit != nil || ports.Idempotency != nil || ports.Private != nil {
		t.Fatal("optional capabilities must default to nil, not silent stubs")
	}
}
