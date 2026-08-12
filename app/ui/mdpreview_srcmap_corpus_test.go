package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/umputun/revdiff/app/diff"
)

// This file is the alignment corpus harness for the markdown-preview source
// map (task 3, mdpreview_srcmap.go). It is NOT part of the normal suite: the
// one test here skips unless srcMapCorpusListEnv names a file, so `make test`
// and CI are unaffected, mirroring mdpreview_corpus_test.go's differential
// mermaid harness (same repo, a sibling feature) on the same env-gated shape.
//
// # What it is for
//
// Task 3's alignment mechanism (mdPreviewAlignRows) is a safety net: either
// the goldmark walk and the marker extraction agree, or the map refuses
// itself entirely. What this harness measures is how often real documents
// land on each side of that net, and what shape the resulting map has when
// they agree — one number the unit tests cannot produce, because a unit test
// picks documents to prove a specific rule, not to sample how often that rule
// actually fires.
//
// # How to run it
//
// Point srcMapCorpusListEnv at a file holding one markdown path per line
// (relative paths are resolved from the process's working directory, which
// `go test` sets to the package directory — so a path written relative to the
// repo root needs a leading `../../`, or pass absolute paths):
//
//	REVDIFF_MDPREVIEW_SRCMAP_CORPUS=/path/to/md-files.txt \
//	go test ./app/ui -run TestMdPreviewSrcMapCorpusAlignment -v
//
// The corpus this plan's own numbers were measured against is the repo's own
// document tree: every file under docs/plans/completed/, every file directly
// under docs/, and every markdown file at the repo root — the same three
// globs the task 1-3 spike harness used (see its zzCorpus function), so this
// harness's numbers are comparable to the ones already recorded in the plan's
// Technical Details section.
const srcMapCorpusListEnv = "REVDIFF_MDPREVIEW_SRCMAP_CORPUS"

// srcMapCorpusWidth is the pane width every document is rendered at. Fixed so
// repeated runs are comparable; matches the spike's primary width and the
// width most of the source-map unit tests use.
const srcMapCorpusWidth = 80

// TestMdPreviewSrcMapCorpusAlignment renders every document named in the
// corpus list through mdPreviewRenderWithMap and reports, per document,
// whether the map aligned, and for the aligned ones, how many anchors each
// tracked block kind produced. It never fails on a document that does not
// align — a real corpus containing an adjacent-tables document or a
// --no-colors run is expected to produce unaligned documents on purpose (see
// mdPreviewRenderWithMap's doc comment) — it only fails if the corpus list
// itself yields no documents at all, which would mean the harness measured
// nothing and any percentage it printed would be meaningless.
func TestMdPreviewSrcMapCorpusAlignment(t *testing.T) {
	listPath := os.Getenv(srcMapCorpusListEnv)
	if listPath == "" {
		t.Skipf("%s not set; skipping source-map corpus alignment measurement", srcMapCorpusListEnv)
	}

	raw, err := os.ReadFile(listPath) //nolint:gosec // path comes from the operator's own env var
	if err != nil {
		t.Skipf("corpus list %s unreadable: %v", listPath, err)
	}

	var (
		total, aligned int
		unaligned      []string
		kindCounts     = map[mdPreviewBlockKind]int{}
		blocksAligned  int
	)
	for path := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		body, readErr := os.ReadFile(path) //nolint:gosec // same
		if readErr != nil {
			continue // a corpus list is a snapshot of a user's disk, not a fixture under test
		}
		total++

		_, srcMap := mdPreviewRenderWithMap(mdLines(string(body)), srcMapCorpusWidth, false)
		if !srcMap.aligned {
			unaligned = append(unaligned, filepath.Base(path))
			continue
		}
		aligned++
		for _, a := range srcMap.blocks() {
			kindCounts[a.kind]++
			blocksAligned++
		}
	}

	if total == 0 {
		t.Fatalf("corpus list %s yielded no readable documents", listPath)
	}

	t.Logf("alignment: %d/%d documents (%.1f%%)", aligned, total, 100*float64(aligned)/float64(total))
	if len(unaligned) > 0 {
		sort.Strings(unaligned)
		t.Logf("unaligned documents: %s", strings.Join(unaligned, ", "))
	}

	t.Logf("anchors produced across %d aligned documents: %d total", aligned, blocksAligned)
	kinds := make([]string, 0, len(kindCounts))
	for k := range kindCounts {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		t.Logf("  %-12s %d", k, kindCounts[mdPreviewBlockKind(k)])
	}
}

