# Alert Management — Backend

> Last reviewed: 2026-04-13

This document captures the backend layer of the Alert Management service — a Go microservice rooted at `alert-service/`. The service follows Clean / hexagonal layering: a pure `domain` package holding the entity, enums, errors, events, and ports; a `service` package orchestrating use cases; a `storage/memory` adapter and an `events` stdout adapter satisfying the ports from outside; and an `api` package wrapping it all in HTTP. The **domain** layer (Stories 2–5 — entity, errors, events, ports) and the **infrastructure adapters** for storage (Stories 6–7 — in-memory repo + 12-test concurrency-safe suite) and events (Story 8 — mutex-serialized stdout publisher) are implemented; service (9–10), API (11–16), and composition-root wiring (17) are specified by the design doc and per-story checklist but not yet coded.

## Project Structure

```text
alert-service/
├── cmd/
│   └── server/          # main.go (Story 17)
├── internal/
│   ├── domain/          # Entity, Status, errors, events, ports — layer complete
│   │   ├── alert.go
│   │   ├── errors.go
│   │   ├── events.go
│   │   └── ports.go
│   ├── service/         # Use cases (Stories 9–10)
│   ├── storage/
│   │   └── memory/      # In-memory AlertRepository (Story 6)
│   ├── events/          # Stdout EventPublisher (Story 8)
│   └── api/             # DTOs, router, handlers, middleware, error mapper (Stories 11–16)
├── go.mod               # module github.com/dangolds/idoalerts/alert-service; go 1.22
└── go.sum
```

Module path tracks the git remote: `github.com/dangolds/idoalerts/alert-service` (see project-wide ADR D2). Go directive floor is pinned at `1.22` — the minimum for `net/http` method-prefixed mux patterns (D3, DesignAndBreakdown §9.6). Dep-auto-bumps from `go mod tidy` (e.g. `validator/v10` declaring `1.25`) must be manually restored with `go mod edit -go=1.22`.

## Data Model

### `domain.Alert`

```go
type Alert struct {
    ID                string
    TenantID          string
    TransactionID     string
    MatchedEntityName string
    MatchScore        float64
    Status            Status
    AssignedTo        *string       // optional — real nullability
    DecisionNote      string        // "" means "no note yet"; DTO keeps required
    CreatedAt         time.Time
    UpdatedAt         time.Time
}
```

`DecisionNote` is a plain `string` on the entity but the wire DTO `DecideRequest.DecisionNote` is `required` via validator tags. This DTO-vs-domain asymmetry is intentional and commented inline in `alert.go` — a seeded/legacy alert may have `""`, but an analyst submitting a new decision must provide a note. See DesignAndBreakdown §9.11.

`AssignedTo` is `*string` because it represents a real optional relationship (an alert may be unassigned).

### `domain.Status`

Typed string enum. Wire values exactly match the PRD:

```go
type Status string

const (
    StatusOpen         Status = "OPEN"
    StatusEscalated    Status = "ESCALATED"
    StatusCleared      Status = "CLEARED"
    StatusConfirmedHit Status = "CONFIRMED_HIT"
)
```

Declared as typed consts (`Status = "OPEN"`, not bare `= "OPEN"`) so callers cannot accidentally pass a raw string where `Status` is expected.

### State-predicate methods

```go
func (a *Alert) CanDecide() bool   { return a.Status == StatusOpen || a.Status == StatusEscalated }
func (a *Alert) CanEscalate() bool { return a.Status == StatusOpen }
```

Pure, stateless invariants over `Alert`'s own fields — these belong on the entity per the DDD rule "data-intrinsic predicates on the aggregate; orchestration in the service." The service retains full ownership of load → check → mutate → persist → publish. See DesignAndBreakdown §2.1. Rejected alternatives (`a.Decide(...)` / `a.Escalate(...)` transition methods; `NewAlert(...)` factory) are captured in the Story 2 Implementation Notes.

### `domain.Alert.Clone`

```go
func (a *Alert) Clone() *Alert {
    cp := *a
    if a.AssignedTo != nil {
        v := *a.AssignedTo
        cp.AssignedTo = &v
    }
    return &cp
}
```

