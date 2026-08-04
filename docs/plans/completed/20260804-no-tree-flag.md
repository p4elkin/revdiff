# revdiff: a flag to start with the tree/TOC pane hidden

## Overview
- Add `--no-tree` (`REVDIFF_NO_TREE` / `no-tree` ini key), a bool flag defaulting `false`, that
  seeds the tree/TOC pane as hidden at startup — the same state the `t` key (`toggle_tree`)
  produces at runtime.
- Problem: a single markdown file opened with `--only` wastes a fifth of the width on the TOC
  pane. `--tree-width=1` only narrows it, `t` has to be pressed every launch, and a caller that
  opens revdiff inside a terminal overlay cannot press `t` at all.
- This is plumbing only: `m.layout.treeHidden` (`app/ui/model.go:341`) already is the flag every
  reader (`treePaneHidden()`, `handleResize`, `handleSwitchToTree`, tree-click routing, TOC
  selection) checks. The only gap is that nothing seeds it before the first render.

## Context (from discovery)
- Template flag: `NoStatusBar` (`app/config.go:25`) — same shape (bool, false default, no CLI
  value needed beyond presence).
- `ModelConfig` construction: `app/main.go` copies every `opts.X` into `ui.ModelConfig{...}`
  field by field (e.g. `NoStatusBar: opts.NoStatusBar,`, `TreeWidthRatio: opts.TreeWidth,`).
- `NewModel` (`app/ui/model.go`) builds the initial `layoutState{focus: paneTree}` — this is
  where the seed goes.
- `treePaneHidden()` (`app/ui/model.go:1288-1291`) is the sole read path used by layout/resize
  code: `return m.layout.treeHidden || (m.file.singleFile && m.file.mdTOC == nil)`. Setting
  `layout.treeHidden` at construction is sufficient — `handleResize` (`app/ui/model.go:1466`)
  computes `treeWidth`/viewport width from `treePaneHidden()` on the first `WindowSizeMsg`, so
  there is no separate "apply initial width" step to write.
- `toggleTreePane()` (`app/ui/model.go:1315-1330`) unconditionally flips `layout.treeHidden`
  except when `singleFile && mdTOC == nil` (structurally no tree to show). Starting hidden and
  pressing `t` flips it to `false` and recomputes width normally — no special-casing needed.
- Doc surfaces carrying `--no-status-bar` today, all four need the new row: `README.md:359`,
  `site/docs.html:401`, `.claude-plugin/skills/revdiff/references/config.md:26`,
  `plugins/codex/skills/revdiff/references/config.md:26`.
- Not needed: `SKILL.md` agent-trigger notes. This is a fixed display preference the *user*
  decides at launch time, not a judgment call an AI agent makes about repo state (unlike
  `--untracked`, which the SKILL.md guides agents to infer). No SKILL.md changes in scope.
- Not needed: `plugins/pi/skills/revdiff/SKILL.md` — same reasoning, and the Pi skill only lists
  user-facing command examples, not a flag reference table.

## Development Approach
- **parallel waves**: none - every task edits the same option-plumbing chain (config.go →
  main.go → ui/model.go) in sequence; the doc task also touches multiple files but has no
  code dependency worth parallelizing for a 4-file, mechanical, single-row edit.
- **testing approach**: Regular (code first, then tests) — this is a well-trodden pattern
  (`--no-status-bar`/`--tree-width`), not new design; tests assert the plumbing, not discover it.
- complete each task fully before moving to the next
- run tests after each change, using the narrow per-task command; the full suite runs once in
  the verify task
- maintain backward compatibility (default stays visible; no behavior change when flag is unset)

## Testing Strategy
- unit tests: required for every task, added beside the existing pattern (no new test files)
- no e2e tests in this project (TUI, no browser-driven e2e)

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Solution Overview
- Thread a single bool from CLI/env/ini through `options` → `ui.ModelConfig` → the initial
  `layoutState`, exactly mirroring `NoStatusBar`. No new UI logic; `treePaneHidden()` and
  `toggleTreePane()` already handle a pane that starts hidden.

## Technical Details
```go
// app/config.go — near TreeWidth
NoTree bool `long:"no-tree" ini-name:"no-tree" env:"REVDIFF_NO_TREE" description:"start with the file tree / TOC pane hidden"`

// app/ui/model.go — ModelConfig, Configuration values section, near TreeWidthRatio
NoTree bool // start with the tree/TOC pane hidden

// app/ui/model.go — NewModel, initial layoutState
layout: layoutState{
    focus:      paneTree,
    treeHidden: cfg.NoTree,
},

// app/main.go — ModelConfig literal, near NoStatusBar / TreeWidthRatio
NoTree: opts.NoTree,
```

## What Goes Where
- Implementation Steps below cover the flag, plumbing, tests, and docs — everything is doable
  in this repo.
- No Post-Completion section — nothing here needs manual/external follow-up: the plan itself
  says do not push and do not open a PR.

## Implementation Steps

### Task 1: Add the `--no-tree` CLI flag

**Files:**
- Modify: `app/config.go` (add `NoTree bool` field to the `options` struct, next to `TreeWidth`)
- Modify: `app/config_test.go` (`TestParseArgs_Defaults`, `TestParseArgs_Flags`,
  `TestParseArgs_EnvVars`)

