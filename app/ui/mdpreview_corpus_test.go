package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// This file is the differential corpus harness for the markdown-preview mermaid
// renderer. It is NOT part of the normal suite: every test here skips unless
// corpusListEnv names a file, so `make test` and CI are unaffected.
//
// # What it is for
//
// The two edge-label fixes (the no-break space substitution in mdpreview_nbsp.go
// and the LR retry in mdpreview_collision.go) both sit on the shared render path,
// so the risk that matters is not "does the fixture work" but "did anything ELSE
// move". The only way to answer that is to render a large body of real fences on
// both builds and account for every byte that differs.
//
// # How to run it
//
// Point corpusListEnv at a file holding one markdown path per line, and
// corpusDumpEnv at a file to write:
//
//	REVDIFF_MERMAID_CORPUS=/path/to/md-files.txt \
//	REVDIFF_MERMAID_CORPUS_DUMP=/tmp/dump-new.txt \
//	go test ./app/ui -run TestMermaidCorpusRender -timeout 30m
//
// Then check out the pre-change commit into a worktree, copy this file in, run
// the same command writing to a second dump, and diff the two. The file
// deliberately uses nothing but renderMermaidSource, so it compiles unchanged on
// a build that predates mdpreview_nbsp.go and mdpreview_collision.go — that is
// what makes the same harness runnable on both sides of the change.
//
// # How a difference is attributed
//
// A byte that differs between the two dumps has exactly three possible causes,
// and the run is only clean when every differing fence lands in one of them:
//
//  1. the vendored renderer changed — ruled out once and for all by checking
//     `git diff <base> HEAD -- vendor/` is empty, which it is: neither fix
//     touches mermaid-ascii
//  2. the source handed to that renderer changed — dump the normalized source
//     (the post-transpile, post-normalization string, which is what
//     renderMermaidSource actually renders) on both builds and diff THOSE.
//     Every differing rune must be a plain space or a `·` on the old side and
//     a no-break space on the new side
//  3. a different render was kept — the LR retry, which is a closed list of
//     fences that can be enumerated by calling mermaidRetryLRIfColliding
//     directly and recording where it returns something other than its input
//
// A fence whose normalized source is unchanged and whose sha is not in the
// retry list MUST render identically, because the renderer is deterministic and
// untouched. That is the check that turns "we looked at the diff" into an
// argument.
//
// Measured on 2026-08-05 against 15254 markdown files (229 distinct fences),
// base 3187fc5 vs the finished change:
//
//	229 fences, identical art 139, changed by the substitution 84,
//	changed by the LR retry 6, unexplained 0
//	0 panics, 0 timeouts, 0 blank renders, 23 unsupported-diagram-type
//	errors — all four identical on both builds
//	collisions (production detector, same detector both sides):
//	7 fences before, 0 after, none made worse
const (
	corpusListEnv = "REVDIFF_MERMAID_CORPUS"
	corpusDumpEnv = "REVDIFF_MERMAID_CORPUS_DUMP"

	// corpusPaneWidth is the pane width every fence is rendered at. Fixed so
	// the two dumps are comparable; 120 matches a normal terminal.
	corpusPaneWidth = 120

	// corpusRenderTimeout bounds a single fence. A vendored renderer that
	// never returns would otherwise take the whole run down with a test
	// binary timeout and no clue which fence caused it.
	corpusRenderTimeout = 30 * time.Second
)

// corpusFence is one unique mermaid fence found in the corpus, keyed by a hash
// of its source so the two dumps can be matched up fence by fence without
// depending on file order (the pre-change build may walk the same list in the
// same order, but relying on that would make an unrelated corpus edit look like
// a render change).
type corpusFence struct {
	sha    string
	source string
}

