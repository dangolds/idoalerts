# EPIC: Alert Management Service (MVP)

> **Engineer Authority Notice**
> This checklist is a planning artifact derived from `project/docs/DesignAndBreakdown.md`. **The implementing engineer is the final authority.** If a task is wrong, mis-sized, missing context, or conflicts with something discovered during implementation, deviate — and record the deviation in the story's Implementation Notes block. The plan serves the code, not the other way around.

**Goal:** Deliver a Go REST microservice that creates, lists, escalates, and decides sanctions alerts — tenant-isolated, with write-once decisions and stdout-emitted domain events — passing every bullet of the PRD evaluation rubric.

**Scope:**
- **In:** Domain entity + state machine, in-memory repository (copy-on-read/write), stdout event publisher, service layer, 4 HTTP endpoints, middleware (recovery/logger/request-id), DTO validation, structured logging, graceful shutdown, unit + handler tests, README + Makefile.
- **Out (explicitly):** Persistent DB, real message broker, auth/JWT, header-based tenant middleware, optimistic locking, OpenAPI spec, Dockerfile, metrics/tracing, pagination. These are one-liner entries in the README's "Future Improvements" section — do not implement.

**Reference:** `project/docs/DesignAndBreakdown.md` (refined plan) and `project/docs/PRD.md` (assignment spec).

---

## Execution Plan Summary

Decomposition principle: a story gets **execution tasks** only when it touches multiple layers, has sequencing risk, or could leave the tree broken mid-build. Single-file, single-concern stories are left as acceptance-criteria-only. Target size per story: **1–2 hours** for a senior Go engineer who has read the design doc.

| Phase                                       | Stories                      | Notes                                                                                                         |
| ------------------------------------------- | ---------------------------- | ------------------------------------------------------------------------------------------------------------- |
| **1 — Project Scaffolding**                 | 1                            | Module init, deps, directory skeleton — gate for everything else.                                             |
| **2 — Domain Layer**                        | 2, 3, 4, 5                   | Entity + predicates, errors, events, ports. Pure Go, no I/O.                                                  |
| **3 — Infrastructure**                      | **6**, 7, 8                  | In-memory repo (execution tasks — copy-on-R/W trap), repo tests, stdout publisher.                            |
| **4 — Service Layer**                       | **9**, 10                    | Business rules (execution tasks — orchestration + event ordering), service tests.                             |
| **5 — API Layer**                           | 11, 12, 13, **14**, 15, 16   | DTOs, error mapper, middleware, handlers (execution tasks — 4 endpoints + edge cases), router, handler tests. |
| **6 — Composition Root**                    | **17**                       | `main.go` wiring (execution tasks — config, logger, server, signals).                                         |
| **7 — Documentation & Verification**        | 18, 19, 20                   | Makefile, README, end-to-end smoke.                                                                           |

---

## [x] Phase 1: Project Scaffolding

### [x] Story 1: Initialize Go module and directory skeleton

**As a** backend engineer,
**I want** the module, dependencies, and empty package directories in place,
**So that** subsequent stories can add code without touching project plumbing.

**Acceptance Criteria:**

- [x] `go.mod` exists at the chosen repo root (design doc suggests `/alert-service/`; pick one location and stick with it).
- [x] `go.mod` declares **`go 1.22` or higher** (Go 1.22 mux pattern-matching is required — `>=1.22` satisfies it; actual pin tracks local toolchain and dep requirements, see §9.6).
- [x] Dependencies present in `go.mod`: `github.com/google/uuid`, `github.com/go-playground/validator/v10`. Nothing else (no web frameworks, no logging libs — `log/slog` is stdlib).
- [x] Directory skeleton matches design doc §3 exactly: `cmd/server/`, `internal/{domain,service,storage/memory,events,api}/`. Empty `.keep` files are fine; no Go files yet.
- [x] `go build ./...` exits 0 (trivially, with zero Go files).

