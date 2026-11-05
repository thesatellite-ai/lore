package orgresolve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

const (
	pid     = "prj_019a1b2c3d4e7f00a1b2c3d4e5f60718"
	shStore = "github.com/northwind/lore"
)

// fixture is a two-company HOME: northwind (with a staging entry on the
// same store), betalabs, and a "shared" client both companies list.
func fixture() *orgconf.Config {
	return &orgconf.Config{
		Orgs: []orgconf.Org{
			{
				Name:     "northwind",
				Identity: orgconf.OrgIdentity{Key: "human:alice@northwind.example"},
				Stores:   []orgconf.Store{{URL: "git@github.com:northwind/lore", OrgFolder: "org"}},
				Own:      []string{"github.com/northwind/*"},
				Repos:    []string{"github.com/acme/*", "github.com/globex/**", "github.com/shared/*"},
			},
			{
				Name:     "northwind-stag",
				Identity: orgconf.OrgIdentity{Key: "human:alice@northwind.example"},
				Stores:   []orgconf.Store{{URL: "git@github.com:northwind/lore", Branch: "staging", OrgFolder: "org"}},
				// staging lists the same patterns; it must never match on its own
				Own:   []string{"github.com/northwind/*"},
				Repos: []string{"github.com/acme/*"},
			},
			{
				Name:     "betalabs",
				Identity: orgconf.OrgIdentity{Key: "human:alice@betalabs.example"},
				Stores:   []orgconf.Store{{URL: "git@github.com:betalabs/lore", OrgFolder: "org"}},
				Own:      []string{"github.com/betalabs/*"},
				Repos:    []string{"github.com/shared/*"},
			},
		},
		RepoOrgs: map[string]orgconf.RepoOrg{},
	}
}

func norm(t *testing.T, raws ...string) []string {
	t.Helper()
	out := make([]string, 0, len(raws))
	for _, r := range raws {
		n, err := repourl.Normalize(r)
		if err != nil {
			t.Fatalf("normalize %q: %v", r, err)
		}
		out = append(out, n)
	}
	return out
}

func link(entry, remote, folder string) Link {
	return Link{Entry: entry, StoreURL: shStore, Folder: folder, Remote: remote}
}

