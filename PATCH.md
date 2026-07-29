# PATCH.md — Markdown Preview Mode (local patch)

This is a personal patch on a local clone of `umputun/revdiff`, not an upstream contribution
(see `docs/plans/completed/20260722-markdown-preview-mode.md` for why). This file is the rebase
playbook: what the patch touches, and how to carry it onto a new upstream release.

Because the patch never goes upstream, it deliberately leaves every upstream-facing document
alone: `README.md`, `site/index.html`, `site/docs.html`, and the plugin reference docs under
`.claude-plugin/` and `plugins/` are all untouched. So the `P` key (`toggle_preview`) and the `▤`
status-bar icon appear in neither the README keybindings table, nor the status-icon table, nor
`site/docs.html`, nor any plugin `config.md`/`usage.md`. That is on purpose, not an oversight —
those files describe the released `revdiff` binary that users install from brew, which has no
preview mode. Editing them would put the fork's own features into documents shipped to people
running a build without them, and would add a conflict site to every future rebase. The `P`
binding is discoverable at runtime through the in-app help overlay (`?`) and `--dump-keys`,
both of which read the real keymap and so list it automatically in this build.

## Base

- Branch `md-preview` forks from `master` at commit `1b563f8` (2026-07-19), which is
  `v1.11.1` + 10 commits.
- Upstream tag `v1.11.1` is commit `39da604` (2026-07-13).
- Run `git merge-base md-preview master` and `git describe --tags <that-commit>` to reconfirm
  this after any rebase — the base moves every time the branch is rebased onto a new tag.

## New files added by the patch

- `app/ui/mdpreview.go` — almost all patch logic lives here by design (fence extraction,
  glamour rendering, the preview-mode action allowlist, `toggleMarkdownPreview`).
- `app/ui/mdpreview_test.go` — its tests.
- `app/ui/mdpreview_transpile.go` — the classDiagram/stateDiagram-v2 → flowchart transpiler
  (see `docs/plans/completed/20260728-markdown-preview-diagram-transpile.md`): diagram-kind
  dispatch, the shared `flowchartBuilder`, both per-type transpilers, and the adaptive
  label-width cap.
- `app/ui/mdpreview_transpile_test.go` — its tests.

A clean rebase never conflicts on these four files — they don't exist upstream. All conflict
risk is in the hunks below.

## Existing files edited, and where

Confirmed against `git diff master...md-preview --stat -- . ':!vendor'`. Line numbers are
current HEAD of `md-preview`; they will drift after every rebase — treat them as a map of
*what* to look for, not permanent coordinates.

**Task 4 wiring (the mode toggle itself):**

- `app/keymap/keymap.go`
  - line 54: `ActionTogglePreview` added to the `Action` const enum
  - line 92: `ActionTogglePreview: true` added to the `validActions` map
  - line 233: help-section entry in `defaultDescriptions()`
  - line 294: `"P": ActionTogglePreview` in `defaultBindings()`
- `app/ui/model.go`
  - `mdPreview bool` field on `modeState`
  - `markdownPreviewable bool` field on `loadedFileState` — the real gate for preview mode (a
    single, full-context markdown file). It exists because `mdTOC` is nil for a heading-less
    markdown file, so `mdTOC != nil` wrongly refused preview for valid heading-less docs.
  - `keymap.ActionTogglePreview` added to the toggle-grouping `case` that routes to
    `handleViewToggle`
  - `case keymap.ActionTogglePreview: m.toggleMarkdownPreview()` inside `handleViewToggle`
- `app/ui/loaders.go`
  - in `handleFileLoaded`, beside the existing `mdTOC` computation: sets
    `m.file.markdownPreviewable = m.file.singleFile && m.isMarkdownFile(msg.file) &&
    m.file.singleColLineNum` (the same three conditions that gate `mdTOC`, minus the
    has-headings requirement). This makes `loaders.go` a newly-touched file for the patch, so a
    rebase moving that block will surface a fresh conflict here.
