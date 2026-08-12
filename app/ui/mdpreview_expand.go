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

// mdPreviewClipRawSpan is the anchor of block, with its source span cut short at
// the line where the next block's source begins.
//
// A container — a list item or a blockquote — claims a span that runs to the end
// of its own content, but swallowedSpan (mdpreview_blocks.go) EXCLUDES any
// boundary child from the aggregation while leaving it inside the resulting
// min/max range. So an item holding a fenced code block between two of its own
// paragraphs spans all of those source lines, while the rendered rows it owns
// stop at the row before the fence's own rows begin. Painting the whole span into
// those few rows would draw the fence body and the trailing paragraph as source
// AND leave them rendered directly underneath — the reader sees them twice.
//
// Clipping keeps the pass's one real invariant: the rows a block owns and the
// source lines painted into them describe the same part of the document. The
// lines beyond the cut are not lost to the reader — they belong to the nested
// block, which is a stop of its own and can be expanded (or, for a code fence,
// already shows its source).
//
// Anchors are strictly increasing in startLine (enforced by
// mdPreviewBuildSourceMap), so only the immediately following anchor can fall
// inside this one's span, and the clipped span can never end before it starts.
//
// Caller guarantees 0 <= block < len(anchors).
func mdPreviewClipRawSpan(anchors []mdPreviewBlockAnchor, block int) mdPreviewBlockAnchor {
	a := anchors[block]
	if next := block + 1; next < len(anchors) && anchors[next].startLine <= a.endLine {
		a.endLine = anchors[next].startLine - 1
	}
	return a
}

// Refusal messages for mdPreviewExpandRefusal. Each says what the reader can do
// instead, because a hint that only says "no" is barely better than the silent
// refusal mdPreviewUnanchorableHint's doc comment argues against.
const (
	mdPreviewExpandMermaidHint = "Diagram source is one line — press a to annotate it"
	mdPreviewExpandCodeHint    = "Code blocks already show their source"
	mdPreviewExpandNothingHint = "Nothing to show"
)

// mdPreviewExpandRefusal reports why raw expansion is refused for block a, as
// the status-bar hint to show, or "" when expansion may proceed.
//
// Three refusals, and each is a case where the raw source is already on screen
// or does not exist:
//
//   - a fenced code block (by kind) renders its own source already, so
//     expanding it would redraw the same text with the fences added back.
//   - a mermaid diagram arrives here as an mdBlockParagraph whose span is the
//     single ```mermaid line: joinWithMermaidFences attributes every line of
//     the replacement art to the fence's opening line. Detection is therefore
//     by fence text on a single-line span, NOT by kind — the kind is
//     "paragraph", the same as prose.
//   - a block whose every source line is blank has no source to show.
//
// The blankness question is asked of mdPreviewRawLines rather than of the source
// text directly, so this function and the pass that paints the rows share ONE
// definition of blank. They differ: a line holding nothing but a control byte is
// non-blank as source text and blank once mdPreviewRawLines has dropped it, and
// two answers to "is this block worth expanding" is exactly the drift the
// refusal exists to prevent.
//
// The two other refusals in the feature — an unaligned map and a cursor sitting
// on the file-level annotation — are not block properties and are decided by
// the caller (see mdPreviewToggleRaw).
func mdPreviewExpandRefusal(a mdPreviewBlockAnchor, lines []diff.DiffLine, tabSpaces string) string {
	if a.kind == mdBlockCodeBlock {
		return mdPreviewExpandCodeHint
	}
	if a.startLine == a.endLine && a.startLine >= 0 && a.startLine < len(lines) &&
		mdPreviewMermaidFenceLine(lines[a.startLine].Content) {
		return mdPreviewExpandMermaidHint
	}
	if _, ok := mdPreviewRawStopLine(mdPreviewRawLines(lines, a, tabSpaces), -1); !ok {
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
// The source span is CLIPPED at the next block's first source line
// (mdPreviewClipRawSpan), so a container holding a nested block never paints that
// block's lines into rows it does not own — see that function for the shape and
// what going without it looked like on screen.
//
// The trailing blank rows of the block's span are kept rather than replaced. A
// non-last block's span runs to the row before the next block starts, so it
// includes the padding glamour puts between blocks, and the last block's span
// runs to the end of the document. Replacing the whole span would swallow that
// padding and make the document jump on every toggle.
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
	raw := mdPreviewRawLines(lines, mdPreviewClipRawSpan(anchors, block), tabSpaces)
	if _, ok := mdPreviewRawStopLine(raw, -1); !ok {
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
	span := mdPreviewClipRawSpan(anchors, bi)
	if hint := mdPreviewExpandRefusal(span, m.file.lines, m.cfg.tabSpaces); hint != "" {
		m.preview.hint = hint
		return 0, 0, false
	}

	want := mdPreviewNoWantedLine
	if hasStop && stop.ref.onAnnot {
		want = m.mdPreviewWantedLine(stop)
	}
	// the same clipped span the pass will paint, so the line `r` lands on is
	// always a line the frame actually has a row for.
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
// about which fences are diagrams.
func mdPreviewMermaidFenceLine(content string) bool {
	trimmed := strings.TrimSpace(content)
	_, n := mdFencePrefix(trimmed)
	if n < 3 {
		return false
	}
	return mdFenceLang(trimmed, n) == "mermaid"
}
