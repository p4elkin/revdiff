package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/keymap"
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
	require.True(t, srcMap.aligned, "fixture sanity: this document must align for the test to prove anything")

	painted, _ := m.mdPreviewPaintAnnotationsTracked(rendered, srcMap)
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
	require.True(t, srcMap.aligned)
	require.Len(t, srcMap.blocks(), 1, "fixture sanity: three consecutive non-blank lines must be one paragraph block")

	painted, _ := m.mdPreviewPaintAnnotationsTracked(rendered, srcMap)
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

	got, _ := m.mdPreviewPaintAnnotationsTracked(rendered, srcMap)

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
	require.True(t, srcMap.aligned)

	painted, _ := m.mdPreviewPaintAnnotationsTracked(rendered, srcMap)

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
	require.True(t, srcMap.aligned)

	painted, _ := m.mdPreviewPaintAnnotationsTracked(rendered, srcMap)
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

	painted, _ := m.mdPreviewPaintAnnotationsTracked(rendered, unaligned)

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
	alignedNoBlocks := mdPreviewSourceMap{aligned: true} // Aligned but zero anchors

	painted, _ := m.mdPreviewPaintAnnotationsTracked(rendered, alignedNoBlocks)

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
	require.True(t, srcMap.aligned)
	baseOffset := m.mdPreviewMaxOffset(rendered, m.mdPreviewCutWidth())
	require.Positive(t, baseOffset, "fixture sanity: the diagram must actually be wider than the pane")

	m.store.Add(annotation.Annotation{File: "plan.md", Line: 1, Type: " ", Comment: "a short note"})
	painted, _ := m.mdPreviewPaintAnnotationsTracked(rendered, srcMap)
	paintedOffset := m.mdPreviewMaxOffset(painted, m.mdPreviewCutWidth())

	assert.Equal(t, baseOffset, paintedOffset,
		"annotation rows wrap to the pane width, so they must never widen the pan clamp")
}

// TestMdPreviewStartAnnotation_NothingHighlighted_NoOp covers the aim-with-
// nothing-highlighted case: an empty document has no block for
// mdPreviewHighlightAnchor to mark, so starting a preview annotation must be
// a refusal, not a crash or an annotation on a line that does not exist.
func TestMdPreviewStartAnnotation_NothingHighlighted_NoOp(t *testing.T) {
	m := mdPreviewTestModel(mdLines(""))
	m.modes.mdPreview = true

	cmd := m.mdPreviewStartAnnotation()

	assert.Nil(t, cmd)
	assert.False(t, m.annot.annotating, "with nothing to anchor to, starting a preview annotation must be a no-op")
}

// TestMdPreviewStartAnnotation_AimMidDocument proves `a` anchors to whatever
// block the scroll-following highlight currently marks — here the viewport is
// scrolled so the second of three blocks is topmost, and the resolved
// StartLine must be that block's, not the first or the last.
func TestMdPreviewStartAnnotation_AimMidDocument(t *testing.T) {
	doc := "# Heading\n\nFirst paragraph.\n\nSecond paragraph.\n\nThird paragraph."
	lines := mdLines(doc)
	m := mdPreviewTestModel(lines)
	m.modes.mdPreview = true

	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned)
	require.GreaterOrEqual(t, len(srcMap.blocks()), 3, "fixture sanity: need at least three distinct blocks")
	target := srcMap.blocks()[1]
	// direct field assignment, not SetYOffset: the viewport's own clamp is
	// against its internal content buffer (populated by SetContent), which
	// nothing here has set yet — target.row is a row of the srcMap's own
	// render, a value the viewport's clamp knows nothing about and would zero
	// out.
	m.layout.viewport.YOffset = target.row

	m.mdPreviewStartAnnotation()

	assert.True(t, m.annot.annotating)
	assert.Equal(t, target.startLine, m.nav.diffCursor,
		"aim must land on the block the highlight currently marks, not the first or last")
}

