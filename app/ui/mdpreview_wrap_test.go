package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"
	xansi "github.com/charmbracelet/x/ansi"
)

func TestMermaidWrapLabelText(t *testing.T) {
	tests := []struct {
		name  string
		label string
		width int
		want  string
	}{
		{
			name:  "a label wider than the target breaks on spaces",
			label: "Resolve schema for the next path segment",
			width: 16,
			want:  "Resolve schema<br/>for the next<br/>path segment",
		},
		{
			name:  "a label that already fits is returned unchanged",
			label: "short label",
			width: 16,
			want:  "short label",
		},
		{
			name:  "a label exactly at the target is returned unchanged",
			label: "0123456789abcdef",
			width: 16,
			want:  "0123456789abcdef",
		},
		{
			name:  "one rune past the target wraps",
			label: "0123456789abcde fg",
			width: 16,
			want:  "0123456789abcde<br/>fg",
		},
		{
			name:  "an author's own <br/> is never touched",
			label: "Resolve schema for<br/>the next path segment",
			width: 16,
			want:  "Resolve schema for<br/>the next path segment",
		},
		{
			name:  "an author's own <br> is never touched either",
			label: "Resolve schema for<br>the next path segment",
			width: 16,
			want:  "Resolve schema for<br>the next path segment",
		},
		{
			name:  "an author's own <BR /> is never touched either",
			label: "Resolve schema for<BR />the next path segment",
			width: 16,
			want:  "Resolve schema for<BR />the next path segment",
		},
		{
			name:  `an author's own literal \n is never touched either`,
			label: `Resolve schema for\nthe next path segment`,
			width: 16,
			want:  `Resolve schema for\nthe next path segment`,
		},
		{
			name:  "a single over-long word stays intact on its own line",
			label: "supercalifragilisticexpialidocious",
			width: 16,
			want:  "supercalifragilisticexpialidocious",
		},
		{
			name:  "an over-long word keeps its own line and the rest wraps around it",
			label: "call supercalifragilisticexpialidocious now",
			width: 16,
			want:  "call<br/>supercalifragilisticexpialidocious<br/>now",
		},
		{
			name:  "an empty label is returned unchanged",
			label: "",
			width: 16,
			want:  "",
		},
		{
			name:  "punctuation travels with the word it is attached to",
			label: "Allow, then stop — not this method's concern",
			width: 20,
			want:  "Allow, then stop —<br/>not this method's<br/>concern",
		},
		{
			name:  "surrounding quotes are kept outside the wrap",
			label: `"Resolve schema for the next path segment"`,
			width: 16,
			want:  `"Resolve schema<br/>for the next<br/>path segment"`,
		},
		{
			name:  "a non-positive width wraps nothing",
			label: "Resolve schema for the next path segment",
			width: 0,
			want:  "Resolve schema for the next path segment",
		},
		{
			name:  "no-break spaces are not break opportunities",
			label: mermaidNBSPSubstitute("index or '-': item add/remove/reorder"),
			width: 16,
			want:  mermaidNBSPSubstitute("index or '-': item add/remove/reorder"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mermaidWrapLabelText(tt.label, tt.width))
		})
	}
}

func TestMermaidWrapLabelText_Idempotent(t *testing.T) {
	// A second pass must be a no-op: the first pass's own `<br/>` reads as an
	// author break on the way back in, which is what keeps the wrap from
	// re-breaking already-broken lines.
	once := mermaidWrapLabelText("Resolve schema for the next path segment", 16)
	assert.Equal(t, once, mermaidWrapLabelText(once, 16))
}

