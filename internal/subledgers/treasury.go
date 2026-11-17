package subledgers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/internal/posting"
	financialtypes "github.com/osmai4603-collab/accounting_engine/internal/types"
	apisubledger "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/subledger"
	publictypes "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	spisubledger "github.com/osmai4603-collab/accounting_engine/pkg/spi/subledger"
	"github.com/shopspring/decimal"
)

var ErrBankAdjustmentApprovalRequired = errors.New("explicit approval is required for a bank reconciliation adjustment")

type TreasuryLedger struct {
	repo         TreasuryRepository
	companyID    uuid.UUID
	storeMu      sync.RWMutex
	banks        map[string]apisubledger.BankAccount
	transactions map[string]apisubledger.TreasuryTransaction
}

var _ spisubledger.TreasuryStore = (*TreasuryLedger)(nil)

type TreasuryRepository interface {
	BankItemRepository
	CompanyBankItemRepository
	ConditionalBankItemsAndDraftRepository
	ConditionalBankItemRepository
}

func NewTreasuryLedger(repo TreasuryRepository) *TreasuryLedger {
	return &TreasuryLedger{repo: repo, banks: make(map[string]apisubledger.BankAccount), transactions: make(map[string]apisubledger.TreasuryTransaction)}
}

func NewTreasuryLedgerForCompany(repo TreasuryRepository, companyID uuid.UUID) *TreasuryLedger {
	return &TreasuryLedger{repo: repo, companyID: companyID, banks: make(map[string]apisubledger.BankAccount), transactions: make(map[string]apisubledger.TreasuryTransaction)}
}

