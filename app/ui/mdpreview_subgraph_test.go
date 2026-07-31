package ui

import (
	"regexp"
	"strings"
	"testing"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"
	"github.com/mattn/go-runewidth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// splittableFence is the shape the whole split exists for: a before/after
// diagram whose two blocks share no node id at all. Every refusal table below
// is a one-line change away from this, so a test that fails says which rule
// did it rather than which fence was mistyped.
const splittableFence = `flowchart TD
    subgraph before["Before"]
        B1[Read doc] --> B2[Write doc]
    end
    subgraph after["After"]
        A1[Read doc] --> A2[Write doc]
    end`

func TestSplitFlowchartSubgraphs_SplitsTwoDisjointBlocks(t *testing.T) {
	header, blocks, ok := splitFlowchartSubgraphs(splittableFence)

	require.True(t, ok, "two disjoint top-level subgraphs must be splittable")
	assert.Equal(t, "flowchart TD", header)
	require.Len(t, blocks, 2)

	assert.Equal(t, "Before", blocks[0].title, "blocks come back in source order")
	assert.Equal(t, "before", blocks[0].id, "a block keeps its own id — rule 5 counts it as a node id")
	assert.Equal(t, []string{"        B1[Read doc] --> B2[Write doc]"}, blocks[0].body,
		"a body line keeps its original indentation")

	assert.Equal(t, "After", blocks[1].title)
	assert.Equal(t, "after", blocks[1].id)
	assert.Equal(t, []string{"        A1[Read doc] --> A2[Write doc]"}, blocks[1].body)
}

// TestSplitFlowchartSubgraphs_GraphKeyword pins the OTHER diagram kind rule 1
// accepts. Every other positive test writes `flowchart`, so without this one
// dropping "graph" from the switch would silently stop splitting every
// `graph TD` fence and nothing would fail.
func TestSplitFlowchartSubgraphs_GraphKeyword(t *testing.T) {
	source := strings.Replace(splittableFence, "flowchart TD", "graph TD", 1)

	header, blocks, ok := splitFlowchartSubgraphs(source)

	require.True(t, ok, "`graph` has subgraphs exactly like `flowchart` and splits the same way")
	assert.Equal(t, "graph TD", header)
	require.Len(t, blocks, 2)

	art, err := renderMermaidSource(source, mermaidUnconstrainedWidth)
	require.NoError(t, err)
	assert.Contains(t, art, "Before")
	assert.Contains(t, art, "After")
}

func TestSplitFlowchartSubgraphs_TitleSpellings(t *testing.T) {
	tests := []struct {
		name, first, wantID, want string
	}{
		{"bracketed quoted label", `subgraph one["First Block"]`, "one", "First Block"},
		{"bracketed bare label", "subgraph one [First Block]", "one", "First Block"},
		{"rounded label", "subgraph one (First Block)", "one", "First Block"},
		{"no label at all", "subgraph one", "one", "one"},
		{"empty label falls back to the id", "subgraph one[]", "one", "one"},
		{"punctuation-only label", `subgraph one["***"]`, "one", "***"},
		{"bare keyword", "subgraph", "", ""},
		{"html line break becomes a space", `subgraph one["Read<br/>then write"]`, "one", "Read then write"},
		{"html line break, self-closing variant", `subgraph one["Read<br>then write"]`, "one", "Read then write"},
		{"a control byte is dropped from the heading", "subgraph one[\"A\x1b[31mRED\"]", "one", "A[31mRED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := "flowchart TD\n    " + tt.first + "\n        A1 --> A2\n    end\n" +
				"    subgraph two[Second]\n        B1 --> B2\n    end"

			_, blocks, ok := splitFlowchartSubgraphs(source)

			require.True(t, ok)
			require.Len(t, blocks, 2)
			assert.Equal(t, tt.want, blocks[0].title)
			assert.Equal(t, tt.wantID, blocks[0].id)
		})
	}
}

