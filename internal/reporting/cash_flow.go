package reporting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/masterdata"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	"github.com/osmai4603-collab/accounting_engine/internal/valuation"
	"github.com/shopspring/decimal"
)

var (
	ErrCashFlowClassificationRequired            = errors.New("explicit cash-flow activity is required")
	ErrCashFlowTransactionClassificationRequired = errors.New("every cash transaction requires an explicit cash-flow classification")
	ErrCashEquivalentAccountRequired             = errors.New("cash and cash-equivalent accounts are required")
	ErrInvalidCashFlowRequest                    = errors.New("invalid cash-flow request")
)

type CashFlowMethod string
type CashFlowProfile string

const (
	CashFlowMethodIndirect CashFlowMethod = "indirect"
	CashFlowMethodDirect   CashFlowMethod = "direct"

	CashFlowProfileIAS7   CashFlowProfile = "ias7"
	CashFlowProfileIFRS18 CashFlowProfile = "ifrs18"
)

type CashFlowActivitySection struct {
	Lines []CashFlowLine
	Total decimal.Decimal
}

type CashFlowLine struct {
	Code   string
	Label  string
	Amount decimal.Decimal
}

type CashFlowTransactionClassification struct {
	Activity CashFlowActivity
	Code     string
	Label    string
}

type CashFlowActivity string

const (
	CashFlowActivityOperating CashFlowActivity = "operating"
	CashFlowActivityInvesting CashFlowActivity = "investing"
	CashFlowActivityFinancing CashFlowActivity = "financing"
)

type NonCashTransaction struct {
	Code        string
	Description string
	Amount      decimal.Decimal
	Currency    string
}

type CashFlowPolicy struct {
	// When set, this list is authoritative and can include liability overdrafts
	// designated as cash equivalents by the entity.
	CashEquivalentAccountIDs []uuid.UUID
}

type CashFlowRequest struct {
	StartDate           time.Time
	EndDate             time.Time
	Currency            string
	Method              CashFlowMethod
	Profile             CashFlowProfile
	Policy              CashFlowPolicy
	Transactions        map[uuid.UUID]CashFlowTransactionClassification
	NonCashTransactions []NonCashTransaction
}

// CashFlow is the common statement model returned by both presentation methods.
// The legacy summary fields are retained for existing report consumers.
type CashFlow struct {
	StartDate                        time.Time
	EndDate                          time.Time
	Currency                         string
	Method                           CashFlowMethod
	Profile                          CashFlowProfile
	StartingProfit                   decimal.Decimal
	NetProfitBeforeTax               decimal.Decimal
	InvestingAndFinancingAdjustments decimal.Decimal
	NonCashAdjustments               decimal.Decimal
	WorkingCapitalChange             decimal.Decimal
	OperatingActivities              CashFlowActivitySection
	InvestingActivities              CashFlowActivitySection
	FinancingActivities              CashFlowActivitySection
	OperatingCashFlow                decimal.Decimal
	InvestingCashFlow                decimal.Decimal
	FinancingCashFlow                decimal.Decimal
	NetChangeInCash                  decimal.Decimal
	EffectOfExchangeRatesOnCash      decimal.Decimal
	OpeningCash                      decimal.Decimal
	ClosingCash                      decimal.Decimal
	NonCashTransactions              []NonCashTransaction
	Reconciled                       bool
}

type CashFlowEngine struct{ repository Repository }

func NewCashFlowEngine(repository Repository) *CashFlowEngine {
	return &CashFlowEngine{repository: repository}
}

func (e *CashFlowEngine) Generate(start, end time.Time, currency string) (CashFlow, error) {
	return e.GenerateWithRequest(CashFlowRequest{
		StartDate: start, EndDate: end, Currency: currency,
		Method: CashFlowMethodIndirect, Profile: CashFlowProfileIAS7,
	})
}

