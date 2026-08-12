package ui

import (
	"iter"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/umputun/revdiff/app/diff"
)

// mdPreviewRawLine is one source line of a block, prepared for painting into
// the rendered preview in place of that block's rendered rows.
//
// text is the line as it will be written to the screen: tabs already expanded
// and control bytes already dropped, but nothing added — no prefix, no gutter,
// no styling (see the plan's "How raw lines are presented"). lineIdx is the
// index into the []diff.DiffLine the block was rendered from, i.e. the same
// coordinate mdPreviewBlockAnchor.startLine and m.nav.diffCursor use, which is
// what lets an annotation on a raw line be saved through the ordinary
// source-view path. blank marks a line with nothing but whitespace on it: it
// still produces a painted row (dropping it would break the "one source line,
// one rendered row" arithmetic) but never a cursor stop, because
// mdPreviewHighlight skips blank rows and a stop the reader cannot see breaks
// the "you always see what you are about to annotate" invariant.
type mdPreviewRawLine struct {
	text    string
	lineIdx int
	blank   bool
}

// mdPreviewRawLines turns one block anchor into the rows that will replace the
// block's rendered ones: the source lines a.startLine..a.endLine, in order, one
// entry per line.
//
// Divider rows are skipped, matching joinWithMermaidFences — the document
// glamour rendered never contained them, so painting them here would make raw
// expansion and the render disagree about what the document is. Tabs are
// replaced with tabSpaces because ansi.StringWidth counts a tab as one cell
// while the terminal renders eight, so a surviving tab would make the pan clamp
// and the horizontal cut disagree with the screen. C0 control bytes and DEL are
// dropped for the reason mermaidArtWithoutControls exists: this text bypasses
// glamour, so a raw ESC in the source would otherwise reach the terminal live.
//
// The span is clamped to the bounds of lines, so an anchor produced against a
// different (shorter) load can never index out of range.
func mdPreviewRawLines(lines []diff.DiffLine, a mdPreviewBlockAnchor, tabSpaces string) []mdPreviewRawLine {
	start, end := max(a.startLine, 0), min(a.endLine, len(lines)-1)
	if start > end {
		return nil
	}
	out := make([]mdPreviewRawLine, 0, end-start+1)
	for i := start; i <= end; i++ {
		if lines[i].ChangeType == diff.ChangeDivider {
			continue
		}
		text := mdPreviewRawText(lines[i].Content, tabSpaces)
		out = append(out, mdPreviewRawLine{text: text, lineIdx: i, blank: strings.TrimSpace(text) == ""})
	}
	return out
}

// mdPreviewRawText prepares one source line for painting: tabs expanded to
// tabSpaces (matching what renderDiffLine does for source view, which is the
// standard this feature is held to), then C0 control bytes and DEL dropped.
// Tab expansion runs first so the expansion itself cannot be eaten by the
// control-byte pass.
func mdPreviewRawText(content, tabSpaces string) string {
	expanded := strings.ReplaceAll(content, "\t", tabSpaces)
	return strings.Map(func(r rune) rune {
		if controlRune(r) {
			return -1
		}
		return r
	}, expanded)
}

