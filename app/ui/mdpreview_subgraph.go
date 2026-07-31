package ui

import "strings"

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
// under its own title. This file answers the first half of that: may this
// fence be split, and if so what are its blocks. Rendering and stacking is the
// caller's job.
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
// inline so this file and flowchartStructuralKeywords cannot drift apart.
const (
	flowchartSubgraphKeyword = "subgraph"
	flowchartSubgraphEnd     = "end"
)

// mermaidSubgraph is one top-level `subgraph ... end` block: the title to
// print above its art, and the body lines between the header and the closing
// `end`, in source order with their original indentation and any inline `%%`
// comment intact.
type mermaidSubgraph struct {
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
//  5. No node id is mentioned by more than one subgraph.
//
// The last rule is the important one and it is deliberately strict. It catches
// an edge crossing from one subgraph to another, and it also catches a node
// that two subgraphs both point at. Either case would lose an edge or silently
// duplicate a box, and one rule covers both.
//
// Two malformed shapes refuse as well, for the same reason: an unterminated
// `subgraph` and a stray `end` with nothing open. Neither can be split into
// blocks that mean what the author wrote.
func splitFlowchartSubgraphs(source string) (header string, blocks []mermaidSubgraph, ok bool) {
	switch mermaidDiagramKind(source) {
	case "graph", "flowchart":
	default:
		return "", nil, false // rule 1
	}

	headerSeen, inBlock := false, false
	for line := range strings.SplitSeq(source, "\n") {
		trimmed := strings.TrimSpace(mermaidStripComment(line))
		switch {
		case trimmed == "":
			continue
		case !headerSeen:
			headerSeen, header = true, line
		case flowchartFirstWord(trimmed) == flowchartSubgraphKeyword:
			if inBlock {
				return "", nil, false // rule 3: nested
			}
			blocks = append(blocks, mermaidSubgraph{title: flowchartSubgraphTitle(trimmed)})
			inBlock = true
		case trimmed == flowchartSubgraphEnd:
			if !inBlock {
				return "", nil, false // a stray `end`
			}
			inBlock = false
		case !inBlock:
			if flowchartDirective(trimmed, flowchartDroppedKeywords) == "" {
				return "", nil, false // rule 4: a statement outside every subgraph
			}
		default:
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

// flowchartFirstWord returns the first whitespace-delimited token of an
// already-trimmed line. This is how a `subgraph` header is told from a node
// whose id merely starts with the word: `subgraphOne --> B` has first word
// "subgraphOne", which is not the keyword, so it stays a node statement.
func flowchartFirstWord(trimmed string) string {
	if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
		return trimmed[:i]
	}
	return trimmed
}

// flowchartSubgraphTitle extracts the title from a subgraph header line.
// Mermaid writes it three ways and all three appear in the real corpus:
//
//	subgraph before["Before"]    the bracketed, quoted label
//	subgraph before [Before]     the bracketed, bare label
//	subgraph before              no label at all
//
// The first two give the label, the third gives the id. A header with neither
// (a bare `subgraph`) yields "", which the caller renders as an untitled
// block rather than refusing the split — a missing title costs a heading, not
// correctness.
func flowchartSubgraphTitle(trimmed string) string {
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, flowchartSubgraphKeyword))
	if open := strings.IndexAny(rest, "[("); open >= 0 {
		if end := flowchartLabelEnd(rest, open+1, 1); end > 0 {
			if label := strings.Trim(strings.TrimSpace(rest[open+1:end-1]), `"`); label != "" {
				return label
			}
		}
		rest = strings.TrimSpace(rest[:open])
	}
	return strings.Trim(rest, `"`)
}

// flowchartSubgraphsDisjoint reports whether no node id is mentioned by more
// than one block — rule 5 of splitFlowchartSubgraphs. It walks the blocks in
// order, records which one first mentions each id, and fails the moment an id
// turns up again under a different one.
func flowchartSubgraphsDisjoint(blocks []mermaidSubgraph) bool {
	owner := make(map[string]int, len(blocks))
	for i, block := range blocks {
		for _, line := range block.body {
			for _, id := range flowchartSubgraphNodeIDs(line) {
				if first, seen := owner[id]; seen && first != i {
					return false
				}
				owner[id] = i
			}
		}
	}
	return true
}

// flowchartSubgraphNodeIDs returns every node id one statement line mentions,
// in order. A directive line (see flowchartDroppedKeywords) mentions none: a
// `direction TB` written inside two different subgraphs would otherwise read
// as the same two ids in both and refuse a perfectly splittable fence.
//
// The walk reuses the normalizer's own three helpers rather than re-deriving
// what an id looks like. flowchartEdgeAt consumes each arrow WITH its
// `|label|` suffix, so edge-label text can never be read as a node; then a run
// of flowchartIdentByte bytes is the id; then flowchartLabelEnd skips that
// node's own label, whose text is likewise not an id. Arrows are located on
// the MASKED copy (see flowchartMaskLabels) and every slice is taken from the
// original, the same discipline every other pass in this patch uses.
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
		if !flowchartIdentByte(body[i]) {
			i++
			continue
		}
		start := i
		for i < len(body) && flowchartIdentByte(body[i]) {
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
