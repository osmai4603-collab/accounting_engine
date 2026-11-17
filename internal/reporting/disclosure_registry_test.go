package reporting

import (
	"context"
	"errors"
	"testing"
	"time"

	publicreporting "github.com/osmai4603-collab/accounting_engine/pkg/api/v1/reporting"
	"github.com/osmai4603-collab/accounting_engine/pkg/api/v1/types"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
)

type disclosureTestProvider struct {
	name        string
	disclosures []publicreporting.StructuredDisclosure
	err         error
	calls       int
	lastQuery   publicreporting.DisclosureQuery
}

func (p *disclosureTestProvider) Name() string { return p.name }

func (p *disclosureTestProvider) GetDisclosures(
	_ context.Context,
	query publicreporting.DisclosureQuery,
) ([]publicreporting.StructuredDisclosure, error) {
	p.calls++
	p.lastQuery = query
	return p.disclosures, p.err
}

var _ standards.DisclosureProvider = (*disclosureTestProvider)(nil)

func disclosureTestRecord(companyID types.CompanyID) publicreporting.StructuredDisclosure {
	return publicreporting.StructuredDisclosure{
		CompanyID: companyID,
		Code:      "CASH", Title: "Cash and cash equivalents",
		StandardReference: "IAS 7.45",
		Period: publicreporting.DisclosurePeriod{
			StartDate: time.Date(2026, 1, 1, 14, 30, 0, 0, time.FixedZone("local", 2*60*60)),
			EndDate:   time.Date(2026, 12, 31, 14, 30, 0, 0, time.FixedZone("local", 2*60*60)),
		},
		Currency: "usd", Version: "1.0.0",
		Source: []publicreporting.DisclosureSource{
			{Kind: "general_ledger", Reference: "cash-accounts"},
			{Kind: "accounting", Reference: "cash-accounting"},
		},
		Data: map[string]any{"balance": map[string]any{"closing": 1200, "opening": 900}},
	}
}

func disclosureTestQuery(companyID types.CompanyID) publicreporting.DisclosureQuery {
	return publicreporting.DisclosureQuery{
		CompanyID: companyID,
		Period: publicreporting.DisclosurePeriod{
			StartDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		},
	}
}

func TestDisclosureRegistryNormalizesAndHashesDeterministically(t *testing.T) {
	companyID := types.NewCompanyID()
	record := disclosureTestRecord(companyID)
	first, err := NewDisclosureRegistry([]standards.DisclosureProvider{
		&disclosureTestProvider{name: "ifrs-provider", disclosures: []publicreporting.StructuredDisclosure{record}},
	})
	if err != nil {
		t.Fatalf("NewDisclosureRegistry: %v", err)
	}
	firstResult, err := first.GetDisclosures(context.Background(), disclosureTestQuery(companyID))
	if err != nil {
		t.Fatalf("GetDisclosures: %v", err)
	}

	record.Source[0], record.Source[1] = record.Source[1], record.Source[0]
	second, err := NewDisclosureRegistry([]standards.DisclosureProvider{
		&disclosureTestProvider{name: "ifrs-provider", disclosures: []publicreporting.StructuredDisclosure{record}},
	})
	if err != nil {
		t.Fatalf("NewDisclosureRegistry: %v", err)
	}
	secondResult, err := second.GetDisclosures(context.Background(), disclosureTestQuery(companyID))
	if err != nil {
		t.Fatalf("GetDisclosures: %v", err)
	}
	if firstResult[0].ContentHash != secondResult[0].ContentHash {
		t.Fatalf("content hashes differ for equivalent payloads: %q != %q",
			firstResult[0].ContentHash, secondResult[0].ContentHash)
	}
	if firstResult[0].Period.StartDate.Location() != time.UTC {
		t.Fatalf("period was not normalized to UTC: %v", firstResult[0].Period.StartDate.Location())
	}
	if firstResult[0].Currency != "USD" {
		t.Fatalf("currency = %q, want USD", firstResult[0].Currency)
	}
}

