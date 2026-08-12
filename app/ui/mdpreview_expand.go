package ui

import (
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
		if mermaidControlRune(r) {
			return -1
		}
		return r
	}, expanded)
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
// The two other refusals in the feature — an unaligned map and a cursor sitting
// on the file-level annotation — are not block properties and are decided by
// the caller (see mdPreviewToggleRaw).
func mdPreviewExpandRefusal(a mdPreviewBlockAnchor, lines []diff.DiffLine) string {
	if a.kind == mdBlockCodeBlock {
		return mdPreviewExpandCodeHint
	}
	if a.startLine == a.endLine && a.startLine >= 0 && a.startLine < len(lines) &&
		mdPreviewMermaidFenceLine(lines[a.startLine].Content) {
		return mdPreviewExpandMermaidHint
	}
	start, end := max(a.startLine, 0), min(a.endLine, len(lines)-1)
	for i := start; i <= end; i++ {
		if lines[i].ChangeType != diff.ChangeDivider && strings.TrimSpace(lines[i].Content) != "" {
			return ""
		}
	}
	return mdPreviewExpandNothingHint
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
// aligned, the block index is out of range, or the block has no source lines to
// show. In every one of those cases it returns the string it was HANDED, not a
// rebuilt copy: mdPreviewScrollCache.forBody compares bodies by value, which is
// O(1) on a shared backing pointer and a full memcmp on a copy, so a rebuilt
// no-op string would cost the whole document on every repaint.
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
	raw := mdPreviewRawLines(lines, anchors[block], tabSpaces)
	if len(raw) == 0 {
		return rendered, srcMap
	}
	rows := strings.Split(rendered, "\n")
	start := anchors[block].row
	if start < 0 || start >= len(rows) {
		return rendered, srcMap // an anchor that does not describe this render; refuse rather than guess
	}

	end := min(anchors[block].endRow, len(rows)-1)
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

// mdPreviewMermaidFenceLine reports whether content is a fence opening whose
// info string names mermaid. It reads the info string exactly the way
// joinWithMermaidFences does — first whitespace-delimited token after the fence
// marker, lowercased — so the two agree on which fences are diagrams.
func mdPreviewMermaidFenceLine(content string) bool {
	trimmed := strings.TrimSpace(content)
	_, n := mdFencePrefix(trimmed)
	if n < 3 {
		return false
	}
	fields := strings.Fields(trimmed[n:])
	return len(fields) > 0 && strings.EqualFold(fields[0], "mermaid")
}
