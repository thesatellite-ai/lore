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
//	Read/Get ─▶ once per command: live.ShouldFetch? ─▶ fetch + merge, stamp
//	          (a read command never pushes: commits that wait are named,
//	          with `lore sync`; pushreport.go)
//	Write ─▶ inner Write ─▶ autopush? ─▶ Sync (a failure is one note)
//	every push ─▶ "lore: pushed store <s>: commit <sha> to branch <b>"
//
// The store's push mode (owner decision 2026-11-09; storepushmode.go,
// logic/storepush) changes when the push happens:
//
//	now     as above
//	batch   a write commits locally; the push (all unpushed commits as ONE
//	        batch commit) runs when push_every has passed since the last
//	        push, on `lore sync`, and on a command that needs the remote
//	        current (FetchAlways, the agent's session start); a read fetch
//	        merges without pushing
//	manual  a write commits locally; only an explicit Sync pushes (`lore
//	        sync`, and the roster/coordination steps that push on purpose)
//
// After a push that landed, compact=on compacts the store at most once a
// day (personal store, an org store from an admin's machine, or a
// stores.yaml store of no org).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/fsreason"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/gitfault"
	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	opsmigrate "github.com/thesatellite-ai/lore/ops/migrate"
	"github.com/thesatellite-ai/lore/store"
)

// writeFetchKey is the context key of WithWriteFetch.
type writeFetchKey struct{}

// WithWriteFetch marks ctx as a writing command's (the host's live hook):
// its store clones fetch when the last fetch is older than
// orgconf.WriteFetchTTL, not the whole live.fetch_ttl a read reuses (owner
// item M-4: reads went to the network on every command; a write still
// needs the remote current, e.g. a claim of a task a teammate just added).
func WithWriteFetch(ctx context.Context) context.Context {
	return context.WithValue(ctx, writeFetchKey{}, true)
}

// writeFetch reports whether ctx carries WithWriteFetch.
func writeFetch(ctx context.Context) bool {
	w, _ := ctx.Value(writeFetchKey{}).(bool)
	return w
}

// envAutopushSwitch=0 turns pushing off (v0.1.17 LORE_LIVE_AUTOPUSH).
const (
	envAutopushSwitch = "LORE_LIVE_AUTOPUSH"
	autopushOff       = "0"
)

// storePushWrap is the WrapBackend of cmd/lore for org store clones.
func storePushWrap(res *OrgResolution) func(context.Context, store.Source, string, store.Backend) store.Backend {
	return func(_ context.Context, name store.Source, folder string, b store.Backend) store.Backend {
		if name == store.SourceRepo || !b.Capabilities().Remote {
			return b
		}
		sp := &storePush{inner: b, name: name, res: res, notes: os.Stderr}
		if d, ok := b.(interface{ Dir() string }); ok {
			sp.stateDir = gitbackend.StateDirOf(d.Dir()) // the clone's .git/lore, never in its files
			if res != nil {
				sp.own = storeOwnName(res.home, gitbackend.OriginAt(d.Dir()), folder) // B-9 (storeshown.go)
			}
		}
		return sp
	}
}

// storePush is a store clone's backend that pushes after each write.
type storePush struct {
	inner store.Backend
	name  store.Source
	// own is the store's own name ("client-store/bigco_hub"), which push
	// lines print beside name when the repo calls it otherwise (B-9); ""
	// for a store of no configured org.
	own   string
	res   *OrgResolution
	notes io.Writer
	// stateDir holds the clone's fetch stamp ("" = fetch every command).
	stateDir string
	// req is the REST request the per-command state below belongs to (0 in
	// a CLI process). `lore api` builds this backend once and serves every
	// request with it; without the reset (begin) its first read decided the
	// fetch for the life of the process, so a store clone never followed
	// its remote again however old the fetch stamp grew, and one failed
	// push stopped every later push.
	req requestID
	// fetched: the once-per-command read sync was decided.
	fetched bool
	// earlier is what this command's read fetch and push-after-write syncs
	// did, added to the next Sync's report (lore sync's store lines).
	earlier store.SyncReport
	// pushFailed: this command's push already failed and said so (FLAG4:
	// one note; later writes commit locally for `lore sync`).
	pushFailed bool
	// push is the store's push settings, resolved once per command.
	push *storepush.Settings
	// compacted: this command already decided the automatic compaction.
	compacted bool
	// waitSaid: this read command already said its commits wait for
	// `lore sync` (one line per store and command).
	waitSaid bool
	// reach is what this command's read fetch learned of the remote
	// (reachOnline, reachOffline; reachUnknown without one): sayWaiting
	// says the waiting commits again when it changed.
	reach string
}

