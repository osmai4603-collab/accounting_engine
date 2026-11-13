//go:build gapred

// Red test for GAP-AST-02: two concurrent depreciation runs on the SAME asset
// must not both post. The existing green suite only exercises two different
// assets, so it cannot detect the duplicate-posting race.
package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
)

func TestGAP_AST_02_ConcurrentDepreciationOfSameAssetPostsOnce(t *testing.T) {
	driver, _, engine, companyID, journalID, assetAcc, accumAcc := setupMemorySubledgerEnv(t)
	ctx := context.Background()

	cost, err := financialtypes.NewMoneyFromString("12000.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	zero, err := financialtypes.Zero("USD")
	if err != nil {
		t.Fatal(err)
	}
	assetID := uuid.New()
	asset := subledgers.FixedAsset{
		CompanyID: companyID, ID: assetID,
		AssetAccountID: assetAcc, AccumulatedAccountID: accumAcc,
		ExpenseAccountID: accumAcc, InServiceDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		HistoricalCost: cost, CurrentBookValue: cost, SalvageValue: zero, UsefulLifeMonths: 12,
		Method: subledgers.StraightLine, AccumulatedDepreciation: zero,
	}
	if err := driver.SaveFixedAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}

	ledger := subledgers.NewFixedAssetLedgerForCompany(driver, nil, companyID)
	asOf := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, poster := range []string{"poster-1", "poster-2"} {
		go func(posterID string) {
			<-start
			_, err := ledger.DepreciateAssetAtomic(ctx, engine, assetID, journalID, asOf, posterID)
			results <- err
		}(poster)
	}
	close(start)

	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("GAP-AST-02: concurrent depreciation of one asset produced %d successful postings, want exactly 1", successes)
	}
}
