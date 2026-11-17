package subledgers

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	apisubledger "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/subledger"
	apitypes "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/shopspring/decimal"
)

func TestP1002FixedAssetStoreSavesGetsAndListsByCompany(t *testing.T) {
	ctx := context.Background()
	companyID, assetID := uuid.New(), uuid.New()
	repository := NewMemoryRepository()
	ledger := NewFixedAssetLedgerForCompany(repository, nil, companyID)
	cost, _ := apitypes.NewMoneyFromString("12000", "USD")
	salvage, _ := apitypes.NewMoneyFromString("1000", "USD")
	publicAsset := apisubledger.FixedAsset{
		ID: assetID.String(), CompanyID: apitypes.CompanyID(companyID),
		AcquisitionDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		AcquisitionCost: cost, SalvageValue: salvage, UsefulLifeMonths: 12,
		DepreciationMethod: string(StraightLine), Status: "active",
	}
	if err := ledger.SaveAsset(ctx, apitypes.CompanyID(companyID), publicAsset); err != nil {
		t.Fatalf("SaveAsset() error = %v", err)
	}
	got, err := ledger.GetAsset(ctx, apitypes.CompanyID(companyID), assetID.String())
	if err != nil {
		t.Fatalf("GetAsset() error = %v", err)
	}
	if got.ID != publicAsset.ID || got.CompanyID != publicAsset.CompanyID ||
		!got.AcquisitionCost.Equal(cost) || !got.SalvageValue.Equal(salvage) ||
		!got.NetBookValue.Equal(cost) || got.DepreciationMethod != string(StraightLine) ||
		got.Status != "active" {
		t.Fatalf("GetAsset() = %+v, want saved accounting fields", got)
	}
	assets, err := ledger.ListAssets(ctx, apitypes.CompanyID(companyID))
	if err != nil {
		t.Fatalf("ListAssets() error = %v", err)
	}
	if len(assets) != 1 || assets[0].ID != assetID.String() {
		t.Fatalf("ListAssets() = %+v, want only company asset %s", assets, assetID)
	}
}

func TestP1002PublicDepreciationCalculatorSupportsBothMethods(t *testing.T) {
	ledger := &FixedAssetLedger{}
	cost, _ := apitypes.NewMoneyFromString("12000", "USD")
	salvage, _ := apitypes.Zero("USD")
	bookValue, _ := apitypes.NewMoneyFromString("9000", "USD")
	cases := []struct {
		name   string
		method string
		want   string
	}{
		{name: "straight-line", method: string(StraightLine), want: "1000"},
		{name: "declining-balance", method: string(DecliningBalance), want: "1500"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := ledger.ComputeMonthlyDepreciation(apisubledger.FixedAsset{
				AcquisitionCost: cost, SalvageValue: salvage, NetBookValue: bookValue,
				UsefulLifeMonths: 12, DepreciationMethod: test.method,
			})
			if err != nil {
				t.Fatalf("ComputeMonthlyDepreciation() error = %v", err)
			}
			if !got.Amount().Equal(decimal.RequireFromString(test.want)) || got.Currency() != "USD" {
				t.Fatalf("depreciation = %s, want %s USD", got.Amount(), test.want)
			}
		})
	}
}

func TestP1002PublicDepreciationCalculatorCapsAtSalvageValue(t *testing.T) {
	ledger := &FixedAssetLedger{}
	cost, _ := apitypes.NewMoneyFromString("12000", "USD")
	salvage, _ := apitypes.NewMoneyFromString("1000", "USD")
	bookValue, _ := apitypes.NewMoneyFromString("1100", "USD")
	got, err := ledger.ComputeMonthlyDepreciation(apisubledger.FixedAsset{
		AcquisitionCost: cost, SalvageValue: salvage, NetBookValue: bookValue,
		UsefulLifeMonths: 12, DepreciationMethod: string(DecliningBalance),
	})
	if err != nil {
		t.Fatalf("ComputeMonthlyDepreciation() error = %v", err)
	}
	if !got.Amount().Equal(decimal.NewFromInt(100)) {
		t.Fatalf("depreciation = %s, want 100", got.Amount())
	}
}
