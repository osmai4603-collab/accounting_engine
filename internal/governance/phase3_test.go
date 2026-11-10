package governance

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"accounting_engine/internal/posting"
	financialtypes "accounting_engine/internal/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type auditExportRepositoryStub struct {
	rootHash string
	count    int64
}

func (r *auditExportRepositoryStub) GetDayFinalHash(context.Context, uuid.UUID, time.Time) (string, int64, error) {
	return r.rootHash, r.count, nil
}

func (r *auditExportRepositoryStub) VerifyDay(context.Context, uuid.UUID, time.Time) error {
	return nil
}

type makerCheckerPolicyStub struct {
	threshold  decimal.Decimal
	configured bool
}

func (s makerCheckerPolicyStub) MakerCheckerThreshold(context.Context, uuid.UUID) (decimal.Decimal, bool, error) {
	return s.threshold, s.configured, nil
}

type wormStorageStub struct {
	objects map[string][]byte
}

func (s *wormStorageStub) PutImmutableObject(_ context.Context, key string, object []byte, _ time.Time) error {
	if s.objects[key] != nil {
		return fmt.Errorf("object already exists: %s", key)
	}
	s.objects[key] = append([]byte(nil), object...)
	return nil
}

func (s *wormStorageStub) GetImmutableObject(_ context.Context, key string) ([]byte, error) {
	object, ok := s.objects[key]
	if !ok {
		return nil, errors.New("object not found")
	}
	return append([]byte(nil), object...), nil
}

