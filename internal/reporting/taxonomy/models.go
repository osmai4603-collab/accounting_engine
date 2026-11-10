package taxonomy

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type ReportingFramework string

const (
	FrameworkIFRS   ReportingFramework = "IFRS"
	FrameworkUSGAAP ReportingFramework = "US_GAAP"
	FrameworkSOCPA  ReportingFramework = "SOCPA"
)

type StatementType string

const (
	StatementBalanceSheet   StatementType = "BALANCE_SHEET"
	StatementIncome         StatementType = "INCOME_STATEMENT"
	StatementCashFlow       StatementType = "CASH_FLOW"
	StatementEquityMovement StatementType = "EQUITY_STATEMENT"
)

type NodeType string

const (
	NodeHeader       NodeType = "HEADER"
	NodeAccountRange NodeType = "ACCOUNT_RANGE"
	NodeAccountTag   NodeType = "ACCOUNT_TAG"
	NodeFormula      NodeType = "FORMULA"
	NodeSubtotal     NodeType = "SUBTOTAL"
)

type ReportNode struct {
	ID             uuid.UUID
	DefinitionID   uuid.UUID
	ParentID       *uuid.UUID
	Code           string
	TitleAr        string
	TitleEn        string
	NodeType       NodeType
	DisplayOrder   int
	SignMultiplier int
	AccountFrom    string
	AccountTo      string
	AccountTags    []string
	FormulaExpr    string
	Children       []*ReportNode
}

type FinancialReportDefinition struct {
	ID        uuid.UUID
	CompanyID *uuid.UUID
	Code      string
	Name      string
	Framework ReportingFramework
	Type      StatementType
	RootNodes []*ReportNode
	Version   int
	CreatedAt time.Time
}

// AccountBalance contains the normalized signed amount used by a report.
type AccountBalance struct {
	AccountID    uuid.UUID
	Code         string
	Tags         []string
	Amount       decimal.Decimal
	PeriodAmount decimal.Decimal
	EndingAmount decimal.Decimal
}

type PeriodBalances struct {
	AsOfDate time.Time
	Accounts []AccountBalance
}

type BalanceReader interface {
	GetPeriodBalances(context.Context, uuid.UUID, uuid.UUID, string) (PeriodBalances, error)
}

type EvaluatedLine struct {
	Code         string
	TitleAr      string
	TitleEn      string
	Level        int
	Amount       decimal.Decimal
	IsSubtotal   bool
	DrilldownIDs []uuid.UUID
}

type EvaluatedStatement struct {
	DefinitionCode string
	Framework      ReportingFramework
	Type           StatementType
	AsOfDate       time.Time
	Currency       string
	Lines          []EvaluatedLine
	GeneratedAt    time.Time
}
