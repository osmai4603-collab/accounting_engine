package sdk

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	internalreporting "github.com/osmai4603-collab/accounting_engine/internal/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/shopspring/decimal"
)

func (s *defaultReportingService) GetCashFlow(
	ctx context.Context,
	request reporting.CashFlowRequest,
) (reporting.CashFlowStatementReport, error) {
	if s.store == nil {
		return reporting.CashFlowStatementReport{}, fmt.Errorf("%w: reporting store not configured", types.ErrCapabilityNotImplemented)
	}
	repository, ok := s.store.(internalreporting.Repository)
	if !ok {
		return reporting.CashFlowStatementReport{}, fmt.Errorf(
			"%w: reporting store does not expose cash-flow ledger data", types.ErrCapabilityNotImplemented,
		)
	}
	if request.CompanyID == (types.CompanyID{}) {
		return reporting.CashFlowStatementReport{}, fmt.Errorf("%w: company id is required", internalreporting.ErrCompanyScopeRequired)
	}

	cashEquivalentIDs := make([]uuid.UUID, len(request.CashEquivalentIDs))
	for i, accountID := range request.CashEquivalentIDs {
		cashEquivalentIDs[i] = uuid.UUID(accountID)
	}
	classifications := make(map[uuid.UUID]internalreporting.CashFlowTransactionClassification, len(request.Transactions))
	for _, transaction := range request.Transactions {
		lineID := uuid.UUID(transaction.CashLineID)
		if lineID == uuid.Nil {
			return reporting.CashFlowStatementReport{}, fmt.Errorf(
				"%w: cash-line classification requires a line id", internalreporting.ErrInvalidCashFlowRequest,
			)
		}
		if _, exists := classifications[lineID]; exists {
			return reporting.CashFlowStatementReport{}, fmt.Errorf(
				"%w: duplicate cash-line classification %s", internalreporting.ErrInvalidCashFlowRequest, lineID,
			)
		}
		classifications[lineID] = internalreporting.CashFlowTransactionClassification{
			Activity: internalreporting.CashFlowActivity(transaction.Activity),
			Code:     transaction.Code,
			Label:    transaction.Label,
		}
	}
	nonCash := make([]internalreporting.NonCashTransaction, len(request.NonCashTransactions))
	for i, transaction := range request.NonCashTransactions {
		if transaction.Amount.Currency() == "" {
			return reporting.CashFlowStatementReport{}, fmt.Errorf(
				"%w: non-cash transaction amount requires a currency", internalreporting.ErrInvalidCashFlowRequest,
			)
		}
		nonCash[i] = internalreporting.NonCashTransaction{
			Code: transaction.Code, Description: transaction.Description,
			Amount: transaction.Amount.Amount(), Currency: transaction.Amount.Currency(),
		}
	}

	result, err := internalreporting.NewCashFlowEngine(repository).GenerateWithRequestForCompanyContext(
		ctx, uuid.UUID(request.CompanyID), internalreporting.CashFlowRequest{
			StartDate: request.StartDate, EndDate: request.EndDate, Currency: request.Currency,
			Method:       internalreporting.CashFlowMethod(request.Method),
			Profile:      internalreporting.CashFlowProfile(request.Profile),
			Policy:       internalreporting.CashFlowPolicy{CashEquivalentAccountIDs: cashEquivalentIDs},
			Transactions: classifications, NonCashTransactions: nonCash,
		},
	)
	if err != nil {
		return reporting.CashFlowStatementReport{}, err
	}
	return publicCashFlowResult(request.CompanyID, result)
}

