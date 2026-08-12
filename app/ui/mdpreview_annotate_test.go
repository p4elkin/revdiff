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
// content (a long line in a code fence, here) already held it.
//
// The fixture is deliberately NOT a mermaid diagram. It was one until the
// mermaid node-label wrapping from `mdpreview_wrap.go` landed on master: that
// pass re-renders overflowing art narrower until it fits the pane, so a
// diagram is now the one kind of content that cannot be relied on to stay
// wider than the pane. A code fence is never reflowed, so it holds the
// invariant this test is actually about.
func TestMdPreviewMaxOffset_UnchangedByAnnotationRows(t *testing.T) {
	doc := "# Title\n\n```\n" + strings.Repeat("wide", 40) + "\n```\n\nClosing text."
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
// block the block cursor currently marks — here the cursor is steered onto the
// second of three blocks, and the resolved StartLine must be that block's, not
// the first or the last.
func TestMdPreviewStartAnnotation_AimMidDocument(t *testing.T) {
	doc := "# Heading\n\nFirst paragraph.\n\nSecond paragraph.\n\nThird paragraph."
	lines := mdLines(doc)
	m := mdPreviewTestModel(lines)
	m.modes.mdPreview = true

	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned)
	require.GreaterOrEqual(t, len(srcMap.blocks()), 3, "fixture sanity: need at least three distinct blocks")
	target := srcMap.blocks()[1]
	m.setMdPreviewCursorToBlock(1)

	m.mdPreviewStartAnnotation()

	assert.True(t, m.annot.annotating)
	assert.Equal(t, target.startLine, m.nav.diffCursor,
		"aim must land on the block the cursor marks, not the first or last")
}

// TestMdPreviewStartAnnotation_AimPastLastBlock covers the "past the last
// block" case: with the cursor on the final block, aim must still resolve to it
// rather than falling off the end of the anchors slice.
func TestMdPreviewStartAnnotation_AimPastLastBlock(t *testing.T) {
	doc := "# Heading\n\nFirst paragraph.\n\nSecond paragraph."
	lines := mdLines(doc)
	m := mdPreviewTestModel(lines)
	m.modes.mdPreview = true

	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned)
	last := srcMap.blocks()[len(srcMap.blocks())-1]
	m.setMdPreviewCursorToBlock(len(srcMap.blocks()) - 1)

	m.mdPreviewStartAnnotation()

	assert.True(t, m.annot.annotating)
	assert.Equal(t, last.startLine, m.nav.diffCursor,
		"aim on the last block must anchor there, not lose the block entirely")
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

	t.Run("keyboard aim is refused too", func(t *testing.T) {
		m := base(t)
		m.file.markdownPreviewable = false
		before := m.preview

		assert.Nil(t, m.mdPreviewStartAnnotation())
		assert.False(t, m.annot.annotating,
			"`a` must not run the markdown pipeline over a file the render path will not preview")
		assert.Equal(t, before, m.preview, "and must not seed a cursor against a map of a document not on screen")
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
	m.setMdPreviewCursorToBlock(0) // nothing is marked until the reader steers the cursor
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

// mdPaintExpandedFixture is mdExpandFixture already run through the expansion
// pass, i.e. exactly what the annotation painter sees in production once a block
// is expanded: the middle paragraph drawn as its three source lines, and the map
// in that render's own rows.
//
// The expanded render is:
//
//	row 0  "  Heading"
//	row 1  ""
//	row 2  "para one"     <- lineIdx 2, Line 3
//	row 3  "para two"     <- lineIdx 3, Line 4
//	row 4  "para three"   <- lineIdx 4, Line 5
//	row 5  ""
//	row 6  "  tail"
//	row 7  ""
func mdPaintExpandedFixture() (rendered string, lines []diff.DiffLine, sm mdPreviewSourceMap) {
	base, lines, sm := mdExpandFixture()
	rendered, sm = mdPreviewExpandBlock(base, sm, 1, lines, "    ")
	return rendered, lines, sm
}

func TestMdPreviewSpliceRow_CollapsedDocumentSplicesAtTheBlockEndRow(t *testing.T) {
	_, _, sm := mdExpandFixture()

	tests := []struct {
		name      string
		idx       int
		idxOK     bool
		wantRow   int
		wantBlock int
	}{
		{"a block's own start line", 0, true, 1, 0},
		{"a line inside a block's span", 3, true, 4, 1},
		{"a line no block starts on", 5, true, 4, 1},
		{"a line the file no longer has", 0, false, 6, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row, block := sm.spliceRow(tt.idx, tt.idxOK)
			assert.Equal(t, tt.wantRow, row)
			assert.Equal(t, tt.wantBlock, block)
		})
	}
}

func TestMdPreviewSpliceRow_ExpandedBlockSplicesAtTheRawLineItself(t *testing.T) {
	_, _, sm := mdPaintExpandedFixture()

	// a slice, not a map: a map range is unordered, so a failure would report a
	// different case on every run
	for _, tc := range []struct{ idx, wantRow int }{{2, 2}, {3, 3}, {4, 4}} {
		row, block := sm.spliceRow(tc.idx, true)
		assert.Equal(t, tc.wantRow, row,
			"a comment on raw line %d belongs under that line, not at the block's end", tc.idx)
		assert.Equal(t, 1, block, "the block is the line anchor's own, so the runs stay grouped by ascending row")
	}

	row, block := sm.spliceRow(6, true)
	assert.Equal(t, 7, row, "a line outside the expanded block still splices at its own block's endRow")
	assert.Equal(t, 2, block)
}

func TestMdPreviewPaintAnnotations_ExpandedBlockCommentPaintsUnderItsRawLine(t *testing.T) {
	rendered, lines, sm := mdPaintExpandedFixture()
	m := mdPreviewTestModel(lines)
	const comment = "a note on the middle line"
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 4, Type: " ", Comment: comment}) // "para two"

	painted, gotMap := m.mdPreviewPaintAnnotationsTracked(rendered, sm)

	rows := strings.Split(ansi.Strip(painted), "\n")
	require.Len(t, gotMap.annots, 1)
	got := gotMap.annots[0]
	require.Contains(t, rows[got.row], comment, "the recorded anchor must name the row the comment was painted on")
	assert.Equal(t, "para two", rows[got.row-1], "the comment belongs under the line it was written against")
	assert.Equal(t, "para three", rows[got.endRow+1], "the rest of the block continues below the comment")
	assert.Equal(t, 1, got.block)
}

