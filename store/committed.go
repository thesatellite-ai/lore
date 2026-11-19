package store

// committed.go — what each commit of a write really holds (devco D-691).
// A write's "lore: wrote …" lines came from the rows the engine wrote, so a
// row rewritten with the same bytes (no change, an empty part of the
// commit) was counted, and a file the commit added beside the rows (a
// folder's _meta.json on its first write) was never named: "wrote 4 row
// file(s)" over a commit of 1 file. A backend that commits calls
// NoteCommitted with the files its commit holds; the host's write log
// (wire WithWriteLog) reports exactly those.
//
//	host ─▶ WithCommitted(log) ─▶ op ─▶ git backend commit
//	   ─▶ NoteCommitted(clone top, files of the commit) ─▶ log

import "context"

// CommittedFunc receives one commit's files: top is the checkout the
// commit was made in, paths are slash paths relative to it.
type CommittedFunc func(top string, paths []string)

// committedKey carries a CommittedFunc on a context.
type committedKey struct{}

// WithCommitted returns ctx whose commits report their files to fn.
func WithCommitted(ctx context.Context, fn CommittedFunc) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, committedKey{}, fn)
}

// NoteCommitted tells ctx's CommittedFunc (none: nothing) the files one
// commit made in top holds.
func NoteCommitted(ctx context.Context, top string, paths []string) {
	if fn, ok := ctx.Value(committedKey{}).(CommittedFunc); ok {
		fn(top, paths)
	}
}

// WantsCommitted reports whether ctx has a CommittedFunc, so a backend
// skips the git call that lists a commit's files when nobody reads them.
func WantsCommitted(ctx context.Context) bool {
	_, ok := ctx.Value(committedKey{}).(CommittedFunc)
	return ok
}
