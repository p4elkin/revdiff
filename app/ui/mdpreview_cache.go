package ui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/umputun/revdiff/app/ui/style"
)

// mdPreviewCacheKey is the comparable fingerprint of every input the base
// preview render depends on. loadSeq — not just fileName — is what makes the
// key safe across an R reload of the same file at the same width: a reload
// bumps m.file.loadSeq (see triggerReload, app/ui/loaders.go) even though the
// file name and width are unchanged, so a render cached before the reload can
// never satisfy a lookup after it. globalRenderKey (app/ui/diffview.go)
// already relies on the same technique for the diff-line render cache, and
// this is the precedent renderMarkdownPreview's old doc comment pointed to
// when it declined a cache for exactly this staleness risk.
type mdPreviewCacheKey struct {
	fileName string
	loadSeq  uint64
	width    int
	noColors bool
}

// mdPreviewRenderCache memoizes the base preview render — the whole-document
// glamour+mermaid render plus its source map (mdPreviewRenderWithMap) — for
// exactly one cache key at a time. It exists because the scroll-following
// block highlight (a later task in this plan) has to repaint on every offset
// change, the way the diff-pane wheel-burst coalescing already repaints
// without re-rendering the diff itself (see wheelState in
// .claude/rules/gotchas.md). Without this cache, that repaint would pay for a
// full glamour+mermaid pass per scroll step instead of per file/width/color
// change.
//
// Held behind a pointer, the same reason renderCache (diffRenderCache,
// app/ui/diffview.go) is: renderMarkdownPreview has a value receiver, so a
// plain field would memoize into a Model copy that is discarded when the
// method returns. Every Model copy sharing one instance is safe because the
// single entry is keyed by the exact state it was rendered under — an entry
// written by a copy that was later discarded is still correct for any copy
// whose key matches. NewModel initializes this; direct Model{} construction
// is unsupported.
//
// One entry, not a map: the preview shows one file at a time, so there is
// never more than one base render worth keeping warm, and a single entry
// means a key mismatch (any field changing) is a plain evict-and-replace
// with no eviction policy to get wrong.
type mdPreviewRenderCache struct {
	key      mdPreviewCacheKey
	valid    bool
	rendered string
	srcMap   mdPreviewSourceMap

	scroll mdPreviewScrollCache
}

// mdPreviewScrollCache memoizes the two whole-document passes
// applyMdPreviewScroll owes on every repaint: the widest-row grapheme scan the
// pan clamp is measured against, and the per-row horizontal cut itself.
//
// It is keyed on the body string rather than on mdPreviewCacheKey, because the
// body is the base render PLUS this file's annotation rows — it changes on
// every annotation edit, which the base render's own key cannot see. Neither
// pass depends on the vertical offset, so the repaint every j/k keypress now
// triggers (the block highlight has to move with the viewport) reuses both.
//
// The measurement that made this necessary, taken on
// docs/plans/completed/20260722-markdown-preview-mode.md (983 rendered rows,
// Apple M2 Max): the width scan alone is ~1.9-2.1ms and ran unconditionally,
// even at scrollX 0 with nothing to cut; the cut is another ~2.7ms whenever
// the document is wider than the pane. An earlier version of the repaint
// benchmark timed the cache lookup instead of the frame, which is how both
// came to sit unnoticed in every keypress.
//
// Comparing the key is not the cost the memo removes: with no annotations
// painted, mdPreviewPaintAnnotationsTracked returns the base render untouched,
// so the strings share a backing pointer and the compare is O(1); with
// annotations it is a memcmp, orders of magnitude cheaper per byte than a
// grapheme scan.
type mdPreviewScrollCache struct {
	body    string
	bodySet bool

	widest     int
	haveWidest bool

	offset     int
	cutWidth   int
	indicators string // the resolved «/» strings: they carry the theme the cut baked in
	cut        string
	haveCut    bool
}

// forBody drops both memos when the body changes. Everything this cache holds
// is derived from that one string, so there is nothing to keep across it.
func (c *mdPreviewScrollCache) forBody(body string) {
	if c.bodySet && c.body == body {
		return
	}
	*c = mdPreviewScrollCache{body: body, bodySet: true}
}

// widestOf returns the widest row's display width in body.
func (c *mdPreviewScrollCache) widestOf(body string) int {
	c.forBody(body)
	if !c.haveWidest {
		c.widest, c.haveWidest = mdPreviewMaxLineWidth(body), true
	}
	return c.widest
}

