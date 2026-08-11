package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hitsOf builds a marker-hit sequence from a compact spec, so an alignment
// test can state the sequence it is testing instead of constructing structs.
func hitsOf(spec ...any) []mdPreviewMarkerHit {
	hits := make([]mdPreviewMarkerHit, 0, len(spec)/2)
	for i := 0; i+1 < len(spec); i += 2 {
		hits = append(hits, mdPreviewMarkerHit{kind: spec[i].(mdPreviewBlockKind), row: spec[i+1].(int)})
	}
	return hits
}

// targetsOf builds a block-target sequence of the given kinds, with source
// line spans that are one line each, starting at line 1 and stepping by two so
// the spans never touch.
func targetsOf(kinds ...mdPreviewBlockKind) []mdPreviewBlockTarget {
	targets := make([]mdPreviewBlockTarget, 0, len(kinds))
	for i, k := range kinds {
		targets = append(targets, mdPreviewBlockTarget{Kind: k, StartLine: 1 + i*2, EndLine: 1 + i*2})
	}
	return targets
}

// TestAlignRowsCleanSequence is the agreeing case: one marker per target, same
// kinds in the same order, rows advancing. Every target gets the row its
// marker landed on.
func TestAlignRowsCleanSequence(t *testing.T) {
	targets := targetsOf(mdBlockH1, mdBlockParagraph, mdBlockItem, mdBlockItem, mdBlockCodeBlock)
	hits := hitsOf(mdBlockH1, 1, mdBlockParagraph, 3, mdBlockItem, 5, mdBlockItem, 6, mdBlockCodeBlock, 9)

	rows, ok := mdPreviewAlignRows(targets, hits, nil)
	require.True(t, ok, "clean sequence must align")
	assert.Equal(t, []int{1, 3, 5, 6, 9}, rows)
}

// TestAlignRowsLengthMismatch covers both directions of a length disagreement:
// a marker no target claims, and a target no marker accounts for. Both must
// degrade rather than align a prefix.
func TestAlignRowsLengthMismatch(t *testing.T) {
	t.Run("extra marker", func(t *testing.T) {
		_, ok := mdPreviewAlignRows(targetsOf(mdBlockH1, mdBlockParagraph),
			hitsOf(mdBlockH1, 1, mdBlockParagraph, 3, mdBlockParagraph, 5), nil)
		assert.False(t, ok)
	})
	t.Run("missing marker", func(t *testing.T) {
		_, ok := mdPreviewAlignRows(targetsOf(mdBlockH1, mdBlockParagraph, mdBlockParagraph),
			hitsOf(mdBlockH1, 1, mdBlockParagraph, 3), nil)
		assert.False(t, ok)
	})
}

// TestAlignRowsKindMismatch: same length, same row order, one kind differs.
func TestAlignRowsKindMismatch(t *testing.T) {
	_, ok := mdPreviewAlignRows(targetsOf(mdBlockH1, mdBlockParagraph, mdBlockItem),
		hitsOf(mdBlockH1, 1, mdBlockH2, 3, mdBlockItem, 5), nil)
	assert.False(t, ok)
}

// TestAlignRowsNonMonotonicRows: kinds agree throughout, but the rows do not
// advance. A block cannot render above the block before it, so the render and
// the parse are describing different documents.
func TestAlignRowsNonMonotonicRows(t *testing.T) {
	t.Run("row goes backwards", func(t *testing.T) {
		_, ok := mdPreviewAlignRows(targetsOf(mdBlockH1, mdBlockParagraph, mdBlockParagraph),
			hitsOf(mdBlockH1, 1, mdBlockParagraph, 7, mdBlockParagraph, 4), nil)
		assert.False(t, ok)
	})
	t.Run("row repeats", func(t *testing.T) {
		_, ok := mdPreviewAlignRows(targetsOf(mdBlockH1, mdBlockParagraph),
			hitsOf(mdBlockH1, 2, mdBlockParagraph, 2), nil)
		assert.False(t, ok)
	})
}

