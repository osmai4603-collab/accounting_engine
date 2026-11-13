package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"accounting_engine/internal/governance"
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"accounting_engine/pkg/api/v1/ledger"
	"accounting_engine/pkg/api/v1/reporting"
	"accounting_engine/pkg/api/v1/types"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	_ accountingEngineStorageCore      = (*Driver)(nil)
	_ accountingEngineStorageReporting = (*Driver)(nil)
	_ accountingEngineStorageGov       = (*Driver)(nil)
)

type accountingEngineStorageCore interface {
	SaveEntry(ctx context.Context, entry ledger.JournalEntry) error
	GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error)
	PostEntry(ctx context.Context, entry ledger.JournalEntry) error
	GetAccount(ctx context.Context, companyID types.CompanyID, accountID types.AccountID) (ledger.Account, error)
	GetFiscalPeriod(ctx context.Context, companyID types.CompanyID, periodID types.FiscalPeriodID) (ledger.FiscalPeriod, error)
}

type accountingEngineStorageReporting interface {
	GetTrialBalance(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.TrialBalance, error)
	GetGeneralLedger(ctx context.Context, companyID types.CompanyID, accountID types.AccountID, start, end time.Time) (reporting.GeneralLedgerReport, error)
	GetBalanceSheet(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.BalanceSheetReport, error)
	GetIncomeStatement(ctx context.Context, companyID types.CompanyID, start, end time.Time, currency string) (reporting.IncomeStatementReport, error)
}

type accountingEngineStorageGov interface {
	RecordAuditLog(ctx context.Context, eventID string, details map[string]any) error
	GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error)
}

func (d *Driver) SaveEntry(ctx context.Context, entry ledger.JournalEntry) error {
	pEntry := mapLedgerToPosting(entry)
	return d.SaveDraftContext(ctx, pEntry)
}

func (d *Driver) GetEntry(ctx context.Context, companyID types.CompanyID, entryID types.JournalEntryID) (ledger.JournalEntry, error) {
	pEntry, err := d.GetEntryByIDForCompany(ctx, uuid.UUID(companyID), uuid.UUID(entryID))
	if err != nil {
		return ledger.JournalEntry{}, err
	}
	return mapPostingToLedger(pEntry), nil
}

func (d *Driver) PostEntry(ctx context.Context, entry ledger.JournalEntry) error {
	var totalDebit, totalCredit decimal.Decimal
	for _, line := range entry.Lines {
		totalDebit = totalDebit.Add(line.FunctionalDebit.Amount())
		totalCredit = totalCredit.Add(line.FunctionalCredit.Amount())
	}
	if !totalDebit.Equal(totalCredit) {
		return posting.ErrFunctionalBalanceMismatch
	}

	pEntry := mapLedgerToPosting(entry)

	// C.3: do not fabricate master data. Require referenced entities to exist.
	d.mu.RLock()
	_, companyOK := d.companies[pEntry.CompanyID]
	_, periodOK := d.periods[pEntry.PeriodID]
	accountsOK := true
	for _, l := range pEntry.Lines {
		acc, ok := d.accounts[l.AccountID]
		if !ok || (pEntry.CompanyID != uuid.Nil && acc.CompanyID != pEntry.CompanyID) {
			accountsOK = false
			break
		}
	}
	d.mu.RUnlock()
	if pEntry.CompanyID == uuid.Nil || !companyOK {
		return errors.New("company not found")
	}
	if pEntry.PeriodID == uuid.Nil || !periodOK {
		return types.ErrFiscalPeriodNotFound
	}
	if !accountsOK {
		return types.ErrAccountNotFound
	}

	draft := pEntry
	draft.Status = posting.EntryDraft
	if err := d.SaveDraftContext(ctx, draft); err != nil {
		return err
	}
	pEntry.Status = posting.EntryPosted
	return d.PostAtomicContext(ctx, pEntry)
}

