# Alert Management — Gotchas

> Feature-scoped traps. For cross-cutting Go-idiom traps and PRD-alignment invariants, see the canonical project-wide log at `../../docs/gotchas.md` (which covers domain events today and will grow as stories land).

This file captures traps specific to the Alert Management feature that don't fit the broader cross-cutting log. Today most non-obvious invariants are cross-cutting and live in `project/docs/gotchas.md`; this file exists so future respawned agents know where feature-local traps go.

## Feature-local entries

_(none yet — see `project/docs/gotchas.md` for the `## Domain Events`, `## Storage (In-Memory Repo)`, and `## Testing` sections which currently hold all active traps relevant to this feature. The `## Domain Events` section now includes two Story 8 entries — the updated §2.7a "stdout = events, stderr = logs" invariant (publisher side landed; main.go slog wiring lands Story 17) and the "concurrent Publish can interleave JSON bytes on stdout" trap explaining why `StdoutPublisher.Publish` serializes `json.Encoder.Encode` under a `sync.Mutex` and why the encoder is per-call rather than cached. The `## Testing` section covers the `go test -race` cgo-on-Windows constraint and explains why Story 7's concurrency test is partitioned by operation so it doesn't rely on `-race` as the primary assertion.)_
