//go:build teame2e

package teame2e

// teame2e_storepush_test.go — store push modes and history compaction
// (owner decision 2026-11-09): `lore store set|show|compact` through the
// built binary and through `lore api`, each with two clones (two HOMEs) of
// one fake remote. The assertions are what a person sees: rows readable on
// both clones, the commit shape on the remote, exit and error codes.
//
//	personal store me   two machines of one person (laptop, desktop),
//	                    each with ~/.lore/config.yaml personal.store = the url
//	org store           alice (creates org acme, later an admin setup) and
//	                    bob (a member who imported the org)

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Remotes and clocks of the store push scenarios.
const (
	spPersonalSlug = "github.com/acme/notes"
	spOrgSlug      = "github.com/acme/team"
	spOrgStore     = "acme_notes"
	spOrg          = "acme"
	spBranch       = "main"
)

// spOld is when the "old" history is written, spNow the present: the
// default cutoff (30d) falls between them.
var (
	spOld = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	spNow = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
)

// Commit subjects the feature writes (FEATURE: batch, snapshot, compact, replay).
const (
	spBatchSubject    = "lore: batch "
	spSnapshotSubject = "lore: snapshot of the store history before "
	spCompactSubject  = "lore: compact store history (epoch "
	spReplaySubject   = "lore: replay local work onto epoch "
)

// Exit codes and error codes the commands promise.
const (
	spExitRefused      = 23
	spExitNeedsConfirm = 26
	spCodeRefused      = "E_REFUSED"
	spCodeConfirm      = "E_NEEDS_CONFIRM"
)

// spPair is one store with two clones: a and b each run lore in their own
// checkout of a code repo, with their own HOME.
type spPair struct {
	w      *th.World
	store  *th.Remote
	a, b   *th.Actor
	ca, cb *th.Clone
}

// spCodeClone gives actor a checkout of the code repo with lore initialised.
func spCodeClone(w *th.World, a *th.Actor) *th.Clone {
	w.T.Helper()
	c := a.Clone(w.Remote(slugApp), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	return c
}

// newSPPersonal: one person's personal store on two machines. start is the
// clock both machines start at (spOld to make old history).
func newSPPersonal(t *testing.T, start time.Time) *spPair {
	t.Helper()
	w := newTeamWorld(t)
	p := &spPair{w: w, store: w.Remote(spPersonalSlug)}
	p.a = w.Human("laptop", "pat@example.test")
	p.b = w.Human("desktop", "pat@example.test")
	// one person: the same git name on both machines (a git-only identity
	// shows git's user.name, so two names would be two display names)
	p.a.SetGitName("Pat")
	p.b.SetGitName("Pat")
	p.a.SetNow(start)
	p.b.SetNow(start)
	p.ca, p.cb = spCodeClone(w, p.a), spCodeClone(w, p.b)
	p.ca.MustLore("store", "init", "me", p.store.URL()).Says(t, "created store me")
	p.cb.MustLore("store", "init", "me", p.store.URL()).Says(t, "already a lore store")
	return p
}

// newSPOrg: org acme with store acme_notes; a created it (and is made an
// admin by spMakeAdmin), b imported it as a member.
func newSPOrg(t *testing.T, start time.Time) *spPair {
	t.Helper()
	w := newTeamWorld(t)
	p := &spPair{w: w, store: w.Remote(spOrgSlug)}
	p.a = w.Human("alice", "alice@example.test")
	p.b = w.Human("bob", "bob@example.test")
	p.a.SetNow(start)
	p.b.SetNow(start)
	p.ca, p.cb = spCodeClone(w, p.a), spCodeClone(w, p.b)
	p.ca.MustLore("store", "init", spOrgStore, p.store.URL())
	p.ca.MustLore("org", "create", spOrg, "--store", spOrgStore, "--key", "human:alice@example.test", "--name", "Alice", "--yes")
	p.cb.MustLore("store", "add", spOrgStore, p.store.URL())
	p.cb.MustLore("org", "import", p.store.URL(), "--key", "human:bob@example.test", "--name", "Bob", "--yes")
	return p
}

// spMakeAdmin gives a's machine an admin setup of org acme (config.yaml
// setups: kind admin), the only thing that lets it change the org store's
// settings or compact it.
func spMakeAdmin(t *testing.T, a *th.Actor) {
	t.Helper()
	path := a.LorePath("config.yaml")
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b) + "setups:\n  - name: acme-admin\n    kind: admin\n    org: " + spOrg + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// add writes one memory to store through c and returns its id.
func (p *spPair) add(t *testing.T, c *th.Clone, store, body string) string {
	t.Helper()
	return mustID(t, c.MustLore("memory", "add", body, "--to", store), "mem")
}

// sees fails unless c lists every body in `lore memory list`.
func spSees(t *testing.T, c *th.Clone, bodies ...string) {
	t.Helper()
	c.MustLore("memory", "list").Says(t, bodies...)
}

// spLog is the remote branch's subjects, newest first.
func (p *spPair) log() []string { return p.store.Log(spBranch) }

// spBody is the message body of the remote branch's tip.
func (p *spPair) tipBody(t *testing.T) string {
	t.Helper()
	return p.w.Git(p.store.Dir, "log", "-1", "--format=%b", spBranch)
}

// spSameLog fails unless the remote history did not move.
func spSameLog(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s: the remote history moved:\nbefore %q\nafter  %q", what, want, got)
	}
}

