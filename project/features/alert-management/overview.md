# Alert Management — Overview

> Last reviewed: 2026-04-13

Alert Management is the Fincom home-assignment Go microservice that models the sanctions-screening alert lifecycle: a compliance officer receives alerts when a payment transaction matches a sanctioned entity, and the service lets them list, escalate, and decide on those alerts with strict write-once semantics and per-tenant isolation. The service is a single Go binary under `alert-service/`, uses in-memory storage behind a repository port so a real database can be swapped in later, and publishes domain events as JSON lines on stdout to simulate a downstream message broker.

## Key Concepts

- **Alert lifecycle** — `OPEN` → (`ESCALATED` | `CLEARED` | `CONFIRMED_HIT`), and `ESCALATED` → (`CLEARED` | `CONFIRMED_HIT`). Escalation is only valid from `OPEN`. Decisions are write-once (any attempt to re-decide returns `409 Conflict`). This refines the draft design's "only OPEN can transition" — see DesignAndBreakdown §2.1.
- **Multi-tenancy** — every operation is scoped by `tenantId`. Cross-tenant reads return `404 ALERT_NOT_FOUND` (not 403) to avoid leaking cross-tenant existence. Queries without `tenantId` return `400`.
- **Event publishing** — after a persisted escalate or decide, the service emits one `alert.escalated` or `alert.decided` JSON event on stdout. Publish happens **after** the repository write. Exact wire format is pinned by the PRD example — notably the decided-event key is `decision`, not `status`.
- **Ports over plumbing** — `AlertRepository` and `EventPublisher` live in `internal/domain` as interfaces. Infrastructure implementations (`internal/storage/memory`, `internal/events`) satisfy them from outside. This preserves the hexagonal boundary and makes the service layer testable with fakes.

## How It Works

The service exposes four HTTP endpoints (Go 1.22 native mux pattern matching, no framework):

- `POST /alerts` — create a new alert (simulating a screening result). Returns `201` with the created alert.
- `GET /alerts?tenantId=X&status=Y&minScore=Z` — list with filters. Requires `tenantId`. Returns `200` with `{ "alerts": [...] }`.
- `PATCH /alerts/{id}/decision` — submit `CLEARED` or `CONFIRMED_HIT` with a `decisionNote`. Write-once. Returns `200` with the updated alert or `409 ALERT_ALREADY_DECIDED`.
- `POST /alerts/{id}/escalate` — transition `OPEN` to `ESCALATED`. Returns `200` with the updated alert or `409 INVALID_STATE_TRANSITION` if not OPEN.

Request validation happens at the DTO boundary via `go-playground/validator/v10`. Domain errors (`ErrNotFound`, `ErrAlreadyExists`, `ErrAlreadyDecided`, `ErrInvalidTransition`, `ErrTenantMismatch`) are sentinels compared with `errors.Is` and mapped to HTTP by a single `api.writeError` helper.

## Layers

Only a single backend layer; no frontend, no IoT, no external persistence.

- [backend/api.md](backend/api.md) — current state of the Go backend layer (domain model, planned endpoints, repository and event ports, project structure).

## Current State

