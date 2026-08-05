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
			name: "two long labels the gap cap apart are not a collision",
			// Both labels are longer than the cap, so the cap is the
			// threshold and a gap that reaches it is enough separation.
			source: "flowchart TD\n  A -->|collection| B\n  A -->|single composite| C\n",
			art:    "collection" + strings.Repeat("─", mermaidCollisionGap) + "single composite",
			want:   0,
		},
		{
			name:   "two long labels one column inside the gap cap collide",
			source: "flowchart TD\n  A -->|collection| B\n  A -->|single composite| C\n",
			art:    "collection" + strings.Repeat("─", mermaidCollisionGap-1) + "single composite",
			want:   1,
		},
		{
			name: "two short labels further apart than their own length are not a collision",
			// The shape that made a readable 80-column diagram flip to a
			// 273-column one: `no` and `yes` five columns apart on a decision
			// node's output row read as two plainly separate words. The gap is
			// under the cap, so only the length half of the threshold rejects
			// this.
			source: "flowchart TD\n  A -->|no| B\n  A -->|yes| C\n",
			art:    "│◄─no─────yes──────────┘",
			want:   0,
		},
		{
			name:   "two short labels flush together are still a collision",
			source: "flowchart TD\n  A -->|no| B\n  A -->|yes| C\n",
			art:    "│◄──noyes──────────┘",
			want:   1,
		},
		{
			name: "one label painted over another counts even though neither survives",
			// The overwriting shape: `collection` drawn on top of `single
			// composite` leaves `sin` and `ite` around it, and `single
			// composite` no longer exists as a word anywhere on the row. The
			// crowded-labels rule cannot see this — there is only one match on
			// the row, and its neighbors are letters.
			source: "flowchart TD\n  A -->|collection| B\n  A -->|single composite| C\n",
			art:    "│ Shape ├◄───sincollectionite───────┤",
			want:   1,
		},
		{
			name: "a short label inside node-box text is not a hit",
			// `no` and `yes` are inside `nothing` and `yesterday`, which is
			// ordinary node text on the same row. Counting them made a fence
			// with no real collision pay a second full render.
			source: "flowchart TD\n  A -->|no| B\n  A -->|yes| C\n",
			art:    "│ nothing yesterday │",
			want:   0,
		},
		{
			name:   "a label inside a longer word is not a hit",
			source: "flowchart TD\n  A -->|open| B\n  A -->|close| C\n",
			art:    "reopen closet",
			want:   0,
		},
		{
			name: "a match overlapping a claimed span is not a second hit",
			// `aaa bbb` starts BEFORE the span `bbb ccc ddd` already claimed
			// and runs into it. Testing only the opening column let this
			// through and reported one contiguous run of text as two labels.
			source: "flowchart TD\n  A -->|bbb ccc ddd| B\n  A -->|aaa bbb| C\n",
			art:    "aaa bbb ccc ddd",
			want:   0,
		},
		{
			name: "two labels drawn flush against each other still collide",
			// The word-boundary rule must not swallow the worst collision
			// shape: the longer label is matched first, so the shorter one's
			// letter neighbor is already consumed.
			source: "flowchart TD\n  A -->|collection| B\n  A -->|single composite| C\n",
			art:    "──single compositecollection──",
			want:   1,
		},
		{
			name: "a label carrying regex metacharacters matches literally",
			// Real corpus labels look like `listVariants (strict mode)`.
			// QuoteMeta is what keeps the parens from becoming a group.
			source: "flowchart TD\n  A -->|listVariants (strict mode)| B\n  A -->|other| C\n",
			art:    "──listVariants (strict mode)─other──",
			want:   1,
		},
		{
			name:   "a metacharacter label with no literal match does not count",
			source: "flowchart TD\n  A -->|a.c| B\n  A -->|other| C\n",
			art:    "──abc─other──",
			want:   0,
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
			name:   "trailing content after the direction survives untouched",
			source: "flowchart TD  %% laid out top-down\n  A --> B",
			want:   "flowchart LR  %% laid out top-down\n  A --> B",
			wantOK: true,
		},
		{
			name: "header with no direction keyword gains LR",
			// The renderer lays a direction-less header out top-down, so it
			// collides exactly like an explicit TD and deserves the same
			// retry — the flip inserts the keyword instead of replacing it.
			source: "flowchart\n  A --> B",
			want:   "flowchart LR\n  A --> B",
			wantOK: true,
		},
		{
			name:   "indented direction-less header keeps its indent",
			source: "  graph\n  A --> B",
			want:   "  graph LR\n  A --> B",
			wantOK: true,
		},
		{
			name:   "malformed direction declines rather than being rewritten",
			source: "flowchart TDX\n  A --> B",
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
		got := mermaidRetryLRIfColliding(collidingSource, nonCollidingArt, mermaidUnconstrainedWidth, render)
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
		got := mermaidRetryLRIfColliding("flowchart LR\n  A -->|collection| B\n  A -->|single composite| C\n", collidingArt, mermaidUnconstrainedWidth, render)
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
		got := mermaidRetryLRIfColliding("  A -->|collection| B\n  A -->|single composite| C\n", collidingArt, mermaidUnconstrainedWidth, render)
		if got != collidingArt {
			t.Fatalf("got %q, want first render unchanged", got)
		}
	})

	t.Run("flipped render error keeps the first render", func(t *testing.T) {
		render := func(string) (string, error) {
			return "", errors.New("boom")
		}
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, mermaidUnconstrainedWidth, render)
		if got != collidingArt {
			t.Fatalf("got %q, want first render unchanged on flipped-render error", got)
		}
	})

	t.Run("blank flipped render keeps the first render", func(t *testing.T) {
		render := func(string) (string, error) {
			return "   \n  ", nil
		}
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, mermaidUnconstrainedWidth, render)
		if got != collidingArt {
			t.Fatalf("got %q, want first render unchanged on blank flipped render", got)
		}
	})

	t.Run("flipped render with equal collisions keeps the first render", func(t *testing.T) {
		render := func(string) (string, error) {
			return collidingArt, nil
		}
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, mermaidUnconstrainedWidth, render)
		if got != collidingArt {
			t.Fatalf("got %q, want first render unchanged when flipped render is no better", got)
		}
	})

	t.Run("flipped render with more collisions keeps the first render", func(t *testing.T) {
		worseArt := collidingArt + "\n" + collidingArt
		render := func(string) (string, error) {
			return worseArt, nil
		}
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, mermaidUnconstrainedWidth, render)
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
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, mermaidUnconstrainedWidth, render)
		if got != nonCollidingArt {
			t.Fatalf("got %q, want the flipped render", got)
		}
		if !strings.Contains(gotSource, "flowchart LR") {
			t.Fatalf("second render was not called with the flipped LR source, got %q", gotSource)
		}
	})

	t.Run("a flip that pushes a fitting diagram modestly off the pane is still kept", func(t *testing.T) {
		paneWidth := len([]rune(collidingArt)) + 1 // the first render fits, the flipped one does not
		if len([]rune(nonCollidingArt)) <= paneWidth {
			t.Fatalf("test setup: flipped art must be wider than the pane, got %d for pane %d", len([]rune(nonCollidingArt)), paneWidth)
		}
		render := func(string) (string, error) { return nonCollidingArt, nil }
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, paneWidth, render)
		if got != nonCollidingArt {
			t.Fatalf("got %q, want the flipped render — leaving the pane is not on its own a reason to decline a correct render", got)
		}
	})

	t.Run("a flip that blows a fitting diagram up past the ratio keeps the first render", func(t *testing.T) {
		// Same shape as the case above — the first render fits, the flipped
		// one does not — but the flipped art is far past
		// mermaidFlipBlowupRatio times as wide, which is the only width the
		// gate still objects to.
		blownUp := nonCollidingArt + strings.Repeat("─", 4*len([]rune(collidingArt)))
		paneWidth := len([]rune(collidingArt)) + 1
		render := func(string) (string, error) { return blownUp, nil }
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, paneWidth, render)
		if got != collidingArt {
			t.Fatalf("got %q, want first render unchanged when the flip blows up past the ratio", got)
		}
	})

	t.Run("a flip is allowed when the first render already overflows the pane", func(t *testing.T) {
		render := func(string) (string, error) { return nonCollidingArt, nil }
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, len([]rune(collidingArt))-1, render)
		if got != nonCollidingArt {
			t.Fatalf("got %q, want the flipped render — the first render did not fit either", got)
		}
	})

	t.Run("a flip that still fits the pane is kept", func(t *testing.T) {
		render := func(string) (string, error) { return nonCollidingArt, nil }
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, len([]rune(nonCollidingArt)), render)
		if got != nonCollidingArt {
			t.Fatalf("got %q, want the flipped render — it fits the pane", got)
		}
	})

	t.Run("a panic in the flipped render keeps the first render", func(t *testing.T) {
		render := func(string) (string, error) { panic("vendored renderer exploded") }
		got := mermaidRetryLRIfColliding(collidingSource, collidingArt, mermaidUnconstrainedWidth, render)
		if got != collidingArt {
			t.Fatalf("got %q, want first render unchanged after a panic in the flipped render", got)
		}
	})
}

