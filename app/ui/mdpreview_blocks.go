package ui

import (
	"strings"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	gext "github.com/yuin/goldmark/extension"
	gextast "github.com/yuin/goldmark/extension/ast"
	gparser "github.com/yuin/goldmark/parser"
	gtext "github.com/yuin/goldmark/text"
)

// mdPreviewBlockKind identifies one of the block kinds this feature can
// anchor an annotation to. It is the SINGLE shared vocabulary between this
// file's goldmark block walk and mdpreview_marker.go's marker table
// (mdPreviewMarkerKind.kind field) — one type, not a pair of enums with a
// mapping between them, because the two sides track the same block kinds by
// construction: this walk decides which lines a comment on "the h2" or "the
// table" covers, the marker table decides which rendered row glamour put
// that same h2/table on, and task 3's alignment is a direct kind-for-kind
// comparison between the two, which only works cleanly if both read from
// one enum. Values: "paragraph", "h1".."h6", "item", "enumeration",
// "code_block", "block_quote", "table", "hr", "html_block".
// There is deliberately no generic "heading" value — glamour never emits a
// marker for it (see the spike's "Decided by the spike" notes), only the
// level-specific h1..h6 kinds do, so mdPreviewBlockTargets never produces
// one either. This walk does not need a "task" value (see
// mdPreviewMarkerKinds' doc comment in mdpreview_marker.go for why the
// marker side excludes it too) because a task-list item is folded into its
// enclosing item/enumeration target — see the KindListItem case below.
type mdPreviewBlockKind string

const (
	mdBlockParagraph mdPreviewBlockKind = "paragraph"
	mdBlockH1        mdPreviewBlockKind = "h1"
	mdBlockH2        mdPreviewBlockKind = "h2"
	mdBlockH3        mdPreviewBlockKind = "h3"
	mdBlockH4        mdPreviewBlockKind = "h4"
	mdBlockH5        mdPreviewBlockKind = "h5"
	mdBlockH6        mdPreviewBlockKind = "h6"
	mdBlockItem      mdPreviewBlockKind = "item"
	mdBlockEnum      mdPreviewBlockKind = "enumeration"
	mdBlockCodeBlock mdPreviewBlockKind = "code_block"
	mdBlockQuote     mdPreviewBlockKind = "block_quote"
	mdBlockTable     mdPreviewBlockKind = "table"
	mdBlockHR        mdPreviewBlockKind = "hr"
	mdBlockHTMLBlock mdPreviewBlockKind = "html_block"

	// mdBlockTask is produced by the MARKER side only (see
	// mdPreviewMarkerKinds in mdpreview_marker.go): glamour renders a
	// checkbox list item through Styles.Task instead of Styles.Item, so its
	// rendered row carries a "task" marker where this walk emits an
	// mdBlockItem / mdBlockEnum target. mdPreviewBlockTargets never produces
	// this kind — mdPreviewKindMatches (mdpreview_srcmap.go) is what bridges
	// the two sides.
	mdBlockTask mdPreviewBlockKind = "task"
)

// mdPreviewBlockTarget is one annotatable unit: a tracked block kind and the
// 1-based, inclusive source line span it owns. Targets returned by
// mdPreviewBlockTargets are in document order and non-overlapping — every
// source line belongs to at most one target (see the "deepest block owning
// a start line wins" note on swallowedSpan).
type mdPreviewBlockTarget struct {
	Kind      mdPreviewBlockKind
	StartLine int
	EndLine   int
}

// mdBlockMarkdown is the goldmark instance used to locate block targets. Its
// extension set MUST match glamour's own parser exactly (see
// vendor/github.com/charmbracelet/glamour/glamour.go's NewTermRenderer) —
// GFM (which brings the table extension) plus DefinitionList, with
// auto-heading-IDs on. A mismatch here (e.g. missing the table extension)
// would make this walk see a table as a paragraph while glamour renders it
// as a table, disagreeing with the render on block boundaries before
// alignment (task 3) ever gets a chance to compare them.
var mdBlockMarkdown = goldmark.New(
	goldmark.WithExtensions(gext.GFM, gext.DefinitionList),
	goldmark.WithParserOptions(gparser.WithAutoHeadingID()),
)

