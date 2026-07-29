package ui

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mermaidDiagramKind ---

func TestMermaidDiagramKind_Table(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"graph", "graph TD\n    A --> B", "graph"},
		{"flowchart", "flowchart LR\n    A --> B", "flowchart"},
		{"sequenceDiagram", "sequenceDiagram\n    Alice->>Bob: Hi", "sequenceDiagram"},
		{"erDiagram", "erDiagram\n    CUSTOMER ||--o{ ORDER : places", "erDiagram"},
		{"gantt", "gantt\n    title A Gantt Diagram", "gantt"},
		{"quadrantChart", "quadrantChart\n    title Reach and engagement", "quadrantChart"},
		{"classDiagram", "classDiagram\n    class Foo", "classDiagram"},
		{"stateDiagram-v2", "stateDiagram-v2\n    [*] --> A", "stateDiagram-v2"},
		// prose is not validated against a known list — mermaidDiagramKind
		// mechanically extracts the first whitespace-delimited token,
		// whatever it is; transpileMermaid's default case treats any
		// unrecognized result identically to a truly unknown kind.
		{"prose", "just some notes, not a real diagram", "just"},
		{"leading blank lines", "\n\n\ngraph TD\n    A --> B", "graph"},
		{"leading %% comments", "%% a comment\n%% another\nflowchart LR\n    A --> B", "flowchart"},
		{"empty source", "", ""},
		{"only blank lines and comments", "\n%% only a comment\n\n", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mermaidDiagramKind(tc.source))
		})
	}
}

// --- mermaidSafeText ---

func TestMermaidSafeText_Table(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"quote deleted", `Display "Name"`, "Display Name"},
		{"brackets mapped to parens", "a[b]c", "a(b)c"},
		{"angle brackets mapped to parens", "a<b>c", "a(b)c"},
		{"pipe mapped to slash", "a|b", "a/b"},
		{"colon run collapses to one", "a:::b", "a:b"},
		{"run of five colons collapses to one, not three", "a:::::b", "a:b"},
		{"single colon left alone", "a:b", "a:b"},
		{"percent-percent truncates the rest of the line", "keep this %% drop this", "keep this"},
		{"backslash deleted", `a\b`, "ab"},
		{"tab, CR, and LF become space", "a\tb\rc\nd", "a b c d"},
		{"ampersand squeeze removes surrounding spaces", "A & B", "A&B"},
		{"space runs collapse to one and trim", "  a   b  ", "a b"},
		{"parens, braces, and tilde left alone", "(a) {b} ~c~", "(a) {b} ~c~"},
		{"non-ASCII left alone", "café 日本語", "café 日本語"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mermaidSafeText(tc.input))
		})
	}
}

// --- mermaidEdgeLabel ---

func TestMermaidEdgeLabel_CutAtBRTag(t *testing.T) {
	got := mermaidEdgeLabel("decision==approve<br/>AND publicationDate in future", 32)
	assert.Equal(t, "decision==approve", got)
}

func TestMermaidEdgeLabel_CutAtLiteralBackslashN(t *testing.T) {
	got := mermaidEdgeLabel(`first part\nsecond part`, 32)
	assert.Equal(t, "first·part", got,
		"the cut must run before mermaidSafeText deletes the backslash, or the \\n marker is destroyed before it can be found")
}

func TestMermaidEdgeLabel_CutAtFirstParen(t *testing.T) {
	got := mermaidEdgeLabel("submit (pins version, starts workflow)", 32)
	assert.Equal(t, "submit", got)
}

func TestMermaidEdgeLabel_CutAtFirstBrace(t *testing.T) {
	got := mermaidEdgeLabel("resolve {outcome} (act without claiming)", 32)
	assert.Equal(t, "resolve", got)
}

func TestMermaidEdgeLabel_SpacesBecomeMiddleDot(t *testing.T) {
	got := mermaidEdgeLabel("owns 1 n", 32)
	assert.Equal(t, "owns·1·n", got,
		"every remaining space must become a middle dot, or the arrow bleeds through it (position-dependent)")
}

func TestMermaidEdgeLabel_RunOfMiddleDotsCollapsesToOne(t *testing.T) {
	// a raw label that already contains a literal middle dot surrounded by
	// spaces (real corpus text — see TestStateTranspiler_SixVerbatimCorpusLabels_ShortenedForms)
	// turns both of those surrounding spaces into "·" too, which without the
	// collapse would read as three dots in a row where the author wrote one.
	got := mermaidEdgeLabel("open · C1 (Request)", 32)
	assert.Equal(t, "open·C1", got,
		"a run of two or more middle dots must collapse to a single one, not read as a triple-dot artifact")
}

func TestMermaidEdgeLabel_ParenAtStart_FallsBackToTruncationInsteadOfEmpty(t *testing.T) {
	got := mermaidEdgeLabel("(short)", 32)
	assert.Equal(t, "(short)", got,
		"cutting at index 0 would discard the whole label for no gain; the guard must skip the cut instead")
}

func TestMermaidEdgeLabel_HardTruncation_NoCutPoint(t *testing.T) {
	got := mermaidEdgeLabel(strings.Repeat("x", 50), 20)
	assert.Equal(t, strings.Repeat("x", 17)+"...", got)
	assert.Len(t, []rune(got), 20)
}

func TestMermaidEdgeLabel_ByteCapBindsBeforeRuneCap_NonASCII(t *testing.T) {
	// 32 runes of "≤" (3 bytes each) satisfy a 32-RUNE cap but reserve 96
	// bytes — mapping_edge.go bills column width by len() (bytes), so the
	// byte cap (same numeric value) must bind here instead, matching the
	// plan's Probe finding #3: 30 bytes of budget spent, here as 9 kept runes
	// plus the 3-byte ellipsis.
	raw := strings.Repeat("≤", 32)
	got := mermaidEdgeLabel(raw, 32)
	assert.Equal(t, strings.Repeat("≤", 9)+"...", got)
	assert.LessOrEqual(t, len(got), 32)
}

func TestMermaidEdgeLabel_ByteCappedMultiWordLabel_KeepsTheEllipsis(t *testing.T) {
	// Regression pin. mermaidEdgeLabel turns every space into a 2-byte "·",
	// so the BYTE cap — not the rune cap — is what binds for essentially
	// every multi-word edge label. An earlier version of
	// mermaidTruncateRunesAndBytes could only ever return a bare prefix on
	// that path, so a cut label looked complete: two different transitions
	// out of the same state both displayed as "reviewer·resolv" with nothing
	// marking the truncation.
	got := mermaidEdgeLabel("reviewer resolves request-changes", mermaidLabelMinRunes)
	assert.True(t, strings.HasSuffix(got, "..."),
		"a byte-capped edge label must still carry the ellipsis, or truncation is invisible: %q", got)
	assert.LessOrEqual(t, len(got), mermaidLabelMinRunes, "the byte cap must still hold")
	assert.LessOrEqual(t, len([]rune(got)), mermaidLabelMinRunes, "the rune cap must still hold")
	assert.Equal(t, "reviewer·res...", got)
}

func TestMermaidTruncateRunesAndBytes_NoRoomForEllipsis_FallsBackToBarePrefix(t *testing.T) {
	// A cap at or below the ellipsis's own length cannot carry one; the
	// result degrades to a bare prefix rather than to the ellipsis alone.
	got := mermaidTruncateRunesAndBytes(strings.Repeat("≤", 5), 3)
	assert.Equal(t, "≤", got)
	assert.LessOrEqual(t, len(got), 3)
}

func TestMermaidEdgeLabel_AngleBracketInLabel_NotCutAsAParenthetical(t *testing.T) {
	// mermaidSafeText maps '<' and '[' to '(' — so cutting the parenthetical
	// AFTER sanitizing silently truncated every label containing either
	// character. The cut must run on the raw text, where no parenthesis
	// exists.
	assert.Equal(t, "count·(·max", mermaidEdgeLabel("count < max", 32))
	assert.Equal(t, "uses·arr(i)", mermaidEdgeLabel("uses arr[i]", 32))
	assert.Equal(t, "emits·(event)", mermaidEdgeLabel("emits <event>", 32))
}

func TestMermaidEdgeLabel_AngleBracketWithCardinality_KeepsTheSuffix(t *testing.T) {
	// The classDiagram path composes "word + cardinality suffix" and hands
	// the whole thing here. With the cut running after sanitizing, a '<' in
	// the word took the cardinality down with it.
	got := mermaidEdgeLabel(classJoinLabelParts("count < max", classComposeCardinality("1", "n")), 32)
	assert.Equal(t, "count·(·max·1·n", got)
}

func TestMermaidEdgeLabel_AllCutAway_YieldsNoLabelNotEmptyString(t *testing.T) {
	got := mermaidEdgeLabel("<br/>", 32)
	assert.Empty(t, got, "every rule stripping the label to nothing must yield \"\", not a residual fragment")
}

// --- mermaidTruncate ---

func TestMermaidTruncate_ShortString_Unchanged(t *testing.T) {
	assert.Equal(t, "short", mermaidTruncate("short", 32))
}

func TestMermaidTruncate_ExactFit_Unchanged(t *testing.T) {
	s := strings.Repeat("a", 16)
	assert.Equal(t, s, mermaidTruncate(s, 16))
}

func TestMermaidTruncate_OverLength_TruncatesWithASCIIEllipsis(t *testing.T) {
	got := mermaidTruncate(strings.Repeat("a", 40), 16)
	assert.Equal(t, strings.Repeat("a", 13)+"...", got)
	assert.Len(t, []rune(got), 16)
}

func TestMermaidTruncate_RuneSafe_DoesNotSplitMultiByteRune(t *testing.T) {
	// 20 runes of "日" (3 bytes each); truncating to 10 runes must cut on a
	// rune boundary, never leaving a mangled partial UTF-8 sequence.
	s := strings.Repeat("日", 20)
	got := mermaidTruncate(s, 10)
	assert.True(t, utf8.ValidString(got), "truncation must never split a multi-byte rune")
	assert.Equal(t, strings.Repeat("日", 7)+"...", got)
}

// --- mermaidStripComment ---

func TestMermaidStripComment_FullLineComment_BecomesEmpty(t *testing.T) {
	assert.Empty(t, mermaidStripComment("   %% a full line comment"))
}

func TestMermaidStripComment_InlineComment_CutAndTrimmed(t *testing.T) {
	assert.Equal(t, "keep this", mermaidStripComment("keep this %% drop this"))
}

func TestMermaidStripComment_NoComment_Unchanged(t *testing.T) {
	assert.Equal(t, "A --> B", mermaidStripComment("A --> B"))
}

// --- splitOnFirstColon ---

func TestSplitOnFirstColon_Table(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantBefore string
		wantAfter  string
		wantOK     bool
	}{
		{"spaced colon in a transition label", "A --> B : label", "A --> B", "label", true},
		{"tight colon in a transition label", "A --> B: label", "A --> B", "label", true},
		{"classDiagram member colon form", "Foo : +bar() void", "Foo", "+bar() void", true},
		{"no colon at all", "A --> B", "A --> B", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before, after, ok := splitOnFirstColon(tc.line)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.wantBefore, before)
			assert.Equal(t, tc.wantAfter, after)
		})
	}
}

// --- mermaidLabelCap ---

func TestMermaidLabelCap_AdaptiveTable_80ColumnPane(t *testing.T) {
	// Worked values from the plan's "The width cap is adaptive" section,
	// cross-checked against the probe: k=1 hits the ceiling (32), k=2 gives
	// 29, k=3 lands exactly on the floor (16), k=4 also floors (16, the
	// formula "wanted" 8 but the floor clamps it).
	tests := []struct {
		k    int
		want int
	}{
		{1, 32},
		{2, 29},
		{3, 16},
		{4, 16},
	}
	for _, tc := range tests {
		got := mermaidLabelCap(80, tc.k, true)
		assert.Equalf(t, tc.want, got, "k=%d", tc.k)
	}
}

func TestMermaidLabelCap_NoLabeledEdge_LabelJogNotApplied(t *testing.T) {
	// The four unlabeled relation kinds pay no labelJog cost at all: with
	// hasLabel=false, k=2 reduces to the plain box-only formula.
	got := mermaidLabelCap(80, 2, false)
	// (80 - 5*1 - 0)/2 - 4 = 75/2-4 = 37-4 = 33, clamped to the ceiling.
	assert.Equal(t, 32, got)
}

func TestMermaidLabelCap_Floor(t *testing.T) {
	got := mermaidLabelCap(80, 4, true)
	assert.Equal(t, mermaidLabelMinRunes, got)
}

func TestMermaidLabelCap_Ceiling(t *testing.T) {
	got := mermaidLabelCap(80, 1, true)
	assert.Equal(t, mermaidLabelMaxRunes, got)
}

func TestMermaidLabelCap_UnconstrainedSentinel_AlwaysReturnsCeiling(t *testing.T) {
	assert.Equal(t, mermaidLabelMaxRunes, mermaidLabelCap(mermaidUnconstrainedWidth, 1, false))
	assert.Equal(t, mermaidLabelMaxRunes, mermaidLabelCap(mermaidUnconstrainedWidth, 4, true))
}

func TestMermaidLabelCap_KLessThanOne_ClampedToOne(t *testing.T) {
	// Defensive clamp: every real caller derives k from widestLevel(), which
	// never returns less than 1, but mermaidLabelCap itself must not divide
	// by a non-positive k if some future caller passes one directly.
	assert.Equal(t, mermaidLabelCap(80, 1, true), mermaidLabelCap(80, 0, true))
	assert.Equal(t, mermaidLabelCap(80, 1, true), mermaidLabelCap(80, -3, true))
}

// --- classInheritanceLabel (pinned so a future edit cannot silently change it) ---

func TestClassInheritanceLabel_Value(t *testing.T) {
	assert.Equal(t, "implements", classInheritanceLabel)
}

// --- flowchartBuilder ---

func TestFlowchartBuilder_NodeIDAssignment_FirstSeenOrder(t *testing.T) {
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	first := b.node("B")
	second := b.node("A")
	sameAsFirst := b.node("B")

	assert.Equal(t, "n0", first.id)
	assert.Equal(t, "n1", second.id)
	assert.Same(t, first, sameAsFirst, "re-requesting an existing key must return the same node, not a new id")
}

func TestFlowchartBuilder_TitleAndStereotypeOrdering(t *testing.T) {
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	b.setTitle("Repo", "Repo")
	b.setStereotype("Repo", "«interface»")

	lines := b.nodes["Repo"].renderLabelLines(mermaidLabelMaxRunes)
	require.Len(t, lines, 2)
	assert.Equal(t, "«interface»", lines[0], "stereotype must render above the title")
	assert.Equal(t, "Repo", lines[1])
}

func TestFlowchartBuilder_LabelLineCap_OverflowRow(t *testing.T) {
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	for i := range 14 {
		b.addLabelLine("Task", strings.Repeat("x", i+1))
	}

	lines := b.nodes["Task"].renderLabelLines(mermaidLabelMaxRunes)
	require.Len(t, lines, classMaxMembers+1, "must keep exactly classMaxMembers lines plus one overflow row")
	assert.Equal(t, "... +2 more", lines[len(lines)-1])
}

func TestFlowchartBuilder_EdgeDedup(t *testing.T) {
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	b.addEdge("A", "B", "owns")
	b.addEdge("A", "B", "owns")
	b.addEdge("A", "B", "uses") // different label: not a duplicate

	assert.Len(t, b.edges, 2)
}

func TestFlowchartBuilder_Empty(t *testing.T) {
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	assert.True(t, b.empty())

	b.node("A")
	assert.False(t, b.empty())
}