func TestMermaidWidthTradeAcceptable(t *testing.T) {
	tests := []struct {
		name         string
		firstWidth   int
		flippedWidth int
		paneWidth    int
		want         bool
	}{
		{
			// Measured on testdata/mermaid/collision-fitting-td-render.mmd at
			// a 160-column pane: the top-down render is 150 columns wide and
			// fits, the LR flip is 238. The old fit-only gate vetoed this and
			// left the reader looking at `collection` painted over
			// `single composite`. 238/150 is 1.59, well inside the ratio.
			name:       "the pane-width window this rule exists for: a 150-column render is replaced by a 238-column flip",
			firstWidth: 150, flippedWidth: 238, paneWidth: 160, want: true,
		},
		{
			// The corpus case the gate was added for: two short labels five
			// clear columns apart, called a collision by the detector, sent a
			// perfectly readable 71-column diagram off to 330 columns.
			// 330/71 is 4.65, past the ratio, so this stays vetoed.
			name:       "the blowup this rule still vetoes: a 71-column render is not replaced by a 330-column flip",
			firstWidth: 71, flippedWidth: 330, paneWidth: 120, want: false,
		},
		{
			name:       "exactly the ratio is not MORE than the ratio, so the flip is kept",
			firstWidth: 100, flippedWidth: 300, paneWidth: 120, want: true,
		},
		{
			name:       "one column past the ratio is vetoed",
			firstWidth: 100, flippedWidth: 301, paneWidth: 120, want: false,
		},
		{
			name:       "a first render that already overflows the pane raises no objection, however wide the flip",
			firstWidth: 130, flippedWidth: 1300, paneWidth: 120, want: true,
		},
		{
			name:       "a flip that still fits the pane is kept even when it is many times wider",
			firstWidth: 10, flippedWidth: 100, paneWidth: 120, want: true,
		},
		{
			name:       "a zero-width first render has no fitting diagram to protect",
			firstWidth: 0, flippedWidth: 330, paneWidth: 120, want: true,
		},
		{
			name:       "a negative first width has no fitting diagram to protect either",
			firstWidth: -1, flippedWidth: 330, paneWidth: 120, want: true,
		},
		{
			name:       "mermaidUnconstrainedWidth means no width objection at all",
			firstWidth: 71, flippedWidth: 330, paneWidth: mermaidUnconstrainedWidth, want: true,
		},
		{
			name:       "a zero pane is no constraint, not a pane nothing fits",
			firstWidth: 71, flippedWidth: 330, paneWidth: 0, want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mermaidWidthTradeAcceptable(tt.firstWidth, tt.flippedWidth, tt.paneWidth)
			if got != tt.want {
				t.Fatalf("mermaidWidthTradeAcceptable(%d, %d, %d) = %v, want %v",
					tt.firstWidth, tt.flippedWidth, tt.paneWidth, got, tt.want)
			}
		})
	}
}