// SaveBankAccount stores administrative bank-account metadata only; it never creates a journal entry.
func (l *TreasuryLedger) SaveBankAccount(ctx context.Context, companyID publictypes.CompanyID, bank apisubledger.BankAccount) error {
	if ctx == nil {
		return ErrInvalidBankAccount
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	company := uuid.UUID(companyID)
	bankCompany := uuid.UUID(bank.CompanyID)
	accountID, err := uuid.Parse(bank.AccountID)
	if err != nil || company == uuid.Nil || bankCompany != company ||
		(l.companyID != uuid.Nil && company != l.companyID) ||
		strings.TrimSpace(bank.BankName) == "" || strings.TrimSpace(bank.AccountNumber) == "" ||
		strings.TrimSpace(bank.Currency) == "" || uuid.UUID(bank.GLAccountID) == uuid.Nil {
		if bankCompany != uuid.Nil && company != bankCompany {
			return ErrCompanyMismatch
		}
		return ErrInvalidBankAccount
	}
	if _, ok := financialtypes.LookupCurrency(bank.Currency); !ok {
		return ErrInvalidBankAccount
	}
	bank.AccountID = accountID.String()
	key := treasuryStoreKey(company, bank.AccountID)
	l.storeMu.Lock()
	defer l.storeMu.Unlock()
	if l.banks == nil {
		l.banks = make(map[string]apisubledger.BankAccount)
	}
	l.banks[key] = bank
	return nil
}

func (l *TreasuryLedger) GetBankAccount(ctx context.Context, companyID publictypes.CompanyID, id string) (apisubledger.BankAccount, error) {
	if ctx == nil {
		return apisubledger.BankAccount{}, ErrInvalidBankAccount
	}
	if err := ctx.Err(); err != nil {
		return apisubledger.BankAccount{}, err
	}
	company := uuid.UUID(companyID)
	if company == uuid.Nil || (l.companyID != uuid.Nil && company != l.companyID) {
		return apisubledger.BankAccount{}, ErrCompanyMismatch
	}
	accountID, err := uuid.Parse(id)
	if err != nil {
		return apisubledger.BankAccount{}, ErrInvalidBankAccount
	}
	l.storeMu.RLock()
	defer l.storeMu.RUnlock()
	bank, exists := l.banks[treasuryStoreKey(company, accountID.String())]
	if !exists {
		return apisubledger.BankAccount{}, ErrNotFound
	}
	return bank, nil
}

// SaveTransaction stores transaction metadata only. Posting is an explicit, separate operation.
func (l *TreasuryLedger) SaveTransaction(ctx context.Context, companyID publictypes.CompanyID, tx apisubledger.TreasuryTransaction) error {
	if ctx == nil {
		return ErrInvalidOpenLine
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	company := uuid.UUID(companyID)
	txCompany := uuid.UUID(tx.CompanyID)
	transactionID, transactionErr := uuid.Parse(tx.TransactionID)
	accountID, accountErr := uuid.Parse(tx.BankAccountID)
	if company == uuid.Nil || txCompany != company ||
		(l.companyID != uuid.Nil && company != l.companyID) ||
		transactionErr != nil || accountErr != nil || tx.Amount.Currency() == "" ||
		tx.Amount.IsZero() || tx.Amount.IsNegative() || tx.ValueDate.IsZero() ||
		strings.TrimSpace(tx.TransactionType) == "" {
		if txCompany != uuid.Nil && company != txCompany {
			return ErrCompanyMismatch
		}
		return ErrInvalidAmount
	}
	bank, err := l.GetBankAccount(ctx, companyID, accountID.String())
	if err != nil {
		return err
	}
	if tx.Amount.Currency() != bank.Currency {
		return ErrInvalidAmount
	}
	tx.TransactionID = transactionID.String()
	tx.BankAccountID = accountID.String()
	key := treasuryStoreKey(company, tx.TransactionID)
	l.storeMu.Lock()
	defer l.storeMu.Unlock()
	if l.transactions == nil {
		l.transactions = make(map[string]apisubledger.TreasuryTransaction)
	}
	if _, exists := l.transactions[key]; exists {
		return ErrAlreadyExists
	}
	l.transactions[key] = tx
	return nil
}

func treasuryStoreKey(companyID uuid.UUID, id string) string {
	return companyID.String() + ":" + id
}

// CreateTransactionDraftContext is the explicit GL path for direct deposits and withdrawals.
func (l *TreasuryLedger) CreateTransactionDraftContext(
	ctx context.Context,
	creator JournalCreator,
	bank BankAccount,
	tx apisubledger.TreasuryTransaction,
	offsetAccountID, journalID uuid.UUID,
) (posting.JournalEntry, error) {
	if ctx == nil {
		return posting.JournalEntry{}, ErrInvalidOpenLine
	}
	if err := ctx.Err(); err != nil {
		return posting.JournalEntry{}, err
	}
	if creator == nil || offsetAccountID == uuid.Nil || journalID == uuid.Nil ||
		tx.ValueDate.IsZero() || strings.TrimSpace(tx.TransactionType) == "" ||
		tx.Amount.Currency() == "" || tx.Amount.IsZero() || tx.Amount.IsNegative() {
		return posting.JournalEntry{}, ErrInvalidAmount
	}
	if _, err := uuid.Parse(tx.TransactionID); err != nil {
		return posting.JournalEntry{}, ErrInvalidAmount
	}
	companyID := uuid.UUID(tx.CompanyID)
	txBankID, err := uuid.Parse(tx.BankAccountID)
	if err != nil || txBankID != bank.ID || companyID != bank.CompanyID ||
		(l.companyID != uuid.Nil && companyID != l.companyID) {
		return posting.JournalEntry{}, ErrCompanyMismatch
	}
	if err := bank.ValidateForCompany(companyID); err != nil {
		return posting.JournalEntry{}, err
	}
	if tx.Amount.Currency() != bank.Currency {
		return posting.JournalEntry{}, ErrInvalidAmount
	}
	amount, err := financialtypes.NewMoney(tx.Amount.Amount(), tx.Amount.Currency())
	if err != nil {
		return posting.JournalEntry{}, err
	}
	zero, err := financialtypes.Zero(amount.Currency())
	if err != nil {
		return posting.JournalEntry{}, err
	}
	var lines []posting.JournalEntryLine
	switch strings.ToLower(strings.TrimSpace(tx.TransactionType)) {
	case "deposit", "direct_deposit":
		lines = []posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: bank.GLAccountID, Debit: amount, Credit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: offsetAccountID, Debit: zero, Credit: amount},
		}
	case "withdrawal", "direct_withdrawal":
		lines = []posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: offsetAccountID, Debit: amount, Credit: zero},
			{CompanyID: companyID, ID: uuid.New(), AccountID: bank.GLAccountID, Debit: zero, Credit: amount},
		}
	default:
		return posting.JournalEntry{}, ErrInvalidBankAccount
	}
	reference := tx.Reference
	if reference == "" {
		reference = "TREASURY:" + tx.TransactionID
	}
	entry := posting.JournalEntry{
		CompanyID: companyID, ID: uuid.New(), JournalID: journalID,
		EntryDate: tx.ValueDate, Date: tx.ValueDate, Status: posting.EntryDraft,
		DocumentReference: reference, Reference: reference, CreatedAt: time.Now().UTC(),
		Lines: lines,
	}
	if err := entry.Validate(); err != nil {
		return posting.JournalEntry{}, err
	}
	if err := creator.CreateDraft(entry); err != nil {
		return posting.JournalEntry{}, err
	}
	return entry, nil
}

// BankAdjustmentApproval records the explicit authorization for a reconciliation-generated adjustment.
type BankAdjustmentApproval struct {
	ApprovalID string
	ApprovedBy string
	Reason     string
	ApprovedAt time.Time
}

func (a BankAdjustmentApproval) valid() bool {
	return strings.TrimSpace(a.ApprovalID) != "" &&
		strings.TrimSpace(a.ApprovedBy) != "" &&
		strings.TrimSpace(a.Reason) != "" &&
		!a.ApprovedAt.IsZero()
}

// ImportStatementContext validates and normalizes externally parsed statement lines.
// The imported data is returned for matching and is never persisted as a GL entry.
func (l *TreasuryLedger) ImportStatementContext(
	ctx context.Context,
	accountID uuid.UUID,
	lines []BankItem,
) ([]BankItem, error) {
	if ctx == nil || accountID == uuid.Nil {
		return nil, ErrInvalidOpenLine
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return []BankItem{}, nil
	}
	imported := make([]BankItem, len(lines))
	seen := make(map[uuid.UUID]struct{}, len(lines))
	currency := ""
	for i, line := range lines {
		if line.AccountID != uuid.Nil && line.AccountID != accountID {
			return nil, ErrInvalidBankAccount
		}
		if line.Date.IsZero() || line.Amount.Currency() == "" || line.Amount.Amount().IsZero() {
			return nil, ErrInvalidOpenLine
		}
		if l.companyID != uuid.Nil && line.CompanyID != uuid.Nil && line.CompanyID != l.companyID {
			return nil, ErrCompanyMismatch
		}
		if currency == "" {
			currency = line.Amount.Currency()
		} else if currency != line.Amount.Currency() {
			return nil, ErrMixedCurrencyReconciliation
		}
		if line.ID == uuid.Nil {
			line.ID = uuid.New()
		}
		if _, exists := seen[line.ID]; exists {
			return nil, ErrInvalidOpenLine
		}
		seen[line.ID] = struct{}{}
		line.AccountID = accountID
		if l.companyID != uuid.Nil {
			line.CompanyID = l.companyID
		}
		line.JournalEntryID = uuid.Nil
		line.Status = BankItemUnrecorded
		line.MatchedAt = time.Time{}
		imported[i] = line
	}
	return imported, nil
}

