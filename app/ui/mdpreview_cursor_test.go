package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMdPreviewBlockCursor_NothingSelectedUntilTheReaderMoves is behavior 1 and
// 2 together: the cursor's zero state is "no block", and entering preview leaves
// it there. The old highlight always marked something near the top of the pane,
// which is what this replaced.
func TestMdPreviewBlockCursor_NothingSelectedUntilTheReaderMoves(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	assert.Equal(t, -1, m.mdPreviewBlockCursor(), "a fresh model must select no block")

	m.setMdPreviewBlockCursor(2)
	require.Equal(t, 2, m.mdPreviewBlockCursor(), "sanity: the setter must place the cursor")

	m.modes.mdPreview = false // toggleMarkdownPreview flips this itself; start from off
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview, "sanity: preview must be on")
	assert.Equal(t, -1, m.mdPreviewBlockCursor(), "entering preview must select nothing")

	m.setMdPreviewBlockCursor(1)
	m.toggleMarkdownPreview()
	assert.Equal(t, -1, m.mdPreviewBlockCursor(), "leaving preview must clear the cursor too")
}

// TestMdPreviewBlockCursor_DroppedByAFileLoadAndAReload is the other half of
// behavior 2: the cursor is tagged with the load it belongs to, so a file switch
// and an R reload of the same file both leave nothing selected without any reset
// on the load path (see mdPreviewCursorState).
func TestMdPreviewBlockCursor_DroppedByAFileLoadAndAReload(t *testing.T) {
	t.Run("reload of the same file", func(t *testing.T) {
		m := mdPreviewHighlightModel(t)
		m.setMdPreviewBlockCursor(2)
		require.Equal(t, 2, m.mdPreviewBlockCursor())

		m.triggerReload() // bumps m.file.loadSeq; the file name is unchanged

		assert.Equal(t, -1, m.mdPreviewBlockCursor(), "a reload must leave nothing selected")
	})

	t.Run("a different file", func(t *testing.T) {
		m := mdPreviewHighlightModel(t)
		m.setMdPreviewBlockCursor(2)
		require.Equal(t, 2, m.mdPreviewBlockCursor())

		m.file.name = "other.md"

		assert.Equal(t, -1, m.mdPreviewBlockCursor(), "a cursor placed on another file must not carry over")
	})
}

// TestMdPreviewCenterBlock_SeedsAtTheCenterNotTheTop is behavior 3's seeding
// rule, and the one the user asked for in so many words: the first down/up press
// starts from the middle of the pane, not from its top edge.
func TestMdPreviewCenterBlock_SeedsAtTheCenterNotTheTop(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity")
	anchors := srcMap.blocks()

	m.layout.viewport.YOffset = anchors[1].row
	center := m.layout.viewport.YOffset + (m.layout.viewport.Height-1)/2

	got := m.mdPreviewCenterBlock(srcMap)

	require.GreaterOrEqual(t, got, 0)
	assert.LessOrEqual(t, anchors[got].row, center+1, "the seeded block must start at or above the viewport center")
	assert.GreaterOrEqual(t, anchors[got].endRow, center-1, "the seeded block must reach the viewport center")
	assert.Greater(t, got, 1, "the seeded block must not be the one at the top edge of the pane")
}

// TestMdPreviewCenterBlock_NoBlocksSelectsNothing pins the refusal the seed
// inherits from the source map.
func TestMdPreviewCenterBlock_NoBlocksSelectsNothing(t *testing.T) {
	m := mdPreviewHighlightModel(t)

	assert.Equal(t, -1, m.mdPreviewCenterBlock(mdPreviewSourceMap{}), "an unaligned map seeds nothing")
	assert.Equal(t, -1, m.mdPreviewCenterBlock(mdPreviewSourceMap{aligned: true}), "an empty map seeds nothing")
}

