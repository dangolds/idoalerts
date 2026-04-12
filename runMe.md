# runMe.md — Handoff for a Fresh Agent

> Paste this whole file into a new Claude Code session. It captures **every user-issued rule** and the **current state of work** for the Alert Management Service build.

---

## 1. What this project is

You are orchestrating the **Fincom Alert Management Service** home assignment. A Go REST microservice that creates, lists, escalates, and decides sanctions alerts — tenant-isolated, write-once decisions, stdout-emitted domain events.

- **Primary working directory:** `C:/Users/dude/dev/fincom/`
- **Git remote:** `github.com/dangolds/idoalerts`
- **Branch:** `main`
- **Go module:** `github.com/dangolds/idoalerts/alert-service` (lives at `alert-service/` subdir)
- **Platform:** Windows, bash shell (use forward slashes, `/dev/null` not `NUL`, Unix commands).

### Authoritative docs (read these first)

- `project/docs/PRD.md` — the assignment spec (4 endpoints, write-once decisions, tenant isolation, stdout events).
- `project/docs/DesignAndBreakdown.md` — the refined design with section numbers (§2.1, §9.6, etc.) the checklist references.
- `project/docs/checklist.md` — **20 stories across 7 phases.** This is the work queue. Each story has acceptance criteria + an Implementation Notes block to fill in on completion.
- `project/docs/design-decisions.md` — **running memory doc.** Append one `## Dn` entry per non-obvious decision per story. MVP-lightweight, not a deliverable.

---

## 2. User-issued rules (non-negotiable)

These came from the user across multiple turns — treat them as standing orders.

### 2.1 Use `/team-lead` skill
The user explicitly invoked `/team-lead`. You orchestrate; you do **not** implement code directly. You spawn a 3-teammate squad (Architect + Senior Reviewer + Oversight) and coordinate via SendMessage. See §4 below for the exact protocol.

### 2.2 Respawn cadence: every 3 stories (not every story)
Shut down and respawn the entire squad (fresh Claude contexts for all three teammates) **after every 3 completed stories.** The team-lead skill defaults to per-story respawn — the user overrode this to **per-3-story respawn** to reduce spawn overhead while still keeping contexts fresh.

Planned cycles: **Stories 1–3 → respawn → 4–6 → respawn → 7–9 → respawn → 10–12 → respawn → 13–15 → respawn → 16–18 → respawn → 19–20 (final).**

### 2.3 Team discusses every single task
No silent implementations. For **every story**, run the full loop: **Plan → Consult (Oversight calls Gemini) → Debate → Implement → Review → Finalize.** The team-lead facilitates; the teammates actually debate each other (reviewer challenges architect, oversight flags architectural drift).

### 2.4 Team-lead + user have final authority
If the squad deadlocks, team-lead decides. User can override anyone at any time. The checklist's "Engineer Authority Notice" is in force — **diverge from the plan when implementation reveals a better approach**, and record the deviation in the story's Implementation Notes block.

### 2.5 Commit AND push after every single story
Regardless of the 3-story respawn boundary. Every story = at least one commit + push. Use specific `git add <files>` (no `-A`). Standard commit trailer: `Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>`.

### 2.6 Mark each story done + leave continuity notes
When a story finishes, the Architect must:
1. Tick `[ ] → [x]` on the story header AND every sub-bullet in `project/docs/checklist.md`.
2. Replace the HTML-commented Implementation Notes block with a **visible** block containing:
   - `**Files created/modified:**` — list
   - `**Design decisions:**` — any non-obvious choices
   - `**Deviation from plan:**` — if any, with reasoning
   - `**Next-story handoff:**` — 1–2 sentences on what's ready for the next story to build on. **This is how the post-respawn squad picks up where you left off — critical.**

### 2.7 Maintain `project/docs/design-decisions.md` for memory (MVP scope)
Append-only log of genuine fork-in-the-road decisions. **Memory only — not a deliverable.** Keep entries terse (3–6 lines each) in this format:

```markdown
## D<n> — <short title> (Story <N>, YYYY-MM-DD)

**Decision:** <what was chosen>
**Why:** <the reasoning>
**Revisitable:** <optional — is this locked in or reversible?>
```