// mdPreviewBlockTargets parses doc (as produced by mermaidPlaceholderDocument
// — this function itself is agnostic to that, it just walks whatever
// markdown text it is given) and returns the ordered, non-overlapping list
// of annotatable block targets.
//
// Container kinds that glamour renders as a single unit — a list item's own
// paragraph, a blockquote's paragraph(s), a table's rows and cells — do not
// get their own targets; they are folded into the target of the kind that
// owns their chrome (item/enumeration, block_quote, table respectively). See
// swallowedSpan for why, and blockLineSpan for the mechanics of turning
// goldmark's per-line text.Segments into a source line span.
func mdPreviewBlockTargets(doc string) []mdPreviewBlockTarget {
	src := []byte(doc)
	root := mdBlockMarkdown.Parser().Parse(gtext.NewReader(src))
	idx := newMdLineIndex(doc)

	var targets []mdPreviewBlockTarget
	add := func(kind mdPreviewBlockKind, start, end int, ok bool) {
		if ok {
			targets = append(targets, mdPreviewBlockTarget{Kind: kind, StartLine: start, EndLine: end})
		}
	}

	_ = gast.Walk(root, func(n gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}
		switch n.Kind() {
		case gast.KindHeading:
			kind, ok := headingBlockKind(n.(*gast.Heading).Level)
			if ok {
				start, end, hok := blockLineSpan(n, idx)
				add(kind, start, end, hok)
			}
			return gast.WalkSkipChildren, nil

		case gast.KindParagraph:
			// A paragraph directly inside a list item renders nothing at all
			// (see vendor/.../glamour/ansi/elements.go's KindParagraph case,
			// which returns an empty Element for that parent) — it is not a
			// target, the enclosing item already is one. A paragraph inside a
			// blockquote DOES render, but under the quote's own chrome, not
			// as an independent paragraph — see swallowedSpan's doc comment.
			if p := n.Parent(); p != nil && (p.Kind() == gast.KindListItem || p.Kind() == gast.KindBlockquote) {
				return gast.WalkSkipChildren, nil
			}
			start, end, ok := blockLineSpan(n, idx)
			add(mdBlockParagraph, start, end, ok)
			return gast.WalkSkipChildren, nil

		case gast.KindListItem:
			list, ok := n.Parent().(*gast.List)
			if !ok {
				return gast.WalkContinue, nil
			}
			kind := mdBlockItem
			if list.IsOrdered() {
				kind = mdBlockEnum
			}
			start, end, sok := swallowedSpan(n, idx)
			add(kind, start, end, sok)
			// Continue descending: a nested list under this item gets its
			// own item/enumeration targets, not swallowed by this one (see
			// isMdSpanBoundary — swallowedSpan already excluded those lines
			// from the span just added).
			return gast.WalkContinue, nil

		case gast.KindCodeBlock, gast.KindFencedCodeBlock:
			start, end, ok := blockLineSpan(n, idx)
			add(mdBlockCodeBlock, start, end, ok)
			return gast.WalkSkipChildren, nil

		case gast.KindBlockquote:
			start, end, ok := swallowedSpan(n, idx)
			add(mdBlockQuote, start, end, ok)
			// Continue descending for the same reason as KindListItem: a
			// nested list, table, code block or heading inside the quote is
			// itself a tracked kind and gets its own target.
			return gast.WalkContinue, nil

		case gextast.KindTable:
			// One target for the whole table (decided by the spike: "Tables
			// are one target each" — a comment on a table means "this
			// table", not "this row"). Never descend into rows/cells.
			start, end, ok := blockLineSpan(n, idx)
			add(mdBlockTable, start, end, ok)
			return gast.WalkSkipChildren, nil

		case gast.KindThematicBreak:
			line, ok := thematicBreakLine(n, idx, doc)
			add(mdBlockHR, line, line, ok)
			return gast.WalkContinue, nil

		case gast.KindHTMLBlock:
			start, end, ok := blockLineSpan(n, idx)
			add(mdBlockHTMLBlock, start, end, ok)
			return gast.WalkSkipChildren, nil
		}
		return gast.WalkContinue, nil
	})
	return targets
}