// TestMoveMdPreviewCursor_FirstPressSeedsWithoutMoving is behavior 3's
// first half: with nothing selected, one press places the cursor at the center
// block and stops there. Moving as well would flick past the block the reader
// aimed at before they saw it marked.
func TestMoveMdPreviewCursor_FirstPressSeedsWithoutMoving(t *testing.T) {
	for _, delta := range []int{1, -1} {
		m := mdPreviewHighlightModel(t)
		_, srcMap := m.mdPreviewBody()
		require.True(t, srcMap.aligned, "fixture sanity")
		want := m.mdPreviewCenterBlock(srcMap)
		require.GreaterOrEqual(t, want, 0)

		m.moveMdPreviewCursor(delta)

		assert.Equal(t, want, m.mdPreviewBlockCursor(),
			"the seeding press (delta %d) must land on the center block and go no further", delta)
	}
}

// TestMoveMdPreviewCursor_StepsOneBlockAndClampsAtBothEnds is behavior 3's
// second half: one block per press, and a hard stop at the first and the last —
// no wrap, so holding the key down cannot silently return to the other end of
// the document.
func TestMoveMdPreviewCursor_StepsOneBlockAndClampsAtBothEnds(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity")
	last := len(srcMap.blocks()) - 1
	require.GreaterOrEqual(t, last, 3, "fixture sanity: need several blocks")

	m.setMdPreviewBlockCursor(1)
	m.moveMdPreviewCursor(1)
	assert.Equal(t, 2, m.mdPreviewBlockCursor(), "one press must move exactly one block")
	m.moveMdPreviewCursor(-1)
	assert.Equal(t, 1, m.mdPreviewBlockCursor(), "the reverse press must come straight back")

	m.setMdPreviewBlockCursor(0)
	m.moveMdPreviewCursor(-1)
	assert.Equal(t, 0, m.mdPreviewBlockCursor(), "the first block must clamp, not wrap to the last")

	m.setMdPreviewBlockCursor(last)
	m.moveMdPreviewCursor(1)
	assert.Equal(t, last, m.mdPreviewBlockCursor(), "the last block must clamp, not wrap to the first")
}

// TestMoveMdPreviewCursor_ViewportFollowsMinimally is behavior 4: the
// viewport scrolls the least it can to bring the cursor's block into view, and
// not at all while it is already there. Centering on every press — what search
// and hunk jumps do — would make each keystroke jump the page under the reader,
// which is the top-edge behavior this change is replacing.
func TestMoveMdPreviewCursor_ViewportFollowsMinimally(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity")
	anchors := srcMap.blocks()

	t.Run("no scroll while the block is already visible", func(t *testing.T) {
		mm := m
		mm.setMdPreviewBlockCursor(0)
		mm.layout.viewport.SetContent(mm.renderMarkdownPreview())
		require.LessOrEqual(t, anchors[1].endRow, mm.layout.viewport.Height-1,
			"fixture sanity: block 1 must already be on screen at the top")

		mm.moveMdPreviewCursor(1)

		assert.Equal(t, 0, mm.layout.viewport.YOffset, "a block already in view must not move the viewport")
	})

	t.Run("scrolls down by exactly what the block needs", func(t *testing.T) {
		mm := m
		mm.layout.viewport.SetContent(mm.renderMarkdownPreview())
		// walk down until the next block is below the fold, then check the step.
		var target int
		for i, a := range anchors {
			if a.endRow > mm.layout.viewport.Height-1 {
				target = i
				break
			}
		}
		require.Positive(t, target, "fixture sanity: some block must fall below the first screen")
		mm.setMdPreviewBlockCursor(target - 1)

		mm.moveMdPreviewCursor(1)

		want := min(anchors[target].endRow-mm.layout.viewport.Height+1, anchors[target].row)
		assert.Equal(t, want, mm.layout.viewport.YOffset,
			"the viewport must scroll by the minimum that makes the block whole, not center it")
		assert.NotEqual(t, max(0, anchors[target].row-mm.layout.viewport.Height/2), mm.layout.viewport.YOffset,
			"a centered offset is exactly what this must not do")
	})

	t.Run("scrolls up to the block's own first row", func(t *testing.T) {
		mm := m
		mm.setMdPreviewBlockCursor(2)
		mm.layout.viewport.SetContent(mm.renderMarkdownPreview())
		mm.layout.viewport.SetYOffset(anchors[3].row)
		require.Greater(t, mm.layout.viewport.YOffset, anchors[1].row, "fixture sanity: block 1 must be above the fold")

		mm.moveMdPreviewCursor(-1)

		assert.Equal(t, anchors[1].row, mm.layout.viewport.YOffset,
			"scrolling up must stop at the block's first row, not center it")
	})
}

