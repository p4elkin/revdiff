package ui

import (
	"regexp"
	"strings"
)

// This file holds the shared no-break-space substitution used by every
// mermaid edge-label call site: mermaidEdgeLabel in mdpreview_transpile.go
// (the transpiled classDiagram/stateDiagram-v2 path) and
// normalizeFlowchartEdgeLabel in mdpreview_flowchart.go (the plain flowchart
// path). Like mdpreview.go, mdpreview_transpile.go, mdpreview_flowchart.go
// and mdpreview_subgraph.go it is patch-owned and does not exist upstream —
// see mdpreview.go's own doc comment for why new logic goes in new files
// rather than into an upstream one.
//
// # Why the substitution exists
//
// mergeDrawings in vendor/github.com/AlexanderGrooff/mermaid-ascii/cmd/draw.go
// composites the rendered layers with `if c != " "`: a plain space is
// transparent, so whatever character sits underneath it in a lower layer
// shows through. An edge label sits on its own arrow line, so a space inside
// the label lets the arrow's `─` bleed through and "read as fallback" comes
// out as "read─as─fallback". Where a second edge crosses the label's row the
// character underneath is `│` instead, corrupting the label outright rather
// than merely decorating it.
//
// U+00A0 NO-BREAK SPACE is not the byte `" "`, so mergeDrawings treats it as
// an opaque cell and keeps it, and terminals still draw it as a blank —
// visually identical to a real space but immune to the transparency rule.
// Two costs are accepted in exchange: art copied out of the terminal carries
// no-break spaces rather than plain ones, and a small number of fonts render
// U+00A0 visibly instead of blank.
//
// # Why a run can appear at all
//
// A source label that already contains a literal no-break space has its
// NEIGHBORING plain spaces converted too, so a label containing one real
// no-break space turns into a run of three once the substitution runs on the
// spaces around it. Collapsing a run down to one is safe because the
// substitution's purpose is purely "make this cell opaque" — a run of two or
// more opaque cells in a row reads no differently from one, so collapsing
// loses no information the reader can see.
const mermaidNBSP = " "

// mermaidNBSPRun matches a run of two or more consecutive no-break spaces,
// which mermaidNBSPSubstitute collapses to a single one.
var mermaidNBSPRun = regexp.MustCompile(mermaidNBSP + "{2,}")

// mermaidNBSPSubstitute replaces every space in s with a no-break space,
// then collapses any run of two or more no-break spaces this created
// (including a run seeded by a no-break space already present in s) down to
// one. See the file doc comment above for why both steps are needed. Both
// mermaidEdgeLabel and normalizeFlowchartEdgeLabel call this as their sole
// space-handling step.
func mermaidNBSPSubstitute(s string) string {
	s = strings.ReplaceAll(s, " ", mermaidNBSP)
	return mermaidNBSPRun.ReplaceAllString(s, mermaidNBSP)
}
