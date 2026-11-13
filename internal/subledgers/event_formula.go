package subledgers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/shopspring/decimal"
)

func ruleMatchesEvent(rule EventAccountingRule, event EconomicEvent) bool {
	if rule.CompanyID != event.CompanyID || rule.EventType != event.EventType {
		return false
	}
	for key, expected := range rule.Conditions {
		if event.Attributes[key] != expected {
			return false
		}
	}
	return true
}

func translateEventLines(rule EventAccountingRule, event EconomicEvent) ([]posting.JournalEntryLine, error) {
	if err := rule.Validate(); err != nil {
		return nil, err
	}
	zero, err := financialtypes.Zero(event.Amount.Currency())
	if err != nil {
		return nil, err
	}
	lines := make([]posting.JournalEntryLine, 0, len(rule.Lines))
	for _, template := range rule.Lines {
		accountID := template.AccountID
		if accountID == uuid.Nil {
			accountID = rule.Accounts[template.AccountLookupKey]
		}
		if accountID == uuid.Nil {
			return nil, ErrMappingNotFound
		}
		amount, err := evaluateAmountFormula(template.AmountFormula, event)
		if err != nil || amount.IsNegative() || amount.IsZero() {
			return nil, ErrInvalidEvent
		}
		money, err := financialtypes.NewMoney(amount, event.Amount.Currency())
		if err != nil {
			return nil, err
		}
		money = money.RoundForPosting()
		if money.Amount().IsZero() {
			return nil, ErrInvalidEvent
		}
		line := posting.JournalEntryLine{
			CompanyID: event.CompanyID, ID: uuid.New(), AccountID: accountID,
			PartnerID: event.PartnerID, Debit: zero, Credit: zero,
		}
		switch template.Side {
		case EventLineDebit:
			line.Debit = money
		case EventLineCredit:
			line.Credit = money
		default:
			return nil, ErrMappingNotFound
		}
		lines = append(lines, line)
	}
	return lines, nil
}

func evaluateAmountFormula(formula string, event EconomicEvent) (decimal.Decimal, error) {
	expression, err := parser.ParseExpr(strings.TrimSpace(formula))
	if err != nil {
		return decimal.Zero, ErrInvalidEvent
	}
	return evaluateAmountNode(expression, event)
}

func evaluateAmountNode(node ast.Expr, event EconomicEvent) (decimal.Decimal, error) {
	switch typed := node.(type) {
	case *ast.BasicLit:
		if typed.Kind != token.INT && typed.Kind != token.FLOAT {
			return decimal.Zero, ErrInvalidEvent
		}
		value, err := decimal.NewFromString(typed.Value)
		if err != nil {
			return decimal.Zero, ErrInvalidEvent
		}
		return value, nil
	case *ast.Ident:
		if typed.Name == "amount" {
			return event.Amount.Amount(), nil
		}
		value, ok := event.Values[typed.Name]
		if !ok {
			return decimal.Zero, ErrInvalidEvent
		}
		return value.Amount(), nil
	case *ast.ParenExpr:
		return evaluateAmountNode(typed.X, event)
	case *ast.UnaryExpr:
		value, err := evaluateAmountNode(typed.X, event)
		if err != nil {
			return decimal.Zero, err
		}
		switch typed.Op {
		case token.ADD:
			return value, nil
		case token.SUB:
			return value.Neg(), nil
		default:
			return decimal.Zero, ErrInvalidEvent
		}
	case *ast.BinaryExpr:
		left, err := evaluateAmountNode(typed.X, event)
		if err != nil {
			return decimal.Zero, err
		}
		right, err := evaluateAmountNode(typed.Y, event)
		if err != nil {
			return decimal.Zero, err
		}
		switch typed.Op {
		case token.ADD:
			return left.Add(right), nil
		case token.SUB:
			return left.Sub(right), nil
		case token.MUL:
			return left.Mul(right), nil
		case token.QUO:
			if right.IsZero() {
				return decimal.Zero, ErrInvalidEvent
			}
			return left.DivRound(right, financialtypes.InternalScale), nil
		default:
			return decimal.Zero, ErrInvalidEvent
		}
	default:
		return decimal.Zero, ErrInvalidEvent
	}
}
