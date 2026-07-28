package ui

import (
	"fmt"
	"regexp"
	"strings"

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
// already uses. Task 2 builds the shared spine both diagram types need — the
// dispatch, the sanitizing/label helpers, the flowchart-source builder, and
// the block scanner — plus the one-line hook that replaces the direct
// mermaidcmd.RenderDiagram call in mdpreview.go. classTranspiler
// (Task 3) and stateTranspiler (Task 4) are not implemented yet:
// transpileMermaid recognizes both kinds but reports "not handled" for
// both, so every fence type — the 206 already working today, and these two
// not-yet-wired kinds — takes an exactly-unchanged path through this task.

// mermaidUnconstrainedWidth is passed wherever no real viewport width is
// available (renderMermaidFences's callers — see its doc comment for why
// that function's own signature does not gain a width parameter). It tells
// mermaidLabelCap to skip the adaptive shrink entirely and always allow the
// ceiling (mermaidLabelMaxRunes), matching how every other diagram type
// already behaves: unconstrained, full-width rendering.
const mermaidUnconstrainedWidth = 0

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
// (space -> · and truncation), since an edge label is everything a node
// label is, drawn in a more hazardous position (through an arrow, delimited
// by literal '|' characters).
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
// The <br/>/literal-\n cut runs on the RAW text, before mermaidSafeText —
// deliberately the one rule not layered on top like the others. mermaidSafeText
// maps '<'/'>' to '('/')' and deletes bare backslashes, either of which would
// destroy the very pattern this step is trying to recognize (an already-
// sanitized "<br/>" reads as "(br/)", which the break pattern no longer
// matches; a literal "\n" would already have lost its backslash). Cutting
// first, on the untouched raw text, avoids that ordering trap entirely.
func mermaidEdgeLabel(raw string, capRunes int) string {
	s := raw
	if loc := mermaidBRPattern.FindStringIndex(s); loc != nil {
		s = strings.TrimSpace(s[:loc[0]])
	}

	s = mermaidSafeText(s)

	// Cut at the first '(' or '{': the real corpus puts the trigger word
	// first and the explanation in a parenthetical. A label that STARTS
	// with one (idx == 0) falls back to plain truncation instead of being
	// cut to nothing — cutting at index 0 would discard real content for
	// no gain.
	if idx := strings.IndexAny(s, "({"); idx > 0 {
		s = strings.TrimSpace(s[:idx])
	}

	// Every remaining space becomes a middle dot: the arrow is drawn
	// through the label's own row, so it bleeds through any space at its
	// column (position-dependent — see the plan's Probe finding table).
	s = strings.ReplaceAll(s, " ", "·")

	return mermaidTruncateRunesAndBytes(s, capRunes)
}

// mermaidTruncateRunesAndBytes truncates s so the result satisfies BOTH a
// rune-count cap and, using the same numeric value, a byte-count cap —
// whichever binds first. mermaidTruncate alone only guarantees the rune
// count; a label built from multi-byte runes (e.g. "≤", 3 bytes each) can
// still pass the rune check while reserving far more than capRunes bytes of
// mermaid-ascii's per-column width budget.
func mermaidTruncateRunesAndBytes(s string, capRunes int) string {
	out := mermaidTruncate(s, capRunes)
	if len(out) <= capRunes {
		return out
	}
	// The rune-capped result is still over the byte budget: shrink the
	// ORIGINAL string rune-by-rune from the end, re-deriving the ellipsis
	// each time via mermaidTruncate, until a candidate clears the same
	// numeric cap in bytes too. Bounded by capRunes (at most
	// mermaidLabelMaxRunes = 32) iterations, so the O(n) rescan per
	// candidate is negligible.
	runes := []rune(s)
	for n := len(runes); n > 0; n-- {
		candidate := mermaidTruncate(string(runes[:n]), capRunes)
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
// type already renders: unconstrained.
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

	edgeSrcOrder []string        // node keys, first-seen-as-an-edge-SOURCE order
	seenAsSrc    map[string]bool // set membership for edgeSrcOrder, O(1) dedup

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
		seenAsSrc: make(map[string]bool),
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

	if !b.seenAsSrc[fromKey] {
		b.seenAsSrc[fromKey] = true
		b.edgeSrcOrder = append(b.edgeSrcOrder, fromKey)
	}

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

// declarationOrder returns node keys in the order their `nX[label]`
// declaration lines must be emitted: first-seen-as-an-edge-source order,
// then any node never used as a source, in ITS OWN first-seen order. This is
// not cosmetic — see the plan's "Declaration order" section. The vendored
// renderer computes layout roots by INSERTION order into its own internal
// node list (graph.go's createMapping: a node is a root unless an
// earlier-processed node already claimed it as a child), which mirrors the
// order names first appear anywhere in our emitted text. Declaring every
// node up front (regardless of source/target role) would make every single
// node a root and collapse the whole diagram into one row; declaring a
// fan-in's sources before its shared target instead reproduces the correct
// multi-level layout, because by the time the target's own declaration is
// parsed, at least one source has already registered it as a child.
func (b *flowchartBuilder) declarationOrder() []string {
	out := make([]string, 0, len(b.order))
	out = append(out, b.edgeSrcOrder...)
	inSrc := make(map[string]bool, len(b.edgeSrcOrder))
	for _, k := range b.edgeSrcOrder {
		inSrc[k] = true
	}
	for _, k := range b.order {
		if !inSrc[k] {
			out = append(out, k)
		}
	}
	return out
}

// levels assigns each node a layout level, mirroring closely enough the
// vendored renderer's own level assignment (graph.go's createMapping) to
// predict k — the widest level — without ever invoking the renderer: a root
// is a node never used as an edge target (level 0); every other node sits
// one level below the DEEPEST parent that reaches it, since a fan-in target
// can have more than one source. That needs a fixed-point relaxation rather
// than one topological sweep in edge order — a target reached by an
// already-relaxed deep parent late in the edge list must still be allowed to
// sink further. Bounded at len(b.order) passes (always enough for a DAG;
// classDiagram/stateDiagram-v2 relation graphs are DAGs by construction) —
// bounded rather than unconditional so a cycle (state diagrams can loop)
// cannot spin this forever; a cycle's own levels are then merely
// approximate, which only affects the cap's tightness, never correctness or
// termination.
func (b *flowchartBuilder) levels() map[string]int {
	isTarget := make(map[string]bool, len(b.order))
	for _, e := range b.edges {
		isTarget[e.to] = true
	}

	level := make(map[string]int, len(b.order))
	for _, key := range b.order {
		if !isTarget[key] {
			level[key] = 0
		}
	}

	for range b.order {
		changed := false
		for _, e := range b.edges {
			if parentLevel, ok := level[e.from]; ok {
				if want := parentLevel + 1; want > level[e.to] {
					level[e.to] = want
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return level
}

// widestLevel returns k: the largest number of nodes sharing one layout
// level (see levels) — the axis mermaidLabelCap actually needs, since
// flowchart TD places same-level siblings side by side (graph.go).
func (b *flowchartBuilder) widestLevel() int {
	level := b.levels()
	counts := make(map[int]int, len(b.order))
	for _, key := range b.order {
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
// it. Declarations are emitted via declarationOrder (see its doc comment for
// why the order is load-bearing, not cosmetic); edges follow, each either
// `from -->|label| to` or plain `from --> to` when mermaidEdgeLabel reports
// no label survived. Returns "" when empty() — callers must check that
// first, since an empty flowchart source is itself something the vendored
// parser rejects (a builder-empty diagram is meant to fall back to the
// original source, not to this empty string — see transpileMermaid).
func (b *flowchartBuilder) source() string {
	if b.empty() {
		return ""
	}

	capRunes := mermaidLabelCap(b.paneWidth, b.widestLevel(), b.hasAnyEdgeLabel())

	var out strings.Builder
	out.WriteString("flowchart TD\n")
	for _, key := range b.declarationOrder() {
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
type mermaidBlockHandler interface {
	blockHeader(header string, depth int) bool
	line(text string, depth int)
}

// scanMermaidBlocks walks source line by line for h — see mermaidBlockHandler
// for the two calls it drives. Comment stripping and blank skipping happen
// here so neither transpiler needs to repeat that boilerplate; the brace
// stack (named, tracking which currently-open blocks count toward depth —
// see blockHeader's bool result) lives here too, so a mismatched extra `}`
// (more closes than opens) is silently ignored rather than a defensive check
// every transpiler would otherwise need of its own.
func scanMermaidBlocks(source string, h mermaidBlockHandler) {
	depth := 0
	var named []bool // brace stack: does the block at this position count toward depth?

	for raw := range strings.SplitSeq(source, "\n") {
		trimmed := strings.TrimSpace(mermaidStripComment(raw))
		if trimmed == "" {
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

// transpileMermaid dispatches on source's own diagram kind (see
// mermaidDiagramKind) to produce synthetic flowchart source for the two
// diagram types this patch adds support for. Both are named explicitly in
// the switch below — "recognized" in the sense the plan's Task 2 scope calls
// for — but return "not handled" for now: classTranspiler and
// stateTranspiler are Task 3 and Task 4. Until either lands, every diagram
// kind — the 206 fences that already render today, and these two
// recognized-but-not-yet-transpiled kinds — takes exactly today's path
// through renderMermaidSource, which is what lets this task land with the
// already-working set of diagrams provably unchanged.
//
//nolint:unparam // paneWidth is threaded down now — matching the plan's
// Task 2 scope for width-plumbing — so classTranspiler/stateTranspiler
// (Task 3/4) can build a paneWidth-aware flowchartBuilder without a second
// signature change here. Both stub branches below ignore it until then.
func transpileMermaid(source string, paneWidth int) (string, bool) {
	switch mermaidDiagramKind(source) {
	case "classDiagram", "stateDiagram-v2":
		return "", false
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
func renderMermaidSource(source string, paneWidth int) (string, error) {
	toRender := source
	if transpiled, ok := transpileMermaid(source, paneWidth); ok {
		toRender = transpiled
	}

	rendered, err := mermaidcmd.RenderDiagram(toRender, nil)
	if err != nil {
		return "", fmt.Errorf("render mermaid diagram: %w", err)
	}
	return rendered, nil
}
