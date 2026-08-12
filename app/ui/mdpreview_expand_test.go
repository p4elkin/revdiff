package ui

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/keymap"
)

func TestMdPreviewRawLines_WholeSpanInOrder(t *testing.T) {
	lines := mdLines("intro\n\n| a | b |\n| - | - |\n| 1 | 2 |\n\ntail")
	got := mdPreviewRawLines(lines, mdPreviewBlockAnchor{kind: mdBlockTable, startLine: 2, endLine: 4}, "    ")

	require.Len(t, got, 3)
	assert.Equal(t, []string{"| a | b |", "| - | - |", "| 1 | 2 |"},
		[]string{got[0].text, got[1].text, got[2].text})
	assert.Equal(t, []int{2, 3, 4}, []int{got[0].lineIdx, got[1].lineIdx, got[2].lineIdx},
		"lineIdx must stay an index into the diff lines, the coordinate an annotation needs")
	for _, rl := range got {
		assert.False(t, rl.blank)
	}
}

func TestMdPreviewRawLines_SkipsDividers(t *testing.T) {
	lines := mdLines("a\nb\nc")
	lines[1].ChangeType = diff.ChangeDivider
	lines[1].Content = "⋯ 5 lines ⋯"

	got := mdPreviewRawLines(lines, mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 0, endLine: 2}, "    ")

	require.Len(t, got, 2, "a divider row is not part of the document glamour rendered")
	assert.Equal(t, []string{"a", "c"}, []string{got[0].text, got[1].text})
	assert.Equal(t, []int{0, 2}, []int{got[0].lineIdx, got[1].lineIdx})
}

func TestMdPreviewRawLines_ExpandsTabs(t *testing.T) {
	lines := mdLines("\tindented\tcell")
	got := mdPreviewRawLines(lines, mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 0, endLine: 0}, "  ")

	require.Len(t, got, 1)
	assert.Equal(t, "  indented  cell", got[0].text, "a surviving tab would desync the pan clamp from the screen")
	assert.NotContains(t, got[0].text, "\t")
}

func TestMdPreviewRawLines_DropsControlBytes(t *testing.T) {
	lines := mdLines("safe\x1b[31mtext\x07 \x7f")
	got := mdPreviewRawLines(lines, mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 0, endLine: 0}, "    ")

	require.Len(t, got, 1)
	assert.Equal(t, "safe[31mtext ", got[0].text, "raw rows bypass glamour, so control bytes must not reach the terminal")
}

func TestMdPreviewRawLines_MarksBlankLines(t *testing.T) {
	lines := mdLines("text\n   \nmore")
	got := mdPreviewRawLines(lines, mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 0, endLine: 2}, "    ")

	require.Len(t, got, 3, "a blank source line still paints a row, so the row arithmetic stays exact")
	assert.Equal(t, []bool{false, true, false}, []bool{got[0].blank, got[1].blank, got[2].blank})
}

func TestMdPreviewRawLines_ClampsOutOfRangeSpan(t *testing.T) {
	lines := mdLines("only")

	got := mdPreviewRawLines(lines, mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 0, endLine: 40}, "    ")
	require.Len(t, got, 1)
	assert.Equal(t, "only", got[0].text)

	assert.Empty(t, mdPreviewRawLines(lines, mdPreviewBlockAnchor{startLine: 5, endLine: 9}, "    "),
		"a span entirely past the end of the file yields nothing rather than panicking")
	assert.Empty(t, mdPreviewRawLines(nil, mdPreviewBlockAnchor{startLine: 0, endLine: 0}, "    "))
}

func TestMdPreviewExpandRefusal_AllowsOrdinaryBlocks(t *testing.T) {
	lines := mdLines("# Title\n\nsome prose\n\n| a | b |\n| - | - |")

	assert.Empty(t, mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockH1, startLine: 0, endLine: 0}, lines, "    "))
	assert.Empty(t, mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 2, endLine: 2}, lines, "    "))
	assert.Empty(t, mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockTable, startLine: 4, endLine: 5}, lines, "    "))
}

func TestMdPreviewExpandRefusal_CodeBlockByKind(t *testing.T) {
	lines := mdLines("```go\nfunc main() {}\n```")
	got := mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockCodeBlock, startLine: 1, endLine: 1}, lines, "    ")
	assert.Equal(t, mdPreviewExpandCodeHint, got)
}

func TestMdPreviewExpandRefusal_MermaidFenceOnSingleLineSpan(t *testing.T) {
	lines := mdLines("```mermaid\nflowchart TD\n  a --> b\n```")
	// joinWithMermaidFences attributes the whole art to the opening fence line,
	// so the block arrives as a paragraph whose span is that one line.
	got := mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 0, endLine: 0}, lines, "    ")
	assert.Equal(t, mdPreviewExpandMermaidHint, got)

	tilde := mdLines("~~~MERMAID title=x\nflowchart TD\n~~~")
	assert.Equal(t, mdPreviewExpandMermaidHint,
		mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 0, endLine: 0}, tilde, "    "),
		"the info string is read the same way joinWithMermaidFences reads it")
}

func TestMdPreviewExpandRefusal_MermaidTextOnMultiLineSpanIsNotADiagram(t *testing.T) {
	// a paragraph that merely starts with fence-looking text but spans more than
	// one line is not the collapsed-diagram shape, so it expands normally.
	lines := mdLines("```mermaid\nstill the same paragraph")
	assert.Empty(t, mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 0, endLine: 1}, lines, "    "))
}

