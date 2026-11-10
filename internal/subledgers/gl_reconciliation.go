package subledgers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrMixedCurrencyReconciliation              = errors.New("subledger: control account contains multiple currencies")
	ErrInvalidReconciliationKind                = errors.New("subledger: invalid reconciliation line kind")
	ErrReconciliationNotConfigured              = errors.New("subledger: reconciliation engine is not configured")
	ErrControlBalanceCurrencyMismatch           = errors.New("subledger: GL control balance cannot be expressed in its functional currency")
	ErrReconciliationFunctionalAmountMissing    = errors.New("subledger: functional open-line balance is unavailable")
	ErrReconciliationHistoryUnavailable         = errors.New("subledger: dated reconciliation history is unavailable")
	ErrAsOfReconciliationNotSupported           = errors.New("subledger: general-ledger reader does not support as-of balances")
)

type ReconciliationBreakdown struct {
	TransactionCurrency string
	SubledgerAmount     decimal.Decimal
	FunctionalAmount    decimal.Decimal
}

type DiscrepancyReport struct {
	CompanyID        uuid.UUID
	Domain           string
	ControlAccountID uuid.UUID
	AsOfDate         time.Time
	FunctionalCurr   string
	SubledgerBalance decimal.Decimal
	GLBalance        decimal.Decimal
	Difference       decimal.Decimal
	Breakdown        []ReconciliationBreakdown
	DiscoveredAt     time.Time
}

func (r DiscrepancyReport) HasVariance() bool {
	return !r.Difference.IsZero()
}

type ReconciliationCertificate struct {
	CompanyID  uuid.UUID
	AsOfDate   time.Time
	CertifiedAt time.Time
	Clean      bool
	Reports    []DiscrepancyReport
}

type ReconciliationDiscrepancy struct {
	CompanyID        uuid.UUID
	ControlAccountID uuid.UUID
	Currency         string
	SubledgerBalance decimal.Decimal
	GLBalance        decimal.Decimal
	Difference       decimal.Decimal
}

type GeneralLedgerReader interface {
	GetAccountBalance(ctx context.Context, companyID, accountID uuid.UUID) (financialtypes.Money, error)
}

type asOfGeneralLedgerReader interface {
	GetAccountBalanceAsOf(ctx context.Context, companyID, accountID uuid.UUID, asOf time.Time) (financialtypes.Money, error)
}

type openLineBalanceReader interface {
	ListOpenLinesForCompany(uuid.UUID, string) ([]OpenLine, error)
}

type contextOpenLineBalanceReader interface {
	ListOpenLinesForCompanyContext(context.Context, uuid.UUID, string) ([]OpenLine, error)
}

type fixedAssetReconciliationReader interface {
	ListFixedAssetsForCompanyContext(context.Context, uuid.UUID) ([]FixedAsset, error)
}

type bankItemReconciliationReader interface {
	ListBankItemsForCompany(context.Context, uuid.UUID, string) ([]BankItem, error)
}

type companyAccountReconciliationReader interface {
	ListAccountsForCompanyContext(context.Context, uuid.UUID) ([]masterdata.Account, error)
}

type assetBalanceTotals struct {
	balance decimal.Decimal
	domain  string
}

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

func (e *LedgerReconciliationEngine) ReconcileARControlAsOf(ctx context.Context, companyID, controlAccountID uuid.UUID, asOf time.Time) (DiscrepancyReport, error) {
	return e.reconcileControlAsOf(ctx, companyID, controlAccountID, LineReceivable, asOf)
}

func (e *LedgerReconciliationEngine) ReconcileAPControlAsOf(ctx context.Context, companyID, controlAccountID uuid.UUID, asOf time.Time) (DiscrepancyReport, error) {
	return e.reconcileControlAsOf(ctx, companyID, controlAccountID, LinePayable, asOf)
}

func (e *LedgerReconciliationEngine) ReconcileControlAsOf(ctx context.Context, companyID, controlAccountID uuid.UUID, kind LineKind, asOf time.Time) (DiscrepancyReport, error) {
	return e.reconcileControlAsOf(ctx, companyID, controlAccountID, kind, asOf)
}

