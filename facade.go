package accounting_engine

// This package is a deprecated compatibility facade. Every declaration here is
// an alias or a thin wrapper around pkg/sdk; no implementation, storage detail,
// or internal type is re-exported. New code must import pkg/sdk directly.

import (
	"github.com/osmai4603-collab/accounting_engine/pkg/sdk"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/legacy"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/standards"
	"github.com/osmai4603-collab/accounting_engine/pkg/spi/storage"
)

// Engine is the composed engine surface.
//
// Deprecated: use sdk.Engine.
type Engine = sdk.Engine

// LedgerService posts and reads journal entries.
//
// Deprecated: use sdk.LedgerService.
type LedgerService = sdk.LedgerService

// ReportingService exposes financial reports.
//
// Deprecated: use sdk.ReportingService.
type ReportingService = sdk.ReportingService

// GovernanceService exposes audit and approval capabilities.
//
// Deprecated: use sdk.GovernanceService.
type GovernanceService = sdk.GovernanceService

// EventIngestionService accepts host-system accounting events.
//
// Deprecated: use sdk.EventIngestionService.
type EventIngestionService = sdk.EventIngestionService

// EngineBuilder composes the engine from public storage ports.
//
// Deprecated: use sdk.EngineBuilder.
type EngineBuilder = sdk.EngineBuilder

// Option configures an EngineBuilder.
//
// Deprecated: use sdk.Option.
type Option = sdk.Option

// PublicEventIngestion is the durable idempotency record of one event.
//
// Deprecated: use sdk.PublicEventIngestion.
type PublicEventIngestion = sdk.PublicEventIngestion

// PublicEventAccountMapping maps an event type to posting accounts.
//
// Deprecated: use sdk.PublicEventAccountMapping.
type PublicEventAccountMapping = sdk.PublicEventAccountMapping

// PublicEventIdempotencyStore is the optional durable idempotency capability.
//
// Deprecated: use sdk.PublicEventIdempotencyStore.
type PublicEventIdempotencyStore = sdk.PublicEventIdempotencyStore

// PublicEventMappingStore is the optional event account mapping capability.
//
// Deprecated: use sdk.PublicEventMappingStore.
type PublicEventMappingStore = sdk.PublicEventMappingStore

// NewBuilder creates an engine builder.
//
// Deprecated: use sdk.NewBuilder.
func NewBuilder(opts ...Option) *EngineBuilder { return sdk.NewBuilder(opts...) }

// WithStoragePorts configures the recommended public storage ports.
//
// Deprecated: use sdk.WithStoragePorts.
func WithStoragePorts(ports storage.Ports) Option { return sdk.WithStoragePorts(ports) }

// WithCoreStorage configures the legacy monolithic store.
//
// Deprecated: use sdk.WithStoragePorts with the adapter's Ports() entry point.
func WithCoreStorage(store legacy.CoreLedgerStore) Option { return sdk.WithCoreStorage(store) }

// WithReporting enables reporting capabilities.
//
// Deprecated: use sdk.WithReporting.
func WithReporting(store storage.ReportingStore) Option { return sdk.WithReporting(store) }

// WithGovernance enables governance capabilities.
//
// Deprecated: use sdk.WithGovernance.
func WithGovernance(store storage.GovernanceStore) Option { return sdk.WithGovernance(store) }

// WithStandard enables a standards module.
//
// Deprecated: use sdk.WithStandard.
func WithStandard(module standards.StandardModule) Option { return sdk.WithStandard(module) }

// ErrIdempotencyKeyConflict reports reuse of a key with another payload.
//
// Deprecated: use sdk.ErrIdempotencyKeyConflict.
var ErrIdempotencyKeyConflict = sdk.ErrIdempotencyKeyConflict

// ErrIdempotencyInProgress reports a concurrent request for the same key.
//
// Deprecated: use sdk.ErrIdempotencyInProgress.
var ErrIdempotencyInProgress = sdk.ErrIdempotencyInProgress

// ErrEventMappingNotFound reports a missing event account mapping.
//
// Deprecated: use sdk.ErrEventMappingNotFound.
var ErrEventMappingNotFound = sdk.ErrEventMappingNotFound

// ErrExchangeRateUnavailable reports a missing exchange-rate source.
//
// Deprecated: use sdk.ErrExchangeRateUnavailable.
var ErrExchangeRateUnavailable = sdk.ErrExchangeRateUnavailable

// ErrEventDimensionsUnsupported reports dimensions that cannot be resolved.
//
// Deprecated: use sdk.ErrEventDimensionsUnsupported.
var ErrEventDimensionsUnsupported = sdk.ErrEventDimensionsUnsupported

// ErrEventPayloadUnsupported reports a payload without an explicit template.
//
// Deprecated: use sdk.ErrEventPayloadUnsupported.
var ErrEventPayloadUnsupported = sdk.ErrEventPayloadUnsupported

// ErrIdempotencyRecordNotFound reports a missing idempotency record.
//
// Deprecated: use sdk.ErrIdempotencyRecordNotFound.
var ErrIdempotencyRecordNotFound = sdk.ErrIdempotencyRecordNotFound

// ErrIdempotencyStateConflict reports an idempotency state conflict.
//
// Deprecated: use sdk.ErrIdempotencyStateConflict.
var ErrIdempotencyStateConflict = sdk.ErrIdempotencyStateConflict