func TestMdPreviewExpandRefusal_AllBlankSpan(t *testing.T) {
	lines := mdLines("text\n   \n\t\nmore")
	got := mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 1, endLine: 2}, lines, "    ")
	assert.Equal(t, mdPreviewExpandNothingHint, got)
}

func TestMdPreviewExpandRefusal_DividerOnlySpan(t *testing.T) {
	lines := mdLines("a\nplaceholder\nb")
	lines[1].ChangeType = diff.ChangeDivider
	lines[1].Content = "⋯ 3 lines ⋯"

	got := mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 1, endLine: 1}, lines, "    ")
	assert.Equal(t, mdPreviewExpandNothingHint, got, "a divider is not source the reader can annotate")
}

// mdExpandFixture is a hand-built stand-in for a base render and its map: three
// blocks, where the middle one is a three-line source paragraph glamour wrapped
// into two rendered rows followed by one row of padding. Hand-built rather than
// rendered so the row arithmetic under test is pinned by numbers the test states
// itself.
func mdExpandFixture() (rendered string, lines []diff.DiffLine, sm mdPreviewSourceMap) {
	rendered = "  Heading\n\n  para one para two para\n  three\n\n  tail\n"
	lines = mdLines("# Heading\n\npara one\npara two\npara three\n\ntail")
	sm = mdPreviewSourceMap{aligned: true, anchors: []mdPreviewBlockAnchor{
		{kind: mdBlockH1, row: 0, endRow: 1, startLine: 0, endLine: 0},
		{kind: mdBlockParagraph, row: 2, endRow: 4, startLine: 2, endLine: 4},
		{kind: mdBlockParagraph, row: 5, endRow: 6, startLine: 6, endLine: 6},
	}}
	return rendered, lines, sm
}

// sameStringValue reports whether two strings share a backing pointer, i.e. one
// is the other rather than an equal copy. mdPreviewScrollCache.forBody compares
// bodies by value, which is O(1) only on a shared pointer, so "returns its input
// untouched" is a real property with a cost attached and not a phrasing.
func sameStringValue(a, b string) bool {
	//nolint:gosec // G103: comparing backing pointers is the only way to prove the no-op path returns its input
	return unsafe.StringData(a) == unsafe.StringData(b) && len(a) == len(b)
}

func TestMdPreviewExpandBlock_ReturnsInputUntouchedWhenNotExpanding(t *testing.T) {
	rendered, lines, sm := mdExpandFixture()

	tests := []struct {
		name  string
		block int
		sm    mdPreviewSourceMap
	}{
		{"no block selected", -1, sm},
		{"map not aligned", 1, mdPreviewSourceMap{anchors: sm.anchors}},
		{"block past the end", 3, sm},
		{"empty map", 0, mdPreviewSourceMap{aligned: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotMap := mdPreviewExpandBlock(rendered, tt.sm, tt.block, lines, "    ")
			assert.True(t, sameStringValue(rendered, got),
				"a rebuilt copy would turn the scroll cache's O(1) body compare into a full memcmp")
			assert.Equal(t, tt.sm, gotMap)
			assert.Empty(t, gotMap.lines)
		})
	}
}

func TestMdPreviewExpandBlock_NoSourceLinesLeavesRenderAlone(t *testing.T) {
	rendered, lines, sm := mdExpandFixture()
	sm.anchors[1].startLine, sm.anchors[1].endLine = 40, 44 // a span past the end of the file

	got, gotMap := mdPreviewExpandBlock(rendered, sm, 1, lines, "    ")
	assert.True(t, sameStringValue(rendered, got))
	assert.Equal(t, sm, gotMap)
}

func TestMdPreviewExpandBlock_ReplacesRowsAndKeepsTrailingBlanks(t *testing.T) {
	rendered, lines, sm := mdExpandFixture()

	got, _ := mdPreviewExpandBlock(rendered, sm, 1, lines, "    ")

	assert.Equal(t, []string{"  Heading", "", "para one", "para two", "para three", "", "  tail", ""},
		strings.Split(got, "\n"),
		"the block's own rows become one row per source line; its trailing padding row stays")
}

func TestMdPreviewExpandBlock_ShiftsLaterAnchorsBySignedDelta(t *testing.T) {
	rendered, lines, sm := mdExpandFixture()

	_, gotMap := mdPreviewExpandBlock(rendered, sm, 1, lines, "    ")

	require.True(t, gotMap.aligned)
	require.Len(t, gotMap.anchors, 3)
	assert.Equal(t, sm.anchors[0], gotMap.anchors[0], "a block above the expanded one never moves")
	assert.Equal(t, [2]int{2, 5}, [2]int{gotMap.anchors[1].row, gotMap.anchors[1].endRow},
		"the expanded block keeps its start row and grows by the delta")
	assert.Equal(t, [2]int{6, 7}, [2]int{gotMap.anchors[2].row, gotMap.anchors[2].endRow})
	assert.Equal(t, 2, sm.anchors[1].row, "the caller's map is never edited in place")
	assert.Equal(t, 5, sm.anchors[2].row)
}

