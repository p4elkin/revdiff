package ui

import (
	"fmt"
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

// --- classDiagram transpiler: classIgnoredStatement ---

func TestClassIgnoredStatement_Lollipop(t *testing.T) {
	assert.True(t, classIgnoredStatement("Class1 ()-- Class2"))
	assert.False(t, classIgnoredStatement("A --> B"))
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

func TestParseClassDecl_Generic_TildeKeptVerbatim(t *testing.T) {
	key, title := parseClassDecl("Repo~T~")
	assert.Equal(t, "Repo~T~", key)
	assert.Equal(t, "Repo~T~", title)
}

func TestParseClassDecl_StyleSuffix_StrippedFromKeyAndTitle(t *testing.T) {
	key, title := parseClassDecl("Animal:::highlight")
	assert.Equal(t, "Animal", key)
	assert.Equal(t, "Animal", title)
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
		{strings.Repeat("x", 12), "xxxxx..."}, // not in the table: sanitized (no-op) then capped at 8 runes
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			assert.Equal(t, tc.want, classCardinality(tc.raw))
		})
	}
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
