package subledgers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/ledger"
	apisubledger "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/subledger"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	publicsubledger "github.com/osmai4603-collab/accounting_engine/pkg/spi/subledger"
	"github.com/shopspring/decimal"
)

var (
	ErrARAPStoresRequired     = publicsubledger.ErrARAPStoresRequired
	ErrARAPHistoryUnavailable = publicsubledger.ErrARAPHistoryUnavailable
	ErrPostedARAPSaveFailed   = publicsubledger.ErrPostedARAPPersistenceFailed
	ErrInvalidARAPOperation   = publicsubledger.ErrInvalidARAPOperation
)

type PublicARAPService struct {
	app              publicapplication.Service
	stores           publicsubledger.ARAPStores
	requirePrincipal bool
}

func NewPublicARAPService(app publicapplication.Service, stores publicsubledger.ARAPStores, requirePrincipal bool) (*PublicARAPService, error) {
	if app == nil || stores.Receivables == nil || stores.Payables == nil || stores.Balance == nil {
		return nil, ErrARAPStoresRequired
	}
	return &PublicARAPService{app: app, stores: stores, requirePrincipal: requirePrincipal}, nil
}

func (s *PublicARAPService) CreateReceivableInvoice(ctx context.Context, command apisubledger.InvoiceCommand) (apisubledger.InvoiceResult, error) {
	return s.createInvoice(ctx, command, apisubledger.SubledgerReceivable)
}

func (s *PublicARAPService) CreatePayableInvoice(ctx context.Context, command apisubledger.InvoiceCommand) (apisubledger.InvoiceResult, error) {
	return s.createInvoice(ctx, command, apisubledger.SubledgerPayable)
}

// arapPostingIDs carries the deterministic draft and line identifiers an
// idempotent AR/AP retry needs so its accounting content hashes identically to
// the first attempt. Zero identifiers fall back to generated ones.
type arapPostingIDs struct {
	entryID types.JournalEntryID
	lineIDs []types.JournalEntryLineID
}

func (ids arapPostingIDs) line(index int) types.JournalEntryLineID {
	if index < len(ids.lineIDs) {
		return ids.lineIDs[index]
	}
	return types.JournalEntryLineID{}
}

func (s *PublicARAPService) createInvoice(ctx context.Context, command apisubledger.InvoiceCommand, kind apisubledger.SubledgerType) (apisubledger.InvoiceResult, error) {
	functional, err := invoiceFunctionalAmount(command)
	if err != nil {
		return apisubledger.InvoiceResult{}, err
	}
	if uuid.UUID(command.CompanyID) == uuid.Nil || strings.TrimSpace(command.PartnerID) == "" ||
		uuid.UUID(command.ControlAccountID) == uuid.Nil || uuid.UUID(command.CounterAccountID) == uuid.Nil ||
		uuid.UUID(command.JournalID) == uuid.Nil || command.Amount.IsNegative() || command.Amount.IsZero() ||
		functional.IsNegative() || functional.IsZero() {
		return apisubledger.InvoiceResult{}, ErrInvalidARAPOperation
	}
	if command.DocumentDate.IsZero() {
		command.DocumentDate = time.Now().UTC()
	}
	if command.DueDate.IsZero() {
		command.DueDate = command.DocumentDate
	}
	if strings.TrimSpace(command.Reference) == "" {
		return apisubledger.InvoiceResult{}, ErrInvalidARAPOperation
	}

	key := arapIdempotencyKey(ctx)
	openItemID := ""
	ids := arapPostingIDs{}
	if key != "" {
		scope := arapScopeKey(string(kind), uuid.UUID(command.CompanyID).String(), key, arapCommandFingerprint(command))
		openItemID = arapDeterministicID(scope + ":open-item")
		if derived, ok := s.deterministicIDs(scope, 2); ok {
			ids = derived
		}
	}

	var result apisubledger.InvoiceResult
	operationErr := s.withinTransaction(ctx, command.CompanyID, func(txCtx context.Context) error {
		operationResult, err := s.createInvoiceOperation(txCtx, command, kind, functional, openItemID, ids)
		if err != nil {
			return err
		}
		result = operationResult
		return nil
	})
	if operationErr != nil {
		return apisubledger.InvoiceResult{}, operationErr
	}
	return result, nil
}