// TestMermaidFlipBlowupRatio_SeparatesTheMeasuredCases pins the margin on both
// sides of the constant. The two cases in the table above are the real ones the
// rule has to tell apart, and a constant that only just separates them would be
// a coincidence rather than a rule.
func TestMermaidFlipBlowupRatio_SeparatesTheMeasuredCases(t *testing.T) {
	suppressedFix := 238.0 / 150.0 // the pane-width window bug: must be kept
	falsePositive := 330.0 / 71.0  // the detector's phantom collision: must be vetoed

	if suppressedFix >= mermaidFlipBlowupRatio {
		t.Fatalf("the suppressed fix widens by %.2fx, which the ratio %.1f does not keep", suppressedFix, mermaidFlipBlowupRatio)
	}
	if falsePositive <= mermaidFlipBlowupRatio {
		t.Fatalf("the false-positive blowup widens by %.2fx, which the ratio %.1f does not veto", falsePositive, mermaidFlipBlowupRatio)
	}
	if mermaidFlipBlowupRatio-suppressedFix < 1 || falsePositive-mermaidFlipBlowupRatio < 1 {
		t.Fatalf("the ratio %.1f sits too close to a measured case (%.2fx kept, %.2fx vetoed) to be more than a coincidence",
			mermaidFlipBlowupRatio, suppressedFix, falsePositive)
	}
}

// TestMermaidFlipWidthAcceptable_MeasuresArtNotBytes pins that the gate's two
// inputs are art WIDTHS — widest row, trailing padding stripped — and not the
// length of the rendered strings, which for multi-row art are unrelated numbers.
func TestMermaidFlipWidthAcceptable_MeasuresArtNotBytes(t *testing.T) {
	// Ten rows of ten columns, each padded out to 40 columns by the renderer.
	first := strings.TrimSuffix(strings.Repeat(strings.Repeat("─", 10)+strings.Repeat(" ", 30)+"\n", 10), "\n")
	// One row of 25 columns: 2.5x the first render's width, inside the ratio,
	// but a far SHORTER string than the first render.
	flipped := strings.Repeat("─", 25)

	if !mermaidFlipWidthAcceptable(first, flipped, 20) {
		t.Fatalf("gate rejected a 10-column render being replaced by a 25-column one at a 20-column pane")
	}
	if mermaidFlipWidthAcceptable(first, strings.Repeat("─", 31), 20) {
		t.Fatalf("gate accepted a 10-column render being replaced by a 31-column one at a 20-column pane")
	}
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
