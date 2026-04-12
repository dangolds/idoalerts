# Gotchas — Alert Management Service

Running log of Go-idiom traps and PRD-alignment invariants that would not be obvious from reading code alone. Append newest-first. Each entry: **Trap** (what could go wrong) / **What we did** (the chosen fix) / **If you're tempted to change this** (what breaks). Reference DesignAndBreakdown.md sections by number.

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
