package ui

import (
	"strings"
	"testing"
)

// collisionThreeBranchFence is the real fence that motivated the detector,
// copied verbatim from a user document. Its `What kind of property is it?`
// decision node has three labeled out-edges, and the vendored renderer puts
// `collection` and `single composite` on the same output row butted together.
//
// The same fence is added as a testdata fixture by the end-to-end task; this
// copy is what pins the DETECTOR's own behavior, so a change to the fixture
// file cannot quietly change what this test is asserting.
const collisionThreeBranchFence = `flowchart TD
    Start["Resolve schema for the next path segment"] --> Found{"Property exists in the schema?"}
    Found -->|"no"| Allow1["Allow — not this method's concern"]
    Found -->|"yes"| Island["islandHere = already inside an island, or this property is i18n"]
    Island --> Shape{"What kind of property is it?"}
    Shape -->|"plain scalar"| NoVerdict["No verdict — the caller one level up applies the field-edit rule"]
    Shape -->|"single composite"| DescendC["Resolve the item's actual model (polymorphic), descend one level"]
    Shape -->|"collection"| Checkpoint["Go to a checkpoint — see next diagram"]
    DescendC -.->|"recurse"| Start`

// renderForCollision runs a fence through the same normalization the render
// path uses and returns both the source the detector should see and the art,
// so a test asserts against the pair production would actually hand it.
func renderForCollision(t *testing.T, fence string) (string, string) {
	t.Helper()
	toRender := fence
	if transpiled, ok := transpileMermaid(fence, 120); ok {
		toRender = transpiled
	}
	art, err := renderMermaidSource(fence, 120)
	if err != nil {
		t.Fatalf("renderMermaidSource: %v", err)
	}
	if strings.TrimSpace(art) == "" {
		t.Fatal("renderMermaidSource returned blank art")
	}
	return toRender, art
}

func TestCollisionCount_RealThreeBranchFence_Collides(t *testing.T) {
	source, art := renderForCollision(t, collisionThreeBranchFence)

	got := mermaidCollisionCount(source, art)
	if got == 0 {
		t.Fatalf("expected at least one collision on the three-branch fence, got 0\nart:\n%s", art)
	}
}

func TestCollisionCount_TableDriven(t *testing.T) {
	// Rows are hand-built art rather than rendered output so each case
	// isolates exactly one property of the counter.
	tests := []struct {
		name   string
		source string
		art    string
		want   int
	}{
		{
			name:   "two distinct labels butted together on one row",
			source: "flowchart TD\n  A -->|collection| B\n  A -->|single composite| C\n",
			art:    "├◄───collection────single composite───────┤",
			want:   1,
		},
		{
			name:   "two distinct labels far apart on one row",
			source: "flowchart TD\n  A -->|collection| B\n  A -->|single composite| C\n",
			art:    "──collection──┤        ┌──────────────┐        ├──single composite──",
			want:   0,
		},
		{
			name:   "same label twice on one row is not a collision",
			source: "flowchart TD\n  A -->|yes| B\n  A -->|no| C\n",
			art:    "──yes──yes──",
			want:   0,
		},
		{
			name:   "fewer than two distinct labels",
			source: "flowchart TD\n  A -->|yes| B\n  A --> C\n",
			art:    "──yes──yes──yes──",
			want:   0,
		},
		{
			name:   "no labels at all",
			source: "flowchart TD\n  A --> B\n  B --> C\n",
			art:    "A ──> B ──> C",
			want:   0,
		},
		{
			name:   "empty art",
			source: "flowchart TD\n  A -->|yes| B\n  A -->|no| C\n",
			art:    "",
			want:   0,
		},
		{
			name:   "blank rows only",
			source: "flowchart TD\n  A -->|yes| B\n  A -->|no| C\n",
			art:    "\n\n\n",
			want:   0,
		},
		{
			name:   "collisions on two separate rows both count",
			source: "flowchart TD\n  A -->|collection| B\n  A -->|single composite| C\n",
			art:    "──collection──single composite──\n──single composite──collection──",
			want:   2,
		},
		{
			name: "shorter label inside a longer one is not a second hit",
			// `single` is a distinct label of its own AND a prefix of
			// `single composite`. Without longest-first matching plus
			// column marking, `single` would match inside the longer
			// label and the trailing ` composite` fragment would read as
			// an adjacent distinct hit.
			source: "flowchart TD\n  A -->|single composite| B\n  A -->|single| C\n",
			art:    "──────────single composite──────────",
			want:   0,
		},
		{
			name:   "gap of exactly the threshold is not a collision",
			source: "flowchart TD\n  A -->|aa| B\n  A -->|bb| C\n",
			art:    "aa" + strings.Repeat("─", mermaidCollisionGap) + "bb",
			want:   0,
		},
		{
			name:   "gap one below the threshold is a collision",
			source: "flowchart TD\n  A -->|aa| B\n  A -->|bb| C\n",
			art:    "aa" + strings.Repeat("─", mermaidCollisionGap-1) + "bb",
			want:   1,
		},
		{
			name: "a bled space still matches the label",
			// The label carries no-break spaces after normalization but
			// the art shows the arrow line bleeding through, which is
			// exactly what the wildcard-per-space pattern is for.
			source: "flowchart TD\n  A -->|read" + mermaidNBSP + "as" + mermaidNBSP + "fallback| B\n  A -->|other| C\n",
			art:    "──read─as─fallback─other──",
			want:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mermaidCollisionCount(tt.source, tt.art); got != tt.want {
				t.Fatalf("mermaidCollisionCount() = %d, want %d\nart: %q", got, tt.want, tt.art)
			}
		})
	}
}

func TestCollisionLabels_UniqueAndLongestFirst(t *testing.T) {
	source := `flowchart TD
  A -->|"no"| B
  A -->|"single composite"| C
  A -->|yes| D
  D -->|"no"| E
  E --> F`

	got := mermaidEdgeLabels(source)
	want := []string{"single composite", "yes", "no"}
	if len(got) != len(want) {
		t.Fatalf("mermaidEdgeLabels() = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mermaidEdgeLabels() = %q, want %q", got, want)
		}
	}
}

func TestCollisionLabels_NoLabels(t *testing.T) {
	if got := mermaidEdgeLabels("flowchart TD\n  A --> B\n"); len(got) != 0 {
		t.Fatalf("mermaidEdgeLabels() = %q, want none", got)
	}
}
