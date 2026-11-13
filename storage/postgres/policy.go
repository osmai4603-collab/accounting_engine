package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/osmai4603-collab/accounting_engine/internal/policy"
)

func (d *Driver) SavePolicy(ctx context.Context, version policy.VersionedPolicy) error {
	header := version.Header
	if err := policy.ValidateVersionedPolicy(version); err != nil {
		return err
	}
	return d.WithCompanyTx(ctx, header.CompanyID, func(tx pgx.Tx) error {
		var currentVersion int
		var currentPolicyID uuid.UUID
		var currentEffectiveFrom time.Time
		var currentEffectiveTo *time.Time
		err := tx.QueryRow(ctx, `SELECT version, policy_id, effective_from, effective_to
			FROM accounting_policy_versions
			WHERE company_id=$1 AND policy_kind=$2
			ORDER BY version DESC LIMIT 1`,
			header.CompanyID, version.Kind).Scan(&currentVersion, &currentPolicyID, &currentEffectiveFrom, &currentEffectiveTo)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		expected := 1
		if err == nil {
			expected = currentVersion + 1
			if currentPolicyID != header.PolicyID {
				return policy.ErrInvalidPolicy
			}
			if !header.EffectiveFrom.After(currentEffectiveFrom) {
				return policy.ErrPolicyConflict
			}
			if currentEffectiveTo != nil && header.EffectiveFrom.Before(*currentEffectiveTo) {
				return policy.ErrPolicyConflict
			}
		}
		if header.Version != expected {
			return fmt.Errorf("%w: expected %d", policy.ErrPolicyVersionConflict, expected)
		}
		createdAt := header.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		_, err = tx.Exec(ctx, `INSERT INTO accounting_policy_versions(
			company_id, policy_id, policy_kind, version, effective_from, effective_to,
			created_by, approved_by, created_at, payload
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			header.CompanyID, header.PolicyID, version.Kind, header.Version,
			header.EffectiveFrom.UTC(), header.EffectiveTo, header.CreatedBy, header.ApprovedBy,
			createdAt.UTC(), []byte(version.Payload))
		if err != nil {
			return err
		}
		return nil
	})
}

func (d *Driver) GetPolicyAsOf(
	ctx context.Context,
	companyID uuid.UUID,
	kind policy.PolicyKind,
	asOf time.Time,
) (result policy.VersionedPolicy, resultErr error) {
	if companyID == uuid.Nil || asOf.IsZero() {
		return result, policy.ErrInvalidPolicy
	}
	err := d.WithCompanyTx(ctx, companyID, func(tx pgx.Tx) error {
		var header policy.EffectivePolicyHeader
		var payload []byte
		var storedKind string
		err := tx.QueryRow(ctx, `SELECT policy_id, company_id, version, effective_from,
			effective_to, created_by, approved_by, created_at, policy_kind, payload
			FROM accounting_policy_versions
			WHERE company_id=$1 AND policy_kind=$2 AND effective_from <= $3
			ORDER BY effective_from DESC, version DESC LIMIT 1`,
			companyID, kind, asOf.UTC()).Scan(
			&header.PolicyID, &header.CompanyID, &header.Version, &header.EffectiveFrom,
			&header.EffectiveTo, &header.CreatedBy, &header.ApprovedBy, &header.CreatedAt,
			&storedKind, &payload)
		if errors.Is(err, pgx.ErrNoRows) {
			return policy.ErrPolicyNotFound
		}
		if err != nil {
			return err
		}
		if header.EffectiveTo != nil && !asOf.UTC().Before(header.EffectiveTo.UTC()) {
			return policy.ErrPolicyNotFound
		}
		header.EffectiveFrom = header.EffectiveFrom.UTC()
		if header.EffectiveTo != nil {
			end := header.EffectiveTo.UTC()
			header.EffectiveTo = &end
		}
		result = policy.VersionedPolicy{Header: header, Kind: policy.PolicyKind(storedKind), Payload: payload}
		return nil
	})
	return result, err
}
