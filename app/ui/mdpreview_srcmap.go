package ui

import (
	"log"
	"sort"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	gast "github.com/yuin/goldmark/ast"
	gtext "github.com/yuin/goldmark/text"

	"github.com/umputun/revdiff/app/diff"
)

// mdPreviewBlockAnchor ties one annotatable markdown block to both coordinate
// systems the preview has to live in at once: the rendered row range the block
// occupies on screen, and the source lines it came from.
//
// row/endRow are 0-based row indices into the FINAL render (mermaid art
// already spliced in), inclusive on both ends. endRow is the block's LAST ROW
// WITH VISIBLE TEXT ON IT, not the row before the next block starts: glamour
// pads between blocks, and the padding rows belong to no block. That is what
// makes endRow usable as a splice point — an annotation spliced after the
// padding would float a blank row below the block it comments on (see
// spliceRow).
//
// startLine/endLine are indices
// into the []diff.DiffLine the render was produced from — i.e. the same
// coordinate m.nav.diffCursor uses, not a 1-based file line number and not a
// line number of the intermediate placeholder document. That choice is what
// lets a preview annotation be saved through the ordinary source-view path
// (diffLineNum(m.file.lines[i]) / m.file.lines[i].ChangeType), producing an
// annotation indistinguishable from one made with preview off.
type mdPreviewBlockAnchor struct {
	kind      mdPreviewBlockKind
	row       int
	endRow    int
	startLine int
	endLine   int
}

// mdPreviewSourceMap is the result of agreeing two independently produced
// sequences: the block targets goldmark found in the source, and the markers
// glamour left in the render. aligned is the safety net — false means the two
// disagreed and NOTHING may be anchored, so anchors is empty and every query
// reports "no answer". There is no partial map: a map that is right about some
// blocks and silently wrong about others is the one outcome this whole
// mechanism exists to avoid.
//
// The "aligned=false implies no anchors" invariant is enforced where the value
// is built, not where it is read: every disagreement path in this file returns
// the zero mdPreviewSourceMap, and mdPreviewBuildSourceMap is the only place
// that ever sets aligned=true. Both fields are unexported so no caller outside
// this package's preview files can construct a value that breaks it.
// Two fields are NOT produced here, and each is owned by exactly one later
// pass over the render — the pass that created the rows it describes, because
// that pass is the only place that knows where they landed. A map straight out
// of mdPreviewRenderWithMap always has both empty.
//
//   - lines is where the raw source rows of an expanded block ended up, owned
//     by mdPreviewExpandBlock (mdpreview_expand.go). One entry per painted
//     non-blank raw line. Empty whenever no block is expanded.
//   - annots is where the annotation rows spliced into the render ended up,
//     owned by mdPreviewPaintAnnotationsTracked (mdpreview_annotate.go). See
//     mdPreviewAnnotAnchor and mdPreviewSourceMap.stops (mdpreview_stops.go).
//
// The order the two run in is load-bearing: expansion first, so the painter
// receives a map already in expanded row coordinates and needs no knowledge of
// expansion for its own shifting to stay exact.
type mdPreviewSourceMap struct {
	aligned bool
	anchors []mdPreviewBlockAnchor
	lines   []mdPreviewLineAnchor
	annots  []mdPreviewAnnotAnchor
}

// blocks returns the anchors in document order.
func (sm mdPreviewSourceMap) blocks() []mdPreviewBlockAnchor {
	return sm.anchors
}

// resolveBlock resolves which block a line-level annotation should be spliced
// under. idx/idxOK are the annotation's line already resolved through
// mdPreviewLineIndex (see mdPreviewRenderOne for why the lookup is the
// caller's). Caller guarantees len(sm.blocks()) > 0.
//
// Three outcomes, matching this map's own resolution rules:
//   - the line still exists and anchorAtLine finds a containing block (the
//     block starts exactly there, or the line falls inside a block's span
//     without being its first line, e.g. a mid-paragraph annotation): that
//     block.
//   - the line still exists but precedes every block (there is nothing to be
//     "inside", e.g. an annotation on a blank separator line before the very
//     first block): the first block — the nearest one there is.
//   - the line no longer resolves to any current diff line at all (the file
//     changed since the annotation was saved): the last block, so an orphaned
//     annotation is never lost, just pinned to the end of the document.
func (sm mdPreviewSourceMap) resolveBlock(idx int, idxOK bool) int {
	if !idxOK {
		return len(sm.anchors) - 1
	}
	if bi := sm.anchorAtLine(idx); bi >= 0 {
		return bi
	}
	return 0
}