func TestMdPreviewExpandBlock_ShrinkingBlockShiftsLaterAnchorsUp(t *testing.T) {
	// one source line that glamour wrapped into three rendered rows: expansion
	// makes the document SHORTER, so the delta is negative.
	rendered := "  Heading\n\n  a very long paragraph that\n  glamour wrapped across three\n  rendered rows\n\n  tail\n"
	lines := mdLines("# Heading\n\nlong\n\ntail")
	sm := mdPreviewSourceMap{aligned: true, anchors: []mdPreviewBlockAnchor{
		{kind: mdBlockH1, row: 0, endRow: 1, startLine: 0, endLine: 0},
		{kind: mdBlockParagraph, row: 2, endRow: 5, startLine: 2, endLine: 2},
		{kind: mdBlockParagraph, row: 6, endRow: 7, startLine: 4, endLine: 4},
	}}

	got, gotMap := mdPreviewExpandBlock(rendered, sm, 1, lines, "    ")

	assert.Equal(t, []string{"  Heading", "", "long", "", "  tail", ""}, strings.Split(got, "\n"))
	assert.Equal(t, [2]int{2, 3}, [2]int{gotMap.anchors[1].row, gotMap.anchors[1].endRow})
	assert.Equal(t, [2]int{4, 5}, [2]int{gotMap.anchors[2].row, gotMap.anchors[2].endRow})
}

func TestMdPreviewExpandBlock_RecordsOneLineAnchorPerNonBlankRow(t *testing.T) {
	rendered, lines, sm := mdExpandFixture()

	_, gotMap := mdPreviewExpandBlock(rendered, sm, 1, lines, "    ")

	require.Len(t, gotMap.lines, 3)
	assert.Equal(t, []int{2, 3, 4}, []int{gotMap.lines[0].row, gotMap.lines[1].row, gotMap.lines[2].row})
	assert.Equal(t, []int{2, 3, 4},
		[]int{gotMap.lines[0].lineIdx, gotMap.lines[1].lineIdx, gotMap.lines[2].lineIdx},
		"lineIdx stays the diff-line coordinate an annotation is saved against")
	for _, la := range gotMap.lines {
		assert.Equal(t, 1, la.block)
	}
}

func TestMdPreviewExpandBlock_BlankSourceLinePaintsARowButNoAnchor(t *testing.T) {
	rendered, _, sm := mdExpandFixture()
	lines := mdLines("# Heading\n\npara one\n   \npara three\n\ntail")

	got, gotMap := mdPreviewExpandBlock(rendered, sm, 1, lines, "    ")

	assert.Equal(t, []string{"  Heading", "", "para one", "   ", "para three", "", "  tail", ""},
		strings.Split(got, "\n"), "the blank line still paints, so one source line is still one row")
	require.Len(t, gotMap.lines, 2, "a stop on a blank row would be invisible, so it gets no anchor")
	assert.Equal(t, []int{2, 4}, []int{gotMap.lines[0].row, gotMap.lines[1].row})
	assert.Equal(t, []int{2, 4}, []int{gotMap.lines[0].lineIdx, gotMap.lines[1].lineIdx})
}

func TestMdPreviewExpandBlock_PaintsPreparedRawText(t *testing.T) {
	rendered, _, sm := mdExpandFixture()
	lines := mdLines("# Heading\n\n\tone\n\x1b[31mtwo\nthree\n\ntail")

	got, _ := mdPreviewExpandBlock(rendered, sm, 1, lines, "..")

	assert.Equal(t, []string{"  Heading", "", "..one", "[31mtwo", "three", "", "  tail", ""},
		strings.Split(got, "\n"), "rows are painted through mdPreviewRawLines, tabs expanded and control bytes dropped")
}

func TestMdPreviewExpandBlock_LastBlockSpanRunsToDocumentEnd(t *testing.T) {
	rendered, lines, sm := mdExpandFixture()
	// the last block's span reaches the end of the render, so expanding it must
	// keep the document's own trailing rows rather than eating them.
	got, gotMap := mdPreviewExpandBlock(rendered, sm, 2, lines, "    ")

	assert.Equal(t, []string{"  Heading", "", "  para one para two para", "  three", "", "tail", ""},
		strings.Split(got, "\n"))
	assert.Equal(t, [2]int{5, 6}, [2]int{gotMap.anchors[2].row, gotMap.anchors[2].endRow})
	require.Len(t, gotMap.lines, 1)
	assert.Equal(t, 6, gotMap.lines[0].lineIdx)
}

func TestMdPreviewExpandBlock_CarriesAnnotAnchorsThrough(t *testing.T) {
	// production runs this pass before the annotation painter, so annots is empty
	// here; carrying it rather than dropping it keeps the pass a pure rewrite of
	// the fields it owns.
	rendered, lines, sm := mdExpandFixture()
	sm.annots = []mdPreviewAnnotAnchor{{block: 0, row: 1, endRow: 1, line: 1, changeType: "context"}}

	_, gotMap := mdPreviewExpandBlock(rendered, sm, 1, lines, "    ")
	assert.Equal(t, sm.annots, gotMap.annots)
}