func TestFlowchartBuilder_Source_EmittedShape(t *testing.T) {
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	b.setTitle("A", "A")
	b.setTitle("B", "B")
	b.addEdge("A", "B", "uses")

	got := b.source()
	assert.True(t, strings.HasPrefix(got, "flowchart TD\n"))
	assert.Contains(t, got, "n0[A]")
	assert.Contains(t, got, "n1[B]")
	assert.Contains(t, got, "n0 -->|uses| n1")
}

func TestFlowchartBuilder_Source_Empty_ReturnsEmptyString(t *testing.T) {
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	assert.Empty(t, b.source())
}

func TestFlowchartBuilder_Source_AdaptiveCapUsesRealPaneWidth(t *testing.T) {
	// k=3 with a labeled edge at an 80-column pane yields a cap of 16 (see
	// the plan's adaptive-cap table) — well under the ceiling an
	// mermaidUnconstrainedWidth builder would use for the same content, so
	// this proves paneWidth genuinely drives the cap rather than being
	// plumbed through unused.
	b := newFlowchartBuilder(80)
	longLabel := strings.Repeat("m", 30)
	b.addLabelLine("A", longLabel)
	b.addEdge("Impl1", "A", classInheritanceLabel)
	b.addEdge("Impl2", "A", classInheritanceLabel)
	b.addEdge("Impl3", "A", classInheritanceLabel)

	got := b.source()
	assert.NotContains(t, got, longLabel, "a 30-rune member line must be truncated under the k=3/width=80 cap of 16")
}

func TestFlowchartBuilder_WidestLevel_FeedsK(t *testing.T) {
	// Three implementors, all pointing at one shared interface: the
	// implementors are never targets (roots, level 0 — all three share that
	// level), the interface is level 1 (alone) — k must be 3.
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	b.addEdge("Impl1", "IFace", classInheritanceLabel)
	b.addEdge("Impl2", "IFace", classInheritanceLabel)
	b.addEdge("Impl3", "IFace", classInheritanceLabel)

	assert.Equal(t, 3, b.topology().widestLevel())
}

func TestFlowchartBuilder_DeclarationOrder_SourcesBeforeTargets_PinnedAgainstFanIn(t *testing.T) {
	// A fan-in: B and C are both edge sources (in that order), A is only
	// ever a target. Declaring A before its sources would make graph.go's
	// insertion-order root computation treat A as an extra root too,
	// collapsing the layout — see flowchartBuilder.declarationOrder's doc
	// comment. A must therefore be emitted last.
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	b.addEdge("B", "A", "x")
	b.addEdge("C", "A", "y")

	assert.Equal(t, []string{"B", "C", "A"}, b.topology().order)
}

func TestFlowchartBuilder_DeclarationOrder_NodeWithNoEdgesAtAll_KeepsItsFirstSeenSlot(t *testing.T) {
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	b.addEdge("Src", "Dst", "x")
	b.setTitle("Standalone", "Standalone") // never mentioned in any edge, first-seen after Dst

	got := b.topology().order
	assert.Equal(t, []string{"Src", "Dst", "Standalone"}, got,
		"leftover (never-a-source) nodes must keep their own first-seen relative order")
}

// --- scanMermaidBlocks ---

type recordedBlockCall struct {
	kind  string // "header" or "line"
	text  string
	depth int
}

// fakeBlockHandler implements mermaidBlockHandler purely to record every
// call scanMermaidBlocks makes, in order — the test oracle for verifying the
// scanner's own behavior (brace nesting, transparency, comment/blank
// handling) without needing a real classTranspiler/stateTranspiler to exist.
type fakeBlockHandler struct {
	calls []recordedBlockCall
	named map[string]bool // header text -> "counts toward depth" to return
}

func (f *fakeBlockHandler) blockHeader(header string, depth int) bool {
	f.calls = append(f.calls, recordedBlockCall{"header", header, depth})
	return f.named[header]
}

func (f *fakeBlockHandler) line(text string, depth int) {
	f.calls = append(f.calls, recordedBlockCall{"line", text, depth})
}

func TestScanMermaidBlocks_NamedBlock_IncrementsDepth(t *testing.T) {
	source := "class Foo {\n  +bar() void\n}"
	h := &fakeBlockHandler{named: map[string]bool{"class Foo": true}}

	scanMermaidBlocks(source, h)

	assert.Equal(t, []recordedBlockCall{
		{"header", "class Foo", 0},
		{"line", "+bar() void", 1},
		{"line", "}", 1},
	}, h.calls)
}

func TestScanMermaidBlocks_TransparentBlock_DoesNotIncrementDepth(t *testing.T) {
	// namespace is transparent: a class nested inside it must report at the
	// SAME depth as a top-level class — see the plan's "Composite blocks"/
	// namespace-attribution discussion.
	source := "namespace Ns {\nclass Foo {\n+bar() void\n}\n}"
	h := &fakeBlockHandler{named: map[string]bool{
		"namespace Ns": false, // transparent
		"class Foo":    true,
	}}

	scanMermaidBlocks(source, h)

	assert.Equal(t, []recordedBlockCall{
		{"header", "namespace Ns", 0},
		{"header", "class Foo", 0}, // still depth 0: namespace didn't count
		{"line", "+bar() void", 1},
		{"line", "}", 1}, // closes "class Foo"
		{"line", "}", 0}, // closes "namespace Ns"
	}, h.calls)
}

func TestScanMermaidBlocks_CommentStripping(t *testing.T) {
	source := "%% a full-line comment\nA --> B %% inline comment"
	h := &fakeBlockHandler{}

	scanMermaidBlocks(source, h)

	assert.Equal(t, []recordedBlockCall{
		{"line", "A --> B", 0},
	}, h.calls, "the full-line comment must be skipped entirely; the inline comment must be stripped, not passed through")
}

func TestScanMermaidBlocks_BlankLineSkipping(t *testing.T) {
	source := "A --> B\n\n   \nC --> D"
	h := &fakeBlockHandler{}

	scanMermaidBlocks(source, h)

	assert.Equal(t, []recordedBlockCall{
		{"line", "A --> B", 0},
		{"line", "C --> D", 0},
	}, h.calls)
}

func TestScanMermaidBlocks_UnbalancedExtraClosingBrace_SilentlyIgnored(t *testing.T) {
	// A "}" with nothing currently open (more closes than opens) must not
	// panic, underflow the brace stack, or reach the handler as a line — see
	// scanMermaidBlocks's own doc comment: "a mismatched extra `}` (more
	// closes than opens) is silently ignored rather than a defensive check
	// every transpiler would otherwise need of its own."
	source := "A --> B\n}\nC --> D"
	h := &fakeBlockHandler{}

	assert.NotPanics(t, func() {
		scanMermaidBlocks(source, h)
	})

	assert.Equal(t, []recordedBlockCall{
		{"line", "A --> B", 0},
		{"line", "C --> D", 0},
	}, h.calls, "the unbalanced \"}\" must never reach the handler as a line, and must not disturb what comes after it")
}

// --- classDiagram transpiler: classMemberText ---

func TestClassMemberText_TrailingSpaceParenCommentary_Stripped(t *testing.T) {
	// All eight lines are verbatim from real corpus classDiagram fences (see
	// the plan's "Keeping boxes readable" section): five carry genuine
	// trailing commentary that must be stripped (from the 18-member `Task`
	// class in
	// mx/api-overview/plans/workflow-task-separation/architecture-proposal.md,
	// the same corpus source the plan's Probe finding #4 measures), and
	// three are method signatures whose own "()" has no space before it at
	// all — so the space-paren rule must leave them completely untouched
	// (two from mx/magnolia-content-api/adrs/024-generalized-reference-resources.md,
	// plus the plan's own canonical "+bar() void" example, echoed
	// verbatim from a real corpus classDiagram in
	// keen-noodling-russell.md's `splitForCreate/Update/Replace()`). This is
	// the exact three-of-eight split the plan's derivation calls out: "the
	// blunt rule is wrong on three".
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"trailing commentary: our opaque id", `+id : String  (our opaque id)`, "+id : String"},
		{"trailing commentary: enum-like values", `+type : String  (approval | review | checklist | generic | custom)`, "+type : String"},
		{"trailing commentary: nullable", `+assignee : String  (nullable)`, "+assignee : String"},
		{"trailing commentary: nullable (second field)", `+dueDate : Instant  (nullable)`, "+dueDate : Instant"},
		{"trailing commentary: em-dash explanation", `+workflow : WorkflowRef  (NULLABLE — correlation only)`, "+workflow : WorkflowRef"},
		{"method with return type: no space before paren", "+bar() void", "+bar() void"},
		{"method group with no args: no space before paren", "splitForCreate/Update/Replace()", "splitForCreate/Update/Replace()"},
		{"method with return type: real corpus", "+token() String", "+token() String"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classMemberText(tc.input))
		})
	}
}

func TestClassMemberText_MethodArgList_NotStripped(t *testing.T) {
	assert.Equal(t, "+bar() void", classMemberText("+bar() void"),
		"the blunt \"cut at the first '('\" rule would wrongly truncate this to \"+bar\"")
	assert.Equal(t, "splitForCreate/Update/Replace()", classMemberText("splitForCreate/Update/Replace()"),
		"the blunt rule would wrongly clip the trailing \"()\" off this method group")
}

func TestClassMemberText_PipeInMemberLine_ReplacedWithSlash(t *testing.T) {
	// Verbatim from the plan's "Declaration order" section: a real member
	// line whose pipe must survive as a slash, not vanish or corrupt the
	// emitted flowchart source.
	assert.Equal(t, "+kind : content/asset/uri", classMemberText("+kind : content|asset|uri"))
}

// --- classDiagram transpiler: classStereotype ---

func TestClassStereotype_Table(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		wantOK bool
	}{
		{"interface", "<<interface>>", "«interface»", true},
		{"extra internal spacing", "<< abstract >>", "«abstract»", true},
		{"not a stereotype", "+bar() void", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := classStereotype(tc.input)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// --- classDiagram transpiler: classSplitTrailingLabel ---

func TestClassSplitTrailingLabel_SkipsStyleSuffixColonRuns(t *testing.T) {
	// Both operands carry a ":::style" suffix (a run of 3 colons each); the
	// real trailing label separator is the single lone colon before
	// "bites". A naive first-colon cut (splitOnFirstColon) would instead
	// split inside the FIRST ":::style" run.
	body, label, ok := classSplitTrailingLabel(`Dog:::style --> Cat:::style2 : bites`)
	require.True(t, ok)
	assert.Equal(t, "Dog:::style --> Cat:::style2", body)
	assert.Equal(t, "bites", label)
}

func TestClassSplitTrailingLabel_NoLabel(t *testing.T) {
	body, label, ok := classSplitTrailingLabel("A --> B")
	assert.False(t, ok)
	assert.Equal(t, "A --> B", body)
	assert.Empty(t, label)
}

func TestClassSplitTrailingLabel_ColonInsideQuotedCardinality_IsNotTheLabelSeparator(t *testing.T) {
	// `"1:n"` is a valid mermaid cardinality. Scanning the raw line cuts
	// inside it, leaving a body of `Customer "1` with no arrow at all — the
	// relation is dropped and the leftover text becomes a phantom node.
	body, label, ok := classSplitTrailingLabel(`Customer "1:n" --> Order`)
	assert.False(t, ok, "the only colon here is inside a quoted cardinality, so there is no trailing label")
	assert.Equal(t, `Customer "1:n" --> Order`, body)
	assert.Empty(t, label)

	// a real trailing label AFTER a colon-carrying cardinality still splits
	body, label, ok = classSplitTrailingLabel(`Customer "1:n" --> Order : places`)
	require.True(t, ok)
	assert.Equal(t, `Customer "1:n" --> Order`, body)
	assert.Equal(t, "places", label)
}

func TestClassTranspiler_QuotedCardinalityWithColon_KeepsTheRelationAndMakesNoPhantomNode(t *testing.T) {
	src := "classDiagram\n" +
		"    Customer \"1:n\" --> Order\n" +
		"    Order \"1\" *-- \"1..*\" LineItem\n"

	got, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
	require.True(t, ok)

	assert.Contains(t, got, "[Customer]")
	assert.Contains(t, got, "[Order]")
	assert.Contains(t, got, "[LineItem]")
	assert.NotContains(t, got, "Customer 1", "no phantom node built out of the split cardinality")
	assert.NotContains(t, got, "--)", "no phantom member built out of the relation's own text")
	assert.Equal(t, 2, strings.Count(got, "-->"), "both relations must survive")
}

// --- classDiagram transpiler: classIgnoredStatement ---

func TestClassLollipopRelation_Lollipop(t *testing.T) {
	assert.True(t, classLollipopRelation.MatchString("Class1 ()-- Class2"))
	assert.True(t, classLollipopRelation.MatchString("Class1 --() Class2"), "the mirrored lollipop form too")
	assert.True(t, classLollipopRelation.MatchString("()-- Class2"), "at start of line")
	assert.False(t, classLollipopRelation.MatchString("A --> B"))
}

func TestClassLollipopRelation_IsATokenNotASubstring(t *testing.T) {
	// A method signature closes its paren directly against the method name,
	// so "()" is never its own token there. A substring test for "()--"
	// dropped the whole member line.
	assert.False(t, classLollipopRelation.MatchString("Node : +splitForCreate()--Update"))
	assert.False(t, classLollipopRelation.MatchString("Foo : +calc()--x"))
}

func TestClassTranspiler_LollipopStatement_DroppedNotTurnedIntoARelation(t *testing.T) {
	// end-to-end companion to the two regexp tests above: the lollipop check
	// runs BEFORE the relation parse, so classArrowPattern's plain "--"
	// fallback never gets to manufacture an edge out of the "()" text.
	got, ok := transpileMermaid("classDiagram\n    A <|-- B\n    Class1 ()-- Class2\n", mermaidUnconstrainedWidth)
	require.True(t, ok)
	assert.NotContains(t, got, "Class1")
	assert.NotContains(t, got, "Class2")
	assert.Equal(t, 1, strings.Count(got, "-->"), "only the real relation survives")
}

// --- classDiagram transpiler: parseClassDecl ---

func TestParseClassDecl_Bare(t *testing.T) {
	key, title := parseClassDecl("Foo")
	assert.Equal(t, "Foo", key)
	assert.Equal(t, "Foo", title)
}

func TestParseClassDecl_BracketAlias_QuotesAndBracketsConsumed_ParentheticalKept(t *testing.T) {
	// Verbatim shape from a real corpus classDiagram (a design plan's
	// before/after diagram, keen-noodling-russell.md): the bracket alias's
	// own parenthetical must survive, because the space-paren strip is a
	// MEMBER rule, not a title rule.
	key, title := parseClassDecl(`VariantSplitter_OLD["VariantSplitter (before: 1680 lines)"]`)
	assert.Equal(t, "VariantSplitter_OLD", key)
	assert.Equal(t, "VariantSplitter (before: 1680 lines)", title,
		"the bracket alias's parenthetical must survive — the space-paren strip only applies to members")
}

func TestParseClassDecl_Generic_TildeKeptInTitleStrippedFromKey(t *testing.T) {
	// The title keeps the type parameter (it is what the author documented);
	// the KEY drops it, because relations name the bare class — see
	// TestClassTranspiler_GenericDeclaration_RelationResolvesToTheSameBox.
	key, title := parseClassDecl("Repo~T~")
	assert.Equal(t, "Repo", key)
	assert.Equal(t, "Repo~T~", title)
}

func TestClassStripGeneric_Table(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain generic", "Repo~T~", "Repo"},
		{"nested generic collapses to the bare name", "Map~String,List~Int~~", "Map"},
		{"no tilde at all", "Repo", "Repo"},
		{"tilde inside but not trailing", "Re~po", "Re~po"},
		{"leading tilde leaves nothing to keep", "~T~", "~T~"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classStripGeneric(tc.input))
		})
	}
}

