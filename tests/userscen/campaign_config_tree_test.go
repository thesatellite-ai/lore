//go:build userscen

package userscen

// campaign_config_tree_test.go — cross-cutting sweeps over EVERY leaf
// command of the tree (discovered from `lore --help`, so a command added
// later is covered without touching this file):
//
//   - every leaf's --help starts with a one-line description and has an
//     Examples block with at least one line that runs lore (a command
//     without an example is a doc bug: the user has nothing to copy)
//   - every leaf that needs an argument or a required flag, run bare in an
//     initialised repo, is a typed refusal: a documented non-zero exit
//     (never 1), `ERROR: [E_…]` and a hint line naming the next step, no
//     Go stack; and with --json the error object carries the same code
//     and a hint
//   - every leaf, given an unknown flag inside an initialised repo, after
//     a positional too, exits 20 with the FLAG2 line and no stack
//   - the shared flags --db, --project, --repo, --read-only and --json on
//     every noun's list (and --read-only on every add): each is either
//     refused by name with its own typed error and hint, or accepted the
//     way the flag's help says

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// ccInteractive are leaves a bare call would start a server, open a
// browser, replace the binary or wait on a person (each has its own
// scenario); the missing-argument sweep leaves them out.
var ccInteractive = [][]string{{"setup"}, {"onboard"}, {"tui"}, {"api", "serve"}, {"upgrade"}}

// ccBareClears are leaves whose required argument may be empty on
// purpose, so a bare call is not refused for the missing argument:
// `lore machine label ""` clears the label and the decoder cannot tell a
// missing argument from "" (ops/infra MachineLabelIn, G-ORGMIG-9). A bare
// call still refuses, here for the missing own store.
var ccBareClears = [][]string{{"machine", "label"}}

func ccHasPrefix(path []string, set [][]string) bool {
	for _, s := range set {
		if len(path) >= len(s) && slices.Equal(path[:len(s)], s) {
			return true
		}
	}
	return false
}

// TestCampaignConfig_HelpExamples: every leaf command has a description
// and at least one example line that runs lore.
func TestCampaignConfig_HelpExamples(t *testing.T) {
	w := newWorld(t)
	_, outside := cliActors(t, w)
	leaves := ccLeaves(t, cliWalk(t, outside))
	t.Logf("%d leaf commands", len(leaves))
	var noExample []string
	for _, n := range leaves {
		if d := ccDescription(n.Help); d == "" || strings.HasPrefix(d, "Usage:") {
			t.Errorf("%s --help: no description line before Usage:\n%s", n.name(), n.Help)
		}
		if len(ccExampleLines(n.Help)) == 0 {
			noExample = append(noExample, n.name())
		}
	}
	if len(noExample) > 0 {
		t.Errorf("%d leaf commands have no example line in --help:\n  %s", len(noExample), strings.Join(noExample, "\n  "))
	}
}

// TestCampaignConfig_MissingArgument: a bare call of every leaf that
// needs input is a typed refusal with a next step, as text and as JSON.
func TestCampaignConfig_MissingArgument(t *testing.T) {
	w := newWorld(t)
	actors, outside := cliActors(t, w)
	leaves := ccLeaves(t, cliWalk(t, outside))
	repos := cliInitRepos(t, w, actors)
	var picked []cliNode
	var jobs [][]string
	for _, n := range leaves {
		if !ccRequired(n) || ccHasPrefix(n.Path, ccInteractive) {
			continue
		}
		picked = append(picked, n)
		jobs = append(jobs, slices.Clone(n.Path))
		if n.JSON {
			jobs = append(jobs, append(slices.Clone(n.Path), "--json"))
		}
	}
	if len(picked) < 250 {
		t.Fatalf("only %d leaves need input: the usage parser is broken", len(picked))
	}
	t.Logf("%d leaves run bare (%d runs)", len(picked), len(jobs))
	for _, r := range cliRunMany(t, repos, jobs) {
		path := r.Args
		asJSON := slices.Contains(path, "--json")
		if asJSON {
			path = path[:len(path)-1]
		}
		if ccHasPrefix(path, ccBareClears) {
			// Refused, but not for the argument (see ccBareClears).
			if r.Code == 0 || r.Code == 1 {
				t.Errorf("bare call must still refuse: %s", r)
			}
			ccNoStack(t, r)
			continue
		}
		if !asJSON {
			ccTypedRefusal(t, r, true)
			continue
		}
		ccNoStack(t, r)
		cliCheckJSON(t, r)
		raw, err := cliOneJSON(r.Stdout)
		if err != nil {
			continue // reported by cliCheckJSON
		}
		var doc cliErrorDoc
		if json.Unmarshal(raw, &doc) != nil || doc.Error == nil || doc.Error.Hint == "" {
			t.Errorf("bare call with --json: want the error object with a hint: %s", r)
		}
	}
}