func TestMdPreviewExpandBlock_AnchorOutsideTheRenderIsRefused(t *testing.T) {
	tests := []struct {
		name string
		edit func(sm *mdPreviewSourceMap)
	}{
		{"row past the render", func(sm *mdPreviewSourceMap) { sm.anchors[1].row = 99 }},
		{"endRow past the render", func(sm *mdPreviewSourceMap) { sm.anchors[1].endRow = 99 }},
		{"endRow before row", func(sm *mdPreviewSourceMap) { sm.anchors[1].endRow = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rendered, lines, sm := mdExpandFixture()
			tt.edit(&sm) // a map built against a different render

			got, gotMap := mdPreviewExpandBlock(rendered, sm, 1, lines, "    ")
			assert.True(t, sameStringValue(rendered, got),
				"a malformed span must be refused outright, not clamped into a map whose rows are off")
			assert.Equal(t, sm, gotMap)
		})
	}
}

func TestMdPreviewExpandBlock_AllBlankSpanIsRefused(t *testing.T) {
	rendered, _, sm := mdExpandFixture()
	lines := mdLines("# Heading\n\n   \n\t\n \n\ntail")

	got, gotMap := mdPreviewExpandBlock(rendered, sm, 1, lines, "    ")
	assert.True(t, sameStringValue(rendered, got),
		"expanding would paint blank rows and record no anchor, so the stop list would say nothing is expanded")
	assert.Equal(t, sm, gotMap)
}

// TestMdPreviewClipRawSpan pins the clip itself: a container's span stops at the
// first source line the NEXT block claims, and an ordinary block is untouched.
func TestMdPreviewClipRawSpan(t *testing.T) {
	anchors := []mdPreviewBlockAnchor{
		{kind: mdBlockItem, row: 2, endRow: 2, startLine: 0, endLine: 6},
		{kind: mdBlockCodeBlock, row: 3, endRow: 5, startLine: 3, endLine: 3},
		{kind: mdBlockParagraph, row: 6, endRow: 8, startLine: 8, endLine: 8},
	}

	assert.Equal(t, 2, mdPreviewClipRawSpan(anchors, 0).endLine,
		"the item's span must stop before the nested fence's own first line")
	assert.Equal(t, 0, mdPreviewClipRawSpan(anchors, 0).startLine, "the start is never moved")
	assert.Equal(t, anchors[1], mdPreviewClipRawSpan(anchors, 1),
		"a block the next one does not reach into keeps its whole span")
	assert.Equal(t, anchors[2], mdPreviewClipRawSpan(anchors, 2), "the last block has nothing to clip against")
}

// TestMdPreviewToggleRaw_NestedBlockIsNotPaintedTwice is the regression the clip
// exists for. A list item holding a fenced code block spans the fence's source
// lines while owning only the rows above it, so painting the whole span put the
// fence body and the trailing paragraph on screen as source AND left them
// rendered right underneath.
func TestMdPreviewToggleRaw_NestedBlockIsNotPaintedTwice(t *testing.T) {
	docs := map[string]string{
		"list item":  "- para a\n\n  ```go\n  x := 1\n  ```\n\n  para b\n\nafter\n",
		"blockquote": "> quoted a\n>\n> ```go\n> y := 2\n> ```\n>\n> quoted b\n\nafter\n",
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			m := mdPreviewStyledModel(t, doc)
			_, sm := m.mdPreviewBody()
			require.True(t, sm.aligned, "fixture sanity: the document must align")
			require.Greater(t, len(sm.blocks()), 1, "fixture sanity: the container plus its nested fence")
			require.Less(t, sm.blocks()[1].startLine, sm.blocks()[0].endLine,
				"fixture sanity: the nested block's source sits inside the container's span")

			m.setMdPreviewBlockCursor(0)
			m.mdPreviewToggleRaw()
			require.Equal(t, 0, m.mdPreviewExpandedBlock(), "the container must still expand")

			body, _ := m.mdPreviewBody()
			rows := strings.Split(ansi.Strip(body), "\n")
			counts := map[string]int{}
			for _, r := range rows {
				if t := strings.TrimSpace(r); t != "" {
					counts[t]++
				}
			}
			for _, text := range []string{"x := 1", "y := 2", "para b", "quoted b"} {
				assert.LessOrEqual(t, counts[text], 1, "%q must appear at most once on screen, not rendered AND as source", text)
			}
		})
	}
}

// TestMdPreviewToggleRaw_ClippedSpanStopsAtTheNestedBlock states the positive
// half of the clip: the container's own leading lines are what gets painted, and
// the lines the nested block owns are left to it.
func TestMdPreviewToggleRaw_ClippedSpanStopsAtTheNestedBlock(t *testing.T) {
	m := mdPreviewStyledModel(t, "- para a\n\n  ```go\n  x := 1\n  ```\n\n  para b\n\nafter\n")
	m.setMdPreviewBlockCursor(0)
	m.mdPreviewToggleRaw()

	_, sm := m.mdPreviewBody()
	got := make([]int, 0, len(sm.lines))
	for _, la := range sm.lines {
		got = append(got, la.lineIdx)
	}
	assert.Equal(t, []int{0, 2}, got,
		"only the item's own lines up to the fence get anchors; line 1 is blank so it paints without one")
}

// toggleRawDoc is the fixture for the `r` tests: a heading (block 0), a
// two-source-line paragraph (block 1) and a fenced code block (block 2), which
// is the one block expansion refuses by kind.
const toggleRawDoc = "# Title\n\nAlpha line one.\nAlpha line two.\n\n```go\nfmt.Println(1)\n```\n"