// mdPreviewClipRawSpan is the anchor of block with its source span re-cut to the
// lines that were rendered into the rows the block owns — which is what the pass
// paints them into, and which is neither the anchor's own span nor a plain prefix
// of it.
//
// The re-cut exists because the two coordinate systems are partitioned
// differently:
//
//   - rows TILE. mdPreviewBuildSourceMap closes each block off at the row before
//     the next block starts, so block i owns exactly rows[row_i .. row_{i+1}-1]:
//     no row belongs to two blocks, and none to none.
//   - source spans OVERLAP. A container — a list item or a blockquote — claims a
//     span running to the end of its own content, and swallowedSpan
//     (mdpreview_blocks.go) EXCLUDES any boundary child from the aggregation while
//     leaving it inside the resulting min/max range. So an item holding a nested
//     list between two of its own paragraphs spans all of those lines, while the
//     rows it owns stop where the nested list's rows begin.
//
// So the cut runs in BOTH directions, and both are load-bearing:
//
//   - it SHRINKS a container, at the next block's first source line. The item
//     above owns only the rows above its nested list, so painting its whole span
//     there drew the nested list and the trailing paragraph as source AND left
//     them rendered underneath — the reader saw the same text twice.
//   - it GROWS a nested block, over the enclosing container's continuation. The
//     rows the nested list owns run to the row before the container's next sibling
//     starts, which includes the rows the CONTAINER's own trailing paragraph was
//     rendered into. Painting only the nested list's own lines there DELETED that
//     paragraph from the frame for as long as the block stayed expanded, and left
//     its source line reachable from no block at all. A container's
//     post-nested-block lines therefore belong, for this pass, to the nested block
//     whose row range they are rendered inside.
//
// The growth is bounded by how far any EARLIER block's source reaches, not simply
// run out to the next block's first line: only an enclosing container's span
// covers a line rendered inside this block's rows, and stretching every block to
// the next one would also swallow lines that render to nothing at all — a
// following fence's opening marker, say — and show them as this block's source.
// Anchors are strictly increasing in startLine (mdPreviewBuildSourceMap enforces
// it), so "any earlier block" and "an enclosing container" are the same test, and
// the re-cut span can never end before it starts.
//
// Trailing blank source lines are dropped for the same symmetry from the other
// end: mdPreviewExpandBlock keeps the block's trailing blank RENDERED rows rather
// than replacing them (they are glamour's padding between blocks), so painting the
// blank source lines too would show that padding twice.
//
// The block's OWN span is read through mdPreviewOwnSpanEnd rather than off the
// anchor, which is where a mermaid diagram's fence lines are recovered — see
// there for the whole of that case.
//
// Caller guarantees 0 <= block < len(anchors).
func mdPreviewClipRawSpan(anchors []mdPreviewBlockAnchor, block int, lines []diff.DiffLine) mdPreviewBlockAnchor {
	a := anchors[block]
	end := mdPreviewOwnSpanEnd(a, lines)
	for _, prev := range anchors[:block] {
		end = max(end, prev.endLine) // an enclosing container continuing past this block
	}
	if next := block + 1; next < len(anchors) {
		end = min(end, anchors[next].startLine-1)
	}
	end = min(end, len(lines)-1)
	for end > a.startLine && mdPreviewBlankSourceLine(lines[end]) {
		end--
	}
	a.endLine = max(a.startLine, end)
	return a
}

// mdPreviewOwnSpanEnd is the last source line of a block's own span, as the
// expansion pass needs it: the anchor's endLine for every block except a mermaid
// diagram, whose fence lines it recovers.
//
// A diagram reaches the pass as an mdBlockParagraph whose span is the SINGLE
// ```mermaid line, because joinWithMermaidFences (mdpreview.go) replaces the whole
// fence with one placeholder paragraph and attributes every line of the
// replacement to the fence's opening line. Its rows, though, are the art rows the
// splice put there — so the anchor's own span and the rows it owns describe
// different amounts of document, and painting that span into those rows would
// replace a twenty-row diagram with one row reading "```mermaid". Reading the
// fence's true extent here is what keeps the pass's one-source-line-one-row
// arithmetic exact: the fence's lines are exactly the lines the art was rendered
// from.
//
// This widens the span AT EXPANSION TIME rather than widening the anchor where
// mdPreviewBuildSourceMap builds it, and the reason is that endLine has exactly
// one consumer — this pass. Widening the recorded anchor would mean threading the
// source lines (or the fence spans) into the map builder to change a field nothing
// else reads, and would leave the map claiming a span its own row range was never
// derived from. Keeping it here leaves the anchor a faithful record of what the
// render agreed on and keeps the fence knowledge inside the pass that needs it.
// If a second reader of endLine ever appears, the widening belongs in the builder
// instead, because by then the anchor itself would be under-reporting.
//
// The single-line shape is required, not just checked: it is what a collapsed
// diagram looks like, and a multi-line paragraph that merely opens with fence text
// is ordinary prose (the fence was never substituted, so nothing was collapsed).
//
// The span runs from the opening ```mermaid line to the closing ``` INCLUSIVE, so
// both markers are painted as rows of their own. Deliberate, and the smaller
// choice: they are source lines like every other one, and the pass's whole
// arithmetic is "one source line, one rendered row" — hiding two of them would
// make this the one span whose painted rows are not its lines, for the sake of two
// rows. They are worth showing on their own terms too: the opening line is where a
// comment on the diagram anchors (joinWithMermaidFences attributes the art to it),
// and it carries the info string the reader may well be commenting on.
func mdPreviewOwnSpanEnd(a mdPreviewBlockAnchor, lines []diff.DiffLine) int {
	if a.startLine != a.endLine {
		return a.endLine
	}
	if end, ok := mdPreviewMermaidFenceSpan(lines, a.startLine); ok {
		return end
	}
	return a.endLine
}

