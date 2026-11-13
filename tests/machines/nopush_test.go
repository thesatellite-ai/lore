//go:build machines && !windows

package machines

// nopush_test.go — S-1, no push without asking: a real machine whose code
// repos and stores all have a bare remote here. The person has said
// "don't push for me" the two ways lore offers (push=manual on every
// store, live.autopush false), so only a command that is itself a push
// (`lore sync`, `lore sync land`, `lore store push`, `lore migrate run`)
// may move a remote ref. Every command of the generic sweep (sweep_cmds.go,
// which TestSweepCoversEveryCommand keeps complete against lore's own
// help, so no hand list here) runs in a code repo and inside a store
// clone; after each run every ref of every remote must be byte-identical.
// Then the stores go back to the defaults (push=now, autopush on) with
// commits waiting, and every read runs again: a read never pushes what
// waits, whatever the push mode (docs/sync/store-clones.md). A second
// test runs every example of every command (each leaf command's --help,
// built from the operation registry) the same way. The only writes that
// may push are the org-store writes owner decision D18 keeps
// (npOrgWritesPushAtOnce).

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// npAsked are the leaf commands that are a push the person asked for.
// live restore's whole job is putting the live rows back on the remote
// ("Put the live rows back on the remote from the local copy"), so it
// pushes; live compact pushes only a rewrite it makes, never when there
// is nothing to compact, so it stays under the rule here.
var npAsked = []string{"sync run", "sync land", "land run", "store push", "migrate run", "live restore"}

// npOrgWritesPushAtOnce are the commands whose write goes to an org
// store's roster or settings and is pushed at once whatever the push
// mode: owner decision D18 (2026-11-13) keeps "the org-store write push"
// (with claims, releases, holds and the handoff branch push) because a
// teammate must see it before acting on it. Each is still a write the
// person ran; reads and every other command stay under the rule.
var npOrgWritesPushAtOnce = map[string]string{
	"role add":          "org roster write (D18)",
	"role edit":         "org roster write (D18)",
	"contact-type add":  "org roster write (D18)",
	"contact-type edit": "org roster write (D18)",
	"store folder add":  "org store layout write (D18)",
	"store set":         "org store settings, lore-store.yaml (D18)",
}

// npReads are the leaf commands the operation registry marks read, from
// docs/reference/cli-commands.md (written from the registry by `lore spec
// docs`, which fails the gate with --check when it is stale).
var npReads = sync.OnceValues(func() (map[string]bool, error) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "cli-commands.md"))
	if err != nil {
		return nil, err
	}
	reads := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "| `lore ") {
			continue
		}
		cells := strings.Split(line, " | ")
		if len(cells) < 5 {
			continue
		}
		var path []string
		for _, w := range strings.Fields(strings.Trim(strings.TrimPrefix(cells[0], "| "), "`"))[1:] {
			if strings.ContainsAny(w[:1], "[<-") {
				break
			}
			path = append(path, w)
		}
		if strings.TrimSpace(cells[4]) == "read" {
			reads[strings.Join(path, " ")] = true
		}
	}
	if len(reads) < 100 {
		return nil, fmt.Errorf("only %d read commands in cli-commands.md: the table changed shape", len(reads))
	}
	return reads, nil
})

// npRefs is every ref of a bare repo with its object id.
func npRefs(t testing.TB, bare string) map[string]string {
	t.Helper()
	out, err := exec.Command("/usr/bin/git", "--git-dir", bare, "for-each-ref", "--format=%(refname) %(objectname)").Output()
	if err != nil {
		t.Fatalf("git for-each-ref %s: %v", bare, err)
	}
	refs := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name, oid, ok := strings.Cut(l, " "); ok {
			refs[name] = oid
		}
	}
	return refs
}

// npRemotes snapshots every remote, by name.
type npRemotes map[string]string // name → bare dir

func (r npRemotes) snap(t testing.TB) map[string]map[string]string {
	t.Helper()
	all := map[string]map[string]string{}
	for name, bare := range r {
		all[name] = npRefs(t, bare)
	}
	return all
}

// npMoved lists "<remote> <ref> <old>→<new>" for every ref that differs.
func npMoved(before, after map[string]map[string]string) []string {
	var moved []string
	for name, refs := range after {
		for ref, oid := range refs {
			if was := before[name][ref]; was != oid {
				moved = append(moved, name+" "+ref+" "+npShort(was)+"→"+npShort(oid))
			}
		}
		for ref, was := range before[name] {
			if _, ok := refs[ref]; !ok {
				moved = append(moved, name+" "+ref+" "+npShort(was)+"→deleted")
			}
		}
	}
	slices.Sort(moved)
	return moved
}