func TestSplitFlowchartSubgraphs_Refusals(t *testing.T) {
	tests := []struct {
		name, source string
	}{
		{"rule 1: not a flowchart", `classDiagram
    subgraph before
        B1 --> B2
    end
    subgraph after
        A1 --> A2
    end`},
		{"rule 2: a single subgraph", `flowchart TD
    subgraph only["Only"]
        B1 --> B2
    end`},
		{"rule 2: no subgraph at all", "flowchart TD\n    A --> B"},
		{"rule 3: nested subgraphs", `flowchart TD
    subgraph outer["Outer"]
        subgraph inner["Inner"]
            B1 --> B2
        end
    end
    subgraph after["After"]
        A1 --> A2
    end`},
		{"rule 4: a node declared outside every subgraph", `flowchart TD
    Z[outside]
    subgraph before["Before"]
        B1 --> B2
    end
    subgraph after["After"]
        A1 --> A2
    end`},
		{"rule 4: an edge written outside every subgraph", `flowchart TD
    subgraph before["Before"]
        B1 --> B2
    end
    subgraph after["After"]
        A1 --> A2
    end
    B2 --> A1`},
		{"rule 5: an edge crossing two subgraphs", `flowchart TD
    subgraph before["Before"]
        B1 --> A1
    end
    subgraph after["After"]
        A1 --> A2
    end`},
		{"rule 5: a node id shared by two subgraphs", `flowchart TD
    subgraph before["Before"]
        B1[Read doc] --> Shared[Store]
    end
    subgraph after["After"]
        A1[Read doc] --> Shared[Store]
    end`},
		// A subgraph id is a node id: `A --> groupB` is valid, documented
		// mermaid. Both directions must refuse, since the block an edge names
		// may be declared either after or before the edge naming it.
		{"rule 5: an edge pointing at a later subgraph by id", `flowchart TD
    subgraph before["Before"]
        B1 --> after
    end
    subgraph after["After"]
        A1 --> A2
    end`},
		{"rule 5: an edge pointing at an earlier subgraph by id", `flowchart TD
    subgraph before["Before"]
        B1 --> B2
    end
    subgraph after["After"]
        before --> A2
    end`},
		// rule 5 must see a node id whatever script it is written in. Missing
		// one is not a missed split but a wrong picture: the crossing edge is
		// dropped and the shared node drawn twice.
		{"rule 5: a shared node id in a non-Latin script", `flowchart TD
    subgraph before["Before"]
        Начало --> B2
    end
    subgraph after["After"]
        Начало --> A2
    end`},
		{"rule 5: an edge crossing two subgraphs in a non-Latin script", `flowchart TD
    subgraph before["Before"]
        Альфа --> Бета
    end
    subgraph after["After"]
        Бета --> Гамма
    end`},
		{"an unterminated subgraph", `flowchart TD
    subgraph before["Before"]
        B1 --> B2
    subgraph after["After"]
        A1 --> A2
    end`},
		{"a stray end", `flowchart TD
    subgraph before["Before"]
        B1 --> B2
    end
    end
    subgraph after["After"]
        A1 --> A2
    end`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header, blocks, ok := splitFlowchartSubgraphs(tt.source)

			assert.False(t, ok, "this fence must keep today's single-render behavior")
			assert.Empty(t, header, "a refusal returns nothing for the caller to use by mistake")
			assert.Nil(t, blocks)
		})
	}
}

// TestSplitFlowchartSubgraphs_LayoutDirectivesAreNotNodes pins the two places
// a `direction` statement would otherwise break the split: outside a subgraph
// it must not read as a node declaration (rule 4), and inside two different
// subgraphs it must not read as the same shared ids (rule 5).
func TestSplitFlowchartSubgraphs_LayoutDirectivesAreNotNodes(t *testing.T) {
	source := `flowchart TD
    direction LR
    subgraph before["Before"]
        direction TB
        B1 --> B2
    end
    subgraph after["After"]
        direction TB
        A1 --> A2
    end`

	_, blocks, ok := splitFlowchartSubgraphs(source)

	require.True(t, ok, "a direction statement is neither a node nor a reason to refuse")
	require.Len(t, blocks, 2)
}

// TestSplitFlowchartSubgraphs_CommentsAndBlanks pins that a fence whose only
// out-of-block content is comments and blank lines still splits, and that a
// comment line never becomes a block's body line.
func TestSplitFlowchartSubgraphs_CommentsAndBlanks(t *testing.T) {
	source := `flowchart TD

%% two independent blocks

    subgraph before["Before"]
        B1 --> B2
    end

    subgraph after["After"]
        A1 --> A2
    end`

	_, blocks, ok := splitFlowchartSubgraphs(source)

	require.True(t, ok)
	require.Len(t, blocks, 2)
	assert.Equal(t, []string{"        B1 --> B2"}, blocks[0].body)
	assert.Equal(t, []string{"        A1 --> A2"}, blocks[1].body)
}

