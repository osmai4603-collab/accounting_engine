package ifrsautomation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/ifrs16"
	"github.com/osmai4603-collab/accounting_engine/internal/ifrs9"
	"github.com/osmai4603-collab/accounting_engine/internal/subledgers"
	"github.com/osmai4603-collab/accounting_engine/internal/types"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/shopspring/decimal"
)

var ErrInvalidAutomationConfig = errors.New("invalid IFRS automation configuration")

type AutomationConfig struct {
	Companies []CompanyAutomationConfig `json:"companies"`
}

type CompanyAutomationConfig struct {
	CompanyID uuid.UUID              `json:"company_id"`
	JournalID uuid.UUID              `json:"journal_id"`
	CreatedBy string                 `json:"created_by"`
	IFRS9     IFRS9AutomationConfig  `json:"ifrs9"`
	IFRS15    IFRS15AutomationConfig `json:"ifrs15"`
	IFRS16    IFRS16AutomationConfig `json:"ifrs16"`
}

type IFRS9AutomationConfig struct {
	ImpairmentAccountID    uuid.UUID                                  `json:"impairment_account_id"`
	AllowanceAccountID     uuid.UUID                                  `json:"allowance_account_id"`
	Currency               string                                     `json:"currency"`
	ProvisionMatrix        map[subledgers.AgingBucket]decimal.Decimal `json:"provision_matrix"`
	SICRReceivableIDs      []uuid.UUID                                `json:"sicr_receivable_ids,omitempty"`
	DefaultedReceivableIDs []uuid.UUID                                `json:"defaulted_receivable_ids,omitempty"`
}

type IFRS15AutomationConfig struct {
	RevenueAccountID    uuid.UUID `json:"revenue_account_id"`
	ContractLiabilityID uuid.UUID `json:"contract_liability_id"`
	ContractAssetID     uuid.UUID `json:"contract_asset_id"`
}

type IFRS16AutomationConfig struct {
	Leases []LeaseAutomationConfig `json:"leases"`
}

type LeaseAutomationConfig struct {
	ID       uuid.UUID            `json:"id"`
	Contract LeaseTerms           `json:"contract"`
	Accounts ifrs16.LeaseAccounts `json:"accounts"`
}

type LeaseTerms struct {
	CommencementDate   time.Time             `json:"commencement_date"`
	AnnualDiscountRate decimal.Decimal       `json:"annual_discount_rate"`
	Payments           []ifrs16.LeasePayment `json:"payments"`
	InitialDirectCosts types.Money           `json:"initial_direct_costs"`
	Prepayments        types.Money           `json:"prepayments"`
	LeaseIncentives    types.Money           `json:"lease_incentives"`
	UsefulLifePeriods  int                   `json:"useful_life_periods"`
}

type AutomationDataSource interface {
	RevenueContractStore
	ListOpenLinesForCompany(context.Context, uuid.UUID, string) ([]subledgers.OpenLine, error)
	GetAccountBalanceAsOf(context.Context, uuid.UUID, uuid.UUID, time.Time) (types.Money, error)
}

var _ AutomationDataSource = (*postgres.Driver)(nil)

