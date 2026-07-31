package ui

import (
	"regexp"
	"strings"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"
	"github.com/mattn/go-runewidth"
)

// This file holds the SUBGRAPH SPLIT pass. Like mdpreview.go,
// mdpreview_transpile.go and mdpreview_flowchart.go it is patch-owned and does
// not exist upstream — see mdpreview.go's own doc comment for why new logic
// goes in new files rather than into an upstream one.
//
// # Why this exists
//
// The vendored mermaid-ascii renderer does not lay out `subgraph` blocks. It
// builds the node grid without looking at which subgraph a node belongs to,
// then draws a rectangle around the cells it guessed. Two things go wrong at
// once on a real before/after diagram: the rectangles OVERLAP (two subgraph
// titles print on the same row and one border runs through the other), and
// nodes land in the WRONG rectangle, so a label appears twice and the picture
// says something untrue. A reader then cannot tell which node belongs to which
// group, which is the only reason the author wrote a subgraph at all.
//
// The fix is to render each subgraph as its own diagram and stack them, each
// under its own title. Both halves live here: splitFlowchartSubgraphs decides
// whether a fence may be split and into which blocks, and
// stackFlowchartSubgraphs renders those blocks and joins the art. The single
// caller is renderMermaidSource, which falls back to its existing whole-source
// render whenever either half declines.
//
// # Why the refusals are not errors
//
// Measured over every .md file in the user's plan and source trees: 300
// mermaid fences, 89 use `subgraph`. Of those 89, 23 have an edge crossing
// between two subgraphs and 30 nest subgraphs. So a meaningful share of real
// diagrams must keep today's single-render behavior. Falling back is the
// normal case here, not an error path, which is why every refusal below is a
// plain `ok == false` and the caller simply renders the whole source the way
// it does today.
//
// # Order: normalize first, split second
//
// This runs on the output of normalizeFlowchartSource, never on raw fence
// text. That pass already leaves `subgraph` and `end` alone by design (see
// flowchartStructuralKeywords), so the split sees clean, normalized statements
// — every link written as `-->` or `-->|label|`, every node shape written with
// square brackets — which is what lets flowchartSubgraphNodeIDs reuse the
// normalizer's own arrow and label helpers instead of re-deriving them.

// flowchartSubgraphKeyword is the statement keyword that opens a block, and
// flowchartSubgraphEnd is the line that closes one. Named rather than spelled
// inline because flowchartStructuralKeywords is built from these same two
// constants, so this file and that list cannot drift apart.
const (
	flowchartSubgraphKeyword = "subgraph"
	flowchartSubgraphEnd     = "end"
)

// mermaidSubgraph is one top-level `subgraph ... end` block: the block's own
// id, the title to print above its art, and the body lines between the header
// and the closing `end`, in source order with their original indentation and
// any inline `%%` comment intact.
//
// The id is kept because it is a node id like any other as far as the renderer
// is concerned: `A --> groupB` is valid mermaid and points an edge at a whole
// subgraph. Rule 5 of splitFlowchartSubgraphs has to see those ids or it would
// split a fence whose blocks really do reference each other — see
// flowchartSubgraphsDisjoint.
type mermaidSubgraph struct {
	id    string
	title string
	body  []string
}