// toggleRawModel is toggleRawDoc in a styled preview model, with the block shape
// the tests below name asserted once here rather than in each of them.
func toggleRawModel(t *testing.T) Model {
	t.Helper()
	m := mdPreviewStyledModel(t, toggleRawDoc)
	_, sm := m.mdPreviewBody()
	require.True(t, sm.aligned, "fixture sanity: toggleRawDoc must align")
	require.Len(t, sm.blocks(), 3, "fixture sanity: heading, paragraph, code fence")
	require.Equal(t, 2, sm.blocks()[1].startLine, "fixture sanity: the paragraph starts at source line 2")
	require.Equal(t, mdBlockCodeBlock, sm.blocks()[2].kind, "fixture sanity: block 2 is the code fence")
	return m
}

// TestMdPreviewToggleRaw_ExpandsTheCursorBlock is the headline: `r` on a block
// redraws it as its source, one row per line, and drops the cursor onto the
// first of those lines so j/k and `a` work at line level straight away.
func TestMdPreviewToggleRaw_ExpandsTheCursorBlock(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewBlockCursor(1)

	m.mdPreviewToggleRaw()

	assert.Equal(t, 1, m.mdPreviewExpandedBlock(), "the block the cursor was on must now be drawn as source")
	ref, ok := m.mdPreviewCursorRef()
	require.True(t, ok, "expanding must leave the cursor placed")
	assert.Equal(t, mdPreviewStopRef{block: 1, onLine: true, line: 2}, ref,
		"the cursor must land on the block's first raw source line")

	body, sm := m.mdPreviewBody()
	rows := strings.Split(ansi.Strip(body), "\n")
	require.Len(t, sm.lines, 2, "the paragraph's two source lines must each have painted a row")
	for _, la := range sm.lines {
		require.Less(t, la.row, len(rows))
		assert.Equal(t, m.file.lines[la.lineIdx].Content, rows[la.row],
			"a raw row must be the source line and nothing else — no prefix, no gutter")
	}
}

// TestMdPreviewToggleRaw_SecondPressCollapses pins the toggle half: `r` again
// puts the block back and leaves the cursor on the block's own stop, so the
// reader is where they started rather than nowhere.
func TestMdPreviewToggleRaw_SecondPressCollapses(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewBlockCursor(1)
	m.mdPreviewToggleRaw()
	require.Equal(t, 1, m.mdPreviewExpandedBlock())

	m.mdPreviewToggleRaw()

	assert.Equal(t, -1, m.mdPreviewExpandedBlock(), "the second press must collapse the block")
	ref, ok := m.mdPreviewCursorRef()
	require.True(t, ok, "collapsing must leave the cursor on the block, not clear it")
	assert.Equal(t, mdPreviewStopRef{block: 1}, ref)

	_, sm := m.mdPreviewBody()
	assert.Empty(t, sm.lines, "a collapsed document paints no raw rows")
}

// TestMdPreviewToggleRaw_SeedsAtTheViewportCenter covers `r` pressed with
// nothing selected — the state every preview session starts in. It seeds exactly
// where `a` would, so the two keys can never aim at different blocks.
func TestMdPreviewToggleRaw_SeedsAtTheViewportCenter(t *testing.T) {
	m := mdPreviewStyledModel(t, stopsDoc)
	_, sm := m.mdPreviewBody()
	want := m.mdPreviewCenterBlock(sm)
	require.GreaterOrEqual(t, want, 0, "fixture sanity: the center seed must resolve a block")
	_, hasCursor := m.mdPreviewCursorRef()
	require.False(t, hasCursor, "fixture sanity: nothing is selected yet")

	m.mdPreviewToggleRaw()

	assert.Equal(t, want, m.mdPreviewExpandedBlock(), "`r` must expand the block `a` would have annotated")
	ref, ok := m.mdPreviewCursorRef()
	require.True(t, ok)
	assert.True(t, ref.onLine, "and land on one of its raw lines")
}

// TestMdPreviewToggleRaw_FromAnnotationStopLandsOnTheAnnotatedLine: the reader
// is looking at a comment on a line, so `r` shows them that line rather than the
// top of the block it happens to sit in.
func TestMdPreviewToggleRaw_FromAnnotationStopLandsOnTheAnnotatedLine(t *testing.T) {
	m := toggleRawModel(t)
	annotateLine(m, 4, "on alpha line two") // store Line is 1-based: source index 3
	m.setMdPreviewCursorRef(mdPreviewStopRef{block: 1, onAnnot: true})
	_, sm := m.mdPreviewBody()
	_, ok := m.mdPreviewCursorStop(sm)
	require.True(t, ok, "fixture sanity: the annotation stop must resolve")

	m.mdPreviewToggleRaw()

	ref, ok := m.mdPreviewCursorRef()
	require.True(t, ok)
	assert.Equal(t, mdPreviewStopRef{block: 1, onLine: true, line: 3}, ref,
		"`r` on a comment must expand its block and land on the line the comment is attached to")
}

// TestMdPreviewToggleRaw_RefusesTheFileLevelStop: the file-level annotation owns
// no block, so there is no source to show. The refusal says how to reach one.
func TestMdPreviewToggleRaw_RefusesTheFileLevelStop(t *testing.T) {
	m := toggleRawModel(t)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 0, Type: "", Comment: "about the whole file"})
	m.setMdPreviewCursorRef(mdPreviewStopRef{block: mdPreviewFileStopBlock, onAnnot: true})

	m.mdPreviewToggleRaw()

	assert.Equal(t, mdPreviewExpandFileHint, m.preview.hint)
	assert.Equal(t, -1, m.mdPreviewExpandedBlock(), "nothing may expand")
}

