package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
)

// stopsDoc is the fixture for everything in this file: four blocks, one of them
// (the "Alpha" paragraph) two source lines long so two annotations can sit in
// the SAME block without sharing a store key. Block index -> source line index:
// 0 -> 0 (the heading), 1 -> 2, 2 -> 5, 3 -> 7. Store Line is the 1-based line
// number, so the index plus one.
const stopsDoc = "# Title\n\n" +
	"Alpha line one.\nAlpha line two.\n\n" +
	"Bravo paragraph.\n\n" +
	"Charlie paragraph.\n"

// stopsModel is stopsDoc in a preview model whose resolver carries a real
// search background, so the highlight is visible at all.
func stopsModel(t *testing.T) Model {
	t.Helper()
	m := mdPreviewStyledModel(t, stopsDoc)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity: stopsDoc must align")
	require.Len(t, srcMap.blocks(), 4, "fixture sanity: stopsDoc must produce four blocks")
	return m
}

// annotate adds one line-level annotation to the current file.
func annotateLine(m Model, line int, comment string) {
	m.store.Add(annotation.Annotation{File: "plan.md", Line: line, Type: " ", Comment: comment})
}

// TestMdPreviewStops_JFromABlockLandsOnItsOwnAnnotation is the headline
// behavior: an annotation painted under a block is a stop of its own, sitting
// between that block and the next, so `j` reaches it instead of stepping over
// it. Skipping it is what made a comment impossible to select, and therefore
// impossible to delete without leaving preview.
func TestMdPreviewStops_JFromABlockLandsOnItsOwnAnnotation(t *testing.T) {
	m := stopsModel(t)
	annotateLine(m, 3, "on alpha") // block 1's own first line

	m.setMdPreviewBlockCursor(1)
	m.moveMdPreviewCursor(1)

	ref, ok := m.mdPreviewCursorRef()
	require.True(t, ok, "the press must leave a cursor placed")
	assert.Equal(t, mdPreviewStopRef{block: 1, onAnnot: true, annot: 0}, ref,
		"j from a block must land on that block's own annotation, not on the next block")

	m.moveMdPreviewCursor(1)
	ref, ok = m.mdPreviewCursorRef()
	require.True(t, ok)
	assert.Equal(t, mdPreviewStopRef{block: 2}, ref, "the next press must then reach the following block")

	m.moveMdPreviewCursor(-1)
	ref, _ = m.mdPreviewCursorRef()
	assert.Equal(t, mdPreviewStopRef{block: 1, onAnnot: true, annot: 0}, ref,
		"k must come straight back onto the annotation")
}

// TestMdPreviewStops_SeveralAnnotationsInOneBlockAreEachAStop covers the case
// the row accounting has to get right: two annotations under one block occupy
// two separate row spans, so they are two stops, reached in the order they are
// painted.
func TestMdPreviewStops_SeveralAnnotationsInOneBlockAreEachAStop(t *testing.T) {
	m := stopsModel(t)
	annotateLine(m, 3, "first on alpha")  // block 1, line one
	annotateLine(m, 4, "second on alpha") // block 1, line two

	_, srcMap := m.mdPreviewBody()
	stops := srcMap.stops()
	require.Len(t, stops, 6, "four blocks plus two annotations")

	m.setMdPreviewBlockCursor(1)
	walked := make([]mdPreviewStopRef, 0, 3)
	for range 3 {
		m.moveMdPreviewCursor(1)
		ref, ok := m.mdPreviewCursorRef()
		require.True(t, ok)
		walked = append(walked, ref)
	}
	assert.Equal(t, []mdPreviewStopRef{
		{block: 1, onAnnot: true, annot: 0},
		{block: 1, onAnnot: true, annot: 1},
		{block: 2},
	}, walked, "each annotation under the block must be its own stop, before the next block")

	// the two stops must be different rows, or they are one stop wearing two refs
	firstStop, ok := srcMap.stopAt(mdPreviewStopRef{block: 1, onAnnot: true, annot: 0})
	require.True(t, ok)
	secondStop, ok := srcMap.stopAt(mdPreviewStopRef{block: 1, onAnnot: true, annot: 1})
	require.True(t, ok)
	assert.Greater(t, secondStop.row, firstStop.endRow, "the second annotation must be painted below the first")
	assert.Equal(t, 3, firstStop.line, "the first stop must carry the store key of the first annotation")
	assert.Equal(t, 4, secondStop.line, "and the second stop the second one's")
}

