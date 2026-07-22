package ui

import (
	"strings"
	"testing"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/keymap"
	"github.com/umputun/revdiff/app/ui/sidepane"
)

// mdLines turns a plain document string into []diff.DiffLine the way a full-context
// markdown load would produce it: one context line per source line, in order.
func mdLines(doc string) []diff.DiffLine {
	raw := strings.Split(doc, "\n")
	lines := make([]diff.DiffLine, len(raw))
	for i, c := range raw {
		lines[i] = diff.DiffLine{NewNum: i + 1, Content: c, ChangeType: diff.ChangeContext}
	}
	return lines
}

func TestRenderMermaidFences_NoFences_PassesThrough(t *testing.T) {
	doc := "# Title\n\nSome text.\n\nMore text."
	got := renderMermaidFences(mdLines(doc))
	assert.Equal(t, doc+"\n", got)
}

func TestRenderMermaidFences_OtherLanguageFence_LeftUntouched(t *testing.T) {
	doc := "before\n```go\nfunc main() {}\n```\nafter"
	got := renderMermaidFences(mdLines(doc))
	assert.Equal(t, doc+"\n", got, "a non-mermaid fence must be left byte-identical to the source")
}

func TestRenderMermaidFences_BacktickFence_Renders(t *testing.T) {
	mermaidSrc := "graph TD\n    A --> B"
	want, err := mermaidcmd.RenderDiagram(mermaidSrc, nil)
	require.NoError(t, err)
	require.NotEmpty(t, want)

	doc := "before\n```mermaid\n" + mermaidSrc + "\n```\nafter"
	got := renderMermaidFences(mdLines(doc))

	assert.Contains(t, got, want, "rendered diagram art should appear in place of the fence")
	assert.NotContains(t, got, "```mermaid", "the fence marker itself should be gone once rendered")
	assert.Contains(t, got, "before")
	assert.Contains(t, got, "after")
}

func TestRenderMermaidFences_TildeFence_Renders(t *testing.T) {
	mermaidSrc := "graph TD\n    A --> B"
	want, err := mermaidcmd.RenderDiagram(mermaidSrc, nil)
	require.NoError(t, err)
	require.NotEmpty(t, want)

	doc := "~~~mermaid\n" + mermaidSrc + "\n~~~"
	got := renderMermaidFences(mdLines(doc))

	assert.Contains(t, got, want)
	assert.NotContains(t, got, "~~~mermaid")
}

func TestRenderMermaidFences_TwoFences_BothRender(t *testing.T) {
	mermaidSrc := "graph TD\n    A --> B"
	want, err := mermaidcmd.RenderDiagram(mermaidSrc, nil)
	require.NoError(t, err)
	require.NotEmpty(t, want)

	doc := "before\n```mermaid\n" + mermaidSrc + "\n```\nmiddle\n```mermaid\n" + mermaidSrc + "\n```\nafter"
	got := renderMermaidFences(mdLines(doc))

	assert.Equal(t, 2, strings.Count(got, want), "both mermaid fences should be rendered")
	assert.Contains(t, got, "before")
	assert.Contains(t, got, "middle")
	assert.Contains(t, got, "after")
}

func TestRenderMermaidFences_LongerFenceMarker_StillDetected(t *testing.T) {
	mermaidSrc := "graph TD\n    A --> B"
	want, err := mermaidcmd.RenderDiagram(mermaidSrc, nil)
	require.NoError(t, err)
	require.NotEmpty(t, want)

	doc := "````mermaid\n" + mermaidSrc + "\n````"
	got := renderMermaidFences(mdLines(doc))

	assert.Contains(t, got, want)
}

func TestRenderMermaidFences_LongerFenceMarker_EmbeddedShorterFenceDoesNotClose(t *testing.T) {
	// a fence opened with 4 backticks must not be closed by a 3-backtick line inside it
	// (CommonMark: closing fence length must be >= opening fence length).
	doc := "````go\n```\nstill inside\n````"
	got := renderMermaidFences(mdLines(doc))
	assert.Equal(t, doc+"\n", got)
}

