# Plan to remove the legacy note-type system

## Purpose

This document describes how to remove the legacy note-type API and plugin lifecycle from the codebase now that the explicit manifest/config/view/action model is in place.

This plan assumes:

- we do **not** need backward compatibility for older backend plugins
- we do **not** need backward compatibility for older frontend behavior
- all built-in note types can be migrated fully before the legacy code is removed

## Current state

The codebase currently has two note-type models living side by side:

1. **Legacy `NoteType` interface** in `pkg/notetype/notetype.go`
   - `Validate`
   - `ProcessSave`
   - `ProcessLoad`
   - `UISchema`
   - `CronJobs`

2. **New capability interfaces**
   - `ManifestProvider`
   - `ConfigValidator`
   - `ConfigSaver`
   - `ConfigLoader`
   - `ViewBuilder`
   - `ActionHandler`

The server already prefers the new interfaces, but still has fallback branches that call the legacy methods when the new interfaces are missing.

The built-in note types (`example`, `recipe`, `index`, `recipeoverview`) currently implement **both** models.

## High-level goal

Reach an end state where:

- the legacy persistence/rendering hooks are completely removed
- every registered note type uses the explicit model only
- the server no longer contains fallback branches for legacy methods
- the test harness validates only the explicit model
- the docs teach only the explicit model

## What counts as "legacy system"

For this plan, the legacy system includes:

- `Validate`
- `ProcessSave`
- `ProcessLoad`
- `UISchema`
- legacy test-harness expectations around those methods
- server fallbacks that call those methods
- docs that teach plugin authors to implement those methods as the primary model
- optional compatibility routes or wording that exist only to support the old model

## Key observation

Based on the current codebase, the legacy `NoteType` interface is no longer needed to **design** a note type. The important concepts are now:

- manifest metadata
- persisted config
- computed view data
- actions
- schema initialization
- cron jobs

The only remaining reason the legacy interface still exists is transitional compatibility inside the backend.

## Recommended target model

Replace the current legacy-heavy `NoteType` contract with a minimal required base interface plus explicit capabilities.

### Proposed required base interface

A note type should be required to implement only the non-legacy foundational parts:

- `ID() string`
- `InitSchema(db *sql.DB) error`
- `Manifest() Manifest`
- `CronJobs() []CronJob`

Everything else should remain capability-based:

- `ConfigValidator`
- `ConfigSaver`
- `ConfigLoader`
- `ViewBuilder`
- `ActionHandler`

## Design rules for the cleaned-up system

1. **Manifest is mandatory** for every plugin.
2. **Schema initialization stays mandatory**.
3. **Cron jobs stay optional**, but exposed through the base interface.
4. **Config support is explicit**:
   - if `manifest.has_config` is true, the plugin must implement:
     - `ConfigValidator`
     - `ConfigSaver`
     - `ConfigLoader`
5. **View support is explicit**:
   - if `manifest.has_view` is true, the plugin must implement `ViewBuilder`
6. **Action support is explicit**:
   - if `manifest.has_actions` is true, the plugin must implement `ActionHandler`
7. **Schema-driven editor metadata lives in the manifest**, not in `UISchema()`.
8. The server should **fail fast at startup** if a plugin's manifest claims a capability that the plugin does not actually implement.

## Migration strategy

Do this in phases so the codebase stays buildable after each step.

## Phase 0 — inventory and guardrails

### Goal

Create a clear inventory of all legacy touchpoints before deleting anything.

### Files to inspect

- `pkg/notetype/notetype.go`
- `internal/server/notes.go`
- `internal/server/server.go`
- `pkg/notetype/example/example.go`
- `pkg/notetype/recipe/recipe.go`
- `pkg/notetype/index/index.go`
- `pkg/notetype/recipeoverview/recipeoverview.go`
- `pkg/notetype/plugintest/plugintest.go`
- plugin test files
- `README.md`
- `AGENTS.md`

### Tasks

1. List every remaining fallback branch from explicit → legacy behavior.
2. Confirm every built-in plugin already implements `ManifestProvider`.
3. Confirm every built-in plugin implements the explicit interfaces required by its manifest.
4. Add a short checklist in commit notes or issue tracking so nothing is missed during deletion.

### Acceptance criteria

- all legacy touchpoints are known
- all built-in plugins are classified by required capabilities

## Phase 1 — define the new required plugin base interface

### Goal

Stop treating the legacy `NoteType` interface as the primary plugin contract.

### Primary files

- `pkg/notetype/notetype.go`
- `pkg/notetype/capabilities.go`

### Tasks

1. Introduce a new base interface, for example:
   - `Plugin`
   - or `RegisteredType`

   It should require only:
   - `ID()`
   - `InitSchema()`
   - `Manifest()`
   - `CronJobs()`

2. Change `Registry` to store this new base type instead of the legacy `NoteType`.
3. Update `Register(...)` to accept the new base type.
4. Remove the legacy methods from the base type entirely.
5. Keep the capability interfaces unchanged.