// cutOf returns body cut to the visible column window, calling compute on a
// miss. indicators is part of the key, not decoration: the cut bakes the «/»
// glyphs in with their resolved colors, so a theme change has to miss even
// though the body, offset and width are unchanged.
func (c *mdPreviewScrollCache) cutOf(body string, offset, cutWidth int, indicators string, compute func() string) string {
	c.forBody(body)
	if c.haveCut && c.offset == offset && c.cutWidth == cutWidth && c.indicators == indicators {
		return c.cut
	}
	c.offset, c.cutWidth, c.indicators = offset, cutWidth, indicators
	c.cut, c.haveCut = compute(), true
	return c.cut
}

// get returns the cached render and source map when key matches the stored
// entry. ok is false on any miss, including a cache that has never been
// filled.
func (c *mdPreviewRenderCache) get(key mdPreviewCacheKey) (rendered string, srcMap mdPreviewSourceMap, ok bool) {
	if !c.valid || c.key != key {
		return "", mdPreviewSourceMap{}, false
	}
	return c.rendered, c.srcMap, true
}

// put replaces the cache's single entry. A new key always evicts whatever was
// there before: the cache tracks the current file/width/color state, not a
// history of them.
func (c *mdPreviewRenderCache) put(key mdPreviewCacheKey, rendered string, srcMap mdPreviewSourceMap) {
	c.key = key
	c.rendered = rendered
	c.srcMap = srcMap
	c.valid = true
}

// mdPreviewCacheKey computes the current cache key from Model state. See the
// type's doc for why loadSeq, not just fileName, is part of it.
func (m Model) mdPreviewCacheKey() mdPreviewCacheKey {
	return mdPreviewCacheKey{
		fileName: m.file.name,
		loadSeq:  m.file.loadSeq,
		width:    m.layout.viewport.Width,
		noColors: m.cfg.noColors,
	}
}

// mdPreviewBaseRender returns the base preview render (no horizontal-scroll
// cut applied — see applyMdPreviewScroll) and its source map, computing and
// caching on a miss. renderMarkdownPreview and panMarkdownPreview both go
// through this rather than calling mdPreviewRenderWithMap directly, so every
// repaint at an unchanged file/width/color state reuses the same render
// instead of paying for a fresh glamour+mermaid pass.
func (m Model) mdPreviewBaseRender() (string, mdPreviewSourceMap) {
	key := m.mdPreviewCacheKey()
	if rendered, srcMap, ok := m.mdPreviewCache.get(key); ok {
		return rendered, srcMap
	}
	rendered, srcMap := mdPreviewRenderWithMap(m.file.lines, m.layout.viewport.Width, m.cfg.noColors)
	m.mdPreviewCache.put(key, rendered, srcMap)
	return rendered, srcMap
}

// mdPreviewBody is the full-width, uncut preview body: the cached base render
// with this file's annotations painted into it, plus the source map re-expressed
// in the painted render's own row numbers (see
// mdPreviewPaintAnnotationsTracked).
//
// This is the render the pan clamp must measure — mdPreviewMaxOffset needs the
// widest row of the string that will actually be cut — and the one
// applyMdPreviewScroll cuts. The block highlight is deliberately NOT part of it;
// see mdPreviewFinalRender for why it lands after the cut.
func (m Model) mdPreviewBody() (string, mdPreviewSourceMap) {
	rendered, srcMap := m.mdPreviewBaseRender()
	return m.mdPreviewPaintAnnotationsTracked(rendered, srcMap)
}

// mdPreviewFinalRender is the whole preview pipeline in one place: cached base
// render -> annotations spliced in -> horizontal cut -> block highlight. Both
// entry points that push preview content into the viewport
// (renderMarkdownPreview and panMarkdownPreview, app/ui/mdpreview.go) bottom out
// here, so there is exactly one definition of what a preview frame looks like.
//
// The highlight runs AFTER applyMdPreviewScroll, unlike the annotations, and the
// order is load-bearing in both directions. Annotation rows are ordinary content
// — whole new rows — so they have to exist before the cut or they would never
// pan or truncate like the rows around them. The highlight adds no rows at all:
// it re-styles rows that are already there, and its closing "\033[49m" sits at
// the end of a row that ansi.Cut would happily slice off, leaving the background
// to bleed through the » indicator and into the pane padding. Applying it to the
// already-cut rows makes that impossible. Row indices are unaffected by the cut
// (it is per-row), so the map stays valid across it.
func (m Model) mdPreviewFinalRender() string {
	body, srcMap := m.mdPreviewBody()
	return m.mdPreviewHighlight(m.applyMdPreviewScroll(body), srcMap)
}