// TestSplitFlowchartSubgraphs_KeywordLookalikes pins that a node whose id
// merely starts with "subgraph" or "end" is a node statement, not block
// structure — the same trap mermaidKeywordRest exists for, reached from a
// different direction.
func TestSplitFlowchartSubgraphs_KeywordLookalikes(t *testing.T) {
	source := `flowchart TD
    subgraph before["Before"]
        subgraphOne --> ending
    end
    subgraph after["After"]
        A1 --> A2
    end`

	_, blocks, ok := splitFlowchartSubgraphs(source)

	require.True(t, ok, "subgraphOne and ending are node ids, not structure")
	require.Len(t, blocks, 2)
	assert.Equal(t, []string{"        subgraphOne --> ending"}, blocks[0].body)
}

// TestSplitFlowchartSubgraphs_SharedStyleClassStillSplits pins that a
// `:::className` suffix is a style tag, not a node id. Two blocks tagging their
// nodes with the same class share no node, so rule 5 must let them through —
// `classDef` appears in 24% of the corpus fences, so this shape is common
// enough that reading the class name as an id would cost real splits.
func TestSplitFlowchartSubgraphs_SharedStyleClassStillSplits(t *testing.T) {
	source := `flowchart TD
    subgraph before["Before"]
        B1[old]:::hot --> B2
    end
    subgraph after["After"]
        A1[new]:::hot --> A2
    end`

	_, blocks, ok := splitFlowchartSubgraphs(source)

	require.True(t, ok, "a shared style class is not a shared node")
	require.Len(t, blocks, 2)
}

// TestSplitFlowchartSubgraphs_NonLatinIDsStillSplit is the other half of the
// non-ASCII pin: widening what counts as an id must not make every non-Latin
// fence refuse. These two blocks share nothing, so they split.
func TestSplitFlowchartSubgraphs_NonLatinIDsStillSplit(t *testing.T) {
	source := `flowchart TD
    subgraph before["Было"]
        Начало --> Конец
    end
    subgraph after["Стало"]
        Старт --> Финиш
    end`

	_, blocks, ok := splitFlowchartSubgraphs(source)

	require.True(t, ok, "two disjoint blocks split whatever script their ids use")
	require.Len(t, blocks, 2)
	assert.Equal(t, "Было", blocks[0].title)
}

// --- stacking ---

// stackableFence is splittableFence with every label spelled differently, so a
// test can tell one block's art from the other's. splittableFence deliberately
// repeats the same two labels in both blocks, which is what makes it useless
// for checking WHERE a label was drawn.
const stackableFence = `flowchart TD
    subgraph before["Before"]
        B1[old read] --> B2[old write]
    end
    subgraph after["After"]
        A1[new read] --> A2[new write]
    end`

func TestStackFlowchartSubgraphs_TitleRuleAndOrder(t *testing.T) {
	header, blocks, ok := splitFlowchartSubgraphs(stackableFence)
	require.True(t, ok)

	art, ok := stackFlowchartSubgraphs(header, blocks)
	require.True(t, ok)

	// mermaidcmd.RenderDiagram never ends its art in a newline, and the stacked
	// art must not either: renderMermaidBlock appends exactly one newline to
	// whichever it got and spliceMermaidArt strips exactly one back off, so one
	// extra here would print as a blank line under a split diagram and under no
	// other. Every assertion below trims the tail, which is why this one is
	// stated separately rather than left to them.
	require.NotEmpty(t, art)
	assert.False(t, strings.HasSuffix(art, "\n"), "the stacked art carries no trailing newline")

	// no block's own art contains a blank line, so one blank line is exactly
	// the separator between two blocks.
	chunks := strings.Split(strings.TrimRight(art, "\n"), "\n\n")
	require.Len(t, chunks, 2, "the two blocks must be separated by one blank line")

	want := []struct{ title, drawn, notDrawn string }{
		{"Before", "old read", "new read"},
		{"After", "new read", "old read"},
	}
	for i, tt := range want {
		lines := strings.Split(chunks[i], "\n")
		require.Greater(t, len(lines), 2)

		assert.Equal(t, tt.title, lines[0], "blocks are stacked in source order, each under its own title")
		assert.Equal(t, strings.Repeat(mermaidSubgraphRule, runewidth.StringWidth(tt.title)), lines[1],
			"the rule under a title matches the title's display width")

		body := strings.Join(lines[2:], "\n")
		assert.Contains(t, body, tt.drawn, "the block's own art sits under its own title")
		assert.NotContains(t, body, tt.notDrawn, "the other block's art must not appear here")
	}
}