// spliceRow answers the two questions the annotation painter actually has:
// under WHICH RENDERED ROW must this annotation's rows be spliced, and which
// block do they belong to. idx/idxOK are the annotation's line already resolved
// through mdPreviewLineIndex, exactly as resolveBlock takes them. Caller
// guarantees len(sm.blocks()) > 0.
//
// Without expansion the two questions have one answer, which is why the painter
// used to ask only for the block: every annotation of block i is spliced at
// anchors[i].endRow. Inside an EXPANDED block that is wrong by the height of the
// block — a comment on raw line 4 of a thirty-line table would paint below raw
// line 30, and so would the input the reader is watching themselves type. So a
// splice point is a ROW here, not a block.
//
// row is the raw line's own anchor row when idx is a line of the expanded block,
// and the owning block's endRow otherwise — the block's last row with text on
// it, so the annotation hugs the block the way the diff pane's annotation hugs
// its line (see mdPreviewBlockAnchor).
//
// block follows the same split: the
// LINE ANCHOR's own block for a raw line, rather than resolveBlock's answer,
// which for nested constructs (a code fence inside an expanded blockquote) can
// name an inner block whose rows sit elsewhere and would break the ascending-row
// grouping stops() walks. Everything else is resolveBlock's answer with all
// three of its outcomes unchanged, orphans and pre-first-block lines included.
func (sm mdPreviewSourceMap) spliceRow(idx int, idxOK bool) (row, block int) {
	if idxOK {
		for _, la := range sm.lines {
			if la.lineIdx == idx {
				return la.row, la.block
			}
		}
	}
	block = sm.resolveBlock(idx, idxOK)
	if block < 0 || block >= len(sm.anchors) {
		return 0, block // defensive: resolveBlock cannot land out of range for a non-empty map
	}
	return sm.anchors[block].endRow, block
}

// anchorAtRow answers "which block did this rendered row come from?" — the one
// question the whole feature reduces to, once a caller has the block it can
// read the source line off it. It returns the index of the block owning row
// (the last block whose row is at or before it), or -1. A row that belongs to
// no block (glamour's top margin aside, that is padding between blocks)
// resolves to the nearest preceding one; -1 means row precedes the first block
// entirely, or the map is not aligned.
func (sm mdPreviewSourceMap) anchorAtRow(row int) int {
	if !sm.aligned || len(sm.anchors) == 0 {
		return -1
	}
	// anchors are strictly increasing in row (enforced by alignment), so the
	// first anchor starting after row bounds the search.
	i := sort.Search(len(sm.anchors), func(i int) bool { return sm.anchors[i].row > row })
	if i == 0 {
		return -1
	}
	return i - 1
}

// anchorAtLine is anchorAtRow's inverse: the index of the block owning source
// line line, or -1. A line no block claims (a blank separator line, a line
// inside a construct that was folded into an enclosing block) resolves to the
// nearest preceding block; -1 means line precedes the first block, or the map
// is not aligned.
//
// Where blocks nest — a code fence inside a blockquote claims lines the quote
// also spans — the LAST block starting at or before the line wins, which is
// the innermost one, matching the "deepest block owning a start line wins"
// rule the block walk already applies.
func (sm mdPreviewSourceMap) anchorAtLine(line int) int {
	if !sm.aligned || len(sm.anchors) == 0 {
		return -1
	}
	i := sort.Search(len(sm.anchors), func(i int) bool { return sm.anchors[i].startLine > line })
	if i == 0 {
		return -1
	}
	return i - 1
}

