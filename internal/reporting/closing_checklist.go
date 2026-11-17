package reporting

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
)

type ChecklistSeverity string

const (
	SeverityBlocker ChecklistSeverity = "BLOCKER"
	SeverityWarning ChecklistSeverity = "WARNING"
	SeverityInfo    ChecklistSeverity = "INFO"
)

type ChecklistStatus string

const (
	ChecklistPass   ChecklistStatus = "PASS"
	ChecklistFailed ChecklistStatus = "FAILED"
)

type ChecklistItemCode string

const (
	CheckCodeDraftEntriesPending        ChecklistItemCode = "DRAFT_ENTRIES_PENDING"
	CheckCodeSuspenseAccountsNonZero    ChecklistItemCode = "SUSPENSE_ACCOUNTS_NON_ZERO"
	CheckCodeSubledgerExceptionsPending ChecklistItemCode = "SUBLEDGER_EXCEPTIONS_PENDING"
	CheckCodeMonthlyPeriodsClosed       ChecklistItemCode = "MONTHLY_PERIODS_CLOSED"
	CheckCodeAdjustmentPeriodAvailable  ChecklistItemCode = "PERIOD_ADJUSTMENT_AVAILABLE"
	CheckCodeUnrealizedFXValuation      ChecklistItemCode = "UNREALIZED_FX_VALUATION"
	CheckCodeFiscalYearOpen             ChecklistItemCode = "FISCAL_YEAR_OPEN"
)