func TestApprovalHierarchyEnforcesRoleOrderAndSeparation(t *testing.T) {
	ctx := context.Background()
	companyID, entryID, maker := uuid.New(), uuid.New(), uuid.New()
	users := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	roles := []Role{RoleFinanceManager, RoleController, RoleCFO}
	authorizations := NewMemoryAuthorizationRepository()
	for i := range users {
		if err := authorizations.GrantRole(RoleAssignment{CompanyID: companyID, UserID: users[i], Role: roles[i]}); err != nil {
			t.Fatal(err)
		}
	}
	amount, err := financialtypes.NewMoneyFromString("500001", "USD")
	if err != nil {
		t.Fatal(err)
	}
	repository := NewMemoryApprovalRepository()
	entry, err := posting.NewJournalEntry(uuid.New(), time.Now().UTC(), maker.String(), []posting.JournalEntryLine{
		{ID: uuid.New(), AccountID: uuid.New(), Debit: amount},
		{ID: uuid.New(), AccountID: uuid.New(), Credit: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry.ID = entryID
	entry.CompanyID = companyID
	repository.SaveEntryForApproval(entry)
	engine := NewApprovalEngine(repository, NewAuthorizer(authorizations), nil)
	request, err := engine.Start(ctx, companyID, entryID, maker, "general", amount, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Steps) != 3 {
		t.Fatalf("approval steps = %d, want 3", len(request.Steps))
	}
	if _, err := engine.Approve(ctx, companyID, request.ID, maker, time.Time{}); !errors.Is(err, ErrSelfApprovalProhibited) {
		t.Fatalf("maker approval error = %v, want self-approval rejection", err)
	}
	if _, err := engine.Approve(ctx, companyID, request.ID, users[1], time.Time{}); !errors.Is(err, ErrApprovalRoleRequired) {
		t.Fatalf("out-of-order approval error = %v, want role rejection", err)
	}
	for i := range users {
		if _, err := engine.Approve(ctx, companyID, request.ID, users[i], time.Time{}); err != nil {
			t.Fatalf("approval step %d: %v", i+1, err)
		}
	}
	approved, err := engine.IsApproved(ctx, companyID, entryID)
	if err != nil || !approved {
		t.Fatalf("approval status = %v, %v; want approved", approved, err)
	}
	audit := repository.ApprovalAuditRecords()
	if len(audit) != 3 {
		t.Fatalf("audited approvals = %d, want 3", len(audit))
	}
	for i := range users {
		if audit[i].ActorID != users[i] || audit[i].CompanyID != companyID || audit[i].EntityID != entryID {
			t.Fatalf("approval audit record %d = %+v", i, audit[i])
		}
	}
}

func TestApprovalRuleSupportsJournalSpecificTiers(t *testing.T) {
	amount, _ := financialtypes.NewMoneyFromString("100", "USD")
	engine := NewApprovalEngineWithMatrix(nil, nil, nil, []ApprovalRule{
		{JournalType: "payroll", Minimum: decimal.Zero, Roles: []Role{RoleController}},
	})
	steps, err := engine.approvalSteps(amount, "payroll")
	if err != nil || len(steps) != 1 || steps[0].Role != RoleController {
		t.Fatalf("journal-specific rule = %#v, %v", steps, err)
	}
	if _, err := engine.approvalSteps(amount, "general"); err == nil {
		t.Fatal("expected unmatched journal type to fail")
	}
}

func TestApprovalHierarchyUsesInclusiveUpperTierBoundary(t *testing.T) {
	engine := NewApprovalEngine(nil, nil, nil)
	atLimit, err := financialtypes.NewMoneyFromString("500000", "USD")
	if err != nil {
		t.Fatal(err)
	}
	aboveLimit, err := financialtypes.NewMoneyFromString("500001", "USD")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := engine.approvalSteps(atLimit, "general")
	if err != nil || len(steps) != 2 {
		t.Fatalf("approval steps at 500000 = %d, %v; want 2", len(steps), err)
	}
	steps, err = engine.approvalSteps(aboveLimit, "general")
	if err != nil || len(steps) != 3 {
		t.Fatalf("approval steps above 500000 = %d, %v; want 3", len(steps), err)
	}
}

func TestApprovalGateFailsClosedWhenPolicyHasNoApprovalRepository(t *testing.T) {
	gate := &ApprovalGate{
		policy: makerCheckerPolicyStub{threshold: decimal.NewFromInt(100), configured: true},
	}
	approved, err := gate.IsApproved(context.Background(), uuid.New(), uuid.New())
	if approved || !errors.Is(err, ErrApprovalRepositoryRequired) {
		t.Fatalf("approval gate result = %v, %v; want false and missing repository error", approved, err)
	}
}

func TestAuditExportSignatureDetectsChanges(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	anchor := AuditDayAnchor{
		CompanyID: uuid.New(), Date: "2026-11-10", Entries: 8, RootHash: "root-hash",
	}
	payload, err := json.Marshal(anchor)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	signature := hex.EncodeToString(ed25519.Sign(privateKey, digest[:]))
	object, err := json.Marshal(struct {
		Anchor    AuditDayAnchor `json:"anchor"`
		Signature string         `json:"signature"`
	}{Anchor: anchor, Signature: signature})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := VerifyAuditAnchor(object, publicKey); err != nil || got != anchor {
		t.Fatalf("verify export = %#v, %v", got, err)
	}
	anchor.Entries++
	tampered, err := json.Marshal(struct {
		Anchor    AuditDayAnchor `json:"anchor"`
		Signature string         `json:"signature"`
	}{Anchor: anchor, Signature: signature})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAuditAnchor(tampered, publicKey); !errors.Is(err, ErrInvalidAuditExport) {
		t.Fatalf("tampered export error = %v, want signature rejection", err)
	}
}

func TestAuditExporterVerifiesExternalAnchorAgainstChain(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	repository := &auditExportRepositoryStub{rootHash: "root-hash", count: 8}
	storage := &wormStorageStub{objects: make(map[string][]byte)}
	exporter, err := NewAuditExporter(repository, storage, privateKey, 365*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	companyID := uuid.New()
	date := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	if err := exporter.ExportDailyAnchor(context.Background(), companyID, date); err != nil {
		t.Fatal(err)
	}
	if err := exporter.VerifyDailyAnchor(context.Background(), companyID, date); err != nil {
		t.Fatalf("verify exported anchor: %v", err)
	}
	repository.rootHash = "changed-root"
	if err := exporter.VerifyDailyAnchor(context.Background(), companyID, date); !errors.Is(err, ErrAuditAnchorMismatch) {
		t.Fatalf("verification after chain change = %v, want mismatch", err)
	}
}
