# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`ainovel-cli` is a Go 1.25 CLI that writes full-length novels (200–500 chapters) autonomously. A deterministic Engine drives three LLM workers (Architect / Writer / Editor) via a fact-driven routing table; a small set of Arbiter LLM functions handles semantic judgments. All code comments, docs, prompts, and UI strings are in Chinese; keep new comments in Chinese to match.

The authoritative design doc is `docs/architecture.md`. Read its §2 (core principles), §10 (things we explicitly do not do), and §11 (verification strategy) before changing anything under `internal/host`, `internal/flow`, `internal/arbiter`, `internal/tools`, or `internal/store`. `docs/refactor-flow-driven.md` is historical and must not be used as a design reference.

## Commands

```bash
go build ./cmd/ainovel-cli            # build binary
go run ./cmd/ainovel-cli              # TUI (first run launches setup wizard → ~/.ainovel/config.json)
go run ./cmd/ainovel-cli --headless --prompt "..."   # headless; resumes from ./output/novel if present
go run ./cmd/ainovel-cli books                        # read-only: list books, chapters [dir], read <n> [dir]

gofmt -l .                            # CI fails if this prints anything
go vet ./...
go test -count=1 ./...                # full suite (CI runs on ubuntu + windows)
go test -race -count=1 ./internal/host ./internal/store ./internal/tools   # CI race job

go test ./internal/flow -run TestRoute_ExhaustiveAgainstSpec   # single test by name
go test ./internal/agents -run 'TestContract_'                # agentcore contract tests (run before bumping agentcore)

go generate ./internal/models/...     # regenerate models_generated.go from OpenRouter (network)

go run ./cmd/ainovel-cli eval --cases evals/cases/smoke             # offline eval harness (needs real LLM config)
go run ./cmd/ainovel-cli eval --cases evals/cases/smoke --variant ./my-prompts --repeat 2   # A/B prompt variant
```

**Local builds need `go.work` with a sibling `../agentcore` checkout** (gitignored, so create it if missing: `use (. ../agentcore)`; clone `https://github.com/voocel/agentcore` next to this repo). `internal/agents/ctxpack` uses `corecontext.FindCutPoint`, which only exists on agentcore main, not in the v1.8.2 release pinned by `go.mod`. CI sets `GOWORK=off` and therefore fails on this snapshot until agentcore tags a release and `go.mod` is bumped (run the `TestContract_*` tests before bumping). Do not set `GOWORK=off` locally.

Runtime artifacts land in `./output/novel/` (gitignored as `output*`). One book per working directory; running again in the same directory resumes from checkpoints.

## Multiple books

One launch directory = one book. A book is identified by its launch dir: `Config.LaunchDir` (`json:"-"`, defaults to cwd) is where the per-book `./.ainovel/config.json`, `./.ainovel/rules/` and `./output/novel` all hang off. `OutputDir` derives from it and is not user-configurable.

- **Sequential switching inside one session** (`internal/entry/tui/switch_book.go`): `/books` → `Enter` swaps the whole session to another book, and `/newbook <dir>` creates one in a fresh directory and swaps to it (refused if that dir already holds a book — overwriting one is irreversible). Both funnel through `openBookAt` — stop the engine (checkpoints make it lossless), `Close()` the Host to release the dir lock, then `startup.OpenBook(launchDir)` and rebuild the Model from scratch. `Model.registryDir` exists solely so tests can point the shelf at a temp dir: adopting a book records into `books.json`, and a test must never write the user's real shelf (it has happened twice). Rebuild, never field-by-field reset: Model has dozens of fields bound to the live Host, and a missed one leaks the previous book. Books held by another process are probed with `host.BookInUse` and marked 使用中 in the shelf (the current book is excluded — flock also reports your own lock). This is still one book at a time, so it does not contradict §10 item 8.

- **Per-book overrides**: `./.ainovel/config.json` and `./.ainovel/rules/*.md` overlay the global `~/.ainovel/` config and rules for that directory. Voice-layer overrides live in `output/novel/style/` so they travel with the book.
- **One process per book**: `internal/host/book_lock.go` takes a `flock` on `output/novel/.ainovel.lock` for the Host's lifetime; a second process on the same directory fails with `ErrBookInUse`. The OS releases the lock on crash.
- **No in-process multi-book**: a single serial Engine loop, no parallel workers (architecture.md §10 item 8). To write two books concurrently, run two processes in two directories. Docker: mount a different `/workspace` per book, config dir can be shared.
- **Bookshelf / browsing** (`internal/library`, observer-only like `diag`): entry layers register the book dir in `~/.ainovel/books.json` after `host.New` succeeds. `ainovel-cli books | search <q> [dir] | chapters [dir] | read <n> [dir] | reviews [n] [dir] | foreshadow [dir] | characters [dir] | timeline [dir] | violations [dir]` and the matching TUI `/books /search /chapters /read /reviews /foreshadow /characters /timeline /violations` read store files directly without the lock, so they work while another process is writing. The registry stores only dirs; titles and progress are read live; `/books` is also the book switcher (Enter), while `→` keeps the read-only chapter browse. `library` reports counts and ages only; judgments such as "stale foreshadow" stay in `diag`. `search` scans chapter/outline/summary files on every call — no index, because chapters get rewritten by rework and `/sync`, and a stale index is worse than a 100ms scan. The shelf reads `meta/usage.json` plus a bounded walk of chapter mtimes for cost/activity, only on the `Registry.List` path.

