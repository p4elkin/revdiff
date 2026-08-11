package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	wantMap := mdPreviewSourceMap{Aligned: true}

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
	m.mdPreviewCache.put(preReloadKey, "STALE-PRE-RELOAD", mdPreviewSourceMap{Aligned: true})

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
	m.mdPreviewCache.put(key80, "SENTINEL-WIDTH-80", mdPreviewSourceMap{Aligned: true})

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
	assert.True(t, srcMap.Aligned, "a clean document must align")

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
// glamour+mermaid pass. "uncached" is what every repaint cost before this
// task (and what it would still cost if a later repaint path, such as the
// scroll-following highlight, called mdPreviewRenderWithMap directly instead
// of going through the cache). "cached" pre-warms the cache once outside the
// timed loop and then measures the steady-state cost of a repeat repaint
// through mdPreviewBaseRender — the shape every scroll-driven repaint takes
// once the highlight task lands on top of this cache.
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

	b.Run("cached_repeat_repaint", func(b *testing.B) {
		_, _ = m.mdPreviewBaseRender() // warm the cache once, outside the timed loop
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			rendered, _ := m.mdPreviewBaseRender()
			sink = rendered
		}
	})
}