// TestAlignRowsTaskMarkerStandsForItem pins the one bridged kind pair: a
// checkbox list item is an item/enumeration target on the parse side and a
// task marker on the render side.
func TestAlignRowsTaskMarkerStandsForItem(t *testing.T) {
	rows, ok := mdPreviewAlignRows(targetsOf(mdBlockItem, mdBlockEnum),
		hitsOf(mdBlockTask, 2, mdBlockTask, 3), nil)
	require.True(t, ok)
	assert.Equal(t, []int{2, 3}, rows)

	_, ok = mdPreviewAlignRows(targetsOf(mdBlockParagraph), hitsOf(mdBlockTask, 2), nil)
	assert.False(t, ok, "a task marker must not stand for a paragraph")
}

// TestAlignRowsTableRunCollapses: one table target absorbs the whole run of
// per-row table markers glamour emits for it.
func TestAlignRowsTableRunCollapses(t *testing.T) {
	rows, ok := mdPreviewAlignRows(targetsOf(mdBlockTable, mdBlockParagraph),
		hitsOf(mdBlockTable, 2, mdBlockTable, 4, mdBlockTable, 5, mdBlockParagraph, 8), nil)
	require.True(t, ok)
	assert.Equal(t, []int{2, 8}, rows)
}

// TestAlignRowsQuoteParagraphsAccounted proves the blockquote fold is
// accounted for exactly, not merely skipped: a two-paragraph quote's second
// paragraph marker is discarded and the paragraph target that follows the
// quote gets the row of the paragraph that really is outside it.
func TestAlignRowsQuoteParagraphsAccounted(t *testing.T) {
	rows, ok := mdPreviewAlignRows(targetsOf(mdBlockQuote, mdBlockParagraph),
		hitsOf(mdBlockParagraph, 2, mdBlockQuote, 2, mdBlockParagraph, 4, mdBlockParagraph, 6), []int{2})
	require.True(t, ok)
	assert.Equal(t, []int{2, 6}, rows, "the trailing paragraph target must not steal the quote's own paragraph row")
}

// TestAlignRowsQuoteWithNestedBlock: a quote whose first child is a code fence
// emits its own paragraph marker AFTER the nested block's, so the leftover
// paragraph budget has to survive across the nested target.
func TestAlignRowsQuoteWithNestedBlock(t *testing.T) {
	rows, ok := mdPreviewAlignRows(targetsOf(mdBlockQuote, mdBlockCodeBlock, mdBlockParagraph),
		hitsOf(mdBlockQuote, 1, mdBlockCodeBlock, 3, mdBlockParagraph, 6, mdBlockParagraph, 9), []int{1})
	require.True(t, ok)
	assert.Equal(t, []int{1, 3, 9}, rows)
}

func TestMdPreviewSrcMapEmptyDocument(t *testing.T) {
	rendered, srcMap := mdPreviewRenderWithMap(mdLines(""), 80, false)
	assert.True(t, srcMap.Aligned, "an empty document has nothing to disagree about")
	assert.Empty(t, srcMap.blocks())
	assert.Equal(t, renderMarkdownDocument(mdLines(""), 80, false), rendered)

	_, ok := srcMap.rowToLine(0)
	assert.False(t, ok, "an empty map answers nothing")
	_, ok = srcMap.lineToRow(0)
	assert.False(t, ok)
}

// TestMdPreviewSrcMapOnlyTable is the whole-document-is-one-table case: the
// per-row table markers must collapse to exactly one anchor.
func TestMdPreviewSrcMapOnlyTable(t *testing.T) {
	doc := "| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |\n"
	_, srcMap := mdPreviewRenderWithMap(mdLines(doc), 80, false)
	require.True(t, srcMap.Aligned)
	require.Len(t, srcMap.blocks(), 1)
	assert.Equal(t, mdBlockTable, srcMap.blocks()[0].Kind)
	assert.Equal(t, 0, srcMap.blocks()[0].StartLine, "the table starts on the document's first line")
	assert.Equal(t, 3, srcMap.blocks()[0].EndLine)
}