// ReconciliationReportContext matches imported lines and reports book and statement balances.
// It only updates matching metadata; adjustment entries require explicit approval.
func (l *TreasuryLedger) ReconciliationReportContext(
	ctx context.Context,
	accountID uuid.UUID,
	statement []BankItem,
	toleranceDays int,
) (apisubledger.ReconciliationReport, []BankItem, []BankItem, error) {
	if ctx == nil || accountID == uuid.Nil {
		return apisubledger.ReconciliationReport{}, nil, nil, ErrInvalidOpenLine
	}
	if l.companyID == uuid.Nil {
		return apisubledger.ReconciliationReport{}, nil, nil, ErrCompanyScopeRequired
	}
	if err := ctx.Err(); err != nil {
		return apisubledger.ReconciliationReport{}, nil, nil, err
	}
	_, pending, unrecorded, err := l.ReconcileWithToleranceContext(ctx, accountID, statement, toleranceDays)
	if err != nil {
		return apisubledger.ReconciliationReport{}, nil, nil, err
	}
	currency := ""
	for _, line := range statement {
		if currency == "" {
			currency = line.Amount.Currency()
		} else if currency != line.Amount.Currency() {
			return apisubledger.ReconciliationReport{}, nil, nil, ErrMixedCurrencyReconciliation
		}
	}
	statementBalance, bookBalance := decimal.Zero, decimal.Zero
	for _, item := range unrecorded {
		if currency == "" {
			currency = item.Amount.Currency()
		} else if currency != item.Amount.Currency() {
			return apisubledger.ReconciliationReport{}, nil, nil, ErrMixedCurrencyReconciliation
		}
		statementBalance = statementBalance.Add(item.Amount.Amount())
	}
	for _, item := range pending {
		if currency == "" {
			currency = item.Amount.Currency()
		} else if currency != item.Amount.Currency() {
			return apisubledger.ReconciliationReport{}, nil, nil, ErrMixedCurrencyReconciliation
		}
		bookBalance = bookBalance.Add(item.Amount.Amount())
	}
	unmatched := len(pending) + len(unrecorded)
	return apisubledger.ReconciliationReport{
		ReportID: uuid.NewString(), CompanyID: publictypes.CompanyID(l.companyID),
		BankAccountID: accountID.String(), StatementBalance: statementBalance,
		BookBalance: bookBalance, UnmatchedItems: unmatched, Reconciled: unmatched == 0,
	}, pending, unrecorded, nil
}

func (l *TreasuryLedger) InitiateInterbankTransferContext(
	ctx context.Context,
	creator JournalCreator,
	source, destination BankAccount,
	amount financialtypes.Money,
	journalID uuid.UUID,
	date time.Time,
	reference string,
) (InterbankTransfer, posting.JournalEntry, error) {
	if ctx == nil {
		return InterbankTransfer{}, posting.JournalEntry{}, ErrInvalidOpenLine
	}
	if err := ctx.Err(); err != nil {
		return InterbankTransfer{}, posting.JournalEntry{}, err
	}
	companyID := l.companyID
	if companyID == uuid.Nil {
		companyID = source.CompanyID
	}
	if err := source.ValidateForCompany(companyID); err != nil {
		return InterbankTransfer{}, posting.JournalEntry{}, err
	}
	if err := destination.ValidateForCompany(companyID); err != nil {
		return InterbankTransfer{}, posting.JournalEntry{}, err
	}
	if creator == nil || source.ID == destination.ID ||
		source.ClearingAccountID != destination.ClearingAccountID ||
		source.Currency != destination.Currency || amount.Currency() != source.Currency ||
		amount.Amount().IsZero() || amount.IsNegative() || journalID == uuid.Nil || date.IsZero() {
		return InterbankTransfer{}, posting.JournalEntry{}, ErrInvalidAmount
	}
	transferID := uuid.New()
	entry := posting.JournalEntry{
		CompanyID: companyID, ID: uuid.New(), JournalID: journalID,
		EntryDate: date, Date: date, Status: posting.EntryDraft,
		DocumentReference: fmt.Sprintf("BANK_TRANSFER:%s:INITIATE", transferID),
		Reference:         reference, CreatedAt: time.Now().UTC(),
		Lines: []posting.JournalEntryLine{
			{CompanyID: companyID, ID: uuid.New(), AccountID: source.ClearingAccountID, Debit: amount},
			{CompanyID: companyID, ID: uuid.New(), AccountID: source.GLAccountID, Credit: amount},
		},
	}
	if err := entry.Validate(); err != nil {
		return InterbankTransfer{}, posting.JournalEntry{}, err
	}
	if err := creator.CreateDraft(entry); err != nil {
		return InterbankTransfer{}, posting.JournalEntry{}, err
	}
	return InterbankTransfer{
		ID: transferID, CompanyID: companyID, SourceBankAccountID: source.ID,
		DestinationAccountID: destination.ID, ClearingAccountID: source.ClearingAccountID,
		Amount: amount, Reference: reference, Status: BankTransferInTransit,
		InitiatedEntryID: entry.ID, InitiatedAt: date.UTC(),
	}, entry, nil
}

