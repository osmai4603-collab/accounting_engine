package application_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
)

func TestExecuteYearEndClosingCommandCarriesPolicyAndAuditContext(t *testing.T) {
	command := application.ExecuteYearEndClosingCommand{
		Context: application.Context{
			Principal:     application.Principal{ActorID: "controller-7"},
			CorrelationID: "close-2026",
		},
		CompanyID:                 types.CompanyID(uuid.New()),
		FiscalYearID:              types.FiscalYearID(uuid.New()),
		NextFiscalYearID:          types.FiscalYearID(uuid.New()),
		JournalID:                 types.JournalID(uuid.New()),
		RetainedEarningsAccountID: types.AccountID(uuid.New()),
		IncomeSummaryAccountID:    types.AccountID(uuid.New()),
		Policy:                    application.YearEndClosingThroughIncomeSummary,
		Currency:                  "USD",
		Reason:                    "Approved year-end close",
	}
	if command.Policy != application.YearEndClosingThroughIncomeSummary ||
		command.Context.Principal.ActorID == "" || command.Reason == "" ||
		command.CompanyID == (types.CompanyID{}) || command.FiscalYearID == (types.FiscalYearID{}) {
		t.Fatalf("year-end close command is missing policy, scope, or audit intent: %+v", command)
	}
}
