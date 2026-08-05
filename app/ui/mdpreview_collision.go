package ui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	xansi "github.com/charmbracelet/x/ansi"
)

// This file holds the edge-label collision detector for rendered mermaid art.
// Like mdpreview.go, mdpreview_transpile.go, mdpreview_flowchart.go,
// mdpreview_subgraph.go and mdpreview_nbsp.go it is patch-owned and does not
// exist upstream — see mdpreview.go's own doc comment for why new logic goes
// in new files rather than into an upstream one.
//
// # What it detects
//
// drawTextOnLine in vendor/github.com/AlexanderGrooff/mermaid-ascii/cmd/arrow.go
// places each edge label at the midpoint of its own arrow line with no check
// for cells another label already occupies. A decision node with three or more
// labeled out-edges therefore puts two labels on the same output row, and there
// are two shapes to that, both of which this file counts.
//
// The crowded shape — both labels survive, crammed into one corridor, and the
// reader cannot tell which arrow either belongs to:
//
//	│   What kind of property is it?   ├◄───collection────single composite──────┤
//
// The overwriting shape — the layers land on the same columns, so mergeDrawings
// paints one label over the other and NEITHER survives as a word:
//
//	│   What kind of property is it?   ├◄───sincollectionite───────┤
//
// That second row is `collection` painted over `single composite`; `sin` and
// `ite` are all that is left of the label underneath. It is the worse of the
// two — the crowded shape is at least readable if you know what you are looking
// at — so a detector that only knew the crowded shape was blind to the case
// this whole retry exists for.
//
// The complete fix is in drawTextOnLine itself — reserve occupied cells and
// nudge the label along its line — which would mean forking mermaid-ascii and
// carrying a `replace` directive. This detector is the cheap half of the cheap
// fix instead: it counts collisions so the caller can decide whether a second
// render in another direction is worth trying (see the retry in
// renderMermaidSource).
//
// # Why the count and not a bool
//
// The retry keeps the second render only when it is STRICTLY better, so the
// caller needs to compare two renders rather than ask each "is this broken".
// A flipped render that trades one collision for another is not an improvement
// and must lose the comparison.

// mermaidCollisionGap caps how many columns two DISTINCT edge labels on one
// row may be apart and still read as crammed into one arrow corridor. It is a
// CAP, not the threshold itself: the threshold for a given pair is the
// smaller of this and either label's own length (see mermaidCollisionLimit).
//
// Length has to enter the threshold because "too close to tell apart" is
// relative to how big the words are. `collection` and `single composite` four
// columns apart read as one run of text — the gap is far smaller than either
// word. `no` and `yes` five columns apart read as two plainly separate words —
// the gap is larger than both. A fixed threshold cannot separate those two,
// and a fixed 8 called the second one a collision, which sent a perfectly
// readable 80-column diagram off to a 273-column LR retry.
//
// The cap still matters on top of that: two long labels in different regions
// of a wide diagram may share a row with a node box between them, and without
// the cap their own length would make even that gap "close".
const mermaidCollisionGap = 8

// mermaidLabelFragmentMinRunes is the shortest run of leftover letters that is
// allowed to count as the wreckage of an overwritten label (see
// mermaidRunIsLabelFragment). One or two letters match inside almost any word
// by chance, so a shorter run is treated as ordinary node text.
const mermaidLabelFragmentMinRunes = 3

// mermaidPipedEdgeLabel captures the text inside an `-->|label|` edge-label
// suffix, with the surrounding quotes of a `-->|"label"|` optional.
//
// Only the piped spelling is matched, and that is enough: normalizeFlowchartLine
// rewrites the inline `A -- label --> B` form into the piped one before any of
// this runs (see normalizeFlowchartEdgeLabel, which is the single chokepoint
// for both spellings), and the transpiled class/state paths emit the piped form
// directly. The detector is only ever handed source that has already been
// through that normalization, and only for a source whose header
// mermaidFlipDirectionToLR accepted — mermaidRetryLRIfColliding tests the
// header before it counts, so a `|...|` that means something else entirely in
// some other diagram type never reaches this pattern.
var mermaidPipedEdgeLabel = regexp.MustCompile(`\|"?([^|"\n]+)"?\|`)

