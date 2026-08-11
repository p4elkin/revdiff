package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/annotation"
)

// mdPreviewPaintAnnotationsTracked splices this file's annotations onto
// rendered — a base preview render from mdPreviewBaseRender, still uncut —
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
// srcMap.aligned=false degrades to mdPreviewAnnotateDegraded: with no
// trustworthy row mapping, nothing may be spliced into the body, so every
// annotation is listed as one group at the top instead — visible, if not
// positioned. An aligned map with zero blocks (an empty or all-stripped
// document) has nowhere to splice a line-level annotation either, and takes
// the same path.
//
// It also returns the source map re-expressed in the coordinates of the string
// it returns. Splicing rows into a render moves every row below the splice
// point, so the map that came out of mdPreviewBaseRender describes the BASE
// render and stops describing the painted one the moment a single annotation
// row is inserted. Anything that reads a row number off the painted render —
// the scroll-following block highlight and the click-to-block mapping — must
// use the map this returns, not the one it was handed.
//
// The accounting is exact rather than estimated: file-level rows are prepended
// ahead of every block, and block i's own annotation rows are inserted at its
// endRow, which is always before block i+1's row. So block i moves by the
// file-level row count plus every earlier block's annotation row count, and by
// nothing else.
func (m Model) mdPreviewPaintAnnotationsTracked(rendered string, srcMap mdPreviewSourceMap) (string, mdPreviewSourceMap) {
	all := m.store.Get(m.file.name)
	liveIdx, liveOK := m.mdPreviewLiveInputTarget()
	// mdPreviewLiveInputTarget only ever reports true for the line-level case
	// (see its own doc comment), so a brand-new file-level annotation has to be
	// checked directly here — without it the early return below would skip
	// renderFileAnnotationHeader entirely. It is the same invisible-until-saved
	// gap the liveOK && !liveCovered branch closes for line-level input, and
	// that branch carries the full explanation.
	liveFileAnnotating := m.annot.annotating && m.annot.fileAnnotating
	if len(all) == 0 && !liveOK && !liveFileAnnotating {
		return rendered, srcMap // nothing spliced, so every row kept its number
	}

	anchors := srcMap.blocks()
	if !srcMap.aligned || len(anchors) == 0 {
		// the degraded path prepends one group and anchors nothing; there was no
		// usable map to shift, and there is none to hand back either. A live
		// input has nowhere to go here either — mdPreviewStartAnnotation and
		// mdPreviewClickDiff both already refuse to start one when the map cannot
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
		// resolved once and threaded down: mdPreviewLineIndex is a linear scan
		// of m.file.lines, and it used to run three times per annotation per
		// repaint (inside resolve, inside render, and for the liveIdx compare).
		idx, idxOK := m.mdPreviewLineIndex(a.Line, a.Type)
		bi := srcMap.resolveBlock(idx, idxOK)
		blockRows[bi] = append(blockRows[bi], m.mdPreviewRenderOne(a, annotationMap, idx, idxOK)...)
		if liveOK && idxOK && idx == liveIdx {
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
	// resolves the same containing-block case resolveBlock's own success path
	// does; the idx==0 fallback only matters for a defensive out-of-range
	// diffCursor, since every caller of mdPreviewStartAnnotation and
	// mdPreviewClickDiff targets an exact block's startLine.
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
		shifted[i].row += delta
		shifted[i].endRow += delta
		delta += len(blockRows[i])
	}

	rows := strings.Split(rendered, "\n")
	out := topRows
	bi := 0
	for i, line := range rows {
		out = append(out, line)
		for bi < len(anchors) && anchors[bi].endRow == i {
			out = append(out, blockRows[bi]...)
			bi++
		}
	}
	// defensive: a block whose endRow never matched a row index (should not
	// happen — endRow is derived from this same rendered string) still gets
	// its annotations painted rather than silently dropped.
	for ; bi < len(anchors); bi++ {
		out = append(out, blockRows[bi]...)
	}
	return strings.Join(out, "\n"), mdPreviewSourceMap{aligned: true, anchors: shifted}
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
		idx, idxOK := m.mdPreviewLineIndex(a.Line, a.Type)
		for _, row := range m.mdPreviewRenderOne(a, annotationMap, idx, idxOK) {
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

// mdPreviewRenderOne renders one line-level annotation's visual rows. idx/idxOK
// are a's line resolved through mdPreviewLineIndex by the caller — passed in
// rather than looked up here, because that lookup is a linear scan and every
// caller needs the same answer for its own reasons.
//
// When a's line still resolves to a diff-line index (the overwhelmingly common
// case), it goes through renderAnnotationOrInput exactly as the diff pane
// would, which is what makes the result byte-identical to source view's own
// rendering of the same annotation. When a's line no longer resolves to any
// current diff line — the file changed since the annotation was saved —
// renderAnnotationOrInput has no index to key off, so this falls back to
// renderWrappedAnnotation directly: still the same chokepoint
// (annotationVisualRows), just without the idx-based lookup wrapper.
func (m Model) mdPreviewRenderOne(a annotation.Annotation, annotationMap map[annotLineKey]string,
	idx int, idxOK bool) []string {
	var b strings.Builder
	if idxOK {
		m.renderAnnotationOrInput(&b, idx, annotationMap)
	} else {
		m.renderWrappedAnnotation(&b, " ", m.annotPrefix(), a.Comment)
	}
	return mdPreviewRowsFromBuilder(&b)
}

// mdPreviewLineIndex is findDiffLineIndex (app/ui/annotlist.go) in the
// (index, ok) convention the callers in this file want — the same
// diffLineNum(dl)/dl.ChangeType lookup the annotation list already does, not a
// second scan of m.file.lines with its own rules. The result is the coordinate
// mdPreviewBlockAnchor.startLine and m.nav.diffCursor both use. ok is false
// when no current line matches, which is exactly the "line no longer exists"
// case: the annotation was saved against content the file no longer has (a
// reload replaced it with different content, most commonly).
func (m Model) mdPreviewLineIndex(line int, changeType string) (int, bool) {
	idx := m.findDiffLineIndex(line, changeType)
	if idx < 0 {
		return 0, false
	}
	return idx, true
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

// mdPreviewStartAnnotation begins creating a line-level annotation anchored to
// the block markdown preview is currently highlighting. It calls
// mdPreviewHighlightAnchor directly — the exact function that decides which
// block the on-screen highlight marks — rather than computing a second,
// parallel notion of "the current block": keyboard aim (`a`/enter) and the
// visible highlight can never disagree about which block gets the comment.
//
// Returns nil when there is nothing to anchor to (see mdPreviewHighlightAnchor:
// an unaligned map, an empty document, or a viewport scrolled above the first
// block) — annotation creation is refused, the same as pressing `a` on a diff
// divider in source view.
//
// A refusal sets a transient status-bar hint rather than being silent. A reader
// who presses `a` on a document whose source map did not align (README.md is
// one, by design — see mdPreviewBuildSourceMap) otherwise gets a byte-identical
// frame back, with no way to tell "this document cannot be anchored" from "the
// key is not bound". The hint is the same mechanism outputState and
// compactState use for their own refusals, and clears on the next key press.
func (m *Model) mdPreviewStartAnnotation() tea.Cmd {
	_, srcMap := m.mdPreviewBody()
	bi := m.mdPreviewHighlightAnchor(srcMap)
	if bi < 0 {
		m.preview.hint = mdPreviewRefusalHint(srcMap)
		return nil
	}
	return m.mdPreviewStartAnnotationAt(srcMap.blocks()[bi].startLine)
}

// mdPreviewRefusalHint is the status-bar message for a refused preview
// annotation, split by which of mdPreviewHighlightAnchor's two reasons applies.
// An unaligned map is a property of the document and never resolves, so the
// hint names the way out (press P, annotate in source view); a map that aligned
// but resolved no block is positional and the reader can scroll out of it.
func mdPreviewRefusalHint(srcMap mdPreviewSourceMap) string {
	if !srcMap.aligned || len(srcMap.blocks()) == 0 {
		return "Preview cannot anchor this document — press P to annotate in source view"
	}
	return "No block in view to annotate"
}

// mdPreviewStartAnnotationAt is the shared core behind mdPreviewStartAnnotation
// (keyboard aim) and mdPreviewClickDiff (mouse aim): point the diff cursor at
// idx — an index into m.file.lines, the same coordinate
// mdPreviewBlockAnchor.startLine uses — and run the ordinary startAnnotation
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
func (m *Model) mdPreviewStartAnnotationAt(idx int) tea.Cmd {
	savedOffset := m.layout.viewport.YOffset
	m.nav.diffCursor = idx
	m.annot.cursorOnAnnotation = false
	cmd := m.startAnnotation()
	m.layout.viewport.SetYOffset(savedOffset)
	m.layout.viewport.SetContent(m.renderDiff())
	return cmd
}

// mdPreviewClickDiff handles a left-click press in the diff viewport while
// markdown preview is on. A preview click has no diff cursor to move — it
// maps the clicked row through the current source map (the same one
// mdPreviewBody used to paint the frame on screen, so the click and what the
// reader sees can never disagree) to the block that row belongs to, and
// starts annotating it: the mouse equivalent of `a` aiming at the highlighted
// block. anchorAtRow's own fallback resolves a row past the last block's
// rows to that last block, so a click below the content still lands
// somewhere sensible rather than doing nothing. A click when the map cannot
// resolve any block at all (unaligned, or an empty document) is a no-op.
//
// Two more no-ops, matching this feature's siblings:
//
//   - not markdownPreviewable — the same double gate panMarkdownPreview and
//     scrollMarkdownPreview carry (see their doc comments). With preview stuck
//     on for a file renderDiff will not preview, running the markdown pipeline
//     over a non-markdown diff would anchor an annotation off a map of a
//     document that is not on screen.
//   - an annotation input already open — mdPreviewStartAnnotationAt goes through
//     startAnnotation, whose clearPendingInputState plus a fresh
//     newAnnotationInput would discard whatever the reader had typed. Source
//     view's clickDiff keeps the text (it only moves the cursor), and losing
//     typed text to a stray click is worse than a click that does nothing.
func (m Model) mdPreviewClickDiff(y int) (tea.Model, tea.Cmd) {
	if m.file.name == "" || !m.file.markdownPreviewable || m.annot.annotating {
		return m, nil
	}
	row := (y - m.diffTopRow()) + m.layout.viewport.YOffset
	_, srcMap := m.mdPreviewBody()
	bi := srcMap.anchorAtRow(row)
	if bi < 0 {
		return m, nil
	}
	m.layout.focus = paneDiff
	cmd := m.mdPreviewStartAnnotationAt(srcMap.blocks()[bi].startLine)
	return m, cmd
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
