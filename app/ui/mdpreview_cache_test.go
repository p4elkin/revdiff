package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/ui/style"
)

func TestMdPreviewRenderCache_MissWhenEmpty(t *testing.T) {
	var c mdPreviewRenderCache
	key := mdPreviewCacheKey{fileName: "a.md", loadSeq: 1, width: 80, noColors: false}

	_, _, ok := c.get(key)
	assert.False(t, ok, "a never-filled cache must report a miss")
}

func TestMdPreviewRenderCache_HitAfterPutWithMatchingKey(t *testing.T) {
	var c mdPreviewRenderCache
	key := mdPreviewCacheKey{fileName: "a.md", loadSeq: 1, width: 80, noColors: false}
	wantMap := mdPreviewSourceMap{aligned: true}

	c.put(key, "rendered body", wantMap)

	rendered, srcMap, ok := c.get(key)
	require.True(t, ok, "the exact key just stored must hit")
	assert.Equal(t, "rendered body", rendered)
	assert.Equal(t, wantMap, srcMap)
}

func TestMdPreviewRenderCache_MissOnAnyKeyFieldMismatch(t *testing.T) {
	base := mdPreviewCacheKey{fileName: "a.md", loadSeq: 1, width: 80, noColors: false}
	variants := []struct {
		name string
		key  mdPreviewCacheKey
	}{
		{"fileName", mdPreviewCacheKey{fileName: "b.md", loadSeq: 1, width: 80, noColors: false}},
		{"loadSeq", mdPreviewCacheKey{fileName: "a.md", loadSeq: 2, width: 80, noColors: false}},
		{"width", mdPreviewCacheKey{fileName: "a.md", loadSeq: 1, width: 100, noColors: false}},
		{"noColors", mdPreviewCacheKey{fileName: "a.md", loadSeq: 1, width: 80, noColors: true}},
	}
	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			var c mdPreviewRenderCache
			c.put(base, "rendered body", mdPreviewSourceMap{})

			_, _, ok := c.get(tc.key)
			assert.False(t, ok, "a mismatch on %s alone must miss the cache", tc.name)
		})
	}
}

func TestMdPreviewRenderCache_PutReplacesPriorEntry(t *testing.T) {
	var c mdPreviewRenderCache
	key1 := mdPreviewCacheKey{fileName: "a.md", loadSeq: 1, width: 80, noColors: false}
	key2 := mdPreviewCacheKey{fileName: "b.md", loadSeq: 1, width: 80, noColors: false}

	c.put(key1, "first", mdPreviewSourceMap{})
	c.put(key2, "second", mdPreviewSourceMap{})

	_, _, ok := c.get(key1)
	assert.False(t, ok, "a single-entry cache must evict the prior key on put")
	rendered, _, ok := c.get(key2)
	require.True(t, ok)
	assert.Equal(t, "second", rendered)
}

func TestMdPreviewCacheKey_CapturesFileWidthAndColorState(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text"))
	m.file.name = "notes.md"
	m.file.loadSeq = 3
	m.layout.viewport.Width = 90
	m.cfg.noColors = true

	got := m.mdPreviewCacheKey()

	assert.Equal(t, mdPreviewCacheKey{fileName: "notes.md", loadSeq: 3, width: 90, noColors: true}, got)
}

// TestMdPreviewBaseRender_ReloadAtSameWidthMissesCache proves staleness is
// impossible: an R reload bumps m.file.loadSeq even though the file name and
// viewport width are unchanged, so a render cached before the reload must not
// answer a lookup made after it (task 4's staleness checkbox).
func TestMdPreviewBaseRender_ReloadAtSameWidthMissesCache(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text"))

	preReloadKey := m.mdPreviewCacheKey()
	m.mdPreviewCache.put(preReloadKey, "STALE-PRE-RELOAD", mdPreviewSourceMap{aligned: true})

	rendered, _ := m.mdPreviewBaseRender()
	require.Equal(t, "STALE-PRE-RELOAD", rendered, "sanity: the same key must hit before any reload")

	m.triggerReload() // bumps m.file.loadSeq; width and file name are untouched

	rendered, _ = m.mdPreviewBaseRender()
	assert.NotEqual(t, "STALE-PRE-RELOAD", rendered,
		"a reload at the same width must miss the pre-reload cache entry")
}