## Architecture in one screen

**Three-way split, applied to every decision point** (architecture.md §2.1):

| Kind of decision | Goes in | Shape |
|---|---|---|
| Enumerable state transition ("who runs next after a commit?") | `internal/flow/router.go` `Route(State) → *Instruction` | Pure function, no IO. Spec'd by `router_exhaustive_test.go` (≈120k combinations). **Change the spec first, then the implementation.** |
| Bounded semantic judgment (pick planner, triage user steer, failure/deadlock exit) | `internal/arbiter/` | One `Collect*Facts` (IO) + `Decide*` (replayable) pair + a dedicated `XxxDecision` type per scenario. Every decision is appended to `meta/decisions.jsonl`. |
| Open-ended creation (a chapter, a review, a plan) | Workers built in `internal/agents/build.go` | Full `agentcore` loop with its own context/model, run by Engine via `subagent.Runner.Run`. Workers never talk to each other; they cooperate only through Store artifacts. |

If a new decision fits none of the three, it probably is not a real decision point.

**Dependency direction** (one-way): `entry → host → agents/arbiter → tools → store → domain`. `flow` sits above `store`, below `host`. `errs` may be imported anywhere; `diag` only subscribes to host events and reads `store` read-only.

**Engine loop** (`internal/host/engine.go`): single goroutine. Each iteration: apply queued intervention actions → advance gate boundary → `Route(LoadState())` (or a pending Arbiter dispatch, or `planStartFallback`) → precheck → deadlock tracking (same Agent+Task repeated: 3× consult Arbiter, 5× hard pause) → `runWorker`. `nil` instruction = finished / waiting for user. `ctx` cancel = pause; checkpoints make it lossless. **Nothing in Host may auto-restart the engine** — only user `Continue` or process `Resume`.

**Tools are the only interface to the fact layer** (`internal/tools/`). They return structured facts (`arc_end`, `pending_rewrites`, `final_verdict`), never dispatch instructions. Every write tool checks the latest checkpoint for the same `Scope+Step+Digest` and returns the existing artifact if matched, so retries and post-crash redispatch are safe. Single-file writes are `temp + fsync + rename`; `commit_chapter` uses a persisted `PendingCommit` saga. Tools do no LLM calls.

**Fact layer is flat** (`internal/store/`): `Progress` (index), `Checkpoint` (append-only `meta/checkpoints.jsonl`), artifacts (chapters/outline/summaries/characters/world), plus append-only side facts (`decisions.jsonl`, `outline_feedback.jsonl`, `rule_violations.jsonl`, `timeline.jsonl`, `state_changes.jsonl`). `meta/run.json` (`RunMeta`) holds user *intent* (planning tier, pending steer, review/advance mode), not creative facts. Recovery reads only Progress + Checkpoint + RunMeta; `decisions.jsonl` is audit, never a recovery source.

**Phase / Flow** (`internal/domain/transitions.go`): `Phase` is monotonic `init → premise → outline → writing → complete`. `Flow` (`writing / reviewing / rewriting / polishing / steering`) is only written by tools; `Route` reads it, Host never mutates it.

**Worker discipline lives in code, not prompts** (§6.3): `StopAfterTools` ends a run when the key tool succeeds; `CheckpointDeltaGuard` (`internal/agents/guard/`) refuses `end_turn` until the expected checkpoint exists; tools enforce ownership/preconditions. Do not add behavioral rules to prompts to fix flow bugs — that signals the wrong layer.

**Observability**: `agentcore.ProgressPayload` (transport, full text) → `host.Event{Summary, Detail}` → file logger writes `Detail`, TUI shows `Summary`. UI reads only the event stream or `Host.Snapshot()`, never Store. `internal/diag` diagnoses and exports (`/diag`, `meta/diag-export.md`) but must never act.

**Read-only panels** (`/books /chapters /search /timeline /violations /reviews /foreshadow /characters`): report pages wrap long text instead of truncating — a `…` there eats the review suggestion or foreshadow description the user opened the panel for. Cursor lists need predictable row heights, so they use `Tab` to expand just the cursor row; expansion makes rows variable-height, so scrolling reads `libraryState.rowOffsets` (registered by the renderer) instead of assuming `listStart + i*rowHeight`. `/chapters` is two columns (book detail left, chapter list right) joined line-by-line into one viewport.

