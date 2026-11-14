# ARD-P2-03: Posting Engine Composition and Required Capabilities

## Status

Implemented.

## Decision

`internal/posting.NewConfiguredEngine` is the only constructor for posting
engine instances. The existing `posting.NewEngine` entry point remains
compatible and delegates to that factory with no optional requirements, so
existing callers retain their prior unconfigured behavior.

The factory accepts a `posting.Config` and a `posting.Capabilities` set.
Approval, financial dimensions, maker-checker policy, and posting policy can
each be marked required. Missing required dependencies reject construction
with `ErrRequiredCapabilityUnavailable`; configured controls are attached to
the engine only by the factory.

SDK callers can set the same requirements using `sdk.PostingConfig`, either
through `WithPostingConfig` on `EngineBuilder` or
`NewApplicationServiceWithPostingConfig`. Approval, dimension, and
maker-checker sources can be supplied using the public
`storage.PostingCapabilities` contract and `WithPostingCapabilities` (or
`NewApplicationServiceWithPostingCapabilities`). Core storage integrations
may also provide controls by implementing the corresponding internal
capability contracts. A policy unit can be supplied by an internal adapter
through the `PrivateExtensions` key
`accounting_engine.posting_capabilities`.

The stable `storage.Ports` structure does not currently include these
capabilities; they are explicit SDK composition inputs instead. Requiring a
control without supplying its dependency fails during SDK construction. This
decision does not claim that a capability exists merely because a builder flag
was set. For core-storage mode, existence is checked through the corresponding
dependency interface before the application is returned. Company-specific
policy configuration remains data checked during posting and is not proven by
the presence of its repository interface at Build time.

## Scope and limitations

This change centralizes engine construction/configuration and validates
required dependencies. It does not change reversal behavior, guarantee that
approval or maker-checker policy data is configured for every company, or
extend stable public storage ports. Audit behavior remains governed by the
existing posting repository/storage contract; it is not represented as a new
posting capability here.

## Verification

Focused SDK tests verify that Build rejects missing approval, financial
dimensions, maker-checker policy, and posting policy dependencies, and accepts
the public approval/dimensions/maker-checker capability contract when supplied.
Existing unconfigured SDK composition continues to use the same default
posting behavior.