// TestMdPreviewRawExpansionCorpusKeepsEveryLetter is the mechanical net under
// mdPreviewClipRawSpan, and it exists because the shape it catches is invisible
// to a fixture test: expanding one block silently DELETED a neighboring part of
// the document from the frame, and every unit test written for the feature at the
// time expanded the container rather than the block nested inside it.
//
// The property is the acceptance criterion stated as something a machine can
// check: expanding a block replaces rows it owns, so every letter on screen
// before the expansion is still on screen after it — the raw source of a block
// carries the same words its render did, plus markup. Letters are counted rather
// than compared in order because glamour wraps table cells, which interleaves the
// columns and reorders the rendered text against its source.
//
// A MERMAID DIAGRAM is the one block that sentence is false about, so its
// expectation is computed differently — see mdPreviewCorpusExpected, which states
// what replaces it and why. It is not a weaker check: everything outside the
// block's own rows must survive exactly as before, and the definition must
// additionally be proven on screen.
//
// Same env gate and same corpus list as TestMdPreviewSrcMapCorpusAlignment above;
// see that test's comment for how to run it. Measured on the repo's own document
// tree it does 10298 expansions across 59 aligned documents in about 100 seconds,
// which is why it stays out of `make test`.
func TestMdPreviewRawExpansionCorpusKeepsEveryLetter(t *testing.T) {
	listPath := os.Getenv(srcMapCorpusListEnv)
	if listPath == "" {
		t.Skipf("%s not set; skipping raw-expansion corpus check", srcMapCorpusListEnv)
	}
	raw, err := os.ReadFile(listPath) //nolint:gosec // path comes from the operator's own env var
	if err != nil {
		t.Skipf("corpus list %s unreadable: %v", listPath, err)
	}

	docs, expansions := 0, 0
	for path := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		body, readErr := os.ReadFile(path) //nolint:gosec // same
		if readErr != nil {
			continue // a corpus list is a snapshot of a user's disk, not a fixture under test
		}
		m := mdPreviewStyledModel(t, string(body))
		before, srcMap := m.mdPreviewBody()
		if !srcMap.aligned {
			continue
		}
		docs++
		for bi := range srcMap.blocks() {
			expanded := m
			expanded.setMdPreviewCursorToBlock(bi)
			expanded.mdPreviewToggleRaw()
			if expanded.mdPreviewExpandedBlock() != bi {
				continue // refused, and the refusal hint says why
			}
			expansions++
			want := mdPreviewCorpusExpected(before, srcMap.blocks()[bi], m.file.lines)
			after, _ := expanded.mdPreviewBody()
			assert.True(t, mdPreviewCorpusCovers(mdPreviewCorpusLetters(after), want),
				"%s: expanding block %d (kind %s, source lines %d..%d) removed content from the frame",
				filepath.Base(path), bi, srcMap.blocks()[bi].kind,
				srcMap.blocks()[bi].startLine, srcMap.blocks()[bi].endLine)
		}
	}
	if docs == 0 {
		t.Fatalf("corpus list %s yielded no aligned documents", listPath)
	}
	t.Logf("expanded %d blocks across %d aligned documents with no content lost", expansions, docs)
}

// mdPreviewCorpusExpected is the letter count the frame must still cover once
// block a has been expanded: the frame as it was, for every block but one.
//
// A mermaid diagram is that one. Its rendered form is a DRAWING produced from the
// definition rather than a typesetting of it, and the renderer writes text into
// the drawing that appears in no source line — a `<|--` edge is labeled
// `implements`, node labels are truncated to `+ChangedFiles...`. Requiring the
// art's own letters to survive the expansion would therefore be requiring the
// source to contain words the author never wrote. (Measured, not assumed: the
// classDiagram in docs/plans/20260730-preview-manual-test-plan.md loses exactly
// three `m` and three `p` that way — the three synthesized `implements` labels.)
//
// So for a diagram the art's rows drop out of the expectation and the fence's own
// source lines are added to it. That is stronger than the plain frame check, not
// weaker: every row OUTSIDE the block still has to survive letter for letter — the
// direction the harness exists to police, since the deletion that motivated it hit
// a neighboring block — and the definition now has to be proven on screen rather
// than merely not-lost. The rows dropped are the block's own tile as the map
// recorded it, so a pass that replaced more rows than it owns is still caught by
// the letters outside going missing.
func mdPreviewCorpusExpected(before string, a mdPreviewBlockAnchor, lines []diff.DiffLine) []int {
	fenceEnd, isDiagram := mdPreviewMermaidFenceSpan(lines, a.startLine)
	if !isDiagram || a.startLine != a.endLine {
		return mdPreviewCorpusLetters(before)
	}
	var b strings.Builder
	for row, text := range strings.Split(ansi.Strip(before), "\n") {
		if row >= a.row && row <= a.endRow {
			continue // the art, which the source it was drawn from does not spell
		}
		b.WriteString(text + "\n")
	}
	for i := a.startLine; i <= min(fenceEnd, len(lines)-1); i++ {
		b.WriteString(lines[i].Content + "\n")
	}
	return mdPreviewCorpusLetters(b.String())
}

// mdPreviewCorpusLetters is a frame reduced to its lowercase ASCII letters:
// styling, box drawing, bullets, indentation and wrapping all drop out, so what
// is left is the text the reader can read.
func mdPreviewCorpusLetters(frame string) []int {
	var counts [26]int
	for _, r := range strings.ToLower(ansi.Strip(frame)) {
		if r >= 'a' && r <= 'z' {
			counts[r-'a']++
		}
	}
	return counts[:]
}

// mdPreviewCorpusCovers reports whether got holds at least as many of every
// letter as want.
func mdPreviewCorpusCovers(got, want []int) bool {
	for i := range want {
		if got[i] < want[i] {
			return false
		}
	}
	return true
}
