# PATCH.md — Markdown Preview Mode (local patch)

This is a personal patch, not a contribution to `umputun/revdiff` (see
`docs/plans/completed/20260722-markdown-preview-mode.md` for why). This file is the rebase
playbook: what the patch touches, and how to carry it onto a new upstream release.

Two different things both get called "master" in this repo, and this file means only the first
one whenever it says "upstream":

- **upstream** — remote `upstream` = `umputun/revdiff` on GitHub, the real open-source project.
  This is what `brew`/`go install` ship. The patch NEVER goes here, in any form, ever.
- **the fork** — remote `origin` = `p4elkin/revdiff` on GitHub, the personal fork this patch
  lives in. The local `master` branch tracks `origin/master`, i.e. the fork, so a bare
  `git push` from `master` goes to the fork and cannot reach upstream by accident. `master` is
  also where fork-only feature branches (including `md-preview`) get merged together before
  being pushed — see "Integrating into the fork's master" below. That push is a normal,
  expected use of this patch, not an exception to the no-upstream rule.

To take new upstream work, fetch it explicitly: `git fetch upstream`, then merge or rebase
`upstream/master`. A plain `git pull` on `master` follows the fork and does not do this.

(These remote names were swapped on 2026-08-11. Before that `origin` was upstream and the fork
was `fork`, which meant a bare `git push` from `master` aimed at `umputun/revdiff`. Any older
note or transcript using the old names is describing that layout, not this one.)

Because the patch never goes to upstream, it deliberately leaves every doc that describes the
released, brew-installed binary alone: `README.md`, `site/index.html`, `site/docs.html`, and
the plugin reference docs under `.claude-plugin/` and `plugins/`. So the `P` key
(`toggle_preview`), the `r` key (`toggle_raw`), the `▤` status-bar icon, and the `--preview`
flag appear in neither the README keybindings/options tables, nor `site/docs.html`, nor any
plugin `config.md`/`usage.md`.
That is on purpose, not an oversight: editing them would describe a feature to people running
the real upstream binary, which does not have it — true whether those docs live on `md-preview`
or get merged all the way to the fork's `master`, since the fork's `master` is still not what
those docs are about. The `P` and `r` bindings and the `--preview` flag are discoverable at runtime instead, through the
in-app help overlay (`?`), `--dump-keys`, and `--help`/`--dump-config`, all of which read the
real keymap/options and so list them automatically in this build.

## Integrating into the fork's master

`md-preview` rebases onto upstream tags directly (see "Rebase procedure" below) and is not
itself pushed anywhere upstream. Separately, feature work meant for the fork as a whole —
whether upstream-portable (like `--no-tree`) or preview-only (like `--preview`) — gets merged
into the local `master` branch and pushed to the fork:

```sh
git checkout master
git fetch origin              # origin is the fork; master tracks origin/master
git merge --no-ff md-preview  # or a feature branch, if the feature isn't on md-preview yet
make test && make lint
git push origin master
```

This is how the fork's `master` ends up ahead of `upstream/master`: it carries every fork-only
feature, preview included, published to the user's own fork, never to upstream.

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
- `app/ui/mdpreview_flowchart.go` — the `graph`/`flowchart` normalization pass: rewrites the
  constructs the vendored parser mis-parses (node shape suffixes, non-`-->` link forms, a `|`
  inside a node label) into the subset it reads correctly, and drops the styling and layout
  directives it cannot draw at all. Text-level, structure-preserving — it is NOT a transpiler
  and does not use `flowchartBuilder`.
- `app/ui/mdpreview_flowchart_test.go` — its tests.
- `app/ui/mdpreview_subgraph.go` — subgraph splitting for flowchart sources: detects when a
  diagram contains independent top-level subgraphs, decides whether it is safe to split, and
  renders each subgraph as a separate diagram stacked under its own title. Covers the five-rule
  split decision and the render + stack + fallback workflow.
- `app/ui/mdpreview_subgraph_test.go` — its tests.
- `app/ui/mdpreview_nbsp.go` — the shared no-break-space substitution used by every mermaid
  edge-label call site, replacing spaces so `mergeDrawings` in the vendored renderer treats the
  cell as opaque instead of letting the arrow line or a crossing edge bleed through (see
  "Edge-label rendering wiring" below).
- `app/ui/mdpreview_nbsp_test.go` — its tests.
- `app/ui/mdpreview_collision.go` — the edge-label collision detector plus the LR-direction
  retry gate: counts how many distinct labels land too close together on one row of rendered
  art, and decides whether a second render with the direction flipped to LR is strictly better.
- `app/ui/mdpreview_collision_test.go` — its tests.
- `app/ui/mdpreview_wrap.go` — the node-label wrap pass: breaks long node labels over several
  lines with `<br/>` so a diagram whose art overflows the pane is re-rendered narrower, gated on
  the art actually overflowing and kept only when it is strictly narrower and no more colliding
  (see "Long node labels are wrapped" below).
- `app/ui/mdpreview_wrap_test.go` — its tests.
- `app/ui/mdpreview_corpus_test.go` — the differential corpus harness used to verify both fixes
  against a large body of real fences; skipped unless `REVDIFF_MERMAID_CORPUS` is set, so it
  never runs in `make test` or CI.

- `app/ui/mdpreview_marker.go` — the zero-width marker mechanism preview annotations are built on
  (see "Preview annotations" below): the marker encoding, the tracked-block-kind table, and the
  style-cloning that prepends a marker without discarding an existing prefix.
- `app/ui/mdpreview_marker_test.go` — its tests.
- `app/ui/mdpreview_blocks.go` — the goldmark block walk: parses the placeholder document and
  emits one target per tracked block kind with its source line span, folding task-list items into
  their enclosing list item and a table into one target covering the whole table.
- `app/ui/mdpreview_blocks_test.go` — its tests.
- `app/ui/mdpreview_srcmap.go` — agrees the block walk's targets against the markers glamour left
  in the render and builds the row↔line map (`mdPreviewSourceMap`), or sets `aligned = false` and
  exposes no targets when the two disagree. Also carries the mermaid-splice row-shift adjustment
  and the `--no-colors` degrade.
- `app/ui/mdpreview_srcmap_test.go` — its tests.
- `app/ui/mdpreview_srcmap_corpus_test.go` — the env-gated corpus harness that measures alignment
  success rate and per-kind anchor counts over a real document tree (mirrors
  `mdpreview_corpus_test.go`'s differential mermaid harness on the fork's `master`, which this
  branch predates — see "Preview annotations" → "Corpus measurement" below).
- `app/ui/mdpreview_cache.go` — the single-entry render cache (`mdPreviewRenderCache`) that keys
  the base render and its source map on `(file.name, file.loadSeq, viewport.Width, noColors)`, plus
  the composed-frame assembly (`mdPreviewBody`, `mdPreviewFrame`, `mdPreviewHighlight`) that
  layers the block cursor's highlight and painted annotations on top of the cached base.
- `app/ui/mdpreview_cache_test.go` — its tests, plus `BenchmarkMdPreviewRepaint`.
- `app/ui/mdpreview_annotate.go` — painting existing annotations into the preview
  (`mdPreviewPaintAnnotationsTracked`, reusing `renderAnnotationOrInput`/`renderFileAnnotationHeader`
  rather than a parallel painter) and creating one (`mdPreviewStartAnnotation`,
  `mdPreviewStartAnnotationAt`, `mdPreviewClickDiff`, the live-input visibility helper).
- `app/ui/mdpreview_annotate_test.go` — its tests.
- `app/ui/mdpreview_cursor.go` — the driveable preview cursor: its tagged state
  (`mdPreviewCursorState`), the read/place/clear helpers, the center-of-viewport seed
  (`mdPreviewViewportCenter`, `mdPreviewCenterBlock`), the one-stop-per-press move with its minimal
  viewport follow (`moveMdPreviewCursor`, `syncMdPreviewViewportToStop`), and the
  drop-when-scrolled-out-of-view rule every viewport-only scroll goes through
  (`dropMdPreviewCursorIfHidden`, `afterMdPreviewViewportScroll`). See "Preview cursor" below.
- `app/ui/mdpreview_cursor_test.go` — its tests.
- `app/ui/mdpreview_stops.go` — what the cursor can stop on: the identity a stop is addressed by
  (`mdPreviewStopRef`), the stop and painted-annotation anchor types (`mdPreviewStop`,
  `mdPreviewAnnotAnchor`), the ordered stop list and the two lookups over it
  (`mdPreviewSourceMap.stops`/`stopAt`, `mdPreviewStopIndex`, `mdPreviewNearestStop`), and `d`
  inside preview (`mdPreviewDeleteAnnotation`, `repaintMdPreviewAfterStopChange`).
- `app/ui/mdpreview_stops_test.go` — its tests.
- `app/ui/mdpreview_expand.go` — raw source expansion for the selected block: the pass that swaps
  one block's rendered rows for its markdown source one row per source line
  (`mdPreviewRawLines`, `mdPreviewExpandBlock`, `mdPreviewLineAnchor`), the refusal rules that
  say when `r` will not expand (`mdPreviewExpandRefusal` — a code fence by block kind, a block
  whose every source line is blank), the recovery of a mermaid diagram's fence extent
  (`mdPreviewOwnSpanEnd`, `mdPreviewMermaidFenceSpan`), and the `r` handler
  itself (`mdPreviewToggleRaw`, `mdPreviewExpandTarget`, `mdPreviewRawStopLine`,
  `mdPreviewCollapseRaw`). See "Raw source expansion" below.
- `app/ui/mdpreview_expand_test.go` — its tests.

A clean rebase never conflicts on these thirty-two files — they don't exist upstream. All conflict
risk is in the hunks below.

## Existing files edited, and where

Confirmed against `git diff master...md-preview --stat -- . ':!vendor'`. Line numbers are
current HEAD of `md-preview`; they will drift after every rebase — treat them as a map of
*what* to look for, not permanent coordinates.

**Task 4 wiring (the mode toggle itself):**

- `app/keymap/keymap.go`
  - line 55: `ActionTogglePreview` added to the `Action` const enum
  - line 95: `ActionTogglePreview: true` added to the `validActions` map
  - line 238: help-section entry in `defaultDescriptions()`
  - line 301: `"P": ActionTogglePreview` in `defaultBindings()`
- `app/ui/model.go`
  - `mdPreview bool` field on `modeState`
  - `markdownPreviewable bool` field on `loadedFileState` — the real gate for preview mode (a
    full-context markdown file). It exists because `mdTOC` is nil for a heading-less
    markdown file, so `mdTOC != nil` wrongly refused preview for valid heading-less docs.
  - `keymap.ActionTogglePreview` added to the toggle-grouping `case` that routes to
    `handleViewToggle`
  - `case keymap.ActionTogglePreview: m.toggleMarkdownPreview()` inside `handleViewToggle`
- `app/ui/loaders.go`
  - in `handleFileLoaded`, beside the existing `mdTOC` computation: sets
    `m.file.markdownPreviewable = m.isMarkdownFile(msg.file) && m.file.singleColLineNum`.
    This makes `loaders.go` a newly-touched file for the patch, so a rebase moving that block
    will surface a fresh conflict here.
  - the `mdTOC` build below it gained its own `m.file.singleFile &&` condition (second hunk in
    this file). The two conditions used to be one shared expression; they are now deliberately
    different, and each carries a comment saying so. See "Preview in multi-file reviews" below.
- `app/ui/diffview.go`
  - line ~280: early-return branch in `renderDiff()` — `if m.modes.mdPreview &&
    m.file.markdownPreviewable { return m.renderMarkdownPreview() }`, placed beside the existing
    `m.modes.collapsed.enabled` branch
- `app/ui/view.go`
  - line 459: `{"▤", m.modes.mdPreview}` added to `statusModeIcons()`