func npShort(oid string) string {
	if oid == "" {
		return "none"
	}
	return oid[:min(8, len(oid))]
}

// npOrigin gives repo a bare origin here with its history pushed.
func npOrigin(t *testing.T, m *Machine, repo, name string) string {
	t.Helper()
	bare := spBare(t, m, name, "git@github.com:alpha-dev/"+name+".git")
	for _, args := range [][]string{
		{"commit", "-q", "--allow-empty", "-m", "start"},
		{"remote", "add", "origin", bare},
		{"push", "-q", "origin", "HEAD:refs/heads/main"},
	} {
		c := exec.Command("/usr/bin/git", append([]string{"-C", repo}, args...)...)
		c.Env = m.env()
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v %s", args, repo, err, out)
		}
	}
	return bare
}

// npCloneDir is the store clone of url as `lore store show --json` names it.
func npCloneDir(t *testing.T, m *Machine, cwd, url string) string {
	t.Helper()
	out := lsRun(t, m, cwd, "store", "show")
	var l struct {
		Stores []struct {
			URL   string `json:"url"`
			Clone string `json:"clone"`
		} `json:"stores"`
	}
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		t.Fatalf("store list: %v: %s", err, out)
	}
	for _, c := range l.Stores {
		if c.URL == url && c.Clone != "" {
			return c.Clone
		}
	}
	t.Fatalf("store show names no clone of %s: %s", url, out)
	return ""
}

// npDir fails the test unless dir is a folder; it returns dir.
func npDir(t *testing.T, dir string) string {
	t.Helper()
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("no store clone at %s: %v", dir, err)
	}
	return dir
}

// npPlace is a folder the sweep's repo steps run in.
type npPlace struct{ name, dir string }

// npSweep runs every chain of groups once per place and fails each run
// of a command the person did not ask to push that moved a remote ref.
// readsOnly checks only the commands the registry marks read (the
// defaults: a write pushes as its store's push mode says).
func npSweep(t *testing.T, m *Machine, remotes npRemotes, places []npPlace, groups []string, when string, readsOnly bool) {
	t.Helper()
	reads, err := npReads()
	if err != nil {
		t.Fatal(err)
	}
	before := remotes.snap(t)
	for _, p := range places {
		for _, g := range groups {
			for _, c := range sweepChains(g) {
				var id, prev string
				for _, s := range c.Steps {
					dirs := []string{s.In}
					if s.In == inBoth {
						dirs = []string{inHome, inRepo}
					}
					for _, d := range dirs {
						cwd := m.Home
						if d != inHome {
							cwd = p.dir
						}
						args := strings.Fields(s.Cmd)
						for _, a := range s.Args {
							args = append(args, strings.NewReplacer(varID, id, varPrev, prev).Replace(a))
						}
						if !s.NoJSON {
							args = append(args, "--json")
						}
						r := m.Lore(t, cwd, args...)
						if s.Save && r.Code == 0 {
							var out struct {
								ID   string `json:"id"`
								Data struct {
									ID string `json:"id"`
								} `json:"data"`
							}
							_ = json.Unmarshal([]byte(r.Stdout), &out)
							prev = id
							if id = out.ID; id == "" {
								id = out.Data.ID
							}
						}
						after := remotes.snap(t)
						_, d18 := npOrgWritesPushAtOnce[s.Cmd]
						checked := !slices.Contains(npAsked, s.Cmd) && !d18 && (!readsOnly || reads[s.Cmd])
						if moved := npMoved(before, after); len(moved) > 0 && checked {
							t.Errorf("[%s/%s] `lore %s` (in %s) pushed without being asked: %v\n%s", when, c.Name, strings.Join(args, " "), p.name, moved, r)
						}
						before = after
					}
				}
			}
		}
	}
}

// npSettings fingerprints every file that holds a push setting: the
// machine's config.yaml, each store clone's lore-store.yaml and each
// place's .lore/stores.yaml.
func npSettings(t *testing.T, m *Machine, places []npPlace) string {
	t.Helper()
	files := []string{m.configPath()}
	_ = filepath.WalkDir(filepath.Join(m.Home, ".lore", "stores"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "lore-store.yaml" {
			files = append(files, p)
		}
		return nil
	})
	for _, p := range places {
		files = append(files, filepath.Join(p.dir, ".lore", "stores.yaml"))
	}
	var b strings.Builder
	for _, f := range files {
		body, _ := os.ReadFile(f)
		b.WriteString(f + "\x00" + string(body) + "\x00")
	}
	return b.String()
}