// spOneBatchOnTop fails unless after is before plus exactly one batch
// commit whose subject counts the memories it carries.
func spOneBatchOnTop(t *testing.T, before, after []string, memories int) {
	t.Helper()
	if len(after) != len(before)+1 || strings.Join(after[1:], "\n") != strings.Join(before, "\n") {
		t.Fatalf("want exactly one new commit on the remote:\nbefore %q\nafter  %q", before, after)
	}
	if !strings.HasPrefix(after[0], spBatchSubject) || !strings.Contains(after[0], "memories "+strconv.Itoa(memories)) {
		t.Fatalf("the pushed commit must be one batch counting %d memories, got %q", memories, after[0])
	}
}

// spPushView is the part of `lore store show <store> --json` the tests read.
type spPushView struct {
	Push struct {
		Store     string `json:"store"`
		Place     string `json:"place"`
		Push      string `json:"push"`
		PushEvery string `json:"push_every"`
		Epoch     int    `json:"epoch"`
		Unpushed  int    `json:"unpushed"`
		Admin     bool   `json:"admin"`
	} `json:"push"`
}

// spShow is `lore store show <store> --json`.
func spShow(t *testing.T, c *th.Clone, store string) spPushView {
	t.Helper()
	return th.DecodeJSON[spPushView](t, c.Lore("store", "show", store, "--json"))
}

// spCompactOut is `lore store compact --json` (and the REST reply).
type spCompactOut struct {
	Store     string `json:"store"`
	DryRun    bool   `json:"dry_run"`
	Nothing   bool   `json:"nothing"`
	Squashed  int    `json:"squashed"`
	Kept      int    `json:"kept"`
	Rows      int    `json:"rows"`
	FromEpoch int    `json:"from_epoch"`
	ToEpoch   int    `json:"to_epoch"`
}

// spCompacted fails unless the remote history is one parentless snapshot,
// then the kept commits, then the epoch commit.
func (p *spPair) spCompacted(t *testing.T, epoch, kept int) {
	t.Helper()
	log := p.log()
	if len(log) != kept+2 {
		t.Fatalf("want snapshot + %d kept + epoch commit, got %q", kept, log)
	}
	if want := spCompactSubject + strconv.Itoa(epoch) + ")"; log[0] != want {
		t.Fatalf("tip must be %q, got %q", want, log[0])
	}
	if !strings.HasPrefix(log[len(log)-1], spSnapshotSubject) {
		t.Fatalf("root must be the snapshot, got %q", log[len(log)-1])
	}
	root := p.w.Git(p.store.Dir, "rev-list", "--max-parents=0", spBranch)
	if strings.Count(root, "\n") != 0 {
		t.Fatalf("want one root commit, got %q", root)
	}
	if n := len(strings.Fields(p.w.Git(p.store.Dir, "rev-list", "--parents", "-n", "1", root))) - 1; n != 0 {
		t.Fatalf("the snapshot must have no parent, has %d", n)
	}
}

// ---------------------------------------------------------------- batch