// mermaidEdgeLabels pulls every distinct edge label out of a mermaid source,
// longest first.
//
// Longest-first is not cosmetic — it is what makes the column marking in
// mermaidCollisionCount correct. A short label can appear INSIDE a longer one
// (`no` inside `no coarser cell`, `single` inside `single composite`), and if
// the short label were matched first it would claim cells that in truth belong
// to the longer label, and the leftover fragment of the longer label would then
// look like a second, adjacent hit: a collision counted where the art shows one
// label. Matching longest first and marking the columns each hit consumes means
// the inner match is skipped, because its opening column is already taken.
func mermaidEdgeLabels(source string) []string {
	seen := make(map[string]bool)
	var labels []string
	for _, m := range mermaidPipedEdgeLabel.FindAllStringSubmatch(source, -1) {
		label := strings.TrimSpace(m[1])
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		labels = append(labels, label)
	}
	sort.SliceStable(labels, func(i, j int) bool {
		return len([]rune(labels[i])) > len([]rune(labels[j]))
	})
	return labels
}

// mermaidLabelSpaceClass is what a space inside an edge label is matched by in
// rendered art: any single rune that is neither a letter nor a digit.
//
// It has to be a class rather than a literal because the same pattern must
// find the label whichever side of the no-break-space substitution the
// caller's source sits on, and must find it even when a space HAS bled
// through: the source handed to the detector already carries no-break spaces
// (mermaidNBSPSubstitute ran during normalization) while a rendered row may
// carry a no-break space, a plain space, or the `─` or `│` of whatever the
// vendored layer merge let through at that column. See mdpreview_nbsp.go for
// the bleed itself.
//
// It excludes letters and digits rather than being a plain `.` wildcard so
// `a b` cannot match `aXb` — a two-word label would otherwise match a run of
// unrelated node text that merely happens to have the same letters in the
// same places.
const mermaidLabelSpaceClass = `[^\p{L}\p{N}]`

// mermaidLabelPattern compiles one edge label into a pattern that matches that
// label as it appears in rendered art. Every space-like rune in the label
// becomes mermaidLabelSpaceClass; everything else is matched literally.
func mermaidLabelPattern(label string) (*regexp.Regexp, error) {
	quoted := regexp.QuoteMeta(label)
	quoted = strings.ReplaceAll(quoted, " ", mermaidLabelSpaceClass)
	quoted = strings.ReplaceAll(quoted, mermaidNBSP, mermaidLabelSpaceClass)
	pattern, err := regexp.Compile(quoted)
	if err != nil {
		return nil, fmt.Errorf("compile edge-label pattern %q: %w", label, err)
	}
	return pattern, nil
}

// mermaidLabelMatcher pairs one edge label with the pattern that finds it in
// rendered art. They travel together because the overwrite rule needs the
// label TEXT of the other labels, not only their patterns — see
// mermaidRunIsLabelFragment.
type mermaidLabelMatcher struct {
	label   string
	pattern *regexp.Regexp
}

// mermaidLabelHit is one label's placement on one row of rendered art, in rune
// columns. index identifies WHICH label matched, so two hits of the same label
// on one row can be told apart from two different labels sitting side by side.
type mermaidLabelHit struct {
	start int
	end   int
	index int
}

