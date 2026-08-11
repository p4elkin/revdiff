package ui

import (
	"strings"
	"testing"

	glamourStyles "github.com/charmbracelet/glamour/styles"
	xansi "github.com/charmbracelet/x/ansi"
)

// TestMdPreviewMarkerZeroWidth proves a marker never contributes to a row's
// measured display width — the property the whole mechanism depends on
// (word-wrap and lipgloss must never see it as content).
func TestMdPreviewMarkerZeroWidth(t *testing.T) {
	for _, k := range mdPreviewMarkerKinds {
		m := mdPreviewMarker(k.id)
		if w := xansi.StringWidth(m); w != 0 {
			t.Errorf("kind %s: marker %q has width %d, want 0", k.kind, m, w)
		}
		// also zero-width when surrounded by real content on the row, not
		// just in isolation.
		line := "prefix " + m + " suffix"
		if w := xansi.StringWidth(line); w != len("prefix  suffix") {
			t.Errorf("kind %s: marker changed surrounding line width: got %d, want %d", k.kind, w, len("prefix  suffix"))
		}
	}
}

// TestMdPreviewMarkerDistinctBytes proves every kind's marker is a unique
// byte sequence and none is a substring of another — required for
// mdPreviewStripMarkers' exact-match removal and mdPreviewExtractMarkers'
// per-kind strings.Index scan to never cross-match a different kind.
func TestMdPreviewMarkerDistinctBytes(t *testing.T) {
	seen := map[string]mdPreviewBlockKind{}
	for _, k := range mdPreviewMarkerKinds {
		m := mdPreviewMarker(k.id)
		if other, ok := seen[m]; ok {
			t.Fatalf("kind %s and %s share marker %q", k.kind, other, m)
		}
		seen[m] = k.kind
	}
	for _, a := range mdPreviewMarkerKinds {
		for _, b := range mdPreviewMarkerKinds {
			if a.kind == b.kind {
				continue
			}
			ma, mb := mdPreviewMarker(a.id), mdPreviewMarker(b.id)
			if strings.Contains(ma, mb) {
				t.Errorf("marker for %s (%q) contains marker for %s (%q)", a.kind, ma, b.kind, mb)
			}
		}
	}
}

// TestMdPreviewMarkerKindsCoverage pins the kind table to exactly the set
// this task specifies, and to the style field each kind prepends to. The
// generic "heading" is deliberately absent (glamour never emits
// Heading.Prefix — only H1..H6.Prefix), and so are "list", "document" and
// "text" — see mdPreviewMarkerKinds' doc comment for why. "task" IS present:
// glamour renders a checkbox list item through Styles.Task and never through
// Styles.Item, so without it every checklist document would fail alignment.
// A change here is a scope change to this feature, not routine maintenance.
func TestMdPreviewMarkerKindsCoverage(t *testing.T) {
	want := map[mdPreviewBlockKind]string{
		"paragraph":   "Paragraph.Prefix",
		"h1":          "H1.Prefix",
		"h2":          "H2.Prefix",
		"h3":          "H3.Prefix",
		"h4":          "H4.Prefix",
		"h5":          "H5.Prefix",
		"h6":          "H6.Prefix",
		"item":        "Item.BlockPrefix",
		"enumeration": "Enumeration.BlockPrefix",
		"code_block":  "CodeBlock.BlockPrefix",
		"block_quote": "BlockQuote.Prefix",
		"table":       "Table.Prefix",
		"hr":          "HorizontalRule.Prefix",
		"html_block":  "HTMLBlock.Prefix",
		"task":        "Task.BlockPrefix",
	}
	if len(mdPreviewMarkerKinds) != len(want) {
		t.Fatalf("got %d kinds, want %d", len(mdPreviewMarkerKinds), len(want))
	}
	for _, k := range mdPreviewMarkerKinds {
		field, ok := want[k.kind]
		if !ok {
			t.Errorf("unexpected kind %q in table (excluded kinds: heading, list, document, text)", k.kind)
			continue
		}
		if k.styleField != field {
			t.Errorf("kind %s: styleField = %q, want %q", k.kind, k.styleField, field)
		}
	}
	for name := range want {
		found := false
		for _, k := range mdPreviewMarkerKinds {
			if k.kind == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing kind %q from table", name)
		}
	}
	for _, excluded := range []mdPreviewBlockKind{"heading", "list", "document", "text"} {
		for _, k := range mdPreviewMarkerKinds {
			if k.kind == excluded {
				t.Errorf("kind %q must not be in the table (see doc comment)", excluded)
			}
		}
	}
}