func (e *CashFlowEngine) GenerateWithRequest(request CashFlowRequest) (CashFlow, error) {
	request, err := normalizeCashFlowRequest(request)
	if err != nil {
		return CashFlow{}, err
	}
	accounts, err := listAccounts(e.repository)
	if err != nil {
		return CashFlow{}, err
	}
	cashAccounts, err := selectCashAccounts(accounts, request.Policy)
	if err != nil {
		if errors.Is(err, ErrCashEquivalentAccountRequired) && request.Method == CashFlowMethodIndirect {
			for _, account := range accounts {
				if account.IsNonCashAdjustment {
					continue
				}
				if _, classificationErr := cashFlowActivity(account); classificationErr != nil {
					return CashFlow{}, classificationErr
				}
			}
		}
		return CashFlow{}, err
	}
	openingCash, closingCash, err := cashBalances(
		e.repository, cashAccounts, request.StartDate, request.EndDate, request.Currency,
	)
	if err != nil {
		return CashFlow{}, err
	}
	report := CashFlow{
		StartDate: request.StartDate, EndDate: request.EndDate, Currency: request.Currency,
		Method: request.Method, Profile: request.Profile, OpeningCash: openingCash, ClosingCash: closingCash,
		NonCashTransactions: append([]NonCashTransaction(nil), request.NonCashTransactions...),
	}
	switch request.Method {
	case CashFlowMethodIndirect:
		err = e.generateIndirect(&report, request, accounts, cashAccounts)
	case CashFlowMethodDirect:
		err = e.generateDirect(&report, request, cashAccounts)
	}
	if err != nil {
		return CashFlow{}, err
	}
	report.OperatingActivities.Total = sumCashFlowLines(report.OperatingActivities.Lines)
	report.InvestingActivities.Total = sumCashFlowLines(report.InvestingActivities.Lines)
	report.FinancingActivities.Total = sumCashFlowLines(report.FinancingActivities.Lines)
	report.NetChangeInCash = report.OperatingCashFlow.Add(report.InvestingCashFlow).Add(report.FinancingCashFlow)
	translated, err := cashAccountsRequireTranslation(e.repository, cashAccounts, request.EndDate, request.Currency)
	if err != nil {
		return CashFlow{}, err
	}
	if translated {
		report.EffectOfExchangeRatesOnCash = report.ClosingCash.Sub(report.OpeningCash).Sub(report.NetChangeInCash)
	}
	report.Reconciled = report.NetChangeInCash.Add(report.EffectOfExchangeRatesOnCash).
		Equal(report.ClosingCash.Sub(report.OpeningCash))
	if !report.Reconciled {
		return report, ErrUnbalancedCashFlow
	}
	return report, nil
}

func cashAccountsRequireTranslation(
	repository Repository,
	cashAccounts map[uuid.UUID]masterdata.Account,
	asOf time.Time,
	currency string,
) (bool, error) {
	entries, err := listPostedEntries(repository, time.Time{}, asOf)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !isLedgerPosted(entry.Status) {
			continue
		}
		for _, line := range entry.Lines {
			if _, isCash := cashAccounts[line.AccountID]; !isCash {
				continue
			}
			_, _, sourceCurrency := sourceLineAmounts(line)
			if sourceCurrency != "" && currency != "" && sourceCurrency != currency {
				return true, nil
			}
		}
	}
	return false, nil
}

func (e *CashFlowEngine) GenerateForCompany(companyID uuid.UUID, start, end time.Time, currency string) (CashFlow, error) {
	return e.GenerateForCompanyContext(context.Background(), companyID, start, end, currency)
}

func (e *CashFlowEngine) GenerateForCompanyContext(ctx context.Context, companyID uuid.UUID, start, end time.Time, currency string) (CashFlow, error) {
	return e.GenerateWithRequestForCompanyContext(ctx, companyID, CashFlowRequest{
		StartDate: start, EndDate: end, Currency: currency,
		Method: CashFlowMethodIndirect, Profile: CashFlowProfileIAS7,
	})
}