func TestRenderMermaidFences_UnterminatedMermaidFence_NoPanicNoDataLoss(t *testing.T) {
	doc := "intro\n```mermaid\ngraph TD\n    A --> B\nstill going, never closes"

	var got string
	assert.NotPanics(t, func() {
		got = renderMermaidFences(mdLines(doc))
	})

	// nothing from the source document may be dropped
	assert.Contains(t, got, "intro")
	assert.Contains(t, got, "```mermaid")
	assert.Contains(t, got, "graph TD")
	assert.Contains(t, got, "still going, never closes")
}

func TestRenderMermaidFences_MalformedDiagram_FallsBackVerbatim(t *testing.T) {
	malformed := "this is not a valid mermaid diagram at all"

	// sanity check: confirm the underlying library actually errors on this input,
	// otherwise this test would not exercise the fallback path at all.
	_, err := mermaidcmd.RenderDiagram(malformed, nil)
	require.Error(t, err)

	doc := "```mermaid\n" + malformed + "\n```"

	var got string
	assert.NotPanics(t, func() {
		got = renderMermaidFences(mdLines(doc))
	})
	assert.Equal(t, doc+"\n", got, "a diagram that fails to parse must fall back to the original fence text verbatim")
}

func TestRenderMermaidFences_EmptyMermaidFenceBody_NoPanic(t *testing.T) {
	doc := "```mermaid\n```"

	var got string
	assert.NotPanics(t, func() {
		got = renderMermaidFences(mdLines(doc))
	})
	assert.Equal(t, doc+"\n", got, "an empty diagram body should fail to render and fall back verbatim")
}

func TestRenderMermaidFences_SkipsDividerLines(t *testing.T) {
	lines := []diff.DiffLine{
		{NewNum: 1, Content: "before", ChangeType: diff.ChangeContext},
		{Content: "⋯ 3 lines ⋯", ChangeType: diff.ChangeDivider},
		{NewNum: 5, Content: "after", ChangeType: diff.ChangeContext},
	}
	got := renderMermaidFences(lines)
	assert.Equal(t, "before\nafter\n", got, "divider rows carry no document content and must be skipped")
}

// wideMermaidSrc renders to art whose natural width (46 runes, verified by
// TestRenderMarkdownDocument_MermaidArtSurvivesGlamourWithoutReflow's own
// sanity check below) exceeds every narrow width used in this file's tests,
// so those tests actually exercise the anti-reflow behavior instead of
// vacuously passing because the diagram happened to already fit.
const wideMermaidSrc = "graph TD\n    A[This is a moderately long label for node A] --> " +
	"B[This is a moderately long label for node B]"

func TestRenderMarkdownDocument_TableRendersWithAlignedBorders(t *testing.T) {
	doc := "| a | b |\n|---|---|\n| 1 | 2 |\n"

	got := renderMarkdownDocument(mdLines(doc), 80)
	stripped := xansi.Strip(got)

	assert.NotContains(t, stripped, "|---|", "raw markdown table syntax must not survive rendering")
	assert.NotContains(t, stripped, "| a | b |", "raw pipe-delimited source row must not survive rendering")
	assert.Contains(t, stripped, "│", "rendered table should use an aligned column separator")
	assert.Contains(t, stripped, "─", "rendered table should use a horizontal border rule")
	assert.Contains(t, stripped, "a", "cell content should still be present")
	assert.Contains(t, stripped, "1", "cell content should still be present")
}

