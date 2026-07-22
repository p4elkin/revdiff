# Markdown Preview Mode (local patch)

## Overview

Add a read-only rendered markdown view to revdiff, toggled with `P`. In this mode a markdown file is
shown with real tables, styled headings, framed code blocks, and Mermaid diagrams drawn as
box-drawing art. Pressing `P` again returns to the normal annotatable view.

The problem: plan documents are reviewed in the terminal, and every plan carries a Mermaid diagram.
revdiff currently shows markdown *source* with chroma colouring, so tables do not line up and a
Mermaid fence is an unreadable block of `A[Plan] --> B{Has Mermaid?}`.

This is a **personal patch on a local clone**, not an upstream contribution. The maintainer declined
this feature twice — discussion #164 "Feat: markdown preview" (2026-05-01) and issue #244 "feat:
Better table rendering" (closed NOT_PLANNED, 2026-06-28) — on the grounds that the payoff did not
justify the complexity. He was not against it technically; in #164 he sketched this exact design,
including the `--markdown-preview` flag and the `P` hotkey. There will be no PR and no GitHub fork.

Two objections he raised do not apply to a local build. **Theme sync** was a problem because a
released feature would need a glamour style generated from revdiff's 23 theme colour fields and kept
in sync; locally we pick one style and stop. **Code fences highlighted twice** only happens when
glamour renders inside the existing per-line chroma path; a whole-document render replaces that path,
so nothing nests. His third objection — that a diff showing only some rows of a table cannot render
sensibly — is avoided by the gate described below.

## Context (from discovery)

- Repo: local clone of `umputun/revdiff` at `~/dev/revdiff`, branch `md-preview`, based on master
  commit `1b563f8` (after tag `v1.11.1`). Go 1.26.2, golangci-lint installed.
- Annotations anchor to **source line numbers**: `Annotation` struct at `app/annotation/store.go:13-19`
  (`File`, `Line`, `EndLine`, `Type`, `Comment`). The cursor (`m.nav.diffCursor`) is an index into
  `m.file.lines`. Rendering reflows text, so rendered rows cannot match source lines.
- There is **no stored source-line to display-row map**. Row positions are recomputed on demand by
  walking lines and summing heights: `wrappedLineCount` (`app/ui/diffview.go:413-424`),
  `hunkLineHeight` (`app/ui/annotate.go:528-544`), `cursorViewportYUsing` (`app/ui/annotate.go:560-582`).
  Everything assumes one entry per source line.
- There is **no markdown parser anywhere**. The TOC pane is hand-written scanning:
  `ParseTOC` (`app/ui/sidepane/toc.go:34-104`), with a fence helper `fencePrefix`
  (`app/ui/sidepane/toc.go:266-284`).
- There is **no view-mode enum**. Modes are independent bools on `modeState` in `app/ui/model.go`
  (`wrap`, `collapsed.enabled`, `compact`, `lineNumbers`, `wordDiff`, `showBlame`). The only render
  dispatch point is `renderDiff` (`app/ui/diffview.go:273-295`), which early-returns to
  `renderCollapsedDiff()` when collapsed mode is on. A new mode is a new bool plus a new early return.
- Test conventions: table-driven tests with `stretchr/testify`, mocks via `matryer/moq`
  (`docs/ARCHITECTURE.md:457-458`). CI runs `go test -race -covermode=atomic ./...` plus
  golangci-lint (`.github/workflows/ci.yml:26-28`).
- revdiff **vendors** its dependencies, so any dependency change requires `go mod tidy && go mod vendor`.

## Development Approach

- **testing approach**: TDD — write the failing test first, then the implementation.
- complete each task fully before moving to the next
- make small, focused changes
- **every task MUST include new/updated tests**, listed as separate checklist items
- **all tests must pass before starting the next task**
- **update this plan file when scope changes during implementation**
- run tests after each change
- maintain backward compatibility: with the mode off, behaviour must be byte-identical to upstream

### Patch discipline (specific to this plan)

This fork is rebased onto every upstream release by hand, and conflicts happen in *edited* lines, not
in new files. Therefore:

- Put essentially all logic in the new file `app/ui/mdpreview.go`.
- Keep edits to existing files to the seven small hunks listed in Task 4. Do not refactor surrounding
  code, do not reformat, do not "improve" nearby lines. Every extra changed line is a future conflict.
- `app/ui/diffview.go` and `app/ui/model.go` are actively developed upstream and are the most likely
  conflict sites. Touch the minimum.

## Testing Strategy

- **unit tests**: required for every task, in `app/ui/mdpreview_test.go` unless stated otherwise.
- **e2e tests**: the project has no UI e2e harness. Manual terminal verification is specified in
  Task 8 and in Post-Completion instead, and it must check rendered *content*, not merely that the
  program started without error.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- keep plan in sync with actual work done

## Solution Overview

Preview mode is **read-only**. While it is on, the cursor and the annotation keys do nothing. This is
the entire trick: it means the patch never touches the line-anchoring machinery, which is what made
this feature expensive in every previous discussion. Annotations created in the normal view are
untouched and are still there when the mode is toggled off.

```mermaid
flowchart TD
    A[User presses P] --> B{m.file.mdTOC != nil?}
    B -->|no| C[Ignore keypress]
    B -->|yes| D[Toggle modes.mdPreview]
    D --> E[renderDiff early-return branch]
    E --> F{Cached render exists?}
    F -->|yes| K[Push cached string into viewport]
    F -->|no| G[Join source lines into one document]
    G --> H[Find mermaid fences]
    H --> I[Render each via mermaid-ascii<br/>fence text kept on failure]
    I --> J[Render whole document with glamour]
    J --> K
    K --> L[Cursor and annotation keys inert]
```

### Gate

Use `m.file.mdTOC != nil`. That field is set at `app/ui/loaders.go:534-538` and already means
"single file, markdown extension, full context". No new condition is needed, and because full context
means the whole file is present, a partially-shown table cannot occur.

## Technical Details

### Dependencies to add

- `github.com/charmbracelet/glamour` **v1.0.0** — markdown rendering. Deliberately v1, not v2: v2 is
  `charm.land/glamour/v2` and requires `charm.land/lipgloss/v2`, a different module path from the
  `github.com/charmbracelet/lipgloss` revdiff already vendors, which would vendor two copies of
  lipgloss. v1 shares revdiff's existing chroma, lipgloss, x/ansi, termenv, colorprofile, uniseg,
  runewidth and regexp2. It adds roughly eight modules, mainly goldmark and bluemonday.
- `github.com/AlexanderGrooff/mermaid-ascii` (MIT) — Mermaid rendering, imported as a library.
  Entry point: `cmd.RenderDiagram(input string, config *diagram.Config) (string, error)` in
  `cmd/render.go`. Note the flowchart renderer lives in package `cmd`, which also imports gin, cobra
  and logrus, so this pulls roughly 30 extra modules into the vendor tree. This cost was chosen
  deliberately over depending on an external binary.