func TestResolveMatrix(t *testing.T) {
	acme := "github.com/acme/app"
	type tc struct {
		name      string
		env       string
		repoOrg   *orgconf.RepoOrg
		committed *orgconf.OrgFile
		remotes   []string
		noPID     bool
		links     LinkResult
		// expectations
		org     string
		step    Step
		role    Role
		errKind Kind
		unknown bool // LinkUnknown
		disagr  bool
		warn    string // substring of some warning
	}
	known := func(ls ...Link) LinkResult { return LinkResult{Status: LinkStatusKnown, Links: ls} }
	cases := []tc{
		// ── rule 6 / nothing ───────────────────────────────────────────
		{name: "no remote, nothing set", org: "", step: StepNone, role: RoleNone},
		{name: "unrelated remote", remotes: norm(t, "git@github.com:someone/else.git"), step: StepNone, role: RoleNone},

		// ── rule 5 patterns, every remote spelling ─────────────────────
		{name: "client by ssh remote", remotes: norm(t, "git@github.com:acme/app.git"), org: "northwind", step: StepPattern, role: RoleClient},
		{name: "client by https remote", remotes: norm(t, "https://github.com/acme/app"), org: "northwind", step: StepPattern, role: RoleClient},
		{name: "own repo", remotes: norm(t, "ssh://git@github.com/northwind/tools.git"), org: "northwind", step: StepPattern, role: RoleOwn},
		{name: "* is one segment", remotes: norm(t, "git@github.com:acme/team/app"), step: StepNone, role: RoleNone},
		{name: "** any depth", remotes: norm(t, "git@github.com:globex/hub/backend"), org: "northwind", step: StepPattern, role: RoleClient},
		{name: "betalabs own", remotes: norm(t, "git@github.com:betalabs/api"), org: "betalabs", step: StepPattern, role: RoleOwn},
		{name: "two entries match → ambiguous", remotes: norm(t, "git@github.com:shared/app"), errKind: KindAmbiguous},
		{name: "two remotes, two orgs → ambiguous", remotes: norm(t, "git@github.com:acme/app", "git@github.com:betalabs/app"), errKind: KindAmbiguous},
		{name: "two remotes, one org", remotes: norm(t, "git@github.com:acme/app", "git@github.com:someone/fork"), org: "northwind", step: StepPattern, role: RoleClient},
		{name: "staging never matched by pattern", remotes: norm(t, "git@github.com:acme/app"), org: "northwind", step: StepPattern, role: RoleClient},
		{name: "pattern match, links unknown → fail-closed flag", remotes: norm(t, acme), links: LinkResult{Status: LinkStatusUnknown, Detail: "northwind/lore unreachable"}, org: "northwind", step: StepPattern, role: RoleClient, unknown: true},
		{name: "pattern match, links not checked (P1) → no flag", remotes: norm(t, acme), links: LinkResult{Status: LinkStatusNotChecked}, org: "northwind", step: StepPattern, role: RoleClient},

		// ── rule 1 LORE_ORG ────────────────────────────────────────────
		{name: "LORE_ORG unknown entry", env: "nobody", remotes: norm(t, acme), errKind: KindUnknownEntry},
		{name: "LORE_ORG picks staging", env: "northwind-stag", remotes: norm(t, acme), org: "northwind-stag", step: StepEnv, role: RoleClient},
		{name: "LORE_ORG beats ambiguity", env: "betalabs", remotes: norm(t, "git@github.com:shared/app"), org: "betalabs", step: StepEnv, role: RoleClient},
		{name: "LORE_ORG on unmatched repo → client (fail closed)", env: "betalabs", remotes: norm(t, "github.com/x/y"), org: "betalabs", step: StepEnv, role: RoleClient},
		{name: "LORE_ORG own pattern → own", env: "betalabs", remotes: norm(t, "github.com/betalabs/x"), org: "betalabs", step: StepEnv, role: RoleOwn},
		{name: "LORE_ORG disagrees with link", env: "betalabs", remotes: norm(t, acme), links: known(link("northwind", acme, "acme")), org: "betalabs", step: StepEnv, role: RoleClient, disagr: true},
		{name: "LORE_ORG staging vs main-entry link: same org, no disagreement", env: "northwind-stag", remotes: norm(t, acme), links: known(link("northwind", acme, "acme")), org: "northwind-stag", step: StepEnv, role: RoleClient},
		{name: "LORE_ORG beats repo_orgs", env: "betalabs", repoOrg: &orgconf.RepoOrg{Org: "northwind"}, remotes: norm(t, acme), org: "betalabs", step: StepEnv, role: RoleClient},

		// ── rule 2 repo_orgs ───────────────────────────────────────────
		{name: "repo_orgs short form", repoOrg: &orgconf.RepoOrg{Org: "northwind-stag"}, remotes: norm(t, acme), org: "northwind-stag", step: StepRepoOrgs, role: RoleClient},
		{name: "repo_orgs remote matches", repoOrg: &orgconf.RepoOrg{Org: "betalabs", Remotes: []string{"git@github.com:shared/app.git"}}, remotes: norm(t, "github.com/shared/app"), org: "betalabs", step: StepRepoOrgs, role: RoleClient},
		{name: "repo_orgs from upstream, fork remote → falls through + warning", repoOrg: &orgconf.RepoOrg{Org: "betalabs", Remotes: []string{"github.com/shared/app"}}, remotes: norm(t, "github.com/acme/app"), org: "northwind", step: StepPattern, role: RoleClient, warn: "fork"},
		{name: "repo_orgs but no project id", noPID: true, repoOrg: &orgconf.RepoOrg{Org: "betalabs"}, remotes: norm(t, acme), org: "northwind", step: StepPattern, role: RoleClient},
		{name: "repo_orgs resolves ambiguity", repoOrg: &orgconf.RepoOrg{Org: "northwind"}, remotes: norm(t, "github.com/shared/app"), org: "northwind", step: StepRepoOrgs, role: RoleClient},
		{name: "repo_orgs disagrees with link", repoOrg: &orgconf.RepoOrg{Org: "betalabs"}, remotes: norm(t, acme), links: known(link("northwind", acme, "acme")), org: "betalabs", step: StepRepoOrgs, disagr: true, role: RoleClient},

		// ── rule 3 committed .lore/org.yaml ────────────────────────────
		{name: "org.yaml in own repo", committed: &orgconf.OrgFile{Org: "northwind"}, remotes: norm(t, "github.com/northwind/tools"), org: "northwind", step: StepCommitted, role: RoleOwn},
		{name: "org.yaml in client repo (pattern) ignored", committed: &orgconf.OrgFile{Org: "betalabs"}, remotes: norm(t, acme), org: "northwind", step: StepPattern, role: RoleClient},
		{name: "org.yaml in own repo but linked as client → refused", committed: &orgconf.OrgFile{Org: "northwind"}, remotes: norm(t, "github.com/northwind/tools"), links: known(link("northwind", "github.com/northwind/tools", "tools")), org: "northwind", step: StepLink, role: RoleClient, warn: "CLIENT"},
		{name: "org.yaml without remote", committed: &orgconf.OrgFile{Org: "northwind"}, step: StepNone, role: RoleNone},
		{name: "org.yaml names unknown org", committed: &orgconf.OrgFile{Org: "ghost"}, remotes: norm(t, "github.com/northwind/tools"), org: "northwind", step: StepPattern, role: RoleOwn},
		{name: "org.yaml naming staging entry, own repo", committed: &orgconf.OrgFile{Org: "northwind-stag"}, remotes: norm(t, "github.com/northwind/tools"), org: "northwind-stag", step: StepCommitted, role: RoleOwn},
		{name: "org.yaml own repo, links unknown → accepted, flagged", committed: &orgconf.OrgFile{Org: "northwind"}, remotes: norm(t, "github.com/northwind/tools"), links: LinkResult{Status: LinkStatusUnknown}, org: "northwind", step: StepCommitted, role: RoleOwn, unknown: true},

		// ── rule 4 links ───────────────────────────────────────────────
		{name: "one link beats patterns", remotes: norm(t, "github.com/shared/app"), links: known(link("betalabs", "github.com/shared/app", "shared")), org: "betalabs", step: StepLink, role: RoleClient},
		{name: "two links same entry (group)", remotes: norm(t, acme), links: known(link("northwind", acme, "acme"), link("northwind", acme, "globex")), org: "northwind", step: StepLink, role: RoleClient},
		{name: "links in two entries → ambiguous", remotes: norm(t, acme), links: known(link("northwind", acme, "acme"), link("betalabs", acme, "acme")), errKind: KindAmbiguous},
		{name: "own link → own", remotes: norm(t, "github.com/other/tool"), links: known(Link{Entry: "betalabs", StoreURL: "github.com/betalabs/lore", Folder: "tool", Remote: "github.com/other/tool", Own: true}), org: "betalabs", step: StepLink, role: RoleOwn},
		{name: "staging link ignored", remotes: norm(t, "github.com/x/y"), links: known(link("northwind-stag", "github.com/x/y", "x")), step: StepNone, role: RoleNone},
		{name: "link of an unknown entry ignored", remotes: norm(t, "github.com/x/y"), links: known(link("ghost", "github.com/x/y", "x")), step: StepNone, role: RoleNone},
		{name: "link found while some store unknown: link decides, no flag", remotes: norm(t, acme), links: LinkResult{Status: LinkStatusUnknown, Links: []Link{link("northwind", acme, "acme")}}, org: "northwind", step: StepLink, role: RoleClient},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := fixture()
			if c.repoOrg != nil {
				cfg.RepoOrgs[pid] = *c.repoOrg
			}
			p := pid
			if c.noPID {
				p = ""
			}
			res, err := Resolve(Inputs{Config: cfg, EnvOrg: c.env, ProjectID: p, Remotes: c.remotes, Committed: c.committed, Links: c.links})
			if c.errKind != 0 {
				if KindOf(err) != c.errKind {
					t.Fatalf("err = %v (kind %d), want kind %d", err, KindOf(err), c.errKind)
				}
				var e *Error
				if c.errKind == KindAmbiguous {
					if !errors.As(err, &e) {
						t.Fatalf("err = %v, want *Error", err)
					}
					if len(e.Candidates) < 2 {
						t.Errorf("ambiguous error lists %v; must name every candidate", e.Candidates)
					}
				}
				if len(res.Trace) == 0 {
					t.Error("a refusal must still carry the trace for `lore explain org`")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if res.OrgName() != c.org || res.Step != c.step || res.Role != c.role {
				t.Fatalf("got org=%q step=%v role=%s; want org=%q step=%v role=%s\ntrace: %+v",
					res.OrgName(), res.Step, res.Role, c.org, c.step, c.role, res.Trace)
			}
			if res.LinkUnknown != c.unknown {
				t.Errorf("LinkUnknown = %v, want %v", res.LinkUnknown, c.unknown)
			}
			if (res.Disagreement != nil) != c.disagr {
				t.Errorf("Disagreement = %+v, want %v", res.Disagreement, c.disagr)
			}
			if c.warn != "" && !strings.Contains(strings.Join(res.Warnings, "\n"), c.warn) {
				t.Errorf("warnings %q lack %q", res.Warnings, c.warn)
			}
			// the trace always covers all six rules, in order, exactly one hit
			if len(res.Trace) != 6 {
				t.Fatalf("trace has %d rules: %+v", len(res.Trace), res.Trace)
			}
			hits := 0
			for i, tr := range res.Trace {
				if tr.Step != Step(i+1) {
					t.Errorf("trace[%d].Step = %v", i, tr.Step)
				}
				if tr.Outcome == OutcomeHit {
					hits++
					if tr.Step != res.Step {
						t.Errorf("hit at rule %d but Step = %d", tr.Step, res.Step)
					}
				}
			}
			if hits != 1 {
				t.Errorf("trace has %d hits, want 1: %+v", hits, res.Trace)
			}
		})
	}
}

