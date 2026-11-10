//go:build machines && !windows

package machines

// life_storepush_test.go — the lives of a store's push settings and of a
// history compaction, through the built binary:
//
//	settings (CLI, every shape)  me as a git store: show now → batch every
//	                             10m → manual (a write waits for sync, then
//	                             one batch commit) → back to now (a write is
//	                             pushed at once) → compact=on needs
//	                             compact_after → on with 30d → off; every
//	                             bad value, an unknown store, a folder store
//	                             and `repo` refused, nothing changed by them
//	settings (REST, lore api)    the same transitions through POST
//	                             /v1/stores/set and GET /v1/stores/me, the
//	                             CLI reading back each one on the same home
//	compaction (two machines)    an admin and a member of one team, history
//	                             written under an old clock (LORE_NOW, a
//	                             loretest build); the member holds an
//	                             unpushed row and is refused set and compact
//	                             (CLI and REST); the admin's dry run changes
//	                             nothing, a run without --yes asks, the run
//	                             squashes the old history into one parentless
//	                             snapshot and raises the epoch; the member's
//	                             sync moves onto the new history and replays
//	                             its row; both see every row; compact=on with
//	                             compact_after set by the admin over REST
//	                             reaches the member
//
// After every settings transition the setup model's checks run
// (lifeModel.checkModel: list, which setup applies, config.yaml — whose
// personal: push keys config check must know —, repo files, doctor, the
// page's reads); the compaction checks the machine invariants on both
// machines at its milestones.

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
)

// spLifeShapes run the REST settings life and the compaction (as the
// member's machine); the CLI settings life runs on every built-in shape.
var spLifeShapes = []string{ShapeFresh, ShapeOwnerLike, ShapeGraveyard}

// The clocks the compaction runs under: the old history, then today.
const (
	spOld = "2026-09-01T10:00:00Z"
	spNow = "2026-11-09T10:00:00Z"
	// spCutoff is store compact's cutoff for --before 30d at spNow.
	spCutoff = "2026-10-10"
)

func TestLifeStorePush(t *testing.T) {
	for _, shape := range AllShapes() {
		t.Run("settings/cli/"+shape, func(t *testing.T) {
			t.Parallel()
			spSettingsLife(t, shape, spCLI{})
		})
	}
	for _, shape := range spLifeShapes {
		t.Run("settings/rest/"+shape, func(t *testing.T) {
			t.Parallel()
			spSettingsLife(t, shape, spREST{})
		})
		t.Run("compaction/"+shape, func(t *testing.T) {
			t.Parallel()
			spCompactionLife(t, shape)
		})
	}
}

// spSurface changes and reads a store's push settings: the CLI, or
// lore api's REST routes.
type spSurface interface {
	name() string
	// start readies the surface on m in cwd (lore api starts there).
	start(t *testing.T, m *Machine, cwd string) spSurface
	set(t *testing.T, store string, kv ...string) spPush
	show(t *testing.T, store string) spPush
	// refused runs a set that must be refused with code and returns the
	// error's message.
	refused(t *testing.T, code, store string, kv ...string) string
}

type spCLI struct {
	m   *Machine
	cwd string
}

func (spCLI) name() string { return "cli" }

func (spCLI) start(_ *testing.T, m *Machine, cwd string) spSurface { return spCLI{m, cwd} }

func (c spCLI) set(t *testing.T, store string, kv ...string) spPush {
	t.Helper()
	return spSet(t, c.m, c.cwd, store, kv...)
}

func (c spCLI) show(t *testing.T, store string) spPush {
	t.Helper()
	return spShow(t, c.m, c.cwd, store)
}

func (c spCLI) refused(t *testing.T, code, store string, kv ...string) string {
	t.Helper()
	r := c.m.Lore(t, c.cwd, append([]string{"store", "set", store, "--json"}, kv...)...)
	if e := csRefused(t, r, code); e != nil {
		return e.Message
	}
	return ""
}