// npHold is the person's "push only when I sync": push=manual on both
// stores and live.autopush off (a store an example removed is skipped).
func npHold(t *testing.T, m *Machine, app string) {
	t.Helper()
	for _, s := range []string{"me", spOrg} {
		m.Lore(t, app, "store", "set", s, "push=manual", "--json")
	}
	m.Lore(t, app, "config", "set", "live.autopush", "false", "--json")
}

// npMachine is the S-1 machine: an admin of a team whose team store and
// personal store are git stores on bare remotes here, two code repos with
// a bare origin each, a first row in each store pushed by lore sync, and
// the person's "push only when I sync" (push=manual, live.autopush false).
// It returns the remotes and the folders the sweeps run in.
func npMachine(t *testing.T) (*Machine, npRemotes, []npPlace) {
	t.Helper()
	m := Build(t, ShapeFresh, loreBin)
	lsGHHosts(t, m)
	app := m.Repos[RepoWorkApp]
	remotes := npRemotes{
		"team":               spBare(t, m, "team", lsTeamURL),
		"me":                 spBare(t, m, "me", spMeURL),
		"origin/" + RepoLore: npOrigin(t, m, m.Repos[RepoLore], "has-lore"),
		"origin/app":         npOrigin(t, m, app, "app"),
	}
	lsCreate(t, m, spSetup, kindAdmin, "boss@example.test", []string{lsFolder}, false, lsTeam{storeURL: lsTeamURL}, lsAlpha)
	lsRun(t, m, app, "store", "init", "me", spMeURL, "--yes")
	spMemAdd(t, m, app, "me", "a first fact in me")
	spMemAdd(t, m, app, spOrg, "a first fact in the team store")
	lsRun(t, m, app, "sync")
	npHold(t, m, app)
	for _, s := range []string{"me", spOrg} {
		if v := spShow(t, m, app, s); v.Push != "manual" {
			t.Fatalf("store %s: push %s after store set push=manual", s, v.Push)
		}
	}
	team := npCloneDir(t, m, app, lsTeamURL)
	return m, remotes, []npPlace{
		{npPlaceNames[0], m.Repos[RepoLore]},
		{npPlaceNames[1], app},
		{npPlaceNames[2], team},
		// store show lists org stores; the personal clone sits beside them
		{npPlaceNames[3], npDir(t, filepath.Join(filepath.Dir(filepath.Dir(team)), "alpha-dev", "lore-me"))},
	}
}

// npPlaceNames name npMachine's places, in its order: each subtest of
// the S-1 tests builds its own machine and sweeps one place, so the four
// run side by side.
var npPlaceNames = []string{"code-repo-has-lore", "code-repo-work-app", "team-store-clone", "personal-store-clone"}

// TestNoPushWithoutAsking is S-1 over the sweep table (see the file
// comment), then the reads again on the defaults with commits waiting.
func TestNoPushWithoutAsking(t *testing.T) {
	t.Parallel()
	for i, name := range npPlaceNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m, remotes, places := npMachine(t)
			app, here := m.Repos[RepoWorkApp], places[i:i+1]
			npSweep(t, m, remotes, here, []string{groupKnowledge, groupRows, groupTasks, groupReads}, "push=manual, autopush off", false)

			// a write in each store while push is manual: it waits for lore sync
			for _, st := range []string{"me", spOrg} {
				spMemAdd(t, m, app, st, "a fact that waits in "+st)
			}
			// back to the defaults with commits waiting: a read still never pushes.
			// Only the personal store goes back to push=now: store set on the
			// team store is an org write that pushes at once (D18) and would
			// push the commit meant to wait, so that store stays manual with
			// its commit waiting.
			spSet(t, m, app, "me", "push=now")
			for _, st := range []string{"me", spOrg} {
				if v := spShow(t, m, app, st); v.Unpushed < 1 {
					t.Errorf("store %s: nothing waits, so the reads below prove nothing (unpushed %d)", st, v.Unpushed)
				}
			}
			lsRun(t, m, app, "config", "set", "live.autopush", "true")
			npSweep(t, m, remotes, here, []string{groupReads}, "defaults, reads", true)
		})
	}
}

