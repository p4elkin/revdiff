package ui

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
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
// why that ordering is load-bearing). A match that overlaps a claimed span,
// or that butts against surrounding node text, is not a hit at all — see
// mermaidRowCollisions. Hits are then sorted by column and each adjacent pair
// tested.
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
//  4. the flipped render collides STRICTLY FEWER times than the first —
//     a flip that trades one collision for another is not an improvement
//
// Any failure of the above returns rendered unchanged, so every failure mode
// degrades to today's output.
func mermaidRetryLRIfColliding(toRender, rendered string, render func(string) (string, error)) string {
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

	flippedCount := mermaidCollisionCount(flippedSource, flippedRender)
	if flippedCount >= firstCount {
		return rendered
	}
	return flippedRender
}

// mermaidRowCollisions counts collisions on a single row of art. patterns must
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
//  2. delimit: a claimed match is then dropped when it butts against a letter
//     or digit that no claimed match covers. Checking against the claims of
//     the whole first pass rather than one match at a time is what keeps the
//     worst collision shape — two labels drawn flush together, each one's
//     neighbor being the other — while still rejecting `no` inside
//     `nothing`, whose neighboring `t` belongs to no label at all.
func mermaidRowCollisions(row string, patterns []*regexp.Regexp) int {
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
	for i, pattern := range patterns {
		for _, loc := range pattern.FindAllStringIndex(row, -1) {
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

	kept := make([]mermaidLabelHit, 0, len(hits))
	for _, hit := range hits {
		if mermaidHitDelimited(runes, claimed, hit.start, hit.end) {
			kept = append(kept, hit)
		}
	}
	if len(kept) < 2 {
		return 0
	}
	sort.SliceStable(kept, func(a, b int) bool { return kept[a].start < kept[b].start })

	count := 0
	for i := 1; i < len(kept); i++ {
		prev, cur := kept[i-1], kept[i]
		if cur.index == prev.index {
			continue
		}
		if cur.start-prev.end < mermaidCollisionGap {
			count++
		}
	}
	return count
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

// mermaidHitDelimited reports whether a match at [start, end) reads as a word
// of its own rather than as a fragment of surrounding node text. A letter or
// digit immediately outside the match disqualifies it, unless that neighbor
// is covered by some other label's match — see mermaidRowCollisions for why
// that exception is what still catches two labels drawn flush together.
func mermaidHitDelimited(runes []rune, claimed []bool, start, end int) bool {
	if start > 0 && mermaidWordRune(runes[start-1]) && !claimed[start-1] {
		return false
	}
	if end < len(runes) && mermaidWordRune(runes[end]) && !claimed[end] {
		return false
	}
	return true
}

// mermaidWordRune reports whether r can continue a word, which is what makes
// a match of a short label inside a longer run of text a false hit.
func mermaidWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