// TestMdPreviewStartAnnotation_AimPastLastBlock covers the "past the last
// block" case: scrolled all the way to the final block, aim must still
// resolve to it rather than falling off the end of the anchors slice.
func TestMdPreviewStartAnnotation_AimPastLastBlock(t *testing.T) {
	doc := "# Heading\n\nFirst paragraph.\n\nSecond paragraph."
	lines := mdLines(doc)
	m := mdPreviewTestModel(lines)
	m.modes.mdPreview = true

	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned)
	last := srcMap.blocks()[len(srcMap.blocks())-1]
	m.layout.viewport.YOffset = last.row // see the AimMidDocument test for why not SetYOffset

	m.mdPreviewStartAnnotation()

	assert.True(t, m.annot.annotating)
	assert.Equal(t, last.startLine, m.nav.diffCursor,
		"aim scrolled to the last block must anchor there, not lose the block entirely")
}

// TestMdPreviewClickAnnotate_InsideBlockResolvesToIt covers "a click inside a
// block resolves to it": clicking exactly on a block's first row must anchor
// the new annotation to that block's source line.
func TestMdPreviewClickAnnotate_InsideBlockResolvesToIt(t *testing.T) {
	doc := "# Heading\n\nFirst paragraph.\n\nSecond paragraph."
	lines := mdLines(doc)
	m := mdPreviewTestModel(lines)
	m.modes.mdPreview = true

	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned)
	require.GreaterOrEqual(t, len(srcMap.blocks()), 2, "fixture sanity: need at least two distinct blocks")
	target := srcMap.blocks()[1]

	result, _ := m.mdPreviewClickDiff(m.diffTopRow() + target.row)
	got := result.(Model)

	assert.True(t, got.annot.annotating)
	assert.Equal(t, target.startLine, got.nav.diffCursor,
		"a click inside a block must anchor the annotation to that block's source line")
}

// TestMdPreviewClickAnnotate_BelowLastRowResolvesToLastBlock covers "a click
// below the last row resolves to the last block": a click far past every
// rendered row must still land on the document's final block rather than
// being a no-op.
func TestMdPreviewClickAnnotate_BelowLastRowResolvesToLastBlock(t *testing.T) {
	doc := "# Heading\n\nFirst paragraph.\n\nSecond paragraph."
	lines := mdLines(doc)
	m := mdPreviewTestModel(lines)
	m.modes.mdPreview = true

	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned)
	last := srcMap.blocks()[len(srcMap.blocks())-1]

	result, _ := m.mdPreviewClickDiff(100000)
	got := result.(Model)

	assert.True(t, got.annot.annotating)
	assert.Equal(t, last.startLine, got.nav.diffCursor,
		"a click below every block's rows must resolve to the last block")
}

// TestMdPreviewClickAnnotate_UnalignedIsNoOp proves a click cannot start an
// annotation when the map gave up: with no trustworthy row-to-line mapping,
// there is nothing to safely resolve the click against.
func TestMdPreviewClickAnnotate_UnalignedIsNoOp(t *testing.T) {
	lines := mdLines("| a | b |\n|---|---|\n| 1 | 2 |\n\n| c | d |\n|---|---|\n| 3 | 4 |")
	m := mdPreviewTestModel(lines)
	m.modes.mdPreview = true

	_, srcMap := m.mdPreviewBody()
	require.False(t, srcMap.aligned,
		"fixture sanity: two tables separated only by a blank line must fail alignment (see the plan's Technical Details)")

	result, cmd := m.mdPreviewClickDiff(m.diffTopRow())
	got := result.(Model)

	assert.Nil(t, cmd)
	assert.False(t, got.annot.annotating, "a click against an unaligned map must be a no-op")
}