func TestRenderMarkdownDocument_MermaidArtSurvivesGlamourWithoutReflow(t *testing.T) {
	want, err := mermaidcmd.RenderDiagram(wideMermaidSrc, nil)
	require.NoError(t, err)
	require.NotEmpty(t, want)

	maxArtWidth := 0
	for l := range strings.SplitSeq(want, "\n") {
		if n := len([]rune(l)); n > maxArtWidth {
			maxArtWidth = n
		}
	}
	const narrowWidth = 20
	require.Greater(t, maxArtWidth, narrowWidth,
		"test fixture sanity: the diagram must be wider than narrowWidth or this test cannot detect reflow")

	doc := "before\n\n```mermaid\n" + wideMermaidSrc + "\n```\n\nafter\n"

	// deliberately narrower than the diagram's own natural width, so an
	// implementation that lets glamour reflow the art (e.g. relying on a
	// plain ```mermaid fence alone, without splicing the art in after
	// glamour has rendered everything else) would break this diagram into
	// multiple re-wrapped lines and fail this assertion.
	got := renderMarkdownDocument(mdLines(doc), narrowWidth)
	stripped := xansi.Strip(got)

	assert.Contains(t, stripped, want,
		"the diagram must reach the output byte-exact: unwrapped, unreflowed, untruncated")
	assert.Contains(t, stripped, "before")
	assert.Contains(t, stripped, "after")
}

func TestRenderMarkdownDocument_NarrowWidth_ProseRespectsWidthArtOverflows(t *testing.T) {
	want, err := mermaidcmd.RenderDiagram(wideMermaidSrc, nil)
	require.NoError(t, err)

	doc := "# Heading\n\n" +
		"Some long paragraph text that should wrap because glamour word wraps normal " +
		"prose at the configured width, this sentence is long on purpose to force wrapping.\n\n" +
		"```mermaid\n" + wideMermaidSrc + "\n```\n"

	const width = 20

	var got string
	assert.NotPanics(t, func() {
		got = renderMarkdownDocument(mdLines(doc), width)
	})
	stripped := xansi.Strip(got)

	// decision: box-drawing art has a natural minimum width, and truncating
	// or reflowing it to force it under a narrow viewport would corrupt its
	// shape rather than just make it small — so the diagram is allowed to
	// overflow a narrow width exactly as mermaid-ascii rendered it. Prose,
	// which has no such shape to preserve, must still respect the width.
	assert.Contains(t, stripped, want, "the diagram is allowed to overflow a narrow width, unmodified")

	artLines := make(map[string]bool)
	for l := range strings.SplitSeq(want, "\n") {
		artLines[l] = true
	}
	for l := range strings.SplitSeq(stripped, "\n") {
		if artLines[l] {
			continue // the diagram is exempt from the width constraint — see above
		}
		assert.LessOrEqualf(t, len([]rune(l)), width, "non-diagram line exceeds the requested width: %q", l)
	}
}

func TestMdPreviewCache_HitIgnoresChangedLinesWhenFileAndWidthMatch(t *testing.T) {
	var cache mdPreviewCache

	first := cache.render("plan.md", mdLines("# One"), 80)
	second := cache.render("plan.md", mdLines("# Something else entirely"), 80)

	assert.Equal(t, first, second, "same file and width must be served from cache, ignoring the new lines")
}

func TestMdPreviewCache_WidthChangeInvalidatesCache(t *testing.T) {
	var cache mdPreviewCache
	doc := "Some paragraph text that is long enough to visibly wrap differently at two widths, padded further still.\n"

	narrow := cache.render("plan.md", mdLines(doc), 20)
	wide := cache.render("plan.md", mdLines(doc), 80)

	assert.NotEqual(t, narrow, wide, "a viewport width change must invalidate the cache and re-render")
}

func TestMdPreviewCache_FileChangeInvalidatesCache(t *testing.T) {
	var cache mdPreviewCache

	aOut := cache.render("a.md", mdLines("# A only"), 80)
	bOut := cache.render("b.md", mdLines("# B only"), 80)

	assert.NotEqual(t, aOut, bOut, "a different file name at the same width must not reuse the other file's render")
}