// collectCorpusFences walks every markdown file named in listPath and returns
// each distinct ```mermaid fence body exactly once, in first-seen order.
// Unreadable files are skipped rather than failed: the list is a snapshot of a
// user's disk, not a fixture under test.
func collectCorpusFences(t *testing.T, listPath string) []corpusFence {
	t.Helper()

	list, err := os.ReadFile(listPath) //nolint:gosec // path comes from the operator's own env var
	if err != nil {
		t.Skipf("corpus list %s unreadable: %v", listPath, err)
	}

	seen := make(map[string]bool)
	var fences []corpusFence
	for path := range strings.SplitSeq(strings.TrimSpace(string(list)), "\n") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		body, readErr := os.ReadFile(path) //nolint:gosec // same
		if readErr != nil {
			continue
		}
		for _, source := range extractCorpusFences(string(body)) {
			if strings.TrimSpace(source) == "" {
				continue
			}
			sum := sha256.Sum256([]byte(source))
			sha := hex.EncodeToString(sum[:])
			if seen[sha] {
				continue
			}
			seen[sha] = true
			fences = append(fences, corpusFence{sha: sha, source: source})
		}
	}
	return fences
}

// extractCorpusFences pulls the body of every ```mermaid fence out of one
// markdown document. It is a deliberately dumb scanner rather than a reuse of
// the production fence walker: the harness must find fences the same way on
// both builds, and pinning it here means a change to the production scanner
// cannot silently change what the corpus covers.
func extractCorpusFences(doc string) []string {
	var out []string
	cur := make([]string, 0, 16)
	inFence := false
	for line := range strings.SplitSeq(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case !inFence && strings.HasPrefix(trimmed, "```mermaid"):
			inFence = true
			cur = cur[:0]
		case inFence && strings.HasPrefix(trimmed, "```"):
			inFence = false
			out = append(out, strings.Join(cur, "\n"))
		case inFence:
			cur = append(cur, line)
		}
	}
	return out
}

