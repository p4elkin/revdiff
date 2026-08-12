package ui

import (
	"sort"
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
// the block highlight and the click-to-block mapping — must
// use the map this returns, not the one it was handed.
//
// The accounting is exact rather than estimated: file-level rows are prepended
// ahead of every block, and every other run of rows is inserted at a splice row
// the map named (mdPreviewSourceMap.spliceRow). So a row moves by the file-level
// row count plus everything inserted strictly above it — the prefix sum
// mdPreviewRowShift computes, and the reason a block's row and its endRow are
// shifted separately: with an expanded block a splice point can sit INSIDE a
// block's span, so the two no longer move by the same amount.
//
// The map it returns also carries where each painted annotation landed
// (mdPreviewAnnotAnchor, mdpreview_stops.go), which is what lets the cursor stop
// on one and `d` delete it. Those spans are recorded AS THE ROWS ARE SPLICED,
// never by scanning the painted string afterwards: the splice is the only place
// that knows which rows belong to which annotation, and a scan looking for them
// again would be a second source of truth free to drift from it.
func (m Model) mdPreviewPaintAnnotationsTracked(rendered string, srcMap mdPreviewSourceMap) (string, mdPreviewSourceMap) {
	all := m.store.Get(m.file.name)
	_, liveOK := m.mdPreviewLiveInputTarget()
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
	groups := m.mdPreviewCollectAnnotationRows(all, srcMap, annotationMap)

	var top strings.Builder
	m.renderFileAnnotationHeader(&top, fileComment)
	topRows := mdPreviewRowsFromBuilder(&top)
	shift := newMdPreviewRowShift(len(topRows), groups)

	rows := strings.Split(rendered, "\n")
	out := topRows
	// the file-level annotation occupies the prepended rows, so it is the first
	// stop of the whole document. Gated on hasFileAnnotation, not on topRows
	// being non-empty: a file-level input still being typed paints rows too, and
	// there is nothing in the store for it to delete yet.
	var annots []mdPreviewAnnotAnchor
	if len(topRows) > 0 && m.hasFileAnnotation() && !liveFileAnnotating {
		annots = append(annots, mdPreviewAnnotAnchor{block: mdPreviewFileStopBlock, row: 0, endRow: len(topRows) - 1})
	}
	// a file-level input being typed occupies exactly the prepended rows, which
	// is why it is recorded here rather than by the splice walk below: it is
	// never spliced at a row of the render at all.
	var live mdPreviewRowSpan
	if len(topRows) > 0 && liveFileAnnotating {
		live = mdPreviewRowSpan{row: 0, endRow: len(topRows) - 1, ok: true}
	}
	// ords counts each block's annotations as they are painted. It is per block
	// rather than per splice point because a block's annotations can now be
	// spliced at several rows, and mdPreviewAnnotAnchor.ord is the position among
	// the BLOCK's annotations — the identity mdPreviewStopRef holds on to.
	ords := make(map[int]int, len(anchors))
	si := 0
	for i, line := range rows {
		out = append(out, line)
		for si < len(shift.rows) && shift.rows[si] <= i {
			g := groups[shift.rows[si]]
			annots, live = appendMdPreviewAnnotAnchors(annots, live, g.runs, len(out), ords)
			out = append(out, g.rows...)
			si++
		}
	}
	// defensive: a splice row past the last row of this render (should not happen
	// — every splice row is a row of this same string) still gets its annotations
	// painted rather than silently dropped.
	for ; si < len(shift.rows); si++ {
		g := groups[shift.rows[si]]
		annots, live = appendMdPreviewAnnotAnchors(annots, live, g.runs, len(out), ords)
		out = append(out, g.rows...)
	}
	return strings.Join(out, "\n"), mdPreviewSourceMap{
		aligned: true, anchors: shift.blockAnchors(anchors), lines: shift.lineAnchors(srcMap.lines),
		annots: annots, liveInput: live,
	}
}

// mdPreviewSpliceGroup is everything painted at one splice row: the rendered
// rows themselves, and the same rows described as one run per annotation so the
// splice can turn them into row spans (see appendMdPreviewAnnotAnchors). One
// struct rather than two parallel maps, because the two must stay in step — a
// run whose rows went missing would anchor a stop on a row nobody painted.
type mdPreviewSpliceGroup struct {
	rows []string
	runs []mdPreviewAnnotRun
}

// mdPreviewAddSplice records one painted annotation at splice row row. The map
// holds group VALUES, so the read-modify-write is what lets a run be appended to
// a group that does not exist yet.
func mdPreviewAddSplice(groups map[int]mdPreviewSpliceGroup, row int, rows []string, run mdPreviewAnnotRun) {
	g := groups[row]
	g.rows = append(g.rows, rows...)
	g.runs = append(g.runs, run)
	groups[row] = g
}

// mdPreviewRowShift translates a row of the render the painter was handed into
// its row in the painted frame: shift(r) = len(topRows) + Σ inserted[s] for
// every splice row s < r.
//
// The comparison is strict because a group is spliced AFTER the row it is keyed
// on, so that row itself never moves. The prefix sum is what a per-line splice
// point forces: with every run inserted at its block's endRow, a block's row and
// endRow moved by the same delta and one running total was enough. Once a splice
// can sit INSIDE a block's span — a comment under raw line 4 of an expanded
// block — the block's own row is above it and its endRow below, so each has to
// be shifted on its own.
type mdPreviewRowShift struct {
	top   int
	rows  []int // splice rows, ascending
	cumul []int // cumul[i] = rows painted at every splice row before rows[i]; one entry longer than rows
}

// newMdPreviewRowShift builds the prefix sum over groups' splice rows.
func newMdPreviewRowShift(top int, groups map[int]mdPreviewSpliceGroup) mdPreviewRowShift {
	rows := make([]int, 0, len(groups))
	for r := range groups {
		rows = append(rows, r)
	}
	sort.Ints(rows)
	cumul := make([]int, len(rows)+1)
	for i, r := range rows {
		cumul[i+1] = cumul[i] + len(groups[r].rows)
	}
	return mdPreviewRowShift{top: top, rows: rows, cumul: cumul}
}

// at returns row's index in the painted frame.
func (s mdPreviewRowShift) at(row int) int {
	return row + s.top + s.cumul[sort.SearchInts(s.rows, row)]
}

// blockAnchors re-expresses every block anchor in the painted frame's rows. The
// result is a copy, never an in-place edit: the caller's backing array belongs to
// the render cache (mdpreview_cache.go) and is handed to every later repaint.
func (s mdPreviewRowShift) blockAnchors(anchors []mdPreviewBlockAnchor) []mdPreviewBlockAnchor {
	out := make([]mdPreviewBlockAnchor, len(anchors))
	for i, a := range anchors {
		out[i] = a
		out[i].row = s.at(a.row)
		out[i].endRow = s.at(a.endRow)
	}
	return out
}

// lineAnchors re-expresses an expanded block's raw-line anchors in the painted
// frame's rows, so a line stop still names the row its text is on after comments
// have been spliced between the raw lines. nil in, nil out — the overwhelmingly
// common case is no block expanded at all.
func (s mdPreviewRowShift) lineAnchors(lines []mdPreviewLineAnchor) []mdPreviewLineAnchor {
	if len(lines) == 0 {
		return nil
	}
	out := make([]mdPreviewLineAnchor, len(lines))
	for i, la := range lines {
		out[i] = la
		out[i].row = s.at(la.row)
	}
	return out
}

// mdPreviewCollectAnnotationRows renders this file's line-level annotations and
// buckets them by the RENDERED ROW each one is spliced under, keyed by that row:
// a group's rows are the rows to insert there, its runs the same rows described
// one per annotation so the splice can turn them into row spans (see
// appendMdPreviewAnnotAnchors).
//
// Keyed by row rather than by block because of expansion: inside an expanded
// block a comment belongs under the raw source line it was written against, not
// at the bottom of the block. mdPreviewSourceMap.spliceRow is what decides which,
// and it answers with the owning block too, so a run carries the block it belongs
// to instead of the caller deriving it from a slice position.
//
// Split out of mdPreviewPaintAnnotationsTracked so that function stays under the
// cyclomatic ceiling; the two halves are "what to paint" and "where it lands".
func (m Model) mdPreviewCollectAnnotationRows(all []annotation.Annotation, srcMap mdPreviewSourceMap,
	annotationMap map[annotLineKey]string) map[int]mdPreviewSpliceGroup {
	groups := make(map[int]mdPreviewSpliceGroup, len(all))

	liveIdx, liveOK := m.mdPreviewLiveInputTarget()
	liveCovered := false
	for _, a := range all {
		if a.Line == 0 {
			continue // file-level: painted separately, always at the very top
		}
		// resolved once and threaded down: mdPreviewLineIndex is a linear scan
		// of m.file.lines, and it used to run three times per annotation per
		// repaint (inside resolve, inside render, and for the liveIdx compare).
		idx, idxOK := m.mdPreviewLineIndex(a.Line, a.Type)
		row, bi := srcMap.spliceRow(idx, idxOK)
		painted := m.mdPreviewRenderOne(a, annotationMap, idx, idxOK)
		// live: this stored annotation's rows ARE the input box while the reader
		// edits it (renderAnnotationOrInput draws the input in place of the
		// comment for the annotated line), so the rows to keep on screen are the
		// same rows — recorded here rather than by a second branch below.
		editing := liveOK && idxOK && idx == liveIdx
		mdPreviewAddSplice(groups, row, painted,
			mdPreviewAnnotRun{rows: len(painted), block: bi, line: a.Line, changeType: a.Type, stored: true, live: editing})
		if editing {
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
	// typing would stay invisible until the moment it is saved. It goes through
	// the same spliceRow the stored ones do, which is both what puts the live box
	// under the raw line being typed against and what replaces the two separate
	// fallbacks this branch used to carry (anchorAtLine, then max(..., 0)) with
	// resolveBlock's own three.
	if liveOK && !liveCovered {
		row, bi := srcMap.spliceRow(liveIdx, true)
		var b strings.Builder
		m.renderAnnotationOrInput(&b, liveIdx, annotationMap)
		rows := mdPreviewRowsFromBuilder(&b)
		// stored=false: the store holds nothing for this line yet, so the row is
		// a live input rather than a deletable annotation. It still counts toward
		// the row offsets of the annotations painted after it, which is why it is
		// recorded at all instead of being left out of the run list.
		mdPreviewAddSplice(groups, row, rows, mdPreviewAnnotRun{rows: len(rows), block: bi, live: true})
	}
	return groups
}

// mdPreviewAnnotRun is one painted annotation's row count plus the block it
// belongs to and the store key that identifies it, collected while the rows are
// rendered and turned into row spans once the splice point is known. stored is
// false for a live input row — it takes up rows like any other, so it must be
// counted, but there is nothing in the store behind it to select or delete.
type mdPreviewAnnotRun struct {
	rows       int
	block      int
	line       int
	changeType string
	stored     bool
	live       bool // these rows are the annotation input the reader is typing into
}

// appendMdPreviewAnnotAnchors turns one splice point's runs into row spans, given
// the painted row those rows start at. Runs are laid out back to back in the
// order they were rendered, so the spans follow from the row counts alone. A run
// of zero rows contributes no anchor — there is nothing on screen to put a cursor
// on.
//
// ords carries each block's running annotation count ACROSS splice points, and
// the caller shares one map over the whole walk: a block's annotations can now
// land at several rows, while mdPreviewAnnotAnchor.ord stays "position among this
// block's annotations, in paint order" — the identity mdPreviewStopRef holds. The
// walk visits splice rows in ascending order, so paint order is row order.
//
// live is threaded through rather than returned on its own because the live
// input's span is found by the same arithmetic and at most one run in the whole
// walk carries it: a run marked live overwrites it, every other run passes the
// caller's value back unchanged. That is what lets the visibility step scroll to
// the input without re-scanning the painted string for it.
func appendMdPreviewAnnotAnchors(dst []mdPreviewAnnotAnchor, live mdPreviewRowSpan, runs []mdPreviewAnnotRun,
	start int, ords map[int]int) ([]mdPreviewAnnotAnchor, mdPreviewRowSpan) {
	row := start
	for _, r := range runs {
		if r.stored && r.rows > 0 {
			dst = append(dst, mdPreviewAnnotAnchor{
				block: r.block, ord: ords[r.block], row: row, endRow: row + r.rows - 1,
				line: r.line, changeType: r.changeType,
			})
			ords[r.block]++
		}
		if r.live && r.rows > 0 {
			live = mdPreviewRowSpan{row: row, endRow: row + r.rows - 1, ok: true}
		}
		row += r.rows
	}
	return dst, live
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

// mdPreviewStartAnnotation is `a`/enter in preview: open an annotation input on
// whatever the cursor is stopped on.
//
// Three cases, and all of them bottom out in the ordinary startAnnotation path:
//   - on a RAW SOURCE LINE of an expanded block, it targets that exact line.
//     This is the whole point of raw expansion: the ref already carries the
//     line's index into m.file.lines (see mdPreviewStopRef), so there is nothing
//     to resolve and no new save code — the comment it produces is
//     byte-identical in the output to one made on the same line in source view.
//   - on an ANNOTATION stop, it targets that annotation's own (Line, Type), so
//     startAnnotation's existing pre-fill loads its current text and saving
//     replaces it. That is how editing already works in the diff pane —
//     Store.Add replaces on a (File, Line, Type) collision — so this is a reuse
//     of the edit path, not a new mechanism. The multi-line case comes with it
//     unchanged: a comment containing newlines is stashed in
//     annot.existingMultiline rather than loaded into the textinput (whose
//     sanitizer would flatten it), so the editor key can seed $EDITOR from it and
//     Enter on an empty input preserves it instead of blanking it.
//   - on a BLOCK stop, it targets the block's own start line, which by the same
//     mechanism edits a comment already sitting exactly there, or creates a new
//     one.
//
// It reads the cursor through the same functions that decide what the on-screen
// highlight marks — mdPreviewCursorStop for the stop, mdPreviewHighlightAnchor
// for the block — rather than computing a second, parallel notion of "the
// current thing": keyboard aim and the visible highlight can never disagree.
//
// With no cursor placed yet — the state every preview session starts in, since
// nothing is highlighted until the reader moves — `a` SEEDS the cursor at the
// block nearest the viewport center, exactly where the first down/up press would
// have put it, and annotates that. Without the seed, `a` would be a dead key
// until the reader happened to press j or k first, and pressing `a` straight
// after `P` is the shortest path this feature has.
//
// Returns nil when there is nothing to anchor to at all (an unaligned map or an
// empty document) — annotation creation is refused, the same as pressing `a` on
// a diff divider in source view.
//
// A refusal sets a transient status-bar hint rather than being silent. A reader
// who presses `a` on a document whose source map did not align (README.md is
// one, by design — see mdPreviewBuildSourceMap) otherwise gets a byte-identical
// frame back, with no way to tell "this document cannot be anchored" from "the
// key is not bound". The hint is the same mechanism outputState and
// compactState use for their own refusals, and clears on the next key press.
//
// A file renderDiff will not preview is refused outright, the same double gate
// every other preview entry point carries (mdPreviewClickDiff below,
// moveMdPreviewCursor, mdPreviewToggleRaw, mdPreviewDeleteAnnotation,
// panMarkdownPreview). The gate cannot be hoisted into handleMdPreviewAction:
// several actions it dispatches must still work with preview stuck on for a
// non-previewable file — esc has to keep falling through to handleEscKey, the
// scroll keys still move the viewport, and everything reaching the default
// branch (P itself included) still has to fall through, or the reader would be
// stuck in preview with no key that leaves it.
func (m *Model) mdPreviewStartAnnotation() tea.Cmd {
	if !m.file.markdownPreviewable {
		return nil // preview stuck on for a file renderDiff will not preview; see panMarkdownPreview
	}
	_, srcMap := m.mdPreviewBody()
	if stop, ok := m.mdPreviewCursorStop(srcMap); ok {
		if stop.ref.onLine {
			return m.mdPreviewStartAnnotationAt(stop.ref.line)
		}
		if stop.ref.onAnnot {
			return m.mdPreviewEditAnnotationStop(stop, srcMap)
		}
	}
	bi := m.mdPreviewHighlightAnchor(srcMap)
	if bi < 0 {
		bi = m.mdPreviewCenterBlock(srcMap)
		if bi < 0 {
			m.preview.hint = mdPreviewUnanchorableHint
			return nil
		}
		m.setMdPreviewCursorToBlock(bi)
	}
	return m.mdPreviewStartAnnotationAt(srcMap.blocks()[bi].startLine)
}

// mdPreviewUnanchorableHint is the status-bar message for a refused preview
// annotation. There is only one reason left for a refusal: the document's source
// map did not align, or it holds no block at all. That is a property of the
// document and never resolves by scrolling, so the hint names the way out
// instead.
//
// The old second reason — aligned, but the viewport sat where no block could be
// marked — is gone with the scroll-derived highlight. `a` now seeds the block
// cursor at the viewport center when none is set (mdPreviewStartAnnotation), and
// an aligned map with at least one block always has a nearest block to seed on.
const mdPreviewUnanchorableHint = "Preview cannot anchor this document — press P to annotate in source view"

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
// on the confirm case) — no diff-line-coordinate scroll survives the call, so
// there is nothing for a stale TOC-jump-shaped side effect to have gotten
// wrong.
//
// The restore is followed by ensureMdPreviewInputVisible, which does the same
// job the discarded scroll was meant to do, in preview row coordinates. So the
// viewport may still move — by the smallest amount that brings the input on
// screen, and only when it was off screen. What it may never do is move because
// of a number computed in diff-line coordinates.
//
// The content refresh after restoring the offset is what makes the freshly
// started input actually visible: mdPreviewPaintAnnotationsTracked only draws
// a live input row for the exact line this Model is currently annotating, so
// without a fresh SetContent here the viewport would keep showing the
// pre-annotation frame until the next keystroke's own re-render.
// mdPreviewEditAnnotationStop opens the input on the annotation the cursor is
// stopped on, so the reader edits THAT comment rather than adding a second one
// beside it. It aims at the annotation's own (Line, Type) — resolved back to a
// diff-line index the same way the painter resolved it to paint the rows — which
// is what makes startAnnotation's pre-fill find it and Store.Add replace it.
//
// The file-level annotation takes the file-level input instead, because Line 0
// has no diff line to point the cursor at. That is the same call `A` makes, and
// it carries the same pre-fill, so editing works there too.
//
// An ORPHANED annotation — one whose line the file no longer has, painted under
// the last block by resolveBlock — has no index to aim at. It falls back to the
// owning block's start line, which creates or edits a comment there and leaves
// the orphan untouched. Aiming at a line that does not exist would just cancel
// the save.
func (m *Model) mdPreviewEditAnnotationStop(stop mdPreviewStop, srcMap mdPreviewSourceMap) tea.Cmd {
	if stop.ref.block == mdPreviewFileStopBlock {
		cmd := m.startFileAnnotation()
		m.layout.viewport.SetContent(m.renderDiff())
		return cmd
	}
	idx, ok := m.mdPreviewLineIndex(stop.line, stop.changeType)
	if !ok {
		anchors := srcMap.blocks()
		if stop.ref.block < 0 || stop.ref.block >= len(anchors) {
			return nil
		}
		idx = anchors[stop.ref.block].startLine
	}
	return m.mdPreviewStartAnnotationAt(idx)
}

func (m *Model) mdPreviewStartAnnotationAt(idx int) tea.Cmd {
	savedOffset := m.layout.viewport.YOffset
	m.nav.diffCursor = idx
	m.annot.cursorOnAnnotation = false
	cmd := m.startAnnotation()
	m.layout.viewport.SetYOffset(savedOffset)
	m.layout.viewport.SetContent(m.renderDiff())
	m.ensureMdPreviewInputVisible()
	return cmd
}

// ensureMdPreviewInputVisible scrolls the preview the least it can so the
// annotation input the reader is about to type into is on screen.
//
// It replaces what the save/restore in mdPreviewStartAnnotationAt takes away,
// rather than putting it back. Restoring the offset is what stops
// ensureLineAnnotationInputVisible (app/ui/annotate.go) repositioning the preview
// off diff-line coordinates that mean nothing against a glamour render — that is
// still exactly what it is for. But the consequence was that NOTHING then
// scrolled to reveal the input: mid-document nobody noticed, because the input is
// spliced directly under the block the reader is already looking at, and at the
// bottom edge of the viewport — the last block above all — it landed below the
// visible window and the reader typed blind.
//
// So this is the same job done again in PREVIEW row coordinates. The row span
// comes from the painter that put the input there (mdPreviewSourceMap.liveInput),
// never from re-scanning the painted string, for the reason every other anchor in
// this file is recorded at splice time: a scan would be a second source of truth
// about where a row is, free to drift from the one that placed it.
//
// Order matters at the call sites: the frame carrying the input must already be
// in the viewport before this runs, because SetYOffset clamps against the
// viewport's own content buffer and would otherwise clamp the input's row away.
//
// A no-op whenever there is no input painted in this frame — startAnnotation
// refused (a divider, a collapsed-hidden line), or the map could anchor nothing
// and the painter took its degraded path.
func (m *Model) ensureMdPreviewInputVisible() {
	if !m.modes.mdPreview || !m.file.markdownPreviewable {
		return // not showing a preview frame; preview row numbers would mean nothing
	}
	_, srcMap := m.mdPreviewBody()
	if !srcMap.liveInput.ok {
		return
	}
	m.syncMdPreviewViewportToRows(srcMap.liveInput.row, srcMap.liveInput.endRow)
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
// A row painted from a RAW SOURCE LINE of an expanded block is resolved to that
// line first, so a click inside an expanded block annotates the line it hit
// rather than the block around it — the mouse equivalent of `a` on a raw-line
// stop, and the same precision the expansion exists for. A click on one of the
// block's other rows (a blank source line, which paints a row but gets no
// anchor, or a comment spliced between the raw lines) falls through to the block
// below, which collapses the expansion exactly as a click always has.
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
	if la, ok := srcMap.lineAt(row); ok {
		m.layout.focus = paneDiff
		m.setMdPreviewLineCursor(la.block, la.lineIdx)
		cmd := m.mdPreviewStartAnnotationAt(la.lineIdx)
		return m, cmd
	}
	bi := srcMap.anchorAtRow(row)
	if bi < 0 {
		return m, nil
	}
	m.layout.focus = paneDiff
	// a click is aim, exactly like j/k: it leaves the block cursor on the block
	// it annotated, so the highlight marks what the click hit and a following
	// j/k continues from there instead of re-seeding at the viewport center.
	m.setMdPreviewCursorToBlock(bi)
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