// createInvoiceOperation posts the balanced invoice entry and saves the open
// item. It runs inside the caller's unit of work so a failed open-item save
// rolls the GL posting back instead of leaving a posted entry with no
// subledger record.
func (s *PublicARAPService) createInvoiceOperation(
	ctx context.Context,
	command apisubledger.InvoiceCommand,
	kind apisubledger.SubledgerType,
	functional types.Money,
	openItemID string,
	ids arapPostingIDs,
) (apisubledger.InvoiceResult, error) {
	entry, _, err := s.postBalanced(ctx, command.CompanyID, command.JournalID, command.DocumentDate,
		command.Reference, command.Description, kind, command.ControlAccountID, command.CounterAccountID,
		command.Amount, functional, ids)
	if err != nil {
		return apisubledger.InvoiceResult{}, err
	}
	if openItemID == "" {
		openItemID = uuid.NewString()
	}
	now := time.Now().UTC()
	switch kind {
	case apisubledger.SubledgerReceivable:
		line := apisubledger.Receivable{
			ID: openItemID, CompanyID: command.CompanyID, PartnerID: command.PartnerID,
			ControlAccountID: command.ControlAccountID, JournalEntryID: entry.ID,
			Amount: command.Amount, RemainingAmount: command.Amount,
			FunctionalAmount: functional, FunctionalRemainingAmount: functional,
			DocumentDate: command.DocumentDate, DueDate: command.DueDate,
			Reference: command.Reference, Status: "open", CreatedAt: now,
		}
		if err := s.stores.Receivables.SaveReceivable(ctx, command.CompanyID, line); err != nil {
			return apisubledger.InvoiceResult{}, fmt.Errorf("%w: %v", ErrPostedARAPSaveFailed, err)
		}
		return apisubledger.InvoiceResult{Receivable: &line, EntryID: entry.ID}, nil
	case apisubledger.SubledgerPayable:
		line := apisubledger.Payable{
			ID: openItemID, CompanyID: command.CompanyID, PartnerID: command.PartnerID,
			ControlAccountID: command.ControlAccountID, JournalEntryID: entry.ID,
			Amount: command.Amount, RemainingAmount: command.Amount,
			FunctionalAmount: functional, FunctionalRemainingAmount: functional,
			DocumentDate: command.DocumentDate, DueDate: command.DueDate,
			Reference: command.Reference, Status: "open", CreatedAt: now,
		}
		if err := s.stores.Payables.SavePayable(ctx, command.CompanyID, line); err != nil {
			return apisubledger.InvoiceResult{}, fmt.Errorf("%w: %v", ErrPostedARAPSaveFailed, err)
		}
		return apisubledger.InvoiceResult{Payable: &line, EntryID: entry.ID}, nil
	default:
		return apisubledger.InvoiceResult{}, ErrInvalidARAPOperation
	}
}

func invoiceFunctionalAmount(command apisubledger.InvoiceCommand) (types.Money, error) {
	if command.FunctionalAmount.Currency() != "" {
		if command.FunctionalAmount.IsNegative() || command.FunctionalAmount.IsZero() {
			return types.Money{}, ErrInvalidARAPOperation
		}
		return command.FunctionalAmount, nil
	}
	return command.Amount, nil
}

func (s *PublicARAPService) postBalanced(
	ctx context.Context,
	companyID types.CompanyID,
	journalID types.JournalID,
	date time.Time,
	reference, description string,
	kind apisubledger.SubledgerType,
	controlID, counterpartID types.AccountID,
	amount, functional types.Money,
	ids arapPostingIDs,
) (ledger.JournalEntry, bool, error) {
	if amount.Currency() == "" || functional.Currency() == "" {
		return ledger.JournalEntry{}, false, ErrInvalidARAPOperation
	}
	rate := functional.Amount().Div(amount.Amount())
	if !rate.IsPositive() {
		return ledger.JournalEntry{}, false, ErrInvalidARAPOperation
	}
	zeroFunctional, err := types.Zero(functional.Currency())
	if err != nil {
		return ledger.JournalEntry{}, false, err
	}
	controlDebit := kind == apisubledger.SubledgerReceivable
	lines := []ledger.EntryLine{
		newPublicLine(ids.line(0), controlID, amount, functional, rate, controlDebit, zeroFunctional),
		newPublicLine(ids.line(1), counterpartID, amount, functional, rate, !controlDebit, zeroFunctional),
	}
	entryDate := date.UTC()
	commandContext := arapCommandContext(ctx, companyID, s.requirePrincipal)
	draftCommand := publicapplication.CreateDraftCommand{
		Context: commandContext, CompanyID: companyID, JournalID: journalID,
		EffectiveDate: entryDate, PostingDate: entryDate, Description: description,
		SourceModule: "ARAP", ReferenceID: reference,
		TransactionCurrency: amount.Currency(), ExchangeRate: rate.String(),
		Classification: ledger.EntryClassRegular, Lines: lines,
	}
	return s.postDraft(ctx, draftCommand, ids)
}

// createARAPDraft creates the operation's draft. When the operation carries a
// deterministic identifier, a previous attempt under the same idempotency key
// may already have saved that identifier: a posted entry replays unchanged,
// and a draft left by an interrupted attempt resumes posting. Generated
// identifiers keep the plain create-draft behavior.
func (s *PublicARAPService) createARAPDraft(
	ctx context.Context,
	draftCommand publicapplication.CreateDraftCommand,
	ids arapPostingIDs,
) (publicapplication.EntryResult, bool, error) {
	if ids.entryID == (types.JournalEntryID{}) {
		created, err := s.app.CreateDraft(ctx, draftCommand)
		return created, false, err
	}
	created, err := s.createDraftWithID(ctx, draftCommand, ids.entryID)
	if err == nil {
		return created, false, nil
	}
	existing, getErr := s.app.GetEntry(ctx, publicapplication.GetEntryQuery{
		Context: draftCommand.Context, CompanyID: draftCommand.CompanyID, EntryID: ids.entryID,
	})
	if getErr != nil {
		return publicapplication.EntryResult{}, false, err
	}
	switch existing.Status {
	case ledger.EntryPosted:
		return publicapplication.EntryResult{Entry: existing, IdempotentReplay: true}, true, nil
	case ledger.EntryDraft:
		return publicapplication.EntryResult{Entry: existing}, false, nil
	default:
		return publicapplication.EntryResult{}, false, err
	}
}