func TestMdPreviewPaintAnnotations_SpliceInsideABlockShiftsRowAndEndRowApart(t *testing.T) {
	rendered, lines, sm := mdPaintExpandedFixture()
	m := mdPreviewTestModel(lines)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 4, Type: " ", Comment: "a note"}) // raw row 3

	_, gotMap := m.mdPreviewPaintAnnotationsTracked(rendered, sm)

	require.Len(t, gotMap.annots, 1)
	n := gotMap.annots[0].endRow - gotMap.annots[0].row + 1 // rows the comment took
	assert.Equal(t, [2]int{0, 1}, [2]int{gotMap.anchors[0].row, gotMap.anchors[0].endRow},
		"a block above the splice never moves")
	assert.Equal(t, [2]int{2, 5 + n}, [2]int{gotMap.anchors[1].row, gotMap.anchors[1].endRow},
		"the splice sits inside this block's span, so its row and endRow move by different amounts")
	assert.Equal(t, [2]int{6 + n, 7 + n}, [2]int{gotMap.anchors[2].row, gotMap.anchors[2].endRow})
	assert.Equal(t, 2, sm.anchors[1].row, "the caller's map is never edited in place")
}

func TestMdPreviewPaintAnnotations_CarriesLineAnchorsShifted(t *testing.T) {
	rendered, lines, sm := mdPaintExpandedFixture()
	m := mdPreviewTestModel(lines)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 4, Type: " ", Comment: "a note"}) // raw row 3

	painted, gotMap := m.mdPreviewPaintAnnotationsTracked(rendered, sm)

	require.Len(t, gotMap.lines, 3, "dropping the line anchors would take every raw-line stop away the moment a comment exists")
	require.Len(t, gotMap.annots, 1)
	n := gotMap.annots[0].endRow - gotMap.annots[0].row + 1
	assert.Equal(t, []int{2, 3, 4 + n}, []int{gotMap.lines[0].row, gotMap.lines[1].row, gotMap.lines[2].row},
		"only the raw lines BELOW the splice move")
	assert.Equal(t, []int{2, 3, 4}, []int{gotMap.lines[0].lineIdx, gotMap.lines[1].lineIdx, gotMap.lines[2].lineIdx})

	rows := strings.Split(ansi.Strip(painted), "\n")
	for _, la := range gotMap.lines {
		assert.Equal(t, lines[la.lineIdx].Content, rows[la.row],
			"a shifted line anchor must still name the row its own source text is on")
	}
}

func TestMdPreviewPaintAnnotations_ExpandedBlockOrdCountsAcrossSplicePoints(t *testing.T) {
	rendered, lines, sm := mdPaintExpandedFixture()
	m := mdPreviewTestModel(lines)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 3, Type: " ", Comment: "first comment"}) // raw row 2
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 5, Type: " ", Comment: "second comment"})

	painted, gotMap := m.mdPreviewPaintAnnotationsTracked(rendered, sm)

	require.Len(t, gotMap.annots, 2)
	assert.Equal(t, []int{0, 1}, []int{gotMap.annots[0].ord, gotMap.annots[1].ord},
		"ord is the position among the BLOCK's annotations, so it must keep counting across splice points")
	assert.Equal(t, []int{1, 1}, []int{gotMap.annots[0].block, gotMap.annots[1].block})
	assert.Less(t, gotMap.annots[0].row, gotMap.annots[1].row, "annots must stay row-ascending for stops()")

	rows := strings.Split(ansi.Strip(painted), "\n")
	assert.Equal(t, "para one", rows[gotMap.annots[0].row-1])
	assert.Equal(t, "para three", rows[gotMap.annots[1].row-1])
}

func TestMdPreviewPaintAnnotations_ExpandedStopsInterleaveLinesAndComments(t *testing.T) {
	rendered, lines, sm := mdPaintExpandedFixture()
	m := mdPreviewTestModel(lines)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 3, Type: " ", Comment: "first comment"}) // raw row 2
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 5, Type: " ", Comment: "second comment"})

	_, gotMap := m.mdPreviewPaintAnnotationsTracked(rendered, sm)

	refs := make([]mdPreviewStopRef, 0, 6)
	rows := make([]int, 0, 6)
	for _, s := range gotMap.stops() {
		refs = append(refs, s.ref)
		rows = append(rows, s.row)
	}
	assert.Equal(t, []mdPreviewStopRef{
		{block: 0},
		{block: 1, onLine: true, line: 2},
		{block: 1, onAnnot: true, annot: 0},
		{block: 1, onLine: true, line: 3},
		{block: 1, onLine: true, line: 4},
		{block: 1, onAnnot: true, annot: 1},
		{block: 2},
	}, refs, "a comment sits between the raw line it belongs to and the next one")
	assert.IsIncreasing(t, rows, "the merged stop list must stay row-ascending")
}

