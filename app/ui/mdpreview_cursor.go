package ui

// mdPreviewCursorState is the markdown preview's cursor: which STOP the reader
// has steered the highlight onto, if any. A stop is a rendered block or a single
// annotation painted under one (see mdPreviewStop, mdpreview_stops.go); the
// cursor started out as blocks only, and annotations joined it so a comment can
// be selected — and so deleted — without leaving preview.
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
// leave the tag mismatched, and refOf reports "no cursor" without anyone having
// to remember to clear it there. It is the same seq-tagging compactState's
// pendingAnchor uses for exactly this reason (see .claude/rules/gotchas.md), and
// it is what keeps this feature out of app/ui/loaders.go and app/ui/model.go,
// which are upstream-owned files this fork rebases by hand (see PATCH.md).
//
// The zero value therefore means "nothing selected" without any initialization
// step: set is false, so refOf answers "no cursor" whatever the other fields
// hold. Note that the zero mdPreviewStopRef is a real stop (block 0's own), so
// set is the only thing that carries "nothing selected" — see mdPreviewStopRef.
//
// expanded is where raw source expansion lives, and putting it HERE rather than
// in a state struct of its own is what makes the feature cost no invalidation
// code: the cursor is already tagged with the load, so a file switch and an `R`
// reload both collapse the block for free, and every existing call site that
// places the cursor assigns a fresh struct literal, so expanded defaults back to
// false and every existing path collapses automatically. The failure direction
// is "collapsed when I did not expect it", never "a stale expanded block with a
// row map that does not match the screen".
//
// Invariant: ref.onLine implies expanded. A raw-line stop only exists while its
// block is drawn as source, so a line ref with expanded false would name a stop
// no map can resolve.
type mdPreviewCursorState struct {
	set      bool             // false in the zero value, which is what makes "nothing highlighted" the default
	ref      mdPreviewStopRef // which stop; meaningless unless set
	file     string           // m.file.name the cursor was placed against
	seq      uint64           // m.file.loadSeq it was placed under
	expanded bool             // ref.block is drawn as its raw markdown source
}

// refOf returns the stop the cursor marks for the load identified by
// (file, seq). ok is false when it marks nothing — never placed, cleared, or
// placed against a different file or an earlier load of this one.
func (c mdPreviewCursorState) refOf(file string, seq uint64) (mdPreviewStopRef, bool) {
	if !c.set || c.file != file || c.seq != seq {
		return mdPreviewStopRef{}, false
	}
	return c.ref, true
}

// blockOf is refOf reduced to the block the cursor sits on or under: the block
// itself for a block stop, the OWNING block for an annotation stop. -1 means no
// cursor, or a cursor on the file-level annotation, which owns no block.
//
// This is the coordinate `a` aims at on a BLOCK stop. On an annotation stop `a`
// edits the annotation itself instead, through its own (Line, Type) — see
// mdPreviewStartAnnotation — so this answer is only the fallback there.
func (c mdPreviewCursorState) blockOf(file string, seq uint64) int {
	ref, ok := c.refOf(file, seq)
	if !ok || ref.block < 0 {
		return -1
	}
	return ref.block
}

// expandedBlockOf is the block drawn as its raw markdown source for the load
// identified by (file, seq), or -1 when none is: no cursor, a cursor from
// another file or an earlier load, or a cursor that is simply not expanded.
//
// One block is expanded at a time and it is always the block the cursor is in,
// so this is blockOf narrowed by the expanded flag rather than a second piece of
// state that could disagree with it.
func (c mdPreviewCursorState) expandedBlockOf(file string, seq uint64) int {
	if !c.expanded {
		return -1
	}
	return c.blockOf(file, seq)
}

// mdPreviewCursorRef is the one read of the preview cursor's identity. ok is
// false when nothing is selected, which is the state every preview session
// starts in — see mdPreviewCursorState for why a file load and an `R` reload
// both produce it without an explicit reset.
func (m Model) mdPreviewCursorRef() (mdPreviewStopRef, bool) {
	return m.preview.cursor.refOf(m.file.name, m.file.loadSeq)
}

// mdPreviewBlockCursor is the block the cursor sits on or under, or -1. See
// mdPreviewCursorState.blockOf.
func (m Model) mdPreviewBlockCursor() int {
	return m.preview.cursor.blockOf(m.file.name, m.file.loadSeq)
}

// mdPreviewExpandedBlock is the block drawn as its raw markdown source, or -1
// when none is — the read mdPreviewBody hands to mdPreviewExpandBlock on every
// frame. See mdPreviewCursorState.expandedBlockOf for why the cursor, and not a
// state struct of its own, is what carries expansion.
func (m Model) mdPreviewExpandedBlock() int {
	return m.preview.cursor.expandedBlockOf(m.file.name, m.file.loadSeq)
}

