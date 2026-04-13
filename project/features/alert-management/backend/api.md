# Alert Management — Backend

> Last reviewed: 2026-04-13

This document captures the backend layer of the Alert Management service — a Go microservice rooted at `alert-service/`. The service follows Clean / hexagonal layering: a pure `domain` package holding the entity, enums, errors, events, and ports; a `service` package orchestrating use cases; a `storage/memory` adapter and an `events` stdout adapter satisfying the ports from outside; and an `api` package wrapping it all in HTTP. Only the **domain** layer is implemented today (Stories 2–5 complete — entity, errors, events, ports); the other layers are specified by the design doc and the per-story checklist but not yet coded.

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
    ErrAlreadyDecided    = errors.New("alert has already been decided")
    ErrInvalidTransition = errors.New("invalid state transition")
    ErrTenantMismatch    = errors.New("tenant mismatch") // never client-surfaced; repo collapses to ErrNotFound
)
```

All plain `errors.New` sentinels — no custom types, no codes, no wrapping at sentinel level. Service and API layers compare with `errors.Is`. The HTTP mapper (Story 14) translates to:

| Sentinel | HTTP | `error` code | Notes |
|---|---|---|---|
| `ErrNotFound` | 404 | `ALERT_NOT_FOUND` | |
| `ErrTenantMismatch` | 404 | `ALERT_NOT_FOUND` | Collapsed at repo boundary; never surfaces |
| `ErrAlreadyDecided` | 409 | `ALERT_ALREADY_DECIDED` | |
| `ErrInvalidTransition` | 409 | `INVALID_STATE_TRANSITION` | |
| DTO validation failures | 400 | `VALIDATION_ERROR` | Produced by validator/v10 |

`ErrTenantMismatch` is kept distinct from `ErrNotFound` so the repo can log the two cases separately if needed, and so future policy hooks (e.g., cross-tenant audit logging) don't need to reshape the repository API.

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

## HTTP API (Planned — Stories 12–17)

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

## Stdout / Stderr Split (Planned — Stories 7, 18)

The `EventPublisher` writes **only** to `os.Stdout`. `slog` is configured with a handler writing to **`os.Stderr`** in `cmd/server/main.go`. This keeps the simulated broker stream clean — a downstream consumer can `tail -f | jq` stdout without being polluted by server boot logs, request logs, or panic traces. This trap is seeded in `project/docs/gotchas.md` as a preview so the wiring agent catches it. See DesignAndBreakdown §2.7a.

## Testing (Planned — Stories 8–15)

The design doc §5 defines the minimum test suite, mapping to the PRD rubric:

- **Service layer** — `CreateAlert` happy path; decide on OPEN / ESCALATED; decide on already-decided (`ErrAlreadyDecided`); decide with wrong tenant (`ErrNotFound`); escalate on OPEN; escalate on non-OPEN (`ErrInvalidTransition`).
- **Storage layer** — concurrent Create/FindByID/Update under `-race`. Does **not** assert strict business-rule atomicity under concurrent decides (known MVP limitation, documented in README; real-world fix is DB-level `SELECT ... FOR UPDATE`).
- **API layer** — `GET /alerts` without tenant → 400; `PATCH` on already-decided → 409.

Run with `go test ./... -race -cover`.

## Out of Scope

Per DesignAndBreakdown §8: persistent DB, Kafka/SNS publisher, JWT/header-based tenant auth, optimistic locking / ETags, OpenAPI spec, Dockerfile, structured outbox, metrics/tracing, pagination. Each is a one-line entry in the README's "Future Improvements" section.