// TestMdPreviewStartAnnotation_RefusalIsNotSilent pins the feedback half of a
// refused preview annotation. Before this, pressing `a` on a document whose
// source map did not align produced a byte-identical frame with every transient
// hint empty — the reader could not tell "this document cannot be anchored"
// from "the key is not bound". outputState/compactState set the precedent for
// saying so in the status bar, and the hint clears on the next key like theirs.
func TestMdPreviewStartAnnotation_RefusalIsNotSilent(t *testing.T) {
	unalignedDoc := "| a | b |\n|---|---|\n| 1 | 2 |\n\n| c | d |\n|---|---|\n| 3 | 4 |"

	base := func(t *testing.T) Model {
		t.Helper()
		m := mdPreviewTestModel(mdLines(unalignedDoc))
		m.modes.mdPreview = true
		_, srcMap := m.mdPreviewBody()
		require.False(t, srcMap.aligned,
			"fixture sanity: two tables separated only by a blank line must fail alignment")
		return m
	}

	t.Run("unaligned document explains itself", func(t *testing.T) {
		m := base(t)
		before := m.statusBarText()

		got := pressKey(t, m, "a")

		require.False(t, got.annot.annotating, "an unaligned document still refuses to anchor")
		assert.NotEmpty(t, got.preview.hint, "a refused annotation must not be silent")
		assert.Contains(t, got.statusBarText(), got.preview.hint, "the hint must reach the status bar")
		assert.NotEqual(t, before, got.statusBarText(), "the status bar must change, or the refusal is invisible")
	})

	t.Run("hint clears on the next key", func(t *testing.T) {
		refused := pressKey(t, base(t), "a")
		require.NotEmpty(t, refused.preview.hint)

		next := pressKey(t, refused, "j")

		assert.Empty(t, next.preview.hint, "the hint is transient, like reload/output/compact")
	})
}

// TestDispatchAction_PreviewConfirmWithTreeFocus_DoesNotMoveViewport is the
// checklist's "enter with tree focus does not move the viewport" case: with
// the tree/TOC pane focused (reachable while previewing, since nothing in
// mdPreviewAllowedActions changes m.layout.focus), ActionConfirm must NOT
// fall through to handleEnterKey's TOC-jump branch — which would realign the
// viewport to a diff-line coordinate the preview render does not have.
//
// It must also not start an annotation from that press: annotate_file (A) is
// gated on diff-pane focus by handleFileAnnotateKey, and the two
// annotation-creating keys have to agree, or `a` silently opens an input on a
// block the reader was not aiming at with a pane they were not looking at.
//
// What it must do instead is TAKE focus, so the second press annotates — the
// same progression handleEnterKey's own paneTree branch gives source view.
// Returning a bare no-op here left `a` and `A` permanently dead in every
// multi-file review, since paneTree is the focus a review starts in and preview
// blocks every focus-moving action.
func TestDispatchAction_PreviewConfirmWithTreeFocus_DoesNotMoveViewport(t *testing.T) {
	// a long document, not the usual three-line fixture: mdPreviewStartAnnotationAt
	// restores the offset via the real SetYOffset, which clamps against the
	// viewport's own content buffer — a short document that fits inside the
	// viewport has nowhere to legitimately scroll to, so the interesting offset
	// (5) needs a document tall enough to make it a real, non-clamped position.
	parts := make([]string, 0, 82)
	parts = append(parts, "# Title", "")
	for range 40 {
		parts = append(parts, "Paragraph text.", "")
	}
	lines := mdLines(strings.Join(parts, "\n"))
	m := mdPreviewTestModel(lines)
	m.modes.mdPreview = true
	m.layout.focus = paneTree
	require.NotNil(t, m.file.mdTOC, "fixture sanity: the TOC jump branch must be reachable for this test to have teeth")
	m.layout.viewport.SetContent(m.renderMarkdownPreview())
	require.Greater(t, m.layout.viewport.TotalLineCount(), m.layout.viewport.Height+5,
		"fixture sanity: the document must render taller than the viewport for offset 5 to be a real, non-clamped position")
	m.layout.viewport.SetYOffset(5)
	require.Equal(t, 5, m.layout.viewport.YOffset, "fixture sanity: the offset must actually take before dispatch")

	model, _ := m.dispatchAction(keymap.ActionConfirm)
	got := model.(Model)

	assert.Equal(t, 5, got.layout.viewport.YOffset,
		"ActionConfirm in preview must not run the TOC jump's viewport realignment")
	assert.False(t, got.annot.annotating,
		"the first ActionConfirm must not annotate with the tree/TOC pane focused, matching annotate_file")
	assert.Equal(t, paneDiff, got.layout.focus,
		"the first ActionConfirm must take focus, or a/A stay dead with no keyboard way back")

	// the second press, on the model the first one returned, annotates: the gate
	// is a self-healing focus step and not the action being dead
	second, _ := got.dispatchAction(keymap.ActionConfirm)
	assert.True(t, second.(Model).annot.annotating,
		"the second ActionConfirm must start an annotation, now that focus has moved to the diff pane")
	assert.Equal(t, 5, second.(Model).layout.viewport.YOffset,
		"starting the annotation must still leave the preview viewport where the reader left it")
}

