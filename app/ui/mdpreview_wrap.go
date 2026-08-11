package ui

import (
	"regexp"
	"strings"

	"github.com/mattn/go-runewidth"
)

// This file holds the node-label WRAP pass for rendered mermaid art. Like
// mdpreview.go, mdpreview_transpile.go, mdpreview_flowchart.go,
// mdpreview_subgraph.go, mdpreview_nbsp.go and mdpreview_collision.go it is
// patch-owned and does not exist upstream — see mdpreview.go's own doc comment
// for why new logic goes in new files rather than into an upstream one.
//
// # What it does
//
// A box in the vendored renderer is as wide as its widest label line, and a
// flowchart's art is as wide as the boxes it has to place side by side. One
// long node label therefore drags the whole drawing past the pane, and the
// reader has to pan sideways with arrow keys to read a diagram that would fit
// if the label were broken over two lines. The renderer already honors
// `<br/>` inside a node label (newGraphLabel in
// vendor/.../mermaid-ascii/cmd/label.go splits on it and takes the widest
// line), so inserting breaks into the SOURCE before rendering is enough — no
// renderer change, no fork.
//
// Measured on testdata/mermaid/collision-three-branches.mmd at pane 160, on
// the art the LR retry left behind:
//
//	before the wrap    281 columns, 25 rows, 0 collisions
//	after the wrap     150 columns, 39 rows, 0 collisions
//
// Width nearly halves and height grows by half. That is the right trade here
// and only here: the preview scrolls vertically for free (the diff viewport's
// own scroll), while sideways it moves one column per arrow-key press, so a
// tall diagram costs the reader nothing and a wide one costs them keystrokes
// per column. The renderer puts a blank row between label lines
// (graphLabelLineGap), so a label broken in two costs three rows, which is
// where most of the height goes. See mermaidWrapPaneShare for the full
// target-by-target table.
//
// # Why it is gated on overflow
//
// Unlike the LR retry, which only ever fires on a fence the collision detector
// believes is already broken, wrapping would otherwise change EVERY diagram
// carrying a long label — including the great majority that render perfectly
// today. That blast radius is not worth a readability win, so the pass is
// gated exactly the way everything else on this path is: render normally
// first, and only when the resulting art is WIDER THAN THE PANE try the
// wrapped variant, keeping it only when it is strictly narrower and no more
// colliding than the render it would replace. A fence whose art already fits
// takes a byte-identical path to the pre-wrap build, and never pays a second
// render — see TestRenderMermaidSource_FittingFenceIsByteIdenticalToTheUnwrappedRender.
//
// # Why it runs AFTER the LR retry, not before
//
// The two passes answer different questions. The LR retry is a CORRECTNESS
// pass: a diagram whose edge labels are painted over each other cannot be read
// at any pane width. The wrap is a READABILITY pass: a diagram that is merely
// too wide can still be read, it just costs panning. So the retry decides
// which render the reader gets, and the wrap only tries to narrow whatever
// won — it re-wraps the source in the direction the retry kept and never
// re-flips. The other order would let a narrowing decision throw away a
// collision fix, trading a diagram the reader can read for one they cannot.

// mermaidWrapPaneShare is the divisor that turns a pane width into the FIRST
// wrap target tried: a label line may take about one quarter of the pane.
//
// The share comes from the shape of the art rather than from taste. A
// flowchart places boxes side by side along its direction, and a decision node
// with three branches puts three boxes on one row; each box spends 4 columns
// on its own border and padding, and the renderer leaves a gap between
// neighbors. Three quarter-pane labels plus that chrome is about a pane wide,
// which is the widest arrangement that can still fit.
//
// A quarter is only the opening bid, because how much art width a given target
// buys back depends on the diagram's direction, and the direction is not this
// pass's to choose (see the file doc comment on ordering). Measured on
// testdata/mermaid/collision-three-branches.mmd at pane 160, where the LR
// retry has already won and so LR is the direction wrapped:
//
//	target  TD width  LR width
//	none       207       281
//	40         157       229
//	34         136       212
//	28         122       188
//	22         106       164
//	20         101       150
//	16          90       135
//
// The same target that brings the top-down render well under the pane leaves
// the left-to-right one 50 columns over it, because LR sums the box widths
// along the row the reader is panning across. So the targets are a short
// ladder rather than one number — see mermaidWrapTargets.
const mermaidWrapPaneShare = 4

