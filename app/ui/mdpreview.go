package ui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"

	"github.com/charmbracelet/glamour"
	glamourStyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

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
//
// renderMermaidBlock now takes a pane-width parameter (see its doc comment
// and mdpreview_transpile.go's adaptive label cap), but this function's own
// signature is deliberately left unchanged: it is called from eleven places
// in mdpreview_test.go, none of which care about width, and every one of
// this function's own callers only has a mermaid fence's rendered art to
// verify, not a real viewport to size it against. mermaidUnconstrainedWidth
// tells the adaptive cap to skip sizing entirely and behave like every other
// (non-transpiled) diagram type: unconstrained.
func renderMermaidFences(lines []diff.DiffLine) string {
	return joinWithMermaidFences(lines, func(body []string, openLine, closeLine string) string {
		return renderMermaidBlock(body, openLine, closeLine, mermaidUnconstrainedWidth)
	})
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

// renderMermaidBlock renders one mermaid fence's body via mermaid-ascii,
// through renderMermaidSource (mdpreview_transpile.go) so classDiagram and
// stateDiagram-v2 fences get a chance to transpile to flowchart source
// first. On any parse/render error, or a panic inside the third-party
// renderer, it falls back to the original fence text (opening line, body,
// closing line) joined verbatim. This function never returns an error and
// never panics. paneWidth is the diff pane's current width, threaded down
// for the adaptive label-width cap the transpiler uses — see
// renderMermaidSource and mermaidLabelCap.
func renderMermaidBlock(body []string, openLine, closeLine string, paneWidth int) (result string) {
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

	rendered, err := renderMermaidSource(strings.Join(body, "\n"), paneWidth)
	if err != nil || strings.TrimSpace(rendered) == "" {
		return verbatim()
	}
	return rendered + "\n"
}

// mdPreviewStyle is the fixed glamour style used for the whole document in the
// normal (colored) preview. This is a personal patch on a local clone (see the
// plan), not an upstream feature, so there is deliberately no style generator
// deriving colors from revdiff's 23 theme fields — one bundled style, picked
// once. When --no-colors is in effect the preview uses mdPreviewStyleNoColor
// instead (see renderMarkdownDocument).
var mdPreviewStyle = glamourStyles.DarkStyleConfig

// mdPreviewStyleNoColor is glamour's ASCII/notty style: it carries no color,
// bold, or underline attributes at all, so it renders as plain text with ASCII
// markers (e.g. a leading "# " on headings, "**" around bold, "|" table
// separators) instead of color. Combined with the Ascii color profile in
// renderMarkdownDocument, it guarantees the no-colors preview emits zero ANSI
// escape sequences, honoring --no-colors / REVDIFF_NO_COLORS.
var mdPreviewStyleNoColor = glamourStyles.ASCIIStyleConfig

// mdPreviewMinWidth is the floor applied to the requested render width
// before handing it to glamour. glamour does not panic on width <= 0 (word
// wrap degrades to "no wrapping" internally), but a positive floor keeps
// prose reasonably readable instead of collapsing to a one-character-per-line
// render on a transient zero-width layout state.
const mdPreviewMinWidth = 8

// mermaidNonce returns a short random hex token unique to one
// renderMarkdownDocument call, used to build placeholders the source document
// cannot predict (see mermaidPlaceholder). Production code may use crypto/rand
// (unlike the workflow scripts). On the practically-impossible event of a
// crypto/rand read failure it returns a fixed token: the placeholder is then
// only as collision-resistant as the pre-nonce static string, which still
// renders correctly — it just loses the extra guarantee against a document
// that contains the token verbatim.
func mermaidNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// mermaidPlaceholder returns the sentinel text substituted for the idx'th
// mermaid diagram while the document is handed to glamour. nonce is a random
// per-render token (see mermaidNonce) so the placeholder is effectively
// uncollidable: the source document cannot contain a token built from a nonce
// it never saw, so a paragraph of prose can never be mistaken for a
// placeholder. The token is plain alphanumeric (letters, decimal digits, and
// hex nonce chars 0-9a-f) with no markdown or glamour/x-ansi word-wrap break
// characters (space, comma, period, semicolon, hyphen, plus, pipe — see
// x/ansi.Wordwrap) in it, so it always survives glamour's internal word-wrap
// pass as a single atomic, unsplit token and can be found again afterward.
func mermaidPlaceholder(nonce string, idx int) string {
	return fmt.Sprintf("mdpvw%sN%dN%s", nonce, idx, nonce)
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
//
// paneWidth is passed straight through to renderMermaidBlock, which is what
// makes the current viewport width available inside the classDiagram/
// stateDiagram-v2 transpiler's adaptive label cap (see mermaidLabelCap in
// mdpreview_transpile.go) — the whole reason this function has a width
// parameter at all, since renderMarkdownDocument is the only caller that
// ever has a real viewport width to offer.
func mermaidPlaceholderDocument(lines []diff.DiffLine, nonce string, paneWidth int) (doc string, arts []string) {
	idx := 0
	doc = joinWithMermaidFences(lines, func(body []string, openLine, closeLine string) string {
		arts = append(arts, renderMermaidBlock(body, openLine, closeLine, paneWidth))
		placeholder := mermaidPlaceholder(nonce, idx)
		idx++
		return "\n" + placeholder + "\n\n"
	})
	return doc, arts
}

// spliceMermaidArt replaces, for each art in order, the FIRST not-yet-consumed
// rendered line equal to that art's placeholder (after stripping glamour's ANSI
// styling and surrounding pad/margin whitespace) with the art itself, verbatim
// and unstyled. This is what makes the diagram reach the screen byte-exact: the
// art itself is never passed through glamour, only located and spliced in after
// the fact. nonce must be the same token mermaidPlaceholderDocument used.
//
// Matches are consumed: once a rendered line is used for one placeholder it
// cannot be reused for another, so a single value can never splice twice. This
// (together with the per-render nonce making placeholders uncollidable — see
// mermaidPlaceholder) is why a prose line can never steal a diagram's slot and
// one diagram can never overwrite two lines.
//
// A placeholder glamour did not place alone on its own line (should not
// happen given mermaidPlaceholderDocument's isolation and mermaidPlaceholder
// being word-wrap-atomic — see there) is left as visible plain text rather
// than silently dropping the diagram or panicking.
func spliceMermaidArt(rendered, nonce string, arts []string) string {
	if len(arts) == 0 {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	consumed := make([]bool, len(lines))
	for idx, art := range arts {
		placeholder := mermaidPlaceholder(nonce, idx)
		for i, line := range lines {
			if consumed[i] {
				continue
			}
			if strings.TrimSpace(ansi.Strip(line)) != placeholder {
				continue
			}
			lines[i] = strings.TrimSuffix(art, "\n")
			consumed[i] = true
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
//
// noColors selects the render style: the colored DarkStyleConfig by default,
// or the ASCII/notty style plus the Ascii color profile when --no-colors is in
// effect (so the preview emits no ANSI, matching the rest of the UI). The
// mermaid art is already plain box-drawing text spliced in after glamour, so
// it is unaffected by either path. Also NOTE (wide mermaid art): the art is
// deliberately never re-wrapped or truncated to fit the width, and the preview
// render path does not apply horizontal scroll (applyHorizontalScroll is a
// per-diff-line transform that this whole-document render bypasses), so a
// diagram wider than the pane is clipped — widen the terminal to see it. See
// PATCH.md "Known limitations".
func renderMarkdownDocument(lines []diff.DiffLine, width int, noColors bool) string {
	nonce := mermaidNonce()
	doc, arts := mermaidPlaceholderDocument(lines, nonce, width)

	w := max(width, mdPreviewMinWidth)

	opts := []glamour.TermRendererOption{glamour.WithStyles(mdPreviewStyle), glamour.WithWordWrap(w)}
	if noColors {
		opts = []glamour.TermRendererOption{
			glamour.WithStyles(mdPreviewStyleNoColor),
			glamour.WithWordWrap(w),
			glamour.WithColorProfile(termenv.Ascii),
		}
	}

	r, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		log.Printf("[WARN] create glamour renderer: %v", err)
		return doc
	}
	out, err := r.Render(doc)
	if err != nil {
		log.Printf("[WARN] render markdown preview: %v", err)
		return doc
	}
	return spliceMermaidArt(out, nonce, arts)
}

// toggleMarkdownPreview flips markdown preview mode on/off for the currently
// loaded file. Turning it ON is refused (no state change) unless
// m.file.markdownPreviewable is set — that flag marks a single, full-context
// markdown file (see the gate in loaders.go), which is exactly the condition
// under which a whole-document render is safe (no partially-shown table — see
// the plan's Overview). It is deliberately NOT gated on m.file.mdTOC: mdTOC is
// nil for a heading-less markdown file, which is still a valid full-context
// document that must be previewable. Turning it OFF is always allowed: gating
// the OFF transition too would strand the mode with no exit key if a file
// switch cleared markdownPreviewable while preview was on. The mode defaults
// to off.
//
// On the ON transition the viewport is reset to the top. It now shows the
// whole-document glamour render, whose rows do not map to m.file.lines, so the
// cursor-follow scroll math (syncViewportToCursor -> cursorVisualRange, all in
// diff-line coordinates) is meaningless here. On the OFF transition content
// and cursor are both back in diff-line space, so the normal
// keep-cursor-visible scroll is correct again.
func (m *Model) toggleMarkdownPreview() {
	if !m.modes.mdPreview && !m.file.markdownPreviewable {
		return
	}
	m.modes.mdPreview = !m.modes.mdPreview
	if m.modes.mdPreview {
		m.layout.viewport.SetContent(m.renderDiff())
		m.layout.viewport.GotoTop()
		return
	}
	m.syncViewportToCursor()
}

// renderMarkdownPreview renders the currently loaded file as a markdown
// preview at the current viewport width. It re-renders on every call, with no
// cache: the render only fires on a P toggle or a viewport content refresh,
// never per frame, so the one saved glamour+mermaid pass is not worth the
// staleness risk of a file+width-keyed cache surviving an R reload of the same
// file at the same width.
func (m Model) renderMarkdownPreview() string {
	// KNOWN LIMITATION: wide mermaid diagrams may be clipped in preview; widen
	// the terminal. This whole-document render bypasses applyHorizontalScroll
	// (a per-diff-line transform), so scroll_left/right cannot pan the art —
	// see PATCH.md "Known limitations".
	return renderMarkdownDocument(m.file.lines, m.layout.viewport.Width, m.cfg.noColors)
}

// mdPreviewAllowedActions is the fixed allowlist of keymap actions that stay
// live while markdown preview is on. Every action not in this set is a no-op
// while previewing (see mdPreviewActionAllowed and its call site in
// dispatchAction, app/ui/model.go) because it would create, edit, delete, or
// navigate to an annotation, or move/reposition m.nav.diffCursor — all
// meaningless once the diff pane shows one whole-document glamour render
// instead of one row per source line (see this plan's Solution Overview).
//
//   - toggle_preview must stay allowed so P can turn the mode back
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