// postDraft creates the operation's draft and posts it, reporting whether the
// result replays an earlier identical attempt.
func (s *PublicARAPService) postDraft(
	ctx context.Context,
	draftCommand publicapplication.CreateDraftCommand,
	ids arapPostingIDs,
) (ledger.JournalEntry, bool, error) {
	created, replayed, err := s.createARAPDraft(ctx, draftCommand, ids)
	if err != nil {
		return ledger.JournalEntry{}, false, err
	}
	posted, err := s.app.Post(ctx, publicapplication.PostEntryCommand{
		Context: draftCommand.Context, CompanyID: draftCommand.CompanyID, EntryID: created.Entry.ID,
	})
	if err != nil {
		return ledger.JournalEntry{}, false, err
	}
	return posted.Entry, replayed || posted.IdempotentReplay, nil
}

// createDraftWithID creates the draft with a caller-supplied identifier so an
// idempotent retry produces identical accounting content.
func (s *PublicARAPService) createDraftWithID(
	ctx context.Context,
	command publicapplication.CreateDraftCommand,
	entryID types.JournalEntryID,
) (publicapplication.EntryResult, error) {
	if creator, ok := s.app.(publicapplication.DeterministicDraftCreator); ok {
		return creator.CreateDraftWithID(ctx, command, entryID)
	}
	return s.app.CreateDraft(ctx, command)
}

func newPublicLine(
	lineID types.JournalEntryLineID,
	accountID types.AccountID,
	transactionAmount, functionalAmount types.Money,
	rate decimal.Decimal,
	debit bool,
	zeroFunctional types.Money,
) ledger.EntryLine {
	if lineID == (types.JournalEntryLineID{}) {
		lineID = types.NewJournalEntryLineID()
	}
	line := ledger.EntryLine{
		ID: lineID, AccountID: accountID,
		TransactionAmount: transactionAmount, TransactionCurrency: transactionAmount.Currency(),
		ExchangeRate: rate,
	}
	if debit {
		line.FunctionalDebit, line.FunctionalCredit = functionalAmount, zeroFunctional
	} else {
		line.FunctionalDebit, line.FunctionalCredit = zeroFunctional, functionalAmount
	}
	return line
}

// arapIdempotencyKey returns the caller-supplied idempotency key carried in
// the request metadata, if any.
func arapIdempotencyKey(ctx context.Context) string {
	metadata, _ := publicapplication.RequestMetadataFromContext(ctx)
	return strings.TrimSpace(metadata.IdempotencyKey)
}

// arapScopeKey derives the deterministic scope of an idempotent AR/AP
// operation from the operation kind, the caller key and the request payload.
// An identical retry derives the first attempt's identifiers and replays; a
// different payload under the same key derives different accounting content
// and is therefore rejected by the posting layer as an idempotency conflict.
func arapScopeKey(kind, companyID, key, fingerprint string) string {
	return fmt.Sprintf("accounting-engine:arap:%s:%s:%s:%s", kind, companyID, key, fingerprint)
}

// arapCommandFingerprint fingerprints the request payload so a replay under
// the same idempotency key can be distinguished from a different request that
// reuses the key. Dates must be caller-supplied for a stable fingerprint; a
// defaulted date makes retries produce different content, which the posting
// layer reports as a conflict rather than replaying.
func arapCommandFingerprint(command any) string {
	encoded, err := json.Marshal(command)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// arapDeterministicID derives a stable identifier for an idempotent AR/AP
// operation. A retry that carries the same key derives the same identifier,
// so a replay observes the record the first attempt saved.
func arapDeterministicID(scope string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(scope)).String()
}

// deterministicIDs derives the draft and line identifiers an idempotent retry
// needs for byte-identical accounting content. It reports false when the
// application layer cannot accept caller-supplied identifiers; the operation
// then keeps its generated identifiers.
func (s *PublicARAPService) deterministicIDs(scope string, lineCount int) (arapPostingIDs, bool) {
	if _, ok := s.app.(publicapplication.DeterministicDraftCreator); !ok {
		return arapPostingIDs{}, false
	}
	ids := arapPostingIDs{
		entryID: types.JournalEntryID(uuid.NewSHA1(uuid.NameSpaceOID, []byte(scope+":entry"))),
		lineIDs: make([]types.JournalEntryLineID, 0, lineCount),
	}
	for index := 0; index < lineCount; index++ {
		ids.lineIDs = append(ids.lineIDs, types.JournalEntryLineID(
			uuid.NewSHA1(uuid.NameSpaceOID, []byte(scope+":line:"+strconv.Itoa(index)))))
	}
	return ids, true
}

// withinTransaction runs fn inside one storage unit of work when the
// application layer supports it: the GL posting and the subledger updates
// then commit together or roll back together. Without the capability the
// engine keeps its per-entry posting atomicity and callers must not assume
// cross-store atomicity.
func (s *PublicARAPService) withinTransaction(ctx context.Context, companyID types.CompanyID, fn func(context.Context) error) error {
	if txService, ok := s.app.(publicapplication.TransactionalService); ok {
		return txService.WithinTransaction(ctx, companyID, fn)
	}
	return fn(ctx)
}

func arapCommandContext(ctx context.Context, companyID types.CompanyID, requirePrincipal bool) publicapplication.Context {
	metadata, _ := publicapplication.RequestMetadataFromContext(ctx)
	principal, hasPrincipal := publicapplication.PrincipalFromContext(ctx)
	if !requirePrincipal && !hasPrincipal {
		principal = publicapplication.Principal{ActorID: "sdk", CompanyIDs: []types.CompanyID{companyID}}
	}
	return publicapplication.Context{
		Principal: principal, CorrelationID: metadata.CorrelationID,
		IdempotencyKey: metadata.IdempotencyKey, SourceSystem: metadata.SourceSystem,
	}
}

