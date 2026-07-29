package ui

import (
	"strings"
	"testing"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// normalizeFlowchartBody runs one body line through the real
// normalizeFlowchartSource entry point (header line included, since the header
// is deliberately never normalized) and hands back just that body line. Every
// table below goes through this rather than calling the per-line helpers
// directly, so the tests pin the behavior a fence actually gets.
func normalizeFlowchartBody(t *testing.T, line string) string {
	t.Helper()
	const header = "flowchart TD\n"
	got := normalizeFlowchartSource(header + line)
	require.True(t, strings.HasPrefix(got, header), "the header line must survive untouched")
	return strings.TrimPrefix(got, header)
}

func TestNormalizeFlowchartSource_ShapeSuffixes_BecomeSquareBrackets(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"diamond", "A --> B{decision}", "A --> B[decision]"},
		{"diamond quoted", `A --> B{"gradation flag?"}`, `A --> B["gradation flag?"]`},
		{"rounded", "A --> B(rounded)", "A --> B[rounded]"},
		{"stadium", "A --> B([stadium])", "A --> B[stadium]"},
		{"circle", "A --> B((circle))", "A --> B[circle]"},
		{"double circle", "A --> B(((circle)))", "A --> B[circle]"},
		{"subroutine", "A --> B[[subroutine]]", "A --> B[subroutine]"},
		{"cylinder", "A --> B[(database)]", "A --> B[database]"},
		{"hexagon", "A --> B{{hexagon}}", "A --> B[hexagon]"},
		{"asymmetric", "A --> B>flag]", "A --> B[flag]"},
		{"square already", "A --> B[square]", "A --> B[square]"},
		{"shape on the left operand", "A{decide} --> B", "A[decide] --> B"},
		{"shape on both operands", "A(one) --> B{two}", "A[one] --> B[two]"},
		{"nested brackets in the label", "A --> B[uses arr[i] here]", "A --> B[uses arr[i] here]"},
		{"parens inside a quoted label", `A --> B["▸ Advanced filters (collapsed)"]`, `A --> B["▸ Advanced filters (collapsed)"]`},
		{"declaration on its own line", "  B{decision}", "  B[decision]"},
		{"shape mid-chain", "A --> B{mid} --> C", "A --> B[mid] --> C"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, normalizeFlowchartBody(t, tc.input))
		})
	}
}

func TestNormalizeFlowchartSource_LinkVariants_BecomePlainArrow(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"undirected", "A --- B", "A --> B"},
		{"undirected long", "A ----- B", "A --> B"},
		{"dotted open", "A -.- B", "A --> B"},
		{"dotted arrow", "A -.-> B", "A --> B"},
		{"thick open", "A === B", "A --> B"},
		{"thick arrow", "A ==> B", "A --> B"},
		{"circle head", "A --o B", "A --> B"},
		{"cross head", "A --x B", "A --> B"},
		{"plain arrow untouched", "A --> B", "A --> B"},
		{"no spaces", "A---B", "A-->B"},
		{"labeled undirected", "A -- carries --- B", "A -->|carries| B"},
		{"labeled dotted", "A -. maybe .-> B", "A -->|maybe| B"},
		{"labeled thick", "A == always ==> B", "A -->|always| B"},
		{"labeled normal", "A -- plain --> B", "A -->|plain| B"},
		{"labeled quoted", `A -- "with spaces" --> B`, "A -->|with spaces| B"},
		{"pipe label untouched", "A -->|already| B", "A -->|already| B"},
		{"pipe label on an undirected link", "A ---|late| B", "A -->|late| B"},
		{"bidirectional kept", "A <--> B", "A <--> B"},
		{"bidirectional normalized", "A <==> B", "A <--> B"},
		{"two links on one line", "A --- B -.- C", "A --> B --> C"},
		{"link chars inside a label are not links", "A --> B[a --- b]", "A --> B[a --- b]"},
		{"link chars inside an edge label are not links", "A -->|a==b| B", "A -->|a==b| B"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, normalizeFlowchartBody(t, tc.input))
		})
	}
}

func TestNormalizeFlowchartSource_PipeInsideNodeLabel_BecomesSlash(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"quoted label", `A --> B["x=with|without"]`, `A --> B["x=with/without"]`},
		{"unquoted label", "A --> B[with|without]", "A --> B[with/without]"},
		{"shape suffix and pipe together", "A --> B{with|without}", "A --> B[with/without]"},
		{"edge-label delimiters untouched", "A -->|go| B[keep]", "A -->|go| B[keep]"},
		{
			"edge label and a pipe-carrying node label on one line",
			`A -->|go| B["x=with|without"]`,
			`A -->|go| B["x=with/without"]`,
		},
		{"two pipes in one label", "A --> B[a|b|c]", "A --> B[a/b/c]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, normalizeFlowchartBody(t, tc.input))
		})
	}
}