type spREST struct {
	api *csAPI
}

func (spREST) name() string { return "rest" }

func (spREST) start(t *testing.T, m *Machine, cwd string) spSurface {
	return spREST{csStartAPI(t, m, cwd)}
}

// spSetBody is POST /v1/stores/set's body.
type spSetBody struct {
	Store    string   `json:"store"`
	Settings []string `json:"settings"`
}

func spJSON(t *testing.T, v spSetBody) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (a spREST) set(t *testing.T, store string, kv ...string) spPush {
	t.Helper()
	st, body := a.api.call(t, http.MethodPost, "/v1/stores/set", spJSON(t, spSetBody{store, kv}))
	var v spPush
	if st != http.StatusOK || json.Unmarshal([]byte(body), &v) != nil || v.Store == "" {
		t.Fatalf("POST /v1/stores/set %s %v: HTTP %d %s", store, kv, st, body)
	}
	return v
}

func (a spREST) show(t *testing.T, store string) spPush {
	t.Helper()
	st, body := a.api.call(t, http.MethodGet, "/v1/stores/"+store, "")
	var o spShowOut
	if st != http.StatusOK || json.Unmarshal([]byte(body), &o) != nil || o.Push.Store == "" {
		t.Fatalf("GET /v1/stores/%s: HTTP %d %s", store, st, body)
	}
	return o.Push
}

func (a spREST) refused(t *testing.T, code, store string, kv ...string) string {
	t.Helper()
	st, body := a.api.call(t, http.MethodPost, "/v1/stores/set", spJSON(t, spSetBody{store, kv}))
	return spRESTRefused(t, "POST /v1/stores/set "+store, st, body, code)
}

// spRESTRefused asserts a 4xx answer whose envelope carries code and a
// hint, and returns its message.
func spRESTRefused(t *testing.T, what string, st int, body, code string) string {
	t.Helper()
	var env errs.Envelope
	switch {
	case st/100 != 4:
		t.Errorf("%s: want a 4xx %s, got HTTP %d %s", what, code, st, body)
	case json.Unmarshal([]byte(body), &env) != nil || string(env.Error.Code) != code:
		t.Errorf("%s: want %s, got HTTP %d %s", what, code, st, body)
	case env.Error.Hint == "":
		t.Errorf("%s: %s names no next step: %s", what, code, body)
	}
	return env.Error.Message
}

