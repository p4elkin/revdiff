package ui

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"
)

// This file holds the classDiagram/stateDiagram-v2 -> flowchart transpiler
// (see docs/plans/20260728-markdown-preview-diagram-transpile.md for the
// full design). Like mdpreview.go, it is patch-owned and does not exist
// upstream — see that file's own doc comment for why new logic goes in new
// files rather than editing across a package boundary.
//
// The approach is a transpiler, not a new renderer: classDiagram and
// stateDiagram-v2 sources become synthetic `flowchart TD` source, which goes
// to the SAME existing mermaid-ascii renderer every other diagram type
// already uses. Task 2 built the shared spine both diagram types need — the
// dispatch, the sanitizing/label helpers, the flowchart-source builder, and
// the block scanner — plus the one-line hook that replaces the direct
// mermaidcmd.RenderDiagram call in mdpreview.go. Task 3 implements
// classTranspiler (see its own doc comment below, and the plan's "Grammar
// handled — classDiagram" section), so classDiagram fences now transpile.
// Task 4 implements stateTranspiler the same way (see its own doc comment
// below, and the plan's "Grammar handled — stateDiagram-v2" section), so
// stateDiagram-v2 (and plain stateDiagram) fences now transpile too — every
// diagram kind this patch set out to cover is wired in.

// mermaidUnconstrainedWidth is passed wherever no real viewport width is
// available (renderMermaidFences's callers — see its doc comment for why
// that function's own signature does not gain a width parameter). It tells
// mermaidLabelCap to skip the adaptive shrink entirely and always allow the
// ceiling (mermaidLabelMaxRunes), matching how every other diagram type
// already behaves: unconstrained, full-width rendering.
//
// The value is deliberately far out of band rather than 0. A real viewport
// CAN report width 0, or even a negative width, during a degenerate layout
// state — the viewport width is computed as `m.layout.width - treeWidth - 4`
// (see handleFileLoaded), which goes negative on a very narrow terminal. That
// means "no room at all", the opposite of "no limit". Picking a sentinel no
// layout arithmetic can ever land on keeps the two apart: any real width,
// including 0 and negatives, falls through to the adaptive formula, which
// clamps it up to mermaidLabelMinRunes. A small negative like -1 would not be
// safe here, since the layout can produce exactly that.
const mermaidUnconstrainedWidth = math.MinInt

const (
	// mermaidLabelMaxRunes is the ceiling on the adaptive per-line rune cap
	// (see mermaidLabelCap): past this width the extra room buys little and
	// a box starts to dominate the pane.
	mermaidLabelMaxRunes = 32

	// mermaidLabelMinRunes is the floor on the adaptive per-line rune cap:
	// below this a member line is mostly ellipsis.
	mermaidLabelMinRunes = 16

	// classMaxMembers caps how many member/description lines a single node
	// keeps before the rest collapse into one "... +K more" row. Height is
	// 2*n+3 rows and a 40-row terminal shows about 35, so a box wants to
	// stay under about 30 rows — 13 label lines' worth of height budget. A
	// median real corpus class (6-11 members) never hits this cap; it only
	// bounds the tail (see the plan's "Keeping boxes readable" section).
	classMaxMembers = 12

	// classCardinalityMaxRunes caps a cardinality value classCardinalityTable
	// does not recognize. Every entry that table DOES hold normalizes to at
	// most three runes ("0/1"), so an unrecognized one is already the wide
	// case; this keeps it from growing an edge label without bound while
	// still leaving room for the longest shape an author plausibly writes by
	// hand ("0..many" is 7).
	classCardinalityMaxRunes = 8

	// classInheritanceLabel is the word used for the two generalization/
	// realization relation arrows (<|-- and <|..). It is UML-inexact: solid
	// <|-- is generalization (inheritance), so "extends" would be the more
	// correct word for that arrow alone, while <|.. really is
	// interface-realization, which "implements" fits exactly. One word was
	// picked for both because interface-realization is the overwhelmingly
	// common real-corpus case; if that stops being true, swapping the word
	// is this one line.
	classInheritanceLabel = "implements"
)

// mermaidDiagramKind returns the keyword mermaid itself uses to pick a
// grammar: the first whitespace-delimited token of the first line that is
// neither blank nor a %% comment. It performs no validation against a known
// list of diagram types — it only extracts. transpileMermaid (and, for every
// kind it does not transpile, the unchanged fallback path in
// renderMermaidSource) are what decide whether a kind means anything to this
// patch; a prose line — or any fence body that is not a mermaid diagram at
// all — simply yields whatever its first word happens to be, which
// transpileMermaid's default case then treats exactly like any other
// not-handled kind.
func mermaidDiagramKind(source string) string {
	for line := range strings.SplitSeq(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		return fields[0]
	}
	return ""
}

// mermaidStripFrontmatter removes a leading YAML frontmatter block — the
// `---` / keys / `---` header mermaid allows above a diagram's own type
// keyword — and returns the diagram source that follows it. A source with no
// frontmatter, or one whose opening `---` is never closed, is returned
// completely unchanged.
//
// Two separate things break without this. mermaidDiagramKind would report
// "---" as the diagram kind, so transpileMermaid's switch would never reach
// the classDiagram/stateDiagram case at all and the whole fence would fall
// back verbatim. And even if the kind were detected some other way, the
// frontmatter's own "key: value" lines would reach the transpilers' generic
// colon-form fallback and manufacture bogus nodes named after YAML keys.
// Stripping once, at the top of transpileMermaid, closes both.
func mermaidStripFrontmatter(source string) string {
	lines := strings.Split(source, "\n")

	open := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		if trimmed == "---" {
			open = i
		}
		break // the first substantive line decides; nothing below it can open frontmatter
	}
	if open == -1 {
		return source
	}

	for i := open + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return strings.Join(lines[i+1:], "\n")
		}
	}
	return source // unterminated: not really frontmatter, leave the source alone
}

// mermaidStripComment removes a %% comment from a single source line: a line
// that is a comment once trimmed (starts with %%) becomes empty; a line that
// carries a trailing inline %% comment is cut there, with trailing space
// left by the cut trimmed away; any other line is returned unchanged
// (leading indentation preserved — callers that want a fully trimmed line,
// e.g. scanMermaidBlocks, trim it themselves). This mirrors the inline- and
// full-line-comment handling the vendored mermaidFileToMap already does for
// top-level graph/flowchart source (parse.go's comment-stripping loop); this
// patch needs its own copy for the same reason mdFencePrefix duplicates
// sidepane.fencePrefix — see that function's doc comment.
func mermaidStripComment(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "%%") {
		return ""
	}
	if before, _, found := strings.Cut(line, "%%"); found {
		return strings.TrimRight(before, " \t")
	}
	return line
}

// splitOnFirstColon splits line at its first ':' character, trimming
// surrounding space from both halves. Shared by both diagram types: a
// classDiagram member line ("Foo : +bar() void"), and a stateDiagram-v2
// transition label, tolerate either spacing mermaid allows around the colon
// ("A --> B : label" and "A --> B: label" both occur in the real corpus).
// ok is false when line has no colon at all, in which case before is line
// unchanged and after is empty.
func splitOnFirstColon(line string) (before, after string, ok bool) {
	before, after, ok = strings.Cut(line, ":")
	if !ok {
		return line, "", false
	}
	return strings.TrimSpace(before), strings.TrimSpace(after), true
}

// mermaidTruncate truncates s to at most maxRunes runes, rune-safe (slicing
// by byte index would split a multi-byte rune in half), replacing the tail
// with an ASCII "..." ellipsis only when truncation actually happens.
func mermaidTruncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	const ellipsis = "..."
	if maxRunes <= len(ellipsis) {
		return string(runes[:max(maxRunes, 0)])
	}
	return string(runes[:maxRunes-len(ellipsis)]) + ellipsis
}

// mermaidColonRun matches a run of two or more consecutive colons. Collapsing
// with this regexp (rather than a single strings.ReplaceAll(s, ":::", ":"))
// matters: a run of five colons is one match here, replaced once with a
// single ":" — a fixed-width ReplaceAll on ":::" would instead consume the
// run three colons at a time, leaving "::" (2 colons) behind, which still
// matches parse.go's `:::` styleClass suffix check and corrupts the node.
var mermaidColonRun = regexp.MustCompile(`:{2,}`)