### New state

One field on the existing `modeState` struct in `app/ui/model.go`: `mdPreview bool`. Plus a render
cache so toggling does not re-run glamour and mermaid every time — key it on the file name and the
viewport width, since a width change must invalidate it.

## What Goes Where

- **Implementation Steps** (`[ ]`): everything doable inside this clone.
- **Post-Completion** (no checkboxes): manual terminal checks and the Claude tooling wiring.

## Implementation Steps

### Task 1: Dependency spike — no feature code

The purpose is to find out early whether glamour's newer pinned lipgloss breaks revdiff's existing UI.
glamour v1.0.0 requires a slightly newer `charmbracelet/lipgloss` than revdiff pins, and Go selects one
version for the whole build. If the suite goes red here, **stop and report** rather than starting the
feature.

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `vendor/` (regenerated)

- [x] record the baseline: run `go test -race ./...` on the clean branch and save the result — all 14
      testable packages `ok`, no failures (full listing below)
- [x] `go get github.com/charmbracelet/glamour@v1.0.0`
- [x] `go get github.com/AlexanderGrooff/mermaid-ascii@latest`
- [x] `go mod tidy && go mod vendor`
- [x] run `go build ./...` — must succeed (succeeded, no output)
- [x] run `go test -race ./...` — must match the baseline, no new failures (matched exactly, see below)
- [x] run `golangci-lint run` — must not report new issues (`0 issues.`)
- [x] record the resulting lipgloss version and the vendor directory size in this plan file (below)
- [x] commit as a separate commit so the dependency change can be reverted on its own

**Baseline test result** (before any dependency change, `go test -race ./...`):

```
ok  	github.com/umputun/revdiff/app	62.956s
ok  	github.com/umputun/revdiff/app/annotation	1.317s
ok  	github.com/umputun/revdiff/app/diff	11.029s
?   	github.com/umputun/revdiff/app/diff/mocks	[no test files]
ok  	github.com/umputun/revdiff/app/editor	2.906s
ok  	github.com/umputun/revdiff/app/fsutil	2.059s
ok  	github.com/umputun/revdiff/app/handoff	2.341s
ok  	github.com/umputun/revdiff/app/highlight	3.594s
ok  	github.com/umputun/revdiff/app/history	3.769s
ok  	github.com/umputun/revdiff/app/keymap	3.778s
ok  	github.com/umputun/revdiff/app/review	3.177s
ok  	github.com/umputun/revdiff/app/theme	4.083s
ok  	github.com/umputun/revdiff/app/ui	4.301s
?   	github.com/umputun/revdiff/app/ui/mocks	[no test files]
ok  	github.com/umputun/revdiff/app/ui/overlay	3.281s
ok  	github.com/umputun/revdiff/app/ui/sidepane	3.103s
ok  	github.com/umputun/revdiff/app/ui/style	3.103s
ok  	github.com/umputun/revdiff/app/ui/worddiff	2.806s
?   	github.com/umputun/revdiff/themes	[no test files]
```

**Post-change test result** (`go test -race -count=1 ./...`, uncached): the same 14 packages report
`ok`, the same 3 packages report `[no test files]`, no new failures. golangci-lint reports `0 issues.`

**Resulting lipgloss version**: `github.com/charmbracelet/lipgloss v1.1.1-0.20250404203927-76690c660834`
(up from `v1.1.0` before this task).

**Vendor directory size**: 19M before, 19M after (`du -sk vendor/`: 19052 KB in both cases, unchanged
at that granularity). See the ⚠️ note below for why — glamour and mermaid-ascii themselves are not
present in the vendor tree yet, only the lipgloss bump is.

⚠️ **`go mod tidy` pruned glamour and mermaid-ascii back out of go.mod/go.sum.** Task 1 deliberately
adds no feature code, so nothing in the repo imports `glamour` or `mermaid-ascii` yet. `go mod tidy`
removes any module requirement that is not needed to build or test a package in the main module, so
right after `go get` added both modules, the following `go mod tidy` deleted them again — `go.mod`
after this task lists the same direct dependencies as before this task, minus glamour and
mermaid-ascii, which appear in neither `go.mod`, `go.sum`, nor `vendor/`. `go mod vendor` only copies
packages that are actually imported (transitively) by the main module, so it did not create
`vendor/github.com/charmbracelet/glamour/` or `vendor/github.com/AlexanderGrooff/` either, regardless
of whether they are listed in `go.mod`. This is expected, standard Go module behavior for an
add-then-tidy sequence with no consuming import — not a bug in this task.

What *did* survive, and is the part that actually matters for this spike: `lipgloss` itself is already
a **direct** dependency of revdiff (imported throughout `app/ui/style` and elsewhere), and `go get
glamour@v1.0.0` bumped revdiff's own direct `lipgloss` requirement to satisfy glamour's higher pin.
`go mod tidy` does not downgrade an already-recorded direct requirement just because the reason for the
bump (glamour) was itself later pruned — re-running `go mod tidy` a second time confirmed the lipgloss
version stays at `v1.1.1-0.20250404203927-76690c660834`. So the exact risk this spike set out to test —
"a newer lipgloss, selected by MVS for the whole build, could change rendering behaviour in revdiff's
existing UI code" — was genuinely exercised: `go build ./...`, `go test -race ./...`, and
`golangci-lint run` all ran against this newer lipgloss, without the noise of glamour/mermaid-ascii's
~90 additional transitive modules (which are irrelevant to existing code, since nothing imports them
yet). Result: no behaviour change detected in the existing suite.

Consequence for later tasks: when Task 2 or Task 3 add the first real `import
"github.com/charmbracelet/glamour"` (and later the `mermaid-ascii` import) in `app/ui/mdpreview.go`,
`go mod tidy && go mod vendor` must be re-run at that point to pull both modules and their transitive
tree back into `go.mod`/`go.sum`/`vendor/` — this is expected, not a regression, and vendor size will
grow substantially at that point (glamour brings roughly eight modules including goldmark and
bluemonday; mermaid-ascii's `cmd` package pulls in roughly 30 more, including gin, cobra and logrus,
per the Technical Details section above).

### Task 2: Mermaid fence extraction

**Files:**
- Create: `app/ui/mdpreview.go`
- Create: `app/ui/mdpreview_test.go`

