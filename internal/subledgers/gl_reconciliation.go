package subledgers

import (
	"context"
	"errors"
	"fmt"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrMixedCurrencyReconciliation    = errors.New("subledger: control account contains multiple currencies")
	ErrInvalidReconciliationKind      = errors.New("subledger: invalid reconciliation line kind")
	ErrReconciliationNotConfigured    = errors.New("subledger: reconciliation engine is not configured")
	ErrControlBalanceCurrencyMismatch = errors.New("subledger: GL control balance cannot be expressed in its functional currency")
)

// ReconciliationDiscrepancy is returned when a subledger balance does not match
// the corresponding control account in the general ledger.
type ReconciliationDiscrepancy struct {
	CompanyID        uuid.UUID
	ControlAccountID uuid.UUID
	Currency         string
	SubledgerBalance decimal.Decimal
	GLBalance        decimal.Decimal
	Difference       decimal.Decimal
}

// GeneralLedgerReader exposes only the minimum read contract a subledger
// reconciliation engine needs from the general ledger.
type GeneralLedgerReader interface {
	// GetAccountBalance returns the positive normal-side balance in its
	// functional currency (debit for AR, credit for AP).
	GetAccountBalance(ctx context.Context, companyID, accountID uuid.UUID) (financialtypes.Money, error)
}

type openLineBalanceReader interface {
	ListOpenLinesForCompany(uuid.UUID, string) ([]OpenLine, error)
}

// LedgerReconciliationEngine compares AR/AP subledger totals against the GL
// control account balances and flags mismatches.
type LedgerReconciliationEngine struct {
	arRepo openLineBalanceReader
	apRepo openLineBalanceReader
	glRepo GeneralLedgerReader
}

func NewLedgerReconciliationEngine(arRepo, apRepo openLineBalanceReader, glRepo GeneralLedgerReader) *LedgerReconciliationEngine {
	return &LedgerReconciliationEngine{arRepo: arRepo, apRepo: apRepo, glRepo: glRepo}
}

func (e *LedgerReconciliationEngine) ReconcileARControl(ctx context.Context, companyID, controlAccountID uuid.UUID) (*ReconciliationDiscrepancy, error) {
	return e.reconcileControl(ctx, companyID, controlAccountID, LineReceivable)
}

func (e *LedgerReconciliationEngine) ReconcileAPControl(ctx context.Context, companyID, controlAccountID uuid.UUID) (*ReconciliationDiscrepancy, error) {
	return e.reconcileControl(ctx, companyID, controlAccountID, LinePayable)
}

func (e *LedgerReconciliationEngine) ReconcileControl(ctx context.Context, companyID, controlAccountID uuid.UUID, kind LineKind) (*ReconciliationDiscrepancy, error) {
	return e.reconcileControl(ctx, companyID, controlAccountID, kind)
}

func (e *LedgerReconciliationEngine) reconcileControl(ctx context.Context, companyID, controlAccountID uuid.UUID, kind LineKind) (*ReconciliationDiscrepancy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if companyID == uuid.Nil || controlAccountID == uuid.Nil {
		return nil, ErrInvalidOpenLine
	}
	if kind != LineReceivable && kind != LinePayable {
		return nil, ErrInvalidReconciliationKind
	}
	if e.glRepo == nil {
		return nil, ErrReconciliationNotConfigured
	}
	reader := e.arRepo
	if kind == LinePayable {
		reader = e.apRepo
	}
	if reader == nil {
		return nil, ErrReconciliationNotConfigured
	}
	lines, err := reader.ListOpenLinesForCompany(companyID, "")
	if err != nil {
		return nil, err
	}

	subledgerTotal := decimal.Zero
	currency := ""
	for _, line := range lines {
		if line.ControlAccountID != controlAccountID || line.Kind != kind || line.Closed {
			continue
		}
		if line.CompanyID != companyID {
			return nil, ErrCompanyMismatch
		}
		if line.ResidualAmount.Currency() == "" || line.ResidualAmount.IsNegative() {
			return nil, ErrInvalidOpenLine
		}
		if currency == "" {
			currency = line.ResidualAmount.Currency()
		} else if currency != line.ResidualAmount.Currency() {
			return nil, ErrMixedCurrencyReconciliation
		}
		subledgerTotal = subledgerTotal.Add(line.ResidualAmount.Amount())
	}
	glBalance, err := e.glRepo.GetAccountBalance(ctx, companyID, controlAccountID)
	if err != nil {
		return nil, err
	}
	if glBalance.Currency() == "" {
		return nil, ErrInvalidOpenLine
	}
	if currency == "" {
		currency = glBalance.Currency()
	} else if currency != glBalance.Currency() {
		return nil, ErrMixedCurrencyReconciliation
	}
	difference := subledgerTotal.Sub(glBalance.Amount())
	if difference.IsZero() {
		return nil, nil
	}
	errorText := "AR"
	if kind == LinePayable {
		errorText = "AP"
	}
	return &ReconciliationDiscrepancy{
		CompanyID:        companyID,
		ControlAccountID: controlAccountID,
		Currency:         currency,
		SubledgerBalance: subledgerTotal,
		GLBalance:        glBalance.Amount(),
		Difference:       difference,
	}, fmt.Errorf("%s subledger mismatch for account %s: diff=%s", errorText, controlAccountID, difference)
}

func ControlAccountBalanceFromEntries(account masterdata.Account, entries []posting.JournalEntry, functionalCurrency string) (financialtypes.Money, error) {
	if account.ID == uuid.Nil || account.CompanyID == uuid.Nil || functionalCurrency == "" {
		return financialtypes.Money{}, ErrInvalidOpenLine
	}
	balance := decimal.Zero
	for _, entry := range entries {
		if entry.Status != posting.EntryPosted {
			continue
		}
		if entry.CompanyID != account.CompanyID {
			return financialtypes.Money{}, ErrCompanyMismatch
		}
		for _, line := range entry.Lines {
			if line.AccountID != account.ID {
				continue
			}
			debit, credit := line.Debit, line.Credit
			if line.FunctionalDebit.Currency() != "" || line.FunctionalCredit.Currency() != "" {
				debit, credit = line.FunctionalDebit, line.FunctionalCredit
			}
			if debit.Currency() != "" && debit.Currency() != functionalCurrency ||
				credit.Currency() != "" && credit.Currency() != functionalCurrency {
				return financialtypes.Money{}, ErrControlBalanceCurrencyMismatch
			}
			currency := debit.Currency()
			if currency == "" {
				currency = credit.Currency()
			}
			if currency != functionalCurrency {
				return financialtypes.Money{}, ErrControlBalanceCurrencyMismatch
			}
			balance = balance.Add(debit.Amount()).Sub(credit.Amount())
		}
	}
	if account.NormalBalance == masterdata.NormalBalanceCredit {
		balance = balance.Neg()
	}
	return financialtypes.NewMoney(balance, functionalCurrency)
}
