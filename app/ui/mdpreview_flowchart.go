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
//   - A styling or layout directive draws a box named after itself. The
//     vendored `classDef` pattern is anchored at column 0, so an indented one
//     never matches, and `style`, `class`, `direction`, `linkStyle`, `click`
//     and friends match no pattern at all. Every one of those lines falls
//     through to parseNode and becomes a node whose label is the whole line.
//   - An edge label keeps its quotes. Mermaid quotes a label to protect the
//     spaces in it, a job the `|...|` delimiters already do here, and the
//     renderer draws the label verbatim — so `-->|"listVariants (strict
//     mode)"|` shows the quote marks in the art.
//
// Corpus scan over the unique `flowchart`/`graph` fences in the user's plan
// documents: round/stadium shapes ~28%, diamond shapes ~33%, undirected `---`
// ~11%, pipe-in-label ~3% — about 60% hit at least one. Styling and layout
// directives are the single most common defect of all: `style` in 26% of
// fences, `classDef` in 24%, `class` in 12%, `direction` in 12%, `linkStyle`
// in 5%. The constructs that already work and must NOT be disturbed are just
// as common: `<br/>` in a label ~80%, `subgraph` ~41%, chained
// `a --> b --> c` ~9%.
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
// order, same subgraph blocks, same node ids, same comments. The four
// mis-parsed constructs above are rewritten in place, and the styling and
// layout directives are removed outright (see flowchartDroppedKeywords for why
// removal is the only honest option there). Anything the pass does not
// recognize is copied through byte for byte, which makes the whole pass a
// fixed point on already-well-formed source — see
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
// Each body line goes through link normalization FIRST and node normalization
// SECOND, and that order is load-bearing. After the link pass every link is
// written as `-->` or `-->|label|`, so the node pass can tell an EDGE label
// (delimited by '|' right after an arrow) from a NODE label (inside brackets).
// Running the node pass first would have to guess which '|' is which, and an
// inline labeled link like `A -- foo(1) --> B` would look like a node shape.
//
// The node pass handles both label kinds, differently: inside a node label a
// '|' becomes '/', while an edge label is copied through with only its
// surrounding quotes dropped (see unquoteFlowchartEdgeLabel). Both live in
// that one pass because both need the same thing — each arrow consumed whole,
// its `|label|` suffix included — and a separate quote pass would mask and
// re-scan every line to find the arrows the node pass already has in hand.

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

// unquoteFlowchartEdgeLabel drops one layer of surrounding double quotes from
// one arrow segment as returned by flowchartEdgeAt — either a bare arrow, or
// an arrow followed by `|label|` — so `A -->|"listVariants (strict mode)"| B`
// reaches the renderer as `A -->|listVariants (strict mode)| B`.
//
// flowchartLinkText already unquotes a label, but only for the labels IT
// builds — the ones written in the inline `-- label -->` form. A label written
// the common way, straight after the arrow, is part of neither
// flowchartLinkPattern alternative: the arrow matches the bare-link
// alternative and the `|...|` that follows is copied through untouched. Its
// quotes then survive into the art, where the renderer draws them.
//
// The segment is handed back unchanged when there is no label, when the label
// is not quoted on both ends, or when it carries a quote of its own. That
// leaves `|"a" and "b"|` alone rather than eating its inner quotes, and it is
// also what makes the rewrite a fixed point: what it writes back can never be
// stripped a second time.
//
// The closing `|` is required rather than assumed. flowchartEdgeAt only
// extends the segment past an opening pipe once it has found the closing one,
// so today the check never fires — but the alternative to checking is a
// slice-bounds panic that would cost the reader the whole fence's art.
func unquoteFlowchartEdgeLabel(segment string) string {
	open := strings.IndexByte(segment, '|')
	if open < 0 || !strings.HasSuffix(segment, "|") {
		return segment
	}
	label := segment[open+1 : len(segment)-1]
	if len(label) <= 2 || label[0] != '"' || label[len(label)-1] != '"' {
		return segment
	}
	inner := label[1 : len(label)-1]
	if strings.Contains(inner, `"`) {
		return segment
	}
	return segment[:open+1] + inner + "|"
}