// splitFlowchartSubgraphs decides whether a normalized `graph`/`flowchart`
// source may be rendered as one diagram per subgraph, and when it may, returns
// the fence's own header line plus one block per top-level subgraph in source
// order. ok is false for every refusal — see this file's doc comment for why a
// refusal is not an error.
//
// All five of these must hold. If any one fails the caller renders the whole
// source the way it does today.
//
//  1. The diagram is a `graph` or a `flowchart`. No other kind has subgraphs.
//  2. There are at least two top-level subgraphs. With one subgraph the
//     renderer draws a single rectangle and nothing overlaps, so there is
//     nothing to fix.
//  3. No subgraph is nested inside another.
//  4. Nothing but the header, comments, blank lines and layout directives sits
//     outside a subgraph. A node declared outside would simply be dropped by
//     splitting.
//  5. No node id is mentioned by more than one subgraph. A subgraph's OWN id
//     counts as a node id here, since an edge may point straight at a whole
//     block (`A --> groupB`).
//
// The last rule is the important one and it is deliberately strict. It catches
// an edge crossing from one subgraph to another, and it also catches a node
// that two subgraphs both point at. Either case would lose an edge or silently
// duplicate a box, and one rule covers both.
//
// Two malformed shapes refuse as well, for the same reason: an unterminated
// `subgraph` and a stray `end` with nothing open. Neither can be split into
// blocks that mean what the author wrote.
//
// Rule 4 tolerates a styling or layout directive outside a subgraph, which on
// the production path never comes up — normalizeFlowchartSource has already
// removed every one of them by the time this runs. The tolerance is there so
// that a direct call on un-normalized source refuses for a real reason rather
// than for a line the renderer would have dropped anyway.
func splitFlowchartSubgraphs(source string) (header string, blocks []mermaidSubgraph, ok bool) {
	switch mermaidDiagramKind(source) {
	case "graph", "flowchart":
	default:
		return "", nil, false // rule 1
	}

	headerSeen, inBlock := false, false
	for line := range strings.SplitSeq(source, "\n") {
		trimmed := strings.TrimSpace(mermaidStripComment(line))
		if trimmed == "" {
			continue
		}
		if !headerSeen {
			headerSeen, header = true, line
			continue
		}
		switch flowchartDirective(trimmed, flowchartStructuralKeywords) {
		case flowchartSubgraphKeyword:
			if inBlock {
				return "", nil, false // rule 3: nested
			}
			id, title := flowchartSubgraphHeader(trimmed)
			blocks = append(blocks, mermaidSubgraph{id: id, title: title})
			inBlock = true
		case flowchartSubgraphEnd:
			if !inBlock {
				return "", nil, false // a stray `end`
			}
			inBlock = false
		default:
			if !inBlock {
				if flowchartDirective(trimmed, flowchartDroppedKeywords) == "" {
					return "", nil, false // rule 4: a statement outside every subgraph
				}
				continue
			}
			blocks[len(blocks)-1].body = append(blocks[len(blocks)-1].body, line)
		}
	}

	if inBlock || len(blocks) < 2 {
		return "", nil, false // an unterminated `subgraph`, or rule 2
	}
	if !flowchartSubgraphsDisjoint(blocks) {
		return "", nil, false // rule 5
	}
	return header, blocks, true
}

// mermaidSubgraphRule is the character the title underline is drawn with. A
// box-drawing horizontal, matching the art the renderer itself emits, so the
// heading does not read as a different kind of output from the diagram below
// it.
const mermaidSubgraphRule = "─"

// source renders this block as standalone diagram source: the fence's own
// header line, then the block's body lines exactly as they were written. The
// body is already normalized (see this file's doc comment) and, by rule 5 of
// splitFlowchartSubgraphs, mentions no node any other block mentions, so
// nothing outside the block is needed to draw it.
func (s mermaidSubgraph) source(header string) string {
	return header + "\n" + strings.Join(s.body, "\n") + "\n"
}

