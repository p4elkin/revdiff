package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
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
		if !srcMap.Aligned {
			unaligned = append(unaligned, filepath.Base(path))
			continue
		}
		aligned++
		for _, a := range srcMap.blocks() {
			kindCounts[a.Kind]++
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
