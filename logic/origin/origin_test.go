package origin

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
)

const commit = "4e6ed1c09b7a2f51d3c8e0a4b6f2d1e9c7a5b3f0"

// TestBuildShapes: absent facts are left out; git writes only what its
// state allows; keys come out sorted.
func TestBuildShapes(t *testing.T) {
	cases := []struct {
		name string
		f    Facts
		want string
	}{
		{"nothing", Facts{}, ``},
		{"cmd and store", Facts{Cmd: "memory add", Store: "repo"}, `{"cmd":"memory add","store":"repo"}`},
		{"branch", Facts{Cmd: "task add", Git: Git{State: GitBranch, Branch: "feat/vpn", Commit: commit}},
			`{"cmd":"task add","git":{"branch":"feat/vpn","commit":"` + commit + `","state":"branch"}}`},
		{"detached drops a branch", Facts{Git: Git{State: GitDetached, Branch: "x", Commit: commit}}, `{"git":{"commit":"` + commit + `","state":"detached"}}`},
		{"unborn has no commit", Facts{Git: Git{State: GitUnborn, Commit: commit}}, `{"git":{"state":"unborn"}}`},
		{"none", Facts{Git: Git{State: GitNone}}, `{"git":{"state":"none"}}`},
		{"repo dir import", Facts{Repo: "rep_1", Dir: "services/billing", Import: Import{Source: "jira", ExtID: "AGY-1"}},
			`{"dir":"services/billing","import":{"ext_id":"AGY-1","source":"jira"},"repo":"rep_1"}`},
	}
	for _, c := range cases {
		got, err := Build(c.f)
		if err != nil || got.String() != c.want {
			t.Errorf("%s: %s %v, want %s", c.name, got.String(), err, c.want)
		}
	}
}

// TestRelDir is PROV6: relative, forward slashes, omitted at the root and
// outside the repo; never absolute.
func TestRelDir(t *testing.T) {
	cases := []struct{ root, dir, want string }{
		{"/r", "/r", ""},
		{"/r", "/r/", ""},
		{"/r", "/r/services/billing", "services/billing"},
		{"/r", "/r/a/../b", "b"},
		{"/r", "/elsewhere", ""},
		{"/r", "/rx/y", ""},
		{"", "/r/a", ""},
		{"/r", "", ""},
	}
	for _, c := range cases {
		if got := RelDir(c.root, c.dir); got != c.want {
			t.Errorf("RelDir(%q, %q) = %q, want %q", c.root, c.dir, got, c.want)
		}
	}
}

// full is an _o with every key set.
func full(t *testing.T) value.JSON {
	t.Helper()
	o, err := value.JSONOf([]byte(`{"cmd":"memory add","copied_from":"mem_x","dir":"svc","folder":"prj_f","git":{"branch":"mira/leaving-acme","commit":"` + commit + `","state":"branch"},"import":{"source":"jira","src":"ext_1"},"moves":[{"moved_to":"repo"}],"org":"org_1","plugin":"plg_1","project":"prj_p","repo":"rep_r","store":"agency"}`))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// TestClientFilterTable is PROVENANCE_DESIGN 8.1, one row per key: an
// internal value is dropped, the client's own is kept.
func TestClientFilterTable(t *testing.T) {
	o := full(t)
	// moved in from our side, nothing known about the client: cmd alone
	got, err := ClientFilter(o, Client{OwnStore: "repo"})
	if err != nil || got.String() != `{"cmd":"memory add"}` {
		t.Fatalf("moved-in block = %s %v", got, err)
	}
	// born here, the client's own ids, branch with an upstream, local source and row
	c := Client{OwnStore: "agency", RepoID: "rep_r", ProjectID: "prj_p", BornHere: true,
		Upstream:    func(b string) bool { return b == "mira/leaving-acme" },
		LocalSource: func(s string) bool { return s == "ext_1" },
		LocalRow:    func(id string) bool { return id == "mem_x" }}
	got, err = ClientFilter(o, c)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"cmd":"memory add","copied_from":"mem_x","dir":"svc","git":{"branch":"mira/leaving-acme","commit":"` + commit + `","state":"branch"},"import":{"source":"jira","src":"ext_1"},"project":"prj_p","repo":"rep_r","store":"agency"}`
	if got.String() != want {
		t.Fatalf("born-here block =\n%s\nwant\n%s", got, want)
	}
	for _, never := range []string{"org", "folder", "plugin", "moves"} {
		if strings.Contains(got.String(), `"`+never+`"`) {
			t.Fatalf("%s reached a client repo: %s", never, got)
		}
	}
	// other ids than the client's own: dropped
	other := c
	other.RepoID, other.ProjectID, other.OwnStore = "rep_other", "prj_other", "repo"
	got, _ = ClientFilter(o, other)
	for _, k := range []string{`"repo"`, `"project"`, `"store"`} {
		if strings.Contains(got.String(), k) {
			t.Fatalf("foreign %s kept: %s", k, got)
		}
	}
}

// TestClientFilterDropsLocalBranch is PROV8, break-verified: a local-only
// branch is dropped while commit and state stay; nothing records the drop.
func TestClientFilterDropsLocalBranch(t *testing.T) {
	o := full(t)
	for _, up := range []func(string) bool{nil, func(string) bool { return false }} {
		got, err := ClientFilter(o, Client{BornHere: true, Upstream: up})
		if err != nil {
			t.Fatal(err)
		}
		s := got.String()
		if strings.Contains(s, "branch\":") && strings.Contains(s, "leaving-acme") {
			t.Fatalf("local-only branch kept: %s", s)
		}
		if !strings.Contains(s, `"commit":"`+commit+`"`) || !strings.Contains(s, `"state":"branch"`) {
			t.Fatalf("commit/state dropped: %s", s)
		}
	}
}

// TestClientFilterEmpty: an empty result is no _o at all.
func TestClientFilterEmpty(t *testing.T) {
	o, _ := value.JSONOf([]byte(`{"org":"org_1"}`))
	got, err := ClientFilter(o, Client{})
	if err != nil || !got.IsZero() {
		t.Fatalf("got %s %v", got, err)
	}
	if got, _ = ClientFilter(value.JSON{}, Client{}); !got.IsZero() {
		t.Fatal("zero in, zero out")
	}
}

// TestStamp is OQ7 / Q-R3-3: {m, v} on our side, {v} in a client repo.
func TestStamp(t *testing.T) {
	m, err := ids.ParseMachine("mch_" + strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	ours, _ := Stamp("0.1.18", m, false)
	if ours.String() != `{"m":"`+m.String()+`","v":"0.1.18"}` {
		t.Fatalf("ours = %s", ours)
	}
	client, _ := Stamp("0.1.18", m, true)
	if client.String() != `{"v":"0.1.18"}` {
		t.Fatalf("client = %s", client)
	}
	if none, _ := Stamp("", ids.MachineID{}, false); !none.IsZero() {
		t.Fatalf("nothing to say = %s", none)
	}
}

// TestTextWith: reading and setting one member.
func TestTextWith(t *testing.T) {
	o, err := With(value.JSON{}, KeyStore, "repo")
	if err != nil || o.String() != `{"store":"repo"}` {
		t.Fatalf("%s %v", o, err)
	}
	o, _ = With(full(t), KeyStore, "repo")
	if Text(o, KeyStore) != "repo" || Text(o, KeyRepo) != "rep_r" || Text(o, KeyGit) != "" || Text(value.JSON{}, KeyRepo) != "" {
		t.Fatalf("Text/With: %s", o)
	}
}