// spSettingsLife drives me's push settings through their life on surface
// s; after each REST change the CLI reads the same settings back.
func spSettingsLife(t *testing.T, shape string, s spSurface) {
	m := Build(t, shape, loreBin)
	repo := m.Repos[RepoLore]
	meBare := spBare(t, m, "me", spMeURL)
	lm := newLifeModel(t, m)
	at := shape + "/" + s.name()
	step := func(what string, want spWant) {
		t.Helper()
		want.check(t, at+"/"+what, s.show(t, "me"))
		// The CLI on the same home agrees with what the surface set.
		want.check(t, at+"/"+what+" (cli)", spShow(t, m, repo, "me"))
		lm.checkModel(t, m, at+"/"+what)
		if t.Failed() {
			t.FailNow()
		}
	}
	lsRun(t, m, repo, "store", "init", "me", spMeURL, "--yes")
	s = s.start(t, m, repo)
	if v := s.show(t, "me"); v.Place != "personal" || v.Epoch != 0 {
		t.Errorf("me: place %q epoch %d, want personal 0", v.Place, v.Epoch)
	}
	want := spWant{Push: "now", PushEvery: "5m", Compact: "off"}
	step("init", want)

	want = spWant{Push: "batch", PushEvery: "10m", Compact: "off"}
	if v := s.set(t, "me", "push=batch", "push_every=10m"); !v.Changed {
		t.Errorf("set push=batch: changed=false")
	}
	if got := strings.TrimSpace(m.Lore(t, "", "config", "get", "personal.push").Stdout); got != "batch" {
		t.Errorf("config get personal.push = %q after set push=batch", got)
	}
	step("batch", want)
	// The same value again changes nothing.
	if v := s.set(t, "me", "push=batch"); v.Changed {
		t.Errorf("set push=batch again: changed=true")
	}
	step("batch again", want)

	want.Push = "manual"
	s.set(t, "me", "push=manual")
	step("manual", want)
	before := spLog(t, meBare)
	id := spMemAdd(t, m, repo, "me", "kept until sync")
	if got := spLog(t, meBare); len(got) != len(before) {
		t.Errorf("manual: a write was pushed: %v", spSubjects(got))
	}
	lsRun(t, m, repo, "sync")
	if got := spLog(t, meBare); len(got) != len(before)+1 || !strings.HasPrefix(got[0].Subject, spBatchSubject) || !strings.Contains(got[0].Body, id) {
		t.Errorf("manual: sync did not push one batch commit naming %s: %v", id, spSubjects(got))
	}
	step("manual write and sync", want)

	want.Push = "now"
	s.set(t, "me", "push=now")
	step("back to now", want)
	before = spLog(t, meBare)
	id = spMemAdd(t, m, repo, "me", "pushed at once")
	if got := spLog(t, meBare); len(got) != len(before)+1 || strings.HasPrefix(got[0].Subject, spBatchSubject) || !strings.Contains(got[0].Subject+got[0].Body, id) {
		t.Errorf("now: the write was not pushed as its own commit: %v", spSubjects(got))
	}
	step("now write", want)

	// compact=on needs compact_after; the refusal changes nothing.
	if msg := s.refused(t, "E_INVALID_INPUT", "me", "compact=on"); !strings.Contains(msg, "compact_after") {
		t.Errorf("compact=on alone: the refusal does not name compact_after: %q", msg)
	}
	step("compact=on refused", want)
	want.Compact, want.CompactAfter = "on", "30d"
	s.set(t, "me", "compact=on", "compact_after=30d")
	step("compact on", want)
	want.Compact = "off"
	s.set(t, "me", "compact=off")
	step("compact off", want)

	// Every bad value is refused with its reason; nothing changes.
	for _, bad := range []struct {
		code string
		kv   []string
		says string
	}{
		{"E_INVALID_INPUT", []string{"push=sometimes"}, "now, batch or manual"},
		{"E_INVALID_INPUT", []string{"push_every=30s"}, "at least 1m"},
		{"E_INVALID_INPUT", []string{"compact_after=12h"}, "at least 1d"},
		{"E_INVALID_INPUT", []string{"bogus=1"}, "unknown setting"},
		{"E_INVALID_INPUT", []string{"push"}, "key=value"},
		// A bad value with a good one: the good one is not applied either.
		{"E_INVALID_INPUT", []string{"push=batch", "push_every=0m"}, "at least 1m"},
		// Zero is a duration below the minimum, not a non-duration.
		{"E_INVALID_INPUT", []string{"compact_after=0d"}, "at least 1d"},
	} {
		if msg := s.refused(t, bad.code, "me", bad.kv...); !strings.Contains(msg, bad.says) {
			t.Errorf("set %v: refusal %q does not say %q", bad.kv, msg, bad.says)
		}
	}
	step("bad values refused", want)

	// Stores push settings never apply to.
	if msg := s.refused(t, "E_REFUSED", "repo", "push=batch"); !strings.Contains(msg, "code repo") {
		t.Errorf("set on repo: refusal %q does not name the code repo", msg)
	}
	s.refused(t, "E_NOT_FOUND", "nosuch", "push=batch")
	lsRun(t, m, repo, "store", "init", "kb", "file://"+filepath.Join(m.Dir, "kb-store"), "--yes")
	if msg := s.refused(t, "E_REFUSED", "kb", "push=batch"); !strings.Contains(msg, "folder store") {
		t.Errorf("set on a folder store: refusal %q does not say folder store", msg)
	}
	step("other stores refused", want)
}