// TestStackFlowchartSubgraphs_TitleRuleIsDisplayWidth pins that the rule is
// sized by display width, not rune count. Every other title in this file is
// ASCII, where the two agree, so the expected length here is hardcoded rather
// than recomputed with the helper the implementation itself uses.
func TestStackFlowchartSubgraphs_TitleRuleIsDisplayWidth(t *testing.T) {
	art, ok := stackFlowchartSubgraphs("flowchart TD", []mermaidSubgraph{
		{id: "one", title: "数据", body: []string{"B1 --> B2"}},
		{id: "two", title: "After", body: []string{"A1 --> A2"}},
	})

	require.True(t, ok)
	lines := strings.Split(art, "\n")
	require.Greater(t, len(lines), 2)
	assert.Equal(t, "数据", lines[0])
	assert.Equal(t, "────", lines[1], "two full-width runes take four columns, not two")
}

func TestStackFlowchartSubgraphs_NoBlocksRefuses(t *testing.T) {
	// splitFlowchartSubgraphs never hands back fewer than two blocks, but that
	// is an invariant of a different function. Returning ok here with empty art
	// would send renderMermaidBlock to the VERBATIM fence text, which is worse
	// than the whole-source render this is supposed to fall back to.
	art, ok := stackFlowchartSubgraphs("flowchart TD", nil)

	assert.False(t, ok, "nothing to stack must send the caller to the single-render path")
	assert.Empty(t, art)
}

func TestStackFlowchartSubgraphs_ErroringBlockRefuses(t *testing.T) {
	// the blank-result check cannot cover this: a header the vendored parser
	// rejects outright comes back as a real error, not as empty art.
	_, err := mermaidcmd.RenderDiagram("sequenceDiagram\nB1 --> B2\n", nil)
	require.Error(t, err, "guard: this header must still make the vendored renderer return an error")

	art, ok := stackFlowchartSubgraphs("sequenceDiagram", []mermaidSubgraph{
		{id: "before", title: "Before", body: []string{"B1 --> B2"}},
		{id: "after", title: "After", body: []string{"A1 --> A2"}},
	})

	assert.False(t, ok, "a render error in one block sends the whole fence back to the single render")
	assert.Empty(t, art, "nothing partial comes back")
}

func TestStackFlowchartSubgraphs_UntitledBlockGetsNoHeading(t *testing.T) {
	art, ok := stackFlowchartSubgraphs("flowchart TD", []mermaidSubgraph{
		{body: []string{"B1 --> B2"}},
		{title: "After", body: []string{"A1 --> A2"}},
	})

	require.True(t, ok, "a missing title costs a heading, not the whole split")
	assert.True(t, strings.HasPrefix(art, "┌"), "an untitled block starts straight into its art, with no empty heading")
	assert.Contains(t, art, "After\n"+mermaidSubgraphRule)
}

func TestStackFlowchartSubgraphs_BlankBlockRefuses(t *testing.T) {
	// an empty subgraph body renders as whitespace rather than erroring, which
	// is why the blank check is not redundant with the error check.
	header, blocks, ok := splitFlowchartSubgraphs(`flowchart TD
    subgraph before["Before"]
        B1 --> B2
    end
    subgraph after["After"]
    end`)
	require.True(t, ok)

	art, ok := stackFlowchartSubgraphs(header, blocks)

	assert.False(t, ok, "one blank block sends the whole fence back to the single-render path")
	assert.Empty(t, art, "nothing partial comes back")
}