func TestClassTranspiler_GenericDeclaration_RelationResolvesToTheSameBox(t *testing.T) {
	// Real corpus shape (magnolia-content-model's README): the class is
	// declared generic, every relation names it bare. Keying the declaration
	// WITH its type parameter split one class into two boxes — an empty one
	// carrying all the relations, and a floating one holding the stereotype
	// and members.
	source := "classDiagram\n" +
		"    class ScalarContentProperty~T~ {\n" +
		"        <<abstract>>\n" +
		"        -T defaultValue\n" +
		"    }\n" +
		"    ContentProperty <|-- ScalarContentProperty\n"

	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newClassTranspiler(b))

	assert.Equal(t, []string{"ScalarContentProperty", "ContentProperty"}, b.order,
		"the generic declaration and the bare relation operand must be one node, not two")
	n := b.nodes["ScalarContentProperty"]
	require.NotNil(t, n)
	assert.Equal(t, "ScalarContentProperty~T~", n.title, "the title keeps the type parameter")
	assert.Equal(t, "«abstract»", n.stereotype)
	assert.Equal(t, []string{"-T defaultValue"}, n.labelLines)
	assert.Contains(t, b.edges,
		flowchartEdge{from: "ScalarContentProperty", to: "ContentProperty", label: classInheritanceLabel})
}

func TestClassOperand_GenericSpelledOnTheRelation_ResolvesToTheBareKey(t *testing.T) {
	// The mirror case: the relation spells the type parameter, the
	// declaration does not. Both must normalize to the same key.
	key, cardinality := classOperand("ScalarContentProperty~T~")
	assert.Equal(t, "ScalarContentProperty", key)
	assert.Empty(t, cardinality)
}

func TestParseClassDecl_StyleSuffix_StrippedFromKeyAndTitle(t *testing.T) {
	key, title := parseClassDecl("Animal:::highlight")
	assert.Equal(t, "Animal", key)
	assert.Equal(t, "Animal", title)
}

func TestParseClassDecl_EmptyAfterStyleSuffixStrip_YieldsEmptyKey(t *testing.T) {
	// classDeclFromStatement's own doc comment: ok is false when
	// "parseClassDecl could not extract a usable key (an empty declaration
	// after the style-suffix strip)" — e.g. a malformed "class :::onlyStyle"
	// line with no real identifier at all.
	key, title := parseClassDecl(":::onlyStyle")
	assert.Empty(t, key)
	assert.Empty(t, title)
}

// --- classDiagram transpiler: parseClassRelation, the approved worked example ---

func TestParseClassRelation_RendererImplementsGit_Example(t *testing.T) {
	fromKey, toKey, word, cardinalitySuffix, ok := parseClassRelation("Renderer <|-- Git")
	require.True(t, ok)
	assert.Equal(t, "Git", fromKey)
	assert.Equal(t, "Renderer", toKey)
	assert.Equal(t, "implements", word)
	assert.Empty(t, cardinalitySuffix)
}

func TestClassTranspiler_RendererImplementsGit_EmittedSourceUsesFlippedSyntheticEdge(t *testing.T) {
	// Node ids in the emitted flowchart source are always synthetic (n0,
	// n1, ...), never the real class name — see flowchartBuilder's doc
	// comment — so "Git -->|implements| Renderer" shows up as synthetic ids
	// in that SAME flipped order, not as the literal class names. "Git" is
	// declared first (n0) because declarationOrder emits edge SOURCES
	// before their targets, and the emitted edge is source-to-target.
	source := "classDiagram\n    Renderer <|-- Git\n"
	got, ok := transpileMermaid(source, mermaidUnconstrainedWidth)
	require.True(t, ok)
	assert.Contains(t, got, "n0[Git]")
	assert.Contains(t, got, "n1[Renderer]")
	assert.Contains(t, got, "n0 -->|implements| n1")
}

// --- classDiagram transpiler: parseClassRelation, all fourteen arrows ---

func TestParseClassRelation_AllFourteenArrows_Table(t *testing.T) {
	tests := []struct {
		arrow    string
		wantFrom string
		wantTo   string
		wantWord string
	}{
		{"<|--", "B", "A", "implements"},
		{"--|>", "A", "B", "implements"},
		{"<|..", "B", "A", "implements"},
		{"..|>", "A", "B", "implements"},
		{"*--", "A", "B", "owns"},
		{"--*", "B", "A", "owns"},
		{"o--", "A", "B", "has"},
		{"--o", "B", "A", "has"},
		{"-->", "A", "B", ""},
		{"<--", "B", "A", ""},
		{"..>", "A", "B", "uses"},
		{"<..", "B", "A", "uses"},
		{"--", "A", "B", ""},
		{"..", "A", "B", ""},
	}
	for _, tc := range tests {
		t.Run(tc.arrow, func(t *testing.T) {
			fromKey, toKey, word, _, ok := parseClassRelation("A " + tc.arrow + " B")
			require.True(t, ok)
			assert.Equal(t, tc.wantFrom, fromKey)
			assert.Equal(t, tc.wantTo, toKey)
			assert.Equal(t, tc.wantWord, word)
		})
	}
}

// --- classDiagram transpiler: classCardinality ---

func TestClassCardinality_Table(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"1", "1"},
		{"0..1", "0/1"},
		{"1..*", "1+"},
		{"1..n", "1+"},
		{"*", "n"},
		{"n", "n"},
		{"0..*", "n"},
		{"0..n", "n"},
		{"many", "n"},
		{"?", "?"},                            // not in the table: sanitized (no-op here) and left as-is
		{strings.Repeat("x", 12), "xxxxx..."}, // not in the table: sanitized (no-op) then capped
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			assert.Equal(t, tc.want, classCardinality(tc.raw))
		})
	}

	// the unrecognized-value cap is a named constant, so pin the row above to
	// it rather than to the literal it happens to equal today.
	assert.Equal(t, classCardinalityMaxRunes, utf8.RuneCountInString(classCardinality(strings.Repeat("x", 12))))
}

func TestParseClassRelation_CardinalityOrder_FlipsWithArrow(t *testing.T) {
	// A non-flipping arrow: the pair stays in left-to-right source order.
	_, _, _, suffix, ok := parseClassRelation(`Task "1" --> "0..1" WorkflowRef`)
	require.True(t, ok)
	assert.Equal(t, "1 0/1", suffix)

	// A flipping arrow (<|--): the emitted edge runs right-to-left, so the
	// cardinality pair must flip too, or the FROM side would report the
	// WRONG end's multiplicity.
	_, _, _, flippedSuffix, ok := parseClassRelation(`A "1" <|-- "0..1" B`)
	require.True(t, ok)
	assert.Equal(t, "0/1 1", flippedSuffix)
}

func TestClassTranspiler_ExplicitLabelOverridesTableDefault_CardinalitySuffixStillAppended(t *testing.T) {
	fromKey, toKey, word, suffix, ok := parseClassRelation(`A "1" --> "n" B`)
	require.True(t, ok)
	assert.Equal(t, "A", fromKey)
	assert.Equal(t, "B", toKey)
	assert.Empty(t, word, "the plain association arrow carries no table default")
	assert.Equal(t, "1 n", suffix)

	body, explicit, hasExplicit := classSplitTrailingLabel(`A "1" --> "n" B : mentions`)
	require.True(t, hasExplicit)
	assert.Equal(t, "mentions", explicit)

	fromKey2, toKey2, word2, suffix2, ok2 := parseClassRelation(body)
	require.True(t, ok2)
	assert.Equal(t, fromKey, fromKey2)
	assert.Equal(t, toKey, toKey2)
	assert.Empty(t, word2)
	assert.Equal(t, suffix, suffix2)
	assert.Equal(t, "mentions 1 n", classJoinLabelParts(explicit, suffix2),
		"the explicit label replaces the word, but the cardinality suffix must still be appended")
}

// --- classDiagram transpiler: stereotype hoisted above the class name ---

func TestClassTranspiler_Stereotype_HoistedAboveClassName(t *testing.T) {
	source := "classDiagram\n" +
		"    class Shape {\n" +
		"        <<interface>>\n" +
		"        +area() double\n" +
		"    }\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newClassTranspiler(b))

	n := b.nodes["Shape"]
	require.NotNil(t, n)
	assert.Equal(t, "«interface»", n.stereotype)
	lines := n.renderLabelLines(mermaidLabelMaxRunes)
	require.Len(t, lines, 3)
	assert.Equal(t, "«interface»", lines[0], "stereotype must render above the class name")
	assert.Equal(t, "Shape", lines[1])
	assert.Equal(t, "+area() double", lines[2])
}

func TestClassTranspiler_ColonFormStereotype_HoistedAboveClassName(t *testing.T) {
	// Mirrors the test above but via the top-level "Shape : <<interface>>"
	// colon form (statementColonMember) rather than a class-body line
	// (member) — the plan's Grammar section lists "<<interface>> and other
	// stereotypes" without restricting them to the block-body form, and
	// statementColonMember has its own classStereotype check, separate from
	// member's.
	source := "classDiagram\n" +
		"    class Shape\n" +
		"    Shape : <<interface>>\n" +
		"    Shape : +area() double\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newClassTranspiler(b))

	n := b.nodes["Shape"]
	require.NotNil(t, n)
	assert.Equal(t, "«interface»", n.stereotype)
	assert.Equal(t, []string{"+area() double"}, n.labelLines)
}

func TestClassTranspiler_StatementColonMember_KeyAllStyleSuffixNoIdentifier_Dropped(t *testing.T) {
	// classStripStyleSuffix can reduce a key down to "" when the whole
	// identifier in a "Key : text" colon-member line was nothing but a
	// ":::styleName" suffix (a malformed line with no real class name) —
	// statementColonMember must drop it rather than register a node keyed
	// on the empty string.
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	c := newClassTranspiler(b)
	c.statementColonMember(":::styleOnly", "+bar()")

	assert.True(t, b.empty(), "no node may be registered when the key strips down to empty")
}

// --- classDiagram transpiler: colon member form, namespace attribution, ignored statements ---

func TestClassTranspiler_ColonMemberForm_NoBlockNeeded(t *testing.T) {
	// Verbatim shape from a real corpus classDiagram
	// (magnolia/feature-toggles/ClassDiagram.mmd): "FeatureToggles" is never
	// declared with a "class FeatureToggles" statement anywhere in that
	// file — it only ever appears as a relation endpoint and via the
	// colon-member form, so ensureTitle's fallback is what gives the box a
	// readable title at all.
	source := "classDiagram\n" +
		"    FeatureTogglesProvider --> FeatureToggles : provides\n" +
		"    FeatureToggles : +isEnabled()\n" +
		"    FeatureToggles : +enable()\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newClassTranspiler(b))

	n := b.nodes["FeatureToggles"]
	require.NotNil(t, n)
	assert.Equal(t, "FeatureToggles", n.title)
	assert.Equal(t, []string{"+isEnabled()", "+enable()"}, n.labelLines)
}

func TestClassTranspiler_NamespaceAttribution_ClassInsideNamespaceSameAsTopLevel(t *testing.T) {
	source := "classDiagram\n" +
		"    namespace Ns {\n" +
		"        class Foo {\n" +
		"            +bar() void\n" +
		"        }\n" +
		"    }\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newClassTranspiler(b))

	n := b.nodes["Foo"]
	require.NotNil(t, n)
	assert.Equal(t, "Foo", n.title)
	assert.Equal(t, []string{"+bar() void"}, n.labelLines)
	assert.Len(t, b.nodes, 1, "the namespace itself must never become a node")
}

func TestClassTranspiler_IgnoredStatements_NoteStyleClickClassDefDoNotCorruptDiagram(t *testing.T) {
	source := "classDiagram\n" +
		"    class Foo\n" +
		`    note for Foo "some note: with a colon"` + "\n" +
		"    style Foo fill:#fff\n" +
		"    classDef highlight fill:#f00\n" +
		"    click Foo call callback()\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newClassTranspiler(b))

	assert.Len(t, b.nodes, 1, "only the real \"class Foo\" declaration may produce a node")
	require.Contains(t, b.nodes, "Foo")
	assert.Empty(t, b.nodes["Foo"].labelLines)
}

// --- classDiagram transpiler: style suffix vs. key resolution ---

func TestClassTranspiler_StyleSuffixDeclaration_RelationResolvesToSameNode(t *testing.T) {
	source := "classDiagram\n" +
		"    class Animal:::highlight\n" +
		"    Zoo --> Animal\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newClassTranspiler(b))

	require.Contains(t, b.nodes, "Animal")
	assert.Len(t, b.nodes, 2,
		"the relation must resolve \"Animal\" to the SAME node as the declaration, not create a second box")
}

func TestClassTranspiler_RelationResolvesByKeyNotAlias(t *testing.T) {
	source := "classDiagram\n" +
		`    class Foo["Display Name"]` + "\n" +
		"    Foo --> Bar\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newClassTranspiler(b))

	require.Contains(t, b.nodes, "Foo")
	assert.Equal(t, "Display Name", b.nodes["Foo"].title)
	assert.NotContains(t, b.nodes, "Display Name",
		"the relation must key off \"Foo\" (the declaration's identifier), never its display alias")
}

// --- classDiagram transpiler: direction dropped ---

func TestTranspileClassDiagram_DirectionStatement_DroppedHeaderAlwaysFlowchartTD(t *testing.T) {
	source := "classDiagram\n" +
		"    direction LR\n" +
		"    class A\n" +
		"    class B\n" +
		"    A --> B\n"
	got, ok := transpileMermaid(source, mermaidUnconstrainedWidth)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(got, "flowchart TD\n"))
	assert.NotContains(t, got, "direction")
}

// --- classDiagram transpiler: classMaxMembers overflow row, end to end ---

func TestTranspileClassDiagram_NineteenMembers_CappedWithOverflowRow(t *testing.T) {
	var body strings.Builder
	body.WriteString("classDiagram\n    class Big {\n")
	for i := 1; i <= 19; i++ {
		fmt.Fprintf(&body, "        +m%d : String\n", i)
	}
	body.WriteString("    }\n")

	got, ok := transpileMermaid(body.String(), mermaidUnconstrainedWidth)
	require.True(t, ok)
	assert.Contains(t, got, "+m12 : String")
	assert.NotContains(t, got, "m13", "the 13th member and beyond must collapse into the overflow row")
	assert.Contains(t, got, "... +7 more")
}

// --- stateDiagram-v2 transpiler: parseStateTransition ---

func TestParseStateTransition_Table(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantFrom  string
		wantTo    string
		wantLabel string
	}{
		{"no label", "Draft --> InReview", "Draft", "InReview", ""},
		// Both colon spacings occur in the real corpus (publication-request.md
		// uses the spaced form, live-copy.md's diagram uses the tight one)
		// and must parse identically.
		{"spaced colon label", "Draft --> InReview : submit", "Draft", "InReview", "submit"},
		{"tight colon label", "[*] --> Draft: open", "[*]", "Draft", "open"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fromRaw, toRaw, label, ok := parseStateTransition(tc.line)
			require.True(t, ok)
			assert.Equal(t, tc.wantFrom, fromRaw)
			assert.Equal(t, tc.wantTo, toRaw)
			assert.Equal(t, tc.wantLabel, label)
		})
	}
}

func TestParseStateTransition_NotATransition_NoArrow(t *testing.T) {
	_, _, _, ok := parseStateTransition("state Foo")
	assert.False(t, ok)
}