func BuildStandardJobs(cfg AutomationConfig, db AutomationDataSource) ([]Job, error) {
	if db == nil || len(cfg.Companies) == 0 {
		return nil, ErrInvalidAutomationConfig
	}
	jobs := make([]Job, 0, len(cfg.Companies)*3)
	seenCompanies := make(map[uuid.UUID]struct{}, len(cfg.Companies))
	for _, company := range cfg.Companies {
		if company.CompanyID == uuid.Nil || company.JournalID == uuid.Nil ||
			strings.TrimSpace(company.CreatedBy) == "" {
			return nil, fmt.Errorf("%w: company, journal, and created_by are required", ErrInvalidAutomationConfig)
		}
		if _, exists := seenCompanies[company.CompanyID]; exists {
			return nil, fmt.Errorf("%w: duplicate company %s", ErrInvalidAutomationConfig, company.CompanyID)
		}
		seenCompanies[company.CompanyID] = struct{}{}
		if err := validateReceivableFlags(company.IFRS9); err != nil {
			return nil, fmt.Errorf("%w: company %s IFRS 9: %w",
				ErrInvalidAutomationConfig, company.CompanyID, err)
		}

		portfolio := receivablePortfolio{source: db, sicrIDs: uuidSet(company.IFRS9.SICRReceivableIDs),
			defaultedIDs: uuidSet(company.IFRS9.DefaultedReceivableIDs)}
		allowance := accountAllowance{source: db}
		eclConfig := ifrs9.Config{
			CompanyID: company.CompanyID, JournalID: company.JournalID,
			ImpairmentAccountID: company.IFRS9.ImpairmentAccountID,
			AllowanceAccountID:  company.IFRS9.AllowanceAccountID,
			Currency:            company.IFRS9.Currency,
			ProvisionMatrix:     company.IFRS9.ProvisionMatrix,
		}
		eclEngine, err := ifrs9.NewEngine(eclConfig, portfolio, allowance)
		if err != nil {
			return nil, fmt.Errorf("%w: company %s IFRS 9: %w",
				ErrInvalidAutomationConfig, company.CompanyID, err)
		}
		jobs = append(jobs, ECLJob{
			ID: "ifrs9-ecl:" + company.CompanyID.String(), Engine: eclEngine,
		})

		if company.IFRS15.RevenueAccountID == uuid.Nil ||
			company.IFRS15.ContractLiabilityID == uuid.Nil ||
			company.IFRS15.ContractAssetID == uuid.Nil {
			return nil, fmt.Errorf("%w: company %s IFRS 15 accounts are required", ErrInvalidAutomationConfig, company.CompanyID)
		}
		jobs = append(jobs, &RevenueJob{
			ID: "ifrs15-revenue:" + company.CompanyID.String(), Store: db,
			CompanyID: company.CompanyID, JournalID: company.JournalID, CreatedBy: company.CreatedBy,
			RevenueAccountID:    company.IFRS15.RevenueAccountID,
			ContractLiabilityID: company.IFRS15.ContractLiabilityID,
			ContractAssetID:     company.IFRS15.ContractAssetID,
		})

		leases := make([]LeaseSchedule, 0, len(company.IFRS16.Leases))
		seenLeases := make(map[uuid.UUID]struct{}, len(company.IFRS16.Leases))
		for _, lease := range company.IFRS16.Leases {
			if lease.ID == uuid.Nil {
				return nil, fmt.Errorf("%w: company %s has a lease without an id", ErrInvalidAutomationConfig, company.CompanyID)
			}
			if _, exists := seenLeases[lease.ID]; exists {
				return nil, fmt.Errorf("%w: duplicate lease %s", ErrInvalidAutomationConfig, lease.ID)
			}
			seenLeases[lease.ID] = struct{}{}
			contract, err := ifrs16.NewLeaseContract(
				lease.Contract.CommencementDate, lease.Contract.AnnualDiscountRate,
				lease.Contract.Payments, lease.Contract.InitialDirectCosts,
				lease.Contract.Prepayments, lease.Contract.LeaseIncentives,
				lease.Contract.UsefulLifePeriods,
			)
			if err != nil {
				return nil, fmt.Errorf("%w: company %s lease %s: %w",
					ErrInvalidAutomationConfig, company.CompanyID, lease.ID, err)
			}
			if err := validateLeaseAccounts(lease.Accounts); err != nil {
				return nil, fmt.Errorf("%w: company %s lease %s accounts: %w",
					ErrInvalidAutomationConfig, company.CompanyID, lease.ID, err)
			}
			leases = append(leases, LeaseSchedule{
				ID: lease.ID, Contract: contract, Accounts: lease.Accounts,
				CompanyID: company.CompanyID, JournalID: company.JournalID, CreatedBy: company.CreatedBy,
			})
		}
		jobs = append(jobs, LeaseJob{
			ID: "ifrs16-leases:" + company.CompanyID.String(), Leases: leases,
		})
	}
	return jobs, nil
}

func validateReceivableFlags(config IFRS9AutomationConfig) error {
	seen := make(map[uuid.UUID]struct{}, len(config.SICRReceivableIDs)+len(config.DefaultedReceivableIDs))
	for _, id := range append(append([]uuid.UUID(nil), config.SICRReceivableIDs...), config.DefaultedReceivableIDs...) {
		if id == uuid.Nil {
			return ifrs9.ErrInvalidConfiguration
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validateLeaseAccounts(accounts ifrs16.LeaseAccounts) error {
	if accounts.ROUAsset == uuid.Nil || accounts.LeaseLiability == uuid.Nil ||
		accounts.Cash == uuid.Nil || accounts.InterestExpense == uuid.Nil ||
		accounts.DepreciationExpense == uuid.Nil || accounts.AccumulatedDepreciation == uuid.Nil {
		return ErrJobConfiguration
	}
	return nil
}

type receivablePortfolio struct {
	source       AutomationDataSource
	sicrIDs      map[uuid.UUID]struct{}
	defaultedIDs map[uuid.UUID]struct{}
}

func (p receivablePortfolio) ListReceivables(ctx context.Context, companyID uuid.UUID) ([]ifrs9.Exposure, error) {
	lines, err := p.source.ListOpenLinesForCompany(ctx, companyID, "")
	if err != nil {
		return nil, err
	}
	result := make([]ifrs9.Exposure, 0, len(lines))
	for _, line := range lines {
		if line.Kind != subledgers.LineReceivable {
			continue
		}
		_, sicr := p.sicrIDs[line.ID]
		_, defaulted := p.defaultedIDs[line.ID]
		result = append(result, ifrs9.Exposure{Receivable: line, SICR: sicr, Defaulted: defaulted})
	}
	return result, nil
}

type accountAllowance struct {
	source AutomationDataSource
}

func (a accountAllowance) ExistingAllowance(
	ctx context.Context, companyID, accountID uuid.UUID, currency string,
) (types.Money, error) {
	return a.ExistingAllowanceAsOf(ctx, companyID, accountID, currency, time.Now().UTC())
}

func (a accountAllowance) ExistingAllowanceAsOf(
	ctx context.Context, companyID, accountID uuid.UUID, currency string, asOf time.Time,
) (types.Money, error) {
	balance, err := a.source.GetAccountBalanceAsOf(ctx, companyID, accountID, asOf)
	if err != nil {
		return types.Money{}, err
	}
	if balance.Currency() != currency {
		return types.Money{}, ifrs9.ErrInvalidAllowance
	}
	return balance, nil
}

func uuidSet(values []uuid.UUID) map[uuid.UUID]struct{} {
	result := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value != uuid.Nil {
			result[value] = struct{}{}
		}
	}
	return result
}