func TestCheckWrite(t *testing.T) {
	acme := "github.com/acme/app"
	cfg := fixture()
	cfg.RepoOrgs[pid] = orgconf.RepoOrg{Org: "betalabs"}
	res, err := Resolve(Inputs{Config: cfg, ProjectID: pid, Remotes: []string{acme},
		Links: LinkResult{Status: LinkStatusKnown, Links: []Link{link("northwind", acme, "acme")}}})
	if err != nil {
		t.Fatal(err)
	}
	if KindOf(res.CheckWrite(false, true)) != KindDisagrees {
		t.Error("disagreement must refuse an org-scoped write")
	}
	if werr := res.CheckWrite(true, true); werr != nil {
		t.Errorf("--force-org must allow it: %v", werr)
	}

	res, err = Resolve(Inputs{Config: fixture(), Remotes: []string{acme}, Links: LinkResult{Status: LinkStatusUnknown}})
	if err != nil {
		t.Fatal(err)
	}
	if KindOf(res.CheckWrite(false, false)) != KindLinkUnknown {
		t.Error("unknown link status must refuse a write without --to (fail closed)")
	}
	if err := res.CheckWrite(false, true); err != nil {
		t.Errorf("explicit --to must be allowed: %v", err)
	}

	res, _ = Resolve(Inputs{Config: fixture(), Remotes: []string{acme}})
	if err := res.CheckWrite(false, false); err != nil {
		t.Errorf("P1 (links not checked) must not refuse: %v", err)
	}
}

