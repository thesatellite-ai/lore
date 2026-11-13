package wire

// livecursor.go — the repo store's cache cursor when live rows are on
// (wire/host liveBackend): "lv1:<live ref commit>|<the store's cursor>".
// A run that reads the repo store without the live overlay (a commit
// hook's leak check, a run with live rows off) must keep that live part:
// answering with the bare store cursor made the next live run see "the
// live ref moved" and re-read and re-apply every row of the store to the
// cache, and the hook the same the other way round (B1-23: ~5 s per
// command after each commit in a 5,000-row repo).

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/store"
)

// The live cursor's prefix and separator (wire/host liveBackend issues it).
const (
	LiveCursorPrefix = "lv1:"
	LiveCursorSep    = "|"
)

// ReadKeepingLive is b.Read for a reader without the live overlay: a live
// cursor's store part is read from and its live part handed back as it
// was (this reader saw nothing of the live ref, so the cache's live rows
// are as current as before). Any other cursor is b.Read's own.
func ReadKeepingLive(ctx context.Context, b store.Backend, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	rest, ok := strings.CutPrefix(string(cursor), LiveCursorPrefix)
	if !ok {
		return b.Read(ctx, cursor)
	}
	seen, inner, ok := strings.Cut(rest, LiveCursorSep)
	if !ok {
		return b.Read(ctx, cursor)
	}
	rows, next, err := b.Read(ctx, store.Cursor(inner))
	if err != nil {
		return nil, "", err
	}
	return rows, store.Cursor(LiveCursorPrefix + seen + LiveCursorSep + string(next)), nil
}

// liveCursorKeeper is the repo store's backend in an App opened without a
// backend wrapper (the commit hook's leak check builds one: no live
// overlay): its reads keep a live cursor's live part (ReadKeepingLive).
// It embeds the git backend itself, so every other method (purge, audit
// journal, freshness, its folder) is the backend's own.
type liveCursorKeeper struct{ *git.Backend }

// Read implements store.Backend.
func (k liveCursorKeeper) Read(ctx context.Context, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	return ReadKeepingLive(ctx, k.Backend, cursor)
}

// Unwrap implements Unwrapper.
func (k liveCursorKeeper) Unwrap() store.Backend { return k.Backend }