func (e *LedgerReconciliationEngine) reconcileControl(ctx context.Context, companyID, controlAccountID uuid.UUID, kind LineKind) (*ReconciliationDiscrepancy, error) {
	if e.glRepo == nil {
		return nil, ErrReconciliationNotConfigured
	}
	gl, err := e.glRepo.GetAccountBalance(ctx, companyID, controlAccountID)
	if err != nil {
		return nil, err
	}
	report, err := e.reconcileControlWithGL(ctx, companyID, controlAccountID, kind, time.Time{}, gl)
	if err != nil {
		return nil, err
	}
	if !report.HasVariance() {
		return nil, nil
	}
	return &ReconciliationDiscrepancy{
		CompanyID: report.CompanyID, ControlAccountID: report.ControlAccountID,
		Currency: report.FunctionalCurr, SubledgerBalance: report.SubledgerBalance,
		GLBalance: report.GLBalance, Difference: report.Difference,
	}, fmt.Errorf("%s subledger mismatch for account %s: diff=%s", report.Domain, controlAccountID, report.Difference)
}

func (e *LedgerReconciliationEngine) reconcileControlAsOf(
	ctx context.Context,
	companyID, controlAccountID uuid.UUID,
	kind LineKind,
	asOf time.Time,
) (DiscrepancyReport, error) {
	if asOf.IsZero() {
		return DiscrepancyReport{}, ErrReconciliationHistoryUnavailable
	}
	if e.glRepo == nil {
		return DiscrepancyReport{}, ErrReconciliationNotConfigured
	}
	reader, ok := e.glRepo.(asOfGeneralLedgerReader)
	if !ok {
		return DiscrepancyReport{}, ErrAsOfReconciliationNotSupported
	}
	gl, err := reader.GetAccountBalanceAsOf(ctx, companyID, controlAccountID, reconciliationCutoff(asOf))
	if err != nil {
		return DiscrepancyReport{}, err
	}
	return e.reconcileControlWithGL(ctx, companyID, controlAccountID, kind, asOf, gl)
}

func (e *LedgerReconciliationEngine) reconcileControlWithGL(
	ctx context.Context,
	companyID, controlAccountID uuid.UUID,
	kind LineKind,
	asOf time.Time,
	glBalance financialtypes.Money,
) (DiscrepancyReport, error) {
	if err := ctx.Err(); err != nil {
		return DiscrepancyReport{}, err
	}
	if companyID == uuid.Nil || controlAccountID == uuid.Nil {
		return DiscrepancyReport{}, ErrInvalidOpenLine
	}
	if kind != LineReceivable && kind != LinePayable {
		return DiscrepancyReport{}, ErrInvalidReconciliationKind
	}
	if glBalance.Currency() == "" {
		return DiscrepancyReport{}, ErrInvalidOpenLine
	}
	reader := e.arRepo
	domain := "AR"
	if kind == LinePayable {
		reader = e.apRepo
		domain = "AP"
	}
	if reader == nil {
		return DiscrepancyReport{}, ErrReconciliationNotConfigured
	}
	lines, err := listReconciliationLines(ctx, reader, companyID)
	if err != nil {
		return DiscrepancyReport{}, err
	}

	type balanceTotals struct {
		transaction decimal.Decimal
		functional  decimal.Decimal
	}
	transactionTotals := make(map[string]balanceTotals)
	functionalTotal := decimal.Zero
	for _, line := range lines {
		if line.ControlAccountID != controlAccountID || line.Kind != kind {
			continue
		}
		if line.CompanyID != companyID {
			return DiscrepancyReport{}, ErrCompanyMismatch
		}
		transactionAmount, functionalAmount, include, err := openLineBalanceAsOf(line, asOf)
		if err != nil {
			return DiscrepancyReport{}, err
		}
		if !include {
			continue
		}
		if transactionAmount.Currency() == "" || transactionAmount.IsNegative() ||
			functionalAmount.Currency() != glBalance.Currency() || functionalAmount.IsNegative() {
			return DiscrepancyReport{}, ErrReconciliationFunctionalAmountMissing
		}
		totals := transactionTotals[transactionAmount.Currency()]
		totals.transaction = totals.transaction.Add(transactionAmount.Amount())
		totals.functional = totals.functional.Add(functionalAmount.Amount())
		transactionTotals[transactionAmount.Currency()] = totals
		functionalTotal = functionalTotal.Add(functionalAmount.Amount())
	}
	breakdown := make([]ReconciliationBreakdown, 0, len(transactionTotals))
	for currency, totals := range transactionTotals {
		breakdown = append(breakdown, ReconciliationBreakdown{
			TransactionCurrency: currency, SubledgerAmount: totals.transaction,
			FunctionalAmount: totals.functional,
		})
	}
	sort.Slice(breakdown, func(i, j int) bool {
		return breakdown[i].TransactionCurrency < breakdown[j].TransactionCurrency
	})
	difference := functionalTotal.Sub(glBalance.Amount())
	reportDate := asOf
	if reportDate.IsZero() {
		reportDate = time.Now().UTC()
	}
	reportDate = time.Date(reportDate.UTC().Year(), reportDate.UTC().Month(), reportDate.UTC().Day(), 0, 0, 0, 0, time.UTC)
	return DiscrepancyReport{
		CompanyID: companyID, Domain: domain, ControlAccountID: controlAccountID,
		AsOfDate: reportDate, FunctionalCurr: glBalance.Currency(),
		SubledgerBalance: functionalTotal, GLBalance: glBalance.Amount(),
		Difference: difference, Breakdown: breakdown, DiscoveredAt: time.Now().UTC(),
	}, nil
}

