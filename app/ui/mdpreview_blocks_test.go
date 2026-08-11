package ui

import (
	"strings"
	"testing"
	"time"
)

// findTarget returns the first target of kind k, failing the test if none
// exists.
func findTarget(t *testing.T, targets []mdPreviewBlockTarget, k mdPreviewBlockKind) mdPreviewBlockTarget {
	t.Helper()
	for _, tg := range targets {
		if tg.kind == k {
			return tg
		}
	}
	t.Fatalf("no target of kind %q in %+v", k, targets)
	return mdPreviewBlockTarget{}
}

// countKind returns how many targets of kind k are present.
func countKind(targets []mdPreviewBlockTarget, k mdPreviewBlockKind) int {
	n := 0
	for _, tg := range targets {
		if tg.kind == k {
			n++
		}
	}
	return n
}

// assertNonOverlappingOrdered fails the test unless targets are in
// document order (non-decreasing StartLine) and no two targets share a
// source line — the invariant mdPreviewBlockTargets must uphold for every
// document (see the plan's "targets are non-overlapping and in document
// order" checklist item).
func assertNonOverlappingOrdered(t *testing.T, targets []mdPreviewBlockTarget) {
	t.Helper()
	for i, tg := range targets {
		if tg.startLine > tg.endLine {
			t.Errorf("target %d (%s): StartLine %d > EndLine %d", i, tg.kind, tg.startLine, tg.endLine)
		}
		if i == 0 {
			continue
		}
		prev := targets[i-1]
		if tg.startLine < prev.startLine {
			t.Errorf("target %d (%s) starts at line %d, before target %d (%s) at line %d — not in document order",
				i, tg.kind, tg.startLine, i-1, prev.kind, prev.startLine)
		}
		if tg.startLine <= prev.endLine {
			t.Errorf("target %d (%s) [%d,%d] overlaps target %d (%s) [%d,%d]",
				i, tg.kind, tg.startLine, tg.endLine, i-1, prev.kind, prev.startLine, prev.endLine)
		}
	}
}

func TestMdPreviewBlocks_HeadingsAllLevels(t *testing.T) {
	doc := "# one\n\n## two\n\n### three\n\n#### four\n\n##### five\n\n###### six\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)

	want := []struct {
		kind mdPreviewBlockKind
		line int
	}{
		{mdBlockH1, 1}, {mdBlockH2, 3}, {mdBlockH3, 5}, {mdBlockH4, 7}, {mdBlockH5, 9}, {mdBlockH6, 11},
	}
	if len(targets) != len(want) {
		t.Fatalf("got %d targets, want %d: %+v", len(targets), len(want), targets)
	}
	for i, w := range want {
		if targets[i].kind != w.kind || targets[i].startLine != w.line || targets[i].endLine != w.line {
			t.Errorf("target %d = %+v, want kind=%s line=%d", i, targets[i], w.kind, w.line)
		}
	}
}

func TestMdPreviewBlocks_NoGenericHeadingKind(t *testing.T) {
	doc := "# a heading\n\nbody\n"
	targets := mdPreviewBlockTargets(doc)
	for _, tg := range targets {
		if tg.kind == "heading" {
			t.Fatalf("got a generic %q kind, want only level-specific h1..h6: %+v", tg.kind, targets)
		}
	}
}

func TestMdPreviewBlocks_Paragraph(t *testing.T) {
	doc := "first paragraph\n\nsecond paragraph\nstill second\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2: %+v", len(targets), targets)
	}
	if targets[0] != (mdPreviewBlockTarget{kind: mdBlockParagraph, startLine: 1, endLine: 1}) {
		t.Errorf("first paragraph = %+v", targets[0])
	}
	if targets[1] != (mdPreviewBlockTarget{kind: mdBlockParagraph, startLine: 3, endLine: 4}) {
		t.Errorf("second paragraph = %+v", targets[1])
	}
}

func TestMdPreviewBlocks_TightList(t *testing.T) {
	doc := "- one\n- two\n- three\n- four\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	if got := countKind(targets, mdBlockItem); got != 4 {
		t.Fatalf("got %d item targets, want 4: %+v", got, targets)
	}
	wantLines := []int{1, 2, 3, 4}
	for i, tg := range targets {
		if tg.kind != mdBlockItem || tg.startLine != wantLines[i] || tg.endLine != wantLines[i] {
			t.Errorf("target %d = %+v, want item at line %d", i, tg, wantLines[i])
		}
	}
}

