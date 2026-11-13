//go:build gapred

// Red tests for GAP-AST-01 and GAP-AST-03. Expected to pass only after P1-T9
// binds the depreciation attachment to the posted entry's amount, date, and
// schedule.
package subledgers_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

func gapAssetMoney(t *testing.T, amount string) financialtypes.Money {
	t.Helper()
	money, err := financialtypes.NewMoneyFromString(amount, "USD")
	if err != nil {
		t.Fatal(err)
	}
	return money
}

func TestGAP_AST_01_DepreciationAttachmentValidatesEntryContent(t *testing.T) {
	companyID := uuid.New()
	amount := gapAssetMoney(t, "100.00")
	asOf := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

	entry, err := posting.NewJournalEntry(uuid.New(), asOf, "maker", []posting.JournalEntryLine{
		{CompanyID: companyID, ID: uuid.New(), AccountID: uuid.New(), Debit: gapAssetMoney(t, "1.00"), Credit: gapAssetMoney(t, "0.00")},
		{CompanyID: companyID, ID: uuid.New(), AccountID: uuid.New(), Debit: gapAssetMoney(t, "0.00"), Credit: gapAssetMoney(t, "1.00")},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.CompanyID = companyID

	attachment := &subledgers.DepreciationAttachment{
		AssetID: uuid.New(), CompanyID: companyID,
		Amount: amount, AsOf: asOf,
	}
	if err := attachment.ValidateAttachment(context.Background(), entry); err == nil {
		t.Fatal("GAP-AST-01: ValidateAttachment accepted an entry whose amount/date/schedule do not match the asset depreciation")
	}
}

func TestGAP_AST_03_AssetUpdateUsesOptimisticVersion(t *testing.T) {
	t.Skip("GAP-AST-03: DepreciationAttachment executes an unconditional UPDATE without an expected version; tracked by P1-T9")
}