// TestNoPushWithoutAskingExamples is S-1 over every command of the
// operation registry: each leaf command's own examples (its --help) run
// in the code repos and the store clones of a push=manual machine.
func TestNoPushWithoutAskingExamples(t *testing.T) {
	t.Parallel()
	ex := npExamples(t)
	for i, name := range npPlaceNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m, remotes, places := npMachine(t)
			npRunExamples(t, m, remotes, places, i, ex, "examples, push=manual, autopush off")
		})
	}
}

// npExamples is every example of every leaf command, read from the
// binary's own help (built from the operation registry, so a new
// operation's examples join this pass the day it is added): leaf → each
// example's words after "lore".
func npExamples(t *testing.T) map[string][][]string {
	t.Helper()
	home := t.TempDir()
	out := map[string][][]string{}
	for _, leaf := range leafCommands(t) {
		c := exec.Command(loreBin, append(strings.Fields(leaf), "--help")...)
		c.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "NO_COLOR=1", "LORE_NO_UPDATE_CHECK=1"}
		b, err := c.Output()
		if err != nil {
			t.Fatalf("lore %s --help: %v", leaf, err)
		}
		inside := false
		for _, line := range strings.Split(string(b), "\n") {
			switch l := strings.TrimSpace(line); {
			case line == "Examples:":
				inside = true
			case inside && l == "":
				inside = false
			case inside && strings.HasPrefix(l, "lore "):
				if w := npWords(strings.TrimPrefix(l, "lore ")); len(w) > 0 {
					out[leaf] = append(out[leaf], w)
				}
			}
		}
	}
	return out
}

// npWords splits a shell line into words: '…' and "…" quote, a backslash
// escapes, and an example that pipes, redirects or chains commands is
// left out (nil).
func npWords(line string) []string {
	var (
		words []string
		cur   strings.Builder
		in    rune
		have  bool
	)
	for i := 0; i < len(line); i++ {
		ch := rune(line[i])
		switch {
		case in != 0 && ch == in:
			in = 0
		case in != 0:
			cur.WriteRune(ch)
		case ch == '\'' || ch == '"':
			in, have = ch, true
		case ch == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
			have = true
		case ch == '|' || ch == '>' || ch == '<' || ch == ';' || ch == '&' || ch == '$' || ch == '`':
			return nil
		case ch == ' ' || ch == '\t':
			if have {
				words, have = append(words, cur.String()), false
				cur.Reset()
			}
		case ch == '#':
			if !have {
				return words
			}
			cur.WriteRune(ch)
		default:
			cur.WriteRune(ch)
			have = true
		}
	}
	if have {
		words = append(words, cur.String())
	}
	return words
}

// npRunExamples runs every example of every leaf command (npExamples) in
// each place, the person's asked pushes excepted, and fails each run that
// moved a remote ref.
func npRunExamples(t *testing.T, m *Machine, remotes npRemotes, places []npPlace, at int, ex map[string][][]string, when string) {
	t.Helper()
	leaves := make([]string, 0, len(ex))
	for leaf := range ex {
		leaves = append(leaves, leaf)
	}
	slices.Sort(leaves)
	ran := 0
	app := places[1].dir
	settings := npSettings(t, m, places)
	before := remotes.snap(t)
	for _, p := range places[at : at+1] {
		for _, leaf := range leaves {
			if slices.Contains(npAsked, leaf) {
				continue
			}
			for _, words := range ex[leaf] {
				r := m.Lore(t, p.dir, words...)
				ran++
				after := remotes.snap(t)
				if _, d18 := npOrgWritesPushAtOnce[leaf]; d18 {
					before = after
				}
				if moved := npMoved(before, after); len(moved) > 0 {
					t.Errorf("[%s] `lore %s` (in %s) pushed without being asked: %v\n%s", when, strings.Join(words, " "), p.name, moved, r)
				}
				before = after
				// An example that changed the push settings (config set
				// live.autopush, store set push=now, a store attached) is
				// the person asking; put "don't push" back for the next.
				if now := npSettings(t, m, places); now != settings {
					npHold(t, m, app)
					settings, before = npSettings(t, m, places), remotes.snap(t)
				}
			}
		}
	}
	if ran < 300 {
		t.Errorf("only %d example runs: the help walk found too few examples", ran)
	}
}