// mermaidSafeText makes arbitrary user text safe to place literally inside
// our own synthesized flowchart source, so nothing the diagram author typed
// can be misread as flowchart syntax when the real renderer re-parses it —
// see the plan's "Sanitizing" table for the derivation of every rule below,
// including why several classes of punctuation are deliberately left alone
// (parens, braces, tildes, non-ASCII: all provably inert against the
// vendored parser). This is the base sanitizer for node/member/title text;
// mermaidEdgeLabel builds on top of it for the additional edge-only rules
// (the no-break-space substitution and truncation), since an edge label is
// everything a node label is, drawn in a more hazardous position (through an
// arrow, delimited by literal '|' characters).
func mermaidSafeText(s string) string {
	if idx := strings.Index(s, "%%"); idx != -1 {
		s = strings.TrimRight(s[:idx], " \t")
	}
	s = strings.ReplaceAll(s, `"`, "")
	s = strings.ReplaceAll(s, "[", "(")
	s = strings.ReplaceAll(s, "]", ")")
	// < and > are mapped before any <br/> separator is ever inserted (that
	// happens later, in flowchartNode.renderLabelLines) so a "<br/>" in our
	// synthesized source can only ever be one WE inserted, never one a
	// diagram author's raw text happened to contain.
	s = strings.ReplaceAll(s, "<", "(")
	s = strings.ReplaceAll(s, ">", ")")
	s = strings.ReplaceAll(s, "|", "/")
	s = mermaidColonRun.ReplaceAllString(s, ":")
	s = strings.ReplaceAll(s, `\`, "")
	// tab/CR/LF -> space, then collapse whitespace runs and trim: Fields
	// already splits on every whitespace rune (not just literal spaces) and
	// Join re-joins with single spaces, so this one call does both table
	// rows at once.
	s = strings.Join(strings.Fields(s), " ")
	// squeeze " & " (with the collapse above already done, only ever a
	// single space on each side survives) so a member/title line can never
	// accidentally match the vendored parser's "^(.+) & (.+)$" two-node
	// split pattern, which triggers only with the surrounding spaces intact.
	s = strings.ReplaceAll(s, " & ", "&")
	return s
}

// mermaidBRPattern matches an HTML line break or a literal two-character
// "\n" escape. This runs on text the caller has already treated as one
// logical line, so a real embedded newline rune should not reach here; the
// literal two-character escape is defensive, in case a diagram author typed
// it directly in an edge label. Mirrors the shape of the vendored renderer's
// own break pattern (cmd/label.go's htmlBreakPattern).
var mermaidBRPattern = regexp.MustCompile(`(?i)<br\s*/?>|\\n`)

// mermaidEdgeLabel turns a raw `: label` (or `-->|label|`) capture into text
// safe to place inside our own synthesized `-->|label|` edge — see the
// plan's "Edge label" sanitizing table for the derivation of every step.
// capRunes is the adaptive per-line rune cap from mermaidLabelCap; the same
// numeric value doubles as a hard BYTE cap (mapping_edge.go reserves column
// width with len(), i.e. bytes, not runewidth — see the plan's Probe finding
// #3 — so a rune-capped label built from multi-byte runes can still reserve
// far more than capRunes columns). Returns "" when every rule below strips
// the label down to nothing, meaning "emit no label at all": a synthesized
// `-->||` is not read as an empty label by the vendored parser, but as a
// malformed edge whose remainder becomes a phantom node named "|| n1".
//
// Both cutting rules — the <br/>/literal-\n cut and the parenthetical cut —
// run on the RAW text, before mermaidSafeText. They are deliberately not
// layered on top of the sanitizer like the rest, because mermaidSafeText
// rewrites exactly the characters they look for:
//
//   - it maps '<'/'>' to '('/')' and deletes bare backslashes, so an
//     already-sanitized "<br/>" reads as "(br/)", which the break pattern no
//     longer matches, and a literal "\n" would already have lost its
//     backslash;
//   - it maps '<' and '[' to '(' too, so a label with no parenthetical at all
//     grows one during sanitizing. Cutting after that silently truncated every
//     label containing '<' or '[': "count < max" became "count", "uses arr[i]"
//     lost everything from "arr" onward, and a classDiagram relation lost its
//     cardinality suffix along with the rest.
//
// Cutting first, on the untouched raw text, avoids both traps: only a
// parenthesis or brace the author actually typed can end a label.
func mermaidEdgeLabel(raw string, capRunes int) string {
	s := raw
	if loc := mermaidBRPattern.FindStringIndex(s); loc != nil {
		s = strings.TrimSpace(s[:loc[0]])
	}

	s = mermaidCutParenthetical(s)

	s = mermaidSafeText(s)

	// Every remaining space becomes a no-break space, and any run this
	// creates collapses to one — see mermaidNBSPSubstitute's doc comment
	// (mdpreview_nbsp.go) for why the arrow bleeds through an ordinary space
	// at this position and why a run can appear at all. Applied before the
	// rune/byte cap below, so the truncation budget is spent on real content
	// rather than on redundant characters this step itself introduced.
	//
	// In THIS pipeline the collapse branch is defensive rather than commonly
	// exercised: mermaidSafeText's own whitespace normalization (the
	// Fields/Join step) already reduces any run of whitespace — including a
	// literal no-break space, since Go's unicode.IsSpace counts U+00A0 as
	// whitespace — down to a single ASCII space before this line ever runs.
	// A literal middle dot in a label (e.g. the real corpus text
	// "open · C1") is NOT whitespace, so mermaidSafeText leaves it alone and
	// it survives here untouched, sandwiched between two independently
	// substituted no-break spaces rather than merging into a run with them
	// — see TestMermaidEdgeLabel_LiteralMiddleDotInLabel_SurvivesBetweenNoBreakSpaces.
	s = mermaidNBSPSubstitute(s)

	return mermaidTruncateRunesAndBytes(s, capRunes)
}

// mermaidCutParenthetical cuts s at the first '(' or '{': the real corpus
// puts the trigger word first and the explanation in a parenthetical. A
// string that STARTS with one (idx == 0) is returned unchanged instead of
// being cut to nothing — cutting at index 0 would discard real content for
// no gain, and the caller's own truncation still bounds the width.
//
// Two callers share this rule, and BOTH run it on raw, pre-sanitizing text.
// mermaidEdgeLabel applies it to a whole edge label (see that function's doc
// comment for why the order matters — mermaidSafeText manufactures new '('
// characters out of '<' and '['). classJoinLabelParts applies it to a
// relation's WORD alone, before the cardinality suffix is appended —
// otherwise a parenthetical inside an explicit "  : label" override would cut
// the suffix away with it, losing the cardinality the plan says an explicit
// label never replaces.
func mermaidCutParenthetical(s string) string {
	if idx := strings.IndexAny(s, "({"); idx > 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}

// mermaidTruncateRunesAndBytes truncates s so the result satisfies BOTH a
// rune-count cap and, using the same numeric value, a byte-count cap —
// whichever binds first. mermaidTruncate alone only guarantees the rune
// count; a label built from multi-byte runes (e.g. "≤", 3 bytes each) can
// still pass the rune check while reserving far more than capRunes bytes of
// mermaid-ascii's per-column width budget.
//
// A byte-capped result carries the same "..." ellipsis a rune-capped one
// does. That is not cosmetic. mermaidEdgeLabel turns every space into a
// 2-byte no-break space (mermaidNBSPSubstitute), so the byte cap binds for
// essentially every multi-word edge label; without the ellipsis two different
// transitions out of the same state can both display as a bare prefix
// ("reviewer resolv", with the space a no-break space rather than an ASCII
// one), reading as complete labels that happen to be identical rather than as
// two truncated ones.
//
// The candidate is built as "first n runes + ellipsis" directly, NOT by
// re-running mermaidTruncate on a prefix. That was the earlier shape and it
// was silently broken: every prefix handed back was already at or under
// capRunes runes, so mermaidTruncate returned it unchanged and the ellipsis
// could never appear.
func mermaidTruncateRunesAndBytes(s string, capRunes int) string {
	out := mermaidTruncate(s, capRunes)
	if len(out) <= capRunes {
		return out
	}

	// Still over the byte budget: shrink the ORIGINAL string rune by rune
	// from the end until the kept prefix PLUS the ellipsis clears the same
	// numeric cap in bytes. The walk starts at capRunes-len(ellipsis) rather
	// than at len(runes) because every longer prefix would only produce a
	// candidate that is longer in both runes and bytes than one already
	// rejected, so it bounds the loop at capRunes (at most
	// mermaidLabelMaxRunes = 32) iterations instead of len(runes).
	runes := []rune(s)
	const ellipsis = "..."
	if capRunes <= len(ellipsis) {
		// No room for an ellipsis at all — fall back to a bare prefix, the
		// same degenerate case mermaidTruncate itself handles this way.
		for n := min(len(runes), capRunes); n > 0; n-- {
			if candidate := string(runes[:n]); len(candidate) <= capRunes {
				return candidate
			}
		}
		return ""
	}
	for n := min(len(runes), capRunes-len(ellipsis)); n > 0; n-- {
		candidate := string(runes[:n]) + ellipsis
		if len(candidate) <= capRunes {
			return candidate
		}
	}
	return ""
}

// mermaidLabelCap computes the adaptive per-line rune cap: how many runes of
// a node's title/stereotype/member line, or an edge's label, survive before
// mermaidTruncate cuts it. See the plan's "The width cap is adaptive"
// section for the full derivation; the short version:
//
//   - k is the number of nodes sharing the widest layout level of the
//     diagram (flowchartBuilder.widestLevel) — not the direction keyword —
//     because flowchart TD places same-level siblings side by side
//     (graph.go), so a wide fan-in is what actually threatens to overflow
//     the pane.
//   - labelJog folds in a cost a box-width-only formula misses: once a
//     fan-in needs more than one edge into a shared target, at least one
//     edge has to jog sideways through what is normally a narrow gap
//     between boxes, widening that gap. Measured at exactly 8*floor(k/2)
//     cells (see the plan's Probe findings), paid only when hasLabel is
//     true — the four unlabeled relation kinds (plain association/
//     undirected) pay nothing.
//
// paneWidth == mermaidUnconstrainedWidth skips all of the above and always
// returns the ceiling, matching how every other (non-transpiled) diagram
// type already renders: unconstrained. Only that exact sentinel is special —
// a degenerate real width (0, or negative from a very narrow terminal) goes
// through the formula and clamps up to mermaidLabelMinRunes, which is the
// right answer for "no room", the opposite of "no limit".
func mermaidLabelCap(paneWidth, k int, hasLabel bool) int {
	if paneWidth == mermaidUnconstrainedWidth {
		return mermaidLabelMaxRunes
	}
	if k < 1 {
		k = 1
	}
	labelJog := 0
	if hasLabel {
		labelJog = 8 * (k / 2)
	}
	raw := (paneWidth-5*(k-1)-labelJog)/k - 4
	return min(max(raw, mermaidLabelMinRunes), mermaidLabelMaxRunes)
}

// flowchartNode is one node accumulated by flowchartBuilder: a synthetic id
// (never derived from user text — see flowchartBuilder's doc comment for
// why), an optional stereotype line, an optional title line, and any number
// of member/description label lines in source order, all still at their
// full, untruncated length. Truncation and the classMaxMembers overflow row
// are applied once, in renderLabelLines, at the point the final adaptive cap
// is known (see flowchartBuilder.source).
type flowchartNode struct {
	id         string
	stereotype string
	title      string
	labelLines []string
}

// renderLabelLines assembles this node's final label lines — stereotype
// first (mermaid's own "«interface»" convention), then title, then member/
// description lines capped at classMaxMembers with a trailing "... +K more"
// row — each individually truncated to capRunes. A node that accumulated no
// stereotype, title, or label lines at all (defensive: every real caller
// sets at least a title) falls back to its own synthetic id so the emitted
// node declaration is never `n3[]`, which the vendored parser would accept
// but render as an empty box.
func (n *flowchartNode) renderLabelLines(capRunes int) []string {
	var lines []string
	if n.stereotype != "" {
		lines = append(lines, mermaidTruncate(n.stereotype, capRunes))
	}
	if n.title != "" {
		lines = append(lines, mermaidTruncate(n.title, capRunes))
	}

	members := n.labelLines
	overflow := 0
	if len(members) > classMaxMembers {
		overflow = len(members) - classMaxMembers
		members = members[:classMaxMembers]
	}
	for _, m := range members {
		lines = append(lines, mermaidTruncate(m, capRunes))
	}
	if overflow > 0 {
		lines = append(lines, fmt.Sprintf("... +%d more", overflow))
	}

	if len(lines) == 0 {
		lines = append(lines, n.id)
	}
	return lines
}

// flowchartEdge is one edge accumulated by flowchartBuilder, keyed by the
// same node keys callers pass to node/addEdge (not the synthetic ids, which
// are only assigned for the emitted source — see flowchartBuilder.source).
// label is the still-raw, unprocessed text a caller extracted from the
// diagram source; mermaidEdgeLabel (which needs the adaptive cap, only known
// once the whole diagram is built) is applied once, at emission time.
type flowchartEdge struct {
	from, to string
	label    string
}

// flowchartBuilder accumulates one diagram's nodes and edges as
// classTranspiler/stateTranspiler parse them, then emits synthetic
// `flowchart TD` source for the existing mermaid-ascii renderer — see this
// file's package doc comment for why a transpiler rather than a new
// renderer. Every string handed to node/setTitle/setStereotype/addLabelLine/
// addEdge is expected to already be sanitized (mermaidSafeText for node-side
// text; edge labels are stored raw and run through mermaidEdgeLabel at
// emission time, once the adaptive cap is known — see addEdge). Node keys
// are caller-chosen strings (e.g. a class or state name); synthetic ids
// (n0, n1, ...) are assigned internally and are the ONLY text that ever
// appears on an arrow-carrying line, which is what makes the `-->` / `|`
// hazard class structurally impossible in node position — see the plan's
// "Declaration order" section.
type flowchartBuilder struct {
	paneWidth int

	order []string // node keys, first-seen order (via node/addEdge/setTitle/...)
	nodes map[string]*flowchartNode

	edges    []flowchartEdge
	edgeSeen map[string]bool // dedup key: from + "\x00" + to + "\x00" + label
}

// newFlowchartBuilder creates an empty builder. paneWidth is the diff pane's
// current width (or mermaidUnconstrainedWidth), threaded down to
// mermaidLabelCap when source() computes the adaptive cap.
func newFlowchartBuilder(paneWidth int) *flowchartBuilder {
	return &flowchartBuilder{
		paneWidth: paneWidth,
		nodes:     make(map[string]*flowchartNode),
		edgeSeen:  make(map[string]bool),
	}
}

// node returns the flowchartNode for key, creating it (and assigning the
// next synthetic id, in first-seen order) if this is the first time key has
// been mentioned at all — as a declaration, a label target, or either side
// of an edge.
func (b *flowchartBuilder) node(key string) *flowchartNode {
	if n, ok := b.nodes[key]; ok {
		return n
	}
	n := &flowchartNode{id: fmt.Sprintf("n%d", len(b.order))}
	b.nodes[key] = n
	b.order = append(b.order, key)
	return n
}

// setTitle sets key's title line (e.g. a class or state name).
func (b *flowchartBuilder) setTitle(key, title string) {
	b.node(key).title = title
}

// setStereotype sets key's stereotype line (e.g. "«interface»"), rendered
// above the title — see flowchartNode.renderLabelLines.
func (b *flowchartBuilder) setStereotype(key, stereotype string) {
	b.node(key).stereotype = stereotype
}

// addLabelLine appends one member/description line to key's label, in the
// order callers add them. Capping at classMaxMembers and truncating to the
// adaptive width both happen later, at emission time (see
// flowchartNode.renderLabelLines) — addLabelLine itself never drops or
// shortens anything, since the cap is not yet known.
func (b *flowchartBuilder) addLabelLine(key, line string) {
	n := b.node(key)
	n.labelLines = append(n.labelLines, line)
}

// addEdge records an edge from fromKey to toKey with the given still-raw
// label (mermaidEdgeLabel is applied at emission time — see source). Both
// endpoints are registered as nodes if this is their first mention (so an
// edge alone is enough to introduce a node with no explicit declaration). An
// edge exactly identical to one already added (same from, to, AND label) is
// dropped rather than duplicated in the emitted source.
func (b *flowchartBuilder) addEdge(fromKey, toKey, label string) {
	b.node(fromKey)
	b.node(toKey)

	dedupKey := fromKey + "\x00" + toKey + "\x00" + label
	if b.edgeSeen[dedupKey] {
		return
	}
	b.edgeSeen[dedupKey] = true
	b.edges = append(b.edges, flowchartEdge{from: fromKey, to: toKey, label: label})
}

// empty reports whether no node has been registered at all — the signal
// transpileMermaid uses to fall back to the original source (a recognized
// diagram kind whose body produced nothing to draw, e.g. every statement was
// a comment or something this patch ignores).
func (b *flowchartBuilder) empty() bool {
	return len(b.order) == 0
}

// flowchartTopology is everything source() derives from the accumulated
// edges before it can emit a single line: the declaration order and each
// node's child list. Both come out of one O(V+E) walk (see topology), and
// both are needed twice — the order to predict the layout AND to emit the
// declarations, the child list to predict the layout AND, indirectly, to
// build the order. Passing this one value around is what keeps that walk to
// a single run per source() call instead of the three it took when each
// step recomputed what it needed from the builder.
type flowchartTopology struct {
	// order is the topological declaration order (see declarationOrder).
	order []string
	// children maps each node key to the keys it has edges to, in edge
	// insertion order, self-loops excluded (see parentCounts).
	children map[string][]string
}

// topology computes both derived structures once, and is the only place
// parentCounts is called from. declarationOrder consumes (and mutates) the
// parent counts, so those stay local here — nothing needs them afterwards,
// while the child list outlives the call.
func (b *flowchartBuilder) topology() flowchartTopology {
	remainingParents, children := b.parentCounts()
	return flowchartTopology{order: b.declarationOrder(remainingParents, children), children: children}
}

// declarationOrder returns node keys in the order their `nX[label]`
// declaration lines must be emitted: a TOPOLOGICAL order — every edge's
// source is declared before its target — tie-broken by first-seen order so
// the result is deterministic for a given diagram.
//
// This is not cosmetic; see the plan's "Declaration order" section. The
// vendored renderer computes layout roots by INSERTION order into its own
// internal node list (graph.go's createMapping: a node is a root unless an
// earlier-processed node already claimed it as a child), which mirrors the
// order names first appear anywhere in our emitted text. Declaring every
// node up front (regardless of source/target role) would make every single
// node a root and collapse the whole diagram into one row.
//
// A weaker rule — "all edge sources first, in first-seen-as-a-source order,
// then everything else" — fixes the simple fan-in but still flattens deeper
// hierarchies, because an intermediate node is itself an edge source. A
// three-level chain written parent-first, `A <|-- B` then `B <|-- C`, makes B
// the first source seen and declares it before A, so the renderer treats B as
// a root and draws B in the SAME row as its own children. Measured on one
// real corpus classDiagram, that flattening cost 121 cells of width against
// 73 for the topological order, on top of drawing the hierarchy wrong.
//
// Cycles have no topological order at all (state diagrams loop routinely).
// When no node is left whose parents are all declared, the remaining nodes
// are emitted in first-seen order — every node is still declared exactly
// once, and the first of them becomes a root, so the renderer always has
// somewhere to start.
//
// Both inputs come from one parentCounts call made by topology, the only
// caller. remainingParents is drained as the walk proceeds and must not be
// reused afterwards.
func (b *flowchartBuilder) declarationOrder(remainingParents map[string]int, children map[string][]string) []string {
	out := make([]string, 0, len(b.order))
	declared := make(map[string]bool, len(b.order))
	for len(out) < len(b.order) {
		key, ok := b.nextDeclarable(remainingParents, declared)
		if !ok {
			// a cycle: nothing is left with all its parents declared
			for _, k := range b.order {
				if !declared[k] {
					declared[k] = true
					out = append(out, k)
				}
			}
			break
		}

		declared[key] = true
		out = append(out, key)
		for _, child := range children[key] {
			remainingParents[child]--
		}
	}
	return out
}

// parentCounts builds the two structures declarationOrder's topological walk
// needs: how many not-yet-declared parents each node still has, and each
// node's child list. A self-loop (a state that transitions to itself) is
// skipped on both sides — counting it would leave the node permanently
// blocked on itself and push the whole diagram onto the cycle path.
// Duplicate edges between the same pair are deliberately NOT collapsed: the
// count and the child list stay in step, so decrementing once per child
// entry drains the count exactly.
func (b *flowchartBuilder) parentCounts() (remainingParents map[string]int, children map[string][]string) {
	remainingParents = make(map[string]int, len(b.order))
	children = make(map[string][]string, len(b.order))
	for _, e := range b.edges {
		if e.from == e.to {
			continue
		}
		children[e.from] = append(children[e.from], e.to)
		remainingParents[e.to]++
	}
	return remainingParents, children
}

// nextDeclarable returns the earliest-first-seen node that is not declared
// yet and has no undeclared parent left, or ok == false when none is left
// (the cycle case declarationOrder handles). Scanning b.order from the start
// every time is what makes the tie-break "earliest first seen wins" rather
// than "whatever the last decrement happened to free".
func (b *flowchartBuilder) nextDeclarable(remainingParents map[string]int, declared map[string]bool) (key string, ok bool) {
	for _, k := range b.order {
		if !declared[k] && remainingParents[k] == 0 {
			return k, true
		}
	}
	return "", false
}

// levels assigns each node a layout level by replaying the vendored
// renderer's own placement rule (graph.go's createMapping) over the very
// declaration order this builder is about to emit, so k — the widest level —
// is predicted without ever invoking the renderer.
//
// The renderer's rule has two steps, and both are reproduced exactly here.
// First it walks its node list in insertion order and calls a node a ROOT
// unless some earlier node already named it as a child; every root goes to
// level 0. Then it walks the same list again and gives each of a node's
// still-unplaced children the node's own level plus one — first parent to
// reach a child wins, later parents are skipped.
//
// That "first parent wins" detail is why this is a replay and not a
// longest-path computation. A node reached both from level 0 and from level 1
// lands on level 1, not level 2, because the level-0 parent is processed
// first. Computing the deepest parent instead over-estimates the depth, which
// under-estimates how many nodes share the widest level, which hands
// mermaidLabelCap a k that is too small.
//
// Cycles need no special case. declarationOrder always emits a first node
// that no earlier node claimed, so there is always at least one root, and
// every later node is either a root itself or was already placed by an
// earlier parent — exactly the invariant the renderer relies on to avoid
// walking a node it has not positioned yet.
func (t flowchartTopology) levels() map[string]int {
	claimed := make(map[string]bool, len(t.order))
	level := make(map[string]int, len(t.order))
	placed := make(map[string]bool, len(t.order))
	for _, key := range t.order {
		if !claimed[key] {
			level[key], placed[key] = 0, true
		}
		claimed[key] = true
		for _, child := range t.children[key] {
			claimed[child] = true
		}
	}

	for _, key := range t.order {
		for _, child := range t.children[key] {
			if placed[child] {
				continue
			}
			level[child], placed[child] = level[key]+1, true
		}
	}
	return level
}

// widestLevel returns k: the largest number of nodes sharing one layout
// level (see levels) — the axis mermaidLabelCap actually needs, since
// flowchart TD places same-level siblings side by side (graph.go). Counting
// over t.order rather than the builder's own insertion order is equivalent:
// declarationOrder emits every node key exactly once, only rearranged.
func (t flowchartTopology) widestLevel() int {
	level := t.levels()
	counts := make(map[int]int, len(t.order))
	for _, key := range t.order {
		counts[level[key]]++
	}
	k := 1
	for _, c := range counts {
		if c > k {
			k = c
		}
	}
	return k
}

// hasAnyEdgeLabel reports whether any accumulated edge carries a non-empty
// label — mermaidLabelCap's labelJog term applies only then (see its doc
// comment).
func (b *flowchartBuilder) hasAnyEdgeLabel() bool {
	for _, e := range b.edges {
		if e.label != "" {
			return true
		}
	}
	return false
}

// source emits this builder's accumulated content as synthetic
// `flowchart TD` source: the adaptive cap is computed exactly once, from the
// diagram's own topology (widestLevel, hasAnyEdgeLabel) — no iteration, no
// dependency on the cap while building (see the plan's "The width cap is
// adaptive" section) — then every label line and edge label is truncated to
// it. Declarations are emitted in declarationOrder (see its doc comment for
// why the order is load-bearing, not cosmetic); edges follow, each either
// `from -->|label| to` or plain `from --> to` when mermaidEdgeLabel reports
// no label survived. Returns "" when empty() — callers must check that
// first, since an empty flowchart source is itself something the vendored
// parser rejects (a builder-empty diagram is meant to fall back to the
// original source, not to this empty string — see transpileMermaid).
//
// The declaration order and the child list are derived ONCE, into a single
// flowchartTopology value the width prediction and the declaration loop then
// share. They used to be recomputed per step, which ran the topological walk
// twice and the O(V+E) parentCounts walk three times for one call.
func (b *flowchartBuilder) source() string {
	if b.empty() {
		return ""
	}

	topo := b.topology()
	capRunes := mermaidLabelCap(b.paneWidth, topo.widestLevel(), b.hasAnyEdgeLabel())

	var out strings.Builder
	out.WriteString("flowchart TD\n")
	for _, key := range topo.order {
		n := b.nodes[key]
		fmt.Fprintf(&out, "%s[%s]\n", n.id, strings.Join(n.renderLabelLines(capRunes), "<br/>"))
	}
	for _, e := range b.edges {
		from, to := b.nodes[e.from].id, b.nodes[e.to].id
		if label := mermaidEdgeLabel(e.label, capRunes); label != "" {
			fmt.Fprintf(&out, "%s -->|%s| %s\n", from, label, to)
		} else {
			fmt.Fprintf(&out, "%s --> %s\n", from, to)
		}
	}
	return out.String()
}

// mermaidBlockHandler is implemented by each diagram-specific transpiler
// (classTranspiler, stateTranspiler) and driven by scanMermaidBlocks, which
// does the shared work neither one needs to duplicate: %% comment stripping,
// blank-line skipping, and a brace-depth stack recognizing a `{ ... }` block
// wherever mermaid syntax nests one (class bodies, state composites,
// namespaces). Two methods are all a per-diagram transpiler differs on:
//
//   - blockHeader is called once for the line that opens a `{ ... }` block,
//     with header the text before the `{`, trimmed (e.g. "class Foo",
//     "state Bar", "namespace Baz"), and depth the nesting level this
//     header line itself sits at (same level as its sibling statements).
//     Its bool result decides whether this block counts toward nesting
//     depth at all: true (a named block — a class body, a composite state)
//     means its contents are dispatched to line at depth+1; false
//     (transparent — a namespace) means its contents are dispatched at the
//     SAME depth, exactly as if the block were not there. This is what
//     makes namespace attribution a no-op for classDiagram: a class nested
//     inside a namespace still reports at depth 1, identical to a top-level
//     class.
//   - line is called for every other non-blank, comment-stripped line,
//     including a block's own closing `}` (so the handler can pop whatever
//     per-block state it pushed in blockHeader) and every plain statement,
//     at whatever depth it currently belongs to.
//   - inRawText is asked BEFORE either of the other two, on every line. While
//     it reports true the handler is consuming free-form text rather than
//     diagram structure — stateDiagram-v2's multi-line note body is the one
//     such region — and the scanner hands the line straight to line without
//     looking at braces at all. That is what stops prose inside a note from
//     opening or closing a block: a note body may perfectly well contain a
//     stray `}` or a line ending in `{`, and either one used to corrupt the
//     brace stack and misattribute every state that followed. The handler
//     itself decides when the region ends, from the line it is given.
type mermaidBlockHandler interface {
	blockHeader(header string, depth int) bool
	line(text string, depth int)
	inRawText() bool
}

// scanMermaidBlocks walks source line by line for h — see mermaidBlockHandler
// for the two calls it drives. Comment stripping and blank skipping happen
// here so neither transpiler needs to repeat that boilerplate; the brace
// stack (named, tracking which currently-open blocks count toward depth —
// see blockHeader's bool result) lives here too, so a mismatched extra `}`
// (more closes than opens) is silently ignored rather than a defensive check
// every transpiler would otherwise need of its own.
//
// The raw-text gate comes first, ahead of every brace test: while the handler
// reports inRawText the line is note prose, not structure, and must not touch
// the brace stack — see mermaidBlockHandler's own doc comment for what went
// wrong when it did.
func scanMermaidBlocks(source string, h mermaidBlockHandler) {
	depth := 0
	var named []bool // brace stack: does the block at this position count toward depth?

	for raw := range strings.SplitSeq(source, "\n") {
		trimmed := strings.TrimSpace(mermaidStripComment(raw))
		if trimmed == "" {
			continue
		}

		if h.inRawText() {
			h.line(trimmed, depth)
			continue
		}

		if trimmed == "}" {
			if len(named) == 0 {
				continue // unbalanced close; nothing open to pop
			}
			h.line(trimmed, depth)
			if named[len(named)-1] {
				depth--
			}
			named = named[:len(named)-1]
			continue
		}

		if header, ok := strings.CutSuffix(trimmed, "{"); ok {
			isNamed := h.blockHeader(strings.TrimSpace(header), depth)
			named = append(named, isNamed)
			if isNamed {
				depth++
			}
			continue
		}

		h.line(trimmed, depth)
	}
}

// mermaidIdentRune reports whether r can CONTINUE a diagram identifier —
// a state name, a class name — and therefore must never be read as the
// boundary that ends a directive keyword (see mermaidKeywordRest).
//
// Letters, digits and "_" are the obvious members. The other three are the
// ones a narrower rule gets wrong in practice: real diagrams name states and
// classes in kebab-case ("style-review", "title-approval", "note-taking"),
// in dotted form ("link.check", "class.Registry"), and with path-like
// segments ("style/guide"). None of those three characters can begin the
// ARGUMENT of a real directive either — every directive this file drops is
// written as "keyword<space>..." or "keyword:<space>..." — so treating them
// as identifier characters costs nothing and saves the statement.
func mermaidIdentRune(r rune) bool {
	switch r {
	case '_', '-', '.', '/':
		return true
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// mermaidKeywordRest cuts keyword off the front of text when text opens with
// it as a WHOLE word: either text is exactly keyword, or the character right
// after it cannot continue an identifier (see mermaidIdentRune). rest is
// whatever followed the keyword, unchanged.
//
// A plain strings.HasPrefix is wrong here, and not harmlessly so. Diagram
// authors name states and classes after ordinary words, and several of those
// names begin with a keyword this patch drops — "notes", "styleGuide",
// "titleFetch", "directionUp", "linkTarget", and the kebab-case
// "style-review" / "title-approval" shapes mermaidIdentRune covers. Matched
// as bare prefixes, every statement mentioning such a name is silently
// discarded, and for the "note" keyword in particular the damage was not one
// line: the multi-line note opener would swallow the entire rest of the
// diagram waiting for an "end note" that never comes.
func mermaidKeywordRest(text, keyword string) (rest string, ok bool) {
	rest, ok = strings.CutPrefix(text, keyword)
	if !ok {
		return "", false
	}
	if rest == "" {
		return "", true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	if mermaidIdentRune(r) {
		return "", false
	}
	return rest, true
}

// mermaidLeadingArrow matches a relation or transition arrow token ANCHORED
// at the very start of the string — the union of every arrow either
// transpiler understands: classDiagram's fourteen relation arrows plus its
// lollipop halves, and stateDiagram-v2's single "-->" (already a member of
// that set). Alternatives are ordered longest-first for the same reason
// classArrowPattern's are: Go's alternation is leftmost-FIRST, so a short
// generic token listed ahead of a longer specific one would steal the match.
//
// One union rather than one pattern per diagram type is deliberate. This is
// used by exactly one caller — mermaidDirectiveShape — whose whole job is to
// answer a question that must have the SAME answer for both transpilers: a
// line whose first token after a keyword is an arrow is a node statement, not
// a directive. Splitting it in two would put the shared invariant back into
// two places that can drift apart, which is precisely the failure this
// discriminator exists to end.
var mermaidLeadingArrow = regexp.MustCompile(
	`^(?:<\|--|--\|>|<\|\.\.|\.\.\|>|--\*|\*--|--o|o--|-->|<--|\.\.>|<\.\.|\(\)--|--\(\)|--|\.\.)`,
)

// mermaidOperandDecoration matches, ANCHORED at the start of the string, one
// piece of syntax mermaid lets a relation operand carry between the node's
// own name and the arrow that follows it. There are exactly three:
//
//	"1", "0..1"     a quoted cardinality        (`Task "1" --> "n" Item`)
//	:::styleName    a style-class assignment    (`Task:::hot --> Item`)
//	~T~             a generic type parameter    (`Repo~T~ --|> Base`)
//
// The style-class alternative stops at the first character that cannot
// continue a style name, so it can never swallow the arrow itself: after
// ":::hot" the "-->" of ":::hot-->Done" is left in place, because a "-" is
// only consumed when a name character follows it. The generic alternative is
// bounded by whitespace and takes the LAST tilde inside that token, so a
// nested parameter ("~Map~String,List~Int~~") is consumed whole.
//
// See mermaidStripOperandDecorations for why these three have to be skipped
// before the arrow test, and mermaidDirectiveShape for the rule they serve.
var mermaidOperandDecoration = regexp.MustCompile(
	`^(?:"[^"]*"|:{2,}[A-Za-z0-9_]*(?:-[A-Za-z0-9_]+)*|~\S*~)`,
)

