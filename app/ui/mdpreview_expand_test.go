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
	anchors := []mdPreviewBlockAnchor{
		{kind: mdBlockH1, startLine: 0, endLine: 0},
		{kind: mdBlockParagraph, startLine: 2, endLine: 2},
		{kind: mdBlockTable, startLine: 4, endLine: 5},
	}

	for i := range anchors {
		assert.Empty(t, mdPreviewExpandRefusal(anchors, i, lines, "    "))
	}
}

func TestMdPreviewExpandRefusal_CodeBlockByKind(t *testing.T) {
	lines := mdLines("```go\nfunc main() {}\n```")
	anchors := []mdPreviewBlockAnchor{{kind: mdBlockCodeBlock, startLine: 1, endLine: 1}}
	assert.Equal(t, mdPreviewExpandCodeHint, mdPreviewExpandRefusal(anchors, 0, lines, "    "))
}

// TestMdPreviewExpandRefusal_AllowsAMermaidDiagram is the asymmetry with the
// code fence above: a fence renders as its own lines, a diagram renders as art
// that looks nothing like its source, so the diagram is the one that must expand.
// joinWithMermaidFences attributes the whole art to the opening fence line, so
// the block arrives as a paragraph whose recorded span is that one line, and the
// clip is where the fence's real extent comes back.
func TestMdPreviewExpandRefusal_AllowsAMermaidDiagram(t *testing.T) {
	lines := mdLines("```mermaid\nflowchart TD\n  a --> b\n```")
	anchors := []mdPreviewBlockAnchor{{kind: mdBlockParagraph, startLine: 0, endLine: 0}}
	assert.Empty(t, mdPreviewExpandRefusal(anchors, 0, lines, "    "))
	assert.Equal(t, 3, mdPreviewClipRawSpan(anchors, 0, lines).endLine,
		"the span must reach the closing fence, or the pass would paint one row over the whole art")

	tilde := mdLines("~~~MERMAID title=x\nflowchart TD\n~~~")
	assert.Empty(t, mdPreviewExpandRefusal(anchors, 0, tilde, "    "),
		"the info string is read the same way joinWithMermaidFences reads it")
	assert.Equal(t, 2, mdPreviewClipRawSpan(anchors, 0, tilde).endLine)
}

func TestMdPreviewExpandRefusal_MermaidTextOnMultiLineSpanIsNotADiagram(t *testing.T) {
	// a paragraph that merely starts with fence-looking text but spans more than
	// one line is not the collapsed-diagram shape, so it expands normally.
	lines := mdLines("```mermaid\nstill the same paragraph")
	anchors := []mdPreviewBlockAnchor{{kind: mdBlockParagraph, startLine: 0, endLine: 1}}
	assert.Empty(t, mdPreviewExpandRefusal(anchors, 0, lines, "    "))
}

func TestMdPreviewExpandRefusal_AllBlankSpan(t *testing.T) {
	lines := mdLines("text\n   \n\t\nmore")
	anchors := []mdPreviewBlockAnchor{
		{kind: mdBlockParagraph, startLine: 1, endLine: 2},
		{kind: mdBlockParagraph, startLine: 3, endLine: 3},
	}
	assert.Equal(t, mdPreviewExpandNothingHint, mdPreviewExpandRefusal(anchors, 0, lines, "    "))
}