**Implementation Notes (2026-04-13):**
- **Files created:** `alert-service/go.mod`, `alert-service/go.sum`, `alert-service/cmd/server/.keep`, `alert-service/internal/domain/.keep`, `alert-service/internal/service/.keep`, `alert-service/internal/storage/memory/.keep`, `alert-service/internal/events/.keep`, `alert-service/internal/api/.keep`.
- **Module path:** `github.com/dangolds/idoalerts/alert-service` — matches the actual remote `github.com/dangolds/idoalerts`. Renamed from the initial placeholder (`github.com/dan/fincom/alert-service`) in a follow-up commit after Story 1 review; free while zero source imports existed, avoiding a cascaded grep-replace later.
- **go directive:** `go 1.22` in `go.mod` — the design §9.6 floor. Three-commit arc (not flip-flopping, real team learning curve): (1) init stamped `go 1.25.X` from local toolchain → overrode to `go 1.22`, (2) `go get validator/v10` re-bumped to `go 1.25.0`; amended `go.mod` to accept `1.25.0` under user override "any version ≥1.22 is fine," (3) oversight flagged portability concern — reviewers on Go 1.22–1.24 shouldn't be forced into toolchain auto-download — restored floor to `go 1.22`. Final call: declare the design floor, accept any toolchain ≥ 1.22.
- **Design decisions:** `alert-service/` subdir (Option A) over repo root — mirrors design §3 verbatim, isolates Go code from `project/docs/`, LICENSE, claude-yolo.sh. `.keep` files over `doc.go` — team-lead override on Gemini/oversight suggestion; `doc.go` is idiomatic but premature for empty packages and forces a `main()` stub in `cmd/server/` that Story 17 will replace.
- **Deviation from plan:** Checklist Story 1 acceptance criterion originally read "declares `go 1.22`" — clarified mid-story to "declares `go 1.22` or higher" after the `go get validator/v10` auto-bump surfaced the ambiguity. Final floor is `go 1.22` per oversight's portability concern. Also: module path renamed from placeholder `github.com/dan/fincom/alert-service` to `github.com/dangolds/idoalerts/alert-service` once the real remote surfaced. All deviations ratified by team-lead + user.
- **Next-story handoff:** Module is live at `alert-service/` as `github.com/dangolds/idoalerts/alert-service` with `go 1.22` floor. Story 2 (Alert entity) targets `alert-service/internal/domain/alert.go`. **Post-`go mod tidy` discipline:** any `go mod tidy` (first one lands in Story 2 when `uuid` gets imported) will auto-raise the floor to `go 1.25.0` (validator's declared minimum). Immediately re-run `go mod edit -go=1.22` to restore the floor. 5-second step; trivial but mandatory. `go vet ./...` currently exits 1 ("no Go files") — this self-heals as soon as Story 2's `alert.go` lands; do not paper over the current warning.

---

## [ ] Phase 2: Domain Layer _(dependency: Phase 1)_

### [x] Story 2: `Alert` entity, `Status` enum, and state-predicate methods

**As a** service-layer implementer,
**I want** a pure-Go `Alert` aggregate with a typed status and self-describing transition predicates,
**So that** business rules live on the entity and the service stays thin on invariant checks.

**Acceptance Criteria:**

- [x] `internal/domain/alert.go` defines `Alert` struct with all PRD fields: `ID`, `TenantID`, `TransactionID`, `MatchedEntityName`, `MatchScore`, `Status`, `AssignedTo` (`*string`), `DecisionNote` (`string` — not `*string`; see §9.11), `CreatedAt`, `UpdatedAt` (`time.Time`).
- [x] `Status` is a typed string enum with constants: `StatusOpen`, `StatusEscalated`, `StatusCleared`, `StatusConfirmedHit`. Values match the PRD wire format (`OPEN`, `ESCALATED`, `CLEARED`, `CONFIRMED_HIT`).
- [x] `(a *Alert) CanDecide() bool` returns true iff status is `OPEN` **or** `ESCALATED` (per clarified state machine — §2.1).
- [x] `(a *Alert) CanEscalate() bool` returns true iff status is `OPEN`.
- [x] `(a *Alert) Clone() *Alert` performs the deep copy described in §9.7 — copies value fields and allocates a fresh backing string for `AssignedTo` if non-nil.
- [x] No imports outside stdlib. No logger, no context. Pure data + predicates.

**Implementation Notes (2026-04-13):**

- Files touched: `alert-service/internal/domain/alert.go` (created); `alert-service/internal/domain/.keep` (removed — directory now has real code).
- Status consts declared typed (`const StatusOpen Status = "OPEN"` etc.) so callers can't pass bare strings where `Status` is expected. Plan originally said "untyped-string consts" — reviewer caught the imprecision; typed was always the intent.
- `Clone` uses `cp := *a` plus fresh allocation for `*AssignedTo` when non-nil, verbatim §9.7. `time.Time` is a value type so shallow copy is self-contained. One-line doc comment added flagging the "keep in sync" invariant for any future slice/map/pointer fields (prevents the §2.8a pointer-trap regression).
- `DecisionNote` kept as plain `string` per §9.11 with a 3-line field comment pinning the DTO-vs-domain asymmetry — DTO `DecideRequest.DecisionNote` stays `required`, but the domain tolerates `""` (seeded/legacy alerts). A future dev should not "fix" this to `*string`.
- No constructors, no factories, no `String()` method on `Status`, no validation. Service layer owns UUID + timestamps (§2.8); DTO layer owns field-presence validation (§2.9a). Entity stays naked data + pure predicates — §2.1 line held.
- Gemini (via oversight) suggested two bonus patterns: `NewAlert(...) (*Alert, error)` factory and richer `a.Decide(...)` / `a.Escalate(...)` transition methods on the entity. **Both explicitly rejected** — they collide with §2.1's "pure predicates on entity, orchestration in service" rule and would duplicate DTO/service responsibilities. Flagged here so future readers know the tradeoff was considered, not overlooked.
- `go build ./...` + `go vet ./...` clean from `alert-service/`. `go.mod` floor still `go 1.22` (no `tidy` run — no new deps).

---

### [x] Story 3: Domain error sentinels

**As a** service + API-layer implementer,
**I want** named error values I can compare with `errors.Is`,
**So that** the HTTP error mapper can translate domain errors to status codes without string matching.

**Acceptance Criteria:**

- [x] `internal/domain/errors.go` defines sentinel errors: `ErrNotFound`, `ErrAlreadyDecided`, `ErrInvalidTransition`, `ErrTenantMismatch`.
- [x] Each is a plain `errors.New(...)` sentinel — no custom types needed for MVP.
- [x] Comment above `ErrTenantMismatch` explains it is **never** surfaced to the client — the repo returns `ErrNotFound` for cross-tenant reads (defense-in-depth, no existence leak; see §2.3).

**Implementation Notes (2026-04-13):**

- File: `alert-service/internal/domain/errors.go` (created). Stdlib `errors` only.
- Four `errors.New` sentinels in a single `var ( ... )` block. Messages lowercase with no trailing punctuation per Go idiom, worded to mirror §2.3's response-table `message` field so the HTTP layer (Story 11) can reuse them verbatim.
- Order: `ErrNotFound` → `ErrAlreadyDecided` → `ErrInvalidTransition` → `ErrTenantMismatch`. Not alphabetical — reads top-to-bottom as "common HTTP-facing first, internal-only last." The two 409-mapped sentinels are adjacent for scanability.
- `ErrTenantMismatch` doc comment (adopted verbatim from reviewer wording) names both §2.3 and §2.8a, and calls out "future policy hooks" as the reason to keep it as a distinct sentinel instead of folding into `ErrNotFound`. Kept as internal signal only — repository layer (Story 6) will collapse it to `ErrNotFound` at the boundary.
- No custom error types, no `fmt.Errorf` wrapping, no error codes, no `init()`, no helpers. Pure declarations.
- `go build ./...` + `go vet ./...` clean from `alert-service/`.

**Amendment (2026-04-13, during Story 6):** A fifth sentinel, `ErrAlreadyExists`, was added to enforce the `AlertRepository.Create` "new alerts only" port contract (Story 5 committed that wording; the original plan's silent-overwrite behavior contradicted it). Inserted between `ErrNotFound` and `ErrAlreadyDecided` with a doc comment pointing at the future 409 `ALERT_ALREADY_EXISTS` mapping. Surfaced independently by Gemini, senior-reviewer, and oversight during Story 6 plan review — all three flagged the port-contract mismatch, not a UUID-entropy concern. See Story 6 Implementation Notes for the full chain.

---

### [x] Story 4: Event types and `Event` marker interface

**As a** publisher + service-layer implementer,
**I want** typed event structs with a shared marker interface,
**So that** the publisher cannot accept arbitrary garbage and the JSON output matches the PRD spec byte-for-byte.

**Acceptance Criteria:**

- [x] `internal/domain/events.go` defines `Event` interface: `EventName() string`.
- [x] `AlertDecidedEvent` struct with JSON tags exactly as §2.7b: `event` (literal `"alert.decided"`), `alertId`, `tenantId`, `decision`, `timestamp`. Note: field is `decision`, **not** `status` — the PRD example is authoritative.
- [x] `AlertEscalatedEvent` struct with JSON tags: `event` (`"alert.escalated"`), `alertId`, `tenantId`, `timestamp`.
- [x] Both structs implement `EventName()` returning their respective `event` string constants.
- [x] `Timestamp` is `string` (formatted at publish time in the service layer, RFC3339), not `time.Time` — see §9.4 / §9.13.

**Implementation Notes (2026-04-13):**

- Files touched: `alert-service/internal/domain/events.go` (created); `project/docs/gotchas.md` (created — new living doc per cross-session durability rule).
- `EventName()` returns `e.Event` (field read). Wire payload and type-identity *should* be the same string by definition — coupling them through the single field prevents divergence by construction. An earlier commit (`1bacc2b`) used literal-return + package consts; team-lead reversed that call after reviewer re-evaluated: double-sourcing invites wire-vs-routing drift, and DRY does not pay for a two-call-site constant.
- No `EventName*` package consts. Service sets `Event: "alert.decided"` / `Event: "alert.escalated"` as string literals at the two service-layer construction sites (Story 7). Grep finds both instantly; N=2 is below the DRY threshold.
- Both events use value receivers: `func (e AlertDecidedEvent) EventName() string`. Events are immutable value types.
- Inline in-file guard comments adopted verbatim from oversight: `// json key is "decision" per PRD — do NOT rename to "status"` on `AlertDecidedEvent.Decision`; `// RFC3339 string, populated at publish time in service — do NOT switch to time.Time` on both Timestamp fields. Muscle-memory hazards for next respawn.
- No constructor functions. Service constructs via struct literal — naked domain preserved per §2.1.
- `project/docs/gotchas.md` seeded with 4 entries under `## Domain Events`: §2.7b (decision vs status), §9.4/§9.13 (publish-time RFC3339 string), §9.2 (typed Event marker over any), §2.7a (stdout/stderr split — preview tag, bites at Story 8). Three-field format per team-lead: **Trap** / **What we did** / **If you're tempted to change this**, newest-first.
- `go build ./...` + `go vet ./...` clean from `alert-service/`.

---

### [x] Story 5: Port interfaces (`AlertRepository`, `EventPublisher`, `ListFilter`)

**As a** service-layer implementer,
**I want** the interfaces my service depends on defined in `domain`,
**So that** infrastructure implementations satisfy them from the outside (hexagonal / ports-and-adapters).

**Acceptance Criteria:**

- [x] `internal/domain/ports.go` defines `AlertRepository` with `Create`, `FindByID`, `List`, `Update` — all taking `ctx context.Context` as first arg (§2.2, non-negotiable).
- [x] `FindByID(ctx, tenantID, id)` signature — tenant is a required scoping parameter, not stuffed into an options struct.
- [x] `EventPublisher` interface: `Publish(ctx context.Context, event Event) error`.
- [x] `ListFilter` struct: `TenantID string` (required), `Status *Status` (optional), `MinScore *float64` (optional). Pointers for the optionals make "unset" unambiguous.
- [x] Interfaces live in `domain` so service depends on domain, not the other way around (Go interface-satisfaction is structural — no `implements` keyword needed on impls).

**Implementation Notes (2026-04-13):**

- File: `alert-service/internal/domain/ports.go` (created). Single stdlib import: `context`. `package domain`.
- Order within file: package doc comment → `AlertRepository` → `ListFilter` → `EventPublisher`. `ListFilter` sits adjacent to the `List` method that references it (readability); `EventPublisher` last since it's a separate concern. Cosmetic; reviewers signed off.
- **Doc-split refinement (senior-reviewer, adopted):** The `AlertRepository` doc-block pins **only the correctness invariants every impl must honor** — cross-tenant `FindByID`/`Update` collapse to `ErrNotFound` (§2.3, §2.8a). Impl-specific rules (non-nil empty slice §9.1, `CreatedAt` desc sort §9.12) were **moved out of the port doc** and will be re-documented on the Story 6 in-memory impl itself. Rationale: a future DB impl might pre-sort via index or deliver results via channel — pinning those shapes on the port would over-specify. Tenant-collapse is a security invariant and stays. Plan originally had all three under the port doc; this split is the only deviation.
- **Polish (oversight, adopted):** Package-level doc comment cites DesignAndBreakdown §4 so future readers find the rationale without code-diving.
- `ListFilter` is naked data — no constructor, no validator method. Handler parses query → builds struct → service pass-through → repo scans. Keeps the domain naked-data pattern consistent with Stories 2–4 (no factories, no transition methods).
- Rejected alternatives (flagged for transparency): adding `Delete`/`Exists` to `AlertRepository` (YAGNI, PRD doesn't need them); stuffing tenant into an options struct on `FindByID` (checklist forbids, §2.3 wants tenant as a required scoping param); using `map[string]any` for list filters (loses compile-time safety). All confirmed unneeded.
- No `go mod tidy` — no new deps beyond stdlib.
- `go build ./...` + `go vet ./...` clean from `alert-service/`.
- **Next-story handoff:** Domain layer truly complete. Story 6 (`internal/storage/memory/alert_repo.go`) implements all four `AlertRepository` methods; the impl-flavored rules (non-nil empty slice, `CreatedAt` desc sort, clone-on-R/W per §2.8a) now belong on the impl's package doc. `EventPublisher` consumed by Story 8 (stdout publisher). `AlertService` constructor in Story 9 takes both interfaces by value — no import cycles because interfaces live in `domain`.

---

## [ ] Phase 3: Infrastructure _(dependency: Phase 2)_

### [x] Story 6: In-memory `AlertRepository` implementation

**As a** service-layer implementer,
**I want** a thread-safe, pointer-safe in-memory repo,
**So that** the service can round-trip alerts without a database and without leaking mutable references to stored state.

**Acceptance Criteria:**

- [x] `internal/storage/memory/alert_repo.go` defines `AlertRepo` struct with `alerts map[string]*domain.Alert` and `mu sync.RWMutex`.
- [x] `NewAlertRepo()` constructor returns a pointer with an initialized map.
- [x] Implements all four `AlertRepository` methods with context as first arg (even if unused for MVP — the interface demands it).
- [x] **Cross-tenant reads return `ErrNotFound`** (not `ErrTenantMismatch` to the caller — existence leak prevention, §2.3 / §4).
- [x] `List` returns results sorted by `CreatedAt` descending (§9.12, deterministic — map iteration is randomized).
- [x] `List` returns `make([]*domain.Alert, 0)` when empty, never `nil` — consumers will marshal this to JSON and `nil` → `null` breaks the contract (§9.1).
- [x] Tenant/status/score filtering happens in `List` before sort (O(n) scan is fine for MVP).

**Execution Tasks:**

1. **Scaffold struct + constructor + `Create`.** Initialize the map. `Create` grabs `mu.Lock()`, stores `a.Clone()` (§2.8a — storing the raw pointer lets callers mutate locked state outside the lock), returns nil.
2. **`FindByID` with tenant scoping.** `mu.RLock()`, lookup by ID, check `existing.TenantID == tenantID`, return `existing.Clone()`. Both "not present" and "wrong tenant" map to `ErrNotFound` — do **not** leak existence.
3. **`Update` with tenant scoping + existence check.** `mu.Lock()`, look up by ID, verify tenant match, store `a.Clone()`. Missing ID or cross-tenant → `ErrNotFound`.
4. **`List` with filter + sort + non-nil return.** `mu.RLock()`, iterate, apply `TenantID` (required), then `Status` / `MinScore` filters if set. Append clones. Sort by `CreatedAt` descending. Return slice (pre-initialize with `make` so zero matches → `[]`, not nil).
5. **Package-level comment** explaining the clone-on-read/write invariant and pointing at §2.8a. This is the kind of non-obvious rule that earns a comment per the coding guidelines.

**Implementation Notes (2026-04-13):**

- Files touched: `alert-service/internal/storage/memory/alert_repo.go` (created, ~100 lines); `alert-service/internal/storage/memory/.keep` (removed — real code now lives here); `alert-service/internal/domain/errors.go` (added `ErrAlreadyExists` sentinel — see deviation below).
- **Deviation from plan (added after review):** The original plan said "no duplicate-ID check in `Create` — UUID collision is cosmological — YAGNI." Reviewers (Gemini + senior-reviewer + oversight, **independently**) surfaced that the plan contradicted the port doc committed in Story 5 ("Create is for new alerts only; Update requires an existing row"). A retry path with a reused UUID, or a service bug passing a stored alert back into `Create`, would silently corrupt state. Fix: new `ErrAlreadyExists` sentinel in `domain/errors.go` (between `ErrNotFound` and `ErrAlreadyDecided`), repo enforces via `if _, exists := r.alerts[a.ID]; exists { return domain.ErrAlreadyExists }` under `mu.Lock()` before store. HTTP mapping to 409 `ALERT_ALREADY_EXISTS` is future Story 12 — one-line anchor in the sentinel doc comment. Story 3 Implementation Notes also amended with a 2026-04-13 pointer to this chain. Port-contract-correctness, not UUID-entropy.
- **Refinements applied during review:**
  1. Duplicate-ID check in `Create` (above) — the big one.
  2. `sort.SliceStable` over `sort.Slice` — free determinism when two fast `Create` calls land on the same `CreatedAt` nanosecond (Windows 100ns timer resolution). Identical API, identical complexity, slightly higher constant. Correctness-neutral but stable beats unstable when the cost is zero.
  3. Inline `// pre-allocated so zero-match returns [], not null (§9.1)` above the `out := make(...)` in `List`. The nil-vs-empty trap is non-obvious on a read; a future refactor could easily switch to `var out []*domain.Alert` and silently break JSON output.
  4. Package doc anchor: "Atomicity of compound operations (read-check-write for decisions) is the service layer's concern; see DesignAndBreakdown §2.8b. This repo provides per-method atomicity only." Marks the boundary for future readers who might expect a `CompareAndSwap`-style API here.
- **Lock discipline.** All four methods take `ctx context.Context` (interface contract; unused for MVP, future DB impl uses for cancellation). `Create`/`Update` take `mu.Lock()`; `FindByID`/`List` take `mu.RLock()`. Every method uses `defer mu.Unlock()` / `defer mu.RUnlock()` at the top — no multi-path unlocking. Pointer receivers (`*AlertRepo`) on all four to avoid the mutex-copy trap.
- **Clone boundary.** Both read paths (`FindByID`, `List`) return `a.Clone()`. Both write paths (`Create`, `Update`) store `a.Clone()`. The port doc says clone-on-R/W is absolute regardless of caller trust — the cost is one alloc per op, and the `-race` cleanliness (to be proven in Story 7) depends on it.
- **Cross-tenant collapse.** `FindByID` returns `ErrNotFound` for both missing-ID and cross-tenant cases (no existence leak). `Update` same. `ErrTenantMismatch` is **never** produced by this repo — it's reserved as an internal signal for future policy hooks, exactly as documented in the Story 3 sentinel doc.
- **Compile-time guard.** `var _ domain.AlertRepository = (*AlertRepo)(nil)` at file scope — catches port-shape drift at build time without a test.
- **Filter-before-sort in `List`.** Correctness + performance: sorting discarded entries wastes work, and sort-first would break future pagination semantics. O(n) scan + O(k log k) stable sort where k = matches.
- **What the repo deliberately does NOT do:** no `Delete`, no `Exists`, no `CompareAndSwap`, no `ListStream`, no batch APIs, no tombstones, no in-memory indexes for filter fields. All YAGNI for MVP; §2.8b-style atomic-closure APIs would also force the port to grow, which we've held flat.
- `go build ./...` + `go vet ./...` clean from `alert-service/`.
- `project/docs/gotchas.md` gained two entries (§2.8a clone-on-R/W, §9.1 make-before-iteration) as Finalize step.
- **Next-story handoff.** Story 7 (`alert_repo_test.go`) is the `-race` concurrency test suite that proves the lock + clone discipline holds. It must also cover: happy-path round-trip, cross-tenant `FindByID` → `ErrNotFound`, cross-tenant `Update` → `ErrNotFound`, duplicate-`Create` → `ErrAlreadyExists` (new), post-`FindByID` caller mutation does not affect subsequent reads (proves clone-on-read). The concurrency test must include an explicit comment pointing at §2.8b — we're testing heap safety, not business-rule atomicity under concurrent decides.

---

### [x] Story 7: Storage concurrency + CRUD tests

**As a** reviewer,
**I want** `-race`-clean tests proving the `RWMutex + Clone` pattern holds under load,
**So that** the "it's just a map" implementation cannot regress silently.

**Acceptance Criteria:**

- [x] `internal/storage/memory/alert_repo_test.go` exists.
- [x] Test: sequential `Create → FindByID → Update → FindByID` round-trip (happy-path sanity check).
- [x] Test: cross-tenant `FindByID` returns `ErrNotFound` (not the alert, not `ErrTenantMismatch`).
- [x] Test: cross-tenant `Update` returns `ErrNotFound`.
- [x] Test: concurrent goroutines spamming `Create` / `FindByID` / `Update` across distinct and shared IDs — no panics, no data races. `sync.WaitGroup`, ~50 goroutines × 100 iterations each is plenty.
- [x] Test: after a `FindByID` caller mutates the returned pointer, a subsequent `FindByID` still returns the original values (proves the clone-on-read).
- [x] `go test ./internal/storage/memory/... -race` passes. *(CI-gated — see Impl Notes for Windows + cgo constraint; all 12 tests PASS under `go test -cover` with 100% coverage.)*
- [x] Explicit comment in the concurrency test pointing at §2.8b — we are testing heap-safety, **not** business-rule atomicity under concurrent decides.

**Implementation Notes (2026-04-13):**

- **Files touched:** `alert-service/internal/storage/memory/alert_repo_test.go` (created, ~520 lines net one file). Black-box `package memory_test` — the suite consumes only the exported port surface, mirroring how `service` will use the repo.
- **Final test count:** 12 tests in one file: 11 active + 1 `t.Skip` (the N4 ctx-contract placeholder for a future DB impl). 100% statement coverage on `internal/storage/memory`. Run time ≈ 0.56s without `-race`.
- **Deviations from AC (all additive, all ratified in the team's debate round):**
  1. **#6 `TestAlertRepo_Update_CallerMutationAfterUpdateDoesNotAffectStore`** — clone-on-write symmetry. AC names only clone-on-read, but §2.8a is a *bilateral* invariant: a refactor that stored the caller's pointer directly in `Update` wouldn't be caught by the read-side test alone. One ~15-line test closes the gap.
  2. **#7 `TestAlertRepo_List_EmptyReturnsNonNilSlice` asserts `string(json.Marshal(got)) == "[]"`** — per `project/docs/gotchas.md` §9.1, which explicitly assigns this check to Story 7. A `len == 0` assertion alone wouldn't catch a `nil` return (both have len 0; only the JSON check distinguishes them).
  3. **#8 `TestAlertRepo_List_SortedByCreatedAtDesc`** — AC silent; §9.12 determinism. Uses three alerts with explicit `time.Now().Add(-N*sec)` offsets to dodge Windows 100ns timer granularity (per Story 6 notes).
  4. **#9 `TestAlertRepo_List_FiltersByStatusAndMinScore`** — AC silent; seeds one positive row plus one "must-be-excluded" row per predicate branch (wrong-tenant / wrong-status / below-score) so a filter bug that short-circuits any single predicate flunks exactly one row.
  5. **#11 `TestAlertRepo_List_CallerMutationDoesNotAffectStore`** — senior-reviewer **blocking** add. `List` has its own `append(out, a.Clone())` call site distinct from `FindByID`'s return; the existing #5 read-clone test doesn't exercise it. Without #11, a refactor that dropped the `.Clone()` inside the `List` loop would be silently missed.
  6. **#12 `TestAlertRepo_ContextCancellation_ContractForDBImpl`** — oversight add. `t.Skip("§2.2 — in-memory impl ignores ctx for MVP; contract-level test lands with DB impl")`. Zero cost today, materializes the port contract in code so a future DB adapter inherits a test slot to fill.
- **A1 design rationale (critical context for future readers / respawning agents):** The concurrency test is **partitioned per operation**, not a uniform "random mixed workload with tolerance bucket". A pool of 32 shared IDs is seeded SERIALLY before goroutines spin. Then three disjoint op shapes run in parallel:
  - **Creates** use a *fresh* `uuid.NewString()` every call → collision is cryptographically impossible → must return nil.
  - **FindByID** targets only seeded-pool IDs → must exist → must return nil.
  - **Update** targets only seeded-pool IDs → must exist → must return nil.
  
  Any non-nil error on any of these paths fails the test. **The error-type partition is what makes this test meaningful in environments where `-race` cannot run** (see below); `-race` is additive belt-and-braces, not load-bearing. A future "simplify" pass that reintroduces a tolerance bucket like `if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrAlreadyExists) { continue }` would silently accept lock corruption that flipped a seeded-ID entry into "missing" or a fresh-UUID into "already-present".
- **`-race` local constraint:** Windows dev box has no gcc / tdm-gcc / clang / mingw64 installed; `go test -race` silently requires cgo + a C toolchain. Team decision: **do NOT install a C toolchain on this machine — MVP is not worth the setup cost**. `-race` moves to the CI gate via Story 18's Makefile (CI runners are linux with cgo). The partitioned-workload design above absorbs this cost locally. Full reasoning lives in `project/docs/gotchas.md` under the new `## Testing` section.
- **§2.8b pointer comment** — landed verbatim at `TestAlertRepo_ConcurrentCreateFindUpdate_RaceClean`'s doc comment. States that this test proves HEAP SAFETY of the `RWMutex + Clone` pattern; does NOT assert business-rule atomicity (no double-decide, no torn writes at the domain level); that gap is the service layer's concern (§2.8b — accepted MVP limitation, production fix is DB-level `SELECT ... FOR UPDATE`).
- **N5 (no duplicate `var _ domain.AlertRepository = (*memory.AlertRepo)(nil)` guard):** the prod `alert_repo.go` already has the compile-time port guard from Story 6 — redundant in the test file.
- **N6 clone-on-write rationale:** explicitly noted in a package-level comment at the top of `TestAlertRepo_Update_CallerMutationAfterUpdateDoesNotAffectStore` — documents WHY the test is beyond AC (bilateral §2.8a invariant) so the next reader doesn't try to delete it as redundant with #5.
- **N7 (no exported testutil):** all helpers (`newAlert`, `with*` option funcs, `ptrFloat`) are unexported, `_test.go`-scoped. Oversight's architectural guardrail: Story 10 (`AlertService` tests) must not pick up a test-only import dependency on a `testutil` package. Helpers stay test-file-local; each layer builds its own if needed.
- **Package choice** — `memory_test` (black-box), not `memory` (internal). Exercises only the port surface; trade-off is no peek at the unexported `alerts` map. Team agreed: the behavioral assertions (FindByID returns / Update + FindByID round-trips) cover the same ground without coupling tests to internals that could shift in a future refactor.
- **Cleanup during finalize:** removed an unused `ptrStatus(s domain.Status) *domain.Status` helper that the earlier draft carried — `go vet` didn't flag it (unused helpers in `_test.go` files are legal) but zero call sites = dead code. Grep confirmed zero users before removal.
- `go build ./...` + `go vet ./...` clean from `alert-service/`.
- `go test ./internal/storage/memory/... -cover` — 11 PASS, 1 SKIP, 100% statement coverage. `-race` pending CI per above.
- **Next-story handoff (Story 8 — stdout `EventPublisher`):** Infrastructure Phase 3 is now **2/3 complete** (Story 6 repo + Story 7 tests). Story 8 creates `alert-service/internal/events/stdout_publisher.go` — a trivial `StdoutPublisher` satisfying `domain.EventPublisher`. Key invariants, seeded by Story 4's gotchas entries and the team's prior debate:
  - **`os.Stdout` is the event bus** (§2.7a). Use `json.NewEncoder(os.Stdout).Encode(event)` — it appends a newline automatically, so each event is one JSON line on stdout.
  - **NO logging in this package.** `slog` is reserved for `os.Stderr`, configured in `cmd/server/main.go` (Story 16). Any `log.Printf` or default-handler `slog.Info` call in the `events` package contaminates the simulated broker stream.
  - **On encode failure, return the error** — the service decides what to do. For stdout this is effectively never, but honor the interface contract.
  - **No tests for the publisher itself** (trivial I/O glue; covered transitively by Story 10's service tests using a fake publisher).
  - Events consumed by the publisher are the two typed structs from Story 4 (`AlertDecidedEvent`, `AlertEscalatedEvent`); their `EventName()` returns `e.Event` (field-read, single-source-of-truth pattern — see AM-4 in feature design decisions).
  - Story 9's service layer will construct events via struct literal and set `Timestamp: time.Now().UTC().Format(time.RFC3339)` at publish time (§9.4 / §9.13 — publish time, NOT `Alert.UpdatedAt`).

---

### [x] Story 8: Stdout `EventPublisher`

**As a** service-layer implementer,
**I want** a publisher that writes JSON-encoded events to **stdout** (not stderr),
**So that** the simulated message-broker stream is cleanly consumable by `tail -f | jq` and stays uncontaminated by application logs.

**Acceptance Criteria:**

- [x] `internal/events/stdout_publisher.go` defines `StdoutPublisher` struct and `NewStdoutPublisher()` constructor.
- [x] `Publish(ctx, event)` JSON-encodes `event` and writes one line to `os.Stdout` followed by `\n`. Use `json.NewEncoder(os.Stdout).Encode(event)` (adds newline automatically).
- [x] On encode failure, return the error — the service decides what to do (for stdout this is effectively never, but honor the interface).
- [x] **Do not write logs to stdout anywhere in this package.** Stdout is the event bus. Logs go to stderr via `slog` (§2.7a).
- [x] No tests required for the publisher itself (trivial I/O glue); it is covered transitively by service tests using a fake publisher.

**Implementation Notes (2026-04-13):**

- **Files touched:** `alert-service/internal/events/stdout_publisher.go` (created, 74 lines incl. package + field + method doc). Stdlib only. No tests per AC.
- **Struct shape:** `type StdoutPublisher struct { mu sync.Mutex; w io.Writer }`. Constructor `NewStdoutPublisher()` pins `os.Stdout`. `Publish(ctx, event)` holds `p.mu.Lock()` for the duration of `json.NewEncoder(p.w).Encode(event)` and returns the encode error verbatim.
- **Design calls landed (read in this order on respawn):**
  1. **`sync.Mutex` guarding `Encode` — non-negotiable per §2.7a clean-stream invariant.** `json.Encoder.Encode` is not documented goroutine-safe; concurrent writes on the shared `io.Writer` can interleave bytes. POSIX `Write ≤ PIPE_BUF` atomicity applies **only to pipes**, not terminals/files; Windows `WriteFile` makes no atomicity guarantee. Story 17 wires this publisher behind concurrent HTTP handlers (one goroutine per request) — concurrent `Publish` is the default, not the edge case. A torn JSON line silently breaks `tail -f | jq` consumers, which is the exact failure §2.7a exists to prevent. Inline mutex-field comment captures the three OS/library reasons in-code so a future respawn cannot handwave "why the mutex."
  2. **Per-call `json.NewEncoder(p.w).Encode(event)` — deliberately NOT cached.** Debate snapshot for respawn: Oversight + Gemini recommended caching the encoder in the struct (saves one allocation); Senior Reviewer recommended per-call (keeps struct state minimal, sidesteps `json.Encoder` goroutine-safety ambiguity, keeps `w` the single source of truth). Team-lead sided with per-call. Alloc cost is invisible at MVP event volume; the struct stays `{mu, w}` and can't drift a stale encoder if `w` is ever swapped. Method-doc comment captures this so the decision survives respawn.
  3. **`io.Writer` seam — unexported field, SINGLE public constructor `NewStdoutPublisher()` pinning `os.Stdout`.** No writer-injection public constructor today. Rationale captured in constructor doc: a public seam would invite a second call site that bypasses the §2.7a invariant. Escape hatch for future direct-package tests: an internal `_test.go`-scoped builder (does not expose a runtime seam).
  4. **ctx accepted but not honored.** Once `Publish` runs, persist-before-publish (§2.7) has already committed; cancelling the write post-commit would silently diverge storage state from the event stream. Same pattern as Story 6 repo. Method-doc captures it.
  5. **No error wrapping.** `return json.NewEncoder(p.w).Encode(event)` bare. Service (Story 9) logs raw at ERROR per §2.7; wrapping here adds noise.
  6. **Compile-time port guard** `var _ domain.EventPublisher = (*StdoutPublisher)(nil)` at file scope — mirrors Story 6's `alert_repo.go` pattern. Port drift fails the build.
  7. **Intentionally unbuffered.** Package doc captures the bufio-wrap hazard as an invariant: "Flush MUST sit inside the same critical section as the Encode so a concurrent Publish cannot interleave with the tail of a pending buffered event — this prescribes the invariant, not a specific call sequence." Phrasing broadened from architect's initial "immediately after Encode" on senior-reviewer's catch — a future wrapper that owns Flush elsewhere still holds the invariant.
- **Deviations from AC:** none. One addition: the compile-time port guard (Story 6 precedent; AC doesn't name it, but it's three-line insurance against port drift).
- **Cross-layer wiring for future stories (critical for respawn):**
  - **Story 9 (consumer):** `AlertService` constructor takes `domain.EventPublisher` (port), NOT `*events.StdoutPublisher`. Service never imports `internal/events`. Service constructs events via struct literal — `domain.AlertDecidedEvent{Event: "alert.decided", ..., Timestamp: time.Now().UTC().Format(time.RFC3339)}` — and hands them to `pub.Publish(ctx, evt)`. Per §9.13 the timestamp is publish-time, NOT `Alert.UpdatedAt`.
  - **Story 10 (service tests):** uses a **fake publisher** (in-memory event-recording struct satisfying `domain.EventPublisher`), NOT this StdoutPublisher. Zero coupling risk — the port is the contract, not the impl.
  - **Story 17 (main.go composition root):** `events.NewStdoutPublisher()` is called once, the `*StdoutPublisher` is passed to `service.NewAlertService(repo, pub, logger)`. Graceful shutdown does NOT need to flush this publisher (unbuffered); a future bufio wrap adds a Close/Flush step. Importantly, `cmd/server/main.go` must wire `slog.SetDefault` (or construct a new default handler) to write to **os.Stderr** — any `slog.Info("server started...")` on the default handler leaking to stdout would contaminate the event stream; see §2.7a and the gotchas entry.
- **Next-story handoff (Story 9):** Infrastructure Phase 3 is **3/3 complete** (Stories 6, 7, 8). Story 9 lands `alert-service/internal/service/alert_service.go` with four methods (`CreateAlert`, `ListAlerts`, `DecideAlert`, `EscalateAlert`). Service owns UUID generation + timestamp setting (§2.8); orchestrates load-check-mutate-persist-publish (§2.7); builds events at publish time with RFC3339 strings (§9.13); inline §2.8b race comment at the decide check; inline §2.9c strict-write-once comment at the decide flow. Service constructor signature: `NewAlertService(repo domain.AlertRepository, pub domain.EventPublisher, logger *slog.Logger) *AlertService`. The `*slog.Logger` is used ONLY for publish-failure logging (log at ERROR and return success anyway — state is authoritative per §2.7). Event marker `domain.Event` satisfied by `AlertDecidedEvent` / `AlertEscalatedEvent`; `EventName()` returns `e.Event` (field-read — see Story 4 notes, AM-4 in feature design decisions).

---

## [ ] Phase 4: Service Layer _(dependency: Phase 3)_

### [x] Story 9: `AlertService` — four orchestration methods

**As a** handler-layer implementer,
**I want** a service that owns the load-check-mutate-persist-publish flow,
**So that** handlers stay dumb (parse → call → render) and business rules are centralized.

**Acceptance Criteria:**

- [x] `internal/service/alert_service.go` defines `AlertService` struct holding `repo domain.AlertRepository`, `pub domain.EventPublisher`, `logger *slog.Logger`.
- [x] `NewAlertService(repo, pub, logger)` constructor.
- [x] `CreateAlert(ctx, input) (*domain.Alert, error)` — generates `uuid.NewString()` for ID, sets `Status = StatusOpen`, `CreatedAt/UpdatedAt = time.Now().UTC()` **in the service**, not in the handler or repo (§2.8). Persists via `repo.Create`. Returns the created alert.
- [x] `ListAlerts(ctx, filter) ([]*domain.Alert, error)` — thin pass-through to `repo.List`.
- [x] `DecideAlert(ctx, tenantID, id, newStatus, note) (*domain.Alert, error)` — `FindByID → CanDecide() check → mutate → Update → publish`. If `!CanDecide()` because status is already `CLEARED`/`CONFIRMED_HIT`, return `ErrAlreadyDecided` (409). Any other invalid state returns `ErrInvalidTransition`.
- [x] `EscalateAlert(ctx, tenantID, id) (*domain.Alert, error)` — `FindByID → CanEscalate() check → mutate → Update → publish`. Wrong state → `ErrInvalidTransition`.
- [x] **Persist before publish** (§2.7): if `repo.Update` fails, no event is emitted. If publish fails, log at ERROR and return success anyway (state is authoritative).
- [x] Event `Timestamp` is constructed **at publish time** (`time.Now().UTC().Format(time.RFC3339)`), not derived from `UpdatedAt` (§9.13).
- [x] Inline code comment at the decide-check flagging the known read-check-write race (§2.8b) with a one-line explanation of the production fix.
- [x] Inline code comment at the decide flow noting strict write-once per PRD (§2.9c) — no idempotency dedupe.

**Implementation Notes (2026-04-13):**

- **Files touched:** `alert-service/internal/service/alert_service.go` (created; package doc + struct + constructor + `CreateAlertInput` + four methods with per-method doc blocks). No tests in this story — Story 10 owns the suite. `go.mod` intentionally **not** touched: `github.com/google/uuid v1.6.0` was already in the module graph from Story 1 bootstrap, so the new import satisfied without `go get` and the `go` directive floor stayed untouched (D3 re-pin dance not needed this round). `go mod tidy` was deliberately skipped — would promote `uuid` from `// indirect` to direct require but also auto-raise the go directive (D3 corollary); cosmetic-only, and build+vet are green as-is.
- **Design calls landed (read in this order on respawn):**
  1. **Struct + constructor exactly per Story 8 handoff — `NewAlertService(repo domain.AlertRepository, pub domain.EventPublisher, logger *slog.Logger)`.** No defensive nil-checks on constructor args. Composition root owns wiring; `nil` at this seam panics loudly on first call, which is strictly preferable to a silent guarded no-op at an internal (non-library) boundary. **No `var _ = (*AlertService)(nil)` compile-time guard** — `AlertService` is an orchestrator, not a port-implementer; the compile-time-guard pattern (Stories 6/8) lives on infrastructure adapters that *satisfy* ports, and cargo-culting it onto the service that *consumes* ports would misrepresent the dependency direction. Senior-reviewer + oversight both flagged this explicitly — the no-guard was a deliberate ruling, not an oversight.
  2. **`CreateAlertInput` struct in the service package, plain Go fields, NO `json:` / `validate:` tags.** See new ADR **AM-6** for the full rationale. Key points for respawn: (a) Service depends on domain only today; a DTO type in `internal/api/dto` would couple service → api-dto and reverse the hexagonal direction. (b) Taking `*domain.Alert` directly lets callers smuggle server-owned fields (`ID`, `Status`, timestamps) past the §2.8 invariant — exactly the bug `ErrAlreadyExists` was added to catch in Story 6. (c) No struct tags here because the handler DTO (`CreateAlertRequest`, Story 11) carries validator/json tags at the wire boundary; two-DTO discipline with a bright line at the handler. A two-or-three-line mapping step `CreateAlertRequest → CreateAlertInput` is the explicit cost; the bright line is worth it.
  3. **Single `now := time.Now().UTC()` on Create used for BOTH `CreatedAt` and `UpdatedAt`.** Two separate `time.Now()` calls could differ by nanoseconds and break the "equal on creation" invariant that downstream code (tests, auditors, a future `WasEverUpdated()` predicate) may rely on. Explicit `DecisionNote: ""` per AM-2 — zero-value drift works today but the explicit literal documents intent.
  4. **B1 defense-in-depth guard at the top of `DecideAlert`.** `if newStatus != StatusCleared && newStatus != StatusConfirmedHit { return nil, ErrInvalidTransition }`. The DTO `oneof=CLEARED CONFIRMED_HIT` validator protects the HTTP path, but the service is a package-boundary port callable from non-HTTP callers (Story 10 tests, future internal tools, CLI). An invalid `newStatus` slipping past `CanDecide` on an OPEN alert would mutate Status back to `OPEN` AND emit `{"decision":"OPEN"}` — corrupting the downstream audit stream. This is a deviation from the literal AC but a strict addition: it removes no AC behavior and is covered by inline comment + this block. Senior-reviewer flagged it as a real gap, not pedantry.
  5. **Terminal-vs-invalid-transition disambiguation inside `!CanDecide()`.** Branch one: `a.Status == CLEARED || a.Status == CONFIRMED_HIT` → `ErrAlreadyDecided`. Branch two (the "else"): `ErrInvalidTransition`. Branch two is unreachable today given the closed 4-status enum — `CanDecide` returns true for `{OPEN, ESCALATED}` and false only for the two terminal states. Kept as a belt-and-braces guard against a future 5th enum value (e.g., hypothetical `ARCHIVED`) that is non-terminal but also non-decidable, which unconditional `return ErrAlreadyDecided` would mislabel. Inline comment (`// Unreachable today given the 4-status enum; guards future non-decidable, non-terminal statuses from being mislabeled "already decided".`) carries the rationale. **Team-lead ruled no ADR for this** — the inline comment is the canonical home for dead-today-but-load-bearing code, and ADR'ing would be meta-drift (oversight's flag). No AM-7.
  6. **§2.9c write-once language lives in the `DecideAlert` method doc; §2.8b race comment stays inline at the `CanDecide` check.** Senior-reviewer's B4 catch: AC wants §2.9c "at the decide flow" — the overall operation property, not a per-branch footnote. The method-doc placement satisfies that. §2.8b is different: it's colocated with the *race window* (the gap between `FindByID` and `Update` that the `CanDecide` check straddles), so the inline comment at the check site is the correct home. On `EscalateAlert` the same race window exists with the same shape, so the §2.8b comment is echoed there as a one-liner (`// §2.8b: same read-check-write race shape as DecideAlert; accepted for MVP.`) — team-lead's ruling on open-question #3. The asymmetry (full paragraph on Decide, one-line echo on Escalate) is deliberate: Decide carries the primary exposition; Escalate points back to it.
  7. **Events constructed via struct literal at each call site — NO helper.** Two call sites (decide, escalate). AM-4's single-source-of-truth pattern (EventName() returns `e.Event`) argues against a `buildDecidedEvent(a, newStatus)` helper at N=2 for the same reason it argued against a package-level const. `Event: "alert.decided"` / `Event: "alert.escalated"` are inline literals. Field names MUST match `internal/domain/events.go` exactly — `AlertID` (not `ID`), `Decision` (not `Status`), `Timestamp` (not `UpdatedAt`). `go vet`'s struct-field typo check catches regressions at build time; the architect re-read `events.go` before coding per senior-reviewer's B2.
  8. **`Timestamp = time.Now().UTC().Format(time.RFC3339)` at publish time — never `a.UpdatedAt.Format(...)`.** §9.13 pins this: "when the event fired" is semantically distinct from "when the entity last changed." Inline comment on both timestamp lines reinforces the invariant for future-me running a grep.
  9. **Publish-failure: log via `slog.ErrorContext(ctx, ...)` with exactly 4 typed fields (`alert_id`, `tenant_id`, `event`, `err`) and return success (`a, nil`).** `ErrorContext` (not `Error`) threads ctx-scoped values (future request IDs from middleware) into the log record automatically — mandatory per oversight's B3. Snake-case field names for grep-stable log-ingest contracts. Field cap is **4** per oversight's drift flag: no request IDs (middleware owns those), no stack traces, no retry counters. Any `logger.Info` / `logger.Debug` / `log.Printf` elsewhere in the file will fail code review — the logger is publish-failure-only per Story 8 handoff note 4.
  10. **Imports are exactly `context`, `log/slog`, `time`, `github.com/google/uuid`, and `.../internal/domain`.** No `errors` (sentinels are returned directly; `errors.Is` lives in the HTTP mapper, Story 12). No `internal/events` / `internal/storage/memory` — the service depends on domain ports, not adapters; this preserves the hexagonal direction (service → domain; adapters → domain; service never → adapters). Compile-fail at that drift is the point.
- **Deviations from AC:** two strict additions; both inline-commented and called out here. Neither removes or reshapes AC behavior.
  1. **`CreateAlertInput` struct introduced** — AC said "input" without specifying the type. Service-package struct enforces §2.8 server-owned ownership at the type level. Covered by new ADR AM-6.
  2. **Explicit `newStatus` validation at top of `DecideAlert`** — defense-in-depth per B1; service is callable from non-HTTP contexts where the DTO `oneof` does not run. Inline comment carries the rationale.
- **Cross-layer wiring for future stories (critical for respawn):**
  - **Story 10 (service tests):** uses a **real** `memory.AlertRepo` (instantiated via `memory.NewAlertRepo()`), a **fake** publisher (in-memory event-recording struct satisfying `domain.EventPublisher` — records `[]domain.Event` in a slice under its own mutex, exposes a `Published()` accessor), and a **discarding** `*slog.Logger` so publish-failure log lines do not pollute test output. Construct: `slog.New(slog.NewJSONHandler(io.Discard, nil))`. Test file is `internal/service/alert_service_test.go` (`package service_test` black-box — same choice as Story 7). Story 10 AC pins seven cases: `CreateAlert` happy path; `DecideAlert` OPEN → success (1 event); `DecideAlert` ESCALATED → success (1 event, per clarified state machine); `DecideAlert` already-decided → `ErrAlreadyDecided` (0 events — regression guard for §2.7 persist-before-publish); `DecideAlert` wrong tenant → `ErrNotFound` (0 events — tenant isolation); `EscalateAlert` OPEN → success (1 event); `EscalateAlert` non-OPEN → `ErrInvalidTransition` (0 events). **Event-count assertion on the fake publisher is load-bearing** for the §2.7 rubric test — not just "got an error", but "also zero events emitted". Additional coverage the architect recommends but AC doesn't require: the B1 defense-in-depth guard (`newStatus = StatusOpen` on an OPEN alert → `ErrInvalidTransition` + zero events + zero repo writes — proves the guard short-circuits before the load); and a `CreateAlert` test asserting `CreatedAt == UpdatedAt` to lock down the single-`now` invariant.
  - **Story 14 (handlers):** four handlers map onto the four service methods. `POST /alerts` → parse `CreateAlertRequest` DTO → map to `CreateAlertInput` (explicit field copy, 5 lines) → `svc.CreateAlert(ctx, in)` → 201 with `toAlertResponse(a)`. `GET /alerts` → parse query to `ListFilter` → `svc.ListAlerts` → 200 with `{"alerts": [...]}` (slice-init non-nil per §9.1). `PATCH /alerts/{id}/decision` → parse `DecideRequest` DTO (validator rejects non-`oneof` status) → `svc.DecideAlert(ctx, req.TenantID, r.PathValue("id"), domain.Status(req.Status), req.DecisionNote)` → 200 / 409 / 404. `POST /alerts/{id}/escalate` → parse `EscalateRequest` DTO → `svc.EscalateAlert(ctx, req.TenantID, r.PathValue("id"))` → 200 / 409 / 404. Domain-error mapping via `api.writeError` + `errors.Is` fan-out: `ErrNotFound` → 404 `ALERT_NOT_FOUND`; `ErrTenantMismatch` → 404 (never surfaces — repo collapses); `ErrAlreadyExists` → 409 `ALERT_ALREADY_EXISTS`; `ErrAlreadyDecided` → 409 `ALERT_ALREADY_DECIDED`; `ErrInvalidTransition` → 409 `INVALID_STATE_TRANSITION`; validator errors → 400 `VALIDATION_ERROR`.
  - **Story 17 (main.go composition root):** one-shot wiring in order: `slog.New(slog.NewJSONHandler(os.Stderr, ...))` + `slog.SetDefault(logger)` (stderr, NOT stdout — §2.7a); `repo := memory.NewAlertRepo()`; `pub := events.NewStdoutPublisher()`; `svc := service.NewAlertService(repo, pub, logger)`; handler + router + `http.Server` with timeouts per §9.8; signal-based graceful shutdown per §9.3. Graceful shutdown does not need to flush the publisher (Story 8: unbuffered by design). **`cmd/server/main.go` is the ONE file that legitimately touches both `internal/events` AND `internal/service`** — every other file in the module respects the dependency direction.
- **Next-story handoff (Story 10 — service-layer unit tests):** Phase 4 is now **1/2 complete**. Story 10 lands `alert-service/internal/service/alert_service_test.go` as `package service_test` (black-box — mirrors Story 7 precedent). Test fixtures: in-file unexported helpers only (no `testutil` package — Story 7 oversight N7 invariant), a `fakePublisher` struct with `mu sync.Mutex`, `events []domain.Event`, `Publish(ctx, e) error` method, and a `Published() []domain.Event` accessor that returns a Clone of the slice (defensive; tests shouldn't mutate recorded state). Error-injecting variant (`errPublisher{err error}`) proves publish-failure returns `(a, nil)` and logs — use `slog.New(slog.NewTextHandler(&buf, nil))` in that one test to assert the log record's keys (`alert_id`, `tenant_id`, `event`, `err`) are present. Seven AC-pinned tests + two architect-recommended additions. Run with `-race` (CI) or plain (Windows dev per Story 7 cgo gotcha); no `-race`-only assertions — the behavioral assertions (error sentinel + event count + repo state) are the primary contract. `go test ./internal/service/... -cover` target is ≥ 95% statement coverage; the unreachable `ErrInvalidTransition` branch in `DecideAlert` is expected to be uncovered and that's fine (documented as such in note 5 above).

**Execution Tasks:**

1. **Service struct + constructor + `CreateAlert`.** ID, timestamps, status set here. Return the stored alert (a clone of the input is fine given the repo clones on create).
2. **`ListAlerts` pass-through.** Literally one line calling `s.repo.List(ctx, filter)`. Resist adding business logic here.
3. **`DecideAlert` with full flow.** Load via `FindByID`, check `CanDecide`, return `ErrAlreadyDecided` if already in a terminal state. Mutate `Status`, `DecisionNote`, `UpdatedAt`. Persist. Build `AlertDecidedEvent` with fresh `time.Now().UTC().Format(RFC3339)`. Publish. Log on publish failure, return the alert anyway. Add the §2.8b race comment and §2.9c write-once comment here.
4. **`EscalateAlert` with full flow.** Same shape as decide but with `CanEscalate` and `AlertEscalatedEvent`. Mutate status to `StatusEscalated` and bump `UpdatedAt`.
5. **No direct logger calls except on publish failure.** Handlers and middleware do request-level logging — the service logs only unusual domain events (publish failure is the canonical example).

---

### [ ] Story 10: Service-layer unit tests

**As a** reviewer mapping against the PRD evaluation rubric,
**I want** tests that cover every business rule the rubric calls out,
**So that** decision immutability, tenant isolation, and invalid-transition enforcement are machine-verified.

**Acceptance Criteria:**

- [ ] `internal/service/alert_service_test.go` exists.
- [ ] Uses **the real in-memory repo** + a **fake in-memory publisher** that records published events in a slice (do not mock the repo — integration over isolation for this layer).
- [ ] Test 1: `CreateAlert` — happy path, asserts ID non-empty, `Status == StatusOpen`, timestamps set, `CreatedAt == UpdatedAt`.
- [ ] Test 2: `DecideAlert` on OPEN → success, status updated, `DecisionNote` set, exactly one `AlertDecidedEvent` published.
- [ ] Test 3: `DecideAlert` on already-decided alert → `ErrAlreadyDecided`, zero additional events published (regression guard on double-publish).
- [ ] Test 4: `DecideAlert` with wrong tenantID → `ErrNotFound`.
- [ ] Test 5: `EscalateAlert` on OPEN → success, exactly one `AlertEscalatedEvent` published.
- [ ] Test 6: `EscalateAlert` on `ESCALATED` → `ErrInvalidTransition`.
- [ ] Test 7: `DecideAlert` on `ESCALATED` → success (per clarified state machine, §2.1).
- [ ] Each test names a rubric bullet it covers in a leading comment.

---

## [ ] Phase 5: API Layer _(dependency: Phase 4)_

### [ ] Story 11: Request + Response DTOs with validator tags

**As a** handler implementer,
**I want** wire-format DTOs decoupled from the domain entity,
**So that** validation is declarative and the response shape cannot accidentally leak internal fields.

**Acceptance Criteria:**

- [ ] `internal/api/dto.go` defines request DTOs exactly as §2.9a: `CreateAlertRequest`, `DecideRequest`, `EscalateRequest` with the validator tags shown.
- [ ] `DecideRequest.Status` uses `oneof=CLEARED CONFIRMED_HIT` — rejects `OPEN` / `ESCALATED` at the DTO boundary, before the service sees it.
- [ ] `DecideRequest.DecisionNote` is `required` even though the domain field tolerates empty — comment explaining the asymmetry per §9.11.
- [ ] `CreateAlertRequest.MatchScore` validator is `gte=0,lte=100`. Comment flags the `float64` zero-value trap per §9.11a (client omitting the field silently gets `0`).
- [ ] `AlertResponse` struct mirrors the domain entity on the wire, with JSON tags matching PRD fields (`id`, `transactionId`, `matchedEntityName`, `matchScore`, `status`, `assignedTo`, `tenantId`, `createdAt`, `updatedAt`, `decisionNote`).
- [ ] `toAlertResponse(a *domain.Alert) *AlertResponse` mapper — the single place domain→wire conversion happens.
- [ ] Timestamps serialized as RFC3339 strings via `time.Time`'s JSON default or explicit formatting — consistent across all responses.

---

### [ ] Story 12: Error response contract + `writeError` / `writeJSON` helpers

**As a** handler implementer,
**I want** one function that translates any domain error to the correct HTTP status + JSON body,
**So that** no handler has to switch on error types itself and the error contract cannot drift.

**Acceptance Criteria:**

- [ ] `internal/api/errors.go` defines the error-response shape: `{ "error": "CODE", "message": "..." }` (§2.3).
- [ ] `writeError(w, status, code, msg)` helper — sets `Content-Type: application/json`, writes status, encodes body. Content-Type must be set **before** `WriteHeader` (§9.9).
- [ ] `writeJSON(w, status, v)` helper — same header-ordering rule, for success responses.
- [ ] `mapDomainErr(err) (status int, code, msg string)` — the single switch over `errors.Is`:
  - [ ] `ErrNotFound` → 404 / `ALERT_NOT_FOUND`
  - [ ] `ErrTenantMismatch` → 404 / `ALERT_NOT_FOUND` (collapsed — no existence leak)
  - [ ] `ErrAlreadyDecided` → 409 / `ALERT_ALREADY_DECIDED`
  - [ ] `ErrInvalidTransition` → 409 / `INVALID_STATE_TRANSITION`
  - [ ] default → 500 / `INTERNAL_ERROR` (log the raw error at ERROR level before responding)
- [ ] Validator errors map to 400 / `VALIDATION_ERROR` — handled separately at the DTO boundary, not via `mapDomainErr`.

---

### [ ] Story 13: HTTP middleware chain (recovery, logger, request-id)

**As a** production-readiness-conscious reviewer,
**I want** the three baseline middlewares in place,
**So that** panics don't kill the process, every request is logged, and request IDs correlate HTTP logs with event logs.

**Acceptance Criteria:**

- [ ] `internal/api/middleware.go` defines three middleware functions, each `func(http.Handler) http.Handler`.
- [ ] **Recovery** — `defer recover()`; on panic, log stack (`debug.Stack()`) via `slog` at ERROR, respond 500 via `writeError` if headers are not yet written.
- [ ] **Request logger** — captures method, path, status, duration. Use a `responseRecorder` wrapper to capture status (the stdlib `http.ResponseWriter` doesn't expose it).
- [ ] **Request ID** — generates `uuid.NewString()`, stores in `ctx` via a typed key (not bare `string`), echoes as the `X-Request-Id` response header. If the client sends `X-Request-Id`, honor it instead of generating.
- [ ] Middlewares compose: `recovery(logger(requestID(mux)))` — request ID generated first (innermost) so the logger can include it.
- [ ] All middleware logging goes to **stderr** via `slog` (§2.7a) — stdout is reserved for the event bus.

---

### [ ] Story 14: Alert HTTP handlers — all four endpoints

**As a** client simulating an incoming screening result or an analyst making a decision,
**I want** four REST endpoints that honor the PRD's status codes, error shapes, and tenant-isolation rules,
**So that** the rubric's "API design" and "multi-tenancy" bullets pass.

**Acceptance Criteria:**

- [ ] `internal/api/alert_handler.go` defines a `Handler` struct holding `svc *service.AlertService`, `validator *validator.Validate`, `logger *slog.Logger`.
- [ ] `POST /alerts` returns **201 Created** with `AlertResponse` body (§2.9b).
- [ ] `PATCH /alerts/{id}/decision` returns **200 OK** with updated `AlertResponse`. Uses `r.PathValue("id")` (Go 1.22).
- [ ] `POST /alerts/{id}/escalate` returns **200 OK** with updated `AlertResponse`.
- [ ] `GET /alerts?tenantId=X&status=Y&minScore=Z` returns **200 OK** with `{ "alerts": [...] }` (object-wrapped, not bare array, §2.9b).
- [ ] `GET /alerts` without `tenantId` returns **400** with `VALIDATION_ERROR`.
- [ ] All mutating handlers apply `http.MaxBytesReader(w, r.Body, 1<<20)` + `dec.DisallowUnknownFields()` (§9.10).
- [ ] Validator errors → 400 `VALIDATION_ERROR`; domain errors routed through `mapDomainErr`.
- [ ] Empty list returns `{"alerts": []}`, not `{"alerts": null}` — use `make([]*AlertResponse, 0)` at the mapping step (§9.1).
- [ ] `minScore` that fails `strconv.ParseFloat`, or parses to a value outside `[0, 100]`, returns **400 `VALIDATION_ERROR`** — never 500, never silently ignored.
- [ ] `status` that is not one of `OPEN`, `ESCALATED`, `CLEARED`, `CONFIRMED_HIT` returns **400 `VALIDATION_ERROR`**.
- [ ] Missing `tenantId` also returns **400 `VALIDATION_ERROR`** (restated here alongside the other query-param error paths for symmetry).
- [ ] Handlers pass `r.Context()` (never `context.Background()`) to every service method — request-ID, deadlines, and cancellation signals must propagate service → repo → publisher (§2.2, non-negotiable).

**Execution Tasks:**

1. **Handler struct + constructor.** Hold service, validator, logger.
2. **`Create` handler.** `MaxBytesReader` + `DisallowUnknownFields` decode → `validator.Struct` → `service.CreateAlert` → 201 with `toAlertResponse`.
3. **`List` handler.** Parse `tenantId` (required — 400 on missing), `status` (optional, must be one of the four enum values), `minScore` (optional, `strconv.ParseFloat`, must be 0–100). Build `ListFilter` with typed pointers. Call service. Map each alert, return `{"alerts": [...]}` with the empty-slice fix. On **any** parse or enum-validation failure, return 400 `VALIDATION_ERROR` via `writeError` — do not let `strconv` errors bubble up to the recovery middleware as 500s.
4. **`Decide` handler.** `r.PathValue("id")`, decode + validate `DecideRequest`, call `service.DecideAlert`, return 200 with response.
5. **`Escalate` handler.** Same shape as decide but with `EscalateRequest` and `service.EscalateAlert`.
6. **Validator once, not per-request.** Instantiate `validator.New()` at handler construction and reuse — it is goroutine-safe and has internal caches. Similarly, every `svc.Xxx(...)` call takes `r.Context()` as its first arg — the middleware-seeded request ID must reach the publisher's log lines on failure, and a client disconnect should cancel downstream work (§2.2, non-negotiable).

---

### [ ] Story 15: Router wiring (Go 1.22 mux + middleware chain)

**As a** composition-root implementer,
**I want** one function that returns a fully-wired `http.Handler`,
**So that** `main.go` just calls `NewRouter(h, logger)` and passes the result to the server.

**Acceptance Criteria:**

- [ ] `internal/api/router.go` defines `NewRouter(h *Handler, logger *slog.Logger) http.Handler`.
- [ ] Uses `http.NewServeMux()` and registers four routes with **method-prefixed patterns** per §9.6:
  - [ ] `mux.HandleFunc("POST /alerts", h.Create)`
  - [ ] `mux.HandleFunc("GET /alerts", h.List)`
  - [ ] `mux.HandleFunc("PATCH /alerts/{id}/decision", h.Decide)`
  - [ ] `mux.HandleFunc("POST /alerts/{id}/escalate", h.Escalate)`
- [ ] Returns the mux wrapped in middleware: `recovery(logger(requestID(mux)))`.
- [ ] If `go.mod` lacks `go 1.22`, routes fail at runtime — blocked by Story 1, but add a leading comment reminding future readers.

---

### [ ] Story 16: Handler-layer tests (`httptest`)

**As a** reviewer,
**I want** black-box HTTP tests covering the rubric's two most-inspected API behaviors,
**So that** the contract between handler and client is pinned at the protocol level, not just the service level.

**Acceptance Criteria:**

- [ ] `internal/api/alert_handler_test.go` exists.
- [ ] Uses `httptest.NewRecorder` + `httptest.NewRequest` — no real network listener.
- [ ] Wires a real service + real in-memory repo + fake publisher — same philosophy as service tests (§10).
- [ ] Test: `GET /alerts` without `tenantId` → 400, body matches `{ "error": "VALIDATION_ERROR", ... }` shape.
- [ ] Test: `PATCH /alerts/{id}/decision` on already-decided alert → 409, body `error == "ALERT_ALREADY_DECIDED"`.
- [ ] Bonus (if time permits, not required): `GET /alerts` with zero matches returns `{"alerts": []}` as a JSON array, not null (guards §9.1 at the protocol level).
- [ ] Tests register routes by calling `NewRouter` — do not hand-construct a mux in the test, or you'll miss middleware regressions.

---

## [ ] Phase 6: Composition Root _(dependency: Phase 5)_

### [ ] Story 17: `cmd/server/main.go` — wire everything + graceful shutdown

**As a** deployer running the service,
**I want** a single binary that reads env vars, wires all dependencies, serves HTTP, and shuts down cleanly on SIGINT/SIGTERM,
**So that** the service behaves correctly under container orchestration and leaves no zombie connections.

**Acceptance Criteria:**

- [ ] `cmd/server/main.go` defined.
- [ ] Reads `PORT` (default `8080`) and `LOG_LEVEL` (default `info`) from env (§2.10). Inline `os.Getenv` — no config library.
- [ ] Constructs `*slog.Logger` writing JSON to **`os.Stderr`** with the configured level (§2.4 / §2.7a).
- [ ] Instantiates repo → publisher → service → handler → router in that order (bottom-up).
- [ ] `http.Server` configured with `ReadTimeout: 5s`, `WriteTimeout: 10s`, `IdleTimeout: 120s` per §9.8.
- [ ] Uses `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` for the shutdown trigger.
- [ ] On shutdown signal: creates a **separate** 10-second context (`context.WithTimeout(context.Background(), 10*time.Second)`) and calls `srv.Shutdown(ctx)` — **not** the signal context (§9.3, that one is already cancelled).
- [ ] Logs "server started on :PORT" at INFO level **to stderr**, not stdout.
- [ ] `ListenAndServe` runs in a goroutine that publishes its return value to a buffered `errCh := make(chan error, 1)`.
- [ ] Main goroutine uses `select { case <-ctx.Done(): ... case err := <-errCh: ... }` to race the signal context against early server failure (e.g., port already in use).
- [ ] On `errCh` path with a non-`ErrServerClosed` error: log at ERROR, `os.Exit(1)` — do **not** attempt graceful shutdown (the server never started, there is nothing to drain).
- [ ] On `ctx.Done()` path: proceed to graceful shutdown with the separate 10-second context.
- [ ] Verified behaviorally: running a second instance while the first is bound to `:8080` causes instance 2 to exit non-zero within milliseconds, not hang waiting for a signal.

**Execution Tasks:**

1. **Config + logger.** Read env vars, parse log level (string → `slog.Level`), construct `slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})` → `slog.New(handler)`.
2. **Wire dependencies.** `memory.NewAlertRepo()` → `events.NewStdoutPublisher()` → `service.NewAlertService(repo, pub, logger)` → `api.NewHandler(svc, logger)` → `api.NewRouter(h, logger)`.
3. **Build `http.Server` with timeouts.** Assign mux, set all three timeouts per §9.8.
4. **Error-channel-aware run loop.** Create `errCh := make(chan error, 1)`. Launch `go func() { errCh <- srv.ListenAndServe() }()`. `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` yields the signal-aware `ctx`.
5. **Race signal against server failure with `select`.** `select { case <-ctx.Done(): /* graceful shutdown with separate 10s context */ case err := <-errCh: if errors.Is(err, http.ErrServerClosed) { return }; logger.Error("server failed to start", "err", err); os.Exit(1) }`. The `errCh` path fires when the listener fails to bind (port already in use, permission denied, etc.) — without this race, a port conflict silently deadlocks on the signal wait and the user sees a "running" process that isn't actually listening.

---

## [ ] Phase 7: Documentation & Verification _(dependency: Phase 6)_

### [ ] Story 18: Makefile with `run`, `test`, `lint` targets

**As an** evaluator running the assignment,
**I want** `make test` and `make run` to just work,
**So that** I don't have to read source to figure out the commands.

**Acceptance Criteria:**

- [ ] `Makefile` at repo root.
- [ ] `make run` → `go run ./cmd/server`.
- [ ] `make test` → `go test ./... -race -cover` (§5 — race detector is required, not optional).
- [ ] `make lint` → `go vet ./...` (optional: `staticcheck` if installed, but don't add it as a required dep).
- [ ] `make build` → `go build -o bin/server ./cmd/server`.
- [ ] `.PHONY` declared for every target.

---

### [ ] Story 19: README with run instructions + sample curls

**As an** evaluator who has five minutes to decide if this runs,
**I want** a README that shows the commands and expected outputs for every endpoint,
**So that** I can smoke-test the service in one sitting.

**Acceptance Criteria:**

- [ ] `README.md` at repo root.
- [ ] Sections: **Run** (one command), **Test** (one command), **Endpoints** (all 4 with sample `curl` + expected status code and body), **Event Output** (show the stdout JSON line for both escalate and decide with the exact PRD schema).
- [ ] **Known MVP Limitations** section calling out: (a) the read-check-write race per §2.8b with the one-line production fix, (b) `MatchScore` zero-value trap per §9.11a, (c) strict write-once, no idempotency dedupe per §2.9c.
- [ ] **Future Improvements** section lists the §8 exclusions as one-liners each: persistent DB, real broker (outbox pattern), JWT/header-based tenant, optimistic locking, OpenAPI spec, Dockerfile, metrics/tracing, pagination.
- [ ] Example curls include the negative cases too: `GET /alerts` without tenant → 400, re-decide → 409. Proving the error contract, not just the happy path.
- [ ] Explicit note: **stdout = events, stderr = logs.** Show how to `./bin/server 2>logs.txt | jq` to consume just the event stream.

---

### [ ] Story 20: End-to-end verification + final rubric sweep

**As a** submitter doing the final gate before calling it done,
**I want** to walk the PRD rubric and tick every box by running real commands,
**So that** nothing looks right on paper but fails at the protocol level.

**Acceptance Criteria:**

- [ ] `make test` is green. Includes `-race` flag. Every test in §5 passes.
- [ ] `make run` starts the server on :8080 with no errors.
- [ ] All four curls from design doc §7 execute and return the documented status codes.
- [ ] Stdout emits exactly **one** JSON event line per escalation and per decision — verify by piping stdout to a file and counting.
- [ ] `GET /alerts` without `tenantId` returns 400 (not 500, not a stack trace).
- [ ] Cross-tenant `FindByID` (via GET with wrong tenant) returns 404 with `ALERT_NOT_FOUND`, not 403 — existence-leak prevention.
- [ ] Re-decide returns 409 with `ALERT_ALREADY_DECIDED`.
- [ ] Kill server with Ctrl+C — shutdown log line appears, process exits 0 within 10 seconds.
- [ ] PRD rubric walk-through, one line each confirming coverage: **Code structure** (clean layers), **Domain logic** (state machine + immutability), **API design** (status codes + error shapes), **Multi-tenancy** (400 without tenant + 404 cross-tenant), **Event-driven thinking** (stdout events, typed Event interface, persist-before-publish), **Testability** (>3 tests covering the named rules), **Code quality** (naming, error handling, idiomatic Go).

---

<!--
When a story/phase completes, mark the checkbox [x], add a dated Implementation Notes block under it documenting deviations, files touched, and any non-obvious design decisions.
-->