func (l *TreasuryLedger) SettleInterbankTransferContext(
	ctx context.Context,
	creator JournalCreator,
	transfer InterbankTransfer,
	destination BankAccount,
	journalID uuid.UUID,
	date time.Time,
) (InterbankTransfer, posting.JournalEntry, error) {
	if ctx == nil {
		return InterbankTransfer{}, posting.JournalEntry{}, ErrInvalidOpenLine
	}
	if err := ctx.Err(); err != nil {
		return InterbankTransfer{}, posting.JournalEntry{}, err
	}
	if transfer.ID == uuid.Nil || transfer.CompanyID == uuid.Nil ||
		(transfer.Status != BankTransferInTransit) ||
		transfer.InitiatedEntryID == uuid.Nil || transfer.SettlementEntryID != uuid.Nil ||
		transfer.DestinationAccountID != destination.ID ||
		transfer.ClearingAccountID != destination.ClearingAccountID ||
		transfer.Amount.Currency() != destination.Currency ||
		journalID == uuid.Nil || date.IsZero() || date.Before(transfer.InitiatedAt) || creator == nil {
		return InterbankTransfer{}, posting.JournalEntry{}, ErrInvalidBankAccount
	}
	if l.companyID != uuid.Nil && l.companyID != transfer.CompanyID {
		return InterbankTransfer{}, posting.JournalEntry{}, ErrCompanyMismatch
	}
	if err := destination.ValidateForCompany(transfer.CompanyID); err != nil {
		return InterbankTransfer{}, posting.JournalEntry{}, err
	}
	entry := posting.JournalEntry{
		CompanyID: transfer.CompanyID, ID: uuid.New(), JournalID: journalID,
		EntryDate: date, Date: date, Status: posting.EntryDraft,
		DocumentReference: fmt.Sprintf("BANK_TRANSFER:%s:SETTLE", transfer.ID),
		Reference:         transfer.Reference, CreatedAt: time.Now().UTC(),
		Lines: []posting.JournalEntryLine{
			{CompanyID: transfer.CompanyID, ID: uuid.New(), AccountID: destination.GLAccountID, Debit: transfer.Amount},
			{CompanyID: transfer.CompanyID, ID: uuid.New(), AccountID: transfer.ClearingAccountID, Credit: transfer.Amount},
		},
	}
	if err := entry.Validate(); err != nil {
		return InterbankTransfer{}, posting.JournalEntry{}, err
	}
	if err := creator.CreateDraft(entry); err != nil {
		return InterbankTransfer{}, posting.JournalEntry{}, err
	}
	settledAt := date.UTC()
	transfer.Status = BankTransferSettled
	transfer.SettlementEntryID = entry.ID
	transfer.SettledAt = &settledAt
	return transfer, entry, nil
}

// Deprecated: use RecordItemContext.
func (l *TreasuryLedger) RecordItem(accountID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	return l.RecordItemContext(context.Background(), accountID, date, amount, reference, status)
}

func (l *TreasuryLedger) RecordItemContext(ctx context.Context, accountID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	if l.companyID == uuid.Nil {
		return l.recordItem(ctx, uuid.Nil, accountID, uuid.Nil, date, amount, reference, status)
	}
	return l.recordItem(ctx, l.companyID, accountID, uuid.Nil, date, amount, reference, status)
}

// Deprecated: use RecordItemForCompanyContext.
func (l *TreasuryLedger) RecordItemForCompany(companyID, accountID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	return l.RecordItemForCompanyContext(context.Background(), companyID, accountID, date, amount, reference, status)
}

func (l *TreasuryLedger) RecordItemForCompanyContext(ctx context.Context, companyID, accountID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	if companyID == uuid.Nil {
		return BankItem{}, ErrCompanyScopeRequired
	}
	if l.companyID != uuid.Nil && l.companyID != companyID {
		return BankItem{}, ErrCompanyMismatch
	}
	return l.recordItem(ctx, companyID, accountID, uuid.Nil, date, amount, reference, status)
}

// Deprecated: use RecordItemWithJournalContext.
func (l *TreasuryLedger) RecordItemWithJournal(accountID, journalEntryID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	return l.RecordItemWithJournalContext(context.Background(), accountID, journalEntryID, date, amount, reference, status)
}

func (l *TreasuryLedger) RecordItemWithJournalContext(ctx context.Context, accountID, journalEntryID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	companyID := l.companyID
	if companyID == uuid.Nil || journalEntryID == uuid.Nil {
		return BankItem{}, ErrInvalidOpenLine
	}
	return l.recordItem(ctx, companyID, accountID, journalEntryID, date, amount, reference, status)
}

