package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"accounting_engine/internal/policy"
	"github.com/google/uuid"
)

func policyKey(companyID uuid.UUID, kind policy.PolicyKind) string {
	return companyID.String() + ":" + string(kind)
}

func (d *Driver) SavePolicy(ctx context.Context, version policy.VersionedPolicy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := policy.ValidateVersionedPolicy(version); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key := policyKey(version.Header.CompanyID, version.Kind)
	versions := d.policies[key]
	expected := 1
	if len(versions) > 0 {
		previous := versions[len(versions)-1]
		expected = previous.Header.Version + 1
		if previous.Header.PolicyID != version.Header.PolicyID {
			return policy.ErrInvalidPolicy
		}
		if !version.Header.EffectiveFrom.After(previous.Header.EffectiveFrom) {
			return policy.ErrPolicyConflict
		}
		if previous.Header.EffectiveTo != nil && version.Header.EffectiveFrom.Before(*previous.Header.EffectiveTo) {
			return policy.ErrPolicyConflict
		}
	}
	if version.Header.Version != expected {
		return fmt.Errorf("%w: expected %d", policy.ErrPolicyVersionConflict, expected)
	}
	version.Header.EffectiveFrom = version.Header.EffectiveFrom.UTC()
	if version.Header.EffectiveTo != nil {
		end := version.Header.EffectiveTo.UTC()
		version.Header.EffectiveTo = &end
	}
	version.Payload = append([]byte(nil), version.Payload...)
	d.policies[key] = append(versions, version)
	return nil
}

func (d *Driver) GetPolicyAsOf(ctx context.Context, companyID uuid.UUID, kind policy.PolicyKind, asOf time.Time) (policy.VersionedPolicy, error) {
	if err := ctx.Err(); err != nil {
		return policy.VersionedPolicy{}, err
	}
	if companyID == uuid.Nil || asOf.IsZero() {
		return policy.VersionedPolicy{}, policy.ErrInvalidPolicy
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	versions := d.policies[policyKey(companyID, kind)]
	for i := len(versions) - 1; i >= 0; i-- {
		version := versions[i]
		if asOf.Before(version.Header.EffectiveFrom) {
			continue
		}
		if version.Header.EffectiveTo != nil && !asOf.Before(*version.Header.EffectiveTo) {
			return policy.VersionedPolicy{}, policy.ErrPolicyNotFound
		}
		version.Payload = append([]byte(nil), version.Payload...)
		return version, nil
	}
	return policy.VersionedPolicy{}, errors.Join(policy.ErrPolicyNotFound, fmt.Errorf("company %s, kind %s", companyID, kind))
}

func (d *Driver) ListPolicies(ctx context.Context, companyID uuid.UUID, kind policy.PolicyKind) ([]policy.VersionedPolicy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	versions := append([]policy.VersionedPolicy(nil), d.policies[policyKey(companyID, kind)]...)
	for i := range versions {
		versions[i].Payload = append([]byte(nil), versions[i].Payload...)
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Header.Version < versions[j].Header.Version
	})
	return versions, nil
}