// normalizeFlowchartNodes rewrites every node shape on line into the
// square-bracket form, turns any '|' inside a node label into '/', and drops
// the surrounding quotes from an edge label (see unquoteFlowchartEdgeLabel).
// An arrow and its `|label|` suffix are otherwise copied through untouched.
//
// The walk is a single left-to-right pass with no lookbehind beyond one byte,
// which is what keeps the rules from fighting: an arrow is consumed whole
// (label included) before any byte of it can be mistaken for a shape
// delimiter, and a shape is only recognized when a node id sits directly in
// front of it. The edge-label unquoting rides on that same "arrow consumed
// whole" step, which is why it is not a pass of its own — a separate walk
// would mask and re-scan every line to find the arrows this one already has.
func normalizeFlowchartNodes(line string) string {
	masked := flowchartMaskLabels(line)

	var out strings.Builder
	for i := 0; i < len(line); {
		if segment, next, ok := flowchartEdgeAt(line, masked, i); ok {
			out.WriteString(unquoteFlowchartEdgeLabel(segment))
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
// it looks exactly like one. `accTitle` and `accDescr` are accessibility
// directives: the renderer cannot draw them either, but unlike everything in
// flowchartDroppedKeywords they carry author PROSE, so removing them would
// delete text rather than presentation. Either way the rewrites must not run
// over them — their punctuation has nothing to do with links or shapes.
//
// A subgraph's CONTENTS are still normalized — only the header and the
// closing `end` are skipped.
var flowchartStructuralKeywords = []string{
	flowchartSubgraphKeyword, flowchartSubgraphEnd, "accTitle", "accDescr",
}

// flowchartDroppedKeywords lists the statement keywords whose lines are
// REMOVED from the source before it reaches the renderer.
//
// Every one of them describes how a diagram should LOOK or BEHAVE — a fill
// color, a style class and its assignment, an edge's stroke, a subgraph's
// internal layout direction, a click target — and the ASCII renderer can draw
// none of it. Left in, each line draws a box named after itself (see this
// file's doc comment), which is the largest single defect the corpus scan
// found: 36 fences carried a `style`, 33 a `classDef`, 17 a `class`, 16 a
// standalone `direction`, 7 a `linkStyle`. Dropping them therefore removes a
// spurious box each and loses nothing that could have been drawn.
//
// `direction` here is the STANDALONE statement, the one written inside a
// subgraph block. The fence's own first line (`flowchart LR`) also sets a
// direction and is never touched — normalizeFlowchartSource skips the header
// before any of this runs.
//
// One thing is genuinely given up. A `classDef` written at column 0 is the one
// directive the vendored parser does read, and paired with a `:::className`
// suffix on a node it colors that node's label text. An indented one has never
// worked (the pattern is anchored), no fence in the corpus does it at all, and
// keeping only the column-0 spelling would make this pass's output depend on
// indentation. Dropping every `classDef` also closes a crash: parseStyleClass
// splits each declaration on ':' and indexes the second field blindly, so a
// column-0 `classDef x stroke-dasharray: 5 5` panics the vendored parser and
// takes the whole fence down to the verbatim fallback.
var flowchartDroppedKeywords = []string{
	"style", "classDef", "class", "linkStyle", "direction",
	"click", "href", "callback",
}

// flowchartDirective reports which of keywords text is a directive statement
// for, or "" when it is a node or edge statement instead. This is the one
// place either flowchart keyword list is matched.
//
// Two things happen before the shared discriminator sees the text, and both
// are needed to keep a NODE from being read as a directive:
//
//   - Labels are masked (see flowchartMaskLabels), so a node id that is a
//     keyword and carries a label — `class[Class registry] --> B` — reads as
//     `class______________ --> B`. The mask byte cannot end an identifier, so
//     the whole-word test in mermaidKeywordRest rejects the keyword and the
//     line stays a node statement.
//   - Links are normalized FIRST by the caller, so the arrow test in
//     mermaidDirectiveShape sees a `-->` whatever the author wrote. That test
//     knows classDiagram's and stateDiagram's arrows, which do not include
//     `===` or `-.->`; without the rewrite, `style ==> B` and `class -.- B`
//     would have no arrow to find and would be dropped as directives.
func flowchartDirective(text string, keywords []string) string {
	return mermaidDirective(strings.TrimSpace(flowchartMaskLabels(text)), keywords)
}

// normalizeFlowchartLine normalizes one body line: links first, then nodes
// (see this file's doc comment for why links-before-nodes is load-bearing).
// keep is false when the line is a styling or layout directive and must be
// dropped from the source entirely — see flowchartDroppedKeywords.
//
// Both keyword lists are matched through the shared mermaidDirective
// discriminator rather than a prefix test, so a node genuinely NAMED after one
// of those keywords is still treated as a node statement: `end --> A{x}`
// normalizes, `style ==> B` normalizes and survives, `style-review --> x` is
// untouched, and only `style A fill:#f9f` is dropped. That is the same trap
// mermaidDirectiveShape's own doc comment describes, reached from a third
// diagram type; reusing the discriminator is what keeps it fixed in one place.
//
// An inline `%%` comment is split off and re-appended untouched. The vendored
// parser cuts every line at its first `%%` before parsing, so the tail is dead
// text as far as rendering goes — rewriting an author's prose there would
// change what a reader sees in the source for no rendering gain. A dropped
// directive takes its own comment tail with it: the tail annotates a line that
// is no longer there.
func normalizeFlowchartLine(line string) (normalized string, keep bool) {
	body, comment, hasComment := strings.Cut(line, "%%")
	linked := normalizeFlowchartLinks(body)

	if flowchartDirective(linked, flowchartStructuralKeywords) != "" {
		return line, true
	}
	if flowchartDirective(linked, flowchartDroppedKeywords) != "" {
		return "", false
	}

	normalized = normalizeFlowchartNodes(linked)
	if hasComment {
		return normalized + "%%" + comment, true
	}
	return normalized, true
}

// normalizeFlowchartSource rewrites a whole `graph`/`flowchart` fence into the
// subset of mermaid syntax the vendored renderer actually parses correctly —
// see this file's doc comment for the four constructs it fixes, the corpus
// measurement behind them, and why this is a text-level pass rather than a
// rebuild through flowchartBuilder.
//
// Blank lines, `%%` comment lines and the diagram's own header line are copied
// through untouched. The header is skipped because it is a declaration, not a
// statement: `flowchart LR` has no node or link in it, and the vendored parser
// rejects the whole diagram if anything unexpected follows the direction
// keyword. Skipping it is also what keeps a fence's own `flowchart LR` safe
// while a standalone `direction TB` inside a subgraph is dropped.
//
// A dropped directive line is REMOVED rather than blanked. A blank line is
// harmless to the vendored parser today, but leaving one would make the pass's
// output depend on a parser detail it has no reason to depend on, and the line
// is gone either way.
//
// Line order and leading indentation are preserved, and so is the line count
// except for the directives that are dropped, so the result stays
// diff-comparable against the fence the author wrote. That is deliberate: when
// something still renders wrongly, the normalized source is meant to be
// readable next to the original.
//
// When dropping would leave the fence with no statement at all, the ORIGINAL
// source is returned instead. A header-only diagram renders as blank art,
// which would make the fence vanish from the preview; handing the original
// back keeps this pass from ever being the reason a fence disappears, however
// the drop rule is changed later.
func normalizeFlowchartSource(source string) string {
	lines := strings.Split(source, "\n")
	out := make([]string, 0, len(lines))
	headerSeen, statements := false, 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "%%"):
			out = append(out, line)
			continue
		case !headerSeen:
			headerSeen = true
			out = append(out, line)
			continue
		}
		normalized, keep := normalizeFlowchartLine(line)
		if !keep {
			continue
		}
		out = append(out, normalized)
		statements++
	}
	if statements == 0 {
		return source
	}
	return strings.Join(out, "\n")
}