// TestMdPreviewToggleRaw_RefusesACodeFence proves the block-level refusals reach
// the key: a code fence already shows its own source, and the cursor stays where
// it was rather than being moved by a press that changed nothing.
func TestMdPreviewToggleRaw_RefusesACodeFence(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewBlockCursor(2)

	m.mdPreviewToggleRaw()

	assert.Equal(t, mdPreviewExpandCodeHint, m.preview.hint)
	assert.Equal(t, -1, m.mdPreviewExpandedBlock())
	ref, ok := m.mdPreviewCursorRef()
	require.True(t, ok)
	assert.Equal(t, mdPreviewStopRef{block: 2}, ref, "a refused press must not move the cursor")
}

// TestMdPreviewToggleRaw_RefusesAnUnanchorableDocument: a document the map
// cannot anchor has no block to expand, and a silent refusal there would be
// indistinguishable from an unbound key.
func TestMdPreviewToggleRaw_RefusesAnUnanchorableDocument(t *testing.T) {
	m := mdPreviewStyledModel(t, "")
	_, sm := m.mdPreviewBody()
	require.Empty(t, sm.blocks(), "fixture sanity: an empty document anchors nothing")

	m.mdPreviewToggleRaw()

	assert.Equal(t, mdPreviewUnanchorableHint, m.preview.hint)
	assert.Equal(t, -1, m.mdPreviewExpandedBlock())
}

// TestMdPreviewToggleRaw_ResetsTheHorizontalPan: the rendered and the raw form of
// a block have different natural widths, so showing the source starting at
// column 40 is not "show me this block's source". Both directions, matching
// toggleMarkdownPreview's own rule.
func TestMdPreviewToggleRaw_ResetsTheHorizontalPan(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewBlockCursor(1)
	m.layout.scrollX = 12

	m.mdPreviewToggleRaw()
	assert.Equal(t, 0, m.layout.scrollX, "expanding must reset the pan")

	m.layout.scrollX = 7
	m.mdPreviewToggleRaw()
	assert.Equal(t, 0, m.layout.scrollX, "and so must collapsing")
}

// TestMdPreviewToggleRaw_ThroughTheKeyPath covers the wiring rather than the
// behavior: the action must be on the preview allowlist and routed inside
// handleMdPreviewAction, or `r` is a dead key in the only mode it means anything.
func TestMdPreviewToggleRaw_ThroughTheKeyPath(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewBlockCursor(1)

	model, cmd, handled := m.handleMdPreviewAction(keymap.ActionToggleRaw)

	require.True(t, handled, "preview must handle toggle_raw itself, never let it fall through")
	assert.Nil(t, cmd, "expanding is a pure state change plus a viewport swap")
	assert.Equal(t, 1, model.(Model).mdPreviewExpandedBlock())
}

// TestMdPreviewToggleRaw_NotPreviewableIsANoOp: preview can be stuck on for a
// file renderDiff will not preview, and running the markdown pipeline there
// would expand a block of a document that is not on screen.
func TestMdPreviewToggleRaw_NotPreviewableIsANoOp(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewBlockCursor(1)
	m.file.markdownPreviewable = false

	m.mdPreviewToggleRaw()

	assert.Equal(t, -1, m.mdPreviewExpandedBlock())
	assert.Empty(t, m.preview.hint)
}

// TestMdPreviewCollapseRaw_NotPreviewableReportsNothingToCollapse pins the guard
// esc depends on: with preview stuck on for a non-previewable file the collapse
// must report false, so ActionDismiss still falls through to handleEscKey.
func TestMdPreviewCollapseRaw_NotPreviewableReportsNothingToCollapse(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewBlockCursor(1)
	m.mdPreviewToggleRaw()
	require.Equal(t, 1, m.mdPreviewExpandedBlock())
	m.file.markdownPreviewable = false

	assert.False(t, m.mdPreviewCollapseRaw())
	assert.Equal(t, 1, m.mdPreviewExpandedBlock(), "nothing may be mutated on the false path")
}

// TestMdPreviewToggleRaw_KeyPressExpandsAndCollapses drives the real `r`
// keystroke through Update — key -> keymap.Resolve -> dispatchAction ->
// handleMdPreviewAction. Every other preview key has a press round trip, and
// without one the chain is proved only in two disconnected halves.
func TestMdPreviewToggleRaw_KeyPressExpandsAndCollapses(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewBlockCursor(1)

	m = pressKey(t, m, "r")
	assert.Equal(t, 1, m.mdPreviewExpandedBlock(), "`r` must reach mdPreviewToggleRaw through the real dispatch chain")
	ref, ok := m.mdPreviewCursorRef()
	require.True(t, ok)
	assert.Equal(t, mdPreviewStopRef{block: 1, onLine: true, line: 2}, ref)

	m = pressKey(t, m, "r")
	assert.Equal(t, -1, m.mdPreviewExpandedBlock(), "a second press must collapse")
	ref, ok = m.mdPreviewCursorRef()
	require.True(t, ok)
	assert.Equal(t, mdPreviewStopRef{block: 1}, ref)
}