// spCompactIn is POST /v1/stores/compact's body.
type spCompactIn struct {
	Store  string `json:"store"`
	Before string `json:"before,omitempty"`
	DryRun bool   `json:"dry_run,omitempty"`
	Yes    bool   `json:"yes,omitempty"`
}

// spCompactOut is store compact's answer (opsstores.CompactOut).
type spCompactOut struct {
	Store     string `json:"store"`
	DryRun    bool   `json:"dry_run"`
	Before    string `json:"before"`
	Nothing   bool   `json:"nothing"`
	Squashed  int    `json:"squashed"`
	Kept      int    `json:"kept"`
	Rows      int    `json:"rows"`
	FromEpoch int    `json:"from_epoch"`
	ToEpoch   int    `json:"to_epoch"`
	Backup    string `json:"backup"`
}

func spCompactCLI(t *testing.T, m *Machine, cwd string, args ...string) spCompactOut {
	t.Helper()
	out := lsRun(t, m, cwd, append([]string{"store", "compact", lsOrg}, args...)...)
	var o spCompactOut
	if err := json.Unmarshal([]byte(out), &o); err != nil || o.Store == "" {
		t.Fatalf("store compact %v: %v: %s", args, err, out)
	}
	return o
}

// spRowIDs is every memory id memory list shows in cwd.
func spRowIDs(t *testing.T, m *Machine, cwd string) string {
	t.Helper()
	return lsRun(t, m, cwd, "memory", "list")
}