func TestMdPreviewBlocks_LooseList(t *testing.T) {
	doc := "- one\n\n- two\n\n- three\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	if got := countKind(targets, mdBlockItem); got != 3 {
		t.Fatalf("got %d item targets, want 3: %+v", got, targets)
	}
	wantLines := []int{1, 3, 5}
	for i, tg := range targets {
		if tg.startLine != wantLines[i] || tg.endLine != wantLines[i] {
			t.Errorf("target %d = %+v, want line %d", i, tg, wantLines[i])
		}
	}
}

func TestMdPreviewBlocks_NestedList(t *testing.T) {
	doc := "- outer one\n  - inner one a\n  - inner one b\n    - deepest\n- outer two\n  - inner two a\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	if got := countKind(targets, mdBlockItem); got != 6 {
		t.Fatalf("got %d item targets, want 6 (2 outer + 3 inner + 1 deepest): %+v", got, targets)
	}
	// the outer item's own span must be exactly its own line, not extended
	// down into the nested sublist (see swallowedSpan).
	outerOne := targets[0]
	if outerOne.startLine != 1 || outerOne.endLine != 1 {
		t.Errorf("outer item one = %+v, want span [1,1] (must not swallow the nested sublist)", outerOne)
	}
	wantLines := []int{1, 2, 3, 4, 5, 6}
	for i, tg := range targets {
		if tg.startLine != wantLines[i] || tg.endLine != wantLines[i] {
			t.Errorf("target %d = %+v, want line %d", i, tg, wantLines[i])
		}
	}
}

func TestMdPreviewBlocks_OrderedList(t *testing.T) {
	doc := "1. first\n2. second\n3. third\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	if got := countKind(targets, mdBlockEnum); got != 3 {
		t.Fatalf("got %d enumeration targets, want 3: %+v", got, targets)
	}
	if got := countKind(targets, mdBlockItem); got != 0 {
		t.Errorf("got %d item targets for an ordered list, want 0: %+v", got, targets)
	}
}

func TestMdPreviewBlocks_Table(t *testing.T) {
	doc := "before\n\n| a | b |\n| --- | --- |\n| one | two |\n| three | four |\n\nafter\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	if got := countKind(targets, mdBlockTable); got != 1 {
		t.Fatalf("got %d table targets, want exactly 1 (a table is one target, not one per row): %+v", got, targets)
	}
	tbl := findTarget(t, targets, mdBlockTable)
	if tbl.startLine != 3 {
		t.Errorf("table StartLine = %d, want 3 (the header row)", tbl.startLine)
	}
	if tbl.endLine != 6 {
		t.Errorf("table EndLine = %d, want 6 (the last data row)", tbl.endLine)
	}
	// before/after paragraphs must survive as their own, non-overlapping targets.
	if got := countKind(targets, mdBlockParagraph); got != 2 {
		t.Errorf("got %d paragraph targets around the table, want 2: %+v", got, targets)
	}
}

func TestMdPreviewBlocks_FencedCode(t *testing.T) {
	doc := "before\n\n```go\nfunc main() {\n\tfmt.Println(1)\n}\n```\n\nafter\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	cb := findTarget(t, targets, mdBlockCodeBlock)
	// goldmark's FencedCodeBlock.Lines() covers only the body, not the
	// opening/closing fence lines themselves (verified empirically before
	// writing this test) — so the span is the body's own line range.
	if cb.startLine != 4 || cb.endLine != 6 {
		t.Errorf("code block span = [%d,%d], want [4,6] (the body, excluding both fence lines)", cb.startLine, cb.endLine)
	}
}