func (s *PublicARAPService) RecordPayment(ctx context.Context, command apisubledger.PaymentCommand) (apisubledger.PaymentResult, error) {
	if uuid.UUID(command.CompanyID) == uuid.Nil || uuid.UUID(command.CashAccountID) == uuid.Nil ||
		uuid.UUID(command.JournalID) == uuid.Nil || len(command.Allocations) == 0 ||
		command.Amount.Currency() == "" || command.Amount.IsNegative() || command.Amount.IsZero() ||
		command.FunctionalAmount.Currency() == "" || command.FunctionalAmount.IsNegative() || command.FunctionalAmount.IsZero() ||
		strings.TrimSpace(command.Reference) == "" ||
		(command.Kind != apisubledger.SubledgerReceivable && command.Kind != apisubledger.SubledgerPayable) {
		return apisubledger.PaymentResult{}, ErrInvalidARAPOperation
	}
	if command.PaymentDate.IsZero() {
		command.PaymentDate = time.Now().UTC()
	}

	key := arapIdempotencyKey(ctx)
	paymentID := ""
	ids := arapPostingIDs{}
	if key != "" {
		scope := arapScopeKey(string(command.Kind), uuid.UUID(command.CompanyID).String(), key, arapCommandFingerprint(command))
		paymentID = arapDeterministicID(scope + ":payment")
		if derived, ok := s.deterministicIDs(scope, len(command.Allocations)+1); ok {
			ids = derived
		}
	}

	var result apisubledger.PaymentResult
	operationErr := s.withinTransaction(ctx, command.CompanyID, func(txCtx context.Context) error {
		payment, entryID, err := s.recordPaymentOperation(txCtx, command, paymentID, ids)
		if err != nil {
			return err
		}
		result = apisubledger.PaymentResult{Payment: payment, EntryID: entryID}
		return nil
	})
	if operationErr != nil {
		return apisubledger.PaymentResult{}, operationErr
	}
	return result, nil
}

