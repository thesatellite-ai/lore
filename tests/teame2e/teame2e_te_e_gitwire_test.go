//go:build teame2e

package teame2e

// later.md 607 (b11-sync ports, the part te-d left) on the cmd/lore team
// binary: the per-clone git wiring a teammate's first lore command
// installs (wire/host gitwire.go, dataignore.go) — merge drivers, the
// .gitattributes block, the hook blocks, the .gitignore re-include — seen
// the way a teammate meets it: in her own clone, through git.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// teEHooks are the hooks every wired clone carries (v0.1.17
// TestHookLinesNamesAreSortable's list).
var teEHooks = []string{"commit-msg", "post-checkout", "post-commit", "post-merge", "post-rewrite", "pre-commit", "pre-push"}

// teEAttrLines are the .gitattributes rules of a lore project (v0.1.17
// TestGitAttributeLines).
var teEAttrLines = []string{
	".lore/data/**/*.json text eol=lf merge=lore linguist-generated=true",
	".lore/data/*.json text eol=lf merge=lore linguist-generated=true",
	".lore/LORE.md text eol=lf merge=lore-keep-ours linguist-generated=true",
}

// teEAwsKey is the AWS documentation example key, built at run time
// (convention 8).
func teEAwsKey() string { return "AKIA" + "IOSFODNN7EXAMPLE" }

// teEPair is bob's clone of the loose repo and carol, both on no-org
// machines; carol clones once bob has pushed.
func teEPair(t *testing.T) (*th.Clone, *th.Actor) {
	t.Helper()
	w := newTeamWorld(t)
	r := w.Remote(slugLoose)
	bob := w.Human("bob", "bob@solo.example")
	bob.WriteConfig(teESoloConfig("human:bob@solo.example", "Bob Roe", "bob"))
	carol := w.Human("carol", "carol@solo.example")
	carol.WriteConfig(teESoloConfig("human:carol@solo.example", "Carol Poe", "carol"))
	return bob.Clone(r, "tool"), carol
}

// teESoloConfig is a no-org machine config with a fresh live fetch.
func teESoloConfig(key, name, handle string) th.Config {
	return th.Config{
		Identity: &th.Identity{Key: key, Name: name, Handle: handle},
		Personal: &th.Personal{Store: th.PersonalLocal},
		Live:     th.FreshFetch(),
	}
}

// teEInitPushed inits the project in bob's clone, adds one rule, commits
// and pushes it; it returns the rule id.
func teEInitPushed(t *testing.T, bc *th.Clone, body string) string {
	t.Helper()
	bc.MustLore("init", "--non-interactive", "--name=tool")
	id := mustID(t, bc.MustLore("rule", "add", "--body", body), "rul")
	bc.CommitAll("lore")
	bc.AdminPush("origin", "main")
	return id
}

// teEHooksDir is the absolute hooks folder git uses in the clone.
func teEHooksDir(c *th.Clone) string {
	return c.Git("rev-parse", "--path-format=absolute", "--git-path", "hooks")
}

// teEReadHook reads one hook file ("" when absent).
func teEReadHook(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, name)))
	if err != nil {
		return ""
	}
	return string(b)
}

// teEDirMode is the mode of a folder the test creates.
const teEDirMode = 0o750

// teEWriteExec writes an executable script, making its folder first
// (teamharness.WriteExecutable holds ForkLock against ETXTBSY).
func teEWriteExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), teEDirMode); err != nil {
		t.Fatal(err)
	}
	th.WriteExecutable(t, path, body)
}