// TestCampaignConfig_UnknownFlagInRepo: inside an initialised repo (where
// a command gets past opening the store) and after a positional, an
// unknown flag is still the FLAG2 refusal, exit 20, no stack: the usage
// line on stderr, or with --json the error object on stdout.
func TestCampaignConfig_UnknownFlagInRepo(t *testing.T) {
	w := newWorld(t)
	actors, outside := cliActors(t, w)
	leaves := ccLeaves(t, cliWalk(t, outside))
	repos := cliInitRepos(t, w, actors)
	// Bare: the text refusal on stderr. After a positional and with
	// --json: the same refusal as the error object on stdout (9efcfbc3).
	var jobs [][]string
	var paths [][]string
	var asJSON []bool
	for _, n := range leaves {
		jobs = append(jobs, append(slices.Clone(n.Path), cliNoFlag))
		paths, asJSON = append(paths, n.Path), append(asJSON, false)
		if ccRequired(n) {
			jobs = append(jobs, append(slices.Clone(n.Path), "zz_value", cliNoFlag, "--json"))
			paths, asJSON = append(paths, n.Path), append(asJSON, true)
		}
	}
	for i, r := range cliRunMany(t, repos, jobs) {
		ccNoStack(t, r)
		if asJSON[i] {
			if !cliFlagRefusedJSON(r, paths[i], cliNoFlag) {
				t.Errorf("unknown flag in a repo with --json: want exit %d and the error object naming %s on stdout, nothing on stderr: %s", cliExitUsage, cliNoFlag, r)
			}
			continue
		}
		if want := cliFlagRefusal(paths[i], cliNoFlag); r.Code != cliExitUsage || r.Stderr != want || r.Stdout != "" {
			t.Errorf("unknown flag in a repo: want exit %d and exactly %q: %s", cliExitUsage, want, r)
		}
	}
}

// ccFlagHelp is the help line of flag in a command's Flags block ("" when
// the command does not take it).
func ccFlagHelp(n cliNode, flag string) string {
	for _, l := range cliBlock(n.Help, "Flags:") {
		f := strings.Fields(l)
		if slices.Contains(f, flag) {
			return l
		}
	}
	return ""
}

// ccReadOnlyMode is the help wording of the shared --read-only flag.
const ccReadOnlyMode = "LORE_READ_ONLY=1"

// ccAccepted is the help wording of a flag kept only so old scripts still
// parse ("accepted for v0.1.17 scripts; …"): such a flag changes nothing.
const ccAccepted = "accepted for v0.1.17"