**Domain layer complete** (Stories 2–5) — `internal/domain/` has:
- `alert.go` — `Alert` struct, `Status` typed enum with four constants, `CanDecide` / `CanEscalate` predicates, `Clone` deep-copy for repo boundary. (Commit `edccd10`.)
- `errors.go` — five `errors.New` sentinels: `ErrNotFound`, `ErrAlreadyExists` (added during Story 6 to enforce the port's "Create is for new alerts only" contract), `ErrAlreadyDecided`, `ErrInvalidTransition`, `ErrTenantMismatch`. (Original: commit `68a8d04`; amended during Story 6.)
- `events.go` — `Event` marker interface with `EventName() string`, plus `AlertDecidedEvent` and `AlertEscalatedEvent` structs with PRD-aligned JSON tags. (Commits `1bacc2b`, revised by `4d57190`.)
- `ports.go` — `AlertRepository` interface (Create/FindByID/List/Update, ctx-first), `EventPublisher` interface (Publish(ctx, Event)), `ListFilter` struct (TenantID required, Status / MinScore pointer-optional). The port doc pins only correctness invariants every impl must honor (cross-tenant collapse to `ErrNotFound`); impl-flavored rules (slice-init, sort) live on the impl's package doc.

**Infrastructure layer complete** (Stories 6–8 done; 3/3 of Phase 3 complete) — `internal/storage/memory/` has:
- `alert_repo.go` — thread-safe `AlertRepo` backed by `map[string]*domain.Alert` + `sync.RWMutex`. Clone-on-read/write at every lock boundary (§2.8a); `List` pre-allocates non-nil empty slice (§9.1) and sorts `CreatedAt` descending via `sort.SliceStable` (§9.12); cross-tenant `FindByID`/`Update` collapse to `ErrNotFound`; `Create` returns `ErrAlreadyExists` on ID collision. Compile-time `var _ domain.AlertRepository = (*AlertRepo)(nil)` guard catches port drift at build time.
- `alert_repo_test.go` — 12-test black-box suite (`package memory_test`) proving clone-on-R/W, empty-slice, sort-determinism, filter composition, and cross-tenant-collapse invariants. 100% statement coverage. Concurrency test uses a partitioned workload (32 seeded-ID pool + fresh-UUID creates) so any race-induced error surfaces loudly without needing `-race`; `-race` remains the CI-runner gate (Story 18's Makefile; cgo not available on Windows dev box). See Story 7 Implementation Notes and the `## Testing` section in `project/docs/gotchas.md`.

And `internal/events/` has:
- `stdout_publisher.go` (Story 8) — `StdoutPublisher` satisfying `domain.EventPublisher` by writing newline-delimited JSON to an unexported `io.Writer` fixed at construction to `os.Stdout`. A `sync.Mutex` serializes `json.NewEncoder(w).Encode(event)` — Story 17 wires this behind concurrent HTTP handlers, and `json.Encoder.Encode` is not documented goroutine-safe (POSIX pipe-atomicity doesn't cover terminals/files; Windows makes no atomicity guarantee at all), so concurrent `Publish` without the mutex would interleave bytes and produce torn lines that silently break `tail -f | jq` consumers. Encoder is per-call (not cached) to keep struct state minimal and sidestep encoder-stale-writer risk; details in the Story 8 Implementation Notes and the new `§2.7a` + "Concurrent Publish" entries in `project/docs/gotchas.md`. No tests in this package per AC; Story 10's service tests cover the integration path via a fake publisher.

**Service layer landed — CreateAlert/ListAlerts/DecideAlert/EscalateAlert** (Story 9; Phase 4 now 1/2 complete) — `internal/service/alert_service.go`:
- `AlertService{repo, pub, logger}` consumes `domain.AlertRepository` + `domain.EventPublisher` ports; orchestrates load-check-mutate-persist-publish for the two state-changing flows (decide, escalate) per §2.1 / §2.7 / §2.7b / §2.8 / §2.8b / §2.9c / §9.13. No compile-time port guard — orchestrator ≠ port-implementer; the guard pattern lives only on infrastructure adapters.
- `CreateAlert` generates `uuid.NewString()`, sets `Status=OPEN` and equal `CreatedAt`/`UpdatedAt` from a single `time.Now().UTC()` (nanosecond-divergence invariant); `CreateAlertInput` is a service-package struct with plain Go fields and no struct tags — see ADR AM-6. `ListAlerts` is a one-line pass-through. `DecideAlert` has a defense-in-depth guard rejecting any `newStatus` other than `CLEARED`/`CONFIRMED_HIT` at method entry (the DTO `oneof` protects HTTP, but the service is callable from non-HTTP contexts), then load → `CanDecide` with terminal-vs-invalid-transition disambiguation (the "else" branch is unreachable today but guards future non-terminal non-decidable statuses) → mutate → persist → publish. `EscalateAlert` mirrors the shape with `CanEscalate` → `ErrInvalidTransition` on any non-OPEN state.
- Events are constructed via struct literal at each call site (AM-4 single-source-of-truth on `EventName()`), with `Timestamp = time.Now().UTC().Format(time.RFC3339)` at publish time per §9.13 — never derived from `a.UpdatedAt`. On `Publish` error the service logs once via `slog.ErrorContext(ctx, ...)` with exactly four typed fields (`alert_id`, `tenant_id`, `event`, `err`) and **returns success anyway** — repository state is authoritative (§2.7). The logger is used ONLY for publish-failure logging; request-level logging belongs to HTTP middleware.
- No tests in this story — Story 10 (service-layer unit tests) lands next with a real `memory.AlertRepo`, a fake publisher satisfying `domain.EventPublisher`, and a discarding `slog.Logger` (`io.Discard` JSON handler) so publish-failure log lines don't pollute test output.

**Not yet implemented** — service-layer tests (Story 10), API / DTOs / middleware / router (Stories 11–16), `main.go` wiring (Story 17), Makefile + README (Stories 18–19), final rubric sweep (Story 20).

See `project/docs/checklist.md` for the full per-story breakdown and per-story Implementation Notes.

## Related Features

None — single-feature repository for the home assignment.

## Source of Truth

- **PRD:** `project/docs/PRD.md` — external assignment spec.
- **Refined design:** `project/docs/DesignAndBreakdown.md` — internal source of truth for architecture, state machine (§2.1), error contract (§2.3), event schemas (§2.7b), Go idiom gotchas (§9.x).
- **Checklist:** `project/docs/checklist.md` — per-story work and Implementation Notes.
- **Gotchas:** `project/docs/gotchas.md` — cross-cutting traps log.
- **Project-wide decisions:** `project/docs/design-decisions.md` — D1–D4 cover Story 1 module layout choices.