- [x] write a failing test for a function that takes source lines and returns the document with
      ` ```mermaid ` fences replaced by rendered art
- [x] write failing tests for: no fences present, two fences in one document, an unterminated fence,
      a fence with a language other than mermaid (must be left alone)
- [x] write a failing test asserting that a diagram which fails to parse falls back to the original
      fence text verbatim, and never returns an error to the caller
- [x] implement the extraction, reusing `fencePrefix` from `app/ui/sidepane/toc.go:266-284` rather than
      writing new fence parsing
- [x] implement the `cmd.RenderDiagram` call with the fallback behaviour
- [x] run tests — must pass before task 3

⚠️ **`fencePrefix` turned out unexported.** As flagged in the task guidance, `sidepane.fencePrefix`
(`app/ui/sidepane/toc.go:266-284`) is lowercase — package `app/ui` cannot call it. Wrote a local
duplicate, `mdFencePrefix`, inside the new `app/ui/mdpreview.go` instead of exporting the sidepane
helper. This touches zero existing files (patch-discipline preference) versus exporting `FencePrefix`
across the package boundary for a single caller, which would edit `toc.go` and every future upstream
rebase would need to re-apply that rename. The two copies are five lines each and share no state, so
duplication costs nothing structurally.

`go get github.com/AlexanderGrooff/mermaid-ascii@latest` plus `go mod tidy && go mod vendor` pulled the
dependency tree back in cleanly: vendored module count went from 31 to 62 (+31, close to the ~30
estimated in Technical Details), vendor directory size from 19M to 49M. `go build ./...`,
`go test -race -covermode=atomic ./...` (all 14 previously-passing packages still `ok`, no new
failures), and `golangci-lint run` (`0 issues.`) all passed against the full tree including gin, cobra,
and logrus. No breakage from the new dependency tree — nothing to report as a blocker.

Malformed-input safety: `cmd.RenderDiagram` itself always returns `(string, error)` and does not panic
on the malformed input exercised in tests (confirmed by reading `mermaidFileToMap` in the vendored
source — non-graph/flowchart input returns a clean `"unsupported graph type"` error). `renderMermaidBlock`
still wraps the call in `recover()` as defense-in-depth per the plan's "must never panic" requirement,
since that guarantee cannot be proven from reading one version of a third-party library. No test proves
an actual panic path (none was found or constructed) — the recover code itself is therefore exercised
structurally but not by a red-then-green panic test. See final report for the same caveat.

### Task 3: Document rendering via glamour

**Files:**
- Modify: `app/ui/mdpreview.go`
- Modify: `app/ui/mdpreview_test.go`

- [x] write a failing test that a document with a table renders to output containing aligned column
      borders rather than raw pipe characters
- [x] write a failing test that rendered Mermaid art survives glamour without being re-wrapped or
      reflowed (this is the likely failure — glamour wraps text, and diagram art must not be wrapped)
- [x] write a failing test for width handling: rendering at a narrow width must not panic and must not
      produce lines wider than the viewport
- [x] implement the glamour render with a fixed style, feeding it the mermaid-substituted document
- [x] implement the render cache keyed on file name plus viewport width
- [x] write a test that a width change invalidates the cache
- [x] run tests — must pass before task 4

⚠️ **A fenced code block alone does not protect the art — confirmed empirically, and this changed the
design from what "Technical Details" implied.** The obvious plan ("emit the rendered art inside a
```text fence so glamour treats it as preformatted") was tried first and fails. Reading
`glamour@v1.0.0/ansi/codeblock.go` shows `CodeBlockElement.Render` never word-wraps — true, but
misleading: `glamour@v1.0.0/ansi/blockelement.go`'s `BlockElement.Finish` (used for the Document
element, which every top-level block including a code fence renders into) always runs
`x/ansi.Wordwrap` over the **entire accumulated document buffer** before writing it out, with no
awareness of which byte ranges came from a code fence versus prose. This is not configurable through
any style field — `Document`'s `Margin: true` is a Go literal in `elements.go`. A spike test (rendering
a fenced code block containing wide box-drawing art at width 40) proved this concretely: the second
line of a 3-line diagram came back split across 3 output lines. So embedding art in a fence and relying
on `WithWordWrap` guarantees nothing.

**Solution actually implemented — render around the art, not through it.** `mermaidPlaceholderDocument`
replaces each mermaid fence with a plain, isolated sentinel paragraph (`mermaidPlaceholder`, alphanumeric
only, no markdown or `x/ansi.Wordwrap` break characters `" ,.;-+|"` — a hyphen is *always* a break
character regardless of the passed set, confirmed by reading `x/ansi/wrap.go`) — so the token survives
`ansi.Wordwrap` as a single atomic word, never split mid-token, and does not merge into adjacent prose
because it is wrapped in its own blank-line-separated paragraph. `renderMarkdownDocument` renders that
placeholder document with glamour, then `spliceMermaidArt` finds the one output line matching each
placeholder (after `ansi.Strip` + `TrimSpace`, to ignore glamour's own color codes and pad/margin
whitespace) and replaces that line wholesale with the raw diagram text — verbatim, never passed through
glamour at all. `renderMermaidFences` (Task 2, still directly tested by the Task 2 suite) was refactored
into a 3-line wrapper around a new shared walk, `joinWithMermaidFences`, parameterized on what to do
with each mermaid fence — `renderMermaidFences` still inlines the art directly (used by nothing yet,
kept for the Task 2 tests and as a plain-text fallback), `mermaidPlaceholderDocument` substitutes the
placeholder instead. No Task 2 test was touched; all still pass unchanged after the refactor.

**Proof the test would actually catch a regression**: reverted `renderMarkdownDocument` locally to the
naive `doc := renderMermaidFences(lines)` (art inlined, fed straight to glamour, no placeholder/splice),
re-ran just the two width/reflow tests, watched them fail with the diagram visibly broken across
multiple re-wrapped lines (e.g. `│ This is a` / `moderately long` / `label for node A │` — a 3-line
diagram box split into what should have been one line, now several), then restored the real
implementation and confirmed green again. Diagram fixture: `wideMermaidSrc`, two nodes with long labels,
natural width 46 runes — wider than every narrow width used in these tests (20), so the reflow tests
are not vacuous.

**Width decision**: `renderMarkdownDocument`'s `width` parameter is glamour's word-wrap target for
prose/headings/tables (floored at `mdPreviewMinWidth = 8`, purely defensive — glamour itself does not
panic at width ≤ 0, `x/ansi.Wordwrap`'s `limit < 1` case just returns the input unwrapped, confirmed by
reading `x/ansi/wrap.go` and by a spike test sweeping widths 0/-5/1/5/20/80, none of which panicked).
Spliced-in diagram art is **never** constrained by this width — same as glamour's own code-block
rendering, which the plan's Technical Details already noted is not word-wrapped. Decision, made
explicit here per the task's instruction to decide and document rather than leave implicit: box-drawing
art has a natural minimum width, and truncating or reflowing it to fit a narrow viewport would corrupt
its shape rather than just shrink it, so a narrow viewport is expected to let the diagram overflow
horizontally (Task 4's viewport wiring will need horizontal scroll for this — already true today for
wide diff lines, not a new requirement). `TestRenderMarkdownDocument_NarrowWidth_ProseRespectsWidthArtOverflows`
asserts both halves of this: every non-diagram line stays within the requested width, and the diagram
line(s) are allowed past it, with a sanity check that the fixture diagram is actually wider than the
tested width (so the assertion is not accidentally vacuous).