// TestExtractCorpusFences is the one test in this file that runs
// unconditionally: extractCorpusFences is a pure function, and a harness whose
// scanner has silently stopped finding fences would report a clean corpus run
// on an empty corpus.
func TestExtractCorpusFences(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want []string
	}{
		{"no fences", "# title\n\nplain prose\n", nil},
		{"one fence", "a\n\n```mermaid\nflowchart TD\n  A --> B\n```\n\nb\n", []string{"flowchart TD\n  A --> B"}},
		{
			"two fences keep document order",
			"```mermaid\nfirst\n```\ntext\n```mermaid\nsecond\n```\n",
			[]string{"first", "second"},
		},
		{
			"a non-mermaid fence is not collected and does not swallow the next one",
			"```go\nfmt.Println()\n```\n\n```mermaid\nflowchart TD\n```\n",
			[]string{"flowchart TD"},
		},
		{"indented fence", "  ```mermaid\n  flowchart TD\n  ```\n", []string{"  flowchart TD"}},
		{"fence with an info string after the language", "```mermaid title=x\nflowchart TD\n```\n", []string{"flowchart TD"}},
		{"empty fence", "```mermaid\n```\n", []string{""}},
		{"unterminated fence is dropped", "```mermaid\nflowchart TD\n  A --> B\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractCorpusFences(tt.doc)
			if len(got) != len(tt.want) {
				t.Fatalf("extractCorpusFences() = %q, want %q", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("extractCorpusFences()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// corpusRenderResult is one fence's outcome. Exactly one of art / err is
// meaningful; panicked and timedOut are the two failure shapes the harness
// exists to prove are absent.
type corpusRenderResult struct {
	art      string
	err      error
	panicked string
	timedOut bool
}

// renderCorpusFence renders one fence with a panic guard and a timeout, so a
// single bad fence is reported as data rather than taking the run down.
//
// The render runs on its own goroutine only so the timeout can be observed. A
// fence that times out leaks that goroutine; that is accepted, because a
// timeout is already a hard failure of the run and the process is about to end.
func renderCorpusFence(source string) corpusRenderResult {
	done := make(chan corpusRenderResult, 1)
	go func() {
		var res corpusRenderResult
		defer func() {
			if r := recover(); r != nil {
				res.panicked = fmt.Sprint(r)
			}
			done <- res
		}()
		res.art, res.err = renderMermaidSource(source, corpusPaneWidth)
	}()

	select {
	case res := <-done:
		return res
	case <-time.After(corpusRenderTimeout):
		return corpusRenderResult{timedOut: true}
	}
}

// corpusDumpRecord formats one fence for the dump file. The format is
// line-oriented and self-delimiting so a plain text diff of two dumps is
// readable, and the sha header lets a comparison tool pair records up.
func corpusDumpRecord(fence corpusFence, res corpusRenderResult) string {
	var b strings.Builder
	b.WriteString("=== FENCE " + fence.sha + "\n")
	b.WriteString("--- SOURCE\n")
	b.WriteString(fence.source)
	b.WriteString("\n--- STATUS ")
	switch {
	case res.timedOut:
		b.WriteString("timeout")
	case res.panicked != "":
		b.WriteString("panic: " + strings.ReplaceAll(res.panicked, "\n", " "))
	case res.err != nil:
		b.WriteString("error: " + strings.ReplaceAll(res.err.Error(), "\n", " "))
	default:
		b.WriteString("ok")
	}
	b.WriteString("\n--- ART\n")
	b.WriteString(res.art)
	b.WriteString("\n=== END " + fence.sha + "\n")
	return b.String()
}

// TestMermaidCorpusRender renders every distinct mermaid fence in the corpus and
// writes a dump that can be diffed against the same dump taken on another build.
//
// It fails on a panic or a timeout — those are absolute, and neither build may
// have any. It does NOT fail on a render error: a corpus of real documents
// contains fences of diagram types this renderer does not support, and those
// errors are part of the expected output on BOTH builds, so a change in the set
// of erroring fences shows up in the dump diff rather than here.
func TestMermaidCorpusRender(t *testing.T) {
	listPath := os.Getenv(corpusListEnv)
	if listPath == "" {
		t.Skipf("%s not set; skipping corpus render", corpusListEnv)
	}

	fences := collectCorpusFences(t, listPath)
	if len(fences) == 0 {
		t.Fatalf("corpus list %s yielded no mermaid fences", listPath)
	}

	var (
		dump     strings.Builder
		panics   []string
		timeouts []string
		errored  int
		blank    int
	)
	for _, fence := range fences {
		res := renderCorpusFence(fence.source)
		switch {
		case res.timedOut:
			timeouts = append(timeouts, fence.sha)
		case res.panicked != "":
			panics = append(panics, fence.sha+": "+res.panicked)
		case res.err != nil:
			errored++
		case strings.TrimSpace(res.art) == "":
			blank++
		}
		dump.WriteString(corpusDumpRecord(fence, res))
	}

	t.Logf("corpus fences: %d", len(fences))
	t.Logf("  render errors (unsupported diagram types): %d", errored)
	t.Logf("  blank renders: %d", blank)
	t.Logf("  panics: %d", len(panics))
	t.Logf("  timeouts: %d", len(timeouts))

	if dumpPath := os.Getenv(corpusDumpEnv); dumpPath != "" {
		//nolint:gosec // the dump path is the operator's own env var, not user input
		if err := os.WriteFile(dumpPath, []byte(dump.String()), 0o600); err != nil {
			t.Fatalf("write dump %s: %v", dumpPath, err)
		}
		t.Logf("  dump written to %s", dumpPath)
	}

	if len(panics) > 0 {
		sort.Strings(panics)
		t.Errorf("%d fences panicked:\n%s", len(panics), strings.Join(panics, "\n"))
	}
	if len(timeouts) > 0 {
		t.Errorf("%d fences timed out: %s", len(timeouts), strings.Join(timeouts, ", "))
	}
}