func (d *Driver) GetAccount(ctx context.Context, companyID types.CompanyID, accountID types.AccountID) (ledger.Account, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	acc, ok := d.accounts[uuid.UUID(accountID)]
	if !ok || acc.CompanyID != uuid.UUID(companyID) {
		return ledger.Account{}, masterdata.ErrAccountNotFound
	}
	var parentID *types.AccountID
	if acc.ParentID != nil {
		pid := types.AccountID(*acc.ParentID)
		parentID = &pid
	}
	return ledger.Account{
		ID:             types.AccountID(acc.ID),
		CompanyID:      types.CompanyID(acc.CompanyID),
		Code:           acc.Code,
		Name:           acc.Name,
		Type:           ledger.AccountType(acc.AccountType),
		NormalBalance:  ledger.NormalBalance(acc.NormalBalance),
		ParentID:       parentID,
		IsReconcilable: acc.IsReconcilable,
		IsActive:       acc.IsActive,
		Currency:       acc.CurrencyID,
	}, nil
}

func (d *Driver) GetFiscalPeriod(ctx context.Context, companyID types.CompanyID, periodID types.FiscalPeriodID) (ledger.FiscalPeriod, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	p, ok := d.periods[uuid.UUID(periodID)]
	if !ok || p.CompanyID != uuid.UUID(companyID) {
		return ledger.FiscalPeriod{}, types.ErrFiscalPeriodNotFound
	}
	return ledger.FiscalPeriod{
		ID:           types.FiscalPeriodID(p.ID),
		CompanyID:    types.CompanyID(p.CompanyID),
		FiscalYearID: types.FiscalYearID(p.FiscalYearID),
		Number:       p.Number,
		Name:         p.Name,
		StartDate:    p.StartDate,
		EndDate:      p.EndDate,
		State:        ledger.PeriodState(p.State),
		Type:         ledger.FiscalPeriodType(p.Type),
	}, nil
}

func (d *Driver) resolveReportingCurrency(companyID types.CompanyID, currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency != "" {
		return currency
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if c, ok := d.companies[uuid.UUID(companyID)]; ok {
		if fc := strings.ToUpper(strings.TrimSpace(c.FunctionalCurrency)); fc != "" {
			return fc
		}
	}
	return "USD"
}

type tbLineAgg struct {
	account                  masterdata.Account
	openD, openC, perD, perC decimal.Decimal
}

func (d *Driver) computeTrialBalance(companyID uuid.UUID, start, end time.Time) (map[uuid.UUID]*tbLineAgg, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	accounts, err := d.ListAccountsForCompany(companyID)
	if err != nil {
		return nil, err
	}
	aggs := make(map[uuid.UUID]*tbLineAgg, len(accounts))
	for _, a := range accounts {
		acc := a
		aggs[a.ID] = &tbLineAgg{account: acc}
	}
	for _, e := range d.entries {
		if e.CompanyID != companyID || e.Status != posting.EntryPosted {
			continue
		}
		date := e.EntryDate
		if date.IsZero() {
			date = e.Date
		}
		inPeriod := true
		beforePeriod := false
		if !end.IsZero() && date.After(end) {
			continue
		}
		if !start.IsZero() && date.Before(start) {
			inPeriod = false
			beforePeriod = true
		}
		for _, l := range e.Lines {
			agg, ok := aggs[l.AccountID]
			if !ok {
				continue
			}
			fd := l.FunctionalDebit.Amount()
			fc := l.FunctionalCredit.Amount()
			if inPeriod {
				agg.perD = agg.perD.Add(fd)
				agg.perC = agg.perC.Add(fc)
			} else if beforePeriod {
				agg.openD = agg.openD.Add(fd)
				agg.openC = agg.openC.Add(fc)
			}
		}
	}
	return aggs, nil
}

func (d *Driver) GetTrialBalance(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.TrialBalance, error) {
	if err := ctx.Err(); err != nil {
		return reporting.TrialBalance{}, err
	}
	currency = d.resolveReportingCurrency(companyID, currency)
	aggs, err := d.computeTrialBalance(uuid.UUID(companyID), time.Time{}, asOf)
	if err != nil {
		return reporting.TrialBalance{}, err
	}
	ids := make([]uuid.UUID, 0, len(aggs))
	for id := range aggs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return aggs[ids[i]].account.Code < aggs[ids[j]].account.Code })
	lines := make([]reporting.TrialBalanceLine, 0, len(ids))
	var totD, totC decimal.Decimal
	for _, id := range ids {
		a := aggs[id]
		endD := a.openD.Add(a.perD)
		endC := a.openC.Add(a.perC)
		net := endD.Sub(endC)
		var eD, eC decimal.Decimal
		if net.IsPositive() {
			eD = net
		} else if net.IsNegative() {
			eC = net.Abs()
		}
		totD = totD.Add(eD)
		totC = totC.Add(eC)
		lines = append(lines, reporting.TrialBalanceLine{
			AccountID: types.AccountID(a.account.ID), AccountCode: a.account.Code, AccountName: a.account.Name,
			AccountType:  string(a.account.AccountType),
			OpeningDebit: mustMoney(a.openD, currency), OpeningCredit: mustMoney(a.openC, currency),
			PeriodDebit: mustMoney(a.perD, currency), PeriodCredit: mustMoney(a.perC, currency),
			EndingDebit: mustMoney(eD, currency), EndingCredit: mustMoney(eC, currency),
		})
	}
	return reporting.TrialBalance{CompanyID: companyID, AsOfDate: asOf, Currency: currency, Lines: lines,
		TotalDebit: mustMoney(totD, currency), TotalCredit: mustMoney(totC, currency), IsBalanced: totD.Equal(totC)}, nil
}