// recordPaymentOperation validates the allocations against the current open
// items, posts the balanced payment entry and reduces the allocated balances.
// It runs inside the caller's unit of work so a failed open-item update rolls
// the GL posting back, and it skips the balance updates when the posting
// layer reports that an identical payment already completed.
func (s *PublicARAPService) recordPaymentOperation(
	ctx context.Context,
	command apisubledger.PaymentCommand,
	paymentID string,
	ids arapPostingIDs,
) (apisubledger.Payment, types.JournalEntryID, error) {
	type allocation struct {
		id         string
		amount     types.Money
		functional types.Money
		control    types.AccountID
		partner    string
		receivable *apisubledger.Receivable
		payable    *apisubledger.Payable
	}
	allocations := make([]allocation, 0, len(command.Allocations))
	txnCurrency, functionalCurrency := "", command.FunctionalAmount.Currency()
	totalFunctional := command.FunctionalAmount
	if functionalCurrency == "" {
		return apisubledger.Payment{}, types.JournalEntryID{}, ErrInvalidARAPOperation
	}
	var sumTxn types.Money
	var sumFunctional types.Money
	var err error
	partnerID := ""
	seenOpenItems := make(map[string]struct{}, len(command.Allocations))
	for _, requested := range command.Allocations {
		if requested.OpenItemID == "" || requested.Amount.Currency() == "" ||
			requested.Amount.IsNegative() || requested.Amount.IsZero() {
			return apisubledger.Payment{}, types.JournalEntryID{}, ErrInvalidARAPOperation
		}
		if _, ok := seenOpenItems[requested.OpenItemID]; ok {
			return apisubledger.Payment{}, types.JournalEntryID{}, ErrInvalidARAPOperation
		}
		seenOpenItems[requested.OpenItemID] = struct{}{}
		var line allocation
		line.id, line.amount = requested.OpenItemID, requested.Amount
		if command.Kind == apisubledger.SubledgerReceivable {
			record, getErr := s.stores.Receivables.GetReceivable(ctx, command.CompanyID, requested.OpenItemID)
			if getErr != nil {
				return apisubledger.Payment{}, types.JournalEntryID{}, getErr
			}
			if record.CompanyID != command.CompanyID || record.Status == "closed" {
				return apisubledger.Payment{}, types.JournalEntryID{}, ErrInvalidOpenLine
			}
			line.partner, line.control, line.receivable = record.PartnerID, record.ControlAccountID, &record
			line.functional = requested.FunctionalAmount
			if line.functional.Currency() == "" {
				line.functional, getErr = proportionalFunctional(record.FunctionalRemainingAmount, record.RemainingAmount, requested.Amount)
				if getErr != nil {
					return apisubledger.Payment{}, types.JournalEntryID{}, getErr
				}
			}
		} else {
			record, getErr := s.stores.Payables.GetPayable(ctx, command.CompanyID, requested.OpenItemID)
			if getErr != nil {
				return apisubledger.Payment{}, types.JournalEntryID{}, getErr
			}
			if record.CompanyID != command.CompanyID || record.Status == "closed" {
				return apisubledger.Payment{}, types.JournalEntryID{}, ErrInvalidOpenLine
			}
			line.partner, line.control, line.payable = record.PartnerID, record.ControlAccountID, &record
			line.functional = requested.FunctionalAmount
			if line.functional.Currency() == "" {
				line.functional, getErr = proportionalFunctional(record.FunctionalRemainingAmount, record.RemainingAmount, requested.Amount)
				if getErr != nil {
					return apisubledger.Payment{}, types.JournalEntryID{}, getErr
				}
			}
		}
		if line.amount.Currency() != txnCurrency && txnCurrency != "" {
			return apisubledger.Payment{}, types.JournalEntryID{}, ErrInvalidARAPOperation
		}
		if line.partner == "" || (partnerID != "" && partnerID != line.partner) {
			return apisubledger.Payment{}, types.JournalEntryID{}, ErrInvalidARAPOperation
		}
		partnerID = line.partner
		txnCurrency = line.amount.Currency()
		if line.amount.GreaterThan(openItemRemaining(line)) ||
			line.functional.GreaterThan(openItemFunctionalRemaining(line)) ||
			line.amount.Currency() != openItemRemaining(line).Currency() ||
			line.functional.Currency() != functionalCurrency ||
			line.functional.IsNegative() || line.functional.IsZero() {
			return apisubledger.Payment{}, types.JournalEntryID{}, ErrInvalidAmount
		}
		if sumTxn.Currency() == "" {
			sumTxn = line.amount
			sumFunctional = line.functional
		} else {
			sumTxn, err = sumTxn.Add(line.amount)
			if err == nil {
				sumFunctional, err = sumFunctional.Add(line.functional)
			}
			if err != nil {
				return apisubledger.Payment{}, types.JournalEntryID{}, err
			}
		}
		allocations = append(allocations, line)
	}
	if command.Amount.Currency() != "" && !command.Amount.Equal(sumTxn) {
		return apisubledger.Payment{}, types.JournalEntryID{}, ErrInvalidAmount
	}
	if !totalFunctional.Equal(sumFunctional) {
		return apisubledger.Payment{}, types.JournalEntryID{}, errors.New("payment functional amount must equal allocated functional balances; record FX differences separately")
	}
	zeroFunctional, err := types.Zero(functionalCurrency)
	if err != nil {
		return apisubledger.Payment{}, types.JournalEntryID{}, err
	}
	lines := make([]ledger.EntryLine, 0, len(allocations)+1)
	for index, line := range allocations {
		rate := line.functional.Amount().Div(line.amount.Amount())
		controlDebit := command.Kind == apisubledger.SubledgerPayable
		lines = append(lines, newPublicLine(ids.line(index), line.control, line.amount,
			line.functional, rate, controlDebit, zeroFunctional))
	}
	cashRate := totalFunctional.Amount().Div(sumTxn.Amount())
	cashDebit := command.Kind == apisubledger.SubledgerReceivable
	lines = append(lines, newPublicLine(ids.line(len(allocations)), command.CashAccountID, sumTxn,
		totalFunctional, cashRate, cashDebit, zeroFunctional))
	for _, line := range lines {
		if line.AccountID == (types.AccountID{}) || line.ExchangeRate.IsZero() {
			return apisubledger.Payment{}, types.JournalEntryID{}, ErrInvalidARAPOperation
		}
	}
	entryDate := command.PaymentDate.UTC()
	draftCommand := publicapplication.CreateDraftCommand{
		Context: arapCommandContext(ctx, command.CompanyID, s.requirePrincipal), CompanyID: command.CompanyID, JournalID: command.JournalID,
		EffectiveDate: entryDate, PostingDate: entryDate, Description: command.Description,
		SourceModule: "ARAP_PAYMENT", ReferenceID: command.Reference,
		TransactionCurrency: txnCurrency, ExchangeRate: cashRate.String(),
		Classification: ledger.EntryClassRegular, Lines: lines,
	}
	posted, replayed, err := s.postDraft(ctx, draftCommand, ids)
	if err != nil {
		return apisubledger.Payment{}, types.JournalEntryID{}, err
	}
	allocatedIDs := make([]string, 0, len(allocations))
	if !replayed {
		for _, item := range allocations {
			allocatedIDs = append(allocatedIDs, item.id)
			if item.receivable != nil {
				item.receivable.RemainingAmount, err = item.receivable.RemainingAmount.Subtract(item.amount)
				if err == nil {
					item.receivable.FunctionalRemainingAmount, err = item.receivable.FunctionalRemainingAmount.Subtract(item.functional)
				}
				if err == nil {
					item.receivable.Status = openItemStatus(item.receivable.RemainingAmount)
					err = s.stores.Receivables.SaveReceivable(ctx, command.CompanyID, *item.receivable)
				}
			} else {
				item.payable.RemainingAmount, err = item.payable.RemainingAmount.Subtract(item.amount)
				if err == nil {
					item.payable.FunctionalRemainingAmount, err = item.payable.FunctionalRemainingAmount.Subtract(item.functional)
				}
				if err == nil {
					item.payable.Status = openItemStatus(item.payable.RemainingAmount)
					err = s.stores.Payables.SavePayable(ctx, command.CompanyID, *item.payable)
				}
			}
			if err != nil {
				return apisubledger.Payment{}, types.JournalEntryID{}, fmt.Errorf("%w: %v", ErrPostedARAPSaveFailed, err)
			}
		}
	} else {
		for _, item := range allocations {
			allocatedIDs = append(allocatedIDs, item.id)
		}
	}
	if paymentID == "" {
		paymentID = uuid.NewString()
	}
	return apisubledger.Payment{
		ID: paymentID, CompanyID: command.CompanyID, PartnerID: partnerID, Kind: command.Kind,
		Amount: sumTxn, FunctionalAmount: totalFunctional, PaymentDate: command.PaymentDate,
		Reference: command.Reference, JournalEntryID: posted.ID, AllocatedLines: allocatedIDs,
	}, posted.ID, nil
}