// --- stateDiagram-v2 transpiler: [*] folding ---

func TestTranspileStateDiagram_MultipleStartAndEndPseudoStates_FoldToTwoNodes(t *testing.T) {
	// Verbatim shape from the real corpus (publication-request.md's lifecycle
	// diagram): two distinct left-side "[*]" transitions and three distinct
	// right-side ones. Mermaid treats every left-side "[*]" as the SAME
	// start pseudo-state and every right-side one as the SAME end — without
	// the fold, this exact shape produces five disconnected stub nodes
	// instead of two shared ones (see the plan's "[*] folding is essential"
	// note).
	source := "stateDiagram-v2\n" +
		"    [*] --> Draft\n" +
		"    [*] --> InReview\n" +
		"    Published --> [*]\n" +
		"    Rejected --> [*]\n" +
		"    Withdrawn --> [*]\n"

	got, ok := transpileMermaid(source, mermaidUnconstrainedWidth)
	require.True(t, ok)
	assert.Equal(t, 1, strings.Count(got, "(start)"), "all left-side [*] mentions must fold to exactly one start node")
	assert.Equal(t, 1, strings.Count(got, "(end)"), "all right-side [*] mentions must fold to exactly one end node")
}

func TestStateNodeKey_Table(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		scope    string
		isTarget bool
		want     string
	}{
		{"pseudo-state as source folds to start", "[*]", "", false, stateStartKey},
		{"pseudo-state as target folds to end", "[*]", "", true, stateEndKey},
		{"real state name as source is unchanged", "Draft", "", false, "Draft"},
		{"real state name as target is unchanged", "Draft", "", true, "Draft"},
		{"pseudo-state inside a composite is scoped to it", "[*]", "Active", false, stateStartKey + "Active"},
		{"end pseudo-state inside a composite is scoped to it", "[*]", "Active", true, stateEndKey + "Active"},
		{"real state name is never scoped", "NumLockOff", "Active", false, "NumLockOff"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, stateNodeKey(tc.raw, tc.scope, tc.isTarget))
		})
	}
}

func TestStateTranspiler_CompositeOwnPseudoState_DoesNotFoldWithTheDiagramLevelOne(t *testing.T) {
	// The canonical mermaid composite-state example. The diagram-level "[*]"
	// enters Active; Active's OWN "[*]" enters NumLockOff, which is Active's
	// internal start, not a second entry point into the diagram. Folding both
	// into one node (the first version of stateNodeKey) drew a false
	// "(start) --> NumLockOff" arrow straight past Active into its interior.
	source := "stateDiagram-v2\n" +
		"    [*] --> Active\n" +
		"    state Active {\n" +
		"        [*] --> NumLockOff\n" +
		"        NumLockOff --> NumLockOn : EvNumLockPressed\n" +
		"    }\n"

	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newStateTranspiler(b))

	diagramStart := b.nodes[stateStartKey]
	require.NotNil(t, diagramStart, "the diagram-level start pseudo-state must exist")
	compositeStart := b.nodes[stateStartKey+"Active"]
	require.NotNil(t, compositeStart, "the composite's own start pseudo-state must be its own node")
	assert.NotEqual(t, diagramStart.id, compositeStart.id,
		"a composite's [*] is its own internal start and must not share a node with the diagram's")
	assert.Equal(t, stateStartLabel, compositeStart.title,
		"a scoped pseudo-state still renders with the readable (start) label")

	// the diagram-level start reaches Active and nothing else
	var fromDiagramStart []string
	for _, e := range b.edges {
		if e.from == stateStartKey {
			fromDiagramStart = append(fromDiagramStart, e.to)
		}
	}
	assert.Equal(t, []string{"Active"}, fromDiagramStart,
		"the diagram-level start must not gain a second arrow into the composite's interior")

	// the composite owns its own start, and that start is what reaches NumLockOff
	assert.Contains(t, b.edges, flowchartEdge{from: "Active", to: stateStartKey + "Active", label: "contains"})
	assert.Contains(t, b.edges, flowchartEdge{from: stateStartKey + "Active", to: "NumLockOff", label: ""})
}

// --- stateDiagram-v2 transpiler: parseStateDecl ---

func TestParseStateDecl_QuotedAlias(t *testing.T) {
	key, title, annotation := parseStateDecl(`"Long description" as X`)
	assert.Equal(t, "X", key)
	assert.Equal(t, "Long description", title)
	assert.Empty(t, annotation)
}

func TestParseStateDecl_ForkAnnotation(t *testing.T) {
	key, title, annotation := parseStateDecl("Fork1 <<fork>>")
	assert.Equal(t, "Fork1", key)
	assert.Equal(t, "Fork1", title)
	assert.Equal(t, "«fork»", annotation, "fork/join/choice render as an ordinary box with the annotation as a label line, same as classDiagram's stereotype notation")
}

func TestParseStateDecl_JoinAnnotation(t *testing.T) {
	key, title, annotation := parseStateDecl("Join1 <<join>>")
	assert.Equal(t, "Join1", key)
	assert.Equal(t, "Join1", title)
	assert.Equal(t, "«join»", annotation)
}

func TestParseStateDecl_Bare(t *testing.T) {
	key, title, annotation := parseStateDecl("Active")
	assert.Equal(t, "Active", key)
	assert.Equal(t, "Active", title)
	assert.Empty(t, annotation)
}

func TestStateTranspiler_BareStateDeclarationWithAnnotation_AppendsAnnotationLabelLine(t *testing.T) {
	// "state Choice1 <<choice>>" as a standalone top-level statement, with no
	// trailing "{" — so it goes through statement()'s stateDeclFromStatement
	// branch and applyStateDecl's annotation append, never blockHeader. The
	// plan's Grammar section lists bare "state X <<fork>>" as its own shape,
	// distinct from the composite-block form that
	// TestStateTranspiler_CompositeBlock_FlattensWithContainsEdge exercises;
	// every other fork/join/choice test in this file only calls
	// parseStateDecl directly, never through the full transpiler dispatch.
	source := "stateDiagram-v2\n" +
		"    state Choice1 <<choice>>\n" +
		"    Draft --> Choice1\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newStateTranspiler(b))

	n := b.nodes["Choice1"]
	require.NotNil(t, n)
	assert.Equal(t, "Choice1", n.title)
	assert.Equal(t, []string{"«choice»"}, n.labelLines)
}

// --- stateDiagram-v2 transpiler: direction dropped ---

func TestTranspileStateDiagram_DirectionStatement_DroppedHeaderAlwaysFlowchartTD(t *testing.T) {
	source := "stateDiagram-v2\n" +
		"    direction LR\n" +
		"    A --> B\n"
	got, ok := transpileMermaid(source, mermaidUnconstrainedWidth)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(got, "flowchart TD\n"))
	assert.NotContains(t, got, "direction")
}

// --- stateDiagram-v2 transpiler: state description appended to the box ---

func TestStateTranspiler_ColonDescription_AppendedAsLabelLine(t *testing.T) {
	source := "stateDiagram-v2\n" +
		"    Active : the request is being actively worked\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newStateTranspiler(b))

	n := b.nodes["Active"]
	require.NotNil(t, n)
	assert.Equal(t, "Active", n.title)
	assert.Equal(t, []string{"the request is being actively worked"}, n.labelLines)
}

// --- stateDiagram-v2 transpiler: composite blocks flatten with contains edges ---

func TestStateTranspiler_CompositeBlock_FlattensWithContainsEdge(t *testing.T) {
	source := "stateDiagram-v2\n" +
		"    state Active {\n" +
		"        Working --> Paused\n" +
		"    }\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newStateTranspiler(b))

	require.Contains(t, b.nodes, "Active")
	require.Contains(t, b.nodes, "Working")
	require.Contains(t, b.nodes, "Paused")

	var containsFromActive []string
	for _, e := range b.edges {
		if e.from == "Active" && e.label == "contains" {
			containsFromActive = append(containsFromActive, e.to)
		}
	}
	assert.ElementsMatch(t, []string{"Working", "Paused"}, containsFromActive,
		"both states first seen inside the composite must get a contains edge from it")
}

func TestStateTranspiler_NestedCompositeBlocks_ContainsEdgesAtEachLevel(t *testing.T) {
	source := "stateDiagram-v2\n" +
		"    state Outer {\n" +
		"        state Inner {\n" +
		"            A --> B\n" +
		"        }\n" +
		"    }\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newStateTranspiler(b))

	hasEdge := func(from, to, label string) bool {
		for _, e := range b.edges {
			if e.from == from && e.to == to && e.label == label {
				return true
			}
		}
		return false
	}
	assert.True(t, hasEdge("Outer", "Inner", "contains"), "Inner must be attributed to Outer, the composite open when Inner's own header is parsed")
	assert.True(t, hasEdge("Inner", "A", "contains"), "A must be attributed to Inner, not Outer, once nesting has moved one level deeper")
	assert.True(t, hasEdge("Inner", "B", "contains"))
	assert.False(t, hasEdge("Outer", "A", "contains"), "A is not a DIRECT child of Outer — only Inner is")
}

func TestStateTranspiler_ClosingBrace_RestoresOuterCompositeAfterNestedCloses(t *testing.T) {
	// A state declared AFTER a nested composite closes, but still inside the
	// outer one, must attribute to the OUTER composite, not to the just-closed
	// inner one and not to the top level — this is what proves the "}"
	// handling restores the right stack entry rather than merely resetting to
	// "".
	source := "stateDiagram-v2\n" +
		"    state Outer {\n" +
		"        state Inner {\n" +
		"            A --> B\n" +
		"        }\n" +
		"        C --> D\n" +
		"    }\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newStateTranspiler(b))

	hasEdge := func(from, to, label string) bool {
		for _, e := range b.edges {
			if e.from == from && e.to == to && e.label == label {
				return true
			}
		}
		return false
	}
	assert.True(t, hasEdge("Outer", "C", "contains"))
	assert.True(t, hasEdge("Outer", "D", "contains"))
}

// --- stateDiagram-v2 transpiler: notes skipped in both forms ---

func TestStateTranspiler_SingleLineNote_Skipped(t *testing.T) {
	source := "stateDiagram-v2\n" +
		"    Active --> Done\n" +
		`    note right of Active : this note must not create a node` + "\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newStateTranspiler(b))

	assert.Len(t, b.nodes, 2, "the single-line note must not add a third node")
}

func TestStateTranspiler_MultiLineNote_SkippedUntilEndNote(t *testing.T) {
	source := "stateDiagram-v2\n" +
		"    Active --> Done\n" +
		"    note left of Active\n" +
		"        this line is note body\n" +
		"        A --> B\n" + // even a transition-shaped line inside the note body must not be parsed
		"    end note\n" +
		"    Done --> [*]\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newStateTranspiler(b))

	assert.NotContains(t, b.nodes, "A", "a transition-shaped line inside an open note must be swallowed as note body, not parsed")
	assert.NotContains(t, b.nodes, "B")
	require.Contains(t, b.nodes, "Done", "parsing must resume normally after \"end note\" closes the note")
	require.Contains(t, b.nodes, stateEndKey, "the transition after the note must still be parsed")
}

// --- stateDiagram-v2 transpiler: ignored statements ---

func TestStateTranspiler_IgnoredStatements_ClassDefStyleAccTitleDoNotCorruptDiagram(t *testing.T) {
	source := "stateDiagram-v2\n" +
		"    Active --> Done\n" +
		"    classDef highlight fill:#f00\n" +
		"    style Active fill:#fff\n" +
		"    class Active highlight\n" +
		"    accTitle: Lifecycle\n" +
		"    title My State Diagram\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newStateTranspiler(b))

	assert.Len(t, b.nodes, 2, "only Active and Done may become nodes")
}

func TestStateTranspiler_ConcurrencySeparator_Ignored(t *testing.T) {
	source := "stateDiagram-v2\n" +
		"    state Active {\n" +
		"        [*] --> NumLockOff\n" +
		"        --\n" +
		"        [*] --> CapsLockOff\n" +
		"    }\n"
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	scanMermaidBlocks(source, newStateTranspiler(b))

	assert.NotContains(t, b.nodes, "--")
	require.Contains(t, b.nodes, "NumLockOff")
	require.Contains(t, b.nodes, "CapsLockOff")
}

// --- stateDiagram-v2 transpiler: six verbatim corpus labels ---

func TestStateTranspiler_SixVerbatimCorpusLabels_ShortenedForms(t *testing.T) {
	// One transition label from each of the six real stateDiagram-v2 corpus
	// files the plan's Overview counts ("stateDiagram-v2 6"). Each label is
	// embedded in a full transition line and run through parseStateTransition
	// then the shared mermaidEdgeLabel, exactly as flowchartBuilder.source
	// would at emission time.
	//
	// Row 3 ("open · C1 (Request)") is the one label in this set that
	// ALREADY carries a literal middle dot in its raw corpus text. The
	// cut-at-paren step leaves "open · C1"; the space-to-middle-dot step then
	// converts BOTH spaces surrounding that pre-existing dot too, which on
	// its own would yield three consecutive dots rather than the one the
	// author wrote. mermaidEdgeLabel now collapses any run of two or more
	// "·" into one (see mermaidDotRun), so this shortens to "open·C1" —
	// matching the plan's own worked example — via the shared
	// mermaidEdgeLabel, not a stateDiagram-specific rule this task
	// introduces.
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"publication-request.md: submit", "submit (pins version, starts workflow)", "submit"},
		{"architecture-proposal.md: resolve", "resolve {outcome}  (act without claiming)", "resolve"},
		{"workflow-policy-brain-sketch.md: open · C1", "open · C1 (Request)", "open·C1"},
		{"live-copy.md: reattach", "reattach (rebase / keep-base)", "reattach"},
		{"workflow-rest-layered/architecture.md: decision==approve", "decision==approve<br/>AND publicationDate in future", "decision==approve"},
		{"four-eyes-api-interactions.md: resolve", "resolve (approve/reject/abort)", "resolve"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line := "A --> B : " + tc.input
			_, _, label, ok := parseStateTransition(line)
			require.True(t, ok)
			assert.Equal(t, tc.want, mermaidEdgeLabel(label, mermaidLabelMaxRunes))
		})
	}
}

// --- transpileMermaid ---

func TestTranspileMermaid_ClassDiagram_NowTranspilesToFlowchart(t *testing.T) {
	// Task 2 pinned classDiagram as "recognized but not handled" — that was
	// only ever describing Task 2's deliberately temporary stub state. Task
	// 3 wires classTranspiler in, so this same minimal source now DOES
	// transpile; this test replaces the earlier "not handled" pin.
	got, ok := transpileMermaid("classDiagram\n    class Foo", mermaidUnconstrainedWidth)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(got, "flowchart TD\n"))
	assert.Contains(t, got, "n0[Foo]")
}

func TestTranspileMermaid_StateDiagramV2_NowTranspilesToFlowchart(t *testing.T) {
	// Task 2 pinned stateDiagram-v2 as "recognized but not handled" — that
	// was only ever describing Task 2's deliberately temporary stub state
	// (see classDiagram's equivalent pin above, replaced the same way in
	// Task 3). Task 4 wires stateTranspiler in, so this same minimal source
	// now DOES transpile; this test replaces the earlier "not handled" pin.
	got, ok := transpileMermaid("stateDiagram-v2\n    [*] --> A", mermaidUnconstrainedWidth)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(got, "flowchart TD\n"))
	assert.Contains(t, got, "(start)")
}

func TestTranspileMermaid_StateDiagramWithoutV2Suffix_AlsoRecognized(t *testing.T) {
	// Mermaid accepts both "stateDiagram" and "stateDiagram-v2" for the same
	// grammar (the plan's Grammar section lists both) — transpileMermaid's
	// switch must match both keywords, not just the "-v2" one every real
	// corpus fence happens to use.
	got, ok := transpileMermaid("stateDiagram\n    [*] --> A", mermaidUnconstrainedWidth)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(got, "flowchart TD\n"))
}

