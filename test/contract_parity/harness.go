package contractparity

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/memory"
	"github.com/osmai4603-collab/accounting_engine/internal/adapters/postgres"
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	publicapplication "github.com/osmai4603-collab/accounting_engine/pkg/spi/application"
	publicmasterdata "github.com/osmai4603-collab/accounting_engine/pkg/spi/masterdata"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

// AdapterHarness wraps an adapter under test, exposing its public SDK Engine,
// Application Service, MasterData store, and declared Capabilities.
type AdapterHarness struct {
	Name         string
	Ports        storage.Ports
	Engine       sdk.Engine
	AppService   publicapplication.Service
	MasterData   publicmasterdata.Store
	Reporting    storage.ReportingStore
	Capabilities storage.AdapterCapabilities
	Cleanup      func()
}

// NewMemoryHarness provisions and wires an in-memory adapter under test.
func NewMemoryHarness(t *testing.T, f UnifiedFixture) *AdapterHarness {
	t.Helper()
	ctx := context.Background()

	drv := memory.New()
	mdStore := drv.MasterDataStore()
	if err := ProvisionFixture(ctx, mdStore, f); err != nil {
		t.Fatalf("memory: provision fixture: %v", err)
	}

	ports := drv.Ports()
	appService, err := sdk.NewApplicationService(ports)
	if err != nil {
		t.Fatalf("memory: build application service: %v", err)
	}

	engine, err := sdk.NewBuilder(
		sdk.WithStoragePorts(ports),
		sdk.WithReporting(drv),
	).Build(ctx)
	if err != nil {
		t.Fatalf("memory: build engine: %v", err)
	}

	return &AdapterHarness{
		Name:         "memory",
		Ports:        ports,
		Engine:       engine,
		AppService:   appService,
		MasterData:   mdStore,
		Reporting:    drv,
		Capabilities: drv.Capabilities(),
		Cleanup: func() {
			_ = engine.Close()
		},
	}
}

// NewExternalHarness provisions and wires an independent external storage adapter.
func NewExternalHarness(t *testing.T, f UnifiedFixture) *AdapterHarness {
	t.Helper()
	ctx := context.Background()

	store := NewExternalStore()
	if err := ProvisionFixture(ctx, store, f); err != nil {
		t.Fatalf("external: provision fixture: %v", err)
	}

	ports := store.Ports()
	appService, err := sdk.NewApplicationService(ports)
	if err != nil {
		t.Fatalf("external: build application service: %v", err)
	}

	engine, err := sdk.NewBuilder(
		sdk.WithStoragePorts(ports),
		sdk.WithReporting(store),
	).Build(ctx)
	if err != nil {
		t.Fatalf("external: build engine: %v", err)
	}

	return &AdapterHarness{
		Name:         "external",
		Ports:        ports,
		Engine:       engine,
		AppService:   appService,
		MasterData:   store,
		Reporting:    store,
		Capabilities: store.Capabilities(),
		Cleanup: func() {
			_ = engine.Close()
		},
	}
}