func listReconciliationLines(ctx context.Context, reader openLineBalanceReader, companyID uuid.UUID) ([]OpenLine, error) {
	if contextual, ok := reader.(contextOpenLineBalanceReader); ok {
		return contextual.ListOpenLinesForCompanyContext(ctx, companyID, "")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return reader.ListOpenLinesForCompany(companyID, "")
}

func openLineBalanceAsOf(line OpenLine, asOf time.Time) (financialtypes.Money, financialtypes.Money, bool, error) {
	transaction := line.ResidualAmount
	functional := line.FunctionalResidualAmount
	if functional.Currency() == "" && line.OriginalAmount.Currency() == line.ResidualAmount.Currency() {
		functional = line.ResidualAmount
	}
	if functional.Currency() == "" && line.FunctionalOriginalAmount.Currency() != "" &&
		line.OriginalAmount.Currency() == line.ResidualAmount.Currency() && !line.OriginalAmount.Amount().IsZero() {
		functional, _ = financialtypes.NewMoney(
			line.FunctionalOriginalAmount.Amount().Mul(line.ResidualAmount.Amount()).Div(line.OriginalAmount.Amount()),
			line.FunctionalOriginalAmount.Currency(),
		)
	}
	if asOf.IsZero() {
		if line.Closed {
			transaction = mustZero(line.OriginalAmount.Currency())
			if functional.Currency() != "" {
				functional = mustZero(functional.Currency())
			}
		}
		return transaction, functional, true, nil
	}
	postingDate := line.PostingDate
	if postingDate.IsZero() {
		postingDate = line.DocumentDate
	}
	if postingDate.IsZero() {
		return financialtypes.Money{}, financialtypes.Money{}, false, ErrReconciliationHistoryUnavailable
	}
	date := time.Date(asOf.UTC().Year(), asOf.UTC().Month(), asOf.UTC().Day(), 0, 0, 0, 0, time.UTC)
	recordedDate := time.Date(postingDate.UTC().Year(), postingDate.UTC().Month(), postingDate.UTC().Day(), 0, 0, 0, 0, time.UTC)
	if recordedDate.After(date) {
		return transaction, functional, false, nil
	}
	matches, err := openLineMatches(line)
	if err != nil {
		return financialtypes.Money{}, financialtypes.Money{}, false, err
	}
	for _, match := range matches {
		if match.MatchedAt.IsZero() {
			return financialtypes.Money{}, financialtypes.Money{}, false, ErrReconciliationHistoryUnavailable
		}
		if !match.MatchedAt.After(date.Add(24*time.Hour - time.Nanosecond)) {
			continue
		}
		transaction, err = transaction.Add(match.Amount)
		if err != nil {
			return financialtypes.Money{}, financialtypes.Money{}, false, err
		}
		matchFunctional := match.FunctionalAmount
		if matchFunctional.Currency() == "" {
			if match.Amount.Currency() != line.OriginalAmount.Currency() ||
				line.OriginalAmount.Currency() != line.FunctionalOriginalAmount.Currency() {
				return financialtypes.Money{}, financialtypes.Money{}, false, ErrReconciliationHistoryUnavailable
			}
			matchFunctional = match.Amount
		}
		functional, err = functional.Add(matchFunctional)
		if err != nil {
			return financialtypes.Money{}, financialtypes.Money{}, false, err
		}
	}
	if transaction.GreaterThan(line.OriginalAmount) {
		return financialtypes.Money{}, financialtypes.Money{}, false, ErrReconciliationHistoryUnavailable
	}
	return transaction, functional, true, nil
}

func reconciliationCutoff(asOf time.Time) time.Time {
	asOf = asOf.UTC()
	return time.Date(asOf.Year(), asOf.Month(), asOf.Day(), 23, 59, 59, 999999999, time.UTC)
}

func (e *LedgerReconciliationEngine) ReconcileAllARControls(ctx context.Context, companyID uuid.UUID, asOf time.Time) ([]DiscrepancyReport, error) {
	return e.reconcileAllControls(ctx, companyID, LineReceivable, asOf)
}

func (e *LedgerReconciliationEngine) ReconcileAllAPControls(ctx context.Context, companyID uuid.UUID, asOf time.Time) ([]DiscrepancyReport, error) {
	return e.reconcileAllControls(ctx, companyID, LinePayable, asOf)
}

func (e *LedgerReconciliationEngine) reconcileAllControls(ctx context.Context, companyID uuid.UUID, kind LineKind, asOf time.Time) ([]DiscrepancyReport, error) {
	reader := e.arRepo
	if kind == LinePayable {
		reader = e.apRepo
	}
	if reader == nil {
		return nil, ErrReconciliationNotConfigured
	}
	lines, err := listReconciliationLines(ctx, reader, companyID)
	if err != nil {
		return nil, err
	}
	accounts := make(map[uuid.UUID]struct{})
	for _, line := range lines {
		if line.CompanyID != companyID {
			return nil, ErrCompanyMismatch
		}
		if line.Kind == kind {
			accounts[line.ControlAccountID] = struct{}{}
		}
	}
	if accountReader, ok := e.glRepo.(companyAccountReconciliationReader); ok {
		companyAccounts, err := accountReader.ListAccountsForCompanyContext(ctx, companyID)
		if err != nil {
			return nil, err
		}
		for _, account := range companyAccounts {
			if account.CompanyID != companyID {
				return nil, ErrCompanyMismatch
			}
			if account.IsControlAccount && account.IsReconcilable &&
				((kind == LineReceivable && account.AccountType == masterdata.AccountTypeAsset) ||
					(kind == LinePayable && account.AccountType == masterdata.AccountTypeLiability)) {
				accounts[account.ID] = struct{}{}
			}
		}
	}
	reports := make([]DiscrepancyReport, 0, len(accounts))
	for accountID := range accounts {
		report, err := e.reconcileControlAsOf(ctx, companyID, accountID, kind, asOf)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].ControlAccountID.String() < reports[j].ControlAccountID.String()
	})
	return reports, nil
}