func TestMermaidWrapNodeLabels(t *testing.T) {
	tests := []struct {
		name   string
		source string
		width  int
		want   string
	}{
		{
			name:   "a node label wider than the target wraps",
			source: "flowchart TD\n    A[Resolve schema for the next path segment] --> B",
			width:  16,
			want:   "flowchart TD\n    A[Resolve schema<br/>for the next<br/>path segment] --> B",
		},
		{
			name:   "an edge label is never wrapped",
			source: "flowchart TD\n    A -->|" + mermaidNBSPSubstitute("this edge label is far wider than the target") + "| B",
			width:  16,
			want:   "flowchart TD\n    A -->|" + mermaidNBSPSubstitute("this edge label is far wider than the target") + "| B",
		},
		{
			name:   "a plain-space edge label is never wrapped either",
			source: "flowchart TD\n    A -->|this edge label is far wider than the target| B",
			width:  16,
			want:   "flowchart TD\n    A -->|this edge label is far wider than the target| B",
		},
		{
			name:   "both nodes on a line wrap, the edge label between them does not",
			source: "flowchart TD\n    A[one two three four] -->|" + mermaidNBSPSubstitute("yes it does") + "| B[five six seven eight]",
			width:  10,
			want:   "flowchart TD\n    A[one two<br/>three four] -->|" + mermaidNBSPSubstitute("yes it does") + "| B[five six<br/>seven<br/>eight]",
		},
		{
			name:   "a subgraph header is left alone",
			source: "flowchart TD\n    subgraph S [a long subgraph title here]\n    A[one two three four]\n    end",
			width:  10,
			want:   "flowchart TD\n    subgraph S [a long subgraph title here]\n    A[one two<br/>three four]\n    end",
		},
		{
			name:   "a comment line is left alone",
			source: "flowchart TD\n    %% a long comment that is wider than the target\n    A[one two three four]",
			width:  10,
			want:   "flowchart TD\n    %% a long comment that is wider than the target\n    A[one two<br/>three four]",
		},
		{
			name:   "a trailing inline comment is left alone",
			source: "flowchart TD\n    A[one two three four] %% a long trailing comment here",
			width:  10,
			want:   "flowchart TD\n    A[one two<br/>three four] %% a long trailing comment here",
		},
		{
			name:   "a source that is not a flowchart is untouched",
			source: "sequenceDiagram\n    Alice->>Bob: a message far wider than the target width",
			width:  10,
			want:   "sequenceDiagram\n    Alice->>Bob: a message far wider than the target width",
		},
		{
			name:   "a label carrying a nested bracket wraps around it",
			source: "flowchart TD\n    A[reads arr[i] from the buffer] --> B",
			width:  12,
			want:   "flowchart TD\n    A[reads arr[i]<br/>from the<br/>buffer] --> B",
		},
		{
			name:   "blank lines survive",
			source: "flowchart TD\n\n    A[one two three four]\n",
			width:  10,
			want:   "flowchart TD\n\n    A[one two<br/>three four]\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mermaidWrapNodeLabels(tt.source, tt.width))
		})
	}
}

func TestMermaidWrapNodeLabels_NonPositiveWidthChangesNothing(t *testing.T) {
	source := "flowchart TD\n    A[one two three four] --> B"
	assert.Equal(t, source, mermaidWrapNodeLabels(source, 0))
}

func TestMermaidWrapBracketedLabel_UnbracketedSegmentIsUntouched(t *testing.T) {
	// Defensive: flowchartShapeAt always returns `[...]`, so this cannot happen
	// today — but slicing a segment that is not bracketed would corrupt it, and
	// the alternative to the check is that corruption reaching the reader.
	for _, segment := range []string{"", "x", "[unclosed", "unopened]"} {
		assert.Equal(t, segment, mermaidWrapBracketedLabel(segment, 4))
	}
}

func TestMermaidNarrowIfOverflowing_UnflippableSourceAfterARetryChangeIsDeclined(t *testing.T) {
	// art differs from the first render, so the LR flip must be what produced
	// it — but this source has no header the flip can be reproduced from, so
	// there is no source to wrap and the art stands.
	source := "  \n  \n"
	r := &mermaidWrapTestRender{art: strings.Repeat("y", 10)}

	assert.Equal(t, "zz", mermaidNarrowIfOverflowing(source, "x", "zz", 1, r.render))
	assert.Empty(t, r.calls)
}

func TestMermaidWrapNodeLabels_Idempotent(t *testing.T) {
	source := normalizeFlowchartSource(readMermaidFixture(t, "collision-three-branches.mmd"))
	once := mermaidWrapNodeLabels(source, 24)
	require.NotEqual(t, source, once, "fixture sanity: the fixture must have labels wide enough to wrap")
	assert.Equal(t, once, mermaidWrapNodeLabels(once, 24))
}