// TestE2E_InitBootstrapFreshCloneAdopt (wiring half) + TestGitAttributeLines
// + TestHookLinesNamesAreSortable, ported: bob's init writes the
// .gitattributes block and the merge drivers and every hook; carol's fresh
// clone ships no lore.db, her first lore command wires her clone the same
// way (driver pointing at the lore she runs), reads bob's rule and leaves
// the shared files clean.
func TestTeE_FreshCloneIsWiredByFirstCommand(t *testing.T) {
	bc, carol := teEPair(t)
	teEInitPushed(t, bc, "use JWT for auth")
	attrs, ok := bc.ReadFile(".gitattributes")
	if !ok {
		t.Fatal("init writes .gitattributes")
	}
	for _, l := range teEAttrLines {
		if !strings.Contains(attrs, l+"\n") {
			t.Fatalf(".gitattributes lacks %q:\n%s", l, attrs)
		}
	}
	if bc.Git("ls-files", ".gitattributes") == "" {
		t.Fatal(".gitattributes is committed with the project")
	}
	if bc.Git("ls-files", ".lore/lore.db") != "" || bc.Git("status", "--porcelain", "--", ".lore/lore.db") != "" {
		t.Fatal("lore.db stays out of git")
	}

	cc := carol.Clone(bc.Remote, "tool")
	if _, err := os.Stat(filepath.Join(cc.Dir, ".lore", "lore.db")); err == nil {
		t.Fatal("a fresh clone ships no lore.db")
	}
	if r := cc.TryGit("config", "--get", "merge.lore.driver"); r.Code == 0 {
		t.Fatalf("a fresh clone has no driver before lore runs: %s", r)
	}
	cc.MustLore("rule", "list").Says(t, "use JWT for auth")
	drv := cc.Git("config", "--get", "merge.lore.driver")
	if !strings.Contains(drv, " merge-driver %O %A %B %P") {
		t.Fatalf("carol's first command configures the row driver: %q", drv)
	}
	if keep := cc.Git("config", "--get", "merge.lore-keep-ours.driver"); !strings.Contains(keep, "merge-driver --keep-ours %O %A %B %P") {
		t.Fatalf("carol's first command configures the LORE.md driver: %q", keep)
	}
	dir := teEHooksDir(cc)
	for _, h := range teEHooks {
		if s := teEReadHook(t, dir, h); !strings.Contains(s, "# >>> lore-") {
			t.Fatalf("carol's clone lacks lore's %s block:\n%s", h, s)
		}
	}
	if out := cc.Git("status", "--porcelain", "--", ".lore/data", ".gitattributes", ".gitignore"); out != "" {
		t.Fatalf("wiring a fresh clone dirties no shared file: %s", out)
	}
}

// TestE2E_NoDriverIsVisibleTextConflict, ported to two clones: carol's
// clone loses the driver (a merge made where git knows no custom driver);
// the same field edited by bob and carol is a plain text conflict, taking
// bob's side imports cleanly, and her next lore command re-installs the
// driver.
func TestTeE_NoDriverTextConflictThenReinstall(t *testing.T) {
	bc, carol := teEPair(t)
	id := teEInitPushed(t, bc, "base")
	cc := carol.Clone(bc.Remote, "tool")
	cc.MustLore("rule", "list").Says(t, "base")

	bc.MustLore("rule", "edit", id, "--body", "bob's body")
	bc.CommitAll("bob")
	bc.AdminPush("origin", "main")
	cc.MustLore("rule", "edit", id, "--body", "carol's body")
	cc.CommitAll("carol")
	cc.Git("config", "--remove-section", "merge.lore")
	if res := cc.TryGit("pull", "-q", "--no-rebase", "--no-edit"); res.Code == 0 {
		t.Fatalf("without the driver a same-row edit is a text conflict: %s", res)
	}
	cc.Git("checkout", "--theirs", "--", dataDir)
	cc.Git("add", "-A")
	cc.Git("commit", "-qm", "take bob's")
	cc.MustLore("rule", "show", id).Says(t, "bob's body").Never(t, "carol's body")
	if v := cc.Git("config", "--get", "merge.lore.driver"); !strings.Contains(v, "merge-driver") {
		t.Fatalf("the next lore command re-installs the driver: %q", v)
	}
}