// NewPostgresHarness provisions and wires a PostgreSQL adapter in an isolated schema.
func NewPostgresHarness(t *testing.T, f UnifiedFixture) *AdapterHarness {
	t.Helper()
	ctx := context.Background()

	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		dsn = "postgres://osm@localhost:5432/accounting_test?sslmode=disable&host=/var/run/postgresql"
	}

	adminPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		if os.Getenv("POSTGRES_URL") != "" {
			t.Fatalf("postgres: parse configured test database URL: %v", err)
		}
		t.Skipf("postgres: connect to test database skipped: %v", err)
		return nil
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		if os.Getenv("POSTGRES_URL") != "" {
			t.Fatalf("postgres: ping configured test database: %v", err)
		}
		t.Skipf("postgres: ping test database skipped: %v", err)
		return nil
	}

	schemaName := "parity_" + strings.ReplaceAll(uuid.NewString(), "-", "_")
	schema := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		adminPool.Close()
		t.Fatalf("postgres: create isolated schema: %v", err)
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		adminPool.Close()
		t.Fatalf("postgres: parse dsn: %v", err)
	}
	if config.ConnConfig.RuntimeParams == nil {
		config.ConnConfig.RuntimeParams = make(map[string]string)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"

	testPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		adminPool.Close()
		t.Fatalf("postgres: create isolated test pool: %v", err)
	}

	if err := postgres.EnsureSchema(ctx, testPool); err != nil {
		testPool.Close()
		adminPool.Close()
		t.Fatalf("postgres: initialize isolated schema: %v", err)
	}

	drv := postgres.NewWithPool(testPool)
	mdStore := drv.MasterDataStore()
	if err := ProvisionFixture(ctx, mdStore, f); err != nil {
		testPool.Close()
		adminPool.Close()
		t.Fatalf("postgres: provision fixture: %v", err)
	}

	ports := drv.Ports()
	appService, err := sdk.NewApplicationService(ports)
	if err != nil {
		testPool.Close()
		adminPool.Close()
		t.Fatalf("postgres: build application service: %v", err)
	}

	engine, err := sdk.NewBuilder(
		sdk.WithStoragePorts(ports),
		sdk.WithReporting(drv),
	).Build(ctx)
	if err != nil {
		testPool.Close()
		adminPool.Close()
		t.Fatalf("postgres: build engine: %v", err)
	}

	return &AdapterHarness{
		Name:         "postgres",
		Ports:        ports,
		Engine:       engine,
		AppService:   appService,
		MasterData:   mdStore,
		Reporting:    drv,
		Capabilities: drv.Capabilities(),
		Cleanup: func() {
			_ = engine.Close()
			testPool.Close()
			_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
			adminPool.Close()
		},
	}
}

// ForEachAdapter runs a test body across all three adapters with freshly isolated state.
func ForEachAdapter(t *testing.T, name string, testFn func(t *testing.T, h *AdapterHarness, f UnifiedFixture)) {
	t.Helper()

	adapters := []struct {
		name string
		ctor func(t *testing.T, f UnifiedFixture) *AdapterHarness
	}{
		{name: "memory", ctor: NewMemoryHarness},
		{name: "external", ctor: NewExternalHarness},
		{name: "postgres", ctor: NewPostgresHarness},
	}

	for _, tc := range adapters {
		t.Run(tc.name+"/"+name, func(t *testing.T) {
			start := time.Now()
			var capabilities storage.AdapterCapabilities
			defer func() {
				differences := []string(nil)
				semanticDifferences := []string{"none; adapter contract assertions passed"}
				unexpected := []string(nil)
				if capabilities.AdapterName != "" {
					differences = []string{
						fmt.Sprintf("durability=%t", capabilities.GuaranteesDurability),
						fmt.Sprintf("foreign_keys=%t", capabilities.EnforcesForeignKeys),
						fmt.Sprintf("serializable=%t", capabilities.SupportsSerializable),
						fmt.Sprintf("savepoints=%t", capabilities.SupportsSavepoints),
					}
				}
				if t.Failed() {
					unexpected = []string{"contract assertions failed; see the corresponding go test output"}
					semanticDifferences = []string{"adapter contract assertions failed; see go test output"}
				}
				GlobalReporter.Record(ScenarioResult{
					ScenarioName:          name,
					AdapterName:           tc.name,
					EngineVersion:         EngineVersion(),
					Passed:                !t.Failed() && !t.Skipped(),
					Skipped:               t.Skipped(),
					CapabilityDifferences: differences,
					SemanticDifferences:   semanticDifferences,
					UnexpectedDifferences: unexpected,
					ExecutionDuration:     time.Since(start),
				})
			}()
			fixture := NewUnifiedFixture()
			h := tc.ctor(t, fixture)
			if h == nil {
				return
			}
			capabilities = h.Capabilities
			defer h.Cleanup()
			testFn(t, h, fixture)
		})
	}
}