// stackFlowchartSubgraphs renders each block as its own diagram and returns
// them stacked, each under its own title and a rule of the same display width,
// with a blank line between blocks. A block whose title is empty (a bare
// `subgraph` header — see flowchartSubgraphTitle) gets no heading at all
// rather than an empty one.
//
// It is all-or-nothing: if any single block errors, renders blank, or panics
// the vendored renderer, ok is false and nothing partial comes back, so the
// caller falls back to one render of the whole source — today's behavior. A
// reader never sees a half-stacked result with one block missing.
//
// The recover matters even though renderMermaidBlock already has one. That
// outer recover falls back to the fence's VERBATIM text, which is strictly
// worse than the single whole-source render this returns to. Catching a panic
// here keeps the guarantee that splitting can never make a fence render worse
// than it does today. It buys nothing for a panic the whole source triggers
// too (the one demonstrated trigger, a column-0 `classDef` with no colon, is
// of that kind and takes the fallback render down as well); it is here for an
// unspecified vendored panic that one block's source reaches and the whole
// source does not.
//
// The stacked art carries NO trailing newline, matching what
// mermaidcmd.RenderDiagram returns on the single-render path. renderMermaidBlock
// appends exactly one newline to whichever it got and spliceMermaidArt strips
// exactly one back off, so a trailing newline here would print as a blank line
// under a split diagram and under no other.
func stackFlowchartSubgraphs(header string, blocks []mermaidSubgraph) (art string, ok bool) {
	if len(blocks) == 0 {
		return "", false // nothing to stack: the caller must render the whole source
	}

	defer func() {
		if recover() != nil {
			art, ok = "", false
		}
	}()

	var out strings.Builder
	for _, block := range blocks {
		rendered, err := mermaidcmd.RenderDiagram(block.source(header), nil)
		if err != nil || strings.TrimSpace(rendered) == "" {
			return "", false
		}

		if out.Len() > 0 {
			out.WriteString("\n")
		}
		if block.title != "" {
			out.WriteString(block.title)
			out.WriteString("\n")
			out.WriteString(strings.Repeat(mermaidSubgraphRule, runewidth.StringWidth(block.title)))
			out.WriteString("\n")
		}
		out.WriteString(strings.TrimRight(rendered, "\n"))
		out.WriteString("\n")
	}
	return strings.TrimRight(out.String(), "\n"), true
}

// flowchartSubgraphBreak matches mermaid's HTML line break in a label, in
// every spelling the vendored renderer's own htmlBreakPattern accepts.
var flowchartSubgraphBreak = regexp.MustCompile(`(?i)<br\s*/?>`)

// flowchartSubgraphHeading turns a raw subgraph label into the single plain
// line printed above a stacked block.
//
// An HTML line break becomes one space. The vendored renderer turns `<br/>`
// into a real line break inside a NODE box (see its newGraphLabel), but a
// heading here is one line, and printed as-is the tag would show literally and
// the rule under it would be sized to count the tag's characters. Roughly 80%
// of the corpus fences carry a `<br/>` somewhere, so this is not a corner.
//
// Control bytes are dropped for the same width reason and one more: the
// heading is written straight into the art, which bypasses glamour, so a raw
// ESC in an author's label would otherwise reach the terminal unescaped.
func flowchartSubgraphHeading(label string) string {
	label = flowchartSubgraphBreak.ReplaceAllString(label, " ")
	label = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return -1
		}
		return r
	}, label)
	return strings.TrimSpace(label)
}

// flowchartSubgraphHeader extracts a subgraph header line's id and the title
// to print above its art. Mermaid writes the header three ways and all three
// appear in the real corpus:
//
//	subgraph before["Before"]    the bracketed, quoted label
//	subgraph before [Before]     the bracketed, bare label
//	subgraph before              no label at all
//
// The id is always the token before the bracket, and it is returned separately
// because rule 5 has to treat it as a node id — see mermaidSubgraph. The title
// is the label when there is one and the id otherwise. A header with neither
// (a bare `subgraph`) yields "" for both, which the caller renders as an
// untitled block rather than refusing the split — a missing title costs a
// heading, not correctness.
func flowchartSubgraphHeader(trimmed string) (id, title string) {
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, flowchartSubgraphKeyword))
	if open := strings.IndexAny(rest, "[("); open >= 0 {
		if end := flowchartLabelEnd(rest, open+1, 1); end > 0 {
			title = flowchartSubgraphHeading(strings.Trim(strings.TrimSpace(rest[open+1:end-1]), `"`))
		}
		rest = strings.TrimSpace(rest[:open])
	}
	id = strings.Trim(rest, `"`)
	if title == "" {
		title = flowchartSubgraphHeading(id)
	}
	return id, title
}

