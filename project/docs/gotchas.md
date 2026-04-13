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

### §2.7a — stdout is the event bus; stderr is logs (Story 8 landed; Story 17 still pending)

**Trap.** Letting `slog` default to stdout pollutes the simulated event stream. A reviewer running `./server | jq .` expects pure event JSON on stdout.

**What we did.** `internal/events/stdout_publisher.go` (Story 8) writes **only** to `os.Stdout` via an unexported `io.Writer` field with no public setter — a test-time writer seam would invite a second call site that bypasses this invariant, so the package exposes only `NewStdoutPublisher()` pinning `os.Stdout`. The package doc declares the "no logging in this package" rule as an enforced invariant, not a convention: any `log.Printf` or default `slog` call added here regresses the stream. `slog` setup against `os.Stderr` still lands in Story 17's `cmd/server/main.go` composition root.

**If you're tempted to change this.** Any `log.Printf` or default `slog` call inside `internal/events/`, or a new public `NewStdoutPublisherWithWriter(w io.Writer)` constructor, will eventually contaminate stdout or bypass §2.7a. For direct-package tests (none today), use an internal `_test.go`-scoped builder that does not expose a runtime seam. Test by piping stdout to a file post-escalation and confirming exactly one JSON line per event.

---

### Story 8 — Concurrent `Publish` can interleave JSON bytes on stdout

**Trap.** `json.Encoder.Encode` is not documented as goroutine-safe; two concurrent `Publish` calls sharing `os.Stdout` can interleave bytes and produce torn JSON lines. POSIX `Write ≤ PIPE_BUF` is atomic **only for pipes**, not terminals or files; Windows `WriteFile` makes no atomicity guarantee at all. Story 17 wires this publisher behind concurrent HTTP handlers (one goroutine per request), so concurrent `Publish` is the default case, not an edge case. A torn line silently breaks downstream `tail -f | jq` consumers (they die on a single malformed line) and the failure is non-local — it doesn't show up in single-request tests.

**What we did.** `StdoutPublisher` holds a `sync.Mutex`; `Publish` takes `mu.Lock()` / `defer mu.Unlock()` around `json.NewEncoder(p.w).Encode(event)`. The encoder is constructed **per call** rather than cached in the struct — with the mutex in place concurrent safety is covered either way, but per-call keeps the struct minimal (`{mu, w}`), keeps `w` the single source of truth, and sidesteps the undocumented goroutine-safety of `json.Encoder`. Pointer receiver on `Publish` (required by the mutex; `go vet`'s `copylocks` analyzer catches a value-receiver regression at build-lint). Debate snapshot: Oversight + Gemini recommended caching the encoder (saves one alloc); Senior Reviewer recommended per-call; team-lead sided with per-call — alloc cost is invisible at MVP event volume, struct stays simpler.

**If you're tempted to change this.** Don't drop the mutex to "optimize" — the PRD-scale event size (~200 bytes) doesn't make the mutex observable, and the Windows atomicity gap is real. Don't cache the encoder without auditing goroutine-safety and updating the comments. Don't add a public writer-injection constructor — the §2.7a invariant depends on the unexported `w` + single constructor. If Story 17 ever wraps `os.Stdout` in `bufio.Writer`, the Flush MUST sit inside the same critical section as the Encode (prescribes the invariant, not the sequence — a wrapper owning Flush elsewhere still holds it).

---

## Testing

### Story 7 — `go test -race` silently requires cgo on Windows

**Trap.** `go test -race` on Windows silently requires cgo + a C toolchain (gcc/tdm-gcc/clang). On a dev box without one, `-race` fails with `C compiler "gcc" not found` and the race detector never runs. A test suite that leans on `-race` as its *primary* assertion against a `sync.RWMutex + Clone` design would silently produce false-green results when run locally — the file builds, the non-race test passes, but the happens-before checks never executed.

**What we did.** The Story 7 concurrency test (`TestAlertRepo_ConcurrentCreateFindUpdate_RaceClean`) is deliberately partitioned so any race-induced error type is caught loudly *without* the race detector. A pool of 32 shared IDs is seeded SERIALLY before goroutines spin; the parallel workload is three disjoint shapes: `Create` on fresh UUIDs (collision cryptographically impossible → must return nil), `FindByID` on seeded IDs (must exist → must return nil), `Update` on seeded IDs (must exist → must return nil). Any non-nil error on any of these paths fails the test. A lock-corruption bug would flip a map entry into "missing" or "already-present" state and surface as `ErrNotFound` / `ErrAlreadyExists` on an op that cannot legally produce it. `-race` remains the CI-runner gate via Story 18's Makefile (CI runners are linux with cgo); locally on Windows, we accept the constraint.

**If you're tempted to change this.** Don't drop the error-type partition just because `-race` is green in CI — the partition is the *primary* assertion; `-race` is belt-and-braces. A "simplify" refactor that reintroduces a tolerance bucket (`if err == ErrNotFound || err == ErrAlreadyExists { continue }`) would silently accept lock corruption. Similarly, don't route Creates and Reads through the same ID pool without seeding-vs-parallel discipline — the partition depends on "Create ops only use fresh UUIDs" and "Read/Update ops only use seeded IDs". If a future test wants to stress collision paths, it should be a *separate* test with its own expected-error shape.