// TestMdPreviewStops_FileLevelAnnotationIsReachable pins that the rows painted
// ABOVE the document body are a stop too. They are the only stop that precedes
// block 0, so `k` from the first block is what reaches them.
func TestMdPreviewStops_FileLevelAnnotationIsReachable(t *testing.T) {
	m := stopsModel(t)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 0, Type: "", Comment: "about the whole file"})

	_, srcMap := m.mdPreviewBody()
	stops := srcMap.stops()
	require.NotEmpty(t, stops)
	assert.Equal(t, mdPreviewStopRef{block: mdPreviewFileStopBlock, onAnnot: true}, stops[0].ref,
		"the file-level annotation must be the first stop of the document")
	assert.Equal(t, 0, stops[0].row, "it is painted at the very top")

	m.setMdPreviewBlockCursor(0)
	m.moveMdPreviewCursor(-1)

	ref, ok := m.mdPreviewCursorRef()
	require.True(t, ok, "k from the first block must land on the file-level annotation, not clamp on the block")
	assert.Equal(t, mdPreviewStopRef{block: mdPreviewFileStopBlock, onAnnot: true}, ref)
	assert.Equal(t, -1, m.mdPreviewBlockCursor(), "it owns no block, so `a` there falls back to the center seed")
}

// TestMdPreviewStops_HighlightMarksTheAnnotationNotItsBlock is the visible half
// of the same change: with the cursor on an annotation stop, the annotation's
// own rows carry the highlight and the block's rows do not. What is marked has
// to be what `d` will remove.
func TestMdPreviewStops_HighlightMarksTheAnnotationNotItsBlock(t *testing.T) {
	m := stopsModel(t)
	annotateLine(m, 3, "on alpha")
	bg := mdPreviewHighlightBg(m)
	require.NotEmpty(t, bg, "fixture sanity: the resolver must carry a search background")

	_, srcMap := m.mdPreviewBody()
	annotStop, ok := srcMap.stopAt(mdPreviewStopRef{block: 1, onAnnot: true, annot: 0})
	require.True(t, ok)
	block := srcMap.blocks()[1]

	m.setMdPreviewCursorRef(annotStop.ref)
	rows := strings.Split(m.mdPreviewFinalRender(), "\n")

	require.Greater(t, len(rows), annotStop.endRow)
	assert.Contains(t, rows[annotStop.row], bg, "the annotation's own row must be marked")
	assert.NotContains(t, rows[block.row], bg, "its block must not be marked at the same time")
}

// TestMdPreviewDeleteAnnotation_RemovesExactlyTheSelectedOne is the delete this
// whole change exists to unblock. Three annotations, one selected, one deleted —
// the other two survive untouched.
func TestMdPreviewDeleteAnnotation_RemovesExactlyTheSelectedOne(t *testing.T) {
	m := stopsModel(t)
	annotateLine(m, 3, "first on alpha")
	annotateLine(m, 4, "second on alpha")
	annotateLine(m, 6, "on bravo")
	require.Equal(t, 3, m.store.Count())

	m.setMdPreviewCursorRef(mdPreviewStopRef{block: 1, onAnnot: true, annot: 1})
	cmd := m.mdPreviewDeleteAnnotation()

	assert.Nil(t, cmd, "no file load is owed when the tree selection did not move")
	got := m.store.Get("plan.md")
	require.Len(t, got, 2, "exactly one annotation must be gone")
	assert.Equal(t, "first on alpha", got[0].Comment, "the sibling in the same block must survive")
	assert.Equal(t, "on bravo", got[1].Comment, "so must the one in another block")
	assert.Empty(t, m.preview.hint, "a delete that worked must not also complain")
}

// TestMdPreviewDeleteAnnotation_ThroughTheKeyPath is the same delete via the
// real dispatch chain, which is what proves `d` is on the allowlist AND routed
// inside handleMdPreviewAction rather than falling through.
func TestMdPreviewDeleteAnnotation_ThroughTheKeyPath(t *testing.T) {
	m := stopsModel(t)
	annotateLine(m, 3, "on alpha")
	m.setMdPreviewCursorRef(mdPreviewStopRef{block: 1, onAnnot: true, annot: 0})

	got := pressKey(t, m, "d")

	assert.Equal(t, 0, got.store.Count(), "d must delete the selected annotation from inside preview")
	assert.True(t, got.modes.mdPreview, "and must not drop the reader out of preview to do it")
	assert.Equal(t, "plan.md", got.file.name, "nor switch file")
}