func (e *CashFlowEngine) GenerateWithRequestForCompanyContext(
	ctx context.Context,
	companyID uuid.UUID,
	request CashFlowRequest,
) (CashFlow, error) {
	repository, err := newCompanyReportingRepository(ctx, e.repository, companyID, request.EndDate)
	if err != nil {
		return CashFlow{}, err
	}
	return NewCashFlowEngine(repository).GenerateWithRequest(request)
}

// GenerateDirect is intentionally strict. Direct-method totals must be based
// on classifications attached to individual cash journal lines; counterparty
// account classes are not a reliable substitute for transaction intent.
func (e *CashFlowEngine) GenerateDirect(start, end time.Time, currency string) (CashFlow, error) {
	return e.GenerateWithRequest(CashFlowRequest{
		StartDate: start, EndDate: end, Currency: currency,
		Method: CashFlowMethodDirect, Profile: CashFlowProfileIAS7,
	})
}

func (e *CashFlowEngine) GenerateDirectWithClassifications(
	start, end time.Time,
	currency string,
	classifications map[uuid.UUID]CashFlowTransactionClassification,
) (CashFlow, error) {
	return e.GenerateWithRequest(CashFlowRequest{
		StartDate: start, EndDate: end, Currency: currency,
		Method: CashFlowMethodDirect, Profile: CashFlowProfileIAS7,
		Transactions: classifications,
	})
}

func (e *CashFlowEngine) GenerateDirectForCompany(companyID uuid.UUID, start, end time.Time, currency string) (CashFlow, error) {
	return e.GenerateDirectForCompanyContext(context.Background(), companyID, start, end, currency)
}

func (e *CashFlowEngine) GenerateDirectForCompanyContext(ctx context.Context, companyID uuid.UUID, start, end time.Time, currency string) (CashFlow, error) {
	repository, err := newCompanyReportingRepository(ctx, e.repository, companyID, end)
	if err != nil {
		return CashFlow{}, err
	}
	return NewCashFlowEngine(repository).GenerateDirect(start, end, currency)
}

func normalizeCashFlowRequest(request CashFlowRequest) (CashFlowRequest, error) {
	if !request.StartDate.IsZero() && !request.EndDate.IsZero() && request.EndDate.Before(request.StartDate) {
		return request, fmt.Errorf("%w: end date precedes start date", ErrInvalidCashFlowRequest)
	}
	if request.Method == "" {
		request.Method = CashFlowMethodIndirect
	}
	if request.Profile == "" {
		request.Profile = CashFlowProfileIAS7
	}
	if request.Method != CashFlowMethodDirect && request.Method != CashFlowMethodIndirect {
		return request, fmt.Errorf("%w: unsupported method %q", ErrInvalidCashFlowRequest, request.Method)
	}
	if request.Profile != CashFlowProfileIAS7 && request.Profile != CashFlowProfileIFRS18 {
		return request, fmt.Errorf("%w: unsupported profile %q", ErrInvalidCashFlowRequest, request.Profile)
	}
	request.Currency = strings.ToUpper(strings.TrimSpace(request.Currency))
	for _, transaction := range request.NonCashTransactions {
		if strings.TrimSpace(transaction.Code) == "" || strings.TrimSpace(transaction.Description) == "" ||
			transaction.Amount.IsZero() || strings.ToUpper(strings.TrimSpace(transaction.Currency)) != request.Currency {
			return request, fmt.Errorf("%w: invalid non-cash transaction disclosure", ErrInvalidCashFlowRequest)
		}
	}
	return request, nil
}