// mermaidStripOperandDecorations trims leading whitespace from s and then
// removes every operand decoration written at the front of it — see
// mermaidOperandDecoration for the three shapes — repeating until what is
// left starts with something that is not a decoration. The result is the
// text the arrow test in mermaidDirectiveShape must look at.
//
// Repeating rather than stripping once is required: an operand can carry more
// than one decoration at a time, e.g. `Task:::hot "1" --> Item`, and both
// have to be out of the way before the "-->" is the first thing left.
func mermaidStripOperandDecorations(s string) string {
	for {
		s = strings.TrimLeft(s, " \t")
		loc := mermaidOperandDecoration.FindStringIndex(s)
		if loc == nil {
			return s
		}
		s = s[loc[1]:]
	}
}

// mermaidDirectiveShape reports whether rest — the text that FOLLOWS a
// directive keyword matched as a whole word, see mermaidKeywordRest — has
// directive shape rather than node-statement shape.
//
// # Why this function exists at all
//
// This bug class has been found and "fixed" five separate times, and the
// first three fixes all failed the same way: each moved or guarded ONE
// dispatch branch and left the next one open. Round one matched keywords as
// bare prefixes, so a state named "notes" or "styleGuide" was dropped.
// Round two added a word-boundary test, which still cannot separate a node
// named EXACTLY "note" from the note directive. Round three moved the
// relation and transition parse ahead of the keyword check, which saved the
// arrow forms ("note --> Done") and left the COLON forms ("note : ready",
// "style : +enabled()") still being dropped, because those sit further down
// the dispatch. Round four is this function: the decision is made once, from
// the line's SHAPE, so no dispatch position has to be got right for it to
// hold. Round five kept that single decision and fixed what it looked AT — it
// used to test the very first token after the keyword, which is not always
// the arrow, because an operand may carry a quoted cardinality, a
// ":::styleName" assignment or a "~T~" type parameter of its own first.
// `style "1" --> "n" Done` and `note:::hot --> Done` were still being dropped
// as directives. Those decorations are now skipped before the arrow test —
// see mermaidStripOperandDecorations.
//
// # The invariant
//
// A line is a DIRECTIVE only when it has directive shape, and a node
// statement only when it has node-statement shape. Concretely, after a
// keyword the two shapes are:
//
//	directive          keyword ARGUMENT...      ("style Foo fill:#f9f", "note left of X")
//	directive          keyword: TEXT            ("accTitle: My accessible title")
//	node statement     keyword [DECO] ARROW ... (`note --> Done`, `style "1" --> "n" Done`)
//	node statement     keyword : DESCRIPTION    ("note : ready", "style : +enabled()")
//
// DECO is the operand's own decorations, skipped by
// mermaidStripOperandDecorations. The arrow still has to be the first thing
// after them: an ordinary WORD before the arrow keeps the line a directive,
// which is what stops a title like "title Order flow -- v2" or a note whose
// prose quotes an arrow ("note right of X : uses A --> B") from being torn
// into two garbage nodes. That is why the test is "does an arrow start here",
// not "does the line contain an arrow anywhere".
//
// The one genuinely ambiguous pair is "accTitle: text" (a directive) against
// "accTitle : text" (a state or class named accTitle, with a description).
// They are told apart by the space before the colon: mermaid's own grammar
// spells the accessibility directives "accTitle:" with the colon written
// tight against the keyword, so a colon that follows whitespace is a node
// statement's separator. The alternative considered and rejected was to keep
// "accTitle" out of the node-statement fallback entirely and always treat it
// as a directive — rejected because it re-opens exactly the hole this
// function closes, just for one keyword instead of all of them, and mermaid
// itself accepts a state named "accTitle".
//
// A run of two or more colons is never that separator — it is the style
// suffix — so it is stripped as a decoration BEFORE the tight-colon test
// runs, and `note:::hot --> Done` reaches the arrow test rather than being
// claimed by the "accTitle:" branch.
//
// # If you change this
//
// Keep the decision here. Do not re-add a keyword test to a dispatch branch,
// and do not make a call site's POSITION load-bearing for it — that is the
// shape all five regressions had in common.
func mermaidDirectiveShape(rest string) bool {
	if rest == "" {
		return true // the bare keyword alone: no node-statement shape to compete with
	}
	arg := mermaidStripOperandDecorations(rest)
	switch {
	case arg == "":
		return true // keyword plus decorations and/or trailing blanks only
	case strings.HasPrefix(arg, ":"):
		// A single colon: tight against the keyword it is mermaid's own
		// "accTitle:" spelling, after whitespace it separates a node
		// statement's description. arg == rest means nothing was skipped,
		// so the colon really is written tight.
		return arg == rest
	case mermaidLeadingArrow.MatchString(arg):
		return false // "Name --> Other" — a relation or transition
	}
	return true
}