- `app/ui/diffview.go`
  - line ~280: early-return branch in `renderDiff()` — `if m.modes.mdPreview &&
    m.file.markdownPreviewable { return m.renderMarkdownPreview() }`, placed beside the existing
    `m.modes.collapsed.enabled` branch
- `app/ui/view.go`
  - line 459: `{"▤", m.modes.mdPreview}` added to `statusModeIcons()`

That is 9 hunks across 4 files. The plan's Task 4 text counts by checklist item, not by literal
diff hunk (e.g. `keymap.go`'s enum + validActions edit is one checklist item but two hunks), so
its site count differs — use the itemized list above as the actual hunk map.

**Task 5 wiring (making annotation/cursor keys inert during preview — required additional
edits beyond Task 4's file list, see the plan's Task 5 section for why):**

- `app/ui/model.go`
  - line 1038: `if m.modes.vimMotion && !m.modes.mdPreview {` — one-token change to the guard
    that decides whether to run `interceptVimMotion`
  - line 1072: `dispatchAction` guard (`if m.modes.mdPreview && !mdPreviewActionAllowed(action)
    { return m, nil }`) — `dispatchAction` was split into a thin wrapper plus
    `dispatchResolvedAction` here to keep `gocyclo` under the lint ceiling; a rebase that
    touches the old single `dispatchAction` body must land inside `dispatchResolvedAction`
    instead
- `app/ui/mouse.go`
  - `if m.modes.mdPreview { return false }` at the top of `pinDiffCursorTo` (keeps an allowed
    diff-pane scroll — J/K or wheel — from pinning/mutating the cursor)
  - `if m.modes.mdPreview { return m, nil }` in `handleMouse`'s left-click branch (a click in
    either pane is read-only in preview — no `clickDiff`/`clickTree` cursor move)
  - `if m.modes.mdPreview { return m, nil }` in `handleWheel`'s `hitTree` branch (TOC wheel must
    not drive `syncDiffToTOCCursor`, matching the swallowed n/N/p keys)

**Review phase 4 wiring (stopping stale diff-cursor state from leaking into the display while
previewing — same root cause as Task 5, caught at three more sites):**

- `app/ui/diffnav.go` — a *new* file for the patch to touch, so a rebase that moves
  `syncViewportToCursor` will surface a fresh conflict here.
  - guard inside `syncViewportToCursor`, between the `SetContent(m.renderDiff())` call and the
    YOffset-repositioning switch: `if m.modes.mdPreview { m.layout.viewport.SetYOffset(
    m.layout.viewport.YOffset); return }`. Keeps the re-wrap (needed on resize / tree toggle)
    but preserves the user's scroll instead of snapping to the frozen diff cursor; the
    `SetYOffset(YOffset)` re-clamps to the new content so a shrink cannot strand the offset past
    the end (blank screen). Reached in preview via the allowed `toggle_tree` action, terminal
    resize, and a blame load landing mid-preview.
- `app/ui/view.go`
  - `hunkSegment()`: guard widened to `if m.layout.focus != paneDiff || m.modes.mdPreview`
  - `lineNumberSegment()`: same widening. Both otherwise print a fake live "hunk X/Y" / "L:N/M"
    off the frozen cursor while the glamour render scrolls freely. `statusSegmentsNoSearch` /
    `statusSegmentsMinimal` need no change — they call these two helpers, which now return `""`.
  - a comment on the `m.file.singleFile && m.file.mdTOC != nil` render branch documents the one
    display-staleness left unfixed: the TOC active-section highlight. It is pinned to the
    toggle-on section while previewing because `syncTOCActiveSection` runs off the frozen cursor.
    Left as a MINOR limitation on purpose — a clean fix needs a new `TOCRender` field in the
    upstream `app/ui/sidepane` package (which this fork keeps untouched), and dropping the TOC
    pane during preview would desync the diff viewport's width geometry. It is a dim highlight,
    not a numeric tracker, so lower stakes than the status-bar segments, which ARE fixed.