func (l *TreasuryLedger) recordItem(ctx context.Context, companyID, accountID, journalEntryID uuid.UUID, date time.Time, amount financialtypes.Money, reference string, status BankItemStatus) (BankItem, error) {
	if ctx == nil {
		return BankItem{}, ErrInvalidOpenLine
	}
	if err := ctx.Err(); err != nil {
		return BankItem{}, err
	}
	if accountID == uuid.Nil || amount.Currency() == "" || status == "" || date.IsZero() {
		return BankItem{}, ErrInvalidAmount
	}
	if l.repo == nil {
		return BankItem{}, ErrInvalidBankAccount
	}
	item := BankItem{
		CompanyID: companyID, ID: uuid.New(), AccountID: accountID, JournalEntryID: journalEntryID,
		Date: date, Amount: amount, Reference: reference, Status: status,
	}
	if err := l.repo.SaveBankItem(ctx, item); err != nil {
		return BankItem{}, err
	}
	return item, nil
}

// Deprecated: use ReconcileMultiLineContext. Fee differences require the
// approval-bearing ReconcileMultiLineApprovedContext operation.
func (l *TreasuryLedger) ReconcileMultiLine(
	accountID uuid.UUID,
	statement []BankItem,
	toleranceDays int,
	feeParams BankFeeParams,
) (BankReconciliationMatch, *posting.JournalEntry, error) {
	return l.ReconcileMultiLineContext(context.Background(), accountID, statement, toleranceDays, feeParams)
}

func (l *TreasuryLedger) ReconcileMultiLineContext(
	ctx context.Context,
	accountID uuid.UUID,
	statement []BankItem,
	toleranceDays int,
	feeParams BankFeeParams,
) (BankReconciliationMatch, *posting.JournalEntry, error) {
	return l.reconcileMultiLineContext(ctx, accountID, statement, toleranceDays, feeParams, nil)
}

// ReconcileMultiLineApprovedContext permits an adjustment draft only when the caller
// supplies a complete approval record. Exact matching remains non-posting.
func (l *TreasuryLedger) ReconcileMultiLineApprovedContext(
	ctx context.Context,
	accountID uuid.UUID,
	statement []BankItem,
	toleranceDays int,
	feeParams BankFeeParams,
	approval BankAdjustmentApproval,
) (BankReconciliationMatch, *posting.JournalEntry, error) {
	if !approval.valid() {
		return BankReconciliationMatch{}, nil, ErrBankAdjustmentApprovalRequired
	}
	return l.reconcileMultiLineContext(ctx, accountID, statement, toleranceDays, feeParams, &approval)
}

