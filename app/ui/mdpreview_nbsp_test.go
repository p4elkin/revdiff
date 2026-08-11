package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- mermaidNBSPSubstitute ---

func TestMermaidNBSPSubstitute_SpaceReplacement(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"single space", "a b", "a" + mermaidNBSP + "b"},
		{"several spaces at different positions", "read as fallback",
			"read" + mermaidNBSP + "as" + mermaidNBSP + "fallback"},
		{"leading space", " leading", mermaidNBSP + "leading"},
		{"trailing space", "trailing ", "trailing" + mermaidNBSP},
		{"leading and trailing spaces", " both ", mermaidNBSP + "both" + mermaidNBSP},
		{"no spaces at all", "nospaces", "nospaces"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mermaidNBSPSubstitute(tt.in))
		})
	}
}

func TestMermaidNBSPSubstitute_RunCollapse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			// Two adjacent plain spaces each become a no-break space,
			// producing a run of two that must collapse to one.
			name: "two adjacent plain spaces collapse to one nbsp",
			in:   "a  b",
			want: "a" + mermaidNBSP + "b",
		},
		{
			// A label that already contains a literal no-break space has
			// its neighboring plain spaces converted too, seeding a run
			// of three that must collapse to one — see the file doc
			// comment's "Why a run can appear at all" section.
			name: "literal nbsp already in source seeds a run",
			in:   "a " + mermaidNBSP + " b",
			want: "a" + mermaidNBSP + "b",
		},
		{
			// Three or more plain spaces in a row must collapse the same
			// way as two.
			name: "long run of plain spaces collapses to one nbsp",
			in:   "a    b",
			want: "a" + mermaidNBSP + "b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, mermaidNBSPSubstitute(tt.in))
		})
	}
}