// mdPreviewMermaidFenceSpan reports the index of the closing fence line of the
// mermaid fence opening at start. ok is false when lines[start] is not a mermaid
// fence opening, or when the fence is never closed.
//
// The closing rule is joinWithMermaidFences': the same fence character, a marker
// at least as long as the opening one, and nothing but whitespace after it. Rows
// of kind ChangeDivider are skipped exactly as that walk skips them, so a compact
// diff's divider inside a fence neither closes it nor shifts what follows.
//
// An unclosed fence widens nothing. That fence was never substituted — the walk
// flushes it verbatim — so it reaches goldmark as a code block, its anchor is an
// mdBlockCodeBlock, and it is refused before any of this runs. Returning "no span"
// keeps that case a plain single-line block rather than a diagram guess.
func mdPreviewMermaidFenceSpan(lines []diff.DiffLine, start int) (end int, ok bool) {
	if start < 0 || start >= len(lines) {
		return 0, false
	}
	opening := strings.TrimSpace(lines[start].Content)
	if !mdPreviewMermaidFenceLine(opening) {
		return 0, false
	}
	ch, n := mdFencePrefix(opening)
	for i := start + 1; i < len(lines); i++ {
		if lines[i].ChangeType == diff.ChangeDivider {
			continue
		}
		trimmed := strings.TrimSpace(lines[i].Content)
		c, k := mdFencePrefix(trimmed)
		if c == ch && k >= n && strings.TrimSpace(trimmed[k:]) == "" {
			return i, true
		}
	}
	return 0, false
}

// mdPreviewBlankSourceLine reports whether a source line paints nothing the
// reader could see. A divider row counts as blank because mdPreviewRawLines skips
// it outright — it paints no row at all — and every other line is asked of
// mdPreviewRawText, so this and the painted rows share one definition of blank.
// The tabSpaces it is asked with does not matter here: tab expansion substitutes
// whitespace for whitespace, which cannot turn a blank line into a non-blank one.
func mdPreviewBlankSourceLine(line diff.DiffLine) bool {
	if line.ChangeType == diff.ChangeDivider {
		return true
	}
	return strings.TrimSpace(mdPreviewRawText(line.Content, " ")) == ""
}

// Refusal messages for mdPreviewExpandRefusal. Each says what the reader can do
// instead, because a hint that only says "no" is barely better than the silent
// refusal mdPreviewUnanchorableHint's doc comment argues against.
const (
	mdPreviewExpandCodeHint    = "Code blocks already show their source"
	mdPreviewExpandNothingHint = "Nothing to show"
)

// mdPreviewExpandRefusal reports why raw expansion is refused for anchors[block],
// as the status-bar hint to show, or "" when expansion may proceed.
//
// Two refusals, and each is a case where the raw source is already on screen or
// does not exist:
//
//   - a fenced code block (by kind) renders its own source already, so
//     expanding it would redraw the same text with the fences added back.
//   - a block whose every source line is blank has no source to show.
//
// A MERMAID DIAGRAM IS NOT REFUSED, and the asymmetry with the code fence above
// is the whole point rather than an inconsistency. A code fence renders as its
// own lines, roughly one for one, so expanding it adds the fence markers and
// nothing else. A diagram renders as box art that looks nothing like the source
// it was drawn from: expanding is the only way to read or comment on the
// definition — "this edge label is wrong", "this node should be a decision" —
// which is exactly what this feature exists for. The earlier refusal said a
// diagram's source is one line; that was a fact about the ANCHOR, not about the
// document, and mdPreviewOwnSpanEnd recovers the fence's real extent instead.
//
// It takes the whole anchor list and an index rather than one anchor because the
// blankness test asks the CLIPPED span — that is what the pass will paint.
// Blankness is asked of mdPreviewRawLines rather than of the source text
// directly, so this function and the pass share ONE definition of blank. They
// differ: a line holding nothing but a control byte is non-blank as source text
// and blank once mdPreviewRawLines has dropped it, and two answers to "is this
// block worth expanding" is exactly the drift the refusal exists to prevent.
//
// The two other refusals in the feature — an unaligned map and a cursor sitting
// on the file-level annotation — are not block properties and are decided by
// the caller (see mdPreviewToggleRaw).
//
// Caller guarantees 0 <= block < len(anchors).
func mdPreviewExpandRefusal(anchors []mdPreviewBlockAnchor, block int, lines []diff.DiffLine, tabSpaces string) string {
	if anchors[block].kind == mdBlockCodeBlock {
		return mdPreviewExpandCodeHint
	}
	raw := mdPreviewRawLines(lines, mdPreviewClipRawSpan(anchors, block, lines), tabSpaces)
	if _, ok := mdPreviewRawStopLine(raw, mdPreviewNoWantedLine); !ok {
		return mdPreviewExpandNothingHint
	}
	return ""
}