func TestMdPreviewBlocks_Blockquote(t *testing.T) {
	doc := "before\n\n> a blockquote paragraph\n> that spans two lines\n\nafter\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	if got := countKind(targets, mdBlockQuote); got != 1 {
		t.Fatalf("got %d block_quote targets, want 1: %+v", got, targets)
	}
	if got := countKind(targets, mdBlockParagraph); got != 2 {
		t.Fatalf("got %d paragraph targets, want 2 (before/after only — the quote's own paragraph is folded): %+v",
			got, targets)
	}
	bq := findTarget(t, targets, mdBlockQuote)
	if bq.startLine != 3 || bq.endLine != 4 {
		t.Errorf("block_quote span = [%d,%d], want [3,4]", bq.startLine, bq.endLine)
	}
}

func TestMdPreviewBlocks_BlockquoteMultipleParagraphs(t *testing.T) {
	doc := "> quote para one line one\n> quote para one line two\n>\n> quote para two\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	if got := countKind(targets, mdBlockQuote); got != 1 {
		t.Fatalf("got %d block_quote targets, want 1 (one target for the whole quote): %+v", got, targets)
	}
	bq := findTarget(t, targets, mdBlockQuote)
	if bq.startLine != 1 || bq.endLine != 4 {
		t.Errorf("block_quote span = [%d,%d], want [1,4] (covers both paragraphs)", bq.startLine, bq.endLine)
	}
}

func TestMdPreviewBlocks_HorizontalRule(t *testing.T) {
	doc := "before\n\n---\n\nafter\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	hr := findTarget(t, targets, mdBlockHR)
	if hr.startLine != 3 || hr.endLine != 3 {
		t.Errorf("hr span = [%d,%d], want [3,3]", hr.startLine, hr.endLine)
	}
}

func TestMdPreviewBlocks_HorizontalRuleFirstAndLastLine(t *testing.T) {
	doc := "---\n\nonly paragraph\n\n***\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	if got := countKind(targets, mdBlockHR); got != 2 {
		t.Fatalf("got %d hr targets, want 2: %+v", got, targets)
	}
	if targets[0].kind != mdBlockHR || targets[0].startLine != 1 {
		t.Errorf("first hr = %+v, want line 1", targets[0])
	}
	last := targets[len(targets)-1]
	if last.kind != mdBlockHR || last.startLine != 5 {
		t.Errorf("last hr = %+v, want line 5", last)
	}
}

// TestMdPreviewBlocks_ConsecutiveHorizontalRules pins the case that used to
// misplace a break onto a previous block's line: a run of thematic breaks with
// nothing but blank lines between them. A ThematicBreak carries no position of
// its own, so the second break's lower-bound walk has to resolve the first
// break rather than bubbling past it (see mdBreakResolver.lowerBound). When it
// bubbled, "text\n\n---\n\n---\n\nmore\n" put the second break on line 1 —
// the paragraph's line — and two targets on one source line silently destroy
// one of two annotations.
func TestMdPreviewBlocks_ConsecutiveHorizontalRules(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []int // expected hr lines, in order
	}{
		{"two breaks after a paragraph", "text\n\n---\n\n---\n\nmore\n", []int{3, 5}},
		{"two breaks opening the document", "---\n\n---\n\ntext\n", []int{1, 3}},
		{"adjacent breaks, no blank between", "a\n\n---\n---\n\nb\n", []int{3, 4}},
		{"three in a row", "a\n\n---\n\n---\n\n---\n\nb\n", []int{3, 5, 7}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			targets := mdPreviewBlockTargets(tc.doc)
			assertNonOverlappingOrdered(t, targets)
			var got []int
			for _, tg := range targets {
				if tg.kind == mdBlockHR {
					got = append(got, tg.startLine)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d hr targets %v, want %v (all targets: %+v)", len(got), got, tc.want, targets)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("hr %d = line %d, want %d (all targets: %+v)", i, got[i], tc.want[i], targets)
				}
			}
		})
	}
}