**Diagram transpile wiring (`classDiagram`/`stateDiagram-v2` rendering, added later — see
`docs/plans/completed/20260728-markdown-preview-diagram-transpile.md`):**

- `app/ui/mdpreview.go`
  - line 173: `renderMermaidBlock` gained a fourth parameter, `paneWidth int` — the diff pane's
    current width, needed by the transpiler's adaptive label-width cap
  - line 193: `renderMermaidBlock` now calls `renderMermaidSource(strings.Join(body, "\n"),
    paneWidth)` instead of `mermaidcmd.RenderDiagram(strings.Join(body, "\n"), nil)` directly —
    the one-line hook that gives `mdpreview_transpile.go` a chance to rewrite a `classDiagram` or
    `stateDiagram-v2` fence into `flowchart` source before it ever reaches the third-party
    renderer
  - line 272: `mermaidPlaceholderDocument` gained a matching `paneWidth int` parameter, passed
    straight through to `renderMermaidBlock` at line 275
  - `renderMermaidFences` (line 71) deliberately keeps its original one-argument signature: it
    passes the `mermaidUnconstrainedWidth` sentinel internally, so its eleven existing callers in
    `mdpreview_test.go` needed no change. It has **no production caller** — production always
    reaches the renderer through `renderMarkdownDocument` → `mermaidPlaceholderDocument`, which
    knows the real pane width. Treat it as a test-only entry point: anything that verifies
    width-dependent behavior must go through `renderMarkdownDocument` instead, or it will run at
    the unconstrained sentinel and never exercise the adaptive cap production always uses

  Both edited lines live inside `app/ui/mdpreview.go`, which is itself one of the patch's own new
  files (listed above) rather than an upstream one, so none of this carries rebase conflict risk
  against upstream. It is recorded here anyway so the call flow between `mdpreview.go` and
  `mdpreview_transpile.go` stays documented in one place, alongside every other hunk map entry.

**Test-only, mechanical, not part of the feature itself:**

- `app/keymap/keymap_test.go` — asserts `P` resolves to `ActionTogglePreview`
- `app/ui/view_test.go` — one pre-existing test (`TestModel_StatusBarFilenameTruncationWideChars`)
  had its pinned `m.layout.width` bumped from 45 to 47 because the new status icon consumed the
  test's last 2 columns of slack (see the plan's Task 4 ⚠️ note — not a bug, the test was
  already at its limit)

### Most likely rebase conflict sites

`app/ui/diffview.go` and `app/ui/model.go` are the most actively developed files upstream and
the most likely to conflict — this was flagged going in (see the plan's "Patch discipline"
section) and confirmed empirically: `model.go` alone carries 5 of the patch's 17 existing-file
hunks (keymap 4, model 5, diffview 1, mouse 3, view 3, diffnav 1).

## Mermaid diagram type coverage

The vendored `mermaid-ascii` renderer only understands `graph`, `flowchart`, and
`sequenceDiagram` natively. `mdpreview_transpile.go` widens that by rewriting two more types into
`flowchart` source before handoff:

- **Render as box art:** `graph`, `flowchart`, `sequenceDiagram` (native, unchanged path) plus
  `classDiagram` and `stateDiagram-v2`/`stateDiagram` (transpiled to `flowchart` first — see
  `transpileMermaid` in `mdpreview_transpile.go`).
- **Still fall back to the verbatim fence text:** `erDiagram`, `gantt`, `quadrantChart`, and any
  other/unrecognized fence language. Measured against a real corpus, these three types covered 45
  non-rendering fences before the transpiler; `erDiagram` never actually appeared in that corpus.
  This stays out of scope on purpose — see the diagram-transpile plan's Overview for the
  fence-count breakdown.

## Known limitations

These are accepted, documented gaps in the preview mode — not bugs to fix under patch discipline.