**TUI layout contract** (`internal/entry/tui`): three columns, sidebar 25% capped at `sidebarMaxWidth` (44) with the leftover given to the detail panel, center takes the rest. Runtime summaries (last commit / review / chapter summaries) live in the sidebar; the detail panel holds outline, characters, synopsis, premise. The side panels' lipgloss `Width` excludes their 1-column border, so `Model.eventFlowWidth` subtracts 2; `TestWorkbenchViewFitsTerminal` renders a dense snapshot at three sizes and fails on any line wider than the terminal. Panels use flat "标题 ───" ruled headers (`renderRuledHeader`), no bordered cards; sidebar values wrap at " · " boundaries (`wrapAtSeparators`) rather than truncating; completed outline chapters collapse to one line once the outline exceeds `outlineGridThreshold`. Read-only modals share `reportModalSize` (92% of the terminal, max 140 cols).

## Assets and prompts (`assets/`)

Everything under `assets/` is `go:embed`ed via `assets/load.go`. See `assets/README.md` for the placement table; the short version:

- `prompts/*.md` — worker system prompts, arbiter prompts, import/simulation/revision prompts. `writer.md` is a protocol template with a `{{VOICE}}` placeholder filled from `voice.md` (overridable per book at `<output>/style/` and globally at `~/.ainovel/style/`; see `docs/voice-layer.md`). `assets/load_test.go` byte-compares the assembled writer prompt against `testdata/writer-golden.md`.
- `references/*.md` — writing knowledge injected via `novel_context`, **not** into system prompts. Adding a file requires three wiring points: a field on `tools.References`, a read in `load.go loadReferences`, and an injection in `novel_context.go` (`writerReferences` / `architectReferences`). Dropping a file in the directory does nothing by itself.
- `styles/<name>.md` — appended to the writer system prompt; filename = `config.style` value.
- Mechanical defaults (banned phrases, thresholds) are code: `internal/rules/snapshot.go SystemDefaults()`. User rules come from `~/.ainovel/rules/*.md` / `./.ainovel/rules/*.md` and are normalized by `internal/userrules` into `meta/user_rules.json`.

Prompt envelope paths (`working_memory.*` etc.) must match what `novel_context` actually emits. Tool parameter shapes are defined only in tool schemas; prompts add semantics, not JSON examples.

## Hard rules when editing (from architecture.md §10)

- No second dispatcher: every "who runs next" goes through `Route` or an Arbiter decision. No stray if/else dispatch.
- No auto-resume / idle-restart logic in Host, ever. Historical patch `idleResumeCount` masked real bugs and was removed.
- Task completion is evidenced only by a checkpoint write, never by "tool exec ended".
- No Task/Job/WorkItem/WorkflowInstance abstractions; no parallel workers; no LLM calls inside tools.
- Do not hard-code guards against "LLM hallucination" (keyword checks, score thresholds). Improve the tool return values or `novel_context` instead. Code fixes only provable invariants (ordering, idempotency, phase, permissions).
- Budget and chapter-advance policy (`BudgetSentinel`, `ChapterAdvanceGate`) are Engine-boundary components; they do not belong in `Route` or tools.
- Recovery code lives only in `host/resume.go` and `engine.planStartFallback`.
- Missing capability in `agentcore`? Fix upstream; do not write an application-layer workaround.

## Key docs by topic

| Topic | Doc |
|---|---|
| Runtime architecture, rules, test assets | `docs/architecture.md` |
| Engine + Arbiter design rationale and review history | `docs/engine-arbiter.md`, `docs/engine-rfc.md` |
| Context compression for the Writer | `docs/context-management.md` (`internal/agents/ctxpack`) |
| `/review on` / `/next` chapter gating | `docs/chapter-advance-gate.md` (`internal/host/advance_gate.go`) |
| Import pipeline (ingest → segment → analyze → synthesize → publish) | `docs/import-pipeline.md` (`internal/host/imp`) |
| Voice layer overrides | `docs/voice-layer.md` |
| User rules normalization | `docs/user-rules-runtime.md` |
| Prompt caching across litellm / agentcore / ainovel | `docs/prompt-cache-design.md` |
| Eval harness (`ainovel-cli eval`) | `docs/evaluation-system.md` (`internal/eval`, cases in `evals/cases/`) |
| Reading logs and store files during a long run | `docs/observability.md` |
| Read-only panel layout rules (two-column chapters, Tab-expand, no truncation) | `internal/entry/tui/library.go`, `library_wrap.go` |
| Building and cross-platform packaging (goreleaser / Docker) | `docs/build-release.md` |
| Channel panel: definition + whole-book switching (`/channel`, `/config` alias) | `internal/entry/tui/command_channel.go`, `internal/bootstrap/channel.go` |

## Configuration

Global config is `~/.ainovel/config.json`; `./.ainovel/config.json` overlays it per project (gitignored, contains keys). Schema and comments are in `config.example.jsonc`. `provider` is a key into `providers`, not a protocol name. Per-role models go under `roles.{architect,writer,editor,import_segment,import_analyze,import_synthesize}`; the Arbiter always uses the default model.