// TestMdPreviewToggleRaw_KeyPressOutsidePreviewChangesNothing: `r` is a global
// default binding, so the press has to be harmless in source view. It falls
// through dispatchResolvedAction to the pane handlers, which is why this asserts
// on the preview state and the store rather than on "nothing at all happened".
func TestMdPreviewToggleRaw_KeyPressOutsidePreviewChangesNothing(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewBlockCursor(1)
	m.modes.mdPreview = false

	got := pressKey(t, m, "r")

	assert.Equal(t, -1, got.mdPreviewExpandedBlock(), "toggle_raw must do nothing with preview off")
	assert.False(t, got.modes.mdPreview, "and must not turn preview on")
	assert.Empty(t, got.preview.hint)
	assert.Empty(t, got.store.Get("plan.md"), "and must not touch the annotation store")
}

// TestMdPreviewToggleRaw_RefusesAMermaidDiagram reaches the mermaid refusal
// through the key rather than through mdPreviewExpandRefusal alone: only the key
// path can show that the hint reaches m.preview.hint.
func TestMdPreviewToggleRaw_RefusesAMermaidDiagram(t *testing.T) {
	m := mdPreviewStyledModel(t, "# Title\n\n```mermaid\nflowchart TD\n  a --> b\n```\n")
	_, sm := m.mdPreviewBody()
	require.True(t, sm.aligned, "fixture sanity: the diagram document must align")
	require.Len(t, sm.blocks(), 2, "fixture sanity: the heading plus the collapsed diagram paragraph")
	require.Equal(t, sm.blocks()[1].startLine, sm.blocks()[1].endLine,
		"fixture sanity: the diagram's span is the single fence-opening line")
	m.setMdPreviewBlockCursor(1)

	m = pressKey(t, m, "r")

	assert.Equal(t, mdPreviewExpandMermaidHint, m.preview.hint)
	assert.Equal(t, -1, m.mdPreviewExpandedBlock())
}

// TestMdPreviewToggleRaw_RefusesABlockWithNothingToShow reaches the "Nothing to
// show" refusal through the key. It is built on a hand-made map because a block
// whose every source line is blank cannot be produced by rendering a document —
// which is also why the guard it exercises had no test at all.
func TestMdPreviewToggleRaw_RefusesABlockWithNothingToShow(t *testing.T) {
	m := toggleRawModel(t)
	for i := range m.file.lines {
		if i >= 2 && i <= 3 {
			m.file.lines[i].Content = "   " // blank out the paragraph the cursor is on
		}
	}
	m.setMdPreviewBlockCursor(1)

	m = pressKey(t, m, "r")

	assert.Equal(t, mdPreviewExpandNothingHint, m.preview.hint)
	assert.Equal(t, -1, m.mdPreviewExpandedBlock())
}

// TestMdPreviewToggleRaw_StaleCursorFallsBackToTheCenterSeed: a cursor left
// pointing at a block index the current map no longer has resolves no stop at
// all, so `r` seeds at the viewport center exactly as it does with no cursor —
// which is why mdPreviewExpandTarget's own out-of-range guard below that is
// unreachable in practice and kept purely as a net.
func TestMdPreviewToggleRaw_StaleCursorFallsBackToTheCenterSeed(t *testing.T) {
	m := mdPreviewStyledModel(t, stopsDoc)
	_, sm := m.mdPreviewBody()
	want := m.mdPreviewCenterBlock(sm)
	require.GreaterOrEqual(t, want, 0, "fixture sanity: the center seed must resolve a block")
	m.setMdPreviewCursorRef(mdPreviewStopRef{block: 99, onAnnot: true})

	m.mdPreviewToggleRaw()

	assert.Equal(t, want, m.mdPreviewExpandedBlock(), "a ref no map can resolve must not be read as a block index")
}

// TestMdPreviewToggleRaw_RawRowsCarryNoStyling pins the "plain, unprefixed,
// unstyled" decision on the UNSTRIPPED body: --no-colors promises a preview with
// no ANSI in it, and comparing against ansi.Strip(body) — which every other test
// here does — would pass just as happily on a dim-styled row.
func TestMdPreviewToggleRaw_RawRowsCarryNoStyling(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewBlockCursor(1)
	m.mdPreviewToggleRaw()

	body, sm := m.mdPreviewBody()
	rows := strings.Split(body, "\n")
	require.NotEmpty(t, sm.lines)
	for _, la := range sm.lines {
		require.Less(t, la.row, len(rows))
		assert.Equal(t, ansi.Strip(rows[la.row]), rows[la.row], "a raw row must carry no escape sequences at all")
	}
}

// TestMdPreviewToggleRaw_HighlightsTheSelectedRawLine: the cursor's own stop
// inside an expanded block is a single row, and the bar has to reach the pane
// edge there — a raw row is never padded by glamour, so without the line-stop
// case in mdPreviewHighlight the mark stops at the end of the text.
func TestMdPreviewToggleRaw_HighlightsTheSelectedRawLine(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewBlockCursor(1)
	m.mdPreviewToggleRaw()

	body, sm := m.mdPreviewBody()
	require.Len(t, sm.lines, 2)
	frame := strings.Split(m.mdPreviewFrame(body, sm), "\n")
	require.Less(t, sm.lines[0].row, len(frame))

	bg := mdPreviewHighlightBg(m)
	require.NotEmpty(t, bg, "fixture sanity: the styled model must carry a search background")
	assert.Contains(t, frame[sm.lines[0].row], bg, "the cursor's raw line must be painted")
	assert.NotContains(t, frame[sm.lines[1].row], bg, "and only that one")
	assert.GreaterOrEqual(t, ansi.StringWidth(frame[sm.lines[0].row]), m.mdPreviewCutWidth(),
		"the bar must span the pane, not stop at the end of an 18-column source line")
}