- **Wide mermaid diagrams are clipped in preview; widen the terminal to see them.** A diagram
  wider than the diff pane is cut off at the right edge and cannot be scrolled into view.
  The preview render (`renderMarkdownDocument` / `renderMarkdownPreview` in `app/ui/mdpreview.go`)
  produces one whole-document glamour render and hands it straight to the viewport. It does not
  flow through `applyHorizontalScroll`, which is a per-diff-line transform used by the normal and
  collapsed diff render paths (`renderDiffLine`, `renderCollapsedDiff`). So `scroll_left` /
  `scroll_right` have nothing to act on here, and they are deliberately left OUT of
  `mdPreviewAllowedActions`. Wiring real horizontal scroll in would mean applying ANSI-aware
  per-line slicing to the entire render (including the spliced-in box-drawing art) and reworking
  how the wide art interacts with the lipgloss pane width — a render-path change out of scope for
  this fork. The art is intentionally never re-wrapped or truncated to fit (see the anti-reflow
  design in `renderMarkdownDocument`'s doc comment), so the only current remedy for a clipped
  diagram is a wider terminal.
- **TOC active-section highlight is stale during preview** — see the `app/ui/view.go` note under
  "Review phase 4 wiring" above.
- **Most transpiled classDiagram/stateDiagram-v2 fences are wider than an 80-column pane, and the
  adaptive label cap only reduces that — it does not prevent it.** Re-measured 2026-07-29 over
  every class/state mermaid fence in the author's document corpus (18 fences), rendered at a pane
  width of 80: widths `36, 50, 59, 73, 81, 84, 92, 101, 101, 102, 103, 106, 109, 125, 137, 144,
  182, 210`. **Four of eighteen fit**; the median is 102.

  These numbers replace an earlier, worse set (`36, 79, 80, 81, 82, 88, 96, 101, 102, 109, 113,
  144, 187, 243, 284` over 15 fences, three fitting, median 101 — the run also deduplicated
  identical fences, hence 15 rather than 18). The improvement came from making
  `flowchartBuilder.declarationOrder` a real topological order: the old "edge sources first" rule
  made every intermediate node a layout root, so multi-level hierarchies were drawn flattened into
  fewer, much wider rows. The widest corpus diagram went from 284 cells to 210, and one fence came
  back inside the pane. `levels()` now replays the renderer's own placement rule as well, so the
  predicted `k` matches the real layout on all 18 fences (it disagreed on 11 of 18 before).

  The per-line label cap (`mermaidLabelCap` in `mdpreview_transpile.go`) shrinks label lines as the
  number of nodes on the widest layout level (`k`) grows, and it is worth keeping, but it cannot
  guarantee a fit. It floors at 16 runes per line, so from `k >= 3` up it is already on the floor
  with nothing left to give, and the `labelJog` term was calibrated on a fixed 10-character
  relation word while a stateDiagram transition label is free text that reserves `len(label)+3`
  columns of its own. Overflow therefore happens at every `k`, not only at `k >= 4` — an earlier
  version of this note and of the plan both claimed a `k >= 4` boundary, which the corpus
  measurement disproves.

  This is the same "no horizontal panning" limitation as the item above — the preview cannot scroll
  sideways to reveal the clipped part — so the only current remedy is a wider terminal. Horizontal
  panning in preview mode is the real fix and needs its own plan. Two tests pin the honest
  behaviour on verbatim corpus fences so the claim cannot quietly drift back:
  `TestRenderMermaidSource_RealCorpusStateDiagram_TopologicalOrderBringsArtInsidePane` (the fence
  the topological order rescued, 88 cells before and 73 now) and
  `TestRenderMermaidSource_RealCorpusClassDiagram_FloorCapStillOverflowsPane` (still 81 cells with
  the cap on its floor).
- **A node with several outgoing labelled edges only gets ONE of those labels drawn, and past about
  four targets the labels merge into a token that is in none of them.** This is the vendored
  `mermaid-ascii` renderer, not the transpiler: the transpiler emits every label it parsed, but the
  renderer routes all of a node's outgoing edges along one shared horizontal row and writes labels
  onto that single row. Measured over the 18-fence corpus at a pane width of 80: **10 of 18 fences
  lose at least one edge label**, worst case 4 of 6 lost on one fence. At six outgoing edges the
  six labels `contains / needs / emits / uses / sends / polls` render as `contains` plus the
  nonsense token `pollss`.

  Note this is fan-out to DIFFERENT targets, not several parallel edges between one pair — an
  earlier version of the plan's Failure-modes table described only the parallel-edge case, which is
  much rarer. It is also independent of the width limitation above: one fence loses a label at
  column ~90 while its art is 144 cells wide, so a wider terminal does not bring the label back.

  Fixing it means changing the vendored renderer's label placement, which is out of scope for this
  patch. Two tests pin the current behaviour so a vendor bump that changes it cannot pass
  silently: `TestRenderMermaidSource_FanOutToDifferentTargets_VendoredRendererDropsEdgeLabels` and
  `TestRenderMermaidSource_WideFanOut_VendoredRendererMergesEdgeLabelsIntoOneToken`.
- **When the label cap is on its floor, two different labels or titles can truncate to the same
  string, so the diagram reads as ambiguous rather than merely short.** Measured over the 18-fence
  corpus: 7 fences contain at least one such collision, and every one of them sits at the 16-rune
  floor. Three real examples:

  - three transitions out of one state — `reviewer resolves request-changes`, `reviewer resolves
    approve`, `reviewer resolves reject (hard)` — all render as `reviewer·res...`;
  - two different conditions, `decision==approve AND publicationDate in future` and
    `decision==approve publicationDate ≤ now`, both render as `decision==app...`;
  - two classes, `SimpleWorkflowEngine` and `SimpleWorkflowManager`, become two boxes both titled
    `SimpleWorkflo...`.

  In all three the cap did not buy a fit anyway (those fences measure 109, 101 and 182 cells
  against an 80-column pane), so the readability is spent for nothing. There is no cheap fix. The
  only lever is the floor, and raising it is not free: at a floor of 24 runes the collisions drop
  from 7 fences to 3, but the corpus median width goes from 102 to 124 cells, the widest from 210
  to 257, and one fence that currently fits stops fitting. A real fix means either horizontal
  panning (so labels need not be short) or a disambiguating suffix, both of which need their own
  plan. The truncation does at least always keep its `...` ellipsis, so a truncated label is never
  mistaken for a complete one.

  A related, separate case: some collisions do not come from the width cap at all. `resolve
  {outcome}` and `resolve {outcome} (act without claiming)` both render as `resolve` because
  `mermaidCutParenthetical` and the brace cut are content rules that run regardless of width. That
  shortening is deliberate (see the plan's "Edge label" section) and is not affected by the cap.

## Dependencies added

- `github.com/charmbracelet/glamour` **v1.0.0** (deliberately v1, not v2 — v2 needs a
  different lipgloss module path that would vendor two copies of lipgloss)
- `github.com/AlexanderGrooff/mermaid-ascii` **@latest** (imported as a library, entry point
  `cmd.RenderDiagram`)

Together these two grew the vendor tree from 31 modules / ~19M (baseline, before this patch) to
71 modules / ~51M (after Task 3). Most of that growth is `mermaid-ascii`'s `cmd` package, which
pulls in gin, cobra, and logrus as transitive dependencies of a library this patch only calls for
one function (`cmd.RenderDiagram`) — see the plan's Technical Details section for why that
tradeoff was accepted, and "Dependency cost" below for what it actually measures. Confirm the
actual counts after any rebase with `du -sh vendor/`; they will drift as upstream's own
dependencies change.

### Dependency cost (measured, accepted for now)

The patch imports `github.com/AlexanderGrooff/mermaid-ascii/cmd` for exactly one symbol,
`RenderDiagram` (one call site, `renderMermaidSource` in `app/ui/mdpreview_transpile.go`). That
package also contains `web.go`, which implements an HTTP server for the upstream tool's own web
mode. Go links a package as a whole, so importing `cmd` at all drags in everything `web.go` needs,
even though nothing in revdiff can ever reach it.

Measured on the `md-preview` branch (2026-07-29, `go build ./app`, no `-s -w`):

| | before the patch (`master`) | after |
| --- | --- | --- |
| binary | 12 MB | 28 MB |
| vendor tree | 19 MB | 51 MB |
| vendored files added | — | 1609 files, ~693k lines |

`vendor/github.com/AlexanderGrooff/mermaid-ascii/cmd/web.go` is the **sole** importer of the whole
web stack: gin-gonic/gin, bytedance/sonic (a JIT with hand-written assembly), cloudwego/iasm and
base64x, google.golang.org/protobuf, go-playground/validator, bluemonday, gorilla/css,
golang.org/x/net and golang.org/x/crypto. Those directories alone account for ~21 MB of the 32 MB
vendor growth, and `go tool nm` finds 632 gin/sonic symbols linked into the built binary. `web.go`
also calls `os/exec` to run `git describe`, so a terminal diff viewer now compiles in an HTTP
server and a subprocess call it never invokes.

Nothing here is a correctness or security problem in normal use — the code is unreachable, no
listener is ever started. It is a size and supply-surface cost. It is recorded rather than fixed
because the fix is a change to the vendored dependency itself (a build tag on `web.go`, a trimmed
vendor copy, or a patched fork exposing the renderer without the `cmd` package), which needs its
own plan: it changes what `go mod vendor` regenerates, so it has to survive every future
`go mod tidy && go mod vendor` in the rebase procedure below. Until then, treat the 28 MB binary
as expected.

**After every rebase, re-run `go mod tidy && go mod vendor`.** `go mod tidy` prunes an unused
module requirement, so if the rebase's conflict resolution temporarily drops the only import of
either dependency, tidy will remove it from `go.mod`/`go.sum` until the import is restored —
this is expected `go mod tidy` behavior for this project (see the plan's Task 1 ⚠️ note), not a
sign of a broken rebase.

## Rebuild and install command

```sh
REV="$(git rev-parse --abbrev-ref HEAD)-$(git rev-parse --short=7 HEAD)-$(git log -1 --format=%ct HEAD | xargs -I{} date -u -r {} +%Y%m%dT%H%M%S)"
go build -ldflags "-X main.revision=$REV -s -w" -o .bin/revdiffm ./app
cp .bin/revdiffm ~/.local/bin/revdiffm
```

This mirrors the Makefile's `build` target (same ldflags, same `REV` computation) but writes
`revdiffm` instead of `revdiff.$(BRANCH)`/`revdiff`, so it never touches the brew-installed
`revdiff` at `/opt/homebrew/bin/revdiff`. Verify after install:

```sh
command -v revdiffm   # should resolve to ~/.local/bin/revdiffm
revdiffm --version
revdiffm --dump-keys | grep 'toggle_preview'   # confirms P is bound in this build
command -v revdiff    # should still resolve to /opt/homebrew/bin/revdiff, unaffected
revdiff --version
```

## Rebase procedure

```sh
git fetch
git rebase <new-tag>          # e.g. git rebase v1.12.0
# resolve conflicts — expect them in app/ui/diffview.go and app/ui/model.go first
go mod tidy && go mod vendor
make test
# rebuild + install per the command above
```

If `go mod tidy` removes glamour/mermaid-ascii from `go.mod` after conflict resolution, check
that `app/ui/mdpreview.go` still imports both — a conflict resolution that accidentally drops
an import will cause tidy to prune the dependency instead of erroring, which surfaces as a
build failure only once something else tries to use it.