func TestStackFlowchartSubgraphs_PanickingBlockRefuses(t *testing.T) {
	// a column-0 classDef with no ':' panics the vendored parser (pinned by
	// TestNormalizeFlowchartSource_ClassDefWithoutAColon_NoLongerKillsTheFence).
	// The normalizer drops that line long before the split runs, so this shape
	// can only be built by hand — which is exactly what the recover in
	// stackFlowchartSubgraphs is there for.
	const panicking = "flowchart TD\nclassDef dashed stroke-dasharray: 5 5\nA1 --> A2\n"
	require.Panics(t, func() { _, _ = mermaidcmd.RenderDiagram(panicking, nil) },
		"guard: this block's source must still PANIC the vendored renderer, not merely error or blank out")

	art, ok := stackFlowchartSubgraphs("flowchart TD", []mermaidSubgraph{
		{id: "before", title: "Before", body: []string{"B1 --> B2"}},
		{id: "after", title: "After", body: []string{"classDef dashed stroke-dasharray: 5 5", "A1 --> A2"}},
	})

	assert.False(t, ok, "a panic in one block must not escape past the fallback")
	assert.Empty(t, art)
}

// --- renderMermaidSource wiring ---

func TestRenderMermaidSource_SplitsIndependentSubgraphs(t *testing.T) {
	combined, err := mermaidcmd.RenderDiagram(normalizeFlowchartSource(stackableFence), nil)
	require.NoError(t, err)

	art, err := renderMermaidSource(stackableFence, mermaidUnconstrainedWidth)
	require.NoError(t, err)

	assert.NotEqual(t, combined, art, "a splittable fence must not take the single-render path")
	require.Contains(t, art, "Before")
	require.Contains(t, art, "After")
	assert.Less(t, strings.Index(art, "Before"), strings.Index(art, "After"),
		"both titles are present, in source order")
	for _, label := range []string{"old read", "old write", "new read", "new write"} {
		assert.Equal(t, 1, strings.Count(art, label), "each label must be drawn exactly once")
	}
	assert.False(t, strings.HasSuffix(art, "\n"),
		"a split fence ends exactly like a single-rendered one, or it gains a blank line under it")
}

// TestRenderMermaidSource_NormalizedBeforeSplit locks the order the split
// depends on. Both blocks here use the SAME inline edge label, written in a
// link form the normalizer rewrites. Fed raw source, flowchartSubgraphNodeIDs
// would read `shared` as a node id in both blocks and rule 5 would refuse; it
// only splits because normalizeFlowchartSource has already turned the link
// into `-->|shared|`, where the label is edge syntax the id walk consumes
// whole. Reorder the two and this test is what fails.
func TestRenderMermaidSource_NormalizedBeforeSplit(t *testing.T) {
	source := `flowchart TD
    subgraph before["Before"]
        B1[old read] --- B2[old write]
        B2 -. shared .-> B3[old cache]
    end
    subgraph after["After"]
        A1[new read] -. shared .-> A2[new write]
    end`

	art, err := renderMermaidSource(source, mermaidUnconstrainedWidth)
	require.NoError(t, err)

	blocks := strings.Split(art, "\n\n")
	require.Len(t, blocks, 2, "the fence must split, which it only can if normalization ran first")
	assert.Contains(t, blocks[0], "old cache")
	assert.NotContains(t, blocks[0], "new read", "an `after` node must not be drawn in the `before` block")
}

// TestRenderMermaidSource_FrontmatterFence_StillSplits pins the other pass
// that runs before the split. mermaidStripFrontmatter cuts the YAML block off
// inside transpileMermaid, so the header the split records is the post-strip
// one — `flowchart TD`, never `---`.
func TestRenderMermaidSource_FrontmatterFence_StillSplits(t *testing.T) {
	source := "---\ntitle: Before and after\n---\n" + stackableFence

	art, err := renderMermaidSource(source, mermaidUnconstrainedWidth)
	require.NoError(t, err)

	blocks := strings.Split(art, "\n\n")
	require.Len(t, blocks, 2)
	assert.NotContains(t, art, "title: Before and after", "the frontmatter must not reach the art")
	for _, label := range []string{"old read", "new write"} {
		assert.Equal(t, 1, strings.Count(art, label))
	}
}

