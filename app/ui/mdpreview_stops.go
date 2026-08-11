package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// mdPreviewFileStopBlock is the block index a file-level annotation carries.
// A file-level annotation belongs to no block — it is painted above the whole
// document body (see mdPreviewPaintAnnotationsTracked) — so it needs a value no
// real block index can take.
const mdPreviewFileStopBlock = -1

// mdPreviewStopRef identifies one stop of the preview cursor by WHAT it is
// rather than by where it sits in the stop list.
//
// The distinction is the whole reason this type exists. A flat index into the
// stop list would drift the moment the list changed length under a cursor that
// did not move: pressing `A` prepends a file-level stop and pushes every other
// stop down by one, and deleting an annotation removes one from the middle. An
// identity survives both — block 4 is still block 4, and the second annotation
// under it is still the second annotation under it.
//
// The zero value is block 0's own stop, which is why "no cursor at all" is
// carried by mdPreviewCursorState.set and never by a sentinel ref. onAnnot is a
// bool rather than a -1 in annot for the same reason: a zero value that
// accidentally means "the first annotation under block 0" would be a trap for
// anyone constructing a ref in a test.
type mdPreviewStopRef struct {
	block   int  // index into srcMap.blocks(), or mdPreviewFileStopBlock
	onAnnot bool // false: the block itself; true: the annot'th annotation painted under it
	annot   int  // position among the annotations painted under that block, in paint order
}

// mdPreviewStop is one thing the preview cursor can stop on: a rendered block,
// or a single annotation painted under one. row/endRow are its inclusive row
// span in the PAINTED frame — the coordinates mdPreviewPaintAnnotationsTracked
// returns its map in, not the base render's.
//
// line/changeType are the annotation.Store key (File is always the current
// file), carried so `d` can delete exactly the annotation the reader is looking
// at without re-deriving it from a scan of the painted string. They are zero for
// a block stop.
type mdPreviewStop struct {
	ref        mdPreviewStopRef
	row        int
	endRow     int
	line       int
	changeType string
}

// mdPreviewAnnotAnchor is one painted annotation's place in the frame, recorded
// by the painter that put it there. It is the annotation-row counterpart of
// mdPreviewBlockAnchor, and it is produced the same way: as a side effect of the
// splice that created the rows, never by scanning the painted string afterwards.
// A scan would be a second source of truth about where a row is, free to drift
// from the one that actually placed it.
//
// block is the block whose rows this annotation was painted under, or
// mdPreviewFileStopBlock for the file-level one. ord is its position among that
// block's annotations, in paint order (ascending store order, i.e. ascending
// line).
type mdPreviewAnnotAnchor struct {
	block      int
	ord        int
	row        int
	endRow     int
	line       int
	changeType string
}

// stop returns the anchor as a cursor stop.
func (a mdPreviewAnnotAnchor) stop() mdPreviewStop {
	return mdPreviewStop{
		ref:        mdPreviewStopRef{block: a.block, onAnnot: true, annot: a.ord},
		row:        a.row,
		endRow:     a.endRow,
		line:       a.line,
		changeType: a.changeType,
	}
}

// stops lists everything the preview cursor can stop on, in the order they are
// painted down the frame: the file-level annotation first (it sits above the
// document body), then each block followed by the annotations painted under it.
//
// That order is what makes `j` from a block land on that block's own first
// annotation rather than skipping to the next block, and it falls out of the
// paint geometry rather than being imposed here: block i's annotation rows
// occupy exactly the gap between block i's endRow and block i+1's row (see
// mdPreviewPaintAnnotationsTracked's shift accounting), so listing them in this
// order is also listing them in ascending row order.
//
// Returns nil for a map that anchors nothing — an unaligned document or one with
// no blocks. There is nothing to steer between there, and the callers fall back
// to a plain row scroll.
func (sm mdPreviewSourceMap) stops() []mdPreviewStop {
	anchors := sm.blocks()
	if !sm.aligned || len(anchors) == 0 {
		return nil
	}

	out := make([]mdPreviewStop, 0, len(anchors)+len(sm.annots))
	next := 0
	take := func(block int) {
		for next < len(sm.annots) && sm.annots[next].block == block {
			out = append(out, sm.annots[next].stop())
			next++
		}
	}

	take(mdPreviewFileStopBlock)
	for i := range anchors {
		out = append(out, mdPreviewStop{ref: mdPreviewStopRef{block: i}, row: anchors[i].row, endRow: anchors[i].endRow})
		take(i)
	}
	// defensive: annots is built grouped by ascending block, so the walk above
	// consumes all of it. An anchor the walk somehow skipped is still made
	// reachable rather than dropped — an annotation the reader can see but can
	// never select is the one outcome worth avoiding here.
	for ; next < len(sm.annots); next++ {
		out = append(out, sm.annots[next].stop())
	}
	return out
}

// stopAt resolves one ref against the map without building the whole stop list,
// which is what keeps the per-frame paths (the highlight, the
// scrolled-out-of-view check) allocation-free on a large document. ok is false
// when the ref names nothing in this map: a block index past the end, or an
// annotation that has since been deleted.
func (sm mdPreviewSourceMap) stopAt(ref mdPreviewStopRef) (mdPreviewStop, bool) {
	anchors := sm.blocks()
	if !sm.aligned || len(anchors) == 0 {
		return mdPreviewStop{}, false
	}
	if !ref.onAnnot {
		if ref.block < 0 || ref.block >= len(anchors) {
			return mdPreviewStop{}, false
		}
		return mdPreviewStop{ref: ref, row: anchors[ref.block].row, endRow: anchors[ref.block].endRow}, true
	}
	for _, a := range sm.annots {
		if a.block == ref.block && a.ord == ref.annot {
			return a.stop(), true
		}
	}
	return mdPreviewStop{}, false
}