// TestMoveMdPreviewCursor_UnalignedDocumentFallsBackToRowScroll covers the
// degradation: a document whose source map did not align has no blocks to steer
// between, so down/up must keep scrolling rather than becoming dead keys.
func TestMoveMdPreviewCursor_UnalignedDocumentFallsBackToRowScroll(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	m.cfg.noColors = true // the documented never-aligns mode (see mdPreviewRenderWithMap)
	_, srcMap := m.mdPreviewBody()
	require.False(t, srcMap.aligned, "fixture sanity: no-colors must not align")
	m.layout.viewport.SetContent(m.renderMarkdownPreview())

	m.moveMdPreviewCursor(1)

	assert.Equal(t, 1, m.layout.viewport.YOffset, "with no blocks to steer between, down must scroll one row")
	assert.Equal(t, -1, m.mdPreviewBlockCursor(), "an unanchorable document can never carry a cursor")
}

// TestMdPreviewViewportOnlyScroll_ClearsACursorItHides is behavior 6 for the key
// paths: J/K, the page keys and home/end move the viewport only, and drop the
// cursor as soon as its block is entirely off screen. Seeing what you are about
// to annotate is the invariant this keeps.
func TestMdPreviewViewportOnlyScroll_ClearsACursorItHides(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"scroll_diff_down (J)", "J"},
		{"page_down", "pgdown"},
		{"half_page_down", "ctrl+d"},
		{"end", "end"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := mdPreviewHighlightModel(t)
			_, srcMap := m.mdPreviewBody()
			require.True(t, srcMap.aligned, "fixture sanity")
			m.setMdPreviewBlockCursor(0)
			m.layout.viewport.SetContent(m.renderMarkdownPreview())

			got := pressKey(t, m, tc.key)

			require.Greater(t, got.layout.viewport.YOffset, srcMap.blocks()[0].endRow,
				"fixture sanity: %s must carry block 0 entirely off the top", tc.name)
			assert.Equal(t, -1, got.mdPreviewBlockCursor(), "%s must drop a cursor it scrolled out of view", tc.name)
		})
	}
}

// TestMdPreviewViewportOnlyScroll_KeepsAPartlyVisibleCursor pins the other side
// of the same rule: only a block entirely off screen loses the cursor. A block
// still partly in view is still what the reader is looking at.
func TestMdPreviewViewportOnlyScroll_KeepsAPartlyVisibleCursor(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity")
	target := blockVisibleAcrossScroll(t, m, srcMap, 1)
	m.setMdPreviewBlockCursor(target)
	m.layout.viewport.SetContent(m.renderMarkdownPreview())

	m.scrollMarkdownPreview(1)

	assert.Equal(t, target, m.mdPreviewBlockCursor(), "a one-row scroll must not drop a fully visible block")
}

// TestMdPreviewHomeReturnsToTopAndClearsAHiddenCursor covers the home key's own
// path, which jumps the viewport absolutely rather than by a delta.
func TestMdPreviewHomeReturnsToTopAndClearsAHiddenCursor(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity")
	last := len(srcMap.blocks()) - 1
	m.setMdPreviewBlockCursor(last)
	m.layout.viewport.SetContent(m.renderMarkdownPreview())
	m.layout.viewport.SetYOffset(srcMap.blocks()[last].row)

	got := pressKey(t, m, "home")

	assert.Equal(t, 0, got.layout.viewport.YOffset, "home must return to the top")
	assert.Equal(t, -1, got.mdPreviewBlockCursor(), "home must drop a cursor left at the bottom of the document")
}

