package ifrsautomation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"accounting_engine/internal/ifrs15"
	"accounting_engine/internal/ifrs16"
	"accounting_engine/internal/ifrs9"
	"accounting_engine/internal/posting"
	"accounting_engine/internal/subledgers"
	"accounting_engine/storage/postgres"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var ErrInvalidAutomationConfig = errors.New("invalid IFRS automation configuration")

type AutomationConfig struct {
	Companies []CompanyAutomationConfig `json:"companies"`
}

type CompanyAutomationConfig struct {
	CompanyID uuid.UUID             `json:"company_id"`
	JournalID uuid.UUID             `json:"journal_id"`
	CreatedBy string                `json:"created_by"`
	IFRS9     IFRS9AutomationConfig `json:"ifrs9"`
	IFRS15    IFRS15AutomationConfig `json:"ifrs15"`
	IFRS16    IFRS16AutomationConfig `json:"ifrs16"`
}

type IFRS9AutomationConfig struct {
	ImpairmentAccountID uuid.UUID                       `json:"impairment_account_id"`
	AllowanceAccountID  uuid.UUID                       `json:"allowance_account_id"`
	Currency            string                          `json:"currency"`
	ProvisionMatrix     map[subledgers.AgingBucket]decimal.Decimal `json:"provision_matrix"`
	SICRReceivableIDs   []uuid.UUID                     `json:"sicr_receivable_ids,omitempty"`
	DefaultedReceivableIDs []uuid.UUID                  `json:"defaulted_receivable_ids,omitempty"`
}

type IFRS15AutomationConfig struct {
	RevenueAccountID       uuid.UUID `json:"revenue_account_id"`
	ContractLiabilityID    uuid.UUID `json:"contract_liability_id"`
	ContractAssetID        uuid.UUID `json:"contract_asset_id"`
}

type IFRS16AutomationConfig struct {
	Leases []LeaseAutomationConfig `json:"leases"`
}

type LeaseAutomationConfig struct {
	ID       uuid.UUID             `json:"id"`
	Contract ifrs16.LeaseContract  `json:"contract"`
	Accounts ifrs16.LeaseAccounts  `json:"accounts"`
}

type AutomationDataSource interface {
	ifrsautomationRevenueStore
	ListOpenLinesForCompany(context.Context, uuid.UUID, string) ([]subledgers.OpenLine, error)
	GetAccountBalanceAsOf(context.Context, uuid.UUID, uuid.UUID, time.Time) (types.Money, error)
}

type ifrsautomationRevenueStore interface {
	ListContracts(context.Context, uuid.UUID) ([]ifrs15.Contract, error)
	SaveContract(context.Context, uuid.UUID, ifrs15.Contract) error
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
			return nil, fmt.Errorf("%w: company %s IFRS 9: %v", ErrInvalidAutomationConfig, company.CompanyID, err)
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
			RevenueAccountID: company.IFRS15.RevenueAccountID,
			ContractLiabilityID: company.IFRS15.ContractLiabilityID,
			ContractAssetID: company.IFRS15.ContractAssetID,
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
			leases = append(leases, LeaseSchedule{
				ID: lease.ID, Contract: lease.Contract, Accounts: lease.Accounts,
				CompanyID: company.CompanyID, JournalID: company.JournalID, CreatedBy: company.CreatedBy,
			})
		}
		jobs = append(jobs, LeaseJob{
			ID: "ifrs16-leases:" + company.CompanyID.String(), Leases: leases,
		})
	}
	return jobs, nil
}

type receivablePortfolio struct {
	source      AutomationDataSource
	sicrIDs     map[uuid.UUID]struct{}
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
	balance, err := a.source.GetAccountBalanceAsOf(ctx, companyID, accountID, time.Now().UTC())
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

var _ posting.JournalEntry