func (l *TreasuryLedger) reconcileMultiLineContext(
	ctx context.Context,
	accountID uuid.UUID,
	statement []BankItem,
	toleranceDays int,
	feeParams BankFeeParams,
	approval *BankAdjustmentApproval,
) (BankReconciliationMatch, *posting.JournalEntry, error) {
	if ctx == nil {
		return BankReconciliationMatch{}, nil, ErrInvalidOpenLine
	}
	if err := ctx.Err(); err != nil {
		return BankReconciliationMatch{}, nil, err
	}
	if accountID == uuid.Nil || len(statement) == 0 || toleranceDays < 0 || toleranceDays > 5 {
		return BankReconciliationMatch{}, nil, ErrInvalidOpenLine
	}
	if l.repo == nil {
		return BankReconciliationMatch{}, nil, ErrInvalidBankAccount
	}
	ledgerItems, err := l.listBankItems(ctx, accountID.String())
	if err != nil {
		return BankReconciliationMatch{}, nil, err
	}
	currency := ""
	statementTotal := decimal.Zero
	statementIDs := make([]uuid.UUID, 0, len(statement))
	seen := make(map[uuid.UUID]struct{}, len(statement))
	statementDates := make([]time.Time, 0, len(statement))
	for _, item := range statement {
		if item.ID == uuid.Nil || item.AccountID != accountID || item.Amount.Currency() == "" || item.Date.IsZero() ||
			(l.companyID != uuid.Nil && item.CompanyID != uuid.Nil && item.CompanyID != l.companyID) {
			return BankReconciliationMatch{}, nil, ErrInvalidOpenLine
		}
		if _, exists := seen[item.ID]; exists {
			return BankReconciliationMatch{}, nil, ErrInvalidOpenLine
		}
		seen[item.ID] = struct{}{}
		if currency == "" {
			currency = item.Amount.Currency()
		} else if currency != item.Amount.Currency() {
			return BankReconciliationMatch{}, nil, ErrMixedCurrencyReconciliation
		}
		statementTotal = statementTotal.Add(item.Amount.Amount())
		statementIDs = append(statementIDs, item.ID)
		statementDates = append(statementDates, item.Date)
	}
	if statementTotal.IsZero() {
		return BankReconciliationMatch{}, nil, ErrInvalidAmount
	}

	candidates := make([]BankItem, 0, len(ledgerItems))
	for _, item := range ledgerItems {
		if item.Status == BankItemMatched {
			continue
		}
		if item.CompanyID != l.companyID && l.companyID != uuid.Nil {
			return BankReconciliationMatch{}, nil, ErrCompanyMismatch
		}
		if item.AccountID != accountID || item.Amount.Currency() != currency || item.Amount.Currency() == "" ||
			item.Date.IsZero() {
			return BankReconciliationMatch{}, nil, ErrInvalidOpenLine
		}
		withinTolerance := false
		for _, statementDate := range statementDates {
			if withinValueDateTolerance(statementDate, item.Date, toleranceDays) {
				withinTolerance = true
				break
			}
		}
		if !withinTolerance {
			continue
		}
		candidates = append(candidates, item)
	}
	indices, ledgerTotal, err := selectBankReconciliationSubset(candidates, statementTotal)
	if err != nil {
		return BankReconciliationMatch{}, nil, err
	}
	if ledgerTotal.LessThan(statementTotal) {
		return BankReconciliationMatch{}, nil, ErrUnmatchedLine
	}
	difference := ledgerTotal.Sub(statementTotal)
	if difference.IsPositive() {
		if approval == nil || !approval.valid() {
			return BankReconciliationMatch{}, nil, ErrBankAdjustmentApprovalRequired
		}
		if feeParams.JournalID == uuid.Nil || feeParams.FeeAccountID == uuid.Nil || feeParams.BankAccountID != accountID {
			return BankReconciliationMatch{}, nil, ErrUnmatchedLine
		}
	}
	selected := make([]BankItem, 0, len(indices))
	for _, index := range indices {
		selected = append(selected, candidates[index])
	}
	expected := append([]BankItem(nil), selected...)
	updated := append([]BankItem(nil), selected...)
	ledgerIDs := make([]uuid.UUID, 0, len(selected))
	entryIDs := make([]uuid.UUID, 0, len(selected))
	matchedAt := feeParams.Date
	if matchedAt.IsZero() {
		matchedAt = time.Now().UTC()
	}
	for i := range updated {
		updated[i].Status = BankItemMatched
		updated[i].MatchedAt = matchedAt
		ledgerIDs = append(ledgerIDs, updated[i].ID)
		if updated[i].JournalEntryID != uuid.Nil {
			entryIDs = append(entryIDs, updated[i].JournalEntryID)
		}
	}
	fee, err := financialtypes.NewMoney(difference, currency)
	if err != nil {
		return BankReconciliationMatch{}, nil, err
	}
	zero, err := financialtypes.Zero(currency)
	if err != nil {
		return BankReconciliationMatch{}, nil, err
	}
	match := BankReconciliationMatch{
		StatementLineIDs: statementIDs, JournalEntryIDs: entryIDs, LedgerItemIDs: ledgerIDs,
		BankFeesAmount: zero, Difference: difference,
	}
	if !fee.Amount().IsZero() {
		if approval == nil || !approval.valid() {
			return BankReconciliationMatch{}, nil, ErrBankAdjustmentApprovalRequired
		}
		if l.companyID == uuid.Nil {
			return BankReconciliationMatch{}, nil, ErrCompanyScopedBankItemsRequired
		}
		feeParams.BankAccountID = accountID
		feeDate := feeParams.Date
		if feeDate.IsZero() {
			feeDate = statement[0].Date
		}
		feeLines := []posting.JournalEntryLine{
			{CompanyID: l.companyID, ID: uuid.New(), AccountID: feeParams.FeeAccountID, Debit: fee, Credit: zero},
			{CompanyID: l.companyID, ID: uuid.New(), AccountID: accountID, Debit: zero, Credit: fee},
		}
		reference := feeParams.Description
		if reference == "" {
			reference = "BANK_FEE:MULTI_LINE"
		}
		draft := posting.JournalEntry{
			CompanyID: l.companyID, ID: uuid.New(), JournalID: feeParams.JournalID,
			EntryDate: feeDate, Date: feeDate, Status: posting.EntryDraft,
			DocumentReference: reference, Reference: approval.ApprovalID + ": " + approval.Reason,
			ApprovedBy: approval.ApprovedBy,
			CreatedAt:  time.Now().UTC(),
			Lines:      feeLines,
		}
		approvedAt := approval.ApprovedAt.UTC()
		draft.ApprovedAt = &approvedAt
		if err := draft.Validate(); err != nil {
			return BankReconciliationMatch{}, nil, err
		}
		if err := l.repo.CompareAndSaveBankItemsAndDraft(ctx, expected, updated, draft); err != nil {
			return BankReconciliationMatch{}, nil, err
		}
		match.BankFeesAmount = fee
		match.Difference = decimal.Zero
		return match, &draft, nil
	}
	if err := l.repo.CompareAndSaveBankItems(ctx, expected, updated); err != nil {
		return BankReconciliationMatch{}, nil, err
	}
	return match, nil, nil
}