const (
	// mermaidWrapMinRunes is the floor on the wrap target. Below this a label
	// line is one short word and the box becomes a column of words, which is
	// harder to read than a wide box — and on a pane narrow enough to compute
	// a smaller target, no flowchart of any width was going to fit anyway.
	// It matches mermaidLabelMinRunes, the floor the transpiled class/state
	// path already uses for the same reason.
	mermaidWrapMinRunes = 16

	// mermaidWrapMaxRunes is the ceiling on the wrap target. Past this the
	// wrap stops buying width back: on the three-branch fixture at pane 160 a
	// 34-rune target already renders 136 columns, while the unwrapped render
	// is 281 — the last few runes of target move the art by a column or two.
	// A very wide pane does not need a bigger target, it needs no wrap at
	// all, and the overflow gate above is what gives it that.
	mermaidWrapMaxRunes = 34
)

// mermaidWrapBreak is the line break inserted between wrapped label lines. It
// is the spelling htmlBreakPattern in the vendored renderer's label.go reads,
// and the spelling ~80% of the corpus's own hand-written multi-line labels
// already use.
const mermaidWrapBreak = "<br/>"

// mermaidAuthorLineBreak matches every line break the vendored renderer honors
// inside a label: any `<br>` spelling (the pattern is copied from
// htmlBreakPattern in vendor/.../mermaid-ascii/cmd/label.go), a literal
// backslash-n, and a real newline. A label containing any of them is the
// author's own line breaking and is left completely alone — they decided where
// the box breaks, and a second opinion from this pass would fight theirs.
//
// It doubles as the idempotence guard: this pass's own `<br/>` reads as an
// author break on a second run, so wrapping an already-wrapped source is a
// no-op.
var mermaidAuthorLineBreak = regexp.MustCompile(`(?i)<br\s*/?>|\\n|\n`)

// mermaidNodeLabelWrapWidth is the per-line rune target for one pane share: the
// pane divided by share, clamped into the
// mermaidWrapMinRunes..mermaidWrapMaxRunes band.
//
// A non-positive or absurd pane width — including mermaidUnconstrainedWidth,
// which is math.MinInt and would divide to a large negative — clamps up to the
// floor rather than producing a nonsense target. In production the
// unconstrained sentinel never reaches here at all, because the overflow gate
// declines any non-positive pane before asking for a target.
func mermaidNodeLabelWrapWidth(paneWidth, share int) int {
	return min(max(paneWidth/share, mermaidWrapMinRunes), mermaidWrapMaxRunes)
}

// mermaidWrapTargets is the ladder of label targets mermaidNarrowIfOverflowing
// tries, widest first: a quarter of the pane, then an eighth. It stops at two
// rungs, and drops the second when the clamp has already collapsed it onto the
// first (which is what a pane narrow enough to bottom out at
// mermaidWrapMinRunes does).
//
// Widest first, stopping at the first rung whose render FITS, means the reader
// gets the fewest broken labels that solve the problem — a diagram that only
// just overflows is nudged rather than folded into a column of words. The
// second rung exists because a quarter is not always enough: on the
// three-branch fixture at pane 160 the quarter-rung (34 after the clamp)
// renders 212 columns in the LR direction the retry kept, while the
// eighth-rung (20) renders 150 and fits. See mermaidWrapPaneShare for the full
// measurement.
//
// Two rungs is the whole ladder because each rung costs a render of the fence.
// A third would buy at most a few more columns — the table in
// mermaidWrapPaneShare flattens out below an eighth of the pane — for a third
// of the wall-clock a preview keypress takes on a large document. Only a fence
// that already overflows pays even the first one.
func mermaidWrapTargets(paneWidth int) []int {
	first := mermaidNodeLabelWrapWidth(paneWidth, mermaidWrapPaneShare)
	if second := mermaidNodeLabelWrapWidth(paneWidth, 2*mermaidWrapPaneShare); second < first {
		return []int{first, second}
	}
	return []int{first}
}