- [x] add `NoTree bool` with tag `` `long:"no-tree" ini-name:"no-tree" env:"REVDIFF_NO_TREE" description:"start with the file tree / TOC pane hidden"` `` in `app/config.go`, positioned next to `TreeWidth`
- [x] in `TestParseArgs_Defaults`, add `assert.False(t, opts.NoTree)`
- [x] in `TestParseArgs_Flags`, add `--no-tree` to the args slice and `assert.True(t, opts.NoTree)`
- [x] in `TestParseArgs_EnvVars`, add `t.Setenv("REVDIFF_NO_TREE", "true")` and `assert.True(t, opts.NoTree)`
- [x] run `go test ./app/... -run TestParseArgs` — must pass before task 2

### Task 2: Wire `NoTree` into `ui.ModelConfig` and seed `layout.treeHidden`

**Files:**
- Modify: `app/ui/model.go` (add `NoTree bool` field to `ModelConfig` Configuration-values
  section, next to `TreeWidthRatio`; set `treeHidden: cfg.NoTree` in the `layoutState{}` literal
  inside `NewModel`)
- Modify: `app/main.go` (add `NoTree: opts.NoTree,` to the `ui.ModelConfig{}` literal, next to
  `TreeWidthRatio: opts.TreeWidth,`)
- Modify: `app/ui/model_test.go` (new subtests near the existing `TreeWidthRatio` subtests
  around line 249)

- [x] add `NoTree bool // start with the tree/TOC pane hidden` to `ModelConfig` in `app/ui/model.go`
- [x] in `NewModel`, change `layout: layoutState{focus: paneTree}` to also set `treeHidden: cfg.NoTree`
- [x] add `NoTree: opts.NoTree,` in `app/main.go`'s `ui.ModelConfig{}` construction
- [x] write test: `ModelConfig{NoTree: true}` produces `m.layout.treeHidden == true` (and `m.treePaneHidden() == true`)
- [x] write test: starting with `NoTree: true`, calling `m.toggleTreePane()` (or dispatching `keymap.ActionToggleTree`) flips `treeHidden` back to `false`
- [x] write test: `ModelConfig{}` (NoTree unset) keeps `m.layout.treeHidden == false` (default-visible regression guard)
- [x] run `go test ./app/ui/... -run TestNewModel` (and any renamed/adjacent test names actually used) — must pass before task 3

### Task 3: Update docs for `--no-tree`

**Files:**
- Modify: `README.md` (options table, next to the `--no-status-bar` row at line 359)
- Modify: `site/docs.html` (options table, next to the `--no-status-bar` row at line 401)
- Modify: `.claude-plugin/skills/revdiff/references/config.md` (table, next to the
  `--no-status-bar` row at line 26)
- Modify: `plugins/codex/skills/revdiff/references/config.md` (table, next to the
  `--no-status-bar` row at line 26 — keep in sync with the Claude copy per CLAUDE.md)

- [x] add a `--no-tree` row to `README.md`'s options table, matching the existing row style (`--no-status-bar` / `--no-tree` phrasing, `REVDIFF_NO_TREE`, default `false`)
- [x] add the matching `<tr>` row to `site/docs.html`'s options table
- [x] add the matching `|` row to `.claude-plugin/skills/revdiff/references/config.md`
- [x] add the matching `|` row to `plugins/codex/skills/revdiff/references/config.md`
- [x] no test to run for this task (docs-only); visually diff the four rows against the `--no-status-bar` rows for format consistency

### Task 4: Verify acceptance criteria
- [x] verify `--no-tree` / `REVDIFF_NO_TREE` / `no-tree` ini key all set `opts.NoTree` (covered by `TestParseArgs_Flags`, `TestParseArgs_EnvVars`; ini key shares the same `options` struct tag path as every other flag)
- [x] verify default behavior is unchanged (tree visible) when the flag is not passed (covered by `TestParseArgs_Defaults` and the `ModelConfig{}`-unset regression test in Task 2)
- [x] verify `t` still reveals the pane after starting hidden (covered by the `toggleTreePane()` test in Task 2; re-confirmed manually below)
- [x] run full suite: `make test` — passed, 90.5% total coverage, no failures
- [x] run `make lint` — clean, "0 issues."
- [x] build and manually check: `go build -o /tmp/revdiff-notree ./app && /tmp/revdiff-notree --no-tree --only=README.md` — verified via a scripted pty session (python3 `pty` module) since this run has no attached human terminal: initial render is a single full-width box with no left pane, and pressing `t` reveals a second box on the left (tree/TOC pane) alongside the diff pane, confirming both the seeded-hidden state and the runtime toggle
- [x] commit on the current branch with a conventional-prefix message (e.g. `feat: add --no-tree flag to start with tree pane hidden`)
- [x] do NOT push, do NOT open a pull request (satisfied by inaction — nothing was pushed, no PR opened)
- [x] move this plan to `docs/plans/completed/` (skipped by harness instruction — the orchestrator moves the plan file after all phases finish; moving it now would break later review/finalize/stats phases that read this path)