// mdPreviewCursorStop resolves the cursor against a source map — the painted
// map, whose row numbers are the frame's own. ok is false when nothing is
// selected or the stop no longer exists in this map (a block index past the end
// after a width change cost the document its alignment, an annotation someone
// deleted).
func (m Model) mdPreviewCursorStop(srcMap mdPreviewSourceMap) (mdPreviewStop, bool) {
	ref, ok := m.mdPreviewCursorRef()
	if !ok {
		return mdPreviewStop{}, false
	}
	return srcMap.stopAt(ref)
}

// setMdPreviewCursorRef places the cursor on one stop of the current load.
func (m *Model) setMdPreviewCursorRef(ref mdPreviewStopRef) {
	m.preview.cursor = mdPreviewCursorState{set: true, ref: ref, file: m.file.name, seq: m.file.loadSeq}
}

// setMdPreviewBlockCursor places the cursor on block bi's own stop. A negative
// bi clears it, so callers that computed "no block" can pass the result through
// unguarded.
func (m *Model) setMdPreviewBlockCursor(bi int) {
	if bi < 0 {
		m.clearMdPreviewBlockCursor()
		return
	}
	m.setMdPreviewCursorRef(mdPreviewStopRef{block: bi})
}

// setMdPreviewLineCursor places the cursor on one raw source line of block bi
// and marks that block expanded. lineIdx is the index into m.file.lines the row
// was painted from (mdPreviewLineAnchor.lineIdx), which is what the ref carries
// — see mdPreviewStopRef for why a line's identity is its source index and not
// its position among the block's rows.
//
// This is the only writer of expanded=true in production. Every other placement
// assigns a fresh mdPreviewCursorState literal and therefore collapses, which is
// what makes "one block expanded at a time, and it is always the block the
// cursor is in" hold without a single line of invalidation code.
func (m *Model) setMdPreviewLineCursor(bi, lineIdx int) {
	m.preview.cursor = mdPreviewCursorState{
		set:      true,
		ref:      mdPreviewStopRef{block: bi, onLine: true, line: lineIdx},
		file:     m.file.name,
		seq:      m.file.loadSeq,
		expanded: true,
	}
}

// clearMdPreviewBlockCursor takes the cursor off every stop, so nothing is
// highlighted until the reader moves it again.
func (m *Model) clearMdPreviewBlockCursor() {
	m.preview.cursor = mdPreviewCursorState{}
}

// mdPreviewViewportCenter is the rendered row at the vertical middle of the
// pane. It is where the cursor is seeded — by the first down/up press, and by
// `a` pressed with no cursor set. The center, not the top row: a block at the
// very top edge is the one the reader has just scrolled past, while the middle
// of the pane is where they are looking. Seeding at the top is the placement the
// scroll-derived highlight had, and the one this whole mechanism replaced.
func (m Model) mdPreviewViewportCenter() int {
	return m.layout.viewport.YOffset + max(0, m.layout.viewport.Height-1)/2
}

// mdPreviewCenterBlock returns the BLOCK nearest the vertical center of the
// current viewport, or -1 when the map cannot anchor anything. It is what `a`
// seeds on when the reader has not placed a cursor yet, and it goes through the
// same nearest-stop search the down/up seed uses rather than a second, parallel
// notion of "the middle of the pane": if the nearest stop is an annotation, its
// owning block is the answer, exactly as `a` would resolve it with the cursor
// actually parked there.
//
// A nearest stop that is the FILE-level annotation owns no block, so the first
// block is the answer there — the nearest block there is, and never -1, which
// would make `a` report a document it can perfectly well anchor as unanchorable.
func (m Model) mdPreviewCenterBlock(srcMap mdPreviewSourceMap) int {
	stops := srcMap.stops()
	i := mdPreviewNearestStop(stops, m.mdPreviewViewportCenter())
	if i < 0 {
		return -1
	}
	return max(stops[i].ref.block, 0)
}