// TestDispatchAction_PreviewAnnotate_MultiFileReviewThroughRealLoadPath is the
// entry-path test the focus gate needed and did not have. Every other test here
// reaches preview through mdPreviewTestModel, which assigns m.layout.focus =
// paneDiff by hand — the one state in which the gate is invisible. This one
// assigns focus nowhere and builds the model the way a running session does
// (WindowSizeMsg, then filesLoadedMsg, then fileLoadedMsg) for a MULTI-FILE
// review, which is where handleFilesLoaded leaves focus on paneTree: only
// single-file mode flips it to paneDiff (loaders.go).
//
// That is the shape in which `a` was permanently dead: the gate returned
// handled with no state change, and preview blocks toggle_pane / focus_tree /
// focus_diff, so no key could recover. Press `P` then `a` twice and an input
// must open.
func TestDispatchAction_PreviewAnnotate_MultiFileReviewThroughRealLoadPath(t *testing.T) {
	mdDoc := "# Title\n\nFirst paragraph.\n\nSecond paragraph.\n"
	diffs := map[string][]diff.DiffLine{
		"plan.md": mdLines(mdDoc),
		"other.go": {
			{ChangeType: diff.ChangeAdd, Content: "package main", NewNum: 1},
		},
	}
	m := testModel([]string{"plan.md", "other.go"}, diffs)

	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = result.(Model)
	result, _ = m.Update(filesLoadedMsg{entries: []diff.FileEntry{{Path: "plan.md"}, {Path: "other.go"}}})
	m = result.(Model)
	result, _ = m.Update(m.loadFileDiff("plan.md")())
	m = result.(Model)

	require.False(t, m.file.singleFile, "fixture sanity: this must be a multi-file review, where focus stays on the tree")
	require.Equal(t, paneTree, m.layout.focus, "fixture sanity: the real load path must leave focus on the tree pane")
	require.True(t, m.file.markdownPreviewable, "fixture sanity: a full-context markdown file in a multi-file review is previewable")

	m = pressKey(t, m, "P")
	require.True(t, m.modes.mdPreview, "P must turn preview on")

	m = pressKey(t, m, "a")
	assert.Equal(t, paneDiff, m.layout.focus, "the first `a` must take focus")
	assert.False(t, m.annot.annotating, "the first `a` must not annotate yet")

	m = pressKey(t, m, "a")
	assert.True(t, m.annot.annotating,
		"the second `a` must open an annotation input — this is the press that was dead in every multi-file review")
	assert.False(t, m.annot.fileAnnotating, "`a` must start a LINE-level annotation")
}

