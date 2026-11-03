// Package xref holds cross-project references (LORE_TEAM_SPEC F15): a row
// in one project pointing at a row in another, written for people as
// acme/app:tsk_01, and the one rule that keeps a pointer from revealing a
// hidden row — pointers flow inward only.
//
//	from \ to   client   internal   personal
//	client      yes      NO         NO
//	internal    yes      yes        NO
//	personal    yes      yes        NO (another person's) / yes (same folder)
//
// "client" is a project the client can read (a client repo); "internal" is
// an org store project; "personal" is a personal folder. A pointer stored
// in a place readers of the target cannot see is harmless; the reverse
// tells the client a hidden row exists (its id) even when its text stays
// hidden.
package xref

import (
	"errors"
	"fmt"
	"strings"
)

// Kind is who can read a project.
type Kind string

// Kinds.
const (
	Client   Kind = "client"
	Internal Kind = "internal"
	Personal Kind = "personal"
)

// Ref is one reference: (project id, row id). ProjectID "" means "the same
// project as the row holding the reference" (every existing row stays
// valid, C16).
type Ref struct {
	ProjectID string
	RowID     string
}

// String renders the people form: "<project>:<row>" or just "<row>".
func (r Ref) String() string {
	if r.ProjectID == "" {
		return r.RowID
	}
	return r.ProjectID + ":" + r.RowID
}

// ErrBadRef is a reference that cannot be parsed.
var ErrBadRef = errors.New("xref: bad reference")

// Parse reads "acme/app:tsk_01", "prj_x:tsk_01" or "tsk_01". The project
// part is kept as written (a mount name, a repo slug or a project id); the
// caller resolves it among loaded sources.
func Parse(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, fmt.Errorf("%w: empty", ErrBadRef)
	}
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return Ref{RowID: s}, nil
	}
	p, id := s[:i], s[i+1:]
	if p == "" || id == "" || strings.ContainsAny(id, "/ ") {
		return Ref{}, fmt.Errorf("%w: %q (use <project>:<id>)", ErrBadRef, s)
	}
	return Ref{ProjectID: p, RowID: id}, nil
}

// ErrInward is the F15 refusal: a pointer from a wider audience to a
// narrower one.
var ErrInward = errors.New("xref: pointers flow inward only")

// Allowed applies the direction rule. samePersonal is true when a personal
// row points at the same personal folder (the only personal target a
// pointer may name).
func Allowed(from, to Kind, samePersonal bool) error {
	switch {
	case to == Client:
		return nil
	case from == Client:
		return fmt.Errorf("%w: a client-readable row may not point at a %s row (the pointer alone reveals it)", ErrInward, to)
	case to == Personal && !(from == Personal && samePersonal):
		return fmt.Errorf("%w: nothing may point at a personal row from a place others read", ErrInward)
	}
	return nil
}

// Unavailable is how a reference to a row that is not loaded here prints
// (F15: "→ acme/app:tsk_01 (not available here)", never an error).
func Unavailable(r Ref) string { return "→ " + r.String() + " (not available here)" }