func TestMdPreviewPaintAnnotations_LiveInputPaintsUnderTheExpandedRawLine(t *testing.T) {
	rendered, lines, sm := mdPaintExpandedFixture()
	m := mdPreviewTestModel(lines)
	m.modes.mdPreview = true

	m.mdPreviewStartAnnotationAt(3) // "para two", nothing in the store for it yet
	require.True(t, m.annot.annotating)
	m.annot.input.SetValue("typing now")

	painted, _ := m.mdPreviewPaintAnnotationsTracked(rendered, sm)

	rows := strings.Split(ansi.Strip(painted), "\n")
	at, found := 0, false
	for i, r := range rows {
		if strings.Contains(r, "typing now") {
			at, found = i, true
			break
		}
	}
	require.True(t, found, "the input being typed must be visible before it is saved")
	require.GreaterOrEqual(t, at, 1, "and must be painted under a row, never as the document's first")
	assert.Equal(t, "para two", rows[at-1],
		"watching the text you type detach from the line you aimed at is what per-line splicing exists to prevent")
}

// TestMdPreviewStartAnnotation_OnARawLineTargetsThatExactLine is the payoff of
// the whole feature: with a block drawn as its source, `a` comments on the
// source line the cursor is on rather than on the block around it. The fixture
// aims at the block's SECOND line, so aiming at the block would visibly land
// somewhere else.
func TestMdPreviewStartAnnotation_OnARawLineTargetsThatExactLine(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewCursorToBlock(1)
	m.mdPreviewToggleRaw()
	m.moveMdPreviewCursor(1)
	require.Equal(t, mdPreviewStopRef{block: 1, onLine: true, line: 3}, mustMdPreviewRef(t, m),
		"fixture sanity: the cursor must be on the block's second raw line")

	m.mdPreviewStartAnnotation()

	require.True(t, m.annot.annotating, "`a` on a raw line must open an input")
	assert.False(t, m.annot.fileAnnotating, "a line-level one")
	assert.Equal(t, 3, m.nav.diffCursor, "aimed at the raw line itself, not at the block's start line")

	m.annot.input.SetValue("on alpha line two")
	m.saveAnnotation()

	got := m.store.Get("plan.md")
	require.Len(t, got, 1)
	assert.Equal(t, 4, got[0].Line, "the comment must carry the raw line's own 1-based line number")
	assert.Equal(t, 1, m.mdPreviewExpandedBlock(), "and annotating must not collapse the block under the reader")
}

// TestMdPreviewStartAnnotation_BlockAndFirstRawLineAreOneAnnotation pins the
// first of section 3's two properties: `a` on a collapsed block and `a` on the
// first raw line of that same block resolve to the SAME source line, so
// Store.Add replaces rather than adding a second comment meaning the same thing.
// There is no way to end up with two.
func TestMdPreviewStartAnnotation_BlockAndFirstRawLineAreOneAnnotation(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewCursorToBlock(1)

	m.mdPreviewStartAnnotation()
	require.True(t, m.annot.annotating)
	blockTarget := m.nav.diffCursor
	m.annot.input.SetValue("from the block")
	m.saveAnnotation()
	require.Equal(t, 1, m.store.Count())

	m.mdPreviewToggleRaw()
	require.Equal(t, mdPreviewStopRef{block: 1, onLine: true, line: blockTarget}, mustMdPreviewRef(t, m),
		"fixture sanity: the block's first raw line is its own start line")

	m.mdPreviewStartAnnotation()

	require.True(t, m.annot.annotating)
	assert.Equal(t, "from the block", m.annot.input.Value(),
		"`a` on the first raw line must open the comment the block-level `a` already put there")
	m.annot.input.SetValue("from the raw line")
	m.saveAnnotation()

	got := m.store.Get("plan.md")
	require.Len(t, got, 1, "both routes resolve to one (Line, Type), so there can only ever be one comment")
	assert.Equal(t, "from the raw line", got[0].Comment, "the second one replaced the first")
}

// TestMdPreviewStartAnnotation_RawLineAnnotationMatchesSourceView pins the
// second property: a comment made on a raw line is byte-identical in the store —
// and so in the -o output — to one made on the same line with preview off. The
// feature adds no save code at all; it only changes what the diff cursor is
// aimed at before the ordinary path runs.
func TestMdPreviewStartAnnotation_RawLineAnnotationMatchesSourceView(t *testing.T) {
	const comment = "a note on alpha line two"

	preview := toggleRawModel(t)
	preview.setMdPreviewCursorToBlock(1)
	preview.mdPreviewToggleRaw()
	preview.moveMdPreviewCursor(1)
	ref := mustMdPreviewRef(t, preview)
	require.True(t, ref.onLine, "fixture sanity: the cursor must be on a raw source line")

	preview.mdPreviewStartAnnotation()
	require.True(t, preview.annot.annotating)
	preview.annot.input.SetValue(comment)
	preview.saveAnnotation()

	source := mdPreviewTestModel(mdLines(toggleRawDoc))
	source.nav.diffCursor = ref.line
	source.startAnnotation()
	source.annot.input.SetValue(comment)
	source.saveAnnotation()

	previewAnns := preview.store.Get("plan.md")
	sourceAnns := source.store.Get("plan.md")
	require.Len(t, previewAnns, 1)
	require.Len(t, sourceAnns, 1)
	assert.Equal(t, sourceAnns[0], previewAnns[0],
		"an annotation made on a raw line must be indistinguishable from one made in source view")
}