func (e *LedgerReconciliationEngine) ReconcileFixedAssetsControl(ctx context.Context, companyID uuid.UUID, asOf time.Time) ([]DiscrepancyReport, error) {
	if companyID == uuid.Nil || asOf.IsZero() || e.glRepo == nil {
		return nil, ErrInvalidOpenLine
	}
	reader, ok := e.glRepo.(fixedAssetReconciliationReader)
	if !ok {
		return nil, ErrReconciliationNotConfigured
	}
	assets, err := reader.ListFixedAssetsForCompanyContext(ctx, companyID)
	if err != nil {
		return nil, err
	}
	balances := make(map[uuid.UUID]assetBalanceTotals)
	asOfDate := reconciliationCutoff(asOf)
	for _, asset := range assets {
		if asset.CompanyID != companyID {
			return nil, ErrCompanyMismatch
		}
		if asset.InServiceDate.IsZero() {
			return nil, ErrReconciliationHistoryUnavailable
		}
		if asset.InServiceDate.After(asOfDate) ||
			(asset.DisposedAt != nil && !asset.DisposedAt.After(asOfDate)) {
			continue
		}
		if asset.LastDepreciationDate.After(asOfDate) || asset.LastRevaluationDate.After(asOfDate) {
			return nil, ErrReconciliationHistoryUnavailable
		}
		if asset.AssetAccountID == uuid.Nil || asset.HistoricalCost.Currency() == "" {
			return nil, ErrInvalidAsset
		}
		if err := addAssetBalance(balances, asset.AssetAccountID, "FIXED_ASSETS_COST", asset.HistoricalCost.Amount()); err != nil {
			return nil, err
		}
		if !asset.AccumulatedDepreciation.Amount().IsZero() {
			if asset.AccumulatedAccountID == uuid.Nil {
				return nil, ErrInvalidAsset
			}
			if err := addAssetBalance(balances, asset.AccumulatedAccountID, "FIXED_ASSETS_DEPRECIATION", asset.AccumulatedDepreciation.Amount()); err != nil {
				return nil, err
			}
		}
		if !asset.AccumulatedImpairment.Amount().IsZero() {
			if asset.AccumulatedImpairmentAccountID == uuid.Nil {
				return nil, ErrInvalidAsset
			}
			if err := addAssetBalance(balances, asset.AccumulatedImpairmentAccountID, "FIXED_ASSETS_IMPAIRMENT", asset.AccumulatedImpairment.Amount()); err != nil {
				return nil, err
			}
		}
	}
	reports := make([]DiscrepancyReport, 0, len(balances))
	for accountID, balance := range balances {
		gl, err := e.accountBalanceAsOf(ctx, companyID, accountID, asOf)
		if err != nil {
			return nil, err
		}
		subledger := balance.balance
		if gl.Currency() != "" {
			for _, asset := range assets {
				if asset.CompanyID == companyID && asset.AssetAccountID == accountID && asset.HistoricalCost.Currency() != gl.Currency() {
					return nil, ErrReconciliationFunctionalAmountMissing
				}
				if asset.CompanyID == companyID && asset.AccumulatedAccountID == accountID && asset.AccumulatedDepreciation.Currency() != "" &&
					asset.AccumulatedDepreciation.Currency() != gl.Currency() {
					return nil, ErrReconciliationFunctionalAmountMissing
				}
				if asset.CompanyID == companyID && asset.AccumulatedImpairmentAccountID == accountID && asset.AccumulatedImpairment.Currency() != "" &&
					asset.AccumulatedImpairment.Currency() != gl.Currency() {
					return nil, ErrReconciliationFunctionalAmountMissing
				}
			}
		}
		report := newBalanceReport(companyID, accountID, balance.domain, asOf, gl, subledger)
		reports = append(reports, report)
	}
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].ControlAccountID.String() < reports[j].ControlAccountID.String()
	})
	return reports, nil
}