// Batch mode: N writes are N local commits and ONE pushed commit, whose
// subject counts the rows and whose body names each one; the other clone
// reads every row after its sync.
func TestE40_StorePushBatchCombinesWrites(t *testing.T) {
	p := newSPPersonal(t, spNow)
	p.ca.MustLore("store", "set", "me", "push=batch", "push_every=10m").Says(t, "me: push batch every 10m")
	before := p.log()
	ids := []string{
		p.add(t, p.ca, "me", "batched fact one"),
		p.add(t, p.ca, "me", "batched fact two"),
		p.add(t, p.ca, "me", "batched fact three"),
	}
	spSameLog(t, "batch writes inside push_every", p.log(), before)
	spSees(t, p.ca, "batched fact one", "batched fact two", "batched fact three") // local writes read at once
	if v := spShow(t, p.ca, "me"); v.Push.Push != "batch" || v.Push.PushEvery != "10m" || v.Push.Unpushed < len(ids) {
		t.Fatalf("show: want batch every 10m with >= %d waiting, got %+v", len(ids), v.Push)
	}
	p.ca.MustLore("sync").Says(t, "store me: sent")
	spOneBatchOnTop(t, before, p.log(), len(ids))
	body := p.tipBody(t)
	for _, id := range ids {
		if !strings.Contains(body, "memories "+id) {
			t.Fatalf("batch body must name %s:\n%s", id, body)
		}
	}
	if v := spShow(t, p.ca, "me"); v.Push.Unpushed != 0 {
		t.Fatalf("nothing waits after sync, got %d", v.Push.Unpushed)
	}
	p.cb.MustLore("sync")
	spSees(t, p.cb, "batched fact one", "batched fact two", "batched fact three")
	// A second sync with nothing waiting pushes nothing.
	after := p.log()
	p.ca.MustLore("sync")
	spSameLog(t, "idle sync", p.log(), after)
}

// Batch mode: once push_every has passed since the last push, the next
// write pushes every waiting write as one batch commit (no sync needed).
func TestE41_StorePushBatchPushesWhenIntervalPassed(t *testing.T) {
	p := newSPPersonal(t, spNow)
	p.ca.MustLore("store", "set", "me", "push=batch", "push_every=10m")
	p.ca.MustLore("sync") // the last push is now
	before := p.log()
	p.add(t, p.ca, "me", "interval fact one")
	p.a.Advance(5 * time.Minute)
	p.add(t, p.ca, "me", "interval fact two")
	spSameLog(t, "inside push_every", p.log(), before)
	p.a.Advance(6 * time.Minute)
	p.add(t, p.ca, "me", "interval fact three")
	spOneBatchOnTop(t, before, p.log(), 3)
	p.cb.MustLore("sync")
	spSees(t, p.cb, "interval fact one", "interval fact two", "interval fact three")
}

// Batch mode with the remote unreachable: writes succeed, sync keeps them
// (offline, nothing lost); once the remote is back one sync pushes one
// batch commit and the other clone reads every row.
func TestE42_StorePushOfflineBatchThenOnline(t *testing.T) {
	p := newSPPersonal(t, spNow)
	p.ca.MustLore("store", "set", "me", "push=batch", "push_every=1m")
	p.ca.MustLore("sync")
	before := p.log()
	away := p.store.Dir + ".away"
	if err := os.Rename(p.store.Dir, away); err != nil {
		t.Fatal(err)
	}
	p.a.Advance(2 * time.Minute) // push_every has passed: every write tries to push
	p.add(t, p.ca, "me", "offline fact one")
	p.add(t, p.ca, "me", "offline fact two")
	p.a.Advance(2 * time.Minute)
	p.add(t, p.ca, "me", "offline fact three")
	p.ca.MustLore("sync").Says(t, "offline")
	spSees(t, p.ca, "offline fact one", "offline fact two", "offline fact three")
	if v := spShow(t, p.ca, "me"); v.Push.Unpushed < 3 {
		t.Fatalf("three writes wait while offline, show says %d", v.Push.Unpushed)
	}
	if err := os.Rename(away, p.store.Dir); err != nil {
		t.Fatal(err)
	}
	spSameLog(t, "while offline", p.log(), before)
	p.ca.MustLore("sync").Says(t, "store me: sent")
	spOneBatchOnTop(t, before, p.log(), 3)
	p.cb.MustLore("sync")
	spSees(t, p.cb, "offline fact one", "offline fact two", "offline fact three")
}

