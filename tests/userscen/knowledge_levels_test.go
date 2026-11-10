//go:build userscen

package userscen

// Knowledge rows at every level a person writes them to, the flows the
// first knowledge pass left out (reports/knowledge.md "Not covered"):
// the personal store (--to me, --scope global), an org store and its
// folders (--to <store>/<folder>, --create, list --store), folder scopes
// (--scope dir:<path>), the repo flags of a multi-repo project (--repo,
// --no-inherit, --master-only, --all-repos) and live sharing (--live,
// --no-live). Each test is one person's (or two people's) real session
// through the binary.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// knowExitNotFound is E_NOT_FOUND's exit code (model/errs ClassNotFound).
const knowExitNotFound = 27

// knowExitNeedsConfirm is E_NEEDS_CONFIRM's exit code (errs class 26).
const knowExitNeedsConfirm = 26

// knowStoreOf is the store a list row came from: `_label` names it as
// "[<store>/<folder>]" for store rows; repo rows carry no label.
type knowLabelled struct {
	ID    string `json:"id"`
	Label string `json:"_label"`
	Repo  string `json:"repo_id"`
}

// knowLabelledList is list --json with the per-row store label.
type knowLabelledList struct {
	Count int            `json:"count"`
	Data  []knowLabelled `json:"data"`
}

// knowIDs runs `lore memory list --json` plus extra in c and returns the
// ids in order.
func knowIDs(t *testing.T, c *th.Clone, extra ...string) []string {
	t.Helper()
	l := th.DecodeJSON[knowLabelledList](t, c.MustLore(append([]string{"memory", "list", "--json"}, extra...)...))
	out := make([]string, 0, len(l.Data))
	for _, r := range l.Data {
		out = append(out, r.ID)
	}
	return out
}

// knowSame reports whether got holds exactly want, in any order.
func knowSame(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}