// mermaidCollisionCount reports how many edge labels the art places where the
// reader cannot tell which arrow they belong to. Both shapes described in the
// file doc comment count: two surviving labels crammed into one corridor, and
// one label painted over another so only wreckage is left.
//
// Zero means every label is readable — or that there was nothing to collide:
// fewer than two distinct labels, or empty art. A fence that counts zero is
// left exactly as rendered.
//
// Two hits of the SAME label on one row are not a collision. A label repeated
// on two arrows is drawn twice on purpose, and reading either occurrence gives
// the reader the right word; what makes a collision unreadable is two different
// words with no way to split them.
//
// The scan is per row, and within a row it is longest label first with the
// columns of each accepted hit marked as consumed (see mermaidEdgeLabels for
// why that ordering is load-bearing). A match that overlaps a claimed span, or
// that butts against surrounding node text, is not a hit at all — see
// mermaidRowCollisions. Surviving hits are then sorted by column and each
// adjacent pair tested.
func mermaidCollisionCount(source, art string) int {
	labels := mermaidEdgeLabels(source)
	if len(labels) < 2 {
		return 0
	}

	matchers := make([]mermaidLabelMatcher, 0, len(labels))
	for _, label := range labels {
		pattern, err := mermaidLabelPattern(label)
		if err != nil {
			// QuoteMeta output is always a valid pattern, so this is
			// unreachable; declining the label rather than the whole count
			// keeps a hypothetical bad label from hiding real collisions.
			continue
		}
		matchers = append(matchers, mermaidLabelMatcher{label: label, pattern: pattern})
	}
	if len(matchers) < 2 {
		return 0
	}

	total := 0
	for row := range strings.SplitSeq(art, "\n") {
		total += mermaidRowCollisions(row, matchers)
	}
	return total
}

// mermaidArtWidth is the width of rendered art in display cells: its widest
// row, with trailing spaces stripped first. The vendored renderer pads every
// row out to the drawing's full extent, and that padding is not content the
// reader has to pan to see.
func mermaidArtWidth(art string) int {
	widest := 0
	for row := range strings.SplitSeq(art, "\n") {
		if n := xansi.StringWidth(strings.TrimRight(row, " ")); n > widest {
			widest = n
		}
	}
	return widest
}

// mermaidHeaderDirectionPattern matches a flowchart/graph header's direction
// keyword on its own line, capturing the text before and after it separately
// so mermaidFlipDirectionToLR can swap only the keyword and leave everything
// else on the line — including a trailing title or accessibility text —
// untouched.
var mermaidHeaderDirectionPattern = regexp.MustCompile(`^(\s*(?:flowchart|graph)\s+)(TD|TB|BT|RL|LR)\b(.*)$`)

// mermaidHeaderNoDirectionPattern matches a header that names the diagram kind
// and stops there — `flowchart` or `graph` with nothing after it. The vendored
// renderer lays such a fence out top-down, exactly like an explicit `TD`, so
// it collides the same way and is worth the same retry; the flip inserts the
// keyword the author left out rather than replacing one.
//
// Anything else after the kind (a stray word, a malformed direction like
// `TDX`) does NOT match, because rewriting a header we do not understand is a
// bigger change than declining to retry.
var mermaidHeaderNoDirectionPattern = regexp.MustCompile(`^(\s*(?:flowchart|graph))[ \t]*$`)

// mermaidFlipDirectionToLR rewrites a flowchart or graph header so it reads LR
// and reports true, or reports false and an unusable source when the flip
// cannot be done safely:
//
//   - the diagram's header line (the first line that is not blank and not a
//     `%%` comment, matching how mermaidDiagramKind finds it) is neither
//     `flowchart DIRECTION` / `graph DIRECTION` nor a bare `flowchart` /
//     `graph` — a missing or malformed header
//   - the direction present is not TD or TB — LR is already the flip
//     target, RL and BT are not what the retry is for, and flipping either
//     could change the diagram in ways beyond fixing a collision
//
// A header with no direction keyword at all is flipped by inserting `LR`,
// since the renderer's own default for it is top-down.
//
// This is the only lever renderMermaidSource's retry pulls — see the plan's
// "Why the flip is to LR and not something cleverer" for why padding and
// reordering were tried first and rejected.
func mermaidFlipDirectionToLR(source string) (string, bool) {
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		header, ok := mermaidFlipHeaderLine(line)
		if !ok {
			return "", false
		}
		flipped := make([]string, len(lines))
		copy(flipped, lines)
		flipped[i] = header
		return strings.Join(flipped, "\n"), true
	}
	return "", false
}