func selectBankReconciliationSubset(candidates []BankItem, target decimal.Decimal) ([]int, decimal.Decimal, error) {
	const maxCandidates = 40
	const maxStates = 50000
	if len(candidates) == 0 {
		return nil, decimal.Zero, ErrNotFound
	}
	if len(candidates) > maxCandidates {
		return nil, decimal.Zero, ErrBankReconciliationTooComplex
	}
	type subset struct {
		indices   []int
		ambiguous bool
	}
	states := map[string]subset{"0": {indices: []int{}}}
	sums := map[string]decimal.Decimal{"0": decimal.Zero}
	for index, candidate := range candidates {
		amount := candidate.Amount.Amount()
		nextStates := make(map[string]subset, len(states)*2)
		nextSums := make(map[string]decimal.Decimal, len(sums)*2)
		for key, existing := range states {
			nextStates[key] = existing
			nextSums[key] = sums[key]
		}
		for key, existing := range states {
			total := sums[key].Add(amount)
			totalKey := total.String()
			indices := append(append([]int(nil), existing.indices...), index)
			if current, exists := nextStates[totalKey]; exists {
				current.ambiguous = true
				current.ambiguous = current.ambiguous || existing.ambiguous
				nextStates[totalKey] = current
			} else {
				nextStates[totalKey] = subset{indices: indices, ambiguous: existing.ambiguous}
				nextSums[totalKey] = total
			}
		}
		if len(nextStates) > maxStates {
			return nil, decimal.Zero, ErrBankReconciliationTooComplex
		}
		states, sums = nextStates, nextSums
	}

	bestKey := ""
	bestTotal := decimal.Zero
	exactKey := target.String()
	if exact, exists := states[exactKey]; exists {
		if exact.ambiguous {
			return nil, decimal.Zero, ErrAmbiguousBankReconciliation
		}
		if len(exact.indices) > 0 {
			return exact.indices, target, nil
		}
	}
	for key, total := range sums {
		candidate := states[key]
		if len(candidate.indices) == 0 || !total.GreaterThan(target) {
			continue
		}
		if bestKey == "" || total.LessThan(bestTotal) {
			bestKey, bestTotal = key, total
		} else if total.Equal(bestTotal) {
			return nil, decimal.Zero, ErrAmbiguousBankReconciliation
		}
	}
	if bestKey == "" {
		return nil, decimal.Zero, ErrUnmatchedLine
	}
	if states[bestKey].ambiguous {
		return nil, decimal.Zero, ErrAmbiguousBankReconciliation
	}
	return states[bestKey].indices, bestTotal, nil
}

// Deprecated: use ReconcileContext.
func (l *TreasuryLedger) Reconcile(accountID uuid.UUID, statement []BankItem) (matched []BankItem, pending []BankItem, unrecorded []BankItem, err error) {
	return l.ReconcileContext(context.Background(), accountID, statement)
}

func (l *TreasuryLedger) ReconcileContext(ctx context.Context, accountID uuid.UUID, statement []BankItem) (matched []BankItem, pending []BankItem, unrecorded []BankItem, err error) {
	return l.ReconcileWithToleranceContext(ctx, accountID, statement, 0)
}

// Deprecated: use ReconcileWithToleranceContext.
func (l *TreasuryLedger) ReconcileWithTolerance(accountID uuid.UUID, statement []BankItem, toleranceDays int) (matched []BankItem, pending []BankItem, unrecorded []BankItem, err error) {
	return l.ReconcileWithToleranceContext(context.Background(), accountID, statement, toleranceDays)
}