func TestTranspileMermaid_UnrecognizedKind_NotHandled(t *testing.T) {
	got, ok := transpileMermaid("graph TD\n    A --> B", mermaidUnconstrainedWidth)
	assert.False(t, ok)
	assert.Empty(t, got)
}

func TestTranspileMermaid_ClassDiagram_AllStatementsIgnored_BuilderEmpty_NotHandled(t *testing.T) {
	// The plan's Failure modes table has a row for "recognized kind, builder
	// empty" (a classDiagram whose every line is a comment or something
	// classIgnoredStatement drops, so scanMermaidBlocks never dispatches a
	// single node/edge into the builder). transpileMermaid must report "not
	// handled" here exactly like an unrecognized kind, letting
	// renderMermaidSource fall back to the ORIGINAL classDiagram source
	// (which the vendored parser's "unsupported graph type" check then
	// rejects — see TestRenderMermaidFences_ClassDiagram_AllStatementsIgnored_FallsBackVerbatim
	// for the full pipeline down to the verbatim fence text).
	got, ok := transpileMermaid("classDiagram\n    %% just a comment\n", mermaidUnconstrainedWidth)
	assert.False(t, ok)
	assert.Empty(t, got)
}

func TestTranspileMermaid_StateDiagram_AllStatementsIgnored_BuilderEmpty_NotHandled(t *testing.T) {
	// Same "builder empty" failure mode as the classDiagram case above, for
	// stateDiagram-v2.
	got, ok := transpileMermaid("stateDiagram-v2\n    %% just a comment\n", mermaidUnconstrainedWidth)
	assert.False(t, ok)
	assert.Empty(t, got)
}

// --- renderMermaidSource ---

func TestRenderMermaidSource_Graph_ByteIdenticalToDirectRenderDiagram(t *testing.T) {
	src := "graph TD\n    A --> B"
	want, err := mermaidcmd.RenderDiagram(src, nil)
	require.NoError(t, err)

	got, err := renderMermaidSource(src, 80)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestRenderMermaidSource_Flowchart_ByteIdenticalToDirectRenderDiagram(t *testing.T) {
	src := "flowchart LR\n    A --> B"
	want, err := mermaidcmd.RenderDiagram(src, nil)
	require.NoError(t, err)

	got, err := renderMermaidSource(src, 80)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestRenderMermaidSource_SequenceDiagram_ByteIdenticalToDirectRenderDiagram(t *testing.T) {
	// sequenceDiagram takes an entirely different code path in the vendored
	// library (diagram.go's DiagramFactory checks sequence.IsSequenceDiagram
	// before ever looking at "graph "/"flowchart " prefixes) — this pins
	// that our new dispatch does not disturb it.
	src := "sequenceDiagram\n    Alice->>Bob: Hello Bob"
	want, err := mermaidcmd.RenderDiagram(src, nil)
	require.NoError(t, err)

	got, err := renderMermaidSource(src, 80)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestRenderMermaidSource_MalformedDiagram_WrapsUnderlyingError(t *testing.T) {
	_, err := renderMermaidSource("this is not a valid mermaid diagram at all", 80)
	require.Error(t, err)

	// the wrapping itself is the contract, not just "an error came back":
	// the caller (renderMermaidBlock) logs nothing and silently falls back,
	// so the renderer's own message is the only diagnostic that survives.
	require.ErrorContains(t, err, "render mermaid diagram:", "the error must carry this function's own context")
	require.Error(t, errors.Unwrap(err), "the underlying renderer error must stay unwrappable, not be flattened into a string")
}

// --- Task 5: integration against the real renderer ---
//
// Everything above this point exercises the transpiler's own pieces in
// isolation (string in, string out) or, at most, transpileMermaid's decision
// of whether to hand off synthetic flowchart source at all. The tests below
// go one step further and actually call the real, vendored
// mermaidcmd.RenderDiagram — through renderMermaidSource and
// renderMermaidFences, exactly as production code does — so a mismatch
// between what this file's own sanitizing rules assume and what the real
// parser/renderer actually does cannot hide behind a unit test that never
// left our own code.

func TestRenderMermaidSource_ClassDiagram_RendersRealBoxArt(t *testing.T) {
	src := "classDiagram\n" +
		"    class Renderer {\n" +
		"        <<interface>>\n" +
		"        +render() void\n" +
		"    }\n" +
		"    class Git {\n" +
		"        +render() void\n" +
		"    }\n" +
		"    Renderer <|-- Git\n"

	got, err := renderMermaidSource(src, mermaidUnconstrainedWidth)
	require.NoError(t, err)
	assert.Contains(t, got, "Renderer")
	assert.Contains(t, got, "Git")
	assert.Contains(t, got, "│", "the real renderer must draw a box-drawing character, not just plain text")
}

func TestRenderMermaidSource_StateDiagram_RendersRealBoxArt(t *testing.T) {
	src := "stateDiagram-v2\n" +
		"    [*] --> Draft\n" +
		"    Draft --> Done : submit\n" +
		"    Done --> [*]\n"

	got, err := renderMermaidSource(src, mermaidUnconstrainedWidth)
	require.NoError(t, err)
	assert.Contains(t, got, "Draft")
	assert.Contains(t, got, "Done")
	assert.Contains(t, got, "(start)")
	assert.Contains(t, got, "(end)")
	assert.Contains(t, got, "│", "the real renderer must draw a box-drawing character, not just plain text")
}

func TestRenderMermaidSource_ClassDiagram_BracketAliasEmbeddedQuote_SurvivesIntact(t *testing.T) {
	// Regression pin for the VENDORED parser's own quote-eating behavior
	// (parse.go:128's strings.Trim(labelText, `"`)) — see the plan's
	// Sanitizing note: an odd count of quotes in a NODE label stops the
	// closing "]" from decrementing bracketDepth and swallows the rest of
	// the diagram, and even a balanced pair loses its trailing quote to the
	// Trim call. classTranspiler's own parseClassDecl mimics that same
	// outer-quote trim to pull the display text out of a bracket alias, but
	// mermaidSafeText then deletes every remaining quote character —
	// including one the author embedded INSIDE the display text — so by the
	// time this synthesized flowchart source reaches the real renderer there
	// is no quote left at all for parse.go:128 to eat. Triggered here
	// through the bracket alias, which is its most likely real source; this
	// is NOT about an edge label.
	src := "classDiagram\n" +
		`    class Foo["Display "Name""]` + "\n" +
		"    class Bar\n" +
		"    Foo --> Bar\n"

	got, err := renderMermaidSource(src, mermaidUnconstrainedWidth)
	require.NoError(t, err)
	assert.Contains(t, got, "Display Name",
		"the label must survive whole, not truncated or split at the embedded quote")
	assert.NotContains(t, got, `"`, "no quote character may ever reach the rendered art")
}

func TestRenderMermaidSource_ClassDiagram_CardinalityEdgeLabel_NoArrowBleed(t *testing.T) {
	// "owns 1 n" is exactly the shape the plan's Probe finding #5 measured
	// as clean under both TD and LR once every space becomes a middle dot;
	// this pins that same shape against the REAL renderer's output, not just
	// the label string mermaidEdgeLabel/classComposeCardinality build.
	src := "classDiagram\n" +
		"    class Repo\n" +
		"    class Item\n" +
		`    Repo "1" *-- "n" Item` + "\n"

	got, err := renderMermaidSource(src, mermaidUnconstrainedWidth)
	require.NoError(t, err)
	assert.Contains(t, got, "owns·1·n", "the composed cardinality label must appear as one contiguous, unbroken run")
	assert.NotContains(t, got, "owns│1", "a box-drawing vertical must never bleed through the middle of the label")
}

func TestRenderMermaidSource_AdversarialSources_NeverPanic(t *testing.T) {
	// None of these are well-formed diagrams (several are deliberately
	// malformed), so success or failure of the render itself is not the
	// point — the only assertion is that nothing panics anywhere in the
	// pipeline: transpileMermaid's parsing, flowchartBuilder's level/cap
	// math, or the real vendored renderer it hands off to.
	tests := []struct {
		name string
		src  string
	}{
		{
			"unbalanced [ in a class declaration",
			"classDiagram\n    class Foo[Bar\n    class Baz\n    Foo --> Baz\n",
		},
		{
			"member line of only pipe/bracket/brace/angle/quote punctuation",
			"classDiagram\n    class Foo {\n        |[]{}<>\"\n    }\n",
		},
		{
			"300-character member",
			"classDiagram\n    class Foo {\n        " + strings.Repeat("x", 300) + "\n    }\n",
		},
		{
			"relation missing an operand",
			"classDiagram\n    class Foo\n    --> Foo\n",
		},
		{
			"start-to-end pseudostate edge",
			"stateDiagram-v2\n    [*] --> [*]\n",
		},
		{
			"self-transition",
			"stateDiagram-v2\n    A --> A\n",
		},
		{
			"three parallel edges between one pair",
			"classDiagram\n    class Foo\n    class Bar\n" +
				"    Foo --> Bar : one\n    Foo --> Bar : two\n    Foo --> Bar : three\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				_, _ = renderMermaidSource(tc.src, mermaidUnconstrainedWidth)
			})
		})
	}
}

// The six tests below drive the two REAL production entry points, at a real
// pane width, rather than renderMermaidFences — which production never calls
// and which always passes mermaidUnconstrainedWidth, so the adaptive label
// cap that production always runs would never be exercised (see
// renderMermaidFences's own doc comment).
//
// Two entry points, because the assertions want different things.
// renderMarkdownDocument is the whole path a P keypress takes, so the two
// "now renders" tests use it to see the art in place, surrounded by real
// prose. The four "still falls back" tests want byte-exact fence text, which
// glamour's own indentation would destroy, so they use
// mermaidPlaceholderDocument and read the collected art slice directly — the
// same values renderMarkdownDocument splices back in verbatim.

// mdPreviewPaneWidth is a realistic diff-pane width for the tests below: a
// real number, never the mermaidUnconstrainedWidth sentinel, so the adaptive
// cap runs the way it does in production.
const mdPreviewPaneWidth = 80

func TestRenderMarkdownDocument_ClassDiagram_NoLongerFallsBackVerbatim(t *testing.T) {
	src := "classDiagram\n    class Foo\n    class Bar\n    Foo --> Bar\n"
	doc := "before\n```mermaid\n" + src + "```\nafter"

	got := renderMarkdownDocument(mdLines(doc), mdPreviewPaneWidth, true)

	assert.NotContains(t, got, "classDiagram", "the raw fence body must not survive once the diagram transpiles and renders")
	assert.NotContains(t, got, "```mermaid", "the fence marker itself should be gone once rendered")
	assert.Contains(t, got, "│", "box art must appear in place of the fence")
	assert.Contains(t, got, "Foo")
	assert.Contains(t, got, "Bar")
	assert.Contains(t, got, "before")
	assert.Contains(t, got, "after")
}

func TestRenderMarkdownDocument_StateDiagram_NoLongerFallsBackVerbatim(t *testing.T) {
	src := "stateDiagram-v2\n    [*] --> Draft\n    Draft --> Done : submit\n    Done --> [*]\n"
	doc := "before\n```mermaid\n" + src + "```\nafter"

	got := renderMarkdownDocument(mdLines(doc), mdPreviewPaneWidth, true)

	assert.NotContains(t, got, "stateDiagram", "the raw fence body must not survive once the diagram transpiles and renders")
	assert.NotContains(t, got, "```mermaid", "the fence marker itself should be gone once rendered")
	assert.Contains(t, got, "│", "box art must appear in place of the fence")
	assert.Contains(t, got, "(start)")
	assert.Contains(t, got, "(end)")
	assert.Contains(t, got, "before")
	assert.Contains(t, got, "after")
}

// mdPreviewFenceArt renders doc through the production placeholder path at a
// real pane width and returns the single collected diagram art — the exact
// bytes renderMarkdownDocument splices back into the rendered page.
func mdPreviewFenceArt(t *testing.T, doc string) string {
	t.Helper()
	_, arts := mermaidPlaceholderDocument(mdLines(doc), "testnonce", mdPreviewPaneWidth)
	require.Len(t, arts, 1, "the document must contain exactly one mermaid fence")
	return arts[0]
}

func TestMermaidPlaceholderDocument_ErDiagram_StillFallsBackVerbatim(t *testing.T) {
	// pins the deliberate scope decision: this patch covers classDiagram and
	// stateDiagram-v2 only (see the plan's Overview) — erDiagram is
	// unaffected and must still take the pre-existing fallback path.
	src := "erDiagram\n    CUSTOMER ||--o{ ORDER : places\n"
	doc := "```mermaid\n" + src + "```"

	assert.Equal(t, doc+"\n", mdPreviewFenceArt(t, doc), "erDiagram is out of this patch's scope and must still fall back verbatim")
}

func TestMermaidPlaceholderDocument_Gantt_StillFallsBackVerbatim(t *testing.T) {
	// pins the deliberate scope decision: gantt is unaffected by this patch
	// and must still take the pre-existing fallback path.
	src := "gantt\n    title A Gantt Diagram\n    section Section\n    A task :a1, 2024-01-01, 30d\n"
	doc := "```mermaid\n" + src + "```"

	assert.Equal(t, doc+"\n", mdPreviewFenceArt(t, doc), "gantt is out of this patch's scope and must still fall back verbatim")
}

func TestMermaidPlaceholderDocument_QuadrantChart_StillFallsBackVerbatim(t *testing.T) {
	// pins the deliberate scope decision: quadrantChart is the third kind the
	// plan's Overview names as out of scope (alongside erDiagram and gantt
	// above) and must still take the pre-existing fallback path. Before this
	// test, quadrantChart was only ever exercised at the mermaidDiagramKind
	// extraction level (TestMermaidDiagramKind_Table) — never through the
	// full render pipeline.
	src := "quadrantChart\n    title Reach and engagement\n"
	doc := "```mermaid\n" + src + "```"

	assert.Equal(t, doc+"\n", mdPreviewFenceArt(t, doc), "quadrantChart is out of this patch's scope and must still fall back verbatim")
}

func TestMermaidPlaceholderDocument_ClassDiagram_AllStatementsIgnored_FallsBackVerbatim(t *testing.T) {
	// Full pipeline for the Failure modes table's "recognized kind, builder
	// empty" row (see TestTranspileMermaid_ClassDiagram_AllStatementsIgnored_BuilderEmpty_NotHandled
	// for the transpileMermaid-level pin): a classDiagram fence with nothing
	// for the builder to use falls all the way back to the original fence
	// text, exactly like an unrecognized kind — never an error, never a
	// panic, never a half-rendered diagram.
	src := "classDiagram\n    %% just a comment\n"
	doc := "```mermaid\n" + src + "```"

	assert.Equal(t, doc+"\n", mdPreviewFenceArt(t, doc),
		"a classDiagram fence the builder never populates must fall back verbatim, not error or panic")
}