// TestMdPreviewDeleteAnnotation_OnABlockRefuses pins the refusal: `d` aimed at a
// block is a near miss, not an instruction to remove everything attached to it.
func TestMdPreviewDeleteAnnotation_OnABlockRefuses(t *testing.T) {
	t.Run("cursor on a block that has annotations", func(t *testing.T) {
		m := stopsModel(t)
		annotateLine(m, 3, "first on alpha")
		annotateLine(m, 4, "second on alpha")
		m.setMdPreviewBlockCursor(1)

		cmd := m.mdPreviewDeleteAnnotation()

		assert.Nil(t, cmd)
		assert.Equal(t, 2, m.store.Count(), "a block stop must never delete the block's annotations wholesale")
		assert.Equal(t, mdPreviewDeleteNeedsAnnotationHint, m.preview.hint, "the refusal must say why")
		assert.Equal(t, 1, m.mdPreviewBlockCursor(), "and must leave the cursor where it was")
	})

	t.Run("no cursor placed at all", func(t *testing.T) {
		m := stopsModel(t)
		annotateLine(m, 3, "on alpha")
		require.Equal(t, -1, m.mdPreviewBlockCursor(), "sanity: nothing selected")

		m.mdPreviewDeleteAnnotation()

		assert.Equal(t, 1, m.store.Count(), "with nothing selected there is nothing to delete")
		assert.Equal(t, mdPreviewDeleteNeedsAnnotationHint, m.preview.hint)
	})
}

// TestMdPreviewDeleteAnnotation_LeavesTheCursorOnTheOwningBlock is the
// after-state rule. The owning block is the one placement always available: a
// block stop exists for every block whatever the annotations do, so the cursor
// can never end up past the end of the stop list.
func TestMdPreviewDeleteAnnotation_LeavesTheCursorOnTheOwningBlock(t *testing.T) {
	t.Run("an annotation in the middle of the document", func(t *testing.T) {
		m := stopsModel(t)
		annotateLine(m, 3, "on alpha")
		m.setMdPreviewCursorRef(mdPreviewStopRef{block: 1, onAnnot: true, annot: 0})

		m.mdPreviewDeleteAnnotation()

		ref, ok := m.mdPreviewCursorRef()
		require.True(t, ok, "the cursor must survive the delete")
		assert.Equal(t, mdPreviewStopRef{block: 1}, ref, "it must land on the block that owned the annotation")
		_, srcMap := m.mdPreviewBody()
		_, found := srcMap.stopAt(ref)
		assert.True(t, found, "and that stop must really exist in the repainted map")
	})

	t.Run("the very last stop in the document", func(t *testing.T) {
		m := stopsModel(t)
		annotateLine(m, 8, "on charlie") // the last block's own line
		_, srcMap := m.mdPreviewBody()
		stops := srcMap.stops()
		last := stops[len(stops)-1]
		require.True(t, last.ref.onAnnot, "fixture sanity: the annotation must be the last stop")
		m.setMdPreviewCursorRef(last.ref)

		m.mdPreviewDeleteAnnotation()

		ref, ok := m.mdPreviewCursorRef()
		require.True(t, ok)
		_, after := m.mdPreviewBody()
		remaining := after.stops()
		i := mdPreviewStopIndex(remaining, ref)
		require.GreaterOrEqual(t, i, 0, "the cursor must still name a stop that exists")
		assert.Equal(t, len(remaining)-1, i, "deleting the last stop must leave the cursor on the new last one")
	})

	t.Run("the file-level annotation", func(t *testing.T) {
		m := stopsModel(t)
		m.store.Add(annotation.Annotation{File: "plan.md", Line: 0, Type: "", Comment: "about the whole file"})
		m.setMdPreviewCursorRef(mdPreviewStopRef{block: mdPreviewFileStopBlock, onAnnot: true})

		m.mdPreviewDeleteAnnotation()

		assert.Equal(t, 0, m.store.Count(), "the file-level annotation must be deletable from preview too")
		ref, ok := m.mdPreviewCursorRef()
		require.True(t, ok)
		assert.Equal(t, mdPreviewStopRef{block: 0}, ref,
			"it owns no block, so the cursor falls to the first block rather than nowhere")
	})
}