// ---------------------------------------------------------------- manual

// Manual mode: writes and later reads (even after a long time) never reach
// the remote; only `lore sync` pushes, as one batch commit.
func TestE43_StorePushManualPushesOnlyOnSync(t *testing.T) {
	p := newSPPersonal(t, spNow)
	p.ca.MustLore("store", "set", "me", "push=manual").Says(t, "me: push manual")
	before := p.log()
	p.add(t, p.ca, "me", "manual fact one")
	p.add(t, p.ca, "me", "manual fact two")
	p.a.Advance(3 * time.Hour)
	p.add(t, p.ca, "me", "manual fact three")
	spSees(t, p.ca, "manual fact one", "manual fact two", "manual fact three")
	p.ca.MustLore("task", "list")
	p.ca.MustLore("sync", "status").Says(t, "stores:", "; push manual", "(`lore sync` pushes the waiting commits)")
	p.ca.Lore("doctor").Says(t, "store me: push manual")
	spSameLog(t, "manual mode before sync", p.log(), before)
	p.cb.MustLore("sync")
	p.cb.MustLore("memory", "list").Never(t, "manual fact one")
	p.ca.MustLore("sync").Says(t, "store me: sent")
	spOneBatchOnTop(t, before, p.log(), 3)
	p.cb.MustLore("sync")
	spSees(t, p.cb, "manual fact one", "manual fact two", "manual fact three")
}

// ---------------------------------------------------------------- compaction

// Compaction of the personal store: history before the cutoff becomes one
// parentless snapshot, later commits stay, an epoch commit tops it; every
// row reads back on both machines, and the other machine re-fetches the
// new history and keeps writing on it.
func TestE44_StorePushCompactPersonalKeepsEveryRow(t *testing.T) {
	p := newSPPersonal(t, spOld)
	old := []string{"old fact one", "old fact two", "old fact three"}
	for _, b := range old {
		p.add(t, p.ca, "me", b)
	}
	p.a.SetNow(spNow)
	p.b.SetNow(spNow)
	p.add(t, p.ca, "me", "new fact")
	before := p.log()
	// Asks first; a dry run changes nothing.
	r := p.ca.Lore("store", "compact", "me").Fails(t, spExitNeedsConfirm, spCodeConfirm, "--yes")
	r.Says(t, "every row is kept")
	dry := th.DecodeJSON[spCompactOut](t, p.ca.Lore("store", "compact", "me", "--dry-run", "--json"))
	if !dry.DryRun || dry.Squashed < 2 || dry.Kept != 1 || dry.FromEpoch != 0 || dry.ToEpoch != 1 {
		t.Fatalf("dry run: want squash >= 2, keep 1, epoch 0 → 1, got %+v", dry)
	}
	spSameLog(t, "refused and dry-run compactions", p.log(), before)
	p.ca.MustLore("store", "compact", "me", "--yes").Says(t, "compacted me", "epoch 0 → 1")
	p.spCompacted(t, 1, 1)
	if p.log()[1] != before[0] {
		t.Fatalf("the commit after the cutoff is kept unchanged: want %q, got %q", before[0], p.log()[1])
	}
	spSees(t, p.ca, append(old, "new fact")...)
	if v := spShow(t, p.ca, "me"); v.Push.Epoch != 1 {
		t.Fatalf("compacting machine: epoch 1, got %d", v.Push.Epoch)
	}
	// The other machine re-fetches, sees every row and writes on the new history.
	p.cb.MustLore("sync")
	if v := spShow(t, p.cb, "me"); v.Push.Epoch != 1 {
		t.Fatalf("other machine after sync: epoch 1, got %d", v.Push.Epoch)
	}
	spSees(t, p.cb, append(old, "new fact")...)
	p.add(t, p.cb, "me", "after compaction")
	log := p.log()
	if len(log) != 4 || !strings.HasPrefix(log[1], spCompactSubject) {
		t.Fatalf("a write after compaction lands on the new history, got %q", log)
	}
	p.ca.MustLore("sync")
	spSees(t, p.ca, append(old, "new fact", "after compaction")...)
	// Nothing older than the cutoff is left beyond the snapshot.
	p.ca.MustLore("store", "compact", "me", "--yes").Says(t, "nothing to compact")
}