// TestMdPreviewDeleteAnnotation_StaysOnTheRawLineInsideAnExpandedBlock is `d`
// inside an expanded block: the comment goes, the expansion stays, and the
// cursor lands on the raw line the comment was attached to. Landing on the
// block's own stop instead would collapse it and reflow the document under a
// reader who only deleted a comment.
func TestMdPreviewDeleteAnnotation_StaysOnTheRawLineInsideAnExpandedBlock(t *testing.T) {
	m := toggleRawModel(t)
	annotateLine(m, 4, "on alpha line two") // store Line is 1-based: source index 3
	m.setMdPreviewCursorToBlock(1)
	m.mdPreviewToggleRaw()
	m.moveMdPreviewCursor(1) // the second raw line
	m.moveMdPreviewCursor(1) // the comment spliced under it
	require.True(t, mustMdPreviewRef(t, m).onAnnot, "fixture sanity: the cursor must be on the comment")

	m.mdPreviewDeleteAnnotation()

	assert.Equal(t, 0, m.store.Count(), "the selected comment must be gone")
	assert.Equal(t, 1, m.mdPreviewExpandedBlock(), "deleting a comment must not collapse the block")
	ref := mustMdPreviewRef(t, m)
	assert.Equal(t, mdPreviewStopRef{block: 1, onLine: true, line: 3}, ref,
		"the cursor must land on the raw line the deleted comment was attached to")
	_, sm := m.mdPreviewBody()
	_, ok := sm.stopAt(ref)
	assert.True(t, ok, "and that stop must really exist in the repainted map")
}

// TestMdPreviewDeleteAnnotation_FallsBackToTheBlocksFirstRawLine covers
// lineStopFor's fallback arm: the deleted comment sat on a source line that
// paints no stoppable raw row, so there is no "the line it was attached to" to
// return to and the block's first raw line answers instead.
func TestMdPreviewDeleteAnnotation_FallsBackToTheBlocksFirstRawLine(t *testing.T) {
	// a paragraph whose middle source line is blank: it paints a row but gets no
	// anchor, so a comment on it is inside the block yet on no raw-line stop.
	m := mdPreviewStyledModel(t, "# Title\n\nAlpha one.\nAlpha two.\n\ntail\n")
	m.file.lines[3].Content = "   "
	annotateLine(m, 4, "on the blank source line") // store Line is 1-based: index 3
	m.setMdPreviewCursorToBlock(1)
	m.mdPreviewToggleRaw()
	require.Equal(t, 1, m.mdPreviewExpandedBlock(), "fixture sanity: the paragraph must expand")

	_, sm := m.mdPreviewBody()
	require.Len(t, sm.lines, 1, "fixture sanity: only the non-blank source line gets an anchor")
	m.moveMdPreviewCursor(1) // step onto the comment, which keeps the block expanded
	require.True(t, mustMdPreviewRef(t, m).onAnnot, "fixture sanity: the cursor must be on the comment")

	m.mdPreviewDeleteAnnotation()

	assert.Equal(t, 0, m.store.Count())
	assert.Equal(t, 1, m.mdPreviewExpandedBlock(), "deleting must not collapse the block")
	assert.Equal(t, mdPreviewStopRef{block: 1, onLine: true, line: 2}, mustMdPreviewRef(t, m),
		"with no stoppable row for the comment's own line, the block's first raw line answers")
}

// TestMdPreviewDeleteAnnotation_StoreNoLongerHoldsIt: the cursor names a stop
// whose annotation is already gone from the store. Nothing is removed, so `d`
// says so rather than repainting an unchanged frame silently.
func TestMdPreviewDeleteAnnotation_StoreNoLongerHoldsIt(t *testing.T) {
	m := stopsModel(t)
	annotateLine(m, 3, "on alpha")
	_, sm := m.mdPreviewBody()
	stop, ok := sm.stopAt(mdPreviewStopRef{block: 1, onAnnot: true, annot: 0})
	require.True(t, ok, "fixture sanity: the comment must be a stop")
	m.setMdPreviewCursorRef(stop.ref)
	require.True(t, m.store.Delete("plan.md", stop.line, stop.changeType), "removed behind the cursor's back")

	cmd := m.mdPreviewDeleteAnnotation()

	assert.Nil(t, cmd)
	assert.Equal(t, mdPreviewDeleteNeedsAnnotationHint, m.preview.hint)
}

// TestMdPreviewClickDiff_OnANonAnchoredRowOfAnExpandedBlockCollapsesIt pins the
// accepted rough edge documented on mdPreviewClickDiff: a click on a row of an
// expanded block that carries no line anchor — a blank source line, or a comment
// spliced between raw lines — falls through to anchorAtRow and collapses.
func TestMdPreviewClickDiff_OnANonAnchoredRowOfAnExpandedBlockCollapsesIt(t *testing.T) {
	m := mdPreviewStyledModel(t, "# Title\n\nAlpha one.\nAlpha two.\n\ntail\n")
	m.file.lines[3].Content = "   " // paints a row, gets no anchor
	m.setMdPreviewCursorToBlock(1)
	m.mdPreviewToggleRaw()
	_, sm := m.mdPreviewBody()
	require.Len(t, sm.lines, 1, "fixture sanity: only the non-blank source line gets an anchor")
	blank := sm.lines[0].row + 1

	result, _ := m.mdPreviewClickDiff(blank - m.layout.viewport.YOffset + m.diffTopRow())

	got := result.(Model)
	assert.Equal(t, -1, got.mdPreviewExpandedBlock(), "a click the map cannot resolve to a line still collapses")
	assert.Equal(t, mdPreviewStopRef{block: 1}, mustMdPreviewRef(t, got), "and lands on the block around it")
}

// TestMdPreviewClickDiff_OnARawLineAnnotatesThatLine is the mouse half of `a` on
// a raw line: the clicked row resolves to the source line painted on it, before
// the fallback that resolves a row to the block around it.
func TestMdPreviewClickDiff_OnARawLineAnnotatesThatLine(t *testing.T) {
	m := toggleRawModel(t)
	m.setMdPreviewCursorToBlock(1)
	m.mdPreviewToggleRaw()
	_, sm := m.mdPreviewBody()
	require.Len(t, sm.lines, 2, "fixture sanity: the paragraph paints two raw rows")
	target := sm.lines[1]

	result, _ := m.mdPreviewClickDiff(target.row - m.layout.viewport.YOffset + m.diffTopRow())

	got := result.(Model)
	assert.Equal(t, target.lineIdx, got.nav.diffCursor, "a click on a raw row must annotate the line it hit")
	assert.True(t, got.annot.annotating, "and open an input on it, as a click always does in preview")
	assert.Equal(t, 1, got.mdPreviewExpandedBlock(), "clicking inside the block must not collapse it")
	assert.Equal(t, mdPreviewStopRef{block: 1, onLine: true, line: target.lineIdx}, mustMdPreviewRef(t, got),
		"the click leaves the cursor on the line it hit, so a following j/k continues from there")
}