// TestMdPreviewDeleteAnnotation_RepaintDoesNotServeAStaleFrame is the staleness
// shape the preview memos demand (see .claude/rules/gotchas.md): the second
// state is reached on a model whose caches were WARMED by the first, so a memo
// that failed to notice the annotation rows disappearing would hand back the
// frame that still contains them. A fresh-model test could never catch that.
//
// It runs panned (scrollX > 0) on purpose, so the horizontal-cut memo is really
// used rather than skipped by applyMdPreviewScroll's "nothing hidden" early
// return.
func TestMdPreviewDeleteAnnotation_RepaintDoesNotServeAStaleFrame(t *testing.T) {
	m := mdPreviewStyledModel(t, widePanDoc)
	m.layout.viewport.Width = 40
	m.layout.scrollX = 5
	annotateLine(m, 3, "pad doomed-token-42") // "Short paragraph."

	body, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity")
	require.Greater(t, m.mdPreviewWidestRow(body), m.mdPreviewCutWidth(),
		"fixture sanity: the document must be wider than the pane, or the cut memo is never used")
	annots := srcMap.annots
	require.Len(t, annots, 1, "fixture sanity: the comment must have been painted")
	m.setMdPreviewCursorRef(mdPreviewStopRef{block: annots[0].block, onAnnot: true, annot: 0})

	before := m.mdPreviewFinalRender() // warms the base, width and cut memos
	// the token, not the whole comment: the frame is panned, so the row's first
	// columns are cut away by design.
	require.Contains(t, ansi.Strip(before), "doomed-token-42", "sanity: the comment must be on screen first")

	m.mdPreviewDeleteAnnotation()
	after := m.mdPreviewFinalRender() // every memo is warm from the state above

	assert.NotContains(t, ansi.Strip(after), "doomed-token-42",
		"the frame served from the warm memos must not still carry the deleted annotation")
	assert.Less(t, strings.Count(after, "\n"), strings.Count(before, "\n"),
		"and it must be shorter by the rows the annotation used to occupy")
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
	m.setMdPreviewBlockCursor(1)

	m.mdPreviewStartAnnotation()

	require.True(t, m.annot.annotating)
	assert.Equal(t, srcMap.blocks()[1].startLine, m.nav.diffCursor, "a block stop aims at the block's own line")
	assert.Empty(t, m.annot.input.Value(), "and finds nothing to pre-fill, since no comment sits on that line")

	m.annot.input.SetValue("a new note on the block")
	m.saveAnnotation()

	assert.Equal(t, 2, m.store.Count(), "so it adds a comment rather than replacing the one further down")
}

// expandedStopsFixture is a hand-built map standing in for a frame whose block 1
// is drawn as its raw markdown source: three blocks, block 1 expanded into three
// raw rows. Hand-built rather than rendered so the row numbers the merge is
// judged on are stated by the test itself.
func expandedStopsFixture() mdPreviewSourceMap {
	return mdPreviewSourceMap{
		aligned: true,
		anchors: []mdPreviewBlockAnchor{
			{kind: mdBlockH1, row: 0, endRow: 1, startLine: 0, endLine: 0},
			{kind: mdBlockParagraph, row: 2, endRow: 5, startLine: 5, endLine: 7},
			{kind: mdBlockParagraph, row: 6, endRow: 7, startLine: 9, endLine: 9},
		},
		lines: []mdPreviewLineAnchor{
			{block: 1, row: 2, lineIdx: 5},
			{block: 1, row: 3, lineIdx: 6},
			{block: 1, row: 4, lineIdx: 7},
		},
	}
}

// TestMdPreviewStopAt_ResolvesARawLineRef pins the second level of the cursor:
// a line ref names a source line, and it resolves against the line anchors the
// expansion pass recorded rather than against anything scanned off the frame.
func TestMdPreviewStopAt_ResolvesARawLineRef(t *testing.T) {
	sm := expandedStopsFixture()

	stop, ok := sm.stopAt(mdPreviewStopRef{block: 1, onLine: true, line: 6})
	require.True(t, ok, "a raw line of the expanded block must be a resolvable stop")
	assert.Equal(t, mdPreviewStopRef{block: 1, onLine: true, line: 6}, stop.ref)
	assert.Equal(t, [2]int{3, 3}, [2]int{stop.row, stop.endRow}, "one source line is one rendered row")
	assert.Equal(t, 0, stop.line, "the store key stays zero: a raw line is not an annotation")

	_, ok = sm.stopAt(mdPreviewStopRef{block: 1, onLine: true, line: 8})
	assert.False(t, ok, "a source line that painted no row is not a stop")

	collapsed := mdPreviewSourceMap{aligned: true, anchors: sm.anchors}
	_, ok = collapsed.stopAt(mdPreviewStopRef{block: 1, onLine: true, line: 6})
	assert.False(t, ok, "collapsing the block takes the cursor off its line stops without an explicit clear")
}

