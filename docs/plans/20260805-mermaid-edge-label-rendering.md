# Mermaid edge-label rendering: no-break spaces and collision retry

## Contents

1. [Overview](#overview)
2. [Context (from discovery)](#context-from-discovery)
3. [Development Approach](#development-approach)
4. [Testing Strategy](#testing-strategy)
5. [Progress Tracking](#progress-tracking)
6. [Solution Overview](#solution-overview)
7. [Technical Details](#technical-details)
8. [What Goes Where](#what-goes-where)
9. [Implementation Steps](#implementation-steps)
10. [Post-Completion](#post-completion)

## Overview

Two independent defects in how mermaid edge labels render in markdown preview mode (`P` key).

**The space bleed.** A space inside an edge label is transparent to the vendored renderer's
layer merge, so whatever lies underneath the label shows through at that column. On the label's
own arrow line the thing underneath is `─`, so `read as fallback` renders as `read─as─fallback`
and looks like part of the connector. Where another edge crosses, the thing underneath is `│`,
so `index or '-': item add/remove/reorder` renders as `index or '-': item│add/remove/reorder` —
a corrupted label. Measured across 229 unique mermaid fences from the user's corpus: 40 fences
have multi-word edge labels and show the dashes today, and 11 of those have a non-dash
character cutting through a label.

**The label collision.** A decision node with three or more labeled out-edges puts two of the
labels on the same output row, so the reader cannot tell which arrow either belongs to. A real
example renders as:

```
│   What kind of property is it?   ├◄───collection────single─composite───────┤
```

`collection` and `single composite` belong to two different arrows. Measured on the same
corpus: 94 fences are top-down flowcharts and 3 of them collide.

Fixing both means a hand-written flowchart with multi-word branch labels reads correctly
without the author restructuring the diagram.

## Context (from discovery)

Files and components involved:

- `app/ui/mdpreview_transpile.go` — `renderMermaidSource` (line 2359) is the single render
  hook, and `mermaidEdgeLabel` (line 300) holds the existing space substitution for the
  transpiled diagram types.
- `app/ui/mdpreview_flowchart.go` — `unquoteFlowchartEdgeLabel` (line 407) is the last
  function to touch a plain flowchart's edge label before it reaches the renderer.
- `app/ui/mdpreview_subgraph.go` — the shape to copy for a new gated transform: measure,
  gate on explicit conditions, fall back to today's render when any condition fails.
- `vendor/github.com/AlexanderGrooff/mermaid-ascii/cmd/draw.go` — `mergeDrawings` composites
  layers with `if c != " "`, which is the whole cause of the space bleed.
- `vendor/github.com/AlexanderGrooff/mermaid-ascii/cmd/arrow.go` — `drawTextOnLine` places
  each label at its own line's midpoint with no check for occupied cells, which is the cause
  of the collision.

Patterns found:

- The fork keeps new logic in new files so hand rebases against upstream never conflict.
  `PATCH.md` is the playbook and lists every added file.
- Every transform in this area declines by returning `ok == false` rather than an error, and
  declining lands on the unchanged render below it.
- `mdpreview.go` already wraps the render in a `recover()`, so a panic in new code lands on
  the verbatim fence text rather than reaching the user.

Dependencies identified:

- `normalizeFlowchartLine` runs `normalizeFlowchartLinks` first and `normalizeFlowchartNodes`
  second. That ordering means an inline `-- yes -->` label has already become `-->|yes|` by the
  time `normalizeFlowchartNodes` walks the line. So `unquoteFlowchartEdgeLabel` sees **both**
  edge-label spellings and is a single chokepoint, not two.
- `mermaidEdgeLabel`'s cap is a hard **byte** cap as well as a rune cap, because the vendored
  `mapping_edge.go` reserves column width with `len()`. U+00A0 and `·` are both 2 bytes in
  UTF-8, so swapping one for the other moves no width arithmetic and no cap test.

## Development Approach

- **parallel waves**: `labels (tasks 2-3)` — the two label call sites live in different files
  with different test files and share only the helper built in task 1. Everything else is
  sequential.
- **testing approach**: TDD — write the failing test first in each task, then the code. Both
  changes are pure text-to-text transforms with exactly checkable output, which is the case
  where test-first costs nothing and pins the behaviour precisely.
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional - they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** - no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change, using the narrow per-task command; the full suite runs once in
  the verify task
- maintain backward compatibility

## Testing Strategy

- **unit tests**: required for every task (see Development Approach above)
- **e2e tests**: this project has no UI-based e2e suite. The equivalent here is the
  differential corpus render in task 7, which is a required deliverable and not optional.
- **golden tests**: `app/ui` carries a render golden. Neither change touches diff rendering,
  so the golden must come out unchanged; if it does not, that is a real finding, not a
  fixture to update.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

Both fixes sit on the existing render path and both degrade to today's output when anything
about them does not hold.

```mermaid
flowchart TD
    Src["Fence source"] --> Transpile["Transpile class and state diagrams"]
    Transpile --> Split{"Independent subgraphs?"}
    Split -->|"yes"| Stacked["Stack one block per subgraph"]
    Split -->|"no"| Render["Render as the author wrote it"]
    Render --> Detect{"Two labels share a row?"}
    Detect -->|"no"| Done["Return the art"]
    Detect -->|"yes"| Retry["Render again, direction flipped to LR"]
    Retry --> Better{"Fewer collisions than before?"}
    Better -->|"yes"| UseLR["Return the LR art"]
    Better -->|"no"| Done
    Stacked --> Done
```

Three properties this shape gives us:

- A fence that renders cleanly today never reaches the retry, so its bytes are unchanged.
- The second render only ever happens for a fence that is already broken, so the cost lands
  where there is something to gain.
- The no-break space substitution runs before any of this, so by the time the detector looks
  for a label in the art, that label appears verbatim rather than with its spaces bled through.

**Why the flip is to LR and not something cleverer.** The collision is a layout failure, and
the only lever that changes layout without changing the graph is the direction. Padding does
not work: `diagram.Config` exposes `PaddingBetweenX` and `PaddingBetweenY`, and across seven
settings from 5,5 to 15,15 the colliding row comes out byte-identical. Reordering the branch
declarations only changes which two labels collide. Removing the back edge does not help.

**What is deliberately not done.** The complete fix is in `drawTextOnLine`, which would need
to reserve occupied cells and nudge the label along its line. That means forking
`mermaid-ascii` and carrying a `replace` directive — a second fork to maintain, for 3 fences
in 229. The retry is the cheap fix that covers the measured cases; the limitation gets written
down instead.

**Trade-off, stated plainly.** Flipping to LR makes the art wider — measured on the three
affected fences, 188 to 215 columns on average. So a broken-but-narrow picture becomes a
correct-but-wider one. That is the right trade here because preview mode has horizontal
panning (`scroll_left` / `scroll_right` are both in `mdPreviewAllowedActions`), so a wide
render is reachable, while a collided label is unreadable at any width.

## Technical Details

### The substitution character

U+00A0 NO-BREAK SPACE. It is not the byte `" "`, so `mergeDrawings` keeps it instead of
letting the layer underneath through, and terminals draw it as a blank. Verified by probe
against the real renderer:

| label as written | renders today | renders after |
|---|---|---|
| `read as fallback` | `read─as─fallback` | `read as fallback` |
| `index or '-': item add/remove/reorder` | `index or '-': item│add/remove/reorder` | intact |

Two known costs, both accepted: art copied out of the terminal carries no-break spaces rather
than plain ones, and a small number of fonts draw U+00A0 visibly.

### Where the substitution goes

One shared helper in a new file, called from exactly two places:

- `mermaidEdgeLabel` in `mdpreview_transpile.go`, replacing the existing `" "` to `·`
  substitution. The neighbouring `mermaidDotRun` collapse (`·{2,}` to one dot) exists because a
  source label containing a literal `·` turned its surrounding spaces into dots too, leaving a
  run of three. The same thing happens with no-break spaces around a literal one, so the
  collapse rule moves to the new character with it.
- `unquoteFlowchartEdgeLabel` in `mdpreview_flowchart.go`, which today returns the segment
  untouched whenever the label is absent, unquoted, or carries an inner quote. The
  substitution has to run in all of those cases, so the function becomes "normalize this edge
  label segment" — unquote when quotable, then substitute — rather than "unquote or bail".
  Its name and doc comment change to match.

### The collision detector

Input is the fence source and the rendered art. Output is a count.

1. Pull every edge label out of the source.
2. Sort them longest first, so a short label cannot match inside a longer one.
3. For each row of the art, find each label, marking the columns it consumes so a later
   (shorter) label cannot claim the same cells.
4. Flag each adjacent pair of hits on one row that are **distinct labels** and separated by
   fewer than 8 columns.

The 8-column threshold is what separates "two labels crammed into one corridor" from "two
labels that happen to be on the same row in different parts of a wide diagram". On the
measured corpus it flags 3 fences with no false positives; the LR render of all three flags
zero.

A working prototype of both the detector and the corpus harness is at
`/private/tmp/claude-501/-Users-sasha-dev-oss-revdiff/3ca44ced-2e51-4dbb-97e0-c4e65cb65f87/scratchpad/corpus_harness.go.txt`.
Read it before writing task 4 — it is the measured version, not a sketch.

### The retry gate

Flip only when all of these hold, and keep the flipped render only when the last one does:

- the fence has an explicit direction header and that direction is `TD` or `TB`
- the first render flags at least one collision
- the flipped render succeeds and is non-blank
- the flipped render flags **strictly fewer** collisions than the first

Any failure returns the first render. A fence with no collisions never renders twice.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): code, tests, and in-repo documentation.
- **Post-Completion** (no checkboxes): visual confirmation in the real TUI and the binary
  reinstall, which need a terminal and a person.

## Implementation Steps

### Task 1: Add the shared no-break space helper

**Files:**
- Create: `app/ui/mdpreview_nbsp.go`
- Create: `app/ui/mdpreview_nbsp_test.go`

**Model:** sonnet

- [x] create `app/ui/mdpreview_nbsp.go` with a `mermaidNBSP` constant (U+00A0) and a function
      that replaces every space in an edge label with it, then collapses any run of two or
      more into one
- [x] document in the doc comment why the substitution exists: `mergeDrawings` in
      `vendor/.../cmd/draw.go` treats `" "` as transparent, so a space lets the arrow line or a
      crossing edge show through
- [x] document why a run can appear at all (a source label already containing the substitution
      character has its neighbouring spaces converted too) and why collapsing is safe
- [x] write tests for the substitution: single space, several spaces, leading and trailing
      spaces, a label with no spaces at all, the empty string
- [x] write tests for the run collapse, including a label that already contains a literal
      no-break space
- [x] run `go test ./app/ui -run TestMermaidNBSP` - must pass before task 2

### Task 2: Use no-break spaces on the transpiled path

**Files:**
- Modify: `app/ui/mdpreview_transpile.go` (`mermaidEdgeLabel`'s `strings.ReplaceAll(s, " ", "·")`
  and the `mermaidDotRun` variable above it)
- Modify: `app/ui/mdpreview_transpile_test.go` (every assertion carrying a literal `·`, around
  lines 89, 105, 112-115, 144-182)

**Model:** sonnet
**Wave:** labels

- [x] replace the `·` substitution and the `mermaidDotRun` collapse in `mermaidEdgeLabel` with
      a call to the task 1 helper
- [x] remove `mermaidDotRun` if it has no remaining caller, and update `mermaidEdgeLabel`'s doc
      comment where it explains the middle dot
- [x] update the existing tests that assert `·` literals to assert the no-break space instead
- [x] verify the byte-cap tests still hold unchanged — U+00A0 and `·` are both 2 bytes, so a
      changed expectation here means something else moved and must be understood, not adjusted
- [x] write a test that a class or state diagram edge label with spaces reaches the art with
      its spaces intact
- [x] run `go test ./app/ui -run 'TestMermaidEdgeLabel|TestMermaid|TestTranspile'` - must pass
      before the next task

### Task 3: Use no-break spaces on the plain flowchart path

**Files:**
- Modify: `app/ui/mdpreview_flowchart.go` (`unquoteFlowchartEdgeLabel` and its doc comment,
  plus the reference to it in `normalizeFlowchartNodes`' doc comment)
- Modify: `app/ui/mdpreview_flowchart_test.go` (the `unquoteFlowchartEdgeLabel` tests)

**Model:** sonnet
**Wave:** labels

- [x] rename `unquoteFlowchartEdgeLabel` to reflect that it now normalizes rather than only
      unquotes, and restructure it so the substitution runs on every path with a label — not
      only when the label was quoted
- [x] keep the existing bail-outs intact for the cases that must stay untouched: a segment with
      no label at all, and a label carrying an inner quote
- [x] update the doc comment to record that this is the single chokepoint for both edge-label
      spellings, because `normalizeFlowchartLine` normalizes links before nodes
- [x] write tests covering both spellings: the inline `A -- read as fallback --> B` form and the
      piped `A -->|"read as fallback"| B` form, each reaching the renderer with spaces intact
- [x] write tests for the unchanged cases: bare arrow with no label, a label with an inner quote,
      a label with no spaces
- [x] run `go test ./app/ui -run TestFlowchart` - must pass before task 4

### Task 4: Add the collision detector

**Files:**
- Create: `app/ui/mdpreview_collision.go`
- Create: `app/ui/mdpreview_collision_test.go`

**Model:** opus

- [x] read the prototype at
      `/private/tmp/claude-501/-Users-sasha-dev-oss-revdiff/3ca44ced-2e51-4dbb-97e0-c4e65cb65f87/scratchpad/corpus_harness.go.txt`
      first — it is the version the corpus numbers came from
- [x] create `app/ui/mdpreview_collision.go` with a function taking the fence source and the
      rendered art and returning a collision count
- [x] implement label extraction, longest-first matching with consumed-column marking, and the
      adjacent-distinct-pair test with the 8-column threshold as a named constant
- [x] document why longest-first plus column marking is required (a short label matching inside
      a longer one would count a collision that is not there) and why the threshold is 8
- [x] write tests for a real collision, for two labels far apart on one row, for the same label
      appearing twice on a row, for a fence with fewer than two distinct labels, and for empty
      art
- [x] write a test using the real colliding fence added in task 6 as the positive case
      (embedded as the `collisionThreeBranchFence` literal — task 6 has not run yet, so the
      fence is copied from the same source document the fixture will come from)
- [x] run `go test ./app/ui -run TestCollision` - must pass before task 5

### Task 5: Retry in LR when the first render collides

**Files:**
- Modify: `app/ui/mdpreview_transpile.go` (`renderMermaidSource`, at its
  `mermaidcmd.RenderDiagram(toRender, nil)` call and the `return rendered, nil` after it)
- Modify: `app/ui/mdpreview_collision.go` (add the direction flip and the retry decision)
- Modify: `app/ui/mdpreview_collision_test.go`

**Model:** sonnet

- [ ] add a direction-flip function that rewrites a `TD` or `TB` header to `LR` and reports
      false for any other direction, a missing header, or a malformed one
- [ ] add the retry decision: given the first render and its collision count, return the LR
      render only when it succeeds, is non-blank, and flags strictly fewer collisions
- [ ] wire it into `renderMermaidSource` after the existing `RenderDiagram` call, leaving the
      transpile and subgraph-split paths above it untouched
- [ ] update `renderMermaidSource`'s doc comment to record the retry and its gate
- [ ] write a test that a fence with no collisions renders byte-identically to a direct
      `RenderDiagram` call, pinning that the common path is unchanged
- [ ] write tests for each gate condition failing: an `LR` fence, a fence with no header, a
      flipped render that is worse, a flipped render that errors
- [ ] run `go test ./app/ui -run 'TestCollision|TestRenderMermaidSource'` - must pass before
      task 6

### Task 6: Add the real-world fixtures end to end

**Files:**
- Create: `app/ui/testdata/mermaid/collision-three-branches.mmd`
- Create: `app/ui/testdata/mermaid/bleed-crossing-edge.mmd`
- Modify: `app/ui/mdpreview_test.go` (add the end-to-end cases next to the existing
  `renderMarkdownDocument` tests)

**Model:** sonnet

- [ ] copy the two fences from
      `/var/folders/cf/v4dl10nn2c3g0l4l8qnmr8fc0000gn/T/agterm-annotate/0334DB93-A1F7-4EF4-AA3B-9AEE8925B21E.left/lae-finalisation.md`
      into the two fixture files — the first fence has the three-branch collision, the second
      has the crossing-edge bleed
- [ ] write an end-to-end test that the collision fixture renders with zero collisions after
      the change
- [ ] write an end-to-end test that the bleed fixture's `index or '-': item add/remove/reorder`
      label reaches the art without a `│` cutting through it
- [ ] write an end-to-end test that neither fixture panics and neither renders blank
- [ ] run `go test ./app/ui -run TestRenderMarkdownDocument` - must pass before task 7

### Task 7: Verify differentially against the whole corpus

**Files:**
- Create: `app/ui/mdpreview_corpus_test.go` (skipped unless a corpus path is set in the
  environment, so it never runs in CI)

**Model:** opus

- [ ] port the corpus harness from the scratchpad into a test that is skipped when its
      environment variable is unset, so `make test` is unaffected
- [ ] render all 229 unique corpus fences on the pre-change build (`git stash` or a worktree at
      the parent commit) and capture the output
- [ ] render the same fences on the current build and diff the two sets
- [ ] account for **every** difference: expected is about 40 fences changed by the space fix and
      3 by the LR retry, and **0 unexplained**
- [ ] confirm 0 panics and 0 hangs on both builds
- [ ] ⚠️ if any difference cannot be explained, stop and fix the cause — do not proceed with an
      unexplained diff
- [ ] record the final counts in this plan file under Progress Tracking
- [ ] run `go test ./app/ui` - must pass before task 8

### Task 8: Update PATCH.md and the manual test plan

**Files:**
- Modify: `PATCH.md` (the "New files added by the patch" list, and the known-limitations
  section)
- Modify: `docs/plans/20260730-preview-manual-test-plan.md` (add a new numbered section after
  the existing subgraph-splitting section)

**Model:** sonnet

- [ ] add `mdpreview_nbsp.go` and `mdpreview_collision.go` to the new-files list in `PATCH.md`
- [ ] record the no-break space substitution, why it exists, and the two accepted costs
      (copy-paste carries U+00A0, some fonts draw it visibly)
- [ ] record the LR retry, its four gate conditions, and the measured corpus numbers
- [ ] record the remaining limitation: the collision is a vendored `drawTextOnLine` defect, the
      retry only covers fences with a flippable direction, and a collided `LR` fence stays
      collided
- [ ] add a manual test plan section with both fixtures and what to look for in each
- [ ] run `go test ./app/ui` - must pass before task 9

### Task 9: Verify acceptance criteria

- [ ] verify both defects from Overview are fixed on the real fixtures
- [ ] verify a fence that renders cleanly today is byte-identical after the change
- [ ] verify every failure path degrades to today's output
- [ ] run full test suite: `make test`
- [ ] run `make lint` - must report 0 issues
- [ ] run `make build`
- [ ] verify test coverage for `app/ui` has not dropped

### Task 10: [Final] Update documentation

- [ ] update `CLAUDE.md` or `.claude/rules/gotchas.md` if either change introduces a trap a
      future session would otherwise re-derive
- [ ] confirm README.md and `site/` need no change — this is fork-local preview behaviour with
      no new flag or keybinding
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems - no checkboxes, informational only*

**Manual verification:**

- open the two fixture documents in the real TUI, press `P`, and confirm the collision is gone
  and no label has a `│` through it
- confirm the flipped LR render is readable by panning with the left and right arrows
- check U+00A0 renders as a blank in the terminal actually in use, since this is font-dependent

**Binary reinstall:**

- rebuild and reinstall following the versioned-build layout: build, copy to
  `~/.local/bin/revdiff-builds/<sha>/revdiff`, `codesign --force --sign -`, then repoint the
  `revdiffm` symlink. Copying over a running binary in place leaves a stale signature and macOS
  kills it with no output.