func TestRenderMermaidSource_ManyMemberClass_RendersWithinFortyCells(t *testing.T) {
	// Pins the width arithmetic from the plan's "The 19-member class"
	// section against a future renderer change. The plan's own worked
	// example there was a real 19-member corpus class; Task 1's probe found
	// that file actually has 18 members today (see the Probe findings
	// table) — both counts exceed classMaxMembers (12) and produce an
	// identical capped shape, so this fixture uses its own synthetic member
	// count (mirroring TestTranspileClassDiagram_NineteenMembers_CappedWithOverflowRow's
	// own synthetic "Big" class from Task 3) rather than hardcoding either
	// historical figure.
	var body strings.Builder
	body.WriteString("classDiagram\n    class Task {\n")
	for i := 1; i <= 18; i++ {
		fmt.Fprintf(&body, "        +member%d() void\n", i)
	}
	body.WriteString("    }\n")

	got, err := renderMermaidSource(body.String(), 80)
	require.NoError(t, err)

	maxWidth := 0
	for l := range strings.SplitSeq(got, "\n") {
		if n := len([]rune(l)); n > maxWidth {
			maxWidth = n
		}
	}
	assert.LessOrEqual(t, maxWidth, 40, "a single member-heavy class must stay within 40 cells wide")
}

// mermaidArtWidth returns the widest rendered line of a box-art render, in
// display cells — trailing spaces stripped first so padding is not counted as
// content.
func mermaidArtWidth(art string) int {
	widest := 0
	for l := range strings.SplitSeq(art, "\n") {
		if n := xansi.StringWidth(strings.TrimRight(l, " ")); n > widest {
			widest = n
		}
	}
	return widest
}

// --- honest width behavior on REAL corpus diagrams ---
//
// The synthetic fan-in fixtures the cap formula was derived from (see
// TestMermaidLabelCap_AdaptiveTable_80ColumnPane) do fit an 80-column pane at
// k=2 and k=3. Most real corpus diagrams still do not. The tests below use
// verbatim corpus fences and pin the measured truth on both sides: one
// diagram that the topological declaration order brought back inside the
// pane, and one that stays wider than the pane even with the cap on its
// floor.
//
// Measured over every classDiagram/stateDiagram fence in the author's
// document corpus (18 fences), rendered at a paneWidth of 80: widths 36, 50,
// 59, 73, 81, 84, 92, 101, 101, 102, 103, 106, 109, 125, 137, 144, 182, 210
// — four of eighteen fit, median 102. So the cap reduces overflow but does
// not prevent it, at any k. These tests exist so that stays written down in
// executable form: the earlier synthetic-only pinning is exactly why the plan
// carried a wrong "only k>=4 clips" claim through implementation.

func TestRenderMermaidSource_RealCorpusStateDiagram_TopologicalOrderBringsArtInsidePane(t *testing.T) {
	// Verbatim from /Users/sasha/dev/mx/publication-requests/docs/architecture/
	// 2026-07-10-workflow-policy-brain-sketch.md.
	//
	// This fence is the clearest single measurement of what the topological
	// declaration order buys. Under the old "edge sources first" order the
	// renderer flattened it, three states shared the widest row, and the art
	// measured 88 cells against an 80-column pane. Under the topological
	// order the levels come out right and the same fence measures 73 cells —
	// it fits. The cap is still doing work (it sits on its floor here), but
	// it is no longer being asked to rescue a layout that was wrong.
	source := `stateDiagram-v2
    [*] --> Draft: open · C1 (Request)
    Draft --> InReview: submit + validate · C2 (Request/Content)
    InReview --> ChangesRequested: gate concludes · C3 (Engine)
    ChangesRequested --> InReview: revise (Request)
    InReview --> Approved: quorum met · C3 (Engine)
    InReview --> Rejected: hard reject · C3 (Engine)
    Approved --> Scheduled: has publicationDate · C4 (Scheduler)
    Approved --> Published: publish · C5 (Publishing)
    Scheduled --> Published: tick due · C5 (Scheduler)
    Published --> [*]: close + notify · C6 (Request/Notifications)
    Draft --> Withdrawn: withdraw (Request)`

	b := newFlowchartBuilder(80)
	scanMermaidBlocks(source, newStateTranspiler(b))
	k := b.topology().widestLevel()
	require.Equal(t, 3, k, "this corpus diagram's widest layout level holds three states")
	assert.Equal(t, mermaidLabelMinRunes, mermaidLabelCap(80, k, b.hasAnyEdgeLabel()),
		"the adaptive cap must still shrink to its floor here — do not weaken or delete it")

	art, err := renderMermaidSource(source, 80)
	require.NoError(t, err)
	width := mermaidArtWidth(art)
	assert.LessOrEqual(t, width, 80,
		"the topological declaration order brings this fence inside an 80-column pane (measured 73); "+
			"a regression here means the declaration order stopped being topological, got %d", width)
}

func TestRenderMermaidSource_RealCorpusClassDiagram_FloorCapStillOverflowsPane(t *testing.T) {
	// Verbatim from /Users/sasha/dev/magnolia/dam/docs/plans/
	// 2026-07-28-dam-source-refactor-design.md. Widest layout level holds 3
	// nodes, so the cap is already on its floor (16 runes) and cannot shrink
	// any further — mermaidLabelMinRunes is what stops it. The plan's table
	// predicted 78 cells, "fits 80".
	source := `classDiagram
    class AssetProviderRegistry {
        <<interface>>
        +getProviderById(String) AssetProvider
        +getProviderFor(ItemKey) AssetProvider
        +getProvidersFor(MediaType...) Iterator
        +getRendererFor(Asset, MediaType) AssetRenderer
    }
    class AssetProvider {
        <<interface>>
        +getAsset(ItemKey) Asset
        +list(AssetQuery) Iterator
        +provides(MediaType) boolean
        +getRendererFor(Asset, MediaType) AssetRenderer
        +isEnabled() boolean
    }
    class Asset {
        <<interface>>
        +getLink() String
        +getContentStream() InputStream
        +getBinaryReference() BinaryReference
        +getTitle() String
    }
    class Item {
        <<interface>>
        +getParent() Folder
        +getAssetProvider() AssetProvider
    }
    class Folder {
        <<interface>>
        +getChildren() Iterator
        +getItem(String) Item
        +isRoot() boolean
    }
    class WithMutableContent {
        <<interface>>
        +setContent(InputStream) CompletableFuture
    }

    AssetProviderRegistry --> AssetProvider : looks up by id or media type
    AssetProvider --> Asset : returns
    Item <|-- Asset
    Item <|-- Folder
    Asset <|.. WithMutableContent : cast to write
    Item --> AssetProvider : points back`

	b := newFlowchartBuilder(80)
	scanMermaidBlocks(source, newClassTranspiler(b))
	k := b.topology().widestLevel()
	require.Equal(t, 3, k, "this corpus diagram's widest layout level holds three classes")
	assert.Equal(t, mermaidLabelMinRunes, mermaidLabelCap(80, k, b.hasAnyEdgeLabel()),
		"the cap is already on its floor here and has nothing left to give")

	art, err := renderMermaidSource(source, 80)
	require.NoError(t, err)
	width := mermaidArtWidth(art)
	assert.Greater(t, width, 80,
		"honest behavior: even on the cap's floor a real k=3 corpus classDiagram overflows an 80-column pane (measured 81)")
	assert.Less(t, width, 130, "sanity band — a much larger number means the cap stopped working entirely, got %d", width)
}

// --- accepted limitation: the vendored renderer draws one edge label per
// shared routing row ---
//
// The transpiler emits every edge label it parsed. The vendored renderer then
// routes all of one node's outgoing edges along a single horizontal row and
// writes labels onto that row, so a fan-out to several DIFFERENT targets ends
// up with only one label drawn — and past about four targets the labels
// overwrite each other into a token that is not any of them.
//
// This is inside mermaid-ascii (mapping_edge.go / draw.go), not in anything
// this patch owns, so it is documented rather than fixed — see PATCH.md's
// Known limitations and the plan's Failure-modes table. Measured over the
// author's 18-fence corpus at a pane width of 80: 10 of 18 fences lose at
// least one edge label this way. The tests below pin the behavior so a future
// vendor bump that changes it does not pass silently.

func TestRenderMermaidSource_FanOutToDifferentTargets_VendoredRendererDropsEdgeLabels(t *testing.T) {
	source := "classDiagram\n" +
		"    A --> B : uses\n" +
		"    A --> C : owns\n" +
		"    A --> D : reads\n"

	transpiled, ok := transpileMermaid(source, mermaidUnconstrainedWidth)
	require.True(t, ok)
	for _, label := range []string{"uses", "owns", "reads"} {
		require.Contains(t, transpiled, "|"+label+"|", "the transpiler emits every label; the loss is downstream")
	}

	art, err := renderMermaidSource(source, mermaidUnconstrainedWidth)
	require.NoError(t, err)

	drawn := 0
	for _, label := range []string{"uses", "owns", "reads"} {
		if strings.Contains(art, label) {
			drawn++
		}
	}
	assert.Less(t, drawn, 3,
		"accepted limitation: the renderer cannot draw one label per fan-out edge, got %d of 3 in\n%s", drawn, art)
}

func TestRenderMermaidSource_WideFanOut_VendoredRendererMergesEdgeLabelsIntoOneToken(t *testing.T) {
	// Past about four targets the dropped labels do not merely disappear:
	// each one is written over the previous one on the same routing row, and
	// what is left is a token that appears in no label at all. Here six
	// labels collapse to "pollss" — the tail of "polls" printed over the
	// tail of an earlier label.
	source := "classDiagram\n" +
		"    First --> B : contains\n" +
		"    First --> C : needs\n" +
		"    First --> D : emits\n" +
		"    First --> E : uses\n" +
		"    First --> F : sends\n" +
		"    First --> G : polls\n"

	art, err := renderMermaidSource(source, mermaidUnconstrainedWidth)
	require.NoError(t, err)

	assert.Contains(t, art, "pollss",
		"pins the merge artifact: a token that is in none of the six labels\n%s", art)
	for _, label := range []string{"needs", "emits", "uses", "sends"} {
		assert.NotContains(t, art, label, "the middle labels are overwritten entirely")
	}
	assert.Contains(t, art, "contains", "only the straight-down edge keeps its own label")
}

func TestRenderMarkdownDocument_ClassDiagramArtSurvivesGlamourWithoutReflow(t *testing.T) {
	// Mirrors TestRenderMarkdownDocument_MermaidArtSurvivesGlamourWithoutReflow
	// (mdpreview_test.go:180) for a transpiled diagram type. One real
	// difference from that test: paneWidth is inert for flowchart/graph
	// source (transpileMermaid never touches it, so "want" there can be
	// computed at any width), but it DOES feed classDiagram's adaptive label
	// cap here (mermaidLabelCap) — so "want" must be computed at the exact
	// same paneWidth renderMarkdownDocument uses internally
	// (mermaidPlaceholderDocument threads its own width parameter straight
	// through to renderMermaidBlock/renderMermaidSource), not at
	// mermaidUnconstrainedWidth, which would silently pin a different cap
	// than what actually gets rendered and spliced in.
	// Two implementors (k=2, a real fan-in) rather than one: at k=1 the
	// renderer stacks Git above Renderer vertically (18 cells wide, under
	// narrowWidth) rather than side by side, which would make the fixture
	// sanity check below vacuous. Two implementors force the wide-fan-in
	// layout the plan's adaptive cap is actually shaped around.
	const narrowWidth = 20
	src := "classDiagram\n" +
		"    class Renderer {\n" +
		"        <<interface>>\n" +
		"        +render() void\n" +
		"    }\n" +
		"    class Git {\n" +
		"        +render() void\n" +
		"    }\n" +
		"    class Hg {\n" +
		"        +render() void\n" +
		"    }\n" +
		"    Renderer <|-- Git\n" +
		"    Renderer <|-- Hg\n"

	want, err := renderMermaidSource(src, narrowWidth)
	require.NoError(t, err)
	require.NotEmpty(t, want)

	maxArtWidth := 0
	for l := range strings.SplitSeq(want, "\n") {
		if n := len([]rune(l)); n > maxArtWidth {
			maxArtWidth = n
		}
	}
	require.Greater(t, maxArtWidth, narrowWidth,
		"test fixture sanity: the diagram must be wider than narrowWidth or this test cannot detect reflow")

	doc := "before\n\n```mermaid\n" + src + "```\n\nafter\n"

	got := renderMarkdownDocument(mdLines(doc), narrowWidth, false)
	stripped := xansi.Strip(got)

	assert.Contains(t, stripped, want,
		"the diagram must reach the output byte-exact: unwrapped, unreflowed, untruncated")
	assert.Contains(t, stripped, "before")
	assert.Contains(t, stripped, "after")
}

// --- Review fixes: parsing hazards found after Task 6 ---

func TestParseClassRelation_QuotedCardinalityRangeOnLeftOperand_ArrowStillWins(t *testing.T) {
	// A dotted range on the LEFT operand puts a literal ".." earlier in the
	// line than the real arrow. Searched unmasked, the leftmost-match rule
	// picks that ".." as the relation token and splits the line into two
	// garbage operands ("Customer \"0" and "11..*\" Order"), losing the real
	// relation entirely. classMaskQuotedSegments is what keeps the arrow the
	// only thing findable.
	tests := []struct {
		name                       string
		body                       string
		wantFrom, wantTo, wantWord string
		wantCardinality            string
	}{
		{"dotted range left of a directed arrow", `Customer "0..1" --> "1..*" Order`, "Customer", "Order", "", "0/1 1+"},
		{"dotted range both sides of an undirected arrow", `Student "1..*" -- "1..*" Course`, "Student", "Course", "", "1+ 1+"},
		{"dotted range left of a flipping arrow", `Order "1..*" <|-- "1" Invoice`, "Invoice", "Order", classInheritanceLabel, "1 1+"},
		{"dotted range only on the right", `Task "1" --> "0..1" WorkflowRef`, "Task", "WorkflowRef", "", "1 0/1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			from, to, word, cardinality, ok := parseClassRelation(tc.body)
			require.True(t, ok)
			assert.Equal(t, tc.wantFrom, from)
			assert.Equal(t, tc.wantTo, to)
			assert.Equal(t, tc.wantWord, word)
			assert.Equal(t, tc.wantCardinality, cardinality)
		})
	}
}

func TestTranspileClassDiagram_QuotedCardinalityRangeOnLeftOperand_NoGarbageNodes(t *testing.T) {
	src := "classDiagram\n" + `    Customer "0..1" --> "1..*" Order` + "\n"

	got, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
	require.True(t, ok)

	assert.Equal(t, "flowchart TD\nn0[Customer]\nn1[Order]\nn0 -->|0/1·1+| n1\n", got,
		"exactly two real nodes, not four boxes split out of the cardinality text")
}

func TestParseClassRelation_NoArrow_NotOK(t *testing.T) {
	// the "line carries no relation arrow at all" return path — reached in
	// production every time classTranspiler.statement offers it an ordinary
	// colon-member line before falling through to statementColonMember.
	from, to, word, cardinality, ok := parseClassRelation("JustAClassName")
	assert.False(t, ok)
	assert.Empty(t, from)
	assert.Empty(t, to)
	assert.Empty(t, word)
	assert.Empty(t, cardinality)
}

func TestClassTranspiler_ColonMemberWithDotsOrDashes_NotMisroutedToRelationParser(t *testing.T) {
	// Member text routinely contains ".." (a range) or "--" (a dash run).
	// Testing the arrow pattern against the WHOLE line, label included, sends
	// these to the relation parser, which finds no arrow in the body and
	// drops the member without a trace.
	tests := []struct {
		name, statement, wantKey, wantMember string
	}{
		{"ellipsis in member text", "Foo : +args ...", "Foo", "+args ..."},
		{"numeric range in member text", "Config : +retries 0..3", "Config", "+retries 0..3"},
		{"dash run in member text", "Foo : +note -- deprecated", "Foo", "+note -- deprecated"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := newFlowchartBuilder(mermaidUnconstrainedWidth)
			newClassTranspiler(b).statement(tc.statement)

			require.Equal(t, []string{tc.wantKey}, b.order)
			assert.Equal(t, tc.wantKey, b.nodes[tc.wantKey].title)
			assert.Equal(t, []string{tc.wantMember}, b.nodes[tc.wantKey].labelLines)
		})
	}
}