// TestDispatchAction_MdPreviewDownUpDriveTheBlockCursor is behavior 3 through the
// real key path — j/k AND the arrow keys, which resolve to the same two actions.
func TestDispatchAction_MdPreviewDownUpDriveTheBlockCursor(t *testing.T) {
	for _, keys := range [][2]string{{"j", "k"}, {"down", "up"}} {
		t.Run(keys[0]+"/"+keys[1], func(t *testing.T) {
			m := mdPreviewHighlightModel(t)
			_, srcMap := m.mdPreviewBody()
			require.True(t, srcMap.aligned, "fixture sanity")
			seeded := m.mdPreviewCenterBlock(srcMap)
			require.GreaterOrEqual(t, seeded, 1, "fixture sanity: the center block must have a block above it")

			afterFirst := pressKey(t, m, keys[0])
			assert.Equal(t, seeded, afterFirst.mdPreviewBlockCursor(), "the first press must seed at the center")

			afterSecond := pressKey(t, afterFirst, keys[0])
			assert.Equal(t, seeded+1, afterSecond.mdPreviewBlockCursor(), "the second press must move one block on")

			back := pressKey(t, afterSecond, keys[1])
			assert.Equal(t, seeded, back.mdPreviewBlockCursor(), "the reverse key must move one block back")
			assert.Equal(t, 0, back.nav.diffCursor, "the source-line cursor must never move while previewing")
		})
	}
}

// TestMdPreviewFinalRender_CacheDoesNotServeAStaleFrameAcrossACursorMove is the
// staleness test the render memos demand (see .claude/rules/gotchas.md): the
// second state is reached on a model whose caches were WARMED by the first, so a
// memo that failed to account for the cursor would hand back the previous
// frame. A fresh-model test could never catch that.
//
// It runs panned (scrollX > 0) on purpose, so the horizontal-cut memo is really
// used rather than skipped by applyMdPreviewScroll's "nothing hidden" early
// return — the cut is the memo the highlight is layered on top of.
func TestMdPreviewFinalRender_CacheDoesNotServeAStaleFrameAcrossACursorMove(t *testing.T) {
	m := mdPreviewStyledModel(t, widePanDoc)
	m.layout.viewport.Width = 40
	m.layout.scrollX = 5

	body, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity")
	require.Greater(t, m.mdPreviewWidestRow(body), m.mdPreviewCutWidth(),
		"fixture sanity: the document must be wider than the pane, or the cut memo is never used")
	anchors := srcMap.blocks()
	require.GreaterOrEqual(t, len(anchors), 3, "fixture sanity: need blocks to move between")
	bg := mdPreviewHighlightBg(m)
	require.NotEmpty(t, bg, "fixture sanity: the resolver must carry a search background")

	m.setMdPreviewBlockCursor(0)
	first := strings.Split(m.mdPreviewFinalRender(), "\n") // warms the base, width and cut memos

	m.setMdPreviewBlockCursor(2)
	second := strings.Split(m.mdPreviewFinalRender(), "\n") // every memo is warm from the state above

	require.Contains(t, first[anchors[0].row], bg, "sanity: block 0 must be marked in the first frame")
	assert.NotContains(t, second[anchors[0].row], bg,
		"the memoized frame must not keep the previous cursor's highlight")
	assert.Contains(t, second[anchors[2].row], bg,
		"the frame served from the warm memos must carry the CURRENT cursor's highlight")
}

// TestMdPreviewStartAnnotation_NoCursorSeedsAtCenterAndAnnotates is behavior 7:
// `a` is never a dead key. With nothing selected it seeds the cursor exactly
// where the first down/up press would have, then annotates that block — and the
// cursor stays there, so the highlight shows what the comment is attached to.
func TestMdPreviewStartAnnotation_NoCursorSeedsAtCenterAndAnnotates(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity")
	m.layout.viewport.YOffset = srcMap.blocks()[2].row
	want := m.mdPreviewCenterBlock(srcMap)
	require.GreaterOrEqual(t, want, 0, "fixture sanity: the center must resolve a block")
	require.Equal(t, -1, m.mdPreviewBlockCursor(), "sanity: nothing may be selected yet")

	m.mdPreviewStartAnnotation()

	assert.True(t, m.annot.annotating, "`a` with no cursor must still open an annotation input")
	assert.Equal(t, want, m.mdPreviewBlockCursor(), "`a` must leave the cursor on the block it seeded")
	assert.Equal(t, srcMap.blocks()[want].startLine, m.nav.diffCursor,
		"the annotation must be anchored to the seeded block's own source line")
}

