package governance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	financialtypes "accounting_engine/internal/types"

	"github.com/google/uuid"
)

type memoryAuditRepository struct {
	mu   sync.Mutex
	logs []*FinancialAuditLog
}

func copyAuditLog(log FinancialAuditLog) FinancialAuditLog {
	log.PayloadBefore = append([]byte(nil), log.PayloadBefore...)
	log.PayloadAfter = append([]byte(nil), log.PayloadAfter...)
	return log
}

func (r *memoryAuditRepository) AppendAuditLog(ctx context.Context, log FinancialAuditLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	log.ID = int64(len(r.logs) + 1)
	copyOfLog := copyAuditLog(log)
	r.logs = append(r.logs, &copyOfLog)
	return nil
}

func (r *memoryAuditRepository) GetLastAuditLog(ctx context.Context) (*FinancialAuditLog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.logs) == 0 {
		return nil, nil
	}
	log := copyAuditLog(*r.logs[len(r.logs)-1])
	return &log, nil
}

func (r *memoryAuditRepository) IterateAuditLogs(ctx context.Context, fromID int64, handler AuditLogHandler) error {
	r.mu.Lock()
	logs := make([]FinancialAuditLog, 0, len(r.logs))
	for _, log := range r.logs {
		if log.ID >= fromID {
			logs = append(logs, copyAuditLog(*log))
		}
	}
	r.mu.Unlock()
	sort.Slice(logs, func(i, j int) bool { return logs[i].ID < logs[j].ID })
	for _, log := range logs {
		if err := handler(log); err != nil {
			return err
		}
	}
	return nil
}

func newAuditLog(action string, payload []byte) FinancialAuditLog {
	return FinancialAuditLog{
		EntityType:    "move",
		EntityID:      uuid.New(),
		Action:        action,
		ActorID:       uuid.New(),
		PayloadAfter:  payload,
		PayloadBefore: []byte("null"),
		Timestamp:     time.Now().UTC().Truncate(time.Millisecond),
	}
}

func TestAuditChainHashesAndVerifies(t *testing.T) {
	repository := &memoryAuditRepository{}
	chain := NewAuditChain(repository)

	for i := 0; i < 3; i++ {
		log := newAuditLog("POST", fmt.Appendf(nil, `{"amount":"%d"}`, i+1))
		if err := chain.Append(context.Background(), log); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	valid, err := chain.VerifyIntegrity(context.Background())
	if err != nil || !valid {
		t.Fatalf("VerifyIntegrity() = (%v, %v), want (true, nil)", valid, err)
	}
	if repository.logs[0].PreviousLogHash != GenesisHash {
		t.Fatalf("genesis previous hash = %q", repository.logs[0].PreviousLogHash)
	}
	for i := 1; i < len(repository.logs); i++ {
		if repository.logs[i].PreviousLogHash != repository.logs[i-1].CurrentLogHash {
			t.Fatalf("log %d is not linked to log %d", i+1, i)
		}
	}
}

func TestAuditChainDetectsPayloadTampering(t *testing.T) {
	repository := &memoryAuditRepository{}
	chain := NewAuditChain(repository)
	for i := 0; i < 3; i++ {
		if err := chain.Append(context.Background(), newAuditLog("POST", []byte(fmt.Sprintf(`{"amount":"%d"}`, i+1)))); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	repository.logs[1].PayloadAfter = append(repository.logs[1].PayloadAfter, '9')
	valid, err := chain.VerifyIntegrity(context.Background())
	if valid {
		t.Fatal("VerifyIntegrity() accepted tampered payload")
	}
	if !errors.Is(err, ErrAuditChainBroken) {
		t.Fatalf("VerifyIntegrity() error = %v, want ErrAuditChainBroken", err)
	}
	var integrityErr *AuditIntegrityError
	if !errors.As(err, &integrityErr) || integrityErr.LogID != 2 {
		t.Fatalf("VerifyIntegrity() error = %#v, want log ID 2", err)
	}
}

func TestAuditChainIsolatedPerCompany(t *testing.T) {
	repository := &memoryAuditRepository{}
	chain := NewAuditChain(repository)
	companyA, companyB := uuid.New(), uuid.New()
	first := newAuditLog("POST", []byte(`{"amount":"10"}`))
	first.CompanyID = companyA
	second := newAuditLog("POST", []byte(`{"amount":"20"}`))
	second.CompanyID = companyB
	third := newAuditLog("REVERSE", []byte(`{"amount":"10"}`))
	third.CompanyID = companyA
	for _, log := range []FinancialAuditLog{first, second, third} {
		if err := chain.Append(context.Background(), log); err != nil {
			t.Fatal(err)
		}
	}
	if repository.logs[0].PreviousLogHash != GenesisHash || repository.logs[1].PreviousLogHash != GenesisHash {
		t.Fatal("each company audit chain must start with the genesis hash")
	}
	if repository.logs[2].PreviousLogHash != repository.logs[0].CurrentLogHash {
		t.Fatal("audit record did not continue its company's chain")
	}
	valid, err := chain.VerifyIntegrity(context.Background())
	if err != nil || !valid {
		t.Fatalf("VerifyIntegrity() = (%v, %v), want (true, nil)", valid, err)
	}
}

func TestSelfApprovalIsProhibitedAboveThreshold(t *testing.T) {
	threshold, err := financialtypes.NewMoneyFromString("1000", "USD")
	if err != nil {
		t.Fatal(err)
	}
	amount, err := financialtypes.NewMoneyFromString("1000.01", "USD")
	if err != nil {
		t.Fatal(err)
	}
	actor := uuid.New()

	err = ValidatePostingApproval(actor, actor, amount, threshold)
	if !errors.Is(err, ErrSelfApprovalProhibited) {
		t.Fatalf("ValidatePostingApproval() error = %v", err)
	}
	err = ValidatePostingApproval(actor, uuid.New(), amount, threshold)
	if err != nil {
		t.Fatalf("different approver returned error: %v", err)
	}
}

func TestAnnualCloseRequiresController(t *testing.T) {
	if err := ValidateAnnualClose(uuid.New(), true, []Permission{PermissionPost}); !errors.Is(err, ErrFinancialControllerRequired) {
		t.Fatalf("missing controller error = %v", err)
	}
	if err := ValidateAnnualClose(uuid.New(), true, []Permission{PermissionController}); err != nil {
		t.Fatalf("controller permission returned error: %v", err)
	}
}

func TestBankDutySegregation(t *testing.T) {
	actor := uuid.New()
	if err := ValidateBankDutySegregation(actor, actor); !errors.Is(err, ErrBankDutySegregationViolation) {
		t.Fatalf("shared bank duty error = %v", err)
	}
	if err := ValidateBankDutySegregation(actor, uuid.New()); err != nil {
		t.Fatalf("separate bank duties returned error: %v", err)
	}
}
