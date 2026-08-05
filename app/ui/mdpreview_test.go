package ui

import (
	"fmt"
	"os"
	"path/filepath"
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
//
// Each label is one hyphenated word ON PURPOSE, for the same reason
// mdPreviewWideDoc's are: these tests are about glamour leaving overflowing art
// alone, and the node-label wrap pass (mdpreview_wrap.go) would otherwise
// narrow the art at the deliberately narrow widths they render at. A label
// with no space to break on is declined by that pass, so the art stays the
// width these tests need.
const wideMermaidSrc = "graph TD\n    A[This-is-a-moderately-long-label-for-node-A] --> " +
	"B[This-is-a-moderately-long-label-for-node-B]"

func TestRenderMarkdownDocument_TableRendersWithAlignedBorders(t *testing.T) {
	doc := "| a | b |\n|---|---|\n| 1 | 2 |\n"

	got := renderMarkdownDocument(mdLines(doc), 80, false)
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
	got := renderMarkdownDocument(mdLines(doc), narrowWidth, false)
	stripped := xansi.Strip(got)

	assert.Contains(t, stripped, want,
		"the diagram must reach the output byte-exact: unwrapped, unreflowed, untruncated")
	assert.Contains(t, stripped, "before")
	assert.Contains(t, stripped, "after")
}

// TestRenderMarkdownDocument_MermaidArtCarriesNoControlBytes pins that a
// control byte written into a diagram cannot reach the terminal. The art is the
// one part of the document that never passes through glamour (see
// spliceMermaidArt), so an ESC in an author's node label would otherwise arrive
// live and repaint the screen. Rendered with --no-colors on purpose: glamour
// then emits no escape of its own, so any escape left in the output can only
// have come out of the art.
func TestRenderMarkdownDocument_MermaidArtCarriesNoControlBytes(t *testing.T) {
	tests := []struct {
		name, fence string
	}{
		{"a single-rendered diagram", "flowchart TD\n    A[\"x\x1b[31mRED\"] --> B"},
		{"a split diagram", "flowchart TD\n" +
			"    subgraph before[\"Before\"]\n        B1[\"b\x1b[31mRED\"] --> B2[old write]\n    end\n" +
			"    subgraph after[\"After\"]\n        A1[new read] --> A2[new write]\n    end"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := "prose\n\n```mermaid\n" + tc.fence + "\n```\n"

			got := renderMarkdownDocument(mdLines(doc), 80, true)

			assert.NotContains(t, got, "\x1b", "a control byte in a label must never reach the terminal")
			assert.Contains(t, got, "RED", "only the escape is dropped, the label text stays")
		})
	}
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
		got = renderMarkdownDocument(mdLines(doc), width, false)
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

func TestRenderMarkdownDocument_ProseMatchingOldPlaceholder_NotReplaced(t *testing.T) {
	// The pre-nonce placeholder was a fixed string. A document paragraph exactly
	// equal to it collided with the splice step and got replaced by the real
	// diagram's art. With a per-render nonce the placeholder is unguessable, so
	// this prose line must survive untouched while the real mermaid fence still
	// renders and splices in.
	const oldStaticPlaceholder = "mdpreviewmermaidplaceholder0mdpreviewmermaidplaceholder"
	want, err := mermaidcmd.RenderDiagram("graph TD\n    A --> B", nil)
	require.NoError(t, err)
	require.NotEmpty(t, want)

	doc := "intro\n\n" + oldStaticPlaceholder + "\n\n```mermaid\ngraph TD\n    A --> B\n```\n\noutro\n"
	got := renderMarkdownDocument(mdLines(doc), 80, false)
	stripped := xansi.Strip(got)

	assert.Contains(t, stripped, oldStaticPlaceholder,
		"a prose line equal to the old static placeholder must not be replaced by diagram art")
	assert.Contains(t, stripped, want,
		"the real mermaid diagram must still be rendered and spliced in")
}

func TestRenderMarkdownDocument_TwoDiagrams_LandInOrderOnOwnPlaceholders(t *testing.T) {
	// consume-left-to-right: two distinct diagrams must each land on their own
	// placeholder, in document order — no value splices twice, and the order is
	// preserved.
	srcA := "graph TD\n    A1 --> A2"
	srcB := "graph LR\n    B1 --> B2"
	artA, err := mermaidcmd.RenderDiagram(srcA, nil)
	require.NoError(t, err)
	require.NotEmpty(t, artA)
	artB, err := mermaidcmd.RenderDiagram(srcB, nil)
	require.NoError(t, err)
	require.NotEmpty(t, artB)
	require.NotEqual(t, artA, artB, "fixture sanity: the two diagrams must render to distinct art")

	doc := "top\n\n```mermaid\n" + srcA + "\n```\n\nmid\n\n```mermaid\n" + srcB + "\n```\n\nbot\n"
	got := renderMarkdownDocument(mdLines(doc), 80, false)
	stripped := xansi.Strip(got)

	iA := strings.Index(stripped, artA)
	iB := strings.Index(stripped, artB)
	require.GreaterOrEqual(t, iA, 0, "first diagram art must be present")
	require.GreaterOrEqual(t, iB, 0, "second diagram art must be present")
	assert.Less(t, iA, iB, "each diagram's art must land on its own placeholder, in document order")
}

// readMermaidFixture loads one of the two real-world fences under
// testdata/mermaid/ — collision-three-branches.mmd and bleed-crossing-edge.mmd,
// copied verbatim from the user document that motivated both the collision
// detector (task 4) and the no-break-space substitution (tasks 2-3). The
// source document itself is a temp file outside the repo and is never
// referenced from a test, only from this plan's task 6 note; the fixtures
// here are the durable copy.
func readMermaidFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("testdata", "mermaid", name)
	b, err := os.ReadFile(path) //nolint:gosec // fixed test path
	require.NoError(t, err)
	return strings.TrimRight(string(b), "\n")
}

func TestRenderMarkdownDocument_CollisionFixture_RendersWithZeroCollisions(t *testing.T) {
	source := readMermaidFixture(t, "collision-three-branches.mmd")
	doc := "```mermaid\n" + source + "\n```\n"

	got := renderMarkdownDocument(mdLines(doc), 120, false)
	stripped := xansi.Strip(got)

	toRender := source
	if transpiled, ok := transpileMermaid(source, 120); ok {
		toRender = transpiled
	}
	collisions := mermaidCollisionCount(toRender, stripped)
	assert.Equal(t, 0, collisions,
		"the three-branch fixture must render with zero label collisions after the LR retry\nart:\n%s", stripped)

	// The assertion above uses the same detector the retry is gated on, so on
	// its own it can only say "the detector is satisfied". This one is
	// independent of it: the two labels that shared a row in the broken render
	// must now be readable — either on different rows, or with real space
	// between them.
	assertLabelsReadable(t, stripped,
		mermaidNBSPSubstitute("collection"), mermaidNBSPSubstitute("single composite"))
}

// assertLabelsReadable checks two edge labels are not crammed into one arrow
// corridor, without consulting mermaidCollisionCount: it finds every row
// carrying both labels and requires a visible run of separator between them.
func assertLabelsReadable(t *testing.T, art, first, second string) {
	t.Helper()
	shared := 0
	for row := range strings.SplitSeq(art, "\n") {
		i, j := strings.Index(row, first), strings.Index(row, second)
		if i < 0 || j < 0 {
			continue
		}
		shared++
		lo, hi := i+len(first), j
		if j < i {
			lo, hi = j+len(second), i
		}
		assert.GreaterOrEqual(t, hi-lo, mermaidCollisionGap,
			"%q and %q share a row with only %d columns between them:\n%s", first, second, hi-lo, row)
	}
	assert.Equal(t, 0, shared, "the two labels must not share a row at all after the flip:\n%s", art)
}

