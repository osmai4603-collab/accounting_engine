package reporting

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/valuation"
)

var (
	ErrReportingDependencyMissing = errors.New("reporting dependency missing")
	ErrReportingTaxonomyMissing   = errors.New("reporting account taxonomy is missing")
)

type ReportKind string

const (
	ReportTrialBalance  ReportKind = "trial_balance"
	ReportGeneralLedger ReportKind = "general_ledger"
	ReportBalanceSheet  ReportKind = "balance_sheet"
	ReportIncome        ReportKind = "income_statement"
	ReportCashFlow      ReportKind = "cash_flow"
	ReportEquity        ReportKind = "equity"
)

// ReportRequirements describes the source contracts needed for a report.
// Currency translation is required only when source and presentation
// currencies differ.
type ReportRequirements struct {
	LedgerRead          bool
	EntryRead           bool
	AccountTaxonomy     bool
	CurrencyTranslation bool
}

func RequirementsForReport(kind ReportKind, sourceCurrency, presentationCurrency string) (ReportRequirements, error) {
	switch kind {
	case ReportTrialBalance, ReportGeneralLedger, ReportBalanceSheet, ReportIncome, ReportCashFlow, ReportEquity:
	default:
		return ReportRequirements{}, fmt.Errorf("unknown report kind %q", kind)
	}
	sourceCurrency = strings.ToUpper(strings.TrimSpace(sourceCurrency))
	presentationCurrency = strings.ToUpper(strings.TrimSpace(presentationCurrency))
	return ReportRequirements{
		LedgerRead:          true,
		EntryRead:           kind == ReportGeneralLedger,
		AccountTaxonomy:     true,
		CurrencyTranslation: presentationCurrency != "" && sourceCurrency != presentationCurrency,
	}, nil
}

// ValidateReportRequirements checks the declared contracts and confirms that
// the chart of accounts contains usable taxonomy data before a report is run.
func ValidateReportRequirements(repository any, requirements ReportRequirements) error {
	if requirements.LedgerRead {
		if repository == nil {
			return fmt.Errorf("%w: ledger read", ErrReportingDependencyMissing)
		}
		_, hasEntries := repository.(LedgerRepository)
		_, hasBalances := repository.(MaterializedBalanceReader)
		if requirements.EntryRead && !hasEntries {
			return fmt.Errorf("%w: journal entry detail read", ErrReportingDependencyMissing)
		}
		if !hasEntries && !hasBalances {
			return fmt.Errorf("%w: ledger read (posted entries or materialized balances)", ErrReportingDependencyMissing)
		}
	}
	if requirements.AccountTaxonomy {
		accounts, ok := repository.(AccountRepository)
		if !ok || accounts == nil {
			return fmt.Errorf("%w: account taxonomy", ErrReportingDependencyMissing)
		}
		values, err := accounts.ListAccounts(context.Background())
		if err != nil {
			return fmt.Errorf("load reporting account taxonomy: %w", err)
		}
		if len(values) == 0 {
			return ErrReportingTaxonomyMissing
		}
		for _, account := range values {
			if account.ID == uuid.Nil || strings.TrimSpace(account.Code) == "" ||
				strings.TrimSpace(string(account.AccountType)) == "" {
				return fmt.Errorf("%w: account %s lacks an ID, code, or account type", ErrReportingTaxonomyMissing, account.ID)
			}
		}
	}
	if requirements.CurrencyTranslation {
		if _, ok := repository.(valuation.RateProviderSPI); !ok {
			return fmt.Errorf("%w: currency translation rate provider", ErrReportingDependencyMissing)
		}
	}
	return nil
}

func (e *Engine) ValidateCapability(kind ReportKind, sourceCurrency, presentationCurrency string) error {
	if e == nil || e.TrialBalance == nil {
		return fmt.Errorf("%w: reporting engine", ErrReportingDependencyMissing)
	}
	requirements, err := RequirementsForReport(kind, sourceCurrency, presentationCurrency)
	if err != nil {
		return err
	}
	return ValidateReportRequirements(e.TrialBalance.repository, requirements)
}