func proportionalFunctional(functionalRemaining, remaining, allocation types.Money) (types.Money, error) {
	if remaining.IsZero() || remaining.Currency() != allocation.Currency() || allocation.GreaterThan(remaining) {
		return types.Money{}, ErrInvalidAmount
	}
	if functionalRemaining.Currency() == "" && remaining.Currency() == allocation.Currency() {
		return allocation, nil
	}
	if functionalRemaining.Currency() == "" {
		return types.Money{}, ErrReconciliationFunctionalAmountMissing
	}
	ratio := allocation.Amount().Div(remaining.Amount())
	return types.NewMoney(functionalRemaining.Amount().Mul(ratio), functionalRemaining.Currency())
}

func openItemRemaining(line struct {
	id         string
	amount     types.Money
	functional types.Money
	control    types.AccountID
	partner    string
	receivable *apisubledger.Receivable
	payable    *apisubledger.Payable
}) types.Money {
	if line.receivable != nil {
		return line.receivable.RemainingAmount
	}
	return line.payable.RemainingAmount
}

func openItemFunctionalRemaining(line struct {
	id         string
	amount     types.Money
	functional types.Money
	control    types.AccountID
	partner    string
	receivable *apisubledger.Receivable
	payable    *apisubledger.Payable
}) types.Money {
	if line.receivable != nil {
		return line.receivable.FunctionalRemainingAmount
	}
	return line.payable.FunctionalRemainingAmount
}

func openItemStatus(remaining types.Money) string {
	if remaining.IsZero() {
		return "closed"
	}
	return "open"
}

func (s *PublicARAPService) WriteOff(ctx context.Context, command apisubledger.WriteOffCommand) (types.JournalEntryID, error) {
	if uuid.UUID(command.CompanyID) == uuid.Nil || uuid.UUID(command.AdjustmentAccountID) == uuid.Nil ||
		uuid.UUID(command.JournalID) == uuid.Nil || command.OpenItemID == "" ||
		strings.TrimSpace(command.Reference) == "" ||
		command.Amount.IsZero() || command.Amount.IsNegative() ||
		(command.Kind != apisubledger.SubledgerReceivable && command.Kind != apisubledger.SubledgerPayable) {
		return types.JournalEntryID{}, ErrInvalidARAPOperation
	}
	if command.Date.IsZero() {
		command.Date = time.Now().UTC()
	}

	key := arapIdempotencyKey(ctx)
	ids := arapPostingIDs{}
	if key != "" {
		scope := arapScopeKey(string(command.Kind), uuid.UUID(command.CompanyID).String(), key, arapCommandFingerprint(command))
		if derived, ok := s.deterministicIDs(scope, 2); ok {
			ids = derived
		}
	}

	var result types.JournalEntryID
	operationErr := s.withinTransaction(ctx, command.CompanyID, func(txCtx context.Context) error {
		entryID, err := s.writeOffOperation(txCtx, command, ids)
		if err != nil {
			return err
		}
		result = entryID
		return nil
	})
	if operationErr != nil {
		return types.JournalEntryID{}, operationErr
	}
	return result, nil
}