// TestMdPreviewStops_ExpandedBlockEmitsItsLinesInPlaceOfItself is the headline
// of the two-level cursor: while a block is expanded, j/k step between its
// source lines, and the block's own stop is gone — there is nothing left for it
// to mean when every line under it is selectable.
func TestMdPreviewStops_ExpandedBlockEmitsItsLinesInPlaceOfItself(t *testing.T) {
	got := expandedStopsFixture().stops()

	assert.Equal(t, []mdPreviewStopRef{
		{block: 0},
		{block: 1, onLine: true, line: 5},
		{block: 1, onLine: true, line: 6},
		{block: 1, onLine: true, line: 7},
		{block: 2},
	}, stopRefs(got), "the expanded block's raw lines replace its own stop; every other block is untouched")
}

// TestMdPreviewStops_ExpandedBlockMergesLinesAndAnnotationsByRow is the ordering
// rule the expanded block imposes rather than inherits. A comment sits under the
// line it belongs to, so the two lists interleave, and only ascending row order
// makes j/k walk the block the way it is painted.
func TestMdPreviewStops_ExpandedBlockMergesLinesAndAnnotationsByRow(t *testing.T) {
	sm := expandedStopsFixture()
	// the annotation rows push the later raw rows down, exactly as the painter
	// will once it splices per line.
	sm.lines = []mdPreviewLineAnchor{
		{block: 1, row: 2, lineIdx: 5},
		{block: 1, row: 4, lineIdx: 6},
		{block: 1, row: 5, lineIdx: 7},
	}
	sm.annots = []mdPreviewAnnotAnchor{
		{block: 1, ord: 0, row: 3, endRow: 3, line: 6, changeType: " "},
		{block: 1, ord: 1, row: 6, endRow: 6, line: 8, changeType: " "},
	}

	got := sm.stops()

	assert.Equal(t, []mdPreviewStopRef{
		{block: 0},
		{block: 1, onLine: true, line: 5},
		{block: 1, onAnnot: true, annot: 0},
		{block: 1, onLine: true, line: 6},
		{block: 1, onLine: true, line: 7},
		{block: 1, onAnnot: true, annot: 1},
		{block: 2},
	}, stopRefs(got), "a comment must be reached right after the raw line it was painted under")
	for i := 1; i < len(got); i++ {
		assert.LessOrEqual(t, got[i-1].row, got[i].row, "the merged list must stay row-ascending for mdPreviewNearestStop")
	}
}

// TestMdPreviewStops_UnexpandedBlocksKeepTodayOrder guards the half that must
// not move: with nothing expanded, the list is exactly what it was — block, then
// the annotations painted under it.
func TestMdPreviewStops_UnexpandedBlocksKeepTodayOrder(t *testing.T) {
	sm := expandedStopsFixture()
	sm.lines = nil
	sm.annots = []mdPreviewAnnotAnchor{
		{block: mdPreviewFileStopBlock, row: 0, endRow: 0},
		{block: 1, ord: 0, row: 4, endRow: 4, line: 6, changeType: " "},
	}

	assert.Equal(t, []mdPreviewStopRef{
		{block: mdPreviewFileStopBlock, onAnnot: true},
		{block: 0},
		{block: 1},
		{block: 1, onAnnot: true, annot: 0},
		{block: 2},
	}, stopRefs(sm.stops()))
	assert.Equal(t, -1, sm.expandedBlock(), "no line anchors means no block is drawn as source")
}