// TestMdPreviewBaseRender_WidthChangeMissesRepeatAtSameWidthHits covers both
// halves of task 4's second cache test in one flow: a repeat call at an
// unchanged width hits, and a width change misses.
func TestMdPreviewBaseRender_WidthChangeMissesRepeatAtSameWidthHits(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text"))

	key80 := m.mdPreviewCacheKey()
	m.mdPreviewCache.put(key80, "SENTINEL-WIDTH-80", mdPreviewSourceMap{aligned: true})

	first, _ := m.mdPreviewBaseRender()
	require.Equal(t, "SENTINEL-WIDTH-80", first, "sanity: the width-80 entry must hit at width 80")

	second, _ := m.mdPreviewBaseRender()
	assert.Equal(t, "SENTINEL-WIDTH-80", second, "a repeat call at the same width must hit again")

	m.layout.viewport.Width = 40
	third, _ := m.mdPreviewBaseRender()
	assert.NotEqual(t, "SENTINEL-WIDTH-80", third, "a width change must miss the width-80 cache entry")
}

// TestMdPreviewBaseRender_CacheMissComputesAndStores proves the miss path
// itself: mdPreviewBaseRender must both return a real render (not the empty
// zero value) and leave the cache populated so the next call at the same key
// hits it.
func TestMdPreviewBaseRender_CacheMissComputesAndStores(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text"))
	require.False(t, m.mdPreviewCache.valid, "fixture sanity: cache starts empty")

	rendered, srcMap := m.mdPreviewBaseRender()
	assert.Contains(t, rendered, "Title", "a cache miss must compute and return a real render")
	assert.True(t, srcMap.aligned, "a clean document must align")

	rendered2, srcMap2 := m.mdPreviewBaseRender()
	assert.Equal(t, rendered, rendered2, "a repeat call at the same key must return the exact cached bytes")
	assert.Equal(t, srcMap, srcMap2, "the cached source map must round-trip unchanged")
}

// fenceHeavyCorpusDoc reads a real, fence-heavy plan document from this
// repo's own completed-plans corpus for the task 4 repaint-cost measurement.
// Falls back to skipping the benchmark rather than failing the suite if the
// checkout does not have it (e.g. a shallow clone).
func fenceHeavyCorpusDoc(b *testing.B) string {
	b.Helper()
	path := filepath.Join("..", "..", "docs", "plans", "completed", "20260722-markdown-preview-mode.md")
	data, err := os.ReadFile(path) //nolint:gosec // fixed test path into this repo's own corpus
	if err != nil {
		b.Skipf("fence-heavy corpus document unavailable at %s: %v", path, err)
	}
	return string(data)
}

// BenchmarkMdPreviewRepaint measures the claim behind task 4: a repaint at an
// unchanged file/width/color state must stop paying for a fresh
// glamour+mermaid pass.
//
// Three cases, and the distinction between the last two is the whole point.
// "uncached_full_render_per_repaint" is what every repaint cost before the
// cache existed, and what one would still cost if a repaint path called
// mdPreviewRenderWithMap directly. "cached_base_render_lookup" is the cache
// lookup ALONE — a key comparison and a string return. It is NOT a repaint,
// and an earlier version of this benchmark recorded it as one, which is how a
// full-document width scan came to sit unnoticed in every keypress.
// "cached_repaint_frame" is the real thing: mdPreviewFinalRender, i.e. what
// renderDiff returns while preview is on — cached base render, annotations
// painted in, the horizontal cut, and the block highlight.
func BenchmarkMdPreviewRepaint(b *testing.B) {
	doc := fenceHeavyCorpusDoc(b)
	lines := mdLines(doc)
	m := mdPreviewTestModel(lines)
	m.layout.viewport.Width = 100

	b.Run("uncached_full_render_per_repaint", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			rendered, _ := mdPreviewRenderWithMap(lines, m.layout.viewport.Width, m.cfg.noColors)
			sink = rendered
		}
	})

	b.Run("cached_base_render_lookup", func(b *testing.B) {
		_, _ = m.mdPreviewBaseRender() // warm the cache once, outside the timed loop
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			rendered, _ := m.mdPreviewBaseRender()
			sink = rendered
		}
	})

	b.Run("cached_repaint_frame", func(b *testing.B) {
		// a repaint's real cost lives in the cut and the highlight, and both are
		// skipped by the plain test model: mdPreviewTestModel uses
		// style.PlainResolver (no search background, so mdPreviewHighlight is a
		// documented no-op) and at width 100 this document's widest row is
		// exactly 100 (so applyMdPreviewScroll passes the render straight
		// through). A narrower pane plus a resolver that carries a background is
		// what a reader on a real terminal actually pays per keypress.
		fm := m
		fm.layout.viewport.Width = 80
		res := style.NewResolver(style.Colors{DiffBg: "#112233", SearchBg: "#5f00af", Normal: "#cccccc"})
		fm.resolver, fm.renderer = res, style.NewRenderer(res)
		fm.mdPreviewCache = &mdPreviewRenderCache{}
		sink = fm.mdPreviewFinalRender() // warm both memos once, outside the timed loop
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			sink = fm.mdPreviewFinalRender()
		}
	})
}