// mdPreviewRenderWithMap renders lines as a markdown preview exactly the way
// renderMarkdownDocument does — same style, same width floor, same mermaid
// splice — and additionally returns the map from rendered rows back to source
// lines.
//
// The extra work over renderMarkdownDocument is: render through a marker-laden
// copy of the style (mdPreviewStyleWithMarkers), read the markers back out,
// agree them against goldmark's own block walk, then remove them again. The
// render this returns is VISUALLY identical to renderMarkdownDocument's —
// identical under ansi.Strip, identical in row count, identical in per-row
// display width — but not byte-identical, and byte-identity is not available
// as a criterion here at all: the spike measured today's unmarked preview
// render producing different bytes between two runs of the same document for
// roughly a quarter of a real corpus. See the plan's Technical Details.
//
// On a glamour construction or render error it degrades the same way
// renderMarkdownDocument does (returns the plain mermaid-substituted document)
// with an unaligned map, so preview stays readable and simply refuses to
// anchor.
//
// ⚠️ Known limitation, measured not assumed: with noColors set the map
// effectively never aligns — 0 of 59 real documents did. The ASCII style
// glamour uses for that mode places its style prefixes differently from the
// colored one: a heading writes its prefix on the row AFTER its text as well
// as on it, and a task item's prefix reappears on the next list row, so the
// marker sequence stops being one marker per block. Alignment is still
// attempted (the cost is one parse, and a future glamour would simply start
// working) and the designed degradation carries the outcome: --no-colors
// preview renders exactly as before and refuses to anchor annotations.
func mdPreviewRenderWithMap(lines []diff.DiffLine, width int, noColors bool) (string, mdPreviewSourceMap) {
	nonce := mermaidNonce()
	doc, arts, origins := mermaidPlaceholderDocument(lines, nonce, width)
	w := max(width, mdPreviewMinWidth)

	base := mdPreviewStyle
	opts := []glamour.TermRendererOption{glamour.WithWordWrap(w)}
	if noColors {
		base = mdPreviewStyleNoColor
		opts = append(opts, glamour.WithColorProfile(termenv.Ascii))
	}
	marked := mdPreviewStyleWithMarkers(base, mdPreviewMarkerKinds)
	opts = append(opts, glamour.WithStyles(marked))

	r, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		log.Printf("[WARN] create glamour renderer: %v", err)
		return doc, mdPreviewSourceMap{}
	}
	out, err := r.Render(doc)
	if err != nil {
		log.Printf("[WARN] render markdown preview: %v", err)
		return doc, mdPreviewSourceMap{}
	}

	targets := mdPreviewBlockTargets(doc)
	rows, aligned := mdPreviewAlignRows(targets, mdPreviewExtractMarkers(out), mdPreviewQuoteParagraphs(doc))

	out = mdPreviewCleanMarkers(out, noColors)
	out, shifts := spliceMermaidArtTracked(out, nonce, arts)
	if !aligned {
		return out, mdPreviewSourceMap{}
	}
	return out, mdPreviewBuildSourceMap(targets, rows, shifts, origins, out)
}

// mdPreviewCleanMarkers removes every trace of the markers from a rendered
// document: the marker byte sequences themselves by exact match, then the
// empty SET/RESET SGR pairs stripping one out of an otherwise-empty style
// prefix leaves behind.
//
// In no-colors mode it finishes with ansi.Strip as a net. That mode promises
// zero ANSI in the RENDERED DOCUMENT (see mdPreviewStyleNoColor, which spells
// out what the promise does and does not cover — annotation rows spliced in
// later carry their own italic escapes in every mode), and the markers are ANSI
// by construction — a marker that somehow escaped the exact-match pass would
// break that promise in the one mode that states it, so the cheap
// belt-and-braces pass is worth its cost. It is safe there and only there:
// with colors on, ansi.Strip would remove the render's actual styling.
func mdPreviewCleanMarkers(rendered string, noColors bool) string {
	out := mdPreviewDropEmptySGRPairs(mdPreviewStripMarkers(rendered))
	if noColors {
		out = ansi.Strip(out)
	}
	return out
}