// mermaidDeclarationShape is mermaidDirectiveShape narrowed for the two
// DECLARATION keywords, classDiagram's "class" and stateDiagram-v2's "state".
// Those two behave exactly like a directive keyword when deciding whether the
// line is theirs at all — the only difference is what happens on a match, a
// declaration recorded instead of a line dropped — with one exception: the
// tight-colon form belongs to the accessibility directives alone. Nobody
// writes "class:Foo", so reading it as a declaration of a class keyed ":Foo"
// helps no one, while reading it as a node statement at least keeps the text.
func mermaidDeclarationShape(rest string) bool {
	return !strings.HasPrefix(rest, ":") && mermaidDirectiveShape(rest)
}

// mermaidDirective returns the directive keyword that text is a directive
// for, or "" when text is not a directive at all. This is the ONE place
// either transpiler decides directive-versus-node-statement — see
// mermaidDirectiveShape for the rule and for why it must stay in one place.
//
// Keyword order within keywords does not matter: the whole-word test in
// mermaidKeywordRest already stops a short keyword from claiming a longer
// one's line ("class" never matches "classDef hi fill:red").
func mermaidDirective(text string, keywords []string) string {
	for _, keyword := range keywords {
		rest, ok := mermaidKeywordRest(text, keyword)
		if ok && mermaidDirectiveShape(rest) {
			return keyword
		}
	}
	return ""
}

// --- classDiagram transpiler ---
//
// classDirectiveKeywords lists classDiagram statement keywords that carry no
// information the flowchart-source builder needs: styling/metadata directives
// (style, cssClass, classDef), doc/interaction directives (note, click,
// callback, link, href), layout ("direction"), and accessibility/title
// directives (accTitle, accDescr, title). A line opening with one of these in
// DIRECTIVE shape is dropped — see mermaidDirectiveShape, which is where
// directive shape is defined and is the only place that decision is made.
//
// A class can legitimately be named after any of these words. "note <|-- Done"
// is an ordinary inheritance relation between a class called "note" and one
// called "Done", and "style : +enabled()" is a member of a class called
// "style". Both survive, because neither has directive shape. Longer names
// that merely START with a keyword ("linkTarget", "titleCase", "style-review")
// never match the keyword at all — see mermaidKeywordRest.
//
// "class" is deliberately absent. In classDiagram grammar it is the
// DECLARATION keyword ("class Foo", `class Foo["Display"]`, "class Repo~T~"),
// not a styling directive; classDiagram spells styling as `cssClass "Foo" hi`
// or "Foo:::hi" instead. stateDiagram-v2 is the opposite way round, which is
// why "class" appears in stateDirectiveKeywords and not here.
var classDirectiveKeywords = []string{
	mermaidNoteKeyword, "click", "callback", "link", "href",
	"style", "cssClass", "classDef", "direction",
	"accTitle", "accDescr", "title",
}

// mermaidNoteKeyword is the one directive keyword whose handling goes beyond
// dropping its own line: in stateDiagram-v2 the multi-line opener suppresses
// every following line until "end note" (see
// stateTranspiler.openMultilineNote). Named rather than spelled inline so the
// keyword list and the dispatch that special-cases it cannot drift apart.
const mermaidNoteKeyword = "note"

// classLollipopRelation matches mermaid's lollipop interface notation in
// either direction — "Class1 ()-- Class2" and "Class1 --() Class2" — as a
// WHOLE token: the "()" half must be preceded by start-of-line or
// whitespace.
//
// The boundary is what makes this a token test rather than a substring test,
// and it matters. A plain strings.Contains(text, "()--") also fires on an
// ordinary member line whose method signature happens to run a call into a
// following "--", e.g. "Node : +splitForCreate()--Update", and drops the
// whole member. A real lollipop always writes the "()" as its own token with
// a space in front of it, while a method signature never does — the paren
// closes directly against the method name.
//
// This is the one dropped statement kind classTranspiler.statement tests
// BEFORE the relation parse, and it has to be: a lollipop line really does
// contain a "--" token, so classArrowPattern's plain "--" fallback would
// happily manufacture a bogus relation out of the "()" text if the relation
// parse saw the line first.
var classLollipopRelation = regexp.MustCompile(`(?:^|\s)(?:\(\)--|--\(\))`)

// classDirective returns the classDiagram directive keyword text is a
// directive for, or "" when text is not a directive — the classDiagram
// binding of the shared discriminator (see mermaidDirective). The lollipop
// notation is deliberately NOT folded in here: it is not keyword-shaped at
// all, it is an arrow shape, so it stays its own regexp test in
// classTranspiler.statement.
func classDirective(text string) string {
	return mermaidDirective(text, classDirectiveKeywords)
}