// mermaidFlipHeaderLine rewrites one header line so it reads LR, or reports
// false when that line is not a header this retry is allowed to touch. Any
// trailing content after the direction keyword is carried over untouched.
func mermaidFlipHeaderLine(line string) (string, bool) {
	if m := mermaidHeaderDirectionPattern.FindStringSubmatch(line); m != nil {
		if m[2] != "TD" && m[2] != "TB" {
			return "", false
		}
		return m[1] + "LR" + m[3], true
	}
	if m := mermaidHeaderNoDirectionPattern.FindStringSubmatch(line); m != nil {
		return m[1] + " LR", true
	}
	return "", false
}

// mermaidRetryLRIfColliding decides whether a second, LR-flipped render
// should replace the first, and performs that decision. render is the
// second render call, injected so the decision is testable without the
// real vendored renderer — production passes mermaidcmd.RenderDiagram.
// paneWidth is the diff pane's current width, or mermaidUnconstrainedWidth
// when there is no constraint to respect.
//
// The flip is kept only when ALL of these hold, checked in the order that
// makes each one a cheap short-circuit before the next:
//
//  1. toRender has a header that mermaidFlipDirectionToLR can flip. This is
//     tested FIRST because it is a single regexp against one line, while the
//     collision count scans every row of the art against every label — and
//     because it is what keeps the piped-label scan away from sources where
//     `|...|` is not an edge label at all (sequenceDiagram, journey, and
//     anything else that never reaches a flowchart header)
//  2. the first render (toRender/rendered) collides at least once — a
//     fence with no collisions never renders twice
//  3. the flipped render succeeds and is non-blank
//  4. the flip does not push a diagram that fitted the pane off the side of
//     it — see mermaidFlipFitsPane
//  5. the flipped render collides STRICTLY FEWER times than the first —
//     a flip that trades one collision for another is not an improvement
//
// Any failure of the above returns rendered unchanged, so every failure mode
// degrades to today's output. A panic inside the injected render is caught
// here for the same reason: the whole point of the retry is to improve on a
// render we already have in hand, and letting the panic out would lose it to
// renderMermaidBlock's outer recover, whose fallback is the fence's verbatim
// source text. The vendored renderer is known to panic on some sources — see
// TestNormalizeFlowchartSource_ClassDefWithoutAColon_NoLongerKillsTheFence —
// and stackFlowchartSubgraphs guards its own extra render the same way.
func mermaidRetryLRIfColliding(toRender, rendered string, paneWidth int, render func(string) (string, error)) (result string) {
	defer func() {
		if r := recover(); r != nil {
			result = rendered
		}
	}()

	flippedSource, ok := mermaidFlipDirectionToLR(toRender)
	if !ok {
		return rendered
	}

	firstCount := mermaidCollisionCount(toRender, rendered)
	if firstCount == 0 {
		return rendered
	}

	flippedRender, err := render(flippedSource)
	if err != nil || strings.TrimSpace(flippedRender) == "" {
		return rendered
	}

	if !mermaidFlipFitsPane(rendered, flippedRender, paneWidth) {
		return rendered
	}

	flippedCount := mermaidCollisionCount(flippedSource, flippedRender)
	if flippedCount >= firstCount {
		return rendered
	}
	return flippedRender
}

// mermaidFlipFitsPane reports whether the LR flip is allowed to replace the
// first render on width grounds. It objects to exactly one trade: a first
// render that fits the pane being replaced by a flipped render that does not.
//
// Flipping to LR makes the art wider, often several times wider — measured on
// the corpus, one 80-column diagram flipped to 273. When the first render
// already overflows the pane the reader is panning either way, so a wider
// flip costs them nothing they were not already paying and the fix is worth
// it. When the first render fits on one screen, taking that away is a real
// loss, and it is not worth paying for a collision that is at worst crowded
// rather than destroyed.
//
// No pane width to respect (mermaidUnconstrainedWidth, or any non-positive
// width) means no width objection — not a pane of width zero that nothing
// fits.
func mermaidFlipFitsPane(rendered, flipped string, paneWidth int) bool {
	if paneWidth <= 0 {
		return true
	}
	if mermaidArtWidth(rendered) > paneWidth {
		return true
	}
	return mermaidArtWidth(flipped) <= paneWidth
}

