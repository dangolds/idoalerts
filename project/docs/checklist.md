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

### [ ] Story 5: Port interfaces (`AlertRepository`, `EventPublisher`, `ListFilter`)

**As a** service-layer implementer,
**I want** the interfaces my service depends on defined in `domain`,
**So that** infrastructure implementations satisfy them from the outside (hexagonal / ports-and-adapters).

**Acceptance Criteria:**

- [ ] `internal/domain/ports.go` defines `AlertRepository` with `Create`, `FindByID`, `List`, `Update` — all taking `ctx context.Context` as first arg (§2.2, non-negotiable).
- [ ] `FindByID(ctx, tenantID, id)` signature — tenant is a required scoping parameter, not stuffed into an options struct.
- [ ] `EventPublisher` interface: `Publish(ctx context.Context, event Event) error`.
- [ ] `ListFilter` struct: `TenantID string` (required), `Status *Status` (optional), `MinScore *float64` (optional). Pointers for the optionals make "unset" unambiguous.
- [ ] Interfaces live in `domain` so service depends on domain, not the other way around (Go interface-satisfaction is structural — no `implements` keyword needed on impls).

---

## [ ] Phase 3: Infrastructure _(dependency: Phase 2)_

### [ ] Story 6: In-memory `AlertRepository` implementation

**As a** service-layer implementer,
**I want** a thread-safe, pointer-safe in-memory repo,
**So that** the service can round-trip alerts without a database and without leaking mutable references to stored state.

**Acceptance Criteria:**

- [ ] `internal/storage/memory/alert_repo.go` defines `AlertRepo` struct with `alerts map[string]*domain.Alert` and `mu sync.RWMutex`.
- [ ] `NewAlertRepo()` constructor returns a pointer with an initialized map.
- [ ] Implements all four `AlertRepository` methods with context as first arg (even if unused for MVP — the interface demands it).
- [ ] **Cross-tenant reads return `ErrNotFound`** (not `ErrTenantMismatch` to the caller — existence leak prevention, §2.3 / §4).
- [ ] `List` returns results sorted by `CreatedAt` descending (§9.12, deterministic — map iteration is randomized).
- [ ] `List` returns `make([]*domain.Alert, 0)` when empty, never `nil` — consumers will marshal this to JSON and `nil` → `null` breaks the contract (§9.1).
- [ ] Tenant/status/score filtering happens in `List` before sort (O(n) scan is fine for MVP).

**Execution Tasks:**

1. **Scaffold struct + constructor + `Create`.** Initialize the map. `Create` grabs `mu.Lock()`, stores `a.Clone()` (§2.8a — storing the raw pointer lets callers mutate locked state outside the lock), returns nil.
2. **`FindByID` with tenant scoping.** `mu.RLock()`, lookup by ID, check `existing.TenantID == tenantID`, return `existing.Clone()`. Both "not present" and "wrong tenant" map to `ErrNotFound` — do **not** leak existence.
3. **`Update` with tenant scoping + existence check.** `mu.Lock()`, look up by ID, verify tenant match, store `a.Clone()`. Missing ID or cross-tenant → `ErrNotFound`.
4. **`List` with filter + sort + non-nil return.** `mu.RLock()`, iterate, apply `TenantID` (required), then `Status` / `MinScore` filters if set. Append clones. Sort by `CreatedAt` descending. Return slice (pre-initialize with `make` so zero matches → `[]`, not nil).
5. **Package-level comment** explaining the clone-on-read/write invariant and pointing at §2.8a. This is the kind of non-obvious rule that earns a comment per the coding guidelines.

---

### [ ] Story 7: Storage concurrency + CRUD tests

**As a** reviewer,
**I want** `-race`-clean tests proving the `RWMutex + Clone` pattern holds under load,
**So that** the "it's just a map" implementation cannot regress silently.

**Acceptance Criteria:**