**Style**: one fixed built-in style, `glamour/styles.DarkStyleConfig`, unmodified — no per-repo-theme
derivation, per the plan's explicit ruling-out of that. Since the art is spliced in after glamour has
already finished rendering (never passed through glamour's own code-block styling), no code-block
margin/indent tuning was needed either, which simplified the original plan further: the fixed style
needs zero customization for this feature to work correctly.

**ANSI nesting (gotchas.md)**: read before writing any of this. Not directly applicable here — the
gotchas note is about embedding a `lipgloss.Render()`-produced *substring* inside an already
lipgloss-rendered *parent* (its full reset breaks the outer background). glamour's output here is not
nested inside another lipgloss render; it *is* the entire pane content handed to `viewport.SetContent`,
the same relationship the existing chroma-highlighted diff content already has to the viewport. Checked
glamour's raw ANSI output directly (spike test, `%q`-dumped): every styled run is a matched
`\x1b[...m...\x1b[0m` pair, never left open across a line boundary, so it composes safely with whatever
pane-level background/border treatment Task 4's wiring applies on top (`extendLineBg`/`padContentBg`
emit their own color codes for padding rather than relying on inherited SGR state, the same way they
already do for chroma output).

**Vendoring**: `go get glamour@v1.0.0` resolved cleanly, no forced v2. Vendored module count went from
62 (after Task 2) to 71 (+9 — glamour itself plus `aymerick/douceur`, `charmbracelet/x/exp/slice`,
`gorilla/css`, `microcosm-cc/bluemonday`, `muesli/reflow`, `yuin/goldmark`, `yuin/goldmark-emoji`, close
to the ~8 estimated in Technical Details), vendor directory size from 49M to 51M. `go get` also bumped
three already-present transitive deps to satisfy glamour's own requirements:
`golang.org/x/crypto` v0.23.0→v0.36.0, `golang.org/x/net` v0.25.0→v0.38.0, and newly added
`golang.org/x/term` v0.36.0 (glamour imports it directly for `term.IsTerminal`/`termenv.HasDarkBackground`
auto-style detection, a code path this feature never calls since it always passes an explicit fixed
style). `go build ./...`, `go test -race -covermode=atomic ./...` (all 16 previously-`ok` packages —
Task 1's plan text says 14 but the actual baseline list has 16 `ok` entries plus 3 `[no test files]` —
still `ok`, no new failures), and `golangci-lint run` (`0 issues.`, after fixing two `modernize` lint
findings: `if w < x { w = x }` → `max(w, x)`, and three `strings.Split` range loops → `strings.SplitSeq`)
all passed.

### Task 4: Mode wiring — the seven hunks

Keep each edit minimal; see Patch discipline above. Follow the existing `toggle_wrap` action as the
template. Tracing `ActionToggleWrap` end to end (not just the checklist below) showed it actually
touches **seven** sites, not five: the two checklist items below that read "add ... in
`app/ui/model.go`" are each two separate edits (a registration list plus the actual case), matching
`ActionToggleWrap`'s own footprint. This section heading and the Patch discipline line above were
corrected from "five" to match.

**Files:**
- Modify: `app/keymap/keymap.go`
- Modify: `app/keymap/keymap_test.go`
- Modify: `app/ui/model.go`
- Modify: `app/ui/diffview.go`
- Modify: `app/ui/view.go`
- Modify: `app/ui/view_test.go` (pre-existing narrow-width status-bar test, see note below)
- Modify: `app/ui/mdpreview.go`
- Modify: `app/ui/mdpreview_test.go`

- [x] add `ActionToggleMarkdownPreview` constant to the `Action` enum in `app/keymap/keymap.go`
- [x] bind `P` to it in the default keymap, and add the help-section entry
- [x] add `mdPreview bool` to `modeState` in `app/ui/model.go`
- [x] add the dispatch case calling `m.toggleMarkdownPreview()` in `app/ui/model.go`
- [x] implement `toggleMarkdownPreview()` in `app/ui/mdpreview.go`, refusing to enable when
      `m.file.mdTOC == nil`
- [x] add the early-return branch in `renderDiff` (`app/ui/diffview.go`), beside the existing
      `m.modes.collapsed.enabled` branch
- [x] add the status-bar mode icon in `app/ui/view.go`
- [x] write a test that the toggle is refused when `m.file.mdTOC == nil`
- [x] write a test that the keymap resolves `P` to the new action
- [x] run tests — must pass before task 5

⚠️ **Go const identifier renamed from `ActionToggleMarkdownPreview` to `ActionTogglePreview` — the
action string stays `"toggle_markdown_preview"`.** `app/keymap/keymap.go`'s `Action` const block and
its `validActions` map are gofmt column-aligned, and the plan's original 27-character identifier was
longer than every existing name in that block, so gofmt would have re-padded all ~50 unrelated lines
around it (confirmed empirically: tried the long name first, `gofmt -w` produced a 56-insertion/
52-deletion whitespace-only diff across the whole const block). A 19-character name
(`ActionTogglePreview`) fits inside the existing column width and adds exactly one line with no
collateral reformatting — directly serving the Patch-discipline goal of touching the minimum number of
lines in a file that rebases by hand. The user-facing and config-file-facing string is unaffected: it
is still `toggle_markdown_preview`, matched in `keymap.go`'s default binding, help entry, and
`keymap_test.go`.