func (d *Driver) GetGeneralLedger(ctx context.Context, companyID types.CompanyID, accountID types.AccountID, start, end time.Time) (reporting.GeneralLedgerReport, error) {
	if err := ctx.Err(); err != nil {
		return reporting.GeneralLedgerReport{}, err
	}
	currency := d.resolveReportingCurrency(companyID, "")
	d.mu.RLock()
	defer d.mu.RUnlock()
	acc, ok := d.accounts[uuid.UUID(accountID)]
	if !ok || acc.CompanyID != uuid.UUID(companyID) {
		return reporting.GeneralLedgerReport{}, posting.ErrEntryNotFound
	}
	type row struct {
		entry posting.JournalEntry
		line  posting.JournalEntryLine
	}
	var opening, running decimal.Decimal
	var rows []row
	for _, e := range d.entries {
		if e.CompanyID != uuid.UUID(companyID) || e.Status != posting.EntryPosted {
			continue
		}
		date := e.EntryDate
		if date.IsZero() {
			date = e.Date
		}
		if !end.IsZero() && date.After(end) {
			continue
		}
		for _, l := range e.Lines {
			if l.AccountID != uuid.UUID(accountID) {
				continue
			}
			net := l.FunctionalDebit.Amount().Sub(l.FunctionalCredit.Amount())
			if !start.IsZero() && date.Before(start) {
				opening = opening.Add(net)
				continue
			}
			rows = append(rows, row{entry: e, line: l})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		di := rows[i].entry.EntryDate
		if di.IsZero() {
			di = rows[i].entry.Date
		}
		dj := rows[j].entry.EntryDate
		if dj.IsZero() {
			dj = rows[j].entry.Date
		}
		if di.Equal(dj) {
			return rows[i].entry.ID.String() < rows[j].entry.ID.String()
		}
		return di.Before(dj)
	})
	running = opening
	entries := make([]reporting.GeneralLedgerEntry, 0, len(rows))
	for _, r := range rows {
		net := r.line.FunctionalDebit.Amount().Sub(r.line.FunctionalCredit.Amount())
		running = running.Add(net)
		date := r.entry.EntryDate
		if date.IsZero() {
			date = r.entry.Date
		}
		entries = append(entries, reporting.GeneralLedgerEntry{
			EntryID: types.JournalEntryID(r.entry.ID), EntryNumber: fmtInt64(r.entry.SequenceNumber), EffectiveDate: date,
			Description: r.entry.Reference, Debit: mustMoney(r.line.FunctionalDebit.Amount(), currency), Credit: mustMoney(r.line.FunctionalCredit.Amount(), currency),
			RunningBalance: mustMoney(running, currency),
		})
	}
	return reporting.GeneralLedgerReport{CompanyID: companyID, AccountID: accountID, AccountCode: acc.Code, AccountName: acc.Name,
		StartDate: start, EndDate: end, OpeningBalance: mustMoney(opening, currency), Entries: entries, EndingBalance: mustMoney(running, currency)}, nil
}

