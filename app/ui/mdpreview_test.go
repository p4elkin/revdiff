package ui

import (
	"strings"
	"testing"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/diff"
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
