package ui

import "sort"

// mdPreviewCursorState is the markdown preview's block cursor: which rendered
// block the reader has steered the highlight onto, if any.
//
// It replaces the scroll-derived anchor the highlight used to have (the topmost
// fully visible block, recomputed from viewport.YOffset on every repaint and
// holding no state at all). That model marked a block the reader had not
// chosen, permanently, near the top of the pane; this one marks nothing until
// down/up, `a` or a click puts the cursor somewhere.
//
// The cursor is TAGGED with the load it belongs to — the file name plus
// m.file.loadSeq — rather than being reset from the load path. Both are needed:
// loadFileDiff bumps loadSeq before every load and triggerReload bumps it again
// (app/ui/loaders.go), so a file switch and an `R` reload of the same file both
// leave the tag mismatched, and blockOf reports "no block" without anyone having
// to remember to clear it there. It is the same seq-tagging compactState's
// pendingAnchor uses for exactly this reason (see .claude/rules/gotchas.md), and
// it is what keeps this feature out of app/ui/loaders.go and app/ui/model.go,
// which are upstream-owned files this fork rebases by hand (see PATCH.md).
//
// The zero value therefore means "no block selected" without any initialization
// step: set is false, so blockOf answers -1 whatever the other fields hold.
type mdPreviewCursorState struct {
	set   bool   // false in the zero value, which is what makes "nothing highlighted" the default
	block int    // index into the current source map's blocks(); meaningless unless set
	file  string // m.file.name the cursor was placed against
	seq   uint64 // m.file.loadSeq it was placed under
}

// blockOf returns the block index the cursor marks for the load identified by
// (file, seq), or -1 when it marks nothing — never placed, cleared, or placed
// against a different file or an earlier load of this one.
func (c mdPreviewCursorState) blockOf(file string, seq uint64) int {
	if !c.set || c.block < 0 || c.file != file || c.seq != seq {
		return -1
	}
	return c.block
}

// mdPreviewBlockCursor is the one read of the block cursor. -1 means no block is
// selected, which is the state every preview session starts in — see
// mdPreviewCursorState for why a file load and an `R` reload both produce it
// without an explicit reset.
func (m Model) mdPreviewBlockCursor() int {
	return m.preview.cursor.blockOf(m.file.name, m.file.loadSeq)
}

// setMdPreviewBlockCursor places the cursor on block bi of the current load. A
// negative bi clears it, so callers that computed "no block" can pass the result
// through unguarded.
func (m *Model) setMdPreviewBlockCursor(bi int) {
	if bi < 0 {
		m.clearMdPreviewBlockCursor()
		return
	}
	m.preview.cursor = mdPreviewCursorState{set: true, block: bi, file: m.file.name, seq: m.file.loadSeq}
}

// clearMdPreviewBlockCursor takes the cursor off every block, so nothing is
// highlighted until the reader moves it again.
func (m *Model) clearMdPreviewBlockCursor() {
	m.preview.cursor = mdPreviewCursorState{}
}

// mdPreviewCenterBlock returns the block nearest the vertical center of the
// current viewport, or -1 when the map cannot anchor anything.
//
// This is where the cursor is seeded — by the first down/up press, and by `a`
// pressed with no cursor set. The center, not the top row: a block at the very
// top edge is the one the reader has just scrolled past, while the middle of the
// pane is where they are looking. Seeding at the top is the placement the
// scroll-derived highlight had, and the one this whole change exists to replace.
//
// "Nearest" is measured against a block's whole span, so a center row falling
// inside a block picks that block at distance 0. When the center lands in the
// padding between two blocks, the preceding one wins a tie — it is the one whose
// text the reader has just read.
func (m Model) mdPreviewCenterBlock(srcMap mdPreviewSourceMap) int {
	anchors := srcMap.blocks()
	if !srcMap.aligned || len(anchors) == 0 {
		return -1
	}
	center := m.layout.viewport.YOffset + max(0, m.layout.viewport.Height-1)/2

	next := sort.Search(len(anchors), func(i int) bool { return anchors[i].row > center })
	prev := next - 1
	switch {
	case prev < 0:
		return 0 // the viewport center sits above the first block; the nearest block is the first
	case next >= len(anchors):
		return prev
	}
	prevDist := max(0, center-anchors[prev].endRow) // 0 whenever the center is inside prev's own span
	if anchors[next].row-center < prevDist {
		return next
	}
	return prev
}