// mermaidRowCollisions counts collisions on a single row of art. matchers must
// already be ordered longest label first.
//
// The scan is two passes, and both exist to stop a run of letters that merely
// SPELLS a label from being read as one — node-box text shares the art with
// the labels, and short labels like `no`, `yes` or `open` sit inside ordinary
// words such as `nothing`, `yesterday` and `reopen`:
//
//  1. claim: longest label first, a match is taken only when EVERY column it
//     covers is still free, not merely its opening column. A shorter label
//     whose match starts before an already-claimed span and runs into it
//     would otherwise be taken, reporting one contiguous run of text as two
//     crammed labels.
//  2. classify: a claimed match reads as a word of its own, as the wreckage
//     of an overwrite, or as a fragment of node text — see
//     mermaidClassifyHit.
//
// The count is then the overwrites found in pass 2 plus, over the surviving
// hits sorted by column, each adjacent pair of DISTINCT labels closer together
// than mermaidCollisionLimit allows.
func mermaidRowCollisions(row string, matchers []mermaidLabelMatcher) int {
	runes := []rune(row)
	if len(runes) == 0 {
		return 0
	}
	// byteToRune maps a byte offset in row to its rune column, so a match
	// reported in bytes can be compared against the rune-indexed consumed
	// slice. Offsets inside a multi-byte rune keep the -1 sentinel and are
	// skipped. Building it once per row keeps the scan linear in the row
	// length instead of re-slicing the prefix for every match.
	byteToRune := make([]int, len(row)+1)
	for i := range byteToRune {
		byteToRune[i] = -1
	}
	col := 0
	for offset := range row {
		byteToRune[offset] = col
		col++
	}
	byteToRune[len(row)] = col

	claimed := make([]bool, len(runes))
	var hits []mermaidLabelHit
	for i, matcher := range matchers {
		for _, loc := range matcher.pattern.FindAllStringIndex(row, -1) {
			start, end := byteToRune[loc[0]], byteToRune[loc[1]]
			if start < 0 || end < 0 || !mermaidSpanFree(claimed, start, end) {
				continue
			}
			for k := start; k < end && k < len(claimed); k++ {
				claimed[k] = true
			}
			hits = append(hits, mermaidLabelHit{start: start, end: end, index: i})
		}
	}

	count := 0
	kept := make([]mermaidLabelHit, 0, len(hits))
	for _, hit := range hits {
		switch mermaidClassifyHit(runes, claimed, matchers, hit) {
		case mermaidHitWord:
			kept = append(kept, hit)
		case mermaidHitOverwritten:
			count++
		case mermaidHitNoise:
		}
	}
	if len(kept) < 2 {
		return count
	}
	sort.SliceStable(kept, func(a, b int) bool { return kept[a].start < kept[b].start })

	for i := 1; i < len(kept); i++ {
		prev, cur := kept[i-1], kept[i]
		if cur.index == prev.index {
			continue
		}
		if cur.start-prev.end < mermaidCollisionLimit(prev, cur) {
			count++
		}
	}
	return count
}

// mermaidCollisionLimit is how few columns apart two surviving labels have to
// be before they read as one crammed run rather than as two separate words: the
// smaller of either label's own length and mermaidCollisionGap. See that
// constant for why length is in the formula at all.
func mermaidCollisionLimit(prev, cur mermaidLabelHit) int {
	limit := mermaidCollisionGap
	if n := prev.end - prev.start; n < limit {
		limit = n
	}
	if n := cur.end - cur.start; n < limit {
		limit = n
	}
	return limit
}

