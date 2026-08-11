package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
)

// mdPreviewPaintAnnotations splices this file's annotations onto rendered — a
// base preview render from mdPreviewBaseRender, still full-width and uncut —
// so the result carries the same annotation rows the diff pane would show,
// positioned under the block each one belongs to. Callers must splice BEFORE
// applyMdPreviewScroll, never after: an annotation row is ordinary preview
// content once it is in the string, and has to pan and truncate exactly like
// every other row rather than being drawn on top of the cut result.
//
// Painting always goes through the diff pane's own renderAnnotationOrInput /
// renderFileAnnotationHeader / renderWrappedAnnotation — never a parallel
// painter — so annotationVisualRows (app/ui/annotate.go) stays the single
// source of truth for how an annotation's text wraps and styles. That is also
// what makes a painted row byte-identical to the same annotation's row in
// source view: both paths bottom out in the same function.
//
// srcMap.Aligned=false degrades to mdPreviewAnnotateDegraded: with no
// trustworthy row mapping, nothing may be spliced into the body, so every
// annotation is listed as one group at the top instead — visible, if not
// positioned. An aligned map with zero blocks (an empty or all-stripped
// document) has nowhere to splice a line-level annotation either, and takes
// the same path.
func (m Model) mdPreviewPaintAnnotations(rendered string, srcMap mdPreviewSourceMap) string {
	out, _ := m.mdPreviewPaintAnnotationsTracked(rendered, srcMap)
	return out
}

// mdPreviewPaintAnnotationsTracked is mdPreviewPaintAnnotations plus the source
// map re-expressed in the coordinates of the string it returns. Splicing rows
// into a render moves every row below the splice point, so the map that came
// out of mdPreviewBaseRender describes the BASE render and stops describing the
// painted one the moment a single annotation row is inserted. Anything that
// reads a row number off the painted render — the scroll-following block
// highlight, and later the click-to-block mapping — must use the map this
// returns, not the one it was handed.
//
// The accounting is exact rather than estimated: file-level rows are prepended
// ahead of every block, and block i's own annotation rows are inserted at its
// EndRow, which is always before block i+1's Row. So block i moves by the
// file-level row count plus every earlier block's annotation row count, and by
// nothing else.
//
// Same wrapper/implementation split as spliceMermaidArt over
// spliceMermaidArtTracked: callers that only want the painted string do not
// have to carry a map they will not read.
func (m Model) mdPreviewPaintAnnotationsTracked(rendered string, srcMap mdPreviewSourceMap) (string, mdPreviewSourceMap) {
	all := m.store.Get(m.file.name)
	liveIdx, liveOK := m.mdPreviewLiveInputTarget()
	// a brand-new file-level annotation (A with nothing saved yet for this
	// file) has no entry in `all` either, same gap startPreviewAnnotationAt
	// closed for line-level input above. mdPreviewLiveInputTarget only ever
	// reports true for the line-level case (see its own doc comment), so the
	// file-level live-input state has to be checked directly here — without
	// it the early return below would skip renderFileAnnotationHeader
	// entirely and the text a reader is actively typing into the file-level
	// box would stay invisible until the moment it is saved.
	liveFileAnnotating := m.annot.annotating && m.annot.fileAnnotating
	if len(all) == 0 && !liveOK && !liveFileAnnotating {
		return rendered, srcMap // nothing spliced, so every row kept its number
	}

	anchors := srcMap.blocks()
	if !srcMap.Aligned || len(anchors) == 0 {
		// the degraded path prepends one group and anchors nothing; there was no
		// usable map to shift, and there is none to hand back either. A live
		// input has nowhere to go here either — startPreviewAnnotation and
		// clickPreviewDiff both already refuse to start one when the map cannot
		// anchor anything, so liveOK cannot be true in this branch in practice.
		return m.mdPreviewAnnotateDegraded(rendered, all), mdPreviewSourceMap{}
	}

	annotationMap, fileComment := m.buildAnnotationMap()
	blockRows := make([][]string, len(anchors))
	liveCovered := false
	for _, a := range all {
		if a.Line == 0 {
			continue // file-level: painted separately, always at the very top
		}
		bi := m.mdPreviewResolveBlock(srcMap, a)
		blockRows[bi] = append(blockRows[bi], m.mdPreviewRenderOne(a, annotationMap)...)
		if idx, ok := m.mdPreviewLineIndex(a.Line, a.Type); liveOK && ok && idx == liveIdx {
			// mdPreviewRenderOne already routed through renderAnnotationOrInput for
			// this exact idx, which draws the live input in place of the stored
			// comment (see renderAnnotationOrInput's own annotating/diffCursor
			// check) — editing an EXISTING annotation is covered by the loop
			// above with no separate step needed.
			liveCovered = true
		}
	}
	// a brand-new annotation (nothing in the store yet for this line) has no
	// entry in `all` for the loop above to iterate, so renderAnnotationOrInput
	// was never called for it — without this, the input a reader is actively
	// typing would stay invisible until the moment it is saved. anchorAtLine
	// resolves the same containing-block case mdPreviewResolveBlock's own
	// success path does; the idx==0 fallback only matters for a defensive
	// out-of-range diffCursor, since every caller of startPreviewAnnotation and
	// clickPreviewDiff targets an exact block's StartLine.
	if liveOK && !liveCovered {
		bi := max(srcMap.anchorAtLine(liveIdx), 0)
		var b strings.Builder
		m.renderAnnotationOrInput(&b, liveIdx, annotationMap)
		blockRows[bi] = append(blockRows[bi], mdPreviewRowsFromBuilder(&b)...)
	}

	var top strings.Builder
	m.renderFileAnnotationHeader(&top, fileComment)
	topRows := mdPreviewRowsFromBuilder(&top)

	// shifted anchors are a copy, never an in-place edit: srcMap's backing array
	// belongs to the render cache (mdpreview_cache.go) and is handed to every
	// later repaint.
	shifted := make([]mdPreviewBlockAnchor, len(anchors))
	delta := len(topRows)
	for i := range anchors {
		shifted[i] = anchors[i]
		shifted[i].Row += delta
		shifted[i].EndRow += delta
		delta += len(blockRows[i])
	}

	rows := strings.Split(rendered, "\n")
	out := topRows
	bi := 0
	for i, line := range rows {
		out = append(out, line)
		for bi < len(anchors) && anchors[bi].EndRow == i {
			out = append(out, blockRows[bi]...)
			bi++
		}
	}
	// defensive: a block whose EndRow never matched a row index (should not
	// happen — EndRow is derived from this same rendered string) still gets
	// its annotations painted rather than silently dropped.
	for ; bi < len(anchors); bi++ {
		out = append(out, blockRows[bi]...)
	}
	return strings.Join(out, "\n"), mdPreviewSourceMap{Aligned: true, anchors: shifted}
}

