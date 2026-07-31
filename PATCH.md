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

A clean rebase never conflicts on these eight files — they don't exist upstream. All conflict
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

**Test-only, mechanical, not part of the feature itself:**

- `app/keymap/keymap_test.go` — asserts `P` resolves to `ActionTogglePreview`
- `app/ui/view_test.go` — one pre-existing test (`TestModel_StatusBarFilenameTruncationWideChars`)
  had its pinned `m.layout.width` bumped from 45 to 47 because the new status icon consumed the
  test's last 2 columns of slack (see the plan's Task 4 ⚠️ note — not a bug, the test was
  already at its limit)

### Most likely rebase conflict sites

`app/ui/diffview.go` and `app/ui/model.go` are the most actively developed files upstream and
the most likely to conflict — this was flagged going in (see the plan's "Patch discipline"
section) and confirmed empirically: `model.go` alone carries 6 of the patch's 21 existing-file
hunks (keymap 4, model 6, loaders 2, diffview 1, mouse 3, view 4, diffnav 1).

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
  consumes whole (`unquoteFlowchartEdgeLabel`). Exactly one layer comes off, and only when the
  label opens and closes with a quote and carries none inside — so `|"a" and "b"|` is left whole
  rather than half-eaten, and the pass stays a fixed point, because what it writes back can never
  be stripped a second time.
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
- **Spaces inside an edge label render as `─` (dash) on the arrow line.** When a label like
  `"listVariants (segment coords)"` is drawn on top of an arrow line, space characters do not
  paint — the arrow line shows through underneath. This is the vendored `mermaid-ascii`
  renderer's `drawText` function, not the transpiler or normalizer. Fixing it requires changing
  the vendored drawing code. Workaround: use non-breaking spaces or replace spaces with other
  characters when the label must sit on an arrow.
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