// writeOffOperation posts the balanced write-off entry and reduces the open
// item balance. It runs inside the caller's unit of work so a failed open-item
// update rolls the GL posting back instead of leaving a posted entry with a
// subledger balance that was never reduced.
func (s *PublicARAPService) writeOffOperation(ctx context.Context, command apisubledger.WriteOffCommand, ids arapPostingIDs) (types.JournalEntryID, error) {
	var amountRemaining, functionalRemaining types.Money
	var receivable *apisubledger.Receivable
	var payable *apisubledger.Payable
	var controlID types.AccountID
	var err error
	if command.Kind == apisubledger.SubledgerReceivable {
		line, getErr := s.stores.Receivables.GetReceivable(ctx, command.CompanyID, command.OpenItemID)
		if getErr != nil {
			return types.JournalEntryID{}, getErr
		}
		if line.CompanyID != command.CompanyID {
			return types.JournalEntryID{}, ErrCompanyMismatch
		}
		if line.Status == "closed" {
			return types.JournalEntryID{}, ErrInvalidOpenLine
		}
		receivable, amountRemaining, functionalRemaining, controlID = &line, line.RemainingAmount, line.FunctionalRemainingAmount, line.ControlAccountID
	} else {
		line, getErr := s.stores.Payables.GetPayable(ctx, command.CompanyID, command.OpenItemID)
		if getErr != nil {
			return types.JournalEntryID{}, getErr
		}
		if line.CompanyID != command.CompanyID {
			return types.JournalEntryID{}, ErrCompanyMismatch
		}
		if line.Status == "closed" {
			return types.JournalEntryID{}, ErrInvalidOpenLine
		}
		payable, amountRemaining, functionalRemaining, controlID = &line, line.RemainingAmount, line.FunctionalRemainingAmount, line.ControlAccountID
	}
	functional := command.FunctionalAmount
	if functional.Currency() == "" {
		functional, err = proportionalFunctional(functionalRemaining, amountRemaining, command.Amount)
		if err != nil {
			return types.JournalEntryID{}, err
		}
	}
	if command.Amount.GreaterThan(amountRemaining) || functional.GreaterThan(functionalRemaining) ||
		command.Amount.IsNegative() || command.Amount.IsZero() || functional.IsNegative() || functional.IsZero() ||
		command.Amount.Currency() != amountRemaining.Currency() || functional.Currency() != functionalRemaining.Currency() {
		return types.JournalEntryID{}, ErrInvalidAmount
	}
	rate := functional.Amount().Div(command.Amount.Amount())
	zeroFunctional, err := types.Zero(functional.Currency())
	if err != nil {
		return types.JournalEntryID{}, err
	}
	adjustmentDebit := command.Kind == apisubledger.SubledgerReceivable
	lines := []ledger.EntryLine{
		newPublicLine(ids.line(0), command.AdjustmentAccountID, command.Amount, functional,
			rate, adjustmentDebit, zeroFunctional),
		newPublicLine(ids.line(1), controlID, command.Amount, functional,
			rate, !adjustmentDebit, zeroFunctional),
	}
	date := command.Date.UTC()
	draftCommand := publicapplication.CreateDraftCommand{
		Context: arapCommandContext(ctx, command.CompanyID, s.requirePrincipal), CompanyID: command.CompanyID, JournalID: command.JournalID,
		EffectiveDate: date, PostingDate: date, Description: command.Description,
		SourceModule: "ARAP_WRITEOFF", ReferenceID: command.Reference,
		TransactionCurrency: command.Amount.Currency(), ExchangeRate: rate.String(),
		Classification: ledger.EntryClassRegular, Lines: lines,
	}
	posted, replayed, err := s.postDraft(ctx, draftCommand, ids)
	if err != nil {
		return types.JournalEntryID{}, err
	}
	if !replayed {
		if receivable != nil {
			receivable.RemainingAmount, err = receivable.RemainingAmount.Subtract(command.Amount)
			if err == nil {
				receivable.FunctionalRemainingAmount, err = receivable.FunctionalRemainingAmount.Subtract(functional)
			}
			if err == nil {
				receivable.Status = openItemStatus(receivable.RemainingAmount)
				err = s.stores.Receivables.SaveReceivable(ctx, command.CompanyID, *receivable)
			}
		} else {
			payable.RemainingAmount, err = payable.RemainingAmount.Subtract(command.Amount)
			if err == nil {
				payable.FunctionalRemainingAmount, err = payable.FunctionalRemainingAmount.Subtract(functional)
			}
			if err == nil {
				payable.Status = openItemStatus(payable.RemainingAmount)
				err = s.stores.Payables.SavePayable(ctx, command.CompanyID, *payable)
			}
		}
		if err != nil {
			return types.JournalEntryID{}, fmt.Errorf("%w: %v", ErrPostedARAPSaveFailed, err)
		}
	}
	return posted.ID, nil
}

func (s *PublicARAPService) GetReceivable(ctx context.Context, companyID types.CompanyID, id string) (apisubledger.Receivable, error) {
	if uuid.UUID(companyID) == uuid.Nil || strings.TrimSpace(id) == "" {
		return apisubledger.Receivable{}, ErrInvalidOpenLine
	}
	line, err := s.stores.Receivables.GetReceivable(ctx, companyID, id)
	if err != nil {
		return apisubledger.Receivable{}, err
	}
	if line.CompanyID != companyID {
		return apisubledger.Receivable{}, ErrCompanyMismatch
	}
	return line, nil
}

func (s *PublicARAPService) ListReceivables(ctx context.Context, companyID types.CompanyID, partnerID string) ([]apisubledger.Receivable, error) {
	if uuid.UUID(companyID) == uuid.Nil {
		return nil, ErrCompanyScopeRequired
	}
	lines, err := s.stores.Receivables.ListReceivables(ctx, companyID, partnerID)
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		if line.CompanyID != companyID {
			return nil, ErrCompanyMismatch
		}
	}
	return lines, nil
}

func (s *PublicARAPService) GetPayable(ctx context.Context, companyID types.CompanyID, id string) (apisubledger.Payable, error) {
	if uuid.UUID(companyID) == uuid.Nil || strings.TrimSpace(id) == "" {
		return apisubledger.Payable{}, ErrInvalidOpenLine
	}
	line, err := s.stores.Payables.GetPayable(ctx, companyID, id)
	if err != nil {
		return apisubledger.Payable{}, err
	}
	if line.CompanyID != companyID {
		return apisubledger.Payable{}, ErrCompanyMismatch
	}
	return line, nil
}

func (s *PublicARAPService) ListPayables(ctx context.Context, companyID types.CompanyID, partnerID string) ([]apisubledger.Payable, error) {
	if uuid.UUID(companyID) == uuid.Nil {
		return nil, ErrCompanyScopeRequired
	}
	lines, err := s.stores.Payables.ListPayables(ctx, companyID, partnerID)
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		if line.CompanyID != companyID {
			return nil, ErrCompanyMismatch
		}
	}
	return lines, nil
}

