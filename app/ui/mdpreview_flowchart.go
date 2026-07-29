package ui

import (
	"regexp"
	"strings"
)

// This file holds the graph/flowchart NORMALIZATION pass. Like mdpreview.go
// and mdpreview_transpile.go it is patch-owned and does not exist upstream —
// see mdpreview.go's own doc comment for why new logic goes in new files
// rather than into an upstream one.
//
// # Why this exists
//
// `graph` and `flowchart` are the two diagram kinds the vendored
// mermaid-ascii renderer claims to support, so mdpreview_transpile.go used to
// hand them straight to mermaidcmd.RenderDiagram untouched. Measured against
// the real corpus of plan documents, that pass-through is wrong for most
// fences. The vendored parser (vendor/.../cmd/parse.go) understands exactly
// three node/edge shapes — `Id`, `Id[label]`, and `lhs --> rhs`, with an
// optional `-->|label|` — and silently mis-parses everything else:
//
//   - Any shape suffix other than square brackets splits ONE node into two
//     boxes. parseNode cuts the node name at the first '[' and requires the
//     text to end in ']'; `B{decision}` matches neither, so the whole string
//     becomes the node NAME. A diagram that also writes plain `B` elsewhere
//     then draws a `B` box and a separate `B{decision}` box, with the edges
//     split between them.
//   - `A --- B` is not a link at all. No pattern in parseString matches it, so
//     mermaidFileToMap falls back to "parse the remaining text as a node" and
//     draws a single box literally labeled `A --- B`.
//   - A literal '|' inside a node label breaks the edge parse. The edge
//     pattern is `^(.+)\s*-->\s*\|(.+)\|\s*(.+)$` with a GREEDY label group,
//     so `A -->|go| B["x=with|without"]` captures `go| B["x=with` as the edge
//     label and leaves `without"]` as the target node.
//
// Corpus scan over the unique `flowchart`/`graph` fences in the user's plan
// documents: round/stadium shapes ~28%, diamond shapes ~33%, undirected `---`
// ~11%, pipe-in-label ~3% — about 60% hit at least one. The constructs that
// already work and must NOT be disturbed are just as common: `<br/>` in a
// label ~80%, `subgraph` ~41%, chained `a --> b --> c` ~9%.
//
// # Why a text-level pass, not a rebuild through flowchartBuilder
//
// mdpreview_transpile.go already owns a flowchart-source builder, and routing
// these fences through it would look like the obvious reuse. It is the wrong
// tool here, for one decisive reason: 41% of these fences use `subgraph`, and
// flowchartBuilder has no concept of a subgraph at all — it emits a flat list
// of node declarations followed by a flat list of edges. Re-emitting through
// it would silently drop every grouping box, regressing two fifths of the
// corpus in order to fix a different fifth. The builder also assigns its own
// synthetic node ids (n0, n1, ...), which is exactly what a `subgraph` body
// cannot tolerate, since it names its members by id.
//
// So this pass rewrites the SOURCE TEXT and leaves its structure alone: same
// lines, same order, same subgraph blocks, same node ids, same comments. Only
// the three mis-parsed constructs above are rewritten, in place. Anything the
// pass does not recognize is copied through byte for byte, which makes the
// whole pass a fixed point on already-well-formed source — see
// TestNormalizeFlowchartSource_Idempotent.
//
// Two other approaches were considered and rejected. Patching the vendored
// parser was rejected because PATCH.md's whole premise is that vendored code
// stays untouched so `go mod vendor` can be re-run at will. Pre-parsing into a
// real mermaid AST was rejected as far more machinery than three rewrites
// need, and it would have to reproduce the vendored parser's own quirks to
// avoid changing what already renders correctly.
//
// # How the two passes fit together
//
// Each body line goes through link normalization FIRST and node-shape
// normalization SECOND, and that order is load-bearing. After the link pass
// every link is written as `-->` or `-->|label|`, so the node pass can tell an
// EDGE label (delimited by '|' right after an arrow, and left strictly alone)
// from a NODE label (inside brackets, where a '|' becomes '/'). Running the
// node pass first would have to guess which '|' is which, and an inline
// labeled link like `A -- foo(1) --> B` would look like a node shape.