func selectCashAccounts(accounts []masterdata.Account, policy CashFlowPolicy) (map[uuid.UUID]masterdata.Account, error) {
	result := make(map[uuid.UUID]masterdata.Account)
	if len(policy.CashEquivalentAccountIDs) != 0 {
		byID := make(map[uuid.UUID]masterdata.Account, len(accounts))
		for _, account := range accounts {
			byID[account.ID] = account
		}
		for _, accountID := range policy.CashEquivalentAccountIDs {
			account, ok := byID[accountID]
			if !ok || (account.AccountType != masterdata.AccountTypeAsset &&
				account.AccountType != masterdata.AccountTypeLiability) {
				return nil, fmt.Errorf("%w: account %s", ErrInvalidCashFlowRequest, accountID)
			}
			result[account.ID] = account
		}
	} else {
		for _, account := range accounts {
			if isCashAccount(account) {
				result[account.ID] = account
			}
		}
	}
	if len(result) == 0 {
		return nil, ErrCashEquivalentAccountRequired
	}
	return result, nil
}

func cashBalances(
	repository Repository,
	cashAccounts map[uuid.UUID]masterdata.Account,
	start, end time.Time,
	currency string,
) (decimal.Decimal, decimal.Decimal, error) {
	opening := decimal.Zero
	if !start.IsZero() {
		balances, err := accountBalances(repository, time.Time{}, start.Add(-time.Nanosecond), currency)
		if err != nil {
			return decimal.Zero, decimal.Zero, err
		}
		for accountID, account := range cashAccounts {
			opening = opening.Add(signedCashBalance(balances[accountID], account))
		}
	}
	closingBalances, err := accountBalances(repository, time.Time{}, end, currency)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	closing := decimal.Zero
	for accountID, account := range cashAccounts {
		closing = closing.Add(signedCashBalance(closingBalances[accountID], account))
	}
	return opening, closing, nil
}