func TestMdPreviewExpandRefusal_DividerOnlySpan(t *testing.T) {
	lines := mdLines("a\nplaceholder\nb")
	lines[1].ChangeType = diff.ChangeDivider
	lines[1].Content = "⋯ 3 lines ⋯"
	anchors := []mdPreviewBlockAnchor{
		{kind: mdBlockParagraph, startLine: 1, endLine: 1},
		{kind: mdBlockParagraph, startLine: 2, endLine: 2},
	}

	got := mdPreviewExpandRefusal(anchors, 0, lines, "    ")
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

// TestMdPreviewClipRawSpan pins the re-cut itself, in both directions: a
// container's span STOPS at the first source line the next block claims, and a
// nested block's span GROWS to cover the container's trailing lines, because
// those are rendered into rows the nested block owns.
func TestMdPreviewClipRawSpan(t *testing.T) {
	lines := mdLines("- para a\n\n  ```go\n  x := 1\n  ```\n\n  para b\n\nafter\n")
	anchors := []mdPreviewBlockAnchor{
		{kind: mdBlockItem, row: 2, endRow: 2, startLine: 0, endLine: 6},
		{kind: mdBlockCodeBlock, row: 3, endRow: 5, startLine: 3, endLine: 3},
		{kind: mdBlockParagraph, row: 6, endRow: 8, startLine: 8, endLine: 8},
	}

	assert.Equal(t, 2, mdPreviewClipRawSpan(anchors, 0, lines).endLine,
		"the item's span must stop before the nested fence's own first line")
	assert.Equal(t, 0, mdPreviewClipRawSpan(anchors, 0, lines).startLine, "the start is never moved")
	assert.Equal(t, 6, mdPreviewClipRawSpan(anchors, 1, lines).endLine,
		"the fence's span must cover the item's trailing paragraph, whose rendered row the fence owns")
	assert.Equal(t, 8, mdPreviewClipRawSpan(anchors, 2, lines).endLine,
		"the last block runs to the end of the document, past its own recorded endLine")
}

// TestMdPreviewClipRawSpan_DropsTrailingBlankLines is the other end of the same
// symmetry: mdPreviewExpandBlock keeps a block's trailing blank RENDERED rows, so
// the span must not carry the blank SOURCE lines that produced them.
func TestMdPreviewClipRawSpan_DropsTrailingBlankLines(t *testing.T) {
	lines := mdLines("one\n\n\ntwo\n\n\n")
	anchors := []mdPreviewBlockAnchor{
		{kind: mdBlockParagraph, startLine: 0, endLine: 0},
		{kind: mdBlockParagraph, startLine: 3, endLine: 3},
	}

	assert.Equal(t, 0, mdPreviewClipRawSpan(anchors, 0, lines).endLine, "the two blank lines before the next block go")
	assert.Equal(t, 3, mdPreviewClipRawSpan(anchors, 1, lines).endLine, "and so do the ones at the end of the file")

	blank := mdLines("\n\n\n")
	only := []mdPreviewBlockAnchor{{kind: mdBlockParagraph, startLine: 1, endLine: 2}}
	assert.Equal(t, 1, mdPreviewClipRawSpan(only, 0, blank).endLine,
		"an all-blank span keeps its first line rather than collapsing to nothing")

	short := []mdPreviewBlockAnchor{{kind: mdBlockParagraph, startLine: 9, endLine: 9}}
	assert.Equal(t, mdPreviewBlockAnchor{kind: mdBlockParagraph, startLine: 9, endLine: 9},
		mdPreviewClipRawSpan(short, 0, lines), "an anchor past the end of a shorter load is left alone")
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

			m.setMdPreviewCursorToBlock(0)
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

// nestedRawDocs are the container shapes the raw span has to be re-cut for: a
// container holding a nested block AND its own content after it. Expanding the
// NESTED block is the direction that used to delete the container's trailing
// content from the frame, because that content is rendered into rows the nested
// block owns. None of the nested blocks is a code fence, which
// mdPreviewExpandRefusal turns away before any of this runs.
var nestedRawDocs = map[string]struct {
	doc      string
	nested   int    // the block index of the nested construct
	trailing string // the container's own content rendered after it
}{
	"nested list": {"- para one\n\n  - nested item\n\n  para two\n\n# After\n", 1, "para two"},
	"two nested lists": {"- para one\n\n  - nested a\n\n  mid para\n\n  - nested b\n\n  tail para\n\n# After\n",
		1, "mid para"},
	"blockquote":   {"> quote one\n>\n> - nested item\n>\n> quote para two\n\n# After\n", 1, "quote para two"},
	"nested table": {"- para one\n\n  | a | b |\n  | - | - |\n  | 1 | 2 |\n\n  para two\n\n# After\n", 1, "para two"},
}

// TestMdPreviewToggleRaw_NestedBlockKeepsTheContainersTrailingRows is the
// regression for the deletion half of the re-cut. The rows a nested block owns
// run to the row before the container's next sibling, so they include the rows
// the container's OWN trailing paragraph was rendered into. Replacing that whole
// range with the nested block's few source lines wiped the paragraph off the
// screen for as long as the block stayed expanded — no warning, no marker, and
// the reader had no way to tell part of the document was missing.
func TestMdPreviewToggleRaw_NestedBlockKeepsTheContainersTrailingRows(t *testing.T) {
	for name, tc := range nestedRawDocs {
		t.Run(name, func(t *testing.T) {
			m := mdPreviewStyledModel(t, tc.doc)
			before, sm := m.mdPreviewBody()
			require.True(t, sm.aligned, "fixture sanity: the document must align")
			require.Greater(t, len(sm.blocks()), tc.nested+1, "fixture sanity: a block must follow the nested one")
			require.Contains(t, ansi.Strip(before), tc.trailing, "fixture sanity: the trailing content renders")

			m.setMdPreviewCursorToBlock(tc.nested)
			m.mdPreviewToggleRaw()
			require.Equal(t, tc.nested, m.mdPreviewExpandedBlock(), "the nested block must expand, not be refused")

			after, _ := m.mdPreviewBody()
			assert.Contains(t, ansi.Strip(after), tc.trailing,
				"the container's own trailing content must survive the expansion of a block nested above it")
		})
	}
}

// TestMdPreviewToggleRaw_NestedBlockOwnsTheContainersTrailingLines is the other
// half: those trailing source lines are painted BY the nested block, so they are
// reachable — j steps onto one and `a` comments on that exact line. Before the
// re-cut they belonged to no block's span at all, so no key could reach them.
func TestMdPreviewToggleRaw_NestedBlockOwnsTheContainersTrailingLines(t *testing.T) {
	m := mdPreviewStyledModel(t, nestedRawDocs["nested list"].doc)
	m.setMdPreviewCursorToBlock(1)
	m.mdPreviewToggleRaw()
	require.Equal(t, 1, m.mdPreviewExpandedBlock(), "fixture sanity: the nested item must expand")

	_, sm := m.mdPreviewBody()
	got := make([]int, 0, len(sm.lines))
	for _, la := range sm.lines {
		got = append(got, la.lineIdx)
	}
	assert.Equal(t, []int{2, 4}, got,
		"the nested item's own line and the container's trailing paragraph; line 3 is blank so it paints without an anchor")

	m.moveMdPreviewCursor(1)
	require.Equal(t, mdPreviewStopRef{block: 1, onLine: true, line: 4}, mustMdPreviewRef(t, m),
		"j must step onto the container's trailing source line")

	m.mdPreviewStartAnnotation()
	require.True(t, m.annot.annotating, "`a` must open an input on it")
	assert.Equal(t, 4, m.nav.diffCursor, "aimed at that exact source line")
}

// TestMdPreviewToggleRaw_SpanDoesNotGrowPastItsOwnContent bounds the growth: a
// block with no enclosing container keeps its own span, so expanding a paragraph
// that happens to be followed by a fence does not show the fence's opening
// marker as if it were the paragraph's last source line. Only a container's
// continuation is rendered into a block's rows; a line that renders to nothing
// is not.
func TestMdPreviewToggleRaw_SpanDoesNotGrowPastItsOwnContent(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewCursorToBlock(1)
	m.mdPreviewToggleRaw()

	body, sm := m.mdPreviewBody()
	got := make([]int, 0, len(sm.lines))
	for _, la := range sm.lines {
		got = append(got, la.lineIdx)
	}
	assert.Equal(t, []int{2, 3}, got, "the paragraph's own two source lines and nothing after them")
	assert.NotContains(t, ansi.Strip(body), "```go", "the following fence's opening marker is not this block's source")
}

// TestMdPreviewToggleRaw_ClippedSpanStopsAtTheNestedBlock states the positive
// half of the clip: the container's own leading lines are what gets painted, and
// the lines the nested block owns are left to it.
func TestMdPreviewToggleRaw_ClippedSpanStopsAtTheNestedBlock(t *testing.T) {
	m := mdPreviewStyledModel(t, "- para a\n\n  ```go\n  x := 1\n  ```\n\n  para b\n\nafter\n")
	m.setMdPreviewCursorToBlock(0)
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
	m.setMdPreviewCursorToBlock(1)

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
	m.setMdPreviewCursorToBlock(1)
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
	m.setMdPreviewCursorToBlock(2)

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
	m.setMdPreviewCursorToBlock(1)
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
	m.setMdPreviewCursorToBlock(1)

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
	m.setMdPreviewCursorToBlock(1)
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
	m.setMdPreviewCursorToBlock(1)
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
	m.setMdPreviewCursorToBlock(1)

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
	m.setMdPreviewCursorToBlock(1)
	m.modes.mdPreview = false

	got := pressKey(t, m, "r")

	assert.Equal(t, -1, got.mdPreviewExpandedBlock(), "toggle_raw must do nothing with preview off")
	assert.False(t, got.modes.mdPreview, "and must not turn preview on")
	assert.Empty(t, got.preview.hint)
	assert.Empty(t, got.store.Get("plan.md"), "and must not touch the annotation store")
}

// mermaidRawDoc is the fixture for the diagram tests: a heading (block 0), a
// mermaid diagram (block 1, a paragraph whose recorded span is its single
// fence-opening line) and a paragraph after it (block 2).
const mermaidRawDoc = "# Title\n\n```mermaid\nflowchart TD\n    a[Alpha] --> b[Beta]\n```\n\nAfter the diagram.\n"

// mermaidRawModel is mermaidRawDoc in a styled preview model, with the block
// shape the tests below name asserted once here rather than in each of them.
func mermaidRawModel(t *testing.T) Model {
	t.Helper()
	m := mdPreviewStyledModel(t, mermaidRawDoc)
	_, sm := m.mdPreviewBody()
	require.True(t, sm.aligned, "fixture sanity: the diagram document must align")
	require.Len(t, sm.blocks(), 3, "fixture sanity: the heading, the collapsed diagram, the paragraph after it")
	require.Equal(t, sm.blocks()[1].startLine, sm.blocks()[1].endLine,
		"fixture sanity: the diagram's recorded span is the single fence-opening line")
	require.Equal(t, 2, sm.blocks()[1].startLine, "fixture sanity: the fence opens on source line 2")
	return m
}

// TestMdPreviewToggleRaw_ExpandsAMermaidDiagram is the headline of this change:
// `r` on a diagram shows the definition it was drawn from. The art is the one
// rendered form that carries none of its source's text, so this is the only way
// to read or comment on the definition at all.
func TestMdPreviewToggleRaw_ExpandsAMermaidDiagram(t *testing.T) {
	m := mermaidRawModel(t)
	m.setMdPreviewCursorToBlock(1)

	m = pressKey(t, m, "r")

	assert.Empty(t, m.preview.hint, "a diagram must no longer be refused")
	require.Equal(t, 1, m.mdPreviewExpandedBlock())
	assert.Equal(t, mdPreviewStopRef{block: 1, onLine: true, line: 2}, mustMdPreviewRef(t, m),
		"the cursor lands on the fence-opening line, which is where a comment on the diagram anchors")

	body, sm := m.mdPreviewBody()
	rows := strings.Split(ansi.Strip(body), "\n")
	got := make([]int, 0, len(sm.lines))
	for _, la := range sm.lines {
		got = append(got, la.lineIdx)
		require.Less(t, la.row, len(rows))
		assert.Equal(t, m.file.lines[la.lineIdx].Content, rows[la.row],
			"a raw row must be the source line and nothing else")
	}
	assert.Equal(t, []int{2, 3, 4, 5}, got,
		"the whole fence, opening and closing markers included — one source line, one row")
}

// TestMdPreviewToggleRaw_ExpandingADiagramRemovesItsArt is the other half of the
// same press, and the one the row arithmetic can get wrong on its own: the source
// replaces the art rather than being painted beside it. A diagram that expanded
// while its art stayed on screen would show the same diagram twice.
func TestMdPreviewToggleRaw_ExpandingADiagramRemovesItsArt(t *testing.T) {
	m := mermaidRawModel(t)
	before, _ := m.mdPreviewBody()
	require.Contains(t, ansi.Strip(before), "┌", "fixture sanity: the diagram must render as box art")
	require.Contains(t, ansi.Strip(before), "Alpha", "fixture sanity: the art must carry the node label")

	m.setMdPreviewCursorToBlock(1)
	m.mdPreviewToggleRaw()

	after := ansi.Strip(mdPreviewBodyOf(t, m))
	assert.NotContains(t, after, "┌", "every art row must be gone while the block is expanded")
	assert.Contains(t, after, "flowchart TD", "and the definition must be on screen in its place")
	assert.Contains(t, after, "Title", "the rest of the document is untouched")
	assert.Contains(t, after, "After the diagram.", "including everything below the diagram")
}

// TestMdPreviewToggleRaw_CollapsingADiagramRestoresTheArt covers both ways out —
// a second `r` and `esc` — against the frame the reader started with. Byte
// equality is the assertion worth making here: the expansion changes the row
// count, so anything left over from it would move the whole document below.
func TestMdPreviewToggleRaw_CollapsingADiagramRestoresTheArt(t *testing.T) {
	for _, tt := range []struct {
		name     string
		collapse func(m Model) Model
	}{
		{"second r", func(m Model) Model { m.mdPreviewToggleRaw(); return m }},
		{"esc", func(m Model) Model {
			model, _, handled := m.handleMdPreviewAction(keymap.ActionDismiss)
			require.True(t, handled)
			return model.(Model)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := mermaidRawModel(t)
			m.setMdPreviewCursorToBlock(1)
			before, _ := m.mdPreviewBody()
			m.mdPreviewToggleRaw()
			require.Equal(t, 1, m.mdPreviewExpandedBlock(), "fixture sanity: expand first")

			got := tt.collapse(m)

			assert.Equal(t, -1, got.mdPreviewExpandedBlock())
			assert.Equal(t, before, mdPreviewBodyOf(t, got), "the art must come back exactly as it was")
		})
	}
}

// TestMdPreviewStartAnnotation_OnADiagramDefinitionLine is what expanding a
// diagram is FOR: commenting on one line of the definition ("this edge label is
// wrong") rather than on the diagram as a whole. The fixture aims at the edge
// line, so a comment landing on the block instead would visibly carry the fence
// line's number.
func TestMdPreviewStartAnnotation_OnADiagramDefinitionLine(t *testing.T) {
	m := mermaidRawModel(t)
	m.setMdPreviewCursorToBlock(1)
	m.mdPreviewToggleRaw()
	m.moveMdPreviewCursor(1)
	m.moveMdPreviewCursor(1)
	require.Equal(t, mdPreviewStopRef{block: 1, onLine: true, line: 4}, mustMdPreviewRef(t, m),
		"fixture sanity: the cursor must be on the edge line of the definition")

	m.mdPreviewStartAnnotation()
	require.True(t, m.annot.annotating, "`a` on a definition line must open a line-level input")
	assert.Equal(t, 4, m.nav.diffCursor)
	m.annot.input.SetValue("this edge label is wrong")
	m.saveAnnotation()

	anns := m.store.Get("plan.md")
	require.Len(t, anns, 1)
	assert.Equal(t, 5, anns[0].Line, "the comment carries the definition line's own 1-based number")
	assert.Equal(t, "this edge label is wrong", anns[0].Comment)
	assert.Equal(t, 1, m.mdPreviewExpandedBlock(), "annotating must not collapse the diagram under the reader")
}

// TestMdPreviewToggleRaw_ExpandsADiagramInsideAListItem: a diagram indented into
// a list item is substituted by a placeholder written at column 0, so it ends up
// its own top-level block rather than part of the item. Expanding it must still
// paint the fence as it stands in the source, indentation included, and leave the
// item's own lines to the item.
func TestMdPreviewToggleRaw_ExpandsADiagramInsideAListItem(t *testing.T) {
	doc := "- item text\n\n  ```mermaid\n  flowchart LR\n      a[Alpha] --> b[Beta]\n  ```\n\n  trailing line\n"
	m := mdPreviewStyledModel(t, doc)
	_, sm := m.mdPreviewBody()
	require.True(t, sm.aligned, "fixture sanity: the document must align")
	require.Len(t, sm.blocks(), 3, "fixture sanity: the item, the diagram, the trailing paragraph")
	require.Equal(t, 2, sm.blocks()[1].startLine, "fixture sanity: block 1 is the diagram")

	m.setMdPreviewCursorToBlock(1)
	m.mdPreviewToggleRaw()

	body, painted := m.mdPreviewBody()
	require.Equal(t, 1, m.mdPreviewExpandedBlock())
	got := make([]int, 0, len(painted.lines))
	for _, la := range painted.lines {
		got = append(got, la.lineIdx)
	}
	assert.Equal(t, []int{2, 3, 4, 5}, got, "the fence's own lines and nothing of the item around it")
	rows := strings.Split(ansi.Strip(body), "\n")
	assert.Equal(t, "  ```mermaid", rows[painted.lines[0].row], "the source is painted as it stands, indentation included")
	assert.Contains(t, ansi.Strip(body), "item text", "the item's own rendered row stays")
	assert.Contains(t, ansi.Strip(body), "trailing line", "and so does the paragraph after the diagram")
}

// TestMdPreviewToggleRaw_TwoDiagramsExpandIndependently: with more than one
// diagram in a document, each block's row tile has to be found on its own —
// expanding the second must not disturb the first, and the art that comes back
// on collapse must be the one that was there.
func TestMdPreviewToggleRaw_TwoDiagramsExpandIndependently(t *testing.T) {
	doc := "```mermaid\nflowchart TD\n    one[First] --> two[Second]\n```\n\nBetween.\n\n" +
		"```mermaid\nflowchart LR\n    three[Third] --> four[Fourth]\n```\n"
	m := mdPreviewStyledModel(t, doc)
	before, sm := m.mdPreviewBody()
	require.True(t, sm.aligned, "fixture sanity: the document must align")
	require.Len(t, sm.blocks(), 3, "fixture sanity: diagram, paragraph, diagram")
	require.Equal(t, []int{0, 5, 7}, []int{sm.blocks()[0].startLine, sm.blocks()[1].startLine, sm.blocks()[2].startLine},
		"fixture sanity: both fences open where the test says they do")

	m.setMdPreviewCursorToBlock(2)
	m.mdPreviewToggleRaw()

	body, painted := m.mdPreviewBody()
	got := make([]int, 0, len(painted.lines))
	for _, la := range painted.lines {
		got = append(got, la.lineIdx)
	}
	assert.Equal(t, []int{7, 8, 9, 10}, got, "the second diagram's own fence, not the first one's")
	stripped := ansi.Strip(body)
	assert.Contains(t, stripped, "First", "the first diagram's art must be untouched")
	assert.Contains(t, stripped, "flowchart LR", "the second diagram's definition is on screen")
	assert.Less(t, strings.Count(stripped, "┌"), strings.Count(ansi.Strip(before), "┌"),
		"and its art rows are gone — the node labels stay only because the definition names them")

	m.mdPreviewToggleRaw()
	assert.Equal(t, before, mdPreviewBodyOf(t, m), "collapsing restores both diagrams exactly")
}

// mdPreviewBodyOf is the painted body of m, for the tests that compare frames.
func mdPreviewBodyOf(t *testing.T, m Model) string {
	t.Helper()
	body, _ := m.mdPreviewBody()
	return body
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
	m.setMdPreviewCursorToBlock(1)

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
	m.setMdPreviewCursorToBlock(1)
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
	m.setMdPreviewCursorToBlock(1)
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
	m.setMdPreviewCursorToBlock(1)

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
	m.setMdPreviewCursorToBlock(1)
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

func TestMdPreviewMermaidFenceSpan(t *testing.T) {
	tests := []struct {
		name  string
		doc   string
		start int
		want  int
		ok    bool
	}{
		{"closing fence", "```mermaid\nflowchart TD\n```\nafter", 0, 2, true},
		{"longer closing marker", "```mermaid\nflowchart TD\n`````", 0, 2, true},
		{"tilde", "~~~mermaid\nflowchart TD\n~~~", 0, 2, true},
		{"backticks do not close a tilde fence", "~~~mermaid\n```\n~~~", 0, 2, true},
		{"shorter marker does not close", "````mermaid\n```\n````", 0, 2, true},
		{"trailing text does not close", "```mermaid\n``` still art\n```", 0, 2, true},
		{"not a diagram", "```go\nx := 1\n```", 0, 0, false},
		{"unclosed", "```mermaid\nflowchart TD", 0, 0, false},
		{"out of range", "```mermaid\n```", 7, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := mdPreviewMermaidFenceSpan(mdLines(tt.doc), tt.start)
			assert.Equal(t, tt.ok, ok)
			if tt.ok {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// TestMdPreviewMermaidFenceSpan_SkipsDividers: a compact diff drops rows out of
// the middle of a file and marks the gap with a divider. joinWithMermaidFences
// skips those rows outright, so a divider inside a fence neither closes it nor
// shifts what does.
func TestMdPreviewMermaidFenceSpan_SkipsDividers(t *testing.T) {
	lines := mdLines("```mermaid\nplaceholder\n```\nafter")
	lines[1].ChangeType = diff.ChangeDivider
	lines[1].Content = "⋯ 4 lines ⋯"

	got, ok := mdPreviewMermaidFenceSpan(lines, 0)

	require.True(t, ok)
	assert.Equal(t, 2, got)
}

// TestMdPreviewOwnSpanEnd_OnlyWidensACollapsedDiagram: the single-line shape is
// what a substituted diagram looks like. A paragraph that merely opens with fence
// text spans more than one line, which means the fence was never substituted, so
// there is nothing collapsed to recover.
func TestMdPreviewOwnSpanEnd_OnlyWidensACollapsedDiagram(t *testing.T) {
	lines := mdLines("```mermaid\nflowchart TD\n```\ntail")

	assert.Equal(t, 2, mdPreviewOwnSpanEnd(mdPreviewBlockAnchor{startLine: 0, endLine: 0}, lines))
	assert.Equal(t, 1, mdPreviewOwnSpanEnd(mdPreviewBlockAnchor{startLine: 0, endLine: 1}, lines),
		"a multi-line paragraph is ordinary prose, whatever its first line looks like")
	assert.Equal(t, 3, mdPreviewOwnSpanEnd(mdPreviewBlockAnchor{startLine: 3, endLine: 3}, lines))
	assert.Equal(t, 9, mdPreviewOwnSpanEnd(mdPreviewBlockAnchor{startLine: 9, endLine: 9}, lines),
		"an anchor pointing past the end of the file must not index out of range")
}

// TestMdPreviewEsc_CollapsesTheExpandedBlock: esc is the second way out of raw
// source, beside pressing r again, and the one every reader tries first. It
// leaves the cursor on the block it collapsed, so the reader is where they
// started rather than nowhere.
func TestMdPreviewEsc_CollapsesTheExpandedBlock(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewCursorToBlock(1)
	m.mdPreviewToggleRaw()
	require.Equal(t, 1, m.mdPreviewExpandedBlock(), "fixture sanity: the block must be expanded first")
	m.layout.scrollX = 9

	model, cmd, handled := m.handleMdPreviewAction(keymap.ActionDismiss)

	require.True(t, handled, "esc with a block expanded must be handled inside preview")
	assert.Nil(t, cmd, "collapsing is a pure state change plus a viewport swap")
	got := model.(Model)
	assert.Equal(t, -1, got.mdPreviewExpandedBlock(), "esc must collapse the block")
	ref, ok := got.mdPreviewCursorRef()
	require.True(t, ok, "collapsing must leave the cursor on the block, not clear it")
	assert.Equal(t, mdPreviewStopRef{block: 1}, ref)
	assert.Equal(t, 0, got.layout.scrollX, "collapsing must reset the pan, exactly as the second r does")
}

// TestMdPreviewEsc_FallsThroughWhenNothingIsExpanded: with no expansion to
// collapse, esc must report itself UNHANDLED so it keeps reaching handleEscKey
// and clearing a leftover search-match highlight. Handling it unconditionally
// would make esc a dead key for the search a reader ran before pressing P.
func TestMdPreviewEsc_FallsThroughWhenNothingIsExpanded(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewCursorToBlock(1)
	require.Equal(t, -1, m.mdPreviewExpandedBlock(), "fixture sanity: nothing is expanded")
	before := m.preview

	model, _, handled := m.handleMdPreviewAction(keymap.ActionDismiss)

	assert.False(t, handled, "esc must fall through when there is no expansion to collapse")
	// "nothing is mutated on that false path" is load-bearing, not incidental:
	// dispatchAction DISCARDS the returned model when handled is false, so a
	// mutation made here would be silently thrown away rather than applied.
	got := model.(Model)
	assert.Equal(t, before, got.preview, "the false path must leave the preview state untouched")
	assert.Equal(t, m.layout.scrollX, got.layout.scrollX)
}