// TestRenderMarkdownDocument_FittingTDFixture_LabelSurvivesAtAPaneItFits is the
// regression pin for the pane-width window the old fit-only width gate opened.
// This fixture's top-down render is 150 columns wide, so at a 160-column pane it
// FITS — and the gate used to read that as "leave it alone", throwing away the LR
// flip and leaving the reader with `collection` painted over `single composite`
// (`├◄───collectioningle composite──────┤`, the `s` destroyed). Measured at the
// time: labels intact at panes 80, 120, 240, 300 and 400, wrecked at 160 and 200.
// The gate now objects on the width RATIO instead (238/150 is 1.59), so the flip
// is kept at every one of those pane widths.
func TestRenderMarkdownDocument_FittingTDFixture_LabelSurvivesAtAPaneItFits(t *testing.T) {
	source := readMermaidFixture(t, "collision-fitting-td-render.mmd")
	doc := "```mermaid\n" + source + "\n```\n"

	got := renderMarkdownDocument(mdLines(doc), 160, false)
	stripped := xansi.Strip(got)

	// The no-break-space substitution runs before the render, so the label
	// reaches the art with U+00A0 where its space was.
	assert.Contains(t, stripped, mermaidNBSPSubstitute("single composite"),
		"the `single composite` label must reach the art intact at a pane the top-down render fits\nart:\n%s", stripped)
	assert.Contains(t, stripped, "collection",
		"the `collection` label must reach the art intact too\nart:\n%s", stripped)
}

func TestRenderMarkdownDocument_BleedFixture_LabelReachesArtIntact(t *testing.T) {
	source := readMermaidFixture(t, "bleed-crossing-edge.mmd")
	doc := "```mermaid\n" + source + "\n```\n"

	got := renderMarkdownDocument(mdLines(doc), 120, false)
	stripped := xansi.Strip(got)

	wantLabel := mermaidNBSPSubstitute("index or '-': item add/remove/reorder")
	assert.Contains(t, stripped, wantLabel,
		"the crossing-edge label must reach the art with its spaces intact, no character cutting through it")
	assert.NotContains(t, stripped, "item│add/remove/reorder",
		"the crossing edge's │ must not bleed through the label")
}

func TestRenderMarkdownDocument_Fixtures_NoPanicNoBlankRender(t *testing.T) {
	for _, name := range []string{"collision-three-branches.mmd", "bleed-crossing-edge.mmd", "collision-fitting-td-render.mmd"} {
		t.Run(name, func(t *testing.T) {
			source := readMermaidFixture(t, name)
			doc := "```mermaid\n" + source + "\n```\n"

			var got string
			assert.NotPanics(t, func() {
				got = renderMarkdownDocument(mdLines(doc), 120, false)
			})
			stripped := xansi.Strip(got)
			assert.NotEmpty(t, strings.TrimSpace(stripped), "fixture must not render blank")
		})
	}
}

func TestRenderMarkdownPreview_NoColors_ProducesNoANSI(t *testing.T) {
	// --no-colors / REVDIFF_NO_COLORS must reach the preview: pressing P with
	// colors disabled must not emit any ANSI escape sequence. Uses content that
	// glamour would normally color (heading, bold, list, table) so a colored
	// render would definitely contain "\x1b[".
	doc := "# Heading\n\n**bold** and normal text\n\n- item one\n- item two\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"
	m := mdPreviewTestModel(mdLines(doc))
	m.cfg.noColors = true
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	got := m.renderMarkdownPreview()
	assert.NotContains(t, got, "\x1b[",
		"with --no-colors, the markdown preview must emit no ANSI escape sequences")
	// content must still render (not be empty or dropped)
	assert.Contains(t, got, "Heading", "heading text must survive the no-color render")
	assert.Contains(t, got, "item one", "list content must survive the no-color render")
}

func TestRenderMarkdownPreview_Colors_ProducesANSI(t *testing.T) {
	// the default (colors on) path must still emit ANSI styling — proves the
	// no-color assertion above is not vacuous.
	doc := "# Heading\n\nsome text"
	m := mdPreviewTestModel(mdLines(doc))
	m.cfg.noColors = false
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	got := m.renderMarkdownPreview()
	assert.Contains(t, got, "\x1b[",
		"the default (colors on) markdown preview must emit ANSI styling")
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
	m.file.markdownPreviewable = true // full-context markdown, the way loaders.go sets it
	m.file.mdTOC = sidepane.ParseTOC(lines, "plan.md")
	m.layout.focus = paneDiff
	m.layout.viewport.Width = 80
	m.layout.viewport.Height = 20
	return m
}

func TestToggleMarkdownPreview_RefusedWhenNotPreviewable(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text."))
	m.file.markdownPreviewable = false // not eligible: e.g. a non-markdown file or a partial diff
	m.file.mdTOC = nil
	require.False(t, m.modes.mdPreview)

	m.toggleMarkdownPreview()

	assert.False(t, m.modes.mdPreview, "toggle must be refused when the file is not previewable")
}

func TestToggleMarkdownPreview_FlipsStateWhenTOCPresent(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text."))
	require.False(t, m.modes.mdPreview)

	m.toggleMarkdownPreview()
	assert.True(t, m.modes.mdPreview, "toggle must flip the mode on when mdTOC is non-nil")

	m.toggleMarkdownPreview()
	assert.False(t, m.modes.mdPreview, "a second toggle must flip it back off")
}

func TestToggleMarkdownPreview_HeadinglessMarkdown_TogglesOnAndRenders(t *testing.T) {
	// A single full-context markdown file with NO '#' headings: ParseTOC returns
	// nil (no TOC entries), but the file is still a valid single full-context
	// markdown document. Preview must open and render — the old gate on
	// mdTOC != nil wrongly refused it, because mdTOC also encodes "has headings".
	// Drive the real load path so markdownPreviewable is set by loaders.go.
	lines := mdLines("just some prose here\n\n- a list item\n- another list item\n\nclosing prose")
	m := testModel([]string{"notes.md"}, map[string][]diff.DiffLine{"notes.md": lines})
	m.file.singleFile = true

	result, _ := m.handleFileLoaded(fileLoadedMsg{file: "notes.md", lines: lines, seq: m.file.loadSeq})
	m = result.(Model)
	require.Nil(t, m.file.mdTOC, "fixture sanity: heading-less markdown must yield a nil TOC")

	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview, "heading-less markdown must still enter preview")

	out := m.renderDiff()
	assert.Equal(t, m.renderMarkdownPreview(), out,
		"renderDiff must dispatch to the markdown preview render for heading-less markdown")
	assert.NotContains(t, xansi.Strip(out), "- a list item",
		"glamour must style the raw list markers away, proving the preview actually rendered")
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

	out := m.renderDiff()

	want := renderMarkdownDocument(m.file.lines, m.layout.viewport.Width, m.cfg.noColors)
	assert.Equal(t, want, out, "renderDiff must dispatch to the markdown preview render")
	assert.NotContains(t, xansi.Strip(out), "# Title", "glamour must style away the raw '#' heading marker")
}

func TestRenderDiff_MarkdownPreviewOn_FileWithoutTOC_FallsBackToNormalDiff(t *testing.T) {
	// defensive: mdPreview left on from a previous file, but the currently
	// loaded file is no longer eligible (e.g. switched to a non-markdown
	// file). renderDiff's own gate must not trust the stale mode bit alone.
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text."))
	m.modes.mdPreview = true
	m.file.markdownPreviewable = false
	m.file.mdTOC = nil

	out := m.renderDiff()

	assert.Contains(t, xansi.Strip(out), "# Title", "when not previewable, renderDiff must fall back to the normal diff render")
}

// --- multi-file reviews: preview follows the displayed file, not the review size ---
//
// What makes a whole-document glamour render safe is that the displayed file is
// full-context markdown — every line of it is present, so nothing can be shown
// half-rendered. How many files the review contains has nothing to do with that,
// so markdownPreviewable is not gated on m.file.singleFile. The markdown TOC is:
// it draws into the paneTree slot that the file tree owns in a multi-file review
// (see the gate in loaders.go and the render branch in view.go).