// TestRenderMermaidSource_UnsplittableFences_KeepTodaysSingleRender is the
// no-change guarantee, pinned byte for byte: every fence the five rules refuse
// must come out of renderMermaidSource exactly as the single whole-source
// render produces it, with no heading, no rule and no restacking anywhere in
// it. Roughly half the real corpus takes this path (see the counts in
// mdpreview_subgraph.go's doc comment), so it is the common case, not a corner.
func TestRenderMermaidSource_UnsplittableFences_KeepTodaysSingleRender(t *testing.T) {
	tests := []struct {
		name, source string
	}{
		{"nested subgraphs", `flowchart TD
    subgraph outer["Outer"]
        subgraph inner["Inner"]
            B1 --> B2
        end
    end
    subgraph after["After"]
        A1 --> A2
    end`},
		{"a single subgraph", `flowchart TD
    subgraph only["Only"]
        B1 --> B2
    end`},
		{"no subgraph at all", "flowchart TD\n    A --> B"},
		{"a node declared outside every subgraph", `flowchart TD
    Z[outside]
    subgraph before["Before"]
        B1 --> B2
    end
    subgraph after["After"]
        A1 --> A2
    end`},
		{"an edge crossing two subgraphs", `flowchart TD
    subgraph before["Before"]
        B1 --> A1
    end
    subgraph after["After"]
        A1 --> A2
    end`},
		{"a node id shared by two subgraphs", `flowchart TD
    subgraph before["Before"]
        B1[old read] --> Shared[store]
    end
    subgraph after["After"]
        A1[new read] --> Shared[store]
    end`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, err := mermaidcmd.RenderDiagram(normalizeFlowchartSource(tt.source), nil)
			require.NoError(t, err)

			got, err := renderMermaidSource(tt.source, mermaidUnconstrainedWidth)
			require.NoError(t, err)

			assert.Equal(t, want, got, "a fence that may not be split must render exactly as it does today")
		})
	}
}

func TestRenderMermaidSource_BlankBlock_FallsBackToSingleRender(t *testing.T) {
	src := `flowchart TD
    subgraph before["Before"]
        B1 --> B2
    end
    subgraph after["After"]
    end`

	want, err := mermaidcmd.RenderDiagram(normalizeFlowchartSource(src), nil)
	require.NoError(t, err)

	got, err := renderMermaidSource(src, mermaidUnconstrainedWidth)
	require.NoError(t, err)

	assert.Equal(t, want, got, "a part that cannot render sends the whole fence back to the single render")
	assert.NotContains(t, got, "Before\n"+mermaidSubgraphRule, "no stacked heading may survive the fallback")
}

// --- integration: the real diagram this split was written for ---

// variantPlanFence is the before/after diagram from
// docs/plans/2026-07-31-variant-document-full-adoption.md in the
// magnolia-content-api worktree, copied verbatim. It is the fence whose broken
// combined render motivated this whole pass — see mdpreview_subgraph.go's doc
// comment — so it is pinned here rather than paraphrased: an author's real
// diagram uses quoted edge labels, em dashes and method-call text, and a
// hand-written stand-in would quietly stop exercising any of that.
const variantPlanFence = `flowchart LR
    subgraph before["Before"]
        B1["decomposeOverlay"] -->|"listVariants (segment coords)"| BL1["sharedSliceBaseline"]
        B1 -->|"listVariants (strict mode)"| BL2["strictValidationBaseline"]
        B3["createVariant"] -->|"listVariants"| BL3["keepTheUnaddressedSibling"]
    end
    subgraph after["After"]
        A1["decomposeOverlay"] -->|"loadForWrite — ONE listVariants"| AD["VariantDocument"]
        A3["createVariant"] --> AD
        AD -->|".slice(sharedTarget)"| AL1["sharedSliceBaseline"]
        AD -->|".composedExcluding(at, excluded)"| AL2["strictValidationBaseline"]
        AD -->|".slice(write.coords())"| AL3["keepTheUnaddressedSibling"]
    end`

// mermaidAdjacentBorders matches two frame characters sitting side by side with
// at most three columns between them. Measured on the real fence, the combined
// render carries this on 12 lines and the stacked art on none.
//
// Be honest about what it proves: it detects a surviving SUBGRAPH RECTANGLE,
// not an overlap. A correct side-by-side layout of two rectangles matches it
// just as readily (measured: 19 lines on the correct combined render of
// stackableFence). So "zero of these" says the stacked art has no rectangle
// left in it, only node boxes — which is the shape the split produces, but not
// on its own proof that the broken overlap is gone. repeatedLabelLines is what
// carries that half.
var mermaidAdjacentBorders = regexp.MustCompile(`[│├┤┬┴┼]\s{0,3}[│├┤┬┴┼]`)

