package ui

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour/ansi"
	xansi "github.com/charmbracelet/x/ansi"
)

// mdPreviewMarkerBase is added to a block kind's id to build its marker's
// SGR parameter. See mdPreviewMarker for the encoding rationale.
const mdPreviewMarkerBase = 700

// mdPreviewMarker returns the zero-width marker for a block kind id.
//
// Encoding rationale (lifted from the spike harness's zzMarker — see this
// plan's Context section):
//
//   - It is a single-parameter CSI SGR sequence: ESC [ <n> m. Every
//     ANSI-aware layer in the render path (muesli/reflow wordwrap+indent+
//     padding, x/ansi.Wordwrap, lipgloss) treats CSI ... m as zero width, so
//     it cannot move a wrap point or change a measured width.
//   - The parameter is mdPreviewMarkerBase+id, i.e. 7xx: far outside
//     ECMA-48's defined SGR range (0..65) and outside the de-facto vendor
//     extensions (90..97, 100..107). A terminal that ever saw it would
//     ignore it, and there is no combination of standard params that would
//     accidentally turn on blink/inverse/etc — that is why the kind id goes
//     into the SAME parameter rather than a second one: "\x1b[700;5m" would
//     enable blink if it ever leaked unstripped.
//   - A single param means the byte string is short (6-7 bytes) and unique,
//     so it can be removed with strings.ReplaceAll — an exact byte match,
//     not an ANSI strip.
//   - It cannot collide with anything glamour or chroma emits: termenv
//     emits 0/1/3/4/5/7/9/53 and 38;5;N / 48;5;N / 38;2;R;G;B with N<=255,
//     and chroma's terminal256 formatter the same. None of those produce the
//     byte sequence ESC [ 7 x x m.
func mdPreviewMarker(id int) string {
	return "\x1b[" + strconv.Itoa(mdPreviewMarkerBase+id) + "m"
}

// mdPreviewMarkerKind is one block kind this package can locate in a
// rendered glamour document: the shared mdPreviewBlockKind (see
// mdpreview_blocks.go's doc comment on that type for why the marker table
// and the goldmark block walk share one kind vocabulary instead of two),
// the id its marker is built from, the style field the marker is
// prepended to (documentation and test-coverage only — no production code reads it), and the apply func
// that clones the marker into that field on a style config copy.
type mdPreviewMarkerKind struct {
	kind       mdPreviewBlockKind
	id         int
	styleField string
	apply      func(sc *ansi.StyleConfig, marker string)
}

// mdPreviewMarkerKinds is every block kind this feature marks: paragraph,
// h1..h6, item, enumeration, code_block, block_quote, table, hr, and
// html_block.
//
// Deliberately NOT the generic "heading" — glamour never emits
// Heading.Prefix, only H1..H6.Prefix (established by the spike; see this
// plan's Technical Details). Also deliberately not "list" (its items are
// marked individually via item/enumeration, which is the granularity this
// feature needs), "document" (not a block a comment can target), "task"
// (glamour renders a task list item through a separate Task style field;
// task 2's goldmark walk folds task items into their enclosing list item
// instead of needing a marker of their own — see the plan's "Decided by the
// spike" section), or "text" (a spike-only probe kind used to investigate
// table cell rendering, not a block kind).
var mdPreviewMarkerKinds = []mdPreviewMarkerKind{
	{mdBlockParagraph, 1, "Paragraph.Prefix", func(sc *ansi.StyleConfig, m string) {
		sc.Paragraph.Prefix = m + sc.Paragraph.Prefix
	}},
	{mdBlockH1, 2, "H1.Prefix", func(sc *ansi.StyleConfig, m string) { sc.H1.Prefix = m + sc.H1.Prefix }},
	{mdBlockH2, 3, "H2.Prefix", func(sc *ansi.StyleConfig, m string) { sc.H2.Prefix = m + sc.H2.Prefix }},
	{mdBlockH3, 4, "H3.Prefix", func(sc *ansi.StyleConfig, m string) { sc.H3.Prefix = m + sc.H3.Prefix }},
	{mdBlockH4, 5, "H4.Prefix", func(sc *ansi.StyleConfig, m string) { sc.H4.Prefix = m + sc.H4.Prefix }},
	{mdBlockH5, 6, "H5.Prefix", func(sc *ansi.StyleConfig, m string) { sc.H5.Prefix = m + sc.H5.Prefix }},
	{mdBlockH6, 7, "H6.Prefix", func(sc *ansi.StyleConfig, m string) { sc.H6.Prefix = m + sc.H6.Prefix }},
	{mdBlockItem, 8, "Item.BlockPrefix", func(sc *ansi.StyleConfig, m string) {
		sc.Item.BlockPrefix = m + sc.Item.BlockPrefix
	}},
	{mdBlockEnum, 9, "Enumeration.BlockPrefix", func(sc *ansi.StyleConfig, m string) {
		sc.Enumeration.BlockPrefix = m + sc.Enumeration.BlockPrefix
	}},
	{mdBlockCodeBlock, 10, "CodeBlock.BlockPrefix", func(sc *ansi.StyleConfig, m string) {
		sc.CodeBlock.BlockPrefix = m + sc.CodeBlock.BlockPrefix
	}},
	{mdBlockQuote, 11, "BlockQuote.Prefix", func(sc *ansi.StyleConfig, m string) {
		sc.BlockQuote.Prefix = m + sc.BlockQuote.Prefix
	}},
	{mdBlockTable, 12, "Table.Prefix", func(sc *ansi.StyleConfig, m string) { sc.Table.Prefix = m + sc.Table.Prefix }},
	{mdBlockHR, 13, "HorizontalRule.Prefix", func(sc *ansi.StyleConfig, m string) {
		sc.HorizontalRule.Prefix = m + sc.HorizontalRule.Prefix
	}},
	{mdBlockHTMLBlock, 14, "HTMLBlock.Prefix", func(sc *ansi.StyleConfig, m string) {
		sc.HTMLBlock.Prefix = m + sc.HTMLBlock.Prefix
	}},
}

