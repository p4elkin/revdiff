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
//
// onLine/line is the raw-source level of the cursor, and it is a bool-plus-int
// pair for exactly the reason onAnnot/annot is: line 0 of the file is a real
// line, so a zero value that accidentally means "the first source line of the
// document" would be the same trap. onLine and onAnnot are mutually exclusive —
// a stop is a block, one of its raw source lines, or one annotation.
//
// line is the index into the []diff.DiffLine the render came from (i.e.
// mdPreviewLineAnchor.lineIdx), NOT a position among the block's raw rows. That
// is this type's own identity rule applied one level down: the source line a
// reader picked is still the same source line after an annotation is added under
// it or a blank row stops producing an anchor, while its position in the list
// would drift. It is also the coordinate `a` needs, so annotating a raw line
// reads straight off the ref.
//
// A line ref is only ever resolvable while its block is expanded, which is the
// invariant mdPreviewCursorState.expanded carries: onLine implies expanded.
type mdPreviewStopRef struct {
	block   int  // index into srcMap.blocks(), or mdPreviewFileStopBlock
	onAnnot bool // false: the block itself; true: the annot'th annotation painted under it
	annot   int  // position among the annotations painted under that block, in paint order
	onLine  bool // true: one raw source line of the block, which must be expanded
	line    int  // index into the []diff.DiffLine, i.e. mdPreviewLineAnchor.lineIdx
}

// mdPreviewStop is one thing the preview cursor can stop on: a rendered block,
// one raw source line of an expanded block, or a single annotation painted under
// either. row/endRow are its inclusive row span in the PAINTED frame — the
// coordinates mdPreviewPaintAnnotationsTracked returns its map in, not the base
// render's. A raw-line stop spans the single row it painted, because one source
// line is one rendered row (see mdPreviewLineAnchor).
//
// line/changeType are the annotation.Store key (File is always the current
// file), carried so `d` can delete exactly the annotation the reader is looking
// at without re-deriving it from a scan of the painted string. They are zero for
// a block stop and for a raw-line stop — neither is an annotation, and a raw
// line's own source index is carried by ref.line instead.
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

// stop returns the anchor as a cursor stop. The span is the single row the
// anchor painted: one source line is one rendered row, which is what keeps the
// raw row for a source line pure arithmetic off the block's start row.
func (a mdPreviewLineAnchor) stop() mdPreviewStop {
	return mdPreviewStop{
		ref:    mdPreviewStopRef{block: a.block, onLine: true, line: a.lineIdx},
		row:    a.row,
		endRow: a.row,
	}
}

// expandedBlock is the block currently drawn as its raw source, or -1 when none
// is. It is read off sm.lines rather than passed in, because mdPreviewExpandBlock
// only ever records line anchors for the one block it expanded — so the anchors
// themselves already say which block that was, and no caller of stops() has to
// carry the answer alongside the map it was handed.
func (sm mdPreviewSourceMap) expandedBlock() int {
	if len(sm.lines) == 0 {
		return -1
	}
	return sm.lines[0].block
}

// lineStops is the expanded block's raw source lines as cursor stops, in paint
// order (which is source order, since mdPreviewExpandBlock walks the span
// forwards). Empty for any block that is not expanded.
func (sm mdPreviewSourceMap) lineStops(block int) []mdPreviewStop {
	out := make([]mdPreviewStop, 0, len(sm.lines))
	for _, la := range sm.lines {
		if la.block == block {
			out = append(out, la.stop())
		}
	}
	return out
}