- [ ] `internal/storage/memory/alert_repo_test.go` exists.
- [ ] Test: sequential `Create → FindByID → Update → FindByID` round-trip (happy-path sanity check).
- [ ] Test: cross-tenant `FindByID` returns `ErrNotFound` (not the alert, not `ErrTenantMismatch`).
- [ ] Test: cross-tenant `Update` returns `ErrNotFound`.
- [ ] Test: concurrent goroutines spamming `Create` / `FindByID` / `Update` across distinct and shared IDs — no panics, no data races. `sync.WaitGroup`, ~50 goroutines × 100 iterations each is plenty.
- [ ] Test: after a `FindByID` caller mutates the returned pointer, a subsequent `FindByID` still returns the original values (proves the clone-on-read).
- [ ] `go test ./internal/storage/memory/... -race` passes.
- [ ] Explicit comment in the concurrency test pointing at §2.8b — we are testing heap-safety, **not** business-rule atomicity under concurrent decides.

---

### [ ] Story 8: Stdout `EventPublisher`

**As a** service-layer implementer,
**I want** a publisher that writes JSON-encoded events to **stdout** (not stderr),
**So that** the simulated message-broker stream is cleanly consumable by `tail -f | jq` and stays uncontaminated by application logs.

**Acceptance Criteria:**

- [ ] `internal/events/stdout_publisher.go` defines `StdoutPublisher` struct and `NewStdoutPublisher()` constructor.
- [ ] `Publish(ctx, event)` JSON-encodes `event` and writes one line to `os.Stdout` followed by `\n`. Use `json.NewEncoder(os.Stdout).Encode(event)` (adds newline automatically).
- [ ] On encode failure, return the error — the service decides what to do (for stdout this is effectively never, but honor the interface).
- [ ] **Do not write logs to stdout anywhere in this package.** Stdout is the event bus. Logs go to stderr via `slog` (§2.7a).
- [ ] No tests required for the publisher itself (trivial I/O glue); it is covered transitively by service tests using a fake publisher.

---

## [ ] Phase 4: Service Layer _(dependency: Phase 3)_

### [ ] Story 9: `AlertService` — four orchestration methods

**As a** handler-layer implementer,
**I want** a service that owns the load-check-mutate-persist-publish flow,
**So that** handlers stay dumb (parse → call → render) and business rules are centralized.

**Acceptance Criteria:**

- [ ] `internal/service/alert_service.go` defines `AlertService` struct holding `repo domain.AlertRepository`, `pub domain.EventPublisher`, `logger *slog.Logger`.
- [ ] `NewAlertService(repo, pub, logger)` constructor.
- [ ] `CreateAlert(ctx, input) (*domain.Alert, error)` — generates `uuid.NewString()` for ID, sets `Status = StatusOpen`, `CreatedAt/UpdatedAt = time.Now().UTC()` **in the service**, not in the handler or repo (§2.8). Persists via `repo.Create`. Returns the created alert.
- [ ] `ListAlerts(ctx, filter) ([]*domain.Alert, error)` — thin pass-through to `repo.List`.
- [ ] `DecideAlert(ctx, tenantID, id, newStatus, note) (*domain.Alert, error)` — `FindByID → CanDecide() check → mutate → Update → publish`. If `!CanDecide()` because status is already `CLEARED`/`CONFIRMED_HIT`, return `ErrAlreadyDecided` (409). Any other invalid state returns `ErrInvalidTransition`.
- [ ] `EscalateAlert(ctx, tenantID, id) (*domain.Alert, error)` — `FindByID → CanEscalate() check → mutate → Update → publish`. Wrong state → `ErrInvalidTransition`.
- [ ] **Persist before publish** (§2.7): if `repo.Update` fails, no event is emitted. If publish fails, log at ERROR and return success anyway (state is authoritative).
- [ ] Event `Timestamp` is constructed **at publish time** (`time.Now().UTC().Format(time.RFC3339)`), not derived from `UpdatedAt` (§9.13).
- [ ] Inline code comment at the decide-check flagging the known read-check-write race (§2.8b) with a one-line explanation of the production fix.
- [ ] Inline code comment at the decide flow noting strict write-once per PRD (§2.9c) — no idempotency dedupe.

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
