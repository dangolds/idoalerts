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

## D3 — Go directive: floor at 1.22, accept any toolchain ≥ 1.22 (Story 1, 2026-04-13)

**Decision:** Declared `go 1.22` — the minimum floor per design §9.6's mux pattern-matching requirement. Dep-auto-bumps (validator declares `go 1.25.0`) are a *maintainer* minimum, not a language-feature requirement; we explicitly re-pin to the design floor post-`go get` / post-`tidy`.

**Why:** Design §9.6 requires `>= 1.22`. Oversight flagged a portability concern: hardcoding `go 1.25.0` would force any reviewer on Go 1.22–1.24 into a toolchain auto-download just to try this assignment. Keeping the floor at 1.22 lets any compatible toolchain build without surprises; mux behavior is identical on 1.22 through 1.25+.

**Corollary:** Any `go mod tidy` in future stories will auto-raise the floor to 1.25.0 (validator's declared minimum). The architect on duty should `go mod edit -go=1.22` immediately after `tidy` to keep the floor at design intent. This is a 5-second step; document in the story's Implementation Notes if it trips anyone up.

---

## D4 — `.keep` files over `doc.go` for empty packages (Story 1, 2026-04-13)

**Decision:** Empty leaf directories under `internal/` and `cmd/server/` use empty `.keep` files to satisfy git, not `package X` `doc.go` stubs.

**Why:** `cmd/server/` would require `package main` + stub `func main(){}` because `package main` without `main()` fails `go build ./...`. That forces inconsistency (`.keep` in one dir, `doc.go` in five) or a throwaway stub Story 17 immediately rewrites. `.keep` is simpler, and all packages get real code within 2–3 stories anyway.

**Gemini pushed back:** said `doc.go` is more idiomatic. Team-lead overrode after weighing the `cmd/server/` stub cost.

---