// TestMdPreviewStartAnnotation_OnAnAnnotationStopEditsIt is the `a` rule: the
// selected annotation is the target, so its current text is pre-filled and
// saving REPLACES it rather than leaving a second comment beside it. This is the
// diff pane's own edit path reused — Store.Add replaces on a (File, Line, Type)
// collision — reached by aiming at the annotation's own line instead of the
// block's.
//
// The fixture puts the annotation on block 1's SECOND source line, so aiming at
// the block would visibly land somewhere else.
func TestMdPreviewStartAnnotation_OnAnAnnotationStopEditsIt(t *testing.T) {
	m := stopsModel(t)
	annotateLine(m, 4, "the existing note") // block 1's second line, not its start
	_, srcMap := m.mdPreviewBody()
	require.NotEqual(t, 3, srcMap.blocks()[1].startLine, "fixture sanity: the annotation is not on the block's start")

	m.setMdPreviewCursorRef(mdPreviewStopRef{block: 1, onAnnot: true, annot: 0})
	m.mdPreviewStartAnnotation()

	require.True(t, m.annot.annotating, "`a` on an annotation must open an input")
	assert.Equal(t, 3, m.nav.diffCursor, "aimed at the annotation's own source line (Line 4 -> index 3)")
	assert.Equal(t, "the existing note", m.annot.input.Value(), "the input must be pre-filled with the current text")

	m.annot.input.SetValue("the edited note")
	m.saveAnnotation()

	got := m.store.Get("plan.md")
	require.Len(t, got, 1, "editing must replace the annotation, never add a second one beside it")
	assert.Equal(t, 4, got[0].Line, "on the same line")
	assert.Equal(t, "the edited note", got[0].Comment, "with the new text")
}

// TestMdPreviewStartAnnotation_EditKeepsAMultiLineAnnotation pins the one part
// of the edit path that is not "type over it": a comment containing newlines
// cannot go through the textinput at all (its sanitizer flattens them), so
// startAnnotation stashes it and Enter on an empty input preserves it. Editing
// from preview must inherit that, or opening `a` on a multi-line comment and
// pressing Enter would silently blank it.
func TestMdPreviewStartAnnotation_EditKeepsAMultiLineAnnotation(t *testing.T) {
	const multi = "first line of the note\nsecond line of the note"
	m := stopsModel(t)
	annotateLine(m, 4, multi)

	m.setMdPreviewCursorRef(mdPreviewStopRef{block: 1, onAnnot: true, annot: 0})
	m.mdPreviewStartAnnotation()

	require.True(t, m.annot.annotating)
	assert.Empty(t, m.annot.input.Value(), "a multi-line comment must not be flattened into the input")
	assert.Equal(t, multi, m.annot.existingMultiline, "it must be stashed for the editor key and for Enter")

	m.saveAnnotation() // Enter on an empty input

	got := m.store.Get("plan.md")
	require.Len(t, got, 1)
	assert.Equal(t, multi, got[0].Comment, "confirming an empty input must leave the multi-line text unchanged")
}

// TestMdPreviewStartAnnotation_OnTheFileLevelStopEditsTheFileAnnotation is the
// same rule for the one annotation with no diff line behind it: Line 0 cannot be
// aimed at with the diff cursor, so it takes the file-level input — the same call
// `A` makes, carrying the same pre-fill.
func TestMdPreviewStartAnnotation_OnTheFileLevelStopEditsTheFileAnnotation(t *testing.T) {
	m := stopsModel(t)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 0, Type: "", Comment: "about the whole file"})
	m.setMdPreviewCursorRef(mdPreviewStopRef{block: mdPreviewFileStopBlock, onAnnot: true})

	m.mdPreviewStartAnnotation()

	require.True(t, m.annot.annotating)
	assert.True(t, m.annot.fileAnnotating, "it must open the file-level input, not a line-level one")
	assert.Equal(t, "about the whole file", m.annot.input.Value(), "pre-filled with the existing file annotation")

	m.annot.input.SetValue("about the whole file, revised")
	m.saveAnnotation()

	got := m.store.Get("plan.md")
	require.Len(t, got, 1, "the file-level annotation must be replaced, not duplicated")
	assert.Equal(t, "about the whole file, revised", got[0].Comment)
}

// TestMdPreviewStartAnnotation_OnABlockStopStillTargetsTheBlock is the
// unchanged half: a block stop aims at the block's own start line, whether or
// not the block carries annotations elsewhere in its span.
func TestMdPreviewStartAnnotation_OnABlockStopStillTargetsTheBlock(t *testing.T) {
	m := stopsModel(t)
	annotateLine(m, 4, "a note on the block's second line")
	_, srcMap := m.mdPreviewBody()
	m.setMdPreviewCursorToBlock(1)

	m.mdPreviewStartAnnotation()

	require.True(t, m.annot.annotating)
	assert.Equal(t, srcMap.blocks()[1].startLine, m.nav.diffCursor, "a block stop aims at the block's own line")
	assert.Empty(t, m.annot.input.Value(), "and finds nothing to pre-fill, since no comment sits on that line")

	m.annot.input.SetValue("a new note on the block")
	m.saveAnnotation()

	assert.Equal(t, 2, m.store.Count(), "so it adds a comment rather than replacing the one further down")
}

