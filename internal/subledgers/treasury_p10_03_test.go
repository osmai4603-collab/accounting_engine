package subledgers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	apisubledger "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/subledger"
	publictypes "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
)

type treasuryP1003DraftRecorder struct {
	entries []posting.JournalEntry
}

func (r *treasuryP1003DraftRecorder) CreateDraft(entry posting.JournalEntry) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	r.entries = append(r.entries, entry)
	return nil
}

func TestTreasuryP1003PublicStoreAndDirectTransactionPosting(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	companyID, bankID, glAccountID := uuid.New(), uuid.New(), uuid.New()
	ledger := NewTreasuryLedgerForCompany(repo, companyID)
	bank := apisubledger.BankAccount{
		AccountID: bankID.String(), CompanyID: publictypes.CompanyID(companyID),
		BankName: "Example Bank", AccountNumber: "123456", Currency: "USD",
		GLAccountID: publictypes.AccountID(glAccountID),
	}
	if err := ledger.SaveBankAccount(ctx, publictypes.CompanyID(companyID), bank); err != nil {
		t.Fatalf("SaveBankAccount(): %v", err)
	}
	storedBank, err := ledger.GetBankAccount(ctx, publictypes.CompanyID(companyID), bankID.String())
	if err != nil || storedBank.AccountID != bank.AccountID || storedBank.GLAccountID != bank.GLAccountID {
		t.Fatalf("GetBankAccount() = %+v, %v", storedBank, err)
	}
	amount, err := publictypes.NewMoneyFromString("125.50", "USD")
	if err != nil {
		t.Fatal(err)
	}
	transaction := apisubledger.TreasuryTransaction{
		TransactionID: uuid.NewString(), CompanyID: publictypes.CompanyID(companyID),
		BankAccountID: bankID.String(), Amount: amount, TransactionType: "direct_deposit",
		ValueDate: time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), Reference: "DEP-1",
	}
	if err := ledger.SaveTransaction(ctx, publictypes.CompanyID(companyID), transaction); err != nil {
		t.Fatalf("SaveTransaction(): %v", err)
	}
	if err := ledger.SaveTransaction(ctx, publictypes.CompanyID(companyID), transaction); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate SaveTransaction() error = %v, want already exists", err)
	}
	repo.mu.Lock()
	storedDraftCount := len(repo.drafts)
	repo.mu.Unlock()
	if storedDraftCount != 0 {
		t.Fatalf("metadata-only SaveTransaction created %d journal drafts", storedDraftCount)
	}
	recorder := &treasuryP1003DraftRecorder{}
	internalBank := BankAccount{
		ID: bankID, CompanyID: companyID, BankName: bank.BankName, IBAN: bank.AccountNumber,
		Currency: bank.Currency, GLAccountID: glAccountID, ClearingAccountID: uuid.New(),
	}
	entry, err := ledger.CreateTransactionDraftContext(
		ctx, recorder, internalBank, transaction, uuid.New(), uuid.New(),
	)
	if err != nil {
		t.Fatalf("CreateTransactionDraftContext(): %v", err)
	}
	if len(recorder.entries) != 1 || entry.ID != recorder.entries[0].ID || len(entry.Lines) != 2 ||
		entry.Lines[0].AccountID != glAccountID ||
		!entry.Lines[0].Debit.Equal(money(t, "125.50")) ||
		!entry.Lines[1].Credit.Equal(money(t, "125.50")) {
		t.Fatalf("direct deposit did not produce the expected balanced draft: %+v", entry)
	}
	withdrawal := transaction
	withdrawal.TransactionID = uuid.NewString()
	withdrawal.TransactionType = "direct_withdrawal"
	withdrawalEntry, err := ledger.CreateTransactionDraftContext(
		ctx, recorder, internalBank, withdrawal, entry.Lines[1].AccountID, uuid.New(),
	)
	if err != nil {
		t.Fatalf("CreateTransactionDraftContext(withdrawal): %v", err)
	}
	if len(recorder.entries) != 2 || withdrawalEntry.Lines[0].AccountID != entry.Lines[1].AccountID ||
		!withdrawalEntry.Lines[0].Debit.Equal(money(t, "125.50")) ||
		withdrawalEntry.Lines[1].AccountID != glAccountID ||
		!withdrawalEntry.Lines[1].Credit.Equal(money(t, "125.50")) {
		t.Fatalf("direct withdrawal did not produce the expected balanced draft: %+v", withdrawalEntry)
	}
}