// highlightDoc is a plain multi-block document: several short paragraphs, so
// every block is a couple of rows tall and a viewport of 20 rows holds a handful
// of them at once. Deliberately free of tables and mermaid fences — this file's
// tests are about which block the highlight lands on, not about alignment.
const highlightDoc = "# Title\n\n" +
	"First paragraph.\n\n" +
	"Second paragraph.\n\n" +
	"Third paragraph.\n\n" +
	"Fourth paragraph.\n\n" +
	"Fifth paragraph.\n\n" +
	"Sixth paragraph.\n\n" +
	"Seventh paragraph.\n\n" +
	"Eighth paragraph.\n\n" +
	"Ninth paragraph.\n\n" +
	"Tenth paragraph.\n\n" +
	"Eleventh paragraph.\n\n" +
	"Twelfth paragraph."

// widePanDoc is highlightDoc's counterpart for the ragged-cut case: a fenced
// code block far wider than any pane these tests use, so applyMdPreviewScroll
// really cuts instead of taking its "nothing hidden in either direction"
// early return. The short blocks around it are what the highlight lands on, and
// they are the rows the cut leaves ragged.
var widePanDoc = "# Wide\n\n" +
	"Short paragraph.\n\n" +
	"```\n" + strings.Repeat("x", 120) + "\n```\n\n" +
	"Another short paragraph.\n"

// mdPreviewHighlightModel loads highlightDoc into mdPreviewTestModel with
// preview turned on and a resolver that actually carries a search
// background — the two things every test below needs. testModel uses
// style.PlainResolver, whose every color is empty — the highlight is a
// documented no-op there, so a test built on it could never see one.
func mdPreviewHighlightModel(t *testing.T) Model {
	t.Helper()
	return mdPreviewStyledModel(t, highlightDoc)
}

// mdPreviewStyledModel is mdPreviewHighlightModel for any document: preview on,
// with a resolver that carries a real search background so the highlight is
// visible at all.
func mdPreviewStyledModel(t *testing.T, doc string) Model {
	t.Helper()
	m := mdPreviewTestModel(mdLines(doc))
	res := style.NewResolver(style.Colors{DiffBg: "#112233", SearchBg: "#5f00af", Normal: "#cccccc"})
	m.resolver = res
	m.renderer = style.NewRenderer(res)
	m.modes.mdPreview = true
	return m
}

// mdPreviewHighlightBg returns the background escape the highlight paints with,
// so a test can look for it in a frame.
func mdPreviewHighlightBg(m Model) string {
	return string(m.resolver.Color(style.ColorKeySearchBg))
}

// TestMdPreviewHighlightAnchor_FollowsTopmostFullyVisibleBlock is the task's
// central behavior AFTER the block cursor replaced the scroll-derived mark: the
// anchor is whatever block the cursor sits on, and the viewport offset does not
// enter into it at all. Scrolling with the cursor untouched must leave the mark
// exactly where the reader put it — the old rule handed it to a different block
// on every offset change, which is the behavior this replaced.
func TestMdPreviewHighlightAnchor_FollowsTheBlockCursorNotTheOffset(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity: this document must align")
	anchors := srcMap.blocks()
	require.GreaterOrEqual(t, len(anchors), 4, "fixture sanity: need several blocks to steer between")

	for i := range anchors {
		m.setMdPreviewBlockCursor(i)
		assert.Equal(t, i, m.mdPreviewHighlightAnchor(srcMap),
			"with the cursor on block %d, block %d must be the highlighted one", i, i)
	}

	m.setMdPreviewBlockCursor(1)
	for _, offset := range []int{0, anchors[2].row, anchors[len(anchors)-1].row} {
		m.layout.viewport.YOffset = offset
		assert.Equal(t, 1, m.mdPreviewHighlightAnchor(srcMap),
			"the viewport offset must not move the mark; the cursor is what decides it")
	}
}