// TestMdPreviewSrcMapAdjacentTablesDegrade records the accepted limitation:
// two tables with only a blank line between them produce one indistinguishable
// run of table markers, so the map refuses rather than mis-anchoring.
func TestMdPreviewSrcMapAdjacentTablesDegrade(t *testing.T) {
	doc := "| a | b |\n|---|---|\n| 1 | 2 |\n\n| c | d |\n|---|---|\n| 3 | 4 |\n"
	_, srcMap := mdPreviewRenderWithMap(mdLines(doc), 80, false)
	assert.False(t, srcMap.Aligned)
	assert.Empty(t, srcMap.blocks(), "an unaligned map exposes no targets at all")
	_, ok := srcMap.rowToLine(2)
	assert.False(t, ok)
}

// TestMdPreviewSrcMapAlignsRealDocument walks a document mixing every tracked
// kind and checks each anchor lands on a rendered row whose text really is
// that block's.
func TestMdPreviewSrcMapAlignsRealDocument(t *testing.T) {
	doc := "# Title\n" + // 0
		"\n" +
		"intro paragraph\n" + // 2
		"\n" +
		"## Section\n" + // 4
		"\n" +
		"- [ ] unchecked task\n" + // 6
		"- [x] checked task\n" + // 7
		"- plain bullet\n" + // 8
		"\n" +
		"1. first\n" + // 10
		"2. second\n" + // 11
		"\n" +
		"| a | b |\n" + // 13
		"|---|---|\n" +
		"| 1 | 2 |\n" +
		"\n" +
		"> quoted note\n" + // 17
		"\n" +
		"```sh\n" + // 19
		"ls -la\n" +
		"```\n" +
		"\n" +
		"---\n" + // 23
		"\n" +
		"closing paragraph\n" // 25

	rendered, srcMap := mdPreviewRenderWithMap(mdLines(doc), 80, false)
	require.True(t, srcMap.Aligned, "a document of ordinary markdown must align")

	wantKinds := []mdPreviewBlockKind{
		mdBlockH1, mdBlockParagraph, mdBlockH2,
		mdBlockItem, mdBlockItem, mdBlockItem,
		mdBlockEnum, mdBlockEnum,
		mdBlockTable, mdBlockQuote, mdBlockCodeBlock, mdBlockHR, mdBlockParagraph,
	}
	got := make([]mdPreviewBlockKind, 0, len(srcMap.blocks()))
	for _, a := range srcMap.blocks() {
		got = append(got, a.Kind)
	}
	require.Equal(t, wantKinds, got)

	rows := strings.Split(ansi.Strip(rendered), "\n")
	// spot-check that a handful of anchors really sit on their own text
	for _, tc := range []struct {
		idx  int
		want string
	}{
		{0, "Title"}, {1, "intro paragraph"}, {3, "unchecked task"}, {5, "plain bullet"},
		{6, "first"}, {9, "quoted note"}, {10, "ls -la"}, {12, "closing paragraph"},
	} {
		a := srcMap.blocks()[tc.idx]
		require.Less(t, a.Row, len(rows))
		assert.Contains(t, rows[a.Row], tc.want, "anchor %d (%s) row %d", tc.idx, a.Kind, a.Row)
	}

	// the checked task item is the 5th anchor and comes from source line 7
	assert.Equal(t, 7, srcMap.blocks()[4].StartLine)
}