type ChecklistItem struct {
	Code        ChecklistItemCode `json:"code"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Severity    ChecklistSeverity `json:"severity"`
	Status      ChecklistStatus   `json:"status"`
	Details     string            `json:"details"`
	Evidence    []string          `json:"evidence,omitempty"`
}

type ClosingChecklistReport struct {
	CompanyID     uuid.UUID       `json:"company_id"`
	FiscalYearID  uuid.UUID       `json:"fiscal_year_id,omitempty"`
	PeriodID      uuid.UUID       `json:"period_id,omitempty"`
	AsOfDate      time.Time       `json:"as_of_date"`
	CanClose      bool            `json:"can_close"`
	BlockersCount int             `json:"blockers_count"`
	WarningsCount int             `json:"warnings_count"`
	PassesCount   int             `json:"passes_count"`
	Items         []ChecklistItem `json:"items"`
	EvaluatedAt   time.Time       `json:"evaluated_at"`
}

func (r ClosingChecklistReport) Summary() string {
	if r.CanClose {
		return fmt.Sprintf("Ready to close: %d passed, %d warnings, 0 blockers", r.PassesCount, r.WarningsCount)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Cannot close: %d blockers failed, %d warnings, %d passed. Blocker details: ",
		r.BlockersCount, r.WarningsCount, r.PassesCount))
	for _, item := range r.Items {
		if item.Severity == SeverityBlocker && item.Status == ChecklistFailed {
			sb.WriteString(fmt.Sprintf("[%s: %s] ", item.Code, item.Details))
		}
	}
	return sb.String()
}

// EvaluateFiscalYearChecklist runs pre-close integrity and compliance validations
// before allowing a fiscal year closing move to proceed.
func EvaluateFiscalYearChecklist(ctx context.Context, repository Repository, year masterdata.FiscalYear) (ClosingChecklistReport, error) {
	if year.CompanyID == uuid.Nil {
		return ClosingChecklistReport{}, ErrCompanyScopeRequired
	}

	report := ClosingChecklistReport{
		CompanyID:    year.CompanyID,
		FiscalYearID: year.ID,
		AsOfDate:     year.EndDate,
		EvaluatedAt:  time.Now().UTC(),
		Items:        make([]ChecklistItem, 0, 8),
	}

	repo, err := newCompanyReportingRepository(ctx, repository, year.CompanyID, year.EndDate)
	if err != nil {
		return report, err
	}

	if year.State != masterdata.FiscalYearOpen {
		report.Items = append(report.Items, ChecklistItem{
			Code:        CheckCodeFiscalYearOpen,
			Name:        "Fiscal Year Open",
			Description: "Annual closing can only be initiated for an open fiscal year.",
			Severity:    SeverityBlocker,
			Status:      ChecklistFailed,
			Details:     fmt.Sprintf("Fiscal year state is %s, not open", year.State),
		})
	} else {
		report.Items = append(report.Items, ChecklistItem{
			Code:        CheckCodeFiscalYearOpen,
			Name:        "Fiscal Year Open",
			Description: "Annual closing can only be initiated for an open fiscal year.",
			Severity:    SeverityBlocker,
			Status:      ChecklistPass,
			Details:     "Fiscal year is open",
		})
	}

	// 1. Check Draft Entries Pending (Blocker)
	var draftEvidence []string
	if draftRepo, ok := repository.(DraftEntryRepository); ok {
		drafts, err := draftRepo.ListDraftEntriesForCompanyContext(ctx, year.CompanyID)
		if err != nil {
			return report, err
		}
		for _, e := range drafts {
			dDate := e.EntryDate
			if dDate.IsZero() {
				dDate = e.Date
			}
			if (year.StartDate.IsZero() || !dDate.Before(year.StartDate)) && (year.EndDate.IsZero() || !dDate.After(year.EndDate)) {
				ref := e.Reference
				if ref == "" {
					ref = e.ID.String()
				}
				draftEvidence = append(draftEvidence, fmt.Sprintf("Draft Entry %s (Date: %s)", ref, dDate.Format("2006-01-02")))
			}
		}
	} else {
		entries, err := repo.ListEntriesForCompanyContext(ctx, year.CompanyID, year.StartDate, year.EndDate)
		if err != nil {
			return report, err
		}
		for _, e := range entries {
			if e.Status == posting.EntryDraft {
				ref := e.Reference
				if ref == "" {
					ref = e.ID.String()
				}
				draftEvidence = append(draftEvidence, fmt.Sprintf("Draft Entry %s (Date: %s)", ref, e.EntryDate.Format("2006-01-02")))
			}
		}
	}
	if len(draftEvidence) > 0 {
		report.Items = append(report.Items, ChecklistItem{
			Code:        CheckCodeDraftEntriesPending,
			Name:        "Unposted Draft Journal Entries",
			Description: "All draft entries within the fiscal year must be posted or rejected before closing.",
			Severity:    SeverityBlocker,
			Status:      ChecklistFailed,
			Details:     fmt.Sprintf("%d unposted draft journal entries exist in the fiscal year", len(draftEvidence)),
			Evidence:    draftEvidence,
		})
	} else {
		report.Items = append(report.Items, ChecklistItem{
			Code:        CheckCodeDraftEntriesPending,
			Name:        "Unposted Draft Journal Entries",
			Description: "All draft entries within the fiscal year must be posted or rejected before closing.",
			Severity:    SeverityBlocker,
			Status:      ChecklistPass,
			Details:     "No pending draft entries found in the fiscal year",
		})
	}

	// 2. Check Monthly Periods Closed (Blocker)
	var openPeriodsEvidence []string
	for _, p := range year.Periods {
		if p.Number >= 1 && p.Number <= 12 && p.State != masterdata.PeriodHardClosed {
			openPeriodsEvidence = append(openPeriodsEvidence, fmt.Sprintf("Period %d (%s) is %s, not hard closed", p.Number, p.Name, p.State))
		}
	}
	if len(openPeriodsEvidence) > 0 {
		report.Items = append(report.Items, ChecklistItem{
			Code:        CheckCodeMonthlyPeriodsClosed,
			Name:        "Monthly Fiscal Periods Closure",
			Description: "All regular monthly periods 1-12 must be hard closed before closing the fiscal year.",
			Severity:    SeverityBlocker,
			Status:      ChecklistFailed,
			Details:     fmt.Sprintf("%d monthly periods are not hard closed", len(openPeriodsEvidence)),
			Evidence:    openPeriodsEvidence,
		})
	} else {
		report.Items = append(report.Items, ChecklistItem{
			Code:        CheckCodeMonthlyPeriodsClosed,
			Name:        "Monthly Fiscal Periods Closure",
			Description: "All regular monthly periods 1-12 must be hard closed before closing the fiscal year.",
			Severity:    SeverityBlocker,
			Status:      ChecklistPass,
			Details:     "All monthly periods 1-12 are closed",
		})
	}

	// 3. Check Period 13 Available (Blocker)
	var period13Found bool
	for _, p := range year.Periods {
		if p.Number == 13 {
			period13Found = p.State == masterdata.PeriodOpen && p.Type == masterdata.FiscalPeriodTypeYearEndAdjustment
			break
		}
	}
	if !period13Found {
		report.Items = append(report.Items, ChecklistItem{
			Code:        CheckCodeAdjustmentPeriodAvailable,
			Name:        "Adjustment Period 13 Availability",
			Description: "Period 13 is required as the procedural destination for year-end closing entries.",
			Severity:    SeverityBlocker,
			Status:      ChecklistFailed,
			Details:     "Adjustment Period 13 is missing or is not open for posting",
		})
	} else {
		report.Items = append(report.Items, ChecklistItem{
			Code:        CheckCodeAdjustmentPeriodAvailable,
			Name:        "Adjustment Period 13 Availability",
			Description: "Period 13 is required as the procedural destination for year-end closing entries.",
			Severity:    SeverityBlocker,
			Status:      ChecklistPass,
			Details:     "Adjustment Period 13 is available",
		})
	}

	// 4. Check Suspense / Clearing Accounts (Blocker)
	accounts, err := repo.ListAccountsForCompanyContext(ctx, year.CompanyID)
	if err != nil {
		return report, err
	}
	balances, err := accountBalances(repo, year.StartDate, year.EndDate, "")
	if err != nil {
		return report, err
	}

	var suspenseEvidence []string
	for _, acc := range accounts {
		nameLower := strings.ToLower(acc.Name)
		if strings.Contains(nameLower, "suspense") || strings.Contains(nameLower, "clearing") ||
			acc.Code == "9999" || acc.SubClassification == "suspense" {
			bal := balances[acc.ID]
			net := bal.debit.Sub(bal.credit)
			if !net.IsZero() {
				suspenseEvidence = append(suspenseEvidence, fmt.Sprintf("Suspense Account %s (%s) has non-zero net balance %s",
					acc.Code, acc.Name, net.String()))
			}
		}
	}
	if len(suspenseEvidence) > 0 {
		report.Items = append(report.Items, ChecklistItem{
			Code:        CheckCodeSuspenseAccountsNonZero,
			Name:        "Suspense and Clearing Account Balances",
			Description: "All temporary, clearing, and suspense accounts must be reconciled and zeroed out before close.",
			Severity:    SeverityBlocker,
			Status:      ChecklistFailed,
			Details:     fmt.Sprintf("%d suspense/clearing accounts have non-zero balance", len(suspenseEvidence)),
			Evidence:    suspenseEvidence,
		})
	} else {
		report.Items = append(report.Items, ChecklistItem{
			Code:        CheckCodeSuspenseAccountsNonZero,
			Name:        "Suspense and Clearing Account Balances",
			Description: "All temporary, clearing, and suspense accounts must be reconciled and zeroed out before close.",
			Severity:    SeverityBlocker,
			Status:      ChecklistPass,
			Details:     "All suspense and clearing accounts are cleared to zero",
		})
	}

	// 5. Check Subledger Reconciliation Exceptions (Blocker / Warning)
	if exRepo, ok := repository.(subledgers.ReconciliationExceptionRepository); ok {
		exceptions, err := exRepo.ListReconciliationExceptions(ctx, year.CompanyID)
		if err != nil {
			return report, err
		}
		var unresolvedEvidence []string
		for _, ex := range exceptions {
			if ex.Status == subledgers.ReconciliationExceptionUnmatched || ex.Status == subledgers.ReconciliationExceptionDisputed {
				unresolvedEvidence = append(unresolvedEvidence, fmt.Sprintf("Exception %s in %s (Variance: %s %s, Status: %s)",
					ex.ID.String()[:8], ex.Domain, ex.Difference.String(), ex.FunctionalCurr, ex.Status))
			}
		}
		if len(unresolvedEvidence) > 0 {
			report.Items = append(report.Items, ChecklistItem{
				Code:        CheckCodeSubledgerExceptionsPending,
				Name:        "Subledger Reconciliation Exceptions",
				Description: "All subledger variances (AR/AP/Bank/Fixed Assets) must be resolved or matched before year-end close.",
				Severity:    SeverityBlocker,
				Status:      ChecklistFailed,
				Details:     fmt.Sprintf("%d unresolved subledger exceptions exist", len(unresolvedEvidence)),
				Evidence:    unresolvedEvidence,
			})
		} else {
			report.Items = append(report.Items, ChecklistItem{
				Code:        CheckCodeSubledgerExceptionsPending,
				Name:        "Subledger Reconciliation Exceptions",
				Description: "All subledger variances must be resolved or matched before year-end close.",
				Severity:    SeverityBlocker,
				Status:      ChecklistPass,
				Details:     "All subledger reconciliation exceptions are resolved",
			})
		}
	} else {
		report.Items = append(report.Items, ChecklistItem{
			Code:        CheckCodeSubledgerExceptionsPending,
			Name:        "Subledger Reconciliation Exceptions",
			Description: "All subledger variances must be resolved or matched before year-end close.",
			Severity:    SeverityInfo,
			Status:      ChecklistPass,
			Details:     "No active subledger reconciliation exceptions reported",
		})
	}

	// 6. Check Foreign Currency Valuation (Info / Warning)
	report.Items = append(report.Items, ChecklistItem{
		Code:        CheckCodeUnrealizedFXValuation,
		Name:        "Foreign Exchange Revaluation",
		Description: "Monetary items in foreign currencies should be revalued at reporting date closing rate under IAS 21.",
		Severity:    SeverityInfo,
		Status:      ChecklistPass,
		Details:     "This checklist does not verify remeasurement; the closing process preserves ledger values and does not book an FX plug. Run the IAS 21 valuation procedure separately.",
	})

	// Tally counts
	for _, item := range report.Items {
		if item.Status == ChecklistPass {
			report.PassesCount++
		} else {
			switch item.Severity {
			case SeverityBlocker:
				report.BlockersCount++
			case SeverityWarning:
				report.WarningsCount++
			}
		}
	}
	report.CanClose = (report.BlockersCount == 0)

	return report, nil
}
