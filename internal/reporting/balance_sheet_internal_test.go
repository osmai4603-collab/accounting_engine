package reporting

import (
	"testing"

	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"

	"github.com/shopspring/decimal"
)

func TestAdjustedAccountBalanceHandlesContraSides(t *testing.T) {
	tests := []struct {
		name        string
		accountType masterdata.AccountType
		normal      masterdata.NormalBalance
		debit       string
		credit      string
		want        int64
	}{
		{name: "contra asset", accountType: masterdata.AccountTypeAsset, normal: masterdata.NormalBalanceCredit, credit: "25", want: -25},
		{name: "contra liability", accountType: masterdata.AccountTypeLiability, normal: masterdata.NormalBalanceDebit, debit: "25", want: -25},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account := masterdata.Account{
				AccountType: test.accountType, IsContraAccount: true, NormalBalance: test.normal,
			}
			side := accountSide{}
			if test.debit != "" {
				side.debit = decimal.RequireFromString(test.debit)
			}
			if test.credit != "" {
				side.credit = decimal.RequireFromString(test.credit)
			}
			if got := adjustedAccountBalance(side, account); !got.Equal(decimal.NewFromInt(test.want)) {
				t.Fatalf("adjustedAccountBalance() = %s, want %d", got, test.want)
			}
		})
	}
}