func (l *TreasuryLedger) ReconcileWithToleranceContext(ctx context.Context, accountID uuid.UUID, statement []BankItem, toleranceDays int) (matched []BankItem, pending []BankItem, unrecorded []BankItem, err error) {
	if ctx == nil {
		return nil, nil, nil, ErrInvalidOpenLine
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	if accountID == uuid.Nil || toleranceDays < 0 || toleranceDays > 5 || l.repo == nil {
		return nil, nil, nil, ErrInvalidOpenLine
	}
	if l.companyID != uuid.Nil {
		for _, item := range statement {
			if item.CompanyID != uuid.Nil && item.CompanyID != l.companyID {
				return nil, nil, nil, ErrCompanyMismatch
			}
		}
	}
	ledger, err := l.listBankItems(ctx, accountID.String())
	if err != nil {
		return nil, nil, nil, err
	}
	statementIDs := make(map[uuid.UUID]struct{}, len(statement))
	statementCurrency := ""
	for _, item := range statement {
		if item.ID == uuid.Nil || item.AccountID != accountID || item.Amount.Currency() == "" ||
			item.Amount.Amount().IsZero() || item.Date.IsZero() ||
			(l.companyID != uuid.Nil && item.CompanyID != uuid.Nil && item.CompanyID != l.companyID) {
			return nil, nil, nil, ErrInvalidOpenLine
		}
		if _, exists := statementIDs[item.ID]; exists {
			return nil, nil, nil, ErrInvalidOpenLine
		}
		statementIDs[item.ID] = struct{}{}
		if statementCurrency == "" {
			statementCurrency = item.Amount.Currency()
		} else if statementCurrency != item.Amount.Currency() {
			return nil, nil, nil, ErrMixedCurrencyReconciliation
		}
	}
	statementMatched := make([]bool, len(statement))
	expectedMatched := make([]BankItem, 0)
	updatedMatched := make([]BankItem, 0)
	for _, ledgerItem := range ledger {
		found := false
		for i, statementItem := range statement {
			if statementMatched[i] {
				continue
			}
			if ledgerItem.Reference == statementItem.Reference &&
				ledgerItem.Amount.Equal(statementItem.Amount) &&
				withinValueDateTolerance(ledgerItem.Date, statementItem.Date, toleranceDays) {
				statementMatched[i] = true
				expectedMatched = append(expectedMatched, ledgerItem)
				ledgerItem.Status = BankItemMatched
				updatedMatched = append(updatedMatched, ledgerItem)
				matched = append(matched, ledgerItem)
				found = true
				break
			}
		}
		if !found && ledgerItem.Status != BankItemMatched {
			pending = append(pending, ledgerItem)
		}
	}
	if len(expectedMatched) != 0 {
		if err := l.repo.CompareAndSaveBankItems(ctx, expectedMatched, updatedMatched); err != nil {
			return nil, nil, nil, err
		}
	}
	for i, statementItem := range statement {
		if !statementMatched[i] {
			unrecorded = append(unrecorded, statementItem)
		}
	}
	return matched, pending, unrecorded, nil
}

func (l *TreasuryLedger) listBankItems(ctx context.Context, accountID string) ([]BankItem, error) {
	if ctx == nil {
		return nil, ErrInvalidOpenLine
	}
	if l.repo == nil {
		return nil, ErrInvalidBankAccount
	}
	if l.companyID == uuid.Nil {
		return l.repo.ListBankItems(ctx, accountID)
	}
	return l.repo.ListBankItemsForCompany(ctx, l.companyID, accountID)
}

func sameDay(a, b time.Time) bool {
	return a.UTC().Format("2006-01-02") == b.UTC().Format("2006-01-02")
}

func withinValueDateTolerance(a, b time.Time, days int) bool {
	if days < 0 {
		days = 0
	} else if days > 5 {
		days = 5
	}
	dateA := time.Date(a.UTC().Year(), a.UTC().Month(), a.UTC().Day(), 0, 0, 0, 0, time.UTC)
	dateB := time.Date(b.UTC().Year(), b.UTC().Month(), b.UTC().Day(), 0, 0, 0, 0, time.UTC)
	delta := dateA.Sub(dateB)
	if delta < 0 {
		delta = -delta
	}
	return delta <= time.Duration(days)*24*time.Hour
}

// Deprecated: use ProcessBankFeeApprovedAtomicContext.
func (l *TreasuryLedger) ProcessBankFeeAtomic(item BankItem, params BankFeeParams) (BankItem, posting.JournalEntry, error) {
	return l.ProcessBankFeeAtomicContext(context.Background(), item, params)
}

func (l *TreasuryLedger) ProcessBankFeeAtomicContext(ctx context.Context, item BankItem, params BankFeeParams) (BankItem, posting.JournalEntry, error) {
	return l.processBankFeeAtomic(ctx, item, params, BankAdjustmentApproval{})
}

func (l *TreasuryLedger) ProcessBankFeeApprovedAtomicContext(
	ctx context.Context,
	item BankItem,
	params BankFeeParams,
	approval BankAdjustmentApproval,
) (BankItem, posting.JournalEntry, error) {
	return l.processBankFeeAtomic(ctx, item, params, approval)
}

func (l *TreasuryLedger) processBankFeeAtomic(
	ctx context.Context,
	item BankItem,
	params BankFeeParams,
	approval BankAdjustmentApproval,
) (BankItem, posting.JournalEntry, error) {
	if ctx == nil {
		return BankItem{}, posting.JournalEntry{}, ErrInvalidOpenLine
	}
	if err := ctx.Err(); err != nil {
		return BankItem{}, posting.JournalEntry{}, err
	}
	if !approval.valid() {
		return BankItem{}, posting.JournalEntry{}, ErrBankAdjustmentApprovalRequired
	}
	if item.ID == uuid.Nil || item.CompanyID == uuid.Nil || item.Amount.Currency() == "" ||
		item.Amount.IsNegative() || item.Amount.Amount().IsZero() ||
		item.Status == BankItemMatched || item.AccountID != params.BankAccountID {
		return BankItem{}, posting.JournalEntry{}, ErrInvalidAmount
	}
	if params.JournalID == uuid.Nil || params.FeeAccountID == uuid.Nil || params.BankAccountID == uuid.Nil {
		return BankItem{}, posting.JournalEntry{}, ErrInvalidOpenLine
	}
	if l.companyID != uuid.Nil && item.CompanyID != l.companyID {
		return BankItem{}, posting.JournalEntry{}, ErrCompanyMismatch
	}
	expected := item
	updated := item
	updated.Status = BankItemMatched

	date := params.Date
	if date.IsZero() {
		date = item.Date
	}
	zero, _ := financialtypes.Zero(item.Amount.Currency())
	lines := []posting.JournalEntryLine{
		{CompanyID: item.CompanyID, ID: uuid.New(), AccountID: params.FeeAccountID, Debit: item.Amount, Credit: zero},
		{CompanyID: item.CompanyID, ID: uuid.New(), AccountID: params.BankAccountID, Credit: item.Amount, Debit: zero},
	}
	ref := item.Reference
	if ref == "" {
		ref = params.Description
	}
	draft := posting.JournalEntry{
		CompanyID:         item.CompanyID,
		ID:                uuid.New(),
		JournalID:         params.JournalID,
		EntryDate:         date,
		Date:              date,
		Status:            posting.EntryDraft,
		DocumentReference: ref,
		Reference:         approval.ApprovalID + ": " + approval.Reason,
		ApprovedBy:        approval.ApprovedBy,
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
		Lines:             lines,
	}
	approvedAt := approval.ApprovedAt.UTC()
	draft.ApprovedAt = &approvedAt
	if err := draft.Validate(); err != nil {
		return BankItem{}, posting.JournalEntry{}, err
	}
	if err := l.repo.CompareAndSaveBankItemsAndDraft(ctx, []BankItem{expected}, []BankItem{updated}, draft); err != nil {
		return BankItem{}, posting.JournalEntry{}, err
	}
	return updated, draft, nil
}