// mdPreviewLineAnchor is one painted raw source line's place in the frame,
// recorded by the pass that put it there. It is the raw-row counterpart of
// mdPreviewAnnotAnchor and is produced the same way and for the same reason: as
// a side effect of the splice that created the row, never by scanning the
// painted string afterwards, because a scan would be a second source of truth
// about where a row is and free to drift from the one that placed it.
//
// row is a row index into the render mdPreviewExpandBlock returned, and lineIdx
// the index into the []diff.DiffLine that row's text came from — the coordinate
// mdPreviewBlockAnchor.startLine and m.nav.diffCursor share, so annotating a raw
// line needs no translation. block is the expanded block the row belongs to;
// only one block is ever expanded, but carrying it keeps the stop list's
// grouping the same shape it already has for annotations.
//
// Invariant: every anchor in one map carries the SAME block, because
// mdPreviewExpandBlock is the only producer and writes its single block argument
// into all of them. mdPreviewSourceMap.expandedBlock reads it back off the first
// one, and that answer — not the cursor's expanded flag — is what the frame is
// actually painted from, so every reader holding a painted map asks it rather
// than the cursor (see moveMdPreviewCursor's clamp for what disagreeing cost).
//
// One source line is one rendered row, so there is no endRow: a line stop's span
// is the single row.
type mdPreviewLineAnchor struct {
	block   int
	row     int
	lineIdx int
}

