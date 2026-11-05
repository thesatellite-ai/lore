package wire

// storepush_standalone.go — cmd/lore's push of org store writes
// (LORE_TEAM_SPEC §3.2: rows written into a store are "committed and pushed
// at once"; P3-CMDLORE-7), ported from saas/cmd/cli/store_push_core.go. The
// core's git backend commits each write in the local store clone and pushes
// only on Sync, so without this wrapper a row written `--to <org>` (or an
// org roster row) reached nobody until `lore sync`. The read side is
// v0.1.17's store fetch (F3 step 1): the first read of a command syncs the
// store when its last fetch is older than live.fetch_ttl.
//
//	WrapBackend ─▶ a remote store clone (not the repo) ─▶ storePush
//	Read/Get ─▶ once per command: live.ShouldFetch? ─▶ Sync, stamp
//	Write ─▶ inner Write ─▶ autopush? ─▶ Sync (a failure is one note)

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/store"
)

// envAutopushSwitch=0 turns pushing off (v0.1.17 LORE_LIVE_AUTOPUSH).
const (
	envAutopushSwitch = "LORE_LIVE_AUTOPUSH"
	autopushOff       = "0"
)

// storePushWrap is the WrapBackend of cmd/lore for org store clones.
func storePushWrap(res *OrgResolution) func(context.Context, store.Source, string, store.Backend) store.Backend {
	return func(_ context.Context, name store.Source, _ string, b store.Backend) store.Backend {
		if name == store.SourceRepo || !b.Capabilities().Remote {
			return b
		}
		sp := &storePush{inner: b, name: name, res: res, notes: os.Stderr}
		if d, ok := b.(interface{ Dir() string }); ok {
			sp.stateDir = filepath.Dir(d.Dir()) // beside the worktree, never inside it
		}
		return sp
	}
}

// storePush is a store clone's backend that pushes after each write.
type storePush struct {
	inner store.Backend
	name  store.Source
	res   *OrgResolution
	notes io.Writer
	// stateDir holds the clone's fetch stamp ("" = fetch every command).
	stateDir string
	// fetched: the once-per-command read sync was decided.
	fetched bool
	// pushFailed: this command's push already failed and said so (FLAG4:
	// one note; later writes commit locally for `lore sync`).
	pushFailed bool
}

var (
	_ store.Backend      = (*storePush)(nil)
	_ Unwrapper          = (*storePush)(nil)
	_ store.AuditJournal = (*storePush)(nil)
)

// settings are the resolved org's live.autopush and live.fetch_ttl.
func (b *storePush) settings(ctx context.Context) (autopush bool, ttl time.Duration) {
	autopush, ttl = true, orgconf.DefaultFetchTTL
	if os.Getenv(envAutopushSwitch) == autopushOff {
		autopush = false
	}
	s, err := b.res.Situation(ctx)
	if err != nil || s == nil || s.Config == nil {
		return autopush, ttl
	}
	org := s.Result.Org
	ttl = s.Config.FetchTTL(org)
	switch {
	case !autopush:
	case org != nil && org.Live != nil && org.Live.Autopush != nil:
		autopush = *org.Live.Autopush
	case s.Config.Live.Autopush != nil:
		autopush = *s.Config.Live.Autopush
	}
	return autopush, ttl
}

// refresh syncs the store once per command before its first read when
// the fetch is due; offline or failed, the command reads the local copy.
func (b *storePush) refresh(ctx context.Context) {
	if b.fetched {
		return
	}
	b.fetched = true
	autopush, ttl := b.settings(ctx)
	in := live.FetchInputs{Now: time.Now(), TTL: ttl, Kind: live.FetchCached, Autopush: autopush, HasRemote: true}
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

// pushAfterWrite pushes the commit a write just made (autopush on); a
// failed push is a note and the commit waits for `lore sync`.
func (b *storePush) pushAfterWrite(ctx context.Context) {
	if b.pushFailed {
		return
	}
	if autopush, _ := b.settings(ctx); !autopush {
		return
	}
	rep, err := b.inner.Sync(ctx)
	switch {
	case err != nil:
		b.note("pushing failed")
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
func (b *storePush) note(why string) {
	_, _ = fmt.Fprintln(b.notes, "lore: the change is committed in the local copy of store "+string(b.name)+"; "+why+"; `lore sync` pushes it")
}

// Write implements store.Backend: commit, then push.
func (b *storePush) Write(ctx context.Context, changes []store.Change) error {
	if err := b.inner.Write(ctx, changes); err != nil {
		return err
	}
	b.pushAfterWrite(ctx)
	return nil
}

// Read implements store.Backend.
func (b *storePush) Read(ctx context.Context, cursor store.Cursor) ([]store.Row, store.Cursor, error) {
	b.refresh(ctx)
	return b.inner.Read(ctx, cursor)
}

// Get implements store.Backend.
func (b *storePush) Get(ctx context.Context, table row.Table, id ids.AnyID) (store.Row, error) {
	b.refresh(ctx)
	return b.inner.Get(ctx, table, id)
}

// Sync implements store.Backend.
func (b *storePush) Sync(ctx context.Context) (store.SyncReport, error) { return b.inner.Sync(ctx) }

// Capabilities implements store.Backend.
func (b *storePush) Capabilities() store.Capabilities { return b.inner.Capabilities() }

// Close implements store.Backend.
func (b *storePush) Close() error { return b.inner.Close() }

// Unwrap implements Unwrapper.
func (b *storePush) Unwrap() store.Backend { return b.inner }

// WriteAudited implements store.AuditJournal: rows and audit records in
// one commit, then the push; a clone without a journal gets Write.
func (b *storePush) WriteAudited(ctx context.Context, changes []store.Change, recs []store.AuditRecord) error {
	j, ok := b.inner.(store.AuditJournal)
	if !ok {
		return b.Write(ctx, changes)
	}
	if err := j.WriteAudited(ctx, changes, recs); err != nil {
		return err
	}
	b.pushAfterWrite(ctx)
	return nil
}

// PendingAudit implements store.AuditJournal.
func (b *storePush) PendingAudit(ctx context.Context) ([]store.AuditRecord, error) {
	if j, ok := b.inner.(store.AuditJournal); ok {
		return j.PendingAudit(ctx)
	}
	return nil, nil
}

// AckAudit implements store.AuditJournal.
func (b *storePush) AckAudit(ctx context.Context, recs []store.AuditRecord) error {
	if j, ok := b.inner.(store.AuditJournal); ok {
		return j.AckAudit(ctx, recs)
	}
	return nil
}