// adjacentBorderLines counts how many of art's lines carry a subgraph
// rectangle's border pair — see mermaidAdjacentBorders.
func adjacentBorderLines(art string) int {
	n := 0
	for line := range strings.SplitSeq(art, "\n") {
		if mermaidAdjacentBorders.MatchString(line) {
			n++
		}
	}
	return n
}

// repeatedLabelLines counts how many of art's lines print label more than
// once. This IS the overlap, made measurable: in a correct render each node
// box owns its own columns, so a label can appear at most once per row. Two on
// one row means the renderer drew both subgraph rectangles over the same rows
// and put a node from each side by side — exactly what the Overview describes
// as nodes landing in the wrong rectangle.
func repeatedLabelLines(art, label string) int {
	n := 0
	for line := range strings.SplitSeq(art, "\n") {
		if strings.Count(line, label) > 1 {
			n++
		}
	}
	return n
}

func TestRenderMermaidSource_VariantPlanFence_RendersAsTwoTitledBlocks(t *testing.T) {
	art, err := renderMermaidSource(variantPlanFence, mermaidUnconstrainedWidth)
	require.NoError(t, err)

	blocks := strings.Split(strings.TrimRight(art, "\n"), "\n\n")
	require.Len(t, blocks, 2, "the real fence must come out as exactly two blocks")

	for i, want := range []string{"Before", "After"} {
		lines := strings.Split(blocks[i], "\n")
		require.Greater(t, len(lines), 2, "each block must carry art of its own, not just a heading")
		assert.Equal(t, want, lines[0], "each block sits under its own title, in source order")
		assert.Equal(t, strings.Repeat(mermaidSubgraphRule, runewidth.StringWidth(want)), lines[1])
	}

	// every node the author drew survives the split, in the block it belongs to
	// and drawn exactly once there.
	for _, label := range []string{
		"decomposeOverlay", "createVariant", "sharedSliceBaseline",
		"strictValidationBaseline", "keepTheUnaddressedSibling",
	} {
		assert.Equal(t, 1, strings.Count(blocks[0], label), "the `before` block draws %q once", label)
		assert.Equal(t, 1, strings.Count(blocks[1], label), "the `after` block draws %q once", label)
	}
	assert.NotContains(t, blocks[0], "VariantDocument", "an `after` node must not be drawn in the `before` block")
	assert.Equal(t, 1, strings.Count(blocks[1], "VariantDocument"))

	// the author wrote every edge label quoted; the renderer draws a label
	// verbatim, so a surviving quote reaches the screen (see
	// unquoteFlowchartEdgeLabel). This is the fence that bug was found in.
	assert.NotContains(t, art, `"listVariants`, "an edge label must reach the art without its quotes")
	assert.NotContains(t, art, `".slice(`)
	assert.Contains(t, art, "listVariants", "the label text itself must survive")
}

// TestRenderMermaidSource_VariantPlanFence_OverlapIsGone is the other half of
// the pin: it checks the two symptoms the Overview names, and first checks that
// the pre-split render really does show them, so neither assertion can pass
// just because the signature stopped being detectable.
func TestRenderMermaidSource_VariantPlanFence_OverlapIsGone(t *testing.T) {
	overlapped := []string{"sharedSliceBaseline", "strictValidationBaseline", "keepTheUnaddressedSibling"}

	combined, err := mermaidcmd.RenderDiagram(normalizeFlowchartSource(variantPlanFence), nil)
	require.NoError(t, err)
	require.Positive(t, adjacentBorderLines(combined),
		"guard: the single-render path must still draw subgraph rectangles at all")
	require.Positive(t, titleCollisionLines(combined),
		"guard: the single-render path must still print both subgraph titles on one row")
	for _, label := range overlapped {
		require.Positive(t, repeatedLabelLines(combined, label),
			"guard: the single-render path must still draw %q twice on one row — that is the overlap", label)
	}

	art, err := renderMermaidSource(variantPlanFence, mermaidUnconstrainedWidth)
	require.NoError(t, err)

	assert.Zero(t, adjacentBorderLines(art), "no subgraph rectangle may survive the split")
	assert.Zero(t, titleCollisionLines(art), "the two subgraph titles must no longer share a row")
	for _, label := range overlapped {
		assert.Zero(t, repeatedLabelLines(art, label),
			"no row may carry %q twice — that was the overlap the split exists to remove", label)
	}
}