func TestClassTranspiler_ExplicitLabelWithParenthetical_KeepsCardinalitySuffix(t *testing.T) {
	// the plan's rule is that an explicit "  : label" replaces the arrow
	// table's word and nothing else — the cardinality is still appended. The
	// parenthetical cut has to run on the word alone, before the suffix is
	// glued on, or it takes the cardinality down with it.
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	newClassTranspiler(b).statement(`A "1" --> "n" B : resolve {outcome} (act without claiming)`)

	require.Len(t, b.edges, 1)
	assert.Equal(t, "resolve 1 n", b.edges[0].label)
	assert.Contains(t, b.source(), "n0 -->|resolve·1·n| n1")
}

func TestClassComposeCardinality_AsymmetricSides(t *testing.T) {
	// the plan allows a cardinality on "either end" independently, so all
	// four combinations have to compose without a stray leading or trailing
	// space that would later become a middle dot.
	tests := []struct {
		name, fromCard, toCard, want string
	}{
		{"both sides", "1", "0..1", "1 0/1"},
		{"source side only", "1", "", "1"},
		{"target side only", "", "1..*", "1+"},
		{"neither side", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, classComposeCardinality(tc.fromCard, tc.toCard))
		})
	}
}

func TestClassTranspiler_AsymmetricCardinality_OnlyOneOperandQuoted(t *testing.T) {
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	newClassTranspiler(b).statement(`Task "1" --> WorkflowRef`)

	require.Len(t, b.edges, 1)
	assert.Equal(t, "1", b.edges[0].label, "the one quoted side survives alone, with no empty second part")
}

// --- Review fixes: keyword matching must respect word boundaries ---

func TestMermaidKeywordPrefix_Table(t *testing.T) {
	tests := []struct {
		name, text, keyword string
		want                bool
	}{
		{"exact match", "note", "note", true},
		{"followed by space", "note left of A", "note", true},
		{"followed by colon", "accTitle: Lifecycle", "accTitle", true},
		{"followed by a letter", "notes --> done", "note", false},
		{"followed by a digit", "style2 --> done", "style", false},
		{"followed by an underscore", "note_box --> done", "note", false},
		{"not a prefix at all", "Draft --> Done", "note", false},
		{"longer keyword still matches its own word", "classDef hi fill:red", "classDef", true},
		{"shorter keyword stops at the longer word", "classDef hi fill:red", "class", false},

		// kebab-case, dotted and path-like names are ordinary identifiers in
		// real diagrams, and a hyphen/dot/slash right after a keyword must
		// therefore NOT read as the end of that keyword.
		{"kebab-case name", "style-review --> title-approval", "style", false},
		{"kebab-case name, second keyword", "title-approval --> [*]", "title", false},
		{"kebab-case note", "note-taking --> done", "note", false},
		{"dotted name", "link.check --> done", "link", false},
		{"path-like name", "style/guide --> done", "style", false},
		{"kebab-case class directive name", "class-registry --> done", "class", false},
		{"real directive with a hyphen in its ARGUMENT still matches", "style link-check fill:#fff", "style", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mermaidKeywordPrefix(tc.text, tc.keyword))
		})
	}
}

func TestStateTranspiler_KebabCaseStateNames_SurviveTheIgnoredKeywordCheck(t *testing.T) {
	// End to end through the transpiler: "style-review" and "title-approval"
	// both open with a stateDiagram ignored-statement keyword. Treating the
	// hyphen as a word boundary dropped both transitions silently, so a state
	// and two transitions vanished from a diagram that still looked complete.
	src := "stateDiagram-v2\n" +
		"    [*] --> draft\n" +
		"    draft --> link-check\n" +
		"    link-check --> style-review\n" +
		"    style-review --> title-approval\n" +
		"    title-approval --> [*]\n"

	got, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
	require.True(t, ok)

	for _, want := range []string{"[draft]", "[link-check]", "[style-review]", "[title-approval]", "[(start)]", "[(end)]"} {
		assert.Contains(t, got, want)
	}
	assert.Equal(t, 5, strings.Count(got, "-->"), "all five transitions must survive")

	// the real directives they shadow must still be dropped
	withDirectives := "stateDiagram-v2\n    style-review --> done\n    style done fill:#fff\n    title Lifecycle\n"
	gotDirectives, ok := transpileMermaid(withDirectives, mermaidUnconstrainedWidth)
	require.True(t, ok)
	assert.Contains(t, gotDirectives, "[style-review]")
	assert.NotContains(t, gotDirectives, "fill")
	assert.NotContains(t, gotDirectives, "Lifecycle")
}

func TestClassTranspiler_KebabCaseAndDottedClassNames_SurviveTheIgnoredKeywordCheck(t *testing.T) {
	// same rule on the classDiagram side, whose ignored list adds "link",
	// "click", "href" and "callback" to the shared style/title/note set.
	src := "classDiagram\n" +
		"    link-resolver --> href.builder\n" +
		"    click/handler --> note-store\n" +
		"    callback_queue --> titleCase\n"

	got, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
	require.True(t, ok)

	for _, want := range []string{"[link-resolver]", "[href.builder]", "[click/handler]", "[note-store]"} {
		assert.Contains(t, got, want)
	}
	assert.Equal(t, 3, strings.Count(got, "-->"), "all three relations must survive")
}

func TestStateTranspiler_StateNameStartingWithNote_DoesNotSwallowTheDiagram(t *testing.T) {
	// the worst bare-prefix failure: "notes" starts with "note", so a
	// prefix match opens a multi-line note block that no "end note" ever
	// closes — and every remaining line of the diagram is discarded.
	src := "stateDiagram-v2\n    notes --> done\n    done --> archived\n"

	got, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
	require.True(t, ok)

	assert.Contains(t, got, "n0[notes]")
	assert.Contains(t, got, "n1[done]")
	assert.Contains(t, got, "n2[archived]", "the lines after the state named like a keyword must still render")
}

func TestStateTranspiler_NoteWithoutLeftOrRightOf_DropsOnlyThatLine(t *testing.T) {
	// a "note" statement in a shape this patch does not recognize is still
	// dropped, but it must not open a note block: only the exact
	// "note left/right of X" opener may suppress following lines.
	src := "stateDiagram-v2\n    note something odd\n    Draft --> Done\n"

	got, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
	require.True(t, ok)

	assert.NotContains(t, got, "something odd")
	assert.Contains(t, got, "n0[Draft]")
	assert.Contains(t, got, "n1[Done]", "the line after an unrecognized note statement must still render")
}

func TestStateTranspiler_StateNamesStartingWithIgnoredKeywords_NotDropped(t *testing.T) {
	tests := []struct{ name, src, wantNode string }{
		{"style prefix", "stateDiagram-v2\n    styleGuide --> Done\n", "styleGuide"},
		{"title prefix", "stateDiagram-v2\n    titleFetch --> Done\n", "titleFetch"},
		{"direction prefix", "stateDiagram-v2\n    directionUp --> Done\n", "directionUp"},
		{"class prefix", "stateDiagram-v2\n    classroom --> Done\n", "classroom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := transpileMermaid(tc.src, mermaidUnconstrainedWidth)
			require.True(t, ok)
			assert.Contains(t, got, "n0["+tc.wantNode+"]")
			assert.Contains(t, got, "n1[Done]")
		})
	}
}

func TestClassTranspiler_ClassNamesStartingWithIgnoredKeywords_NotDropped(t *testing.T) {
	tests := []struct{ name, src, wantNode string }{
		{"note prefix", "classDiagram\n    notes --> Done\n", "notes"},
		{"link prefix", "classDiagram\n    linker --> Done\n", "linker"},
		{"style prefix", "classDiagram\n    styleGuide --> Done\n", "styleGuide"},
		{"title prefix", "classDiagram\n    titleCase --> Done\n", "titleCase"},
		{"href prefix", "classDiagram\n    hrefBox --> Done\n", "hrefBox"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := transpileMermaid(tc.src, mermaidUnconstrainedWidth)
			require.True(t, ok)
			assert.Contains(t, got, "n0["+tc.wantNode+"]")
			assert.Contains(t, got, "n1[Done]")
		})
	}
}

func TestIgnoredStatements_RealDirectivesStillDropped(t *testing.T) {
	// the word-boundary change must not weaken the directives the two
	// ignore lists exist for in the first place.
	classDirectives := []string{
		"note for Foo \"text\"", "click Foo call cb()", "callback Foo cb", "link Foo \"url\"",
		"href Foo \"url\"", "style Foo fill:#f9f", "cssClass \"Foo\" hi", "classDef hi fill:red",
		"accTitle: A title", "accDescr: A description", "title My Diagram",
	}
	for _, text := range classDirectives {
		assert.True(t, classIgnoredStatement(text), "classDiagram must still ignore %q", text)
	}

	stateDirectives := []string{
		"classDef hi fill:red", "class Draft hi", "style Draft fill:#fff",
		"accTitle: A title", "accDescr: A description", "title My Diagram",
	}
	for _, text := range stateDirectives {
		assert.True(t, stateIgnoredStatement(text), "stateDiagram must still ignore %q", text)
	}
}

// --- Relations parse before the ignored-keyword lists are consulted ---
//
// A whole-word keyword match still cannot separate a node named EXACTLY like
// a directive from the directive itself: "note --> Done" and "note right of
// X : text" both open with the bare word plus a space. The dispatch order is
// what separates them — a line that parses as a relation or transition is a
// relation, whatever its first token is called. These tests pin both halves:
// the nodes that must survive, and the directives that must still drop.

func TestStateTranspiler_StateNamedExactlyAKeyword_TransitionSurvives(t *testing.T) {
	tests := []struct{ name, src, wantFrom, wantTo string }{
		{"note", "stateDiagram-v2\n    note --> Done\n", "note", "Done"},
		{"style", "stateDiagram-v2\n    style --> review\n", "style", "review"},
		{"title", "stateDiagram-v2\n    title --> approved\n", "title", "approved"},
		{"direction", "stateDiagram-v2\n    direction --> up\n", "direction", "up"},
		{"class", "stateDiagram-v2\n    class --> loaded\n", "class", "loaded"},
		{"classDef", "stateDiagram-v2\n    classDef --> applied\n", "classDef", "applied"},
		{"accTitle", "stateDiagram-v2\n    accTitle --> shown\n", "accTitle", "shown"},
		{"as the target", "stateDiagram-v2\n    Draft --> note\n", "Draft", "note"},
		{"with a label", "stateDiagram-v2\n    style --> review : submit\n", "style", "review"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := transpileMermaid(tc.src, mermaidUnconstrainedWidth)
			require.True(t, ok, "the diagram must not come out empty")
			assert.Contains(t, got, "n0["+tc.wantFrom+"]")
			assert.Contains(t, got, "n1["+tc.wantTo+"]")
			assert.Contains(t, got, "n0 -->", "the transition itself must be recorded")
		})
	}
}

func TestStateTranspiler_StateNamedExactlyNote_DoesNotSwallowTheRestOfTheDiagram(t *testing.T) {
	// the worst case of the whole class: "note" as a transition subject used
	// to reach startNoteIfAny, which dropped the line — and, for a shape
	// without a colon, would have opened a note block nothing ever closes.
	src := "stateDiagram-v2\n    note --> Done\n    Done --> archived\n"

	got, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
	require.True(t, ok)

	assert.Contains(t, got, "n0[note]")
	assert.Contains(t, got, "n1[Done]")
	assert.Contains(t, got, "n2[archived]")
	assert.Equal(t, 2, strings.Count(got, "-->"), "both transitions must survive")
}

func TestStateTranspiler_KeywordLikeStateNames_StillSurvive(t *testing.T) {
	// the round-3 cases: a longer identifier that merely STARTS with a
	// keyword, including the kebab-case shapes mermaidIdentRune covers.
	tests := []struct{ name, src, wantFrom, wantTo string }{
		{"plural", "stateDiagram-v2\n    notes --> done\n", "notes", "done"},
		{"kebab both sides", "stateDiagram-v2\n    style-review --> title-approval\n", "style-review", "title-approval"},
		{"camel", "stateDiagram-v2\n    styleGuide --> titleFetch\n", "styleGuide", "titleFetch"},
		{"dotted", "stateDiagram-v2\n    class.Registry --> link.check\n", "class.Registry", "link.check"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := transpileMermaid(tc.src, mermaidUnconstrainedWidth)
			require.True(t, ok)
			assert.Contains(t, got, "n0["+tc.wantFrom+"]")
			assert.Contains(t, got, "n1["+tc.wantTo+"]")
		})
	}
}

func TestStateTranspiler_RealDirectives_StillDroppedEndToEnd(t *testing.T) {
	// every directive kind that carries no transition arrow must still be
	// dropped after the reordering, and must not disturb the diagram around
	// it. The anchor transition "Draft --> Done" is checked on every row so a
	// directive that swallowed following lines would show up here.
	tests := []struct{ name, directive, mustNotContain string }{
		{"single-line note", "note right of Draft : needs review", "needs"},
		{"style", "style Draft fill:#f9f", "fill"},
		{"title", "title My Diagram", "My"},
		{"direction", "direction LR", "LR"},
		{"classDef", "classDef foo bold", "bold"},
		{"class css", "class Draft cssClassName", "cssClassName"},
		{"accTitle", "accTitle: Lifecycle", "Lifecycle"},
		{"accDescr", "accDescr: How it flows", "flows"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := "stateDiagram-v2\n    Draft --> Done\n    " + tc.directive + "\n    Done --> archived\n"

			got, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
			require.True(t, ok)

			assert.NotContains(t, got, tc.mustNotContain, "the directive's own text must not reach the output")
			assert.Contains(t, got, "n0[Draft]")
			assert.Contains(t, got, "n1[Done]")
			assert.Contains(t, got, "n2[archived]", "the line after the directive must still render")
			assert.Equal(t, 2, strings.Count(got, "-->"))
		})
	}
}

func TestStateTranspiler_MultiLineNote_StillSkippedUntilEndNote(t *testing.T) {
	// the multi-line opener has no colon and no arrow, so it still reaches
	// startNoteIfAny and still suppresses its body — including a body line
	// that looks like a transition, which belongs to the note, not the
	// diagram.
	src := "stateDiagram-v2\n" +
		"    Draft --> Done\n" +
		"    note left of Draft\n" +
		"      body text\n" +
		"      Ghost --> Phantom\n" +
		"    end note\n" +
		"    Done --> archived\n"

	got, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
	require.True(t, ok)

	assert.NotContains(t, got, "body")
	assert.NotContains(t, got, "Ghost", "a transition inside the note body is note text, not a transition")
	assert.NotContains(t, got, "Phantom")
	assert.Contains(t, got, "n2[archived]", "the line after \"end note\" must still render")
	assert.Equal(t, 2, strings.Count(got, "-->"))
}

