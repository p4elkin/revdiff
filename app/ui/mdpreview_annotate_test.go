package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
)

// annotateFourWayDoc is a document deliberately shaped to exercise all four
// ways a line-level annotation can resolve to a block: a heading (block
// start), a paragraph's second line (mid-block), a blank separator line
// between two list items (matches no block start, falls back to the nearest
// preceding block), and — via a Line no block/document has — an orphaned
// annotation entirely.
const annotateFourWayDoc = "# Title\n\n" +
	"First paragraph line one.\nwith a second line in the same paragraph.\n\n" +
	"- item one\n- item two\n\n" +
	"Closing paragraph."

// TestMdPreviewPaintAnnotations_EveryAnnotationAppearsExactlyOnce is the named
// invariant this task exists to prove: whatever path a line-level annotation
// resolves through, it must show up in the painted preview exactly once. An
// annotation that is silently dropped — or duplicated — is the one
// unacceptable outcome of this feature (see the plan's Testing Strategy).
func TestMdPreviewPaintAnnotations_EveryAnnotationAppearsExactlyOnce(t *testing.T) {
	lines := mdLines(annotateFourWayDoc)
	m := mdPreviewTestModel(lines)

	const (
		atBlockStart = "comment at a block start"
		midParagraph = "comment mid-paragraph"
		blankBetween = "comment on a blank line between blocks"
		lineVanished = "comment on a line that no longer exists"
	)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 1, Type: " ", Comment: atBlockStart})   // "# Title"
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 4, Type: " ", Comment: midParagraph})   // "with a second line..."
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 8, Type: " ", Comment: blankBetween})   // blank line before "Closing paragraph."
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 500, Type: " ", Comment: lineVanished}) // no such line in this document

	rendered, srcMap := mdPreviewRenderWithMap(lines, m.layout.viewport.Width, false)
	require.True(t, srcMap.Aligned, "fixture sanity: this document must align for the test to prove anything")

	painted := m.mdPreviewPaintAnnotations(rendered, srcMap)
	stripped := ansi.Strip(painted)

	for _, comment := range []string{atBlockStart, midParagraph, blankBetween, lineVanished} {
		assert.Equal(t, 1, strings.Count(stripped, comment),
			"annotation %q must appear exactly once in the painted preview", comment)
	}
}

// TestMdPreviewPaintAnnotations_MultipleInOneBlock_PaintedInLineOrder covers
// the "several annotations in one block" checklist item: three annotations on
// three lines of the SAME paragraph must all survive, in ascending line order.
func TestMdPreviewPaintAnnotations_MultipleInOneBlock_PaintedInLineOrder(t *testing.T) {
	doc := "Paragraph line one.\nParagraph line two.\nParagraph line three."
	lines := mdLines(doc)
	m := mdPreviewTestModel(lines)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 3, Type: " ", Comment: "third comment"})
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 1, Type: " ", Comment: "first comment"})
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 2, Type: " ", Comment: "second comment"})

	rendered, srcMap := mdPreviewRenderWithMap(lines, m.layout.viewport.Width, false)
	require.True(t, srcMap.Aligned)
	require.Len(t, srcMap.blocks(), 1, "fixture sanity: three consecutive non-blank lines must be one paragraph block")

	painted := m.mdPreviewPaintAnnotations(rendered, srcMap)
	stripped := ansi.Strip(painted)

	first := strings.Index(stripped, "first comment")
	second := strings.Index(stripped, "second comment")
	third := strings.Index(stripped, "third comment")
	require.True(t, first >= 0 && second >= 0 && third >= 0, "all three annotations must be present")
	assert.Less(t, first, second, "annotations in one block must paint in ascending line order")
	assert.Less(t, second, third, "annotations in one block must paint in ascending line order")
}

// TestMdPreviewPaintAnnotations_NoAnnotations_PassesThrough is the visual
// identity case: with nothing in the store, the render must come back
// completely unchanged.
func TestMdPreviewPaintAnnotations_NoAnnotations_PassesThrough(t *testing.T) {
	lines := mdLines("# Title\n\nSome text.")
	m := mdPreviewTestModel(lines)
	rendered, srcMap := mdPreviewRenderWithMap(lines, m.layout.viewport.Width, false)

	got := m.mdPreviewPaintAnnotations(rendered, srcMap)

	assert.Equal(t, rendered, got, "no annotations in the store must leave the render byte-identical")
}

// TestMdPreviewPaintAnnotations_RowsByteEqualToDiffView proves the painted
// rows are not a reimplementation: they are produced by calling
// renderAnnotationOrInput exactly the way the diff pane does, so the exact
// same bytes it would emit for this annotation must be found in the painted
// preview.
func TestMdPreviewPaintAnnotations_RowsByteEqualToDiffView(t *testing.T) {
	lines := mdLines("# Title\n\nSome text.")
	m := mdPreviewTestModel(lines)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 1, Type: " ", Comment: "a note on the title"})

	rendered, srcMap := mdPreviewRenderWithMap(lines, m.layout.viewport.Width, false)
	require.True(t, srcMap.Aligned)

	painted := m.mdPreviewPaintAnnotations(rendered, srcMap)

	annotationMap, _ := m.buildAnnotationMap()
	var want strings.Builder
	m.renderAnnotationOrInput(&want, 0, annotationMap) // idx 0 = "# Title", the line the annotation targets
	wantRows := mdPreviewRowsFromBuilder(&want)
	require.NotEmpty(t, wantRows, "fixture sanity: the diff pane must actually paint this annotation")

	for _, row := range wantRows {
		assert.Contains(t, painted, row,
			"painted preview must contain the diff view's own rendering of the annotation, byte for byte")
	}
}