// TestE2E_HooksChainIntoExistingHooksPath (pre-commit half), ported: a
// teammate whose clone runs hooks from .husky (core.hooksPath) keeps it;
// lore's block is chained in front of her own hook, which still runs on
// every commit, and lore's guard still refuses a hand-pasted secret.
func TestTeE_HooksChainIntoHusky(t *testing.T) {
	bc, carol := teEPair(t)
	id := teEInitPushed(t, bc, "clean rule")
	cc := carol.Clone(bc.Remote, "tool")
	husky := filepath.Join(cc.Dir, ".husky")
	teEWriteExec(t, filepath.Join(husky, "pre-commit"), "#!/bin/sh\necho user-hook-ran\nexit 0\n")
	cc.Git("config", "core.hooksPath", ".husky")

	cc.MustLore("rule", "list").Says(t, "clean rule")
	if v := cc.Git("config", "--get", "core.hooksPath"); v != ".husky" {
		t.Fatalf("core.hooksPath repointed to %q", v)
	}
	s := teEReadHook(t, husky, "pre-commit")
	if !strings.HasPrefix(s, "#!/bin/sh\n# >>> lore-sync >>>") || !strings.Contains(s, "echo user-hook-ran\nexit 0\n") {
		t.Fatalf("lore's block is chained in front of the user's hook:\n%s", s)
	}
	if s := teEReadHook(t, filepath.Join(cc.Dir, ".git", "hooks"), "pre-commit"); strings.Contains(s, "lore-sync") {
		t.Fatalf("nothing is written to the hooks folder git does not use:\n%s", s)
	}
	cc.WriteFile("notes.txt", "hello\n")
	cc.Git("add", "notes.txt")
	if res := cc.TryGit("commit", "-m", "notes"); res.Code != 0 || !strings.Contains(res.Out(), "user-hook-ran") {
		t.Fatalf("the user's hook still runs: %s", res)
	}
	rel := dataDir + "/" + tblRules + "/" + id + ".json"
	body, _ := cc.ReadFile(rel)
	cc.WriteFile(rel, strings.Replace(body, "clean rule", "key "+teEAwsKey(), 1))
	cc.Git("add", "-A")
	if res := cc.TryGit("commit", "-qm", "leak"); res.Code == 0 {
		t.Fatalf("lore's chained guard refuses a hand-pasted secret: %s", res)
	}
}

// TestE2E_SyncDisabled (git half), ported to the root module's switch:
// LORE_SYNC_GIT=0 leaves the clone's git alone (no driver, no
// .gitattributes, no hook blocks) while rows are still written as files.
func TestTeE_SyncGitOffLeavesGitAlone(t *testing.T) {
	bc, _ := teEPair(t)
	off := bc.With("LORE_SYNC_GIT=0")
	off.MustLore("init", "--non-interactive", "--name=tool")
	id := mustID(t, off.MustLore("rule", "add", "--body", "x"), "rul")
	if _, ok := bc.ReadFile(dataDir + "/" + tblRules + "/" + id + ".json"); !ok {
		t.Fatal("LORE_SYNC_GIT=0 still writes the row file")
	}
	if _, ok := bc.ReadFile(".gitattributes"); ok {
		t.Fatal("LORE_SYNC_GIT=0 writes no .gitattributes")
	}
	if r := bc.TryGit("config", "--get", "merge.lore.driver"); r.Code == 0 {
		t.Fatalf("LORE_SYNC_GIT=0 configures no driver: %s", r)
	}
	if s := teEReadHook(t, teEHooksDir(bc), "pre-commit"); strings.Contains(s, "lore-sync") {
		t.Fatalf("LORE_SYNC_GIT=0 installs no hook:\n%s", s)
	}
	// Switched back on, the next command wires the clone.
	bc.MustLore("rule", "list")
	if r := bc.TryGit("config", "--get", "merge.lore.driver"); r.Code != 0 {
		t.Fatalf("without the switch the next command wires the clone: %s", r)
	}
}