func TestDisclosureRegistryValidatesScopeAndVersion(t *testing.T) {
	companyID := types.NewCompanyID()
	record := disclosureTestRecord(companyID)
	provider := &disclosureTestProvider{name: "ifrs-provider", disclosures: []publicreporting.StructuredDisclosure{record}}
	registry, err := NewDisclosureRegistry([]standards.DisclosureProvider{provider})
	if err != nil {
		t.Fatalf("NewDisclosureRegistry: %v", err)
	}
	query := disclosureTestQuery(companyID)
	query.Version = "2.0.0"
	if _, err := registry.GetDisclosures(context.Background(), query); !errors.Is(err, ErrInvalidDisclosure) {
		t.Fatalf("GetDisclosures error = %v, want invalid disclosure", err)
	}
	if provider.lastQuery.Version != "2.0.0" {
		t.Fatalf("provider query version = %q, want 2.0.0", provider.lastQuery.Version)
	}

	query = disclosureTestQuery(types.NewCompanyID())
	if _, err := registry.GetDisclosures(context.Background(), query); !errors.Is(err, ErrInvalidDisclosure) {
		t.Fatalf("cross-company disclosure error = %v, want invalid disclosure", err)
	}
}

func TestDisclosureRegistryRejectsDuplicatesAndStaleHashes(t *testing.T) {
	companyID := types.NewCompanyID()
	record := disclosureTestRecord(companyID)
	registry, err := NewDisclosureRegistry([]standards.DisclosureProvider{
		&disclosureTestProvider{name: "provider-a", disclosures: []publicreporting.StructuredDisclosure{record}},
		&disclosureTestProvider{name: "provider-b", disclosures: []publicreporting.StructuredDisclosure{record}},
	})
	if err != nil {
		t.Fatalf("NewDisclosureRegistry: %v", err)
	}
	if _, err := registry.GetDisclosures(context.Background(), disclosureTestQuery(companyID)); !errors.Is(err, ErrDuplicateDisclosure) {
		t.Fatalf("duplicate error = %v, want duplicate disclosure", err)
	}

	record.ContentHash = "stale"
	registry, err = NewDisclosureRegistry([]standards.DisclosureProvider{
		&disclosureTestProvider{name: "provider", disclosures: []publicreporting.StructuredDisclosure{record}},
	})
	if err != nil {
		t.Fatalf("NewDisclosureRegistry: %v", err)
	}
	if _, err := registry.GetDisclosures(context.Background(), disclosureTestQuery(companyID)); !errors.Is(err, ErrInvalidDisclosure) {
		t.Fatalf("stale hash error = %v, want invalid disclosure", err)
	}
}

func TestDisclosureRegistryRequiresValidQueryAndProvider(t *testing.T) {
	registry, err := NewDisclosureRegistry(nil)
	if err != nil {
		t.Fatalf("NewDisclosureRegistry: %v", err)
	}
	if _, err := registry.GetDisclosures(context.Background(), publicreporting.DisclosureQuery{}); !errors.Is(err, ErrInvalidDisclosureQuery) {
		t.Fatalf("empty query error = %v, want invalid query", err)
	}
	if _, err := registry.GetDisclosures(context.Background(), disclosureTestQuery(types.NewCompanyID())); !errors.Is(err, ErrDisclosureProviderUnavailable) {
		t.Fatalf("empty registry error = %v, want unavailable provider", err)
	}

	if _, err := NewDisclosureRegistry([]standards.DisclosureProvider{
		&disclosureTestProvider{name: "duplicate"},
		&disclosureTestProvider{name: "duplicate"},
	}); err == nil {
		t.Fatal("duplicate provider names were accepted")
	}
}

func TestDisclosureRegistryPropagatesProviderErrorsAndCancellation(t *testing.T) {
	companyID := types.NewCompanyID()
	providerErr := errors.New("source unavailable")
	registry, err := NewDisclosureRegistry([]standards.DisclosureProvider{
		&disclosureTestProvider{name: "provider", err: providerErr},
	})
	if err != nil {
		t.Fatalf("NewDisclosureRegistry: %v", err)
	}
	if _, err := registry.GetDisclosures(context.Background(), disclosureTestQuery(companyID)); !errors.Is(err, providerErr) {
		t.Fatalf("provider error = %v, want wrapped source error", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.GetDisclosures(ctx, disclosureTestQuery(companyID)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query error = %v, want context.Canceled", err)
	}
}