// TestMdPreviewSrcMapRowLineInverses is the consistency property the two
// queries owe each other: for every block, rowToLine(block row) is the block's
// source line and lineToRow(that line) is the block's row again.
func TestMdPreviewSrcMapRowLineInverses(t *testing.T) {
	body, err := os.ReadFile("../../docs/plans/20260811-preview-annotations.md")
	require.NoError(t, err)

	_, srcMap := mdPreviewRenderWithMap(mdLines(string(body)), 80, false)
	require.True(t, srcMap.Aligned, "this plan document must align")
	require.NotEmpty(t, srcMap.blocks())

	for i, a := range srcMap.blocks() {
		line, ok := srcMap.rowToLine(a.Row)
		require.True(t, ok, "anchor %d: rowToLine(%d)", i, a.Row)
		assert.Equal(t, a.StartLine, line, "anchor %d (%s)", i, a.Kind)

		row, ok := srcMap.lineToRow(a.StartLine)
		require.True(t, ok, "anchor %d: lineToRow(%d)", i, a.StartLine)
		assert.Equal(t, a.Row, row, "anchor %d (%s)", i, a.Kind)
	}

	// rows and lines inside a block resolve to the block that owns them
	for i, a := range srcMap.blocks() {
		for row := a.Row; row <= a.EndRow; row++ {
			line, ok := srcMap.rowToLine(row)
			require.True(t, ok)
			assert.Equal(t, a.StartLine, line, "anchor %d (%s): row %d", i, a.Kind, row)
		}
	}

	// a row before the first block belongs to nobody
	if first := srcMap.blocks()[0]; first.Row > 0 {
		_, ok := srcMap.rowToLine(first.Row - 1)
		assert.False(t, ok)
	}
}

// TestMdPreviewSrcMapVisualIdentity is the acceptance criterion the spike left
// available: the marked-and-cleaned render is identical to today's under
// ansi.Strip, in row count, and in per-row display width. Byte identity is NOT
// asserted and must not be — the unmarked render is not even byte-stable
// against itself (see mdPreviewRenderWithMap's doc comment).
func TestMdPreviewSrcMapVisualIdentity(t *testing.T) {
	docs := map[string]string{
		"prose":    "# Title\n\nA paragraph of prose that is long enough to wrap at eighty columns without any trouble at all.\n",
		"lists":    "- one\n- two\n  - nested\n\n1. first\n2. second\n\n- [ ] task\n- [x] done\n",
		"table":    "| a | b |\n|---|---|\n| 1 | 2 |\n",
		"quote":    "> quoted\n>\n> second para\n\nafter\n",
		"code":     "para\n\n```go\nfmt.Println(\"hi\")\n```\n\nafter\n",
		"mermaid":  "# D\n\n```mermaid\nflowchart TD\n    A[Start] --> B[End]\n```\n\nafter\n",
		"headings": "# h1\n\n## h2\n\n### h3\n\n#### h4\n\n##### h5\n\n###### h6\n",
		"rule":     "before\n\n---\n\nafter\n",
	}
	// widths matter here: glamour re-wraps at every width, and a marker that
	// was not zero-width would move a wrap point and show up as a row-count or
	// row-width difference at the narrow one.
	for _, width := range []int{80, 40} {
		for _, noColors := range []bool{false, true} {
			for name, doc := range docs {
				t.Run(name, func(t *testing.T) {
					want := renderMarkdownDocument(mdLines(doc), width, noColors)
					got, _ := mdPreviewRenderWithMap(mdLines(doc), width, noColors)

					assert.Equal(t, ansi.Strip(want), ansi.Strip(got), "text content must be identical")

					wantRows, gotRows := strings.Split(want, "\n"), strings.Split(got, "\n")
					require.Len(t, gotRows, len(wantRows), "row count must be identical")
					for i := range wantRows {
						assert.Equal(t, ansi.StringWidth(wantRows[i]), ansi.StringWidth(gotRows[i]),
							"row %d display width", i)
					}
				})
			}
		}
	}
}

// TestMdPreviewSrcMapNoColorsEmitsNoEscapes: --no-colors promises zero ANSI in
// the preview, and the markers are ANSI by construction, so this is the mode
// where a leaked marker would be visible as a broken promise.
//
// It also pins the measured limitation of that mode: glamour's ASCII style
// does not put one marker per block (see mdPreviewRenderWithMap's doc
// comment), so the map degrades instead of anchoring. The point of asserting
// it is that it degrades — a no-colors preview that quietly anchored to the
// wrong rows would be worse than one that refuses.
func TestMdPreviewSrcMapNoColorsEmitsNoEscapes(t *testing.T) {
	doc := "# Title\n\ntext\n\n- [ ] task\n- item\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n> quote\n\n```go\nx := 1\n```\n"
	rendered, srcMap := mdPreviewRenderWithMap(mdLines(doc), 80, true)

	assert.NotContains(t, rendered, "\x1b", "no-colors preview must emit no escape sequences")
	assert.Equal(t, rendered, ansi.Strip(rendered))
	assert.False(t, srcMap.Aligned, "the ASCII style's marker placement does not align; it must degrade")
	assert.Empty(t, srcMap.blocks())
}