// javaDiffLines is the stand-in for the non-markdown file in a mixed review: a
// real diff with added and removed lines, so isFullContext is false for it for
// two independent reasons (extension and change types).
func javaDiffLines() []diff.DiffLine {
	return []diff.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "class Main {", ChangeType: diff.ChangeContext},
		{OldNum: 2, Content: "  int old;", ChangeType: diff.ChangeRemove},
		{NewNum: 2, Content: "  int shiny;", ChangeType: diff.ChangeAdd},
		{OldNum: 3, NewNum: 3, Content: "}", ChangeType: diff.ChangeContext},
	}
}

// mdPreviewMultiFileModel builds a review holding several files, with the file
// list already loaded so m.file.singleFile is false the way handleFilesLoaded
// leaves it. No file diff is loaded yet — each test drives handleFileLoaded
// itself for the file it cares about. Viewport width matches View's two-pane
// diffPaneW (width - treeWidth - 4), the way handleResize sets it.
func mdPreviewMultiFileModel(t *testing.T, paths []string, diffs map[string][]diff.DiffLine) Model {
	t.Helper()
	entries := make([]diff.FileEntry, len(paths))
	for i, p := range paths {
		entries[i] = diff.FileEntry{Path: p}
	}
	m := testModel(paths, diffs)
	result, _ := m.Update(filesLoadedMsg{entries: entries})
	m = result.(Model)
	require.False(t, m.file.singleFile, "fixture sanity: a multi-file review must not be single-file")
	m.layout.focus = paneDiff
	m.layout.viewport.Width = m.layout.width - m.layout.treeWidth - 4
	m.layout.viewport.Height = 20
	return m
}

func TestHandleFileLoaded_MultiFileReview_FullContextMarkdownIsPreviewable(t *testing.T) {
	// the reported bug: a mixed markdown + java review refused P on the markdown
	// file, because the gate also demanded a single-file review.
	mdSrc := mdLines("# Plan\n\nSome prose.\n")
	m := mdPreviewMultiFileModel(t, []string{"Main.java", "plan.md"},
		map[string][]diff.DiffLine{"Main.java": javaDiffLines(), "plan.md": mdSrc})

	result, _ := m.handleFileLoaded(fileLoadedMsg{file: "plan.md", lines: mdSrc, seq: m.file.loadSeq})
	m = result.(Model)

	assert.True(t, m.file.markdownPreviewable,
		"a full-context markdown file must be previewable even when the review holds other files")
}

func TestHandleFileLoaded_MultiFileReview_MarkdownKeepsFileTreeInsteadOfTOC(t *testing.T) {
	// the TOC must NOT follow the relaxed preview gate: it renders into the
	// paneTree slot, which in a multi-file review is the file tree's.
	mdSrc := mdLines("# Plan\n\n## Section\n\nSome prose.\n")
	m := mdPreviewMultiFileModel(t, []string{"Main.java", "plan.md"},
		map[string][]diff.DiffLine{"Main.java": javaDiffLines(), "plan.md": mdSrc})

	result, _ := m.handleFileLoaded(fileLoadedMsg{file: "plan.md", lines: mdSrc, seq: m.file.loadSeq})
	m = result.(Model)

	assert.Nil(t, m.file.mdTOC, "the markdown TOC must stay off in a multi-file review")
	assert.False(t, m.treePaneHidden(), "the file tree pane must stay visible")
	assert.Contains(t, xansi.Strip(m.View()), "Main.java",
		"the left pane must still render the file tree, not a table of contents")
}

func TestToggleMarkdownPreview_MultiFileReview_TogglesOnAndRendersPreview(t *testing.T) {
	mdSrc := mdLines("# Plan\n\nSome prose.\n")
	m := mdPreviewMultiFileModel(t, []string{"Main.java", "plan.md"},
		map[string][]diff.DiffLine{"Main.java": javaDiffLines(), "plan.md": mdSrc})
	result, _ := m.handleFileLoaded(fileLoadedMsg{file: "plan.md", lines: mdSrc, seq: m.file.loadSeq})
	m = result.(Model)
	require.False(t, m.modes.mdPreview)

	m.toggleMarkdownPreview()

	require.True(t, m.modes.mdPreview, "P must turn preview on for the markdown file of a mixed review")
	out := m.renderDiff()
	assert.Equal(t, m.renderMarkdownPreview(), out, "renderDiff must dispatch to the markdown preview render")
	assert.NotContains(t, xansi.Strip(out), "# Plan", "glamour must style away the raw '#' heading marker")
}

func TestHandleFileLoaded_MultiFileReview_PreviewSurvivesSwitchToAnotherMarkdown(t *testing.T) {
	// both files are full-context markdown, so the mode stays on across the
	// switch and the pane shows the newly loaded document.
	first := mdLines("# First\n\nprose about the first plan.\n")
	second := mdLines("# Second\n\nprose about the second plan.\n")
	m := mdPreviewMultiFileModel(t, []string{"first.md", "second.md"},
		map[string][]diff.DiffLine{"first.md": first, "second.md": second})
	result, _ := m.handleFileLoaded(fileLoadedMsg{file: "first.md", lines: first, seq: m.file.loadSeq})
	m = result.(Model)
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	result, _ = m.handleFileLoaded(fileLoadedMsg{file: "second.md", lines: second, seq: m.file.loadSeq})
	m = result.(Model)

	assert.True(t, m.modes.mdPreview, "preview must stay on when the next file is previewable too")
	rendered := xansi.Strip(m.renderDiff())
	assert.Contains(t, rendered, "prose about the second plan", "the pane must show the newly loaded document")
	assert.NotContains(t, rendered, "prose about the first plan", "the previous document must be gone")
	assert.NotContains(t, rendered, "# Second", "the new document must be rendered, not shown as raw source")
}

func TestHandleFileLoaded_MultiFileReview_SwitchToJavaClearsPreview(t *testing.T) {
	mdSrc := mdLines("# Plan\n\nSome prose.\n")
	java := javaDiffLines()
	m := mdPreviewMultiFileModel(t, []string{"Main.java", "plan.md"},
		map[string][]diff.DiffLine{"Main.java": java, "plan.md": mdSrc})
	result, _ := m.handleFileLoaded(fileLoadedMsg{file: "plan.md", lines: mdSrc, seq: m.file.loadSeq})
	m = result.(Model)
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	result, _ = m.handleFileLoaded(fileLoadedMsg{file: "Main.java", lines: java, seq: m.file.loadSeq})
	m = result.(Model)

	assert.False(t, m.file.markdownPreviewable, "a java file is never previewable")
	assert.False(t, m.modes.mdPreview, "preview must switch off when the next file cannot render it")
	assert.Contains(t, xansi.Strip(m.renderDiff()), "int shiny", "the java file must render as an ordinary diff")
}

func TestHandleFileLoaded_MultiFileReview_ModifiedMarkdownIsNotPreviewable(t *testing.T) {
	// a markdown file shown as a real diff is only partly present, so the
	// full-context condition still refuses it — relaxing singleFile did not
	// widen the gate in that direction.
	partial := []diff.DiffLine{
		{OldNum: 1, NewNum: 1, Content: "# Plan", ChangeType: diff.ChangeContext},
		{OldNum: 2, Content: "old wording", ChangeType: diff.ChangeRemove},
		{NewNum: 2, Content: "new wording", ChangeType: diff.ChangeAdd},
	}
	m := mdPreviewMultiFileModel(t, []string{"Main.java", "plan.md"},
		map[string][]diff.DiffLine{"Main.java": javaDiffLines(), "plan.md": partial})

	result, _ := m.handleFileLoaded(fileLoadedMsg{file: "plan.md", lines: partial, seq: m.file.loadSeq})
	m = result.(Model)

	require.False(t, m.file.markdownPreviewable, "a partially shown markdown file must stay refused")
	m.toggleMarkdownPreview()
	assert.False(t, m.modes.mdPreview, "P must be refused for a modified (not full-context) markdown file")
}