func TestMermaidNodeLabelWrapWidth(t *testing.T) {
	// The derivation is a fraction of the pane clamped into a band; see the
	// constant's doc comment for the measurements behind each number.
	assert.Equal(t, mermaidWrapMinRunes, mermaidNodeLabelWrapWidth(20, mermaidWrapPaneShare),
		"a very narrow pane clamps up to the floor")
	assert.Equal(t, mermaidWrapMaxRunes, mermaidNodeLabelWrapWidth(400, mermaidWrapPaneShare),
		"a very wide pane clamps down to the ceiling")
	assert.Equal(t, mermaidWrapMinRunes, mermaidNodeLabelWrapWidth(mermaidUnconstrainedWidth, mermaidWrapPaneShare),
		"the unconstrained sentinel never reaches this function in production, but must not overflow")
	assert.Equal(t, 30, mermaidNodeLabelWrapWidth(120, mermaidWrapPaneShare),
		"a pane inside the band is divided by the share")
}

func TestMermaidWrapTargets(t *testing.T) {
	tests := []struct {
		name      string
		paneWidth int
		want      []int
	}{
		{"a wide pane ladders from the ceiling down", 160, []int{mermaidWrapMaxRunes, 20}},
		{"a normal pane ladders from a quarter down to the floor", 120, []int{30, mermaidWrapMinRunes}},
		{"a narrow pane has one rung, both having clamped to the floor", 60, []int{mermaidWrapMinRunes}},
		{"the unconstrained sentinel is one floor rung", mermaidUnconstrainedWidth, []int{mermaidWrapMinRunes}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mermaidWrapTargets(tt.paneWidth))
		})
	}
}

// mermaidWrapTestRender is the injected renderer for the gate tests: it records
// every source it was handed so a test can assert the gate did NOT render.
type mermaidWrapTestRender struct {
	calls []string
	art   string
	err   error
	panic bool
}

func (r *mermaidWrapTestRender) render(source string) (string, error) {
	r.calls = append(r.calls, source)
	if r.panic {
		panic("boom")
	}
	return r.art, r.err
}

func TestMermaidNarrowIfOverflowing_FittingArtIsNeverRerendered(t *testing.T) {
	// The gate that keeps the blast radius down: art that fits the pane takes
	// the same path it took before this pass existed, with no second render.
	source := "flowchart TD\n    A[one two three four five six] --> B"
	art := "one two three four\nfive six"
	r := &mermaidWrapTestRender{art: "narrower"}

	got := mermaidNarrowIfOverflowing(source, art, art, 80, r.render)

	assert.Equal(t, art, got)
	assert.Empty(t, r.calls, "a fitting render must not be re-rendered at all")
}

func TestMermaidNarrowIfOverflowing_KeepsAStrictlyNarrowerRender(t *testing.T) {
	source := "flowchart TD\n    A[one two three four five six seven eight] --> B"
	art := strings.Repeat("x", 40)
	r := &mermaidWrapTestRender{art: strings.Repeat("y", 20)}

	got := mermaidNarrowIfOverflowing(source, art, art, 30, r.render)

	assert.Equal(t, r.art, got)
	require.Len(t, r.calls, 1)
	assert.Contains(t, r.calls[0], "<br/>", "the re-render must be handed the wrapped source")
}

func TestMermaidNarrowIfOverflowing_DeclinesAWiderOrEqualRender(t *testing.T) {
	source := "flowchart TD\n    A[one two three four five six seven eight] --> B"
	art := strings.Repeat("x", 40)

	for _, tt := range []struct {
		name string
		wide int
	}{
		{"wider", 60},
		{"exactly as wide", 40},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := &mermaidWrapTestRender{art: strings.Repeat("y", tt.wide)}
			assert.Equal(t, art, mermaidNarrowIfOverflowing(source, art, art, 30, r.render))
		})
	}
}

