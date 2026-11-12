package store

import (
	"context"
	"slices"
)

// Per-write commit trailers (PROVENANCE 5.4): a write path that knows more
// about one write than the backend does (a move knows where the row went)
// attaches "Key: value" trailer lines to the context; a backend that makes
// commits appends them to the commit of that write, after its own fixed
// trailers. A backend without commits ignores them: carrying trailers is
// optional, never part of the Backend contract.
//
//	engine.MoveWith ─ WithCommitTrailers(ctx, "Lore-Moved-To: rep_…")
//	  └─ Backend.Write(ctx, …) ─ git backend: CommitTrailers(ctx) → commit -m

// commitTrailersKey is the context key of the per-write trailers.
type commitTrailersKey struct{}

// WithCommitTrailers returns ctx carrying lines after any trailers it
// already carries. Each line is "Key: value"; a backend drops a line that
// is empty or spans lines.
func WithCommitTrailers(ctx context.Context, lines ...string) context.Context {
	if len(lines) == 0 {
		return ctx
	}
	all := slices.Concat(CommitTrailers(ctx), lines)
	return context.WithValue(ctx, commitTrailersKey{}, all)
}

// CommitTrailers is the per-write trailer lines ctx carries (nil if none);
// the slice is a copy the caller may keep.
func CommitTrailers(ctx context.Context) []string {
	v, ok := ctx.Value(commitTrailersKey{}).([]string)
	if !ok {
		return nil
	}
	return slices.Clone(v)
}

// commitTitleKey is the context key of a bulk write's commit title.
type commitTitleKey struct{}

// WithCommitTitle returns ctx carrying the title of the commit a bulk
// write makes (one store commit for every row of `lore actor merge`,
// `lore doctor --fix`, …: "actor merge act_… into act_…"). A backend that
// commits uses it as the subject instead of naming the first row; a line
// break or an empty title is ignored. A backend without commits ignores it.
func WithCommitTitle(ctx context.Context, title string) context.Context {
	return context.WithValue(ctx, commitTitleKey{}, title)
}

// CommitTitle is the bulk write title ctx carries ("" if none).
func CommitTitle(ctx context.Context) string {
	t, _ := ctx.Value(commitTitleKey{}).(string)
	return t
}
