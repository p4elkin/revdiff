package ui

import (
	"testing"

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