func TestView_MdPreviewInMultiFileReview_PaneGeometryIntact(t *testing.T) {
	// same invariant as TestView_MdPreviewPanned_PaneGeometryIntact, but with the
	// file tree present: the pan clamp uses the narrower two-pane viewport width,
	// so no row may overflow the terminal or soft-wrap and shift the scrollbar's
	// hardcoded first-viewport-row offset.
	wide := mdLines(mdPreviewWideDoc)
	m := mdPreviewMultiFileModel(t, []string{"Main.java", "wide.md"},
		map[string][]diff.DiffLine{"Main.java": javaDiffLines(), "wide.md": wide})
	result, _ := m.handleFileLoaded(fileLoadedMsg{file: "wide.md", lines: wide, seq: m.file.loadSeq})
	m = result.(Model)
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)
	require.False(t, m.treePaneHidden(), "fixture sanity: the tree pane must be visible for this geometry")

	before := strings.Split(m.View(), "\n")
	for range 3 {
		m.panMarkdownPreview(1)
	}
	require.Positive(t, m.layout.scrollX, "fixture sanity: the art must be pannable")
	after := strings.Split(m.View(), "\n")

	assert.Len(t, after, len(before), "panning must not change the frame's row count (no soft-wrapped row)")
	for i, line := range after {
		assert.LessOrEqual(t, xansi.StringWidth(line), m.layout.width,
			"row %d of the panned frame must fit the terminal width", i)
	}
}

func TestPanMarkdownPreview_MultiFileReview_ClampsAgainstTwoPaneWidth(t *testing.T) {
	// the clamp basis is the viewport width, which is narrower here than in the
	// full-width single-file case, so the same document pans further.
	wide := mdLines(mdPreviewWideDoc)
	m := mdPreviewMultiFileModel(t, []string{"Main.java", "wide.md"},
		map[string][]diff.DiffLine{"Main.java": javaDiffLines(), "wide.md": wide})
	result, _ := m.handleFileLoaded(fileLoadedMsg{file: "wide.md", lines: wide, seq: m.file.loadSeq})
	m = result.(Model)
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	rendered := renderMarkdownDocument(m.file.lines, m.layout.viewport.Width, m.cfg.noColors)
	want := mdPreviewMaxOffset(rendered, m.mdPreviewCutWidth())
	require.Positive(t, want, "fixture sanity: the art must be wider than the two-pane viewport")

	for range 50 {
		m.panMarkdownPreview(1)
	}

	assert.Equal(t, want, m.layout.scrollX, "the pan must clamp at the two-pane viewport width, not the full width")
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
	"left":   {Type: tea.KeyLeft},
	"right":  {Type: tea.KeyRight},
	"down":   {Type: tea.KeyDown},
	"up":     {Type: tea.KeyUp},
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

func TestDispatchAction_MdPreviewOn_ReadingKeysScrollViewportNotCursor(t *testing.T) {
	// the reading keys move the render and never the source-line cursor. The
	// cursor half is already pinned by the tests above; this pins the half
	// that was missing, i.e. that they actually scroll. Without it the keys
	// stay in the allowlist and silently do nothing, which is the bug this
	// replaced: J/K were the only way to reach past the first screen.
	tests := []struct {
		name string
		key  string
	}{
		{"down (j)", "j"},
		{"down (arrow)", "down"},
		{"page_down", "pgdown"},
		{"half_page_down", "ctrl+d"},
		{"end", "end"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lines := make([]diff.DiffLine, 300)
			for i := range lines {
				lines[i] = diff.DiffLine{NewNum: i + 1, Content: fmt.Sprintf("- item %d", i), ChangeType: diff.ChangeContext}
			}
			m := mdPreviewTestModel(lines)
			m.nav.diffCursor = 20
			m.toggleMarkdownPreview()
			require.True(t, m.modes.mdPreview)
			require.Equal(t, 0, m.layout.viewport.YOffset)

			model := pressKey(t, m, tc.key)

			assert.Positive(t, model.layout.viewport.YOffset, "%s must scroll the preview viewport", tc.name)
			assert.Equal(t, 20, model.nav.diffCursor, "%s must not move the source-line cursor while previewing", tc.name)
		})
	}
}

func TestDispatchAction_MdPreviewOn_ReadingKeysReverseAndClamp(t *testing.T) {
	// the up direction, and the clamp at both ends: k at the top must not
	// produce a negative offset, and end/home are absolute.
	lines := make([]diff.DiffLine, 300)
	for i := range lines {
		lines[i] = diff.DiffLine{NewNum: i + 1, Content: fmt.Sprintf("- item %d", i), ChangeType: diff.ChangeContext}
	}
	m := mdPreviewTestModel(lines)
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	assert.Equal(t, 0, pressKey(t, m, "k").layout.viewport.YOffset, "k at the top must clamp, not go negative")

	atEnd := pressKey(t, m, "end")
	require.Positive(t, atEnd.layout.viewport.YOffset, "end must move to the bottom")

	backUp := pressKey(t, atEnd, "pgup")
	assert.Less(t, backUp.layout.viewport.YOffset, atEnd.layout.viewport.YOffset, "pgup must scroll back up")
	assert.Equal(t, 0, pressKey(t, atEnd, "home").layout.viewport.YOffset, "home must return to the top")
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
	// entering preview resets the viewport to the top rather than scrolling to a
	// diff-line-derived offset — assert that here instead of forcing it, so this
	// test also pins the toggle-on scroll behavior.
	require.Equal(t, 0, m.layout.viewport.YOffset, "entering preview must reset the viewport to the top")

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

// --- Task 6: regression check — mode off must be unchanged ---
//
// With m.modes.mdPreview off, renderDiff's early-return branch
// (`if m.modes.mdPreview && m.file.mdTOC != nil`) must never be taken, so the
// rest of the function — the same render loop that existed before this
// feature — must behave exactly as it did before. There is no pre-feature
// build to diff against at runtime, so the two tests below prove the
// guarantee directly on the current code:
//
//   - TestRenderDiff_MarkdownPreviewOff_MarkdownFile_IdenticalRegardlessOfMdTOC
//     covers the case where mdTOC IS set (a full-context single markdown
//     file) — the only thing keeping the branch untaken is the flag itself.
//     It also proves the check is not vacuous: flipping the flag on the same
//     model must actually change the output, or an always-false branch would
//     make every assertion here pass trivially.
//   - TestRenderDiff_MarkdownPreviewOff_NonMarkdownFile_DoublyGated covers the
//     case where mdTOC is nil (a non-markdown file) — the branch must stay
//     untaken even if the flag were somehow left on, so the gate does not
//     rely on the flag alone either.

func TestRenderDiff_MarkdownPreviewOff_MarkdownFile_IdenticalRegardlessOfMdTOC(t *testing.T) {
	doc := "# Title\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"
	m := mdPreviewTestModel(mdLines(doc))
	require.False(t, m.modes.mdPreview)
	require.NotNil(t, m.file.mdTOC, "fixture sanity: a full-context single markdown file must set mdTOC")

	offWithTOC := m.renderDiff()
	assert.Contains(t, offWithTOC, "| a | b |", "mode off must render the raw markdown source, pipes and all")
	assert.NotContains(t, offWithTOC, "│", "mode off must contain no glamour-rendered table border")

	// mdTOC alone (present vs absent) must not change the output when the
	// flag is off — only the flag gates the branch.
	withoutTOC := m
	withoutTOC.file.mdTOC = nil
	offWithoutTOC := withoutTOC.renderDiff()

	assert.Equal(t, offWithTOC, offWithoutTOC,
		"with mdPreview off, renderDiff output must be identical whether or not mdTOC is set — this proves the "+
			"early-return branch is genuinely gated on the flag, not on mdTOC alone")

	// non-vacuous proof: the equality above means nothing if the preview
	// branch never fires under any condition. Flip mdPreview on for the same
	// markdown model (mdTOC still present) and confirm the render genuinely
	// changes — so the off-case checks above are exercising a real branch.
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)
	onOut := m.renderDiff()

	assert.NotEqual(t, offWithTOC, onOut,
		"sanity check: enabling preview must actually change renderDiff's output, otherwise the off-case "+
			"equality above would pass even with a broken guard")
	assert.Contains(t, onOut, "│", "mode on must render an aligned glamour table border")
	assert.NotContains(t, onOut, "| a | b |", "mode on must not leave raw markdown pipe syntax in the output")
}

