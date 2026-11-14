# ARD-P5-01: Module Registry, Capability Resolution, and Dependency Graph

## Status

Implemented.

## Context and Problem

Previously, the `StandardModule` interface identified dependencies using arbitrary strings with no canonical identifier format, no version constraint validation, no circular dependency detection, and no deterministic topological ordering for initialization or shutdown. Furthermore, capability discovery was not supported, making it impossible for the engine to verify and inspect active capabilities or report reasons for disabled modules.

## Decision

1. **Rich Module Descriptors and Canonical Identifiers**:
   - Defined `ModuleID` as a canonical machine-readable identifier (e.g. `ifrs15`, `ias12`, `ifrs16`, `ifrs9`).
   - Introduced `Capability` to uniquely identify exported functional features (e.g. `revenue_recognition`, `deferred_tax`, `lease_accounting`, `financial_instruments`).
   - Created `ModuleDescriptor` capturing ID, display name, SemVer version, dependencies (`Requires`), capabilities (`Provides`), engine compatibility, and human-readable description.
   - Introduced `DescribedModule` interface. Modules that do not implement `DescribedModule` are smoothly adapted via automatic descriptor synthesis from legacy `StandardModule` methods.

2. **Module Registry and Dependency Graph**:
   - Created `standards.Registry` in `pkg/spi/standards`.
   - Automated detection of:
     - Duplicate registrations (`ErrDuplicateModule`).
     - Missing dependencies (`ErrMissingDependency`) with support for optional dependencies.
     - Version incompatibilities (`ErrVersionConflict`) via SemVer comparison.
     - Dependency cycles (`ErrCircularDependency`) using DFS graph coloring, formatting the exact circular cycle path.
   - Deterministic topological ordering computed using Kahn's algorithm with lexicographical tie-breaking.

3. **Deterministic Lifecycle and Reverse Teardown**:
   - Engine initialization initializes modules strictly according to topological dependency order.
   - Engine shutdown (`Close`) closes modules in reverse topological order so dependent modules terminate before their dependencies.

4. **Runtime Diagnostics and Capability Inspection**:
   - Added `StandardsService` on `sdk.Engine` exposing `Registry()`, `Diagnostics()`, and `HasCapability()`.
   - `EngineDiagnostics` provides an inspectable snapshot of active modules, active capabilities, provider mappings, dependency order, and detailed disabled reasons.

5. **Built-in Module Implementation**:
   - Updated `modules/ifrs15`, `modules/ias12`, `modules/ifrs16`, and `modules/ifrs9` to implement `DescribedModule` with explicit IDs, versions, and capabilities.

## Verification

- Tests in `pkg/spi/standards/registry_test.go` prove topological sorting, cycle detection, duplicate handling, missing dependency reporting, version conflict resolution, capability queries, diagnostics, and legacy module compatibility.
- Tests in `pkg/sdk/builder_test.go` prove that `EngineBuilder` executes dependency preflight, initializes modules in topological order, and surfaces engine diagnostics and active capability queries via `engine.Standards()`.