func TestTreasuryP1003StatementImportAndReconciliationReportDoNotPost(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	companyID, accountID := uuid.New(), uuid.New()
	ledger := NewTreasuryLedgerForCompany(repo, companyID)
	date := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	if _, err := ledger.RecordItemWithJournalContext(
		ctx, accountID, uuid.New(), date, money(t, "100"), "MATCH-100", BankItemPending,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.RecordItemWithJournalContext(
		ctx, accountID, uuid.New(), date, money(t, "15"), "PENDING-15", BankItemPending,
	); err != nil {
		t.Fatal(err)
	}
	imported, err := ledger.ImportStatementContext(ctx, accountID, []BankItem{
		{CompanyID: companyID, Date: date, Amount: money(t, "100"), Reference: "MATCH-100"},
		{CompanyID: companyID, Date: date, Amount: money(t, "25"), Reference: "BANK-ONLY-25"},
	})
	if err != nil {
		t.Fatalf("ImportStatementContext(): %v", err)
	}
	if len(imported) != 2 || imported[0].ID == uuid.Nil || imported[1].ID == uuid.Nil ||
		imported[0].Status != BankItemUnrecorded || imported[1].Status != BankItemUnrecorded ||
		imported[0].JournalEntryID != uuid.Nil || imported[1].JournalEntryID != uuid.Nil {
		t.Fatalf("statement import was not normalized as external data: %+v", imported)
	}
	report, pending, unrecorded, err := ledger.ReconciliationReportContext(ctx, accountID, imported, 0)
	if err != nil {
		t.Fatalf("ReconciliationReportContext(): %v", err)
	}
	if report.Reconciled || report.UnmatchedItems != 2 || report.StatementBalance.Cmp(money(t, "25").Amount()) != 0 ||
		report.BookBalance.Cmp(money(t, "15").Amount()) != 0 ||
		len(pending) != 1 || pending[0].Reference != "PENDING-15" ||
		len(unrecorded) != 1 || unrecorded[0].Reference != "BANK-ONLY-25" {
		t.Fatalf("report did not expose the pending book item: report=%+v pending=%+v unrecorded=%+v",
			report, pending, unrecorded)
	}
	repo.mu.Lock()
	draftCount := len(repo.drafts)
	repo.mu.Unlock()
	if draftCount != 0 {
		t.Fatalf("statement import/reconciliation created %d journal drafts", draftCount)
	}
}

func TestTreasuryP1003AdjustmentRequiresExplicitApproval(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	companyID, accountID, feeAccountID, journalID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ledger := NewTreasuryLedgerForCompany(repo, companyID)
	date := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	if _, err := ledger.RecordItemWithJournalContext(
		ctx, accountID, uuid.New(), date, money(t, "100"), "FEE-CASE", BankItemPending,
	); err != nil {
		t.Fatal(err)
	}
	statement := []BankItem{{
		CompanyID: companyID, ID: uuid.New(), AccountID: accountID,
		Date: date, Amount: money(t, "98"), Reference: "FEE-CASE",
	}}
	params := BankFeeParams{
		JournalID: journalID, FeeAccountID: feeAccountID, BankAccountID: accountID,
		Date: date, Description: "Approved bank fee",
	}
	if _, draft, err := ledger.ReconcileMultiLineContext(ctx, accountID, statement, 0, params); !errors.Is(err, ErrBankAdjustmentApprovalRequired) ||
		draft != nil {
		t.Fatalf("unapproved adjustment result = draft %v, error %v", draft, err)
	}
	repo.mu.Lock()
	draftCount := len(repo.drafts)
	repo.mu.Unlock()
	if draftCount != 0 {
		t.Fatalf("unapproved reconciliation created %d drafts", draftCount)
	}
	unapprovedItems, err := repo.ListBankItemsForCompany(ctx, companyID, accountID.String())
	if err != nil || len(unapprovedItems) != 1 || unapprovedItems[0].Status != BankItemPending {
		t.Fatalf("unapproved reconciliation changed bank-item state: items=%+v err=%v", unapprovedItems, err)
	}

	approval := BankAdjustmentApproval{
		ApprovalID: "APP-2026-17", ApprovedBy: "treasury-controller",
		Reason: "Recognize the bank service fee", ApprovedAt: date,
	}
	match, draft, err := ledger.ReconcileMultiLineApprovedContext(ctx, accountID, statement, 0, params, approval)
	if err != nil {
		t.Fatalf("ReconcileMultiLineApprovedContext(): %v", err)
	}
	if draft == nil || !match.BankFeesAmount.Equal(money(t, "2")) ||
		len(draft.Lines) != 2 || draft.ApprovedBy != approval.ApprovedBy ||
		draft.ApprovedAt == nil || draft.Reference != approval.ApprovalID+": "+approval.Reason {
		t.Fatalf("approved adjustment was not recorded in the balanced draft: match=%+v draft=%+v", match, draft)
	}
	if err := draft.Validate(); err != nil {
		t.Fatalf("approved adjustment draft is invalid: %v", err)
	}
	repo.mu.Lock()
	_, persisted := repo.drafts[draft.ID]
	repo.mu.Unlock()
	approvedItems, err := repo.ListBankItemsForCompany(ctx, companyID, accountID.String())
	if err != nil || !persisted || len(approvedItems) != 1 || approvedItems[0].Status != BankItemMatched {
		t.Fatalf("approved reconciliation was not persisted atomically: persisted=%v items=%+v err=%v",
			persisted, approvedItems, err)
	}
}

func TestTreasuryP1003LegacyReconciliationDoesNotImplyApproval(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRepository()
	companyID, accountID := uuid.New(), uuid.New()
	ledger := NewTreasuryLedgerForCompany(repo, companyID)
	date := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	item, err := ledger.RecordItemWithJournalContext(
		ctx, accountID, uuid.New(), date, money(t, "100"), "LEGACY-FEE", BankItemPending,
	)
	if err != nil {
		t.Fatal(err)
	}
	statement := []BankItem{{
		CompanyID: companyID, ID: uuid.New(), AccountID: accountID,
		Date: date, Amount: money(t, "98"), Reference: "LEGACY-FEE",
	}}
	_, draft, err := ledger.ReconcileMultiLine(accountID, statement, 0, BankFeeParams{
		JournalID: uuid.New(), FeeAccountID: uuid.New(), BankAccountID: accountID,
		Date: date, Description: "Caller supplied a fee account but no approval",
	})
	if !errors.Is(err, ErrBankAdjustmentApprovalRequired) || draft != nil {
		t.Fatalf("legacy reconciliation result = draft %v, error %v; want no draft and approval required", draft, err)
	}
	repo.mu.Lock()
	draftCount := len(repo.drafts)
	repo.mu.Unlock()
	stored, err := repo.ListBankItemsForCompany(ctx, companyID, accountID.String())
	if err != nil || draftCount != 0 || len(stored) != 1 ||
		stored[0].ID != item.ID || stored[0].Status != BankItemPending {
		t.Fatalf("legacy reconciliation changed accounting state: drafts=%d items=%+v err=%v",
			draftCount, stored, err)
	}
}

func TestTreasuryP1003ReportRequiresCompanyScope(t *testing.T) {
	ledger := NewTreasuryLedger(NewMemoryRepository())
	_, _, _, err := ledger.ReconciliationReportContext(
		context.Background(), uuid.New(), nil, 0,
	)
	if !errors.Is(err, ErrCompanyScopeRequired) {
		t.Fatalf("unscoped report error = %v, want company scope required", err)
	}
}

func TestTreasuryP1003TransferDraftsAreBalancedAndClearingRouted(t *testing.T) {
	ctx := context.Background()
	companyID, clearingID := uuid.New(), uuid.New()
	source := BankAccount{
		ID: uuid.New(), CompanyID: companyID, BankName: "Source",
		IBAN: "SA0000000000000000000000", Currency: "USD",
		GLAccountID: uuid.New(), ClearingAccountID: clearingID,
	}
	destination := BankAccount{
		ID: uuid.New(), CompanyID: companyID, BankName: "Destination",
		IBAN: "SA0000000000000000000001", Currency: "USD",
		GLAccountID: uuid.New(), ClearingAccountID: clearingID,
	}
	recorder := &treasuryP1003DraftRecorder{}
	ledger := NewTreasuryLedgerForCompany(NewMemoryRepository(), companyID)
	transfer, first, err := ledger.InitiateInterbankTransferContext(
		ctx, recorder, source, destination, money(t, "250"), uuid.New(),
		time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), "TRANSFER-1",
	)
	if err != nil {
		t.Fatalf("InitiateInterbankTransferContext(): %v", err)
	}
	settled, second, err := ledger.SettleInterbankTransferContext(
		ctx, recorder, transfer, destination, uuid.New(), transfer.InitiatedAt.Add(24*time.Hour),
	)
	if err != nil {
		t.Fatalf("SettleInterbankTransferContext(): %v", err)
	}
	for _, entry := range []posting.JournalEntry{first, second} {
		if err := entry.Validate(); err != nil {
			t.Fatalf("transfer entry is unbalanced or invalid: %v", err)
		}
	}
	if len(recorder.entries) != 2 || settled.Status != BankTransferSettled ||
		first.Lines[0].AccountID != clearingID || first.Lines[1].AccountID != source.GLAccountID ||
		second.Lines[0].AccountID != destination.GLAccountID || second.Lines[1].AccountID != clearingID {
		t.Fatalf("transfer drafts were not routed through clearing: transfer=%+v first=%+v second=%+v",
			settled, first, second)
	}
}

func TestTreasuryP1003PublicTransactionCurrencyValidation(t *testing.T) {
	ctx := context.Background()
	companyID, accountID := uuid.New(), uuid.New()
	ledger := NewTreasuryLedgerForCompany(NewMemoryRepository(), companyID)
	if err := ledger.SaveBankAccount(ctx, publictypes.CompanyID(companyID), apisubledger.BankAccount{
		AccountID: accountID.String(), CompanyID: publictypes.CompanyID(companyID),
		BankName: "Example Bank", AccountNumber: "123456", Currency: "USD",
		GLAccountID: publictypes.AccountID(uuid.New()),
	}); err != nil {
		t.Fatal(err)
	}
	amount, err := publictypes.NewMoneyFromString("10", "EUR")
	if err != nil {
		t.Fatal(err)
	}
	tx := apisubledger.TreasuryTransaction{
		TransactionID: uuid.NewString(), CompanyID: publictypes.CompanyID(companyID),
		BankAccountID: accountID.String(), Amount: amount,
		TransactionType: "deposit", ValueDate: time.Now().UTC(),
	}
	if err := ledger.SaveTransaction(ctx, publictypes.CompanyID(companyID), tx); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("cross-currency SaveTransaction() error = %v, want invalid amount", err)
	}
}