func (e *CashFlowEngine) generateIndirect(
	report *CashFlow,
	request CashFlowRequest,
	accounts []masterdata.Account,
	cashAccounts map[uuid.UUID]masterdata.Account,
) error {
	income, err := NewIncomeStatementEngine(e.repository).Generate(request.StartDate, request.EndDate, request.Currency)
	if err != nil {
		return err
	}
	periodBalances, err := accountMovementBalances(e.repository, request.StartDate, request.EndDate, request.Currency)
	if err != nil {
		return err
	}
	openingDate := time.Time{}
	if !request.StartDate.IsZero() {
		openingDate = request.StartDate.Add(-time.Nanosecond)
	}
	openBalances, err := accountBalances(e.repository, time.Time{}, openingDate, request.Currency)
	if err != nil {
		return err
	}
	closeBalances, err := accountBalances(e.repository, time.Time{}, request.EndDate, request.Currency)
	if err != nil {
		return err
	}
	report.NetProfitBeforeTax = income.ProfitBeforeTax
	report.StartingProfit = income.NetProfit
	if request.Profile == CashFlowProfileIFRS18 {
		report.StartingProfit = income.OperatingProfit
		report.OperatingActivities.Lines = append(report.OperatingActivities.Lines, CashFlowLine{
			Code: "OPERATING_PROFIT", Label: "Operating profit or loss", Amount: report.StartingProfit,
		})
	} else {
		report.OperatingActivities.Lines = append(report.OperatingActivities.Lines, CashFlowLine{
			Code: "NET_PROFIT", Label: "Profit or loss for the period", Amount: report.StartingProfit,
		})
		report.InvestingAndFinancingAdjustments = income.InvestingIncome.Add(income.FinancingIncome).
			Sub(income.FinanceCosts).Neg()
		if !report.InvestingAndFinancingAdjustments.IsZero() {
			report.OperatingActivities.Lines = append(report.OperatingActivities.Lines, CashFlowLine{
				Code: "NON_OPERATING_RESULTS", Label: "Remove investing and financing results", Amount: report.InvestingAndFinancingAdjustments,
			})
		}
	}
	report.InvestingCashFlow = income.InvestingIncome
	report.FinancingCashFlow = income.FinancingIncome.Sub(income.FinanceCosts)
	if !income.InvestingIncome.IsZero() {
		report.InvestingActivities.Lines = append(report.InvestingActivities.Lines, CashFlowLine{
			Code: "INVESTING_RESULTS", Label: "Investing results", Amount: income.InvestingIncome,
		})
	}
	if !report.FinancingCashFlow.IsZero() {
		report.FinancingActivities.Lines = append(report.FinancingActivities.Lines, CashFlowLine{
			Code: "FINANCING_RESULTS", Label: "Financing results", Amount: report.FinancingCashFlow,
		})
	}

	for _, account := range accounts {
		change := signedBalance(periodBalances[account.ID], account)
		if _, isCash := cashAccounts[account.ID]; isCash {
			continue
		}
		activity := account.CashFlowActivity
		if !account.IsNonCashAdjustment {
			activity, err = cashFlowActivity(account)
			if err != nil {
				return err
			}
		}
		if account.IsNonCashAdjustment || activity == masterdata.CashFlowNonCashAdjustment {
			// A non-cash adjustment belongs in the reconciliation only when it is
			// an income or expense account; balance-sheet movements are disclosed
			// separately as non-cash transactions.
			if account.AccountType == masterdata.AccountTypeIncome || account.AccountType == masterdata.AccountTypeExpense {
				adjustment := change
				report.NonCashAdjustments = report.NonCashAdjustments.Add(adjustment)
				report.OperatingActivities.Lines = append(report.OperatingActivities.Lines, CashFlowLine{
					Code: account.Code, Label: account.Name, Amount: adjustment,
				})
			}
			continue
		}
		open := signedBalance(openBalances[account.ID], account)
		close := signedBalance(closeBalances[account.ID], account)
		balanceChange := close.Sub(open)
		switch activity {
		case masterdata.CashFlowOperating:
			switch account.AccountType {
			case masterdata.AccountTypeAsset:
				adjustment := balanceChange.Neg()
				report.WorkingCapitalChange = report.WorkingCapitalChange.Add(adjustment)
				if !adjustment.IsZero() {
					report.OperatingActivities.Lines = append(report.OperatingActivities.Lines, CashFlowLine{
						Code: account.Code, Label: "Change in " + account.Name, Amount: adjustment,
					})
				}
			case masterdata.AccountTypeLiability:
				report.WorkingCapitalChange = report.WorkingCapitalChange.Add(balanceChange)
				if !balanceChange.IsZero() {
					report.OperatingActivities.Lines = append(report.OperatingActivities.Lines, CashFlowLine{
						Code: account.Code, Label: "Change in " + account.Name, Amount: balanceChange,
					})
				}
			}
		case masterdata.CashFlowInvesting:
			var amount decimal.Decimal
			switch account.AccountType {
			case masterdata.AccountTypeAsset:
				amount = balanceChange.Neg()
			case masterdata.AccountTypeLiability, masterdata.AccountTypeEquity:
				amount = balanceChange
			}
			report.InvestingCashFlow = report.InvestingCashFlow.Add(amount)
			if !amount.IsZero() {
				report.InvestingActivities.Lines = append(report.InvestingActivities.Lines, CashFlowLine{
					Code: account.Code, Label: "Change in " + account.Name, Amount: amount,
				})
			}
		case masterdata.CashFlowFinancing:
			report.FinancingCashFlow = report.FinancingCashFlow.Add(balanceChange)
			if !balanceChange.IsZero() {
				report.FinancingActivities.Lines = append(report.FinancingActivities.Lines, CashFlowLine{
					Code: account.Code, Label: "Change in " + account.Name, Amount: balanceChange,
				})
			}
		}
	}
	if request.Profile == CashFlowProfileIFRS18 {
		report.OperatingCashFlow = report.StartingProfit.Add(report.NonCashAdjustments).
			Add(report.WorkingCapitalChange)
	} else {
		report.OperatingCashFlow = report.StartingProfit.Add(report.InvestingAndFinancingAdjustments).
			Add(report.NonCashAdjustments).Add(report.WorkingCapitalChange)
	}
	return nil
}