// TestMdPreviewStartAnnotation_KeepsAnExistingCursor is the other half of
// behavior 7: with a cursor already placed, `a` annotates THAT block and does
// not re-seed at the viewport center.
func TestMdPreviewStartAnnotation_KeepsAnExistingCursor(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity")
	m.setMdPreviewBlockCursor(1)

	m.mdPreviewStartAnnotation()

	assert.Equal(t, 1, m.mdPreviewBlockCursor(), "`a` must not move a cursor the reader placed")
	assert.Equal(t, srcMap.blocks()[1].startLine, m.nav.diffCursor, "it must annotate the cursor's own block")
}

// TestMdPreviewClickDiff_SetsTheBlockCursor is behavior 8: a click is aim, so it
// leaves the cursor on the block it hit — the highlight marks what was clicked,
// and a following j continues from there.
func TestMdPreviewClickDiff_SetsTheBlockCursor(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity")
	target := 2
	require.Greater(t, len(srcMap.blocks()), target, "fixture sanity")

	result, _ := m.mdPreviewClickDiff(m.diffTopRow() + srcMap.blocks()[target].row)

	got := result.(Model)
	assert.Equal(t, target, got.mdPreviewBlockCursor(), "a click must place the cursor on the block it hit")
	assert.Equal(t, srcMap.blocks()[target].startLine, got.nav.diffCursor,
		"and annotate that same block, as it did before the cursor existed")
}

// TestMdPreviewCursorState_ZeroValueSelectsNothing pins the property the whole
// tagging scheme rests on: the zero value selects nothing, so no constructor
// and no reset on the load path is needed to make "nothing highlighted" the
// default. Note the zero mdPreviewStopRef is a real stop (block 0's own), so
// this rests entirely on `set` — see mdPreviewStopRef.
func TestMdPreviewCursorState_ZeroValueSelectsNothing(t *testing.T) {
	var c mdPreviewCursorState

	assert.Equal(t, -1, c.blockOf("", 0), "the zero value must select nothing even against a zero file/seq")
	assert.Equal(t, -1, c.blockOf("plan.md", 3), "the zero value must select nothing against a real load either")
	_, ok := c.refOf("plan.md", 3)
	assert.False(t, ok, "the zero value must report no stop at all, not block 0's")

	c = mdPreviewCursorState{set: true, ref: mdPreviewStopRef{block: 4}, file: "plan.md", seq: 3}
	assert.Equal(t, 4, c.blockOf("plan.md", 3), "the exact load it was placed under must read the cursor back")
	assert.Equal(t, -1, c.blockOf("plan.md", 4), "a later load of the same file must not")
	assert.Equal(t, -1, c.blockOf("other.md", 3), "another file must not")

	// an annotation stop names the block that OWNS it, which is the coordinate
	// `a` aims at — never the annotation itself.
	c = mdPreviewCursorState{set: true, ref: mdPreviewStopRef{block: 4, onAnnot: true, annot: 1},
		file: "plan.md", seq: 3}
	assert.Equal(t, 4, c.blockOf("plan.md", 3), "an annotation stop must read back as its owning block")

	// the file-level annotation owns no block at all.
	c = mdPreviewCursorState{set: true, ref: mdPreviewStopRef{block: mdPreviewFileStopBlock, onAnnot: true},
		file: "plan.md", seq: 3}
	assert.Equal(t, -1, c.blockOf("plan.md", 3), "the file-level annotation stop names no block")
	_, ok = c.refOf("plan.md", 3)
	assert.True(t, ok, "but it is still a placed cursor")
}