// TestMdPreviewToggleRaw_PanReachesTheEndOfTheWidestRawLine: the raw rows are
// what the pan clamp must measure once a block is expanded. Every other pan test
// runs on a collapsed document, so the widening was asserted nowhere.
func TestMdPreviewToggleRaw_PanReachesTheEndOfTheWidestRawLine(t *testing.T) {
	wide := strings.Repeat("wide ", 60) // one source line far wider than the pane
	m := mdPreviewStyledModel(t, "# Title\n\n"+wide+"\n")
	m.setMdPreviewBlockCursor(1)

	collapsedBody, _ := m.mdPreviewBody()
	collapsedMax := m.mdPreviewMaxOffset(collapsedBody, m.mdPreviewCutWidth())

	m.mdPreviewToggleRaw()
	require.Equal(t, 1, m.mdPreviewExpandedBlock(), "fixture sanity: the paragraph must expand")

	expandedBody, _ := m.mdPreviewBody()
	expandedMax := m.mdPreviewMaxOffset(expandedBody, m.mdPreviewCutWidth())
	assert.Greater(t, expandedMax, collapsedMax,
		"an unwrapped raw row is wider than the wrapped render, so the clamp must grow with it")
	assert.Equal(t, ansi.StringWidth(m.file.lines[2].Content)-m.mdPreviewCutWidth(), expandedMax,
		"the clamp must be measured against the raw row's own width")

	for range expandedMax + 5 {
		m.panMarkdownPreview(1)
	}
	assert.Equal(t, expandedMax, m.layout.scrollX, "panning right must reach the end of the longest raw line and stop")
}

// TestMdPreviewToggleRaw_SurvivesAWidthChange is the one path where an expanded
// cursor meets a map it was not built against: a resize re-renders the base and
// re-runs expansion against fresh anchors.
func TestMdPreviewToggleRaw_SurvivesAWidthChange(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewBlockCursor(1)
	m.mdPreviewToggleRaw()
	require.Equal(t, 1, m.mdPreviewExpandedBlock())

	m.layout.viewport.Width = 48
	body, sm := m.mdPreviewBody()

	assert.Equal(t, 1, sm.expandedBlock(), "the narrower render must still expand the same block")
	stop, ok := m.mdPreviewCursorStop(sm)
	require.True(t, ok, "the cursor's raw-line stop must still resolve against the re-rendered map")
	rows := strings.Split(ansi.Strip(body), "\n")
	require.Less(t, stop.row, len(rows))
	assert.Equal(t, m.file.lines[2].Content, rows[stop.row], "and still name the row its own source line is on")
}

func TestMdPreviewRawStopLine(t *testing.T) {
	raw := []mdPreviewRawLine{
		{text: "", lineIdx: 4, blank: true},
		{text: "one", lineIdx: 5},
		{text: "two", lineIdx: 6},
	}

	got, ok := mdPreviewRawStopLine(raw, 6)
	assert.True(t, ok)
	assert.Equal(t, 6, got, "a wanted line that painted a stoppable row is used as-is")

	got, ok = mdPreviewRawStopLine(raw, 4)
	assert.True(t, ok)
	assert.Equal(t, 5, got, "a blank line paints a row but is no stop, so the first non-blank one answers")

	got, ok = mdPreviewRawStopLine(raw, -1)
	assert.True(t, ok)
	assert.Equal(t, 5, got, "no wanted line means the block's first raw line")

	_, ok = mdPreviewRawStopLine([]mdPreviewRawLine{{lineIdx: 1, blank: true}}, -1)
	assert.False(t, ok, "an all-blank block has no line the reader could see selected")
}

func TestMdPreviewTrailingBlankRows(t *testing.T) {
	rows := []string{"a", "\x1b[32m  \x1b[0m", "", "b", "", "  "}

	assert.Equal(t, 2, mdPreviewTrailingBlankRows(rows, 3, 5), "ANSI-only rows count as blank")
	assert.Equal(t, 0, mdPreviewTrailingBlankRows(rows, 0, 3))
	assert.Equal(t, 2, mdPreviewTrailingBlankRows(rows, 0, 2))
	assert.Equal(t, 0, mdPreviewTrailingBlankRows(rows, 2, 2), "the span's first row is never counted")
}

func TestMdPreviewMermaidFenceLine(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"backtick mermaid", "```mermaid", true},
		{"tilde mermaid", "~~~mermaid", true},
		{"indented", "   ```mermaid", true},
		{"uppercase", "```MERMAID", true},
		{"extra info", "```mermaid title=foo", true},
		{"other language", "```go", false},
		{"bare fence", "```", false},
		{"too short", "``mermaid", false},
		{"prose", "mermaid diagrams are nice", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mdPreviewMermaidFenceLine(tt.content))
		})
	}
}