// mdPreviewKindMatches reports whether a marker hit of kind hit can stand for
// a block target of kind want. Equal kinds always match. The one bridged pair
// is "task": glamour renders a checkbox list item through Styles.Task, so its
// row carries a task marker where the block walk emits an item or enumeration
// target for the same construct (see mdBlockTask in mdpreview_blocks.go).
func mdPreviewKindMatches(hit, want mdPreviewBlockKind) bool {
	if hit == want {
		return true
	}
	return hit == mdBlockTask && (want == mdBlockItem || want == mdBlockEnum)
}

// mdPreviewAlignRows is the safety net: it agrees the goldmark block sequence
// against the marker sequence glamour left in the render and returns, per
// target, the rendered row that target starts on. It returns ok=false the
// moment the two disagree in any way — a kind that does not match, a sequence
// that runs out, a leftover marker nobody claimed, or a row that fails to
// advance. Nothing is guessed and nothing partial is returned, because a map
// that is quietly wrong about one block would put a reader's comment on a
// different paragraph than the one they were looking at.
//
// Two documented folds are what keep the two sequences comparable at all;
// both come from the block walk (mdpreview_blocks.go) treating a construct as
// one annotatable unit where glamour emits its style prefix more than once:
//
//   - a table gets ONE target, but glamour writes Table.Prefix on every
//     rendered table row. The first is the table's row; the rest are consumed
//     and discarded. Consequence, accepted and recorded as a limitation: two
//     tables separated by nothing but a blank line produce two indistinguishable
//     runs of table markers, so the document fails alignment and degrades
//     rather than mis-anchoring.
//   - a blockquote gets ONE target that swallows its own paragraphs, but
//     glamour still writes Paragraph.Prefix for each of them. quoteParagraphs
//     carries the exact per-quote count (see mdPreviewQuoteParagraphs), which
//     is what makes those extra paragraph markers accountable instead of
//     merely skippable: the first is consumed as the quote's own leading
//     marker where present, the rest become a budget that is drained before
//     each later target. Draining first is correct because document order
//     guarantees a previous quote's leftover paragraphs precede everything
//     that follows the quote.
func mdPreviewAlignRows(targets []mdPreviewBlockTarget, hits []mdPreviewMarkerHit, quoteParagraphs []int) ([]int, bool) {
	st := &mdAlignState{hits: hits, quoteParagraphs: quoteParagraphs, prevRow: -1}
	rows := make([]int, len(targets))
	for i, tg := range targets {
		st.drainQuoteParagraphs()
		row, ok := st.take(tg.kind)
		if !ok || row <= st.prevRow {
			return nil, false
		}
		rows[i], st.prevRow = row, row
	}
	st.drainQuoteParagraphs()
	if st.pos != len(hits) {
		return nil, false // a marker no target claimed: the two sequences disagree
	}
	return rows, true
}

// mdAlignState is mdPreviewAlignRows' cursor over the marker sequence. It is a
// struct rather than a closure set so the three consumption rules (ordinary,
// table, blockquote) read as named steps.
type mdAlignState struct {
	hits            []mdPreviewMarkerHit
	quoteParagraphs []int
	pos             int // next unconsumed hit
	quoteIdx        int // next unconsumed entry of quoteParagraphs
	budget          int // quote-owned paragraph markers still to be discarded
	prevRow         int
}

// peek reports the kind of the next unconsumed hit.
func (s *mdAlignState) peek() (mdPreviewBlockKind, bool) {
	if s.pos >= len(s.hits) {
		return "", false
	}
	return s.hits[s.pos].kind, true
}

// drainQuoteParagraphs discards the paragraph markers a previous blockquote
// owns but has not yet accounted for. It stops at the first non-paragraph
// marker, so a quote's own nested code block or list — which gets its own
// target — is never swallowed by the drain.
func (s *mdAlignState) drainQuoteParagraphs() {
	for s.budget > 0 {
		k, ok := s.peek()
		if !ok || k != mdBlockParagraph {
			return
		}
		s.pos++
		s.budget--
	}
}