// TestMdPreviewBlocks_HorizontalRuleAfterUnpositionedLines is the second half of
// the same family, and the one the consecutive-break fix did not cover: a break
// whose gap contains a non-blank line that is not the break. goldmark's Lines()
// on a fenced code block covers only its CONTENT, so the closing ``` sits inside
// the gap; a bare ">" continuation inside a blockquote does the same. The
// "first non-blank line in the gap" rule anchored the break to those lines, and
// annotating the rule then wrote the wrong source line into the -o output — with
// Aligned=true, so nothing degraded and nothing warned. The candidate now has to
// look like a rule (see looksLikeThematicBreak).
func TestMdPreviewBlocks_HorizontalRuleAfterUnpositionedLines(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []int
	}{
		{
			"break after a fenced code block",
			"# Plan\n\nIntro prose.\n\n```go\nfunc main() {}\n```\n\n---\n\n## Next\n\nMore prose.\n",
			[]int{9},
		},
		{
			"break after a fence with no info string",
			"a\n\n```\nx\n```\n\n---\n\nb\n",
			[]int{7},
		},
		{
			"breaks inside a blockquote",
			"> a\n>\n> ---\n>\n> ---\n",
			[]int{3, 5},
		},
		{
			"starred rule after a fence",
			"a\n\n```\nx\n```\n\n***\n\nb\n",
			[]int{7},
		},
		{
			"spaced rule after a fence",
			"a\n\n```\nx\n```\n\n- - -\n\nb\n",
			[]int{7},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			targets := mdPreviewBlockTargets(tc.doc)
			assertNonOverlappingOrdered(t, targets)
			var got []int
			for _, tg := range targets {
				if tg.kind == mdBlockHR {
					got = append(got, tg.startLine)
				}
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d hr targets %v, want %v (all targets: %+v)", len(got), got, tc.want, targets)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("hr %d = line %d, want %d (all targets: %+v)", i, got[i], tc.want[i], targets)
				}
			}
		})
	}
}

// TestLooksLikeThematicBreak pins the candidate test the gap scan now uses.
// Permissive about the prefix (a break nested in a quote or a list item carries
// that container's markers on its source line), strict about the rest — a
// closing code fence and a bare ">" are the two lines that used to be taken for
// a rule.
func TestLooksLikeThematicBreak(t *testing.T) {
	// "-- -" is a real thematic break: CommonMark counts three matching
	// characters with any spaces between them, not three adjacent ones.
	yes := []string{"---", "***", "___", "- - -", "-- -", "  ---", "---   ", "> ---", ">---", "> > ---", "    ***", "-----"}
	no := []string{"", "   ", "--", "```", ">", ">  ", "---x", "| --- |", "***bold***", "- item", "===", "* * a", "-*-"}
	for _, s := range yes {
		if !looksLikeThematicBreak(s) {
			t.Errorf("looksLikeThematicBreak(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if looksLikeThematicBreak(s) {
			t.Errorf("looksLikeThematicBreak(%q) = true, want false", s)
		}
	}
}

// TestMdPreviewBlocks_LongThematicBreakRunIsLinear guards the cost of the
// lower-bound recursion. Each break's bound resolves the break before it, so
// without the per-node memo (and with the document re-split at every level) a
// run of breaks was superlinear enough to stall the bubbletea Update goroutine:
// 800 breaks measured in seconds. The assertion is deliberately loose — it is
// there to catch a return to that shape, not to pin a number.
func TestMdPreviewBlocks_LongThematicBreakRunIsLinear(t *testing.T) {
	const n = 800
	doc := "text\n\n" + strings.Repeat("---\n\n", n) + "more\n"

	start := time.Now()
	targets := mdPreviewBlockTargets(doc)
	elapsed := time.Since(start)

	hrs := 0
	for _, tg := range targets {
		if tg.kind == mdBlockHR {
			hrs++
		}
	}
	if hrs != n {
		t.Fatalf("got %d hr targets, want %d", hrs, n)
	}
	assertNonOverlappingOrdered(t, targets)
	if elapsed > time.Second {
		t.Errorf("%d consecutive thematic breaks took %v — the per-node memo is gone", n, elapsed)
	}
}

func TestMdPreviewBlocks_HTMLBlock(t *testing.T) {
	doc := "before\n\n<div class=\"x\">\n  <p>hello</p>\n</div>\n\nafter\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	html := findTarget(t, targets, mdBlockHTMLBlock)
	if html.startLine != 3 || html.endLine != 5 {
		t.Errorf("html_block span = [%d,%d], want [3,5]", html.startLine, html.endLine)
	}
}

func TestMdPreviewBlocks_EmptyDocument(t *testing.T) {
	if targets := mdPreviewBlockTargets(""); len(targets) != 0 {
		t.Fatalf("empty document: got %d targets, want 0: %+v", len(targets), targets)
	}
}

func TestMdPreviewBlocks_DocumentOfOnlyATable(t *testing.T) {
	doc := "| a | b |\n| --- | --- |\n| 1 | 2 |\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1: %+v", len(targets), targets)
	}
	if targets[0].kind != mdBlockTable || targets[0].startLine != 1 || targets[0].endLine != 3 {
		t.Errorf("table target = %+v, want kind=table span=[1,3]", targets[0])
	}
}