func TestRenderDiff_MarkdownPreviewOff_NonMarkdownFile_DoublyGated(t *testing.T) {
	lines := []diff.DiffLine{
		{NewNum: 1, Content: "package main", ChangeType: diff.ChangeContext},
		{NewNum: 2, Content: "", ChangeType: diff.ChangeContext},
		{NewNum: 3, Content: "func main() {}", ChangeType: diff.ChangeContext},
	}
	m := mdPreviewTestModel(lines)
	m.file.name = "main.go"
	m.file.markdownPreviewable = false // a non-markdown file is never previewable (see the gate in loaders.go)
	m.file.mdTOC = nil
	require.False(t, m.modes.mdPreview)

	off := m.renderDiff()
	assert.Contains(t, off, "package main", "mode off must render the raw source unchanged")

	// doubly-gated: force the flag on directly, bypassing
	// toggleMarkdownPreview's own not-previewable refusal, to prove renderDiff's
	// own early-return branch also requires markdownPreviewable and falls back
	// byte-for-byte to the exact same normal render even if mdPreview is
	// somehow left true.
	forced := m
	forced.modes.mdPreview = true
	forcedOut := forced.renderDiff()

	assert.Equal(t, off, forcedOut,
		"renderDiff must fall back to the normal diff render when the file is not previewable, even if mdPreview "+
			"is true — the early-return branch requires BOTH conditions together")
	assert.NotContains(t, off, "mdpreviewmermaidplaceholder", "mode-off output must show no markdown-preview internals")
}

// --- Review fixes: mouse read-only guard, reload staleness, toggle scroll/off ---

// mdPreviewMouseLines builds a 40-line markdown fixture with two headings (so
// ParseTOC yields two TOC entries) and otherwise plain context lines. The two
// headings give clickTree / TOC-wheel a real, non-current line to (try to)
// jump the source cursor onto, so the read-only guard is exercised instead of
// vacuously passing on a single-entry TOC.
func mdPreviewMouseLines() []diff.DiffLine {
	lines := make([]diff.DiffLine, 40)
	for i := range lines {
		lines[i] = diff.DiffLine{NewNum: i + 1, Content: "text", ChangeType: diff.ChangeContext}
	}
	lines[0] = diff.DiffLine{NewNum: 1, Content: "# Heading One", ChangeType: diff.ChangeContext}
	lines[20] = diff.DiffLine{NewNum: 21, Content: "## Heading Two", ChangeType: diff.ChangeContext}
	return lines
}

// mdPreviewMouseModel builds a single-file markdown Model with a visible TOC
// pane and concrete layout geometry so hitTest routes clicks/wheels to the
// right zone (x<38 -> TOC, x>=38 -> diff), matching mouseTestModel's setup.
func mdPreviewMouseModel(t *testing.T, lines []diff.DiffLine) Model {
	t.Helper()
	m := testModel([]string{"plan.md"}, map[string][]diff.DiffLine{"plan.md": lines})
	m.file.name = "plan.md"
	m.file.lines = lines
	m.file.singleFile = true
	m.file.markdownPreviewable = true // full-context markdown, the way loaders.go sets it
	m.file.mdTOC = sidepane.ParseTOC(lines, "plan.md")
	require.NotNil(t, m.file.mdTOC, "fixture sanity: markdown lines with headings must produce a TOC")
	m.layout.focus = paneDiff
	m.layout.viewport.Width = 80
	m.layout.viewport.Height = 30
	return m
}

func TestHandleMouse_MdPreviewOn_ClickInDiffDoesNotMoveCursor(t *testing.T) {
	// preview is read-only: a left-click in the diff pane computes a diff-line
	// index from a preview-render row and (without the guard) reassigns the
	// source cursor. clickDiff must be inert while previewing.
	m := mdPreviewMouseModel(t, mdPreviewMouseLines())
	m.nav.diffCursor = 20
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)
	m.layout.viewport.SetYOffset(0) // deterministic click math regardless of toggle-time scroll

	// y=12, diffTopRow=2, YOffset=0 -> row 10; without the guard clickDiff would
	// move the cursor to diff line 10.
	result, _ := m.Update(leftPressAt(60, 12))
	model := result.(Model)

	assert.Equal(t, 20, model.nav.diffCursor,
		"a click in the diff pane must not move the source cursor while previewing")
}

func TestHandleMouse_MdPreviewOn_ClickInTOCDoesNotMoveCursor(t *testing.T) {
	// clicking a TOC entry routes clickTree -> syncDiffToTOCCursor, which
	// reassigns the source cursor and scrolls via diff-line math. The keyboard
	// TOC keys (n/N/p) are swallowed in preview; the click must be inert too.
	m := mdPreviewMouseModel(t, mdPreviewMouseLines())
	m.nav.diffCursor = 10
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	// x=5 -> TOC pane, y=2, treeTopRow=1 -> visible row 1 = "Heading Two" (line 20).
	result, _ := m.Update(leftPressAt(5, 2))
	model := result.(Model)

	assert.Equal(t, 10, model.nav.diffCursor,
		"clicking a TOC entry must not move the source cursor while previewing")
}

func TestHandleMouse_MdPreviewOn_WheelOverTOCDoesNotMoveCursor(t *testing.T) {
	// wheeling over the TOC pane routes handleWheel's hitTree branch ->
	// syncDiffToTOCCursor, same cursor reassignment as clickTree. Must be inert
	// in preview, matching the swallowed n/N/p keys.
	m := mdPreviewMouseModel(t, mdPreviewMouseLines())
	m.nav.diffCursor = 10
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	result, _ := m.Update(wheelMsg(tea.MouseButtonWheelDown, 5, 3, false))
	model := result.(Model)

	assert.Equal(t, 10, model.nav.diffCursor,
		"wheeling over the TOC must not move the source cursor while previewing")
}

func TestHandleMouse_MdPreviewOn_WheelOverDiffScrollsButCursorUnchanged(t *testing.T) {
	// positive control: wheel over the diff pane must still scroll the preview
	// (viewport YOffset), exactly like the J key, without pinning/mutating the
	// source cursor (pinDiffCursorTo is guarded). Not a bug reproduction —
	// guards against regressing the allowed scroll path.
	lines := make([]diff.DiffLine, 100)
	lines[0] = diff.DiffLine{NewNum: 1, Content: "# Heading", ChangeType: diff.ChangeContext}
	for i := 1; i < len(lines); i++ {
		lines[i] = diff.DiffLine{NewNum: i + 1, Content: fmt.Sprintf("- item %d", i), ChangeType: diff.ChangeContext}
	}
	m := mdPreviewMouseModel(t, lines)
	m.nav.diffCursor = 90
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)
	m.layout.viewport.SetContent(m.renderDiff())
	m.layout.viewport.SetYOffset(0)

	model := updateWheelAndFlush(t, m, wheelMsg(tea.MouseButtonWheelDown, 60, 10, false))

	assert.Positive(t, model.layout.viewport.YOffset, "wheel over the diff must still scroll the preview viewport")
	assert.Equal(t, 90, model.nav.diffCursor,
		"scrolling the preview viewport must not reassign the source cursor")
}

func TestRenderMarkdownPreview_ReflectsChangedLinesUnderSameFileAndWidth(t *testing.T) {
	// reload staleness (the R keep-open loop): the same file re-loaded at the
	// same width used to be served from a render cache keyed on file+width only,
	// returning the pre-edit render. With the cache gone, renderMarkdownPreview
	// re-renders from the current lines every time.
	m := mdPreviewTestModel(mdLines("# One\n\noriginal body text"))
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	first := xansi.Strip(m.renderMarkdownPreview())
	require.Contains(t, first, "original body text", "fixture sanity: the initial render must contain the initial content")

	// simulate an R reload: same file name, same width, new content.
	m.file.lines = mdLines("# One\n\ncompletely different body")

	second := xansi.Strip(m.renderMarkdownPreview())
	assert.Contains(t, second, "completely different body",
		"a reload under the same file+width must re-render the new content, not a stale cached render")
	assert.NotContains(t, second, "original body text",
		"the pre-reload content must not persist after the lines change")
}