func (s *PublicARAPService) Aging(ctx context.Context, companyID types.CompanyID, kind apisubledger.SubledgerType, partnerID string, asOf time.Time) ([]apisubledger.AgingBucket, error) {
	if uuid.UUID(companyID) == uuid.Nil {
		return nil, ErrCompanyScopeRequired
	}
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	var err error
	values := make([]agingValue, 0)
	if kind == apisubledger.SubledgerReceivable {
		lines, listErr := s.stores.Receivables.ListReceivables(ctx, companyID, partnerID)
		err = listErr
		for _, line := range lines {
			if line.CompanyID != companyID {
				return nil, ErrCompanyMismatch
			}
			values = append(values, agingValue{dueDate: line.DueDate, remaining: line.RemainingAmount, status: line.Status})
		}
	} else if kind == apisubledger.SubledgerPayable {
		lines, listErr := s.stores.Payables.ListPayables(ctx, companyID, partnerID)
		err = listErr
		for _, line := range lines {
			if line.CompanyID != companyID {
				return nil, ErrCompanyMismatch
			}
			values = append(values, agingValue{dueDate: line.DueDate, remaining: line.RemainingAmount, status: line.Status})
		}
	} else {
		return nil, ErrInvalidARAPOperation
	}
	if err != nil {
		return nil, err
	}
	type bucketKey struct{ label, currency string }
	totals := make(map[bucketKey]types.Money)
	counts := make(map[bucketKey]int)
	for _, value := range values {
		if value.remaining.Currency() == "" || value.remaining.IsZero() || value.status == "closed" {
			continue
		}
		label := publicAgingLabel(asOf, value.dueDate)
		key := bucketKey{label: label, currency: value.remaining.Currency()}
		total, ok := totals[key]
		if !ok {
			total, err = types.Zero(value.remaining.Currency())
			if err != nil {
				return nil, err
			}
		}
		totals[key], err = total.Add(value.remaining)
		if err != nil {
			return nil, err
		}
		counts[key]++
	}
	result := make([]apisubledger.AgingBucket, 0, len(totals))
	for key, total := range totals {
		result = append(result, apisubledger.AgingBucket{
			RangeLabel: key.label, Currency: key.currency, TotalAmount: total.Amount(), Count: counts[key],
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Currency == result[j].Currency {
			return result[i].RangeLabel < result[j].RangeLabel
		}
		return result[i].Currency < result[j].Currency
	})
	return result, nil
}

type agingValue struct {
	dueDate   time.Time
	remaining types.Money
	status    string
}

func publicAgingLabel(asOf, dueDate time.Time) string {
	if dueDate.IsZero() || !dueDate.Before(asOf) {
		return "current"
	}
	days := int(asOf.Sub(dueDate) / (24 * time.Hour))
	switch {
	case days <= 30:
		return "1_30"
	case days <= 60:
		return "31_60"
	case days <= 90:
		return "61_90"
	default:
		return "over_90"
	}
}

func (s *PublicARAPService) Reconcile(ctx context.Context, companyID types.CompanyID, kind apisubledger.SubledgerType, controlID types.AccountID, currency string, asOf time.Time) (apisubledger.ReconciliationEvidence, error) {
	if uuid.UUID(companyID) == uuid.Nil || uuid.UUID(controlID) == uuid.Nil || strings.TrimSpace(currency) == "" {
		return apisubledger.ReconciliationEvidence{}, ErrInvalidARAPOperation
	}
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	now := time.Now().UTC()
	if asOf.After(now) || asOf.UTC().Year() != now.Year() || asOf.UTC().YearDay() != now.YearDay() {
		return apisubledger.ReconciliationEvidence{}, ErrARAPHistoryUnavailable
	}
	var total decimal.Decimal
	switch kind {
	case apisubledger.SubledgerReceivable:
		lines, err := s.stores.Receivables.ListReceivables(ctx, companyID, "")
		if err != nil {
			return apisubledger.ReconciliationEvidence{}, err
		}
		for _, line := range lines {
			if line.CompanyID != companyID {
				return apisubledger.ReconciliationEvidence{}, ErrCompanyMismatch
			}
			if line.ControlAccountID != controlID || line.Status == "closed" {
				continue
			}
			amount, err := functionalRemaining(line.FunctionalRemainingAmount, line.RemainingAmount, currency)
			if err != nil {
				return apisubledger.ReconciliationEvidence{}, err
			}
			total = total.Add(amount.Amount())
		}
	case apisubledger.SubledgerPayable:
		lines, err := s.stores.Payables.ListPayables(ctx, companyID, "")
		if err != nil {
			return apisubledger.ReconciliationEvidence{}, err
		}
		for _, line := range lines {
			if line.CompanyID != companyID {
				return apisubledger.ReconciliationEvidence{}, ErrCompanyMismatch
			}
			if line.ControlAccountID != controlID || line.Status == "closed" {
				continue
			}
			amount, err := functionalRemaining(line.FunctionalRemainingAmount, line.RemainingAmount, currency)
			if err != nil {
				return apisubledger.ReconciliationEvidence{}, err
			}
			total = total.Add(amount.Amount())
		}
	default:
		return apisubledger.ReconciliationEvidence{}, ErrInvalidARAPOperation
	}
	gl, err := s.stores.Balance.GetAccountBalance(ctx, companyID, controlID, asOf, currency)
	if err != nil {
		return apisubledger.ReconciliationEvidence{}, err
	}
	if kind == apisubledger.SubledgerPayable {
		// The BalanceReader reports debit-minus-credit; AP balances are
		// naturally credit-normal while subledger residuals are positive.
		gl = gl.Neg()
	}
	difference := total.Sub(gl)
	return apisubledger.ReconciliationEvidence{
		CompanyID: companyID, Kind: kind, ControlAccountID: controlID,
		Currency: currency, AsOf: asOf, SubledgerBalance: total, GLBalance: gl,
		Difference: difference, Reconciled: difference.IsZero(), EvidenceAt: now,
	}, nil
}

func functionalRemaining(functional, remaining types.Money, currency string) (types.Money, error) {
	if functional.Currency() != "" {
		if functional.Currency() != currency {
			return types.Money{}, ErrReconciliationFunctionalAmountMissing
		}
		return functional, nil
	}
	if remaining.Currency() != currency {
		return types.Money{}, ErrReconciliationFunctionalAmountMissing
	}
	return remaining, nil
}
