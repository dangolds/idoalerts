# Feature Catalog

Index of all features in this repository. Each feature has a self-contained folder under `project/features/<name>/` with an `overview.md`, per-layer docs where applicable, `design-decisions.md` (ADR-lifecycle), and `gotchas.md`.

See also the project-wide docs at `project/docs/`: the refined design doc (`DesignAndBreakdown.md`), the per-story checklist with Implementation Notes (`checklist.md`), the cross-cutting gotchas log (`gotchas.md`), and project-wide design decisions (`design-decisions.md`).

## Features

| Feature | Status | Overview |
|---|---|---|
| [Alert Management](alert-management/overview.md) | In progress (domain + repo impl + repo tests + stdout publisher + service layer done; service tests, API, wiring pending) | Sanctions-screening alert lifecycle service: create, list with tenant/status/score filters, escalate (OPEN → ESCALATED), write-once decide (OPEN \| ESCALATED → CLEARED \| CONFIRMED_HIT). Multi-tenant, in-memory storage, stdout event bus. |

## Glossary

- **Alert** — a potential sanctions match on a payment transaction. Has an ID, tenant, transaction ID, matched-entity name, confidence score (0–100), status, optional assignee, decision note, timestamps.
- **Status** — `OPEN`, `ESCALATED`, `CLEARED`, `CONFIRMED_HIT`. String-valued wire enum; exact values come from the PRD.
- **Decision** — the act of transitioning to `CLEARED` or `CONFIRMED_HIT`. **Write-once**: a decided alert cannot be re-decided.
- **Escalation** — transition from `OPEN` to `ESCALATED`, emitting an event.
- **Tenant** — multi-tenant isolation key (`tenantId`). All queries and mutations scope by tenant; cross-tenant reads collapse to 404 to avoid existence leaks.
- **Event** — domain event published on escalate/decide (`alert.escalated`, `alert.decided`). Stdout JSON lines simulate a message broker.

## Conventions

- Documentation uses GitHub-flavored Markdown.
- Every file opens with a one-paragraph summary.
- Cross-doc references use explicit relative links.
- Design decisions follow the ADR lifecycle (`accepted` / `superseded` / `deprecated`) and are append-only; superseded entries are kept for history, never rewritten in place.
- The **project-wide** `project/docs/gotchas.md` is the canonical running log of Go-idiom traps and PRD-alignment invariants. Per-feature `gotchas.md` files may link into it.

## AI Reading Priority

For an AI agent respawning into this codebase, read in this order:

1. `project/docs/PRD.md` — the assignment spec (the external source of truth).
2. `project/docs/DesignAndBreakdown.md` — the refined internal design (layering, state machine, error contract, event shapes, Go gotchas §9).
3. `project/docs/checklist.md` — per-story breakdown with dated Implementation Notes explaining deviations.
4. `project/docs/design-decisions.md` — project-wide ADRs (D1–D4 cover Story 1 module layout).
5. `project/docs/gotchas.md` — cross-cutting traps (decision-vs-status JSON key, publish-time timestamp, typed Event marker, stdout/stderr split preview).
6. This catalog and the feature docs below.
