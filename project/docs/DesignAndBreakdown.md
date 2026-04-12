# Alert Management Service — Refined Plan (MVP)
## Context
This plan refines `project/docs/DesignAndBreakdown.md` for the Fincom home-assignment microservice defined in `project/docs/PRD.md`. The existing draft is solid (Clean Architecture, repository pattern, in-memory store, native `net/http`), but has gaps around state transitions, context propagation, error-response contract, logging, and testing scope. This refinement closes those gaps while keeping the footprint MVP-sized — no feature/package that isn't directly justified by the PRD's evaluation rubric.
The repo is greenfield (only `project/docs/*.md` exists), so there are no existing conventions to mirror; the plan adopts standard Go 1.22+ idioms.
---
## 1. What's Kept From The Draft
The following choices from `DesignAndBreakdown.md` are endorsed without change:
- **Clean Architecture layering:** `domain` → `service` → `storage` / `api`.
- **Repository pattern** with `AlertRepository` interface in `domain`, in-memory impl in `storage`.
- **`EventPublisher` interface** in `domain`; stdout impl in a small `events` package.
- **Mutex-protected `map[string]*Alert`** (RWMutex) — idiomatic and type-safe over `sync.Map`.
- **Native `net/http` router** (Go 1.22+ pattern matching) — no framework.
- **`go-playground/validator/v10`** on request DTOs.
- **DI wired at composition root** in `cmd/server/main.go`.
- **Graceful shutdown** via `http.Server.Shutdown` + signal handling.
- **`tenantId` in body/query** (per user decision) — header-based tenant is a documented post-MVP improvement.
---
## 2. Refinements (What Changes / Gets Added)
### 2.1 State machine — correction
Draft said "only OPEN can transition". Per user clarification, the real-world compliance flow requires `ESCALATED → decided`. Final transitions:
- `OPEN → ESCALATED`
- `OPEN → CLEARED`
- `OPEN → CONFIRMED_HIT`
- `ESCALATED → CLEARED`
- `ESCALATED → CONFIRMED_HIT`
Decisions remain **write-once**: any attempt to decide an alert whose status is already `CLEARED` or `CONFIRMED_HIT` → `409 Conflict`. Escalate is **only** valid from `OPEN` → `409 Conflict` otherwise.
Model this as pure predicate methods on the `Alert` domain type:
```go
func (a *Alert) CanDecide() bool   { return a.Status == StatusOpen || a.Status == StatusEscalated }
func (a *Alert) CanEscalate() bool { return a.Status == StatusOpen }
```
**Design note — why on the entity, not in the service:** these are pure, stateless invariants over `Alert`'s own fields. They import nothing, touch no ports, and are data-intrinsic. Per DDD, this is exactly what belongs on the entity — pulling them into the service would trend toward the anemic-domain-model anti-pattern. The line we hold: **pure functions over entity state → on the entity; anything that touches repo/publisher/ctx → in the service.** The service retains full ownership of orchestration (load → check → mutate → persist → publish), so it is not anemic.
### 2.2 `context.Context` propagation
Every layer method takes `ctx context.Context` as its first argument: handler → service → repository → publisher. This is non-negotiable idiomatic Go and costs nothing now, but unlocks cancellation, deadlines, and request-scoped values (trace IDs, future tenant middleware) later.
### 2.3 Consistent error response contract
Define one JSON error shape, produced by a single `api.writeError(w, status, code, msg)` helper:
```json
{ "error": "ALERT_ALREADY_DECIDED", "message": "alert has already been decided" }
```
Domain errors map 1:1:
| Domain error | HTTP | `error` code |
|---|---|---|
| `ErrNotFound` | 404 | `ALERT_NOT_FOUND` |
| `ErrTenantMismatch` | 404 | `ALERT_NOT_FOUND` *(don't leak cross-tenant existence)* |
| `ErrAlreadyDecided` | 409 | `ALERT_ALREADY_DECIDED` |
| `ErrInvalidTransition` | 409 | `INVALID_STATE_TRANSITION` |
| validation failures | 400 | `VALIDATION_ERROR` |
Note: cross-tenant `FindByID` returns 404, not 403 — standard practice to avoid leaking existence.
### 2.4 Structured logging with `log/slog`
`log/slog` ships with Go 1.21+, zero cost, and fits the "JSON to stdout" style the event publisher already uses. Use it from day one — the draft defers this to "future improvements" unnecessarily. One `*slog.Logger` constructed in `main`, injected where needed (primarily the HTTP middleware and the publisher).
### 2.5 Minimal HTTP middleware
Three small middlewares, chained in `main`:
1. **Recovery** — catches panics, logs stack, returns 500. Essential, not optional.
2. **Request logger** — method, path, status, duration (via `slog`).
3. **Request ID** — generate a UUID per request, stash in `ctx`, echo as `X-Request-Id` header. Optional-but-trivial; helps when debugging event logs alongside HTTP logs.
No auth middleware — explicitly out of scope per PRD.
### 2.6 Response DTOs
The draft talks about request DTOs but not response DTOs. Add `AlertResponse` in `internal/api/dto` and a `toAlertResponse(domain.Alert)` mapper. Keeps the wire format decoupled from the domain entity (SRP at the boundary). Prevents accidental leakage of internal fields if the domain grows.
### 2.7 Event publishing ordering
Explicit rule: **persist first, then publish**. If publish fails (for stdout: effectively never), log the failure at ERROR level and still return 200 to the client — the state change is authoritative. Document that a real broker implementation would need outbox-pattern or similar; not in MVP scope.
### 2.7a Stdout / stderr separation (critical for simulated broker)
The `EventPublisher` writes domain events to **`os.Stdout`**. `slog` is configured to write to **`os.Stderr`**. This keeps the "simulated message broker" stream clean — a downstream consumer can `tail -f | jq` stdout without being polluted by application logs ("server started on :8080", request logs, panic traces, etc).
### 2.7b Event schema — exact PRD alignment
The PRD specifies `{"event": "alert.decided", "alertId": "...", "tenantId": "...", "decision": "CLEARED", "timestamp": "..."}`. Note the key is `decision`, not `status`. Concrete structs:
```go
// internal/domain/events.go
type AlertDecidedEvent struct {
    Event     string `json:"event"`      // "alert.decided"
    AlertID   string `json:"alertId"`
    TenantID  string `json:"tenantId"`
    Decision  string `json:"decision"`   // CLEARED | CONFIRMED_HIT
    Timestamp string `json:"timestamp"`  // time.RFC3339
}
type AlertEscalatedEvent struct {
    Event     string `json:"event"`      // "alert.escalated"
    AlertID   string `json:"alertId"`
    TenantID  string `json:"tenantId"`
    Timestamp string `json:"timestamp"`
}
```
### 2.8 UUID + timestamps live in the service layer
`CreateAlert` generates `uuid.NewString()` and sets `CreatedAt = time.Now().UTC()` / `UpdatedAt` inside the service, never in handlers or storage. Keeps the storage a dumb persistence layer.
### 2.8a Repository must copy on read/write — the pointer trap
Because the map holds `*Alert`, returning the raw pointer lets callers mutate stored state **outside the mutex**. Both read and write paths must copy at the lock boundary (use `Alert.Clone()` — see §9.7). The `RWMutex` makes each individual method internally safe (no data races, `-race` clean).
```go
func (r *AlertRepo) FindByID(ctx context.Context, tenantID, id string) (*domain.Alert, error) {
    r.mu.RLock(); defer r.mu.RUnlock()
    a, ok := r.alerts[id]
    if !ok || a.TenantID != tenantID { return nil, domain.ErrNotFound }
    return a.Clone(), nil
}
func (r *AlertRepo) Update(ctx context.Context, a *domain.Alert) error {
    r.mu.Lock(); defer r.mu.Unlock()
    existing, ok := r.alerts[a.ID]
    if !ok || existing.TenantID != a.TenantID { return domain.ErrNotFound }
    r.alerts[a.ID] = a.Clone()
    return nil
}
```
### 2.8b Known MVP limitation — read-check-write race
The service's `FindByID → CanDecide check → Update` pattern has a lock gap between the two repo calls. Two exactly-simultaneous decides on the same OPEN alert could both pass the check and both write, with last-write-wins and both emitting events. This technically violates strict "write-once" under exact concurrent clicks.
**We accept this for MVP**, for three reasons:
1. The PRD evaluation rubric tests decision immutability **sequentially** (decide, then re-decide → 409) — which works correctly with the simple pattern. No concurrent test is required.
2. The fix in production is the DB layer, not application code: `BEGIN; SELECT ... FOR UPDATE; UPDATE; COMMIT;` closes the window natively. Engineering a bespoke atomic-closure repo API for an in-memory store we're going to throw away is yak-shaving.
3. Application-layer mitigations (OCC with `Version`, closure-based `MutateByID`, etc.) either pollute the domain with DB concerns or introduce uncommon patterns for no MVP test benefit.
This is a **one-line limitation documented in the README** under "Known MVP limitations", with the post-MVP fix explicitly called out. A reviewer seeing this will read it as "engineer understands the race and is correctly scoping it out of MVP," not as a missed issue.
The concurrency test (§5, #8) must be run with `-race` and must spin up N goroutines reading + updating the same ID to prove the copies hold.
### 2.9 Filter validation on `GET /alerts`
Explicit parsing + validation in the handler:
- `tenantId` — required, else 400
- `status` — optional, must be one of the 4 enum values, else 400
- `minScore` — optional, must parse to float in [0, 100], else 400
Repository receives a typed `ListFilter` struct, not raw strings.
### 2.9a DTO validator tags — lock down decision inputs
The decide endpoint must reject `status: "OPEN"` or `status: "ESCALATED"` at the DTO boundary, before the service even sees it. Use `validator/v10`'s `oneof`:
```go
type CreateAlertRequest struct {
    TenantID          string  `json:"tenantId"          validate:"required"`
    TransactionID     string  `json:"transactionId"     validate:"required"`
    MatchedEntityName string  `json:"matchedEntityName" validate:"required"`
    MatchScore        float64 `json:"matchScore"        validate:"gte=0,lte=100"`
    AssignedTo        *string `json:"assignedTo"        validate:"omitempty"`
}
type DecideRequest struct {
    TenantID     string `json:"tenantId"     validate:"required"`
    Status       string `json:"status"       validate:"required,oneof=CLEARED CONFIRMED_HIT"`
    DecisionNote string `json:"decisionNote" validate:"required"`
}
type EscalateRequest struct {
    TenantID string `json:"tenantId" validate:"required"`
}
```
### 2.9b Success status codes (API design precision)
- `POST /alerts` → **201 Created**, body = `AlertResponse` of the created alert.
- `PATCH /alerts/{id}/decision` → **200 OK**, body = updated `AlertResponse` (client sees new `status`, `decisionNote`, `updatedAt`).
- `POST /alerts/{id}/escalate` → **200 OK**, body = updated `AlertResponse`.
- `GET /alerts` → **200 OK**, body = `{ "alerts": [...] }` (object-wrapped, not bare array — easier to extend with paging later).
### 2.9c Idempotency — not implemented, documented
If the same decision payload arrives twice (double-click), we return **409 Conflict** on the second call per PRD's strict write-once rule. Add an inline code comment at the decision check:
```go
// MVP: strict write-once per PRD — any second decision returns 409.
// Post-MVP: could return 200 OK if the new decision exactly matches the existing one (true idempotency),
// or require an Idempotency-Key header to dedupe retries.
```
### 2.10 Configuration
Tiny `internal/config` (or just inline in `main`) reading env vars with defaults: `PORT` (default `8080`), `LOG_LEVEL` (default `info`). No Viper/cobra — env + `os.Getenv` is plenty.
---
## 3. Final Project Structure
```text
/alert-service
├── cmd/
│   └── server/
│       └── main.go                    # Composition root: config, logger, storage, publisher, service, router, server
├── internal/
│   ├── domain/
│   │   ├── alert.go                   # Alert entity, Status enum, canDecide/canEscalate helpers
│   │   ├── errors.go                  # ErrNotFound, ErrAlreadyDecided, ErrInvalidTransition, ErrTenantMismatch
│   │   ├── events.go                  # AlertEscalated, AlertDecided event structs
│   │   └── ports.go                   # AlertRepository, EventPublisher interfaces
│   ├── service/
│   │   ├── alert_service.go           # CreateAlert, ListAlerts, DecideAlert, EscalateAlert
│   │   └── alert_service_test.go      # Business-rule unit tests (see §5)
│   ├── storage/
│   │   └── memory/
│   │       ├── alert_repo.go          # map + sync.RWMutex impl
│   │       └── alert_repo_test.go     # Concurrency + CRUD tests
│   ├── events/
│   │   └── stdout_publisher.go        # JSON-to-stdout EventPublisher impl
│   └── api/
│       ├── router.go                  # Go 1.22 mux registration
│       ├── middleware.go              # recovery, logger, request-id
│       ├── errors.go                  # writeError helper + domain→HTTP mapping
│       ├── dto.go                     # Request + Response DTOs, validator tags
│       ├── alert_handler.go           # HTTP handlers
│       └── alert_handler_test.go      # httptest-based handler tests
├── go.mod
├── go.sum
├── Makefile                           # run, test, lint targets (trivial)
└── README.md                          # how to run, example curl commands
```
Package naming avoids stutter: `domain.Alert`, `service.AlertService`, `memory.NewAlertRepo()`, `api.NewHandler()`.
---
## 4. Key Interfaces (for reference during implementation)
```go
// internal/domain/ports.go
type AlertRepository interface {
    Create(ctx context.Context, a *Alert) error                              // new alerts only
    FindByID(ctx context.Context, tenantID, id string) (*Alert, error)
    List(ctx context.Context, f ListFilter) ([]*Alert, error)
    Update(ctx context.Context, a *Alert) error                              // existing alerts only
}
type EventPublisher interface {
    Publish(ctx context.Context, event Event) error
}
type ListFilter struct {
    TenantID string   // required
    Status   *Status  // optional
    MinScore *float64 // optional
}
```
Repo returns `ErrNotFound` when the ID exists under a different tenant — tenant filtering happens at the repository boundary, not the service (defense in depth; service also trusts it).
---
## 5. Testing Plan (mapping directly to PRD rubric)
Minimum tests, all in service + handler layers:
**Service layer (`alert_service_test.go`) — uses the real in-memory repo and a fake publisher:**
1. `CreateAlert` — happy path, fields populated (ID, timestamps, status=OPEN).
2. `DecideAlert` on OPEN → succeeds, status=CLEARED/CONFIRMED_HIT, event published once.
3. `DecideAlert` on already-decided → `ErrAlreadyDecided` *(decision immutability)*.
4. `DecideAlert` with wrong tenantId → `ErrNotFound` *(tenant isolation)*.
5. `EscalateAlert` on OPEN → succeeds, event published once.
6. `EscalateAlert` on non-OPEN → `ErrInvalidTransition` *(invalid transition)*.
7. `DecideAlert` on ESCALATED → succeeds (per clarified state machine).
**Storage layer (`alert_repo_test.go`):**
8. Concurrent `Create` / `FindByID` / `Update` across many goroutines — no data race under `-race`, no panics. Proves heap-safety of the `RWMutex` + `Clone` pattern. (We explicitly do NOT assert strict business-rule atomicity under concurrent decides — see §2.8b.)
**API layer (`alert_handler_test.go`) — via `httptest`:**
9. `GET /alerts` without `tenantId` → 400.
10. `PATCH /alerts/{id}/decision` on already-decided → 409 with `ALERT_ALREADY_DECIDED`.
This exceeds the PRD's "3–4 tests" bar and explicitly covers every line of the rubric.
Run with `go test ./... -race -cover`.
---
## 6. Execution Steps (incremental)
1. `go mod init`, add deps: `github.com/google/uuid`, `github.com/go-playground/validator/v10`. Nothing else.
2. **Domain** — entity, enum, errors, events, ports. Add `canDecide` / `canEscalate` methods on `Alert`.
3. **Storage** — in-memory repo + concurrency test.
4. **Events** — stdout publisher (JSON-encode `domain.Event`).
5. **Service** — 4 methods + full unit-test suite.
6. **API** — DTOs, validator, handlers, error mapper, middleware, router.
7. **main.go** — wire everything, signal-based graceful shutdown.
8. **README + Makefile** — run instructions, sample curls for all 4 endpoints.
---
## 7. Verification (end-to-end)
```bash
make test          # go test ./... -race -cover, expect all green
make run           # starts server on :8080
# Create
curl -XPOST localhost:8080/alerts -d '{"tenantId":"t1","transactionId":"tx1","matchedEntityName":"ACME","matchScore":92.5}'
# List (400 without tenant)
curl localhost:8080/alerts                      # → 400
curl 'localhost:8080/alerts?tenantId=t1&minScore=90'
# Escalate (stdout emits alert.escalated event)
curl -XPOST localhost:8080/alerts/<id>/escalate -d '{"tenantId":"t1"}'
# Decide → stdout emits alert.decided event
curl -XPATCH localhost:8080/alerts/<id>/decision -d '{"tenantId":"t1","status":"CLEARED","decisionNote":"false positive"}'
# Re-decide → 409 ALERT_ALREADY_DECIDED
```
Verify in stdout that exactly one JSON event line is emitted per escalation/decision.
---
## 8. Explicitly Out Of Scope (MVP)
Keeping these out to avoid over-engineering — each is a one-line entry in the "Future Improvements" section of the draft and needs no further elaboration: persistent DB, Kafka/SNS publisher, JWT/header-based tenant middleware, optimistic locking / ETags, OpenAPI spec, Dockerfile, structured outbox, metrics/tracing, pagination.
---
## 9. Go Implementation Gotchas (must-remember during coding)
Small but genuine Go-idiom traps — all cheap to get right upfront, painful to debug later.
**9.1 Empty-slice JSON encoding.** A `nil` slice marshals to `null`, not `[]`. Always initialize slices that back JSON arrays:
```go
alerts := make([]*dto.AlertResponse, 0)   // encodes as []
```
This matters for `GET /alerts` when there are zero matches.
**9.2 Event marker interface.** `Publish` takes a typed marker interface, not `any`:
```go
// internal/domain/events.go
type Event interface { EventName() string }   // "alert.decided" | "alert.escalated"
```
Each event struct (`AlertDecidedEvent`, `AlertEscalatedEvent`) implements `EventName()`. Prevents the publisher from accepting arbitrary garbage.
**9.3 Graceful shutdown — separate context.** `http.Server.Shutdown` needs its own deadline, not the server's request context:
```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
_ = srv.Shutdown(ctx)
```
Wire SIGINT/SIGTERM via `signal.NotifyContext` in `main`.
**9.4 Timestamp format.** Standardize on `time.RFC3339` across event payloads and DTOs:
```go
ts := time.Now().UTC().Format(time.RFC3339)
```
Don't rely on `time.Time`'s default JSON encoding in events — the PRD example expects a string.
**9.5 Query-param parsing — hand-rolled, no schema lib.** `validator/v10` binds to JSON bodies, not query strings. For `GET /alerts` use `r.URL.Query().Get(...)` + `strconv.ParseFloat` directly in the handler. Keep it ~15 lines; do not pull in `gorilla/schema`.
**9.6 Go 1.22 routing.** Use method-prefixed patterns and `PathValue` — no third-party router. **`go.mod` must declare `go 1.22`** or the method prefix is treated as a literal path:
```go
mux.HandleFunc("POST /alerts",                     h.Create)
mux.HandleFunc("GET /alerts",                      h.List)
mux.HandleFunc("PATCH /alerts/{id}/decision",      h.Decide)
mux.HandleFunc("POST /alerts/{id}/escalate",       h.Escalate)
// in handler:
id := r.PathValue("id")
```
**9.7 Domain `Alert.Clone()` — future-proof deep copy.** Today `*a` is fine (primitives + `*string`), but a method makes intent explicit and handles any future slice/map field:
```go
func (a *Alert) Clone() *Alert {
    cp := *a
    if a.AssignedTo != nil { v := *a.AssignedTo; cp.AssignedTo = &v }
    return &cp
}
```
The in-memory repo calls `existing.Clone()` on read and stores `a.Clone()` on create/update.
**9.8 `http.Server` timeouts (never use zero defaults).** Zero = infinite = FD leak under slow-client attacks:
```go
srv := &http.Server{
    Addr:         ":" + port,
    Handler:      mux,                 // wrapped with middleware chain
    ReadTimeout:  5 * time.Second,
    WriteTimeout: 10 * time.Second,
    IdleTimeout:  120 * time.Second,
}
```
**9.9 `writeJSON` / `writeError` helper.** Set `Content-Type` *before* `WriteHeader` (once headers are flushed, you can't change them):
```go
func writeJSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(v)
}
```
**9.10 Harden request body decoding.** On every mutating handler:
```go
r.Body = http.MaxBytesReader(w, r.Body, 1<<20)   // 1 MiB cap
dec := json.NewDecoder(r.Body)
dec.DisallowUnknownFields()                      // reject typos loudly
if err := dec.Decode(&req); err != nil { /* 400 */ }
```
**9.11 DTO vs domain — `DecisionNote`.** On the domain entity use plain `string` (empty string `""` = "no note yet") rather than `*string`. PRD calls the field "nullable" but that's a DB concept; `""` covers it in Go without pointer-dereference noise. DTO `DecideRequest.DecisionNote` stays **`required`** — analysts must justify *new* decisions even though legacy/seeded alerts may have been created without one. Pin this asymmetry in a code comment so a future dev doesn't "fix" the mismatch. (`AssignedTo` *does* stay `*string` — it represents a real optional relationship.)
**9.11a `MatchScore` zero-value.** `float64` zero-value equals `0.0`, which is a valid score. A client that *omits* `matchScore` will silently get a 0. For MVP this is fine (document it in the handler) — upgrading to `*float64 + validate:"required"` if strict presence is needed is a one-line change post-MVP.
**9.12 Deterministic `List` ordering.** Go map iteration is randomized. Sort results in the repo by `CreatedAt` descending before returning — keeps tests stable and makes future pagination possible:
```go
sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
```
**9.13 Event `timestamp` = publish time, not `UpdatedAt`.** Construct the event in the service *at publish time*: `Timestamp: time.Now().UTC().Format(time.RFC3339)`. The PRD's `timestamp` field is when the event fired, not when the entity last changed (in practice usually equal, but semantically distinct).