// mermaidWrapLabelText breaks one node label's text so no line is wider than
// width display cells, joining the lines with mermaidWrapBreak. The label is
// returned completely unchanged when:
//
//   - width is non-positive, or the label is empty
//   - the label already carries an author line break (see
//     mermaidAuthorLineBreak)
//   - the label already fits width
//   - the label has no break opportunity: a single word, however long. A word
//     is never split — a broken identifier or path is worse to read than a
//     wide box, and the box is only as wide as that one word either way.
//
// Breaks are taken on ASCII spaces and tabs ONLY, never on U+00A0. That is not
// an accident of the split function: an EDGE label reaching this pass would
// already have had its spaces substituted for no-break spaces (see
// mdpreview_nbsp.go), so treating U+00A0 as unbreakable means even a bug that
// routed one here could not break it — and the renderer does not honor
// `<br/>` in an edge label at all, it prints the five characters literally.
// strings.Fields would split on U+00A0, since unicode.IsSpace counts it, which
// is exactly why this uses its own predicate.
//
// A surrounding pair of double quotes is held outside the wrap and re-applied,
// so the quotes stay at the extremes where the vendored parser's own
// strings.Trim finds them, and so the width measured is the label's own rather
// than the label's plus two.
//
// Width is measured with runewidth.StringWidth because that is exactly what
// newGraphLabel uses to size the box, so the target here and the box's real
// width agree on wide-glyph and combining text.
func mermaidWrapLabelText(label string, width int) string {
	if width <= 0 || label == "" || mermaidAuthorLineBreak.MatchString(label) {
		return label
	}
	inner, quoted := mermaidUnquoteLabel(label)
	if runewidth.StringWidth(inner) <= width {
		return label
	}
	words := strings.FieldsFunc(inner, mermaidWrapBreakOpportunity)
	if len(words) < 2 {
		return label
	}

	var lines []string
	current := ""
	for _, word := range words {
		switch {
		case current == "":
			current = word
		case runewidth.StringWidth(current)+1+runewidth.StringWidth(word) <= width:
			current += " " + word
		default:
			lines = append(lines, current)
			current = word
		}
	}
	wrapped := strings.Join(append(lines, current), mermaidWrapBreak)
	if quoted {
		return `"` + wrapped + `"`
	}
	return wrapped
}

// mermaidWrapBreakOpportunity reports whether a rune is a place a label line may
// break. Only the ASCII space and tab qualify — see mermaidWrapLabelText for why
// U+00A0 deliberately does not.
func mermaidWrapBreakOpportunity(r rune) bool {
	return r == ' ' || r == '\t'
}

// mermaidUnquoteLabel splits a label's surrounding double quotes off its text,
// reporting whether it had them. A label that carries a quote of its own keeps
// both layers and is reported unquoted, matching normalizeFlowchartEdgeLabel's
// rule: there is no honest way to tell where such a label was meant to end.
func mermaidUnquoteLabel(label string) (inner string, quoted bool) {
	if len(label) > 2 && label[0] == '"' && label[len(label)-1] == '"' {
		if trimmed := label[1 : len(label)-1]; !strings.Contains(trimmed, `"`) {
			return trimmed, true
		}
	}
	return label, false
}