func (e *LedgerReconciliationEngine) ReconcileTreasuryControl(
	ctx context.Context,
	companyID, bankAccountID uuid.UUID,
	asOf time.Time,
) (DiscrepancyReport, error) {
	if companyID == uuid.Nil || bankAccountID == uuid.Nil || asOf.IsZero() || e.glRepo == nil {
		return DiscrepancyReport{}, ErrInvalidOpenLine
	}
	reader, ok := e.glRepo.(bankItemReconciliationReader)
	if !ok {
		return DiscrepancyReport{}, ErrReconciliationNotConfigured
	}
	items, err := reader.ListBankItemsForCompany(ctx, companyID, bankAccountID.String())
	if err != nil {
		return DiscrepancyReport{}, err
	}
	gl, err := e.accountBalanceAsOf(ctx, companyID, bankAccountID, asOf)
	if err != nil {
		return DiscrepancyReport{}, err
	}
	statementBalance := decimal.Zero
	pendingLedgerItems := decimal.Zero
	for _, item := range items {
		if item.CompanyID != companyID || item.AccountID != bankAccountID {
			return DiscrepancyReport{}, ErrCompanyMismatch
		}
		if item.Date.IsZero() {
			return DiscrepancyReport{}, ErrReconciliationHistoryUnavailable
		}
		if item.Date.After(reconciliationCutoff(asOf)) {
			continue
		}
		if item.Amount.Currency() != gl.Currency() {
			return DiscrepancyReport{}, ErrReconciliationFunctionalAmountMissing
		}
		if item.JournalEntryID == uuid.Nil {
			statementBalance = statementBalance.Add(item.Amount.Amount())
		} else if item.Status != BankItemMatched || item.MatchedAt.After(reconciliationCutoff(asOf)) {
			if item.Status == BankItemMatched && item.MatchedAt.IsZero() && asOf.Before(reconciliationCutoff(time.Now().UTC())) {
				return DiscrepancyReport{}, ErrReconciliationHistoryUnavailable
			}
			pendingLedgerItems = pendingLedgerItems.Add(item.Amount.Amount())
		}
	}
	return newBalanceReport(
		companyID, bankAccountID, "TREASURY", asOf, gl,
		statementBalance.Add(pendingLedgerItems),
	), nil
}