// headingBlockKind maps a goldmark heading level (1..6) to its tracked
// block kind. false for any level outside that range, which goldmark itself
// never produces (ATX headings are capped at 6 "#" and setext headings only
// ever produce level 1 or 2), so the only realistic caller of this with
// ok==false would be a future goldmark change — handled by simply skipping
// the target rather than panicking.
func headingBlockKind(level int) (mdPreviewBlockKind, bool) {
	switch level {
	case 1:
		return mdBlockH1, true
	case 2:
		return mdBlockH2, true
	case 3:
		return mdBlockH3, true
	case 4:
		return mdBlockH4, true
	case 5:
		return mdBlockH5, true
	case 6:
		return mdBlockH6, true
	default:
		return "", false
	}
}

// mdLineIndex maps a byte offset within the document it was built from to
// its 1-based source line number. idx[i] holds the byte offset where line
// i+1 begins (idx[0] == 0, the start of line 1); a new entry is appended
// after every '\n'.
type mdLineIndex []int

func newMdLineIndex(doc string) mdLineIndex {
	idx := make(mdLineIndex, 1, 64)
	idx[0] = 0
	for i := range len(doc) {
		if doc[i] == '\n' {
			idx = append(idx, i+1)
		}
	}
	return idx
}

// lineAt returns the 1-based line number containing byteOffset.
func (idx mdLineIndex) lineAt(byteOffset int) int {
	lo, hi := 0, len(idx)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if idx[mid] <= byteOffset {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}

// lastLine returns the 1-based number of the last line the index covers.
func (idx mdLineIndex) lastLine() int {
	return len(idx)
}

// blockLineSpan returns the 1-based, inclusive source line span covering n
// and every descendant that carries its own position. Goldmark records one
// text.Segment per physical line on leaf raw-text block nodes (Heading,
// Paragraph, CodeBlock/FencedCodeBlock, HTMLBlock, TableCell) and records
// nothing at all on pure container nodes (List, ListItem, Blockquote,
// Table, TableRow, TableHeader) — verified empirically against this
// project's vendored goldmark, not assumed — so this recurses through
// containers to find the leaves that do carry one.
//
// ok is false only when the whole subtree carries no position at all: an
// empty node, or a ThematicBreak (see thematicBreakLine — it is the one
// tracked kind with zero text and so zero Lines() anywhere in its subtree).
func blockLineSpan(n gast.Node, idx mdLineIndex) (start, end int, ok bool) {
	if n.Type() == gast.TypeBlock {
		lines := n.Lines()
		if lines.Len() > 0 {
			start = idx.lineAt(lines.At(0).Start)
			end = idx.lineAt(lines.At(lines.Len() - 1).Start)
			ok = true
		}
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if c.Type() == gast.TypeInline {
			continue // ast.BaseInline.Lines() panics if called; inline nodes carry no block position of their own anyway
		}
		cs, ce, cok := blockLineSpan(c, idx)
		if !cok {
			continue
		}
		if !ok || cs < start {
			start = cs
		}
		if !ok || ce > end {
			end = ce
		}
		ok = true
	}
	return start, end, ok
}

// swallowedSpan computes the span a "swallowing" container claims for
// itself — used for KindListItem and KindBlockquote. It aggregates over the
// container's own direct content (its own paragraph text) but stops at any
// child that is itself a boundary kind (see isMdSpanBoundary): a nested
// list, blockquote, table, heading, code block, HTML block or thematic
// break is itself a tracked kind and gets its own separate target later in
// the same walk, so those lines must not also be claimed here.
//
// This is "the deepest block owning a start line wins" from the task
// checklist, implemented as exclusion rather than a later dedup pass: a
// nested list's first item and this item's own text can never both claim
// the nested item's line, because swallowedSpan never looks past the nested
// List boundary in the first place. The same mechanism gives block_quote
// and table their "one target for the whole construct" treatment (decided
// for table by the spike; extended here to list item and blockquote, which
// swallow their own paragraph content the same way — see the KindParagraph
// case in mdPreviewBlockTargets for why: item's paragraph renders nothing
// at all, blockquote's paragraph(s) render under the quote's own chrome).
func swallowedSpan(n gast.Node, idx mdLineIndex) (start, end int, ok bool) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if c.Type() == gast.TypeInline || isMdSpanBoundary(c.Kind()) {
			continue
		}
		cs, ce, cok := blockLineSpan(c, idx)
		if !cok {
			continue
		}
		if !ok || cs < start {
			start = cs
		}
		if !ok || ce > end {
			end = ce
		}
		ok = true
	}
	return start, end, ok
}