func TestMermaidNarrowIfOverflowing_DeclinesARenderThatGainsACollision(t *testing.T) {
	// Wrapping packs boxes closer together, so a narrower render CAN cram two
	// edge labels into one corridor that were readable before. The gate must
	// measure that rather than assume it away.
	source := "flowchart TD\n" +
		"    A[one two three four five six seven eight] -->|" + mermaidNBSPSubstitute("single composite") + "| B\n" +
		"    A -->|collection| C"
	clean := strings.Repeat("x", 40) + "\n" +
		mermaidNBSPSubstitute("single composite") + strings.Repeat(" ", 20) + "collection"
	crammed := strings.Repeat("y", 20) + "\n" +
		mermaidNBSPSubstitute("single composite") + "  collection"

	r := &mermaidWrapTestRender{art: crammed}
	assert.Equal(t, clean, mermaidNarrowIfOverflowing(source, clean, clean, 30, r.render),
		"a narrower render that gains a collision must be declined")

	// Same crammed art on both sides: the wrap did not make it worse, so being
	// narrower is enough to keep it.
	r2 := &mermaidWrapTestRender{art: crammed}
	assert.Equal(t, crammed, mermaidNarrowIfOverflowing(source, clean+"\n"+crammed, clean+"\n"+crammed, 30, r2.render),
		"a collision that was already there must not block the narrowing")
}

func TestMermaidNarrowIfOverflowing_DeclinesOnRenderFailure(t *testing.T) {
	source := "flowchart TD\n    A[one two three four five six seven eight] --> B"
	art := strings.Repeat("x", 40)

	t.Run("error", func(t *testing.T) {
		r := &mermaidWrapTestRender{err: assert.AnError}
		assert.Equal(t, art, mermaidNarrowIfOverflowing(source, art, art, 30, r.render))
	})
	t.Run("blank", func(t *testing.T) {
		r := &mermaidWrapTestRender{art: "   \n  "}
		assert.Equal(t, art, mermaidNarrowIfOverflowing(source, art, art, 30, r.render))
	})
	t.Run("panic", func(t *testing.T) {
		r := &mermaidWrapTestRender{panic: true}
		assert.Equal(t, art, mermaidNarrowIfOverflowing(source, art, art, 30, r.render))
	})
}

func TestMermaidNarrowIfOverflowing_WrapsTheDirectionThatWonTheRetry(t *testing.T) {
	// The LR retry runs first, so the source to narrow is whichever direction
	// its art came from — flipping again would undo a correctness fix with a
	// readability one.
	source := "flowchart TD\n    A[one two three four five six seven eight] --> B"
	first := strings.Repeat("x", 40)
	retried := strings.Repeat("z", 50)
	r := &mermaidWrapTestRender{art: strings.Repeat("y", 20)}

	got := mermaidNarrowIfOverflowing(source, first, retried, 30, r.render)

	assert.Equal(t, r.art, got)
	require.Len(t, r.calls, 1)
	assert.Contains(t, r.calls[0], "flowchart LR",
		"the wrapped source must carry the flipped direction the retry kept")
}

func TestMermaidNarrowIfOverflowing_NonFlowchartSourceIsNeverWrapped(t *testing.T) {
	source := "sequenceDiagram\n    Alice->>Bob: a message far wider than any pane"
	art := strings.Repeat("x", 40)
	r := &mermaidWrapTestRender{art: strings.Repeat("y", 10)}

	assert.Equal(t, art, mermaidNarrowIfOverflowing(source, art, art, 30, r.render))
	assert.Empty(t, r.calls)
}

func TestMermaidNarrowIfOverflowing_UnconstrainedPaneNeverWraps(t *testing.T) {
	source := "flowchart TD\n    A[one two three four five six seven eight] --> B"
	art := strings.Repeat("x", 400)
	r := &mermaidWrapTestRender{art: strings.Repeat("y", 20)}

	assert.Equal(t, art, mermaidNarrowIfOverflowing(source, art, art, mermaidUnconstrainedWidth, r.render))
	assert.Empty(t, r.calls, "with no pane constraint there is nothing to narrow to")
}

