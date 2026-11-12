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
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/storepush"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/store"
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
		b.reach = reachOnline
		if err != nil || rep.Offline {
			b.reach = reachOffline
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
// read command (readRefresh, or a read's own commit: pushAfterWrite), and
// only when the count changed since it was last said (waitSaidFile): an
// offline person is not told the same thing on every read (B3-13).
//
// Only a line said settles the command (waitSaid): a read can look at the
// clone before it makes its own commit (the pick-up of a hand edit, whose
// secret scan reads the store first), and that commit changes the count,
// so a look that said nothing must not silence the look after it.
func (b *storePush) sayWaiting(ctx context.Context) {
	if b.waitSaid {
		return
	}
	gb := gitOf(b.inner)
	if gb == nil {
		return
	}
	n, err := gb.Unpushed(ctx)
	if err != nil {
		return
	}
	said := strconv.Itoa(n) + " " + b.reach
	if n == 0 || b.stateDir == "" {
		b.forgetWaitSaid()
		if n == 0 {
			return
		}
	} else if waitAlreadySaid(readNote(b.stateDir, waitSaidFile), n, b.reach) {
		return
	}
	if b.res != nil && !b.res.waited.first(gb.Dir(), b.req) {
		return // another folder of this clone already named its commits
	}
	b.waitSaid = true
	_, _ = fmt.Fprintln(b.notes, WaitingLine(cloneStoreName(b.name), n))
	if b.stateDir != "" {
		_ = os.WriteFile(filepath.Join(b.stateDir, waitSaidFile), []byte(said+"\n"), stampFileMode) // best effort: a lost note only repeats the line once
	}
}

// Reachability a read learned from its fetch (storePush.reach).
const (
	reachUnknown = ""        // no fetch this command
	reachOnline  = "online"  // the fetch reached the remote
	reachOffline = "offline" // the fetch did not
)

// waitAlreadySaid reports whether the note last said ("<n> <reach>")
// already told the person that n commits wait: the same count, and the
// same reachability unless this command did not learn it. Coming back
// online is a change worth one more line.
func waitAlreadySaid(note string, n int, reach string) bool {
	cnt, was, _ := strings.Cut(note, " ")
	if cnt != strconv.Itoa(n) {
		return false
	}
	return reach == reachUnknown || reach == was
}

// waitSaidFile (in the clone's .git/lore) holds the unpushed count the
// last read said (sayWaiting); a push that sends removes it.
const waitSaidFile = "waiting_said"

// forgetWaitSaid removes waitSaidFile, so the next wait is said again.
func (b *storePush) forgetWaitSaid() {
	if b.stateDir != "" {
		_ = os.Remove(filepath.Join(b.stateDir, waitSaidFile)) // best effort: absent is the goal
	}
}

// readNote is a one-line note file's text ("" when absent).
func readNote(dir, name string) string {
	raw, err := os.ReadFile(filepath.Clean(filepath.Join(dir, name)))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// cloneStoreName is the name of the store a clone holds: a folder of a
// store ("agency/client") shares the clone of "agency", so its waiting
// commits are named after the store (B2-5).
func cloneStoreName(s store.Source) string {
	name, _, _ := strings.Cut(string(s), "/")
	return name
}

// waitedClones is the set of store clones (by worktree) whose waiting
// commits were already reported in one request.
type waitedClones struct {
	mu   sync.Mutex
	seen map[string]requestID
}

// first reports whether dir's waiting commits are not yet named in
// request req, and records them as named.
func (w *waitedClones) first(dir string, req requestID) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if r, ok := w.seen[dir]; ok && r == req {
		return false
	}
	if w.seen == nil {
		w.seen = map[string]requestID{}
	}
	w.seen[dir] = req
	return true
}

// WaitingLine is what a read says about commits that wait to be pushed.
func WaitingLine(storeName string, n int) string {
	return "lore: store " + storeName + ": " + strconv.Itoa(n) + " local commit(s) wait to be pushed; a read never pushes: run `lore sync` (or the next lore write here pushes them)"
}
