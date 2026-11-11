package metalist

import (
	"strconv"
	"strings"
)

// Hidden counts the live rows a list left out, by why, so the list can
// say so under its rows (owner report 2026-11-12: `decision list` showed
// 245 of 251 adopted decisions, the 6 scoped to their v0.1.17 repo, with
// nothing saying why, and `--all` did not exist).
type Hidden struct {
	// Repo: scoped to a repo the list's scope does not take (by default a
	// list shows the project-wide rows; --repo, --all-repos widen it).
	Repo int `json:"repo,omitempty"`
	// Archived: archived rows of a list that hides them.
	Archived int `json:"archived,omitempty"`
}

// Note is the footer line naming what was hidden and how to see it (""
// when nothing was): "6 hidden (6 scoped to a repo); --all shows them".
func (h Hidden) Note() string {
	var parts []string
	if h.Repo > 0 {
		parts = append(parts, strconv.Itoa(h.Repo)+" scoped to a repo")
	}
	if h.Archived > 0 {
		parts = append(parts, strconv.Itoa(h.Archived)+" archived")
	}
	if len(parts) == 0 {
		return ""
	}
	return strconv.Itoa(h.Repo+h.Archived) + " hidden (" + strings.Join(parts, ", ") + "); --all shows them"
}

// Notes appends the footer to a list's notes (unchanged when nothing was
// hidden).
func (h Hidden) Notes(notes []string) []string {
	if n := h.Note(); n != "" {
		return append(notes, n)
	}
	return notes
}
