package ui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
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
// labeled out-edges therefore puts two labels on the same output row, butted up
// against each other, and the reader cannot tell which arrow either belongs to:
//
//	│   What kind of property is it?   ├◄───collection────single composite──────┤
//
// `collection` and `single composite` there belong to two different arrows.
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

// mermaidCollisionGap is how many columns must separate two DISTINCT edge
// labels on one row before they are read as belonging to different parts of
// the picture rather than as two labels crammed into one corridor.
//
// The number separates the two real cases seen in rendered art. Two labels
// that collided on one arrow corridor sit flush against each other or with a
// connector rune or two between them — a gap of 0 to about 4. Two labels that
// merely happen to share a row in different regions of a wide diagram are
// separated by a node box, which is never narrower than its own padding plus
// borders. On the measured corpus 8 flags every real collision with no false
// positive, and the LR render of each flagged fence comes back at zero.
const mermaidCollisionGap = 8

// mermaidPipedEdgeLabel captures the text inside an `-->|label|` edge-label
// suffix, with the surrounding quotes of a `-->|"label"|` optional.
//
// Only the piped spelling is matched, and that is enough: normalizeFlowchartLine
// rewrites the inline `A -- label --> B` form into the piped one before any of
// this runs (see normalizeFlowchartEdgeLabel, which is the single chokepoint
// for both spellings), and the transpiled class/state paths emit the piped form
// directly. The detector is only ever handed source that has already been
// through that normalization.
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

// mermaidLabelPattern compiles one edge label into a pattern that matches that
// label as it appears in rendered art.
//
// Every space-like rune in the label becomes `.` — one wildcard rune — rather
// than being matched literally, so the same pattern finds the label whichever
// side of the no-break-space substitution the caller's source sits on, and
// finds it even when a space HAS bled through. Both matter: the source handed
// to the detector already carries no-break spaces (mermaidNBSPSubstitute ran
// during normalization) while a rendered row may carry a no-break space, a
// plain space, or the `─` or `│` of whatever the vendored layer merge let
// through at that column. See mdpreview_nbsp.go for the bleed itself.
func mermaidLabelPattern(label string) (*regexp.Regexp, error) {
	quoted := regexp.QuoteMeta(label)
	quoted = strings.ReplaceAll(quoted, " ", ".")
	quoted = strings.ReplaceAll(quoted, mermaidNBSP, ".")
	pattern, err := regexp.Compile(quoted)
	if err != nil {
		return nil, fmt.Errorf("compile edge-label pattern %q: %w", label, err)
	}
	return pattern, nil
}

// mermaidLabelHit is one label's placement on one row of rendered art, in rune
// columns. index identifies WHICH label matched, so two hits of the same label
// on one row can be told apart from two different labels sitting side by side.
type mermaidLabelHit struct {
	start int
	end   int
	index int
}

// mermaidCollisionCount reports how many times two distinct edge labels from
// source land within mermaidCollisionGap columns of each other on one row of
// art.
//
// Zero means the art places every label where the reader can tell which arrow
// it belongs to — or that there was nothing to collide: fewer than two distinct
// labels, or empty art. A fence that counts zero is left exactly as rendered.
//
// Two hits of the SAME label on one row are not a collision. A label repeated
// on two arrows is drawn twice on purpose, and reading either occurrence gives
// the reader the right word; what makes a collision unreadable is two different
// words butted together with no way to split them.
//
// The scan is per row, and within a row it is longest label first with the
// columns of each accepted hit marked as consumed (see mermaidEdgeLabels for
// why that ordering is load-bearing). Hits are then sorted by column and each
// adjacent pair tested.
func mermaidCollisionCount(source, art string) int {
	labels := mermaidEdgeLabels(source)
	if len(labels) < 2 {
		return 0
	}

	patterns := make([]*regexp.Regexp, 0, len(labels))
	for _, label := range labels {
		pattern, err := mermaidLabelPattern(label)
		if err != nil {
			// QuoteMeta output is always a valid pattern, so this is
			// unreachable; declining the label rather than the whole count
			// keeps a hypothetical bad label from hiding real collisions.
			continue
		}
		patterns = append(patterns, pattern)
	}
	if len(patterns) < 2 {
		return 0
	}

	total := 0
	for row := range strings.SplitSeq(art, "\n") {
		total += mermaidRowCollisions(row, patterns)
	}
	return total
}

// mermaidRowCollisions counts collisions on a single row of art. patterns must
// already be ordered longest label first.
func mermaidRowCollisions(row string, patterns []*regexp.Regexp) int {
	runes := []rune(row)
	if len(runes) == 0 {
		return 0
	}
	// byteToRune maps a byte offset in row to its rune column, so a match
	// reported in bytes can be compared against the rune-indexed consumed
	// map. Building it once per row keeps the scan linear in the row length
	// instead of re-slicing the prefix for every match.
	byteToRune := make(map[int]int, len(runes)+1)
	col := 0
	for offset := range row {
		byteToRune[offset] = col
		col++
	}
	byteToRune[len(row)] = col

	consumed := make([]bool, len(runes))
	var hits []mermaidLabelHit
	for i, pattern := range patterns {
		for _, loc := range pattern.FindAllStringIndex(row, -1) {
			start, ok := byteToRune[loc[0]]
			if !ok {
				continue
			}
			end, ok := byteToRune[loc[1]]
			if !ok {
				continue
			}
			if start < len(consumed) && consumed[start] {
				continue
			}
			for k := start; k < end && k < len(consumed); k++ {
				consumed[k] = true
			}
			hits = append(hits, mermaidLabelHit{start: start, end: end, index: i})
		}
	}
	if len(hits) < 2 {
		return 0
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].start < hits[b].start })

	count := 0
	for i := 1; i < len(hits); i++ {
		prev, cur := hits[i-1], hits[i]
		if cur.index == prev.index {
			continue
		}
		if cur.start-prev.end < mermaidCollisionGap {
			count++
		}
	}
	return count
}
