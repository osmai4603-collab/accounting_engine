package subledgers

import (
	"accounting_engine/internal/masterdata"
	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"context"
	"testing"
	"time"

	"accounting_engine/internal/governance"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type stubGLReader struct {
	balances map[string]financialtypes.Money
}

func (s *stubGLReader) GetAccountBalance(_ context.Context, companyID, accountID uuid.UUID) (financialtypes.Money, error) {
	balance, ok := s.balances[companyID.String()+"|"+accountID.String()]
	if !ok {
		return financialtypes.Money{}, ErrNotFound
	}
	return balance, nil
}

func TestControlAccountBalanceFromEntriesUsesNaturalSide(t *testing.T) {
	companyID, accountID, journalID := uuid.New(), uuid.New(), uuid.New()
	zero := money(t, "0.00")
	debit := posting.JournalEntry{
		CompanyID: companyID, ID: uuid.New(), JournalID: journalID, Status: posting.EntryPosted,
		Lines: []posting.JournalEntryLine{{CompanyID: companyID, AccountID: accountID, Debit: money(t, "75"), Credit: zero}},
	}
	account := masterdata.Account{CompanyID: companyID, ID: accountID, NormalBalance: masterdata.NormalBalanceDebit}
	balance, err := ControlAccountBalanceFromEntries(account, []posting.JournalEntry{debit}, "USD")
	if err != nil || !balance.Equal(money(t, "75")) {
		t.Fatalf("debit-normal balance = %s, err=%v", balance, err)
	}
	account.NormalBalance = masterdata.NormalBalanceCredit
	credit := posting.JournalEntry{
		CompanyID: companyID, ID: uuid.New(), JournalID: journalID, Status: posting.EntryPosted,
		Lines: []posting.JournalEntryLine{{CompanyID: companyID, AccountID: accountID, Debit: zero, Credit: money(t, "25")}},
	}
	balance, err = ControlAccountBalanceFromEntries(account, []posting.JournalEntry{credit}, "USD")
	if err != nil || !balance.Equal(money(t, "25")) {
		t.Fatalf("credit-normal balance = %s, err=%v", balance, err)
	}
}

func TestLedgerReconciliationEngineDetectsMismatch(t *testing.T) {
	repo := NewMemoryRepository()
	companyID := uuid.New()
	controlID := uuid.New()
	partner := uuid.New()
	ledger := NewReceivableLedgerForCompany(repo, companyID)
	if _, err := ledger.AddInvoiceWithControl(partner, controlID, uuid.New(), money(t, "120.00"), time.Now(), time.Now().Add(24*time.Hour), "INV-1"); err != nil {
		t.Fatal(err)
	}
	engine := NewLedgerReconciliationEngine(repo, repo, &stubGLReader{balances: map[string]financialtypes.Money{
		companyID.String() + "|" + controlID.String(): money(t, "100.00"),
	}})
	discrepancy, err := engine.ReconcileARControl(context.Background(), companyID, controlID)
	if err == nil || discrepancy == nil {
		t.Fatal("expected mismatch to be reported")
	}
	if !discrepancy.Difference.Equal(decimal.NewFromInt(20)) {
		t.Fatalf("unexpected difference: got %s want 20", discrepancy.Difference)
	}
}

func TestLedgerReconciliationEngineReconcilesMixedCurrenciesInFunctionalCurrency(t *testing.T) {
	repo := NewMemoryRepository()
	companyID, controlID, partner := uuid.New(), uuid.New(), uuid.New()
	for _, line := range []OpenLine{
		{
			CompanyID: companyID, ID: uuid.New(), PartnerID: &partner, ControlAccountID: controlID,
			Kind: LinePayable, OriginalAmount: mustMoney(t, "10.00", "USD"),
			ResidualAmount: mustMoney(t, "10.00", "USD"),
			FunctionalOriginalAmount: money(t, "10.00"), FunctionalResidualAmount: money(t, "10.00"),
			DocumentDate: time.Now(),
		},
		{
			CompanyID: companyID, ID: uuid.New(), PartnerID: &partner, ControlAccountID: controlID,
			Kind: LinePayable, OriginalAmount: mustMoney(t, "10.00", "EUR"),
			ResidualAmount: mustMoney(t, "10.00", "EUR"),
			FunctionalOriginalAmount: money(t, "10.00"), FunctionalResidualAmount: money(t, "10.00"),
			DocumentDate: time.Now(),
		},
	} {
		if err := repo.SaveOpenLine(context.Background(), line); err != nil {
			t.Fatal(err)
		}
	}
	engine := NewLedgerReconciliationEngine(repo, repo, &stubGLReader{balances: map[string]financialtypes.Money{
		companyID.String() + "|" + controlID.String(): money(t, "20.00"),
	}})
	discrepancy, err := engine.ReconcileAPControl(context.Background(), companyID, controlID)
	if err != nil || discrepancy != nil {
		t.Fatalf("ReconcileAPControl() = (%+v, %v), want a balanced multi-currency reconciliation", discrepancy, err)
	}
}

func TestLedgerReconciliationEngineUsesGLCurrencyForEmptySubledger(t *testing.T) {
	repo := NewMemoryRepository()
	companyID, controlID := uuid.New(), uuid.New()
	engine := NewLedgerReconciliationEngine(repo, repo, &stubGLReader{balances: map[string]financialtypes.Money{
		companyID.String() + "|" + controlID.String(): money(t, "0.00"),
	}})
	discrepancy, err := engine.ReconcileARControl(context.Background(), companyID, controlID)
	if err != nil || discrepancy != nil {
		t.Fatalf("empty balanced control account: discrepancy=%+v err=%v", discrepancy, err)
	}
}

func TestAddInvoiceWithControlRequiresAccount(t *testing.T) {
	repo := NewMemoryRepository()
	ledger := NewReceivableLedgerForCompany(repo, uuid.New())
	_, err := ledger.AddInvoiceWithControl(uuid.New(), uuid.Nil, uuid.New(), money(t, "50.00"), time.Now(), time.Now().Add(24*time.Hour), "INV-2")
	if err != ErrInvalidOpenLine {
		t.Fatalf("AddInvoiceWithControl() error = %v, want %v", err, ErrInvalidOpenLine)
	}
}

func TestReceivableWriteOffRequiresPostedEntryAndSupportsPartialSettlement(t *testing.T) {
	repo := NewMemoryRepository()
	companyID, partner, controlID := uuid.New(), uuid.New(), uuid.New()
	line := OpenLine{
		CompanyID: companyID, ID: uuid.New(), PartnerID: &partner, ControlAccountID: controlID,
		JournalEntryID: uuid.New(), Kind: LineReceivable,
		OriginalAmount: money(t, "100"), ResidualAmount: money(t, "100"),
	}
	if err := repo.SaveOpenLine(context.Background(), line); err != nil {
		t.Fatal(err)
	}
	ledger := NewReceivableLedgerForCompany(repo, companyID)
	params := WriteOffParams{JournalID: uuid.New(), AdjustmentAccountID: uuid.New(), Date: time.Now()}
	draft, err := ledger.PrepareWriteOff(line.ID, money(t, "40"), params)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := repo.GetOpenLine(context.Background(), uuid.Nil, line.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.ResidualAmount.Equal(money(t, "100")) || unchanged.PendingWriteOffID != draft.ID {
		t.Fatalf("write-off draft changed residual amount or was not pending: %+v", unchanged)
	}
	if _, err := ledger.PrepareWriteOff(line.ID, money(t, "20"), params); err != ErrWriteOffPending {
		t.Fatalf("second pending write-off error = %v, want %v", err, ErrWriteOffPending)
	}
	if err := ledger.ConfirmWriteOff(line.ID, draft.ID); err != posting.ErrEntryNotPosted {
		t.Fatalf("ConfirmWriteOff() error = %v, want posted-entry error", err)
	}
	repo.mu.Lock()
	posted := repo.drafts[draft.ID]
	posted.Status = posting.EntryPosted
	repo.drafts[draft.ID] = posted
	repo.mu.Unlock()
	if err := ledger.ConfirmWriteOff(line.ID, draft.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.GetOpenLine(context.Background(), uuid.Nil, line.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.ResidualAmount.Equal(money(t, "60")) || !updated.WrittenOffAmount.Equal(money(t, "40")) ||
		updated.Closed || updated.PendingWriteOffID != uuid.Nil {
		t.Fatalf("partial write-off was not applied: %+v", updated)
	}
	fullDraft, err := ledger.PrepareWriteOff(line.ID, money(t, "60"), params)
	if err != nil {
		t.Fatal(err)
	}
	repo.mu.Lock()
	posted = repo.drafts[fullDraft.ID]
	posted.Status = posting.EntryPosted
	repo.drafts[fullDraft.ID] = posted
	repo.mu.Unlock()
	if err := ledger.ConfirmWriteOff(line.ID, fullDraft.ID); err != nil {
		t.Fatal(err)
	}
	closed, err := repo.GetOpenLine(context.Background(), uuid.Nil, line.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !closed.ResidualAmount.Amount().IsZero() || !closed.WrittenOffAmount.Equal(money(t, "100")) ||
		!closed.Closed || closed.PendingWriteOffID != uuid.Nil {
		t.Fatalf("full write-off did not close the open line: %+v", closed)
	}
}

func mustMoney(t *testing.T, amount, currency string) financialtypes.Money {
	t.Helper()
	value, err := financialtypes.NewMoneyFromString(amount, currency)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestReconcileControlAsOfReconstructsLaterSettlement(t *testing.T) {
	companyID, accountID, partnerID := uuid.New(), uuid.New(), uuid.New()
	asOf := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	line := OpenLine{
		CompanyID: companyID, ID: uuid.New(), PartnerID: &partnerID,
		ControlAccountID: accountID, Kind: LineReceivable,
		OriginalAmount: money(t, "100"), ResidualAmount: money(t, "50"),
		FunctionalOriginalAmount: money(t, "100"), FunctionalResidualAmount: money(t, "50"),
		PostingDate: asOf.Add(-time.Hour), DocumentDate: asOf.Add(-time.Hour),
		Matches: []OpenLineMatch{{
			MatchingNumber: "MATCH-1", CounterpartID: uuid.New(), Amount: money(t, "50"),
			FunctionalAmount: money(t, "50"), MatchedAt: asOf.Add(24 * time.Hour),
		}},
	}
	repo := NewMemoryRepository()
	if err := repo.SaveOpenLine(context.Background(), line); err != nil {
		t.Fatal(err)
	}
	gl := newFakeReconciliationStore()
	gl.balances[accountID] = money(t, "100")
	engine := NewLedgerReconciliationEngine(repo, repo, gl)

	report, err := engine.ReconcileARControlAsOf(context.Background(), companyID, accountID, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if report.HasVariance() || !report.SubledgerBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("as-of report = %+v, want balanced 100", report)
	}
}

func TestReconcileFixedAssetControlMatchesCostAndDepreciation(t *testing.T) {
	companyID, costAccount, depreciationAccount := uuid.New(), uuid.New(), uuid.New()
	asOf := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	gl := newFakeReconciliationStore()
	gl.assets = []FixedAsset{{
		CompanyID: companyID, ID: uuid.New(), AssetAccountID: costAccount,
		AccumulatedAccountID: depreciationAccount,
		HistoricalCost: money(t, "100"), AccumulatedDepreciation: money(t, "20"),
		InServiceDate: asOf.Add(-time.Hour), LastDepreciationDate: asOf.Add(-time.Hour),
	}}
	gl.balances[costAccount] = money(t, "100")
	gl.balances[depreciationAccount] = money(t, "20")
	engine := NewLedgerReconciliationEngine(NewMemoryRepository(), NewMemoryRepository(), gl)

	reports, err := engine.ReconcileFixedAssetsControl(context.Background(), companyID, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 || reports[0].HasVariance() || reports[1].HasVariance() {
		t.Fatalf("fixed-asset reconciliation = %+v, want two balanced reports", reports)
	}
}

func TestReconcileTreasuryControlIncludesUnmatchedLedgerItems(t *testing.T) {
	companyID, accountID := uuid.New(), uuid.New()
	asOf := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	gl := newFakeReconciliationStore()
	gl.balances[accountID] = money(t, "110")
	gl.bankItems = []BankItem{
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountID, Date: asOf, Amount: money(t, "100")},
		{CompanyID: companyID, ID: uuid.New(), AccountID: accountID, JournalEntryID: uuid.New(),
			Date: asOf, Amount: money(t, "10"), Status: BankItemPending},
	}
	engine := NewLedgerReconciliationEngine(NewMemoryRepository(), NewMemoryRepository(), gl)

	report, err := engine.ReconcileTreasuryControl(context.Background(), companyID, accountID, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if report.HasVariance() || !report.SubledgerBalance.Equal(decimal.NewFromInt(110)) {
		t.Fatalf("treasury report = %+v, want balanced 110", report)
	}
}

func TestReconciliationDaemonPersistsCertificateAuditsAndNotifies(t *testing.T) {
	companyID, accountID, partnerID := uuid.New(), uuid.New(), uuid.New()
	repo := NewMemoryRepository()
	line := OpenLine{
		CompanyID: companyID, ID: uuid.New(), PartnerID: &partnerID, ControlAccountID: accountID,
		Kind: LineReceivable, OriginalAmount: money(t, "100"), ResidualAmount: money(t, "100"),
		FunctionalOriginalAmount: money(t, "100"), FunctionalResidualAmount: money(t, "100"),
		PostingDate: time.Now().UTC(), DocumentDate: time.Now().UTC(),
	}
	if err := repo.SaveOpenLine(context.Background(), line); err != nil {
		t.Fatal(err)
	}
	gl := newFakeReconciliationStore()
	gl.accounts = []masterdata.Account{{
		CompanyID: companyID, ID: accountID, AccountType: masterdata.AccountTypeAsset,
		IsControlAccount: true, IsReconcilable: true,
		SubClassification: masterdata.SubClassTradeReceivables,
	}}
	gl.balances[accountID] = money(t, "0")
	notifier := &recordingNotifier{}
	engine := NewLedgerReconciliationEngine(repo, repo, gl)
	daemon := NewReconciliationDaemon(engine, notifier, time.Hour)

	certificate, err := daemon.RunDailyCheckCertificate(context.Background(), companyID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if certificate.Clean || len(certificate.Reports) != 1 || len(gl.reports) != 1 ||
		len(gl.certificates) != 1 || len(gl.auditLogs) != 2 || len(notifier.reports) != 1 {
		t.Fatalf("certificate=%+v reports=%d certs=%d audits=%d alerts=%d",
			certificate, len(gl.reports), len(gl.certificates), len(gl.auditLogs), len(notifier.reports))
	}
}

type fakeReconciliationStore struct {
	balances    map[uuid.UUID]financialtypes.Money
	accounts    []masterdata.Account
	assets      []FixedAsset
	bankItems   []BankItem
	reports     []DiscrepancyReport
	certificates []ReconciliationCertificate
	auditLogs   []governance.FinancialAuditLog
}

func newFakeReconciliationStore() *fakeReconciliationStore {
	return &fakeReconciliationStore{balances: make(map[uuid.UUID]financialtypes.Money)}
}

func (s *fakeReconciliationStore) GetAccountBalance(_ context.Context, _, accountID uuid.UUID) (financialtypes.Money, error) {
	balance, ok := s.balances[accountID]
	if !ok {
		return financialtypes.Money{}, ErrNotFound
	}
	return balance, nil
}

func (s *fakeReconciliationStore) GetAccountBalanceAsOf(ctx context.Context, companyID, accountID uuid.UUID, _ time.Time) (financialtypes.Money, error) {
	return s.GetAccountBalance(ctx, companyID, accountID)
}

func (s *fakeReconciliationStore) ListAccountsForCompanyContext(ctx context.Context, _ uuid.UUID) ([]masterdata.Account, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]masterdata.Account(nil), s.accounts...), nil
}

func (s *fakeReconciliationStore) ListFixedAssetsForCompanyContext(ctx context.Context, _ uuid.UUID) ([]FixedAsset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]FixedAsset(nil), s.assets...), nil
}

func (s *fakeReconciliationStore) ListBankItemsForCompany(ctx context.Context, companyID uuid.UUID, accountID string) ([]BankItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := make([]BankItem, 0)
	for _, item := range s.bankItems {
		if item.CompanyID == companyID && item.AccountID.String() == accountID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (s *fakeReconciliationStore) SaveReconciliationReport(ctx context.Context, report DiscrepancyReport) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.reports = append(s.reports, report)
	return nil
}

func (s *fakeReconciliationStore) SaveReconciliationCertificate(ctx context.Context, certificate ReconciliationCertificate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.certificates = append(s.certificates, certificate)
	return nil
}

func (s *fakeReconciliationStore) AppendAuditLog(ctx context.Context, log governance.FinancialAuditLog) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	log.ID = int64(len(s.auditLogs) + 1)
	s.auditLogs = append(s.auditLogs, log)
	return nil
}

func (s *fakeReconciliationStore) GetLastAuditLog(ctx context.Context) (*governance.FinancialAuditLog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(s.auditLogs) == 0 {
		return nil, nil
	}
	last := s.auditLogs[len(s.auditLogs)-1]
	return &last, nil
}

func (s *fakeReconciliationStore) IterateAuditLogs(ctx context.Context, fromID int64, handler governance.AuditLogHandler) error {
	for _, log := range s.auditLogs {
		if log.ID < fromID {
			continue
		}
		if err := handler(log); err != nil {
			return err
		}
	}
	return ctx.Err()
}

type recordingNotifier struct {
	reports []DiscrepancyReport
	err     error
}

func (n *recordingNotifier) SendDiscrepancyAlert(_ context.Context, report DiscrepancyReport) error {
	n.reports = append(n.reports, report)
	return n.err
}