// mermaidWrapNodeLabels rewrites every NODE label in a flowchart source so no
// label line is wider than width, and returns the source unchanged when there
// was nothing to wrap.
//
// It must run on NORMALIZED source — the output of normalizeFlowchartSource,
// or of the class/state transpilers, which emit the same form. That is what
// makes it a single-shape problem: every node shape has already been folded
// into `Id[label]`, every link into `-->` / `-->|label|`, and every styling
// directive dropped. In production that is guaranteed by where it sits, at the
// end of renderMermaidSource, on a source that has already been through
// transpileMermaid.
//
// Four things are never touched, and each has its own reason:
//
//   - a source whose diagram kind is not graph/flowchart. Everything below
//     assumes flowchart statement shapes; a sequenceDiagram's `|` and `[` mean
//     something else entirely.
//   - the header line, blank lines and `%%` comment lines — matching
//     normalizeFlowchartSource, which skips the same three.
//   - a structural statement (subgraph, end, accTitle, accDescr). A subgraph
//     header's `Id [Label]` looks exactly like a node but is not one, and the
//     renderer draws its title on the group border where a `<br/>` has no
//     defined meaning.
//   - an EDGE label. flowchartEdgeAt consumes each arrow together with its
//     `|label|` suffix, so the walk copies it through before any byte of it
//     can be read as a node shape. The renderer does not honor `<br/>` in an
//     edge label — it prints the five characters literally — so wrapping one
//     would corrupt it.
func mermaidWrapNodeLabels(source string, width int) string {
	if width <= 0 {
		return source
	}
	switch mermaidDiagramKind(source) {
	case "graph", "flowchart":
	default:
		return source
	}

	lines := strings.Split(source, "\n")
	out := make([]string, 0, len(lines))
	headerSeen := false
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
		out = append(out, mermaidWrapNodeLabelsLine(line, width))
	}
	return strings.Join(out, "\n")
}

// mermaidWrapNodeLabelsLine wraps every node label on one normalized body line.
// The walk is deliberately the same shape as normalizeFlowchartNodes — arrows
// consumed whole first, node shapes second, everything else copied byte for
// byte — so the two passes agree on which `[` opens a node label and which
// `|...|` is an edge label, rather than each having its own opinion.
//
// An inline `%%` comment is split off and re-appended untouched, for the same
// reason normalizeFlowchartLine does it: the vendored parser cuts every line at
// its first `%%`, so the tail is prose the render never sees.
func mermaidWrapNodeLabelsLine(line string, width int) string {
	body, comment, hasComment := strings.Cut(line, "%%")
	if flowchartDirective(body, flowchartStructuralKeywords) != "" {
		return line
	}

	masked := flowchartMaskLabels(body)
	var out strings.Builder
	for i := 0; i < len(body); {
		if segment, next, ok := flowchartEdgeAt(body, masked, i); ok {
			out.WriteString(segment)
			i = next
			continue
		}
		if replacement, next, ok := flowchartShapeAt(body, i); ok {
			out.WriteString(mermaidWrapBracketedLabel(replacement, width))
			i = next
			continue
		}
		out.WriteByte(body[i])
		i++
	}
	if hasComment {
		return out.String() + "%%" + comment
	}
	return out.String()
}

// mermaidWrapBracketedLabel wraps the text inside one `[label]` segment.
// flowchartShapeAt always returns that exact shape, so the delimiters are
// stripped positionally; a segment that somehow is not bracketed is handed back
// untouched rather than sliced.
func mermaidWrapBracketedLabel(segment string, width int) string {
	if len(segment) < 2 || segment[0] != '[' || segment[len(segment)-1] != ']' {
		return segment
	}
	return "[" + mermaidWrapLabelText(segment[1:len(segment)-1], width) + "]"
}

