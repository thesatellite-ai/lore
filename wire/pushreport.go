package wire

// pushreport.go — no hidden network writes (2026-11-12, plain files
// sweep 3.3): every push lore makes to a store prints one line naming the
// store, the commit and the branch; a read never pushes and says when
// local commits wait instead.
//
//	storePush sync / compaction / batch flush ─▶ git.Backend.TakePushes
//	   ─▶ "lore: pushed store <s>: commit <sha> to branch <b>"
//	read command's first read ─▶ readRefresh: fetch when due (never a
//	   push) ─▶ commits waiting in a store whose mode would push them now
//	   ─▶ "lore: store <s>: N local commit(s) wait to be pushed; …"

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/op"
)

// PushLine is the line a push prints (also matched by tests).
func PushLine(storeName string, p gitbackend.Pushed) string {
	s := StorePushLine(storeName, p.Commit, p.Branch)
	if p.Forced {
		s += " (history rewritten: the older commits are folded into one)"
	}
	return s
}

// StorePushLine is the line of one push of commit to branch of a store
// (store row writes, org link and org file writes).
func StorePushLine(storeName, commit, branch string) string {
	return "lore: pushed store " + storeName + ": commit " + gitbackend.Pushed{Commit: commit}.Short() + " to branch " + branch
}

// printPushes prints one PushLine per push to w.
func printPushes(w io.Writer, storeName string, pushes []gitbackend.Pushed) {
	for _, p := range pushes {
		_, _ = fmt.Fprintln(w, PushLine(storeName, p))
	}
}

// pushNotes is where an App's own pushes (the batch flush) are reported:
// standard error, beside every other lore note, so --json output stays
// one document.
func (a *App) pushNotes() io.Writer { return os.Stderr }

// readRun reports whether ctx belongs to a command that only reads: the
// operation's access (inside Run), or the host's read-run mark that also
// covers the store opens before the operation runs (WithNoCreate). A
// fixing or set-up run (gitbackend.GitSetUp) is never one.
func readRun(ctx context.Context) bool {
	if gitbackend.GitSetUp(ctx) {
		// doctor --fix, lore fix and the explicit setup commands
		// (host setUpRun) repair or set up: their commits push as the
		// store's mode says, though doctor's operation is a read
		return false
	}
	if access, known := engine.AccessOf(ctx); known {
		return access == op.AccessRead
	}
	return NoCreate(ctx)
}

// reportPushes prints the pushes the clone made since the last report.
func (b *storePush) reportPushes() {
	if gb := gitOf(b.inner); gb != nil {
		printPushes(b.notes, string(b.name), gb.TakePushes())
	}
}

// readRefresh is a read command's first read of the store: fetch and
// merge when the fetch is due, never a push. Commits that wait while the
// store's mode would push them (now, or a batch whose interval passed)
// are named once, with the command that sends them.
func (b *storePush) readRefresh(ctx context.Context, in live.FetchInputs, autopush bool) {
	// a fetch that is due, or commits fetched into the tracking ref but not
	// merged (see refresh): merging them into lore's own clone is no push
	if live.ShouldFetch(in) || (autopush && b.hasUnpulled(ctx)) {
		if b.held() {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, piggybackTimeout)
			defer cancel()
		}
		rep, err := b.syncInner(ctx, true)
		b.carry(rep, err)
		if len(rep.Refused) > 0 {
			// a fetch-only sync pushed nothing, so it cannot say a refused
			// push was lifted: only a new refusal is recorded
			b.observeSync(ctx, rep, err)
		}
		if err == nil && !rep.Offline && b.stateDir != "" {
			_ = live.WriteFetchStamp(b.stateDir, in.Now) // a lost stamp only means the next command fetches again
		}
	}
	// `lore sync` (FetchAlways) reads first and pushes next: telling it to
	// run `lore sync` would be noise; its push names itself (PushLine).
	if !autopush || in.Kind == live.FetchAlways {
		return
	}
	switch b.pushSettings().Push {
	case storepush.ModeManual:
		return // commits wait by the store's choice: `lore sync status` lists them
	case storepush.ModeBatch:
		if !b.batchDue(ctx) {
			return
		}
	}
	b.sayWaiting(ctx)
}

// sayWaiting names the clone's unpushed commits and `lore sync`, once per
// read command (readRefresh, or a read's own commit: pushAfterWrite).
func (b *storePush) sayWaiting(ctx context.Context) {
	if b.waitSaid {
		return
	}
	gb := gitOf(b.inner)
	if gb == nil {
		return
	}
	if n, err := gb.Unpushed(ctx); err == nil && n > 0 {
		b.waitSaid = true
		_, _ = fmt.Fprintln(b.notes, WaitingLine(string(b.name), n))
	}
}

// WaitingLine is what a read says about commits that wait to be pushed.
func WaitingLine(storeName string, n int) string {
	return "lore: store " + storeName + ": " + strconv.Itoa(n) + " local commit(s) wait to be pushed; a read never pushes: run `lore sync` (or the next lore write here pushes them)"
}
