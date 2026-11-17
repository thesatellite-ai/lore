//go:build stories && !windows

package stories

// W_where_you_stand_test.go — story W (tests/stories/W-where-you-stand.md):
// the where-you-stand matrix. Every operation the registry marks as a pure
// read (op.Spec.OnlyReads) runs from every place a person stands: the home
// folder, the org store's clone, a folder of it, an own repo linked to a
// folder, a client repo, a repo that gitignores .lore/, a repo whose
// CLAUDE.md links out of the repo, and a lore 0.1.17 repo. Each run must
// (a) answer, or refuse with a reason whose hint names --store and --setup
// (never "git init"); (b) leave every tracked file byte-identical (and the
// file a link points at outside the repo); (c) move no ref of any remote;
// (d) exit 0 with an answer and no error line, or non-zero with one.
//
// The op list comes from the real registry (wire.Registry), so a read added
// tomorrow joins the matrix the day it lands; inputs are generated from each
// op's required fields (wInput), with a small table where a generated value
// cannot work (wOverride).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
	"github.com/thesatellite-ai/lore/wire"
)

// The org of story W (placeholders).
const (
	wOrg        = "acme"
	wSetup      = "acme-admin"
	wFolders    = "~/code/**"
	wFolder     = "web"
	wClientProj = "client_shop"
	wStoreSlug  = "github.com/acme-co/lore-team"
	wCStoreSlug = "github.com/acme-co/lore-client-shop"
	wAppSlug    = "github.com/acme-co/platform"
	wWebSlug    = "github.com/acme-co/web"
	wShopSlug   = "github.com/shopco/shop"
	wIgnSlug    = "github.com/acme-co/private-notes"
	wLinkSlug   = "github.com/acme-co/linked-claude"
	wOldSlug    = "github.com/acme-co/old-app"
)

// wAdmin is the org admin who stands in every place.
var wAdmin = journeys.Person{Name: "Ada Admin", Email: "ada@example.test", Login: "ada-gh"}

// wPlace is one place a person stands.
type wPlace struct {
	Name string
	Dir  string
	// Repo is the git work tree whose tracked files must stay byte-identical
	// ("" = the place is no repo: the folder must not become one).
	Repo string
	// Outside is a file outside the repo a tracked link points at.
	Outside string
}

// wErrLine is the human error line (surface/cli printError) or cobra's own.
var wErrLine = regexp.MustCompile(`(?m)^(ERROR: \[E_[A-Z_]+\]|Error: )`)

// wCode is the E_ code of an error line.
var wCode = regexp.MustCompile(`ERROR: \[(E_[A-Z_]+)\]`)

// wPlaceCodes are the refusals that say "not here": their hint must name
// --store and --setup (a person anywhere can name the store or the setup),
// unless the read is repo-only (wRepoOnly).
var wPlaceCodes = map[string]bool{"E_NOT_A_STORE": true}

// wRepoOnly marks the refusal of a read that lives in each code repo
// (missions, the inbox, migrate status: devco D-508, D-509): its hint says
// to cd into the code repo and that --store does not apply, which is the
// right way out instead of --store and --setup (owner 2026-11-17).
func wRepoOnly(out string) bool {
	return strings.Contains(out, "cd into") && strings.Contains(out, "--store does not apply")
}

// wSkip is the reads the matrix does not run, each with its reason.
var wSkip = map[string]string{
	"sync.run": "lore sync is the command that sends (the owner's rule: only sync, sync land, store push or a git push move a remote); its reads are sync.status and sync.peek, which run",
}

// wQuiet are reads that answer a clean result with no output by design:
// check leaks is a git hook, and a hook that passes says nothing.
var wQuiet = map[string]string{
	"check.leaks": "a git hook: silent when nothing leaks",
}

// wExits are reads whose documented non-zero exit still prints their
// report, not an error (doctor-style: 1 degraded, 2 broken).
var wExits = map[string][]int{}

// The rules a run is checked against (wCheck); a finding names its rule.
const (
	wRuleHint   = "a-hint"   // a not-here refusal names --store and --setup
	wRuleInit   = "a-init"   // no refusal says "git init"
	wRuleExit   = "d-exit"   // the exit code matches the output
	wRuleFiles  = "b-files"  // tracked files and git status unchanged
	wRuleRemote = "c-remote" // no remote's refs moved
	// a-rowless: a read refuses because the place has no project row; a
	// read never makes one (T-11), so it must answer from the rows there
	wRuleRowless = "a-rowless"
)