// TestMdPreviewHighlightAnchor_NothingMarkedUntilTheCursorMoves is the change's
// headline behavior: entering preview marks nothing at all, at any offset. The
// old rule always marked something, always near the top of the pane, which is
// what the user rejected.
func TestMdPreviewHighlightAnchor_NothingMarkedUntilTheCursorMoves(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned)
	anchors := srcMap.blocks()
	require.GreaterOrEqual(t, len(anchors), 3)

	for _, offset := range []int{0, anchors[1].row, anchors[2].row} {
		m.layout.viewport.YOffset = offset
		assert.Equal(t, -1, m.mdPreviewHighlightAnchor(srcMap),
			"with no cursor placed, nothing may be marked at offset %d", offset)
	}

	rows := strings.Split(m.mdPreviewFinalRender(), "\n")
	bg := mdPreviewHighlightBg(m)
	require.NotEmpty(t, bg, "fixture sanity: the resolver must carry a search background")
	for i, row := range rows {
		assert.NotContains(t, row, bg, "row %d must carry no highlight before the cursor is placed", i)
	}
}

// TestMdPreviewHighlightAnchor_CursorPastTheEndOfTheMapMarksNothing pins the
// defensive bound: a map that lost blocks under a placed cursor (a width where
// alignment fails, for instance) must mark nothing rather than index past its
// own anchors.
func TestMdPreviewHighlightAnchor_CursorPastTheEndOfTheMapMarksNothing(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned)

	m.setMdPreviewBlockCursor(len(srcMap.blocks()))

	assert.Equal(t, -1, m.mdPreviewHighlightAnchor(srcMap),
		"a cursor past the last block must mark nothing")
}

// TestMdPreviewHighlightAnchor_UnalignedMapMarksNothing pins the guard the
// block cursor did not replace: a map that failed alignment exposes no blocks,
// so nothing may be marked even with a cursor placed.
func TestMdPreviewHighlightAnchor_UnalignedMapMarksNothing(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned)
	m.setMdPreviewBlockCursor(0)

	assert.Equal(t, -1, m.mdPreviewHighlightAnchor(mdPreviewSourceMap{}), "an unaligned map marks nothing")
	assert.Equal(t, -1, m.mdPreviewHighlightAnchor(mdPreviewSourceMap{aligned: true}), "a map with no blocks marks nothing")
}

// TestMdPreviewFinalRender_PaintsHighlightOnTheAnchoredBlockOnly proves the
// styling actually lands, and lands only on the marked block's own rows.
func TestMdPreviewFinalRender_PaintsHighlightOnTheAnchoredBlockOnly(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned)
	anchors := srcMap.blocks()
	require.GreaterOrEqual(t, len(anchors), 4)

	m.setMdPreviewBlockCursor(2)
	require.Equal(t, 2, m.mdPreviewHighlightAnchor(srcMap), "fixture sanity: block 2 must be the marked one")

	rows := strings.Split(m.mdPreviewFinalRender(), "\n")
	bg := mdPreviewHighlightBg(m)
	require.NotEmpty(t, bg, "fixture sanity: the resolver must carry a search background")

	assert.Contains(t, rows[anchors[2].row], bg, "the marked block's first row must carry the highlight background")
	assert.NotContains(t, rows[anchors[3].row], bg, "a block that is not marked must be left alone")
	assert.NotContains(t, rows[anchors[1].row], bg, "a block above the mark must be left alone")
}

// TestMdPreviewFinalRender_HighlightPreservesVisualShape checks the highlight is
// pure styling: same rows, same stripped text, same per-row display width as the
// unhighlighted frame. Byte comparison is not available on this path at all (see
// the plan's Technical Details), so visual identity is the assertion.
func TestMdPreviewFinalRender_HighlightPreservesVisualShape(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	body, srcMap := m.mdPreviewBody()
	m.setMdPreviewBlockCursor(2)
	require.Equal(t, 2, m.mdPreviewHighlightAnchor(srcMap), "fixture sanity: block 2 must be the marked one")

	plain := strings.Split(m.applyMdPreviewScroll(body), "\n")
	marked := strings.Split(m.mdPreviewFinalRender(), "\n")

	require.Len(t, marked, len(plain), "the highlight must not add or drop rows")
	for i := range plain {
		assert.Equal(t, ansi.Strip(plain[i]), ansi.Strip(marked[i]), "row %d text must be unchanged", i)
		assert.Equal(t, ansi.StringWidth(plain[i]), ansi.StringWidth(marked[i]), "row %d width must be unchanged", i)
	}
}

