// store_push_core.go — rows written into a store are pushed at once
// (LORE_TEAM_SPEC §3.2: rows written into a store or personal project are
// "committed and pushed at once"; STORE_SERVER_SPEC 6.1: a git store's Write
// is the row files plus one commit for the batch, and a store clone has no
// live ref). Live mode (live_core.go) stays for the repo's own checkout.
//
// The core's git backend commits every write in the local store clone and
// pushes only on Sync, so without this wrapper a task added `--to <org>`
// reached nobody until someone ran `lore sync`. storePushBackend syncs the
// store right after each successful write, as v0.1.17's live.autopush
// pushed at the end of every command; LORE_LIVE_AUTOPUSH=0 (or the org's
// live.autopush: false) leaves the commit for `lore sync`.
//
// The read side is v0.1.17's store fetch (F3 step 1, live.ShouldFetch): the
// clone opens offline, so the first read of a command syncs the store when
// the last fetch is older than live.fetch_ttl (always for `lore sync`),
// and an unreachable store reads the local copy without failing.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/wire"

	"saas/pkg/aicoder/live"
)

// storePushBackend is a store clone's backend that pushes after each write
// (see the file comment).
type storePushBackend struct {
	inner store.Backend
	name  store.Source
	// autopush reports whether this command pushes store writes now.
	autopush func(ctx context.Context) bool
	// notes receives the one-line "committed, not pushed yet" notes
	// (os.Stderr in the CLI).
	notes io.Writer
	// fetch reports the fetch inputs of this command for the clone whose
	// state folder is stateDir (coreStoreFetch in the CLI).
	fetch func(ctx context.Context) live.FetchInputs
	// stateDir holds the clone's fetch stamp: the clone's base folder,
	// beside the worktree, never inside it (a file there would dirty the
	// worktree Sync requires clean). "" = no stamp: fetch every command.
	stateDir string
	// fetched: the once-per-command read sync has been decided.
	fetched bool
	// pushFailed: a push of this command already failed and said so.
	// FLAG4: the "not pushed yet" note is printed once; one command's
	// later writes (the actor row, then the row itself) commit locally
	// without retrying a push that just failed, and `lore sync` pushes
	// them all, as the note says.
	pushFailed bool
}

var (
	_ store.Backend  = (*storePushBackend)(nil)
	_ wire.Unwrapper = (*storePushBackend)(nil)
)

// wrapStorePush wraps b when it is a store clone that talks to a remote
// (Capabilities.Remote: a git store clone); any other backend (the repo, a
// local folder store) is returned unchanged.
func wrapStorePush(name store.Source, b store.Backend) store.Backend {
	if name == store.SourceRepo || !b.Capabilities().Remote {
		return b
	}
	sb := &storePushBackend{inner: b, name: name, autopush: coreStoreAutopush, notes: os.Stderr, fetch: coreStoreFetch}
	if d, ok := b.(interface{ Dir() string }); ok {
		sb.stateDir = filepath.Dir(d.Dir())
	}
	return sb
}

// coreStoreFetch is this command's fetch inputs for a store (the stamp is
// filled in by refresh): live.fetch_ttl and live.autopush of the repo's
// org entry, FetchAlways for `lore sync`.
func coreStoreFetch(ctx context.Context) live.FetchInputs {
	in := live.FetchInputs{Now: liveClock().Now(), Kind: liveCmdState.fetchKind(), HasRemote: true, Autopush: coreStoreAutopush(ctx)}
	s, err := coreSituationOnce(ctx)
	if err == nil && s != nil && s.Config != nil {
		in.TTL = s.Config.LiveFor(s.Result.Org).FetchTTL
	}
	return in
}

// refresh syncs the store once per command before its first read, when
// live.ShouldFetch says the fetch is due. A failed or offline sync is not
// an error: the command reads the local copy, as v0.1.17 did offline.
func (b *storePushBackend) refresh(ctx context.Context) {
	if b.fetched {
		return
	}
	b.fetched = true
	in := b.fetch(ctx)
	if b.stateDir != "" {
		in.LastFetch = live.FetchStamp(b.stateDir)
	}
	if !live.ShouldFetch(in) {
		return
	}
	rep, err := b.inner.Sync(ctx)
	if err != nil || rep.Offline || b.stateDir == "" {
		return
	}
	_ = live.WriteFetchStamp(b.stateDir, in.Now) // a lost stamp only means the next command fetches again
}

// coreStoreAutopush is the store autopush setting of this command:
// LORE_LIVE_AUTOPUSH=0 turns it off, else the repo's org entry's
// live.autopush (default on), as the roster's pushes read it.
func coreStoreAutopush(ctx context.Context) bool {
	s, err := coreSituationOnce(ctx)
	if err != nil || s == nil {
		return storeAutopush(nil, nil)
	}
	return storeAutopush(s.Config, s.Result.Org)
}

// Write commits through the store clone, then pushes. The write has
// happened once the commit exists: a push that fails (offline, refused) is
// a note, never an error, and `lore sync` pushes the commit later.
func (b *storePushBackend) Write(ctx context.Context, changes []store.Change) error {
	if err := b.inner.Write(ctx, changes); err != nil {
		return err
	}
	b.pushAfterWrite(ctx)
	return nil
}

// pushAfterWrite pushes the commit a write just made, when autopush is on;
// a failed push is a note (the commit waits for `lore sync`).
func (b *storePushBackend) pushAfterWrite(ctx context.Context) {
	if b.pushFailed || !b.autopush(ctx) {
		return
	}
	rep, err := b.inner.Sync(ctx)
	switch {
	case err != nil:
		b.note("pushing failed (" + redactIfQuiet(err.Error()) + ")")
	case rep.Offline:
		b.note("the store is unreachable")
	case len(rep.Refused) > 0:
		b.note("the store refused " + strconv.Itoa(len(rep.Refused)) + " row(s)")
	default:
		return
	}
	b.pushFailed = true
}

// note says the write waits in the local store clone.
func (b *storePushBackend) note(why string) {
	_, _ = fmt.Fprintln(b.notes, "lore: the change is committed in the local copy of store "+string(b.name)+"; "+why+"; `lore sync` pushes it")
}

// Read implements store.Backend.
func (b *storePushBackend) Read(ctx context.Context, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	b.refresh(ctx)
	return b.inner.Read(ctx, cursor)
}

// Get implements store.Backend.
func (b *storePushBackend) Get(ctx context.Context, table row.Table, id ids.AnyID) (store.Row, error) {
	b.refresh(ctx)
	return b.inner.Get(ctx, table, id)
}

// Sync implements store.Backend.
func (b *storePushBackend) Sync(ctx context.Context) (store.SyncReport, error) {
	return b.inner.Sync(ctx)
}

// Capabilities implements store.Backend.
func (b *storePushBackend) Capabilities() store.Capabilities { return b.inner.Capabilities() }

// Close implements store.Backend.
func (b *storePushBackend) Close() error { return b.inner.Close() }

// Unwrap implements wire.Unwrapper.
func (b *storePushBackend) Unwrap() store.Backend { return b.inner }