var (
	_ store.Backend      = (*storePush)(nil)
	_ Unwrapper          = (*storePush)(nil)
	_ store.AuditJournal = (*storePush)(nil)
	_ engine.Purger      = (*storePush)(nil)
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

// pushSettings is the store's push settings, resolved once per command:
// the personal: block for `me`, the store's lore-store.yaml for any other
// store. A bad value pushes as mode now (`lore store show` and `lore
// doctor` name it).
func (b *storePush) pushSettings() storepush.Settings {
	if b.push != nil {
		return *b.push
	}
	var raw storepush.Raw
	switch gb := gitOf(b.inner); {
	case b.name == store.Source(attach.ReservedMe):
		if b.res != nil {
			raw, _ = personalPushRaw(b.res.home)
		}
	case gb != nil:
		raw = gb.Marker().Push
	}
	s, err := storepush.FromRaw(raw)
	if err != nil {
		s = storepush.Default()
	}
	b.push = &s
	return s
}

// held reports whether writes wait for a later push (batch, manual).
func (b *storePush) held() bool { return b.pushSettings().Push != storepush.ModeNow }

// batchDue reports whether batch mode pushes now: the oldest waiting write
// is push_every old (storepush.BatchDue). No daemon or timer exists: the
// command that runs anyway pushes.
func (b *storePush) batchDue(ctx context.Context) bool {
	s := b.pushSettings()
	gb := gitOf(b.inner)
	if s.Push != storepush.ModeBatch || gb == nil {
		return false
	}
	now := b.res.now()
	if failed := readStamp(b.stateDir, pushFailStampFile); !failed.IsZero() && !failed.After(now) && now.Sub(failed) < s.PushEvery {
		return false // the last batch push failed less than push_every ago
	}
	oldest, err := gb.OldestUnpushed(ctx)
	return err == nil && storepush.BatchDue(oldest, now, s.PushEvery)
}

// piggybackTimeout bounds a batch push made inside another command, so a
// slow or hanging remote never holds the person's command up; what did
// not go out waits, committed, for the next command or `lore sync`.
const piggybackTimeout = 15 * time.Second

// begin starts the per-command state anew when ctx is another REST
// request than the one it belongs to (a CLI command is always request 0):
// the read fetch, the carried sync counts, the failed push, the resolved
// push settings (a lore-store.yaml edited while `lore api` runs applies to
// the next request) and the compaction decision.
func (b *storePush) begin(ctx context.Context) {
	if r := requestOf(ctx); r != b.req {
		b.req, b.fetched, b.earlier, b.pushFailed = r, false, store.SyncReport{}, false
		b.push, b.compacted, b.waitSaid, b.reach = nil, false, false, reachUnknown
	}
}

// refresh syncs the store once per command before its first read when
// the fetch is due; offline or failed, the command reads the local copy.
// A store whose writes are held fetches without pushing, unless a batch
// is due or the command needs the remote current (batch mode).
func (b *storePush) refresh(ctx context.Context) {
	if fetchDeferred(ctx) {
		return // open before the pick-up of hand edits (handedit.go): no merge yet
	}
	b.begin(ctx)
	if b.fetched {
		return
	}
	b.fetched = true
	autopush, ttl := b.settings(ctx)
	in := live.FetchInputs{Now: b.res.now(), TTL: ttl, Kind: live.FetchCached, Autopush: autopush, HasRemote: true}
	if b.res != nil && b.res.fetchAlways {
		in.Kind = live.FetchAlways
	}
	if writeFetch(ctx) && !readRun(ctx) {
		in.TTL = orgconf.ForWrite(in.TTL) // M-4: only reads reuse a fetch for the whole TTL
	}
	if b.stateDir != "" {
		in.LastFetch = live.FetchStamp(b.stateDir)
	}
	held := b.held()
	// No hidden magic (2026-11-12): a read never pushes. It fetches when
	// the fetch is due and says when local commits wait (readRefresh); the
	// push is a write's, `lore sync`'s or a hook's. A writing command (or
	// one whose access is unknown here) pushes as its store's mode says.
	if readRun(ctx) || opsmigrate.NoPushAsked(ctx) {
		b.readRefresh(ctx, in, autopush) // a --no-push migration fetches like a read
		return
	}
	push := !held || (autopush && b.pushSettings().Push == storepush.ModeBatch && in.Kind == live.FetchAlways)
	// A push that failed earlier (offline) leaves its commits waiting: the
	// next command tries again whatever the fetch stamp says. The stamp is
	// the clone's one "last fetched" (.git/lore/fetch-stamp, read by the
	// org link index's max-age rule too), so a failed push never clears it.
	//
	// A fresh stamp says the remote was fetched lately, not that the branch
	// holds what was fetched: a fetch that only moved the tracking ref (a
	// plain `git fetch` in the clone, the org link index fetching the same
	// clone and stamping it) leaves teammates' rows unmerged, so such a
	// clone syncs whatever the stamp says.
	//
	// With autopush off (live.autopush, LORE_LIVE_AUTOPUSH=0) lore neither
	// fetches nor pushes by itself (live.ShouldFetch), so neither does
	// either retry: the commits wait for `lore sync`.
	if !live.ShouldFetch(in) && !(autopush && b.hasUnpulled(ctx)) && !(autopush && push && !held && b.hasUnpushed(ctx)) {
		if held && push && b.hasUnpushed(ctx) {
			b.pushNow(ctx) // a batch whose interval passed
		}
		return
	}
	if push && held {
		b.pushNow(ctx)
		if !b.pushFailed && b.stateDir != "" {
			_ = live.WriteFetchStamp(b.stateDir, in.Now)
		}
		return
	}
	if held {
		// a held store's fetch runs inside the person's command: bounded
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, piggybackTimeout)
		defer cancel()
	}
	waiting := push && b.hasUnpushed(ctx)
	rep, err := b.syncInner(ctx, !push)
	b.reportPushes()
	b.carry(rep, err)
	if push || len(rep.Refused) > 0 {
		b.observeSync(ctx, rep, err) // a fetch-only sync cannot clear a refusal
	}
	if err != nil || rep.Offline || b.stateDir == "" {
		return
	}
	_ = live.WriteFetchStamp(b.stateDir, in.Now) // a lost stamp only means the next command fetches again
	if waiting && len(rep.Refused) == 0 {
		writeStamp(b.stateDir, pushStampFile, in.Now) // only a push that sent commits: a fetch is no push
	}
}