// TestE2E_LoreMDRerenderedAfterPull, ported: bob pushes a rule without
// re-rendering LORE.md; carol's pull (post-merge hook) re-renders hers, and
// the same data renders byte-identical LORE.md on both clones.
func TestTeE_LoreMDRerenderedAfterPull(t *testing.T) {
	bc, carol := teEPair(t)
	bc.MustLore("init", "--non-interactive", "--name=tool")
	bc.MustLore("render", "--no-pointer")
	bc.CommitAll("lore")
	bc.AdminPush("origin", "main")
	cc := carol.Clone(bc.Remote, "tool")
	cc.MustLore("rule", "list")

	bc.MustLore("rule", "add", "--body", "must rule from bob", "--severity", "must")
	bc.Git("add", dataDir)
	bc.Git("commit", "-qm", "rule data only")
	bc.AdminPush("origin", "main")
	if md, _ := bc.Remote.Read("main", ".lore/LORE.md"); strings.Contains(md, "must rule from bob") {
		t.Fatal("setup: the pushed LORE.md must not carry the rule yet")
	}
	cc.Git("pull", "-q", "--no-rebase", "--no-edit")
	md, ok := cc.ReadFile(".lore/LORE.md")
	if !ok || !strings.Contains(md, "must rule from bob") {
		t.Fatalf("carol's pull re-renders LORE.md:\n%s", md)
	}
	bc.MustLore("render", "--no-pointer")
	if bmd, _ := bc.ReadFile(".lore/LORE.md"); bmd != md {
		t.Fatalf("the same data renders the same LORE.md on both clones:\n--- bob\n%s\n--- carol\n%s", bmd, md)
	}
}

// TestE2E_PreCommitGuardsUnstagedFileInSubdirProject, ported: in a
// teammate's clone of a monorepo whose lore project is svc/, a secret in an
// UNSTAGED row file is refused when she commits only README.md.
func TestTeE_PreCommitGuardsUnstagedRowInSubdirProject(t *testing.T) {
	bc, carol := teEPair(t)
	svc := bc.In("svc")
	svc.MustLore("init", "--non-interactive", "--name=svc")
	id := mustID(t, svc.MustLore("rule", "add", "--body", "clean"), "rul")
	bc.CommitAll("svc lore")
	bc.AdminPush("origin", "main")

	cc := carol.Clone(bc.Remote, "tool")
	csvc := cc.In("svc")
	csvc.MustLore("rule", "list").Says(t, "clean")
	rel := "svc/" + dataDir + "/" + tblRules + "/" + id + ".json"
	body, ok := cc.ReadFile(rel)
	if !ok {
		t.Fatalf("carol's clone has %s", rel)
	}
	cc.WriteFile(rel, strings.Replace(body, "clean", "key "+teEAwsKey(), 1))
	cc.WriteFile("README.md", "# changed\n")
	cc.Git("add", "README.md")
	head := cc.Head()
	if res := cc.TryGit("commit", "-qm", "readme only"); res.Code == 0 {
		t.Fatalf("an unstaged row file holding a secret is refused: %s", res)
	}
	if cc.Head() != head {
		t.Fatal("the refused commit made no commit")
	}
}

// TestE2E_TwoProjectsShareHooks + TestHookMarkerPerProject, ported: two
// lore projects in one monorepo each keep their own hook block (marker and
// `cd` line per project) in a teammate's clone; one ride block covers the
// repo-wide hooks.
func TestTeE_TwoProjectsKeepTheirOwnHookBlocks(t *testing.T) {
	bc, carol := teEPair(t)
	for _, p := range []string{"a", "b"} {
		bc.In(p).MustLore("init", "--non-interactive", "--name="+p)
	}
	bc.CommitAll("two projects")
	bc.AdminPush("origin", "main")

	cc := carol.Clone(bc.Remote, "tool")
	cc.In("a").MustLore("rule", "list")
	cc.In("b").MustLore("rule", "list")
	cc.In("a").MustLore("rule", "list") // a again: b's block survives
	dir := teEHooksDir(cc)
	for _, h := range []string{"pre-commit", "commit-msg", "post-merge"} {
		s := teEReadHook(t, dir, h)
		for _, p := range []string{"a", "b"} {
			if strings.Count(s, "# >>> lore-sync:"+p+" >>>") != 1 || !strings.Contains(s, `cd "`+p+`"`) {
				t.Fatalf("%s holds exactly one block for project %s:\n%s", h, p, s)
			}
		}
	}
	for _, h := range []string{"post-commit", "pre-push"} {
		s := teEReadHook(t, dir, h)
		if strings.Contains(s, "lore-sync:a") || strings.Contains(s, "lore-sync:b") || !strings.Contains(s, "# >>> lore-") {
			t.Fatalf("%s carries one repo-wide block, no per-project one:\n%s", h, s)
		}
	}
}