// The same through REST (`lore api`): show, set, compact (428 without
// "yes", a dry run, then yes); the other machine re-fetches.
func TestE45_StorePushRESTSetShowCompact(t *testing.T) {
	p := newSPPersonal(t, spOld)
	p.add(t, p.ca, "me", "rest old one")
	p.add(t, p.ca, "me", "rest old two")
	p.a.SetNow(spNow)
	p.b.SetNow(spNow)
	p.add(t, p.ca, "me", "rest new")
	api := startAPI(t, p.ca)

	var shown spPushView
	spDecode(t, api.spDo(t, http.MethodGet, "/v1/stores/me", nil), http.StatusOK, &shown)
	if shown.Push.Store != "me" || shown.Push.Place != "personal" || shown.Push.Push != "now" {
		t.Fatalf("GET /v1/stores/me: want me, personal, push now, got %+v", shown.Push)
	}
	var set spSetOut
	spDecode(t, api.spDo(t, http.MethodPost, "/v1/stores/set", spJSON(t, spSetBody{Store: "me", Settings: []string{"push=batch", "push_every=15m"}})), http.StatusOK, &set)
	if set.Push != "batch" || set.PushEvery != "15m" || !set.Changed {
		t.Fatalf("POST /v1/stores/set: got %+v", set)
	}
	p.ca.MustLore("store", "show", "me").Says(t, "me: push batch every 15m")
	bad := api.spDo(t, http.MethodPost, "/v1/stores/set", spJSON(t, spSetBody{Store: "me", Settings: []string{"push=sometimes"}}))
	if bad.status != http.StatusBadRequest || bad.code != "E_INVALID_INPUT" {
		t.Fatalf("a bad value: want 400 E_INVALID_INPUT, got %d %s: %s", bad.status, bad.code, bad.body)
	}

	before := p.log()
	ask := api.spDo(t, http.MethodPost, "/v1/stores/compact", spJSON(t, spCompactBody{Store: "me"}))
	if ask.status != http.StatusPreconditionRequired || ask.code != spCodeConfirm {
		t.Fatalf("compact without yes: want 428 %s, got %d %s: %s", spCodeConfirm, ask.status, ask.code, ask.body)
	}
	var dry spCompactOut
	spDecode(t, api.spDo(t, http.MethodPost, "/v1/stores/compact", spJSON(t, spCompactBody{Store: "me", DryRun: true})), http.StatusOK, &dry)
	if !dry.DryRun || dry.Squashed < 2 || dry.ToEpoch != 1 {
		t.Fatalf("dry run: got %+v", dry)
	}
	spSameLog(t, "REST refused and dry-run compactions", p.log(), before)
	var done spCompactOut
	spDecode(t, api.spDo(t, http.MethodPost, "/v1/stores/compact", spJSON(t, spCompactBody{Store: "me", Yes: true})), http.StatusOK, &done)
	if done.DryRun || done.ToEpoch != 1 || done.Kept != 1 {
		t.Fatalf("compact: got %+v", done)
	}
	p.spCompacted(t, 1, 1)
	spDecode(t, api.spDo(t, http.MethodGet, "/v1/stores/me", nil), http.StatusOK, &shown)
	if shown.Push.Epoch != 1 {
		t.Fatalf("GET after compact: epoch 1, got %+v", shown.Push)
	}
	p.cb.MustLore("sync")
	spSees(t, p.cb, "rest old one", "rest old two", "rest new")
	spSees(t, p.ca, "rest old one", "rest old two", "rest new")
}