// mdPreviewTestModel builds a Model for the mode-wiring tests: a single
// full-context markdown file loaded, with mdTOC set exactly the way
// handleFileLoaded (app/ui/loaders.go) would set it for a single-file,
// full-context markdown load — the gate toggleMarkdownPreview checks.
func mdPreviewTestModel(lines []diff.DiffLine) Model {
	m := testModel([]string{"plan.md"}, map[string][]diff.DiffLine{"plan.md": lines})
	m.file.name = "plan.md"
	m.file.lines = lines
	m.file.singleFile = true
	m.file.mdTOC = sidepane.ParseTOC(lines, "plan.md")
	m.layout.focus = paneDiff
	m.layout.viewport.Width = 80
	m.layout.viewport.Height = 20
	return m
}

func TestToggleMarkdownPreview_RefusedWhenTOCNil(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text."))
	m.file.mdTOC = nil // not eligible: e.g. a non-markdown file or a partial diff
	require.False(t, m.modes.mdPreview)

	m.toggleMarkdownPreview()

	assert.False(t, m.modes.mdPreview, "toggle must be refused when mdTOC is nil")
	assert.Nil(t, m.file.mdPreviewCache, "no cache should be allocated on a refused toggle")
}

func TestToggleMarkdownPreview_FlipsStateWhenTOCPresent(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text."))
	require.False(t, m.modes.mdPreview)

	m.toggleMarkdownPreview()
	assert.True(t, m.modes.mdPreview, "toggle must flip the mode on when mdTOC is non-nil")

	m.toggleMarkdownPreview()
	assert.False(t, m.modes.mdPreview, "a second toggle must flip it back off")
}

func TestModel_MarkdownPreviewToggle_ViaKeypress(t *testing.T) {
	// end-to-end through Update: keymap resolves 'P' -> dispatchAction's toggle
	// group -> handleViewToggle -> toggleMarkdownPreview, mirroring
	// TestModel_WrapToggle for the 'w' action.
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text."))
	require.False(t, m.modes.mdPreview)

	result, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	model := result.(Model)
	assert.True(t, model.modes.mdPreview)

	result, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	model = result.(Model)
	assert.False(t, model.modes.mdPreview)
}

func TestKeymapResolvesP_ToToggleMarkdownPreview(t *testing.T) {
	km := keymap.Default()
	assert.Equal(t, keymap.ActionTogglePreview, km.Resolve("P"))
}

func TestRenderDiff_MarkdownPreviewOff_RendersNormalDiff(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text."))
	require.False(t, m.modes.mdPreview)

	out := m.renderDiff()

	assert.Contains(t, xansi.Strip(out), "# Title", "mode off must render the raw markdown source, unstyled by glamour")
}

func TestRenderDiff_MarkdownPreviewOn_RendersPreview(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text."))
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)
	require.NotNil(t, m.file.mdPreviewCache, "toggling on must allocate the render cache")

	out := m.renderDiff()

	want := m.file.mdPreviewCache.render("plan.md", m.file.lines, m.layout.viewport.Width)
	assert.Equal(t, want, out, "renderDiff must dispatch to the cached markdown preview render")
	assert.NotContains(t, xansi.Strip(out), "# Title", "glamour must style away the raw '#' heading marker")
}

func TestRenderDiff_MarkdownPreviewOn_FileWithoutTOC_FallsBackToNormalDiff(t *testing.T) {
	// defensive: mdPreview left on from a previous file, but the currently
	// loaded file is no longer eligible (e.g. switched to a non-markdown
	// file). renderDiff's own gate must not trust the stale mode bit alone.
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text."))
	m.modes.mdPreview = true
	m.file.mdTOC = nil

	out := m.renderDiff()

	assert.Contains(t, xansi.Strip(out), "# Title", "without mdTOC, renderDiff must fall back to the normal diff render")
}