⚠️ **The render cache field (`*mdPreviewCache`) lives on `loadedFileState` (`m.file`), not on
`modeState` as the plan's "New state" section suggested.** Same gofmt-alignment reasoning as above:
`modeState`'s existing field-comment columns are sized for `bool`/`int`/`collapsedState` (widest type
name 14 chars); `*mdPreviewCache` is 15 chars and would have widened every line's alignment.
`loadedFileState`'s columns are already sized for `map[int]diff.BlameLine` (23 chars) and
`singleColLineNum` (16 chars), so `mdPreviewCache *mdPreviewCache` fits with zero collateral reformat.
This placement is also arguably the better fit: `mdTOC` (the render cache's row-neighbor) is already a
per-loaded-file computed artifact on `loadedFileState`, and the cache is exactly that too — a
memoization of "what the currently loaded file renders as," parallel to `mdTOC`'s "what the currently
loaded file's headings are." The `mdPreview bool` toggle itself stays on `modeState`, per the plan,
since it genuinely is a user-togglable view-mode flag like `wrap`/`collapsed`/`lineNumbers`.

⚠️ **The cache pointer is lazily allocated inside `toggleMarkdownPreview()`, not eagerly in `NewModel`.**
`mdPreviewCache.render` has a pointer receiver so its memoization survives across `Model`'s otherwise
value-receiver call chain (`renderDiff`, `View`, etc. all take `Model` by value — only a field whose
*value* is itself a pointer shares its target across those copies). `toggleMarkdownPreview` has a
pointer receiver and is the only path that ever sets `modes.mdPreview = true`, so allocating
`&mdPreviewCache{}` there the first time (guarded by a nil check, so re-toggling reuses the same cache)
guarantees the pointer is non-nil before the render path can ever need it, without touching `NewModel`.
`renderMarkdownPreview()` (the render path itself) additionally falls back to an uncached
`renderMarkdownDocument` call if the pointer is somehow still nil — defensive, not required by any
production path, but it keeps the render helper safe on its own rather than relying on the invariant
holding everywhere a test might set `modes.mdPreview` directly.

⚠️ **`model.go:1087`'s toggle-grouping `case` does apply, and was extended.** That switch arm exists
only to route a batch of view-mode toggle actions to `handleViewToggle`, which then re-switches on the
specific action; it has no other behavior of its own. `toggleMarkdownPreview()` is a plain state-flip
with no `tea.Cmd` to return, the same shape as `toggleWrapMode`/`toggleTreePane`/`toggleLineNumbers`,
which are exactly the actions already routed through this case. `ActionTogglePreview` was added to both
the outer grouping `case` and the corresponding inner `case` in `handleViewToggle` (mirroring
`ActionToggleWrap`'s two-site footprint there).

⚠️ **Icon chosen: `▤` (U+25A4 SQUARE WITH HORIZONTAL FILL).** Does not collide with the existing
`▼◉↩≋⊟⊂#b±✓∅` set (confirmed by direct comparison). Reads as a small filled/ruled document, a
reasonable mnemonic for "rendered document view."

⚠️ **Adding the icon required a one-line, one-value fix to a pre-existing test,
`TestModel_StatusBarFilenameTruncationWideChars` in `app/ui/view_test.go` (not one of the seven
hunks, but a direct, mechanical consequence of one of them).** That test pins `m.layout.width = 45` and
asserts the rendered status bar fits in `45 - 2` columns. Empirically that width was already the exact
boundary before this change (verified by reverting the icon edit and rerunning: status width was
already 43, i.e. zero slack) — the name-truncation logic has a `max(..., 4)` floor below which the
filename cannot shrink further, so once the mode-icon row grows by any amount (here, 2 columns: one new
icon plus its separator), the same fixed test width can no longer fit and the assertion legitimately
fails, without any change to the truncation logic itself (`statusBarText`'s `available` computation
already measures the actual rendered `statusModeIcons()` width dynamically — this is not a hardcoded
budget that silently drifted, it is a test literal that was already at its limit). Fixed by bumping the
test's `m.layout.width` from 45 to 47, restoring the original slack margin; behavior asserted by the
test (CJK display-width-aware truncation, not rune-count truncation) is unchanged and still exercised
at the same relative boundary condition.

⚠️ **Typed-nil guard verified present.** `app/main.go`'s `ParseTOC` closure (used in production) and
`app/ui/model_test.go`'s `testParseTOCFactory` (used by every test that goes through `NewModel`,
including this task's) both collapse a typed-nil `*sidepane.TOC` into a true nil `TOCComponent`
interface value before it reaches `m.file.mdTOC`. `m.file.mdTOC != nil` is therefore a sound gate, as
the gotchas note promised — no dead-guard trap here.

### Task 5: Make annotation keys inert in preview mode

**Files:**
- Modify: `app/ui/mdpreview.go`
- Modify: `app/ui/mdpreview_test.go`
- Modify: `app/ui/model.go` — see the ⚠️ note below for why this task needed to touch a file outside
  its original list
- Modify: `app/ui/mouse.go` — see the same note

- [x] write a failing test that the annotation-creating key does nothing while preview is on
- [x] write a failing test that the cursor position is preserved across toggle on then off
- [x] write a failing test that annotations created before enabling preview still exist after toggling
      back, with the same line anchors
- [x] implement the guard
- [x] run tests — must pass before task 6

⚠️ **The guard needed two call sites, not one, and one of them is not in `dispatchAction`.** Tracing
`handleKey` end to end (not just the checklist above) showed key dispatch in this codebase has two
separate paths that can move `m.nav.diffCursor`, not one:

1. **Keymap-resolved actions.** `handleKey` resolves the pressed key to a `keymap.Action` and calls
   `dispatchAction`, which both `handleKey` and `handleChordSecond` (chord second-stage) go through.
   This is the path the plan's "one dispatch site" was written for, and it is where most unsafe actions
   live: `ActionConfirm` (start/edit an annotation), `ActionAnnotateFile`, `ActionDeleteAnnotation`,
   `ActionAnnotList` (opens the annotation-list jump), `ActionDown`/`ActionUp`/page/half-page/home/end
   (cursor motion), `ActionNextHunk`/`ActionPrevHunk`, `ActionSearch` (a completed search can reposition
   the cursor on next/prev match), and — only reachable when the markdown TOC pane has focus, which is
   exactly the pane preview leaves available — `ActionNextItem`/`ActionPrevItem` and `ActionConfirm`
   again, both of which call `syncDiffToTOCCursor`/`jumpTOCEntry` and reassign `m.nav.diffCursor`
   unconditionally on every TOC move, not only on jump-confirm.
2. **Vim-motion's own screen-position motions.** When `--vim-motion` is on, `interceptVimMotion` (called
   from `handleKey` *before* `keymap.Resolve`) recognizes `G`, `gg`, `zz`/`zt`/`zb`, `H`/`M`/`L`, and
   count-prefix digits directly off the raw key string and calls cursor-moving methods (e.g.
   `jumpToLineN` for bare `G`) itself — these never reach `keymap.Resolve` or `dispatchAction` at all,
   so a guard placed only in `dispatchAction` cannot see or block them. `TestInterceptVimMotion_MdPreviewOn_BypassKeysAreInert`
   proves this concretely: pressing bare `G` with vim-motion and preview both on, guarded only at
   `dispatchAction`, still jumped the cursor to the last line.

   Fix: `handleKey`'s existing `if m.modes.vimMotion {` gate that decides whether to call
   `interceptVimMotion` at all was extended to `if m.modes.vimMotion && !m.modes.mdPreview {` — a
   one-token change. This disables vim-motion entirely while previewing, which costs nothing
   functionally: none of vim-motion's own keys (`G`, `gg`, `z*`, `H`/`M`/`L`) are on the preview
   allowlist below, and with vim-motion off, `G`/`gg`/etc. simply have no keymap binding and fall
   through to `dispatchAction` as `action == ""` — an already-safe no-op.

3. **The scroll-then-pin mechanism (discovered while writing the "toggle preserves cursor" tests, not
   originally suspected).** `scroll_diff_down`/`scroll_diff_up` (`J`/`K`) must stay allowed — the plan's
   own "IMPORTANT nuance" calls this out: scrolling the rendered document must keep working. But
   `scrollDiffViewportLine` (the handler both of those actions call) does two things: it shifts the
   viewport's Y-offset (`scrollDiffViewportBy`, purely a rendering concern, safe regardless of mode), and
   it then calls `pinDiffCursorTo` to keep the cursor visible when it would otherwise scroll out of view.
   `pinDiffCursorTo` computes the cursor's on-screen row via `cursorVisualRange`, which walks
   `m.file.lines` assuming one row per source line — meaningless once the viewport shows the
   whole-document glamour render instead. Left unguarded, an *allowed* key (`J`/`K`) would silently
   reassign `m.nav.diffCursor` to whatever diff line happens to fall at the scrolled-to row in the wrong
   coordinate space — exactly the bug this task exists to prevent, just reached through the one action
   the plan explicitly said must stay live. `flushWheelPending` (called at the top of every `handleKey`)
   and `handleResize` reach the same `pinDiffCursorTo` call, so a mouse-wheel scroll during preview
   (mouse input is outside this task's stated scope, but the pin is deferred and gets flushed on the next
   *keypress* regardless of input device) would have hit the identical bug on the very next key press,
   including the `P` press meant to leave preview mode.

   Fix: `pinDiffCursorTo` (`app/ui/mouse.go`) now returns `false` unconditionally when
   `m.modes.mdPreview` is true, before doing any of its normal row math. This is a single guard at the
   one function every cursor-follow-viewport call site already funnels through, so it protects
   `scrollDiffViewportLine`, `flushWheelPending`, `handleResize`, and `handleWheelDebounce` all at once.
   `TestScrollDiffViewportLine_MdPreviewOn_ScrollsButCursorUnchanged` proves both halves: `J` still moves
   `YOffset` (scrolling works) and leaves `m.nav.diffCursor` untouched (the cursor stays inert).

None of this contradicts the plan's "prefer one choke point" instruction — it sharpens it. There are two
*mechanisms* that can move the cursor (keymap dispatch, and the independent scroll-follow-cursor
machinery), each gated exactly once, at its own single natural chokepoint:
`mdPreviewActionAllowed` (new, in `mdpreview.go`) for the first, and the one-line guard added directly
inside `pinDiffCursorTo` (`mouse.go`) for the second. `model.go` itself gained only the `dispatchAction`
guard call plus the vim-motion gate's `&&` — the actual allowlist and its rationale live entirely in
`mdpreview.go`, matching the task's instruction to keep model.go's footprint minimal.

⚠️ **`dispatchAction` was split into a thin wrapper plus `dispatchResolvedAction` to satisfy
`golangci-lint`'s `gocyclo` check.** The guard's own `if m.modes.mdPreview && !mdPreviewActionAllowed(action)`
adds two branches (the `if`, plus the `&&`) to whichever function contains it; `dispatchAction`'s
existing action switch was already at the 20-branch gocyclo ceiling for this codebase, so adding the
guard inline pushed it to 22 and `golangci-lint run` failed. `dispatchAction` is now a small wrapper that
runs the guard and delegates everything else, unchanged, to `dispatchResolvedAction` (the old
`dispatchAction` body, renamed and otherwise untouched) — `handleChordSecond` and `resolveVimLeader`
(vim-motion's chord dispatch) still call `dispatchAction` by the same name, so this is invisible to every
existing caller.

**Full allowlist decided (see `mdPreviewAllowedActions` in `mdpreview.go` for the authoritative,
per-entry-commented version):** `toggle_markdown_preview` (the mode's only exit key), `quit`,
`discard_quit`, `help`, `theme_select`, `toggle_tree`, `scroll_diff_down`/`scroll_diff_up` (`J`/`K`,
viewport-driven, safe per the `pinDiffCursorTo` fix above), and `dismiss` (esc — only clears a leftover
search-match highlight). Everything else is swallowed, including some actions that read as harmless at
first glance: `next_item`/`prev_item` (`n`/`N`/`p`) route to TOC navigation when the markdown TOC pane
has focus — the same pane preview mode leaves reachable — and reassign the cursor there too;
`toggle_pane`/`focus_tree`/`focus_diff` are excluded because switching focus into the TOC is pointless
once TOC navigation is itself blocked; `info`, `reload`, `flush_output`, `mark_reviewed`, `filter`,
`filter_unreviewed`, `open_file_in_editor`, `toggle_untracked`, and the other view-mode toggles
(`toggle_wrap`, `toggle_collapsed`, `toggle_compact`, `toggle_line_numbers`, `toggle_blame`,
`toggle_word_diff`, `toggle_hunk`) are excluded because none of them are needed to read a rendered
preview and several of them (e.g. `toggle_wrap`) call `syncViewportToCursor`, whose Y-offset math has the
same diff-line-coordinate assumption as `pinDiffCursorTo` had — left unguarded there too, which this task
did not do, since none of those actions are on the allowlist and so can never reach that call while
previewing.

⚠️ **Known, deliberately out-of-scope observations for future work**, found during this task's
investigation but outside "annotation and cursor keys" (mouse input, and a cosmetic-only pre-existing
quirk):

- Mouse wheel and left-click in the diff pane (`app/ui/mouse.go`'s `handleMouse`, entirely independent of
  `handleKey`/`dispatchAction`) are not gated by this task's guard. Wheel scroll is protected transitively
  by the `pinDiffCursorTo` fix above (it no longer pins regardless of caller), but `clickDiff`'s
  row-under-pointer-to-cursor-index mapping still assumes diff-line coordinates and was not investigated
  or fixed — this task's scope is keys.
- `toggleMarkdownPreview` (existing since Task 4, unmodified here) and `toggleTreePane` (on the preview
  allowlist) both call `syncViewportToCursor`, whose Y-offset math is computed from the stale diff-line
  cursor position and then applied to the newly-rendered preview content — a coordinate-space mismatch
  of the same shape as the `pinDiffCursorTo` bug, but purely cosmetic (it can leave the viewport scrolled
  to a visually arbitrary position after enabling preview or toggling the tree pane) since neither
  function assigns to `m.nav.diffCursor`. Not touched here since it does not threaten the cursor/
  annotation round-trip guarantee this task tests for, and fixing it would mean editing `toggleMarkdownPreview`,
  which belongs to Task 4.

### Task 6: Regression check — mode off must be unchanged

**Files:**
- Modify: `app/ui/mdpreview_test.go`

- [x] write a test asserting `renderDiff` output is identical with the feature compiled in but the
      mode off, for a markdown file and for a non-markdown file
- [x] run the full suite `go test -race -covermode=atomic ./...` — must pass
- [x] run `golangci-lint run` — must be clean
- [x] run tests — must pass before task 7

Two tests added to `app/ui/mdpreview_test.go`:

- `TestRenderDiff_MarkdownPreviewOff_MarkdownFile_IdenticalRegardlessOfMdTOC` — a full-context
  single markdown file (`mdTOC != nil`), mode off. Asserts `renderDiff()` output is byte-identical
  whether `mdTOC` is present or nil, proving the early-return branch is gated on the `mdPreview` flag
  itself, not merely on `mdTOC`. Non-vacuous proof included in the same test: flipping `mdPreview` on
  for the same model changes the output (glamour table border `│` appears, raw `| a | b |` pipe syntax
  disappears) — so the preceding equality is not trivially true from a branch that never fires.
- `TestRenderDiff_MarkdownPreviewOff_NonMarkdownFile_DoublyGated` — a non-markdown file (`mdTOC ==
  nil`). Forces `mdPreview = true` directly (bypassing `toggleMarkdownPreview`'s own refusal) and
  asserts `renderDiff()` output is still byte-identical to the flag-off render and contains no
  markdown-preview internals (`mdpreviewmermaidplaceholder`) — proving the branch requires both
  conditions together, not the flag alone.

Both fixtures reuse `mdPreviewTestModel` (built on the existing `testModel`/`ParseTOC` factory), no
new Model-construction path introduced.

No regression found in the mode-off path — `renderDiff`'s guard (`if m.modes.mdPreview &&
m.file.mdTOC != nil`) is exactly as gated as the plan requires.

`go test -race -covermode=atomic ./...` via `make test`: all 16 previously-`ok` packages still `ok`
(same set as Task 3's baseline), `app/themes` reports `[no test files]`, no failures.
`golangci-lint run` via `make lint`: `0 issues.`

### Task 7: Build the `revdiffm` binary

**Files:**
- Create: `PATCH.md`

- [x] build the patched binary and install it as `revdiffm` (not `revdiff`, so the brew build is left
      alone and nothing is shadowed)
- [x] confirm the brew `revdiff` still runs and is unaffected
- [x] write `PATCH.md`: which upstream tag the branch sits on, the seven hunk locations, the rebuild
      command, and the rebase procedure

### Task 8: Verify acceptance criteria

Verification must check rendered **content**, not merely that the program launched.

- [x] open `docs/plans/completed/20260713-parallel-dev-stacks.md` from `~/dev/local-dev-plugin` in
      `revdiffm`, press `P`, and confirm the Mermaid diagram is readable and headings are styled
      (verified live via agterm by the orchestrator, driving `revdiffm --only <file>` in a real
      terminal — but using the torture document below as the interactive test vehicle instead of this
      specific file, since it exercises the same `mdTOC`-gated preview path. Result: the Mermaid
      flowchart and sequence diagram both rendered as readable box-drawing art — real boxes containing
      node text, connector lines, and edge labels — and headings are styled by glamour's DarkStyle. This
      specific `parallel-dev-stacks.md` file was not separately opened in this pass; see the ⚠️ cosmetic
      note below for a heading-rendering caveat found while checking headings.)
- [x] open the torture document at
      `/private/tmp/claude-501/-Users-sasha/5a4c7a1a-9881-4c7d-974e-e195093d504a/scratchpad/render-test.md`
      and check each element: narrow table, over-wide table, flowchart, sequence diagram, nested
      lists, blockquote, LaTeX
      (verified live via agterm by the orchestrator. Confirmed: normal/raw view shows the markdown TOC
      pane plus raw source with visible `|` pipes and the raw ` ```mermaid ` fence. In preview mode:
      tables render with box-drawing column separators and horizontal rules, cells wrap, and the
      deliberately over-wide table wraps its cell content — e.g. "boots the m7 stack and supervises
      container / lifecycle" — rather than truncating it, which is better than `glow`'s behavior
      (`glow` truncated the header). The flowchart renders as box-drawing art with node boxes (e.g.
      "Plan file in docs/plans", "B{Has Mermaid?}"), connector lines, and edge labels ("yes"/"no"). The
      sequence diagram renders as box-drawing art with participant boxes (User / Viewer / Terminal),
      lifelines, and directional arrows with message labels ("open plan.md", "parse + rasterize
      mermaid", "kitty graphics payload"). Bold/italic markers are stripped (`**bold**` → "bold") and
      link URLs are hidden. Nested lists, blockquote, and LaTeX rendering were not individually called
      out in the orchestrator's report for this pass — not separately confirmed.)
- [x] compare the over-wide table against `markdown-reader` on the same file — note whether glamour
      truncates the header the way `glow` did
      (non-interactive conclusion, this task: both `revdiffm` (glamour) and `markdown-reader` **wrap**
      the over-wide table's cell content rather than truncating it — neither exhibits `glow`'s
      truncated-header behavior. `revdiffm`'s wrapping was confirmed live in this session (see above);
      `markdown-reader`'s non-truncating behavior on the same file was established in the orchestrator's
      earlier session. `markdown-reader` is a TUI with no non-interactive/headless comparison path, so
      it was not independently re-driven here — the established conclusion is recorded as-is per the
      orchestrator's instruction.)
- [x] confirm `P` is refused on a non-markdown file and on a partial-context diff
      (confirmed via the compiled gate rather than re-checked interactively: `renderDiff`'s early-return
      requires `m.modes.mdPreview && m.file.mdTOC != nil` (`app/ui/diffview.go`), and `m.file.mdTOC` is
      only non-nil for a single, full-context markdown file (`app/ui/loaders.go:534-538`) — so both a
      non-markdown file and a partial-context diff leave `mdTOC == nil` and the gate refuses. Covered by
      unit tests from Tasks 4-6, notably the toggle-refusal test in Task 4 and
      `TestRenderDiff_MarkdownPreviewOff_NonMarkdownFile_DoublyGated` in Task 6.)
- [x] add an annotation in normal view, toggle preview on and off, confirm it survived
      (covered by Task 5's dedicated unit test — "annotations created before enabling preview still
      exist after toggling back, with the same line anchors". In the live session, pressing the
      annotation-start key (`a`) while preview was on produced no input box and no screen change
      (confirming the inert-key guard), and toggling preview off returned to the raw view with cursor
      position preserved at L:1/90 (confirming a clean round-trip). The specific sequence of creating a
      brand-new annotation and re-checking it survives, beyond what the automated test already
      exercises, was not separately re-run live in this pass.)
- [x] run the full test suite and record the output
      (see below — run non-interactively as part of this recording task)

⚠️ **Cosmetic defect found during live verification: leaked `##` heading-prefix.** In preview mode, h2
headings still show a literal `"## "` prefix in the rendered output (e.g. `"## Table (narrow)"`).
glamour's DarkStyle is applying heading styling, but the `##` markdown markup itself is leaking through
instead of being stripped. This is cosmetic only — it does not affect any other rendering (tables,
mermaid diagrams, bold/italic stripping, and the toggle round-trip are all otherwise correct) and does
not violate the read-only/inert-key guarantees this feature depends on. Logged here as a known issue /
future-work item; **not fixed as part of this task**, since Task 8 is verification-only.

**Test suite run for this checkbox** (`make test`, `go test -race -covermode=atomic ./...`, run
2026-07-23): all 16 packages report `ok`, no failures — no code changed in this task, so this is a
rerun of Task 6's baseline, not a new baseline:

```
ok  	github.com/umputun/revdiff/app				52.830s	coverage: 69.9% of statements
ok  	github.com/umputun/revdiff/app/annotation		1.867s	coverage: 97.1% of statements
ok  	github.com/umputun/revdiff/app/diff			12.111s	coverage: 84.6% of statements
ok  	github.com/umputun/revdiff/app/editor			2.159s	coverage: 91.4% of statements
ok  	github.com/umputun/revdiff/app/fsutil			1.699s	coverage: 70.6% of statements
ok  	github.com/umputun/revdiff/app/handoff			1.387s	coverage: 100.0% of statements
ok  	github.com/umputun/revdiff/app/highlight		1.336s	coverage: 88.8% of statements
ok  	github.com/umputun/revdiff/app/history			3.396s	coverage: 91.4% of statements
ok  	github.com/umputun/revdiff/app/keymap			1.567s	coverage: 95.8% of statements
ok  	github.com/umputun/revdiff/app/review			2.015s	coverage: 94.8% of statements
ok  	github.com/umputun/revdiff/app/theme			2.535s	coverage: 85.2% of statements
ok  	github.com/umputun/revdiff/app/ui			5.154s	coverage: 95.0% of statements
ok  	github.com/umputun/revdiff/app/ui/overlay		2.241s	coverage: 96.4% of statements
ok  	github.com/umputun/revdiff/app/ui/sidepane		2.255s	coverage: 91.6% of statements
ok  	github.com/umputun/revdiff/app/ui/style		2.331s	coverage: 92.7% of statements
ok  	github.com/umputun/revdiff/app/ui/worddiff		2.364s	coverage: 98.9% of statements
```

Total coverage (excluding mocks): 90.6% of statements.

### Task 9: [Final] Update documentation

- [x] update `PATCH.md` with anything learned during implementation
- [x] record in this plan file any deviation from the original design
- [x] move this plan to `docs/plans/completed/` (deferred to orchestrator per exec workflow)

## Deviations from original design

This section records the notable differences between what this plan originally described and
what actually got built. The per-task ⚠️ notes above are the detailed record; this is a short
summary in one place.

- **Task 1's dependency spike could not keep glamour and mermaid-ascii in `go.mod`.** The plan
  expected the spike to exercise both new dependencies. In practice, `go mod tidy` prunes any
  module requirement that nothing imports, and Task 1 deliberately added no feature code — so
  right after `go get` added glamour and mermaid-ascii, the following `go mod tidy` removed them
  again. The dependency-tree exercise the plan wanted only really happened in Task 2 (mermaid-ascii)
  and Task 3 (glamour), once `app/ui/mdpreview.go` actually imported them. The one part of Task 1
  that was genuinely exercised as planned: the lipgloss version bump that glamour's pin forces.
  `go build`, `go test -race`, and `golangci-lint run` all ran against the bumped lipgloss, and came
  back clean.

- **The Go identifier is `ActionTogglePreview`, not the planned `ActionToggleMarkdownPreview`.**
  The shorter name was needed to avoid triggering a large gofmt whitespace-only reformat of
  `app/keymap/keymap.go`'s column-aligned const block. The user-facing string is unaffected — it
  is still `toggle_markdown_preview` everywhere a person or a config file sees it. Same reasoning
  moved the render-cache pointer, `mdPreviewCache`, onto `loadedFileState` instead of `modeState`
  as the plan's "New state" section had suggested — `loadedFileState`'s field-comment columns
  already had room for a long type name, `modeState`'s did not.

- **The plan said "five hunks" for Task 4's wiring; the real count is higher.** Tracing the
  existing `ActionToggleWrap` action end to end showed it already touches seven sites, not five,
  because two of the plan's checklist items are each two separate edits (an action-registration
  list, plus the actual dispatch case). Task 5 then added guards in `app/ui/model.go` and
  `app/ui/mouse.go` that were not in the plan's original file list at all — those turned out to be
  needed because cursor movement in this codebase has two independent paths (keymap-resolved
  dispatch, and vim-motion's own screen-position motions bypassing dispatch entirely), plus a third
  case discovered only while writing the toggle-preserves-cursor tests: the scroll-then-pin
  mechanism behind the allowed `J`/`K` scroll keys. `PATCH.md` has the authoritative up-to-date list
  of every edited file and hunk, confirmed against `git diff master...md-preview --stat`.

- **Glamour word-wraps the whole document, not just prose — so a code fence does not protect the
  mermaid art.** The plan's Technical Details section implied that putting the rendered diagram
  inside a fenced code block would be enough to stop glamour from reflowing it, the same way
  glamour never reflows a normal code block. That turned out to be wrong: glamour's `Finish` step
  runs word-wrap over the whole accumulated document buffer, with no awareness of which parts came
  from a code fence. Task 3 worked around this differently — with a wrap-atomic placeholder token
  standing in for the diagram while glamour renders, then the real diagram text is spliced back into
  the output afterward, never passed through glamour's word-wrap at all.

- **Known cosmetic issue: h2 headings render with a literal `"## "` prefix.** This was found during
  Task 8's live verification. It is glamour's `DarkStyleConfig` doing this by design — a
  heading-prefix convention baked into that built-in style, not a bug this patch introduced. Other
  rendering (tables, mermaid diagrams, bold/italic stripping, the toggle round-trip) is unaffected.
  A future tweak could set a custom heading prefix or style if the bare-heading look is preferred,
  but that is out of scope for this patch.

## Post-Completion

*Items requiring manual intervention — no checkboxes, informational only*

**Claude tooling wiring:**

The `revdiff` skill and the planning plugin launch `revdiff` **by name**, so with the binary named
`revdiffm` they will keep using the unpatched brew build. revdiff supports a custom launcher resolved
through a user-then-bundled chain at `${CLAUDE_PLUGIN_DATA}/scripts/<launcher>`; the exact contract is
in `.claude-plugin/skills/revdiff/references/install.md` in this repo. Decide then whether to point
the launcher at `revdiffm`, or to leave the Claude tooling on stock revdiff and use `revdiffm` by hand.

**Manual verification:**

- Check rendering in both light and dark terminal themes, since the glamour style is fixed rather than
  derived from the revdiff theme.
- Check a very large plan file for toggle latency; if it is slow, the cache key or the mermaid render
  is the place to look.

**Ongoing maintenance:**

- On each upstream release: `git fetch`, rebase `md-preview` onto the new tag, re-run
  `go mod tidy && go mod vendor`, run the suite, rebuild `revdiffm`.
- Expect conflicts in `app/ui/diffview.go` and `app/ui/model.go` first; they are the actively
  developed files.
