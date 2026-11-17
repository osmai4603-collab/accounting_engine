package reporting

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	publicreporting "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
)

var (
	ErrInvalidDisclosureQuery        = errors.New("reporting: invalid disclosure query")
	ErrInvalidDisclosure             = errors.New("reporting: invalid structured disclosure")
	ErrDisclosureProviderUnavailable = errors.New("reporting: no disclosure providers configured")
	ErrDuplicateDisclosure           = errors.New("reporting: duplicate structured disclosure")
)

type DisclosureRegistry struct {
	providers []standards.DisclosureProvider
}

func NewDisclosureRegistry(providers []standards.DisclosureProvider) (*DisclosureRegistry, error) {
	ordered := append([]standards.DisclosureProvider(nil), providers...)
	seen := make(map[string]struct{}, len(ordered))
	for _, provider := range ordered {
		if provider == nil {
			return nil, errors.New("reporting: disclosure provider is nil")
		}
		name := strings.TrimSpace(provider.Name())
		if name == "" {
			return nil, errors.New("reporting: disclosure provider name is required")
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("reporting: duplicate disclosure provider %q", name)
		}
		seen[name] = struct{}{}
	}
	sort.Slice(ordered, func(i, j int) bool {
		return strings.TrimSpace(ordered[i].Name()) < strings.TrimSpace(ordered[j].Name())
	})
	return &DisclosureRegistry{providers: ordered}, nil
}

func (r *DisclosureRegistry) HasProviders() bool {
	return r != nil && len(r.providers) > 0
}

