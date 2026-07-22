package ui

import (
	"fmt"
	"strings"
	"testing"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
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

// --- Task 5: annotation and cursor keys inert in preview mode ---
//
// Preview mode replaces the diff pane's one-row-per-source-line render with a
// single whole-document glamour render, so m.nav.diffCursor no longer maps to
// anything on screen. Every key that would create, edit, or navigate to an
// annotation, or move that cursor, must be a no-op while m.modes.mdPreview is
// true. See mdPreviewActionAllowed for the fixed allowlist of what still runs.

// namedKeys maps the special (non-rune) key names used by the tests below to
// their tea.KeyMsg. Anything not listed here is treated by pressKey as a
// literal single-rune key (tea.KeyRunes) — the same shape as the existing
// TestModel_MarkdownPreviewToggle_ViaKeypress 'P' press.
var namedKeys = map[string]tea.KeyMsg{
	"enter":  {Type: tea.KeyEnter},
	"esc":    {Type: tea.KeyEsc},
	"home":   {Type: tea.KeyHome},
	"end":    {Type: tea.KeyEnd},
	"pgdown": {Type: tea.KeyPgDown},
	"pgup":   {Type: tea.KeyPgUp},
	"ctrl+d": {Type: tea.KeyCtrlD},
	"ctrl+u": {Type: tea.KeyCtrlU},
}

// pressKey drives a single key through the full Update path, mirroring
// TestModel_MarkdownPreviewToggle_ViaKeypress — this exercises the real
// dispatch chain (handleKey -> vim-motion interceptor gate -> dispatchAction's
// preview guard -> handlers), not the guard helper in isolation.
func pressKey(t *testing.T, m Model, key string) Model {
	t.Helper()
	msg, ok := namedKeys[key]
	if !ok {
		require.Len(t, key, 1, "pressKey only accepts a single-rune key or a name listed in namedKeys")
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	result, _ := m.Update(msg)
	model, ok2 := result.(Model)
	require.True(t, ok2, "Update must return a Model")
	return model
}

func TestDispatchAction_MdPreviewOn_AnnotationKeysAreInert(t *testing.T) {
	// covers every unsafe action identified in the Task 5 investigation:
	// starting/editing an annotation (Enter, 'a' -> ActionConfirm), starting a
	// file-level annotation ('A'), deleting an annotation ('d'), and opening
	// the annotation-list jump ('@'). A real annotation is pre-seeded on the
	// cursor's line so "delete_annotation" has something to (fail to) delete
	// — otherwise that subtest would trivially pass with no guard at all.
	tests := []struct {
		name string
		key  string
	}{
		{"confirm (start/edit annotation)", "enter"},
		{"annotate_file", "A"},
		{"delete_annotation", "d"},
		{"annot_list", "@"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := mdPreviewTestModel(mdLines("# Title\n\nline one\nline two\nline three"))
			m.nav.diffCursor = 2 // "line one", NewNum=3, ChangeType=" "
			seeded := annotation.Annotation{File: "plan.md", Line: 3, Type: string(diff.ChangeContext), Comment: "existing"}
			m.store.Add(seeded)
			m.annot.cursorOnAnnotation = true // so delete_annotation has a real target, matching the normal-mode landing-on-annotation state
			m.toggleMarkdownPreview()
			require.True(t, m.modes.mdPreview)
			require.Equal(t, 1, m.store.Count())

			model := pressKey(t, m, tc.key)

			assert.False(t, model.annot.annotating, "%s must not open the annotation input while previewing", tc.key)
			assert.False(t, model.overlay.Active(), "%s must not open an overlay while previewing", tc.key)
			require.Equal(t, 1, model.store.Count(), "%s must not change the annotation store while previewing", tc.key)
			assert.Equal(t, seeded, model.store.Get("plan.md")[0], "%s must leave the existing annotation byte-for-byte unchanged", tc.key)
		})
	}
}

// cursorMovementTestLines builds a 40-line fixture with a heading (so mdTOC
// is non-nil) and two real change hunks (ChangeAdd) so next_hunk/prev_hunk
// have somewhere to actually jump — a fixture of pure context lines would
// make those two subtests pass trivially even with no guard at all.
func cursorMovementTestLines() []diff.DiffLine {
	lines := make([]diff.DiffLine, 40)
	lines[0] = diff.DiffLine{NewNum: 1, Content: "# Title", ChangeType: diff.ChangeContext}
	for i := 1; i < len(lines); i++ {
		lines[i] = diff.DiffLine{NewNum: i + 1, Content: "line", ChangeType: diff.ChangeContext}
	}
	lines[5] = diff.DiffLine{NewNum: 6, Content: "added before", ChangeType: diff.ChangeAdd}
	lines[6] = diff.DiffLine{NewNum: 7, Content: "added before", ChangeType: diff.ChangeAdd}
	lines[30] = diff.DiffLine{NewNum: 31, Content: "added after", ChangeType: diff.ChangeAdd}
	lines[31] = diff.DiffLine{NewNum: 32, Content: "added after", ChangeType: diff.ChangeAdd}
	return lines
}

func TestDispatchAction_MdPreviewOn_CursorMovementKeysAreInert(t *testing.T) {
	// broader than the single j/k case the task calls out by name: every
	// cursor-relative navigation action identified in the investigation
	// (page/half-page, home/end, hunk nav, and the search-triggering key)
	// must leave diffCursor untouched too. j and k get their own dedicated
	// assertion below as literally requested by the task. cursor starts at
	// 20, strictly between the two hunks built by cursorMovementTestLines,
	// so next_hunk/prev_hunk each have a real, different target to jump to.
	tests := []struct {
		name string
		key  string
	}{
		{"down (j)", "j"},
		{"up (k)", "k"},
		{"page_down", "pgdown"},
		{"page_up", "pgup"},
		{"half_page_down", "ctrl+d"},
		{"half_page_up", "ctrl+u"},
		{"home", "home"},
		{"end", "end"},
		{"next_hunk", "]"},
		{"prev_hunk", "["},
		{"search", "/"}, // starting a search is unsafe: it can reposition the cursor on a match
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := mdPreviewTestModel(cursorMovementTestLines())
			m.nav.diffCursor = 20
			m.toggleMarkdownPreview()
			require.True(t, m.modes.mdPreview)

			model := pressKey(t, m, tc.key)

			assert.Equal(t, 20, model.nav.diffCursor, "%s must not move the source-line cursor while previewing", tc.name)
			assert.False(t, model.search.active, "%s must not start a search while previewing", tc.name)
		})
	}
}