// mdPreviewStyleWithMarkers returns a copy of base with every kind in kinds
// given its marker prepended to its style field. Prepending, never
// replacing, is what keeps an existing prefix intact — e.g. item's
// BlockPrefix is glamour's "• " bullet, and the marked style must still
// render that bullet, just with the marker ahead of it.
func mdPreviewStyleWithMarkers(base ansi.StyleConfig, kinds ...mdPreviewMarkerKind) ansi.StyleConfig {
	sc := base // struct copy: every field this loop touches is a plain string
	for _, k := range kinds {
		k.apply(&sc, mdPreviewMarker(k.id))
	}
	return sc
}

// mdPreviewStripMarkers removes every marker byte sequence from s via an
// exact match on each kind's marker string. Exact and total: nothing else
// in s is touched, and a marker with no matching bytes present is a no-op —
// calling this on a string with no markers at all returns s unchanged.
func mdPreviewStripMarkers(s string) string {
	for _, k := range mdPreviewMarkerKinds {
		s = strings.ReplaceAll(s, mdPreviewMarker(k.id), "")
	}
	return s
}

// mdPreviewEmptySGRPairRe matches an SGR sequence immediately followed by a
// reset with nothing in between — the residue left behind when a marker
// occupied a style field that had no existing prefix (e.g. table, whose
// DarkStyleConfig prefix is empty): stripping the marker alone leaves a
// bare SET then RESET, which is inert but not byte-identical to a render
// with no prefix at all.
var mdPreviewEmptySGRPairRe = regexp.MustCompile(`\x1b\[[0-9;:]*m\x1b\[0m`)

// mdPreviewDropEmptySGRPairs repeatedly removes empty SET/RESET SGR pairs
// until none remain — removing one pair can expose another immediately
// adjacent to it (two markers back to back in an otherwise-empty prefix).
func mdPreviewDropEmptySGRPairs(s string) string {
	for {
		out := mdPreviewEmptySGRPairRe.ReplaceAllString(s, "")
		if out == s {
			return s
		}
		s = out
	}
}

// mdPreviewChromeRe matches a row prefix made only of whitespace, list
// bullets/numbers, or blockquote bars — the "chrome" glamour draws ahead of
// a block's own content, never the content itself.
var mdPreviewChromeRe = regexp.MustCompile(`^[ \t]*(?:[0-9]+|[•▪‣]|\|\s|│\s|\*\s|\+\s|-\s)?[ \t]*$`)

// mdPreviewIsChrome reports whether s — everything on a rendered row before
// a marker — is pure chrome, meaning the marker sits at the start of the
// block's rendered content on that row rather than mid-row.
func mdPreviewIsChrome(s string) bool {
	return mdPreviewChromeRe.MatchString(s)
}

// mdPreviewMarkerHit is one block-kind marker located in a rendered
// document, after the ~3x-per-block repeat glamour emits for several kinds
// (always on the same row — see the plan's Technical Details) collapses to
// a single entry per (kind, row) pair. See mdPreviewExtractMarkers.
type mdPreviewMarkerHit struct {
	kind   mdPreviewBlockKind
	row    int
	midRow bool // true when non-chrome content precedes the marker on its row
}

// mdPreviewExtractMarkers scans rendered (with markers still present, i.e.
// before mdPreviewStripMarkers) for every kind in mdPreviewMarkerKinds and
// returns one hit per (kind, row) pair that actually occurs, in row order
// (kind-table order within a tied row).
//
// A kind emits its marker up to ~3 times on the SAME row for several kinds
// (established by the spike). Only the first occurrence per row is used —
// that is what "deduped by row" means here: the repeats never produce a
// second hit, and their presence or count is not reported.
func mdPreviewExtractMarkers(rendered string) []mdPreviewMarkerHit {
	var hits []mdPreviewMarkerHit
	for row, line := range strings.Split(rendered, "\n") {
		for _, k := range mdPreviewMarkerKinds {
			idx := strings.Index(line, mdPreviewMarker(k.id))
			if idx < 0 {
				continue
			}
			before := xansi.Strip(line[:idx])
			hits = append(hits, mdPreviewMarkerHit{
				kind:   k.kind,
				row:    row,
				midRow: !mdPreviewIsChrome(before),
			})
		}
	}
	return hits
}
