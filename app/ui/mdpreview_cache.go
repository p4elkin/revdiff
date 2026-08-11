package ui

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