func TestDispatchAction_MdPreviewOn_JK_CursorUnchanged(t *testing.T) {
	// literal case called out by the task: j/k are the primary cursor-move
	// keys and must be a no-op on m.nav.diffCursor while previewing.
	m := mdPreviewTestModel(mdLines("# Title\n\nline one\nline two\nline three"))
	m.nav.diffCursor = 1
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	afterDown := pressKey(t, m, "j")
	assert.Equal(t, 1, afterDown.nav.diffCursor, "j must not move the cursor while previewing")

	afterUp := pressKey(t, afterDown, "k")
	assert.Equal(t, 1, afterUp.nav.diffCursor, "k must not move the cursor while previewing")
}

func TestInterceptVimMotion_MdPreviewOn_BypassKeysAreInert(t *testing.T) {
	// vim-motion's own screen-position motions (G, gg, zz, H/M/L, count
	// digits) never go through keymap.Resolve/dispatchAction —
	// interceptVimMotion mutates m.nav.diffCursor directly from the raw key
	// (see jumpToLineN). Gating only dispatchAction would leave this path
	// open; handleKey must skip the whole interceptor while previewing.
	lines := make([]diff.DiffLine, 20)
	lines[0] = diff.DiffLine{NewNum: 1, Content: "# Title", ChangeType: diff.ChangeContext}
	for i := 1; i < len(lines); i++ {
		lines[i] = diff.DiffLine{NewNum: i + 1, Content: "line", ChangeType: diff.ChangeContext}
	}
	m := mdPreviewTestModel(lines)
	m.modes.vimMotion = true
	m.nav.diffCursor = 0
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	// bare 'G' normally jumps straight to the last line (jumpToLineN), with
	// no keymap binding involved at all.
	model := pressKey(t, m, "G")

	assert.Equal(t, 0, model.nav.diffCursor, "vim-motion G must not bypass the preview cursor guard")
}