// mdPreviewAnnotateDegraded lists every one of this file's annotations —
// file-level first, then line-level in store order (ascending by Line) — as
// one group prepended to rendered. This is the fallback for an unaligned map
// or a document with no blocks to splice into: there is no trustworthy row to
// attach a line-level annotation to, so nothing is hidden and nothing is
// guessed at either.
func (m Model) mdPreviewAnnotateDegraded(rendered string, all []annotation.Annotation) string {
	annotationMap, fileComment := m.buildAnnotationMap()
	var top strings.Builder
	m.renderFileAnnotationHeader(&top, fileComment)
	for _, a := range all {
		if a.Line == 0 {
			continue // already painted above
		}
		for _, row := range m.mdPreviewRenderOne(a, annotationMap) {
			top.WriteString(row)
			top.WriteString("\n")
		}
	}
	group := top.String()
	if group == "" {
		return rendered
	}
	return group + rendered
}

// mdPreviewRenderOne renders one line-level annotation's visual rows. When
// a's line still resolves to a diff-line index (the overwhelmingly common
// case), it goes through
// renderAnnotationOrInput exactly as the diff pane would, which is what makes
// the result byte-identical to source view's own rendering of the same
// annotation. When a's line no longer resolves to any current diff line — the
// file changed since the annotation was saved — renderAnnotationOrInput has
// no index to key off, so this falls back to renderWrappedAnnotation
// directly: still the same chokepoint (annotationVisualRows), just without
// the idx-based lookup wrapper.
func (m Model) mdPreviewRenderOne(a annotation.Annotation, annotationMap map[annotLineKey]string) []string {
	var b strings.Builder
	if idx, ok := m.mdPreviewLineIndex(a.Line, a.Type); ok {
		m.renderAnnotationOrInput(&b, idx, annotationMap)
	} else {
		m.renderWrappedAnnotation(&b, " ", m.annotPrefix(), a.Comment)
	}
	return mdPreviewRowsFromBuilder(&b)
}

// mdPreviewResolveBlock resolves which block a line-level annotation should
// be spliced under. Caller guarantees len(srcMap.blocks()) > 0.
//
// Three outcomes, matching the source map's own resolution rules:
//   - a's line still exists and anchorAtLine finds a containing block (the
//     block starts exactly there, or the line falls inside a block's span
//     without being its first line, e.g. a mid-paragraph annotation): that
//     block.
//   - a's line still exists but precedes every block (there is nothing to be
//     "inside", e.g. an annotation on a blank separator line before the very
//     first block): the first block — the nearest one there is.
//   - a's line no longer resolves to any current diff line at all (the file
//     changed since the annotation was saved): the last block, so an orphaned
//     annotation is never lost, just pinned to the end of the document.
func (m Model) mdPreviewResolveBlock(srcMap mdPreviewSourceMap, a annotation.Annotation) int {
	anchors := srcMap.blocks()
	idx, ok := m.mdPreviewLineIndex(a.Line, a.Type)
	if !ok {
		return len(anchors) - 1
	}
	if bi := srcMap.anchorAtLine(idx); bi >= 0 {
		return bi
	}
	return 0
}