// wRowless is the refusal of a read at a place with no project row.
const wRowless = "has no project row yet"

// wWaiting are findings another branch's fix covers, by rule then by
// "<op>@<place>", "*@<place>", "<op>@*" or "*@*": such a finding is logged
// as waiting, never asserted the buggy way, until the fix lands and the
// entry is removed.
var wWaiting = map[string]map[string]string{
	wRuleHint: {
		// open: reads of one repo refuse with hints of their own (git's
		// work-tree hint, storehere's code-repo hint, the repo-only config
		// error) that name neither flag nor say they are repo-only; and
		// sync trash list must read a store clone's own trash
		"migrate.status@home": "D-509", "session.list@home": "D-509", "why_context.show@home": "D-509",
		"migrate.status@store-root": "D-509", "migrate.status@store-folder": "D-509",
		"sync.trash.list@store-root": "D-509", "sync.trash.list@store-folder": "D-509",
	},
	wRuleRowless: {
		// why-context show reads one repo's render history: at a store
		// clone's root it is a repo-only refusal to make (D-509)
		"why_context.show@store-root": "D-509",
	},
}

// wTrace set logs every cell's exit and first error line (run with -v):
// what a person sees from each place, for reading the matrix by hand.
const wTrace = "W_TRACE"

// wBad is one broken rule of one run.
type wBad struct{ Rule, Text string }

