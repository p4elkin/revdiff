package ui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	glamourStyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/keymap"
	"github.com/umputun/revdiff/app/ui/style"
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
// NOTE: this is the unconstrained-width entry point, and it has no
// production caller. Production always renders through
// renderMarkdownDocument -> mermaidPlaceholderDocument, which knows the real
// pane width. This function is retained purely for the eleven pre-existing
// tests in mdpreview_test.go that predate the transpiler and only ever check
// a mermaid fence's rendered art, never its sizing: keeping the original
// one-argument signature is what let renderMermaidBlock gain its paneWidth
// parameter (see its doc comment, and mdpreview_transpile.go's adaptive
// label cap) without touching any of them. mermaidUnconstrainedWidth tells
// the adaptive cap to skip sizing entirely and behave like every other
// (non-transpiled) diagram type: unconstrained. Anything that needs to
// verify width-dependent behavior must go through renderMarkdownDocument
// instead.
func renderMermaidFences(lines []diff.DiffLine) string {
	doc, _ := joinWithMermaidFences(lines, func(body []string, openLine, closeLine string) string {
		return renderMermaidBlock(body, openLine, closeLine, mermaidUnconstrainedWidth)
	})
	return doc
}

// joinWithMermaidFences is the shared fence-scanning walk behind
// renderMermaidFences and mermaidPlaceholderDocument. renderBlock is called
// once per complete mermaid fence (body lines, opening fence line, closing
// fence line) and its return value is written verbatim in place of the
// fence — renderMermaidFences passes renderMermaidBlock (inline rendered
// art), mermaidPlaceholderDocument passes a callback that defers rendering
// and substitutes a placeholder instead (see renderMarkdownDocument for why).
//
// origins is the per-emitted-document-line provenance: origins[i] is the index
// into lines that produced document line i+1. It exists because the document
// this builds is NOT line-for-line with lines — divider rows are dropped and
// every mermaid fence collapses to whatever renderBlock returns — so a
// document line number cannot be used as a diff-line index without it. The
// markdown source map (mdpreview_srcmap.go) is its only consumer; the two
// other callers discard it. Every line of a fence's replacement text is
// attributed to the fence's OPENING line, so a comment anywhere on a rendered
// diagram anchors to the ```mermaid line the reader sees in source view.
func joinWithMermaidFences(lines []diff.DiffLine,
	renderBlock func(body []string, openLine, closeLine string) string) (doc string, origins []int) {
	var out strings.Builder

	var fenceChar rune // 0 when outside any fence
	var fenceLen int   // length of the opening fence marker
	var fenceLang string
	var fenceStart int         // index into lines of the opening fence line
	var fenceBody []string     // body lines collected while inside a mermaid fence
	var fenceBodyOrigins []int // the index into lines each fenceBody entry came from

	writeLine := func(content string, origin int) {
		out.WriteString(content)
		out.WriteString("\n")
		origins = append(origins, origin)
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
			fenceBody, fenceBodyOrigins = nil, nil
			if fenceLang != "mermaid" {
				writeLine(content, i)
			}
			continue
		case fenceChar != 0 && ch == fenceChar && n >= fenceLen && strings.TrimSpace(trimmed[n:]) == "":
			// closing fence
			if fenceLang == "mermaid" {
				replacement := renderBlock(fenceBody, lines[fenceStart].Content, content)
				out.WriteString(replacement)
				for range strings.Count(replacement, "\n") {
					origins = append(origins, fenceStart)
				}
			} else {
				writeLine(content, i)
			}
			fenceChar = 0
			fenceLen = 0
			fenceLang = ""
			fenceBody, fenceBodyOrigins = nil, nil
			continue
		}

		if fenceChar != 0 {
			if fenceLang == "mermaid" {
				fenceBody = append(fenceBody, content)
				fenceBodyOrigins = append(fenceBodyOrigins, i)
			} else {
				writeLine(content, i)
			}
			continue
		}

		writeLine(content, i)
	}

	// an unterminated mermaid fence extends to end of document; its opening
	// line and body were deferred (not yet written) waiting for a close that
	// never came, so flush them verbatim rather than losing them. Non-mermaid
	// fences are written eagerly above and need no flush here.
	if fenceChar != 0 && fenceLang == "mermaid" {
		writeLine(lines[fenceStart].Content, fenceStart)
		for bi, b := range fenceBody {
			// the recorded index, never fenceStart+1+bi: the loop above skips
			// ChangeDivider rows, so a divider inside the fence would make the
			// arithmetic drift and misattribute every body line after it.
			writeLine(b, fenceBodyOrigins[bi])
		}
	}

	return out.String(), origins
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
// renderMarkdownDocument, it guarantees the RENDERED DOCUMENT emits zero ANSI
// escape sequences, honoring --no-colors / REVDIFF_NO_COLORS.
//
// The promise covers the document, not the whole frame. Annotation rows spliced
// into the preview (mdpreview_annotate.go) are painted by the diff pane's own
// renderAnnotationOrInput, whose style.Renderer.AnnotationInline emits a literal
// italic pair regardless of the resolver — so a --no-colors preview carrying an
// annotation does contain escapes. That is the same output source view produces
// under --no-colors for the same annotation, and matching it is the point.
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
//
// origins is joinWithMermaidFences' per-document-line provenance, passed
// straight through — see there. Only the markdown source map uses it.
func mermaidPlaceholderDocument(lines []diff.DiffLine, nonce string, paneWidth int) (doc string, arts []string, origins []int) {
	idx := 0
	doc, origins = joinWithMermaidFences(lines, func(body []string, openLine, closeLine string) string {
		arts = append(arts, renderMermaidBlock(body, openLine, closeLine, paneWidth))
		placeholder := mermaidPlaceholder(nonce, idx)
		idx++
		return "\n" + placeholder + "\n\n"
	})
	return doc, arts, origins
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
	out, _ := spliceMermaidArtTracked(rendered, nonce, arts)
	return out
}

