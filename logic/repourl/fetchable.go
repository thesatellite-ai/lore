package repourl

import (
	"errors"
	"strings"
)

// ErrNotFetchable is returned by CheckFetchable for a store url git cannot
// fetch as written: the bare host/path form (`github.com/agency/notes`).
// That form is lore's normalised id of a remote (Normalize), never an
// address: git reads it as a local path, so a store configured that way
// made a clone whose every fetch failed and the store read as offline
// (decision D-DOCS-1; STORE_SERVER_SPEC 7.2 lists the accepted schemes).
var ErrNotFetchable = errors.New("repourl: not a url git can fetch; use git@host:owner/repo or https://host/owner/repo")

// CheckFetchable reports whether raw is a git store address lore can hand
// to git: a url with a scheme (https://, http://, ssh://, file://, ...) or
// git's scp form (git@host:owner/repo). It checks only the form; Normalize
// (or the store opener) still checks the rest. A blank value is ErrEmpty.
func CheckFetchable(raw string) error {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return ErrEmpty
	case strings.Contains(s, schemeSep), isSCPLike(s):
		return nil
	default:
		return ErrNotFetchable
	}
}