// flowchartMaskByte fills every masked byte in flowchartMaskLabels's output.
// It must not be a space and must not be any character link syntax is built
// from. A space was tried first and is wrong twice over: the labeled-link
// pattern trims whitespace around its label capture, so a link label
// containing a bracket (`-- foo[1] -->`) lost everything from the bracket on;
// and a masked run of spaces let the label capture start past text the author
// wrote. '_' cannot appear in any link token, cannot start or end a label
// capture by accident, and keeps every byte index aligned with the original.
const flowchartMaskByte = '_'

// flowchartMaskLabels returns a copy of line with every byte that belongs to a
// LABEL — bracketed node-label text, double-quoted text, and the text between
// a pair of `|` edge-label delimiters — replaced by flowchartMaskByte, byte
// for byte, so every index into the mask still addresses the same byte of
// line. Both passes search the mask and slice the ORIGINAL, which is what
// keeps author text that merely looks like syntax (`B[a --- b]`,
// `-->|a==b|`) from being rewritten.
//
// This is the same masking trick classMaskQuotedSegments uses for classDiagram
// cardinalities, widened to three region kinds instead of one. It is not
// shared with that function because the two mask different things for
// different parsers: classMaskQuotedSegments hides only `"..."` so a relation
// arrow search cannot trip on a `0..1` range, while this one must also hide
// bracket and pipe regions.
//
// Nesting rules, in the order the switch tests them: text inside quotes is
// masked and its brackets do not move the depth counter (so `A["a(b"]` does
// not leave an unbalanced count); a '|' only opens an edge-label region at
// depth 0, since inside brackets it is ordinary label text; every bracket
// family shares one depth counter, and a stray closer clamps at zero rather
// than going negative.
func flowchartMaskLabels(line string) string {
	masked := []byte(line)
	depth := 0
	inQuotes, inPipe := false, false
	for i, b := range masked {
		switch {
		case inQuotes:
			inQuotes = b != '"'
		case inPipe:
			inPipe = b != '|'
		case b == '"':
			inQuotes = true
		case b == '|' && depth == 0:
			inPipe = true
		case b == '[' || b == '(' || b == '{':
			depth++
		case b == ']' || b == ')' || b == '}':
			depth = max(depth-1, 0)
		case depth == 0:
			continue // ordinary syntax at top level: keep it visible to the patterns
		}
		masked[i] = flowchartMaskByte
	}
	return string(masked)
}

// flowchartLinkPattern matches one mermaid link token. Two alternatives, in
// this order:
//
//	--|-.|== TEXT -->|---|.->|==>   a labeled inline link ("-- yes --> ")
//	-->|---|-.->|===|--o|--x|...    a bare link of any thickness or style
//
// The labeled alternative is listed first because Go's alternation is
// leftmost-FIRST: on `A -- yes --> B` the bare alternative would otherwise
// match the leading `--` alone and cut the line in half. It is kept from
// stealing an unlabeled link by requiring the label's first character to be
// none of `-`, `=`, `<`, `>` or whitespace — so `----` and `-->` fall through
// to the bare alternative, while `-- yes -->` does not.
//
// A leading `<` is deliberately NOT part of either alternative. The caller
// checks the preceding byte itself (see normalizeFlowchartLinks) so a
// bidirectional link keeps its head: the vendored parser has real `<-->` and
// `<-->|label|` patterns, so those must survive as bidirectional rather than
// be flattened to one-way.
var flowchartLinkPattern = regexp.MustCompile(
	`(?:--|-\.|==)[ \t]*([^-=<>|\s][^|<>]*?)[ \t]*(?:-{2,}|\.-+|={2,})[>ox]?` +
		`|(?:-\.+-*|-{2,}|={2,})[>ox]?`)

