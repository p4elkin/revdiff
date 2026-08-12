package ui

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
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

	assert.Empty(t, mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockH1, startLine: 0, endLine: 0}, lines))
	assert.Empty(t, mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 2, endLine: 2}, lines))
	assert.Empty(t, mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockTable, startLine: 4, endLine: 5}, lines))
}

func TestMdPreviewExpandRefusal_CodeBlockByKind(t *testing.T) {
	lines := mdLines("```go\nfunc main() {}\n```")
	got := mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockCodeBlock, startLine: 1, endLine: 1}, lines)
	assert.Equal(t, mdPreviewExpandCodeHint, got)
}

func TestMdPreviewExpandRefusal_MermaidFenceOnSingleLineSpan(t *testing.T) {
	lines := mdLines("```mermaid\nflowchart TD\n  a --> b\n```")
	// joinWithMermaidFences attributes the whole art to the opening fence line,
	// so the block arrives as a paragraph whose span is that one line.
	got := mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 0, endLine: 0}, lines)
	assert.Equal(t, mdPreviewExpandMermaidHint, got)

	tilde := mdLines("~~~MERMAID title=x\nflowchart TD\n~~~")
	assert.Equal(t, mdPreviewExpandMermaidHint,
		mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 0, endLine: 0}, tilde),
		"the info string is read the same way joinWithMermaidFences reads it")
}

func TestMdPreviewExpandRefusal_MermaidTextOnMultiLineSpanIsNotADiagram(t *testing.T) {
	// a paragraph that merely starts with fence-looking text but spans more than
	// one line is not the collapsed-diagram shape, so it expands normally.
	lines := mdLines("```mermaid\nstill the same paragraph")
	assert.Empty(t, mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 0, endLine: 1}, lines))
}

func TestMdPreviewExpandRefusal_AllBlankSpan(t *testing.T) {
	lines := mdLines("text\n   \n\t\nmore")
	got := mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 1, endLine: 2}, lines)
	assert.Equal(t, mdPreviewExpandNothingHint, got)
}

func TestMdPreviewExpandRefusal_DividerOnlySpan(t *testing.T) {
	lines := mdLines("a\nplaceholder\nb")
	lines[1].ChangeType = diff.ChangeDivider
	lines[1].Content = "⋯ 3 lines ⋯"

	got := mdPreviewExpandRefusal(mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 1, endLine: 1}, lines)
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
	rendered, lines, sm := mdExpandFixture()
	sm.anchors[1].row = 99 // a map built against a different render

	got, gotMap := mdPreviewExpandBlock(rendered, sm, 1, lines, "    ")
	assert.True(t, sameStringValue(rendered, got))
	assert.Equal(t, sm, gotMap)
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
