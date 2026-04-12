# Design Decisions — Alert Management Service

> Running log of non-obvious implementation choices. One entry per decision.
> Append-only in time order. Respawned squads read this to regain context.

---

## D1 — Module location: `alert-service/` subdirectory (Story 1, 2026-04-13)

**Decision:** Go module lives at `alert-service/`, not at the repo root.

**Why:** Mirrors design §3 byte-for-byte. Isolates Go sources from repo-level artifacts (LICENSE, `claude-yolo.sh`, `project/docs/`). Cost: all `go`/`make` commands run from inside `alert-service/`.

**Revisitable:** Unlikely — moving later requires rewriting every import path.

---

## D2 — Module path matches remote: `github.com/dangolds/idoalerts/alert-service` (Story 1, 2026-04-13)

**Decision:** Module path tracks the actual git remote (`github.com/dangolds/idoalerts`) with `/alert-service` subdir suffix.

**Why:** Initial placeholder `github.com/dan/fincom/alert-service` was chosen before remote was confirmed. Renamed mid-story once remote surfaced — free to rename pre-import (zero cascades).

---

## D3 — Go directive: track-whatever-`go get`-produces, min 1.22 (Story 1, 2026-04-13)

**Decision:** `go.mod` go directive is **not manually pinned** to a specific version. Accepted `go 1.25.0` as produced by `go get github.com/go-playground/validator/v10`. Minimum is 1.22 for mux pattern-matching per design §9.6.

**Why:** Design §9.6 requires `>= 1.22`, not exact `1.22`. Fighting the auto-bump (validator v10 declares `go 1.25.0`) adds friction with no runtime benefit — mux behavior is identical on 1.22 through 1.25+. Per user override, stop fighting the pin.

**Corollary:** Later stories (notably Story 11's `go mod tidy` + `validator` import) will let the pin drift naturally with deps. Do **not** re-pin manually.

---

## D4 — `.keep` files over `doc.go` for empty packages (Story 1, 2026-04-13)

**Decision:** Empty leaf directories under `internal/` and `cmd/server/` use empty `.keep` files to satisfy git, not `package X` `doc.go` stubs.

**Why:** `cmd/server/` would require `package main` + stub `func main(){}` because `package main` without `main()` fails `go build ./...`. That forces inconsistency (`.keep` in one dir, `doc.go` in five) or a throwaway stub Story 17 immediately rewrites. `.keep` is simpler, and all packages get real code within 2–3 stories anyway.

**Gemini pushed back:** said `doc.go` is more idiomatic. Team-lead overrode after weighing the `cmd/server/` stub cost.

---
