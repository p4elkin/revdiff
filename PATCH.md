# PATCH.md — Markdown Preview Mode (local patch)

This is a personal patch on a local clone of `umputun/revdiff`, not an upstream contribution
(see `docs/plans/20260722-markdown-preview-mode.md` for why). This file is the rebase playbook:
what the patch touches, and how to carry it onto a new upstream release.

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

A clean rebase never conflicts on these two files — they don't exist upstream. All conflict
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

## Dependencies added

- `github.com/charmbracelet/glamour` **v1.0.0** (deliberately v1, not v2 — v2 needs a
  different lipgloss module path that would vendor two copies of lipgloss)
- `github.com/AlexanderGrooff/mermaid-ascii` **@latest** (imported as a library, entry point
  `cmd.RenderDiagram`)

Together these two grew the vendor tree from 31 modules / ~19M (baseline, before this patch) to
71 modules / ~51M (after Task 3). Most of that growth is `mermaid-ascii`'s `cmd` package, which
pulls in gin, cobra, and logrus as transitive dependencies of a library this patch only calls for
one function (`cmd.RenderDiagram`) — see the plan's Technical Details section for why that
tradeoff was accepted. Confirm the actual counts after any rebase with `du -sh vendor/`; they will
drift as upstream's own dependencies change.

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
