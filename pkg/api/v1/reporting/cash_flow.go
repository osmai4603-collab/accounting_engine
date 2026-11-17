package reporting

import (
	"time"

	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

type CashFlowMethod string
type CashFlowProfile string
type CashFlowActivity string

const (
	CashFlowMethodDirect   CashFlowMethod = "direct"
	CashFlowMethodIndirect CashFlowMethod = "indirect"

	CashFlowProfileIAS7   CashFlowProfile = "ias7"
	CashFlowProfileIFRS18 CashFlowProfile = "ifrs18"

	CashFlowActivityOperating CashFlowActivity = "operating"
	CashFlowActivityInvesting CashFlowActivity = "investing"
	CashFlowActivityFinancing CashFlowActivity = "financing"
)

type CashFlowRequest struct {
	CompanyID           types.CompanyID
	StartDate           time.Time
	EndDate             time.Time
	Currency            string
	Method              CashFlowMethod
	Profile             CashFlowProfile
	CashEquivalentIDs   []types.AccountID
	Transactions        []CashFlowTransactionClassification
	NonCashTransactions []NonCashTransaction
}

// Cash transaction classification keys the cash journal line, not its
// counter-account. This avoids inferring transaction purpose from account type.
type CashFlowTransactionClassification struct {
	CashLineID types.JournalEntryLineID
	Activity   CashFlowActivity
	Code       string
	Label      string
}

type CashFlowLine struct {
	Code   string
	Label  string
	Amount types.Money
}

type CashFlowActivitySection struct {
	Lines []CashFlowLine
	Total types.Money
}

type NonCashTransaction struct {
	Code        string
	Description string
	Amount      types.Money
}

type CashFlowStatementReport struct {
	CompanyID                        types.CompanyID
	StartDate                        time.Time
	EndDate                          time.Time
	Currency                         string
	Method                           CashFlowMethod
	Profile                          CashFlowProfile
	StartingProfit                   types.Money
	NetProfitBeforeTax               types.Money
	InvestingAndFinancingAdjustments types.Money
	NonCashAdjustments               types.Money
	WorkingCapitalChange             types.Money
	Operating                        CashFlowActivitySection
	Investing                        CashFlowActivitySection
	Financing                        CashFlowActivitySection
	NetChangeInCash                  types.Money
	EffectOfExchangeRatesOnCash      types.Money
	BeginningCash                    types.Money
	EndingCash                       types.Money
	NonCashTransactions              []NonCashTransaction
	Reconciled                       bool
}