// mdPreviewHugDoc is a plain document with a block in the middle and a block at
// the end, so one fixture covers both halves of the no-gap rule. Every block
// here is followed by glamour's own padding row, which is exactly the row an
// annotation used to be spliced after.
const mdPreviewHugDoc = "# Title\n\nAlpha paragraph.\n\n## Section\n\nOmega paragraph.\n"

// mdPreviewCommentRow returns the painted frame's rows with ANSI stripped, plus
// the row comment landed on. It fails when the comment was painted more than
// once, or not at all — either would make an adjacency assertion meaningless.
func mdPreviewCommentRow(t *testing.T, painted, comment string) (rows []string, at int) {
	t.Helper()
	rows = strings.Split(ansi.Strip(painted), "\n")
	at = -1
	for i, r := range rows {
		if !strings.Contains(r, comment) {
			continue
		}
		require.Equal(t, -1, at, "comment %q must be painted exactly once", comment)
		at = i
	}
	require.Positive(t, at, "comment %q must be painted, and never as the frame's first row", comment)
	return rows, at
}

// TestMdPreviewPaintAnnotations_MidDocumentBlockCommentHugsItsLastContentRow is
// the reported bug: glamour pads a blank row after every block, that padding was
// inside the block's span, and an annotation spliced at the end of the span
// therefore floated one row below the paragraph it commented on. The diff pane
// paints an annotation directly under its line, and preview must match.
func TestMdPreviewPaintAnnotations_MidDocumentBlockCommentHugsItsLastContentRow(t *testing.T) {
	m := mdPreviewStyledModel(t, mdPreviewHugDoc)
	const comment = "a note on the middle paragraph"
	annotateLine(m, 3, comment) // "Alpha paragraph.", a block with two blocks below it

	painted, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity: this document must align")

	rows, at := mdPreviewCommentRow(t, painted, comment)
	assert.Contains(t, rows[at-1], "Alpha paragraph.",
		"the comment must sit directly under the block's last row with text on it")
}

// TestMdPreviewPaintAnnotations_LastBlockCommentHugsItsLastContentRow is the
// same rule at the end of the document, where the block's span used to run to
// the last row of the whole render — two blank rows of it here, so the gap was
// twice as wide as the mid-document one.
func TestMdPreviewPaintAnnotations_LastBlockCommentHugsItsLastContentRow(t *testing.T) {
	m := mdPreviewStyledModel(t, mdPreviewHugDoc)
	const comment = "a note on the last paragraph"
	annotateLine(m, 7, comment) // "Omega paragraph.", the document's last block

	painted, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity: this document must align")

	rows, at := mdPreviewCommentRow(t, painted, comment)
	assert.Contains(t, rows[at-1], "Omega paragraph.",
		"the last block's span runs to the end of the render, and its comment must still hug the text")
}

// TestMdPreviewPaintAnnotations_TwoCommentsOnOneBlockStackUnderIt: closing the
// gap must not reorder anything. Both comments belong to the same block, so they
// are painted back to back under its last content row, in ascending line order.
func TestMdPreviewPaintAnnotations_TwoCommentsOnOneBlockStackUnderIt(t *testing.T) {
	m := mdPreviewStyledModel(t, "# Title\n\nAlpha one.\nAlpha two.\n\ntail paragraph\n")
	const (
		first  = "a note on the first line"
		second = "a note on the second line"
	)
	annotateLine(m, 4, second) // added out of order on purpose: paint order is line order
	annotateLine(m, 3, first)

	painted, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity: this document must align")

	rows, at := mdPreviewCommentRow(t, painted, first)
	assert.Contains(t, rows[at-1], "Alpha one.", "the first comment hugs the block's own text")
	_, second2 := mdPreviewCommentRow(t, painted, second)
	assert.Equal(t, at+1, second2, "and the second is painted directly below the first, in line order")
}

// TestMdPreviewPaintAnnotations_ExpandedRawLineCommentUnchanged is the case that
// must NOT move: inside an expanded block a comment is spliced at the raw line's
// own anchor row, which never carried padding, so it hugged its line before this
// change and has to keep hugging it after.
func TestMdPreviewPaintAnnotations_ExpandedRawLineCommentUnchanged(t *testing.T) {
	m := mdPreviewStyledModel(t, "# Title\n\nAlpha one.\nAlpha two.\n\ntail paragraph\n")
	const comment = "a note on the first raw line"
	annotateLine(m, 3, comment) // "Alpha one.", the expanded block's first source line
	m.setMdPreviewCursorToBlock(1)
	m.mdPreviewToggleRaw()
	require.Equal(t, 1, m.mdPreviewExpandedBlock(), "fixture sanity: the paragraph must expand")

	painted, srcMap := m.mdPreviewBody()
	require.Len(t, srcMap.lines, 2, "fixture sanity: both source lines must paint a raw row")

	rows, at := mdPreviewCommentRow(t, painted, comment)
	assert.Equal(t, "Alpha one.", rows[at-1], "the comment stays under its own raw source line")
	assert.Equal(t, "Alpha two.", rows[at+1], "and the block's remaining source continues below it")
}

// mdPreviewTallDoc is a document with far more blocks than the test viewport
// (20 rows) can show at once, so the last block can only be reached by
// scrolling. Every paragraph renders to a single row, which keeps the row
// arithmetic in the visibility tests below readable.
func mdPreviewTallDoc() string {
	var b strings.Builder
	b.WriteString("# Title\n\n")
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&b, "Paragraph number %d with some text in it.\n\n", i)
	}
	b.WriteString("Last block of the document.")
	return b.String()
}