// hasUnpushed reports whether the clone holds commits its remote does not.
func (b *storePush) hasUnpushed(ctx context.Context) bool {
	gb := gitOf(b.inner)
	if gb == nil {
		return false
	}
	n, err := gb.Unpushed(ctx)
	return err == nil && n > 0
}

// hasUnpulled reports whether the clone's tracking ref holds commits its
// branch has not merged.
func (b *storePush) hasUnpulled(ctx context.Context) bool {
	gb := gitOf(b.inner)
	if gb == nil {
		return false
	}
	n, err := gb.Unpulled(ctx)
	return err == nil && n > 0
}

// syncInner syncs the clone: noPush fetches and merges only; a held
// store's push folds its unpushed commits into one batch commit.
func (b *storePush) syncInner(ctx context.Context, noPush bool) (store.SyncReport, error) {
	gb := gitOf(b.inner)
	if gb == nil || (!noPush && !b.held()) {
		if noPush {
			return store.SyncReport{}, nil // a store lore cannot sync without pushing is left alone
		}
		return b.inner.Sync(ctx)
	}
	return gb.SyncWith(ctx, gitbackend.SyncOptions{NoPush: noPush, Squash: !noPush})
}

// pushNow pushes what the clone holds (a batch commit for a held store);
// a failed push is one note, and the commits wait for `lore sync`.
func (b *storePush) pushNow(ctx context.Context) {
	if b.held() && (b.res == nil || !b.res.fetchAlways) {
		// a piggybacked batch push (not `lore sync`): bounded
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, piggybackTimeout)
		defer cancel()
	}
	waiting := b.hasUnpushed(ctx)
	noPush := opsmigrate.NoPushAsked(ctx) // a --no-push migration's sync merges only
	if noPush {
		waiting = false
	}
	rep, err := b.syncInner(ctx, noPush)
	b.reportPushes()
	b.carry(rep, err)
	b.observeSync(ctx, rep, err)
	switch {
	case errors.Is(err, gitbackend.ErrRemoteReset):
		// B3-29: the remote was deleted and made again; the commit waits
		// here until the person picks a way out (the refusal names both).
		msg, hint := err.Error(), ""
		var le *errs.Error
		if errors.As(err, &le) {
			msg, hint = le.Message, le.Hint
		}
		_, _ = fmt.Fprintln(b.notes, "lore: the change is committed in the local copy of store "+string(b.name)+" and not pushed: "+msg)
		if hint != "" {
			_, _ = fmt.Fprintln(b.notes, "  hint: "+hint)
		}
	case err != nil:
		b.note("pushing failed")
	case rep.Offline:
		b.note(b.offlineWhy(rep))
	case len(rep.Refused) > 0:
		if own, _ := splitHeld(rep.Refused); len(own) == 0 {
			b.note("the push waits on a refused row in another folder of the store (`lore sync status` shows which)")
			break
		}
		b.note("the store refused the push (`lore sync status` shows the rows and why)")
	default:
		b.pushed(ctx, waiting)
		return
	}
	b.pushFailed = true
	if b.held() {
		// A batch push that failed is retried after push_every (batchDue
		// reads this stamp), not on every read: an offline person never
		// waits on the remote command after command.
		writeStamp(b.stateDir, pushFailStampFile, b.res.now())
		return
	}
	// The waiting commits make the next command push again (refresh):
	// the fetch stamp stays, it records the last fetch that worked.
}