// classMemberSpaceParen matches the FIRST whitespace character that is
// immediately followed by '(' — the space-paren commentary rule from the
// plan's "Keeping boxes readable" section. This is deliberately NOT "the
// first '(' anywhere": a method signature like "+bar() void" or
// "splitForCreate/Update/Replace()" has no space before its own paren at
// all, so this pattern never matches those lines, and the whole line
// survives untouched — a blunter "cut at the first '('" rule would instead
// truncate "+bar() void" down to "+bar" and clip the trailing "()" off
// "splitForCreate/Update/Replace()".
var classMemberSpaceParen = regexp.MustCompile(`\s\(`)

// classMemberText renders one classDiagram member/attribute line safe to
// place as a label line inside a synthesized flowchart node: strip trailing
// space-paren commentary (classMemberSpaceParen, above), then run the
// general node-label sanitizer (mermaidSafeText) so pipes, quotes, and
// angle brackets in real member text (e.g. "+kind : content|asset|uri", a
// real corpus line — see the plan's "Declaration order" section) cannot
// reach the emitted flowchart source unescaped. Truncation to the adaptive
// per-line cap and the classMaxMembers overflow row both happen later, once
// per diagram, in flowchartNode.renderLabelLines — classMemberText itself
// never shortens anything, since the cap is not yet known this early.
func classMemberText(raw string) string {
	s := raw
	if loc := classMemberSpaceParen.FindStringIndex(s); loc != nil {
		s = strings.TrimRight(s[:loc[0]], " \t")
	}
	return mermaidSafeText(s)
}

// classStereotypePattern matches mermaid's "<<word>>" stereotype annotation
// syntax (<<interface>>, <<abstract>>, <<service>>, or any other word an
// author writes), whether the whole line is the annotation (a class-body
// line on its own) or it is the text after a member colon
// ("Foo : <<interface>>"). Detected and rewritten to the literal UML
// notation "«word»" BEFORE mermaidSafeText ever runs on this text —
// mermaidSafeText maps '<' and '>' to parens, which would turn an
// already-sanitized "<<interface>>" into "((interface))" and make this
// pattern permanently unrecognizable afterward. Running the check first
// keeps the blanket '<'/'>' sanitizing rule simple and provably safe, with
// no per-line "is this actually an arrow" exception carved out of it.
var classStereotypePattern = regexp.MustCompile(`^<<\s*(.+?)\s*>>$`)

// classStereotype reports whether text is a stereotype annotation and, if
// so, returns it rendered as "«word»" — the literal notation mermaid itself
// draws for a classDiagram stereotype. "(interface)" was rejected (see the
// plan's Stereotypes section): the corpus already uses parens for member
// commentary in the same box, so a parenthesized stereotype would just read
// as another member.
func classStereotype(text string) (label string, ok bool) {
	m := classStereotypePattern.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", false
	}
	return "«" + mermaidSafeText(m[1]) + "»", true
}

// classStripStyleSuffix strips a mermaid ":::styleName" class-assignment
// suffix from an identifier, keeping the class name itself. Shared by
// parseClassDecl (a declaration's own identifier) and classOperand (a
// relation's operand identifier) — see the plan's Sanitizing note: ignoring
// the suffix entirely, rather than parsing past it, would either skip the
// whole declaration or key the class as "Animal:::highlight", so a relation
// naming plain "Animal" would create a SECOND box for the same class.
// Reuses mermaidColonRun (already ":{2,}", exactly what a style-suffix
// marker is) instead of declaring a near-duplicate pattern.
func classStripStyleSuffix(s string) string {
	if loc := mermaidColonRun.FindStringIndex(s); loc != nil {
		return strings.TrimSpace(s[:loc[0]])
	}
	return s
}

// classStripGeneric strips a trailing "~...~" type-parameter suffix from an
// identifier, keeping the bare class name. This is the same corruption class
// classStripStyleSuffix exists to prevent, reached through a different piece
// of syntax: mermaid lets a class be DECLARED generic
// ("class ScalarContentProperty~T~ { ... }") while every relation naming it
// writes the bare name ("ContentProperty <|-- ScalarContentProperty"). Keying
// the declaration with its type parameter makes those two texts different
// keys, so the diagram draws two boxes for one class — one holding the
// stereotype and members with no relations, one holding all the relations
// with no content. That is a real corpus case (magnolia-content-model's
// README).
//
// The cut is taken at the FIRST '~' rather than by matching a balanced
// "~...~" tail, so a nested type parameter ("Map~String,List~Int~~", which
// ends in two tildes) collapses to "Map" instead of to "Map~String,List~Int".
// A string that merely CONTAINS a tilde without ending in one is left alone,
// and so is one whose first tilde is at index 0 (nothing would be left).
func classStripGeneric(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasSuffix(s, "~") {
		return s
	}
	idx := strings.Index(s, "~")
	if idx <= 0 {
		return s
	}
	return strings.TrimSpace(s[:idx])
}

// classNodeKey normalizes an identifier into the node KEY that every mention
// of the same class — a declaration, a relation operand, a colon-form member
// line — must agree on: both the ":::styleName" style suffix and a trailing
// "~T~" generic type parameter stripped. Anywhere only one of the two strips
// is applied, the same class can end up keyed two different ways and render
// as two disconnected boxes.
func classNodeKey(s string) string {
	return classStripGeneric(classStripStyleSuffix(s))
}

// classBracketAliasPattern matches classDiagram's bracket-alias declaration
// shape, `Foo["Display Name"]`: group 1 is the identifier (the node KEY,
// used for relation matching — never sanitized, see parseClassDecl), group
// 2 is the quoted display text (the node TITLE).
var classBracketAliasPattern = regexp.MustCompile(`^([^\[\s]+)\s*\[(.*)\]$`)

// parseClassDecl parses the text after a "class " keyword — covering all
// four declaration shapes the plan's Grammar section lists: bare ("Foo"),
// bracket alias (`Foo["Display Name"]`), generic ("Repo~T~"), and any of
// those with a trailing ":::styleName" (stripped via classStripStyleSuffix
// before the shape is even examined, since the style suffix always trails
// the whole declaration). key is the raw, UNsanitized identifier, normalized
// through classNodeKey — the same text a relation operand must produce for
// the two to resolve to the same node (see classOperand) — never run through
// mermaidSafeText. title is what a caller passes to flowchartBuilder.setTitle:
// sanitized, but deliberately NOT run through the member-only space-paren
// strip, because the bracket alias's parenthetical is real display text, not
// trailing member commentary (see the plan's note that the bracket alias
// keeps its parenthetical). key is "" when rest is empty after the
// style-suffix strip, signaling "nothing to declare" to the caller.
//
// The generic type parameter is stripped from the KEY but kept in the TITLE:
// "class Repo~T~" keys as "Repo" (so a relation naming plain "Repo" lands on
// the same node — see classStripGeneric) while still rendering its box titled
// "Repo~T~", which is what the author documented. The tilde itself is left
// completely alone by mermaidSafeText (see its own doc comment), so the title
// shows the type parameter exactly as written.
func parseClassDecl(rest string) (key, title string) {
	rest = classStripStyleSuffix(strings.TrimSpace(rest))
	if rest == "" {
		return "", ""
	}
	if m := classBracketAliasPattern.FindStringSubmatch(rest); m != nil {
		key = classStripGeneric(strings.TrimSpace(m[1]))
		display := strings.Trim(strings.TrimSpace(m[2]), `"`)
		return key, mermaidSafeText(display)
	}
	return classStripGeneric(rest), mermaidSafeText(rest)
}

// classDeclFromStatement recognizes a "class ..." declaration — bare
// statement OR block-header form, since scanMermaidBlocks already strips a
// block header's trailing "{" before either classTranspiler.statement or
// classTranspiler.blockHeader ever sees the text, so both reach this same
// helper with identical input. ok is false when text does not open with the
// "class" keyword, when what follows it is a node statement rather than a
// declaration, or when parseClassDecl could not extract a usable key (an
// empty declaration after the style-suffix strip).
//
// "class" is a keyword like any other here, so it goes through the same shape
// test the dropped directives do (mermaidDeclarationShape) — the
// only difference is what happens on a match: a directive is dropped, a
// declaration is recorded. That keeps a class named "class" working the same
// way a class named "note" or "style" does: "class --> loaded" is a relation
// and "class : loaded" is a member, because neither has declaration shape.
// Without the test both used to manufacture a class literally keyed
// "--> loaded".
func classDeclFromStatement(text string) (key, title string, ok bool) {
	rest, isClass := mermaidKeywordRest(text, "class")
	if !isClass || !mermaidDeclarationShape(rest) {
		return "", "", false
	}
	key, title = parseClassDecl(rest)
	return key, title, key != ""
}

// classQuotedSegment matches a double-quoted cardinality literal (e.g. "1",
// "0..1") wherever it appears in a relation operand's raw text.
var classQuotedSegment = regexp.MustCompile(`"([^"]*)"`)

// classOperand splits one relation operand's raw text into its class key
// and optional cardinality — order-independent, since mermaid places the
// cardinality AFTER the class name on a relation's left operand
// (`Task "1"`) but BEFORE it on the right (`"0..1" WorkflowRef`; see the
// real corpus example in the plan's Grammar section): finding the quoted
// segment wherever it falls and treating everything else as the key serves
// both positions with one function. The key is then run through classNodeKey,
// since a relation operand can carry its own ":::styleName" independent of
// the class's own declaration, and can equally well spell the class with its
// generic type parameter when the declaration does (or the other way round)
// — see classStripStyleSuffix and classStripGeneric for why each of those
// must be normalized rather than ignored. key is the RAW identifier — never
// sanitized — so it matches whatever parseClassDecl produced for the same
// class elsewhere in the diagram.
func classOperand(raw string) (key, cardinality string) {
	raw = strings.TrimSpace(raw)
	if m := classQuotedSegment.FindStringSubmatchIndex(raw); m != nil {
		cardinality = raw[m[2]:m[3]]
		key = strings.TrimSpace(raw[:m[0]] + raw[m[1]:])
	} else {
		key = raw
	}
	return classNodeKey(key), cardinality
}

// classArrowPattern matches any of the fourteen classDiagram relation arrow
// tokens the plan's "Relation arrows to labels" table covers. Alternatives
// are ordered longest-first (4-character tokens, then 3-character, then the
// 2-character undirected fallbacks) because Go's regexp alternation is
// leftmost-first, not leftmost-longest: at a start position where more than
// one alternative could match (e.g. "<|--" and a bare "--" share a "--"
// tail), the FIRST listed alternative that matches wins, so a shorter
// generic pattern listed before a longer specific one would silently steal
// the match.
//
// The search itself finds the LEFTMOST occurrence, which is only correct
// once quoted cardinalities are out of the way: the ".." inside a left-hand
// range like `Customer "0..1" --> "1..*" Order` sits earlier in the line
// than the real "-->" and would otherwise win, splitting the line into two
// garbage operands. parseClassRelation therefore searches a masked copy of
// the line (classMaskQuotedSegments) rather than the line itself.
var classArrowPattern = regexp.MustCompile(
	`<\|--|--\|>|<\|\.\.|\.\.\|>|--\*|\*--|--o|o--|-->|<--|\.\.>|<\.\.|--|\.\.`,
)

// classMaskQuotedSegments returns a copy of s with the contents of every
// double-quoted segment (and the quotes themselves) overwritten by spaces,
// byte for byte, so every index into the mask still addresses the same byte
// of s. A space can never form part of a relation arrow token, so searching
// the mask finds only arrows written outside a quoted cardinality — while
// the caller keeps slicing the ORIGINAL string, cardinalities intact.
func classMaskQuotedSegments(s string) string {
	locs := classQuotedSegment.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	masked := []byte(s)
	for _, loc := range locs {
		for i := loc[0]; i < loc[1]; i++ {
			masked[i] = ' '
		}
	}
	return string(masked)
}

// classArrowInfo is one row of classArrowTable: the word label a relation
// arrow maps to (empty for the four association/undirected kinds, which
// carry no default word), and whether the emitted flowchart edge's
// direction is the REVERSE of the arrow's left-to-right source order.
type classArrowInfo struct {
	label string
	flip  bool
}

// classArrowTable is the plan's "Relation arrows to labels" table verbatim:
// one rule generates every flip — the arrow points from the subject to the
// object of the English sentence the label forms (e.g. "Git implements
// Renderer" reads left-to-right regardless of which way "Renderer <|-- Git"
// was written, so that relation flips). A map, rather than a switch or a
// chain of ifs, keeps classArrow itself at cyclomatic complexity 1 — the
// branching lives in this data, not in code a future edit could make
// asymmetric by accident.
var classArrowTable = map[string]classArrowInfo{
	"<|--": {classInheritanceLabel, true},
	"--|>": {classInheritanceLabel, false},
	"<|..": {classInheritanceLabel, true},
	"..|>": {classInheritanceLabel, false},
	"*--":  {"owns", false},
	"--*":  {"owns", true},
	"o--":  {"has", false},
	"--o":  {"has", true},
	"-->":  {"", false},
	"<--":  {"", true},
	"..>":  {"uses", false},
	"<..":  {"uses", true},
	"--":   {"", false},
	"..":   {"", false},
}

