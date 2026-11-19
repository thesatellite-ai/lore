package doctor

import (
	"context"
	"strings"
	"testing"
)

// fleetEnv adds the K-10 upgrade: the repos of the setups' folders.
type fleetEnv struct {
	*setupEnv
	fleet Fleet
}

func (e fleetEnv) DoctorFleet(context.Context) (Fleet, error) { return e.fleet, nil }

// TestAllNamesEveryRepoFound (K-10): doctor --all checked one repo and
// was silent about the 0.1.17-format repo and the repos with no lore data
// in the same folders. It now names the 0.1.17-format repo with its next
// step and a repo with lore data the checks did not cover, counts the
// repos without lore data, and lists each one with --verbose; the block
// never changes the status.
func TestAllNamesEveryRepoFound(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = append(env.store.cfg.Setups, personal(env.home))
	env.top = env.home + "/www/mira/app"
	old := env.home + "/www/mira/old"
	unseen := env.home + "/www/mira/unseen"
	fe := fleetEnv{setupEnv: env, fleet: Fleet{
		Repos: []FleetRepo{
			{Path: env.top},
			{Path: old, FormatOld: true, Next: "to upgrade it here: cd " + old + " && lore fix --migrate"},
			{Path: unseen},
		},
		NoLore: []string{env.home + "/www/mira/jeoga", env.home + "/www/mira/lath"},
	}}
	plain := runAllT(t, env, RunIn{})
	r := runAllT(t, fe, RunIn{})
	if r.Status != plain.Status {
		t.Fatalf("the repos block changed the status: %s, without it %s", r.Status, plain.Status)
	}
	text := r.Text()
	for _, want := range []string{
		"repos in the setups' folders: 3 with lore data (1 checked above), 1 on the lore 0.1.17 format, 2 without lore data",
		old + "  on the lore 0.1.17 format; to upgrade it here: cd " + old + " && lore fix --migrate",
		unseen + "  " + fleetNotChecked,
		"2 repo(s) without lore data, left as they are (lore doctor --all --verbose lists them)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "lath  no lore data") || strings.Contains(text, env.top+"  checked") {
		t.Fatalf("without --verbose the checked repo or a repo without lore data is listed:\n%s", text)
	}
	if !r.Found.Repos[0].Checked || r.Found.Repos[1].Checked || r.Found.Repos[2].Checked {
		t.Fatalf("checked marks %+v", r.Found.Repos)
	}
	verbose := runAllT(t, fe, RunIn{Verbose: true}).Text()
	for _, want := range []string{env.top + "  checked", env.home + "/www/mira/lath  no lore data, left as it is"} {
		if !strings.Contains(verbose, want) {
			t.Fatalf("--verbose text lacks %q:\n%s", want, verbose)
		}
	}
}

// TestAllFleetProblemDegrades (devco:O-19): a repo of the setups' folders
// whose origin is gone is named in the repos block with its step, counts
// as a warning and makes doctor --all DEGRADED (exit 1); before, doctor
// --all said healthy. A repo reached through a link (devco:O-1) is named
// with the link under --verbose.
func TestAllFleetProblemDegrades(t *testing.T) {
	env := newEnv(t)
	env.store.cfg.Setups = append(env.store.cfg.Setups, personal(env.home))
	env.top = env.home + "/www/mira/app"
	linked := env.home + "/elsewhere/kit"
	problem := "origin: no git repository at /srv/git/app-gone.git (moved or deleted); lore sync cannot fetch or push this repo's notes until it is fixed: if the repo moved, run git remote set-url origin <new-url>, then lore sync"
	plain := runAllT(t, env, RunIn{})
	fe := fleetEnv{setupEnv: env, fleet: Fleet{Repos: []FleetRepo{{Path: env.top, Problem: problem}}}}
	r := runAllT(t, fe, RunIn{})
	if r.Status != StatusDegraded || r.Exit != 1 {
		t.Fatalf("status %s exit %d (without the problem %s)\n%s", r.Status, r.Exit, plain.Status, r.Text())
	}
	text := r.Text()
	if !strings.Contains(text, env.top+"  "+problem) || strings.Contains(text, "This repo, warnings") {
		t.Fatalf("text:\n%s", text)
	}
	fe.fleet = Fleet{Repos: []FleetRepo{{Path: env.top}, {Path: linked, Link: env.home + "/www/mira/kit"}}}
	verbose := runAllT(t, fe, RunIn{Verbose: true}).Text()
	if !strings.Contains(verbose, linked+"  "+fleetNotChecked) {
		t.Fatalf("linked repo line:\n%s", verbose)
	}
}
