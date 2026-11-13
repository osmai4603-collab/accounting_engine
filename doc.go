// Package accounting_engine is a deprecated compatibility facade over pkg/sdk.
//
// Deprecated: import github.com/osmai4603-collab/accounting_engine/pkg/sdk
// instead. This package will be removed in the next MAJOR release.
//
// Migration:
//
//	// before (root facade, or the removed legacy root API)
//	engine, err := accounting_engine.New(driver, accounting_engine.DefaultConfig())
//	// after (recommended)
//	engine, err := sdk.NewBuilder(sdk.WithStoragePorts(driver.Ports())).Build(ctx)
//
// The root package no longer exposes internal engine types, storage handles,
// or standards units. The legacy rich API moved to internal/legacyengine for
// in-repository consumers only; external code must use pkg/sdk and the public
// contracts under pkg/api and pkg/spi.
package accounting_engine