func (d *Driver) GetBalanceSheet(ctx context.Context, companyID types.CompanyID, asOf time.Time, currency string) (reporting.BalanceSheetReport, error) {
	tb, err := d.GetTrialBalance(ctx, companyID, asOf, currency)
	if err != nil {
		return reporting.BalanceSheetReport{}, err
	}
	currency = tb.Currency
	assets := reporting.BalanceSheetSection{Title: "Assets"}
	liabs := reporting.BalanceSheetSection{Title: "Liabilities"}
	equity := reporting.BalanceSheetSection{Title: "Equity"}
	var totalAssets, totalLiab, totalEquity decimal.Decimal
	d.mu.RLock()
	accTypes := map[uuid.UUID]masterdata.AccountType{}
	for id, a := range d.accounts {
		if a.CompanyID == uuid.UUID(companyID) {
			accTypes[id] = a.AccountType
		}
	}
	d.mu.RUnlock()
	for _, l := range tb.Lines {
		net := l.EndingDebit.Amount().Sub(l.EndingCredit.Amount())
		switch accTypes[uuid.UUID(l.AccountID)] {
		case masterdata.AccountTypeAsset:
			if net.IsZero() {
				continue
			}
			assets.Lines = append(assets.Lines, reporting.BalanceSheetLine{AccountID: l.AccountID, AccountCode: l.AccountCode, AccountName: l.AccountName, Amount: mustMoney(net, currency)})
			totalAssets = totalAssets.Add(net)
		case masterdata.AccountTypeLiability:
			amt := net.Neg()
			if amt.IsZero() {
				continue
			}
			liabs.Lines = append(liabs.Lines, reporting.BalanceSheetLine{AccountID: l.AccountID, AccountCode: l.AccountCode, AccountName: l.AccountName, Amount: mustMoney(amt, currency)})
			totalLiab = totalLiab.Add(amt)
		case masterdata.AccountTypeEquity:
			amt := net.Neg()
			if amt.IsZero() {
				continue
			}
			equity.Lines = append(equity.Lines, reporting.BalanceSheetLine{AccountID: l.AccountID, AccountCode: l.AccountCode, AccountName: l.AccountName, Amount: mustMoney(amt, currency)})
			totalEquity = totalEquity.Add(amt)
		}
	}
	is, err := d.GetIncomeStatement(ctx, companyID, time.Time{}, asOf, currency)
	if err != nil {
		return reporting.BalanceSheetReport{}, err
	}
	if !is.NetIncome.Amount().IsZero() {
		equity.Lines = append(equity.Lines, reporting.BalanceSheetLine{AccountName: "Current Period Net Income", Amount: is.NetIncome})
		totalEquity = totalEquity.Add(is.NetIncome.Amount())
	}
	assets.TotalAmount = mustMoney(totalAssets, currency)
	liabs.TotalAmount = mustMoney(totalLiab, currency)
	equity.TotalAmount = mustMoney(totalEquity, currency)
	totalLE := totalLiab.Add(totalEquity)
	return reporting.BalanceSheetReport{CompanyID: companyID, AsOfDate: asOf, Currency: currency, Assets: assets, Liabilities: liabs, Equity: equity,
		TotalAssets: mustMoney(totalAssets, currency), TotalLiabEquity: mustMoney(totalLE, currency), IsBalanced: totalAssets.Equal(totalLE)}, nil
}