// classArrow looks up token's word label and flip flag in classArrowTable —
// a pure map read, cyclomatic complexity 1.
func classArrow(token string) (label string, flip bool) {
	info := classArrowTable[token]
	return info.label, info.flip
}

// classCardinalityTable is the plan's cardinality normalization table
// verbatim: the handful of shapes mermaid authors actually write, mapped to
// a short form that keeps an edge label's width cost low (every character
// costs a column of rendered art — see the plan's "Keeping boxes readable"
// section).
var classCardinalityTable = map[string]string{
	"1":    "1",
	"0..1": "0/1",
	"1..*": "1+",
	"1..n": "1+",
	"*":    "n",
	"n":    "n",
	"0..*": "n",
	"0..n": "n",
	"many": "n",
}

// classCardinality normalizes one quoted cardinality value per
// classCardinalityTable; anything the table does not recognize is
// sanitized and capped at classCardinalityMaxRunes rather than dropped
// outright, so an author's unusual cardinality text still shows up in
// shortened form instead of vanishing.
func classCardinality(raw string) string {
	if norm, ok := classCardinalityTable[raw]; ok {
		return norm
	}
	return mermaidTruncate(mermaidSafeText(raw), classCardinalityMaxRunes)
}

// classComposeCardinality joins a relation's two normalized cardinalities
// (skipping either side that carried none at all) with a space, in the
// order the caller passes them. parseClassRelation passes them already
// re-ordered to match the EMITTED arrow direction, so this function itself
// stays direction-agnostic — see parseClassRelation's doc comment for why
// the pair must flip when the arrow flips.
func classComposeCardinality(fromCard, toCard string) string {
	var parts []string
	if fromCard != "" {
		parts = append(parts, classCardinality(fromCard))
	}
	if toCard != "" {
		parts = append(parts, classCardinality(toCard))
	}
	return strings.Join(parts, " ")
}

// classJoinLabelParts combines a relation's word label (from classArrowTable
// or an explicit "  : label" override) with its cardinality suffix (from
// classComposeCardinality): both, either alone, or neither — matching the
// plan's "appended after a space" rule without ever producing a stray
// leading/trailing space when one side is empty. The composed result is
// still raw, unsanitized text — flowchartBuilder.source runs it through
// mermaidEdgeLabel at emission time (see addEdge's doc comment), which is
// what turns any remaining space into a no-break space; this function must
// never hand-roll that substitution itself.
//
// The word is cut at its own parenthetical FIRST (mermaidCutParenthetical),
// before the suffix is appended. mermaidEdgeLabel applies the very same cut
// later, but by then the suffix is already glued to the end of the string,
// so an explicit label like "resolve {outcome} (act without claiming)" would
// take the cardinality down with it and emit a bare "resolve".
func classJoinLabelParts(word, cardinalitySuffix string) string {
	word = mermaidCutParenthetical(word)
	switch {
	case word == "":
		return cardinalitySuffix
	case cardinalitySuffix == "":
		return word
	default:
		return word + " " + cardinalitySuffix
	}
}

// classAnyColonRun matches ANY run of one or more consecutive colons —
// unlike mermaidColonRun (2+ only), this must also find a lone single
// colon, since that is exactly the signal classSplitTrailingLabel looks for.
var classAnyColonRun = regexp.MustCompile(`:+`)

// classSplitTrailingLabel splits a relation line's optional trailing
// "  : label" (or ": label", or "  :label") from its relation body. This is
// deliberately NOT splitOnFirstColon (which cuts at ANY first colon):
// mermaid's ":::styleName" suffix can appear on either relation operand
// (see classOperand), and it is always a run of 2+ colons with no
// surrounding space, while the real trailing-label separator is always a
// LONE single colon. Scanning for the first colon-run whose length is
// exactly 1 finds the real separator even when an earlier ":::" run would
// otherwise fool a naive first-colon cut.
//
// The scan runs over a quote-masked copy, for exactly the reason
// parseClassRelation does the same (see classMaskQuotedSegments): a quoted
// cardinality may legitimately carry a colon of its own — `Customer "1:n"
// --> Order` is valid mermaid — and that colon sits earlier in the line than
// any real label separator. Cutting there would split the relation
// mid-token, leaving a body of `Customer "1` with no arrow in it at all, so
// the whole relation is dropped and the leftover text manufactures a
// phantom node. Every index into the mask still addresses the same byte of
// text, so the two slices below are taken from the ORIGINAL string.
func classSplitTrailingLabel(text string) (body, label string, ok bool) {
	for _, loc := range classAnyColonRun.FindAllStringIndex(classMaskQuotedSegments(text), -1) {
		if loc[1]-loc[0] == 1 {
			return strings.TrimSpace(text[:loc[0]]), strings.TrimSpace(text[loc[1]:]), true
		}
	}
	return text, "", false
}

// parseClassRelation parses one classDiagram relation's BODY (the trailing
// "  : label", if any, already peeled off by classSplitTrailingLabel — see
// classTranspiler.statementRelation) into its emitted from/to keys, table
// word label, and composed cardinality suffix. ok is false when body
// carries no recognizable arrow token, or either operand resolves to an
// empty key (a malformed or one-sided relation — see the plan's Failure
// modes table: "some lines unparseable, those lines dropped, the rest
// renders").
//
// The cardinality pair is reordered to match the EMITTED arrow, not the
// arrow's left-to-right source order: when classArrow reports flip (the
// emitted edge runs right-to-left relative to how the author wrote it), the
// FROM side's own cardinality is the one that was written on the RIGHT, and
// vice versa. Composing it any other way would silently swap which
// cardinality reads as "the multiplicity at the source end" versus "at the
// target end" whenever a flipping relation (<|--, --*, --o, <--, <.., <|..)
// is used — see the plan's cardinality section.
func parseClassRelation(body string) (fromKey, toKey, word, cardinalitySuffix string, ok bool) {
	// the arrow is located in a quote-masked copy (see classMaskQuotedSegments)
	// so a dotted cardinality range cannot be mistaken for the ".." arrow, then
	// every slice below is taken from the ORIGINAL body at the same indices.
	loc := classArrowPattern.FindStringIndex(classMaskQuotedSegments(body))
	if loc == nil {
		return "", "", "", "", false
	}

	leftKey, leftCard := classOperand(body[:loc[0]])
	rightKey, rightCard := classOperand(body[loc[1]:])
	if leftKey == "" || rightKey == "" {
		return "", "", "", "", false
	}

	word, flip := classArrow(body[loc[0]:loc[1]])
	fromKey, toKey = leftKey, rightKey
	fromCard, toCard := leftCard, rightCard
	if flip {
		fromKey, toKey = rightKey, leftKey
		fromCard, toCard = rightCard, leftCard
	}

	return fromKey, toKey, word, classComposeCardinality(fromCard, toCard), true
}

// classTranspiler implements mermaidBlockHandler for classDiagram source,
// accumulating nodes and edges into a shared flowchartBuilder as
// scanMermaidBlocks walks the diagram body — see this file's package doc
// comment for the overall transpile approach, and the plan's "Grammar
// handled — classDiagram" section for the full grammar covered here.
type classTranspiler struct {
	b *flowchartBuilder

	// currentClass is the key of the class body currently open (between a
	// "class Foo {" header and its matching "}"), or "" when only
	// statement-level lines are being processed. classStack saves the
	// PREVIOUS value across every currently-open block — class OR
	// namespace — so closing one restores the right context regardless of
	// nesting: a class opened inside a namespace restores "" on close (the
	// namespace itself is transparent and never becomes "current" — see
	// blockHeader). One stack entry is pushed per blockHeader call and
	// popped per matching "}" line, mirroring scanMermaidBlocks's own brace
	// stack one level up.
	currentClass string
	classStack   []string
}

// newClassTranspiler creates a classTranspiler that accumulates into b.
func newClassTranspiler(b *flowchartBuilder) *classTranspiler {
	return &classTranspiler{b: b}
}

// blockHeader implements mermaidBlockHandler: a "class Foo {" header opens a
// named block (its members dispatch to member, one level deeper — see
// line); any other header (namespace, or anything unrecognized) is
// transparent, exactly as if the block were not there, which is what makes
// namespace attribution a no-op for classDiagram — a class nested inside a
// namespace still reports at the same depth as a top-level class.
func (c *classTranspiler) blockHeader(header string, _ int) bool {
	c.classStack = append(c.classStack, c.currentClass)

	key, title, ok := classDeclFromStatement(header)
	if !ok {
		return false
	}
	c.b.setTitle(key, title)
	c.currentClass = key
	return true
}

// inRawText implements mermaidBlockHandler: always false. classDiagram
// grammar has no multi-line free-text region — its notes are the single-line
// `note "text"` and `note for X "text"` forms, each dropped within the one
// line that carries it — so there is never a stretch of lines this transpiler
// wants the scanner to stop interpreting. stateTranspiler is where the flag
// does real work.
func (c *classTranspiler) inRawText() bool { return false }

// line implements mermaidBlockHandler: a block's closing "}" restores
// whatever class was open before it (or none), a line at depth > 0 while a
// class body is open is a member line, and everything else is a top-level
// statement.
func (c *classTranspiler) line(text string, depth int) {
	if text == "}" {
		if n := len(c.classStack); n > 0 {
			c.currentClass = c.classStack[n-1]
			c.classStack = c.classStack[:n-1]
		}
		return
	}
	if depth > 0 && c.currentClass != "" {
		c.member(text)
		return
	}
	c.statement(text)
}

// statement handles one top-level (depth 0) classDiagram line: a class
// declaration, a relation, the "Foo : member" colon form (real corpus
// example: "FeatureToggles : +isEnabled()", which never gets a
// "class FeatureToggles" declaration anywhere in that diagram — see
// ensureTitle), a dropped "direction" statement, an ignored statement kind,
// or — if none of those match — a line this patch cannot parse, silently
// omitted per the plan's Failure modes table.
//
// Directive-versus-statement is NOT decided here. classDirective answers that
// from the line's shape alone (see mermaidDirectiveShape), so a class named
// exactly "note", "style" or "title" keeps its relations AND its members
// regardless of where in this method the check sits. Do not re-introduce a
// keyword test into any other branch below: making a branch's POSITION
// load-bearing for that decision is what produced four rounds of the same
// data-loss bug.
//
// The remaining order is about parse ambiguity, not keywords:
//   - the lollipop notation is tested FIRST, because it does contain a "--"
//     token the relation parser would otherwise claim (see
//     classLollipopRelation).
//   - the "ClassName : member" colon form is tested LAST, because a relation
//     can carry its own trailing ": label" and would misparse as a member.
func (c *classTranspiler) statement(text string) {
	if classLollipopRelation.MatchString(text) {
		return
	}
	if classDirective(text) != "" {
		return
	}
	if key, title, ok := classDeclFromStatement(text); ok {
		c.b.setTitle(key, title)
		return
	}
	if c.statementRelation(text) {
		return
	}
	if key, after, ok := splitOnFirstColon(text); ok && key != "" {
		c.statementColonMember(key, after)
	}
}

// statementRelation parses and records one relation statement, reporting
// whether text really was a relation. An explicit trailing "  : label"
// overrides the arrow table's default word — the cardinality suffix is still
// appended either way, per the plan's rule that an explicit label only
// replaces the word, never the cardinality.
//
// The false return is what lets statement fall through to the colon-member
// form. Deciding "is this a relation?" by testing classArrowPattern against
// the whole raw line instead would misroute an ordinary member line whose
// text happens to contain ".." or "--" ("Config : +retries 0..3") into this
// parser, which then finds no arrow in the body ("Config") and drops the
// member entirely.
func (c *classTranspiler) statementRelation(text string) bool {
	body, explicit, hasExplicit := classSplitTrailingLabel(text)
	fromKey, toKey, word, cardinalitySuffix, ok := parseClassRelation(body)
	if !ok {
		return false
	}
	c.ensureTitle(fromKey)
	c.ensureTitle(toKey)
	if hasExplicit && explicit != "" {
		word = explicit
	}
	c.b.addEdge(fromKey, toKey, classJoinLabelParts(word, cardinalitySuffix))
	return true
}

// statementColonMember handles the "ClassName : member" top-level form —
// used both for an ordinary member ("Foo : +bar() void") and for a
// stereotype ("Foo : <<interface>>"). key may carry its own ":::styleName"
// suffix or generic type parameter, both normalized away by classNodeKey the
// same way a declaration's or a relation operand's would be.
func (c *classTranspiler) statementColonMember(key, after string) {
	key = classNodeKey(key)
	if key == "" {
		return
	}
	c.ensureTitle(key)
	if label, ok := classStereotype(after); ok {
		c.b.setStereotype(key, label)
		return
	}
	c.b.addLabelLine(key, classMemberText(after))
}