### Important rule

Do **not** keep a hybrid base interface that still includes `Validate`, `ProcessSave`, `ProcessLoad`, or `UISchema`. If we are removing legacy, remove it decisively.

### Acceptance criteria

- the registry no longer depends on legacy save/load/validate methods
- manifest becomes mandatory for registration

## Phase 2 — add startup contract validation

### Goal

Replace fallback behavior with startup-time enforcement.

### Primary files

- `pkg/notetype/notetype.go`
- or a new file like `pkg/notetype/validate.go`
- `internal/server/server.go`

### Tasks

1. Add a validation function that checks each registered plugin against its manifest.
2. Validate at startup:
   - if `HasConfig` is true → plugin implements `ConfigValidator`, `ConfigSaver`, `ConfigLoader`
   - if `HasView` is true → plugin implements `ViewBuilder`
   - if `HasActions` is true → plugin implements `ActionHandler`
   - if `Editor.Mode == "schema"` → manifest editor schema is non-empty valid JSON
   - if actions are declared → action metadata is internally valid
3. Make the server fail fast if validation fails.

### Acceptance criteria

- invalid plugin registrations are rejected at startup
- capability declarations are enforced by the backend rather than handled with fallbacks

## Phase 3 — remove server fallbacks to legacy methods

### Goal

Make the HTTP layer use only the explicit model.

### Primary files

- `internal/server/notes.go`

### Current fallback behavior to remove

1. `createNote` currently does:
   - prefer `ConfigValidator`, else fallback to `Validate`
   - prefer `ConfigSaver`, else fallback to `ProcessSave`

2. `updateNote` currently does:
   - prefer `ConfigValidator`, else fallback to `Validate`
   - prefer `ConfigSaver`, else fallback to `ProcessSave`

3. `enrichDetail` currently loads:
   - `plugin.config` via `ConfigLoader`
   - `plugin.view` via `ViewBuilder`
   - it no longer uses `ProcessLoad`, which is good

### Tasks

1. Remove fallback to `Validate` in create/update.
2. Remove fallback to `ProcessSave` in create/update.
3. If a plugin has config support, always use the config interfaces.
4. If a plugin has no config support and request payload contains plugin config, reject with a clear 400.
5. Keep note detail population based only on `ConfigLoader` and `ViewBuilder`.

### Acceptance criteria

- the server no longer calls legacy validation/save methods anywhere
- create/update behavior is driven only by explicit capabilities

## Phase 4 — migrate built-in plugins to explicit-only code

### Goal

Delete the legacy methods from all built-in note types.

### Primary files

- `pkg/notetype/example/example.go`
- `pkg/notetype/recipe/recipe.go`
- `pkg/notetype/index/index.go`
- `pkg/notetype/recipeoverview/recipeoverview.go`

### Tasks per plugin

#### `example`

- remove `Validate`
- remove `ProcessSave`
- remove `ProcessLoad`
- remove `UISchema`
- keep or inline the schema JSON into `Manifest().Editor.Schema`
- keep `ConfigValidator`, `ConfigSaver`, `ConfigLoader`

#### `recipe`

- remove `Validate`
- remove `ProcessSave`
- remove `ProcessLoad`
- remove `UISchema`
- keep `ConfigValidator`, `ConfigSaver`, `ConfigLoader`
- keep schema JSON in the manifest

#### `index`

- remove `Validate`
- remove `ProcessSave`
- remove `ProcessLoad`
- remove `UISchema`
- keep `ConfigValidator`, `ConfigSaver`, `ConfigLoader`, `ViewBuilder`

#### `recipeoverview`

- remove no-op legacy methods that only exist for compatibility:
   - `Validate`
   - `ProcessSave`
   - `ProcessLoad`
   - `UISchema`
- keep `ManifestProvider`, `ViewBuilder`, `ActionHandler`

### Optional cleanup

If any shared helper is currently named after legacy concepts like `ProcessLoad`, rename it to reflect config/view responsibilities more clearly.

### Acceptance criteria

- no built-in plugin implements legacy persistence/rendering methods anymore
- every built-in plugin compiles using only the explicit model

## Phase 5 — replace legacy UI schema path completely

### Goal

Make manifest editor metadata the single source of truth for schema-driven editors.

### Primary files

- plugin files
- any frontend code still reading `uiSchema` compatibility fields
- docs

### Tasks

1. Remove `UISchema()` from backend plugin contracts.
2. Ensure schema-driven types store schema only in `Manifest().Editor.Schema`.
3. Remove any backend or test code that references `UISchema()`.
4. Update any frontend assumptions if any remain.

### Acceptance criteria

- there is only one schema source of truth: manifest editor metadata

## Phase 6 — rewrite the plugin test harness around the explicit model

### Goal

Stop testing legacy methods as if they were required.

