package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"
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
	// plan's Probe finding #3 exactly: 10 runes / 30 bytes.
	raw := strings.Repeat("≤", 32)
	got := mermaidEdgeLabel(raw, 32)
	assert.Equal(t, strings.Repeat("≤", 10), got)
	assert.LessOrEqual(t, len(got), 32)
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

	assert.Equal(t, 3, b.widestLevel())
}

func TestFlowchartBuilder_DeclarationOrder_FirstSeenAsEdgeSource_PinnedAgainstFanIn(t *testing.T) {
	// A fan-in: B and C are both edge sources (in that order), A is only
	// ever a target. Declaring A before its sources would make graph.go's
	// insertion-order root computation treat A as an extra root too,
	// collapsing the layout — see flowchartBuilder.declarationOrder's doc
	// comment. A must therefore be emitted last.
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	b.addEdge("B", "A", "x")
	b.addEdge("C", "A", "y")

	assert.Equal(t, []string{"B", "C", "A"}, b.declarationOrder())
}

func TestFlowchartBuilder_DeclarationOrder_LeftoverNodeNeverASource_AppearsAfterSources(t *testing.T) {
	b := newFlowchartBuilder(mermaidUnconstrainedWidth)
	b.addEdge("Src", "Dst", "x")
	b.setTitle("Standalone", "Standalone") // never mentioned in any edge, first-seen after Dst

	got := b.declarationOrder()
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

// --- transpileMermaid ---

func TestTranspileMermaid_ClassDiagram_RecognizedButNotHandled(t *testing.T) {
	got, ok := transpileMermaid("classDiagram\n    class Foo", mermaidUnconstrainedWidth)
	assert.False(t, ok)
	assert.Empty(t, got)
}

func TestTranspileMermaid_StateDiagramV2_RecognizedButNotHandled(t *testing.T) {
	got, ok := transpileMermaid("stateDiagram-v2\n    [*] --> A", mermaidUnconstrainedWidth)
	assert.False(t, ok)
	assert.Empty(t, got)
}

func TestTranspileMermaid_UnrecognizedKind_NotHandled(t *testing.T) {
	got, ok := transpileMermaid("graph TD\n    A --> B", mermaidUnconstrainedWidth)
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
}