Deep copy that reallocates the backing string for `*AssignedTo`. The in-memory repository calls `Clone` on read (so callers cannot mutate stored state) and stores `Clone` on write. `time.Time` is a value type so the shallow `*a` handles it; only pointer fields need special treatment. An in-file comment above `Clone` flags "keep in sync" for any future slice / map / pointer fields — this is the escape hatch against §2.8a's pointer trap.

## Errors

`internal/domain/errors.go`:

```go
var (
    ErrNotFound          = errors.New("alert not found")
    ErrAlreadyExists     = errors.New("alert already exists")         // Create collision; enforces port "new alerts only" contract
    ErrAlreadyDecided    = errors.New("alert has already been decided")
    ErrInvalidTransition = errors.New("invalid state transition")
    ErrTenantMismatch    = errors.New("tenant mismatch")              // never client-surfaced; repo collapses to ErrNotFound
)
```

All plain `errors.New` sentinels — no custom types, no codes, no wrapping at sentinel level. Service and API layers compare with `errors.Is`. The HTTP mapper (Story 12) translates to:

| Sentinel | HTTP | `error` code | Notes |
|---|---|---|---|
| `ErrNotFound` | 404 | `ALERT_NOT_FOUND` | |
| `ErrTenantMismatch` | 404 | `ALERT_NOT_FOUND` | Collapsed at repo boundary; never surfaces |
| `ErrAlreadyExists` | 409 | `ALERT_ALREADY_EXISTS` | Repo Create collision — enforces port "new alerts only" contract |
| `ErrAlreadyDecided` | 409 | `ALERT_ALREADY_DECIDED` | |
| `ErrInvalidTransition` | 409 | `INVALID_STATE_TRANSITION` | |
| DTO validation failures | 400 | `VALIDATION_ERROR` | Produced by validator/v10 |

`ErrTenantMismatch` is kept distinct from `ErrNotFound` so the repo can log the two cases separately if needed, and so future policy hooks (e.g., cross-tenant audit logging) don't need to reshape the repository API. `ErrAlreadyExists` was added during Story 6 after reviewers independently caught that the original "silent overwrite in Create" plan contradicted the port doc committed in Story 5 — see that story's Implementation Notes.

## Events

`internal/domain/events.go` defines the publishable-event shape.

```go
type Event interface {
    EventName() string
}

type AlertDecidedEvent struct {
    Event     string `json:"event"`     // "alert.decided"
    AlertID   string `json:"alertId"`
    TenantID  string `json:"tenantId"`
    Decision  string `json:"decision"`  // CLEARED | CONFIRMED_HIT — NOT "status"
    Timestamp string `json:"timestamp"` // RFC3339, publish time
}
func (e AlertDecidedEvent) EventName() string { return e.Event }

type AlertEscalatedEvent struct {
    Event     string `json:"event"`     // "alert.escalated"
    AlertID   string `json:"alertId"`
    TenantID  string `json:"tenantId"`
    Timestamp string `json:"timestamp"`
}
func (e AlertEscalatedEvent) EventName() string { return e.Event }
```

Four invariants are pinned by inline code comments and by entries in `project/docs/gotchas.md`:

1. **`decision` not `status`** — PRD example is authoritative; a broker consumer looking for `decision` would see `null` if this regresses.
2. **`Timestamp string`, not `time.Time`** — RFC3339 formatted at publish time in the service (`time.Now().UTC().Format(time.RFC3339)`), never derived from `Alert.UpdatedAt`. "When the event fired" is semantically distinct from "when the entity last changed."
3. **Typed `Event` marker, not `any`** — `Publisher.Publish` takes `Event` so arbitrary structs can't slip through and emit malformed JSON.
4. **`EventName()` returns `e.Event`** — the wire field and the type identity are the same string by definition; a field-read single source of truth prevents wire-vs-routing divergence. An earlier version used literal-return plus package consts (`EventNameAlertDecided`, commit `1bacc2b`); that was reversed in `4d57190` — DRY doesn't pay at N=2, and double-sourcing invites drift.