func (d *Driver) GetIncomeStatement(ctx context.Context, companyID types.CompanyID, start, end time.Time, currency string) (reporting.IncomeStatementReport, error) {
	tb, err := d.GetTrialBalanceForPeriod(ctx, companyID, start, end, currency)
	if err != nil {
		return reporting.IncomeStatementReport{}, err
	}
	currency = tb.Currency
	revenues := reporting.IncomeStatementSection{Title: "Revenues"}
	expenses := reporting.IncomeStatementSection{Title: "Expenses"}
	var totalRev, totalExp decimal.Decimal
	d.mu.RLock()
	accTypes := map[uuid.UUID]masterdata.AccountType{}
	for id, a := range d.accounts {
		if a.CompanyID == uuid.UUID(companyID) {
			accTypes[id] = a.AccountType
		}
	}
	d.mu.RUnlock()
	for _, l := range tb.Lines {
		net := l.EndingDebit.Amount().Sub(l.EndingCredit.Amount())
		switch accTypes[uuid.UUID(l.AccountID)] {
		case masterdata.AccountTypeIncome:
			amt := net.Neg()
			if amt.IsZero() {
				continue
			}
			revenues.Lines = append(revenues.Lines, reporting.IncomeStatementLine{AccountID: l.AccountID, AccountCode: l.AccountCode, AccountName: l.AccountName, Amount: mustMoney(amt, currency)})
			totalRev = totalRev.Add(amt)
		case masterdata.AccountTypeExpense:
			amt := net
			if amt.IsZero() {
				continue
			}
			expenses.Lines = append(expenses.Lines, reporting.IncomeStatementLine{AccountID: l.AccountID, AccountCode: l.AccountCode, AccountName: l.AccountName, Amount: mustMoney(amt, currency)})
			totalExp = totalExp.Add(amt)
		}
	}
	revenues.TotalAmount = mustMoney(totalRev, currency)
	expenses.TotalAmount = mustMoney(totalExp, currency)
	return reporting.IncomeStatementReport{CompanyID: companyID, StartDate: start, EndDate: end, Currency: currency,
		Revenues: revenues, Expenses: expenses, NetIncome: mustMoney(totalRev.Sub(totalExp), currency)}, nil
}

func (d *Driver) GetTrialBalanceForPeriod(ctx context.Context, companyID types.CompanyID, start, end time.Time, currency string) (reporting.TrialBalance, error) {
	if err := ctx.Err(); err != nil {
		return reporting.TrialBalance{}, err
	}
	currency = d.resolveReportingCurrency(companyID, currency)
	aggs, err := d.computeTrialBalance(uuid.UUID(companyID), start, end)
	if err != nil {
		return reporting.TrialBalance{}, err
	}
	ids := make([]uuid.UUID, 0, len(aggs))
	for id := range aggs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return aggs[ids[i]].account.Code < aggs[ids[j]].account.Code })
	lines := make([]reporting.TrialBalanceLine, 0, len(ids))
	var totD, totC decimal.Decimal
	for _, id := range ids {
		a := aggs[id]
		endD := a.openD.Add(a.perD)
		endC := a.openC.Add(a.perC)
		net := endD.Sub(endC)
		var eD, eC decimal.Decimal
		if net.IsPositive() {
			eD = net
		} else if net.IsNegative() {
			eC = net.Abs()
		}
		totD = totD.Add(eD)
		totC = totC.Add(eC)
		lines = append(lines, reporting.TrialBalanceLine{
			AccountID: types.AccountID(a.account.ID), AccountCode: a.account.Code, AccountName: a.account.Name,
			AccountType:  string(a.account.AccountType),
			OpeningDebit: mustMoney(a.openD, currency), OpeningCredit: mustMoney(a.openC, currency),
			PeriodDebit: mustMoney(a.perD, currency), PeriodCredit: mustMoney(a.perC, currency),
			EndingDebit: mustMoney(eD, currency), EndingCredit: mustMoney(eC, currency),
		})
	}
	return reporting.TrialBalance{CompanyID: companyID, AsOfDate: end, Currency: currency, Lines: lines,
		TotalDebit: mustMoney(totD, currency), TotalCredit: mustMoney(totC, currency), IsBalanced: totD.Equal(totC)}, nil
}