// mdPreviewExpandBlock redraws one block as its raw markdown source: the block's
// rendered rows are replaced by one row per source line, and the map is returned
// re-expressed in the resulting render's own row numbers.
//
// It has mdPreviewPaintAnnotationsTracked's shape — (rendered, srcMap) ->
// (rendered', srcMap') — and runs BEFORE it, so the painter receives rows and
// anchors that already account for the expansion and needs no knowledge of it.
//
// Nothing is expanded when block is negative (no block selected), the map is not
// aligned, the block index is out of range, the block has no source line the
// reader could see selected, or the block's own row span does not describe this
// render. In every one of those cases it returns the string it was HANDED, not a
// rebuilt copy: mdPreviewScrollCache.forBody compares bodies by value, which is
// O(1) on a shared backing pointer and a full memcmp on a copy, so a rebuilt
// no-op string would cost the whole document on every repaint.
//
// The rows replaced are the block's OWN tile — anchors[block].row through
// anchors[block].endRow — and the source painted into them is the matching tile
// of source lines (mdPreviewClipRawSpan), never the anchor's raw startLine..
// endLine. The two must be cut to the same part of the document or the pass
// either paints a nested block's lines into rows it does not own (the same text
// twice on screen) or drops a container's trailing rows on the floor (part of the
// document silently missing while a nested block is expanded). See that function
// for both directions.
//
// The trailing blank rows of the block's row tile are kept rather than replaced:
// they are the padding glamour puts between blocks, and replacing them would make
// the document jump on every toggle. mdPreviewClipRawSpan drops the matching
// trailing blank SOURCE lines, so the padding is neither painted twice nor lost.
//
// srcMap.annots is carried through untouched. It is always empty here in
// production — the painter that fills it runs after this pass — and a caller
// that inverted the order would get stale annotation rows, which is exactly the
// second-correction-pass failure the ordering exists to avoid.
func mdPreviewExpandBlock(rendered string, srcMap mdPreviewSourceMap, block int,
	lines []diff.DiffLine, tabSpaces string) (string, mdPreviewSourceMap) {
	anchors := srcMap.blocks()
	if !srcMap.aligned || block < 0 || block >= len(anchors) {
		return rendered, srcMap
	}
	raw := mdPreviewRawLines(lines, mdPreviewClipRawSpan(anchors, block, lines), tabSpaces)
	if _, ok := mdPreviewRawStopLine(raw, mdPreviewNoWantedLine); !ok {
		// no source lines at all, or none the highlight could show: expanding
		// would blank the block's rows and record no line anchor, leaving the
		// stop list claiming nothing is expanded over a block painted empty.
		return rendered, srcMap
	}
	rows := strings.Split(rendered, "\n")
	start, end := anchors[block].row, anchors[block].endRow
	if start < 0 || start >= len(rows) || end < start || end >= len(rows) {
		// an anchor that does not describe this render; refuse rather than guess.
		// endRow matters as much as row: it is what the replaced-row count and
		// every later anchor's shift are computed from, so clamping it instead
		// would silently hand back a map whose rows are off by the difference.
		return rendered, srcMap
	}

	// mdPreviewBuildSourceMap already ends a block at its last row with text on
	// it, so for a map straight out of the render this trim finds nothing. It
	// stays because it is the only guard for a map that did NOT come from there —
	// a hand-built one in a test, or a future producer — and expanding over a
	// padding row would paint the raw source where the next block's gap belongs.
	replaced := end - mdPreviewTrailingBlankRows(rows, start, end) - start + 1
	out := make([]string, 0, len(rows)-replaced+len(raw))
	out = append(out, rows[:start]...)
	lineAnchors := make([]mdPreviewLineAnchor, 0, len(raw))
	for i, rl := range raw {
		out = append(out, rl.text)
		if !rl.blank {
			// a blank raw line paints a row but gets no anchor: mdPreviewHighlight
			// skips blank rows, so a stop there would be invisible.
			lineAnchors = append(lineAnchors, mdPreviewLineAnchor{block: block, row: start + i, lineIdx: rl.lineIdx})
		}
	}
	out = append(out, rows[start+replaced:]...)

	// shifted anchors are a copy, never an in-place edit: srcMap's backing array
	// belongs to the render cache (mdpreview_cache.go) and is handed to every
	// later repaint.
	delta := len(raw) - replaced
	shifted := make([]mdPreviewBlockAnchor, len(anchors))
	copy(shifted, anchors)
	shifted[block].endRow += delta
	for i := block + 1; i < len(shifted); i++ {
		shifted[i].row += delta
		shifted[i].endRow += delta
	}
	return strings.Join(out, "\n"),
		mdPreviewSourceMap{aligned: true, anchors: shifted, lines: lineAnchors, annots: srcMap.annots}
}

// mdPreviewTrailingBlankRows counts the blank rendered rows at the end of the
// span start..end. It never counts the span's first row, so a block always has
// at least one row to replace — a block whose every rendered row looks blank
// (ANSI-only padding, say) still expands instead of painting its source twice.
func mdPreviewTrailingBlankRows(rows []string, start, end int) int {
	n := 0
	for r := end; r > start; r-- {
		if strings.TrimSpace(ansi.Strip(rows[r])) != "" {
			break
		}
		n++
	}
	return n
}

// mdPreviewExpandFileHint is the refusal for `r` pressed with the cursor on the
// file-level annotation. That stop owns no block (see mdPreviewFileStopBlock),
// so there is no source to show; the message names the way to a block rather
// than only saying no, the same as the block-level refusals above.
const mdPreviewExpandFileHint = "Select a block with j/k to show its source"