// member handles one line inside an open class body: mermaid allows a
// stereotype ("<<interface>>") as a body line on its own, so that is checked
// before treating the line as an ordinary member/attribute.
func (c *classTranspiler) member(text string) {
	if label, ok := classStereotype(text); ok {
		c.b.setStereotype(c.currentClass, label)
		return
	}
	c.b.addLabelLine(c.currentClass, classMemberText(text))
}

// ensureTitle gives key a default title (its own raw name) the first time
// it is referenced with no title of its own — needed because a real corpus
// diagram can reference a class ONLY through relations and the colon-member
// form, with no "class X" declaration anywhere (e.g. the FeatureToggles
// corpus diagrams: "FeatureToggles" only ever appears as a relation
// endpoint or in a "FeatureToggles : +isEnabled()" line). Without this, that
// node's title would stay empty and flowchartNode.renderLabelLines would
// fall back to the SYNTHETIC id ("n3") as its only label line — a
// meaningless string where the reader expects the class name. A later
// explicit `class X["Display"]` declaration (in either source order) still
// wins: setTitle always overwrites unconditionally, and this only ever sets
// a title when none exists yet.
func (c *classTranspiler) ensureTitle(key string) {
	n := c.b.node(key)
	if n.title == "" {
		n.title = mermaidSafeText(key)
	}
}

// --- stateDiagram-v2 transpiler ---
//
// stateTranspiler implements mermaidBlockHandler for stateDiagram-v2 (and
// plain stateDiagram — mermaid accepts both keywords for the same grammar,
// see the plan's Grammar section) source, accumulating nodes and edges into
// a shared flowchartBuilder as scanMermaidBlocks walks the diagram body —
// see this file's package doc comment for the overall transpile approach,
// and the plan's "Grammar handled — stateDiagram-v2" section for the full
// grammar covered here. Structurally this mirrors classTranspiler (a
// currentX/xStack pair tracking whatever block is currently open, an
// ensureTitle fallback, a statement dispatcher tried in a fixed order), but
// the two are NOT merged into one handler: a stateDiagram composite block
// means something different from a classDiagram class body (flattened
// "contains" edges vs. member/label lines), and stateDiagram alone has the
// "[*]" pseudo-state fold and the note-block skip, neither of which
// classDiagram grammar has any equivalent of. Forcing one handler to cover
// both would need per-diagram-type branches threaded through every method —
// exactly the coupling two small, separate types avoid.
type stateTranspiler struct {
	b *flowchartBuilder

	// currentComposite is the key of the composite state body currently
	// open (between a "state Foo {" header and its matching "}"), or "" at
	// the top level. compositeStack mirrors classTranspiler.classStack: one
	// entry — the PREVIOUS currentComposite — is pushed per blockHeader call
	// and popped per matching "}" line, so closing a nested composite
	// restores whichever ancestor composite (or none) was open before it.
	currentComposite string
	compositeStack   []string

	// inNote is true while a multi-line "note left/right of X" (no inline
	// colon) is open, until a line matching "end note" closes it — see
	// startNoteIfAny. The single-line "note ... of X : text" form never sets
	// this: it is recognized and dropped within the one line that carries
	// it.
	inNote bool
}

// newStateTranspiler creates a stateTranspiler that accumulates into b.
func newStateTranspiler(b *flowchartBuilder) *stateTranspiler {
	return &stateTranspiler{b: b}
}

// blockHeader implements mermaidBlockHandler: a "state Foo {" header opens a
// named block (its contents dispatch to line, one level deeper, and are
// flattened with "contains" edges back to Foo — see attributeToComposite);
// any other header is transparent, exactly as if the block were not there —
// there is no other bracketed construct in stateDiagram-v2 grammar, but
// scanMermaidBlocks is shared plumbing and must tolerate an unrecognized
// header defensively, same as classTranspiler.blockHeader does for the same
// reason.
func (s *stateTranspiler) blockHeader(header string, _ int) bool {
	s.compositeStack = append(s.compositeStack, s.currentComposite)

	key, title, annotation, ok := stateDeclFromStatement(header)
	if !ok {
		return false
	}
	s.applyStateDecl(key, title, annotation)
	s.currentComposite = key
	return true
}

// inRawText implements mermaidBlockHandler: true while a multi-line note is
// open, which tells scanMermaidBlocks to hand every line straight to line
// without reading it as a block header or a block close — see
// mermaidBlockHandler's doc comment. A note body is prose written by a human,
// so it can contain a stray "}" or end in a "{"; neither is structure and
// neither may reach the brace stack.
func (s *stateTranspiler) inRawText() bool { return s.inNote }

// line implements mermaidBlockHandler: a line while a multi-line note is open
// is note body and is dropped until "end note" closes it; a block's closing
// "}" restores whatever composite was open before it (or none); everything
// else is a top-level (or composite-body) statement.
//
// The note test comes FIRST, ahead of the "}" test, for the same reason
// scanMermaidBlocks asks inRawText before looking at braces: while a note is
// open there is no structure to read, so a body line that happens to be a
// lone "}" is note text and must not pop a composite. Swapping these two back
// re-opens that hole even with the scanner's own gate in place.
func (s *stateTranspiler) line(text string, _ int) {
	if s.inNote {
		if text == "end note" {
			s.inNote = false
		}
		return
	}
	if text == "}" {
		if n := len(s.compositeStack); n > 0 {
			s.currentComposite = s.compositeStack[n-1]
			s.compositeStack = s.compositeStack[:n-1]
		}
		return
	}
	s.statement(text)
}

// statement handles one stateDiagram-v2 line that is not a block header, a
// block close, or note body: a transition, a dropped "direction" statement,
// an ignored statement kind, a note open (single-line dropped inline,
// multi-line opens s.inNote), a "state ..." declaration, the
// "A : description" colon form, or — if none of those match — a line this
// patch cannot parse, silently omitted per the plan's Failure modes table.
//
// Directive-versus-statement is NOT decided by the order of these branches.
// stateDirective answers that from the line's shape alone (see
// mermaidDirectiveShape), so a state named exactly "note", "style", "title",
// "class" or "direction" keeps both its transitions ("note --> Done") and its
// colon description ("note : ready") no matter where the check sits. Do not
// re-introduce a keyword test into any other branch below — that is the shape
// all four rounds of this data-loss bug had in common.
//
// The note keyword is the one directive with a side effect beyond dropping
// its own line: the multi-line "note left of X" form suppresses every line
// until "end note". That side effect hangs off the SAME decision rather than
// off a second keyword test of its own, which is why openMultilineNote is
// reached through the directive's identity instead of re-matching "note".
//
// The remaining order is about parse ambiguity, not keywords: the
// "A : description" colon form is tested last, since a transition can carry
// its own trailing ": label" and a "state ..." declaration its own alias.
func (s *stateTranspiler) statement(text string) {
	if keyword := stateDirective(text); keyword != "" {
		if keyword == mermaidNoteKeyword {
			s.openMultilineNote(text)
		}
		return
	}
	if fromRaw, toRaw, label, ok := parseStateTransition(text); ok {
		s.statementTransition(fromRaw, toRaw, label)
		return
	}
	if key, title, annotation, ok := stateDeclFromStatement(text); ok {
		s.applyStateDecl(key, title, annotation)
		return
	}
	if key, after, ok := splitOnFirstColon(text); ok && key != "" {
		s.ensureTitle(key)
		s.attributeToComposite(key)
		s.b.addLabelLine(key, mermaidSafeText(after))
	}
}

// applyStateDecl records one "state ..." declaration's title and optional
// fork/join/choice annotation, and — since a "state Foo { ... }" composite
// header is itself a declaration reached through the same parse (see
// blockHeader) — attributes Foo to whatever composite currently encloses IT,
// before Foo becomes the new current composite. Shared by blockHeader and
// statement so the "declare, annotate, attribute" sequence is written once,
// not once per caller.
func (s *stateTranspiler) applyStateDecl(key, title, annotation string) {
	s.attributeToComposite(key)
	s.b.setTitle(key, title)
	if annotation != "" {
		s.b.addLabelLine(key, annotation)
	}
}

// statementTransition folds both endpoints' pseudo-state tokens (see
// stateNodeKey), gives each a default title the first time it is seen (a
// state can be introduced purely by appearing in a transition, with no
// "state X" declaration anywhere — the real corpus norm), attributes both to
// the currently open composite if any, and records the edge. stateDiagram
// has one arrow and no relation table (unlike classDiagram's fourteen), so
// there is no arrow-to-label lookup here: label is already whatever text
// followed the transition's colon, straight from parseStateTransition.
func (s *stateTranspiler) statementTransition(fromRaw, toRaw, label string) {
	fromKey := stateNodeKey(fromRaw, s.currentComposite, false)
	toKey := stateNodeKey(toRaw, s.currentComposite, true)

	s.ensureTitle(fromKey)
	s.ensureTitle(toKey)
	s.attributeToComposite(fromKey)
	s.attributeToComposite(toKey)
	s.b.addEdge(fromKey, toKey, label)
}

// ensureTitle gives key a default title the first time it is referenced with
// no title of its own — see classTranspiler.ensureTitle's doc comment for
// why this fallback matters (a state can be introduced purely by a
// transition endpoint, never declared). Folded pseudo-state keys are
// special-cased to their fixed "(start)"/"(end)" labels rather than falling
// through to mermaidSafeText: sanitizing the raw "\x00start.../\x00end..."
// keys would not even be meaningful (they are synthetic, never user text),
// and sanitizing the literal "[*]" token instead (mermaidSafeText maps "["
// and "]" to parens) would render the confusing "(*)" rather than the
// readable "(start)"/"(end)" the plan specifies.
//
// The match is on PREFIX, not equality, because a pseudo-state key carries
// its enclosing composite's name as a scope suffix (see stateNodeKey). A real
// state name can never collide with either prefix: it cannot contain a NUL
// byte.
func (s *stateTranspiler) ensureTitle(key string) {
	n := s.b.node(key)
	if n.title != "" {
		return
	}
	switch {
	case strings.HasPrefix(key, stateStartKey):
		n.title = stateStartLabel
	case strings.HasPrefix(key, stateEndKey):
		n.title = stateEndLabel
	default:
		n.title = mermaidSafeText(key)
	}
}

// attributeToComposite records a "contains" edge from the composite state
// currently open (if any) to key — see the plan's "Composite blocks are
// flattened" note. This can be called on every mention of key inside the
// composite, not just the first: flowchartBuilder.addEdge's own from+to+
// label dedup (see its doc comment) collapses repeats to the single edge the
// plan describes, so no separate "have I attributed this one yet"
// bookkeeping is needed here.
//
// Pseudo-states are attributed like any other child, and must be: a "[*]"
// written inside a composite is that composite's OWN start or end, and its
// key is scoped to the composite so it is a distinct node from the diagram's
// (see stateNodeKey). Leaving it unattributed would draw a start box floating
// beside the composite it belongs to rather than inside it.
func (s *stateTranspiler) attributeToComposite(key string) {
	if s.currentComposite == "" || key == s.currentComposite {
		return
	}
	s.b.addEdge(s.currentComposite, key, "contains")
}

// stateNoteOpenPattern matches the multi-line note opener exactly —
// "note left of X" / "note right of X" with no inline text. Only a line of
// this precise shape is allowed to set inNote, because that flag suppresses
// every following line until an "end note" arrives. A "note" line of any
// other shape is still recognized and dropped, but it drops one line, not
// the remainder of the diagram.
var stateNoteOpenPattern = regexp.MustCompile(`^note\s+(?:left|right)\s+of\s+\S`)

// openMultilineNote opens a multi-line note block when text is mermaid's
// "note left of X" / "note right of X" opener with no inline text, so every
// following line is suppressed until "end note" closes it — see the line
// method. The single-line form, "note right of X : text", carries its text
// after a colon on the same line and opens nothing: it was already dropped by
// the caller, which is the only thing that has to happen to it.
//
// The caller has ALREADY established that text is a note directive — that is
// the whole of the directive-versus-statement decision and it is made in one
// place (see mermaidDirectiveShape). This function must therefore not re-test
// the "note" keyword: a state named exactly "note" never reaches here,
// because "note --> Done" and "note : ready" are node statements by shape and
// never classified as directives at all. Earlier rounds tested the keyword
// HERE instead, which is how a state called "note" used to open a note block
// nothing ever closed and swallow the entire rest of the diagram.
func (s *stateTranspiler) openMultilineNote(text string) {
	if _, _, hasInlineText := strings.Cut(text, ":"); hasInlineText {
		return
	}
	if stateNoteOpenPattern.MatchString(text) {
		s.inNote = true
	}
}