// mdPreviewVisibleRows is what the reader can actually see: the rows of the
// composed frame inside the viewport's window. The visibility tests assert
// against these rather than against a recorded row span alone, because the
// recorded span is the thing under test.
func mdPreviewVisibleRows(m Model) []string {
	rows := strings.Split(m.mdPreviewFinalRender(), "\n")
	top := max(0, m.layout.viewport.YOffset)
	end := min(top+m.layout.viewport.Height, len(rows))
	if top >= end {
		return nil
	}
	out := make([]string, 0, end-top)
	for _, r := range rows[top:end] {
		out = append(out, ansi.Strip(r))
	}
	return out
}

// mdPreviewInputOnScreen reports whether the live annotation input's own row is
// among the rows the reader can see. It matches on the input's prompt ("> "
// after the annotation prefix) rather than on the placeholder, because an input
// opened on an existing comment is pre-filled and shows no placeholder at all.
func mdPreviewInputOnScreen(m Model) bool {
	for _, r := range mdPreviewVisibleRows(m) {
		if strings.Contains(r, "> ") && strings.Contains(r, ansi.Strip(m.annotPrefix())) {
			return true
		}
	}
	return false
}

// mdPreviewWalkCursorToLastBlock steers the preview cursor down to the last
// block the way a reader holding `j` would, so the viewport ends up wherever
// the ordinary minimal follow put it rather than at a position the test chose.
func mdPreviewWalkCursorToLastBlock(t *testing.T, m *Model) {
	t.Helper()
	body, srcMap := m.mdPreviewBody()
	m.layout.viewport.SetContent(m.mdPreviewFrame(body, srcMap))
	last := len(srcMap.blocks()) - 1
	require.Positive(t, last, "fixture sanity: the document needs several blocks")
	for range len(srcMap.stops()) + 2 {
		m.moveMdPreviewCursor(1)
	}
	ref, ok := m.mdPreviewCursorRef()
	require.True(t, ok)
	require.Equal(t, last, ref.block, "the walk must end on the last block")
}

// mdPreviewFirstBlockBelowFold is the first block whose last row falls outside
// a fresh window of height rows, i.e. the first block that can be parked on the
// bottom edge with content above it. Picking it by measurement rather than by a
// hardcoded index keeps the fixture honest if glamour's row output ever shifts.
func mdPreviewFirstBlockBelowFold(t *testing.T, anchors []mdPreviewBlockAnchor, height int) int {
	t.Helper()
	for i := range anchors {
		if anchors[i].endRow >= height {
			return i
		}
	}
	require.Fail(t, "fixture sanity: no block falls below the first screenful")
	return -1
}

// TestMdPreviewStartAnnotation_LastBlock_InputIsVisible is the named bug: with
// the cursor on the last block of a document taller than the pane, the input
// is spliced under the block's last row — which the minimal cursor follow has
// put at the very bottom edge of the viewport — so before the preview grew its
// own visibility step the input landed one row below the window and the reader
// typed blind.
func TestMdPreviewStartAnnotation_LastBlock_InputIsVisible(t *testing.T) {
	m := mdPreviewTestModel(mdLines(mdPreviewTallDoc()))
	m.modes.mdPreview = true
	mdPreviewWalkCursorToLastBlock(t, &m)
	require.False(t, mdPreviewInputOnScreen(m), "fixture sanity: no input open yet")

	m.mdPreviewStartAnnotation()

	require.True(t, m.annot.annotating)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.liveInput.ok, "the painter must record where it put the live input")
	assert.GreaterOrEqual(t, srcMap.liveInput.row, m.layout.viewport.YOffset)
	assert.Less(t, srcMap.liveInput.endRow, m.layout.viewport.YOffset+m.layout.viewport.Height)
	assert.True(t, mdPreviewInputOnScreen(m),
		"the annotation input must be on screen after `a` on the last block")
}

// TestMdPreviewStartAnnotation_BlockAtViewportBottom_ScrollsMinimally covers
// the same failure away from the end of the document, and pins that the scroll
// is the smallest one that works: exactly enough to bring the input's row into
// the window, never a centering jump.
func TestMdPreviewStartAnnotation_BlockAtViewportBottom_ScrollsMinimally(t *testing.T) {
	m := mdPreviewTestModel(mdLines(mdPreviewTallDoc()))
	m.modes.mdPreview = true

	body, srcMap := m.mdPreviewBody()
	m.layout.viewport.SetContent(m.mdPreviewFrame(body, srcMap))
	anchors := srcMap.blocks()
	target := mdPreviewFirstBlockBelowFold(t, anchors, m.layout.viewport.Height)
	// park the block on the very bottom row of the viewport, which is where the
	// cursor's own minimal follow leaves it after a downward walk
	m.layout.viewport.SetYOffset(anchors[target].endRow - m.layout.viewport.Height + 1)
	m.setMdPreviewCursorToBlock(target)
	before := m.layout.viewport.YOffset
	require.Equal(t, anchors[target].endRow, before+m.layout.viewport.Height-1,
		"fixture sanity: the block must sit on the bottom row")

	m.mdPreviewStartAnnotation()

	_, srcMap = m.mdPreviewBody()
	require.True(t, srcMap.liveInput.ok)
	assert.Equal(t, before+1, m.layout.viewport.YOffset,
		"one row of input below the bottom row must cost exactly one row of scroll")
	assert.True(t, mdPreviewInputOnScreen(m))
}