// TestMdPreviewBlocks_MixedDocument mirrors the plan's "a document mixing
// them" requirement: every tracked kind in one document, checked for the
// non-overlapping/document-order invariant plus a spot-check on counts.
func TestMdPreviewBlocks_MixedDocument(t *testing.T) {
	doc := "# Title\n\nIntro paragraph.\n\n## Section\n\n" +
		"- tight one\n- tight two\n\n" +
		"1. ord one\n2. ord two\n\n" +
		"> a quoted paragraph\n\n" +
		"```go\ncode line\n```\n\n" +
		"| a | b |\n| --- | --- |\n| 1 | 2 |\n\n" +
		"---\n\n" +
		"<div>raw</div>\n\n" +
		"Final paragraph.\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)

	wantCounts := map[mdPreviewBlockKind]int{
		mdBlockH1: 1, mdBlockH2: 1, mdBlockParagraph: 2, mdBlockItem: 2, mdBlockEnum: 2,
		mdBlockQuote: 1, mdBlockCodeBlock: 1, mdBlockTable: 1, mdBlockHR: 1, mdBlockHTMLBlock: 1,
	}
	for kind, want := range wantCounts {
		if got := countKind(targets, kind); got != want {
			t.Errorf("count of %s = %d, want %d: %+v", kind, got, want, targets)
		}
	}
}

// --- folding rules (checklist: "a task item resolves to its list item; a
// table yields exactly one target") ---

func TestMdPreviewBlocks_TaskItemFoldsIntoListItem(t *testing.T) {
	doc := "- [x] done task\n- [ ] pending task\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2 (one per task item, no separate task kind): %+v", len(targets), targets)
	}
	for i, tg := range targets {
		if tg.kind != mdBlockItem {
			t.Errorf("target %d = %+v, want kind=item (task items fold into their enclosing list item)", i, tg)
		}
	}
	if targets[0].startLine != 1 || targets[1].startLine != 2 {
		t.Errorf("task item lines = [%d,%d], want [1,2]", targets[0].startLine, targets[1].startLine)
	}
}

func TestMdPreviewBlocks_TableYieldsExactlyOneTarget(t *testing.T) {
	doc := "| kind | verdict |\n| --- | --- |\n| paragraph | usable |\n| item | unknown |\n| table | unknown |\n"
	targets := mdPreviewBlockTargets(doc)
	assertNonOverlappingOrdered(t, targets)
	if len(targets) != 1 {
		t.Fatalf("got %d targets for a table with 3 body rows, want exactly 1: %+v", len(targets), targets)
	}
	if targets[0].kind != mdBlockTable {
		t.Fatalf("target kind = %s, want table", targets[0].kind)
	}
	if targets[0].startLine != 1 || targets[0].endLine != 5 {
		t.Errorf("table span = [%d,%d], want [1,5] (covers the header and every row)", targets[0].startLine, targets[0].endLine)
	}
}

// --- mdLineIndex, tested directly ---

func TestMdLineIndex_LineAt(t *testing.T) {
	doc := "one\ntwo\nthree\n"
	idx := newMdLineIndex(doc)
	cases := []struct {
		offset int
		want   int
	}{
		{0, 1}, // 'o' of "one"
		{2, 1}, // 'e' of "one"
		{4, 2}, // 't' of "two"
		{8, 3}, // 't' of "three"
	}
	for _, c := range cases {
		if got := idx.lineAt(c.offset); got != c.want {
			t.Errorf("lineAt(%d) = %d, want %d", c.offset, got, c.want)
		}
	}
}