// isMdSpanBoundary reports whether k is a kind that gets its own separate
// target in mdPreviewBlockTargets, and so must never have its lines folded
// into an ancestor's swallowedSpan.
func isMdSpanBoundary(k gast.NodeKind) bool {
	switch k {
	case gast.KindList, gast.KindListItem, gast.KindBlockquote, gextast.KindTable,
		gast.KindHeading, gast.KindCodeBlock, gast.KindFencedCodeBlock,
		gast.KindHTMLBlock, gast.KindThematicBreak:
		return true
	default:
		return false
	}
}

// thematicBreakLine locates a ThematicBreak's one source line. Unlike every
// other tracked kind, goldmark records no text.Segment for it at all — see
// vendor/.../goldmark/parser/thematic_break.go's Open, which returns a bare
// ast.NewThematicBreak() with nothing ever appended to its Lines() because
// the node carries no text (verified empirically: Lines().Len() == 0 for
// every ThematicBreak, with an experiment run against this project's
// vendored goldmark before writing this function).
//
// The line is recovered from context instead: a thematic break is always
// exactly one line, and it must fall strictly between the end of the
// nearest previous sibling with a known position (or the start of the
// parent, if it is the first child) and the start of the nearest next
// sibling with a known position (or the end of the document, if it is the
// last child) — nothing else can occupy that gap. Within the gap, the first
// non-blank line is the break.
//
// Known limitation: back-to-back thematic breaks with nothing recoverable
// between them (a previous sibling that is itself an unresolved
// ThematicBreak) fall back to whatever the next ancestor level offers as
// the lower bound, which can misplace the second break if there is other
// blank-line padding before it. This is rare enough in practice (and absent
// from the task's required test set) that it is accepted rather than
// solved recursively here.
func thematicBreakLine(n gast.Node, idx mdLineIndex, doc string) (line int, ok bool) {
	lo, hi := thematicBreakLowerBound(n, idx), thematicBreakUpperBound(n, idx)

	lines := strings.Split(doc, "\n")
	for l := lo; l <= hi && l <= len(lines); l++ {
		if strings.TrimSpace(lines[l-1]) != "" {
			return l, true
		}
	}
	return 0, false
}

// thematicBreakLowerBound returns the earliest line thematicBreakLine may
// search from: the line right after the end of the nearest preceding
// sibling of n, searching upward through ancestors when a level has no
// previous sibling of its own (n's parent's parent's previous sibling, and
// so on) — bubbling up rather than falling back to the immediate parent's
// own aggregate span, which would be wrong here: a parent's blockLineSpan
// aggregates over EVERY resolved descendant regardless of document order,
// so on a document that opens with an unresolved ThematicBreak it would
// jump straight to whatever comes after the break instead of bounding to
// the true start of the document. Returns 1 if no ancestor level has a
// previous sibling with a known position — n is the very first block.
func thematicBreakLowerBound(n gast.Node, idx mdLineIndex) int {
	for cur := n; cur != nil; cur = cur.Parent() {
		prev := cur.PreviousSibling()
		if prev == nil {
			continue
		}
		if _, e, ok := blockLineSpan(prev, idx); ok {
			return e + 1
		}
		// prev itself carries no position (e.g. another unresolved
		// ThematicBreak) — nothing better to try at this level, keep
		// walking up.
	}
	return 1
}

// thematicBreakUpperBound is the mirror of thematicBreakLowerBound: the
// line right before the start of the nearest following sibling, bubbling up
// through ancestors the same way, or the last line of the document if none
// exists.
func thematicBreakUpperBound(n gast.Node, idx mdLineIndex) int {
	for cur := n; cur != nil; cur = cur.Parent() {
		next := cur.NextSibling()
		if next == nil {
			continue
		}
		if s, _, ok := blockLineSpan(next, idx); ok {
			return s - 1
		}
	}
	return idx.lastLine()
}