// normalizeFlowchartLinks rewrites every link token in line to the one form
// the vendored parser understands: `-->`, or `-->|label|` when the token
// carried an inline label, or `<-->`/`<-->|label|` when it was bidirectional.
// A token that is already in that form is rewritten to itself, so the function
// is a fixed point on normalized input.
//
// Only tokens found in the MASKED copy are touched — see flowchartMaskLabels.
// That is what keeps `B[a --- b]` and `-->|a==b|` intact: their link-looking
// bytes live inside a label, so the pattern never sees them.
//
// The thickness and dot style of a link are dropped rather than approximated.
// The renderer draws exactly one arrow glyph set, so a dotted or thick link
// has no distinct rendering to preserve; keeping the distinction in the source
// would only mean keeping a token the parser cannot read.
func normalizeFlowchartLinks(line string) string {
	locs := flowchartLinkPattern.FindAllStringSubmatchIndex(flowchartMaskLabels(line), -1)
	if len(locs) == 0 {
		return line
	}

	var out strings.Builder
	prev := 0
	for _, loc := range locs {
		start := loc[0]
		if start > prev && line[start-1] == '<' {
			start-- // swallow the bidirectional head so it is not left dangling
		}
		out.WriteString(line[prev:start])
		out.WriteString(flowchartLinkText(line, loc, line[start] == '<'))
		prev = loc[1]
	}
	out.WriteString(line[prev:])
	return out.String()
}

// flowchartLinkText builds the replacement for one matched link token: the
// arrow itself, plus a `|label|` suffix when the labeled alternative of
// flowchartLinkPattern captured one. loc is that match's submatch index pair,
// taken on the mask but sliced out of the ORIGINAL line, so a label that
// contains masked bytes (a bracketed or quoted segment) comes back as the
// author wrote it.
//
// Surrounding double quotes are dropped: mermaid uses them to protect a label
// containing spaces, a job the `|...|` delimiters already do here, and the
// vendored renderer draws an edge label verbatim, quotes included. Any '|'
// inside the label becomes '/' for the same reason a node label's does — a
// second '|' would terminate the edge label early and turn the rest of the
// line into a phantom node.
func flowchartLinkText(line string, loc []int, bidirectional bool) string {
	arrow := "-->"
	if bidirectional {
		arrow = "<-->"
	}
	if loc[2] < 0 {
		return arrow
	}

	label := strings.Trim(strings.TrimSpace(line[loc[2]:loc[3]]), `"`)
	label = strings.ReplaceAll(label, "|", "/")
	if label == "" {
		return arrow
	}
	return arrow + "|" + label + "|"
}

// flowchartShape is one mermaid node-shape wrapper: the delimiter written
// straight after the node id, and the delimiter that closes the label.
type flowchartShape struct{ open, close string }

// flowchartShapes lists every node shape this pass folds into plain square
// brackets, longest opening delimiter FIRST — the list is scanned in order and
// the first prefix match wins, so `((` listed before `(((` would cut a double
// circle one paren short and leave a stray `)` behind.
//
// The label's nesting depth is always len(open), including the asymmetric
// `>text]` shape, whose opening '>' is not a bracket at all but still opens
// exactly one level. The closing delimiter is the same length as the opening
// one except for that shape, which is why the two are stored separately rather
// than derived from each other.
//
// Square brackets are in the list even though they are already the target
// form. The rewrite is a no-op for the delimiters, but the label still has to
// go through the '|' substitution, and routing every shape through one path
// keeps that from being a second, separate scan.
var flowchartShapes = []flowchartShape{
	{"(((", ")))"}, // double circle
	{"([", "])"},   // stadium
	{"[[", "]]"},   // subroutine
	{"[(", ")]"},   // cylinder
	{"((", "))"},   // circle
	{"{{", "}}"},   // hexagon
	{"[", "]"},     // square (already fine; here for the '|' substitution)
	{"(", ")"},     // rounded
	{"{", "}"},     // diamond
	{">", "]"},     // asymmetric
}