// stops lists everything the preview cursor can stop on, in the order they are
// painted down the frame: the file-level annotation first (it sits above the
// document body), then each block followed by the annotations painted under it.
//
// That order is what makes `j` from a block land on that block's own first
// annotation rather than skipping to the next block. For an ordinary block it
// falls out of the paint geometry rather than being imposed here: block i's
// annotation rows occupy exactly the gap between block i's endRow and block
// i+1's row (see mdPreviewPaintAnnotationsTracked's shift accounting), so
// listing them in this order is also listing them in ascending row order.
//
// The EXPANDED block is the one place where it no longer falls out, and so the
// one place the order is imposed. Its raw source lines and its annotations
// interleave — a comment sits under the line it belongs to, not at the bottom of
// the block — so the two lists are merged by ascending row (see
// mdPreviewMergeStopsByRow). The block's own stop is dropped while it is
// expanded: the reader is looking at source lines, and a stop covering all of
// them at once would make `a` ambiguous about which line it meant.
//
// Returns nil for a map that anchors nothing — an unaligned document or one with
// no blocks. There is nothing to steer between there, and the callers fall back
// to a plain row scroll.
func (sm mdPreviewSourceMap) stops() []mdPreviewStop {
	anchors := sm.blocks()
	if !sm.aligned || len(anchors) == 0 {
		return nil
	}

	expanded := sm.expandedBlock()
	out := make([]mdPreviewStop, 0, len(anchors)+len(sm.lines)+len(sm.annots))
	next := 0
	take := func(block int) []mdPreviewStop {
		first := next
		for next < len(sm.annots) && sm.annots[next].block == block {
			next++
		}
		taken := make([]mdPreviewStop, 0, next-first)
		for _, a := range sm.annots[first:next] {
			taken = append(taken, a.stop())
		}
		return taken
	}

	out = append(out, take(mdPreviewFileStopBlock)...)
	for i := range anchors {
		annots := take(i)
		if i == expanded {
			out = append(out, mdPreviewMergeStopsByRow(sm.lineStops(i), annots)...)
			continue
		}
		out = append(out, mdPreviewStop{ref: mdPreviewStopRef{block: i}, row: anchors[i].row, endRow: anchors[i].endRow})
		out = append(out, annots...)
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

// mdPreviewMergeStopsByRow merges two row-ascending stop lists into one. A tie
// gives the raw line the earlier place: an annotation is painted UNDER the line
// it belongs to, so on the same row the line is what the reader reaches first.
func mdPreviewMergeStopsByRow(lines, annots []mdPreviewStop) []mdPreviewStop {
	out := make([]mdPreviewStop, 0, len(lines)+len(annots))
	i, j := 0, 0
	for i < len(lines) && j < len(annots) {
		if lines[i].row <= annots[j].row {
			out = append(out, lines[i])
			i++
			continue
		}
		out = append(out, annots[j])
		j++
	}
	out = append(out, lines[i:]...)
	return append(out, annots[j:]...)
}

// stopAt resolves one ref against the map without building the whole stop list,
// which is what keeps the per-frame paths (the highlight, the
// scrolled-out-of-view check) allocation-free on a large document. ok is false
// when the ref names nothing in this map: a block index past the end, an
// annotation that has since been deleted, or a raw source line of a block that
// is no longer expanded — which is how a collapse takes the cursor off a line
// stop without anyone having to clear it there.
func (sm mdPreviewSourceMap) stopAt(ref mdPreviewStopRef) (mdPreviewStop, bool) {
	anchors := sm.blocks()
	if !sm.aligned || len(anchors) == 0 {
		return mdPreviewStop{}, false
	}
	if ref.onLine {
		for _, la := range sm.lines {
			if la.block == ref.block && la.lineIdx == ref.line {
				return la.stop(), true
			}
		}
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

// mdPreviewBlockStopRange is the first and last index of block bi's own stops in
// a stop list. ok is false when the block has no stop there at all.
//
// A block's stops are contiguous because stops() emits them in painted-row order
// and every row a block owns — its own, its raw source rows while expanded, and
// the annotation rows spliced under them — sits between that block's first row
// and the next block's. That is what lets j/k be clamped to one block's stops by
// two indices rather than by a per-step predicate.
func mdPreviewBlockStopRange(stops []mdPreviewStop, bi int) (lo, hi int, ok bool) {
	for i := range stops {
		if stops[i].ref.block != bi {
			continue
		}
		if !ok {
			lo, ok = i, true
		}
		hi = i
	}
	return lo, hi, ok
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
