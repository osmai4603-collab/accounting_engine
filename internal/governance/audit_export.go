package governance

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidAuditExport  = errors.New("invalid audit export signature")
	ErrAuditAnchorMismatch = errors.New("audit anchor does not match the audit chain")
)

type AuditDayAnchor struct {
	CompanyID uuid.UUID `json:"company_id"`
	Date      string    `json:"date"`
	Entries   int64     `json:"entries"`
	RootHash  string    `json:"root_hash"`
}

type AuditExportRepository interface {
	GetDayFinalHash(ctx context.Context, companyID uuid.UUID, date time.Time) (string, int64, error)
	VerifyDay(ctx context.Context, companyID uuid.UUID, date time.Time) error
}

type WORMStorageClient interface {
	PutImmutableObject(ctx context.Context, key string, object []byte, retainUntil time.Time) error
	GetImmutableObject(ctx context.Context, key string) ([]byte, error)
}

type AuditExporter struct {
	repository AuditExportRepository
	storage    WORMStorageClient
	privateKey ed25519.PrivateKey
	retention  time.Duration
}

func NewAuditExporter(repository AuditExportRepository, storage WORMStorageClient, privateKey ed25519.PrivateKey, retention time.Duration) (*AuditExporter, error) {
	if repository == nil || storage == nil || len(privateKey) != ed25519.PrivateKeySize || retention <= 0 {
		return nil, errors.New("audit exporter requires repository, WORM storage, signing key, and retention")
	}
	return &AuditExporter{repository: repository, storage: storage, privateKey: append(ed25519.PrivateKey(nil), privateKey...), retention: retention}, nil
}

func (e *AuditExporter) ExportDailyAnchor(ctx context.Context, companyID uuid.UUID, date time.Time) error {
	if companyID == uuid.Nil {
		return errors.New("audit export requires company id")
	}
	date = date.UTC().Truncate(24 * time.Hour)
	if err := e.repository.VerifyDay(ctx, companyID, date); err != nil {
		return fmt.Errorf("verify audit chain before export: %w", err)
	}
	rootHash, count, err := e.repository.GetDayFinalHash(ctx, companyID, date)
	if err != nil {
		return err
	}
	if count < 0 || (count > 0 && rootHash == "") {
		return errors.New("audit repository returned an invalid daily anchor")
	}
	anchor := AuditDayAnchor{CompanyID: companyID, Date: date.Format("2006-01-02"), Entries: count, RootHash: rootHash}
	payload, err := json.Marshal(anchor)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(payload)
	signature := ed25519.Sign(e.privateKey, digest[:])
	envelope, err := json.Marshal(struct {
		Anchor    AuditDayAnchor `json:"anchor"`
		Signature string         `json:"signature"`
	}{Anchor: anchor, Signature: hex.EncodeToString(signature)})
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%s/%s/audit-anchor.json", companyID, anchor.Date)
	return e.storage.PutImmutableObject(ctx, key, envelope, date.Add(24*time.Hour).Add(e.retention))
}

func VerifyAuditAnchor(object []byte, publicKey ed25519.PublicKey) (AuditDayAnchor, error) {
	var envelope struct {
		Anchor    AuditDayAnchor `json:"anchor"`
		Signature string         `json:"signature"`
	}
	if err := json.Unmarshal(object, &envelope); err != nil {
		return AuditDayAnchor{}, err
	}
	signature, err := hex.DecodeString(envelope.Signature)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return AuditDayAnchor{}, ErrInvalidAuditExport
	}
	payload, err := json.Marshal(envelope.Anchor)
	if err != nil {
		return AuditDayAnchor{}, err
	}
	digest := sha256.Sum256(payload)
	if !ed25519.Verify(publicKey, digest[:], signature) {
		return AuditDayAnchor{}, ErrInvalidAuditExport
	}
	return envelope.Anchor, nil
}

func (e *AuditExporter) VerifyDailyAnchor(ctx context.Context, companyID uuid.UUID, date time.Time) error {
	if e == nil || e.repository == nil || e.storage == nil ||
		len(e.privateKey) != ed25519.PrivateKeySize || companyID == uuid.Nil {
		return ErrInvalidAuditExport
	}
	date = date.UTC().Truncate(24 * time.Hour)
	key := fmt.Sprintf("%s/%s/audit-anchor.json", companyID, date.Format("2006-01-02"))
	object, err := e.storage.GetImmutableObject(ctx, key)
	if err != nil {
		return fmt.Errorf("read immutable audit anchor: %w", err)
	}
	anchor, err := VerifyAuditAnchor(object, e.privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		return err
	}
	if anchor.CompanyID != companyID || anchor.Date != date.Format("2006-01-02") {
		return ErrAuditAnchorMismatch
	}
	if err := e.repository.VerifyDay(ctx, companyID, date); err != nil {
		return fmt.Errorf("verify audit chain: %w", err)
	}
	rootHash, count, err := e.repository.GetDayFinalHash(ctx, companyID, date)
	if err != nil {
		return err
	}
	if anchor.RootHash != rootHash || anchor.Entries != count {
		return ErrAuditAnchorMismatch
	}
	return nil
}

func (e *AuditExporter) RunDaily(ctx context.Context, companyID uuid.UUID) error {
	if e == nil || e.repository == nil || e.storage == nil ||
		len(e.privateKey) != ed25519.PrivateKeySize || e.retention <= 0 || companyID == uuid.Nil {
		return ErrInvalidAuditExport
	}
	for {
		now := time.Now().UTC()
		nextMidnight := now.Truncate(24 * time.Hour).Add(24 * time.Hour)
		timer := time.NewTimer(time.Until(nextMidnight))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if err := e.ExportDailyAnchor(ctx, companyID, nextMidnight.Add(-24*time.Hour)); err != nil {
			return err
		}
		if err := e.VerifyDailyAnchor(ctx, companyID, nextMidnight.Add(-24*time.Hour)); err != nil {
			return err
		}
	}
}