That is 12 hunks across 5 files. The plan's Task 4 text counts by checklist item, not by literal
diff hunk (e.g. `keymap.go`'s enum + validActions edit is one checklist item but two hunks), so
its site count differs — use the itemized list above as the actual hunk map.

**`--preview` flag (start in markdown preview mode, added later):**

Same reasoning as the `P` binding and `▤` icon above — this is a preview-only option, so it is
NOT added to `README.md`/`site/docs.html`/plugin `config.md` (which describe the released,
upstream binary, no preview mode). It IS expected to reach the fork's `master` via the merge described
in "Integrating into the fork's master" — that section, not this one, is what decides where a
feature ends up; this section only says why the docs stay untouched. Discoverable via `--help`
and `--dump-config` in this build.

- `app/config.go` — `Preview bool` next to `Collapsed`, tag
  `` `long:"preview" ini-name:"preview" env:"REVDIFF_PREVIEW"` ``, description "start in
  markdown preview mode"
- `app/main.go` — `Preview: opts.Preview,` in the `ui.ModelConfig{}` literal, next to
  `Collapsed:`
- `app/ui/model.go`
  - `Preview bool` on `ModelConfig` (Configuration values section, next to `Collapsed`)
  - `NewModel`'s `modes: modeState{...}` literal seeds `mdPreview: cfg.Preview`
- No new applicability gate needed (unlike `Compact`'s `CompactApplicable`): the existing
  per-file `markdownPreviewable` check in `handleFileLoaded` (loaders.go) already resets
  `m.modes.mdPreview` to `false` on the first file load if that file is not previewable, so an
  unusable seed (e.g. `--preview` against a non-markdown file, or a renderer/mode that produces
  a non-full-context diff) silently no-ops instead of needing its own gate.

**Task 5 wiring (making annotation/cursor keys inert during preview — required additional
edits beyond Task 4's file list, see the plan's Task 5 section for why):**

- `app/ui/model.go`
  - line 1038: `if m.modes.vimMotion && !m.modes.mdPreview {` — one-token change to the guard
    that decides whether to run `interceptVimMotion`
  - line 1072: `dispatchAction` guard — now `if m.modes.mdPreview { if model, handled :=
    m.handleMdPreviewAction(action); handled { return model, nil } }`. It was originally the
    flatter `if m.modes.mdPreview && !mdPreviewActionAllowed(action) { return m, nil }`; the
    horizontal-panning work (below) needed preview to *serve* two actions rather than only
    block them, and the whole decision moved into `handleMdPreviewAction` in `mdpreview.go` so
    this file keeps a single three-line hunk. `dispatchAction` was split into a thin wrapper
    plus `dispatchResolvedAction` here to keep `gocyclo` under the lint ceiling; a rebase that
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
    the one-line hook that gives `mdpreview_transpile.go` a chance to rewrite the fence before it
    ever reaches the third-party renderer: a `classDiagram` or `stateDiagram-v2` fence becomes
    `flowchart` source, and a `graph`/`flowchart` fence goes through the normalization pass in
    `mdpreview_flowchart.go` (see "graph/flowchart normalization" below)
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

**Horizontal panning wiring (reading mermaid art that is wider than the pane — replaces the old
"wide diagrams are clipped" limitation):**

- `app/ui/mdpreview.go` — all of the new logic, in the patch's own file:
  - `mdPreviewCutWidth` — how many columns of a preview row are visible. It is the viewport
    width, NOT `applyHorizontalScroll`'s `diffContentWidth() - gutterExtra()`: a preview row has
    no cursor bar, no gutters and no right padding column, so the diff basis would cut two
    columns short on every row.
  - `mdPreviewMaxLineWidth` / `mdPreviewMaxOffset` — the pan clamp, measured on the *rendered*
    document. A clamp derived from `m.file.lines` would stop at the width of the mermaid fence's
    source line (a few dozen cells) instead of the art it renders to (200+).
  - `applyMdPreviewScroll` / `cutMdPreviewLine` — the ANSI-aware cut, reusing `ansi.Cut` and the
    `leftScrollIndicator` / `rightScrollIndicator` glyph helpers so the visual language matches
    the diff pane. Both indicators are drawn inside the cut width, because the viewport
    truncates at exactly that width (the diff path can spill `»` into its own right padding).
  - `mdPreviewLeftIndicator` / `mdPreviewRightIndicator` — plain glyphs under `--no-colors`,
    because the shared helpers fall back to reverse video there and the preview promises a
    zero-ANSI render.
  - `panMarkdownPreview` — the pan itself; renders the document once and reuses that render for
    both the clamp and the cut, so a keypress costs one glamour pass, not two.
  - `handleMdPreviewAction` — the new preview gate called from `dispatchAction`. It wraps
    `mdPreviewActionAllowed` and routes `scroll_left`/`scroll_right` to the pan. **The routing
    is required, not stylistic:** `scroll_right` doubles as the focus-diff action in
    `handleTreeAction` and `handleTOCNav` (`case keymap.ActionFocusDiff,
    keymap.ActionScrollRight:` in `app/ui/diffnav.go`), so letting it fall through would switch
    panes instead of panning.
  - `mdPreviewAllowedActions` gained `ActionScrollLeft` / `ActionScrollRight`; the allowlist
    stays the single source of truth (removing them there disables the pan).
  - `toggleMarkdownPreview` resets `m.layout.scrollX` on both transitions — the offset is shared
    with the diff render but means different things in the two modes. `handleFileLoaded`
    (`loaders.go`) already reset it on file load, so no edit was needed there.
- `app/ui/model.go` — no new hunk: the existing `dispatchAction` guard changed shape (see the
  Task 5 entry above).

**Preview in multi-file reviews (the gate no longer demands a single-file review):**

The first version of the gate also required `m.file.singleFile`, so `P` was inert in every review
holding more than one file — including a mixed markdown + java review where the displayed file was
a perfectly renderable full-context document. What makes a whole-document glamour render safe is
that the displayed file is full context (every line present, so no table or code block can be shown
half-rendered); the size of the review says nothing about that. The `singleFile` term was extra
caution, not a safety condition, and it is gone.

- `app/ui/loaders.go` — the two conditions in `handleFileLoaded` now differ on purpose:
  - `m.file.markdownPreviewable = m.isMarkdownFile(msg.file) && m.file.singleColLineNum` — no
    `singleFile` term.
  - the `mdTOC` build keeps `m.file.singleFile &&` of its own. The TOC renders into the `paneTree`
    slot (`view.go`'s `m.file.singleFile && m.file.mdTOC != nil` branch), which in a multi-file
    review is the file tree's. Building a TOC there would replace the file list with a table of
    contents and leave no way to reach the other files. Preview has no such conflict — it renders
    into the diff pane, which belongs to the displayed file either way.

  Because the two were one shared expression before, both carry a comment saying they are now
  different. A rebase must not "tidy" them back into one.
- Nothing else changed. `mdTOC` is nil in a multi-file review, so every TOC-dependent path
  (`treePaneHidden`, `togglePane`, `toggleTreePane`, `handleTreeAction`'s TOC dispatch,
  `handleFileOrSearchNav`'s TOC branch, `view.go`'s TOC render branch) behaves exactly as it did
  before this change.
- Pane geometry: in a multi-file review with the tree visible, `handleResize` sets
  `viewport.Width = width - treeWidth - 4`, which is the same value `View` uses as `diffPaneW`. The
  pan clamp reads `mdPreviewCutWidth()` = `viewport.Width`, so it clamps against the narrower
  two-pane width and the `»` glyph still lands inside the pane. Pinned by
  `TestView_MdPreviewInMultiFileReview_PaneGeometryIntact` and
  `TestPanMarkdownPreview_MultiFileReview_ClampsAgainstTwoPaneWidth`.
- `mdPreviewAllowedActions` is unchanged. `next_item`/`prev_item` stay blocked — see the expanded
  comment above the map, and the "must leave preview before changing file" limitation below.

**Edge-label rendering wiring (no-break spaces and the LR collision retry, added later — see
`docs/plans/completed/20260805-mermaid-edge-label-rendering.md`):**

- `app/ui/mdpreview_transpile.go`
  - `mermaidEdgeLabel` — the `strings.ReplaceAll(s, " ", "·")` substitution and the
    `mermaidDotRun` collapse were removed; the function now calls `mermaidNBSPSubstitute`
    (`mdpreview_nbsp.go`) as its sole space-handling step, after `mermaidSafeText` and before
    the rune/byte cap.
  - `renderMermaidSource` — after the existing `mermaidcmd.RenderDiagram(toRender, nil)` call,
    the result is passed through `mermaidRetryLRIfColliding(toRender, rendered, paneWidth,
    func(s string) (string, error) { return mermaidcmd.RenderDiagram(s, nil) })` before the
    `return rendered, nil`. `paneWidth` is the same value the transpilers already receive, and
    the retry needs it to refuse a flip that would push a fitting diagram off the pane. The
    transpile and subgraph-split paths above that call are untouched — the retry only ever sees
    the final whole-source (or per-subgraph) render.
- `app/ui/mdpreview_flowchart.go`
  - `unquoteFlowchartEdgeLabel` renamed to `normalizeFlowchartEdgeLabel` and restructured so the
    no-break-space substitution runs on every path that has a label, not only the
    previously-quoted one; it is now the single chokepoint for both edge-label spellings
    (`normalizeFlowchartLine` normalizes links before nodes, so an inline `-- label -->` has
    already become `-->|label|` by the time this function runs).

Both files are already patch-owned (listed under "New files added by the patch" above), so none
of this carries rebase conflict risk against upstream — recorded here for the call-flow map,
same reasoning as the "Diagram transpile wiring" and "Horizontal panning wiring" entries above.

**Preview annotations (reading and creating annotations inside preview, this
plan's own feature — see `docs/plans/completed/20260811-preview-annotations.md`):**

The mechanism: every block glamour renders writes a zero-width marker into its
style's prefix (`mdpreview_marker.go`); a goldmark walk over the same document
independently lists what block starts on what source line
(`mdpreview_blocks.go`); the two sequences are agreed kind-for-kind and
row-for-row (`mdpreview_srcmap.go`) into `mdPreviewSourceMap`, which answers
"which source line did this rendered row come from?" or, on any disagreement,
refuses to answer at all (`aligned = false`, no targets exposed). Everything
downstream — the highlight, the painted annotations, `a`, a click — is built
on that one map and that one refusal contract.

- `app/ui/model.go`
  - `mdPreviewCache *mdPreviewRenderCache` field on `Model`, seeded in
    `NewModel` — held behind a pointer for the same reason `renderCache` is
    (`renderMarkdownPreview` has a value receiver; a plain field would
    memoize into a copy the method throws away).
  - `dispatchAction`'s `mdPreview` guard now threads a `tea.Cmd` through:
    `handleMdPreviewAction` gained a third return value, and this call site
    forwards it instead of always returning `nil`. The only case that ever
    produces a non-nil cmd is `ActionConfirm` starting an annotation input
    (`startAnnotation`'s `ti.Focus()` cmd, ordinarily nil under this fork's
    `cursor.CursorStatic` — see gotchas.md's per-line render cache note).
- `app/ui/mouse.go` — both hunks are a single call line, with the preview logic
  itself in the patch-owned preview files, the way `handleMdPreviewAction` was
  moved out of `model.go`.
  - the preview branch of `handleMouse`'s left-click case now checks
    `zone == hitDiff` first and calls `mdPreviewClickDiff`
    (`mdpreview_annotate.go` — maps the clicked row through the current source
    map to a block and starts annotating it); every other zone still falls
    through to the old read-only `return m, nil`.
  - `flushWheelPending` gained a two-line `if m.flushPreviewWheelPending()
    { return }` at its top (`mdpreview_cache.go`): `pinDiffCursorTo` is an
    unconditional no-op in preview (nothing to pin), so without it the deferred
    repaint the wheel-burst debounce owes never landed. That helper now also
    drops the block cursor once per burst when the burst carried its block off
    screen (see "Preview block cursor" below),
    repaints once and clears both `renderPending` and `tickInFlight` — it rides
    the SAME `wheelState` debounce documented in gotchas.md rather than adding a
    second one, per that task's explicit warning.

`mdPreviewAllowedActions`' doc comment (`mdpreview.go`, a patch-owned file, so
this carries no upstream conflict risk) was rewritten because its blanket
justification stopped being true: the whole map used to be excluded for one
shared reason ("would create, edit, delete, or navigate to an annotation, or
move `m.nav.diffCursor`"), and `ActionConfirm` is now the one exception —
allowed, and it DOES create an annotation, anchored through the source map
instead of through `m.nav.diffCursor`'s ordinary meaning. Each remaining
exclusion now has its own inline reason rather than sharing the old blanket
one. `ActionConfirm` is routed INSIDE `handleMdPreviewAction` rather than only
added to the allowlist map, because its ordinary fall-through target
(`handleEnterKey`) branches on pane focus and would run the TOC jump — in
diff-line coordinates the preview render does not have — if the tree/TOC pane
happened to have focus while previewing. `ActionAnnotateFile` (`A`) and
`ActionFlushOutput` (`O`) needed no such routing: both fall through to their
ordinary handlers unmodified, because
neither reads or assigns `m.nav.diffCursor` (a file-level annotation's `Line`
is always 0, which `mdPreviewPaintAnnotationsTracked` already paints ahead of
every block unconditionally) and neither touches the viewport.

⚠️ **The `ActionConfirm` focus branch takes focus rather than doing nothing.**
With the tree/TOC pane focused it assigns `m.layout.focus = paneDiff` and
returns, so the second press annotates — the same progression
`handleEnterKey`'s own `paneTree` branch gives source view. A bare no-op there
made `a` and `A` permanently dead in every **multi-file** review: `paneTree` is
the focus `NewModel` starts in (only single-file mode flips it to `paneDiff`,
in `handleFilesLoaded`), and `toggle_pane` / `focus_tree` / `focus_diff` are all
excluded from the allowlist, so no key could hand focus back. `A` keeps its own
unmodified diff-pane-only gate and becomes reachable once `a` has moved focus.

A refused preview annotation is not silent either: `mdPreviewStartAnnotation` sets
`m.preview.hint` (an `mdPreviewState`, the same shape as `outputState` /
`compactState`, rendered by `transientHint` and cleared on the next key or mouse
event) when neither the block cursor nor the center-of-viewport seed resolves a
block. Since the block cursor landed that is exactly the unaligned-document case
— `README.md` is one — where the frame is otherwise byte-identical and the
reader cannot tell "this document cannot be anchored" from "the key is not
bound". The message is the constant `mdPreviewUnanchorableHint`; the old second
message ("No block in view to annotate") is gone with the scroll-derived
highlight, because an aligned map always has a nearest block to seed on.

The render cache (`mdpreview_cache.go`) is a single entry, not a map — the
preview shows one file at a time, so there is never more than one base render
worth keeping warm (same shape as `diffRenderCache`'s own per-line, not
per-file, single-generation cache). Key: `(file.name, file.loadSeq,
viewport.Width, noColors)`. `loadSeq` is what makes the key safe across an `R`
reload of the same file at the same width — `renderMarkdownPreview`'s old doc
comment explicitly declined a cache for exactly this reason before `loadSeq`
closed it, the same way `globalRenderKey` already relies on it (see
gotchas.md's per-line render cache note).

Two more memos sit beside it on `mdPreviewRenderCache`, both keyed on the
painted body string rather than on the cache key, because the body is the base
render PLUS this file's annotation rows and changes on every annotation edit:
the widest-row width (`ansi.StringWidth` over the whole document, the pan
clamp's basis) and the horizontal cut itself. Neither depends on the vertical
offset, so the repaint every `j`/`k` keypress now drives — the block highlight
has to follow the viewport — reuses both.

Measured on Apple M2 Max against
`docs/plans/completed/20260722-markdown-preview-mode.md` (810 source lines, 983
rendered rows, 3 mermaid fences), pane width 80, a resolver carrying a search
background so the highlight really paints:

| path | ns/op | B/op | allocs/op |
|---|---|---|---|
| fresh `mdPreviewRenderWithMap` (what a repaint cost before the cache) | 104,003,012 | 42,925,885 | 415,344 |
| repaint frame, before the width/cut memos | 6,222,047 | 1,409,447 | 1,092 |
| repaint frame, current (`mdPreviewFinalRender`) | 124,481 | 671,822 | 21 |
| cached base-render lookup alone — NOT a repaint | 502.5 | 0 | 0 |

⚠️ **The last row is a cache lookup, not a repaint, and an earlier version of
this note recorded it as one** (`~523ns`, "about 215,000x"). The benchmark it
came from timed `mdPreviewBaseRender` — a key comparison and a string return —
while a repaint is `mdPreviewFinalRender`: cached base render, annotations
painted in, the horizontal cut, the highlight. Mislabelling it is how a
full-document width scan came to sit unnoticed in every keypress. The honest
figure for the repaint a block-cursor move actually pays is the
third row: ~0.12ms, down from ~6.2ms, and ~835x below a fresh render. The
remaining cost is the highlight's own per-row pass, which cannot be cached
because it moves with the offset.

**Preview cursor (the highlight became something the reader steers):**

The preview annotations above shipped with a highlight *derived from scroll
position*: `mdPreviewHighlightAnchor` computed the topmost fully visible block
from `viewport.YOffset` and held no state. In use that marked a block nobody had
chosen, always near the top of the pane, and it could not be aimed. It is now a
real cursor over **stops**.

A stop is a rendered block, or a single annotation painted under one. It started
out as blocks only, which meant `j`/`k` stepped straight over every painted
comment: nothing could select one, so nothing could delete one either, and `d`
was excluded from the allowlist for exactly that reason. Annotations are now
stops of their own.

The whole feature lives in the patch's own files. **No upstream-owned file gained
a hunk for it** — not `model.go`, not `loaders.go`, not `mouse.go`, not
`keymap.go`. Three design choices are what bought that, and all three are worth
keeping through a rebase:

- **The cursor is tagged, not reset.** `mdPreviewCursorState` stores the stop
  together with the `file.name` + `file.loadSeq` it was placed under, and reports
  "nothing selected" whenever the tag does not match the current load. A file
  switch and an `R` reload both bump `loadSeq`, so both leave nothing selected
  with no reset in `handleFileLoaded` (`loaders.go`) to remember. It is the same
  seq-tagging `compactState.pendingAnchor` uses. The zero value means "nothing
  selected", so `NewModel` needs no initializer either.
- **A stop is addressed by identity, never by its index in the stop list**
  (`mdPreviewStopRef`: a block index, plus which annotation under it if any). The
  list changes length under a cursor that never moved — `A` prepends a file-level
  stop, `d` removes one from the middle — and an index would silently come to
  mean a different stop. An identity survives both, and it is what makes the
  after-delete placement exact rather than approximate.
- **The reading keys and `d` are routed inside `handleMdPreviewAction`**, which
  already existed. `ActionDown`/`ActionUp` (j/k AND the arrows — one action each,
  they cannot be told apart and are not meant to be) move the cursor;
  `ActionScrollDiffDown`/`ActionScrollDiffUp` (J/K) joined the routed set so the
  scroll can drop the cursor and repaint, which the fall-through
  `scrollDiffViewportLine` never did in preview (`pinDiffCursorTo` is a no-op
  there, so it repainted nothing); `ActionDeleteAnnotation` (`d`) joined it too —
  see the delete rules below for why adding it to the allowlist alone would be
  wrong.

Behavior, in one place:

- nothing is highlighted until the reader moves. Entering preview, a file load
  and an `R` reload all leave the cursor unset.
- `j`/`k` (and the arrows) with no cursor SEED it at the stop nearest the
  vertical center of the viewport and stop there; with a cursor they move one
  stop and clamp at the first and the last, no wrap.
- stop order is paint order: the file-level annotation (painted above the whole
  body), then each block followed by the annotations painted under it. So `j`
  from a block reaches that block's own first annotation before the next block.
  For an ORDINARY block that order is not imposed by the stop list — it follows
  from the paint geometry, since block *i*'s annotation rows occupy exactly the
  gap between block *i*'s `endRow` and block *i+1*'s `row`. The EXPANDED block is
  the one place it IS imposed: its raw source lines and its comments interleave,
  so `stops()` merges the two lists by ascending row
  (`mdPreviewMergeStopsByRow`) and drops the block's own stop while it is
  expanded. See "Raw source expansion" below.
- the highlight marks the cursor's own rows: a block's rows on a block stop, the
  annotation's own rows on an annotation stop. What is marked is always what `a`
  and `d` will act on.
- the viewport then follows minimally — `syncMdPreviewViewportToStop` is
  `syncViewportToCursor` in preview-row coordinates, including its
  "taller than the pane shows its start" clamp. It never centers: centering
  is what made the old top-edge behavior jumpy.
- `J`/`K`, page/half-page, home/end and the wheel stay pure viewport scroll, and
  each drops the cursor when its stop has gone entirely off screen. That keeps
  the feature's invariant: you always see what you are about to annotate or
  delete. The wheel does it in `flushPreviewWheelPending`, once per burst, not
  per event.
- `a` with no cursor seeds at the center block and annotates that, so it is never
  a dead key. On a **block** stop it annotates that block's start line; on an
  **annotation** stop it EDITS that annotation, aimed at its own `(Line, Type)`.
  A click sets the cursor to what it hit, in addition to annotating it: a raw
  source line of an expanded block when the clicked row carries a line anchor
  (`srcMap.lineAt`, tried first), and otherwise the block the row belongs to.
- `d` deletes the annotation the cursor is stopped on, then leaves the cursor on
  the block that owned it — except inside an expanded block, where it stays
  expanded and lands on the raw line the comment was attached to (see "The cursor
  became two-level" below). On a block stop, or with nothing selected, it refuses
  with a transient hint (`mdPreviewDeleteNeedsAnnotationHint`) rather than
  removing the block's annotations wholesale.
- a document whose source map did not align has no stops to steer between, so
  `j`/`k` fall back to a one-row scroll there rather than becoming dead keys.

**Editing is the pre-existing diff-pane path, not a new mechanism.**
`startAnnotation` already pre-fills its input from the store for the same
`(Line, Type)` and `Store.Add` already replaces on that key, so aiming `a` at the
selected annotation's own line IS the edit. The multi-line case comes with it
unchanged: a comment containing newlines is stashed in `annot.existingMultiline`
instead of being loaded into the textinput (whose sanitizer would flatten it), so
the editor key seeds `$EDITOR` from it and Enter on an empty input preserves it
rather than blanking it. No edit action and no new binding were added.

⚠️ **`d` must stay routed inside `handleMdPreviewAction`, not merely allowlisted.**
Its fall-through target `deleteAnnotation` (`app/ui/annotate.go`) reads
`m.nav.diffCursor` + `m.annot.cursorOnAnnotation`, which preview drives neither
of, and its tail can call `requestFileDiff` — a file load, mid-delete, out of
preview. `mdPreviewDeleteAnnotation` deletes off the preview cursor instead. It
does still refresh the tree filter and follow a selection the refresh moved off
this file, exactly as source view does: the alternative is a file tree claiming
this file carries annotations after its last one was deleted.

⚠️ **The preview cursor is deliberately NOT a key of any preview memo, and adding
it to one would be wrong twice over.** `mdPreviewHighlight` runs AFTER
`applyMdPreviewScroll`'s memoized cut (`mdPreviewFrame` owns that order, for the
reasons in its doc comment), so a cursor move misses nothing — the pass it
changes is the only pass that is not memoized. Moving the highlight inside the
cut memo would make every cursor move serve the previous frame until some other
input changed. `TestMdPreviewFinalRender_CacheDoesNotServeAStaleFrameAcrossACursorMove`
reaches the second cursor state on a model whose memos were warmed by the first,
which is the only shape that catches it; it was verified by actually making that
mutation. A DELETE is a different question and needed checking separately, since
it changes the painted body rather than only the highlight: the scroll memos key
on that body string and so miss by themselves, which
`TestMdPreviewDeleteAnnotation_RepaintDoesNotServeAStaleFrame` pins on a model
whose memos were warmed with the annotation still present.

**Raw source expansion (`r` shows the selected block's markdown source, added later — see
`docs/plans/completed/20260811-preview-raw-block-expansion.md`):**

Press `r` on the block the preview cursor marks and that block is redrawn as its raw markdown
source, one rendered row per source line. `j`/`k` then step between those source lines and `a`
annotates the exact line. Press `r` again, or `esc`, and the block goes back to its rendered form.
A mermaid diagram expands too (added after the first version of the feature — see "A mermaid
diagram expands to its fence source" below); a code fence still does not.

Only one upstream-owned file gained hunks for this, and they are all four in `app/keymap/keymap.go`:

- `app/keymap/keymap.go`
  - line 56: `ActionToggleRaw Action = "toggle_raw"` added to the `Action` const enum
  - line 96: `ActionToggleRaw: true` added to the `validActions` map
  - line 239: help-section entry in `defaultDescriptions()` — `{ActionToggleRaw, "toggle raw
    source for the selected preview block", "View"}`
  - line 302: `"r": ActionToggleRaw` in `defaultBindings()`

`app/ui/model.go` is deliberately untouched. `dispatchAction` already routes every action through
`handleMdPreviewAction` while preview is on (that hunk landed with Task 5 above), so both the
allowlist entry (`keymap.ActionToggleRaw: true` in `mdPreviewAllowedActions`) and the handler case
live in fork-owned `app/ui/mdpreview.go`. Outside preview mode `toggle_raw` reaches
`dispatchResolvedAction`, matches no case there, and falls through to the pane handlers — the same
path any unbound key already takes, which is why it needs no new guard. It is not literally inert,
though, and the earlier wording claiming that was wrong: with the file tree focused the pane
handler clears `pendingAnnotJump` and `nav.pendingHunkJump`, and with the markdown TOC pane focused
(single markdown file, preview off) `handleTOCNav`'s default arm runs `EnsureVisible` +
`syncDiffToTOCCursor`, which reassigns `m.nav.diffCursor` and top-aligns the viewport. Neither is a
regression introduced by this key — both are what pressing any unbound key does today — but a
future change that makes an unbound key in those panes do something visible inherits `r` with it.

The rest of the feature is fork-owned: the pass and the `r` handler in the new
`app/ui/mdpreview_expand.go`; the second cursor level in `mdpreview_cursor.go` and
`mdpreview_stops.go`; the per-line annotation splice in `mdpreview_annotate.go`; the wiring line in
`mdpreview_cache.go`; and the `lines` field plus `spliceRow` in `mdpreview_srcmap.go`.

**The cursor became two-level, and the cursor is what carries the expansion:**

- **A raw source line is a stop of its own.** `mdPreviewStopRef` gained an `onLine bool` + `line
  int` pair beside the existing `onAnnot bool` + `annot int`, and for the same reason that type's
  doc comment already gives — a stop is addressed by identity, never by its index in the list.
  `line` holds the diff-line index (`mdPreviewLineAnchor.lineIdx`), which is also the coordinate
  `a` needs, so "annotate this raw line" reads straight off the ref with no translation. A blank
  source line paints a row but gets no stop: `mdPreviewHighlight` skips blank rows inside a span,
  and a stop nobody can see would break the invariant that you always see what `a` will act on.
- **One block is expanded at a time, and it is always the block the cursor is in.** The state is a
  single `expanded bool` on `mdPreviewCursorState`, meaning "`ref.block` is drawn as raw source".
  Putting it there is what makes a file switch and an `R` reload collapse the block for free — that
  struct already carries the `file.name` + `file.loadSeq` tag, and every existing placement helper
  assigns a fresh struct literal, so `expanded` defaults back to false on every path that was not
  written for this feature. The failure direction is "collapsed when I did not expect it", never
  "a stale expanded block with a row map that does not match the screen". Two setters deliberately
  opt out of that default: `setMdPreviewLineCursor` (the only production writer of `expanded =
  true`) and `setMdPreviewCursorRefKeepingExpansion`, which preserves it only when the target ref
  names the block that is already expanded.
- **Expansion ends when** `r` or `esc` is pressed, the cursor moves to another block, the whole
  expanded block scrolls off screen, the file changes, or `R` reloads. A scroll *within* a long
  expanded block does not end it: `dropMdPreviewCursorIfHidden` re-seats the cursor onto the
  nearest still-visible stop of that block instead of clearing it.
- **`j` and `k` clamp inside the expanded block** rather than stepping out of it, via
  `mdPreviewBlockStopRange`. A flat clamp over the whole stop list would only hold for the
  document's first and last block; anywhere else a held-down `j` would silently throw away the
  reader's expansion.
- **The pass runs on the cached base render, so no memo key changed.**
  `mdPreviewBody` is now three stages — cached base render, then `mdPreviewExpandBlock`, then the
  annotation painter. Expansion is not an input to the glamour render, so pressing `r` costs no
  glamour pass; the downstream `mdPreviewScrollCache` keys on the body string, which already
  carries the expansion, so it misses exactly when it should. ⚠️ The no-expansion path must return
  the *same* string value it was handed, not a rebuilt copy, or `mdPreviewScrollCache.forBody`'s
  `body == c.body` compare degrades from a shared-pointer check to a full memcmp on every frame.
- **`d` and a click behave differently inside an expanded block.** `d` normally leaves the cursor
  on the block that owned the deleted comment, which would collapse an expanded one — deleting a
  comment is not a request to stop looking at the source. So `mdPreviewCursorAfterDelete` keeps it
  expanded and lands on the raw line the comment was attached to, falling back to the block's first
  raw line. A click resolves the clicked row to a raw-line stop first (`srcMap.lineAt`), before the
  `anchorAtRow` fallback that resolves a row to the block around it.
- **The stop order for the expanded block is imposed, not geometric.** Everywhere else `stops()`
  lists a block then its comments and that is already ascending-row order. Inside an expanded block
  the raw lines and the comments interleave, so the two lists are merged by ascending row
  (`mdPreviewMergeStopsByRow`) and the block's own stop is dropped while it is expanded — a stop
  covering every raw line at once would make `a` ambiguous about which line it meant.
- **A block's raw span is re-cut to the lines its own rendered rows came from, in both
  directions.** Rows TILE — `mdPreviewBuildSourceMap` closes each block off at the row before the
  next one starts, so every row belongs to exactly one block — while source spans OVERLAP:
  `swallowedSpan` (`mdpreview_blocks.go`) excludes a nested boundary child from a list item's or
  blockquote's aggregation while leaving it inside the resulting min/max range, so a container spans
  source lines whose rendered rows belong to the nested block. `mdPreviewClipRawSpan` reconciles the
  two, and each direction fixes a bug that reached a build:
  - it SHRINKS a container at the next block's first source line. Painting the container's whole
    span into the few rows it owns drew the nested block's content as source AND left it rendered
    underneath — the same text twice on screen.
  - it GROWS a nested block over the enclosing container's continuation, bounded by how far any
    EARLIER block's source reaches. The rows a nested list owns run to the row before the
    container's next sibling, which includes the rows the container's own trailing paragraph was
    rendered into; painting only the nested list's own lines there DELETED that paragraph from the
    frame for as long as the block stayed expanded, with nothing on screen to say so. So a
    container's post-nested-block lines belong, for this pass, to the nested block whose row range
    they are rendered inside — which is also what makes them reachable by `j`/`k` and `a`.

  The growth stops at the enclosing container's reach rather than running to the next block, so a
  paragraph that merely precedes a fence does not show the fence's opening marker as its own last
  source line. Trailing blank source lines are dropped for the same symmetry from the other end,
  because `mdPreviewExpandBlock` keeps the block's trailing blank RENDERED rows (they are glamour's
  padding between blocks) rather than replacing them.
  `TestMdPreviewRawExpansionCorpusKeepsEveryLetter` (`mdpreview_srcmap_corpus_test.go`, env-gated
  on the same corpus list as the alignment harness) is the mechanical net under all of it: it
  expands every block of every corpus document and fails if a letter disappears from the frame. The
  deletion above survived a review and a round of fixture tests because every fixture expanded the
  CONTAINER, never the block nested inside it.
- **A mermaid diagram expands to its fence source, a code fence does not, and the asymmetry is the
  point.** A code fence renders as its own lines, roughly one for one, so expanding it would add the
  fence markers and nothing else — it stays refused by kind. A diagram renders as box art that
  carries none of its source's text, so expanding is the only way to read or comment on the
  definition ("this edge label is wrong", "this node should be a decision"), which is what the
  feature is for. The first version refused it with `Diagram source is one line`; that was a fact
  about the ANCHOR, not about the document, and the refusal and its hint constant are both gone now.
  - The anchor really does carry one line: `joinWithMermaidFences` replaces the whole fence with a
    single placeholder paragraph and attributes every line of the replacement to the fence's opening
    line, so the block arrives as an `mdBlockParagraph` spanning only the ` ```mermaid ` line while
    the ROWS it owns are the art rows the splice put there. `mdPreviewOwnSpanEnd` recovers the
    fence's real extent — opening line to closing fence, inclusive — by scanning forward with
    `joinWithMermaidFences`' own closing rule (`mdPreviewMermaidFenceSpan`: same fence character, a
    marker at least as long, nothing but whitespace after it, `ChangeDivider` rows skipped).
  - ⚠️ The widening happens at EXPANSION time, inside `mdPreviewClipRawSpan`, not where
    `mdPreviewBuildSourceMap` builds the anchor. The deciding fact is that `endLine` has exactly one
    consumer — the expansion pass — so widening the recorded anchor would mean threading the source
    lines into the map builder to change a field nothing else reads, and would leave the map
    claiming a span its own row range was never derived from. If a second reader of `endLine` ever
    appears, the widening belongs in the builder instead.
  - Both fence markers are painted as rows of their own. The pass's whole arithmetic is "one source
    line, one rendered row", and the opening line is where a comment on the diagram anchors anyway.
  - The nesting case needs no special handling, and not by luck: the placeholder is written at
    column 0, so a diagram indented inside a list item breaks out of the item and becomes its own
    top-level block. A diagram inside a BLOCKQUOTE does not exist at all — `> ```mermaid ` does not
    read as a fence opening to `joinWithMermaidFences`, so it is never substituted and renders as an
    ordinary code fence.
  - ⚠️ `TestMdPreviewRawExpansionCorpusKeepsEveryLetter`'s property does not hold for a diagram, and
    the harness now computes that one expectation differently (`mdPreviewCorpusExpected`). The art is
    a drawing produced FROM the definition, not a typesetting of it: the renderer writes text into it
    that appears in no source line — `implements` on a `<|--` edge, `+ChangedFiles...` for a
    truncated member. Measured, not assumed: the classDiagram in
    `docs/plans/20260730-preview-manual-test-plan.md` "loses" exactly three `m` and three `p` on
    expansion, the three synthesized `implements` labels. For a diagram the art's own rows drop out
    of the expectation and the fence's source lines are added to it, which is stronger rather than
    weaker — every row outside the block still has to survive letter for letter, and the definition
    now has to be proven on screen.
- **What the painted map says is expanded beats what the cursor says.** The two can disagree — the
  cursor still carries `expanded` while `mdPreviewExpandBlock` refused, e.g. against a map built at
  another width. Every reader holding a painted map (`moveMdPreviewCursor`'s j/k clamp,
  `reseatMdPreviewCursorInExpandedBlock`, `mdPreviewCursorAfterDelete`) therefore asks
  `mdPreviewSourceMap.expandedBlock`, not `Model.mdPreviewExpandedBlock`: the clamp is clamping
  into that map's stop list, and reading the cursor instead could narrow j/k to one block's stops
  in a frame showing no raw source at all, with no key able to step out. `mdPreviewCollapseRaw` is
  the deliberate exception — it asks the cursor, because it is the escape hatch that clears the
  flag.
- **Annotations splice under the raw line they belong to.** `mdPreviewSourceMap.spliceRow` replaced
  "splice at the block's `endRow`" with "splice at the row the map names for this source line", and
  the painter's anchor shifting became a prefix sum over the splice points. The deciding case is
  the live input: without it, pressing `a` on raw line 4 of a thirty-line table puts the box you
  are typing into below raw line 30.

**Test-only, mechanical, not part of the feature itself:**

- `app/keymap/keymap_test.go` — asserts `P` resolves to `ActionTogglePreview`
- `app/ui/view_test.go` — one pre-existing test (`TestModel_StatusBarFilenameTruncationWideChars`)
  had its pinned `m.layout.width` bumped from 45 to 47 because the new status icon consumed the
  test's last 2 columns of slack (see the plan's Task 4 ⚠️ note — not a bug, the test was
  already at its limit)

### Most likely rebase conflict sites

`app/ui/diffview.go` and `app/ui/model.go` are the most actively developed files upstream and
the most likely to conflict — this was flagged going in (see the plan's "Patch discipline"
section) and confirmed empirically: `model.go` alone carries 6 of the patch's 25 existing-file
hunks (keymap 8, model 6, loaders 2, diffview 1, mouse 3, view 4, diffnav 1).

## Mermaid diagram type coverage

The vendored `mermaid-ascii` renderer only understands `graph`, `flowchart`, and
`sequenceDiagram` natively. The patch widens that in two different ways — one adds new diagram
types, the other fixes the two the renderer already claimed to support:

- **Transpiled to `flowchart` source first:** `classDiagram` and `stateDiagram-v2`/`stateDiagram`
  (see `transpileMermaid` in `mdpreview_transpile.go`).
- **Normalized in place:** `graph` and `flowchart`. These are **no longer a pure pass-through.**
  They now go through `normalizeFlowchartSource` in `mdpreview_flowchart.go`, which rewrites the
  constructs the vendored parser silently mis-parses, drops the directive lines it cannot draw,
  and copies everything else through byte for byte. See "graph/flowchart normalization" below.
- **Native, untouched path:** `sequenceDiagram`. It takes a completely different code path inside
  the vendored library and is pinned as byte-identical to a direct `RenderDiagram` call.
- **Still fall back to the verbatim fence text:** `erDiagram`, `gantt`, `quadrantChart`, and any
  other/unrecognized fence language. Measured against a real corpus, these three types covered 45
  non-rendering fences before the transpiler; `erDiagram` never actually appeared in that corpus.
  This stays out of scope on purpose — see the diagram-transpile plan's Overview for the
  fence-count breakdown.

### graph/flowchart normalization

The vendored parser reads exactly three shapes — `Id`, `Id[label]`, and `lhs --> rhs` with an
optional `-->|label|`. Anything else is mis-parsed silently, so the old pass-through shipped
broken art for most real fences. Measured 2026-07-29 over the 138 unique `graph`/`flowchart`
fences in the author's document corpus (every `.md` under `~/.claude/plans` and `~/dev`,
excluding vendor/node_modules), **86 of 138 contained at least one mis-parsed construct**, and
**76 of 138 actually drew at least one stray box** — a box whose label is a whole source line.
What `normalizeFlowchartSource` rewrites, and what each one did before:

- **Shape suffixes → square brackets.** `A{d}`, `A(d)`, `A([d])`, `A((d))`, `A(((d)))`,
  `A[[d]]`, `A[(d)]`, `A{{d}}`, `A>d]` all become `A[d]`; the node id is untouched. Before, a
  shape suffix split ONE node into two boxes (a `B` box and a separate `B{d}` box) with the
  edges divided between them. 45 corpus fences used a diamond, 9 a round/stadium shape.
- **Link variants → `-->`.** `---`, `-.-`, `===`, `-.->`, `==>`, `--o`, `--x`, longer dash runs,
  and the inline-labeled forms `-- text -->` / `-. text .->` / `== text ==>` (the label carried
  over as `-->|text|`). `<-->` stays bidirectional. Before, none of these matched any parser
  pattern, so the whole line became one box literally named `A --- B`. 12 corpus fences used
  `---`, 12 used `-.->`, and ~40 an inline-labeled form.
- **`|` inside a node label → `/`.** The edge pattern's label group is greedy, so one stray pipe
  in a label swallowed half the line: `A -->|go| B["x=with|without"]` captured `go| B["x=with`
  as the edge label and left `without"]` as the target node.
- **Quotes around an edge label → dropped.** `A -->|"listVariants (strict mode)"| B` becomes
  `A -->|listVariants (strict mode)| B`. Mermaid quotes a label to protect the spaces in it, a
  job the `|...|` delimiters already do here, and the vendored renderer draws a label verbatim —
  so the quote marks showed up in the art. `flowchartLinkText` already unquoted a label, but only
  for the labels it BUILDS, the ones written in the inline `-- label -->` form. A label written
  the common way, straight after the arrow, matches neither `flowchartLinkPattern` alternative:
  the arrow matches the bare-link alternative and the `|...|` after it was copied through
  untouched. The rewrite happens in the node pass, on each arrow segment that pass already
  consumes whole (`normalizeFlowchartEdgeLabel`). Exactly one layer comes off, and only when the
  label opens and closes with a quote and carries none inside — so `|"a" and "b"|` keeps both
  quote layers rather than being half-eaten, and the pass stays a fixed point, because what it
  writes back can never be stripped a second time. Declining to unquote does NOT decline the
  no-break-space substitution: the two decisions are independent, and a label that keeps its
  quotes bleeds `─` through its spaces exactly like any other.
- **Styling and layout directives → dropped.** `style`, `classDef`, `class`, `linkStyle`, a
  standalone `direction` (the one written inside a `subgraph`), and the interaction directives
  `click` / `href` / `callback`. Each described how the diagram should look or behave, which the
  ASCII renderer cannot express, and each drew a box named after the whole line: the vendored
  `classDef` pattern is anchored at column 0 so an indented one never matched, and none of the
  others match any pattern at all. This was the largest single defect in the corpus — `style` in
  36 fences, `classDef` in 33, `class` in 17, `direction` in 16, `linkStyle` in 7; 249 dropped
  lines in total. Dropping every `classDef` also closes a crash: `parseStyleClass` splits each
  declaration on `:` and indexes the second field blindly, so a column-0
  `classDef x stroke-dasharray: 5 5` panicked the vendored parser and took the whole fence down
  to the verbatim fallback. The one thing given up with it is label-text color, which a column-0
  `classDef` gives to nodes carrying a `:::className` suffix — no corpus fence does that, and
  keeping only the column-0 spelling would make the pass's output depend on indentation.

A node named after one of those keywords is NOT a directive and is never dropped:
`style-review --> x`, `classDefault --> y`, `direction --> up`, `class[Class registry] --> B`.
The rule is the shared `mermaidDirectiveShape` discriminator (see `mdpreview_transpile.go`),
reached through `flowchartDirective`, which masks labels first (so a keyword id carrying a label
cannot look like a keyword followed by arguments) and runs the link rewrite first (so the
discriminator's arrow test sees a `-->` whatever link form the author wrote — it does not know
`===` or `-.->`). That discriminator is the one place this decision is made for all three
diagram types; do not add a keyword test anywhere else.

Everything else is copied through unchanged: `subgraph`/`end` blocks, `accTitle`/`accDescr`
(unrenderable too, but they carry author prose rather than presentation, so removing them would
delete text), `<br/>` in labels, chained `a --> b --> c`, `%%` comments (whole-line and inline
tails), and node ids. Line order and indentation are preserved, the line count is preserved
except for the dropped directives, and the pass is a fixed point on already-well-formed source.
When dropping would leave a fence with no statement at all, the original source is returned
instead, so this pass can never be the reason a fence disappears from the preview.

**It is a text-level pass, not a rebuild through `flowchartBuilder`, and that is deliberate.**
41% of these fences use `subgraph`, which the builder has no concept of — re-emitting through it
would drop every grouping box and replace author node ids with synthetic `n0, n1, ...` ids that a
`subgraph` body cannot reference. Fixing one fifth of the corpus by regressing two fifths is not
a trade worth making.

Residual after the pass: **2 of 138 fences**, down from 76, still draw a stray box — both from
the `~~~` invisible link, which is deliberately not normalized. It exists purely for layout, and
the renderer has no invisible edge, so turning it into `-->` would draw a relationship the author
explicitly hid. Two smaller leaks are known and accepted: an `accTitle`/`accDescr` line would
still draw a box named after itself (no corpus fence has one), and mermaid's `%%{init: ...}%%`
theme block is read as an ordinary `%%` comment and simply ignored.

### Mermaid diagram subgraph splitting

When a flowchart contains multiple independent `subgraph` blocks, the vendored ASCII renderer
does not lay them out separately — it builds a single node grid and attempts to draw a rectangle
around the cells each subgraph occupies, resulting in overlapping borders and nodes drawn in the
wrong boxes. To fix this, diagrams that meet the split criteria are rendered once per subgraph,
with each result stacked under its own title and rule line (see `renderMermaidSource` in
`mdpreview_transpile.go` and the split logic in `mdpreview_subgraph.go`).

**The split fires on structure alone, not on a detected defect.** Every fence meeting the five
conditions below is stacked, including the ones the combined render would have laid out
correctly. That is the wanted output, not a side effect: separately titled blocks are what a
reader asked for, and each block is narrower than the combined render, so a wide diagram becomes
readable. Gating on an actual overlap was considered and rejected — it would mean rendering every
fence twice just to decide, and it would make the output depend on the vendored renderer's
current grid heuristics.

**All of the following conditions must hold for a split to be safe. If any one fails, the fence
renders exactly as it does today:**

1. The diagram is a `graph` or a `flowchart`. No other diagram kind has subgraphs.
2. There are at least two top-level subgraphs. With one subgraph the renderer draws a single
   rectangle and nothing overlaps.
3. No subgraph is nested inside another. Nested subgraphs would fragment during the split and
   lose their grouping.
4. Nothing but the header, comments, blank lines and layout directives (`direction`, `style`,
   `classDef`, etc.) sits outside a subgraph. A node declared outside would be dropped by
   splitting.
5. **No node id is mentioned by more than one subgraph.** This catches edges crossing from one
   subgraph to another, and also nodes that two subgraphs both reference. Both cases would either
   lose an edge or silently duplicate a box; a single rule covers both and is easy to check. A
   subgraph's OWN id counts as a node id here, because an edge may point straight at a whole
   block (`A --> groupB` is valid mermaid) — without that, the pair looks disjoint and the split
   draws a stray box literally named after the other subgraph.

Rule 5 only works if both sides read an id the same way. A block's own id is claimed as one
whole string, so the walk over the bodies has to produce that same string. What may be in an id
is `mermaidIdentRune`, the rule the transpiler already uses to find where a directive keyword
ends: letters, digits, `_`, and the three punctuation characters real diagrams write inside
names — `-`, `.` and `/`. Each of those three stays inside the id only when another identifier
rune follows, which keeps `after-state`, `svc.a` and `after/state` whole while still ending the
id at the `-` of `A-->B`. The walk reads runes, not bytes, so a Cyrillic id comes back whole and
a non-Latin punctuation mark between two ids (`A·B`, an em dash) ends the first one instead of
gluing the pair into a string that matches neither half. A `:::className` suffix is skipped up to
the end of the class name, not up to the next space, so a statement written tight against it
(`A:::hot-->B`) still shows every id after the suffix.

Three malformed shapes refuse as well, and none follows from the five conditions: an unterminated
`subgraph` (no closing `end`), a stray `end` with nothing open, and a `subgraph` or `end` line
carrying a second statement behind a `;` separator (`end; B2 --> A1`). Mermaid accepts `;` as a
statement separator, and the structural dispatch reads such a line as the keyword alone, so the
tail would belong to no block and rule 5 would never see the node ids in it — a fence whose only
crossing edge is written that way would split and lose it. The vendored renderer does not split
statements on `;` either, so the single-render fallback these lines drop to is exactly as good as
it was. A `;` inside a label, and a trailing `;` with nothing after it, are not separators.

Rendered art is spliced into the document after glamour has run, bypassing glamour entirely.
C0 control bytes and DEL are dropped from it (newline and tab kept) before it is spliced, so an
ESC written into a node label — or sitting in the fence text used as the verbatim fallback —
cannot reach the terminal as a live escape sequence. Box-drawing glyphs are multi-byte UTF-8 and
are never touched by that filter. This filter is scoped to the mermaid art only, not the whole
document — see "Known limitations" below for what is still unfiltered.

When any condition fails, the fence falls back to a single render of the whole source — today's
behavior. The fallback is all-or-nothing: a part that cannot render, renders blank, or panics
is caught, and the whole fence is re-rendered as one unmodified piece through the existing
single-render path.

Measured over a corpus of 300 mermaid fences in the author's document set: 89 use `subgraph`, of
which 23 have an edge crossing between two subgraphs and 30 nest subgraphs. A meaningful share
keeps today's behavior, so the fallback is a correctness guarantee, not an edge case.

## Known limitations

These are accepted, documented gaps in the preview mode — not bugs to fix under patch discipline.

- ~~**Wide mermaid diagrams are clipped in preview; widen the terminal to see them.**~~ **FIXED —
  the left/right arrows now pan the preview** (`scroll_left` / `scroll_right`, see the
  "Horizontal panning wiring" hunk map above). The art is still never re-wrapped or truncated to
  fit — that anti-reflow design in `renderMarkdownDocument` is unchanged — but the render is now
  cut to a visible column window at `m.layout.scrollX`, with the same `«` / `»` overflow
  indicators the diff pane uses, so every column of a wide diagram is reachable. Measured on a
  real 206-cell `flowchart TD` at a 120-column pane: the third subgraph starts past column 120
  and is unreadable at offset 0, and the pan clamps at offset 86, which puts the diagram's last
  column at the right edge. **Those numbers predate subgraph splitting and describe a fence the
  split declines.** A three-subgraph fence that meets the five split conditions is now drawn as
  three stacked blocks, each only as wide as its own nodes, so it no longer needs panning at all;
  re-measure against a declined fence before quoting a width again. What remains true for any
  render still wider than the pane: only whole columns move, so a box straddling the edge is
  still cut mid-glyph until you pan past it, and prose (already wrapped to the pane by glamour)
  goes blank once you pan past its end.
- ~~**Spaces inside an edge label render as `─` (dash) on the arrow line.**~~ **FIXED — every
  edge-label space is now a no-break space, not a plain one** (see
  `docs/plans/completed/20260805-mermaid-edge-label-rendering.md` and `mdpreview_nbsp.go`'s doc comment).
  U+00A0 is not the byte `" "`, so `mergeDrawings` in the vendored renderer treats the cell as
  opaque and keeps it instead of letting the arrow line or a crossing edge bleed through, while
  terminals still draw it as a blank. Four costs are accepted in exchange: art copied out of the
  terminal carries no-break spaces rather than plain ones; a small number of fonts render U+00A0
  visibly instead of blank; on the plain-flowchart path a run of spaces inside a label collapses
  to one, so deliberate spacing is lost; and on that same path the label is one byte per space
  wider than before, because the vendored renderer reserves an edge label's column width with
  `len()` and U+00A0 is 2 bytes where a space is 1 (`A -->|"read as fallback"| B` comes out 23
  columns against 21 for `read_as_fallback`, and is drawn a column left of true centre). The
  transpiled path pays neither of the last two — it already substituted a 2-byte `·`, and
  `mermaidSafeText` had already collapsed its whitespace runs. Measured across the corpus (see
  "Corpus verification result" under Progress Tracking in the plan above): the substitution
  changed the rendered art of 83 of 231 fences.
- **A decision node with three or more labeled out-edges can still put two labels where the
  reader cannot tell them apart, when the diagram's direction cannot be flipped, the flip
  doesn't help, or the flip would blow a diagram that fitted the pane up to more than three
  times its width.** The
  complete fix is in the vendored `drawTextOnLine` (reserve occupied cells, nudge the label
  along its line), which would mean forking `mermaid-ascii`. Instead, `renderMermaidSource`
  renders once, counts collisions with `mermaidCollisionCount` (`mdpreview_collision.go`), and
  keeps a second render with the direction flipped to `LR` only when ALL of: the source has a
  header `mermaidFlipDirectionToLR` accepts — `TD`, `TB`, or a bare `flowchart`/`graph` with no
  direction at all, which the renderer lays out top-down and which the flip fixes by inserting
  the keyword (`RL`/`BT`/`LR` are left alone, and so is a missing or malformed header); the
  first render collides at least once; the flipped render succeeds and is non-blank; the flip
  does not blow a diagram that fitted `paneWidth` up to more than `mermaidFlipBlowupRatio`
  (3.0) times its width (`mermaidFlipWidthAcceptable`, whose rule in columns is
  `mermaidWidthTradeAcceptable`); the flipped render collides STRICTLY FEWER times than the
  first. The header is tested before
  the collision count, so the label scan never runs on a source whose `|...|` is not an edge
  label at all. Any gate failure keeps the first render, so a fence with no collisions never
  renders twice and every failure path degrades to today's output — including a panic in the
  second render, which `mermaidRetryLRIfColliding` catches itself rather than letting it reach
  `renderMermaidBlock`'s outer recover, whose fallback is the fence's raw source text.
  **The detector counts two collision shapes**, and needs both: two surviving labels crowded
  into one corridor, and one label painted over another so neither survives as a word
  (`collection` over `single composite` leaves `sincollectionite`). The crowded shape is flagged
  when two distinct labels on one row are closer than the smaller of either label's own length
  and 8 columns — a label's own length is in the threshold because `no` and `yes` five columns
  apart read as separate words while `collection` and `single composite` four columns apart do
  not. The overwriting shape is flagged when a matched label has leftover letters against it
  that spell part of a different label. A match is otherwise dropped when it overlaps a longer
  label's span or butts against text unrelated to any label — but that drop is not reliable: see
  the next entry for the measured case where ordinary node text is caught anyway, because
  "unrelated to any label" is checked against every OTHER label's text, not against whether the
  row has an edge label on it at all.
  **Why the width guard is a ratio and not a fit test.** It used to decline any flip that took
  a diagram fitting `paneWidth` and made it not fit, and that guarded the wrong thing. The
  reader can pan (`scroll_left` / `scroll_right` are both on `mdPreviewAllowedActions`), so a
  wider diagram costs keystrokes, while a diagram whose labels are painted over each other
  cannot be read at any pane width and panning does not bring it back. The guard exists only to
  bound the damage when the DETECTOR is wrong — a phantom collision sending a readable diagram
  off to several times its size for no gain — so it has to trigger on absurdity, not on width.
  The fit-only form suppressed the feature's headline fix inside a window of pane widths, which
  is what the ratio replaces it for. Measured on `testdata/mermaid/collision-fitting-td-render.mmd`
  (the section-1 fence of the author's sample plan) on 2026-08-05:

  | pane width | kept art width | is the label intact? |
  |---|---|---|
  | 80, 120 | 238 (flip kept) | yes |
  | 160, 200 | 150 (first render kept) | NO — the row reads `├◄───collectioningle composite──────────────┤`: `collection` painted over `single composite`, the `s` destroyed |
  | 240, 300, 400 | 238 (flip kept) | yes |

  At 160 and 200 the top-down render is 150 columns, so it FITS, so the old guard declined the
  flip and left the wrecked art on screen; below 160 the first render did not fit and above 200
  the flip did, so both ends were fine and only the middle was broken. That fence widens by
  238/150 = 1.6x, while the phantom-collision blowup the guard was added for widens by
  330/71 = 4.6x, so 3.0 separates them with more than a factor of margin on each side
  (`TestMermaidFlipBlowupRatio_SeparatesTheMeasuredCases` fails if a later edit moves the
  constant near either).
  **Measured across the corpus** (15254 markdown files, 233 distinct fences — the list is a live
  snapshot of the author's disk, 231 when the retry shipped — with the old fit-only gate and the
  ratio gate run side by side in one binary, same detector over both outputs, on 2026-08-05):
  at pane width 120, 6 fences still carried a colliding label under the fit-only gate and 3 do
  under the ratio gate; 3 fences change their kept render, all of them from the first render to
  the flip: 103 → 150 columns (1.46x, 2 collisions → 0), 100 → 121 (1.21x, 1 → 0) and 59 → 170
  (2.88x, 1 → 0). Each was checked by eye and each is a real collision, not a phantom: the three
  first renders carry `├◄ddecision==abortrove`, `├◄──materializeeted` and
  `an agent acknowledges───────Publish Selected`. No fence in the corpus widens past the ratio
  as a result of this change — by construction it cannot, since the ratio is the gate. At pane
  width 160 the same run gives 8 colliding under the fit-only gate against 3 under the ratio
  gate, with 5 fences changing their kept render (the three above plus 151 → 260 and 142 → 173).
  The 3 left at either width are 2 that take the subgraph-stacked path, which returns before the
  retry, and one 115 → 328 fence whose flip collides just as often as its first render, so the
  strict `flippedCount < firstCount` comparison declines it and the width guard never decides it
  at all. (An earlier note here counted that fence among "4 declined by the width guard"; that
  was true of the gate ORDER — the width gate runs first — but only 3 of those 4 were actually
  held back by width.) Widths on the flips that were already kept before this change, at pane
  width 120: 137 → 79, 135 → 111, 121 → 112, 155 → 159, 57 → 86, 151 → 260, 148 → 279,
  207 → 281 (the repo's own `collision-three-branches.mmd` fixture) and 136 → 286.
  A fence already written `LR`
  that still collides is not helped — there is no further direction to try. The cost of a fence
  that DOES collide is two full renders every time it is drawn, and preview has no render cache
  by design (see `renderMarkdownPreview`'s doc comment), so a pan keypress across such a
  document pays both.
- **The collision detector itself is inaccurate in both directions, and this was found and
  accepted before shipping, across three review rounds that each reproduced it by running the
  real functions — not discovered later.** It misses real collisions and it invents ones that
  are not there.

  It misses short labels. `mermaidLabelFragmentMinRunes = 3` treats a leftover overwrite run of
  two runes or fewer as ordinary node text, not as wreckage, so a short label painted cleanly
  over another counts zero. A real `flowchart TD` with branch labels `yes`/`no`/`hold` renders
  the row `│ Is it ready? ├─holdo─┬───┐` — `hold` painted over `no`, leaving only the `o` — and
  `mermaidCollisionCount` returns 0 for it, so the retry never fires, even though the LR render
  of that same fence is both collision-free and narrower (30 columns against 43): a strictly
  better render, silently discarded. Reproduced the same way on `aa`/`bb`/`cc` → `├─ccb─┬──┐`,
  `aaa`/`bbb`/`ccc` → `├─cccbb┬──┐`, and `aa`/`bb`/`cc`/`dd` → `├─ddb─┬──┐`. A leftover run of 0,
  1 or 2 runes counts zero every time; one of 3 or more counts one.

  It also misses multi-word labels — exactly the labels the no-break-space half of this change
  exists for. `mermaidNeighborRun` stops at the first non-word rune, and a multi-word label's own
  no-break spaces ARE non-word runes, so an overwrite of a multi-word label breaks into runs of
  0-2 letters on both sides and is indistinguishable from an undamaged draw. `yes` painted over
  `a b c d` at all five possible offsets (`yes c d`, `ayesc d`, `a yes d`, `a byesd`, `a b yes`)
  counts zero at every one.

  In the other direction, it invents collisions on rows that carry no edge label at all.
  `mermaidClassifyHit` calls leftover letters an overwrite whenever they happen to be a substring
  of some OTHER edge label anywhere in the fence, with no check that the row it is looking at is
  an edge-label row in the first place. With labels `no` and `nothing to do` present in one
  fence, the plain art row `│ Queue drained: nothing pending │` counts 1: `no` matches inside
  `nothing`, and the leftover `thing` happens to be a substring of `nothing to do`. A 10-node
  diagram with one repeated word reported 9 collisions with zero real ones.

  It is also inconsistent by construction: `mermaidCollisionLimit`'s `min(len(a), len(b), 8)`
  makes detection sensitivity scale with label length, roughly 8x between the shortest and
  longest labels measured. Smallest gap still reported CLEAN, measured pair by pair: `a`/`b` at
  1 column apart, `no`/`yes` at 2, `add`/`del` at 3, `open`/`close` at 4 — against 8 columns for
  the `collection`/`single composite` pair the cap was built for. Two 2-character branch labels
  two columns apart in one arrow corridor (`├◄──no──yes──┤`) are reported collision-free.

  Below the reporting bar but worth recording alongside these: `mermaidPipedEdgeLabel`'s
  character class excludes `"`, so a quote-carrying label such as `|"a" and "b"|` cannot match
  and is dropped from the label set entirely — if a fence has exactly two labels and one of them
  is quote-carrying, collision detection is disabled for that whole fence.

  **Blast radius.** A missed collision is not a regression — it leaves today's output exactly as
  it was before this feature existed. A phantom collision costs one extra vendored render (about
  9ms, see the corpus note above) and can only ever fire on a fence the detector itself believes
  collides — a clean fence's bytes never change. Even a wrongly-triggered flip is still bounded by
  `mermaidFlipWidthAcceptable`: it cannot blow a fence that fit the pane up to more than three
  times its width, the same guard that bounds a correctly-triggered one. A phantom collision can
  now cost a fence that fit the pane its fit, up to that ratio — that is the deliberate price of
  the ratio rule, paid so a real collision inside the same window is fixed rather than left
  wrecked. What a phantom collision CAN do is win the strict
  `flippedCount < firstCount` comparison on noise rather than on a real fix, since that comparison
  is made on counts that can themselves be wrong in either direction.

  **Direction for a real fix.** The detector's premise is "find each label in the art and measure
  the gap between hits," and that premise is what produces errors in both directions at once: the
  gap measurement is unreliable for short and multi-word labels, and matching a leftover run
  against *any* label's text — rather than against evidence that a specific label was actually
  destroyed — is what lets ordinary node text pass as a collision. Better evidence is that a
  specific SOURCE label does not appear intact anywhere in the art: that test cannot fire on node
  text at all, because node text was never a source label to begin with, and it does not depend on
  how long the label is or how many words it has.
- **Long node labels are wrapped, but only on a fence whose art already overflows the pane.** A
  box is as wide as its widest label line, so one long label drags the whole drawing past the
  pane and the reader has to pan to read a diagram that would otherwise fit. The renderer already
  honours `<br/>` inside a node label (`newGraphLabel` in the vendored `label.go` splits on it),
  so `mermaidNarrowIfOverflowing` (`mdpreview_wrap.go`) re-renders the source with long node
  labels broken over several lines. It runs LAST, after the LR retry, and wraps the source in
  whichever direction that retry kept — the retry is a correctness pass (labels painted over each
  other cannot be read at any width), the wrap only a readability one (a wide diagram is readable,
  it just costs panning), so the wrap must never be able to undo a flip.

  **Why the overflow gate.** Unlike the LR retry, which only fires on a fence the detector already
  believes is broken, wrapping would otherwise change every diagram carrying a long label,
  including the great majority that render perfectly today. So the art is measured first and a
  fence that FITS `paneWidth` returns byte-identical, without a second render
  (`TestRenderMermaidSource_FittingFenceIsByteIdenticalToTheUnwrappedRender`). Past that gate,
  each rung of `mermaidWrapTargets` — a quarter of the pane, then an eighth, both clamped into
  16..34 runes — is rendered widest-first, and a rung's render is kept only when it is STRICTLY
  narrower than the art it replaces and does not collide MORE times (same `mermaidCollisionCount`
  the retry is gated on). The first rung that fits the pane wins outright, so the fewest labels
  are broken; if none fits, the narrowest candidate is kept, since the reader still pans across
  fewer columns. Two rungs is the whole ladder because each costs a render.

  **What is never wrapped**: a label that already contains an author line break (`<br>` in any
  spelling, a literal `\n`, a real newline) — that is the author's own line breaking; an EDGE
  label — the renderer does not honour `<br/>` there, it prints the five characters literally, and
  the walk consumes each arrow together with its `|label|` before any byte can be read as a node
  shape; a single word, however long — a broken identifier or path reads worse than a wide box;
  a subgraph header or an `accTitle`/`accDescr`; and any source whose diagram kind is not
  `graph`/`flowchart`. Breaks are taken on ASCII space and tab only, never on U+00A0, so an edge
  label that somehow reached the wrapper could not be broken anyway (`strings.Fields` would split
  on U+00A0, which is why the split uses its own predicate).

  **Measured on `testdata/mermaid/collision-three-branches.mmd`** at pane 160, where the LR retry
  has already won and so LR is the direction wrapped. The same target buys back very different
  width in the two directions, which is why the targets are a ladder rather than one number:

  | target | TD width | LR width |
  |---|---|---|
  | none | 207 | 281 |
  | 40 | 157 | 229 |
  | 34 | 136 | 212 |
  | 28 | 122 | 188 |
  | 22 | 106 | 164 |
  | 20 | 101 | 150 |
  | 16 | 90 | 135 |

  In production that fixture goes 281 → 150 columns at pane 160 (the second rung, 20, wins; the
  first rung's 212 is kept as the running best until it does), 25 → 39 rows, 0 collisions before
  and after. Height roughly doubles, and that is the intended trade: the preview scrolls
  vertically for free while sideways it moves one column per arrow-key press. The renderer adds a
  blank row between label lines (`graphLabelLineGap`), so a two-line label costs three rows.

  **Measured across the corpus** (15254 markdown files, 234 distinct fences, pane width 120, base
  `681f053` against this change, 2026-08-05): **43 of 234 fences change, all 43 narrower, 0
  wider**; width delta min 2, median 22, max 146, mean 37.8 columns; 14 fences that overflowed
  the 120-column pane now fit inside it. **0 fences gain a collision** — the number the feature
  ships or does not ship on. 0 panics, 0 timeouts, 0 blank renders and the same 24
  unsupported-diagram-type errors on both builds, and no word visible in the base art disappears
  from the new art on any of the 43. Of the 102 fences that overflow the pane on the base build,
  44 have no wrappable label at all (short labels, or the author's own `<br/>` already), 14
  produce no rung narrower than what they had, and 44 keep a wrapped render — 29 of those on the
  second rung. The collision veto is not dead code: on one fence the first rung narrowed 277 → 198
  columns while cramming `nothing left: whole-list write` and `index or '-': item add/remove/reorder`
  into one corridor, and was declined for it.

  **The cost** is up to two extra vendored renders per overflowing fence, every time the fence is
  drawn — preview has no render cache by design (see `renderMarkdownPreview`'s doc comment), so a
  pan keypress across such a document pays them again. A fitting fence pays nothing.

  **Limitations.** A wrap cannot fix a diagram whose width comes from its node COUNT rather than
  its label lengths: 14 corpus fences overflow with labels short enough that no rung is narrower.
  A fence whose labels are all single long words (an identifier, a path, a URL) is declined
  outright. The floor of 16 runes is a real floor — on a very narrow pane the art still overflows,
  and the pass keeps the narrowest candidate rather than promising a fit. And the height cost is
  unbounded in principle: a diagram with many long labels can grow past a screenful, which is
  cheap to scroll but does mean the whole diagram is no longer visible at once.
- **TOC active-section highlight is stale during preview** — see the `app/ui/view.go` note under
  "Review phase 4 wiring" above.
- **You must leave preview before changing file.** Preview now works in a multi-file review, but no
  file-changing key is on the allowlist: `n`/`p` (`next_item`/`prev_item`), tree focus, and tree
  navigation all stay blocked, and a mouse click in either pane is read-only while previewing. So
  reading two markdown files in one review is `P`, read, `P`, `n`, `P`. The mode itself survives a
  file switch correctly (it stays on for another full-context markdown, and `handleFileLoaded`
  switches it off for anything else) — there is simply no key that performs the switch from inside
  preview. Allowing `n`/`p` was considered and rejected: with a search still live (matches survive
  into preview until esc or the next file load) they navigate search matches instead of files,
  which writes `m.nav.diffCursor` and jumps the viewport in diff-line coordinates the preview
  render does not have. See the comment above `mdPreviewAllowedActions`.
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

  The overflow itself is no longer a dead end: horizontal panning (the item above) reaches the
  clipped part, so a too-wide transpiled diagram is now readable on a narrow pane, just not in one
  screenful. The cap is still worth keeping — fewer pan presses — but it cannot promise a fit. Two
  tests pin the honest
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
  to 257, and one fence that currently fits stops fitting. Horizontal panning has since landed,
  which changes the trade-off — a wider diagram is now readable, it just takes pan presses — so
  raising the floor is a live option rather than a blocked one. It is not done here: it is a
  separate change with its own re-measurement over the corpus. The other candidate fix, a
  disambiguating suffix on colliding labels, still needs its own plan. The truncation does at
  least always keep its `...` ellipsis, so a truncated label is never mistaken for a complete
  one.

  A related, separate case: some collisions do not come from the width cap at all. `resolve
  {outcome}` and `resolve {outcome} (act without claiming)` both render as `resolve` because
  `mermaidCutParenthetical` and the brace cut are content rules that run regardless of width. That
  shortening is deliberate (see the plan's "Edge label" section) and is not affected by the cap.
- ⚠️ **`--no-colors` never aligns the preview annotation source map — the whole feature is absent
  in that mode, not degraded at the edges.** Measured 0 of 59 real documents (the task 3 spike
  corpus). With colors off, glamour's ASCII style writes a heading's marker on the row AFTER its
  text as well as on it, and a task item's marker reappears on the next list row, so the marker
  sequence stops being one-per-block and `mdPreviewAlignRows` refuses every document it sees. In
  that mode preview renders exactly as it did before this feature existed: read-only, no
  highlight, no painted annotations, `a`/click do nothing. This is a whole-mode gap, not an edge
  case — it is the single most important limitation to know about before assuming annotate-in-preview
  works everywhere `P` does.
- **A table is one annotation target, not one per row.** One real corpus document produced 561
  markers for 6 tables, 73 landing mid-row; a comment on a table means "this table", not "this
  row" — see `mdPreviewAlignRows`' `takeTable` in `mdpreview_srcmap.go`. A consequence: two tables
  separated by nothing but a blank line produce one indistinguishable run of table markers, so
  that document fails alignment and degrades entirely rather than mis-anchoring one table's
  comment onto the other (`TestMdPreviewSrcMap_AdjacentTablesDegrade`).
- **A blockquote is one annotation target too — its inner paragraphs get no separate anchors.**
  This was a task 2 deviation from the plan's own granularity list, which did not call this out
  explicitly; it follows the same "one target per swallowing construct" treatment the spike
  established for tables, and keeps the target list a clean partition of the document (see the
  task 2/task 3 decision log entries on `mdPreviewQuoteParagraphs`).
- **Anchoring is block granularity, not character-exact.** A comment on a wrapped paragraph
  anchors to the whole paragraph, not the line or word under the cursor at the moment of wrapping
  — matching how the same paragraph would be commented in source view before this feature existed,
  where the paragraph's block boundary is already the finest resolution.
- **Two annotation actions are still blocked in preview: `@` and `}`/`{`.** Creating, editing,
  reading, deleting, and flushing annotations all work inside preview; listing them in the `@`
  popup and stepping between them still require leaving preview first (press `P`, act, press `P`
  again). Neither is on `mdPreviewAllowedActions`' allowlist — see that map and its doc comment in
  `mdpreview.go` for the per-action reason. `d` used to be the third entry here, which made a
  comment created in preview impossible to undo from inside preview; it is now allowed, because an
  annotation is a cursor stop of its own (see "Preview cursor" above).
- **Clearing an annotation's input does not delete it — that is what `d` is for.** `a` on an
  annotation pre-fills the existing comment, but clearing the input and confirming runs
  `cancelAnnotation`, which leaves the stored comment untouched. This is the diff pane's own
  behavior, unchanged.
- **A thematic break (`---`) has no position of its own and is recovered by a gap scan.** goldmark
  appends nothing to a `ThematicBreak`'s `Lines()`, so `mdBreakResolver` (`mdpreview_blocks.go`)
  bounds it between the nearest siblings that DO carry a position and takes the first line in that
  gap which looks like a rule (`looksLikeThematicBreak` — three or more matching `-`/`*`/`_`,
  ignoring leading whitespace and any `>` quote markers). The candidate test is what makes the gap
  scan correct on ordinary documents rather than only on isolated breaks: `Lines()` on a fenced code
  block covers the CONTENT only, so a `---` after a fence has the closing ``` inside its gap, and a
  break inside a blockquote has a bare `>` continuation in its. The earlier "first non-blank line"
  rule anchored to those, with `Aligned=true` — a wrong source line in the `-o` output and nothing
  to warn on. Resolved lines memoize per node, because a run of consecutive breaks resolves each
  break through the one before it (800 consecutive breaks took seconds without the memo, on the
  bubbletea `Update` goroutine). A break whose gap holds no rule-looking line resolves to nothing,
  which fails alignment and degrades the whole document — the correct outcome, since the alternative
  is anchoring to a line that is not the break.
- **A click inside an expanded block, on a row that carries no line anchor, collapses the
  expansion.** `mdPreviewClickDiff` resolves a clicked row to a raw source line by exact row
  equality (`srcMap.lineAt`). Two kinds of row inside an expanded block have no anchor to find: a
  blank source line, which paints a row but never gets one because the highlight skips blank rows,
  and a comment row spliced between the raw lines. Both fall through to `anchorAtRow`, which
  resolves to the block — and a block placement writes a fresh cursor state, so the block collapses
  under the click. Accepted rather than fixed: keeping the fallback unchanged is what makes a click
  behave the same everywhere else in preview, and the reader can re-expand with `r`.
- **Expanding a container shows only its own source, up to the first nested block.** A list item or
  blockquote holding a fenced code block, a nested list, a table or a heading has its raw span cut
  at that block's first source line (`mdPreviewClipRawSpan`, see "Raw source expansion" above), so
  pressing `r` on the item shows the lines before the nested block and stops. The lines after it
  belong to the nested block — expanding THAT shows them, because they are rendered into rows it
  owns. The alternative — painting the container's whole span — put the nested block's content on
  screen twice, as source and as render, which is why this is the cut rather than the bug.
- **A container's trailing lines are unreachable when its nested block is a code fence.** A fence
  refuses expansion (`mdPreviewExpandRefusal`: it already shows its source), and it is the block
  whose rows the container's trailing paragraph is rendered into. So in `- para a` / fence /
  `  para b`, the `  para b` line has no raw-line stop in preview and cannot be annotated per line —
  `a` on the fence block anchors to the fence instead. Accepted: lifting it means expanding a fence
  into its own source, which is the one thing the fence refusal exists to prevent. The line is
  annotatable in the ordinary source view with preview off.

  This used to be stated for a mermaid diagram as well, and that half is gone — measured on
  `- para a` / mermaid fence / `  para b`, which produces three blocks (the item, the diagram, and
  `  para b` as a paragraph of its own). Two separate reasons, either one sufficient: the diagram
  now expands rather than refusing, and the placeholder that replaces the fence is written at column
  0, so it breaks the item apart and the trailing paragraph gets its own anchor instead of being
  rendered into the diagram's rows. The second reason held before this change too — the entry was
  wrong about mermaid when it was written, not made wrong by it.
- **A list item whose children are all boundary kinds contributes no target, and degrades the whole
  document.** `swallowedSpan` aggregates only over a container's own direct content, and a nested
  list, table, code block, heading or thematic break is excluded because it gets its own target —
  so an item like `-` followed only by an indented sub-list (`-\n  - deep`) yields no target of its
  own while glamour still writes its item marker. The length mismatch fails alignment, and the
  document degrades to read-only in full, the same all-or-nothing way the adjacent-tables case
  does.

**Preview annotations — corpus measurement (`mdpreview_srcmap_corpus_test.go`, env-gated via
`REVDIFF_MDPREVIEW_SRCMAP_CORPUS`, same corpus definition the task 1-3 spike used: every file
under `docs/plans/completed/`, every file directly under `docs/`, and every `.md` at the repo
root):**

Measured against the current repo (58 documents, one more than the spike's 59-entry corpus minus
its one synthetic handwritten fixture, at pane width 80, colors on):

| metric | result |
|---|---|
| documents aligned | 57 / 58 (98.3%) |
| unaligned | `README.md` (multiple tables among other constructs; not root-caused further — falls under the documented table/alignment-mismatch degrade above) |
| anchors produced across the 57 aligned documents | 9,851 total |

Per-kind anchor counts across those 9,851 (confirms per-item granularity at real-corpus scale, not
just in the unit-test fixtures — `item` alone accounts for two thirds of every anchor produced):

| kind | anchors |
|---|---|
| item | 6,755 |
| paragraph | 1,339 |
| h3 | 727 |
| h2 | 551 |
| enumeration | 199 |
| code_block | 153 |
| h1 | 57 |
| h4 | 28 |
| table | 34 |
| hr | 6 |
| block_quote | 2 |

Re-run with `go test ./app/ui -run TestMdPreviewSrcMapCorpusAlignment -v
REVDIFF_MDPREVIEW_SRCMAP_CORPUS=<path-to-a-file-listing-one-.md-path-per-line>` — the numbers will
drift as the corpus (this repo's own docs) grows or changes; treat this table as a snapshot, not a
promise.

⚠️ **The preview render is non-deterministic for about a quarter of documents — general finding,
not specific to this feature.** The task 1-3 spike rendered the same document twice through the
*unmodified*, pre-existing glamour style config (nothing this plan added) and got different bytes
on 15 of 59 runs. The variation is ANSI-only — `ansi.Strip` equal, same row count, same per-row
`ansi.StringWidth` — and is not mermaid-related. This is why every visual-identity assertion in
this feature's tests (and any future test on this render path) checks `ansi.Strip` equality + row
count + per-row width, never raw byte equality: a byte-identity test on `renderMarkdownPreview` or
`renderMarkdownDocument` would have been flaky from its first run, independently of anything this
plan changed.

- **A subgraph id containing punctuation outside the identifier set can hide a crossing edge from
  the disjointness check.** The identifier rule — letters, digits, underscore, and the three chars
  real names use (`-` `.` `/`) — is applied when the block header claims its id as one string, and
  again when a body reference reads an id from the same source text. If they tokenize differently,
  the edge is missed, and a fence that should not split may split with the edge dropped. Real-world
  ids are alphanumeric, which is why this is left as a limitation rather than fixed. Example: a
  subgraph id `A·B` (containing the middle dot) is claimed whole by the header (`A·B`), but when
  that name appears in a crossing edge like `A·B --> X`, the body walk stops at the `·` and reads
  only `A` as the target, losing the reference in the disjointness check. The split then proceeds
  and draws a stray box instead of refusing. A non-ASCII mark between two id tokens (`a–b` with an
  en dash instead of a hyphen) or an ASCII shape (`a+b`, `a:b`) shows the same gap.

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
`RenderDiagram`, from two call sites: `renderMermaidSource` in `app/ui/mdpreview_transpile.go`
(the whole-source render) and `stackFlowchartSubgraphs` in `app/ui/mdpreview_subgraph.go` (one
call per subgraph block). That package also contains `web.go`, which implements an HTTP server
for the upstream tool's own web mode. Go links a package as a whole, so importing `cmd` at all
drags in everything `web.go` needs, even though nothing in revdiff can ever reach it.

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

### The `P` key collision with upstream's `jump_file`

Upstream added a `jump_file` action (open a file picker) bound to `P` by default. That
collides with this patch's own `"P": ActionTogglePreview` — `go vet`/the Go compiler catches
it immediately as a duplicate map key, so a rebase that pulls in `jump_file` will not build
until this is resolved.

Resolution (already applied on this branch, `app/keymap/keymap.go`'s `defaultBindings()`):
`P` stays `ActionTogglePreview` (this patch's binding, and the muscle memory this whole file
documents), `jump_file` moves to `ctrl+p` instead of its upstream default. `ctrl+p` is
otherwise unbound and — per gotchas.md's file-picker note — is not a bare printable key, so it
correctly toggles the picker closed on a second press (upstream's own default `P` binding
could not do that; see `README.md`'s file-picker paragraph on printable-key filtering, which
still describes upstream's `P` default accurately and is deliberately NOT edited to match this
fork, per the doc-split policy at the top of this file).

Following files carry test/behavior assertions tied to this specific key and need re-checking
on every rebase that touches `jump_file`'s default: `app/keymap/keymap_test.go`
(`TestActionJumpFile_DefaultBinding`, `TestActionJumpFile_RegistrationHelpAndDump`,
`TestActionJumpFile_CustomConfiguration`), `app/ui/filepicker_test.go`
(`TestModel_JumpFileOpensPickerAndLoadsSelection` sends `tea.KeyCtrlP`;
`TestModel_JumpFileKeyFiltersInsideOpenPicker` explicitly rebinds `P` to `ActionJumpFile` to
still exercise the printable-key-filters-not-closes behavior upstream's own default used to
demonstrate), `app/ui/search_test.go` (`TestModel_SearchPrompt_SwallowsTogglePreviewKey`).

### A future `r` collision with an upstream binding

`r` was unbound upstream at the fork base — `defaultBindings()` had `R` for `ActionReload` and no
lowercase `r` — which is why this patch could take it for `ActionToggleRaw` (raw source expansion,
above). If a rebase brings an upstream action bound to `r`, the Go compiler catches it immediately
as a duplicate map key in `defaultBindings()`, exactly as it did for `P` / `jump_file`, so the
build fails rather than one binding silently winning.

Resolution: keep `toggle_raw` on `r` and move the upstream action to another key, unless
upstream's use is the more valuable one — in that case move `toggle_raw` instead. `r` is worth
defending: it is only reachable inside markdown preview mode, but it is the one key the raw
expansion feature has, and it pairs with `P` in muscle memory.

Following files carry test/behavior assertions tied to this specific key and need re-checking on
every rebase that touches an `r` binding: `app/keymap/keymap_test.go`
(`TestActionToggleRaw_IsValid`, `TestActionToggleRaw_DefaultBinding`,
`TestActionToggleRaw_HelpEntry`) and `app/ui/mdpreview_expand_test.go`
(`TestMdPreviewToggleRaw_ThroughTheKeyPath` sends the `r` key rather than dispatching the action
directly; the other `TestMdPreviewToggleRaw_*` tests call the handler and survive a rebinding).