// mermaidNarrowIfOverflowing decides whether a second, node-label-wrapped
// render should replace the art the reader would otherwise get, and performs
// that decision. render is that second render call, injected so the decision is
// testable without the real vendored renderer — production passes
// mermaidcmd.RenderDiagram.
//
// toRender is the source that produced firstRender; art is what
// mermaidRetryLRIfColliding returned for it, which is either firstRender itself
// or the LR-flipped render that beat it. The source to wrap is therefore
// toRender when those two are equal and the flipped source when they are not:
// the retry keeps a flip only when it collides STRICTLY fewer times, and a
// flipped render that were byte-identical to the first could not collide fewer
// times, so `art != firstRender` holds exactly when the flip was kept.
//
// Two preconditions decide whether any of this happens at all, in the order
// that makes each a cheap short-circuit before the next:
//
//  1. there is a pane to fit into — a non-positive width (including
//     mermaidUnconstrainedWidth) is no constraint to respect
//  2. the art the reader would get OVERFLOWS that pane. This is the gate that
//     keeps the blast radius to diagrams that are already unreadable without
//     panning; a fitting fence returns here, before any wrapping or rendering
//
// Past those, each rung of mermaidWrapTargets is tried widest first, and a
// rung's render is a CANDIDATE only when all of these hold:
//
//   - the wrap changed the source at all — a flowchart with no label long
//     enough to break costs no render
//   - the render succeeds and is non-blank
//   - it is STRICTLY narrower than the art it would replace
//   - it does not collide MORE than that art. Wrapping packs boxes closer
//     together, so it genuinely can create an edge-label collision that was not
//     there; this is measured with the same detector the LR retry is gated on
//     rather than assumed away
//
// The first candidate that FITS the pane wins outright — that is the widest
// target that solves the problem, so the fewest labels are broken. If no rung
// fits, the narrowest candidate is kept: still an improvement, since the reader
// pans across fewer columns than before. If no rung produces a candidate at
// all, art is returned unchanged.
//
// Every failure mode therefore degrades to the pre-wrap output. A panic inside
// the injected render is caught for the same reason mermaidRetryLRIfColliding
// catches it: the point of the pass is to improve on art we already hold, and
// letting the panic out would lose it to renderMermaidBlock's outer recover,
// whose fallback is the fence's verbatim source text.
func mermaidNarrowIfOverflowing(toRender, firstRender, art string, paneWidth int, render func(string) (string, error)) (result string) {
	defer func() {
		if r := recover(); r != nil {
			result = art
		}
	}()

	if paneWidth <= 0 {
		return art
	}
	width := mermaidArtWidth(art)
	if width <= paneWidth {
		return art
	}

	source := toRender
	if art != firstRender {
		flipped, ok := mermaidFlipDirectionToLR(toRender)
		if !ok {
			return art
		}
		source = flipped
	}

	best, bestWidth := art, width
	collisions := mermaidCollisionCount(source, art)
	for _, target := range mermaidWrapTargets(paneWidth) {
		candidate, candidateWidth, ok := mermaidWrapCandidate(source, target, bestWidth, collisions, render)
		if !ok {
			continue
		}
		best, bestWidth = candidate, candidateWidth
		if candidateWidth <= paneWidth {
			break
		}
	}
	return best
}

// mermaidWrapCandidate renders source wrapped at one target and reports the art
// and its width when that render is an acceptable replacement for art of width
// maxWidth and collisions collision count — see mermaidNarrowIfOverflowing for
// the rules and why each is there.
func mermaidWrapCandidate(source string, target, maxWidth, collisions int, render func(string) (string, error)) (art string, width int, ok bool) {
	wrapped := mermaidWrapNodeLabels(source, target)
	if wrapped == source {
		return "", 0, false
	}
	candidate, err := render(wrapped)
	if err != nil || strings.TrimSpace(candidate) == "" {
		return "", 0, false
	}
	if candidateWidth := mermaidArtWidth(candidate); candidateWidth < maxWidth {
		if mermaidCollisionCount(wrapped, candidate) <= collisions {
			return candidate, candidateWidth, true
		}
	}
	return "", 0, false
}