// TestMdPreviewStartAnnotation_InputAlreadyVisible_DoesNotScroll is the other
// half of "minimal": with room below the block, starting an annotation must
// leave the viewport exactly where the reader put it. This is what the
// save/restore of viewport.YOffset in mdPreviewStartAnnotationAt exists for,
// and the new visibility step must not undo it.
func TestMdPreviewStartAnnotation_InputAlreadyVisible_DoesNotScroll(t *testing.T) {
	m := mdPreviewTestModel(mdLines(mdPreviewTallDoc()))
	m.modes.mdPreview = true

	body, srcMap := m.mdPreviewBody()
	m.layout.viewport.SetContent(m.mdPreviewFrame(body, srcMap))
	anchors := srcMap.blocks()
	require.Greater(t, len(anchors), 4, "fixture sanity")
	target := 3
	m.layout.viewport.SetYOffset(0)
	m.setMdPreviewCursorToBlock(target)
	require.Less(t, anchors[target].endRow+1, m.layout.viewport.Height,
		"fixture sanity: the input's row already fits in the window")

	m.mdPreviewStartAnnotation()

	assert.Equal(t, 0, m.layout.viewport.YOffset, "an input already on screen must not move the viewport")
	assert.True(t, mdPreviewInputOnScreen(m))
}

// TestMdPreviewStartAnnotation_RawLineAtBottom_InputIsVisible is the same
// property one level down: inside an expanded block the input is spliced under
// the raw source line it was aimed at, not under the block, so the visibility
// step has to read the row the painter really used.
func TestMdPreviewStartAnnotation_RawLineAtBottom_InputIsVisible(t *testing.T) {
	doc := mdPreviewTallDoc() + "\n\nA closing paragraph that is expanded to source."
	m := mdPreviewTestModel(mdLines(doc))
	m.modes.mdPreview = true
	mdPreviewWalkCursorToLastBlock(t, &m)
	m.mdPreviewToggleRaw()

	_, srcMap := m.mdPreviewBody()
	require.NotEmpty(t, srcMap.lines, "fixture sanity: the last block must expand to raw source")
	lastLine := srcMap.lines[len(srcMap.lines)-1]
	m.setMdPreviewLineCursor(lastLine.block, lastLine.lineIdx)
	// park that raw row on the bottom edge, which is where a downward walk
	// through the expanded block leaves it
	m.layout.viewport.SetYOffset(lastLine.row - m.layout.viewport.Height + 1)

	m.mdPreviewStartAnnotation()

	require.True(t, m.annot.annotating)
	_, srcMap = m.mdPreviewBody()
	require.True(t, srcMap.liveInput.ok)
	assert.GreaterOrEqual(t, srcMap.liveInput.row, m.layout.viewport.YOffset)
	assert.Less(t, srcMap.liveInput.endRow, m.layout.viewport.YOffset+m.layout.viewport.Height)
	assert.True(t, mdPreviewInputOnScreen(m),
		"the input under an expanded block's last raw line must be on screen")
}

// TestMdPreviewEditAnnotation_AtViewportBottom_InputIsVisible covers editing:
// `a` on an annotation stop opens the input in place of that annotation's own
// rows, so the rows to keep on screen are the annotation's, not a freshly
// spliced row below the block.
func TestMdPreviewEditAnnotation_AtViewportBottom_InputIsVisible(t *testing.T) {
	m := mdPreviewTestModel(mdLines(mdPreviewTallDoc()))
	m.modes.mdPreview = true

	_, srcMap := m.mdPreviewBody()
	anchors := srcMap.blocks()
	last := len(anchors) - 1
	lineNum := m.diffLineNum(m.file.lines[anchors[last].startLine])
	m.store.Add(annotation.Annotation{File: "plan.md", Line: lineNum, Type: " ", Comment: "a note to edit"})

	body, srcMap := m.mdPreviewBody()
	m.layout.viewport.SetContent(m.mdPreviewFrame(body, srcMap))
	require.Len(t, srcMap.annots, 1, "fixture sanity: exactly one painted annotation")
	annot := srcMap.annots[0]
	m.setMdPreviewCursorRef(annot.stop().ref)
	// scroll so the annotation's rows sit exactly one row BELOW the window
	m.layout.viewport.SetYOffset(annot.endRow - m.layout.viewport.Height)
	before := m.layout.viewport.YOffset
	require.Positive(t, before, "fixture sanity: the annotation must be below the fold")
	require.False(t, mdPreviewInputOnScreen(m))

	m.mdPreviewStartAnnotation()

	require.True(t, m.annot.annotating)
	_, srcMap = m.mdPreviewBody()
	require.True(t, srcMap.liveInput.ok, "editing must record the rows the input replaced")
	assert.Equal(t, before+1, m.layout.viewport.YOffset, "one hidden row must cost exactly one row of scroll")
	assert.GreaterOrEqual(t, srcMap.liveInput.row, m.layout.viewport.YOffset)
	assert.Less(t, srcMap.liveInput.endRow, m.layout.viewport.YOffset+m.layout.viewport.Height)
	assert.True(t, mdPreviewInputOnScreen(m))
}

// TestMdPreviewClickAnnotate_BottomRow_InputIsVisible is the mouse half: a
// click on the bottom visible row splices the input one row below it, which
// without the visibility step is off screen exactly as the keyboard case was.
func TestMdPreviewClickAnnotate_BottomRow_InputIsVisible(t *testing.T) {
	m := mdPreviewTestModel(mdLines(mdPreviewTallDoc()))
	m.modes.mdPreview = true

	body, srcMap := m.mdPreviewBody()
	m.layout.viewport.SetContent(m.mdPreviewFrame(body, srcMap))
	anchors := srcMap.blocks()
	target := mdPreviewFirstBlockBelowFold(t, anchors, m.layout.viewport.Height)
	m.layout.viewport.SetYOffset(anchors[target].endRow - m.layout.viewport.Height + 1)
	clickY := m.diffTopRow() + (anchors[target].row - m.layout.viewport.YOffset)

	res, _ := m.mdPreviewClickDiff(clickY)
	got := res.(Model)

	require.True(t, got.annot.annotating)
	_, srcMap = got.mdPreviewBody()
	require.True(t, srcMap.liveInput.ok)
	assert.True(t, mdPreviewInputOnScreen(got), "a click on the bottom row must still show the input it opened")
}