func mustMoney(amount decimal.Decimal, currency string) types.Money {
	m, err := types.NewMoney(amount, currency)
	if err != nil {
		m, _ = types.NewMoney(decimal.Zero, currency)
	}
	return m
}

func fmtInt64(v int64) string {
	if v == 0 {
		return ""
	}
	return fmt.Sprintf("%d", v)
}

// RecordAuditLog appends an audit record when company context is provided.
// If the SPI call cannot be mapped to a real audit record, it returns a clear error.
func (d *Driver) RecordAuditLog(ctx context.Context, eventID string, details map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if details == nil {
		return types.ErrCapabilityNotImplemented
	}
	var companyID uuid.UUID
	var actorID uuid.UUID
	var entityID uuid.UUID
	switch v := details["company_id"].(type) {
	case string:
		companyID, _ = uuid.Parse(strings.TrimSpace(v))
	case uuid.UUID:
		companyID = v
	}
	if companyID == uuid.Nil {
		return types.ErrCapabilityNotImplemented
	}
	if v, ok := details["actor_id"].(string); ok {
		actorID, _ = uuid.Parse(strings.TrimSpace(v))
	}
	if v, ok := details["entity_id"].(string); ok {
		entityID, _ = uuid.Parse(strings.TrimSpace(v))
	}
	if entityID == uuid.Nil {
		entityID = companyID
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	entityType, _ := details["entity_type"].(string)
	if strings.TrimSpace(entityType) == "" {
		entityType = "spi_event"
	}
	log := governance.FinancialAuditLog{
		CompanyID:    companyID,
		EntityType:   entityType,
		EntityID:     entityID,
		Action:       eventID,
		ActorID:      actorID,
		PayloadAfter: payload,
		Timestamp:    time.Now().UTC(),
	}
	// Maintain hash chain when appending via memory store.
	d.mu.RLock()
	if n := len(d.audit); n > 0 {
		log.PreviousLogHash = d.audit[n-1].CurrentLogHash
	} else {
		log.PreviousLogHash = governance.GenesisHash
	}
	d.mu.RUnlock()
	if log.PreviousLogHash == "" {
		log.PreviousLogHash = governance.GenesisHash
	}
	log.CurrentLogHash = governance.CalculateHash(log)
	return d.AppendAuditLog(ctx, log)
}

// GetApprovalFingerprint returns the deterministic content hash of the journal entry.
// This matches the approval/content fingerprint basis used by posting/governance.
func (d *Driver) GetApprovalFingerprint(ctx context.Context, entryID types.JournalEntryID) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	d.mu.RLock()
	entry, ok := d.entries[uuid.UUID(entryID)]
	d.mu.RUnlock()
	if !ok {
		return "", posting.ErrEntryNotFound
	}
	return posting.EntryContentHash(entry)
}

