# Gotchas — Alert Management Service

Running log of Go-idiom traps and PRD-alignment invariants that would not be obvious from reading code alone. Append newest-first. Each entry: **Trap** (what could go wrong) / **What we did** (the chosen fix) / **If you're tempted to change this** (what breaks). Reference DesignAndBreakdown.md sections by number.

---

## Storage (In-Memory Repo)

### §2.8a — Clone on read AND write at the repo boundary

**Trap.** The in-memory map holds `*domain.Alert`. If the repo returns the raw stored pointer from `FindByID` / `List`, or stores the caller's pointer directly in `Create` / `Update`, callers can mutate entries that live inside the lock — from outside the lock. Result: data races, broken `-race`, impossible-to-debug read-after-write bugs when the service happens to hold onto a stored reference across method boundaries.

**What we did.** `internal/storage/memory/alert_repo.go` calls `a.Clone()` on every lock-boundary entry and exit: `Create` stores `a.Clone()`; `Update` stores `a.Clone()`; `FindByID` returns `a.Clone()`; `List` appends `a.Clone()`. Clone is an absolute boundary, not conditional on caller trust. `domain.Alert.Clone` is the canonical deep-copy (reallocates `*AssignedTo`'s backing string; every future pointer/slice/map field must extend it — see alert.go "keep in sync" comment).

**If you're tempted to change this.** "The service is the only caller, it won't mutate stored state" — the instant that's wrong, the bug is silent and non-local. One alloc per repo op is cheap; the boundary is cheaper to hold than to debug. Skipping the `Update`-side clone especially: the service holds the post-mutation pointer and may legally reuse the variable, which would write straight into the map without grabbing the lock.

---

### §9.1 — `make([]*Alert, 0)` before iteration in `List`

**Trap.** `var out []*domain.Alert` is zero-value `nil`; on zero matches the repo returns `nil`, which JSON-marshals to `null`. The HTTP response then becomes `{"alerts": null}` instead of `{"alerts": []}` — silently breaks the wire contract on the empty-filter path.

**What we did.** `List` starts with `out := make([]*domain.Alert, 0)` **before** the range loop, with an inline `// pre-allocated so zero-match returns [], not null (§9.1)` comment. Early returns pass through the same non-nil empty slice. Service does not re-check; trusts the repo contract.

**If you're tempted to change this.** Any "tidy-up" refactor to `var out []*domain.Alert` (stylistic preference, no functional change visible at the call site) silently regresses the JSON contract at the zero-match path. The inline comment is there precisely to make a future refactor pause. An impl test that asserts `len == 0` is not enough — JSON marshal check is required (Story 7 covers this).

---

## Domain Events

### §2.7b — Event JSON field is `decision`, not `status`

**Trap.** Muscle memory from `Alert.Status` makes "normalizing" the event field to `status` feel natural. The PRD's example event payload uses `decision`; any downstream consumer parses that exact key.

**What we did.** `AlertDecidedEvent.Decision` has `json:"decision"` plus an inline `// do NOT rename to "status"` comment.

**If you're tempted to change this.** Wire format breaks silently — the JSON still encodes, but a broker consumer looking for `decision` sees `null`. The PRD is the authoritative spec; we align to it.

---

### §9.4 / §9.13 — Event `Timestamp` is an RFC3339 string, populated at publish time

**Trap.** Making `Timestamp time.Time` feels cleaner but gives you Go's default time JSON (`2026-04-13T10:20:30.123456789Z`), and tempts callers to reuse `Alert.UpdatedAt` (wrong semantics — "when the entity changed" ≠ "when the event fired").

**What we did.** `Timestamp string` on both event structs, with an inline `// do NOT switch to time.Time` comment. Service constructs `time.Now().UTC().Format(time.RFC3339)` at the publish call site, not from the entity.

**If you're tempted to change this.** Switching to `time.Time` changes the wire format and loses the "publish time, not entity mutation time" invariant. If you see `Alert.UpdatedAt` flowing into an event, that's a bug.

---

### §9.2 — `EventPublisher.Publish` takes `Event`, not `any`

**Trap.** Using `any` (or `interface{}`) for the publisher parameter is easy and "flexible." It also lets arbitrary structs be published, silently emitting malformed events.

**What we did.** `Event` marker interface (`EventName() string`) in `internal/domain/events.go`. Each event struct implements `EventName()` by returning its own `e.Event` field — the field is the single source of truth for the event name, so wire payload and type-identity cannot diverge by construction. The service sets `Event: "alert.decided"` (literal, two call sites — no package const, DRY does not pay at N=2).

**If you're tempted to change this.** Relaxing to `any` trades compile-time safety for "convenience" you won't need — the event set is closed (two types) and JSON encoding works identically on a concrete struct through the interface.

---

### §2.7a — stdout is the event bus; stderr is logs (PREVIEW — bites at Story 8)

**Trap.** Letting `slog` default to stdout pollutes the simulated event stream. A reviewer running `./server | jq .` expects pure event JSON on stdout.

**What we did (planned).** Publisher writes **only** to `os.Stdout`. `slog` is initialized with a handler writing to **`os.Stderr`** in `cmd/server/main.go`. Seeded here while the domain-events context is fresh; the wiring lands in Story 8 (stdout publisher) and Story 16 (logger setup in main).

**If you're tempted to change this.** Any `log.Printf` or default `slog` call without explicit stderr handler will contaminate stdout. Test by piping stdout to a file post-escalation and confirming exactly one JSON line.