func TestToggleMarkdownPreview_On_ResetsViewportToTop(t *testing.T) {
	// toggle-on scroll (bug): toggleMarkdownPreview used to call
	// syncViewportToCursor, whose YOffset math walks m.file.lines in diff-line
	// coordinates — meaningless against the glamour render. Opening preview with
	// a scrolled cursor on a long file jumped to an arbitrary offset. Entering
	// preview must reset the viewport to the top instead.
	lines := make([]diff.DiffLine, 200)
	lines[0] = diff.DiffLine{NewNum: 1, Content: "# Title", ChangeType: diff.ChangeContext}
	for i := 1; i < len(lines); i++ {
		lines[i] = diff.DiffLine{NewNum: i + 1, Content: fmt.Sprintf("line %d", i), ChangeType: diff.ChangeContext}
	}
	m := mdPreviewTestModel(lines)
	m.nav.diffCursor = 150
	m.syncViewportToCursor() // scroll the normal diff so the starting YOffset is non-zero
	require.Positive(t, m.layout.viewport.YOffset,
		"fixture sanity: a deep cursor on a long file must scroll the diff off the top before entering preview")

	on := pressKey(t, m, "P")
	require.True(t, on.modes.mdPreview)

	assert.Equal(t, 0, on.layout.viewport.YOffset,
		"entering preview must reset the viewport to the top, not to a diff-line-derived offset")
}

func TestToggleMarkdownPreview_TurnsOffEvenWhenTOCNil(t *testing.T) {
	// the ON gate (mdTOC != nil) must not block the OFF transition. Not
	// reachable today (preview only in single-file markdown mode) but a latent
	// trap: a file switch that clears mdTOC while preview is on would otherwise
	// strand the mode with no exit key.
	m := mdPreviewTestModel(mdLines("# Title\n\nSome text."))
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	m.file.markdownPreviewable = false // simulate the file becoming ineligible while preview is on
	m.file.mdTOC = nil

	off := pressKey(t, m, "P")
	assert.False(t, off.modes.mdPreview, "P must turn preview OFF even when the file is no longer previewable")
}

// --- Review phase 4: layout-change scroll + status/TOC display during preview ---

// mdPreviewListLines builds a markdown fixture with a heading (so mdTOC is
// non-nil), a blank separator, then n list items. List items render one glamour
// row each and, being short, never word-wrap — so the rendered line count is
// stable across the viewport widths these scroll tests toggle between, keeping
// the YOffset assertions deterministic (a paragraph of plain context lines would
// be joined and reflowed by glamour, and its line count would change with width).
func mdPreviewListLines(n int) []diff.DiffLine {
	lines := make([]diff.DiffLine, n+2)
	lines[0] = diff.DiffLine{NewNum: 1, Content: "# Title", ChangeType: diff.ChangeContext}
	lines[1] = diff.DiffLine{NewNum: 2, Content: "", ChangeType: diff.ChangeContext}
	for i := range n {
		lines[i+2] = diff.DiffLine{NewNum: i + 3, Content: fmt.Sprintf("- item %d", i), ChangeType: diff.ChangeContext}
	}
	return lines
}

func TestSyncViewportToCursor_MdPreviewOn_TreeToggle_PreservesScroll(t *testing.T) {
	// BUG 1: toggle_tree is an allowed action in preview; toggleTreePane ->
	// syncViewportToCursor. Without the mdPreview guard inside
	// syncViewportToCursor, the YOffset-repositioning switch snaps the scroll to
	// the (frozen, stale) diff cursor's row instead of leaving the user where
	// they scrolled the glamour render to.
	m := mdPreviewTestModel(mdPreviewListLines(200))
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	// user scrolls the rendered document well down, cursor stays frozen near the
	// top (nav is blocked in preview). the diff-cursor row (~3) is far from the
	// scrolled-to offset, so a cursor-follow reposition would be plainly visible.
	m.nav.diffCursor = 3
	m.layout.viewport.SetYOffset(80)
	require.Equal(t, 80, m.layout.viewport.YOffset, "fixture sanity: 80 must be a valid offset on this content")

	m.toggleTreePane() // allowed in preview -> syncViewportToCursor

	assert.Equal(t, 80, m.layout.viewport.YOffset,
		"toggling the tree pane in preview must preserve the scroll position, not snap it to the frozen diff cursor")
}

func TestSyncViewportToCursor_MdPreviewOn_ResizeShrink_ClampsWithoutBlankScreen(t *testing.T) {
	// BUG 1, clamp branch: a resize that grows the viewport height (or otherwise
	// shrinks the content below the current YOffset) must re-clamp YOffset to the
	// new maximum. Pre-fix, with a deep frozen cursor neither switch case fires,
	// so YOffset is left past the end -> a mostly blank screen. The guard's
	// SetYOffset(YOffset) clamp fixes it.
	m := mdPreviewTestModel(mdPreviewListLines(60))
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	m.nav.diffCursor = 58            // deep frozen cursor: neither switch case fires pre-fix
	m.layout.viewport.SetYOffset(40) // valid while height is 20 (maxYOffset ~42)
	require.Equal(t, 40, m.layout.viewport.YOffset, "fixture sanity: 40 must be valid at the pre-resize height")

	// resize to a tall terminal at the same diff width (120*3/10 tree -> diff 80,
	// matching mdPreviewTestModel's viewport.Width, so the render stays ~62 lines):
	// the taller viewport drops maxYOffset to 0, so 40 is now past the end.
	var model Model
	require.NotPanics(t, func() {
		result, _ := m.handleResize(tea.WindowSizeMsg{Width: 120, Height: 100})
		model = result.(Model)
	})

	maxOffset := max(0, model.layout.viewport.TotalLineCount()-model.layout.viewport.Height)
	assert.LessOrEqual(t, model.layout.viewport.YOffset, maxOffset,
		"YOffset must be clamped within the re-wrapped content, never left past the end (blank screen)")
	assert.Equal(t, 0, model.layout.viewport.YOffset,
		"a viewport taller than the content must clamp the scroll to the top")
}

func TestStatusBar_MdPreviewOn_SuppressesHunkAndLineSegments(t *testing.T) {
	// BUG 2: hunkSegment / lineNumberSegment are derived from the frozen
	// m.nav.diffCursor and were gated only on focus != paneDiff (which stays
	// paneDiff in preview). They kept printing a fake live "hunk X/Y" / "L:N/M"
	// while the user scrolled the glamour render. They must be suppressed; the
	// filename, the mode-icon row (incl. the ▤ preview icon) and the help hint
	// stay.
	lines := []diff.DiffLine{
		{NewNum: 1, Content: "# Title", ChangeType: diff.ChangeContext},
		{NewNum: 2, Content: "", ChangeType: diff.ChangeContext},
		{OldNum: 0, NewNum: 3, Content: "added line", ChangeType: diff.ChangeAdd},
		{NewNum: 4, Content: "context", ChangeType: diff.ChangeContext},
	}
	m := mdPreviewTestModel(lines)
	m.layout.width = 200 // wide enough that no narrow-terminal degradation drops segments
	m.nav.diffCursor = 2 // on the added line: a real hunk + line number position exists
	require.Equal(t, paneDiff, m.layout.focus)

	// sanity: with preview OFF these fake trackers are exactly what shows.
	before := m.statusBarText()
	require.Contains(t, before, "hunk 1/1", "fixture sanity: mode-off must show the hunk position")
	require.Contains(t, before, "L:3/", "fixture sanity: mode-off must show the line number position")

	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	status := m.statusBarText()
	assert.NotContains(t, status, "hunk", "hunk position must be suppressed while previewing (frozen cursor)")
	assert.NotContains(t, status, "L:", "line-number position must be suppressed while previewing (frozen cursor)")
	assert.Contains(t, status, "plan.md", "the filename must still show while previewing")
	assert.Contains(t, status, "▤", "the preview mode icon must still show while previewing")
	assert.Contains(t, status, "? help", "the help hint must still show while previewing")
}