// titleCollisionLines counts lines carrying BOTH block titles — the second
// broken-layout symptom from the Overview, where the two subgraph rectangles
// overlap far enough that their titles print on the same row.
func titleCollisionLines(art string) int {
	n := 0
	for line := range strings.SplitSeq(art, "\n") {
		if strings.Contains(line, "Before") && strings.Contains(line, "After") {
			n++
		}
	}
	return n
}

// TestRenderMermaidSource_AdversarialFences_NeverPanic covers the malformed
// shapes an author can actually type. None may take down the preview: the split
// must either refuse them (see splitFlowchartSubgraphs) or render them, and an
// error is an acceptable answer — a panic is not. renderMermaidBlock's own
// recover is the last net, so this pins the layer BELOW it, where a panic would
// otherwise cost the reader the whole fence's rendering and leave raw text.
func TestRenderMermaidSource_AdversarialFences_NeverPanic(t *testing.T) {
	tests := []struct {
		name, source string
	}{
		{"an unterminated subgraph", `flowchart TD
    subgraph before["Before"]
        B1 --> B2
    subgraph after["After"]
        A1 --> A2
    end`},
		{"a stray end with nothing open", `flowchart TD
    subgraph before["Before"]
        B1 --> B2
    end
    end
    subgraph after["After"]
        A1 --> A2
    end`},
		{"one empty subgraph body", `flowchart TD
    subgraph before["Before"]
        B1 --> B2
    end
    subgraph after["After"]
    end`},
		{"every subgraph body empty", "flowchart TD\n    subgraph before\n    end\n    subgraph after\n    end"},
		{"a title that is only punctuation", `flowchart TD
    subgraph before["***"]
        B1 --> B2
    end
    subgraph after["!!!"]
        A1 --> A2
    end`},
		{"a bare subgraph keyword with no id", "flowchart TD\n    subgraph\n        B1 --> B2\n    end\n" +
			"    subgraph\n        A1 --> A2\n    end"},
		{"nothing but subgraph structure", "flowchart TD\n    subgraph\n    end"},
		{"an end with no diagram at all", "end"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var art string
			var err error
			require.NotPanics(t, func() {
				art, err = renderMermaidSource(tt.source, mermaidUnconstrainedWidth)
			})
			// an error is an acceptable answer, and so is art; silently
			// returning neither is not — renderMermaidBlock reads empty art
			// with a nil error as success and would splice nothing at all.
			assert.True(t, err != nil || art != "",
				"a malformed fence must come back as art or as an error, never as blank success")
		})
	}
}

func TestFlowchartSubgraphNodeIDs(t *testing.T) {
	tests := []struct {
		name, line string
		want       []string
	}{
		{"a bare edge", "A --> B", []string{"A", "B"}},
		{"labeled nodes", "A[Read doc] --> B[Write doc]", []string{"A", "B"}},
		{"an edge label is not a node", "A -->|go now| B", []string{"A", "B"}},
		{"a node label is not a node", "A[go --> nowhere] --> B", []string{"A", "B"}},
		{"a quoted node label", `A["a (b"] --> B`, []string{"A", "B"}},
		{"a bidirectional edge", "A <--> B", []string{"A", "B"}},
		{"a chain", "A --> B --> C", []string{"A", "B", "C"}},
		{"a declaration alone", "    A[Read doc]", []string{"A"}},
		{"an inline comment is not a node", "A --> B %% see C", []string{"A", "B"}},
		{"a style-class suffix is not a node", "A:::hot --> B", []string{"A", "B"}},
		{"a style-class suffix after a label", "A[old]:::hot --> B", []string{"A", "B"}},
		{"a non-Latin id", "Начало --> Конец", []string{"Начало", "Конец"}},
		{"a non-Latin id with a label", "Начало[читать] --> B", []string{"Начало", "B"}},
		// Both of the next two are guards for a DIRECT call: normalization has
		// already removed every dropped directive, and splitFlowchartSubgraphs
		// skips comment-only lines, so neither shape reaches here in production.
		{"a directive mentions nothing", "direction TB", nil},
		{"a style directive mentions nothing", "style A fill:#f9f", nil},
		{"a comment line mentions nothing", "%% A --> B", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, flowchartSubgraphNodeIDs(tt.line))
		})
	}
}