// TestMdPreviewPaintAnnotations_FileLevelAlwaysAtTop proves a file-level
// annotation is painted before the document body, matching where
// renderFileAnnotationHeader places it in ordinary source view.
func TestMdPreviewPaintAnnotations_FileLevelAlwaysAtTop(t *testing.T) {
	lines := mdLines("# Title\n\nSome text.")
	m := mdPreviewTestModel(lines)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 0, Type: "", Comment: "file note"})

	rendered, srcMap := mdPreviewRenderWithMap(lines, m.layout.viewport.Width, false)
	require.True(t, srcMap.Aligned)

	painted := m.mdPreviewPaintAnnotations(rendered, srcMap)
	stripped := ansi.Strip(painted)

	noteIdx := strings.Index(stripped, "file note")
	titleIdx := strings.Index(stripped, "Title")
	require.GreaterOrEqual(t, noteIdx, 0)
	require.GreaterOrEqual(t, titleIdx, 0)
	assert.Less(t, noteIdx, titleIdx, "a file-level annotation must be painted before the document body")
}

// TestMdPreviewPaintAnnotations_Degraded_ListsAllAtTop covers the unaligned
// path: with no trustworthy row mapping, every annotation — file-level and
// line-level alike — is listed as one group prepended to the untouched
// original render, rather than any of them being hidden.
func TestMdPreviewPaintAnnotations_Degraded_ListsAllAtTop(t *testing.T) {
	lines := mdLines("# Title\n\nSome text.")
	m := mdPreviewTestModel(lines)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 0, Type: "", Comment: "file-level note"})
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 1, Type: " ", Comment: "line-level note"})

	rendered := "irrelevant body"
	unaligned := mdPreviewSourceMap{} // Aligned defaults to false

	painted := m.mdPreviewPaintAnnotations(rendered, unaligned)

	assert.True(t, strings.HasSuffix(painted, rendered),
		"the original render must survive untouched, appended after the annotation group")
	stripped := ansi.Strip(painted)
	bodyIdx := strings.Index(stripped, "irrelevant body")
	fileIdx := strings.Index(stripped, "file-level note")
	lineIdx := strings.Index(stripped, "line-level note")
	require.GreaterOrEqual(t, fileIdx, 0)
	require.GreaterOrEqual(t, lineIdx, 0)
	assert.Less(t, fileIdx, bodyIdx)
	assert.Less(t, lineIdx, bodyIdx)
}

// TestMdPreviewPaintAnnotations_NoBlocksDegradesLikeUnaligned covers the other
// no-splice-point case: an Aligned map with zero blocks (nothing in the
// document to attach a line-level annotation to) must take the same top-group
// path as an unaligned one, rather than losing the annotation to an
// out-of-range block index.
func TestMdPreviewPaintAnnotations_NoBlocksDegradesLikeUnaligned(t *testing.T) {
	lines := mdLines("")
	m := mdPreviewTestModel(lines)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 1, Type: " ", Comment: "orphaned in an empty document"})

	rendered := "irrelevant body"
	alignedNoBlocks := mdPreviewSourceMap{Aligned: true} // Aligned but zero anchors

	painted := m.mdPreviewPaintAnnotations(rendered, alignedNoBlocks)

	assert.Contains(t, ansi.Strip(painted), "orphaned in an empty document")
	assert.True(t, strings.HasSuffix(painted, rendered))
}

// TestMdPreviewMaxOffset_UnchangedByAnnotationRows proves the horizontal pan
// clamp is unaffected by splicing in annotation rows: an annotation always
// wraps to the pane width (annotationVisualRows), so it can never become the
// widest row in the document — that role stays with whatever unwrapped
// content (a mermaid diagram, here) already held it.
func TestMdPreviewMaxOffset_UnchangedByAnnotationRows(t *testing.T) {
	doc := "# Title\n\n```mermaid\nflowchart TD\n    A[Start of the diagram] --> B[End of the diagram]\n```\n\nClosing text."
	lines := mdLines(doc)
	m := mdPreviewTestModel(lines)
	m.layout.viewport.Width = 20 // narrow pane: forces both the diagram and the pan clamp to matter

	rendered, srcMap := mdPreviewRenderWithMap(lines, m.layout.viewport.Width, false)
	require.True(t, srcMap.Aligned)
	baseOffset := mdPreviewMaxOffset(rendered, m.mdPreviewCutWidth())
	require.Positive(t, baseOffset, "fixture sanity: the diagram must actually be wider than the pane")

	m.store.Add(annotation.Annotation{File: "plan.md", Line: 1, Type: " ", Comment: "a short note"})
	painted := m.mdPreviewPaintAnnotations(rendered, srcMap)
	paintedOffset := mdPreviewMaxOffset(painted, m.mdPreviewCutWidth())

	assert.Equal(t, baseOffset, paintedOffset,
		"annotation rows wrap to the pane width, so they must never widen the pan clamp")
}
