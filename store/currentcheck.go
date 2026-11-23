package store

import "context"

// CurrentCheck reports whether the backend holds r's version of its row
// right now (same table, id and hlc), read again at the moment it is
// asked.
//
// Why (devco D-920, hunt H-178): a cache keeps the newer of two copies of
// a row, so a refresh that read a row before a concurrent write applied a
// newer one never puts the older copy back. But a row file can also go
// back to an older version for good: a branch switch, `git checkout` or
// `git reset` of a row file, a pull of a version written on a machine
// whose clock was behind, a restore from a backup. The cache then kept
// the newer copy it held, the cursor moved past the file, and search and
// ask showed a text no row file had while `lore show --id` showed the
// file. A refresh hands the cache this check (WithCurrentCheck): an older
// row it read replaces the held copy only when the backend still holds
// exactly that row, so the race stays covered and the cache follows the
// files.
type CurrentCheck func(ctx context.Context, r Row) bool

type currentCheckKey struct{}

// WithCurrentCheck returns ctx carrying check for the cache's Apply.
func WithCurrentCheck(ctx context.Context, check CurrentCheck) context.Context {
	return context.WithValue(ctx, currentCheckKey{}, check)
}

// CurrentCheckOf is ctx's CurrentCheck; nil when none was set (a write
// applying the rows it just stored: they are the newest by construction).
func CurrentCheckOf(ctx context.Context) CurrentCheck {
	c, _ := ctx.Value(currentCheckKey{}).(CurrentCheck)
	return c
}
