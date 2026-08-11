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
//
// Deliberately NOT wired into invalidateRenderCaches (app/ui/diffview.go),
// which is the chokepoint for the two diff-pane memos. Everything this cache
// and its nested mdPreviewScrollCache hold is fully self-keying, so there is
// nothing for an external invalidator to catch:
//   - the base render reads only m.file.lines, the viewport width and
//     noColors, all of which mdPreviewCacheKey carries (lines change only with
//     loadSeq, which is in the key). It does not read the style resolver at
//     all — glamour renders through the fixed mdPreviewStyle/mdPreviewStyleNoColor
//     configs — nor file.highlighted, file.blameData or file.intraRanges, which
//     are the four non-comparable inputs invalidateRenderCaches exists for.
//   - the scroll memos key on the painted body string, so an annotation edit or
//     a theme change reaching the annotation rows misses by itself; the cut
//     additionally keys on the resolved «/» indicators, which is where the
//     theme enters that pass.
//   - the block highlight is not memoized at all — it is recomputed per frame
//     from the live resolver.
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

// mdPreviewFrame is the one definition of what a preview frame looks like:
// an uncut body (annotations already spliced in) plus the map in that body's
// own row numbers, cut to the horizontal window, then given the block
// highlight. Both entry points that push preview content into the viewport go
// through it — renderMarkdownPreview via mdPreviewFinalRender, and
// panMarkdownPreview directly, because it has already computed the body to
// measure the pan clamp against and would otherwise pay for a second
// annotation paint per pan step.
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
func (m Model) mdPreviewFrame(body string, srcMap mdPreviewSourceMap) string {
	return m.mdPreviewHighlight(m.applyMdPreviewScroll(body), srcMap)
}

// mdPreviewFinalRender is the whole preview pipeline from Model state alone:
// cached base render -> annotations spliced in -> mdPreviewFrame.
func (m Model) mdPreviewFinalRender() string {
	return m.mdPreviewFrame(m.mdPreviewBody())
}

// flushPreviewWheelPending is the markdown-preview half of flushWheelPending
// (app/ui/mouse.go), which calls it first and returns when it reports true.
// pinDiffCursorTo is an unconditional no-op while previewing (there is no
// cursor to pin), so without this the deferred SetContent the wheel-burst
// debounce owes never runs and the scroll-following block highlight stays
// frozen on the block that was topmost when the burst started.
//
// It rides the SAME wheelState debounce (gen / renderPending / tickInFlight,
// see .claude/rules/gotchas.md) rather than bringing a second one: one repaint
// per burst, not per wheel event. Clearing both flags here is what the diff
// path's own tail does, and is why the caller returns instead of falling
// through.
func (m *Model) flushPreviewWheelPending() bool {
	if !m.modes.mdPreview {
		return false
	}
	m.layout.viewport.SetContent(m.renderDiff())
	m.wheel.renderPending = false
	m.wheel.tickInFlight = false
	return true
}

// mdPreviewHighlightAnchor picks the block the highlight marks: the topmost
// FULLY visible one — the first block whose rows all fall inside the viewport at
// the current YOffset. That is what makes the mark follow reading position
// rather than the viewport edge: a block half-scrolled off the top is not the
// one being read, the next whole one is.
//
// Blocks are contiguous and ascending (endRow of one is the row before the next
// one's row, enforced when the map is built), so the first block starting at or
// after the top is the only candidate — if it is too tall to fit, every later
// block starts further down and cannot fit either. When it does not fit, the
// fallback is the block owning the top row, which is the one filling the screen.
//
// Returns -1 when there is nothing to mark: an unaligned or empty map, or a
// viewport scrolled above the first block.
func (m Model) mdPreviewHighlightAnchor(srcMap mdPreviewSourceMap) int {
	anchors := srcMap.blocks()
	if !srcMap.aligned || len(anchors) == 0 {
		return -1
	}
	top := m.layout.viewport.YOffset
	bottom := top + m.layout.viewport.Height - 1

	i := sort.Search(len(anchors), func(i int) bool { return anchors[i].row >= top })
	if i < len(anchors) && anchors[i].endRow <= bottom {
		return i
	}
	return srcMap.anchorAtRow(top)
}

// mdPreviewHighlight paints the highlighted block's rows with a background.
// rendered is the already-cut preview frame and srcMap must be in that frame's
// coordinates.
//
// The background is ColorKeySearchBg, borrowed deliberately: there is no
// cursor-background color key to use instead, and adding one is the three-site
// change CLAUDE.md describes (theme.go's colorKeys, the options struct, and
// colorFieldPtrs() in themes.go) plus a new field in all 7 bundled themes. The
// diff pane's own cursor is NOT drawn this way — it uses a cursor bar glyph
// over the line's own change background — so this is a reuse of a theme color,
// not a reuse of a rendering convention.
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
	for r := max(0, anchor.row); r <= anchor.endRow && r < len(rows); r++ {
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
// The re-assertion itself belongs to style.SGR (ReassertBackground), which owns
// the set of SGR spellings that clear a background — a glamour row is full of
// per-span resets, and each one would end the highlight partway through the row,
// leaving it striped. Doing it here by string replacement would cover whichever
// spellings this file happened to list. The zero value is how that type is used
// (it is stateless by design); Model's injected sgrProcessor covers only the
// Reemit path.
func mdPreviewHighlightRow(row, bg string, width int) string {
	if row == "" {
		return row
	}
	if pad := width - ansi.StringWidth(row); pad > 0 {
		row += strings.Repeat(" ", pad)
	}
	return style.SGR{}.ReassertBackground(row, bg)
}