// pushed records a push that landed and runs the daily compaction. A
// sync that had no local commit to send (sent false) pushed nothing: it
// leaves push_stamp (the store's "last push") and the compaction alone, so
// a read or a --dry-run never changes a file beside the clone but the
// fetch stamp.
func (b *storePush) pushed(ctx context.Context, sent bool) {
	if !sent {
		return
	}
	b.forgetWaitSaid()
	writeStamp(b.stateDir, pushStampFile, b.res.now())
	b.autoCompact(ctx)
}

// offlineWhy is the store-push note's reason for a push that did not
// reach the remote, read from git's own error text the way the read path
// does (gitfault): no answer stays "the store is unreachable"; a missing
// repository, an unreadable folder or refused credentials say so and name
// the store's url (credentials masked), since waiting fixes none of them.
func (b *storePush) offlineWhy(rep store.SyncReport) string {
	kind := gitfault.ClassifyAt(rep.OfflineDetail, fsreason.GitPathState)
	if rep.OfflineDetail == "" || kind == gitfault.Unreachable {
		return "the store is unreachable"
	}
	shown := string(b.name)
	if gb := gitOf(b.inner); gb != nil && gb.RemoteURL() != "" {
		shown = repourl.Mask(gb.RemoteURL())
	}
	return gitfault.Describe(rep.OfflineDetail, shown, fsreason.GitPathState) + "; " + gitbackend.ProbeHint(kind, shown)
}

// pushAfterWrite pushes the commit a write just made (autopush on, mode
// now; batch only when push_every has passed); a failed push is a note
// and the commit waits for `lore sync`.
func (b *storePush) pushAfterWrite(ctx context.Context) {
	if fetchDeferred(ctx) && !readRun(ctx) {
		return // the sync after the pick-up pushes it (handedit.go)
	}
	// A read's pick-up commit goes on: nothing pushes it (a read never
	// pushes), and the store's first read of the command already ran before
	// the commit, so only this call can say that it waits (readRun below).
	if opsmigrate.NoPushAsked(ctx) {
		return // `lore migrate run --no-push`: the commit stays local
	}
	b.begin(ctx)
	if b.pushFailed {
		return
	}
	if autopush, _ := b.settings(ctx); !autopush {
		return
	}
	switch b.pushSettings().Push {
	case storepush.ModeManual:
		return
	case storepush.ModeBatch:
		if !b.batchDue(ctx) {
			return
		}
	}
	if readRun(ctx) {
		// A read never pushes (D-aa, docs bug B3): the commit a read makes
		// (the pick-up of a hand edit) waits for the next write or `lore
		// sync`, and the read says so once.
		b.sayWaiting(ctx)
		return
	}
	b.pushNow(ctx)
}

