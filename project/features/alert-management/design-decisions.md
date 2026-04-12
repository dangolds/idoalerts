# Alert Management — Design Decisions

> Last reviewed: 2026-04-13

Feature-scoped architectural decisions for the Alert Management service, captured as append-only ADR entries. Entries are never edited in place — if superseded, the old entry stays and a new one is added below with a back-reference. For project-wide decisions (module layout, Go version pin, directory skeleton strategy) see `project/docs/design-decisions.md`. For the refined design this service implements, see `project/docs/DesignAndBreakdown.md`.

---

## AM-1 — Predicate methods on entity, orchestration in service

- **Status:** accepted
- **Date:** 2026-04-13
- **Context:** Story 2 introduced `Alert.CanDecide()` and `Alert.CanEscalate()`. Several DDD patterns were available: (a) pure predicates only, orchestration in service; (b) transition methods on the entity (`a.Decide(...)`, `a.Escalate(...)`); (c) a factory constructor (`NewAlert(...) (*Alert, error)`) with invariant checks on construction.
- **Decision:** Only the pure predicates live on `Alert`. No transition methods, no constructor. The service layer owns the full load → check → mutate → persist → publish orchestration.
- **Rationale:** The predicates are data-intrinsic — they import nothing, touch no ports, read only `Alert`'s own fields. Adding transition methods would collapse the service layer's orchestration responsibility and turn the service anemic. A factory would duplicate what `validator/v10` already enforces at the DTO boundary plus what the service's `CreateAlert` does with `uuid.NewString()` and `time.Now().UTC()`. See DesignAndBreakdown §2.1 for the explicit line: "pure functions over entity state → on the entity; anything that touches repo/publisher/ctx → in the service."
- **Consequences:** The service is responsible for orchestrating invariant checks; if the predicate rules ever grow, they stay pure Go. Callers cannot construct an `Alert` via a safe factory — they use a struct literal. This is fine because the only caller is the service's `CreateAlert`, which generates the required fields inline.

---

## AM-2 — `DecisionNote` is plain `string` on domain, `required` on DTO

- **Status:** accepted
- **Date:** 2026-04-13
- **Context:** The PRD describes `decisionNote` as "string, nullable — analyst's reasoning." The natural Go translation is `*string`. But `*string` is a pointer-dereference noise for a field that is always meaningful.
- **Decision:** `Alert.DecisionNote` is plain `string`. The empty string `""` represents "no note yet" (e.g., a seeded or legacy alert, or an alert that is still OPEN). The wire `DecideRequest.DecisionNote` stays `required` via validator/v10 tags — analysts must justify *new* decisions, even though *existing* alerts may have `""`.
- **Rationale:** "Nullable" in a PRD usually reflects a DB column concern. In Go, the zero-value of `string` is `""`, which covers the semantics without pointer bookkeeping. Keeping the DTO `required` preserves the business rule at the wire boundary where validation belongs. See DesignAndBreakdown §9.11.
- **Consequences:** A future dev may be tempted to "fix" the domain to `*string` for symmetry with the PRD. The struct field in `alert.go` carries an inline comment explicitly warning against this. `AssignedTo` remains `*string` because it represents a real optional relationship (an alert may legitimately be unassigned), distinct from "note pending."

---

## AM-3 — `Clone` at the repository boundary

- **Status:** accepted
- **Date:** 2026-04-13
- **Context:** Because the in-memory repository holds `map[string]*Alert`, returning the raw pointer from `FindByID` would let callers mutate stored state outside the mutex. Both the read and write paths must copy at the lock boundary (DesignAndBreakdown §2.8a — "the pointer trap").
- **Decision:** `Alert.Clone()` reallocates `*AssignedTo` when non-nil and returns a fresh `*Alert`. The in-memory repository calls `existing.Clone()` on read and stores `a.Clone()` on write. An inline comment flags "keep in sync" — any future slice / map / pointer field added to `Alert` must be deep-copied explicitly.
- **Rationale:** `*a` alone is safe today (primitives + `*string` + `time.Time` value-type) but a method makes intent explicit and survives field additions. A prose "keep in sync" comment is the lightweight escape hatch; a field-by-field body would be higher maintenance cost for the MVP.
- **Consequences:** Race-free storage reads under `-race`. Adding a field to `Alert` requires touching `Clone` — the sync comment is the tripwire. If the tripwire fails, a concurrent reader + mutator can corrupt shared state.