// spCompactionLife: an admin (owner-like machine) and a member (shape)
// of one team; see the file comment.
func spCompactionLife(t *testing.T, shape string) {
	bin := spTestBin(t)
	// The admin starts the team under the old clock and invites the member.
	admin := Build(t, ShapeOwnerLike, bin)
	spAt(admin, spOld)
	lsGHHosts(t, admin)
	bare := spBare(t, admin, "team", lsTeamURL)
	tm := lsTeam{storeURL: lsTeamURL, bare: bare}
	lsCreate(t, admin, "boss", kindAdmin, "boss@example.test", []string{"~/code/**"}, true, tm, lsGitHub{LoginBravo, "github-bravo"})
	aRepo := admin.Repos[RepoWorkApp]
	var inv struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal([]byte(lsRun(t, admin, aRepo, "team", "invite", "member@example.test", "--yes")), &inv)
	if tm.invite = inv.Text; !strings.HasPrefix(tm.invite, "lore-invite:") {
		t.Fatalf("team invite printed no invite line: %s", inv.Text)
	}
	member := Build(t, shape, bin)
	spAt(member, spOld)
	lsGHHosts(t, member)
	tm.rewrite(t, member)
	lsCreate(t, member, "joined", kindMember, "member@example.test", []string{lsFolder}, false, tm, lsAlpha)
	mRepo := member.Repos[RepoWorkApp]

	// Old history: three rows written in September by the admin.
	var ids []string
	for _, b := range []string{"old fact one", "old fact two", "old fact three"} {
		ids = append(ids, spMemAdd(t, admin, aRepo, lsOrg, b))
	}
	// Today: one more row by the admin; the member syncs, then writes a
	// row it does not push.
	spAt(admin, spNow)
	spAt(member, spNow)
	ids = append(ids, spMemAdd(t, admin, aRepo, lsOrg, "new fact"))
	lsRun(t, member, mRepo, "sync")
	spEnv(member, "LORE_LIVE_AUTOPUSH", "0")
	unpushed := spMemAdd(t, member, mRepo, lsOrg, "member's unpushed fact")
	spEnv(member, "LORE_LIVE_AUTOPUSH", "")
	for _, id := range ids {
		if !strings.Contains(spRowIDs(t, member, mRepo), id) {
			t.Fatalf("the member does not see %s before compaction", id)
		}
	}
	if strings.Contains(strings.Join(spSubjects(spLog(t, bare)), "\n"), unpushed) {
		t.Fatalf("the member's row %s reached the remote with autopush off", unpushed)
	}
	memberGone := goneAtStart(shape)
	admin.CheckMachine(t, "admin/before compaction", Gone{})
	member.CheckMachine(t, shape+"/member/before compaction", memberGone)

	// The member may neither set nor compact the org store (CLI, REST).
	r := member.Lore(t, mRepo, "store", "compact", lsOrg, "--before", "30d", "--yes", "--json")
	if e := csRefused(t, r, "E_REFUSED"); e != nil && !strings.Contains(e.Message, "admin") {
		t.Errorf("member compact: refusal does not name the admin: %s", e.Message)
	}
	r = member.Lore(t, mRepo, "store", "set", lsOrg, "push=batch", "--json")
	if e := csRefused(t, r, "E_REFUSED"); e != nil && !strings.Contains(e.Message, "admin") {
		t.Errorf("member set: refusal does not name the admin: %s", e.Message)
	}
	mAPI := csStartAPI(t, member, mRepo)
	st, body := mAPI.call(t, http.MethodPost, "/v1/stores/set", spJSON(t, spSetBody{lsOrg, []string{"push=batch"}}))
	spRESTRefused(t, "member POST /v1/stores/set", st, body, "E_REFUSED")
	cb, _ := json.Marshal(spCompactIn{Store: lsOrg, Before: "30d", Yes: true})
	st, body = mAPI.call(t, http.MethodPost, "/v1/stores/compact", string(cb))
	spRESTRefused(t, "member POST /v1/stores/compact", st, body, "E_REFUSED")
	// `repo` is never compacted, by anyone.
	r = admin.Lore(t, aRepo, "store", "compact", "repo", "--yes", "--json")
	csRefused(t, r, "E_REFUSED")

	// The admin: a dry run changes nothing; without --yes it asks.
	remote := spLog(t, bare)
	dry := spCompactCLI(t, admin, aRepo, "--before", "30d", "--dry-run")
	if !dry.DryRun || dry.Squashed < 2 || dry.Before != spCutoff || dry.FromEpoch != 0 || dry.ToEpoch != 1 {
		t.Errorf("dry run: %+v", dry)
	}
	r = admin.Lore(t, aRepo, "store", "compact", lsOrg, "--before", "30d", "--json")
	csRefused(t, r, "E_NEEDS_CONFIRM")
	if got := spLog(t, bare); len(got) != len(remote) || got[0].SHA != remote[0].SHA {
		t.Fatalf("a dry run or an unconfirmed compact changed the remote: %v", spSubjects(got))
	}

	// The compaction.
	out := spCompactCLI(t, admin, aRepo, "--before", "30d", "--yes")
	if out.DryRun || out.Squashed != dry.Squashed || out.Kept != dry.Kept || out.ToEpoch != 1 || !strings.HasPrefix(out.Backup, "refs/lore/backup/compact-0-") {
		t.Errorf("compact: %+v (dry run said %+v)", out, dry)
	}
	compacted := spLog(t, bare)
	root := compacted[len(compacted)-1]
	switch {
	case len(root.Parents) != 0 || root.Subject != "lore: snapshot of the store history before "+spCutoff:
		t.Errorf("compacted history's root %q has parents %v", root.Subject, root.Parents)
	case root.Date != spOld[:10]:
		t.Errorf("snapshot dated %s, want the old history's %s", root.Date, spOld[:10])
	case compacted[0].Subject != "lore: compact store history (epoch 1)":
		t.Errorf("compacted history's tip is %q", compacted[0].Subject)
	case len(compacted) != out.Kept+2:
		t.Errorf("compacted history has %d commits, want snapshot + %d kept + epoch: %v", len(compacted), out.Kept, spSubjects(compacted))
	}
	if v := spShow(t, admin, aRepo, lsOrg); v.Epoch != 1 {
		t.Errorf("admin store show after compaction: epoch %d", v.Epoch)
	}
	for _, id := range ids {
		if !strings.Contains(spRowIDs(t, admin, aRepo), id) {
			t.Errorf("the admin lost %s in the compaction", id)
		}
	}
	admin.CheckMachine(t, "admin/after compaction", Gone{})

	// The member syncs: it moves onto the new history and replays its row.
	lsRun(t, member, mRepo, "sync")
	after := spLog(t, bare)
	if after[0].Subject != "lore: replay local work onto epoch 1" || len(after) != len(compacted)+1 || after[1].SHA != compacted[0].SHA {
		t.Errorf("member sync after compaction: remote %v", spSubjects(after))
	}
	if r := after[len(after)-1]; r.SHA != root.SHA {
		t.Errorf("member sync rewrote the snapshot root: %v", spSubjects(after))
	}
	if v := spShow(t, member, mRepo, lsOrg); v.Epoch != 1 || v.Unpushed != 0 {
		t.Errorf("member store show after sync: epoch %d unpushed %d", v.Epoch, v.Unpushed)
	}
	lsRun(t, admin, aRepo, "sync")
	for _, mm := range []struct {
		who  string
		m    *Machine
		repo string
	}{{"admin", admin, aRepo}, {"member", member, mRepo}} {
		l := spRowIDs(t, mm.m, mm.repo)
		for _, id := range append(ids, unpushed) {
			if !strings.Contains(l, id) {
				t.Errorf("%s after the replay misses %s", mm.who, id)
			}
		}
	}
	member.CheckMachine(t, shape+"/member/after replay", memberGone)

	// Over REST the admin: a second dry run finds nothing; compact=on
	// with compact_after, which the member reads after a sync.
	aAPI := csStartAPI(t, admin, aRepo)
	cb, _ = json.Marshal(spCompactIn{Store: lsOrg, Before: "30d", DryRun: true})
	st, body = aAPI.call(t, http.MethodPost, "/v1/stores/compact", string(cb))
	var again spCompactOut
	if st != http.StatusOK || json.Unmarshal([]byte(body), &again) != nil || !again.Nothing {
		t.Errorf("REST dry run after compaction: HTTP %d %s", st, body)
	}
	st, body = aAPI.call(t, http.MethodPost, "/v1/stores/set", spJSON(t, spSetBody{lsOrg, []string{"compact=on"}}))
	spRESTRefused(t, "admin POST /v1/stores/set compact=on", st, body, "E_INVALID_INPUT")
	st, body = aAPI.call(t, http.MethodPost, "/v1/stores/set", spJSON(t, spSetBody{lsOrg, []string{"compact=on", "compact_after=30d"}}))
	var set spPush
	if st != http.StatusOK || json.Unmarshal([]byte(body), &set) != nil {
		t.Fatalf("admin POST /v1/stores/set compact=on: HTTP %d %s", st, body)
	}
	want := spWant{Push: "now", PushEvery: "5m", Compact: "on", CompactAfter: "30d"}
	want.check(t, "admin REST compact on", set)
	want.check(t, "admin CLI after REST", spShow(t, admin, aRepo, lsOrg))
	lsRun(t, member, mRepo, "sync")
	mv := spShow(t, member, mRepo, lsOrg)
	want.check(t, "member after sync", mv)
	if mv.Admin {
		t.Errorf("member store show says admin")
	}
	// Today's rows are younger than compact_after: an auto-compaction (at
	// most daily, after a push) leaves the history as it is.
	late := spMemAdd(t, admin, aRepo, lsOrg, "after compact on")
	if !strings.Contains(spRowIDs(t, admin, aRepo), late) {
		t.Errorf("the admin does not list %s", late)
	}
	if got := spLog(t, bare); got[len(got)-1].SHA != root.SHA {
		t.Errorf("a write with compact=on rewrote young history: %v", spSubjects(got))
	}
	admin.CheckMachine(t, "admin/compact on", Gone{})
	member.CheckMachine(t, shape+"/member/compact on", memberGone)
}