// TestMdPreviewStartAnnotation_SavedAnnotationMatchesSourceView proves the
// annotation a preview `a` press produces is indistinguishable from one made
// in source view: same File, Line, Type, Comment — created through the exact
// same saveAnnotation/saveComment path, just aimed at a different starting
// cursor.
func TestMdPreviewStartAnnotation_SavedAnnotationMatchesSourceView(t *testing.T) {
	doc := "# Title\n\nSome text."
	lines := mdLines(doc)

	preview := mdPreviewTestModel(lines)
	preview.modes.mdPreview = true
	_, srcMap := preview.mdPreviewBody()
	require.True(t, srcMap.aligned)
	idx := srcMap.blocks()[0].startLine

	preview.mdPreviewStartAnnotationAt(idx)
	require.True(t, preview.annot.annotating)
	preview.annot.input.SetValue("a note on the title")
	preview.saveAnnotation()

	source := mdPreviewTestModel(lines)
	source.nav.diffCursor = idx
	source.startAnnotation()
	source.annot.input.SetValue("a note on the title")
	source.saveAnnotation()

	previewAnns := preview.store.Get("plan.md")
	sourceAnns := source.store.Get("plan.md")
	require.Len(t, previewAnns, 1)
	require.Len(t, sourceAnns, 1)
	assert.Equal(t, sourceAnns[0], previewAnns[0],
		"a preview-created annotation must be indistinguishable from one made in source view")
}

// TestMdPreviewStartAnnotation_TwoItemsInTightList_BothSurvive is the named
// regression test the orchestrator called out explicitly: it is the case that
// rejected the alternative design (a whole tight list as one anchor), so it
// is the case that must prove this one. Annotating two DIFFERENT items of one
// TIGHT bullet list (no blank line between them) through the real creation
// path must leave two distinct annotations in the store, with different Line
// values and both comments intact — not one replacing the other via
// annotation.Store.Add's same-key overwrite.
func TestMdPreviewStartAnnotation_TwoItemsInTightList_BothSurvive(t *testing.T) {
	doc := "- item one\n- item two"
	lines := mdLines(doc)
	m := mdPreviewTestModel(lines)
	m.modes.mdPreview = true

	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned)
	require.Len(t, srcMap.blocks(), 2, "fixture sanity: a tight two-item list must yield two distinct block targets")
	first := srcMap.blocks()[0].startLine
	second := srcMap.blocks()[1].startLine
	require.NotEqual(t, first, second, "fixture sanity: the two items must be distinct source lines")

	m.mdPreviewStartAnnotationAt(first)
	require.True(t, m.annot.annotating)
	m.annot.input.SetValue("comment on item one")
	m.saveAnnotation()

	m.mdPreviewStartAnnotationAt(second)
	require.True(t, m.annot.annotating)
	m.annot.input.SetValue("comment on item two")
	m.saveAnnotation()

	anns := m.store.Get("plan.md")
	require.Len(t, anns, 2, "both list-item annotations must survive as distinct entries, not replace each other")

	byLine := map[int]string{}
	for _, a := range anns {
		byLine[a.Line] = a.Comment
	}
	assert.Equal(t, "comment on item one", byLine[m.diffLineNum(m.file.lines[first])])
	assert.Equal(t, "comment on item two", byLine[m.diffLineNum(m.file.lines[second])])
}

// TestMdPreviewPaintAnnotations_LiveInputVisibleForNewAnnotation proves the
// gap mdPreviewStartAnnotationAt closes: mdPreviewPaintAnnotationsTracked only
// ever iterated the store, so a BRAND NEW annotation (nothing saved yet for
// its line) had no entry for that loop to find, and the input a reader is
// actively typing would never reach the screen until the moment it is saved.
// This is what the mdPreviewLiveInputTarget branch exists to fix.
func TestMdPreviewPaintAnnotations_LiveInputVisibleForNewAnnotation(t *testing.T) {
	doc := "# Title\n\nSome text."
	lines := mdLines(doc)
	m := mdPreviewTestModel(lines)
	m.modes.mdPreview = true

	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned)
	idx := srcMap.blocks()[0].startLine

	m.mdPreviewStartAnnotationAt(idx)
	require.True(t, m.annot.annotating)
	m.annot.input.SetValue("typing now")

	rendered := m.renderMarkdownPreview()

	assert.Contains(t, ansi.Strip(rendered), "typing now",
		"the annotation currently being typed must be visible in the preview before it is saved")
}