func TestScrollDiffViewportLine_MdPreviewOn_ScrollsButCursorUnchanged(t *testing.T) {
	// the nuance the task calls out explicitly: viewport scrolling
	// (scroll_diff_down/up, bound to J/K) must keep working while previewing
	// — the user needs to scroll the rendered document — but
	// scrollDiffViewportLine also calls pinDiffCursorTo to keep the cursor
	// visible when it scrolls out of view. pinDiffCursorTo's math
	// (cursorVisualRange) walks m.file.lines assuming one row per source
	// line, which is meaningless once the viewport shows the glamour render
	// instead. Without the mdPreview guard in pinDiffCursorTo (mouse.go),
	// this allowed key would silently reassign m.nav.diffCursor.
	lines := make([]diff.DiffLine, 100)
	lines[0] = diff.DiffLine{NewNum: 1, Content: "# Title", ChangeType: diff.ChangeContext}
	for i := 1; i < len(lines); i++ {
		lines[i] = diff.DiffLine{NewNum: i + 1, Content: fmt.Sprintf("- item %d", i), ChangeType: diff.ChangeContext}
	}
	m := mdPreviewTestModel(lines)
	m.nav.diffCursor = 90 // deep in the file: far from the rendered viewport's own row range
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)
	m.layout.viewport.SetYOffset(0) // deterministic starting point regardless of Task 4's own toggle-time scroll

	model := pressKey(t, m, "J") // ActionScrollDiffDown

	assert.Positive(t, model.layout.viewport.YOffset, "J must still scroll the preview viewport")
	assert.Equal(t, 90, model.nav.diffCursor, "scrolling the preview viewport must never reassign the source-line cursor")
}

func TestMdPreviewToggle_RoundTrip_PreservesCursorPosition(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title\n\nline one\nline two\nline three"))
	m.nav.diffCursor = 3
	require.False(t, m.modes.mdPreview)

	onModel := pressKey(t, m, "P")
	require.True(t, onModel.modes.mdPreview)
	assert.Equal(t, 3, onModel.nav.diffCursor, "entering preview must not move the cursor")

	offModel := pressKey(t, onModel, "P")
	require.False(t, offModel.modes.mdPreview, "P must still toggle preview back off while previewing")
	assert.Equal(t, 3, offModel.nav.diffCursor, "leaving preview must restore the cursor to where it was")
}

func TestMdPreviewToggle_RoundTrip_PreservesPreExistingAnnotation(t *testing.T) {
	lines := mdLines("# Title\n\nline one\nline two\nline three")
	m := mdPreviewTestModel(lines)

	// seed an annotation the way normal (non-preview) editing would, on a
	// real line of the loaded file (line 3, a context line -> Type " ").
	want := annotation.Annotation{File: "plan.md", Line: 3, Type: string(diff.ChangeContext), Comment: "pre-existing note"}
	m.store.Add(want)
	require.True(t, m.store.Has("plan.md", 3, string(diff.ChangeContext)))

	onModel := pressKey(t, m, "P")
	require.True(t, onModel.modes.mdPreview)

	offModel := pressKey(t, onModel, "P")
	require.False(t, offModel.modes.mdPreview)

	got := offModel.store.Get("plan.md")
	require.Len(t, got, 1, "the pre-existing annotation must survive the preview round-trip untouched")
	assert.Equal(t, want, got[0], "line anchor, type, and comment must be exactly unchanged")
}