// mdPreviewToggleRaw is `r` inside markdown preview: redraw the block the cursor
// is on as its raw markdown source, or collapse it back when it already is.
//
// Collapsing is a plain re-place of the cursor on the block's own stop:
// setMdPreviewCursorToBlock writes a fresh mdPreviewCursorState, whose expanded is
// false, and the next mdPreviewBody therefore paints the rendered rows again.
// Nothing has to be un-done, which is the whole reason expansion lives on the
// cursor.
//
// Expanding resolves the target the same way `a` does, so the key that shows the
// source and the key that comments on it can never aim at different things:
// the cursor's own block when one is placed, and otherwise a fresh seed at the
// block nearest the viewport center (mdPreviewCenterBlock). From an ANNOTATION
// stop it expands the owning block and lands on the raw line that annotation is
// attached to — the reader is looking at a comment on a line, and `r` should show
// them that line — falling back to the block's first raw line when the
// annotation's line is not one of the block's stoppable rows (an orphan, or a
// blank line, which paints a row but gets no anchor).
//
// scrollX is reset in BOTH directions, following toggleMarkdownPreview's own
// rule for the same reason: the rendered and the raw form of a block have
// different natural widths, and showing a block's source starting at column 40
// is not "show me this block's source".
//
// Every refusal sets m.preview.hint. A silent refusal is indistinguishable from
// an unbound key — the argument mdPreviewUnanchorableHint's doc comment already
// makes for `a`, unchanged here.
func (m *Model) mdPreviewToggleRaw() {
	if !m.file.markdownPreviewable {
		return // preview stuck on for a file renderDiff will not preview; see panMarkdownPreview
	}
	if m.mdPreviewCollapseRaw() {
		return
	}
	_, srcMap := m.mdPreviewBody()
	bi, lineIdx, ok := m.mdPreviewExpandTarget(srcMap)
	if !ok {
		return // mdPreviewExpandTarget set the hint that says why
	}
	m.setMdPreviewLineCursor(bi, lineIdx)
	m.layout.scrollX = 0
	// repaint through the shared stop-change tail: the block's height just
	// changed, so the frame has to be recomposed from a freshly painted body
	// rather than patched, and the viewport has to follow the new stop.
	m.repaintMdPreviewAfterStopChange()
}

// mdPreviewCollapseRaw puts an expanded block back to its rendered form and
// leaves the cursor on that block's own stop, so the reader ends up where they
// started rather than nowhere. It reports whether there was anything to
// collapse.
//
// It is the shared collapse half of BOTH keys that end an expansion: the second
// `r` (mdPreviewToggleRaw above) and `esc` (the ActionDismiss case in
// handleMdPreviewAction, mdpreview.go). Sharing it is what keeps the two from
// drifting on the parts that are easy to forget on one of them — the scrollX
// reset and the recompose through repaintMdPreviewAfterStopChange, which the
// height change makes mandatory.
//
// Collapsing is a plain cursor placement: setMdPreviewCursorToBlock assigns a
// fresh mdPreviewCursorState, so expanded goes back to false and the next
// mdPreviewBody paints the rendered rows again. Nothing has to be un-done.
//
// This is the one reader that asks the CURSOR rather than the painted map, and
// deliberately: it is the escape hatch. Where the two disagree — the cursor says
// expanded and mdPreviewExpandBlock refused, so the screen shows rendered rows —
// `r` and `esc` must still be able to clear the flag, or the reader is left with
// a cursor stuck inside a block that is not expanded on screen.
func (m *Model) mdPreviewCollapseRaw() bool {
	if !m.file.markdownPreviewable {
		return false // preview stuck on for a file renderDiff will not preview
	}
	bi := m.mdPreviewExpandedBlock()
	if bi < 0 {
		return false
	}
	m.setMdPreviewCursorToBlock(bi)
	m.layout.scrollX = 0
	m.repaintMdPreviewAfterStopChange()
	return true
}

// mdPreviewExpandTarget resolves what `r` expands and where it lands the cursor:
// the block, and the index into m.file.lines of the raw line to stop on. ok is
// false when expansion is refused, in which case the status-bar hint saying why
// has already been set.
func (m *Model) mdPreviewExpandTarget(srcMap mdPreviewSourceMap) (block, lineIdx int, ok bool) {
	anchors := srcMap.blocks()
	if !srcMap.aligned || len(anchors) == 0 {
		m.preview.hint = mdPreviewUnanchorableHint
		return 0, 0, false
	}
	stop, hasStop := m.mdPreviewCursorStop(srcMap)
	if hasStop && stop.ref.block == mdPreviewFileStopBlock {
		m.preview.hint = mdPreviewExpandFileHint
		return 0, 0, false
	}
	// never read a field off stop before hasStop is known: the zero
	// mdPreviewStopRef is block 0's own stop, so a no-cursor value would read as a
	// real block rather than as an absence (see mdPreviewStopRef).
	var bi int
	if hasStop {
		bi = stop.ref.block
	} else {
		// only pay for the center seed when there is no cursor to read the block
		// off: mdPreviewCenterBlock builds the whole stop list.
		bi = m.mdPreviewCenterBlock(srcMap)
	}
	if bi < 0 || bi >= len(anchors) {
		m.preview.hint = mdPreviewUnanchorableHint
		return 0, 0, false
	}
	if hint := mdPreviewExpandRefusal(anchors, bi, m.file.lines, m.cfg.tabSpaces); hint != "" {
		m.preview.hint = hint
		return 0, 0, false
	}

	want := mdPreviewNoWantedLine
	if hasStop && stop.ref.onAnnot {
		want = m.mdPreviewWantedLine(stop)
	}
	// the same clipped span the pass will paint, so the line `r` lands on is
	// always a line the frame actually has a row for.
	span := mdPreviewClipRawSpan(anchors, bi, m.file.lines)
	raw := mdPreviewRawLines(m.file.lines, span, m.cfg.tabSpaces)
	lineIdx, ok = mdPreviewRawStopLine(raw, want)
	if !ok {
		// mdPreviewExpandRefusal already rules this out for every block it
		// passes; keep the guard so a future refusal change cannot leave the
		// cursor on a line stop no map can resolve.
		m.preview.hint = mdPreviewExpandNothingHint
		return 0, 0, false
	}
	return bi, lineIdx, true
}