// TestMdPreviewMergeStopsByRow_TieGivesTheLineTheEarlierPlace pins the tie-break
// on its own, since the production rows never collide today: an annotation is
// painted UNDER its line, so on an equal row the line is what the reader reaches
// first.
func TestMdPreviewMergeStopsByRow_TieGivesTheLineTheEarlierPlace(t *testing.T) {
	line := mdPreviewStop{ref: mdPreviewStopRef{block: 1, onLine: true, line: 5}, row: 4, endRow: 4}
	annot := mdPreviewStop{ref: mdPreviewStopRef{block: 1, onAnnot: true}, row: 4, endRow: 4}

	got := mdPreviewMergeStopsByRow([]mdPreviewStop{line}, []mdPreviewStop{annot})
	assert.Equal(t, []mdPreviewStop{line, annot}, got)

	assert.Equal(t, []mdPreviewStop{line}, mdPreviewMergeStopsByRow([]mdPreviewStop{line}, nil))
	assert.Equal(t, []mdPreviewStop{annot}, mdPreviewMergeStopsByRow(nil, []mdPreviewStop{annot}))
}

// TestMdPreviewCursorState_ExpandedBlockOf covers the state that carries
// expansion. It answers -1 for every load the cursor does not belong to, which
// is what makes a file switch and an `R` reload collapse the block with no code
// on either path.
func TestMdPreviewCursorState_ExpandedBlockOf(t *testing.T) {
	c := mdPreviewCursorState{
		set: true, ref: mdPreviewStopRef{block: 2, onLine: true, line: 9},
		file: "plan.md", seq: 3, expanded: true,
	}

	assert.Equal(t, 2, c.expandedBlockOf("plan.md", 3))
	assert.Equal(t, -1, c.expandedBlockOf("other.md", 3), "another file's cursor expands nothing here")
	assert.Equal(t, -1, c.expandedBlockOf("plan.md", 4), "and neither does an earlier load's")

	collapsed := c
	collapsed.expanded = false
	assert.Equal(t, -1, collapsed.expandedBlockOf("plan.md", 3), "a cursor on a block expands nothing")
	assert.Equal(t, -1, mdPreviewCursorState{}.expandedBlockOf("plan.md", 3), "nor does no cursor at all")

	fileLevel := c
	fileLevel.ref = mdPreviewStopRef{block: mdPreviewFileStopBlock, onAnnot: true}
	assert.Equal(t, -1, fileLevel.expandedBlockOf("plan.md", 3), "the file-level stop owns no block to expand")
}

// TestMdPreviewCursorState_PlacingTheCursorCollapses is the reason expansion
// lives on the cursor at all: every existing path that places the cursor assigns
// a fresh struct literal, so it collapses the block for free and no call site had
// to learn about expansion.
func TestMdPreviewCursorState_PlacingTheCursorCollapses(t *testing.T) {
	m := stopsModel(t)
	m.preview.cursor = mdPreviewCursorState{
		set: true, ref: mdPreviewStopRef{block: 1, onLine: true, line: 2},
		file: m.file.name, seq: m.file.loadSeq, expanded: true,
	}
	require.Equal(t, 1, m.preview.cursor.expandedBlockOf(m.file.name, m.file.loadSeq), "fixture sanity")

	m.setMdPreviewBlockCursor(2)
	assert.Equal(t, -1, m.preview.cursor.expandedBlockOf(m.file.name, m.file.loadSeq),
		"moving the cursor onto a block must collapse whatever was expanded")

	m.preview.cursor.expanded = true
	m.setMdPreviewCursorRef(mdPreviewStopRef{block: 0})
	assert.Equal(t, -1, m.preview.cursor.expandedBlockOf(m.file.name, m.file.loadSeq))

	m.preview.cursor.expanded = true
	m.clearMdPreviewBlockCursor()
	assert.Equal(t, -1, m.preview.cursor.expandedBlockOf(m.file.name, m.file.loadSeq))
}

// stopRefs is the identity of each stop, which is what the ordering tests are
// about — the rows are asserted separately where they matter.
func stopRefs(stops []mdPreviewStop) []mdPreviewStopRef {
	out := make([]mdPreviewStopRef, 0, len(stops))
	for _, s := range stops {
		out = append(out, s.ref)
	}
	return out
}

// TestMdPreviewStops_UnalignedDocumentHasNone pins the refusal the stop list
// inherits from the source map: with nothing anchorable there is nothing to stop
// on, which is what makes j/k fall back to a plain row scroll there.
func TestMdPreviewStops_UnalignedDocumentHasNone(t *testing.T) {
	assert.Empty(t, mdPreviewSourceMap{}.stops(), "an unaligned map offers no stops")
	assert.Empty(t, mdPreviewSourceMap{aligned: true}.stops(), "nor does an aligned one with no blocks")

	_, ok := mdPreviewSourceMap{}.stopAt(mdPreviewStopRef{})
	assert.False(t, ok, "and no ref resolves against it")
}