func TestNormalizeFlowchartSource_WellFormedConstructs_NotDisturbed(t *testing.T) {
	// Everything the corpus scan found working today. Each of these renders
	// correctly through the vendored parser already, so the normalization pass
	// must hand it back byte-identical.
	tests := []struct{ name, input string }{
		{"html line break in a label", "A --> B[first<br/>second]"},
		{"html line break, self-closing variant", "A --> B[first<br>second]"},
		{"chained arrows", "A --> B --> C --> D"},
		{"chained arrows with a shape in the middle", "A --> B[mid] --> C"},
		{"full-line comment", "%% A --- B{not real syntax}"},
		{"ampersand fan-out", "A --> B & C"},
		{"node id with a dash", "my-node --> other-node"},
		{"already normalized edge label", "A -->|does a thing| B[target]"},
		{"quoted label with a colon", `A --> B["GET /api/x: y"]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.input, normalizeFlowchartBody(t, tc.input))
		})
	}
}

func TestNormalizeFlowchartSource_StructuralLines_LeftVerbatim(t *testing.T) {
	// subgraph/end and the styling+layout directives are skipped WHOLE: a
	// subgraph header's `Id [Label]` is not a node shape, and a `style`/
	// `classDef` line's punctuation is not link syntax. Their contents are
	// still normalized — the block structure is what stays put.
	src := "flowchart LR\n" +
		"    subgraph Constructor [StudySetup]\n" +
		"      direction TB\n" +
		"      ADV[\"advanced\"]\n" +
		"      GR{gradation}\n" +
		"      ADV --- GR\n" +
		"    end\n" +
		"    classDef hot fill:#f9f,stroke:#333\n" +
		"    style Constructor fill:#eee\n" +
		"    click ADV \"https://example.com\"\n"

	want := "flowchart LR\n" +
		"    subgraph Constructor [StudySetup]\n" +
		"      direction TB\n" +
		"      ADV[\"advanced\"]\n" +
		"      GR[gradation]\n" +
		"      ADV --> GR\n" +
		"    end\n" +
		"    classDef hot fill:#f9f,stroke:#333\n" +
		"    style Constructor fill:#eee\n" +
		"    click ADV \"https://example.com\"\n"

	assert.Equal(t, want, normalizeFlowchartSource(src))
}

func TestNormalizeFlowchartSource_NodeNamedAfterAStructuralKeyword_StillNormalized(t *testing.T) {
	// The skip list is matched with the shared whole-word/shape discriminator
	// (mermaidDirective), not a bare prefix test, so a node genuinely NAMED
	// "style" or "end" is still a node statement and still gets normalized.
	assert.Equal(t, "end --> A[x]", normalizeFlowchartBody(t, "end --> A{x}"))
	assert.Equal(t, "style --> A[x]", normalizeFlowchartBody(t, "style --- A{x}"))
	assert.Equal(t, "endpoint --> A[x]", normalizeFlowchartBody(t, "endpoint --- A{x}"))
}

func TestNormalizeFlowchartSource_InlineComment_TailLeftAlone(t *testing.T) {
	// The vendored parser cuts a line at its first %% before parsing, so the
	// tail is dead text. Normalizing it would rewrite the author's prose for
	// no gain; the code BEFORE the marker is still normalized.
	assert.Equal(t, "A --> B[x] %% keep A --- B{this} as written",
		normalizeFlowchartBody(t, "A --- B{x} %% keep A --- B{this} as written"))
}

func TestNormalizeFlowchartSource_HeaderAndBlankLines_Preserved(t *testing.T) {
	src := "\n%% leading comment\ngraph TD\n\n    A --- B{x}\n"
	want := "\n%% leading comment\ngraph TD\n\n    A --> B[x]\n"
	assert.Equal(t, want, normalizeFlowchartSource(src))
}

func TestNormalizeFlowchartSource_Idempotent(t *testing.T) {
	// Running the pass over its own output must be a no-op: production calls it
	// once per render, but a fence that was already hand-written in normalized
	// form goes down the exact same path, so "already normalized" and "just
	// normalized" have to be the same fixed point.
	src := "flowchart LR\n" +
		"    A{decide} -- yes --> B([done])\n" +
		"    A -.- C[(store|cache)]\n" +
		"    subgraph S [Group]\n      D --- E\n    end\n"
	once := normalizeFlowchartSource(src)
	assert.Equal(t, once, normalizeFlowchartSource(once))
}

func TestNormalizeFlowchartSource_UnbalancedLabel_LeftAlone(t *testing.T) {
	// A label whose bracket never closes (a multi-line label, or plain
	// mistyped source) has no shape to rewrite. Bailing must leave the line
	// completely intact rather than emitting half a rewrite.
	assert.Equal(t, "A --> B[never closes", normalizeFlowchartBody(t, "A --> B[never closes"))
	assert.Equal(t, "A --> B{never closes", normalizeFlowchartBody(t, "A --> B{never closes"))
}

// --- against the real vendored renderer ---

func TestNormalizeFlowchartSource_RealCorpusFence_RendersWithoutStrayBoxes(t *testing.T) {
	// The verbatim `flowchart LR` fence from the plan document that prompted
	// this pass. Before normalization the vendored renderer drew three wrong
	// things, all asserted gone below: a stray `HAS{"gradation flag?"}` box
	// beside the real HAS node, an `ADV --- GR` box (the undirected link
	// parsed as one node named after the whole line), and a
	// `without,transitivity"]` box with the rest of that line's raw text
	// spilled into the middle of the art.
	src := "flowchart LR\n" +
		"    subgraph Constructor [StudySetup]\n" +
		"      SRC[Source: All/Weak/Due/Type/Random]\n" +
		"      TAG[Tags]\n" +
		"      LVL[Level A1..C2]\n" +
		"      ADV[\"▸ Advanced filters (collapsed)\"]\n" +
		"      GR[\"Gradation: Any / With / Without\"]\n" +
		"      TR[Transitivity: T/I/Both]\n" +
		"      ADV --- GR\n" +
		"      ADV --- TR\n" +
		"    end\n" +
		"    Constructor -->|buildStudyQuery| Q[\"GET /api/review/session<br/>source,size,tags,pos,levels,<br/>" +
		"gradation=with|without,transitivity\"]\n" +
		"    Q --> SEL[\"ReviewService.selectCards<br/>in-memory filter over candidates\"]\n" +
		"    SEL --> HAS{\"gradation flag?\"}\n" +
		"    HAS -->|with| KEEPW[\"keep terms where gradationPattern is real\"]\n" +
		"    HAS -->|without| KEEPO[\"keep terms where gradationPattern is null-ish\"]\n" +
		"    HAS -->|any| NOOP[\"no gradation filter\"]\n" +
		"    SEL --> CARDS[cards + count] --> Constructor\n"

	before, err := mermaidcmd.RenderDiagram(src, nil)
	require.NoError(t, err)
	require.Contains(t, before, `HAS{"gradation flag?"}`, "fixture sanity: the raw fence must still show the stray diamond box")
	require.Contains(t, before, "ADV --- GR", "fixture sanity: the raw fence must still show the undirected link as a box")
	require.Contains(t, before, `without,transitivity"]`, "fixture sanity: the raw fence must still show the split pipe label")

	after, err := renderMermaidSource(src, mermaidUnconstrainedWidth)
	require.NoError(t, err)

	assert.NotContains(t, after, `HAS{"gradation flag?"}`, "the diamond must be a normal node, not a stray box")
	assert.NotContains(t, after, "ADV --- GR", "the undirected link must be an edge, not a box")
	assert.NotContains(t, after, "ADV --- TR", "the undirected link must be an edge, not a box")
	assert.NotContains(t, after, `without,transitivity"]`, "the pipe-carrying label must stay one intact box")
	assert.NotContains(t, after, "buildStudyQuery|", "no raw edge-label text may spill into the art")

	assert.Contains(t, after, "gradation flag?", "the diamond's text must survive as a real box label")
	assert.Contains(t, after, "gradation=with/without,transitivity", "the pipe in the label must render as a slash")
}

func TestNormalizeFlowchartSource_DiamondNode_RendersAsOneBox(t *testing.T) {
	// The minimal shape of the corpus's most common breakage: one shape
	// suffix used to produce TWO boxes, `B` and `B{decision}`, with the edge
	// attached to the wrong one.
	src := "flowchart LR\n    A --> B{decision}\n    B --> C\n"

	before, err := mermaidcmd.RenderDiagram(src, nil)
	require.NoError(t, err)
	require.Contains(t, before, "B{decision}", "fixture sanity: the raw fence must still draw the stray box")

	after, err := renderMermaidSource(src, mermaidUnconstrainedWidth)
	require.NoError(t, err)
	assert.NotContains(t, after, "B{decision}")
	assert.Contains(t, after, "decision")
	assert.Equal(t, 1, strings.Count(after, "decision"), "the label must be drawn exactly once")
}