func publicCashFlowResult(
	companyID types.CompanyID,
	result internalreporting.CashFlow,
) (reporting.CashFlowStatementReport, error) {
	money := func(amount decimal.Decimal) (types.Money, error) {
		return types.NewMoney(amount, result.Currency)
	}
	startingProfit, err := money(result.StartingProfit)
	if err != nil {
		return reporting.CashFlowStatementReport{}, err
	}
	netProfitBeforeTax, err := money(result.NetProfitBeforeTax)
	if err != nil {
		return reporting.CashFlowStatementReport{}, err
	}
	investingAndFinancingAdjustments, err := money(result.InvestingAndFinancingAdjustments)
	if err != nil {
		return reporting.CashFlowStatementReport{}, err
	}
	nonCashAdjustments, err := money(result.NonCashAdjustments)
	if err != nil {
		return reporting.CashFlowStatementReport{}, err
	}
	workingCapitalChange, err := money(result.WorkingCapitalChange)
	if err != nil {
		return reporting.CashFlowStatementReport{}, err
	}
	operating, err := publicCashFlowSection(result.OperatingActivities, result.Currency)
	if err != nil {
		return reporting.CashFlowStatementReport{}, err
	}
	investing, err := publicCashFlowSection(result.InvestingActivities, result.Currency)
	if err != nil {
		return reporting.CashFlowStatementReport{}, err
	}
	financing, err := publicCashFlowSection(result.FinancingActivities, result.Currency)
	if err != nil {
		return reporting.CashFlowStatementReport{}, err
	}
	netChange, err := money(result.NetChangeInCash)
	if err != nil {
		return reporting.CashFlowStatementReport{}, err
	}
	fxEffect, err := money(result.EffectOfExchangeRatesOnCash)
	if err != nil {
		return reporting.CashFlowStatementReport{}, err
	}
	beginningCash, err := money(result.OpeningCash)
	if err != nil {
		return reporting.CashFlowStatementReport{}, err
	}
	endingCash, err := money(result.ClosingCash)
	if err != nil {
		return reporting.CashFlowStatementReport{}, err
	}
	nonCashTransactions := make([]reporting.NonCashTransaction, len(result.NonCashTransactions))
	for i, transaction := range result.NonCashTransactions {
		amount, err := types.NewMoney(transaction.Amount, transaction.Currency)
		if err != nil {
			return reporting.CashFlowStatementReport{}, err
		}
		nonCashTransactions[i] = reporting.NonCashTransaction{
			Code: transaction.Code, Description: transaction.Description, Amount: amount,
		}
	}
	return reporting.CashFlowStatementReport{
		CompanyID: companyID, StartDate: result.StartDate, EndDate: result.EndDate,
		Currency: result.Currency, Method: reporting.CashFlowMethod(result.Method),
		Profile: reporting.CashFlowProfile(result.Profile), StartingProfit: startingProfit,
		NetProfitBeforeTax:               netProfitBeforeTax,
		InvestingAndFinancingAdjustments: investingAndFinancingAdjustments,
		NonCashAdjustments:               nonCashAdjustments, WorkingCapitalChange: workingCapitalChange,
		Operating: operating, Investing: investing, Financing: financing,
		NetChangeInCash: netChange, EffectOfExchangeRatesOnCash: fxEffect,
		BeginningCash: beginningCash, EndingCash: endingCash,
		NonCashTransactions: nonCashTransactions, Reconciled: result.Reconciled,
	}, nil
}

func publicCashFlowSection(
	section internalreporting.CashFlowActivitySection,
	currency string,
) (reporting.CashFlowActivitySection, error) {
	total, err := types.NewMoney(section.Total, currency)
	if err != nil {
		return reporting.CashFlowActivitySection{}, err
	}
	lines := make([]reporting.CashFlowLine, len(section.Lines))
	for i, line := range section.Lines {
		amount, err := types.NewMoney(line.Amount, currency)
		if err != nil {
			return reporting.CashFlowActivitySection{}, err
		}
		lines[i] = reporting.CashFlowLine{Code: line.Code, Label: line.Label, Amount: amount}
	}
	return reporting.CashFlowActivitySection{Lines: lines, Total: total}, nil
}