// TestMdPreviewMarkerStripExactAndTotal proves mdPreviewStripMarkers removes
// exactly the marker bytes (nothing else) and removes ALL of them, for
// every kind, and is a no-op on a string that contains no markers at all.
func TestMdPreviewMarkerStripExactAndTotal(t *testing.T) {
	var b strings.Builder
	for _, k := range mdPreviewMarkerKinds {
		b.WriteString("before-")
		b.WriteString(string(k.kind))
		b.WriteString(mdPreviewMarker(k.id))
		b.WriteString("-after\n")
	}
	withMarkers := b.String()

	stripped := mdPreviewStripMarkers(withMarkers)

	// exact: everything except the marker bytes themselves survives
	// byte-for-byte.
	var wantB strings.Builder
	for _, k := range mdPreviewMarkerKinds {
		wantB.WriteString("before-")
		wantB.WriteString(string(k.kind))
		wantB.WriteString("-after\n")
	}
	if stripped != wantB.String() {
		t.Fatalf("stripped mismatch:\n got:  %q\n want: %q", stripped, wantB.String())
	}

	// total: not a single marker byte sequence remains, for any kind.
	for _, k := range mdPreviewMarkerKinds {
		if strings.Contains(stripped, mdPreviewMarker(k.id)) {
			t.Errorf("kind %s: marker still present after strip", k.kind)
		}
	}

	// no-op on a string with no markers.
	plain := "just some plain text\nwith two lines\n"
	if got := mdPreviewStripMarkers(plain); got != plain {
		t.Errorf("strip on markerless string changed it:\n got:  %q\n want: %q", got, plain)
	}
}