### Primary files

- `pkg/notetype/plugintest/plugintest.go`
- individual plugin tests

### Tasks

1. Change the harness input type from the legacy `NoteType` to the new required base plugin interface.
2. Remove legacy sub-tests:
   - `UISchema_ValidJSON`
   - `Validate_EmptyPayload`
   - `Validate_AcceptsValid`
   - `Validate_RejectsInvalid`
   - `SaveLoad_RoundTrip`
   - `SaveLoad_OrphanCleanup` as implemented via `ProcessLoad`
   - `SaveLoad_EmptySave` as implemented via `ProcessSave`
3. Replace them with explicit equivalents:
   - `Manifest_Provider`
   - `Config_Validate_EmptyPayload`
   - `Config_Validate_AcceptsValid`
   - `Config_Validate_RejectsInvalid`
   - `Config_RoundTrip`
   - `Config_OrphanCleanup`
   - `View_Builder`
   - `Action_Handler`
4. If a plugin has no config support, config tests should skip cleanly.
5. If a plugin has no view or actions, those tests should skip cleanly.
6. Keep `InitSchema_Idempotent`, registry checks, cron job checks, and manifest checks.

### Acceptance criteria

- the harness validates only the explicit model
- test output no longer refers to legacy methods

## Phase 7 — remove deprecated API compatibility that only exists for legacy note types

### Goal

Remove old API paths or semantics that are only still present for compatibility.

### Candidate cleanup items

- `POST /notes/:id/action` legacy route
- any docs that describe legacy action registration
- any compatibility naming that suggests `custom_data` is tied to old `ProcessLoad` behavior

### Decision rule

If the route or behavior exists only for compatibility and is not needed by the current frontend or current integrations, remove it.

### Acceptance criteria

- no deprecated route remains solely for legacy plugin support
- API surface matches the explicit model cleanly

## Phase 8 — documentation cleanup

### Goal

Remove all references to the old mental model from docs.

### Primary files

- `README.md`
- `AGENTS.md`
- any docs under `docs/`
- plugin template snippets

### Tasks

1. Remove instructions telling plugin authors to implement:
   - `Validate`
   - `ProcessSave`
   - `ProcessLoad`
   - `UISchema`
2. Teach only:
   - base plugin interface
   - manifest
   - config interfaces
   - view builder
   - action handler
3. Update testing docs to describe the rewritten harness.
4. Update example snippets to show the new base interface only.

### Acceptance criteria

- docs no longer teach the legacy plugin lifecycle
- a new contributor can implement a note type without seeing legacy concepts

## Phase 9 — final validation

### Goal

Make sure the codebase is fully legacy-free.

### Validation checklist

- `go test ./pkg/notetype/...`
- `go test ./internal/server/...`
- `go test ./...`
- manual check that no code references:
  - `ProcessLoad`
  - `ProcessSave`
  - `Validate(` on plugins as a required contract
  - `UISchema()`
  - legacy plugin docs

### Grep-based cleanup checklist

Before declaring done, verify no remaining references exist to:

- `ProcessLoad`
- `ProcessSave`
- `UISchema()`
- legacy `Validate(` methods on note-type plugins
- fallback comments mentioning migration or compatibility

## Risks and decision rules

### Risk: hidden plugin authorship assumptions in tests

Decision rule: make startup validation strict and explicit so mistakes fail fast.

### Risk: removing legacy too early from one plugin but not another

Decision rule: delete fallbacks only after all built-in plugins are explicit-only.

### Risk: dual source of truth for schemas

Decision rule: manifest editor metadata wins; remove `UISchema()` entirely.

### Risk: orphaned docs that still teach the old model

Decision rule: documentation cleanup is part of the same change set, not a follow-up.

## Definition of done

This cleanup is complete when all of the following are true:

1. the registry no longer depends on the legacy `NoteType` interface
2. the server does not call `Validate`, `ProcessSave`, `ProcessLoad`, or `UISchema`
3. all built-in plugins implement only the explicit model
4. the plugin test harness validates only the explicit model
5. startup validation enforces manifest/capability consistency
6. deprecated compatibility paths tied to the legacy model are removed
7. docs teach only the explicit model
8. all tests pass

## Recommended execution order

1. define the new base plugin interface
2. add startup validation
3. migrate all built-in plugins to explicit-only code
4. remove server fallbacks
5. rewrite the test harness
6. remove deprecated compatibility API pieces
7. update docs
8. run full validation

## Final recommendation

Do **not** preserve a half-legacy, half-explicit base interface. The codebase is already conceptually on the explicit model; the legacy methods now act mainly as internal compatibility shims. The cleanest path is to:

- make `Manifest()` mandatory
- make config/view/actions purely capability-based
- keep only schema init + cron jobs in the required base plugin interface
- delete the legacy lifecycle methods completely

That will make note-type creation simpler, the server logic clearer, and the tests/docs much easier to reason about.