// TestE2E_IgnoredLoreFolderIsReported, ported: when .gitignore ignores the
// whole .lore/ folder a negation cannot help; lore leaves .gitignore alone,
// names the rule once, re-checks when the rule changes, and goes quiet once
// it is fixed.
func TestTeE_IgnoredLoreFolderIsReported(t *testing.T) {
	bc, _ := teEPair(t)
	bc.WriteFile(".gitignore", ".lore/\n")
	bc.CommitAll("ignore .lore")
	r := bc.MustLore("init", "--non-interactive", "--name=tool")
	r.Says(t, "git ignores lore data", ".gitignore:1:.lore/")
	if got, _ := bc.ReadFile(".gitignore"); got != ".lore/\n" {
		t.Fatalf("a re-include that cannot help is not written: %q", got)
	}
	bc.MustLore("rule", "list").Never(t, "git ignores lore data")
	bc.WriteFile(".gitignore", ".lore/\n# still private\n")
	bc.MustLore("rule", "list").Says(t, "git ignores lore data")
	bc.WriteFile(".gitignore", ".lore/lore.db\n")
	bc.MustLore("rule", "list").Never(t, "git ignores lore data")
}

// TestDataUnignoreKeepsOSJunkIgnored, ported: a repo .gitignore that hides
// *.json gets the re-include, yet OS junk inside .lore/data stays ignored.
func TestTeE_ReincludeKeepsOSJunkIgnored(t *testing.T) {
	bc, _ := teEPair(t)
	bc.WriteFile(".gitignore", "*.json\n.DS_Store\n")
	bc.CommitAll("repo ignore rules")
	bc.MustLore("init", "--non-interactive", "--name=tool").Says(t, "re-included .lore/data")
	bc.WriteFile(dataDir+"/.DS_Store", "junk")
	bc.WriteFile(dataDir+"/"+tblRules+"/.DS_Store", "junk")
	if res := bc.TryGit("check-ignore", "-q", dataDir+"/.DS_Store"); res.Code != 0 {
		t.Fatalf(".DS_Store in .lore/data stays ignored: %s", res)
	}
	if res := bc.TryGit("check-ignore", "-q", dataDir+"/"+tblRules+"/.DS_Store"); res.Code != 0 {
		t.Fatalf(".DS_Store in a table folder stays ignored: %s", res)
	}
	if out := bc.Git("status", "--porcelain", "--untracked-files=all", "--", dataDir); strings.Contains(out, ".DS_Store") {
		t.Fatalf("junk is never offered for commit: %s", out)
	}
}

// TestHookLinesCommitMsgCarriesTrailerThenLeakCheck, ported: in a
// teammate's clone the commit-msg hook refuses a commit whose message holds
// a secret, at the top level and in a monorepo sub-project, while an
// ordinary message commits.
func TestTeE_CommitMsgLeakCheckRefusesSecretMessage(t *testing.T) {
	for _, sub := range []string{"", "svc"} {
		t.Run("project="+sub, func(t *testing.T) {
			bc, carol := teEPair(t)
			p := bc
			if sub != "" {
				p = bc.In(sub)
			}
			p.MustLore("init", "--non-interactive", "--name=tool")
			bc.CommitAll("lore")
			bc.AdminPush("origin", "main")
			cc := carol.Clone(bc.Remote, "tool")
			cp := cc
			if sub != "" {
				cp = cc.In(sub)
			}
			cp.MustLore("rule", "list")
			cc.WriteFile("notes.txt", "one\n")
			cc.Git("add", "notes.txt")
			head := cc.Head()
			if res := cc.TryGit("commit", "-qm", "deploy with "+teEAwsKey()); res.Code == 0 {
				t.Fatalf("a secret in the commit message is refused: %s", res)
			}
			if cc.Head() != head {
				t.Fatal("the refused commit made no commit")
			}
			if res := cc.TryGit("commit", "-qm", "ordinary notes"); res.Code != 0 {
				t.Fatalf("an ordinary message commits: %s", res)
			}
		})
	}
}

