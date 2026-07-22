package ui

import (
	"fmt"
	"strings"

	mermaidcmd "github.com/AlexanderGrooff/mermaid-ascii/cmd"
	"github.com/charmbracelet/glamour"
	glamourStyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/ansi"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/keymap"
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
	return joinWithMermaidFences(lines, renderMermaidBlock)
}

// joinWithMermaidFences is the shared fence-scanning walk behind
// renderMermaidFences and mermaidPlaceholderDocument. renderBlock is called
// once per complete mermaid fence (body lines, opening fence line, closing
// fence line) and its return value is written verbatim in place of the
// fence — renderMermaidFences passes renderMermaidBlock (inline rendered
// art), mermaidPlaceholderDocument passes a callback that defers rendering
// and substitutes a placeholder instead (see renderMarkdownDocument for why).
func joinWithMermaidFences(lines []diff.DiffLine, renderBlock func(body []string, openLine, closeLine string) string) string {
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
				out.WriteString(renderBlock(fenceBody, lines[fenceStart].Content, content))
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

// mdPreviewStyle is the single fixed glamour style used for the whole
// document. This is a personal patch on a local clone (see the plan), not an
// upstream feature, so there is deliberately no style generator deriving
// colors from revdiff's 23 theme fields — one bundled style, picked once.
var mdPreviewStyle = glamourStyles.DarkStyleConfig

// mdPreviewMinWidth is the floor applied to the requested render width
// before handing it to glamour. glamour does not panic on width <= 0 (word
// wrap degrades to "no wrapping" internally), but a positive floor keeps
// prose reasonably readable instead of collapsing to a one-character-per-line
// render on a transient zero-width layout state.
const mdPreviewMinWidth = 8

// mermaidPlaceholder returns the sentinel text substituted for the idx'th
// mermaid diagram while the document is handed to glamour. It is plain
// alphanumeric text with no markdown or glamour/x-ansi word-wrap break
// characters (space, comma, period, semicolon, hyphen, plus, pipe — see
// x/ansi.Wordwrap) in it, so it always survives glamour's internal word-wrap
// pass as a single atomic, unsplit token and can be found again afterward.
func mermaidPlaceholder(idx int) string {
	return fmt.Sprintf("mdpreviewmermaidplaceholder%dmdpreviewmermaidplaceholder", idx)
}

// mermaidPlaceholderDocument builds the document handed to glamour: every
// mermaid fence is replaced by a placeholder token (see mermaidPlaceholder),
// isolated in its own paragraph by surrounding blank lines so glamour cannot
// merge it into an adjacent line of prose. The rendered (or verbatim
// fallback) art for each diagram is collected in order in arts, ready for
// spliceMermaidArt to substitute back in after glamour has rendered doc.
//
// The art itself never appears in doc — see renderMarkdownDocument for why:
// glamour's document-level word-wrap pass reflows the entire rendered
// buffer, including the contents of any fenced code block nested inside it,
// so putting the art directly in doc (even inside a code fence) is not
// sufficient to protect it.
func mermaidPlaceholderDocument(lines []diff.DiffLine) (doc string, arts []string) {
	idx := 0
	doc = joinWithMermaidFences(lines, func(body []string, openLine, closeLine string) string {
		arts = append(arts, renderMermaidBlock(body, openLine, closeLine))
		placeholder := mermaidPlaceholder(idx)
		idx++
		return "\n" + placeholder + "\n\n"
	})
	return doc, arts
}

// spliceMermaidArt walks rendered line by line and replaces the single
// output line matching each placeholder (after stripping glamour's ANSI
// styling and surrounding pad/margin whitespace) with the corresponding
// entry of arts, verbatim and unstyled. This is what makes the diagram
// reach the screen byte-exact: the art itself is never passed through
// glamour, only located and spliced in after the fact.
//
// A placeholder glamour did not place alone on its own line (should not
// happen given mermaidPlaceholderDocument's isolation and mermaidPlaceholder
// being word-wrap-atomic — see there) is left as visible plain text rather
// than silently dropping the diagram or panicking.
func spliceMermaidArt(rendered string, arts []string) string {
	if len(arts) == 0 {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		stripped := strings.TrimSpace(ansi.Strip(line))
		for idx, art := range arts {
			if stripped != mermaidPlaceholder(idx) {
				continue
			}
			lines[i] = strings.TrimSuffix(art, "\n")
			break
		}
	}
	return strings.Join(lines, "\n")
}

// renderMarkdownDocument turns lines (the full, ordered source of a single
// markdown file) into ANSI-styled text ready for the diff viewport: mermaid
// fences are rendered via mermaid-ascii and spliced in after glamour has
// rendered the rest of the document, so the art is never subject to
// glamour's own word-wrap (see mermaidPlaceholderDocument for why a fenced
// code block alone is not enough).
//
// width is glamour's word-wrap target for prose, headings and tables — it is
// floored at mdPreviewMinWidth but otherwise passed straight through. A
// fenced code block's content (and so the spliced-in art) is never
// constrained by this width in glamour's own renderer; the same is true
// here, deliberately: box-drawing art has a natural minimum width, and
// truncating or reflowing it to fit a narrow viewport would corrupt the
// diagram's shape rather than just make it small. A narrow viewport is
// expected to scroll horizontally for the art, not to receive a clipped or
// re-wrapped diagram.
//
// A glamour construction or render error (should not happen with the fixed
// built-in style used here) falls back to the mermaid-substituted plain
// document so the file remains at least readable.
func renderMarkdownDocument(lines []diff.DiffLine, width int) string {
	doc, arts := mermaidPlaceholderDocument(lines)

	w := max(width, mdPreviewMinWidth)

	r, err := glamour.NewTermRenderer(glamour.WithStyles(mdPreviewStyle), glamour.WithWordWrap(w))
	if err != nil {
		return doc
	}
	out, err := r.Render(doc)
	if err != nil {
		return doc
	}
	return spliceMermaidArt(out, arts)
}

// mdPreviewCache holds the last markdown-preview render, keyed on the source
// file it was rendered from and the viewport width it targeted — a width
// change (pane resize, tree toggle) must invalidate it, since glamour's
// word-wrap depends on width. This is deliberately not a field on Model
// (that wiring belongs to the mode-toggle task); a caller holds one instance
// and passes it into render on every call.
type mdPreviewCache struct {
	valid bool
	file  string
	width int
	out   string
}

// render returns the cached preview for file/width when both match the
// previous call, and otherwise (re)renders via renderMarkdownDocument and
// refreshes the cache. lines is only consulted on a miss — a cache hit
// intentionally does not re-inspect lines, so callers must invalidate (or
// use a fresh cache) whenever the file's content actually changes under an
// unchanged name, e.g. on reload.
func (c *mdPreviewCache) render(file string, lines []diff.DiffLine, width int) string {
	if c.valid && c.file == file && c.width == width {
		return c.out
	}
	out := renderMarkdownDocument(lines, width)
	c.valid = true
	c.file = file
	c.width = width
	c.out = out
	return out
}

// toggleMarkdownPreview flips markdown preview mode on/off for the currently
// loaded file. Refused (no state change) unless m.file.mdTOC is non-nil —
// that field is set only for a single, full-context markdown file (see the
// gate in loaders.go), which is exactly the condition under which a
// whole-document render is safe (no partially-shown table — see the plan's
// Overview). The mode defaults to off.
//
// Enabling it lazily allocates the render cache the first time, so repeated
// toggles on the same file reuse the cached glamour/mermaid output instead of
// re-rendering on every press.
func (m *Model) toggleMarkdownPreview() {
	if m.file.mdTOC == nil {
		return
	}
	m.modes.mdPreview = !m.modes.mdPreview
	if m.modes.mdPreview && m.file.mdPreviewCache == nil {
		m.file.mdPreviewCache = &mdPreviewCache{}
	}
	m.syncViewportToCursor()
}

// renderMarkdownPreview returns the cached (or freshly rendered) markdown
// preview for the currently loaded file at the current viewport width. Falls
// back to an uncached render when the cache has not been allocated yet —
// production always allocates it in toggleMarkdownPreview before mdPreview
// can be true, but this keeps the render path itself safe on its own.
func (m Model) renderMarkdownPreview() string {
	if m.file.mdPreviewCache != nil {
		return m.file.mdPreviewCache.render(m.file.name, m.file.lines, m.layout.viewport.Width)
	}
	return renderMarkdownDocument(m.file.lines, m.layout.viewport.Width)
}

// mdPreviewAllowedActions is the fixed allowlist of keymap actions that stay
// live while markdown preview is on. Every action not in this set is a no-op
// while previewing (see mdPreviewActionAllowed and its call site in
// dispatchAction, app/ui/model.go) because it would create, edit, delete, or
// navigate to an annotation, or move/reposition m.nav.diffCursor — all
// meaningless once the diff pane shows one whole-document glamour render
// instead of one row per source line (see this plan's Solution Overview).
//
//   - toggle_markdown_preview must stay allowed so P can turn the mode back
//     off — this is the mode's only exit key.
//   - quit / discard_quit / help / theme_select / toggle_tree are session and
//     layout actions that never touch m.nav.diffCursor or the annotation
//     store (theme_select and help open an overlay; toggle_tree only flips
//     pane visibility).
//   - dismiss (esc) only clears a leftover search-match highlight from a
//     search that completed before preview was turned on; it never touches
//     the cursor or the store either.
//   - scroll_diff_down/up (J/K) drive the viewport's YOffset directly rather
//     than following the cursor — see pinDiffCursorTo's mdPreview guard in
//     mouse.go for why that stays safe even though the same function also
//     tries to "pin" the cursor back into view on a normal (mode-off) scroll.
//
// Deliberately NOT included, despite being layout/session actions with no
// obvious annotation/cursor risk on their own: toggle_pane / focus_tree /
// focus_diff (switching focus into the TOC pane is pointless once TOC
// navigation itself is blocked below), info, reload, flush_output,
// mark_reviewed, filter, filter_unreviewed, open_file_in_editor,
// toggle_untracked, and the other view-mode toggles (wrap/collapsed/compact/
// line_numbers/blame/word_diff/toggle_hunk) — none of them are needed to
// read a rendered preview, and several (toggle_wrap, toggle_collapsed, ...)
// still call syncViewportToCursor, whose Y-offset math silently assumes
// diff-line coordinates (see cursorVisualRange) that preview content doesn't
// have. Keeping the allowlist tight avoids relying on that math staying
// harmless action-by-action.
//
// next_item/prev_item (n/N/p) are excluded even though they read like
// harmless "file navigation": when the markdown TOC is active (the same gate
// that allows preview at all — see toggleMarkdownPreview), these actions
// route to jumpTOCEntry, which calls syncDiffToTOCCursor and unconditionally
// reassigns m.nav.diffCursor — exactly the class of bug this allowlist
// exists to prevent, just reached through TOC navigation instead of the diff
// pane's own j/k.
var mdPreviewAllowedActions = map[keymap.Action]bool{
	keymap.ActionTogglePreview:  true,
	keymap.ActionQuit:           true,
	keymap.ActionDiscardQuit:    true,
	keymap.ActionHelp:           true,
	keymap.ActionThemeSelect:    true,
	keymap.ActionToggleTree:     true,
	keymap.ActionScrollDiffDown: true,
	keymap.ActionScrollDiffUp:   true,
	keymap.ActionDismiss:        true,
}

// mdPreviewActionAllowed reports whether action may run while markdown
// preview is on. Called once, at the top of dispatchAction (app/ui/model.go)
// — the single choke point every keymap-resolved action passes through,
// whether it arrived via handleKey's direct path or handleChordSecond's
// chord path, both of which call dispatchAction. That single call site is
// enough to cover every action reachable through the keymap; it is NOT
// enough on its own to cover vim-motion's own screen-position motions
// (G, gg, zz, H/M/L, count digits), which mutate the cursor directly from
// the raw key before keymap.Resolve ever runs — handleKey additionally
// skips the vim-motion interceptor entirely while preview is on (see the
// comment at that call site) to close that separate path.
func mdPreviewActionAllowed(action keymap.Action) bool {
	return mdPreviewAllowedActions[action]
}
