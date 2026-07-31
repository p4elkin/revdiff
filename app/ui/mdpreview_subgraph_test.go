package ui

import (
	"testing"

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
	assert.Equal(t, []string{"        B1[Read doc] --> B2[Write doc]"}, blocks[0].body,
		"a body line keeps its original indentation")

	assert.Equal(t, "After", blocks[1].title)
	assert.Equal(t, []string{"        A1[Read doc] --> A2[Write doc]"}, blocks[1].body)
}

func TestSplitFlowchartSubgraphs_TitleSpellings(t *testing.T) {
	tests := []struct {
		name, first, want string
	}{
		{"bracketed quoted label", `subgraph one["First Block"]`, "First Block"},
		{"bracketed bare label", "subgraph one [First Block]", "First Block"},
		{"rounded label", "subgraph one (First Block)", "First Block"},
		{"no label at all", "subgraph one", "one"},
		{"empty label falls back to the id", "subgraph one[]", "one"},
		{"punctuation-only label", `subgraph one["***"]`, "***"},
		{"bare keyword", "subgraph", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := "flowchart TD\n    " + tt.first + "\n        A1 --> A2\n    end\n" +
				"    subgraph two[Second]\n        B1 --> B2\n    end"

			_, blocks, ok := splitFlowchartSubgraphs(source)

			require.True(t, ok)
			require.Len(t, blocks, 2)
			assert.Equal(t, tt.want, blocks[0].title)
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