// TestPreCommitLineAgainstOldAndNewLore, ported to a teammate's machine:
// with a lore on PATH too old to read 0.1.18 rows (no `migrate`), her
// commit of the project is refused with the upgrade line rather than
// passing unchecked; one too old for `sync hook` that does know `migrate`
// only warns.
func TestTeE_PreCommitWithOlderLoreOnPath(t *testing.T) {
	bc, carol := teEPair(t)
	teEInitPushed(t, bc, "a rule")
	cc := carol.Clone(bc.Remote, "tool")
	cc.MustLore("rule", "list")
	for _, tc := range []struct {
		name, script string
		refused      bool
		say          string
	}{
		{"pre-0.1.18 lore", "#!/bin/sh\necho 'unknown command' >&2\nexit 1\n", true, "install lore 0.1.18 or newer"},
		{"no sync hook", "#!/bin/sh\n[ \"$1\" = migrate ] && exit 0\necho 'unknown command \"sync\"' >&2\nexit 1\n", false, "upgrade it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := t.TempDir()
			teEWriteExec(t, filepath.Join(old, "lore"), tc.script)
			cold := cc.With("PATH=" + old + string(os.PathListSeparator) + os.Getenv("PATH"))
			cc.WriteFile("notes-"+strings.ReplaceAll(tc.name, " ", "-")+".txt", "x\n")
			cc.Git("add", "-A")
			res := cold.TryGit("commit", "-qm", tc.name)
			if (res.Code != 0) != tc.refused || !strings.Contains(res.Out(), tc.say) {
				t.Fatalf("refused=%v, saying %q: %s", tc.refused, tc.say, res)
			}
		})
	}
}

// TestE2E_DoctorAndStatus, ported (the half cmd/lore has): in a
// teammate's fresh clone `lore doctor --json` is healthy (exit 0), and a
// row file she breaks by hand fails doctor (exit 2, P3-GATED-2) naming the
// file. The [sync] section (merge_driver_configured, uncommitted_files,
// file_errors) is not on cmd/lore (b11-fates.md line 78, LATER).
func TestTeE_DoctorInTeammateClone(t *testing.T) {
	w := newTeamWorld(t)
	r := w.Remote(slugLoose)
	bob := w.Human("bob", "bob@solo.example")
	bob.WriteConfig(teESoloConfig("human:bob@solo.example", "Bob Roe", "bob"))
	carol := w.Human("carol", "carol@solo.example")
	carol.WriteConfig(teESoloConfig("human:carol@solo.example", "Carol Poe", "carol"))
	bc := bob.Clone(r, "tool")
	id := teEInitPushed(t, bc, "x")
	// Only the binary under test is on PATH: a developer's own lore there
	// would make doctor warn about two binaries.
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	cc := carol.Clone(bc.Remote, "tool").With("PATH=" + strings.Join([]string{filepath.Join(w.Root, "shim"), filepath.Dir(git), "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	cc.MustLore("rule", "list")
	type report struct {
		Status string   `json:"status"`
		Errors []string `json:"errors"`
		Exit   int      `json:"exit_status"`
	}
	decode := func(res th.Result) report {
		t.Helper()
		var rep report
		if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
			t.Fatalf("doctor --json prints one report: %v %s", err, res)
		}
		return rep
	}
	res := cc.Lore("doctor", "--json")
	rep := decode(res)
	if res.Code != 0 || rep.Status != "healthy" || rep.Exit != 0 {
		t.Fatalf("a fresh teammate clone is healthy: %s", res)
	}
	file := tblRules + "/" + id + ".json"
	cc.WriteFile(dataDir+"/"+file, "{broken")
	res = cc.Lore("doctor", "--json")
	rep = decode(res)
	if res.Code != 2 || rep.Status != "broken" || !strings.Contains(strings.Join(rep.Errors, "\n"), file) {
		t.Fatalf("a broken row file fails doctor (exit 2) naming %s: %s", file, res)
	}
}