// --- Horizontal panning in preview (scroll_left / scroll_right) ---
//
// Preview renders the whole document at the pane width, but mermaid art is
// never re-wrapped to fit (see renderMarkdownDocument), so wide diagrams run
// past the right edge. Panning is the only way to read them. These tests pin
// the cut itself (offset, indicators, ANSI safety), the clamp (which comes
// from the widest *rendered* row, not from diff lines), the offset resets, and
// the two dispatch traps: scroll_right must not switch panes while previewing,
// and the normal diff path must keep its own unclamped behavior.

// mdPreviewWideDoc renders to art 99 cells wide — wider than the 80-column
// viewport mdPreviewTestModel sets up. "alpha-start-node" sits at the left
// edge and "omega-far-right-node" at columns 76..97, so it is cut mid-word at
// offset 0 and only fully readable once panned.
//
// Every label is a single hyphenated word ON PURPOSE. This fixture is about
// panning art that is wider than the pane, and art that is wider than the pane
// is exactly what the node-label wrap pass (mdpreview_wrap.go) tries to narrow
// — it declines a label with no space to break on, so the art stays as wide as
// this fixture needs whatever the pane width is.
const mdPreviewWideDoc = "# Title\n\n" +
	"```mermaid\n" +
	"graph LR\n" +
	"    A[\"alpha-start-node\"] --> B[\"beta-middle-node\"]\n" +
	"    B --> C[\"gamma-later-node\"]\n" +
	"    C --> D[\"omega-far-right-node\"]\n" +
	"```\n\nclosing prose\n"

func TestApplyMdPreviewScroll_OffsetZero_ContentThatFits_ByteIdentical(t *testing.T) {
	// the "renders as today" guarantee: with no pan and nothing wider than the
	// pane, the cut must be a pass-through, not a re-encoded copy.
	m := mdPreviewTestModel(mdLines("# Title\n\nsome short prose\n"))
	rendered := renderMarkdownDocument(m.file.lines, m.layout.viewport.Width, m.cfg.noColors)
	require.LessOrEqual(t, mdPreviewMaxLineWidth(rendered), m.mdPreviewCutWidth(),
		"fixture sanity: this document must fit the pane, otherwise the test proves nothing")

	assert.Equal(t, rendered, m.applyMdPreviewScroll(rendered),
		"offset 0 on a document that fits must return the render untouched")
}

func TestApplyMdPreviewScroll_OffsetZero_RightIndicatorOnlyOnOverflowingLines(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title"))
	rendered := "short line\n" + strings.Repeat("x", 200)

	got := strings.Split(m.applyMdPreviewScroll(rendered), "\n")

	require.Len(t, got, 2)
	assert.Equal(t, "short line", got[0], "a row that fits must not gain an indicator")
	assert.Equal(t, strings.Repeat("x", 78)+" »", got[1],
		"an overflowing row must be cut to the pane width with the right indicator in the last two columns")
	assert.Equal(t, m.mdPreviewCutWidth(), xansi.StringWidth(got[1]),
		"the indicator must fit inside the pane, not extend past it")
}

func TestApplyMdPreviewScroll_PositiveOffset_CutsAndShowsLeftIndicator(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title"))
	m.layout.scrollX = 40
	// exactly 120 cells: at offset 40 the visible window [40,120) reaches the end
	// of the line, so only the left indicator is due.
	rendered := strings.Repeat("a", 40) + strings.Repeat("b", 40) + strings.Repeat("c", 40)

	got := m.applyMdPreviewScroll(rendered)

	assert.Equal(t, "«"+strings.Repeat("b", 39)+strings.Repeat("c", 40), got,
		"the left indicator replaces the first visible column, the rest is the cut at the offset")
	assert.Equal(t, m.mdPreviewCutWidth(), xansi.StringWidth(got))
}

func TestApplyMdPreviewScroll_PositiveOffset_BothIndicatorsWhenOverflowingBothWays(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title"))
	m.layout.scrollX = 40
	rendered := strings.Repeat("z", 200)

	got := m.applyMdPreviewScroll(rendered)

	assert.Equal(t, "«"+strings.Repeat("z", 77)+" »", got,
		"content hidden on both sides must show both indicators inside the pane width")
	assert.Equal(t, m.mdPreviewCutWidth(), xansi.StringWidth(got))
}

func TestApplyMdPreviewScroll_LineNarrowerThanOffset_RendersBlank(t *testing.T) {
	// prose is already wrapped to the pane by glamour, so panning past its end
	// is the common case, not an edge case: every prose row must go blank rather
	// than leak a stray SGR remnant (ansi.Cut alone returns the escape sequences
	// it walked past, e.g. "\x1b[32m\x1b[0m").
	m := mdPreviewTestModel(mdLines("# Title"))
	m.layout.scrollX = 40
	rendered := "\x1b[32mtiny\x1b[0m\n" + strings.Repeat("w", 200)

	got := strings.Split(m.applyMdPreviewScroll(rendered), "\n")

	require.Len(t, got, 2)
	assert.Empty(t, got[0], "a row that ends before the offset must render as an empty string")
	assert.Equal(t, m.mdPreviewCutWidth(), xansi.StringWidth(got[1]), "fixture sanity: the wide row still renders")
}

func TestApplyMdPreviewScroll_OffsetPastWidestLine_ClampsToLastColumn(t *testing.T) {
	m := mdPreviewTestModel(mdLines("# Title"))
	m.layout.scrollX = 5000 // far past anything in the render
	rendered := "head\n" + strings.Repeat("y", 100) + "TAIL"

	got := strings.Split(m.applyMdPreviewScroll(rendered), "\n")

	require.Len(t, got, 2)
	assert.True(t, strings.HasSuffix(got[1], "TAIL"),
		"an offset past the widest row must clamp so the row's last column stays visible, got %q", got[1])
	assert.Equal(t, m.mdPreviewCutWidth(), xansi.StringWidth(got[1]))
}

func TestApplyMdPreviewScroll_StyledLine_KeepsSGRAcrossTheCut(t *testing.T) {
	// glamour output is styled, so the cut must be ANSI-aware: a byte or rune
	// slice would drop the opening SGR and paint the rest of the row unstyled.
	m := mdPreviewTestModel(mdLines("# Title"))
	m.layout.scrollX = 40
	rendered := "\x1b[31m" + strings.Repeat("r", 200) + "\x1b[0m"

	got := m.applyMdPreviewScroll(rendered)

	assert.Contains(t, got, "\x1b[31m", "the active foreground must be carried across the left cut")
	assert.Equal(t, m.mdPreviewCutWidth(), xansi.StringWidth(got),
		"the escape sequences must not count towards the visible width")
}

func TestApplyMdPreviewScroll_NoColors_PannedRenderStaysANSIFree(t *testing.T) {
	// --no-colors promises the preview emits zero ANSI (mdPreviewStyleNoColor +
	// the Ascii profile). The shared indicator helpers fall back to reverse video
	// ("\x1b[7m") in no-colors mode, which would break that promise, so the
	// preview draws plain glyphs instead.
	m := mdPreviewTestModel(mdLines(mdPreviewWideDoc))
	m.cfg.noColors = true
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)
	m.layout.scrollX = 12

	got := m.renderMarkdownPreview()

	assert.NotContains(t, got, "\x1b[", "a panned no-colors preview must still emit no ANSI escape sequences")
	assert.Contains(t, got, "«", "the left indicator must still be drawn, as a plain glyph")
}