// TestRenderMermaidSource_FittingFenceIsByteIdenticalToTheUnwrappedRender is the
// blast-radius pin. A fence whose art already fits the pane must reach exactly
// the bytes the pre-wrap build produced — which for a fence the LR retry also
// leaves alone is the raw vendored render of the normalized source.
func TestRenderMermaidSource_FittingFenceIsByteIdenticalToTheUnwrappedRender(t *testing.T) {
	source := "flowchart TD\n" +
		"    A[Resolve schema for the next path segment] --> B[Allow, not this concern]\n"

	got, err := renderMermaidSource(source, 160)
	require.NoError(t, err)

	want, err := mermaidcmd.RenderDiagram(normalizeFlowchartSource(source), nil)
	require.NoError(t, err)

	require.LessOrEqual(t, mermaidArtWidth(want), 160, "fixture sanity: this render must fit the pane")
	assert.Equal(t, want, got, "a fence that fits the pane must not be re-rendered wrapped")
	assert.NotContains(t, got, "<br/>")
}

// TestRenderMarkdownDocument_NarrowPane_WrappedArtIsSplicedVerbatim pins the
// whole path end to end at a narrow pane: the wrap narrows the art, and glamour
// still splices those exact bytes into the document rather than reflowing them.
func TestRenderMarkdownDocument_NarrowPane_WrappedArtIsSplicedVerbatim(t *testing.T) {
	const pane = 40
	source := "graph TD\n" +
		"    A[This is a moderately long label for node A] --> B[This is a moderately long label for node B]"

	unwrapped, err := mermaidcmd.RenderDiagram(normalizeFlowchartSource(source), nil)
	require.NoError(t, err)
	require.Greater(t, mermaidArtWidth(unwrapped), pane, "fixture sanity: the unwrapped art must overflow the pane")

	art, err := renderMermaidSource(source, pane)
	require.NoError(t, err)
	assert.Less(t, mermaidArtWidth(art), mermaidArtWidth(unwrapped), "the wrap must narrow the art")
	assert.Contains(t, art, "node A", "no label text may be lost to the wrap")
	assert.Contains(t, art, "node B")

	doc := "prose\n\n```mermaid\n" + source + "\n```\n"
	stripped := xansi.Strip(renderMarkdownDocument(mdLines(doc), pane, false))
	assert.Contains(t, stripped, art, "the wrapped art must reach the document byte-exact")
}

// TestRenderMarkdownDocument_CollisionFixture_WrapsUnderThePane is the
// end-to-end measurement: the fixture's post-retry LR render is 281 columns at a
// 160-column pane, and the wrap must bring it under the pane without gaining a
// collision.
func TestRenderMarkdownDocument_CollisionFixture_WrapsUnderThePane(t *testing.T) {
	source := readMermaidFixture(t, "collision-three-branches.mmd")

	art, err := renderMermaidSource(source, 160)
	require.NoError(t, err)

	assert.LessOrEqual(t, mermaidArtWidth(art), 160,
		"the wrapped render must fit a 160-column pane\nart:\n%s", art)

	// The collision count has to be measured against the source that actually
	// produced this art — the LR flip the retry kept, wrapped at whichever rung
	// of the ladder won. Finding that rung by matching the art is what keeps
	// this assertion honest if the ladder is retuned later.
	flipped, ok := mermaidFlipDirectionToLR(normalizeFlowchartSource(source))
	require.True(t, ok)
	winner := ""
	for _, target := range mermaidWrapTargets(160) {
		wrapped := mermaidWrapNodeLabels(flipped, target)
		if rendered, renderErr := mermaidcmd.RenderDiagram(wrapped, nil); renderErr == nil && rendered == art {
			winner = wrapped
			break
		}
	}
	require.NotEmpty(t, winner, "the art must be the render of one of the ladder's rungs\nart:\n%s", art)
	assert.Equal(t, 0, mermaidCollisionCount(winner, art),
		"the wrapped render must still count zero collisions\nart:\n%s", art)

	// And the whole document path must agree with the direct render.
	doc := "```mermaid\n" + source + "\n```\n"
	stripped := xansi.Strip(renderMarkdownDocument(mdLines(doc), 160, false))
	assert.Contains(t, stripped, "collection", "the edge labels must survive the wrap")
}