// mdPreviewLineIndex finds the index into m.file.lines whose display line
// number and change type match (line, changeType) — the same coordinate
// mdPreviewBlockAnchor.StartLine and m.nav.diffCursor both use. ok is false
// when no current line matches, which is exactly the "line no longer exists"
// case: the annotation was saved against content the file no longer has (a
// reload replaced it with different content, most commonly).
func (m Model) mdPreviewLineIndex(line int, changeType string) (int, bool) {
	for i, dl := range m.file.lines {
		if dl.ChangeType == diff.ChangeDivider {
			continue
		}
		if m.diffLineNum(dl) == line && string(dl.ChangeType) == changeType {
			return i, true
		}
	}
	return 0, false
}

// mdPreviewLiveInputTarget reports the diff-line index a currently-open
// line-level annotation input targets, and whether one is open at all.
// Mirrors renderAnnotationOrInput's own gate (m.annot.annotating &&
// !m.annot.fileAnnotating && idx == m.nav.diffCursor) exactly, so the two
// agree on what "the line being typed" means — file-level input is handled
// separately by renderFileAnnotationHeader and has no block to attach to.
func (m Model) mdPreviewLiveInputTarget() (int, bool) {
	if !m.annot.annotating || m.annot.fileAnnotating {
		return 0, false
	}
	if m.nav.diffCursor < 0 || m.nav.diffCursor >= len(m.file.lines) {
		return 0, false
	}
	return m.nav.diffCursor, true
}

// startPreviewAnnotation begins creating a line-level annotation anchored to
// the block markdown preview is currently highlighting. It calls
// mdPreviewHighlightAnchor directly — the exact function that decides which
// block the on-screen highlight marks — rather than computing a second,
// parallel notion of "the current block": keyboard aim (`a`/enter) and the
// visible highlight can never disagree about which block gets the comment.
//
// Returns nil when there is nothing to anchor to (see mdPreviewHighlightAnchor:
// an unaligned map, an empty document, or a viewport scrolled above the first
// block) — annotation creation is simply refused, the same as pressing `a` on
// a diff divider in source view.
func (m *Model) startPreviewAnnotation() tea.Cmd {
	_, srcMap := m.mdPreviewBody()
	bi := m.mdPreviewHighlightAnchor(srcMap)
	if bi < 0 {
		return nil
	}
	return m.startPreviewAnnotationAt(srcMap.blocks()[bi].StartLine)
}

// startPreviewAnnotationAt is the shared core behind startPreviewAnnotation
// (keyboard aim) and clickPreviewDiff (mouse aim): point the diff cursor at
// idx — an index into m.file.lines, the same coordinate
// mdPreviewBlockAnchor.StartLine uses — and run the ordinary startAnnotation
// path, so a preview annotation is created and saved through the exact same
// code a source-view one is (see saveAnnotation / m.diffLineNum).
//
// startAnnotation calls ensureLineAnnotationInputVisible, which does its own
// diff-line-coordinate viewport math (cursorViewportY / wrappedLineCount) —
// meaningless once the viewport shows a whole-document glamour render
// instead of one row per source line. Saving and restoring
// viewport.YOffset around the call is what keeps that math from silently
// repositioning the preview; it is also what makes ActionConfirm safe to
// dispatch regardless of pane focus (see the mdPreviewAllowedActions comment
// on the confirm case) — the viewport never moves, so there is nothing for a
// stale TOC-jump-shaped side effect to have gotten wrong.
//
// The content refresh after restoring the offset is what makes the freshly
// started input actually visible: mdPreviewPaintAnnotationsTracked only draws
// a live input row for the exact line this Model is currently annotating, so
// without a fresh SetContent here the viewport would keep showing the
// pre-annotation frame until the next keystroke's own re-render.
func (m *Model) startPreviewAnnotationAt(idx int) tea.Cmd {
	savedOffset := m.layout.viewport.YOffset
	m.nav.diffCursor = idx
	m.annot.cursorOnAnnotation = false
	cmd := m.startAnnotation()
	m.layout.viewport.SetYOffset(savedOffset)
	m.layout.viewport.SetContent(m.renderDiff())
	return cmd
}

// mdPreviewRowsFromBuilder splits a throwaway builder's accumulated output —
// always a sequence of complete "row\n" writes, the convention every painter
// this file calls (renderAnnotationOrInput, renderFileAnnotationHeader,
// renderWrappedAnnotation) follows — back into individual rows. Returns nil
// for an empty builder (nothing was painted) rather than a single empty row.
func mdPreviewRowsFromBuilder(b *strings.Builder) []string {
	s := b.String()
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