func TestIsStaging(t *testing.T) {
	cfg := fixture()
	if IsStaging(cfg.FindOrg("northwind"), nil) || !IsStaging(cfg.FindOrg("northwind-stag"), nil) {
		t.Fatal("main vs staging misclassified with the built-in default")
	}
	// a store whose default branch is "trunk": main becomes the staging one
	def := func(u string) string {
		if u == shStore {
			return "staging"
		}
		return ""
	}
	if !IsStaging(cfg.FindOrg("northwind"), def) || IsStaging(cfg.FindOrg("northwind-stag"), def) {
		t.Fatal("store default branch seam ignored")
	}
	if IsStaging(&orgconf.Org{Name: "nostores"}, nil) {
		t.Fatal("an entry without stores is not staging")
	}
}

func TestStaticLinksMatchesIDAndRemote(t *testing.T) {
	src := StaticLinks{Status: LinkStatusKnown, All: map[string][]Link{
		pid: {link("northwind", "github.com/acme/app", "acme")},
	}}
	res, _ := src.Links(context.Background(), pid, []string{"github.com/fork/app"})
	if len(res.Links) != 0 {
		t.Fatal("a fork with the same project id must not be linked (F11)")
	}
	res, _ = src.Links(context.Background(), pid, []string{"github.com/acme/app"})
	if len(res.Links) != 1 {
		t.Fatal("matching id and remote must be linked")
	}
	nl, _ := NoLinks{}.Links(context.Background(), pid, nil)
	if nl.Status != LinkStatusNotChecked || nl.Detail == "" {
		t.Fatal("NoLinks must say it did not check, with a reason")
	}
}