The service will construct events via struct literal at the two call sites: `domain.AlertDecidedEvent{Event: "alert.decided", AlertID: a.ID, ..., Timestamp: time.Now().UTC().Format(time.RFC3339)}`.

## Ports

`internal/domain/ports.go` (Story 5):

```go
type AlertRepository interface {
    Create(ctx context.Context, a *Alert) error
    FindByID(ctx context.Context, tenantID, id string) (*Alert, error)
    List(ctx context.Context, f ListFilter) ([]*Alert, error)
    Update(ctx context.Context, a *Alert) error
}

type EventPublisher interface {
    Publish(ctx context.Context, event Event) error
}

type ListFilter struct {
    TenantID string   // required
    Status   *Status  // optional — nil means no status filter
    MinScore *float64 // optional — nil means no score filter
}
```

The port doc pins only the **correctness invariants every impl must honor**: cross-tenant `FindByID`/`Update` collapse to `ErrNotFound` at the repo boundary (defense in depth, §2.3 / §2.8a, never surface `ErrTenantMismatch` to callers). Impl-flavored concerns — non-nil empty slice on `List` (§9.1), `CreatedAt` descending sort (§9.12), clone-on-read/write (§2.8a) — are documented on the in-memory impl itself, not on the interface. A future DB impl might deliver pre-sorted via an index or stream results through a channel; pinning those on the port would over-specify.

`ListFilter` is naked data — no constructor, no validator method. Handler parses query → builds the struct → service passes through → repo scans (O(n) for MVP). `*Status` / `*float64` make "unset" unambiguous (a typed-zero-value filter would be incorrect: `Status("")` is "no filter", not "alerts with no status"). The service layer trusts the port contract and does not re-check tenant scoping.

## In-Memory Repository

`internal/storage/memory/alert_repo.go` (Story 6) — the sole current implementation of `domain.AlertRepository`, backed by a map plus an `sync.RWMutex`.

```go
type AlertRepo struct {
    mu     sync.RWMutex
    alerts map[string]*domain.Alert
}

func NewAlertRepo() *AlertRepo
```

**Lock discipline.** `Create` and `Update` take `mu.Lock()`; `FindByID` and `List` take `mu.RLock()`. All four methods `defer` the corresponding unlock at the top — no multi-path unlocks. All receivers are `*AlertRepo` (value receivers would copy the mutex — a classic Go trap).