// mdPreviewArtShift records that the splice of one diagram turned a single
// rendered row into several: row is the placeholder's row in the PRE-splice
// render (0-based) and added is how many rows the art introduced beyond it.
// Every pre-splice row strictly greater than row therefore moves down by
// added. See mdPreviewShiftRow (mdpreview_srcmap.go), which is the only
// consumer — the markdown source map's rows are produced against the
// pre-splice render because that is the one the markers were extracted from.
type mdPreviewArtShift struct {
	row   int
	added int
}

// spliceMermaidArtTracked is spliceMermaidArt plus the row shifts its
// substitutions cause. The shifts are in pre-splice row coordinates and in
// ascending row order. See spliceMermaidArt for the substitution rules; this
// function is the implementation and that one is a thin wrapper for the
// callers that do not need the shifts.
func spliceMermaidArtTracked(rendered, nonce string, arts []string) (string, []mdPreviewArtShift) {
	if len(arts) == 0 {
		return rendered, nil
	}
	lines := strings.Split(rendered, "\n")
	consumed := make([]bool, len(lines))
	var shifts []mdPreviewArtShift
	for idx, art := range arts {
		placeholder := mermaidPlaceholder(nonce, idx)
		for i, line := range lines {
			if consumed[i] {
				continue
			}
			if strings.TrimSpace(ansi.Strip(line)) != placeholder {
				continue
			}
			replacement := mermaidArtWithoutControls(strings.TrimSuffix(art, "\n"))
			lines[i] = replacement
			consumed[i] = true
			if added := strings.Count(replacement, "\n"); added > 0 {
				shifts = append(shifts, mdPreviewArtShift{row: i, added: added})
			}
			break
		}
	}
	sort.Slice(shifts, func(a, b int) bool { return shifts[a].row < shifts[b].row })
	return strings.Join(lines, "\n"), shifts
}

// mermaidArtWithoutControls drops C0 control bytes and DEL from one diagram's
// spliced-in art, keeping newline and tab. The art bypasses glamour entirely
// (see spliceMermaidArt), so without this, a raw ESC written into a node
// label — or sitting in the fence text renderMermaidBlock falls back to
// verbatim — would reach the terminal as a live escape sequence and repaint
// the screen. flowchartSubgraphHeading already drops the same bytes from a
// stacked block's heading; this covers the rest of the art with it.
//
// This is NOT a document-wide escape filter — it only cleans the art this
// function is handed. Prose, headings, table cells and non-mermaid code
// fences all go through glamour instead, and glamour does not strip control
// bytes from the source text either, so a raw ESC anywhere else in the
// markdown still reaches the terminal untouched. See PATCH.md's "Known
// limitations" for that wider gap; closing it is a separate change.
//
// The diagram's own box-drawing glyphs are multi-byte UTF-8, never C0, so
// nothing here can change the shape of a diagram. Tab is kept because the
// verbatim fallback is source text, whose indentation is part of what a reader
// is meant to see.
func mermaidArtWithoutControls(art string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if mermaidControlRune(r) {
			return -1
		}
		return r
	}, art)
}