// TestMdPreviewFinalRender_HighlightDisabled_IdenticalToAnnotationsOnly is the
// task's "with the highlight disabled the render is identical to task 5's"
// checkbox. Two ways the highlight is off, both byte-compared against the
// base-plus-annotations frame task 5 produces.
func TestMdPreviewFinalRender_HighlightDisabled_IdenticalToAnnotationsOnly(t *testing.T) {
	t.Run("no-colors mode", func(t *testing.T) {
		m := mdPreviewHighlightModel(t)
		m.cfg.noColors = true // the mode that promises zero ANSI in the preview
		m.store.Add(annotation.Annotation{File: "plan.md", Line: 1, Type: " ", Comment: "a comment"})

		base, srcMap := m.mdPreviewBaseRender()
		painted, _ := m.mdPreviewPaintAnnotationsTracked(base, srcMap)
		want := m.applyMdPreviewScroll(painted)

		assert.Equal(t, want, m.mdPreviewFinalRender(), "no-colors must render exactly the task-5 frame")
	})

	t.Run("unaligned map", func(t *testing.T) {
		m := mdPreviewHighlightModel(t)
		frame := m.applyMdPreviewScroll(mustBody(t, m))

		assert.Equal(t, frame, m.mdPreviewHighlight(frame, mdPreviewSourceMap{}),
			"a map that failed alignment must leave the frame untouched")
	})
}

// mustBody returns the uncut preview body for a model.
func mustBody(t *testing.T, m Model) string {
	t.Helper()
	body, _ := m.mdPreviewBody()
	return body
}

// TestMdPreviewPaintAnnotationsTracked_ShiftsAnchorsOntoPaintedRows is the
// invariant the highlight depends on: splicing annotation rows moves every row
// below them, so the map handed back must point at the SAME text in the painted
// render that the original map pointed at in the base render. Without it the
// highlight would mark a block one or more rows away from the one it names.
func TestMdPreviewPaintAnnotationsTracked_ShiftsAnchorsOntoPaintedRows(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 1, Type: " ", Comment: "on the title"})
	m.store.Add(annotation.Annotation{File: "plan.md", Line: 5, Type: " ", Comment: "on the second paragraph"})

	base, baseMap := m.mdPreviewBaseRender()
	require.True(t, baseMap.aligned, "fixture sanity: this document must align")

	painted, paintedMap := m.mdPreviewPaintAnnotationsTracked(base, baseMap)
	require.True(t, paintedMap.aligned, "painting must hand back a usable map")
	require.Len(t, paintedMap.blocks(), len(baseMap.blocks()), "painting must not add or drop blocks")
	require.Greater(t, len(strings.Split(painted, "\n")), len(strings.Split(base, "\n")),
		"fixture sanity: the annotations must actually have added rows")

	baseRows := strings.Split(base, "\n")
	paintedRows := strings.Split(painted, "\n")
	for i, a := range baseMap.blocks() {
		shifted := paintedMap.blocks()[i]
		require.Less(t, shifted.row, len(paintedRows), "block %d's shifted row must exist", i)
		assert.Equal(t, ansi.Strip(baseRows[a.row]), ansi.Strip(paintedRows[shifted.row]),
			"block %d's shifted row must hold the same text as before painting", i)
	}
}