// Org compaction by an admin: a member's clone holding an unpushed row
// re-fetches the new history, replays its row on top as one commit, and
// both clones read every row.
func TestE46_StorePushOrgCompactReplaysMemberWork(t *testing.T) {
	p := newSPOrg(t, spOld)
	old := []string{"org old one", "org old two", "org old three"}
	for _, b := range old {
		p.add(t, p.ca, spOrgStore, b)
	}
	p.a.SetNow(spNow)
	p.b.SetNow(spNow)
	p.add(t, p.ca, spOrgStore, "org new")
	p.cb.MustLore("sync")
	spSees(t, p.cb, append(old, "org new")...)
	p.add(t, p.cb.With(autopushOff), spOrgStore, "bob unpushed")
	spMakeAdmin(t, p.a)
	if v := spShow(t, p.ca, spOrgStore); !v.Push.Admin || v.Push.Place != "org" {
		t.Fatalf("admin machine: show says admin in an org store, got %+v", v.Push)
	}
	p.ca.Lore("store", "compact", spOrgStore).Fails(t, spExitNeedsConfirm, spCodeConfirm)
	p.ca.MustLore("store", "compact", spOrgStore, "--dry-run").Says(t, "would squash", "epoch 0 → 1")
	p.ca.MustLore("store", "compact", spOrgStore, "--yes").Says(t, "compacted "+spOrgStore)
	p.spCompacted(t, 1, 1)
	compacted := p.log()

	p.cb.MustLore("sync")
	log := p.log()
	if len(log) != len(compacted)+1 || !strings.HasPrefix(log[0], spReplaySubject+"1") || log[1] != compacted[0] {
		t.Fatalf("the member's unpushed row lands as one replay commit on the new history:\ncompacted %q\nnow       %q", compacted, log)
	}
	if v := spShow(t, p.cb, spOrgStore); v.Push.Epoch != 1 || v.Push.Unpushed != 0 || v.Push.Admin {
		t.Fatalf("member after sync: epoch 1, nothing waiting, not admin; got %+v", v.Push)
	}
	all := append(old, "org new", "bob unpushed")
	spSees(t, p.cb, all...)
	p.ca.MustLore("sync")
	spSees(t, p.ca, all...)
	// The admin's settings reach the member through the store.
	p.ca.MustLore("store", "set", spOrgStore, "push=batch", "push_every=10m").Says(t, "push batch every 10m", "which this machine is")
	p.cb.MustLore("sync")
	if v := spShow(t, p.cb, spOrgStore); v.Push.Push != "batch" || v.Push.PushEvery != "10m" {
		t.Fatalf("member sees the org's batch setting, got %+v", v.Push)
	}
}

// A machine without an admin setup of the org is refused set and compact,
// on the CLI and through REST; nothing on the remote changes.
func TestE47_StorePushNonAdminRefused(t *testing.T) {
	p := newSPOrg(t, spOld)
	p.add(t, p.ca, spOrgStore, "refusal old one")
	p.add(t, p.ca, spOrgStore, "refusal old two")
	p.a.SetNow(spNow)
	p.b.SetNow(spNow)
	p.cb.MustLore("sync")
	before := p.log()
	for _, c := range []*th.Clone{p.ca, p.cb} { // alice has no admin setup yet; bob is a member
		c.Lore("store", "set", spOrgStore, "push=batch").Fails(t, spExitRefused, spCodeRefused, "only an admin of "+spOrg)
		c.Lore("store", "compact", spOrgStore, "--yes").Fails(t, spExitRefused, spCodeRefused, "only an admin of "+spOrg)
		c.Lore("store", "compact", spOrgStore, "--dry-run").Fails(t, spExitRefused, spCodeRefused)
	}
	api := startAPI(t, p.cb)
	for _, r := range []apiReply{
		api.spDo(t, http.MethodPost, "/v1/stores/set", spJSON(t, spSetBody{Store: spOrgStore, Settings: []string{"push=manual"}})),
		api.spDo(t, http.MethodPost, "/v1/stores/compact", spJSON(t, spCompactBody{Store: spOrgStore, Yes: true})),
	} {
		if r.status != http.StatusConflict || r.code != spCodeRefused {
			t.Fatalf("member over REST: want 409 %s, got %d %s: %s", spCodeRefused, r.status, r.code, r.body)
		}
	}
	spSameLog(t, "refused set and compact", p.log(), before)
	if v := spShow(t, p.cb, spOrgStore); v.Push.Push != "now" || v.Push.Epoch != 0 {
		t.Fatalf("refusals change nothing, got %+v", v.Push)
	}
}