// TestCampaignConfig_SharedFlags: --db, --project, --repo, --read-only and
// --json on every noun's list, and --read-only on every add.
func TestCampaignConfig_SharedFlags(t *testing.T) {
	w := newWorld(t)
	actors, outside := cliActors(t, w)
	nodes := cliWalk(t, outside)
	repos := cliInitRepos(t, w, actors)
	type job struct {
		node cliNode
		flag string
		args []string
	}
	var plan []job
	for _, n := range ccLeaves(t, nodes) {
		if len(n.Path) != 2 || cliSkipped(n.Path) || (n.Path[1] == "list" && ccRequired(n)) {
			continue // a list that needs a kind or a name is in the missing-argument sweep
		}
		switch n.Path[1] {
		case "list":
			plan = append(plan,
				job{n, "--db", append(slices.Clone(n.Path), "--db", "old.db")},
				job{n, "--project", append(slices.Clone(n.Path), "--project", "zz-no-project")},
				job{n, "--repo", append(slices.Clone(n.Path), "--repo", "zz-no-repo")},
				job{n, "--read-only", append(slices.Clone(n.Path), "--read-only")},
				job{n, "--json", append(slices.Clone(n.Path), "--json")})
		case "add":
			// The shared flag only: `store add --read-only` is a store's own
			// setting (refuse writes to that store), not read-only mode.
			if strings.Contains(ccFlagHelp(n, "--read-only"), ccReadOnlyMode) {
				plan = append(plan, job{n, "--read-only", append(slices.Clone(n.Path), "--read-only")})
			}
		}
	}
	if len(plan) < 150 {
		t.Fatalf("only %d shared-flag runs planned: the tree walk is broken", len(plan))
	}
	before := make([]string, len(repos))
	for i, c := range repos {
		before[i] = ccTreeHash(t, c.Dir+"/.lore/data")
	}
	jobs := make([][]string, len(plan))
	for i, p := range plan {
		jobs[i] = p.args
	}
	for i, r := range cliRunMany(t, repos, jobs) {
		p := plan[i]
		ccSharedFlag(t, p.node, p.flag, r)
	}
	for i, c := range repos {
		if got := ccTreeHash(t, c.Dir+"/.lore/data"); got != before[i] {
			t.Errorf("%s: the shared-flag runs (reads, refusals and --read-only adds) changed .lore/data", c.Dir)
		}
	}
}

// ccSharedFlag holds one run to what its flag's help line promises.
func ccSharedFlag(t *testing.T, n cliNode, flag string, r cliProc) {
	t.Helper()
	ccNoStack(t, r)
	help := ccFlagHelp(n, flag)
	isAdd := n.Path[len(n.Path)-1] == "add"
	switch {
	case help == "":
		if r.Code != cliExitUsage || !strings.Contains(r.Stderr, "unknown flag: "+flag) {
			t.Errorf("%s does not list %s, so it must refuse it as unknown: %s", n.name(), flag, r)
		}
	case flag == "--json":
		cliCheckJSON(t, r)
		if r.Code != 0 {
			t.Errorf("%s --json on an empty repo must succeed: %s", n.name(), r)
		}
	case flag == "--read-only" && isAdd:
		ccTypedRefusal(t, r, true)
		if !strings.HasPrefix(r.Stderr, "ERROR: [E_READ_ONLY]") || r.Code != 23 {
			t.Errorf("%s --read-only: want E_READ_ONLY exit 23: %s", n.name(), r)
		}
	case flag == "--read-only":
		if r.Code != 0 || strings.Contains(r.Stderr, "ERROR:") {
			t.Errorf("%s --read-only is a read and must succeed: %s", n.name(), r)
		}
	case flag == "--db" && strings.Contains(help, "(refused)"):
		ccTypedRefusal(t, r, true)
		if !strings.HasPrefix(r.Stderr, "ERROR: [E_UNSUPPORTED] --db") {
			t.Errorf("%s --db: its help says refused, want E_UNSUPPORTED naming --db: %s", n.name(), r)
		}
	case strings.Contains(help, ccAccepted):
		if r.Code != 0 {
			t.Errorf("%s %s: its help says accepted for old scripts, so it must not refuse: %s", n.name(), flag, r)
		}
	case flag == "--project" || flag == "--repo":
		noun := strings.TrimPrefix(flag, "--")
		ccTypedRefusal(t, r, true)
		if !strings.HasPrefix(r.Stderr, "ERROR: [E_NOT_FOUND]") || !strings.Contains(r.Stderr, "lore "+noun+" list") {
			t.Errorf("%s %s with an unknown name: want E_NOT_FOUND and the hint `lore %s list`: %s", n.name(), flag, noun, r)
		}
	default:
		if r.Code == 1 || r.TimedOut {
			t.Errorf("%s %s: %s", n.name(), flag, r)
		}
	}
}
