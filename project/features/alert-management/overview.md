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

Request validation happens at the DTO boundary via `go-playground/validator/v10`. Domain errors (`ErrNotFound`, `ErrAlreadyDecided`, `ErrInvalidTransition`, `ErrTenantMismatch`) are sentinels compared with `errors.Is` and mapped to HTTP by a single `api.writeError` helper.

## Layers

Only a single backend layer; no frontend, no IoT, no external persistence.

- [backend/api.md](backend/api.md) — current state of the Go backend layer (domain model, planned endpoints, repository and event ports, project structure).

## Current State

**Domain layer complete** — `internal/domain/` has:
- `alert.go` — `Alert` struct, `Status` typed enum with four constants, `CanDecide` / `CanEscalate` predicates, `Clone` deep-copy for repo boundary. (Commit `edccd10`.)
- `errors.go` — four `errors.New` sentinels (`ErrNotFound`, `ErrAlreadyDecided`, `ErrInvalidTransition`, `ErrTenantMismatch`). (Commit `68a8d04`.)
- `events.go` — `Event` marker interface with `EventName() string`, plus `AlertDecidedEvent` and `AlertEscalatedEvent` structs with PRD-aligned JSON tags. (Commits `1bacc2b`, revised by `4d57190`.)

**Not yet implemented** — ports (Story 5), in-memory repo (Story 6), stdout publisher (Story 7), service layer (Stories 8–11), API / DTOs / middleware / router (Stories 12–17), `main.go` wiring (Story 18), README + Makefile (Story 19), final rubric sweep (Story 20).

See `project/docs/checklist.md` for the full per-story breakdown and per-story Implementation Notes.

## Related Features

None — single-feature repository for the home assignment.

## Source of Truth

- **PRD:** `project/docs/PRD.md` — external assignment spec.
- **Refined design:** `project/docs/DesignAndBreakdown.md` — internal source of truth for architecture, state machine (§2.1), error contract (§2.3), event schemas (§2.7b), Go idiom gotchas (§9.x).
- **Checklist:** `project/docs/checklist.md` — per-story work and Implementation Notes.
- **Gotchas:** `project/docs/gotchas.md` — cross-cutting traps log.
- **Project-wide decisions:** `project/docs/design-decisions.md` — D1–D4 cover Story 1 module layout choices.