// A person with no personal store yet: `--to me` and `--scope global`
// (which writes to the personal store too) both refuse with the same
// set-up hint and write nothing. Before the fix --scope global answered a
// bare "wire: write the owner's actor row to store me" with no hint,
// because the hint only looked at the outer message of a wrapped error.
func TestKnowledgeLevels_NoPersonalStoreHint(t *testing.T) {
	scenParallel(t)
	w := newWorld(t)
	a := w.Human("person", "person@example.com")
	c := a.Clone(w.Remote("github.com/octo-personal/app"), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	for _, args := range [][]string{
		{"memory", "add", "--body", "mine everywhere", "--to", "me"},
		{"memory", "add", "--body", "mine everywhere", "--scope", "global"},
		{"rule", "add", "--body", "mine everywhere", "--scope", "global"},
	} {
		e := knowJSONErr(t, c.Lore(append(args, "--json")...))
		if e.Error.Code != "E_NOT_FOUND" || !strings.Contains(e.Error.Hint, "lore store init me") {
			t.Fatalf("%v: want E_NOT_FOUND with the `lore store init me` hint, got %+v", args, e.Error)
		}
	}
	if got := knowIDs(t, c, "--all-repos"); len(got) != 0 {
		t.Fatalf("a refused write wrote rows: %v", got)
	}
}

// The personal store: --to me and --scope global land in `me`; list
// shows them in every repo of the person (a second repo too), list
// --store me / --store repo split them, and an unknown store is refused.
func TestKnowledgeLevels_PersonalStore(t *testing.T) {
	scenParallel(t)
	w, a, c := knowProject(t)
	toMe := th.DecodeJSON[knowAdded](t, c.MustLore("memory", "add", "--body", "I prefer table tests", "--to", "me", "--json"))
	global := th.DecodeJSON[knowAdded](t, c.MustLore("memory", "add", "--body", "I write UTC times", "--scope", "global", "--json"))
	repo := th.DecodeJSON[knowAdded](t, c.MustLore("memory", "add", "--body", "this repo uses make", "--json"))
	if toMe.Store != "me" || global.Store != "me" || repo.Store != "repo" {
		t.Fatalf("stores: --to me %q, --scope global %q, default %q; want me, me, repo", toMe.Store, global.Store, repo.Store)
	}
	if got := knowIDs(t, c); !knowSame(got, toMe.ID, global.ID, repo.ID) {
		t.Fatalf("list in the repo: %v, want the two personal rows and the repo row", got)
	}
	if got := knowIDs(t, c, "--store", "me"); !knowSame(got, toMe.ID, global.ID) {
		t.Fatalf("list --store me: %v", got)
	}
	if got := knowIDs(t, c, "--store", "repo"); !knowSame(got, repo.ID) {
		t.Fatalf("list --store repo: %v", got)
	}
	c.Lore("memory", "list", "--store", "nope").Fails(t, knowExitNotFound, "no store named nope is attached")
	// The same person in another repo sees their personal rows, not the
	// first repo's.
	other := a.Clone(w.Remote("github.com/octo-personal/site"), "site")
	other.MustLore("init", "--non-interactive", "--name=site")
	if got := knowIDs(t, other); !knowSame(got, toMe.ID, global.ID) {
		t.Fatalf("list in a second repo: %v, want only the personal rows", got)
	}
	other.MustLore("memory", "show", global.ID).Says(t, "I write UTC times")
}

// An org store: --to <store>/<folder> refuses an unknown folder (asking,
// nothing written) until --create; --to <store> writes to the store's
// root. A folder this repo does not attach is not listed here (STORE
// TARGETS: a folder is shown only in repos that attach it) until the
// person attaches it with `lore store add --folder`; then list --store
// names it, and a teammate who attaches the same folder sees the rows.
func TestKnowledgeLevels_OrgStoreFolders(t *testing.T) {
	o := newOrgScen(t)
	e := knowJSONErr(t, o.app.Lore("memory", "add", "--body", "shared tip", "--to", orgName+"/tips", "--json"))
	if e.Error.Code != "E_NEEDS_CONFIRM" || !strings.Contains(e.Error.Message, "nothing written") || !strings.Contains(e.Error.Hint, "--create") {
		t.Fatalf("unknown folder: want E_NEEDS_CONFIRM naming --create, got %+v", e.Error)
	}
	// INTEG-KNOWLEDGE-7: a write to a folder this repo does not attach says
	// so on stderr (the --json stdout stays one document) and names the
	// attach command; the store root, which the repo attaches, says nothing.
	note := orgName + "/tips is not attached in this repo"
	r1 := o.app.MustLore("memory", "add", "--body", "shared tip", "--to", orgName+"/tips", "--create", "--json").Says(t, note, "lore store add <name> <store url> --folder tips --yes")
	r2 := o.app.MustLore("memory", "add", "--body", "second tip", "--to", orgName+"/tips", "--json").Says(t, note)
	r3 := o.app.MustLore("memory", "add", "--body", "agency wide", "--to", orgName, "--json").Never(t, "not attached in this repo")
	tip, tip2, root := th.DecodeJSON[knowAdded](t, r1), th.DecodeJSON[knowAdded](t, r2), th.DecodeJSON[knowAdded](t, r3)
	if tip.Store != orgName+"/tips" || tip2.Store != orgName+"/tips" || root.Store != orgName {
		t.Fatalf("stores: %q %q %q", tip.Store, tip2.Store, root.Store)
	}
	o.app.Lore("memory", "add", "--body", "x", "--to", "nope/x").Fails(t, knowExitNotFound, "no store named nope/x is attached")
	if got := knowIDs(t, o.app); !knowSame(got, root.ID) {
		t.Fatalf("list before attaching tips: %v, want only the store-root row", got)
	}
	// INTEG-CFG7-1: attaching the org store's folder writes the org's url
	// into the committed stores.yaml, so without a terminal it is refused
	// (E_NEEDS_CONFIRM naming --yes, nothing written) and --yes writes it.
	storesFile := filepath.Join(o.app.Dir, ".lore", "stores.yaml")
	before, _ := os.ReadFile(storesFile)
	o.app.Lore("store", "add", "tips", "https://"+orgSlugNotes, "--folder", "tips").
		Fails(t, knowExitNeedsConfirm, "visible to everyone who can read this repo", "--yes")
	if after, _ := os.ReadFile(storesFile); string(after) != string(before) {
		t.Fatalf("a refused store add changed stores.yaml:\n%s", after)
	}
	o.app.MustLore("store", "add", "tips", "https://"+orgSlugNotes, "--folder", "tips", "--yes")
	if got := knowIDs(t, o.app, "--store", "tips"); !knowSame(got, tip.ID, tip2.ID) {
		t.Fatalf("list --store tips: %v", got)
	}
	if got := knowIDs(t, o.app); !knowSame(got, root.ID, tip.ID, tip2.ID) {
		t.Fatalf("list across stores after attaching: %v", got)
	}
	o.app.CommitAll("attach tips")
	o.app.AdminPush("origin", "main")
	_, bob := o.join(t, "bob", orgBobEmail, orgBobName, "bob")
	if got := knowIDs(t, bob, "--store", "tips"); !knowSame(got, tip.ID, tip2.ID) {
		t.Fatalf("bob's list --store tips: %v", got)
	}
	bob.MustLore("memory", "show", tip.ID).Says(t, "shared tip")
}

// Folder scopes: --scope dir:<path> binds a row to a folder of the repo
// (./ and a trailing / name the same scope), refuses a path outside the
// repo, an absolute path, an empty path and a missing folder with
// nothing written, and lists by its repo (G-RENDER-1: a list without
// --repo shows project-wide rows only; --all-repos or --repo shows it).
func TestKnowledgeLevels_DirScope(t *testing.T) {
	scenParallel(t)
	_, _, c := knowProject(t)
	if err := os.MkdirAll(filepath.Join(c.Dir, "src", "billing"), 0o750); err != nil {
		t.Fatal(err)
	}
	refusals := []struct{ scope, say string }{
		{"dir:../outside", "outside the repo"},
		{"dir:/etc", "relative to the repo root"},
		{"dir:", "empty path"},
		{"dir:src/missing", "no such folder in the repo"},
		{"folder:src", "not one of global"},
	}
	for _, r := range refusals {
		e := knowJSONErr(t, c.Lore("memory", "add", "--body", "x", "--scope", r.scope, "--json"))
		if e.Error.Code != "E_INVALID_INPUT" || !strings.Contains(e.Error.Message, r.say) {
			t.Fatalf("--scope %q: want E_INVALID_INPUT saying %q, got %+v", r.scope, r.say, e.Error)
		}
	}
	if got := knowIDs(t, c, "--all-repos"); len(got) != 0 {
		t.Fatalf("refused scopes wrote rows: %v", got)
	}
	plain := knowAdd(t, c, knowKinds[0], "", "project wide")
	d1 := knowAdd(t, c, knowKinds[0], "", "amounts in minor units", "--scope", "dir:src/billing")
	d2 := knowAdd(t, c, knowKinds[0], "", "never round twice", "--scope", "dir:./src/billing/")
	rul := knowAdd(t, c, knowKinds[1], "", "billing rule", "--scope", "dir:src/billing")
	c.MustLore("scope", "list").Says(t, "dir:src/billing")
	if strings.Count(c.MustLore("scope", "list").Stdout, "dir:") != 1 {
		t.Fatalf("dir:src/billing and dir:./src/billing/ must be one scope:\n%s", c.MustLore("scope", "list").Stdout)
	}
	if got := knowIDs(t, c); !knowSame(got, plain) {
		t.Fatalf("list without --repo: %v, want only the project-wide row (G-RENDER-1)", got)
	}
	if got := knowIDs(t, c, "--all-repos"); !knowSame(got, plain, d1, d2) {
		t.Fatalf("list --all-repos: %v", got)
	}
	if got := knowIDs(t, c, "--repo", "app", "--no-inherit"); !knowSame(got, d1, d2) {
		t.Fatalf("list --repo app --no-inherit: %v", got)
	}
	c.MustLore("context", "--for-file", "src/billing/invoice.go").Says(t, rul)
}

// A multi-repo project: rows added with --repo belong to that repo; the
// four read scopes pick them as the search docs' table says, an unknown
// repo is refused on add and on list, and --all-repos ignores --repo.
func TestKnowledgeLevels_MultiRepoFlags(t *testing.T) {
	scenParallel(t)
	_, _, c := knowProject(t)
	c.MustLore("repo", "add", "web", "--origin", "https://github.com/octo-personal/website")
	c.MustLore("repo", "add", "api", "--origin", "https://github.com/octo-personal/api")
	shared := knowAdd(t, c, knowKinds[0], "", "times are UTC")
	web := knowAdd(t, c, knowKinds[0], "", "web deploys on merge", "--repo", "web")
	api := knowAdd(t, c, knowKinds[0], "", "api tests before push", "--repo", "api")
	cases := []struct {
		flags []string
		want  []string
	}{
		{nil, []string{shared}},
		{[]string{"--master-only"}, []string{shared}},
		{[]string{"--repo", "web"}, []string{shared, web}},
		{[]string{"--repo", "web", "--no-inherit"}, []string{web}},
		{[]string{"--all-repos"}, []string{shared, web, api}},
		{[]string{"--all-repos", "--repo", "web"}, []string{shared, web, api}},
		{[]string{"--master-only", "--repo", "web"}, []string{shared}},
	}
	for _, tc := range cases {
		if got := knowIDs(t, c, tc.flags...); !knowSame(got, tc.want...) {
			t.Fatalf("memory list %v: %v, want %v", tc.flags, got, tc.want)
		}
	}
	c.MustLore("memory", "add", "--body", "rescoped", "--repo", "web").Says(t, "repo:web")
	c.Lore("memory", "add", "--body", "x", "--repo", "nope").Refused(t, "nope")
	c.Lore("memory", "list", "--repo", "nope").Refused(t, "nope")
}

// Live sharing of knowledge rows: memories are kept with the code by
// default; --live puts one on the live ref, where a teammate sees it on
// their next command with no git step; with live.defaults memories:true
// a write goes live by default and --no-live keeps it with the code.
func TestKnowledgeLevels_LiveFlags(t *testing.T) {
	liveOn := th.FreshFetch()
	liveOn.Defaults = map[string]bool{syncTblMemory: true}
	st := newSyncTeam(t, 2, th.Config{Live: th.FreshFetch()}, th.Config{Live: liveOn})
	a, b := st.clones[0], st.clones[1]
	cases := []struct {
		c, other *th.Clone
		flag     string
		live     bool
	}{
		{a, b, "", false},          // memories default: with the code
		{a, b, "--live", true},     // flag on
		{b, a, "", true},           // live.defaults memories:true
		{b, a, "--no-live", false}, // default on, flag off
	}
	for i, tc := range cases {
		body := "live case " + string(rune('a'+i))
		args := []string{"memory", "add", "--body", body, "--json"}
		if tc.flag != "" {
			args = append(args, tc.flag)
		}
		id := th.DecodeJSON[knowAdded](t, tc.c.MustLore(args...)).ID
		if _, onLive := st.app.Read(syncRefLive, liveRowFile(syncTblMemory, id)); onLive != tc.live {
			t.Fatalf("case %d (%q): on the live ref = %v, want %v", i, tc.flag, onLive, tc.live)
		}
		seen := false
		for _, got := range knowIDs(t, tc.other) {
			seen = seen || got == id
		}
		if seen != tc.live {
			t.Fatalf("case %d (%q): teammate sees it before any git step = %v, want %v", i, tc.flag, seen, tc.live)
		}
		if !tc.live && !strings.Contains(tc.c.Status(), ".lore/data/memories/") {
			t.Fatalf("case %d: a row kept with the code is a working-tree file: %q", i, tc.c.Status())
		}
	}
	a.Lore("memory", "add", "--body", "both", "--live", "--no-live").Refused(t, "--live")
}

// The knowledge open questions the owner settled (INTEG-KNOWLEDGE-4..6),
// as a person meets them: --title works on edit for the five kinds whose
// add takes --title but whose rows store a name; an unknown memory --kind
// is the same E_INVALID_INPUT as an unknown rule --severity; a glob rule
// without globs is refused with the hint to pass them.
func TestKnowledgeLevels_SettledQuestions(t *testing.T) {
	scenParallel(t)
	_, _, c := knowProject(t)
	for _, noun := range []string{"playbook", "prompt", "behaviour", "workflow", "workspace"} {
		id := trashID(t, c.MustLore(noun, "add", "--title", "Old "+noun, "--body", "body of "+noun, "--json"))
		c.MustLore(noun, "edit", id, "--title", "New "+noun)
		c.MustLore(noun, "show", id).Says(t, "New "+noun)
		c.MustLore(noun, "edit", id, "--name", "Newer "+noun) // --name keeps working
		c.MustLore(noun, "show", id).Says(t, "Newer "+noun)
		e := knowJSONErr(t, c.Lore(noun, "edit", id, "--name", "a", "--title", "b", "--json"))
		if e.Error.Code != "E_INVALID_INPUT" {
			t.Fatalf("%s edit --name a --title b: %+v", noun, e.Error)
		}
	}
	kind := knowJSONErr(t, c.Lore("memory", "add", "--body", "x", "--kind", "bogus", "--json"))
	sev := knowJSONErr(t, c.Lore("rule", "add", "--body", "x", "--severity", "bogus", "--json"))
	if kind.Error.Code != "E_INVALID_INPUT" || kind.Error.Code != sev.Error.Code || !strings.Contains(kind.Error.Message, `--kind "bogus" is not one of core`) {
		t.Fatalf("memory --kind bogus %+v, rule --severity bogus %+v: want the same E_INVALID_INPUT naming the flag", kind.Error, sev.Error)
	}
	glob := knowJSONErr(t, c.Lore("rule", "add", "--body", "x", "--activation", "glob", "--json"))
	if glob.Error.Code != "E_INVALID_INPUT" || !strings.Contains(glob.Error.Hint, "pass --globs") {
		t.Fatalf("glob rule without globs: %+v", glob.Error)
	}
	c.MustLore("rule", "add", "--body", "go files", "--activation", "glob", "--globs", `["**/*.go"]`)
	if got := knowIDs(t, c, "--all-repos"); len(got) != 0 {
		t.Fatalf("refused memory writes wrote rows: %v", got)
	}
}