// mdPreviewNoWantedLine is "no line in particular", the value that asks
// mdPreviewRawStopLine and mdPreviewSourceMap.lineStopFor for a block's FIRST
// stoppable raw line. It is -1 because no source line is index -1, so one walk
// serves both "land on this line" and "land on the first one".
const mdPreviewNoWantedLine = -1

// mdPreviewWantedLine is the source-line index the annotation stop names, or
// mdPreviewNoWantedLine when the file no longer has that line — an orphan, which
// has no line for the cursor to return to.
//
// Both callers that place the cursor on a raw line after acting on a comment
// (mdPreviewExpandTarget above and mdPreviewCursorAfterDelete, mdpreview_stops.go)
// need exactly this, and asking it in two places is how the two would drift.
func (m Model) mdPreviewWantedLine(stop mdPreviewStop) int {
	if idx, ok := m.mdPreviewLineIndex(stop.line, stop.changeType); ok {
		return idx
	}
	return mdPreviewNoWantedLine
}

// mdPreviewPickStopLine is the one definition of "which raw source line does the
// cursor land on": want when it turns up as a non-blank line, and the first
// non-blank line otherwise (see mdPreviewNoWantedLine). ok is false when there is
// no non-blank line at all — every painted row would be invisible to the
// highlight, so there is nothing to stop on.
//
// It takes the candidates as (lineIdx, blank) pairs so the two callers can feed
// it from the two different things they hold: mdPreviewRawStopLine from freshly
// prepared raw lines, and mdPreviewSourceMap.lineStopFor from the line anchors of
// the frame already on screen. Writing the walk once is what keeps the two from
// answering the same question differently.
func mdPreviewPickStopLine(candidates iter.Seq2[int, bool], want int) (lineIdx int, ok bool) {
	first, found := 0, false
	for idx, blank := range candidates {
		if blank {
			continue // paints a row, gets no anchor: a stop there would be invisible
		}
		if idx == want {
			return want, true
		}
		if !found {
			first, found = idx, true
		}
	}
	return first, found
}

// mdPreviewRawStopLine picks the source line a fresh expansion stops on, out of
// the raw lines the expansion pass is about to paint. See mdPreviewPickStopLine
// for the rule.
func mdPreviewRawStopLine(raw []mdPreviewRawLine, want int) (lineIdx int, ok bool) {
	return mdPreviewPickStopLine(func(yield func(int, bool) bool) {
		for _, rl := range raw {
			if !yield(rl.lineIdx, rl.blank) {
				return
			}
		}
	}, want)
}

// mdPreviewMermaidFenceLine reports whether content is a fence opening whose
// info string names mermaid. It reads the info string through mdFenceLang, the
// same helper joinWithMermaidFences reads it through, so the two cannot disagree
// about which fences are diagrams — and disagreeing would now show a reader the
// wrong lines rather than merely refuse them (see mdPreviewMermaidFenceSpan).
func mdPreviewMermaidFenceLine(content string) bool {
	trimmed := strings.TrimSpace(content)
	_, n := mdFencePrefix(trimmed)
	if n < 3 {
		return false
	}
	return mdFenceLang(trimmed, n) == "mermaid"
}