// moveMdPreviewCursor is what down/up (j/k and the arrow keys, one action each)
// do in preview: steer the highlight, one stop per press.
//
// A stop is a block or one annotation painted under a block, in painted-row
// order (see mdPreviewSourceMap.stops), so `j` from a block lands on that
// block's own first annotation before reaching the next block. That is what
// makes an annotation selectable, and therefore deletable, without leaving
// preview.
//
// Two behaviors, split on whether a cursor exists yet:
//   - nothing selected: the press SEEDS the cursor at the stop nearest the
//     viewport center and stops there. It deliberately does not also move —
//     the first press is "start here", and moving as well would make the stop
//     the reader aimed at flick past before they saw it marked.
//   - a cursor exists: it moves one stop and clamps at the first and the last.
//     No wrap: arriving back at the top of a long document because a key was
//     held down is never what was meant.
//
// A document whose source map did not align (README.md is one, by design — see
// mdPreviewBuildSourceMap) has no stops to steer between, so these keys fall
// back to a one-row viewport scroll. That is what they did before the cursor
// existed, and it keeps an unanchorable document readable instead of making its
// main reading keys dead.
func (m *Model) moveMdPreviewCursor(delta int) {
	if !m.file.markdownPreviewable {
		return // preview stuck on for a file renderDiff will not preview; see panMarkdownPreview
	}
	body, srcMap := m.mdPreviewBody()
	stops := srcMap.stops()
	if len(stops) == 0 {
		m.scrollMarkdownPreview(delta)
		return
	}

	i := -1
	if ref, ok := m.mdPreviewCursorRef(); ok {
		i = mdPreviewStopIndex(stops, ref)
	}
	if i < 0 {
		if i = mdPreviewNearestStop(stops, m.mdPreviewViewportCenter()); i < 0 {
			return
		}
	} else {
		i = min(max(i+delta, 0), len(stops)-1)
	}
	m.setMdPreviewCursorRef(stops[i].ref)

	// content first, offset second: SetYOffset clamps against the viewport's own
	// content buffer, so a frame that has not been pushed yet would clamp the
	// target row away. Neither the horizontal cut nor the highlight depends on
	// YOffset, so composing the frame before the scroll paints the same bytes.
	m.layout.viewport.SetContent(m.mdPreviewFrame(body, srcMap))
	m.syncMdPreviewViewportToStop(stops[i])
}

// syncMdPreviewViewportToStop scrolls the viewport the least it can so the
// cursor's stop is fully visible, and not at all when it already is.
//
// This is syncViewportToCursor (app/ui/diffnav.go) in preview-row coordinates:
// same three cases, same clamp for a stop taller than the pane (show its start
// rather than its end, so the reader lands where it begins). Centering
// instead — what centerViewportOnCursor does for search and hunk jumps — was
// rejected: it makes every single press jump the page under the reader, and it
// is the top-edge behavior of the old scroll-derived highlight that this
// mechanism replaced.
func (m *Model) syncMdPreviewViewportToStop(s mdPreviewStop) {
	height := m.layout.viewport.Height
	if height <= 0 {
		return
	}
	top := m.layout.viewport.YOffset
	switch {
	case s.row < top:
		m.layout.viewport.SetYOffset(s.row)
	case s.endRow >= top+height:
		m.layout.viewport.SetYOffset(min(s.endRow-height+1, s.row))
	}
}

// dropMdPreviewCursorIfHidden clears the cursor when a viewport-only scroll has
// carried its stop entirely off the screen.
//
// This is the feature's core invariant: you always see what you are about to
// annotate or delete. J/K, the page keys, home/end and the wheel move the
// viewport and never the cursor, so without this the highlight would sit on a
// stop pages away, and `a`/`d` would act on something not on screen. A stop
// still partially visible keeps the cursor — it is still what the reader is
// looking at.
//
// Called from every viewport-only scroll path: afterMdPreviewViewportScroll for
// the key paths, and flushPreviewWheelPending (mdpreview_cache.go) once per
// wheel burst rather than once per event.
func (m *Model) dropMdPreviewCursorIfHidden() {
	if !m.file.markdownPreviewable {
		return
	}
	if _, ok := m.mdPreviewCursorRef(); !ok {
		return
	}
	_, srcMap := m.mdPreviewBody()
	stop, ok := m.mdPreviewCursorStop(srcMap)
	if !ok {
		// the map changed shape under the cursor (a width change that costs the
		// document its alignment, most plausibly); there is no stop to point at.
		m.clearMdPreviewBlockCursor()
		return
	}
	top := m.layout.viewport.YOffset
	bottom := top + max(0, m.layout.viewport.Height) - 1
	if stop.endRow < top || stop.row > bottom {
		m.clearMdPreviewBlockCursor()
	}
}

// afterMdPreviewViewportScroll is the tail every preview key that moves the
// viewport without moving the cursor shares: drop the cursor if its stop went
// off screen, then repaint so the highlight (or its absence) matches what is on
// screen.
func (m *Model) afterMdPreviewViewportScroll() {
	if !m.file.markdownPreviewable {
		return // preview stuck on for a file renderDiff will not preview; see panMarkdownPreview
	}
	m.dropMdPreviewCursorIfHidden()
	m.layout.viewport.SetContent(m.renderMarkdownPreview())
}