// flowchartSubgraphsDisjoint reports whether no node id is mentioned by more
// than one block — rule 5 of splitFlowchartSubgraphs. It records which block
// owns each id and fails the moment an id turns up again under a different
// one.
//
// Every block's OWN id is claimed first, before any body is walked, and that
// order is load-bearing in both directions. An edge may point straight at a
// whole subgraph — `Z --> groupA` is valid, documented mermaid — and the
// target block may be declared either before or after the edge that names it.
// Claiming all the block ids up front catches the reference whichever way it
// points. Without it the pair looks disjoint, the fence splits, and the art
// then shows a stray box literally named `groupA` in one block while the other
// block's rectangle has become a plain heading, with nothing in the output
// correlating the two.
func flowchartSubgraphsDisjoint(blocks []mermaidSubgraph) bool {
	owner := make(map[string]int, len(blocks))
	claim := func(id string, i int) bool {
		if first, seen := owner[id]; seen && first != i {
			return false
		}
		owner[id] = i
		return true
	}

	for i, block := range blocks {
		if block.id != "" && !claim(block.id, i) {
			return false
		}
	}
	for i, block := range blocks {
		for _, line := range block.body {
			for _, id := range flowchartSubgraphNodeIDs(line) {
				if !claim(id, i) {
					return false
				}
			}
		}
	}
	return true
}

// flowchartSubgraphIdentByte widens flowchartIdentByte to every non-ASCII
// byte, so an id written in a non-Latin script is a node id here too.
//
// flowchartIdentByte itself must stay ASCII-only: flowchartShapeAt uses it as
// a lookbehind to decide whether a bracket opens a node's label, and widening
// it there would change what the normalization pass rewrites. Here the
// consequence of missing an id is far worse than a missed rewrite — rule 5
// would not see the shared node at all, so a fence with a genuine crossing
// edge between two Cyrillic-named nodes would split, lose the edge and draw
// the shared node twice.
func flowchartSubgraphIdentByte(b byte) bool {
	return b >= 0x80 || flowchartIdentByte(b)
}

// flowchartSubgraphClassSuffix is mermaid's style-class suffix on a node
// (`B1[old]:::hot`). Its class name is not a node id, and reading it as one
// makes two blocks that tag their nodes with the same class look as if they
// share a node, refusing a fence that splits perfectly well. `classDef`
// appears in 24% of the corpus fences, so the suffix is not rare.
const flowchartSubgraphClassSuffix = ":::"

// flowchartSubgraphNodeIDs returns every node id one statement line mentions,
// in order. A directive line (see flowchartDroppedKeywords) mentions none: a
// `direction TB` written inside two different subgraphs would otherwise read
// as the same two ids in both and refuse a perfectly splittable fence. That
// branch, and the comment-stripping above it, are guards for a DIRECT call —
// normalizeFlowchartSource has already removed every dropped directive and
// splitFlowchartSubgraphs skips comment-only lines, so neither fires on the
// production path.
//
// The walk reuses the normalizer's own helpers rather than re-deriving what an
// id looks like. flowchartEdgeAt consumes each arrow WITH its `|label|`
// suffix, so edge-label text can never be read as a node; a `:::className`
// suffix is skipped whole; then a run of flowchartSubgraphIdentByte bytes is
// the id; then flowchartLabelEnd skips that node's own label, whose text is
// likewise not an id. Arrows are located on the MASKED copy (see
// flowchartMaskLabels) and every slice is taken from the original, the same
// discipline every other pass in this patch uses.
func flowchartSubgraphNodeIDs(line string) []string {
	body := mermaidStripComment(line)
	if flowchartDirective(body, flowchartDroppedKeywords) != "" {
		return nil
	}
	masked := flowchartMaskLabels(body)

	var ids []string
	for i := 0; i < len(body); {
		if _, next, ok := flowchartEdgeAt(body, masked, i); ok {
			i = next
			continue
		}
		if strings.HasPrefix(body[i:], flowchartSubgraphClassSuffix) {
			i += len(flowchartSubgraphClassSuffix)
			for i < len(body) && body[i] != ' ' && body[i] != '\t' {
				i++
			}
			continue
		}
		if !flowchartSubgraphIdentByte(body[i]) {
			i++
			continue
		}
		start := i
		for i < len(body) && flowchartSubgraphIdentByte(body[i]) {
			i++
		}
		ids = append(ids, body[start:i])
		if i < len(body) && (body[i] == '[' || body[i] == '(' || body[i] == '{') {
			if end := flowchartLabelEnd(body, i+1, 1); end > 0 {
				i = end
			}
		}
	}
	return ids
}