// The code repo is never a push-mode store: set and compact are refused
// on the CLI and through REST.
func TestE48_StorePushCodeRepoRefused(t *testing.T) {
	p := newSPPersonal(t, spNow)
	p.ca.Lore("store", "set", "repo", "push=batch").Fails(t, spExitRefused, spCodeRefused, "code repository")
	p.ca.Lore("store", "compact", "repo", "--yes").Fails(t, spExitRefused, spCodeRefused, "code repository")
	head := p.ca.Head()
	api := startAPI(t, p.ca)
	for _, r := range []apiReply{
		api.spDo(t, http.MethodPost, "/v1/stores/set", spJSON(t, spSetBody{Store: "repo", Settings: []string{"push=batch"}})),
		api.spDo(t, http.MethodPost, "/v1/stores/compact", spJSON(t, spCompactBody{Store: "repo", Yes: true})),
	} {
		if r.status != http.StatusConflict || r.code != spCodeRefused {
			t.Fatalf("repo over REST: want 409 %s, got %d %s: %s", spCodeRefused, r.status, r.code, r.body)
		}
	}
	if p.ca.Head() != head {
		t.Fatal("a refused repo command must not commit")
	}
}

// ---------------------------------------------------------------- crashes

// spCrashSetup: batch mode with three waiting writes on a.
func spCrashSetup(t *testing.T) (*spPair, []string, []string) {
	t.Helper()
	p := newSPPersonal(t, spNow)
	p.ca.MustLore("store", "set", "me", "push=batch", "push_every=10m")
	p.ca.MustLore("sync")
	before := p.log()
	bodies := []string{"crash fact one", "crash fact two", "crash fact three"}
	for _, b := range bodies {
		p.add(t, p.ca, "me", b)
	}
	return p, before, bodies
}

// spPushStep is the git push of a sync, spBatchStep the step that moves
// the branch onto the folded batch commit (update-ref -m "lore: batch").
const (
	spPushStep  = " push "
	spBatchStep = "lore: batch"
)

// spKilled runs `lore sync` with lore killed at the first git command
// holding step, before or after it runs.
func spKilled(t *testing.T, p *spPair, step, when string) {
	t.Helper()
	counter := filepath.Join(p.w.Root, "kill-counter-"+when)
	r := p.ca.With(th.EnvKillOn+"="+step, th.EnvKillWhen+"="+when, th.EnvKillCounter+"="+counter).Lore("sync")
	if r.Code == 0 {
		t.Fatalf("lore must die at %q: %s", step, r)
	}
	if _, err := os.Stat(counter); err != nil {
		t.Fatalf("the sync never reached %q: %s", step, r)
	}
}

// Killed right before the push: the batch is local only; the next sync
// pushes one commit and no row is lost.
func TestE49_StorePushCrashBeforePushLosesNothing(t *testing.T) {
	p, before, bodies := spCrashSetup(t)
	spKilled(t, p, spPushStep, th.KillBefore)
	spSameLog(t, "killed before the push", p.log(), before)
	spSees(t, p.ca, bodies...)
	p.ca.MustLore("sync").Says(t, "store me: sent")
	spOneBatchOnTop(t, before, p.log(), 3)
	p.cb.MustLore("sync")
	spSees(t, p.cb, bodies...)
}

// Killed right after the push reached the remote (before lore recorded
// it): the next sync neither loses a row nor pushes the batch twice.
func TestE50_StorePushCrashAfterPushLosesNothing(t *testing.T) {
	p, before, bodies := spCrashSetup(t)
	spKilled(t, p, spPushStep, th.KillAfter)
	spOneBatchOnTop(t, before, p.log(), 3)
	p.ca.MustLore("sync")
	spOneBatchOnTop(t, before, p.log(), 3)
	if v := spShow(t, p.ca, "me"); v.Push.Unpushed != 0 {
		t.Fatalf("nothing waits after the recovering sync, got %d", v.Push.Unpushed)
	}
	spSees(t, p.ca, bodies...)
	p.cb.MustLore("sync")
	spSees(t, p.cb, bodies...)
}

// Killed while folding the batch (the batch commit made, the branch not
// moved yet): the next sync folds and pushes again, one commit, no row lost.
func TestE52_StorePushCrashWhileFoldingLosesNothing(t *testing.T) {
	p, before, bodies := spCrashSetup(t)
	spKilled(t, p, spBatchStep, th.KillBefore)
	spSameLog(t, "killed while folding the batch", p.log(), before)
	spSees(t, p.ca, bodies...)
	p.ca.MustLore("sync").Says(t, "store me: sent")
	spOneBatchOnTop(t, before, p.log(), 3)
	p.cb.MustLore("sync")
	spSees(t, p.cb, bodies...)
}