// take consumes the marker(s) belonging to one target of kind want and returns
// the rendered row that target starts on.
func (s *mdAlignState) take(want mdPreviewBlockKind) (int, bool) {
	switch want {
	case mdBlockQuote:
		return s.takeQuote()
	case mdBlockTable:
		return s.takeTable()
	default:
		k, ok := s.peek()
		if !ok || !mdPreviewKindMatches(k, want) {
			return 0, false
		}
		row := s.hits[s.pos].row
		s.pos++
		return row, true
	}
}

// takeQuote consumes a blockquote's markers: the leading Paragraph.Prefix of
// its first paragraph when the quote opens with one, then BlockQuote.Prefix
// itself. Whatever paragraphs remain become the drain budget, because they can
// be interleaved with the quote's nested targets and so cannot be consumed
// here.
func (s *mdAlignState) takeQuote() (int, bool) {
	paragraphs := 0
	if s.quoteIdx < len(s.quoteParagraphs) {
		paragraphs = s.quoteParagraphs[s.quoteIdx]
	}
	s.quoteIdx++

	if k, ok := s.peek(); ok && k == mdBlockParagraph && paragraphs > 0 {
		s.pos++
		paragraphs--
	}
	k, ok := s.peek()
	if !ok || k != mdBlockQuote {
		return 0, false
	}
	row := s.hits[s.pos].row
	s.pos++
	s.budget += paragraphs
	return row, true
}

// takeTable consumes a whole table: its first Table.Prefix marker gives the
// row, and every immediately following table marker is one more rendered row
// of the same table and is discarded.
func (s *mdAlignState) takeTable() (int, bool) {
	k, ok := s.peek()
	if !ok || k != mdBlockTable {
		return 0, false
	}
	row := s.hits[s.pos].row
	s.pos++
	for {
		k, ok := s.peek()
		if !ok || k != mdBlockTable {
			return row, true
		}
		s.pos++
	}
}

// mdPreviewQuoteParagraphs counts, per blockquote in document order, how many
// paragraphs are its DIRECT children — exactly the paragraphs glamour marks
// but the block walk folds into the quote's single target (see
// mdPreviewBlockTargets' KindParagraph case). A paragraph nested deeper, e.g.
// inside a list item inside the quote, is not counted here because glamour
// renders it as an empty element and so never marks it.
//
// This re-parses doc with the same goldmark instance the block walk uses
// rather than having mdPreviewBlockTargets return the counts alongside its
// targets. The parse is deterministic and the render is cached, so the cost is
// one extra parse per cache miss; the gain is that the block walk's returned
// shape stays exactly what its own task defined and its tests pin.
func mdPreviewQuoteParagraphs(doc string) []int {
	root := mdBlockMarkdown.Parser().Parse(gtext.NewReader([]byte(doc)))
	var counts []int
	_ = gast.Walk(root, func(n gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering || n.Kind() != gast.KindBlockquote {
			return gast.WalkContinue, nil
		}
		paragraphs := 0
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if c.Kind() == gast.KindParagraph {
				paragraphs++
			}
		}
		counts = append(counts, paragraphs)
		return gast.WalkContinue, nil
	})
	return counts
}

// mdPreviewShiftRow translates a row of the pre-splice render into its row in
// the final render. Markers are read off the render glamour produced, where
// each mermaid diagram is still a single placeholder row; splicing the art in
// afterwards turns that one row into many and pushes everything below it down.
// A row at or before a splice point does not move.
func mdPreviewShiftRow(shifts []mdPreviewArtShift, row int) int {
	shifted := row
	for _, s := range shifts {
		if s.row >= row {
			break // shifts are row-ascending
		}
		shifted += s.added
	}
	return shifted
}