Don't document trivial code-follows-design choices. Only choices a respawned squad couldn't infer from the code + PRD + DesignAndBreakdown.md + checklist.

### 2.8 Gemini Bridge at `http://localhost:8000`
Oversight uses this for second opinions on architectural calls. Protocol is in §5 below. Key rules:
- Shell commands only — **no Python scripts, no temp files** (race risk in multi-agent env).
- Hardcode session names into every command — env vars don't persist across Bash calls.
- First turn per session is slow (~10–30s); subsequent turns fast.
- **Always `DELETE /chat/<session>` when done with that story.**

### 2.9 Specific Go version doesn't matter — but floor is `go 1.22`
The user clarified: "we running go 1.25 its not important the specific version." Interpretation (after oversight's portability flag): **minimum floor is `go 1.22`** (design §9.6 — mux pattern matching). Any `toolchain ≥ 1.22` is fine, but the `go` directive in `go.mod` stays pinned to `1.22` for portability.

**Gotcha:** `go get validator` and `go mod tidy` auto-bump the floor to `go 1.25.0` (validator's maintainer minimum). The Architect must **manually `go mod edit -go=1.22` after any `go get` or `go mod tidy`**. Documented in D3.

---

## 3. Non-negotiable design reminders

From `DesignAndBreakdown.md` — every teammate should have these memorized:

- `go.mod` declares `go 1.22` floor (§9.6 — Go 1.22 mux pattern matching).
- **Events go to `os.Stdout`. Logs go to `os.Stderr` via `slog`.** §2.7a — the simulated broker stream must stay clean.
- Event JSON field is **`decision`** (not `status`). §2.7b — PRD wire format is authoritative.
- Repo **clones on read AND write** (§2.8a — the pointer trap). `Alert.Clone()` is called at every lock boundary.
- **Persist before publish** (§2.7). If `repo.Update` fails, no event. If publish fails, log ERROR and still return success (state is authoritative).
- Run tests with **`-race`** (§5).
- Only deps: `github.com/google/uuid`, `github.com/go-playground/validator/v10`.
- **Decision is write-once.** §2.8b — MVP accepts the read-check-write race (documented in README post-Story 19).
- **Cross-tenant reads return `ErrNotFound`**, not `ErrTenantMismatch` — no existence leak.
- `http.Server` timeouts non-zero (§9.8): ReadTimeout 5s, WriteTimeout 10s, IdleTimeout 120s.
- Mutating handlers: `http.MaxBytesReader(1 MiB)` + `json.Decoder.DisallowUnknownFields()` (§9.10).
- Every layer takes `ctx context.Context` as the first arg (§2.2).
- `AssignedTo *string`; `DecisionNote string` (not `*string`) on the domain; DTO still requires `DecisionNote` on decide (§9.11).
- Timestamps: RFC3339 strings, formatted **at publish time** (§9.4 / §9.13).
- `GET /alerts` with zero matches returns `[]` (make the slice with `make(_, 0)` to avoid nil→null JSON) (§9.1).
- Default `List` sort: `CreatedAt` descending (§9.12).

---

## 4. Team protocol (exactly how to run it)

### 4.1 Create the team
Use `TeamCreate` with `team_name: "alert-service-squad"`.

### 4.2 Spawn 3 teammates in parallel (one `Agent` tool call each)
- **architect** — `mode: "acceptEdits"`, full tool access. Implements.
- **reviewer** — `mode: "plan"` (read-only). Challenges plans and code. Uses `senior-engineer` style format (Context Check → Verdict → Feedback Items).
- **oversight** — `mode: "plan"` (read-only). Enforces global coherence + consults Gemini bridge.

Each spawn prompt must include:
- Role description (which skill to use)
- Which files to read first (PRD, DesignAndBreakdown, checklist, **design-decisions.md**)
- The list of teammates by name
- The project-specific non-negotiables from §3 above
- "Wait for team-lead direction — do NOT start work until told."

### 4.3 Per-story loop (directed by team-lead via SendMessage)
1. **Plan** — ask architect to propose the approach.
2. **Consult** — forward plan to oversight; oversight queries Gemini and summarizes key points (5–8 bullets max, not full transcript).
3. **Debate** — forward plan to reviewer for challenge. Facilitate debate between all three via SendMessage until consensus.
4. **Implement** — tell architect "implement." Architect writes code.
5. **Review** — reviewer audits diff. `[APPROVE]` / `[REQUEST_CHANGES]` / `[DISCUSS]`. Loop back on changes.
6. **Finalize** — architect does checklist tick + Implementation Notes + `design-decisions.md` entry (if applicable) + commit + push. Oversight spot-checks conventions + deletes the Gemini session.

### 4.4 Respawn boundary (every 3 stories)
1. `SendMessage` to each teammate: `{"to": "<name>", "message": {"type": "shutdown_request", "reason": "respawn after 3 stories"}}`. They respond with `shutdown_response` and terminate.
2. Re-spawn fresh via `Agent` calls with the same three names. New contexts; they re-orient by reading the docs (which now include the last cycle's Implementation Notes + design-decisions entries).

### 4.5 Never reset mid-story
A story must complete the full Plan → Finalize loop before any respawn. Mid-story respawn loses in-flight debate context.

---

## 5. Gemini Bridge protocol (for Oversight)

The bridge is a local REST service that wraps Gemini. It's used for "different-LLM second opinion" on architectural decisions.

### 5.1 Pre-flight
```bash
curl -s http://localhost:8000/health
```
If this returns empty or an error, the bridge is not running — proceed with internal team debate only.

### 5.2 Generate a unique session name per story
```bash
echo "oversight-story<N>-$(date +%s)-$RANDOM"
```
Copy the output literally into every follow-up command for that session. Alphanumeric + `-` + `_` only; max 64 chars.

### 5.3 Send a message (single-quoted heredoc piped through node — safe for any content)
```bash
cat << 'EOF' | node -e "let d='';process.stdin.on('data',c=>d+=c);process.stdin.on('end',async()=>{const r=await fetch('http://localhost:8000/chat/MY_SESSION_NAME',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({prompt:d.trim()})});const j=await r.json();console.log(j.response??JSON.stringify(j))})"
<your prompt text here — no escaping needed for "quotes", 'quotes', $vars, or ```code blocks```>
EOF
```
Replace `MY_SESSION_NAME` with the session name. First call per session takes ~10–30s (process spawn); follow-ups are fast.

### 5.4 Follow-up turns
Same command, same session name — Gemini remembers prior turns.

### 5.5 Cleanup (mandatory at story end)
```bash
curl -s -X DELETE "http://localhost:8000/chat/MY_SESSION_NAME"
```

### 5.6 How Oversight prompts Gemini
- **Always tell Gemini EXACTLY what you want it to do.** Give it context (relevant design-doc §excerpt + the architect's plan + the specific question).
- Gemini's opinion is **input**, not authority. Team-lead makes the final call.
- Summarize Gemini's response in **5–8 bullets** (not full transcript) and broadcast to team-lead.

---

## 6. Current state (as of 2026-04-13)

### 6.1 Completed
- **Story 1 (Initialize Go module and directory skeleton)** — **in final commit**.
  - Module at `alert-service/`, path `github.com/dangolds/idoalerts/alert-service`, `go 1.22` directive.
  - Deps: `google/uuid v1.6.0`, `go-playground/validator/v10 v10.30.2` (both `// indirect` until a `.go` file imports them).
  - Directory skeleton per design §3: `cmd/server/`, `internal/{domain,service,storage/memory,events,api}/` each with a `.keep`.
  - `project/docs/design-decisions.md` seeded with **D1–D4** (module location, module path, go directive floor, `.keep` vs `doc.go`).
  - Commits on `origin/main`:
    - `cdaf8ab` — initial scaffold
    - `104f1b3` — relax go directive to 1.25.0 (later reverted)
    - `a8a9825` — rename module path to match remote
    - `122646d` — seed `design-decisions.md`
    - **Pending:** one more small commit to pin `go.mod` back to `1.22` and rewrite D3 to match.
  - Three-commit arc reflects team debate — user override → oversight portability concern → final decision. Documented in D3.

### 6.2 Pending at session stop
- Architect to ship final Story 1 commit: `fix(alert-service): pin go directive to 1.22 + correct D3`. Contents: `alert-service/go.mod` (directive `go 1.22`) + rewritten D3 block in `project/docs/design-decisions.md`.
- Oversight to re-spot-check post-commit, sign off, and `DELETE` the Gemini session `oversight-story1-1776033430-2192`.
- Team to shut down gracefully (per user directive below).

### 6.3 User directive that halted this session
> "when you are done with this tasks (from the checklist) stop. i dont want the team to continue. let the team write all the documents and stop"

So: close Story 1 cleanly (final commit + sign-offs + docs current), then **shut down the squad and stop.** Do not start Story 2 in this session.

### 6.4 Task list in the team-lead's head
```
#1. [in_progress] Orchestrate stories 1-3 (first squad cycle)      ← story 1 finishing, STOP after
#2. [pending]     Squad cycle 2 — stories 4-6
#3. [pending]     Squad cycle 3 — stories 7-9
#4. [pending]     Squad cycle 4 — stories 10-12
#5. [pending]     Squad cycle 5 — stories 13-15
#6. [pending]     Squad cycle 6 — stories 16-18
#7. [pending]     Squad cycle 7 — stories 19-20 (final)
```

---

## 7. How the new agent picks up

1. **Read** — in order — `project/docs/PRD.md`, `project/docs/DesignAndBreakdown.md`, `project/docs/checklist.md`, `project/docs/design-decisions.md`. Then `runMe.md` (this file, as a refresher).
2. **Verify state** — `git log --oneline -n 10` + check `alert-service/go.mod` shows `go 1.22` + confirm Story 1 is ticked `[x]` in checklist. If any of these aren't true, re-read the checklist's Story 1 Implementation Notes block and any trailing uncommitted changes in `git status`.
3. **Invoke `/team-lead`** — same spawn prompts as §4 above, same team name `alert-service-squad` (or new one if the prior team is still present on disk — check `~/.claude/teams/` and `TeamDelete` if stale).
4. **Start at Story 2** — `internal/domain/alert.go` + Status enum + `CanDecide` / `CanEscalate` + `Clone()`. Acceptance criteria in checklist Story 2. Run the full team loop.
5. **Every story's last step:** tick checklist, write Implementation Notes, append to `design-decisions.md` if needed, commit + push.
6. **Respawn at story boundaries 3/6/9/12/15/18** (not at 4/7/10 etc. — respawn after 3 is done, before 4 begins).

---

## 8. Gotchas learned this session

- **Overlapping directives create ambiguity.** If team-lead sends two messages to the architect back-to-back with conflicting instructions (e.g., one says "keep go 1.22," next says "user override — accept 1.25"), the architect may pick one and drop the other. **Fix:** send a single consolidated message per directive; if a new insight arrives mid-flight, send one follow-up that supersedes the prior.
- **`git commit` + `git push` are separate actions.** Confirm the push succeeded (`git log origin/main..HEAD` should be empty) before declaring a story done.
- **Doc state can drift from file state.** The architect may update one file and forget to update a dependent doc (`design-decisions.md` D3 contradicted `go.mod` for one commit). Team-lead should verify important docs post-commit when stakes are high.
- **The team-lead skill prohibits team-lead from using Edit/Write/Bash for code changes.** Use SendMessage to direct the architect. Writing `runMe.md` and updating task lists is fine because those are orchestration artifacts, not production code.

---

## 9. Appendix: recent commits on `origin/main`

```
122646d docs: add design-decisions.md with Story 1 entries (D1-D4)
a8a9825 fix(alert-service): rename module to github.com/dangolds/idoalerts/alert-service
104f1b3 chore(alert-service): story 1 amendment — relax go directive to 1.25.0
cdaf8ab feat(alert-service): story 1 — module init + directory skeleton
b805d20 docs
e374ec6 Add project docs and ignore Claude Code artifacts
14bdab2 Initial commit
```
Expected incoming:
```
<pending> fix(alert-service): pin go directive to 1.22 + correct D3
```

---

*End of runMe.md. Paste this entire file into the new agent's first turn, then say "continue from Story 2."*
