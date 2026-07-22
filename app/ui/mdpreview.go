package ui

import (
	"strings"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"

	"github.com/umputun/revdiff/app/diff"
)

// mdFencePrefix returns the fence character ('`' or '~') and the count of leading
// consecutive occurrences at the start of s. It returns (0, 0) when s does not
// start with backticks or tildes.
//
// This duplicates sidepane.fencePrefix (app/ui/sidepane/toc.go), which is
// unexported and cannot be called from this package. The duplication is
// deliberate: this fork is rebased onto upstream releases by hand, and every
// edited line in an existing file is a future merge conflict, so new logic goes
// in new files rather than exporting across a package boundary for one caller.
func mdFencePrefix(s string) (rune, int) {
	if s == "" {
		return 0, 0
	}
	ch := rune(s[0])
	if ch != '`' && ch != '~' {
		return 0, 0
	}
	n := 0
	for _, r := range s {
		if r != ch {
			break
		}
		n++
	}
	return ch, n
}

// renderMermaidFences joins source lines into a single markdown document,
// replacing every ```mermaid (or ~~~mermaid) fence with its rendered
// box-drawing art. Fence tracking is CommonMark-compliant, matching
// sidepane.ParseTOC: a closing fence must use the same character as the
// opening fence and be at least as long, with no non-whitespace after it.
//
// Fences in any other language are left byte-identical to the source. A fence
// that is never closed is preserved verbatim through end of document (nothing
// is dropped). A diagram that fails to render falls back to its original
// fence text verbatim — see renderMermaidBlock. This function never returns
// an error and never panics.
func renderMermaidFences(lines []diff.DiffLine) string {
	var out strings.Builder

	var fenceChar rune // 0 when outside any fence
	var fenceLen int   // length of the opening fence marker
	var fenceLang string
	var fenceStart int     // index into lines of the opening fence line
	var fenceBody []string // body lines collected while inside a mermaid fence

	writeLine := func(content string) {
		out.WriteString(content)
		out.WriteString("\n")
	}

	for i, line := range lines {
		if line.ChangeType == diff.ChangeDivider {
			continue
		}

		content := line.Content
		trimmed := strings.TrimSpace(content)
		ch, n := mdFencePrefix(trimmed)

		switch {
		case fenceChar == 0 && n >= 3:
			// opening fence: the info string is the first whitespace-delimited
			// token after the fence marker (CommonMark allows extra data after
			// the language, e.g. "```mermaid title=foo").
			fenceChar = ch
			fenceLen = n
			fenceLang = ""
			if fields := strings.Fields(trimmed[n:]); len(fields) > 0 {
				fenceLang = strings.ToLower(fields[0])
			}
			fenceStart = i
			fenceBody = nil
			if fenceLang != "mermaid" {
				writeLine(content)
			}
			continue
		case fenceChar != 0 && ch == fenceChar && n >= fenceLen && strings.TrimSpace(trimmed[n:]) == "":
			// closing fence
			if fenceLang == "mermaid" {
				out.WriteString(renderMermaidBlock(fenceBody, lines[fenceStart].Content, content))
			} else {
				writeLine(content)
			}
			fenceChar = 0
			fenceLen = 0
			fenceLang = ""
			fenceBody = nil
			continue
		}

		if fenceChar != 0 {
			if fenceLang == "mermaid" {
				fenceBody = append(fenceBody, content)
			} else {
				writeLine(content)
			}
			continue
		}

		writeLine(content)
	}

	// an unterminated mermaid fence extends to end of document; its opening
	// line and body were deferred (not yet written) waiting for a close that
	// never came, so flush them verbatim rather than losing them. Non-mermaid
	// fences are written eagerly above and need no flush here.
	if fenceChar != 0 && fenceLang == "mermaid" {
		writeLine(lines[fenceStart].Content)
		for _, b := range fenceBody {
			writeLine(b)
		}
	}

	return out.String()
}

// renderMermaidBlock renders one mermaid fence's body via mermaid-ascii. On
// any parse/render error, or a panic inside the third-party renderer, it
// falls back to the original fence text (opening line, body, closing line)
// joined verbatim. This function never returns an error and never panics.
func renderMermaidBlock(body []string, openLine, closeLine string) (result string) {
	verbatim := func() string {
		var b strings.Builder
		b.WriteString(openLine)
		b.WriteString("\n")
		for _, l := range body {
			b.WriteString(l)
			b.WriteString("\n")
		}
		b.WriteString(closeLine)
		b.WriteString("\n")
		return b.String()
	}

	defer func() {
		if r := recover(); r != nil {
			result = verbatim()
		}
	}()

	rendered, err := mermaidcmd.RenderDiagram(strings.Join(body, "\n"), nil)
	if err != nil || strings.TrimSpace(rendered) == "" {
		return verbatim()
	}
	return rendered + "\n"
}