// stateStartKey and stateEndKey are the synthetic node-key PREFIXES every
// "[*]" pseudo-state transition endpoint folds to — see the plan's "[*]
// folding is essential" note: mermaid treats every left-side "[*]" in one
// scope as the SAME start pseudo-state and every right-side one as the SAME
// end, so a real corpus diagram's several "[*]" mentions must collapse to two
// nodes per scope, not one disconnected stub per mention. The leading NUL
// byte can never collide with a real state name (a plain identifier) and the
// ids are synthetic anyway — see flowchartBuilder's own doc comment on
// synthetic ids.
//
// A prefix rather than a whole key because the fold is scoped: stateNodeKey
// appends the enclosing composite's name, so a composite's own "[*]" is a
// different node from the diagram's.
// stateStartLabel and stateEndLabel are the two nodes' rendered titles:
// shape syntax like "(( ))" is unsupported by the vendored renderer and
// parens are inert (see mermaidSafeText), so a literal "start"/"end" would
// be indistinguishable from a real state genuinely named that; wrapping in
// parens keeps the two readable AND distinguishable from any real state
// name mermaid itself would allow.
const (
	stateStartKey   = "\x00start"
	stateEndKey     = "\x00end"
	stateStartLabel = "(start)"
	stateEndLabel   = "(end)"
)

// statePseudoState is the literal token mermaid uses for the start/end
// pseudo-state on either side of a stateDiagram-v2 transition.
const statePseudoState = "[*]"

// stateNodeKey folds raw — one transition endpoint's raw token, exactly as
// captured by parseStateTransition — into its final node key. Every
// occurrence of the literal "[*]" token folds to stateStartKey when it is a
// transition's SOURCE (isTarget false) or stateEndKey when it is a
// transition's TARGET (isTarget true) — the two folds are deliberately
// different keys because mermaid itself treats a left-side "[*]" and a
// right-side one as different pseudo-states (see the const block above).
// Any other token is returned completely unchanged: a real state name is
// never folded or otherwise altered here.
//
// The fold is scoped to scope — the key of the composite state the transition
// was written inside, or "" at the diagram's top level. In mermaid a "[*]"
// written inside `state Active { ... }` is Active's OWN internal start, not
// the diagram's entry point. Folding it together with the diagram-level one
// (which the first version of this function did) draws a false second entry
// arrow straight into the composite's interior: the canonical mermaid
// composite example rendered as BOTH "(start) --> Active" and
// "(start) --> NumLockOff". Appending the scope keeps the two apart while
// still folding every "[*]" WITHIN one scope together, which is the property
// the plan's fold note actually needs. Top-level transitions pass scope "",
// so their key is exactly stateStartKey/stateEndKey as before.
func stateNodeKey(raw, scope string, isTarget bool) string {
	if raw != statePseudoState {
		return raw
	}
	if isTarget {
		return stateEndKey + scope
	}
	return stateStartKey + scope
}

// stateDirectiveKeywords lists stateDiagram-v2 statement keywords that carry
// no information the flowchart-source builder needs. A line opening with one
// of these in DIRECTIVE shape is dropped — see mermaidDirectiveShape, which
// is where directive shape is defined and is the only place that decision is
// made.
//
// It is classDirectiveKeywords plus "class", derived rather than written out
// again so the two lists cannot drift apart. "class" is the one real
// difference between the grammars: in stateDiagram-v2 it APPLIES a CSS class
// to a state ("class Draft highlight"), while in classDiagram it DECLARES a
// class, which is why classDirectiveKeywords leaves it out and
// classDeclFromStatement claims it instead. Every other keyword is a
// directive in both grammars, or is not valid syntax in one of them and is
// dropped there harmlessly.
//
// A state can legitimately be named after any of these words: "note --> Done"
// is a transition out of a state called "note", and "style : enabled" is that
// state's description. Both survive, because neither has directive shape.
// Longer names that merely START with a keyword ("styleGuide", "titleFetch",
// "style-review") never match the keyword at all — see mermaidKeywordRest.
//
// A concurrency separator (a lone "--" line inside a composite's parallel
// regions) needs no entry here: it contains no "-->" and no ":", so it falls
// through every check in statement and is silently dropped for free.
var stateDirectiveKeywords = append([]string{"class"}, classDirectiveKeywords...)

// stateDirective returns the stateDiagram-v2 directive keyword text is a
// directive for, or "" when text is not a directive — the stateDiagram
// binding of the shared discriminator (see mermaidDirective).
func stateDirective(text string) string {
	return mermaidDirective(text, stateDirectiveKeywords)
}

// stateTransitionPattern matches a stateDiagram-v2 transition: group 1 the
// raw "from" token, group 2 the raw "to" token, group 3 the optional label
// text. Both real-corpus colon spacings parse identically —
// "Draft --> InReview : submit" and "[*] --> Draft: open" — because group 2
// is [^\s:]+ (stops at the first whitespace OR colon, so it never swallows
// part of an optional trailing label) and the "\s*" immediately after it
// absorbs any space before an optional colon, however much or little of it
// the author wrote. Group 3 is ".*" to the end of line, deliberately
// unconstrained: a real label is a whole sentence with its own internal
// spaces, parens, and punctuation (see mermaidEdgeLabel, which is what
// actually shortens it later, at emission time).
var stateTransitionPattern = regexp.MustCompile(`^\s*(\S+)\s*-->\s*([^\s:]+)\s*(?::\s*(.*))?$`)

// parseStateTransition parses one stateDiagram-v2 transition line. ok is
// false when line does not contain the "-->" arrow at all — the signal both
// blockHeader's negative case and statement's dispatch order rely on to
// treat text as something other than a transition.
func parseStateTransition(line string) (fromRaw, toRaw, label string, ok bool) {
	m := stateTransitionPattern.FindStringSubmatch(line)
	if m == nil {
		return "", "", "", false
	}
	return m[1], m[2], m[3], true
}

// stateAnnotationPattern matches a trailing "<<word>>" annotation on a
// "state X <<fork>>" / "<<join>>" / "<<choice>>" declaration — mermaid's
// fork/join/choice syntax. It must be located and stripped from the
// declaration BEFORE the state's own identifier is extracted, since it
// trails the whole line rather than standing alone the way a classDiagram
// body's stereotype line does.
var stateAnnotationPattern = regexp.MustCompile(`^(.*\S)\s+(<<\s*.+?\s*>>)$`)

// stateAliasPattern matches mermaid's `"long description" as X` declaration
// alias shape: group 1 the quoted display text (the node TITLE), group 2 the
// identifier that follows "as" (the node KEY, used for transition matching —
// never sanitized, same reasoning as parseClassDecl's bracket-alias key).
var stateAliasPattern = regexp.MustCompile(`^"(.*)"\s+as\s+(\S+)$`)

// parseStateDecl parses the text after a "state " keyword — covering all
// three shapes the plan's Grammar section lists: `state "desc" as X`
// (alias), `state X <<fork>>` (an ordinary box carrying the annotation as a
// label line — mermaid's fork/join/choice states render as an ordinary box
// here, never a diamond, same treatment `<<choice>>` gets per the plan), and
// bare `state X`. key is "" when rest is empty after trimming, signaling
// "nothing to declare" to the caller (mirrors parseClassDecl's own
// empty-key convention).
//
// annotation is "" unless a trailing "<<word>>" was present, in which case
// it is already rendered "«word»" via classStereotype — reused here rather
// than duplicated: the "<<word>>" -> "«word»" notation is generic mermaid
// syntax, not classDiagram-specific, and a fork/join/choice annotation uses
// the exact same shape a classDiagram stereotype does. Calling the existing,
// already-tested helper is what keeps this a shared-spine reuse rather than
// a second near-identical regexp + rewrite the dupl linter (threshold 100)
// would flag between the two transpilers.
func parseStateDecl(rest string) (key, title, annotation string) {
	rest = strings.TrimSpace(rest)

	if m := stateAnnotationPattern.FindStringSubmatch(rest); m != nil {
		if label, ok := classStereotype(m[2]); ok {
			annotation = label
		}
		rest = m[1]
	}

	if m := stateAliasPattern.FindStringSubmatch(rest); m != nil {
		return strings.TrimSpace(m[2]), mermaidSafeText(strings.TrimSpace(m[1])), annotation
	}

	return rest, mermaidSafeText(rest), annotation
}

// stateDeclFromStatement recognizes a "state ..." declaration — bare
// statement OR block-header form, since scanMermaidBlocks already strips a
// block header's trailing "{" before either stateTranspiler.statement or
// stateTranspiler.blockHeader ever sees the text, so both reach this same
// helper with identical input. ok is false when text does not open with the
// "state" keyword, when what follows it is a node statement rather than a
// declaration, or when parseStateDecl could not extract a usable key.
//
// The shape test is the same one classDeclFromStatement applies to "class"
// (mermaidDeclarationShape), for the same reason: a state named "state"
// writes "state : ready", and without the test that produced a state keyed
// ": ready" instead.
func stateDeclFromStatement(text string) (key, title, annotation string, ok bool) {
	rest, isState := mermaidKeywordRest(text, "state")
	if !isState || !mermaidDeclarationShape(rest) {
		return "", "", "", false
	}
	key, title, annotation = parseStateDecl(rest)
	return key, title, annotation, key != ""
}

// transpileMermaid dispatches on source's own diagram kind (see
// mermaidDiagramKind) to produce flowchart source the vendored renderer can
// actually read. Three groups of kinds reach it:
//
//   - classDiagram is fully wired (Task 3): classTranspiler walks the source
//     via scanMermaidBlocks into a paneWidth-aware flowchartBuilder.
//     stateDiagram-v2 and plain stateDiagram are wired the same way (Task 4),
//     via stateTranspiler. Either way, a builder that ends up empty (every
//     statement was a comment or something this patch ignores) falls back to
//     "not handled" exactly like an unrecognized kind — see
//     flowchartBuilder's empty doc comment.
//   - graph and flowchart are NOT transpiled — they are already the renderer's
//     own input language. They go through normalizeFlowchartSource instead,
//     which rewrites the constructs the vendored parser mis-parses (shape
//     suffixes, non-`-->` link forms, a '|' inside a node label) and copies
//     everything else through byte for byte. See mdpreview_flowchart.go's doc
//     comment for the corpus measurement and for why that pass is text-level
//     rather than a rebuild through flowchartBuilder. These two used to be a
//     pure pass-through, and that was the bug: about 60% of real fences hit at
//     least one mis-parsed construct.
//   - everything else, sequenceDiagram included, is not handled here at all.
//     sequenceDiagram in particular takes a completely different code path
//     inside the vendored library and must keep reaching it untouched.
//
// A leading YAML frontmatter block is stripped first (see
// mermaidStripFrontmatter): it would otherwise both hide the real diagram
// kind and leak its own "key: value" lines into the transpilers as bogus
// nodes. Stripping here, rather than in renderMermaidSource, keeps the
// not-handled fallback path handing the completely untouched original
// source to the vendored renderer.
func transpileMermaid(source string, paneWidth int) (string, bool) {
	source = mermaidStripFrontmatter(source)
	switch mermaidDiagramKind(source) {
	case "classDiagram", "classDiagram-v2":
		b := newFlowchartBuilder(paneWidth)
		scanMermaidBlocks(source, newClassTranspiler(b))
		if b.empty() {
			return "", false
		}
		return b.source(), true
	case "stateDiagram", "stateDiagram-v2":
		b := newFlowchartBuilder(paneWidth)
		scanMermaidBlocks(source, newStateTranspiler(b))
		if b.empty() {
			return "", false
		}
		return b.source(), true
	case "graph", "flowchart":
		return normalizeFlowchartSource(source), true
	default:
		return "", false
	}
}

// renderMermaidSource is the hook renderMermaidBlock calls instead of going
// straight to mermaidcmd.RenderDiagram: it gives classDiagram and
// stateDiagram-v2 fences a chance to become synthetic flowchart source first
// (see transpileMermaid), then renders whichever source resulted through the
// exact same underlying renderer every other diagram type already uses.
// paneWidth is the diff pane's current width (or mermaidUnconstrainedWidth),
// threaded down for the two transpilers' adaptive label-width cap — see the
// plan's "The width cap is adaptive" section.
//
// There is deliberately no retry of the ORIGINAL source when a transpiled
// render still errors: the original is classDiagram/stateDiagram-v2 syntax,
// which the vendored parser's "unsupported graph type" check always
// rejects, so a retry could only ever fail the exact same way — see the
// plan's Solution Overview.
//
// A `graph`/`flowchart` fence built from separate subgraphs that do not
// reference each other is rendered one diagram per subgraph and stacked
// instead — see mdpreview_subgraph.go for why, and for the five conditions
// that must hold. The split runs on the TRANSPILED source, never on the raw
// fence, so it always sees normalized statements. Both halves decline by
// returning ok == false rather than an error, and either declining lands on
// the single whole-source render below, unchanged.
func renderMermaidSource(source string, paneWidth int) (string, error) {
	toRender := source
	if transpiled, ok := transpileMermaid(source, paneWidth); ok {
		toRender = transpiled
	}

	if header, blocks, ok := splitFlowchartSubgraphs(toRender); ok {
		if stacked, ok := stackFlowchartSubgraphs(header, blocks); ok {
			return stacked, nil
		}
	}

	rendered, err := mermaidcmd.RenderDiagram(toRender, nil)
	if err != nil {
		return "", fmt.Errorf("render mermaid diagram: %w", err)
	}
	return rendered, nil
}