func mapLedgerToPosting(e ledger.JournalEntry) posting.JournalEntry {
	lines := make([]posting.JournalEntryLine, len(e.Lines))
	for i, l := range e.Lines {
		fdAmount := l.FunctionalDebit.Amount()
		fcAmount := l.FunctionalCredit.Amount()
		tAmount := l.TransactionAmount.Amount()

		cur := l.FunctionalDebit.Currency()
		if cur == "" {
			cur = l.FunctionalCredit.Currency()
		}
		if cur == "" {
			cur = l.TransactionCurrency
		}
		if cur == "" {
			cur = "USD"
		}
		fd, _ := financialtypes.NewMoney(fdAmount, cur)
		fc, _ := financialtypes.NewMoney(fcAmount, cur)
		ta, _ := financialtypes.NewMoney(tAmount, cur)

		lines[i] = posting.JournalEntryLine{
			ID:                  uuid.UUID(l.ID),
			AccountID:           uuid.UUID(l.AccountID),
			TransactionAmount:   ta,
			TransactionCurrency: cur,
			ExchangeRate:        l.ExchangeRate,
			FunctionalDebit:     fd,
			FunctionalCredit:    fc,
		}
	}

	var revID *uuid.UUID
	if e.ReversalOfID != nil {
		id := uuid.UUID(*e.ReversalOfID)
		revID = &id
	}

	class := posting.EntryClassRegular
	if e.Classification != "" {
		class = posting.EntryClassification(e.Classification)
	}

	status := posting.EntryPosted
	if e.Status != "" {
		status = posting.EntryStatus(e.Status)
	}

	// C.3: do not fabricate period/journal IDs from hashes when missing; require real references.
	periodID := uuid.UUID(e.FiscalPeriodID)
	journalID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("journal-"+uuid.UUID(e.CompanyID).String()))
	effective := e.EffectiveDate
	if effective.IsZero() {
		effective = e.PostingDate
	}

	return posting.JournalEntry{
		ID:                uuid.UUID(e.ID),
		CompanyID:         uuid.UUID(e.CompanyID),
		JournalID:         journalID,
		PeriodID:          periodID,
		EntryDate:         effective,
		Date:              effective,
		Status:            status,
		Classification:    class,
		CreatedBy:         e.CreatedBy,
		Reference:         e.Description,
		DocumentReference: e.ReferenceID,
		ReversedEntryID:   revID,
		Lines:             lines,
	}
}

func mapPostingToLedger(e posting.JournalEntry) ledger.JournalEntry {
	lines := make([]ledger.EntryLine, len(e.Lines))
	for i, l := range e.Lines {
		fd, _ := types.NewMoney(l.FunctionalDebit.Amount(), l.FunctionalDebit.Currency())
		fc, _ := types.NewMoney(l.FunctionalCredit.Amount(), l.FunctionalCredit.Currency())
		ta, _ := types.NewMoney(l.TransactionAmount.Amount(), l.TransactionCurrency)

		lines[i] = ledger.EntryLine{
			ID:                  types.JournalEntryLineID(l.ID),
			AccountID:           types.AccountID(l.AccountID),
			TransactionAmount:   ta,
			TransactionCurrency: l.TransactionCurrency,
			ExchangeRate:        l.ExchangeRate,
			FunctionalDebit:     fd,
			FunctionalCredit:    fc,
		}
	}

	var revID *types.JournalEntryID
	if e.OriginalEntryID != nil {
		id := types.JournalEntryID(*e.OriginalEntryID)
		revID = &id
	} else if e.ReversedEntryID != nil && e.Classification == posting.EntryClassReversing {
		id := types.JournalEntryID(*e.ReversedEntryID)
		revID = &id
	}

	return ledger.JournalEntry{
		ID:             types.JournalEntryID(e.ID),
		CompanyID:      types.CompanyID(e.CompanyID),
		FiscalPeriodID: types.FiscalPeriodID(e.PeriodID),
		Status:         ledger.EntryStatus(e.Status),
		Classification: ledger.EntryClassification(e.Classification),
		EffectiveDate:  e.EntryDate,
		PostingDate:    e.Date,
		Description:    e.Reference,
		ReferenceID:    e.DocumentReference,
		CreatedBy:      e.CreatedBy,
		ReversalOfID:   revID,
		Lines:          lines,
	}
}