// TestDispatchAction_MdPreviewOn_AnnotateFileStartsAnnotation is Task 8's
// allowlist round trip for 'A': pressed through the real Update path (not
// the guard helper in isolation), it must start a file-level annotation the
// same way it does in source view — startFileAnnotation always sets
// diffCursor to -1 and needs no source-map anchor, since a file-level
// annotation's Line is always 0.
func TestDispatchAction_MdPreviewOn_AnnotateFileStartsAnnotation(t *testing.T) {
	lines := mdLines("# Title\n\nSome text.")
	m := mdPreviewTestModel(lines)
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	model := pressKey(t, m, "A")

	assert.True(t, model.annot.annotating, "A must start an annotation while previewing")
	assert.True(t, model.annot.fileAnnotating, "A must start a FILE-level annotation, not a line-level one")
	assert.Equal(t, -1, model.nav.diffCursor, "file-level annotation always targets Line 0 via cursor -1")
}

// TestDispatchAction_MdPreviewOn_AnnotateFileNoOpWhenTreeFocused checks the
// assumption the allowlist doc comment makes explicit: handleFileAnnotateKey
// is gated on the diff pane having focus, in preview exactly as in source
// view, so allowing annotate_file to fall through unmodified needs no
// preview-specific focus guard of its own.
//
// The second half is what makes that gate acceptable rather than a dead key:
// `a` is the recovery, taking focus on its first press, after which `A` works.
// Without it there would be no keyboard route out of tree focus in preview at
// all — toggle_pane, focus_tree and focus_diff are all excluded.
func TestDispatchAction_MdPreviewOn_AnnotateFileNoOpWhenTreeFocused(t *testing.T) {
	lines := mdLines("# Title\n\nSome text.")
	m := mdPreviewTestModel(lines)
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)
	m.layout.focus = paneTree

	model := pressKey(t, m, "A")

	assert.False(t, model.annot.annotating, "A must stay a no-op while the tree pane has focus")

	recovered := pressKey(t, model, "a")
	require.Equal(t, paneDiff, recovered.layout.focus, "`a` must be the way back to diff focus in preview")
	require.False(t, recovered.annot.annotating, "the focus-taking press must not itself annotate")

	afterRecovery := pressKey(t, recovered, "A")
	assert.True(t, afterRecovery.annot.annotating, "A must work once `a` has handed focus to the diff pane")
	assert.True(t, afterRecovery.annot.fileAnnotating, "and it must still be a FILE-level annotation")
}

// TestDispatchAction_MdPreviewOn_AnnotateFileSavedAnnotationPaintsAboveRowZero
// is the task's named checklist assertion: a saved file-level annotation must
// paint above row 0, pushing every block down by its own row count, driven
// through the real 'A' dispatch path rather than by seeding the store
// directly (that shape is already covered by
// TestMdPreviewPaintAnnotations_FileLevelAlwaysAtTop).
func TestDispatchAction_MdPreviewOn_AnnotateFileSavedAnnotationPaintsAboveRowZero(t *testing.T) {
	lines := mdLines("# Title\n\nSome text.")
	m := mdPreviewTestModel(lines)
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	model := pressKey(t, m, "A")
	require.True(t, model.annot.annotating)
	require.True(t, model.annot.fileAnnotating)
	model.annot.input.SetValue("file-level note")
	model.saveAnnotation()

	body, srcMap := model.mdPreviewBody()
	require.True(t, srcMap.aligned)
	require.NotEmpty(t, srcMap.blocks())
	assert.Positive(t, srcMap.blocks()[0].row,
		"the first block must be pushed past row 0 by the file-level annotation ahead of it")

	rows := strings.Split(body, "\n")
	assert.Contains(t, ansi.Strip(rows[0]), "file-level note",
		"a file-level annotation must paint at row 0, above every block")
}

