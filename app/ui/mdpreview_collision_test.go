package ui

import (
	"errors"
	"strings"
	"testing"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"
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
// path uses and returns both the source the detector should see and the
// FIRST render's art — deliberately bypassing renderMermaidSource's LR retry
// (added in task 5), so this stays a pin of the detector's own behavior on
// an unretried render. TestRenderMermaidSource_RealThreeBranchFence_CollisionResolvedByRetry
// is what asserts the full pipeline, retry included, resolves this fence.
func renderForCollision(t *testing.T, fence string) (string, string) {
	t.Helper()
	toRender := fence
	if transpiled, ok := transpileMermaid(fence, 120); ok {
		toRender = transpiled
	}
	art, err := mermaidcmd.RenderDiagram(toRender, nil)
	if err != nil {
		t.Fatalf("mermaidcmd.RenderDiagram: %v", err)
	}
	if strings.TrimSpace(art) == "" {
		t.Fatal("RenderDiagram returned blank art")
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

// collidingSource and collidingArt are the same fence/art pair used in the
// "two distinct labels butted together on one row" case above (collision
// count 1). Reused here so the retry-decision tests aren't proving anything
// about mermaidCollisionCount itself, only about the gate built on top of it.
const collidingSource = "flowchart TD\n  A -->|collection| B\n  A -->|single composite| C\n"

const collidingArt = "├◄───collection────single composite───────┤"

// nonCollidingArt is the "far apart" case from the table above (count 0),
// used as a flipped render that is strictly better than collidingArt.
const nonCollidingArt = "──collection──┤        ┌──────────────┐        ├──single composite──"

func TestMermaidFlipDirectionToLR(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
		wantOK bool
	}{
		{
			name:   "TD becomes LR",
			source: "flowchart TD\n  A --> B",
			want:   "flowchart LR\n  A --> B",
			wantOK: true,
		},
		{
			name:   "TB becomes LR",
			source: "graph TB\n  A --> B",
			want:   "graph LR\n  A --> B",
			wantOK: true,
		},
		{
			name:   "leading blank and comment lines are skipped to find the header",
			source: "\n%% a comment\nflowchart TD\n  A --> B",
			want:   "\n%% a comment\nflowchart LR\n  A --> B",
			wantOK: true,
		},
		{
			name:   "already LR declines — LR is the flip target, not a source direction",
			source: "flowchart LR\n  A --> B",
			wantOK: false,
		},
		{
			name:   "RL declines — not TD or TB",
			source: "flowchart RL\n  A --> B",
			wantOK: false,
		},
		{
			name:   "BT declines — not TD or TB",
			source: "graph BT\n  A --> B",
			wantOK: false,
		},
		{
			name:   "missing header declines",
			source: "  A --> B\n  B --> C",
			wantOK: false,
		},
		{
			name:   "header with no direction keyword declines",
			source: "flowchart\n  A --> B",
			wantOK: false,
		},
		{
			name:   "empty source declines",
			source: "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := mermaidFlipDirectionToLR(tt.source)
			if ok != tt.wantOK {
				t.Fatalf("mermaidFlipDirectionToLR() ok = %v, want %v (got %q)", ok, tt.wantOK, got)
			}
			if ok && got != tt.want {
				t.Fatalf("mermaidFlipDirectionToLR() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMermaidRetryLRIfColliding(t *testing.T) {
	t.Run("no collisions in first render never calls the second render", func(t *testing.T) {
		called := false
		render := func(string) (string, error) {
			called = true
			return "", nil
		}
		got := mermaidRetryLRIfColliding(collidingSource, nonCollidingArt, render)
		if got != nonCollidingArt {
			t.Fatalf("got %q, want first render unchanged", got)
		}
		if called {
			t.Fatal("second render was called despite zero collisions in the first")
		}
	})

	t.Run("unflippable header keeps the first render and never calls the second render", func(t *testing.T) {
		called := false
		render := func(string) (string, error) {
			called = true
			return nonCollidingArt, nil
		}
		got := mermaidRetryLRIfColliding("flowchart LR\n  A -->|collection| B\n  A -->|single composite| C\n", collidingArt, render)
		if got != collidingArt {
			t.Fatalf("got %q, want first render unchanged", got)
		}
		if called {
			t.Fatal("second render was called despite an unflippable (LR) header")
		}
	})

	t.Run("missing header keeps the first render", func(t *testing.T) {
		render := func(string) (string, error) {
			t.Fatal("second render should not be called with no header to flip")
			return "", nil
		}
		got := mermaidRetryLRIfColliding("  A -->|collection| B\n  A -->|single composite| C\n", collidingArt, render)
		if got != collidingArt {
			t.Fatalf("got %q, want first render unchanged", got)
		}
	})

	t.Run("flipped render error keeps the first render", func(t *testing.T) {
		render := func(string) (string, error) {
			return "", errors.New("boom")
		}
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, render)
		if got != collidingArt {
			t.Fatalf("got %q, want first render unchanged on flipped-render error", got)
		}
	})

	t.Run("blank flipped render keeps the first render", func(t *testing.T) {
		render := func(string) (string, error) {
			return "   \n  ", nil
		}
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, render)
		if got != collidingArt {
			t.Fatalf("got %q, want first render unchanged on blank flipped render", got)
		}
	})

	t.Run("flipped render with equal collisions keeps the first render", func(t *testing.T) {
		render := func(string) (string, error) {
			return collidingArt, nil
		}
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, render)
		if got != collidingArt {
			t.Fatalf("got %q, want first render unchanged when flipped render is no better", got)
		}
	})

	t.Run("flipped render with more collisions keeps the first render", func(t *testing.T) {
		worseArt := collidingArt + "\n" + collidingArt
		render := func(string) (string, error) {
			return worseArt, nil
		}
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, render)
		if got != collidingArt {
			t.Fatalf("got %q, want first render unchanged when flipped render is worse", got)
		}
	})

	t.Run("flipped render with strictly fewer collisions replaces the first render", func(t *testing.T) {
		var gotSource string
		render := func(s string) (string, error) {
			gotSource = s
			return nonCollidingArt, nil
		}
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, render)
		if got != nonCollidingArt {
			t.Fatalf("got %q, want the flipped render", got)
		}
		if !strings.Contains(gotSource, "flowchart LR") {
			t.Fatalf("second render was not called with the flipped LR source, got %q", gotSource)
		}
	})
}

func TestRenderMermaidSource_NoCollision_MatchesDirectRenderDiagram(t *testing.T) {
	source := "flowchart TD\n  A --> B\n  B --> C"

	direct, err := mermaidcmd.RenderDiagram(source, nil)
	if err != nil {
		t.Fatalf("mermaidcmd.RenderDiagram: %v", err)
	}

	got, err := renderMermaidSource(source, 120)
	if err != nil {
		t.Fatalf("renderMermaidSource: %v", err)
	}

	if got != direct {
		t.Fatalf("renderMermaidSource retry path changed a non-colliding render\ngot:\n%s\nwant:\n%s", got, direct)
	}
}

func TestRenderMermaidSource_RealThreeBranchFence_CollisionResolvedByRetry(t *testing.T) {
	art, err := renderMermaidSource(collisionThreeBranchFence, 120)
	if err != nil {
		t.Fatalf("renderMermaidSource: %v", err)
	}
	if strings.TrimSpace(art) == "" {
		t.Fatal("renderMermaidSource returned blank art")
	}

	toRender := collisionThreeBranchFence
	if transpiled, ok := transpileMermaid(collisionThreeBranchFence, 120); ok {
		toRender = transpiled
	}
	if got := mermaidCollisionCount(toRender, art); got != 0 {
		t.Fatalf("renderMermaidSource still collides after the retry: count = %d\nart:\n%s", got, art)
	}
}