// A merge a crashed run left in the store clone (MERGE_HEAD with the other
// machine's newer row): the next sync recovers, pushes the waiting batch,
// and both machines read every row.
func TestE51_StorePushLeftoverMergeLosesNothing(t *testing.T) {
	p, _, bodies := spCrashSetup(t)
	p.add(t, p.cb, "me", "other machine fact")         // desktop pushes at once (push now)
	_, slugPath, _ := strings.Cut(spPersonalSlug, "/") // the host is never in a clone's path
	work := p.a.LorePath("stores", filepath.FromSlash(slugPath))
	p.w.Git(work, "fetch", "-q", p.store.Dir, spBranch)
	// The merge may stop on conflicts (both machines changed their actor
	// and machine rows): exactly the debris a run killed mid-merge leaves.
	p.w.TryGit(work, "-c", "user.name=Crashed Run", "-c", "user.email=crashed@example.test", "merge", "-q", "--no-commit", "--no-ff", "FETCH_HEAD")
	if r := p.w.TryGit(work, "rev-parse", "-q", "--verify", "MERGE_HEAD"); r.Code != 0 {
		t.Fatalf("precondition: a merge in progress in %s", work)
	}
	// Without lore's mark the merge is the person's own (the clone is set
	// up for plain git): sync leaves it alone and says how to finish it.
	p.ca.Lore("sync").Refused(t, "in the middle of a git merge", "git merge --abort")
	if r := p.w.TryGit(work, "rev-parse", "-q", "--verify", "MERGE_HEAD"); r.Code != 0 {
		t.Fatal("sync aborted a merge the person had in progress")
	}
	// lore marks the merges it starts (<git dir>/lore-merging); a run killed
	// mid-merge leaves that mark beside MERGE_HEAD.
	gitDir := p.w.Git(work, "rev-parse", "--absolute-git-dir")
	if err := os.WriteFile(filepath.Join(gitDir, "lore-merging"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p.ca.MustLore("sync").Says(t, "store me: sent")
	if r := p.w.TryGit(work, "rev-parse", "-q", "--verify", "MERGE_HEAD"); r.Code == 0 {
		t.Fatal("the leftover merge must be gone after sync")
	}
	if v := spShow(t, p.ca, "me"); v.Push.Unpushed != 0 {
		t.Fatalf("nothing waits after the recovering sync, got %d", v.Push.Unpushed)
	}
	all := append(bodies, "other machine fact")
	spSees(t, p.ca, all...)
	p.cb.MustLore("sync")
	spSees(t, p.cb, all...)
}

// ---------------------------------------------------------------- REST helpers

// spSetBody is POST /v1/stores/set's body.
type spSetBody struct {
	Store    string   `json:"store"`
	Settings []string `json:"settings"`
}

// spCompactBody is POST /v1/stores/compact's body.
type spCompactBody struct {
	Store  string `json:"store"`
	Before string `json:"before,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
	Yes    bool   `json:"yes,omitempty"`
}

// spSetOut is POST /v1/stores/set's reply (the part the tests read).
type spSetOut struct {
	Push      string `json:"push"`
	PushEvery string `json:"push_every"`
	Changed   bool   `json:"changed"`
}

// spJSON encodes a typed request body.
func spJSON[T spSetBody | spCompactBody](t *testing.T, v T) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// spDo sends one request with a JSON body (nil: none) and the bearer token.
func (s *apiServer) spDo(t *testing.T, method, path string, body []byte) apiReply {
	t.Helper()
	var rd io.Reader = http.NoBody
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, s.base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env) // a success body has no error member
	return apiReply{status: resp.StatusCode, code: env.Error.Code, body: string(raw)}
}

// spDecode fails unless r has status, then decodes its body into v.
func spDecode[T spPushView | spCompactOut | spSetOut](t *testing.T, r apiReply, status int, v *T) {
	t.Helper()
	if r.status != status {
		t.Fatalf("want HTTP %d, got %d %s: %s", status, r.status, r.code, r.body)
	}
	if err := json.Unmarshal([]byte(r.body), v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}