func (e *CashFlowEngine) generateDirect(
	report *CashFlow,
	request CashFlowRequest,
	cashAccounts map[uuid.UUID]masterdata.Account,
) error {
	entries, err := listPostedEntries(e.repository, request.StartDate, request.EndDate)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !isLedgerPosted(entry.Status) || postingClassification(entry) == posting.EntryClassOpening {
			continue
		}
		entryDate := entryEffectiveDate(entry)
		for _, cashLine := range entry.Lines {
			_, isCash := cashAccounts[cashLine.AccountID]
			if !isCash {
				continue
			}
			debit, credit, err := translateLineAmountsUsingRate(
				cashLine, request.Currency, rateProvider(e.repository), valuation.SpotRate, entryDate,
			)
			if err != nil {
				return err
			}
			amount := debit.Sub(credit)
			if amount.IsZero() {
				continue
			}
			classification, ok := request.Transactions[cashLine.ID]
			if !ok || !validCashFlowActivity(classification.Activity) {
				return fmt.Errorf("%w: journal entry %s line %s", ErrCashFlowTransactionClassificationRequired, entry.ID, cashLine.ID)
			}
			label := strings.TrimSpace(classification.Label)
			if label == "" {
				label = strings.TrimSpace(classification.Code)
			}
			if label == "" {
				label = string(classification.Activity)
			}
			code := strings.TrimSpace(classification.Code)
			if code == "" {
				code = string(classification.Activity)
			}
			line := CashFlowLine{Code: code, Label: label, Amount: amount}
			switch classification.Activity {
			case CashFlowActivityOperating:
				report.OperatingActivities.Lines = append(report.OperatingActivities.Lines, line)
				report.OperatingCashFlow = report.OperatingCashFlow.Add(amount)
			case CashFlowActivityInvesting:
				report.InvestingActivities.Lines = append(report.InvestingActivities.Lines, line)
				report.InvestingCashFlow = report.InvestingCashFlow.Add(amount)
			case CashFlowActivityFinancing:
				report.FinancingActivities.Lines = append(report.FinancingActivities.Lines, line)
				report.FinancingCashFlow = report.FinancingCashFlow.Add(amount)
			}
		}
	}
	return nil
}

func sumCashFlowLines(lines []CashFlowLine) decimal.Decimal {
	total := decimal.Zero
	for _, line := range lines {
		total = total.Add(line.Amount)
	}
	return total
}

func validCashFlowActivity(activity CashFlowActivity) bool {
	return activity == CashFlowActivityOperating ||
		activity == CashFlowActivityInvesting ||
		activity == CashFlowActivityFinancing
}

func isCashAccount(account masterdata.Account) bool {
	return account.AccountType == masterdata.AccountTypeAsset &&
		(account.CashFlowActivity == masterdata.CashFlowCashEquivalent ||
			account.IsCashEquivalent || account.EffectiveIsCashEquivalent())
}

func cashFlowActivity(account masterdata.Account) (string, error) {
	switch account.CashFlowActivity {
	case masterdata.CashFlowOperating,
		masterdata.CashFlowInvesting,
		masterdata.CashFlowFinancing,
		masterdata.CashFlowNonCashAdjustment:
		return account.CashFlowActivity, nil
	case masterdata.CashFlowCashEquivalent:
		if isCashAccount(account) {
			return account.CashFlowActivity, nil
		}
	}
	return "", ErrCashFlowClassificationRequired
}

func signedBalance(side accountSide, account masterdata.Account) decimal.Decimal {
	value := side.debit.Sub(side.credit)
	if account.NormalBalance == masterdata.NormalBalanceCredit {
		return value.Neg()
	}
	return value
}

func signedCashBalance(side accountSide, account masterdata.Account) decimal.Decimal {
	value := signedBalance(side, account)
	if account.AccountType == masterdata.AccountTypeLiability {
		return value.Neg()
	}
	return value
}