// moveMdPreviewBlockCursor is what down/up (j/k and the arrow keys, one action
// each) do in preview: steer the highlight, one block per press.
//
// Two behaviors, split on whether a cursor exists yet:
//   - nothing selected: the press SEEDS the cursor at the block nearest the
//     viewport center and stops there. It deliberately does not also move —
//     the first press is "start here", and moving as well would make the block
//     the reader aimed at flick past before they saw it marked.
//   - a cursor exists: it moves one block and clamps at the first and the last.
//     No wrap: arriving back at the top of a long document because a key was
//     held down is never what was meant.
//
// A document whose source map did not align (README.md is one, by design — see
// mdPreviewBuildSourceMap) has no blocks to steer between, so these keys fall
// back to a one-row viewport scroll. That is what they did before the block
// cursor existed, and it keeps an unanchorable document readable instead of
// making its main reading keys dead.
func (m *Model) moveMdPreviewBlockCursor(delta int) {
	if !m.file.markdownPreviewable {
		return // preview stuck on for a file renderDiff will not preview; see panMarkdownPreview
	}
	body, srcMap := m.mdPreviewBody()
	anchors := srcMap.blocks()
	if !srcMap.aligned || len(anchors) == 0 {
		m.scrollMarkdownPreview(delta)
		return
	}

	bi := m.mdPreviewBlockCursor()
	if bi < 0 {
		bi = m.mdPreviewCenterBlock(srcMap)
		if bi < 0 {
			return
		}
	} else {
		bi = min(max(bi+delta, 0), len(anchors)-1)
	}
	m.setMdPreviewBlockCursor(bi)

	// content first, offset second: SetYOffset clamps against the viewport's own
	// content buffer, so a frame that has not been pushed yet would clamp the
	// target row away. Neither the horizontal cut nor the highlight depends on
	// YOffset, so composing the frame before the scroll paints the same bytes.
	m.layout.viewport.SetContent(m.mdPreviewFrame(body, srcMap))
	m.syncMdPreviewViewportToBlock(anchors[bi])
}

// syncMdPreviewViewportToBlock scrolls the viewport the least it can so the
// cursor's block is fully visible, and not at all when it already is.
//
// This is syncViewportToCursor (app/ui/diffnav.go) in preview-row coordinates:
// same three cases, same clamp for a block taller than the pane (show its start
// rather than its end, so the reader lands where the block begins). Centering
// instead — what centerViewportOnCursor does for search and hunk jumps — was
// rejected: it makes every single press jump the page under the reader, and it
// is the top-edge behavior of the old scroll-derived highlight that this change
// is replacing.
func (m *Model) syncMdPreviewViewportToBlock(a mdPreviewBlockAnchor) {
	height := m.layout.viewport.Height
	if height <= 0 {
		return
	}
	top := m.layout.viewport.YOffset
	switch {
	case a.row < top:
		m.layout.viewport.SetYOffset(a.row)
	case a.endRow >= top+height:
		m.layout.viewport.SetYOffset(min(a.endRow-height+1, a.row))
	}
}

// dropMdPreviewCursorIfHidden clears the block cursor when a viewport-only
// scroll has carried its block entirely off the screen.
//
// This is the feature's core invariant: you always see what you are about to
// annotate. J/K, the page keys, home/end and the wheel move the viewport and
// never the cursor, so without this the highlight would sit on a block pages
// away, and `a` would comment on something not on screen. A block still
// partially visible keeps the cursor — it is still what the reader is looking
// at.
//
// Called from every viewport-only scroll path: afterMdPreviewViewportScroll for
// the key paths, and flushPreviewWheelPending (mdpreview_cache.go) once per
// wheel burst rather than once per event.
func (m *Model) dropMdPreviewCursorIfHidden() {
	bi := m.mdPreviewBlockCursor()
	if bi < 0 || !m.file.markdownPreviewable {
		return
	}
	_, srcMap := m.mdPreviewBody()
	anchors := srcMap.blocks()
	if bi >= len(anchors) {
		// the map changed shape under the cursor (a width change that costs the
		// document its alignment, most plausibly); there is no block to point at.
		m.clearMdPreviewBlockCursor()
		return
	}
	top := m.layout.viewport.YOffset
	bottom := top + max(0, m.layout.viewport.Height) - 1
	if anchors[bi].endRow < top || anchors[bi].row > bottom {
		m.clearMdPreviewBlockCursor()
	}
}

// afterMdPreviewViewportScroll is the tail every preview key that moves the
// viewport without moving the cursor shares: drop the cursor if its block went
// off screen, then repaint so the highlight (or its absence) matches what is on
// screen.
func (m *Model) afterMdPreviewViewportScroll() {
	if !m.file.markdownPreviewable {
		return // preview stuck on for a file renderDiff will not preview; see panMarkdownPreview
	}
	m.dropMdPreviewCursorIfHidden()
	m.layout.viewport.SetContent(m.renderMarkdownPreview())
}