// TestMdPreviewPaintAnnotations_FileLevelLiveInputVisibleForNewAnnotation is
// the file-level counterpart to
// TestMdPreviewPaintAnnotations_LiveInputVisibleForNewAnnotation. Before Task
// 8 this path was unreachable in a running TUI (annotate_file was blocked),
// so the gap never showed: mdPreviewPaintAnnotationsTracked's early return
// only checked the store and the line-level live-input target, neither of
// which sees a brand-new file-level annotation (nothing saved yet for this
// file) — the text a reader is actively typing into the file-level box
// stayed invisible until the moment it was saved. Fixed by checking
// m.annot.fileAnnotating directly alongside the store/line-level checks.
func TestMdPreviewPaintAnnotations_FileLevelLiveInputVisibleForNewAnnotation(t *testing.T) {
	doc := "# Title\n\nSome text."
	lines := mdLines(doc)
	m := mdPreviewTestModel(lines)
	m.modes.mdPreview = true

	require.Equal(t, 0, m.store.Count(), "fixture sanity: no existing annotations for this file")
	m.startFileAnnotation()
	require.True(t, m.annot.annotating)
	require.True(t, m.annot.fileAnnotating)
	m.annot.input.SetValue("typing a file note")

	rendered := m.renderMarkdownPreview()

	assert.Contains(t, ansi.Strip(rendered), "typing a file note",
		"the file-level annotation currently being typed must be visible in preview before it is saved")
}

// TestDispatchAction_MdPreviewOn_FlushOutputWritesFile is Task 8's allowlist
// round trip for 'O': handleFlushOutput touches neither m.nav.diffCursor nor
// the viewport, so it needs no preview-specific handling — creating
// annotations in preview without a way to flush them would be half a
// feature.
func TestDispatchAction_MdPreviewOn_FlushOutputWritesFile(t *testing.T) {
	lines := mdLines("# Title\n\nSome text.")
	m := mdPreviewTestModel(lines)
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 0, Type: "", Comment: "file note"})
	path := filepath.Join(t.TempDir(), "out.md")
	m.cfg.outputPath = path

	model := pressKey(t, m, "O")

	assert.FileExists(t, path, "O must flush annotations to the output file while previewing")
	assert.True(t, model.modes.mdPreview, "flushing output must not exit preview")
}

// TestMdPreviewStartAnnotation_ConsecutiveThematicBreaks_EachKeepsItsComment is
// the regression for the block walk resolving two blocks onto one source line.
// On "text\n\n---\n\n---\n\nmore\n" the second thematic break used to anchor to
// line 1 — the paragraph's own line — because the lower-bound walk bubbled past
// the first, position-less break instead of resolving it. The visible damage was
// not a misplaced comment: two blocks on one line means two annotations sharing
// annotation.Store's (Line, Type) key, and Add replaces on a collision, so
// annotating all four blocks in document order left three annotations, with the
// paragraph's comment destroyed by the first rule's.
func TestMdPreviewStartAnnotation_ConsecutiveThematicBreaks_EachKeepsItsComment(t *testing.T) {
	doc := "text\n\n---\n\n---\n\nmore\n"
	m := mdPreviewTestModel(mdLines(doc))
	m.modes.mdPreview = true

	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "this document must still align after the fix")
	blocks := srcMap.blocks()
	require.Len(t, blocks, 4, "paragraph, hr, hr, paragraph")

	seen := map[int]bool{}
	for i, b := range blocks {
		require.False(t, seen[b.startLine], "block %d (%s) reuses source line %d", i, b.kind, b.startLine)
		seen[b.startLine] = true

		m.mdPreviewStartAnnotationAt(b.startLine)
		require.True(t, m.annot.annotating, "block %d must accept an annotation", i)
		m.annot.input.SetValue(fmt.Sprintf("comment %d", i))
		m.saveAnnotation()
	}

	anns := m.store.Get("plan.md")
	require.Len(t, anns, 4, "one annotation per block: none may overwrite another")
	for i, b := range blocks {
		found := false
		for _, a := range anns {
			if a.Line == m.diffLineNum(m.file.lines[b.startLine]) && a.Comment == fmt.Sprintf("comment %d", i) {
				found = true
			}
		}
		assert.True(t, found, "block %d (%s) lost its comment", i, b.kind)
	}
}