// flowchartIdentByte reports whether b can be the last byte of a node id, and
// therefore whether a delimiter right after it opens that node's LABEL rather
// than standing on its own. Only ASCII letters, digits and '_' qualify.
//
// '-' is deliberately excluded even though mermaid tolerates it inside an id,
// because including it would read the '>' of `-->` as the asymmetric shape's
// opening delimiter. That costs nothing in practice: the node pass reaches a
// '>' only after the arrow scan has already claimed every `-->` on the line.
func flowchartIdentByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '_':
		return true
	}
	return false
}

// flowchartLabelEnd returns the index just past the delimiter that closes a
// node label whose content starts at index start with depth levels already
// open, or -1 when the label never closes on this line (an unbalanced or
// multi-line label — the caller then leaves the line alone entirely).
//
// All three bracket families share one counter, so a label nesting a different
// family (`B[uses arr[i]]`, `B{a (b)}`) closes where the author meant it to.
// Brackets inside double quotes are ignored, which is what lets a quoted label
// carry an unbalanced one (`B["a (b"]`).
func flowchartLabelEnd(line string, start, depth int) int {
	inQuotes := false
	for i := start; i < len(line); i++ {
		switch {
		case inQuotes:
			inQuotes = line[i] != '"'
		case line[i] == '"':
			inQuotes = true
		case line[i] == '[' || line[i] == '(' || line[i] == '{':
			depth++
		case line[i] == ']' || line[i] == ')' || line[i] == '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// flowchartShapeAt rewrites the node shape that opens at index i of line into
// the square-bracket form, and reports the index just past it. ok is false
// when nothing there is a node shape at all — no known delimiter, no node id
// in front of it, an unbalanced label, or a closing delimiter that does not
// match the opening one (a defensive check: it means the depth counter closed
// on some other family's bracket, so the text is not the shape it looked like
// and is safer left alone).
func flowchartShapeAt(line string, i int) (replacement string, next int, ok bool) {
	if i == 0 || !flowchartIdentByte(line[i-1]) {
		return "", i, false
	}
	for _, shape := range flowchartShapes {
		if !strings.HasPrefix(line[i:], shape.open) {
			continue
		}
		content := i + len(shape.open)
		end := flowchartLabelEnd(line, content, len(shape.open))
		if end < 0 {
			return "", i, false
		}
		closeAt := end - len(shape.close)
		if closeAt < content || line[closeAt:end] != shape.close {
			return "", i, false
		}
		return "[" + strings.ReplaceAll(line[content:closeAt], "|", "/") + "]", end, true
	}
	return "", i, false
}

// flowchartEdgeAt returns the arrow at index i of line together with the
// `|label|` that follows it, when one does, and the index just past what it
// returned. ok is false when no arrow starts at i.
//
// This runs after normalizeFlowchartLinks, so the only arrows left are `-->`
// and `<-->`. Everything it returns is copied through UNCHANGED: an edge
// label's own '|' delimiters are the one place a pipe is real syntax rather
// than label text, which is exactly the distinction the whole two-pass order
// exists to make.
//
// masked decides where an arrow is; line is what gets copied. The label's
// extent is measured on line, since its closing '|' is masked away.
func flowchartEdgeAt(line, masked string, i int) (segment string, next int, ok bool) {
	var arrow string
	switch {
	case strings.HasPrefix(masked[i:], "<-->"):
		arrow = "<-->"
	case strings.HasPrefix(masked[i:], "-->"):
		arrow = "-->"
	default:
		return "", i, false
	}

	end := i + len(arrow)
	rest := end
	for rest < len(line) && (line[rest] == ' ' || line[rest] == '\t') {
		rest++
	}
	if rest < len(line) && line[rest] == '|' {
		if closeAt := strings.IndexByte(line[rest+1:], '|'); closeAt >= 0 {
			end = rest + 1 + closeAt + 1
		}
	}
	return line[i:end], end, true
}

// normalizeFlowchartNodes rewrites every node shape on line into the
// square-bracket form and turns any '|' inside a node label into '/'. Arrows
// and their `|label|` suffixes are copied through untouched.
//
// The walk is a single left-to-right pass with no lookbehind beyond one byte,
// which is what keeps the two rules from fighting: an arrow is consumed whole
// (label included) before any byte of it can be mistaken for a shape
// delimiter, and a shape is only recognized when a node id sits directly in
// front of it.
func normalizeFlowchartNodes(line string) string {
	masked := flowchartMaskLabels(line)

	var out strings.Builder
	for i := 0; i < len(line); {
		if segment, next, ok := flowchartEdgeAt(line, masked, i); ok {
			out.WriteString(segment)
			i = next
			continue
		}
		if replacement, next, ok := flowchartShapeAt(line, i); ok {
			out.WriteString(replacement)
			i = next
			continue
		}
		out.WriteByte(line[i])
		i++
	}
	return out.String()
}

// flowchartStructuralKeywords lists the statement keywords whose lines are
// copied through WHOLE, with no normalization at all.
//
// `subgraph` and `end` carry the block structure this pass promises to leave
// alone, and a subgraph header's `Id [Label]` is not a node shape even though
// it looks exactly like one. The rest are styling, layout, interaction and
// accessibility directives whose punctuation (`fill:#f9f`, a URL, a
// comma-separated node list) has nothing to do with links or shapes, so
// running the rewrites over them could only ever do harm.
//
// A subgraph's CONTENTS are still normalized — only the header and the
// closing `end` are skipped.
var flowchartStructuralKeywords = []string{
	"subgraph", "end", "direction",
	"classDef", "class", "style", "linkStyle", "click",
	"accTitle", "accDescr",
}

// normalizeFlowchartLine normalizes one body line: links first, then node
// shapes (see this file's doc comment for why that order is load-bearing).
//
// A line matching flowchartStructuralKeywords is returned verbatim. The match
// goes through the shared mermaidDirective discriminator rather than a prefix
// test, so a node genuinely NAMED after one of those keywords is still treated
// as a node statement — `end --> A{x}` normalizes, `end` alone does not. That
// is the same trap mermaidDirectiveShape's own doc comment describes, reached
// from a third diagram type; reusing the discriminator is what keeps it fixed
// in one place.
//
// An inline `%%` comment is split off and re-appended untouched. The vendored
// parser cuts every line at its first `%%` before parsing, so the tail is dead
// text as far as rendering goes — rewriting an author's prose there would
// change what a reader sees in the source for no rendering gain.
func normalizeFlowchartLine(line string) string {
	body, comment, hasComment := strings.Cut(line, "%%")
	if mermaidDirective(strings.TrimSpace(body), flowchartStructuralKeywords) != "" {
		return line
	}

	normalized := normalizeFlowchartNodes(normalizeFlowchartLinks(body))
	if hasComment {
		return normalized + "%%" + comment
	}
	return normalized
}

// normalizeFlowchartSource rewrites a whole `graph`/`flowchart` fence into the
// subset of mermaid syntax the vendored renderer actually parses correctly —
// see this file's doc comment for the three constructs it fixes, the corpus
// measurement behind them, and why this is a text-level pass rather than a
// rebuild through flowchartBuilder.
//
// Blank lines, `%%` comment lines and the diagram's own header line are copied
// through untouched. The header is skipped because it is a declaration, not a
// statement: `flowchart LR` has no node or link in it, and the vendored parser
// rejects the whole diagram if anything unexpected follows the direction
// keyword.
//
// Line count, line order and leading indentation are all preserved, so the
// result stays diff-comparable against the fence the author wrote. That is
// deliberate: when something still renders wrongly, the normalized source is
// meant to be readable next to the original.
func normalizeFlowchartSource(source string) string {
	lines := strings.Split(source, "\n")
	headerSeen := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		if !headerSeen {
			headerSeen = true
			continue
		}
		lines[i] = normalizeFlowchartLine(line)
	}
	return strings.Join(lines, "\n")
}