// TestStoryWWhereYouStand: every read, from every place.
func TestStoryWWhereYouStand(t *testing.T) {
	s := New(t, "W")
	store := s.Remote(wStoreSlug)
	cstore := s.Remote(wCStoreSlug)
	app := s.Seed(s.Remote(wAppSlug), func(dir string) { PlainRepo(t, dir) })
	web := s.Seed(s.Remote(wWebSlug), func(dir string) { PlainRepo(t, dir) })
	shop := s.Seed(s.Remote(wShopSlug), func(dir string) { Monorepo(t, dir) })
	ign := s.Seed(s.Remote(wIgnSlug), func(dir string) {
		PlainRepo(t, dir)
		writeFile(t, filepath.Join(dir, ".gitignore"), ".lore/\n")
	})
	lnk := s.Seed(s.Remote(wLinkSlug), func(dir string) {
		PlainRepo(t, dir)
		if err := os.Symlink(filepath.Join("..", "..", "notes", "CLAUDE.md"), filepath.Join(dir, "CLAUDE.md")); err != nil {
			t.Fatal(err)
		}
	})
	old := s.Seed(s.Remote(wOldSlug), func(dir string) { V017Repo(t, dir, "old-app", SmallShape) })
	remotes := []*teamharness.Remote{store, cstore, app, web, shop, ign, lnk, old}

	m := s.Persona(wAdmin.Name, wAdmin.Email).Laptop()
	journeys.GitHubLogin(t, s.World, m, wAdmin.Login)
	code := filepath.Join(m.Home, "code")
	clone := func(r *teamharness.Remote, name string) *teamharness.Clone {
		c := m.Clone(r, name)
		m.MoveClone(t, c, filepath.Join(code, name))
		return c
	}
	var places []wPlace
	ids := map[string]string{}
	notes := filepath.Join(m.Home, "notes", "CLAUDE.md")

	s.Step("day1/admin-stands-up-every-place", func() {
		pc := clone(app, "platform")
		journeys.StartTeam(t, m, pc.Dir, wSetup, wOrg, store.SSHURL(), wAdmin, []string{wFolders})
		m.LoreIn(pc, "init").OK(t)
		m.LoreIn(pc, "store", "folder", "add", "team", wFolder).OK(t)
		// rows the show/history reads can name, in the folder
		for noun, args := range map[string][]string{
			"memory":   {"--body", "matrix memory"},
			"task":     {"--title", "matrix task"},
			"decision": {"--title", "matrix decision", "--body", "matrix why"},
			"rule":     {"--body", "matrix rule"},
		} {
			r := m.LoreIn(pc, append([]string{noun, "add", "--json", "--to", wOrg + "/" + wFolder, "--yes"}, args...)...).OK(t)
			var got struct {
				ID   string `json:"id"`
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(r.Stdout), &got); err != nil || got.ID+got.Data.ID == "" {
				t.Fatalf("%s add --json gave no id (%v): %s", noun, err, r)
			}
			ids[noun] = got.ID + got.Data.ID
		}
		pc.Git("add", "-A")
		pc.Git("commit", "-q", "--allow-empty", "-m", "lore setup")

		// own repo linked to a folder of the store
		wc := clone(web, "web")
		m.LoreIn(wc, "org", "link", "--org", wOrg, "--project", wFolder, "--yes").OK(t)

		// client repo on the org's client store
		sc := clone(shop, "shop")
		m.LoreIn(pc, "org", "store", "add", "--org", wOrg, "--url", cstore.SSHURL(), "--yes").OK(t)
		m.LoreIn(sc, "org", "link", "--org", wOrg, "--project", wClientProj, "--store", cstore.SSHURL(), "--yes").OK(t)

		// a repo that gitignores .lore/, and one whose CLAUDE.md links out
		ic := clone(ign, "private-notes")
		m.LoreIn(ic, "init").OK(t)
		if err := os.MkdirAll(filepath.Dir(notes), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(notes, []byte("# my notes\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		lc := clone(lnk, "linked-claude")
		m.LoreIn(lc, "init").OK(t)

		// a lore 0.1.17 repo, never migrated
		oc := clone(old, "old-app")

		// the setup's own changes are committed: every place starts clean
		for _, c := range []*teamharness.Clone{wc, sc, ic, lc} {
			c.Git("add", "-A")
			c.Git("commit", "-q", "--allow-empty", "-m", "setup")
		}
		// every store sent, so a ref that moves later is a read's push
		m.LoreIn(pc, "sync").OK(t)
		root := m.LorePath("stores", "acme-co", "lore-team")
		if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
			t.Fatalf("the org store's clone is not at %s: %v", root, err)
		}
		folder := wFindDir(t, root, wFolder)
		places = []wPlace{
			{Name: "home", Dir: m.Home},
			{Name: "store-root", Dir: root, Repo: root},
			{Name: "store-folder", Dir: folder, Repo: root},
			{Name: "own-linked", Dir: wc.Dir, Repo: wc.Dir},
			{Name: "client", Dir: sc.Dir, Repo: sc.Dir},
			{Name: "lore-ignored", Dir: ic.Dir, Repo: ic.Dir},
			{Name: "claude-link-out", Dir: lc.Dir, Repo: lc.Dir, Outside: notes},
			{Name: "v0117", Dir: oc.Dir, Repo: oc.Dir},
		}
	})

	reads := wReads(t)
	s.Step("day2/every-read-from-every-place", func() {
		s.AdvanceDays(1)
		t.Run("matrix", func(t *testing.T) {
			for _, p := range places {
				t.Run(p.Name, func(t *testing.T) {
					t.Parallel()
					wRunPlace(t, s, m, p, reads, ids, remotes)
				})
			}
		})
	})
}

// wRead is one read op and its CLI path.
type wRead struct {
	Name string
	Path []string
	In   []op.FieldInfo
}

// wReads lists every pure read of the real registry, by name.
func wReads(t testing.TB) []wRead {
	t.Helper()
	reg, err := wire.Registry(&wire.App{})
	if err != nil {
		t.Fatal(err)
	}
	var out []wRead
	for _, e := range reg.Entries() {
		sp := e.Spec()
		if !sp.OnlyReads() || sp.Hidden {
			continue
		}
		if _, skip := wSkip[sp.Name]; skip {
			continue
		}
		out = append(out, wRead{Name: sp.Name, Path: op.CLIPath(sp.Name), In: e.InputFields()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) < 150 {
		t.Fatalf("only %d reads in the registry; the walk is broken", len(out))
	}
	return out
}

// wOverride is the input of a read whose generated one cannot work.
var wOverride = map[string][]string{
	"sql.run":       {"--query", "select 1"},
	"config.get":    {"--key", "live.autopush"},
	"setup.show":    {"--name", wSetup},
	"setup.check":   {"--setup", wSetup},
	"member.show":   {"--who", wAdmin.Email},
	"actor.show":    {"--id", wAdmin.Email},
	"scope.show":    {"--path", "."},
	"sync.peek":     {"--ref", "main"},
	"resource.list": {"--kind", "code_repo"},
}

// wInput is the CLI arguments of r: the override, or each required
// non-bool field named with a generated value (an id seeded for the noun
// when there is one, else a well-formed word the op may refuse as unknown).
func wInput(r wRead, ids map[string]string, prBody string) []string {
	args := append([]string{}, r.Path...)
	if o, ok := wOverride[r.Name]; ok {
		return append(args, o...)
	}
	noun := r.Path[0]
	for _, f := range r.In {
		if !f.Required || f.Flag == "" {
			continue
		}
		v := "matrix"
		switch f.Name {
		case "id":
			if id := ids[noun]; id != "" {
				v = id
			}
		case "file":
			v = prBody
		case "query":
			v = "matrix"
		}
		args = append(args, "--"+f.Flag, v)
	}
	return args
}

// wSnap is what must not change at a place: the tracked files and git
// status of its repo, the file a link points at, every remote's refs.
type wSnap struct {
	tracked, status, outside, refs string
	gitDir                         bool
}

func wSnapshot(t testing.TB, s *Story, p wPlace, remotes []*teamharness.Remote) wSnap {
	t.Helper()
	var sn wSnap
	if p.Repo != "" {
		sn.status = s.Git(p.Repo, "status", "--porcelain", "--untracked-files=no")
		sn.tracked = wTracked(t, s, p.Repo)
	} else {
		_, err := os.Stat(filepath.Join(p.Dir, ".git"))
		sn.gitDir = err == nil
	}
	if p.Outside != "" {
		b, err := os.ReadFile(p.Outside)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		sn.outside = hex.EncodeToString(sum[:])
	}
	var refs []string
	for _, r := range remotes {
		refs = append(refs, r.Slug+"\n"+s.RefsOf(r))
	}
	sn.refs = strings.Join(refs, "\n")
	return sn
}

// wTracked hashes every tracked file of repo (a link by its target).
func wTracked(t testing.TB, s *Story, repo string) string {
	t.Helper()
	var lines []string
	for _, rel := range strings.Split(strings.TrimRight(s.Git(repo, "ls-files"), "\n"), "\n") {
		if rel == "" {
			continue
		}
		p := filepath.Join(repo, rel)
		var body []byte
		info, err := os.Lstat(p)
		switch {
		case err != nil:
			body = []byte("missing")
		case info.Mode()&os.ModeSymlink != 0:
			tgt, _ := os.Readlink(p)
			body = []byte("-> " + tgt)
		default:
			body, _ = os.ReadFile(p)
		}
		sum := sha256.Sum256(body)
		lines = append(lines, rel+" "+hex.EncodeToString(sum[:8]))
	}
	return strings.Join(lines, "\n")
}

// wRunPlace runs every read at p and checks the four rules after each.
func wRunPlace(t *testing.T, s *Story, m *journeys.Machine, p wPlace, reads []wRead, ids map[string]string, remotes []*teamharness.Remote) {
	prBody := filepath.Join(t.TempDir(), "pr-body.md")
	if err := os.WriteFile(prBody, []byte("## Summary\nmatrix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := wSnapshot(t, s, p, remotes)
	var pass, refuse int
	for _, r := range reads {
		args := wInput(r, ids, prBody)
		res := m.Lore(p.Dir, args...)
		now := wSnapshot(t, s, p, remotes)
		cell := r.Name + "@" + p.Name
		var bad []string
		for _, b := range wCheck(r, p, res, before, now) {
			if why := wWaitingFor(b.Rule, r.Name, p.Name); why != "" {
				t.Logf("WAITING for %s: %s %s", why, cell, b.Text)
				continue
			}
			bad = append(bad, b.Text)
		}
		before = now
		if len(bad) > 0 {
			t.Errorf("[W] %s: lore %s\n  %s\n%s", cell, strings.Join(args, " "), strings.Join(bad, "\n  "), res)
		}
		if os.Getenv(wTrace) != "" {
			t.Logf("CELL %s exit=%d %s", cell, res.Code, strings.SplitN(strings.TrimSpace(res.Stderr), "\n", 2)[0])
		}
		if res.Code == 0 {
			pass++
		} else {
			refuse++
		}
	}
	t.Logf("MATRIX %s: %d reads, %d answered, %d refused", p.Name, len(reads), pass, refuse)
}

// wWaitingFor is the finding a cell waits on, or "".
func wWaitingFor(rule, opName, place string) string {
	for _, k := range []string{opName + "@" + place, "*@" + place, opName + "@*", "*@*"} {
		if why, ok := wWaiting[rule][k]; ok {
			return why
		}
	}
	return ""
}

// wCheck is the four rules for one run; each broken one is a line.
func wCheck(r wRead, p wPlace, res teamharness.Result, before, now wSnap) []wBad {
	var bad []wBad
	add := func(rule, text string) { bad = append(bad, wBad{rule, text}) }
	out := res.Out()
	errLine := wErrLine.MatchString(res.Stderr) || wErrLine.MatchString(res.Stdout)
	// (d) the exit code matches the output
	_, quiet := wQuiet[r.Name]
	switch {
	case res.Code == 0 && errLine:
		add(wRuleExit, "(d) exit 0 with an error line")
	case res.Code == 0 && strings.TrimSpace(out) == "" && !quiet:
		add(wRuleExit, "(d) exit 0 with no output at all")
	case res.Code != 0 && !errLine && !wDocumentedExit(r.Name, res.Code):
		add(wRuleExit, "(d) exit non-zero with no error line")
	}
	// (a) a refusal says why and names the way out from anywhere
	if res.Code != 0 {
		if strings.Contains(out, "git init") {
			add(wRuleInit, `(a) the refusal says "git init"`)
		}
		if strings.Contains(out, wRowless) {
			add(wRuleRowless, "(a) refuses for want of a project row the place has none of")
		}
		if c := wCode.FindStringSubmatch(out); c != nil && wPlaceCodes[c[1]] {
			if !wRepoOnly(out) && (!strings.Contains(out, "--store") || !strings.Contains(out, "--setup")) {
				add(wRuleHint, "(a) a not-here refusal ("+c[1]+") whose hint does not name --store and --setup")
			}
		}
	}
	// (b) tracked files byte-identical
	if now.tracked != before.tracked {
		add(wRuleFiles, "(b) changed tracked files: "+strings.Join(wLineDiff(before.tracked, now.tracked), "; "))
	}
	if now.status != before.status {
		add(wRuleFiles, "(b) git status changed:\n"+before.status+"\n->\n"+now.status)
	}
	if now.outside != before.outside {
		add(wRuleFiles, "(b) wrote through the link to "+p.Outside)
	}
	if !before.gitDir && now.gitDir {
		add(wRuleFiles, "(b) made "+p.Dir+" a git repo")
	}
	// (c) nothing reaches a remote
	if now.refs != before.refs {
		add(wRuleRemote, "(c) moved a remote's refs: "+strings.Join(wLineDiff(before.refs, now.refs), "; "))
	}
	return bad
}

// wDocumentedExit reports a documented report-with-exit of opName.
func wDocumentedExit(opName string, code int) bool {
	for _, c := range wExits[opName] {
		if c == code {
			return true
		}
	}
	return false
}

// wLineDiff lists the lines only in one of a and b.
func wLineDiff(a, b string) []string {
	in := map[string]int{}
	for _, l := range strings.Split(a, "\n") {
		in[l]--
	}
	for _, l := range strings.Split(b, "\n") {
		in[l]++
	}
	var out []string
	for l, n := range in {
		switch {
		case n < 0:
			out = append(out, "- "+l)
		case n > 0:
			out = append(out, "+ "+l)
		}
	}
	sort.Strings(out)
	return out
}

// wFindDir is the first folder named name under root (the store keeps a
// folder's rows under its own directory).
func wFindDir(t testing.TB, root, name string) string {
	t.Helper()
	var found string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() && d.Name() == name {
			found = p
			return filepath.SkipDir
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no folder %s in the store clone %s", name, root)
	}
	return found
}
