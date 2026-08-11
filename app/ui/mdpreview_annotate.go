package ui

import (
	"strings"

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
	all := m.store.Get(m.file.name)
	if len(all) == 0 {
		return rendered
	}

	anchors := srcMap.blocks()
	if !srcMap.Aligned || len(anchors) == 0 {
		return m.mdPreviewAnnotateDegraded(rendered, all)
	}

	annotationMap, fileComment := m.buildAnnotationMap()
	blockRows := make([][]string, len(anchors))
	for _, a := range all {
		if a.Line == 0 {
			continue // file-level: painted separately, always at the very top
		}
		bi := m.mdPreviewResolveBlock(srcMap, a)
		blockRows[bi] = append(blockRows[bi], m.mdPreviewRenderOne(a, annotationMap)...)
	}

	var top strings.Builder
	m.renderFileAnnotationHeader(&top, fileComment)

	rows := strings.Split(rendered, "\n")
	out := mdPreviewRowsFromBuilder(&top)
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
	return strings.Join(out, "\n")
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