---

## AM-4 — `Event.EventName()` returns the struct field, not a literal

- **Status:** accepted (supersedes AM-4-v1 below)
- **Date:** 2026-04-13
- **Context:** Story 4 introduced a marker interface `Event { EventName() string }` satisfied by `AlertDecidedEvent` and `AlertEscalatedEvent`. Two implementations were considered: (a) literal-return (`return "alert.decided"`) with a package-level const for grep-ability; (b) field-read (`return e.Event`) with no shared const.
- **Decision:** `EventName()` returns `e.Event`. No package-level `EventName*` consts. Service constructs events via struct literal at two call sites (decide, escalate), setting `Event: "alert.decided"` / `Event: "alert.escalated"` as inline string literals.
- **Rationale:** The JSON wire field and the type identity *should* be the same string by definition — coupling them through the single struct field prevents divergence by construction. A literal-return plus const would introduce a second source of truth that can drift if the service forgets to populate `e.Event` (the JSON wire would become empty, but routing would still succeed — exactly the silent-bug shape to avoid). DRY doesn't pay for a two-call-site constant; grep finds both sites instantly.
- **Consequences:** Any service path that constructs an event must set `Event: "..."`. If it forgets, both the wire and the `EventName()` return empty — a loud failure, detectable by a single test. If a third event type is ever added, the same literal-in-field pattern scales at no cost.
- **Supersedes:** AM-4-v1 (below) after team-lead re-evaluation.

---

## AM-4-v1 — `Event.EventName()` returns a literal const (SUPERSEDED by AM-4)

- **Status:** superseded by AM-4 on 2026-04-13
- **Date:** 2026-04-13
- **Context:** Same as AM-4.
- **Decision (original):** `EventName()` returns a package-level const (`EventNameAlertDecided`, `EventNameAlertEscalated`). Service construction uses the same const as a single source of truth.
- **Rationale (original):** Compile-time constant; type identity stays correct even if the wire field is mis-populated. Event JSON field (wire format) and `EventName()` (type identity) decoupled on purpose.
- **Why superseded:** Reviewer re-evaluated — decoupling wire from identity invites wire-vs-routing divergence. By definition the two should be the same string; forcing them through the single struct field enforces that by construction. DRY also doesn't pay at N=2. Shipped initially in commit `1bacc2b`, reversed in commit `4d57190`.

---

## AM-5 — `ErrTenantMismatch` distinct from `ErrNotFound`

- **Status:** accepted
- **Date:** 2026-04-13
- **Context:** Cross-tenant reads must not leak existence — a request for an alert that exists under another tenant should return the same response as "does not exist" (DesignAndBreakdown §2.3). The simplest implementation is to return `ErrNotFound` directly from the repository on tenant mismatch, and skip the distinct sentinel.
- **Decision:** Keep `ErrTenantMismatch` as a distinct sentinel, but collapse it to `ErrNotFound` at the repository boundary. The sentinel never reaches the HTTP layer.
- **Rationale:** The distinct sentinel costs one extra line but preserves the ability to log the two cases separately (e.g., an audit log of "attempted cross-tenant access") and enables future policy hooks without reshaping the repository API. The inline comment above `ErrTenantMismatch` documents that it is internal-only, naming both §2.3 and §2.8a.
- **Consequences:** HTTP error mapping table has two rows mapping to 404 `ALERT_NOT_FOUND` (`ErrNotFound` and `ErrTenantMismatch`), even though the repository collapses to only one in practice. Cost is zero; benefit is defense-in-depth if a future refactor changes the collapse point.
