package orgresolve

import (
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/orgconf"
)

// TestSetupHint is the ONBOARDING_DESIGN 3.3 org matrix: the setup's org
// fills in only when no F10 rule names one (Q4), and any rule naming
// another org is a mismatch that refuses writes (CheckWrite and
// CheckSetupWrite), never a silent override.
func TestSetupHint(t *testing.T) {
	nwHint := SetupHint{Name: "agency", Org: "northwind", Why: "folder ~/www/projects/agency/**"}
	cases := []struct {
		name      string
		env       string
		repoOrg   string
		committed *orgconf.OrgFile
		remotes   []string
		links     LinkResult
		hint      SetupHint
		org       string
		step      Step
		mismatch  string // the source named in the refusal, "" = none
	}{
		{name: "no hint, no org: unchanged", remotes: []string{"github.com/mira/app"}, step: StepNone},
		{name: "no-org repo takes the setup org (Q4)", remotes: []string{"github.com/mira/app"}, hint: nwHint, org: "northwind", step: StepSetup},
		{name: "repo with no remote takes the setup org", hint: nwHint, org: "northwind", step: StepSetup},
		{name: "same org from a pattern: no mismatch", remotes: []string{"github.com/northwind/app"}, hint: nwHint, org: "northwind", step: StepPattern},
		{name: "LORE_ORG names another org", env: "betalabs", remotes: []string{"github.com/mira/app"}, hint: nwHint, org: "betalabs", step: StepEnv, mismatch: "LORE_ORG"},
		{name: "lore org use names another org", repoOrg: "betalabs", remotes: []string{"github.com/mira/app"}, hint: nwHint, org: "betalabs", step: StepRepoOrgs, mismatch: "lore org use"},
		{name: "committed org.yaml names another org", committed: &orgconf.OrgFile{Org: "betalabs"}, remotes: []string{"github.com/betalabs/site"}, hint: nwHint, org: "betalabs", step: StepCommitted, mismatch: ".lore/org.yaml"},
		{name: "a pattern names another org (fail closed)", remotes: []string{"github.com/betalabs/site"}, hint: nwHint, org: "betalabs", step: StepPattern, mismatch: "repo pattern"},
		{name: "a store link names another org", remotes: []string{"github.com/mira/app"}, links: LinkResult{Status: LinkStatusKnown, Links: []Link{{Entry: "betalabs", StoreURL: "github.com/betalabs/lore", Folder: "mira", Remote: "github.com/mira/app"}}},
			hint: nwHint, org: "betalabs", step: StepLink, mismatch: "store link"},
		{name: "staging entry of the same org: no mismatch", env: "northwind-stag", remotes: []string{"github.com/mira/app"}, hint: nwHint, org: "northwind-stag", step: StepEnv},
		{name: "hint names no entry: ignored", remotes: []string{"github.com/mira/app"}, hint: SetupHint{Name: "x", Org: "gone", Why: "default"}, step: StepNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fixture()
			if tc.repoOrg != "" {
				cfg.RepoOrgs[pid] = orgconf.RepoOrg{Org: tc.repoOrg}
			}
			links := tc.links
			if links.Status == LinkStatusNotChecked {
				links.Status = LinkStatusKnown
			}
			r, err := Resolve(Inputs{Config: cfg, EnvOrg: tc.env, ProjectID: pid, Remotes: tc.remotes, Committed: tc.committed, Links: links, Setup: tc.hint})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if r.OrgName() != tc.org || r.Step != tc.step {
				t.Fatalf("org %q step %v, want %q %v (trace %+v)", r.OrgName(), r.Step, tc.org, tc.step, r.Trace)
			}
			werr := r.CheckSetupWrite()
			if tc.mismatch == "" {
				if werr != nil || r.SetupMismatch != nil {
					t.Fatalf("unexpected mismatch: %v", werr)
				}
				return
			}
			if KindOf(werr) != KindSetupMismatch {
				t.Fatalf("want KindSetupMismatch, got %v", werr)
			}
			if cerr := r.CheckWrite(true, true); KindOf(cerr) != KindSetupMismatch {
				t.Fatalf("CheckWrite must refuse too (even forced), got %v", cerr)
			}
			msg := werr.Error()
			for _, want := range []string{"this repo is org " + tc.org, "(from " + tc.mismatch + ")", "setup agency (folder ~/www/projects/agency/**) is org northwind"} {
				if !strings.Contains(msg, want) {
					t.Fatalf("message %q lacks %q", msg, want)
				}
			}
		})
	}
}

// TestSetupHintTraceOnlyWhenSet: without a setup the trace is the F10
// trace of before (whoami --explain output unchanged).
func TestSetupHintTraceOnlyWhenSet(t *testing.T) {
	r, err := Resolve(Inputs{Config: fixture(), Remotes: []string{"github.com/mira/app"}, Links: LinkResult{Status: LinkStatusKnown}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range r.Trace {
		if tr.Step == StepSetup {
			t.Fatalf("setup step traced with no setup: %+v", r.Trace)
		}
	}
	if len(r.Trace) != 6 {
		t.Fatalf("trace has %d lines, want the 6 F10 rules", len(r.Trace))
	}
}