func TestClassTranspiler_ClassNamedExactlyAKeyword_RelationSurvives(t *testing.T) {
	tests := []struct{ name, src, wantSubject, wantObject string }{
		// "<|--" flips, so the emitted edge runs object -> subject: n0 is the
		// right-hand class of the source line (see classArrowTable).
		{"note", "classDiagram\n    note <|-- Done\n", "note", "Done"},
		{"style", "classDiagram\n    style <|-- review\n", "style", "review"},
		{"link", "classDiagram\n    link <|-- target\n", "link", "target"},
		{"title", "classDiagram\n    title <|-- approved\n", "title", "approved"},
		{"href", "classDiagram\n    href <|-- resolved\n", "href", "resolved"},
		{"click", "classDiagram\n    click <|-- handled\n", "click", "handled"},
		{"callback", "classDiagram\n    callback <|-- fired\n", "callback", "fired"},
		{"cssClass", "classDiagram\n    cssClass <|-- applied\n", "cssClass", "applied"},
		{"classDef", "classDiagram\n    classDef <|-- applied\n", "classDef", "applied"},
		{"accTitle", "classDiagram\n    accTitle <|-- shown\n", "accTitle", "shown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := transpileMermaid(tc.src, mermaidUnconstrainedWidth)
			require.True(t, ok, "the diagram must not come out empty")
			assert.Contains(t, got, "n0["+tc.wantObject+"]")
			assert.Contains(t, got, "n1["+tc.wantSubject+"]")
			assert.Contains(t, got, "n0 -->|"+classInheritanceLabel+"| n1")
		})
	}
}

func TestClassTranspiler_RelationWithKeywordNamedClass_DoesNotDropFollowingLines(t *testing.T) {
	src := "classDiagram\n    note <|-- Done\n    Done <|-- Archived\n"

	got, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
	require.True(t, ok)

	assert.Contains(t, got, "[note]")
	assert.Contains(t, got, "[Done]")
	assert.Contains(t, got, "[Archived]")
	assert.Equal(t, 2, strings.Count(got, "-->"), "both relations must survive")
}

func TestClassTranspiler_RealDirectives_StillDroppedEndToEnd(t *testing.T) {
	tests := []struct{ name, directive, mustNotContain string }{
		{"floating note", `note "a floating note"`, "floating"},
		{"note for", `note for Base "explains Base"`, "explains"},
		{"click", "click Base call handler()", "handler"},
		{"callback", "callback Base cb", " cb"},
		{"link", `link Base "https://example.com"`, "example"},
		{"href", `href Base "https://example.com"`, "example"},
		{"style", "style Base fill:#f9f", "fill"},
		{"cssClass", `cssClass "Base" highlight`, "highlight"},
		{"classDef", "classDef highlight fill:red", "highlight"},
		{"title", "title My Diagram", "My"},
		{"direction", "direction LR", "LR"},
		{"accTitle", "accTitle: Model", "Model"},
		{"accDescr", "accDescr: How it hangs together", "hangs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := "classDiagram\n    Base <|-- Mid\n    " + tc.directive + "\n    Mid <|-- Leaf\n"

			got, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
			require.True(t, ok)

			assert.NotContains(t, got, tc.mustNotContain, "the directive's own text must not reach the output")
			assert.Contains(t, got, "[Base]")
			assert.Contains(t, got, "[Mid]")
			assert.Contains(t, got, "[Leaf]", "the line after the directive must still render")
			assert.Equal(t, 2, strings.Count(got, "-->"))
		})
	}
}

func TestClassTranspiler_ClassDeclarationStillWinsOverTheRelationParse(t *testing.T) {
	// "class Foo" is a declaration keyword in classDiagram grammar, not a
	// class name, so the declaration check stays ahead of the relation parse.
	got, ok := transpileMermaid("classDiagram\n    class Repo~T~\n    Repo <|-- GitRepo\n", mermaidUnconstrainedWidth)
	require.True(t, ok)

	assert.Contains(t, got, "[Repo~T~]", "the declaration supplied the title, generic parameter and all")
	assert.Contains(t, got, "[GitRepo]")
	assert.Equal(t, 1, strings.Count(got, "-->"))
}

// --- Review fixes: frontmatter, classDiagram-v2 ---

func TestMermaidStripFrontmatter_Table(t *testing.T) {
	tests := []struct{ name, source, want string }{
		{"no frontmatter", "classDiagram\n  A --> B", "classDiagram\n  A --> B"},
		{"frontmatter stripped", "---\ntitle: X\n---\nclassDiagram\n  A --> B", "classDiagram\n  A --> B"},
		{"leading blank line before frontmatter", "\n---\ntitle: X\n---\nclassDiagram", "classDiagram"},
		{"unterminated frontmatter left alone", "---\ntitle: X\nclassDiagram", "---\ntitle: X\nclassDiagram"},
		{"a later --- is not frontmatter", "classDiagram\n---\n", "classDiagram\n---\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mermaidStripFrontmatter(tc.source))
		})
	}
}

func TestTranspileMermaid_Frontmatter_StillTranspiles(t *testing.T) {
	// without the strip, mermaidDiagramKind reports "---" and the whole
	// diagram falls back verbatim; worse, the frontmatter's own "key: value"
	// lines reach the colon-form fallback and become bogus nodes.
	src := "---\ntitle: Lifecycle\nconfig:\n  theme: dark\n---\nclassDiagram\n    Foo --> Bar\n"

	got, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
	require.True(t, ok)

	assert.Equal(t, "flowchart TD\nn0[Foo]\nn1[Bar]\nn0 --> n1\n", got,
		"no node may be manufactured out of a frontmatter key")
}

func TestTranspileMermaid_StateDiagramWithFrontmatter_StillTranspiles(t *testing.T) {
	src := "---\ntitle: Lifecycle\n---\nstateDiagram-v2\n    [*] --> Draft\n"

	got, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
	require.True(t, ok)
	assert.Contains(t, got, "(start)")
	assert.Contains(t, got, "n1[Draft]")
}

func TestTranspileMermaid_ClassDiagramV2_AlsoRecognized(t *testing.T) {
	// mermaid accepts the explicit "-v2" suffix on classDiagram exactly as it
	// does on stateDiagram, and the switch already handles the stateDiagram
	// pair.
	got, ok := transpileMermaid("classDiagram-v2\n    Foo --> Bar\n", mermaidUnconstrainedWidth)
	require.True(t, ok)
	assert.Equal(t, "flowchart TD\nn0[Foo]\nn1[Bar]\nn0 --> n1\n", got)
}

// --- Review fixes: uncovered branches ---

func TestMermaidLabelCap_ZeroPaneWidth_IsNotTreatedAsUnconstrained(t *testing.T) {
	// a real viewport can report width 0 — or a negative width, since it is
	// computed as `layout.width - treeWidth - 4` — in a degenerate layout
	// state. That means "no room", the opposite of "no limit", so it must
	// fall through to the adaptive formula and clamp to the floor rather than
	// return the ceiling.
	assert.Equal(t, mermaidLabelMinRunes, mermaidLabelCap(0, 1, false))
	assert.Equal(t, mermaidLabelMinRunes, mermaidLabelCap(-1, 1, false))
	assert.Equal(t, mermaidLabelMinRunes, mermaidLabelCap(-40, 3, true))
	assert.Equal(t, mermaidLabelMaxRunes, mermaidLabelCap(mermaidUnconstrainedWidth, 1, false))
	assert.Equal(t, math.MinInt, mermaidUnconstrainedWidth, "the sentinel must be out of every layout's reach")
}

func TestMermaidTruncate_CapAtOrBelowEllipsisLength_NoEllipsis(t *testing.T) {
	// with no room for "..." the ellipsis itself would be the entire result,
	// so the truncation degrades to a plain rune slice.
	assert.Equal(t, "abc", mermaidTruncate("abcdefgh", 3))
	assert.Equal(t, "a", mermaidTruncate("abcdefgh", 1))
	assert.Empty(t, mermaidTruncate("abcdefgh", 0))
	assert.Empty(t, mermaidTruncate("abcdefgh", -1), "a negative cap must not panic on the slice")
}

func TestFlowchartNode_RenderLabelLines_NoStereotypeTitleOrMembers_FallsBackToSyntheticID(t *testing.T) {
	// a node introduced with nothing on it at all would otherwise emit
	// "n0[]", which the vendored parser accepts and draws as an empty box.
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	b.node("Ghost")

	assert.Equal(t, []string{"n0"}, b.nodes["Ghost"].renderLabelLines(mermaidLabelMaxRunes))
	assert.Equal(t, "flowchart TD\nn0[n0]\n", b.source(), "never an empty label between the brackets")
}

func TestFlowchartBuilder_WidestLevel_CycleLaidOutAsAChain(t *testing.T) {
	// A cycle has no topological order, so declarationOrder falls back to
	// first-seen order and the first node becomes the layout root. The
	// renderer then draws the rest as a chain hanging off it, with the
	// closing transition drawn as a back edge — verified against the real
	// renderer, which puts exactly one box on each row for both shapes
	// below. levels() replays that same placement, so k is 1 here, not the
	// node count.
	twoNodeCycle := newFlowchartBuilder(mermaidUnconstrainedWidth)
	twoNodeCycle.addEdge("A", "B", "")
	twoNodeCycle.addEdge("B", "A", "")
	assert.Equal(t, map[string]int{"A": 0, "B": 1}, twoNodeCycle.topology().levels(),
		"the first-seen node roots the chain, the other hangs one level below it")
	assert.Equal(t, 1, twoNodeCycle.topology().widestLevel())

	threeNodeCycle := newFlowchartBuilder(mermaidUnconstrainedWidth)
	threeNodeCycle.addEdge("A", "B", "")
	threeNodeCycle.addEdge("B", "C", "")
	threeNodeCycle.addEdge("C", "A", "")
	assert.Equal(t, 1, threeNodeCycle.topology().widestLevel())

	// a back-transition hanging off a real root behaves the same way: the
	// root keeps level 0 and the cycle below it is laid out in real levels.
	withRoot := newFlowchartBuilder(mermaidUnconstrainedWidth)
	withRoot.addEdge("Start", "A", "")
	withRoot.addEdge("A", "B", "")
	withRoot.addEdge("B", "A", "")
	assert.Equal(t, 1, withRoot.topology().widestLevel(), "one node per level when the cycle hangs off a single root")
}

func TestFlowchartBuilder_DeclarationOrder_Cycle_EveryNodeDeclaredExactlyOnce(t *testing.T) {
	// the cycle fallback must never drop a node and never declare one twice
	// — a missing declaration leaves the emitted source referring to an id
	// that was never introduced.
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	b.addEdge("A", "B", "")
	b.addEdge("B", "C", "")
	b.addEdge("C", "A", "")
	b.setTitle("Loner", "Loner")

	assert.Equal(t, []string{"Loner", "A", "B", "C"}, b.topology().order,
		"the parentless node goes first, then the cycle in first-seen order")
}

func TestFlowchartBuilder_DeclarationOrder_SelfLoop_DoesNotBlockItsOwnNode(t *testing.T) {
	// a state that transitions to itself must not count as its own parent,
	// or it would never become declarable and push the whole diagram onto
	// the cycle fallback path.
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	b.addEdge("Retry", "Retry", "again")
	b.addEdge("Retry", "Done", "")

	assert.Equal(t, []string{"Retry", "Done"}, b.topology().order)
	assert.Equal(t, map[string]int{"Retry": 0, "Done": 1}, b.topology().levels())
}

func TestFlowchartBuilder_DeclarationOrder_ThreeLevelHierarchyDeclaredParentFirst(t *testing.T) {
	// The real corpus shape (magnolia-content-model/README.md): a class
	// hierarchy written parent-first, so the intermediate class is seen as
	// an edge SOURCE before its own children are declared. Note the emitted
	// arrows run child -> parent, because "<|--" flips (see classArrowTable),
	// which is why Leaf is the topological root here and Base the sink.
	//
	// Declaring in first-seen-as-a-source order would put Mid first, making
	// the renderer treat Mid as a layout root and draw it in the same row as
	// its own children. A topological order keeps every source ahead of its
	// target, so the three levels stay three levels.
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	b.addEdge("Mid", "Base", classInheritanceLabel)  // Base <|-- Mid
	b.addEdge("Leaf", "Mid", classInheritanceLabel)  // Mid  <|-- Leaf
	b.addEdge("Leaf2", "Mid", classInheritanceLabel) // Mid  <|-- Leaf2

	order := b.topology().order
	assert.Equal(t, []string{"Leaf", "Leaf2", "Mid", "Base"}, order)

	pos := map[string]int{}
	for i, key := range order {
		pos[key] = i
	}
	for _, e := range b.edges {
		assert.Less(t, pos[e.from], pos[e.to],
			"every edge source must be declared before its target: %s -> %s", e.from, e.to)
	}

	assert.Equal(t, map[string]int{"Leaf": 0, "Leaf2": 0, "Mid": 1, "Base": 2}, b.topology().levels(),
		"three distinct levels, not a flattened two")
	assert.Equal(t, 2, b.topology().widestLevel(), "the two leaves share the widest level")
}

func TestFlowchartBuilder_Source_ThreeLevelHierarchy_ByteExact(t *testing.T) {
	// Companion pin to the test above, on the SOURCE rather than the order.
	// source() derives the declaration order and the child list once now,
	// into a single flowchartTopology, instead of recomputing them per step;
	// this test is what proves the emitted text did not shift by a byte when
	// that caching went in.
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	b.setTitle("Base", "Base")
	b.setTitle("Mid", "Mid")
	b.setTitle("Leaf", "Leaf")
	b.setTitle("Leaf2", "Leaf2")
	b.addEdge("Mid", "Base", classInheritanceLabel)
	b.addEdge("Leaf", "Mid", classInheritanceLabel)
	b.addEdge("Leaf2", "Mid", classInheritanceLabel)

	want := "flowchart TD\n" +
		"n2[Leaf]\n" +
		"n3[Leaf2]\n" +
		"n1[Mid]\n" +
		"n0[Base]\n" +
		"n1 -->|implements| n0\n" +
		"n2 -->|implements| n1\n" +
		"n3 -->|implements| n1\n"
	assert.Equal(t, want, b.source())
}

func TestStateTranspiler_BlockHeaderNotAStateDeclaration_IsTransparent(t *testing.T) {
	// scanMermaidBlocks is shared plumbing, so blockHeader must tolerate a
	// "{" header that is not a "state ..." declaration: it reports false, the
	// block adds no nesting, and nothing inside it is attributed to a
	// composite that was never opened.
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	s := newStateTranspiler(b)

	assert.False(t, s.blockHeader("Foo", 0), "an unrecognized header must be transparent")
	assert.Empty(t, s.currentComposite, "no composite may be opened by an unrecognized header")

	scanMermaidBlocks("stateDiagram-v2\n    Foo {\n        A --> B\n    }\n", newStateTranspiler(b))
	for _, e := range b.edges {
		assert.NotEqual(t, "contains", e.label, "a transparent block must not produce containment edges")
	}
}

func TestRenderMermaidSource_ThreeParallelEdges_RendererKeepsOneLabelPerColumn(t *testing.T) {
	// the plan's Failure-modes table says three edges between the same pair
	// render with one of the labels dropped — the vendored renderer has only
	// so many label columns between two boxes. Pinning it here so a renderer
	// upgrade that changes the behavior is noticed rather than silently
	// altering how real diagrams read.
	src := "classDiagram\n    class Foo\n    class Bar\n" +
		"    Foo --> Bar : one\n    Foo --> Bar : two\n    Foo --> Bar : three\n"

	transpiled, ok := transpileMermaid(src, mermaidUnconstrainedWidth)
	require.True(t, ok)
	for _, label := range []string{"one", "two", "three"} {
		assert.Contains(t, transpiled, "|"+label+"|", "all three edges must reach the renderer")
	}

	got, err := renderMermaidSource(src, mermaidUnconstrainedWidth)
	require.NoError(t, err)
	assert.Contains(t, got, "Foo")
	assert.Contains(t, got, "Bar")

	kept := 0
	for _, label := range []string{"one", "two", "three"} {
		if strings.Contains(got, label) {
			kept++
		}
	}
	assert.Equal(t, 2, kept, "the renderer draws two of the three parallel edge labels and drops the third")
}