// TestMdPreviewSrcMapMermaidRowShift proves the art splice is accounted for:
// the block after a diagram must anchor to a row below the whole diagram, not
// to the single placeholder row the markers were read against.
func TestMdPreviewSrcMapMermaidRowShift(t *testing.T) {
	doc := "# Title\n\n```mermaid\nflowchart TD\n    A[Start] --> B[Middle]\n    B --> C[End]\n```\n\nafter the diagram\n"
	rendered, srcMap := mdPreviewRenderWithMap(mdLines(doc), 80, false)
	require.True(t, srcMap.Aligned)

	rows := strings.Split(ansi.Strip(rendered), "\n")
	blocks := srcMap.blocks()
	require.GreaterOrEqual(t, len(blocks), 3, "heading, diagram paragraph, trailing paragraph")

	last := blocks[len(blocks)-1]
	require.Less(t, last.Row, len(rows))
	assert.Contains(t, rows[last.Row], "after the diagram",
		"the paragraph after a spliced diagram must anchor below the art, not on the placeholder row")

	// and it must map back to the source line that really holds that text
	line, ok := srcMap.rowToLine(last.Row)
	require.True(t, ok)
	assert.Equal(t, "after the diagram", mdLines(doc)[line].Content)
}

// TestMdPreviewSrcMapUnalignedExposesNothing pins the degrade contract: an
// unaligned map is not a partial map.
func TestMdPreviewSrcMapUnalignedExposesNothing(t *testing.T) {
	sm := mdPreviewSourceMap{}
	assert.False(t, sm.Aligned)
	assert.Empty(t, sm.blocks())
	assert.Equal(t, -1, sm.anchorAtRow(0))
	assert.Equal(t, -1, sm.anchorAtLine(0))
	_, ok := sm.rowToLine(5)
	assert.False(t, ok)
	_, ok = sm.lineToRow(5)
	assert.False(t, ok)
}

// TestMdPreviewSrcMapQuoteParagraphCounts pins the input the blockquote fold
// depends on: the per-quote count of DIRECT paragraph children, in document
// order, and nothing deeper.
func TestMdPreviewSrcMapQuoteParagraphCounts(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []int
	}{
		{"none", "para\n\n- item\n", nil},
		{"single", "> one\n", []int{1}},
		{"two paragraphs", "> one\n>\n> two\n", []int{2}},
		{"paragraph and fence", "> one\n>\n> ```go\n> x := 1\n> ```\n>\n> two\n", []int{2}},
		{"list inside is not a paragraph", "> intro\n>\n> - a\n> - b\n", []int{1}},
		{"two quotes", "> a\n\npara\n\n> b\n>\n> c\n", []int{1, 2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mdPreviewQuoteParagraphs(tc.doc))
		})
	}
}

// TestMdPreviewSrcMapShiftRow covers the row translation on its own, including
// the boundary: a row at a splice point does not move, the next one does.
func TestMdPreviewSrcMapShiftRow(t *testing.T) {
	shifts := []mdPreviewArtShift{{row: 4, added: 6}, {row: 20, added: 3}}
	assert.Equal(t, 0, mdPreviewShiftRow(shifts, 0))
	assert.Equal(t, 4, mdPreviewShiftRow(shifts, 4), "the spliced row itself stays put")
	assert.Equal(t, 11, mdPreviewShiftRow(shifts, 5))
	assert.Equal(t, 26, mdPreviewShiftRow(shifts, 20))
	assert.Equal(t, 30, mdPreviewShiftRow(shifts, 21))
	assert.Equal(t, 7, mdPreviewShiftRow(nil, 7))
}