// mermaidSpanFree reports whether every column of [start, end) is still
// unclaimed by an earlier, longer label's match.
func mermaidSpanFree(claimed []bool, start, end int) bool {
	for k := start; k < end && k < len(claimed); k++ {
		if claimed[k] {
			return false
		}
	}
	return true
}

// mermaidHitVerdict is what one claimed match on a row turned out to be.
type mermaidHitVerdict int

const (
	// mermaidHitWord is a label drawn as a word of its own: nothing but
	// connectors, blanks or another label's cells touch it. Only these take
	// part in the adjacency test.
	mermaidHitWord mermaidHitVerdict = iota
	// mermaidHitOverwritten is a label with the leftovers of a DIFFERENT
	// label stuck to it, which is what one label painted over another looks
	// like from the outside. A collision in its own right.
	mermaidHitOverwritten
	// mermaidHitNoise is a run of node text that happens to spell a label —
	// `no` inside `nothing`. Not a hit at all.
	mermaidHitNoise
)

// mermaidClassifyHit decides which of the three a claimed match is, by looking
// at the letters immediately outside it that no label claimed:
//
//   - nothing outside it (a connector, a blank, the row's end) or only cells
//     another label's match covers: a word of its own. The second half of that
//     is what keeps the two-labels-drawn-flush shape, where each label's
//     neighbor IS the other label.
//   - leftover letters that spell part of one of the OTHER labels: an
//     overwrite. `collection` painted over `single composite` leaves
//     `sincollectionite`, and `sin` and `ite` are pieces of `single composite`
//     that no match could claim because the label they came from no longer
//     exists as a word.
//   - leftover letters belonging to no label: node text, so the match is a
//     coincidence — the `thing` after `no` in `nothing`.
func mermaidClassifyHit(runes []rune, claimed []bool, matchers []mermaidLabelMatcher, hit mermaidLabelHit) mermaidHitVerdict {
	before := mermaidNeighborRun(runes, claimed, hit.start, -1)
	after := mermaidNeighborRun(runes, claimed, hit.end, 1)
	if before == "" && after == "" {
		return mermaidHitWord
	}
	if mermaidRunIsLabelFragment(before, matchers, hit.index) || mermaidRunIsLabelFragment(after, matchers, hit.index) {
		return mermaidHitOverwritten
	}
	return mermaidHitNoise
}

// mermaidNeighborRun returns the unclaimed letters and digits that run away
// from a match's edge, in reading order. step is -1 for the run ending just
// before start, +1 for the run beginning at end. An empty result means the
// match's neighbor on that side is a connector, a blank, the row's end, or a
// cell some other label's match already claimed.
func mermaidNeighborRun(runes []rune, claimed []bool, edge, step int) string {
	first := edge
	if step < 0 {
		first = edge - 1
	}
	var run []rune
	for i := first; i >= 0 && i < len(runes); i += step {
		if !mermaidWordRune(runes[i]) || claimed[i] {
			break
		}
		run = append(run, runes[i])
	}
	if step < 0 {
		for l, r := 0, len(run)-1; l < r; l, r = l+1, r-1 {
			run[l], run[r] = run[r], run[l]
		}
	}
	return string(run)
}

// mermaidRunIsLabelFragment reports whether a run of leftover letters is a
// piece of some label other than the one that just matched — the evidence that
// the run is the wreckage of an overwritten label rather than ordinary node
// text. Runs shorter than mermaidLabelFragmentMinRunes never qualify: one or
// two letters turn up inside almost any word by chance.
func mermaidRunIsLabelFragment(run string, matchers []mermaidLabelMatcher, self int) bool {
	if len([]rune(run)) < mermaidLabelFragmentMinRunes {
		return false
	}
	for i, matcher := range matchers {
		if i == self {
			continue
		}
		if strings.Contains(matcher.label, run) {
			return true
		}
	}
	return false
}

// mermaidWordRune reports whether r can continue a word, which is what makes
// a match of a short label inside a longer run of text a false hit.
func mermaidWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