// mermaidControlRune reports whether r is a C0 control byte or DEL — the bytes
// that must never reach the terminal from text this patch writes outside
// glamour. One definition, two callers: mermaidArtWithoutControls keeps newline
// and tab before consulting it, flowchartSubgraphHeading drops those two as
// well because a stacked block's heading is a single line.
func mermaidControlRune(r rune) bool {
	return r < ' ' || r == 0x7f
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
// it is unaffected by either path.
//
// The returned render is full width — rows wider than the pane are NOT cut
// here, deliberately: this function's whole job is to produce the document
// once, at its natural width, so the caller can decide which columns of it to
// show. Cutting to the visible window is applyMdPreviewScroll's job, and it
// needs the uncut render both to compute the pan clamp (the widest row) and to
// know which rows actually overflow.
func renderMarkdownDocument(lines []diff.DiffLine, width int, noColors bool) string {
	nonce := mermaidNonce()
	doc, arts, _ := mermaidPlaceholderDocument(lines, nonce, width)

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
// m.file.markdownPreviewable is set — that flag marks a full-context markdown
// file (see the gate in loaders.go), which is exactly the condition under which
// a whole-document render is safe (no partially-shown table — see the plan's
// Overview). The review may hold any number of other files: full context is a
// property of the displayed file alone. It is deliberately NOT gated on
// m.file.mdTOC either, for two separate reasons — mdTOC is nil for a
// heading-less markdown file, which is still a valid full-context document, and
// it is nil for EVERY file of a multi-file review because the TOC would have to
// take the file tree's pane. Turning it OFF is always allowed: gating
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
//
// Both transitions also reset the horizontal offset. m.layout.scrollX is
// shared with the diff render, but the two mean different things: in the diff
// it is an unbounded per-line cut offset, in preview it is clamped against the
// widest rendered row (see panMarkdownPreview). Carrying one into the other
// would either drop the reader into the middle of a document they never
// panned, or leave the diff pane scrolled to a column that only made sense for
// a diagram. A file load resets it too, in handleFileLoaded (loaders.go).
func (m *Model) toggleMarkdownPreview() {
	if !m.modes.mdPreview && !m.file.markdownPreviewable {
		return
	}
	m.modes.mdPreview = !m.modes.mdPreview
	m.layout.scrollX = 0
	if m.modes.mdPreview {
		m.layout.viewport.SetContent(m.renderDiff())
		m.layout.viewport.GotoTop()
		return
	}
	m.syncViewportToCursor()
}

// renderMarkdownPreview renders the currently loaded file as a markdown
// preview at the current viewport width, cut to the visible column window at
// the current horizontal offset (see applyMdPreviewScroll). The base render
// goes through mdPreviewBaseRender (mdpreview_cache.go), keyed on
// file/loadSeq/width/noColors, so a repaint at an unchanged state (the
// scroll-following highlight repainting on every offset change, in a later
// task) reuses the last glamour+mermaid pass instead of paying for a fresh
// one. loadSeq is what makes the key safe across an R reload of the same file
// at the same width: reload bumps it even though the file name and width do
// not change, so a stale render can never satisfy a post-reload lookup.
//
// The frame it returns is the composed one — base render, this file's
// annotations painted under the blocks they belong to, and the scroll-following
// block highlight — assembled by mdPreviewFinalRender (mdpreview_cache.go),
// which owns the order those three steps run in.
//
// A pan keypress does NOT go through here — panMarkdownPreview composes the
// frame itself, because it needs the uncut body to compute the clamp and would
// otherwise cut a second copy of the same render to draw the same thing.
func (m Model) renderMarkdownPreview() string {
	return m.mdPreviewFinalRender()
}

// mdPreviewCutWidth returns how many columns of a rendered preview row are
// actually visible. The preview render goes straight into the diff viewport,
// which hard-truncates every row at its own Width (the lipgloss MaxWidth in
// viewport.View), and the diff pane style adds a border but no padding — so
// the viewport width IS the visible column count.
//
// This deliberately does NOT reuse applyHorizontalScroll's basis
// (m.diffContentWidth() - m.gutterExtra()). That width is what is left over
// after the per-diff-line render has spent columns on the cursor bar, the
// line-number/blame gutters and the right padding column it draws itself. A
// preview row has none of those — it is one whole-document glamour render
// handed to the viewport verbatim — so borrowing the diff basis would cut two
// columns short of the pane on every single row.
func (m Model) mdPreviewCutWidth() int {
	return m.layout.viewport.Width
}

// mdPreviewMaxLineWidth returns the display width of the widest row in a
// rendered preview. This is the pan clamp's basis, and it must be measured on
// the rendered document, not on m.file.lines: the source line of a mermaid
// fence is a few dozen characters while the art it renders to can be 200+
// cells, so a diff-line-derived bound would stop the pan long before the
// diagram's right edge.
//
// Trailing pad counts as width. glamour pads prose rows with spaces (it fills
// the line to the wrap width, two columns short of it in practice), so a
// document with no wide art clamps to zero and the pan keys simply do nothing
// — which is the wanted behavior: there is nothing to pan to.
func mdPreviewMaxLineWidth(rendered string) int {
	widest := 0
	for line := range strings.SplitSeq(rendered, "\n") {
		if w := ansi.StringWidth(line); w > widest {
			widest = w
		}
	}
	return widest
}

// mdPreviewMaxOffset returns the largest horizontal offset worth showing: the
// one that puts the widest row's last column at the right edge of the pane.
// Zero when everything already fits.
func (m Model) mdPreviewMaxOffset(rendered string, cutWidth int) int {
	return max(0, m.mdPreviewWidestRow(rendered)-cutWidth)
}

// mdPreviewWidestRow is mdPreviewMaxLineWidth through the memo on the render
// cache (see mdPreviewScrollCache), which is what keeps the full-document
// grapheme scan off every repaint. Falls back to the direct scan for a Model
// built without NewModel, where the cache pointer is nil.
func (m Model) mdPreviewWidestRow(rendered string) int {
	if m.mdPreviewCache == nil {
		return mdPreviewMaxLineWidth(rendered)
	}
	return m.mdPreviewCache.scroll.widestOf(rendered)
}

// applyMdPreviewScroll cuts every row of a rendered preview to the visible
// column window at the current horizontal offset, with «/» indicators on the
// rows that really do continue in that direction.
//
// The offset is clamped here as well as in panMarkdownPreview, against the
// render being cut. The pan handler clamps the stored offset, but the stored
// offset can go stale without any pan: a terminal resize re-wraps the document
// at a new width, which can make the widest row narrower. Clamping at render
// time means a resize can never strand the reader on a blank screen.
//
// Rejected: reusing applyHorizontalScroll. It is built for diff rows — it
// derives its width from the gutters (see mdPreviewCutWidth) and lets the
// right indicator spill one column past the cut into the diff pane's right
// padding, which a preview row does not have. It shares the indicator glyphs
// and ansi.Cut with this function, which is what keeps the two visually
// identical, but not the geometry.
func (m Model) applyMdPreviewScroll(rendered string) string {
	cutWidth := m.mdPreviewCutWidth()
	if cutWidth <= 0 {
		return rendered // pathologically narrow layout state: leave the render alone
	}
	widest := m.mdPreviewWidestRow(rendered)
	offset := min(max(0, m.layout.scrollX), max(0, widest-cutWidth))
	if offset == 0 && widest <= cutWidth {
		return rendered // nothing hidden in either direction: pass the render through untouched
	}

	compute := func() string { return m.cutMdPreviewRows(rendered, offset, cutWidth) }
	if m.mdPreviewCache == nil {
		return compute() // Model built without NewModel: no cache to memoize into
	}
	indicators := m.mdPreviewLeftIndicator() + m.mdPreviewRightIndicator()
	return m.mdPreviewCache.scroll.cutOf(rendered, offset, cutWidth, indicators, compute)
}

// cutMdPreviewRows cuts every row of rendered. Split out of
// applyMdPreviewScroll so the memo (mdPreviewScrollCache) has one function to
// call on a miss and the clamp/early-return logic stays above it.
func (m Model) cutMdPreviewRows(rendered string, offset, cutWidth int) string {
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		lines[i] = m.cutMdPreviewLine(line, offset, cutWidth)
	}
	return strings.Join(lines, "\n")
}

// cutMdPreviewLine cuts one rendered preview row to cutWidth columns starting
// at offset, adding the overflow indicators the row has earned. Both
// indicators are drawn INSIDE cutWidth (« replaces the first visible column,
// " »" takes the last two), because the viewport truncates at exactly that
// width — unlike the diff path, there is no spare padding column to spill the
// right glyph into.
//
// A row that ends at or before the offset returns the empty string. Prose is
// already wrapped to the pane by glamour, so this is the common case once the
// reader pans right to read a diagram, not an edge case. Returning "" rather
// than the ansi.Cut result is deliberate: cutting past the end of a styled row
// leaves the escape sequences it walked over (e.g. "\x1b[32m\x1b[0m"), a
// zero-width remnant whose trailing state can bleed into the padding the pane
// adds after it.
func (m Model) cutMdPreviewLine(line string, offset, cutWidth int) string {
	width := ansi.StringWidth(line)
	if width <= offset {
		return ""
	}

	start, end := offset, offset+cutWidth
	hasLeftOverflow := offset > 0 // guaranteed non-empty to the left, since width > offset here
	hasRightOverflow := width > end
	if !hasLeftOverflow && !hasRightOverflow {
		return ansi.Cut(line, start, end)
	}

	innerStart, innerEnd := start, end
	if hasLeftOverflow {
		innerStart++
	}
	if hasRightOverflow {
		innerEnd -= 2 // the separator space and the » glyph
	}
	if innerEnd <= innerStart {
		// pane too narrow to hold the indicators plus any content: plain cut,
		// matching applyHorizontalScroll's fallback rather than rendering a row
		// made only of chrome.
		return ansi.Cut(line, start, end)
	}

	var b strings.Builder
	if hasLeftOverflow {
		b.WriteString(m.mdPreviewLeftIndicator())
	}
	b.WriteString(ansi.Cut(line, innerStart, innerEnd))
	if hasRightOverflow {
		b.WriteString(m.mdPreviewRightIndicator())
	}
	return b.String()
}

// mdPreviewLeftIndicator returns the « glyph for a preview row, on the diff
// pane background so it reads as pane chrome — a preview row has no per-line
// diff background for it to sit on, the way an added/removed diff line does.
//
// In no-colors mode it is a bare glyph. The shared leftScrollIndicator falls
// back to reverse video ("\x1b[7m") there, which is right on a colored diff
// line but would break the promise --no-colors makes for the preview
// specifically: zero ANSI in the output (see mdPreviewStyleNoColor). Rejected:
// dropping the indicators entirely in no-colors mode — the reader would then
// have no sign that the diagram continues past the edge.
func (m Model) mdPreviewLeftIndicator() string {
	if m.cfg.noColors {
		return "«"
	}
	return m.leftScrollIndicator(m.resolver.Color(style.ColorKeyDiffPaneBg))
}

// mdPreviewRightIndicator returns the " »" separator-plus-glyph for a preview
// row. Same background and same no-colors reasoning as mdPreviewLeftIndicator.
func (m Model) mdPreviewRightIndicator() string {
	if m.cfg.noColors {
		return " »"
	}
	return m.rightScrollIndicator(m.resolver.Color(style.ColorKeyDiffPaneBg))
}

// panMarkdownPreview moves the preview's horizontal offset one scroll step,
// left when direction < 0 and right otherwise, and pushes the re-cut render
// into the viewport. Mirrors handleHorizontalScroll's shape for the diff pane.
//
// It composes the frame itself (mdPreviewBody + the same cut and highlight
// mdPreviewFinalRender applies) instead of going through renderDiff: the clamp
// needs the widest row of the uncut body, and that same body then supplies the
// rows to cut. Going through the cache (mdpreview_cache.go) means a pan
// keypress only pays for a fresh glamour+mermaid pass on the first call at a
// given file/width/color state — every pan step after that, and every
// renderMarkdownPreview call in between, reuses the same cached render.
//
// The stored offset is folded through the current clamp before the step is
// applied, so an offset left over from a wider layout converges back into
// range on the first pan instead of needing several presses to become
// visible again.
//
// Refused when the file is not previewable, matching renderDiff's own double
// gate (m.modes.mdPreview && m.file.markdownPreviewable): with preview stuck
// on for a file the render path will not preview, panning would push a
// preview render into a viewport that renderDiff is about to fill with a
// normal diff.
func (m *Model) panMarkdownPreview(direction int) {
	if !m.file.markdownPreviewable {
		return
	}
	body, srcMap := m.mdPreviewBody()
	maxOffset := m.mdPreviewMaxOffset(body, m.mdPreviewCutWidth())

	offset := min(m.layout.scrollX, maxOffset)
	if direction < 0 {
		offset -= scrollStep
	} else {
		offset += scrollStep
	}
	m.layout.scrollX = min(max(0, offset), maxOffset)

	m.layout.viewport.SetContent(m.mdPreviewHighlight(m.applyMdPreviewScroll(body), srcMap))
}

// handleMdPreviewAction is the preview-mode gate every keymap-resolved action
// passes through, called from dispatchAction (app/ui/model.go) while
// m.modes.mdPreview is on. The bool reports whether preview handled the
// action: true means dispatchAction returns immediately (either the action
// was blocked, or preview ran it here), false means the action is allowed and
// falls through to the ordinary dispatch. The tea.Cmd return exists for
// exactly one case: ActionConfirm starts an annotation input, whose textinput
// Focus() returns a cmd that must reach the Update loop the same way
// handleEnterKey's does in source view. Every other action handled here is a
// pure state change plus a viewport content swap, done in place — their cmd
// is always nil.
//
// The two pan actions are routed here rather than left to fall through, and
// that detour is required, not stylistic: scroll_right doubles as the
// focus-diff action in both pane handlers ("case keymap.ActionFocusDiff,
// keymap.ActionScrollRight:" in handleTreeAction and handleTOCNav,
// app/ui/diffnav.go). Falling through with the TOC pane focused would move
// focus instead of panning, and on the file-tree branch would additionally
// clear the pending jumps and call loadSelectedIfChanged — exactly the class
// of side effect the allowlist exists to prevent. scroll_left has no such
// double meaning; it is routed the same way for symmetry, and because the
// diff-pane handler's handleHorizontalScroll would cut diff rows that the
// preview render does not have.
//
// The vertical reading keys (down/up, page, half-page, home/end) are routed
// here for the same reason and must never fall through: their ordinary
// handlers are the moveDiffCursor* family (handleDiffMovement, diffnav.go),
// which reposition m.nav.diffCursor in diff-line coordinates the preview
// render does not have. Preview has no cursor, so here they mean exactly what
// they look like — move the viewport — and are served by shifting YOffset
// directly. Without this, the only way to read past the first screen was
// J/K, and every key a reader reaches for first was silently dead.
func (m Model) handleMdPreviewAction(action keymap.Action) (tea.Model, tea.Cmd, bool) {
	if !mdPreviewActionAllowed(action) {
		return m, nil, true
	}
	switch action {
	case keymap.ActionConfirm:
		if m.layout.focus != paneDiff {
			return m, nil, true // same focus gate handleFileAnnotateKey applies to A
		}
		cmd := m.startPreviewAnnotation()
		return m, cmd, true
	case keymap.ActionScrollLeft:
		m.panMarkdownPreview(-1)
		return m, nil, true
	case keymap.ActionScrollRight:
		m.panMarkdownPreview(1)
		return m, nil, true
	case keymap.ActionDown:
		m.scrollMarkdownPreview(1)
		return m, nil, true
	case keymap.ActionUp:
		m.scrollMarkdownPreview(-1)
		return m, nil, true
	case keymap.ActionPageDown:
		m.scrollMarkdownPreview(m.mdPreviewPageStep())
		return m, nil, true
	case keymap.ActionPageUp:
		m.scrollMarkdownPreview(-m.mdPreviewPageStep())
		return m, nil, true
	case keymap.ActionHalfPageDown:
		m.scrollMarkdownPreview(max(1, m.mdPreviewPageStep()/2))
		return m, nil, true
	case keymap.ActionHalfPageUp:
		m.scrollMarkdownPreview(-max(1, m.mdPreviewPageStep()/2))
		return m, nil, true
	case keymap.ActionHome:
		m.layout.viewport.GotoTop()
		return m, nil, true
	case keymap.ActionEnd:
		m.layout.viewport.GotoBottom()
		return m, nil, true
	default: // every other allowed action runs through the ordinary dispatch
	}
	return m, nil, false
}

// mdPreviewPageStep is the row count one page key moves the preview viewport.
// One screen less a row of overlap, so the line you were reading when you hit
// the key is still on screen after it — the same convention as a pager.
func (m Model) mdPreviewPageStep() int {
	return max(1, m.layout.viewport.Height-1)
}

// scrollMarkdownPreview shifts the preview viewport by delta rows, clamped to
// the content, and repaints so the block highlight lands on the block that is
// now topmost. It deliberately does NOT go through scrollDiffViewportLine (the
// J/K path): that helper follows the shift with pinDiffCursorTo, which is a
// no-op under preview only because of an explicit mdPreview guard in mouse.go.
// Preview has no cursor to pin, so it calls the pure shifter directly and the
// guard stays a backstop for the wheel rather than load-bearing here.
//
// The repaint is immediate rather than deferred, and that is the one place this
// path differs from the wheel. A key press is one event; the wheel arrives in
// bursts of hundreds, which is what wheelState's debounce exists for (see
// .claude/rules/gotchas.md), and the wheel keeps using it — flushWheelPending
// repaints preview once at burst end. Repainting here costs a cached base render
// plus two per-row passes, not a glamour render, so a held-down j does not need
// its own debounce beside that one.
//
// A no-op scroll (already at an edge) repaints nothing: the offset did not move,
// so neither did the highlight.
func (m *Model) scrollMarkdownPreview(delta int) {
	if !m.scrollDiffViewportBy(delta) {
		return
	}
	if !m.file.markdownPreviewable {
		return // preview stuck on for a file renderDiff will not preview; see panMarkdownPreview
	}
	m.layout.viewport.SetContent(m.renderMarkdownPreview())
}

// mdPreviewAllowedActions is the fixed allowlist of keymap actions that stay
// live while markdown preview is on. Every action not in this set is a no-op
// while previewing (see mdPreviewActionAllowed and its call site in
// dispatchAction, app/ui/model.go). Most of the excluded set still shares one
// reason — it would edit, delete, or navigate to an annotation by index, or
// move/reposition m.nav.diffCursor in diff-line coordinates the preview
// render does not have — but that is no longer a blanket rule: ActionConfirm
// (a/enter) is allowed and DOES create an annotation, anchored to a block
// through the source map instead of through m.nav.diffCursor's ordinary
// meaning. Each remaining exclusion is explained on its own below rather than
// folded into one shared sentence.
//
//   - toggle_preview must stay allowed so P can turn the mode back
//     off — this is the mode's only exit key.
//   - confirm (a/enter) creates a line-level annotation anchored to the block
//     the scroll-following highlight currently marks (mdPreviewHighlightAnchor)
//     — see startPreviewAnnotation, mdpreview_annotate.go. It is routed INSIDE
//     handleMdPreviewAction above rather than left to fall through: its
//     ordinary fall-through target, handleEnterKey, branches on pane focus,
//     and the tree/TOC pane is reachable while previewing (nothing in this
//     allowlist changes m.layout.focus), where handleEnterKey would run the
//     TOC jump — reassigning m.nav.diffCursor and the viewport offset in
//     diff-line coordinates, exactly the class of bug this allowlist exists
//     to prevent. It carries the same focus gate annotate_file does: with the
//     tree/TOC pane focused it does nothing, so the two annotation-creating
//     keys agree on what focus means instead of one of them silently opening
//     an input the reader was not aiming with.
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
//   - scroll_left/scroll_right (left/right arrows) pan the render sideways so
//     mermaid art wider than the pane can be read at all — the art is never
//     re-wrapped to fit (see renderMarkdownDocument), so panning is the only
//     way to reach it. They are safe for the same reason J/K are: the whole
//     path is panMarkdownPreview -> renderMarkdownDocument ->
//     applyMdPreviewScroll -> viewport.SetContent, which writes
//     m.layout.scrollX and the viewport's content buffer and nothing else —
//     it never reads or assigns m.nav.diffCursor and never touches m.store.
//     Both are dispatched by handleMdPreviewAction before the ordinary pane
//     routing can see them; see there for why scroll_right in particular must
//     not be allowed to fall through.
//   - down/up (j/k and the arrows), page_down/page_up, half_page_down/
//     half_page_up and home/end are the ordinary reading keys. Their normal
//     handlers move m.nav.diffCursor, which is why they were originally
//     blocked — but blocking them left J/K as the only way to reach past the
//     first screen, which is not what anyone reaches for. They are allowed
//     here and re-routed in handleMdPreviewAction onto the viewport itself,
//     so they move the render and never the cursor. They must never fall
//     through: the fall-through target is handleDiffMovement (diffnav.go),
//     i.e. the exact cursor motion this mode exists to avoid.
//   - annotate_file (A) creates a file-level annotation the same way source
//     view does — handleFileAnnotateKey (app/ui/handlers.go) falls all the
//     way through to startFileAnnotation (app/ui/annotate.go) unmodified. A
//     file-level annotation's Line is always 0, and mdPreviewPaintAnnotationsTracked
//     (mdpreview_annotate.go) already paints Line-0 rows ahead of every block
//     unconditionally, so there is no block to resolve and no source map to
//     consult — unlike confirm, this needs no routing here. It is gated the
//     same as source view: handleFileAnnotateKey is a no-op unless the diff
//     pane has focus, so it cannot fire while the tree/TOC pane is focused.
//   - flush_output (O) writes the current annotation store to the --output
//     file (handleFlushOutput, app/ui/output.go). It never reads or assigns
//     m.nav.diffCursor and never touches the viewport, so it is safe to let
//     it fall through unmodified — creating annotations in preview without a
//     way to flush them out would be half a feature.
//   - delete_annotation (d) is excluded: deleteAnnotation resolves its target
//     from m.nav.diffCursor plus m.annot.cursorOnAnnotation, and preview drives
//     neither — the cursor points at whichever block the last a/click aimed at,
//     and cursorOnAnnotation is cleared by startPreviewAnnotationAt. To remove
//     a comment made in preview, press P, delete it in source view, press P
//     again.
//   - annot_list (@) is excluded: the overlay's whole purpose is its jump
//     outcome, and that routes through jumpToAnnotationTarget ->
//     positionOnAnnotation, which reassigns m.nav.diffCursor and moves the
//     viewport in diff-line coordinates the preview render does not have. A
//     popup whose one action is unusable is worse than no popup.
//   - next_annotation / prev_annotation (} and {) are excluded for exactly
//     that reason: they ARE the same jump, with the target picked by store
//     order instead of by the popup.
//
// Deliberately NOT included, despite being layout/session actions with no
// obvious annotation/cursor risk on their own: toggle_pane / focus_tree /
// focus_diff (switching focus into the TOC pane is pointless once TOC
// navigation itself is blocked below), info, reload,
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
// harmless "file navigation". They stay excluded in every review shape, for
// two different reasons — one per branch of handleFileOrSearchNav:
//
//   - with a search still live (a search run before P was pressed leaves
//     m.search.matches populated until the user presses esc or another file
//     loads), the FIRST branch wins in any review, single- or multi-file. Note
//     esc is itself an allowed preview action, so this is a state the reader
//     can be in, and can leave, without ever exiting preview. It calls
//     nextSearchMatch/prevSearchMatch, which reassign m.nav.diffCursor and
//     then centerViewportOnCursor — a viewport jump computed in diff-line
//     coordinates that the preview render does not have. That is exactly the
//     class of bug this allowlist exists to prevent.
//   - with no search live and a single-file markdown review, the TOC branch
//     wins: jumpTOCEntry calls syncDiffToTOCCursor, which unconditionally
//     reassigns m.nav.diffCursor. Same bug, reached through TOC navigation
//     instead of the diff pane's own j/k.
//
// In a multi-file review with no search live the third branch would be
// reachable and would merely switch files (StepFile + loadSelectedIfChanged,
// no cursor write). Allowing it only in that case would make the allowlist
// depend on runtime state, and would still have to keep the search branch
// out. So the answer stays "no": to move between files, press P first, then
// navigate, then press P again on the next markdown file.
var mdPreviewAllowedActions = map[keymap.Action]bool{
	keymap.ActionTogglePreview:  true,
	keymap.ActionConfirm:        true,
	keymap.ActionQuit:           true,
	keymap.ActionDiscardQuit:    true,
	keymap.ActionHelp:           true,
	keymap.ActionThemeSelect:    true,
	keymap.ActionToggleTree:     true,
	keymap.ActionScrollDiffDown: true,
	keymap.ActionScrollDiffUp:   true,
	keymap.ActionScrollLeft:     true,
	keymap.ActionScrollRight:    true,
	keymap.ActionDown:           true,
	keymap.ActionUp:             true,
	keymap.ActionPageDown:       true,
	keymap.ActionPageUp:         true,
	keymap.ActionHalfPageDown:   true,
	keymap.ActionHalfPageUp:     true,
	keymap.ActionHome:           true,
	keymap.ActionEnd:            true,
	keymap.ActionDismiss:        true,
	keymap.ActionAnnotateFile:   true,
	keymap.ActionFlushOutput:    true,
}

// mdPreviewActionAllowed reports whether action may run while markdown
// preview is on. Called once, from handleMdPreviewAction, which is itself
// called at the top of dispatchAction (app/ui/model.go)
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