// mdPreviewBuildSourceMap turns an agreed target/row pairing into the finished
// map: rows are moved into final-render coordinates, source line spans are
// translated out of the intermediate placeholder document and into indices of
// the original []diff.DiffLine via origins, and each block's endRow is closed
// off at its last row with text on it — the search runs back from the row
// before the next block starts (from the end of the render for the last block),
// skipping the blank rows glamour pads between blocks with.
//
// Trimming that padding off here, rather than at the one place that was hurt by
// it, is what keeps "where does an annotation attach" a single answer: the
// painter still splices at endRow and shifts by a strict prefix sum over the
// splice rows, so an annotation's rows still land BELOW the block's own span and
// the block highlight still marks the block alone. The alternative — endRow kept
// as the padded span, trimmed only when a splice row is computed — would put the
// splice inside the span and drag the highlight over the annotation with it.
// Every other reader of endRow is unaffected or improved by the trim: the
// highlight already skips blank rows, and the expansion pass trims the same
// rows itself before replacing a block (mdPreviewTrailingBlankRows), so it now
// finds nothing left to trim and replaces exactly the rows it did before.
//
// It refuses the whole map — the same all-or-nothing degrade
// mdPreviewAlignRows applies to rows — when startLine is not strictly
// increasing across the targets. mdPreviewAlignRows already enforces that on
// the RENDER side, and enforcing the same on the SOURCE side is not
// redundant: the two sequences are produced independently, so a goldmark-side
// mistake (the block walk resolving two blocks onto one source line) passes
// the row check untouched. It is not a theoretical class either — consecutive
// thematic breaks used to do exactly that (see mdBreakResolver,
// mdpreview_blocks.go), and the consequence was worse than a misplaced
// comment: two blocks sharing a source line means two annotations sharing
// annotation.Store's (Line, Type) key, and Add REPLACES on a key collision, so
// the first reader's comment was silently destroyed by the second.
func mdPreviewBuildSourceMap(targets []mdPreviewBlockTarget, rows []int, shifts []mdPreviewArtShift,
	origins []int, rendered string) mdPreviewSourceMap {
	anchors := make([]mdPreviewBlockAnchor, 0, len(targets))
	prevLine := -1
	for i, tg := range targets {
		start, sok := mdPreviewOriginOf(origins, tg.startLine)
		end, eok := mdPreviewOriginOf(origins, tg.endLine)
		if !sok || !eok {
			// a target pointing outside the document it was parsed from means
			// the two halves disagree about the document itself; refuse the
			// whole map rather than dropping one block out of it.
			return mdPreviewSourceMap{}
		}
		if start <= prevLine {
			return mdPreviewSourceMap{}
		}
		prevLine = start
		anchors = append(anchors, mdPreviewBlockAnchor{
			kind:      tg.kind,
			row:       mdPreviewShiftRow(shifts, rows[i]),
			startLine: start,
			endLine:   max(start, end),
		})
	}

	renderRows := strings.Split(rendered, "\n")
	lastRow := len(renderRows) - 1
	for i := range anchors {
		span := lastRow
		if i+1 < len(anchors) {
			span = anchors[i+1].row - 1
		}
		anchors[i].endRow = mdPreviewLastContentRow(renderRows, anchors[i].row, max(anchors[i].row, span))
	}
	return mdPreviewSourceMap{aligned: true, anchors: anchors}
}

// mdPreviewLastContentRow is the last row of the span start..end that has
// visible text on it, or start when every row after it is blank. It is how a
// block's endRow stops at the block instead of running through the padding
// glamour leaves behind it; mdPreviewTrailingBlankRows (mdpreview_expand.go)
// does the counting, so the expansion pass and this one agree on what "blank"
// means by construction rather than by two matching definitions.
//
// Out-of-range input is clamped rather than trusted: this runs over the rows of
// the very render the anchors were built from, so a span past the end cannot
// happen, but an index panic in the render path would take the whole TUI down.
func mdPreviewLastContentRow(rows []string, start, end int) int {
	if start < 0 || start >= len(rows) {
		return max(start, 0)
	}
	if end >= len(rows) {
		end = len(rows) - 1
	}
	if end <= start {
		return start
	}
	return end - mdPreviewTrailingBlankRows(rows, start, end)
}

// mdPreviewOriginOf maps a 1-based line number of the placeholder document to
// the index of the diff line that produced it. ok is false for a line number
// the document does not have.
func mdPreviewOriginOf(origins []int, docLine int) (int, bool) {
	if docLine < 1 || docLine > len(origins) {
		return 0, false
	}
	return origins[docLine-1], true
}