// whoami prints MatchedBy; for rule 5 it must name the pattern that matched
// (spec F10 example: "matched by: own pattern github.com/betalabs/*"), and the
// client pattern wins over the own pattern when both match (fail closed).
func TestPatternMatchedByNamesThePattern(t *testing.T) {
	cases := []struct {
		remote, want string
	}{
		{"github.com/betalabs/api", "own pattern github.com/betalabs/* of betalabs"},
		{"github.com/acme/app", "repos pattern github.com/acme/* of northwind"},
		{"github.com/globex/x/y", "repos pattern github.com/globex/** of northwind"},
	}
	for _, c := range cases {
		res, err := Resolve(Inputs{Config: fixture(), Remotes: norm(t, c.remote)})
		if err != nil {
			t.Fatalf("%s: %v", c.remote, err)
		}
		if res.Step != StepPattern || res.MatchedBy != c.want {
			t.Errorf("%s: step=%v matched by %q, want %q", c.remote, res.Step, res.MatchedBy, c.want)
		}
	}
}

// Staging links (cookbook 13): never used to match, but attached when the
// staging entry is chosen on purpose, so `--to <staging entry>` finds the
// folder linked on the staging branch.
func TestStagingLinksOnlyWhenChosenOnPurpose(t *testing.T) {
	acmeRemote := norm(t, "github.com/acme/app")
	known := func(ls ...Link) LinkResult { return LinkResult{Status: LinkStatusKnown, Links: ls} }
	stagingLink := link("northwind-stag", acmeRemote[0], "acme")
	mainLink := link("northwind", acmeRemote[0], "acme")

	// chosen on purpose → its own link only, not the main entry's
	res, err := Resolve(Inputs{Config: fixture(), EnvOrg: "northwind-stag", ProjectID: pid, Remotes: acmeRemote,
		Links: known(stagingLink, mainLink)})
	if err != nil || res.OrgName() != "northwind-stag" || len(res.Links) != 1 || res.Links[0].Entry != "northwind-stag" {
		t.Fatalf("LORE_ORG staging: %+v, %v", res.Links, err)
	}
	// not chosen → the staging link never decides and is not attached
	res, err = Resolve(Inputs{Config: fixture(), ProjectID: pid, Remotes: acmeRemote, Links: known(stagingLink)})
	if err != nil || res.OrgName() != "northwind" || res.Step != StepPattern {
		t.Fatalf("staging link matched: org=%s step=%v, %v", res.OrgName(), res.Step, err)
	}
	for _, l := range res.Links {
		if l.Entry == "northwind-stag" {
			t.Fatalf("staging link attached to the main entry: %+v", res.Links)
		}
	}
}

// TestDisagreesHintNamesLiveFlags: E_ORG_DISAGREES' hint and warning name
// --to <org> (what the write commands accept), never the removed
// --force-org.
func TestDisagreesHintNamesLiveFlags(t *testing.T) {
	r := Result{Disagreement: &Disagreement{Chosen: "freelance", Linked: []string{"agency"}, Message: "LORE_ORG chose freelance, but this repo is linked in agency"}}
	err := r.CheckWrite(false, true)
	var oe *Error
	if !errors.As(err, &oe) || oe.Kind != KindDisagrees {
		t.Fatalf("want KindDisagrees, got %v", err)
	}
	if strings.Contains(oe.Hint, "force-org") || !strings.Contains(oe.Hint, "--to <org>") {
		t.Fatalf("hint %q", oe.Hint)
	}
}