// TestMdPreviewWheelBurst_DropsTheCursorOnceForTheWholeBurst is the task's
// "one wheel burst does its deferred work once rather than per event" checkbox,
// now that the deferred work is dropping the block cursor rather than moving a
// scroll-derived mark. The preview path rides the existing wheelState debounce
// (gen / renderPending / tickInFlight, see .claude/rules/gotchas.md) rather than
// bringing a second one: a burst schedules exactly one tick, touches the cursor
// not at all while it runs, and clears it once when the burst flushes.
func TestMdPreviewWheelBurst_DropsTheCursorOnceForTheWholeBurst(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity")
	m.setMdPreviewBlockCursor(0) // the first block, which the burst scrolls away from
	m.layout.viewport.SetContent(m.renderMarkdownPreview())
	require.Greater(t, m.layout.viewport.TotalLineCount(), m.layout.viewport.Height,
		"fixture sanity: the document must be scrollable")

	scheduled := 0
	var model tea.Model = m
	for range 15 {
		var cmd tea.Cmd
		model, cmd = model.(Model).handleWheel(hitDiff, 2)
		if cmd != nil {
			scheduled++
		}
	}
	m = model.(Model)

	assert.Equal(t, 1, scheduled, "only the first wheel of a burst may schedule a tick")
	require.True(t, m.wheel.renderPending, "the burst must still owe exactly one repaint")
	require.True(t, m.wheel.tickInFlight, "the one scheduled tick must still be the only one in flight")
	assert.Equal(t, 0, m.mdPreviewBlockCursor(), "no wheel event may touch the cursor itself")
	require.Greater(t, m.layout.viewport.YOffset, srcMap.blocks()[0].endRow,
		"fixture sanity: the burst must have carried block 0 entirely off the top")

	m.flushWheelPending()

	assert.Equal(t, -1, m.mdPreviewBlockCursor(),
		"the burst's single deferred pass must drop a cursor it scrolled out of view")
	assert.False(t, m.wheel.renderPending, "the flush must clear the owed repaint")
	assert.False(t, m.wheel.tickInFlight, "the flush must clear the in-flight tick")
}

// TestMdPreviewWheelBurst_KeepsACursorStillInView is the other half: the wheel
// clears the cursor only when it really did hide the block, not on any scroll.
func TestMdPreviewWheelBurst_KeepsACursorStillInView(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned, "fixture sanity")
	target := blockVisibleAcrossScroll(t, m, srcMap, wheelStep)
	m.setMdPreviewBlockCursor(target)
	m.layout.viewport.SetContent(m.renderMarkdownPreview())

	model, _ := m.handleWheel(hitDiff, 2)
	m = model.(Model)
	m.flushWheelPending()

	assert.Equal(t, target, m.mdPreviewBlockCursor(), "a wheel that leaves the block in view must keep the cursor")
}

// blockVisibleAcrossScroll returns the index of a block that is wholly inside
// the viewport both at offset 0 and after scrolling down by scroll rows — the
// fixture shape the "a scroll that does not hide the block keeps the cursor"
// tests need.
func blockVisibleAcrossScroll(t *testing.T, m Model, srcMap mdPreviewSourceMap, scroll int) int {
	t.Helper()
	for i, a := range srcMap.blocks() {
		if a.row >= scroll && a.endRow <= m.layout.viewport.Height-1 {
			return i
		}
	}
	t.Fatalf("fixture sanity: no block stays wholly visible across a %d-row scroll", scroll)
	return -1
}

// TestScrollMarkdownPreview_RepaintsAndKeepsAVisibleMark covers the keyboard
// half: the row-scroll keys (J/K, page, half-page) repaint immediately — a key
// press is one event, not a burst — and leave a cursor whose block is still on
// screen exactly where it was.
func TestScrollMarkdownPreview_RepaintsAndKeepsAVisibleMark(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	_, srcMap := m.mdPreviewBody()
	require.True(t, srcMap.aligned)
	target := blockVisibleAcrossScroll(t, m, srcMap, 1)
	m.setMdPreviewBlockCursor(target)
	m.layout.viewport.SetContent(m.renderMarkdownPreview())

	before := m.layout.viewport.View()
	m.scrollMarkdownPreview(1)

	after := m.layout.viewport.View()
	require.Equal(t, 1, m.layout.viewport.YOffset, "fixture sanity: the scroll must have moved the viewport")
	assert.NotEqual(t, before, after, "a keyboard scroll must move what is on screen")
	assert.Equal(t, target, m.mdPreviewBlockCursor(), "a scroll that keeps the block in view must not move the cursor")
	assert.Contains(t, after, mdPreviewHighlightBg(m), "the mark must still be on screen after the scroll")
}

// TestScrollMarkdownPreview_AtEdgeDoesNotRepaint pins the no-op: a scroll that
// cannot move the offset leaves the mark, and the content, exactly as they were.
func TestScrollMarkdownPreview_AtEdgeDoesNotRepaint(t *testing.T) {
	m := mdPreviewHighlightModel(t)
	m.layout.viewport.SetContent(m.renderMarkdownPreview())
	before := m.layout.viewport.View()

	m.scrollMarkdownPreview(-1) // already at the top

	assert.Equal(t, 0, m.layout.viewport.YOffset)
	assert.Equal(t, before, m.layout.viewport.View(), "a scroll that cannot move must not repaint")
}