// mdPreviewStopIndex is the position of ref in stops, or -1 when the stop list
// no longer holds it.
func mdPreviewStopIndex(stops []mdPreviewStop, ref mdPreviewStopRef) int {
	for i := range stops {
		if stops[i].ref == ref {
			return i
		}
	}
	return -1
}

// mdPreviewNearestStop returns the index of the stop nearest row center, or -1
// for an empty list. This is the seeding rule shared by the first down/up press
// and by `a` with no cursor placed.
//
// "Nearest" is measured against a stop's whole span, so a center row falling
// inside one picks it at distance 0. When the center lands in the padding
// between two, the preceding one wins a tie — it is the one whose text the
// reader has just read.
//
// It walks rather than binary-searches: stops are row-ascending by construction
// (see stops()), but the defensive tail there can append out-of-order entries,
// and a linear scan is correct whatever order it is handed.
func mdPreviewNearestStop(stops []mdPreviewStop, center int) int {
	best, bestDist := -1, 0
	for i := range stops {
		dist := 0
		switch {
		case center < stops[i].row:
			dist = stops[i].row - center
		case center > stops[i].endRow:
			dist = center - stops[i].endRow
		}
		if best < 0 || dist < bestDist {
			best, bestDist = i, dist
		}
	}
	return best
}

// mdPreviewDeleteNeedsAnnotationHint is the status-bar message for a `d` that
// has no annotation to delete — the cursor is on a block, or nowhere at all.
// Deleting a block's annotations wholesale from a block stop was rejected: `d`
// removes one comment everywhere else in the app, and a key that removes an
// unbounded number of them on a near miss is not a milder version of that.
const mdPreviewDeleteNeedsAnnotationHint = "Select an annotation with j/k to delete it"

// mdPreviewDeleteAnnotation is `d` inside markdown preview: delete the
// annotation the cursor is stopped on, and leave the cursor on the block that
// owned it.
//
// It is routed from handleMdPreviewAction rather than being allowed to fall
// through to deleteAnnotation (app/ui/annotate.go), and the detour is required:
// that function resolves its target from m.nav.diffCursor plus
// m.annot.cursorOnAnnotation, neither of which preview drives — the diff cursor
// points at whichever block the last `a`/click aimed at, and cursorOnAnnotation
// is cleared by mdPreviewStartAnnotationAt. Falling through would delete the
// wrong comment or, far more often, silently nothing.
//
// Landing on the owning block afterwards is the one placement that is always
// available: a block stop exists for every valid block index whatever the
// annotations do, so the cursor can never be left pointing past the end of the
// stop list. Deleting the FILE-level annotation has no owning block, so it lands
// on the first block instead.
//
// The tree filter is refreshed exactly the way the source-view delete refreshes
// it, and a selection the refresh moved off this file is followed with a load —
// the same behavior as source view, and a deliberate one here: the alternative
// is a file tree that claims this file still carries annotations after its last
// one was deleted.
func (m *Model) mdPreviewDeleteAnnotation() tea.Cmd {
	if !m.file.markdownPreviewable {
		return nil // preview stuck on for a file renderDiff will not preview; see panMarkdownPreview
	}
	_, srcMap := m.mdPreviewBody()
	stop, ok := m.mdPreviewCursorStop(srcMap)
	if !ok || !stop.ref.onAnnot {
		m.preview.hint = mdPreviewDeleteNeedsAnnotationHint
		return nil
	}
	if !m.store.Delete(m.file.name, stop.line, stop.changeType) {
		// the stop named an annotation the store no longer holds; nothing was
		// removed, so say so rather than repainting an unchanged frame silently.
		m.preview.hint = mdPreviewDeleteNeedsAnnotationHint
		return nil
	}

	m.setMdPreviewBlockCursor(max(stop.ref.block, 0))
	m.pendingAnnotJump = nil    // clear before RefreshFilter, which may trigger a file load
	m.nav.pendingHunkJump = nil // same
	m.tree.RefreshFilter(m.annotatedFiles())
	if newFile := m.tree.SelectedFile(); newFile != "" && newFile != m.file.name {
		return m.requestFileDiff(newFile)
	}
	m.repaintMdPreviewAfterStopChange()
	return nil
}

// repaintMdPreviewAfterStopChange redraws the frame after something changed how
// many rows are painted, and reconciles the cursor with the new stop list first.
//
// Deleting an annotation moves every stop below it, so the frame MUST be
// recomposed from a freshly painted body rather than patched: the row numbers in
// the map handed to mdPreviewFrame have to be the row numbers of the string it
// is cutting. A cursor whose ref no longer resolves is cleared rather than
// clamped to a neighbor — there is no honest neighbor for a stop that stopped
// existing.
func (m *Model) repaintMdPreviewAfterStopChange() {
	body, srcMap := m.mdPreviewBody()
	stop, ok := m.mdPreviewCursorStop(srcMap)
	if !ok {
		m.clearMdPreviewBlockCursor()
	}
	// content first, offset second, for the reason moveMdPreviewCursor gives:
	// SetYOffset clamps against the viewport's own content buffer.
	m.layout.viewport.SetContent(m.mdPreviewFrame(body, srcMap))
	if ok {
		m.syncMdPreviewViewportToStop(stop)
	}
}