func (r *DisclosureRegistry) GetDisclosures(
	ctx context.Context,
	query publicreporting.DisclosureQuery,
) ([]publicreporting.StructuredDisclosure, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidDisclosureQuery)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query, err := normalizeDisclosureQuery(query)
	if err != nil {
		return nil, err
	}
	if !r.HasProviders() {
		return nil, ErrDisclosureProviderUnavailable
	}

	result := make([]publicreporting.StructuredDisclosure, 0)
	identities := make(map[string]struct{})
	for _, provider := range r.providers {
		provided, err := provider.GetDisclosures(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("reporting: disclosure provider %q: %w", provider.Name(), err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, disclosure := range provided {
			disclosure, err = normalizeDisclosure(disclosure, query)
			if err != nil {
				return nil, fmt.Errorf("reporting: disclosure provider %q: %w", provider.Name(), err)
			}
			identity, err := disclosureIdentity(disclosure)
			if err != nil {
				return nil, fmt.Errorf("%w: encode disclosure identity: %v", ErrInvalidDisclosure, err)
			}
			if _, exists := identities[identity]; exists {
				return nil, fmt.Errorf("%w: %s/%s/%s", ErrDuplicateDisclosure,
					disclosure.StandardReference, disclosure.Code, disclosure.Version)
			}
			identities[identity] = struct{}{}
			result = append(result, disclosure)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if left.StandardReference != right.StandardReference {
			return left.StandardReference < right.StandardReference
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		if left.Version != right.Version {
			return left.Version < right.Version
		}
		return left.Currency < right.Currency
	})
	return result, nil
}

func normalizeDisclosureQuery(query publicreporting.DisclosureQuery) (publicreporting.DisclosureQuery, error) {
	if query.CompanyID == (types.CompanyID{}) {
		return query, fmt.Errorf("%w: company is required", ErrInvalidDisclosureQuery)
	}
	if query.Period.StartDate.IsZero() || query.Period.EndDate.IsZero() {
		return query, fmt.Errorf("%w: period start and end dates are required", ErrInvalidDisclosureQuery)
	}
	query.Period.StartDate = normalizeDisclosureDate(query.Period.StartDate)
	query.Period.EndDate = normalizeDisclosureDate(query.Period.EndDate)
	if query.Period.EndDate.Before(query.Period.StartDate) {
		return query, fmt.Errorf("%w: period end precedes period start", ErrInvalidDisclosureQuery)
	}
	query.Currency = strings.ToUpper(strings.TrimSpace(query.Currency))
	query.StandardReference = strings.TrimSpace(query.StandardReference)
	query.Version = strings.TrimSpace(query.Version)
	return query, nil
}

func normalizeDisclosure(disclosure publicreporting.StructuredDisclosure, query publicreporting.DisclosureQuery) (publicreporting.StructuredDisclosure, error) {
	disclosure.Code = strings.TrimSpace(disclosure.Code)
	disclosure.Title = strings.TrimSpace(disclosure.Title)
	disclosure.StandardReference = strings.TrimSpace(disclosure.StandardReference)
	disclosure.Currency = strings.ToUpper(strings.TrimSpace(disclosure.Currency))
	disclosure.Version = strings.TrimSpace(disclosure.Version)
	disclosure.Narrative = strings.TrimSpace(disclosure.Narrative)

	if disclosure.CompanyID != query.CompanyID {
		return disclosure, fmt.Errorf("%w: company does not match request", ErrInvalidDisclosure)
	}
	if disclosure.Code == "" || disclosure.Title == "" || disclosure.StandardReference == "" ||
		disclosure.Currency == "" || disclosure.Version == "" {
		return disclosure, fmt.Errorf("%w: code, title, standard reference, currency, and version are required", ErrInvalidDisclosure)
	}
	disclosure.Period.StartDate = normalizeDisclosureDate(disclosure.Period.StartDate)
	disclosure.Period.EndDate = normalizeDisclosureDate(disclosure.Period.EndDate)
	if disclosure.Period.StartDate.IsZero() || disclosure.Period.EndDate.IsZero() ||
		!disclosure.Period.StartDate.Equal(query.Period.StartDate) ||
		!disclosure.Period.EndDate.Equal(query.Period.EndDate) {
		return disclosure, fmt.Errorf("%w: period does not match request", ErrInvalidDisclosure)
	}
	if query.Currency != "" && disclosure.Currency != query.Currency {
		return disclosure, fmt.Errorf("%w: currency does not match request", ErrInvalidDisclosure)
	}
	if query.StandardReference != "" && disclosure.StandardReference != query.StandardReference {
		return disclosure, fmt.Errorf("%w: standard reference does not match request", ErrInvalidDisclosure)
	}
	if query.Version != "" && disclosure.Version != query.Version {
		return disclosure, fmt.Errorf("%w: version does not match request", ErrInvalidDisclosure)
	}
	if len(disclosure.Source) == 0 {
		return disclosure, fmt.Errorf("%w: at least one source is required", ErrInvalidDisclosure)
	}
	disclosure.Source = append([]publicreporting.DisclosureSource(nil), disclosure.Source...)
	for i := range disclosure.Source {
		disclosure.Source[i].Kind = strings.TrimSpace(disclosure.Source[i].Kind)
		disclosure.Source[i].Reference = strings.TrimSpace(disclosure.Source[i].Reference)
		if disclosure.Source[i].Kind == "" || disclosure.Source[i].Reference == "" {
			return disclosure, fmt.Errorf("%w: source kind and reference are required", ErrInvalidDisclosure)
		}
	}
	sort.Slice(disclosure.Source, func(i, j int) bool {
		if disclosure.Source[i].Kind != disclosure.Source[j].Kind {
			return disclosure.Source[i].Kind < disclosure.Source[j].Kind
		}
		return disclosure.Source[i].Reference < disclosure.Source[j].Reference
	})
	if disclosure.Data == nil {
		return disclosure, fmt.Errorf("%w: structured data is required", ErrInvalidDisclosure)
	}
	canonicalData, err := canonicalizeDisclosureData(disclosure.Data)
	if err != nil {
		return disclosure, fmt.Errorf("%w: data must be JSON serializable: %v", ErrInvalidDisclosure, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(canonicalData))
	decoder.UseNumber()
	if err := decoder.Decode(&disclosure.Data); err != nil {
		return disclosure, fmt.Errorf("%w: decode structured data: %v", ErrInvalidDisclosure, err)
	}

	expectedHash, err := hashDisclosure(disclosure, canonicalData)
	if err != nil {
		return disclosure, fmt.Errorf("%w: generate content hash: %v", ErrInvalidDisclosure, err)
	}
	if disclosure.ContentHash != "" && disclosure.ContentHash != expectedHash {
		return disclosure, fmt.Errorf("%w: content hash does not match payload", ErrInvalidDisclosure)
	}
	disclosure.ContentHash = expectedHash
	return disclosure, nil
}

func canonicalizeDisclosureData(data map[string]any) ([]byte, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	if _, ok := normalized.(map[string]any); !ok {
		return nil, errors.New("data must be a JSON object")
	}
	return json.Marshal(normalized)
}

func hashDisclosure(disclosure publicreporting.StructuredDisclosure, data []byte) (string, error) {
	payload := struct {
		CompanyID         types.CompanyID
		Code              string
		Title             string
		StandardReference string
		Period            publicreporting.DisclosurePeriod
		Currency          string
		Version           string
		Source            []publicreporting.DisclosureSource
		Data              json.RawMessage
		Narrative         string
	}{
		CompanyID: disclosure.CompanyID, Code: disclosure.Code, Title: disclosure.Title,
		StandardReference: disclosure.StandardReference, Period: disclosure.Period,
		Currency: disclosure.Currency, Version: disclosure.Version,
		Source: disclosure.Source, Data: data, Narrative: disclosure.Narrative,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func disclosureIdentity(disclosure publicreporting.StructuredDisclosure) (string, error) {
	identity := struct {
		CompanyID         types.CompanyID
		Code              string
		StandardReference string
		Period            publicreporting.DisclosurePeriod
		Currency          string
		Version           string
	}{
		CompanyID: disclosure.CompanyID, Code: disclosure.Code,
		StandardReference: disclosure.StandardReference, Period: disclosure.Period,
		Currency: disclosure.Currency, Version: disclosure.Version,
	}
	encoded, err := json.Marshal(identity)
	return string(encoded), err
}

func normalizeDisclosureDate(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