// TestMdPreviewClickAnnotate_RefusedGuards covers the two states a preview
// click must leave alone, matching its siblings: a file the render path will
// not preview (the same markdownPreviewable gate panMarkdownPreview and
// scrollMarkdownPreview carry), and an annotation input already open — where
// startAnnotation's clearPendingInputState plus a fresh newAnnotationInput
// would discard whatever the reader had typed.
func TestMdPreviewClickAnnotate_RefusedGuards(t *testing.T) {
	doc := "# Heading\n\nFirst paragraph.\n\nSecond paragraph."
	base := func(t *testing.T) Model {
		t.Helper()
		m := mdPreviewTestModel(mdLines(doc))
		m.modes.mdPreview = true
		_, srcMap := m.mdPreviewBody()
		require.True(t, srcMap.aligned, "fixture sanity")
		return m
	}

	t.Run("not previewable", func(t *testing.T) {
		m := base(t)
		m.file.markdownPreviewable = false
		result, cmd := m.mdPreviewClickDiff(m.diffTopRow())
		assert.Nil(t, cmd)
		assert.False(t, result.(Model).annot.annotating,
			"a click must not run the markdown pipeline over a file the render path will not preview")
	})

	t.Run("annotation input already open", func(t *testing.T) {
		m := base(t)
		_, srcMap := m.mdPreviewBody()
		m.mdPreviewStartAnnotationAt(srcMap.blocks()[0].startLine)
		require.True(t, m.annot.annotating)
		m.annot.input.SetValue("half-typed note")

		result, cmd := m.mdPreviewClickDiff(m.diffTopRow() + srcMap.blocks()[1].row)
		assert.Nil(t, cmd)
		got := result.(Model)
		assert.Equal(t, srcMap.blocks()[0].startLine, got.nav.diffCursor, "the open input must keep its target")
		assert.Equal(t, "half-typed note", got.annot.input.Value(), "a stray click must not discard typed text")
	})
}

// TestMdPreviewHighlight_PannedRowReachesPaneEdge covers the ragged-bar case:
// once the frame is panned, cutMdPreviewLine has cut each row at wherever its
// own content ended, so a row that does not continue to the right is shorter
// than the pane and the highlight would stop short of the edge.
//
// The fixture has to be WIDER than the pane or the scenario never happens:
// applyMdPreviewScroll returns the render untouched when the widest row already
// fits (offset == 0 && widest <= cutWidth), and the widest row of highlightDoc
// at width 40 is 38 — so this test used to pass on the padTo branch alone,
// never reaching a ragged cut. A fenced block of 120 columns is what makes the
// cut real, while the marked block stays a short one that needs the padding.
func TestMdPreviewHighlight_PannedRowReachesPaneEdge(t *testing.T) {
	m := mdPreviewStyledModel(t, widePanDoc)
	m.layout.viewport.Width = 40
	m.layout.scrollX = 5

	body, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity")
	require.Greater(t, m.mdPreviewWidestRow(body), m.mdPreviewCutWidth(),
		"fixture sanity: the document must be wider than the pane, or applyMdPreviewScroll returns the render untouched")
	bi := m.mdPreviewHighlightAnchor(srcMap)
	require.GreaterOrEqual(t, bi, 0, "fixture sanity: a block must be marked")

	markedRow := srcMap.blocks()[bi].row
	cut := strings.Split(m.applyMdPreviewScroll(body), "\n")
	require.Less(t, ansi.StringWidth(cut[markedRow]), m.mdPreviewCutWidth(),
		"fixture sanity: the marked row must come out of the cut SHORT, or there is no ragged bar to pad")

	rows := strings.Split(m.mdPreviewFinalRender(), "\n")
	bg := mdPreviewHighlightBg(m)
	row := rows[markedRow]
	require.Contains(t, row, bg, "fixture sanity: the marked row must carry the highlight")
	assert.Equal(t, m.mdPreviewCutWidth(), ansi.StringWidth(row),
		"a panned highlighted row must be padded out so the bar reaches the pane edge")
}