func TestPanMarkdownPreview_ClampsAtWidestRenderedLine(t *testing.T) {
	// the clamp must come from the rendered document, not from m.file.lines:
	// the source line "    C --> D[...]" is ~40 cells, the art it renders to is
	// 99. A diff-line-derived bound would stop the pan less than half way.
	m := mdPreviewTestModel(mdLines(mdPreviewWideDoc))
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	rendered := renderMarkdownDocument(m.file.lines, m.layout.viewport.Width, m.cfg.noColors)
	maxOffset := mdPreviewMaxLineWidth(rendered) - m.mdPreviewCutWidth()
	require.Positive(t, maxOffset, "fixture sanity: the art must be wider than the pane")

	for range 50 { // far more steps than the clamp allows
		m.panMarkdownPreview(1)
	}

	assert.Equal(t, maxOffset, m.layout.scrollX,
		"panning right must stop when the widest rendered row's last column is visible")
}

func TestPanMarkdownPreview_LeftClampsAtZero(t *testing.T) {
	m := mdPreviewTestModel(mdLines(mdPreviewWideDoc))
	m.toggleMarkdownPreview()

	m.panMarkdownPreview(1)
	require.Equal(t, scrollStep, m.layout.scrollX, "one step right must move by exactly one scroll step")

	for range 10 {
		m.panMarkdownPreview(-1)
	}

	assert.Equal(t, 0, m.layout.scrollX, "panning left must stop at the document's left edge")
}

func TestPanMarkdownPreview_RevealsArtPastThePaneEdge(t *testing.T) {
	// end-to-end: the point of the whole feature. The right-hand box is
	// unreachable at offset 0 and readable once panned.
	m := mdPreviewTestModel(mdLines(mdPreviewWideDoc))
	m.cfg.noColors = true
	m.toggleMarkdownPreview()

	atZero := m.renderMarkdownPreview()
	require.Contains(t, atZero, "alpha-start-node", "fixture sanity: the left-hand box is visible at offset 0")
	require.NotContains(t, atZero, "omega-far-right-node", "fixture sanity: the right-hand box must start off-pane")

	for range 50 {
		m.panMarkdownPreview(1)
	}
	panned := m.renderMarkdownPreview()

	assert.Contains(t, panned, "omega-far-right-node", "panning right must bring the far box into view")
	assert.NotContains(t, panned, "alpha-start-node", "the left-hand box must have scrolled off the left edge")
}

func TestDispatchAction_MdPreviewOn_ArrowKeysPanWithoutTouchingCursorOrStore(t *testing.T) {
	// scroll_left / scroll_right are allowed in preview because they move
	// m.layout.scrollX only. This pins that claim: neither the diff cursor nor
	// the annotation store may change.
	m := mdPreviewTestModel(mdLines(mdPreviewWideDoc))
	m.nav.diffCursor = 3
	seeded := annotation.Annotation{File: "plan.md", Line: 4, Type: string(diff.ChangeContext), Comment: "existing"}
	m.store.Add(seeded)
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	right := pressKey(t, m, "right")
	assert.Equal(t, scrollStep, right.layout.scrollX, "right must pan the preview by one scroll step")
	assert.Equal(t, 3, right.nav.diffCursor, "panning must not move the source-line cursor")
	require.Equal(t, 1, right.store.Count(), "panning must not touch the annotation store")
	assert.Equal(t, seeded, right.store.Get("plan.md")[0])

	left := pressKey(t, right, "left")
	assert.Equal(t, 0, left.layout.scrollX, "left must pan back")
	assert.Equal(t, 3, left.nav.diffCursor)
}

func TestDispatchAction_MdPreviewOn_ScrollRightDoesNotSwitchPanes(t *testing.T) {
	// THE TRAP: scroll_right doubles as the focus-diff action in both pane
	// handlers ("case keymap.ActionFocusDiff, keymap.ActionScrollRight:" in
	// diffnav.go). Allowlisting it without routing it to the preview pan first
	// would move focus (and, on the file-tree branch, kick off a file load)
	// instead of panning.
	m := mdPreviewTestModel(mdLines(mdPreviewWideDoc))
	m.layout.focus = paneTree // user focused the TOC before pressing P
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)
	require.NotNil(t, m.file.mdTOC, "fixture sanity: the TOC pane must exist for the focus branch to be reachable")

	model := pressKey(t, m, "right")

	assert.Equal(t, paneTree, model.layout.focus, "scroll_right must pan the preview, never switch panes")
	assert.Equal(t, scrollStep, model.layout.scrollX, "scroll_right must still pan while the TOC pane has focus")
}

func TestToggleMarkdownPreview_ResetsHorizontalOffset(t *testing.T) {
	// a pan is preview-local: entering must start at the document's left edge
	// even if the diff was scrolled right, and leaving must not carry a
	// preview offset (clamped against rendered rows) into the diff render.
	m := mdPreviewTestModel(mdLines(mdPreviewWideDoc))
	m.layout.scrollX = 24 // the diff pane was panned right before P

	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)
	assert.Equal(t, 0, m.layout.scrollX, "entering preview must reset the horizontal offset")

	m.panMarkdownPreview(1)
	require.Positive(t, m.layout.scrollX, "fixture sanity: pan before leaving preview")

	m.toggleMarkdownPreview()
	require.False(t, m.modes.mdPreview)
	assert.Equal(t, 0, m.layout.scrollX, "leaving preview must reset the horizontal offset")
}

func TestHandleFileLoaded_ResetsHorizontalOffsetForPreview(t *testing.T) {
	// handleFileLoaded already resets scrollX for the diff path; this pins that
	// the preview inherits it, so a pan cannot survive into another file.
	lines := mdLines(mdPreviewWideDoc)
	m := testModel([]string{"notes.md"}, map[string][]diff.DiffLine{"notes.md": lines})
	m.file.singleFile = true
	m.layout.scrollX = 32

	result, _ := m.handleFileLoaded(fileLoadedMsg{file: "notes.md", lines: lines, seq: m.file.loadSeq})
	model := result.(Model)

	assert.Equal(t, 0, model.layout.scrollX, "loading a file must reset the horizontal offset")
}

func TestHandleHorizontalScroll_PreviewOff_StaysUnclamped(t *testing.T) {
	// the normal diff path must behave exactly as before: its offset is not
	// bounded by any content width (applyHorizontalScroll just cuts past the
	// end), so the preview clamp must not leak into it.
	lines := []diff.DiffLine{{NewNum: 1, Content: "short", ChangeType: diff.ChangeContext}}
	m := testModel([]string{"a.go"}, map[string][]diff.DiffLine{"a.go": lines})
	m.file.name = "a.go"
	m.file.lines = lines
	m.layout.focus = paneDiff
	m.layout.viewport.Width = 80
	require.False(t, m.modes.mdPreview)

	for range 10 {
		m.handleHorizontalScroll(1)
	}

	assert.Equal(t, 10*scrollStep, m.layout.scrollX,
		"the diff path keeps growing its offset past the content width, as it always has")
}

func TestView_MdPreviewPanned_PaneGeometryIntact(t *testing.T) {
	// the geometry risk the cut has to respect: the » glyph must stay inside the
	// viewport width. If a panned row were one column too wide, lipgloss would
	// soft-wrap it inside the pane, adding rows and pushing every diff row past
	// applyScrollbar's hardcoded first-viewport-row offset (see view.go). Assert
	// the full frame: no line wider than the terminal, and the same number of
	// rows as the un-panned frame.
	m := mdPreviewTestModel(mdLines(mdPreviewWideDoc))
	m.toggleMarkdownPreview()
	require.True(t, m.modes.mdPreview)

	before := strings.Split(m.View(), "\n")

	for range 3 {
		m.panMarkdownPreview(1)
	}
	require.Positive(t, m.layout.scrollX, "fixture sanity: the art must be pannable")
	after := strings.Split(m.View(), "\n")

	assert.Len(t, after, len(before), "panning must not change the frame's row count (no soft-wrapped row)")
	for i, line := range after {
		assert.LessOrEqual(t, xansi.StringWidth(line), m.layout.width,
			"row %d of the panned frame must fit the terminal width", i)
	}
}
