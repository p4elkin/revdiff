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
  glamour rendering, render cache, the preview-mode action allowlist, `toggleMarkdownPreview`).
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
  - line 307: `mdPreviewCache *mdPreviewCache` field on `loadedFileState` (not on `modeState` —
    see the plan's Task 4 ⚠️ note on gofmt column alignment)
  - line 355: `mdPreview bool` field on `modeState`
  - line 1113: `keymap.ActionTogglePreview` added to the toggle-grouping `case` that routes to
    `handleViewToggle`
  - line 1432: `case keymap.ActionTogglePreview: m.toggleMarkdownPreview()` inside
    `handleViewToggle`
- `app/ui/diffview.go`
  - line 280-282: early-return branch in `renderDiff()` — `if m.modes.mdPreview &&
    m.file.mdTOC != nil { return m.renderMarkdownPreview() }`, placed beside the existing
    `m.modes.collapsed.enabled` branch
- `app/ui/view.go`
  - line 459: `{"▤", m.modes.mdPreview}` added to `statusModeIcons()`

That is 9 hunks across 4 files (the plan's Task 4 text calls this "seven sites" counting by
checklist item, not by literal diff hunk — `keymap.go`'s enum/validActions edit and
`model.go`'s two dispatch-case edits are each one checklist item covering two hunks). Use the
line list above, not the number seven, as the actual hunk map.

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
  - line 451: `if m.modes.mdPreview { return false }` at the top of `pinDiffCursorTo`

**Test-only, mechanical, not part of the feature itself:**

- `app/keymap/keymap_test.go` — asserts `P` resolves to `ActionTogglePreview`
- `app/ui/view_test.go` — one pre-existing test (`TestModel_StatusBarFilenameTruncationWideChars`)
  had its pinned `m.layout.width` bumped from 45 to 47 because the new status icon consumed the
  test's last 2 columns of slack (see the plan's Task 4 ⚠️ note — not a bug, the test was
  already at its limit)

### Most likely rebase conflict sites

`app/ui/diffview.go` and `app/ui/model.go` are the most actively developed files upstream and
the most likely to conflict — this was flagged going in (see the plan's "Patch discipline"
section) and confirmed empirically: `model.go` alone carries 6 of the patch's 10 existing-file
hunks.

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
revdiffm --dump-keys | grep 'toggle_markdown_preview'   # confirms P is bound in this build
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