func (e *LedgerReconciliationEngine) ReconcileAllTreasuryControls(ctx context.Context, companyID uuid.UUID, asOf time.Time) ([]DiscrepancyReport, error) {
	if companyID == uuid.Nil || e.glRepo == nil {
		return nil, ErrInvalidOpenLine
	}
	reader, ok := e.glRepo.(companyAccountReconciliationReader)
	if !ok {
		return nil, ErrReconciliationNotConfigured
	}
	accounts, err := reader.ListAccountsForCompanyContext(ctx, companyID)
	if err != nil {
		return nil, err
	}
	reports := make([]DiscrepancyReport, 0)
	for _, account := range accounts {
		if account.CompanyID != companyID {
			return nil, ErrCompanyMismatch
		}
		if account.SubClassification != masterdata.SubClassBank &&
			account.SubClassification != masterdata.SubClassCashAndEquivalents {
			continue
		}
		report, err := e.ReconcileTreasuryControl(ctx, companyID, account.ID, asOf)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	sort.Slice(reports, func(i, j int) bool {
		return reports[i].ControlAccountID.String() < reports[j].ControlAccountID.String()
	})
	return reports, nil
}

func (e *LedgerReconciliationEngine) accountBalanceAsOf(ctx context.Context, companyID, accountID uuid.UUID, asOf time.Time) (financialtypes.Money, error) {
	if reader, ok := e.glRepo.(asOfGeneralLedgerReader); ok {
		return reader.GetAccountBalanceAsOf(ctx, companyID, accountID, reconciliationCutoff(asOf))
	}
	return financialtypes.Money{}, ErrAsOfReconciliationNotSupported
}

func addAssetBalance(balances map[uuid.UUID]assetBalanceTotals, accountID uuid.UUID, domain string, amount decimal.Decimal) error {
	current := balances[accountID]
	if current.domain != "" && current.domain != domain {
		return fmt.Errorf("%w: asset account %s is assigned to multiple register balances", ErrInvalidAsset, accountID)
	}
	current.domain = domain
	current.balance = current.balance.Add(amount)
	balances[accountID] = current
	return nil
}

func newBalanceReport(companyID, accountID uuid.UUID, domain string, asOf time.Time, gl financialtypes.Money, subledger decimal.Decimal) DiscrepancyReport {
	return DiscrepancyReport{
		CompanyID: companyID, Domain: domain, ControlAccountID: accountID,
		AsOfDate: reconciliationCutoff(asOf).Truncate(24 * time.Hour),
		FunctionalCurr: gl.Currency(), SubledgerBalance: subledger,
		GLBalance: gl.Amount(), Difference: subledger.Sub(gl.Amount()),
		DiscoveredAt: time.Now().UTC(),
	}
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