// TestMdPreviewMarkerDropEmptySGRPairs proves the post-pass collapses a
// SET-then-RESET pair left behind when a marker occupied a style field that
// had no existing prefix, including two such pairs back to back, while
// leaving an unrelated SGR sequence with real content between SET and
// RESET untouched.
func TestMdPreviewMarkerDropEmptySGRPairs(t *testing.T) {
	m1 := mdPreviewMarker(mdPreviewMarkerKindByName(t, "paragraph").id)
	m2 := mdPreviewMarker(mdPreviewMarkerKindByName(t, "h1").id)
	const reset = "\x1b[0m"

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"single empty pair", "before" + m1 + reset + "after", "beforeafter"},
		{"back to back empty pairs", "before" + m1 + reset + m2 + reset + "after", "beforeafter"},
		{"non-empty SGR span untouched", "before" + m1 + "content" + reset + "after", "before" + m1 + "content" + reset + "after"},
		{"no SGR at all", "plain text", "plain text"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mdPreviewDropEmptySGRPairs(c.in); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestMdPreviewMarkerStylePreservesExistingPrefix proves
// mdPreviewStyleWithMarkers prepends rather than replaces: item's
// BlockPrefix ("• " in DarkStyleConfig) and enumeration's (". ") must both
// still render after the marker, and a field with no existing prefix
// (paragraph, table) ends up as exactly the marker with nothing after it.
func TestMdPreviewMarkerStylePreservesExistingPrefix(t *testing.T) {
	item := mdPreviewMarkerKindByName(t, "item")
	enumeration := mdPreviewMarkerKindByName(t, "enumeration")
	paragraph := mdPreviewMarkerKindByName(t, "paragraph")
	table := mdPreviewMarkerKindByName(t, "table")

	base := glamourStyles.DarkStyleConfig
	if base.Item.BlockPrefix == "" {
		t.Fatal("test assumption broken: DarkStyleConfig.Item.BlockPrefix is empty")
	}
	if base.Enumeration.BlockPrefix == "" {
		t.Fatal("test assumption broken: DarkStyleConfig.Enumeration.BlockPrefix is empty")
	}

	sc := mdPreviewStyleWithMarkers(base, item, enumeration, paragraph, table)

	wantItem := mdPreviewMarker(item.id) + base.Item.BlockPrefix
	if sc.Item.BlockPrefix != wantItem {
		t.Errorf("Item.BlockPrefix = %q, want %q", sc.Item.BlockPrefix, wantItem)
	}
	wantEnum := mdPreviewMarker(enumeration.id) + base.Enumeration.BlockPrefix
	if sc.Enumeration.BlockPrefix != wantEnum {
		t.Errorf("Enumeration.BlockPrefix = %q, want %q", sc.Enumeration.BlockPrefix, wantEnum)
	}
	// paragraph and table have no existing prefix in DarkStyleConfig: the
	// marked field must be exactly the marker, nothing appended after it.
	if sc.Paragraph.Prefix != mdPreviewMarker(paragraph.id) {
		t.Errorf("Paragraph.Prefix = %q, want exactly the marker %q", sc.Paragraph.Prefix, mdPreviewMarker(paragraph.id))
	}
	if sc.Table.Prefix != mdPreviewMarker(table.id) {
		t.Errorf("Table.Prefix = %q, want exactly the marker %q", sc.Table.Prefix, mdPreviewMarker(table.id))
	}
}

// mdPreviewMarkerKindByName looks up a kind by name for test setup, failing
// the test immediately if it is not in the table.
func mdPreviewMarkerKindByName(t *testing.T, name mdPreviewBlockKind) mdPreviewMarkerKind {
	t.Helper()
	for _, k := range mdPreviewMarkerKinds {
		if k.kind == name {
			return k
		}
	}
	t.Fatalf("no kind named %q", name)
	return mdPreviewMarkerKind{}
}

// TestMdPreviewMarkerExtractDedupesRepeats proves a kind whose marker
// appears multiple times on the same rendered row (the ~3x-per-block
// repeat several kinds produce) yields exactly one hit for that row, not
// one per occurrence.
func TestMdPreviewMarkerExtractDedupesRepeats(t *testing.T) {
	h2 := mdPreviewMarker(mdPreviewMarkerKindByName(t, "h2").id)
	line := h2 + "## " + h2 + "Heading text" + h2 + "\n"

	hits := mdPreviewExtractMarkers(line)

	var h2Hits []mdPreviewMarkerHit
	for _, h := range hits {
		if h.kind == "h2" {
			h2Hits = append(h2Hits, h)
		}
	}
	if len(h2Hits) != 1 {
		t.Fatalf("got %d h2 hits for a row with 3 marker occurrences, want 1: %+v", len(h2Hits), h2Hits)
	}
	if h2Hits[0].row != 0 {
		t.Errorf("row = %d, want 0", h2Hits[0].row)
	}
}

// TestMdPreviewMarkerExtractMidRowClassification proves a marker preceded
// only by chrome (whitespace, list bullets/numbers, blockquote bars) is
// classified row-start (midRow=false), and a marker preceded by real
// content is classified mid-row (midRow=true) — the distinction task-list
// checkboxes need (established by the spike: their marker always lands
// after the checkbox glyph, which is not chrome).
func TestMdPreviewMarkerExtractMidRowClassification(t *testing.T) {
	item := mdPreviewMarker(mdPreviewMarkerKindByName(t, "item").id)

	cases := []struct {
		name       string
		line       string
		wantMidRow bool
	}{
		{"plain chrome bullet prefix", "  " + item + "item text", false},
		{"blockquote bar prefix", "│ " + item + "quoted text", false},
		{"ordered number prefix", "12" + item + "item text", false},
		{"checkbox glyph before marker is content", "[x] " + item + "done task", true},
		{"arbitrary text before marker is content", "some text " + item + "more", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hits := mdPreviewExtractMarkers(c.line)
			var itemHits []mdPreviewMarkerHit
			for _, h := range hits {
				if h.kind == "item" {
					itemHits = append(itemHits, h)
				}
			}
			if len(itemHits) != 1 {
				t.Fatalf("got %d item hits, want 1: %+v", len(itemHits), itemHits)
			}
			if itemHits[0].midRow != c.wantMidRow {
				t.Errorf("midRow = %v, want %v", itemHits[0].midRow, c.wantMidRow)
			}
		})
	}
}

// TestMdPreviewMarkerExtractRowOrderAndNoFalseHits proves extraction walks
// rows in order, reports a hit only on the row a marker actually occupies,
// and reports nothing at all for a document with no markers.
func TestMdPreviewMarkerExtractRowOrderAndNoFalseHits(t *testing.T) {
	h1 := mdPreviewMarker(mdPreviewMarkerKindByName(t, "h1").id)
	para := mdPreviewMarker(mdPreviewMarkerKindByName(t, "paragraph").id)
	doc := "no marker on this row\n" + h1 + "Title\n" + "still no marker\n" + para + "Body text\n"

	hits := mdPreviewExtractMarkers(doc)
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2: %+v", len(hits), hits)
	}
	if hits[0].kind != "h1" || hits[0].row != 1 {
		t.Errorf("hits[0] = %+v, want kind=h1 row=1", hits[0])
	}
	if hits[1].kind != "paragraph" || hits[1].row != 3 {
		t.Errorf("hits[1] = %+v, want kind=paragraph row=3", hits[1])
	}

	if got := mdPreviewExtractMarkers("nothing marked here\nor here\n"); len(got) != 0 {
		t.Errorf("got %d hits on a markerless document, want 0: %+v", len(got), got)
	}
}