**Clone boundary (§2.8a).** Every lock-boundary entry and exit clones. `Create` and `Update` store `a.Clone()` (callers' pointers never alias map state). `FindByID` and `List` return `a.Clone()` (callers cannot mutate stored state outside the lock). The clone is absolute, not conditional on caller trust — the cost is one allocation per operation and the `-race` guarantee (Story 7) depends on it. Future pointer/slice/map fields on `Alert` must extend `Alert.Clone` — the "keep in sync" comment in `alert.go` enforces this.

**`List` contract (§9.1, §9.12).** Pre-allocates `out := make([]*domain.Alert, 0)` before the range loop with an inline comment so a refactor to `var out []*Alert` can't silently regress the JSON contract (nil → `null` vs. `[]`). Filters before sorting (correctness: pagination semantics want the newest-matching, not matching-from-the-newest-N; performance: sort discarded entries is waste). Uses `sort.SliceStable` over `sort.Slice` for free determinism when two fast `Create` calls land on the same `CreatedAt` nanosecond.

**Cross-tenant collapse.** `FindByID` and `Update` return `domain.ErrNotFound` for both "missing ID" and "ID exists under different tenant" — never leak cross-tenant existence. `ErrTenantMismatch` is deliberately never returned from this repo; it remains reserved as an internal signal for future policy hooks per the Story 3 sentinel doc.

**`Create` collision.** Returns `domain.ErrAlreadyExists` if `a.ID` already lives in the map. Enforces the port contract "Create is for new alerts only; Update requires an existing row." The service generates UUIDs so natural collisions are impossible, but a retry path with a reused UUID, or a service bug passing a stored alert back into `Create`, surfaces cleanly instead of silently overwriting.

**Compile-time port guard.** `var _ domain.AlertRepository = (*AlertRepo)(nil)` at file scope fails to build the moment the port drifts. No test needed.

**What this repo deliberately does not expose.** No `Delete`, no `Exists`, no `CompareAndSwap`, no streaming/channel `List`, no batch APIs, no in-memory indexes on filter fields. Atomicity of compound operations (read-check-write for decisions) is the **service** layer's concern per §2.8b — this repo provides per-method atomicity only. A production DB impl would close the decide-race with `SELECT ... FOR UPDATE`; for MVP the gap is documented and accepted.

## Service Layer

`internal/service/alert_service.go` (Story 9) owns the load-check-mutate-persist-publish orchestration for the four use cases. Handlers (future Story 14) stay dumb (parse → call → render); business rules are centralized here. See DesignAndBreakdown §2.1 / §2.7 / §2.7b / §2.8 / §2.8b / §2.9c / §9.13 for the underlying invariants.

### Struct + constructor

```go
type AlertService struct {
    repo   domain.AlertRepository
    pub    domain.EventPublisher
    logger *slog.Logger
}

func NewAlertService(repo domain.AlertRepository, pub domain.EventPublisher, logger *slog.Logger) *AlertService
```

`AlertService` is an orchestrator, not a port-implementer — **no `var _ ... = (*AlertService)(nil)` compile-time guard** (those belong on the infrastructure adapters that satisfy domain ports, not on the service that consumes ports). The constructor does not defensive-nil-check its args; composition-root wiring guarantees non-nil values and a `nil` at that seam should panic loudly on first call, not silently no-op behind a guard. The injected `*slog.Logger` is used **only** for publish-failure logging — request-level logging is owned by HTTP middleware (Story 13).

### `CreateAlertInput`

```go
type CreateAlertInput struct {
    TenantID, TransactionID, MatchedEntityName string
    MatchScore                                 float64
    AssignedTo                                 *string
}
```

Plain Go fields — **no `json:` / `validate:` tags**. The type is the service-level input shape, not a wire DTO; validator tags live on the handler DTO (`CreateAlertRequest` in Story 11), keeping a two-DTO discipline with a bright line at the handler. Server-owned fields (`ID`, `Status`, `CreatedAt`, `UpdatedAt`, `DecisionNote`) are intentionally absent so §2.8's "service owns them, not handler or repo" invariant is enforced at the type level — a caller cannot smuggle a pre-populated ID into Create. See ADR **AM-6** for the full rationale.

### Orchestration flows

**`CreateAlert(ctx, in CreateAlertInput) (*Alert, error)`.** Generates `uuid.NewString()` for the ID. Sets `Status = StatusOpen`, `DecisionNote = ""` explicitly, and uses a single `now := time.Now().UTC()` value for both `CreatedAt` and `UpdatedAt` — two separate `time.Now()` calls could differ by nanoseconds and break the "equal on creation" invariant that downstream consumers (tests, auditors) may rely on. `DecisionNote = ""` carries the AM-2 invariant: the domain allows empty string (seeded/legacy/new alerts); the DTO still requires it at the wire boundary. Calls `repo.Create(ctx, a)` and propagates `ErrAlreadyExists` unchanged — a UUID collision is effectively unreachable, but honoring the port contract keeps the boundary clean. No event is emitted on Create per PRD (only decide + escalate emit). Returns the stored alert.

**`ListAlerts(ctx, f ListFilter) ([]*Alert, error)`.** One-line pass-through to `repo.List`. The service trusts the port contract: tenant scoping, optional status / `minScore` filtering, deterministic `CreatedAt`-descending sort, and non-nil empty slice on zero matches (§9.1 / §9.12) are all repository-layer responsibilities. **No business logic belongs in this method** — adding any would duplicate repo behavior and create two sources of truth.

**`DecideAlert(ctx, tenantID, id, newStatus, note) (*Alert, error)`.** Four-stage flow:
1. **`newStatus` defense-in-depth guard** (top of method, before the load). If `newStatus` is not `StatusCleared` or `StatusConfirmedHit`, return `ErrInvalidTransition` immediately. The DTO `oneof=CLEARED CONFIRMED_HIT` validator protects the HTTP path, but the service is a package-boundary port callable from non-HTTP contexts (tests, future internal tools). An invalid `newStatus` slipping through would mutate Status back to OPEN on an OPEN alert and emit `{"decision":"OPEN"}`, corrupting the event audit stream.
2. **Load + terminal-state disambiguation.** `FindByID` (repo collapses cross-tenant to `ErrNotFound` per §2.3 / §2.8a, so no cross-tenant existence leaks). Then `!CanDecide()` splits into two error codes: terminal status (`CLEARED` / `CONFIRMED_HIT`) returns `ErrAlreadyDecided` (409 write-once per §2.9c); any other non-decidable state returns `ErrInvalidTransition`. The "other" branch is unreachable today given the closed 4-status enum but guards future non-terminal non-decidable statuses (e.g., a hypothetical `ARCHIVED`) from being mislabeled "already decided." The §2.8b accepted read-check-write race comment sits inline at the `CanDecide` check — colocated with the race window, not abstracted into the method doc.
3. **Mutate + persist (§2.7).** Set `Status`, `DecisionNote`, `UpdatedAt`, then `repo.Update`. If Update fails, return the error and **do NOT publish** — persist-before-publish is load-bearing.
4. **Publish (§9.13, §2.7).** Build `AlertDecidedEvent` via struct literal at the call site (AM-4 single-source-of-truth: `Event: "alert.decided"` literal, no package const). `Timestamp = time.Now().UTC().Format(time.RFC3339)` at publish time — **never** `a.UpdatedAt.Format(...)`; "when the event fired" is semantically distinct from "when the entity last changed." `Decision: string(newStatus)`. On `Publish` error, log at ERROR via `logger.ErrorContext(ctx, ...)` with four typed fields (`alert_id`, `tenant_id`, `event`, `err`) and **return success with the updated alert** — repository state is authoritative; publish failure does not fail the operation. The §2.9c strict-write-once rule lives in the method-level doc comment (operation property, not a per-branch footnote).

**`EscalateAlert(ctx, tenantID, id) (*Alert, error)`.** Same shape as Decide but with `CanEscalate` and `AlertEscalatedEvent`. `CanEscalate()` returns true only for `StatusOpen`, so any non-OPEN source state returns `ErrInvalidTransition`. The §2.8b race comment is echoed at the `CanEscalate` check site — the race window has the same shape as Decide, so the invariant is colocated with the code, not centralized elsewhere. Mutation: `Status = StatusEscalated` + `UpdatedAt = time.Now().UTC()`. Event struct literal uses `Event: "alert.escalated"` and the publish-time RFC3339 timestamp.

### Event construction — struct literals, no helper

Two call sites (decide, escalate) construct events inline via struct literal. No `buildDecidedEvent(...)` / `buildEscalatedEvent(...)` helper — DRY doesn't pay at N=2, and a helper would either have to accept the alert plus extra args (no simpler than inline) or hide the §9.13 `Timestamp` invariant behind a layer. The wire-critical field names (`AlertID` not `ID`, `Decision` not `Status`) are explicit at every literal; `go vet`'s struct-field typo check catches regressions at build time.

### Publish-failure contract

`Publish` returning a non-nil error logs once via `slog.ErrorContext` with exactly four fields — `alert_id`, `tenant_id`, `event` (e.g. `"alert.decided"`), `err` — and returns the updated alert with `nil` error to the caller. The rationale is §2.7: persist already succeeded, state is authoritative, and failing the client-facing operation after the DB commit would silently diverge storage state from the event stream. Only the `logger` field is ever used for structured output in this package; any `log.Printf` or `logger.Info` added elsewhere in the service file will fail review. Field cap is 4 — no request IDs (middleware owns those), no stack traces, no retry counters.

### Imports

`context`, `log/slog`, `time`, `github.com/google/uuid`, and the local `internal/domain` package — nothing else. In particular, the service does **not** import `internal/events` or `internal/storage/memory`: it depends on the ports (`domain.AlertRepository`, `domain.EventPublisher`, `domain.Event`), never on the adapters. This preserves the hexagonal direction (service → domain; adapters → domain; service never → adapters).

### Testing (Story 10, pending)

Covered in the **Testing** section below. The service is consumed with a real `memory.AlertRepo`, a fake publisher (in-memory event recorder satisfying `domain.EventPublisher`), and a discarding `slog.Logger` (`slog.New(slog.NewJSONHandler(io.Discard, nil))`) so publish-failure log lines do not pollute test output. No `internal/events` dependency in test code — the fake publisher is the contract surface.

## HTTP API (Planned — Stories 11–16)

All endpoints emit the single error shape `{"error": "CODE", "message": "text"}` via one shared `api.writeError` helper.

### `POST /alerts`

Creates a new alert. Request body:

```json
{
  "tenantId": "t1",
  "transactionId": "tx-42",
  "matchedEntityName": "ACME Sanctioned Corp",
  "matchScore": 92.5,
  "assignedTo": "analyst-007"
}
```

`assignedTo` is optional; `matchScore` must be in `[0, 100]`. Response: **201 Created** with the created `AlertResponse`. ID and timestamps are generated in the service (`uuid.NewString()`, `time.Now().UTC()`), never in the handler.

### `GET /alerts?tenantId=X&status=Y&minScore=Z`

Lists alerts for the given tenant. `tenantId` is **required**; missing returns **400**. `status` must be one of the four enum values or 400. `minScore` must parse to a float in `[0, 100]` or 400. Response: **200** with `{"alerts": [...]}`. Empty result returns `[]`, not `null` (Go slice-init gotcha, §9.1).

### `PATCH /alerts/{id}/decision`

Submits a decision. Request body:

```json
{
  "tenantId": "t1",
  "status": "CLEARED",
  "decisionNote": "False positive — name collision with public figure."
}
```

`status` must be exactly `CLEARED` or `CONFIRMED_HIT` (DTO-level `oneof` in validator/v10 rejects `OPEN`/`ESCALATED` before the service sees it). `decisionNote` is required. Response: **200** with the updated alert, **409** `ALERT_ALREADY_DECIDED` if the alert is already decided, **404** `ALERT_NOT_FOUND` if id/tenant don't match, **400** on validation failure. Emits one `alert.decided` event on stdout after a successful write.

### `POST /alerts/{id}/escalate`

Escalates. Request body: `{"tenantId": "t1"}`. Response: **200** with the updated alert, **409** `INVALID_STATE_TRANSITION` if the current status is not `OPEN`, **404** `ALERT_NOT_FOUND` on mismatch. Emits one `alert.escalated` event on stdout.

## Middleware (Planned — Story 13)

Three small middlewares, chained in `main`:

1. **Recovery** — catches panics, logs the stack to stderr, returns 500 with a generic error body.
2. **Request logger** — method, path, status, duration via `slog` to stderr.
3. **Request ID** — UUID per request, stashed in `ctx`, echoed as `X-Request-Id`. Helps correlate event stdout lines with HTTP stderr lines.

No auth middleware — explicitly out of scope per the PRD.

## Stdout Event Publisher

`internal/events/stdout_publisher.go` (Story 8) is the sole current implementation of `domain.EventPublisher`.

```go
type StdoutPublisher struct {
    mu sync.Mutex
    w  io.Writer
}

func NewStdoutPublisher() *StdoutPublisher              // pins os.Stdout
func (p *StdoutPublisher) Publish(ctx context.Context, event domain.Event) error
```

**Single-line invariant.** `Publish` serializes one call via `p.mu`, then calls `json.NewEncoder(p.w).Encode(event)` — which appends the trailing newline itself. Exactly one JSON line per published event, no leading/trailing noise.

**Mutex rationale (§2.7a).** `json.Encoder.Encode` is not documented as goroutine-safe, and concurrent writes on the shared `io.Writer` can interleave bytes. POSIX `Write ≤ PIPE_BUF` atomicity applies only to pipes (not terminals/files); Windows `WriteFile` makes no atomicity guarantee at all. Story 17 wires this publisher behind concurrent HTTP handlers (one goroutine per request), so concurrent `Publish` is the default case — a torn JSON line silently breaks `tail -f | jq` consumers. The mutex closes this gap at negligible cost (~200-byte events; lock hold-time dominated by the encode itself).

**Encoder-per-call, not cached.** The struct stays minimal (`{mu, w}`) and `w` remains the single source of truth. Caching a `*json.Encoder` in the struct would save one allocation per event but adds struct state and — outside the mutex — would be a goroutine-safety hazard. Alloc cost is invisible at MVP event volume; per-call wins on simplicity.

**Writer seam is internal.** The `w io.Writer` field is unexported and has no public setter — stdout is the event bus by design (§2.7a). A public writer-injection constructor would invite a second call site that bypasses the invariant. Escape hatch for future direct-package tests: an internal `_test.go`-scoped builder that does not expose a runtime seam.

**ctx accepted but not honored.** `Publish` is called post-repository-commit per §2.7 (persist-before-publish); cancelling the write post-commit would silently diverge storage state from the event stream. Accept the parameter to satisfy the port, ignore its cancellation.

**No logging inside `internal/events/`.** Stdout is the event bus; `slog` lands in `cmd/server/main.go` (Story 17) against **`os.Stderr`**. The package doc on `stdout_publisher.go` declares this as an enforced invariant, not a convention. A downstream consumer can `./server 2>logs.txt | jq` to consume pure events.

**Unbuffered by design.** If Story 17 later wraps `os.Stdout` in `bufio.Writer` for throughput, Flush MUST sit inside the same critical section as the Encode — the invariant prescribes "no other writer touches `w` between a partial-buffered Encode and its Flush", not a specific call sequence. A future wrapper owning Flush elsewhere still holds the invariant so long as it grabs the same mutex.

**Compile-time port guard.** `var _ domain.EventPublisher = (*StdoutPublisher)(nil)` at file scope — mirrors the repo's guard pattern. Port drift fails the build, no test needed.

See DesignAndBreakdown §2.7 / §2.7a and the Story 8 entries in `project/docs/gotchas.md`.

## Testing

The design doc §5 defines the minimum test suite, mapping to the PRD rubric:

- **Storage layer (Story 7, landed)** — `internal/storage/memory/alert_repo_test.go` is a 12-test black-box suite (`package memory_test`, 100% statement coverage) covering round-trip CRUD, cross-tenant `ErrNotFound` collapse (both `FindByID` and `Update`), `ErrAlreadyExists` on duplicate `Create`, clone-on-read proof, clone-on-write proof, clone-on-`List` proof, empty-slice JSON-marshal (`string(json.Marshal(got)) == "[]"`), `CreatedAt` descending sort with explicit time offsets (Windows 100ns timer dodge), tenant+status+score filter composition with per-branch exclusion rows, and a partitioned concurrency test. The concurrency test pre-seeds a pool of 32 shared IDs serially before goroutines spin; the parallel workload is three disjoint shapes (`Create` on fresh UUIDs / `FindByID` + `Update` on seeded IDs) so any non-nil error is a real bug rather than an accepted race — the error-type partition is what makes the test meaningful without `-race`, which remains the CI-runner gate (Story 18's Makefile; cgo-on-Windows constraint captured in `project/docs/gotchas.md` `## Testing`). A 12th `t.Skip` test materializes the ctx-cancellation contract for a future DB impl.
- **Events layer (Story 8)** — no tests by AC; integration covered transitively by Story 10's service tests against a fake publisher.
- **Service layer (Story 10)** — `CreateAlert` happy path; decide on OPEN / ESCALATED; decide on already-decided (`ErrAlreadyDecided`); decide with wrong tenant (`ErrNotFound`); escalate on OPEN; escalate on non-OPEN (`ErrInvalidTransition`).
- **API layer (Story 16)** — `GET /alerts` without tenant → 400; `PATCH` on already-decided → 409.

Run with `go test ./... -race -cover` (CI) or `go test ./... -cover` (Windows dev where cgo isn't installed; the partitioned concurrency test still fails loudly on race-induced bugs without `-race`).

## Out of Scope

Per DesignAndBreakdown §8: persistent DB, Kafka/SNS publisher, JWT/header-based tenant auth, optimistic locking / ETags, OpenAPI spec, Dockerfile, structured outbox, metrics/tracing, pagination. Each is a one-line entry in the README's "Future Improvements" section.