// mdPreviewHighlightAnchor picks the block the highlight marks: the topmost
// FULLY visible one — the first block whose rows all fall inside the viewport at
// the current YOffset. That is what makes the mark follow reading position
// rather than the viewport edge: a block half-scrolled off the top is not the
// one being read, the next whole one is.
//
// Blocks are contiguous and ascending (EndRow of one is the row before the next
// one's Row, enforced when the map is built), so the first block starting at or
// after the top is the only candidate — if it is too tall to fit, every later
// block starts further down and cannot fit either. When it does not fit, the
// fallback is the block owning the top row, which is the one filling the screen.
//
// Returns -1 when there is nothing to mark: an unaligned or empty map, or a
// viewport scrolled above the first block.
func (m Model) mdPreviewHighlightAnchor(srcMap mdPreviewSourceMap) int {
	anchors := srcMap.blocks()
	if !srcMap.Aligned || len(anchors) == 0 {
		return -1
	}
	top := m.layout.viewport.YOffset
	bottom := top + m.layout.viewport.Height - 1

	i := sort.Search(len(anchors), func(i int) bool { return anchors[i].Row >= top })
	if i < len(anchors) && anchors[i].EndRow <= bottom {
		return i
	}
	return srcMap.anchorAtRow(top)
}

// mdPreviewHighlight paints the highlighted block's rows with a background,
// the way the diff pane marks the line the cursor is on. rendered is the
// already-cut preview frame and srcMap must be in that frame's coordinates.
//
// Skipped entirely in no-colors mode. The highlight is ANSI by construction and
// --no-colors promises a preview with none in it (see mdPreviewStyleNoColor);
// that mode does not align a map in practice either, so this costs nothing real.
//
// Blank rows inside the block's span are left alone. A block's span runs to the
// row before the next block starts, which includes the padding glamour puts
// between blocks — and for the LAST block it runs to the end of the document.
// Painting those would drag a solid bar across every empty row below the block
// instead of marking the block.
func (m Model) mdPreviewHighlight(rendered string, srcMap mdPreviewSourceMap) string {
	if m.cfg.noColors {
		return rendered
	}
	bg := string(m.resolver.Color(style.ColorKeySearchBg))
	if bg == "" {
		return rendered
	}
	bi := m.mdPreviewHighlightAnchor(srcMap)
	if bi < 0 {
		return rendered
	}

	// pad the bar out to the pane only on a panned frame. There, cutMdPreviewLine
	// has cut each row at wherever its own content ended, so a row that does not
	// continue to the right is shorter than the pane and the bar would stop short
	// of the edge — ragged, in exactly the mode (a wide diagram or table) panning
	// exists for. Unpanned, glamour has already padded prose rows to its own wrap
	// width and padding further would widen every highlighted row past the shape
	// the rest of the document has.
	padTo := 0
	if m.layout.scrollX > 0 {
		padTo = m.mdPreviewCutWidth()
	}
	anchor := srcMap.blocks()[bi]
	rows := strings.Split(rendered, "\n")
	painted := false
	for r := max(0, anchor.Row); r <= anchor.EndRow && r < len(rows); r++ {
		if strings.TrimSpace(ansi.Strip(rows[r])) == "" {
			continue
		}
		rows[r] = mdPreviewHighlightRow(rows[r], bg, padTo)
		painted = true
	}
	if !painted {
		return rendered
	}
	return strings.Join(rows, "\n")
}

// mdPreviewHighlightRow gives one rendered row the background bg, re-asserting
// it after every reset the row already contains, and first pads it out to
// width — 0 for no padding, see the caller for when each applies.
//
// The padding is what extendLineBg does for the diff cursor line, and it is
// needed for the same reason: a row shorter than the pane leaves the terminal's
// default background showing to its right, so the mark stops short of the edge
// instead of spanning it.
//
// Raw ANSI, never lipgloss.Render: this is a styled substring inside a
// lipgloss-rendered pane, and Render would emit a full "\033[0m" that kills the
// pane's own background (see .claude/rules/gotchas.md, "ANSI nesting with
// lipgloss"). The re-assertion is what a plain prefix cannot do — a glamour row
// is full of per-span resets, and each one would end the highlight partway
// through the row, leaving it striped.
func mdPreviewHighlightRow(row, bg string, width int) string {
	if row == "" {
		return row
	}
	if pad := width - ansi.StringWidth(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	out := strings.ReplaceAll(row, "\033[0m", "\033[0m"+bg)
	out = strings.ReplaceAll(out, "\033[m", "\033[m"+bg)
	out = strings.ReplaceAll(out, "\033[49m", "\033[49m"+bg)
	return bg + out + "\033[49m"
}