// pendingPush is what happens to a commit whose push failed: the next
// write to the store that reaches its remote pushes it (refresh retries
// while commits wait), `lore sync` pushes it at once; a read never pushes,
// it only says the commit waits (readRefresh).
const pendingPush = "run `lore sync` when the store is reachable (or the next lore write to it pushes it; a read never pushes)"

// note says the write waits in the local store clone and when it is
// pushed.
func (b *storePush) note(why string) {
	_, _ = fmt.Fprintln(b.notes, "lore: the change is committed in the local copy of store "+string(b.name)+"; "+why+"; "+pendingPush)
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

// Sync implements store.Backend. The report also counts what this
// command's earlier syncs (the read fetch, the push after a write) sent,
// received and merged, so `lore sync` reports the rows it delivered even
// when its first read already pushed them.
func (b *storePush) Sync(ctx context.Context) (store.SyncReport, error) {
	b.begin(ctx)
	waiting := b.hasUnpushed(ctx)
	noPush := opsmigrate.NoPushAsked(ctx) // a --no-push migration's sync merges only
	if noPush {
		waiting = false
	}
	rep, err := b.syncInner(ctx, noPush)
	b.reportPushes()
	b.observeSync(ctx, rep, err)
	if err != nil {
		return rep, err
	}
	if !rep.Offline && len(rep.Refused) == 0 {
		b.pushed(ctx, waiting)
	}
	rep.Sent += b.earlier.Sent
	rep.Received += b.earlier.Received
	rep.Merged = append(b.earlier.Merged, rep.Merged...)
	rep.Others = addOthers(rep.Others, b.earlier.Others)
	b.earlier = store.SyncReport{}
	return rep, nil
}

// addOthers adds the per-folder counts of from to into (made when nil):
// the rows a read's fetch merged for the store's other folders must reach
// `lore sync`'s report as the folder's own counts do (W-5 split them off;
// a store synced whole outside a repo said "up to date" after pulling).
func addOthers(into, from map[string]store.FolderCount) map[string]store.FolderCount {
	for f, c := range from {
		if into == nil {
			into = map[string]store.FolderCount{}
		}
		o := into[f]
		into[f] = store.FolderCount{Sent: o.Sent + c.Sent, Received: o.Received + c.Received}
	}
	return into
}

// carry adds a sync's counts to earlier (reported by the next Sync).
func (b *storePush) carry(rep store.SyncReport, err error) {
	if err != nil {
		return
	}
	b.earlier.Sent += rep.Sent
	b.earlier.Received += rep.Received
	b.earlier.Merged = append(b.earlier.Merged, rep.Merged...)
	b.earlier.Others = addOthers(b.earlier.Others, rep.Others)
}

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

// WritePurge implements engine.Purger: the clone records the purged ids in
// the same commit (its _purged.json), then pushes. Without it a purge in
// any store clone with a remote was refused E_UNSUPPORTED although the git
// backend under the wrapper keeps the list.
func (b *storePush) WritePurge(ctx context.Context, changes []store.Change, purged []ids.AnyID) error {
	p, ok := b.inner.(engine.Purger)
	if !ok {
		return errs.New(errs.Unsupported, "store "+string(b.name)+" cannot record purged ids")
	}
	if err := p.WritePurge(ctx, changes, purged); err != nil {
		return err
	}
	b.pushAfterWrite(ctx)
	return nil
}

// Purged implements engine.Purger: the clone's purged ids, fetched first
// like every read.
func (b *storePush) Purged(ctx context.Context) (map[string]bool, error) {
	p, ok := b.inner.(engine.Purger)
	if !ok {
		return nil, errs.New(errs.Unsupported, "store "+string(b.name)+" cannot record purged ids")
	}
	b.refresh(ctx)
	return p.Purged(ctx)
}

// WritePurgeAudited implements store.PurgeAuditJournal: WritePurge with
// the audit records in the same commit, then the push.
func (b *storePush) WritePurgeAudited(ctx context.Context, changes []store.Change, purged []ids.AnyID, recs []store.AuditRecord) error {
	j, ok := b.inner.(store.PurgeAuditJournal)
	if !ok {
		return b.WritePurge(ctx, changes, purged)
	}
	if err := j.WritePurgeAudited(ctx, changes, purged, recs); err != nil {
		return err
	}
	b.pushAfterWrite(ctx)
	return nil
}
