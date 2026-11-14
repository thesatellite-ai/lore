package app

// plainfiles_reads_test.go — no hidden magic (plain files sweep 3.3,
// 2026-11-12): a read command changes nothing a person owns and never
// pushes. A code repo linked to a git store (a bare repo on disk reached
// through url.insteadOf) gets a write (pushed, and the push names the
// store, the commit and the branch), then a write whose push fails (the
// remote is away), so a commit waits in the store clone. Every read then
// leaves the repo, its .git (config, hooks, info, lore's own folder,
// refs), the store clone under ~/.lore/stores and the remote exactly as
// they were; the first read that reaches the store says the commit waits
// and names `lore sync`. `lore sync` pushes it and says so.

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// pfMemoryID finds a memory id in command output.
var pfMemoryID = regexp.MustCompile(`mem_[0-9a-f]{32}`)

// pfPushLine is the line every store push prints.
var pfPushLine = regexp.MustCompile(`lore: pushed store notes: commit ([0-9a-f]{7}) to branch main`)

// pfLastPush is the commit of the last push line in s ("" when none).
func pfLastPush(s string) string {
	all := pfPushLine.FindAllStringSubmatch(s, -1)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1][1]
}

// pfSkip are paths a snapshot leaves out: git's own stat cache and fetch
// bookkeeping (a fetch is a read of the remote, and FETCH_HEAD and the
// fetch stamp only say when it ran), the store and live locks (they name who holds
// them while a command reads; they hold no data), the claim
// watch .git/lore/signals-seen.json (a reader must record when it first
// saw a teammate's claim before it may treat the claim as expired,
// LORE_TEAM_SPEC §1; `lore where` lists it) and lore's rebuildable repo
// cache, and the "said once" notes (waiting_said, live-waiting-said:
// what a read last told the person, so it does not repeat it, B3-13).
// remote-tip is fetch bookkeeping too: the remote tip a fetch saw, kept
// past a later prune so a wiped store is never refilled (B2-28); it must
// be written by the read's own fetch, the fetch that prunes.
func pfSkip(rel string) bool {
	base := filepath.Base(rel)
	switch {
	case base == "index" || base == "FETCH_HEAD" || base == "fetch-stamp" || base == "ORIG_HEAD" || base == "lock" || base == "live.lock" || base == "signals-seen.json" ||
		base == "waiting_said" || base == "live-waiting-said" || base == "remote-tip":
		return true
	case strings.HasPrefix(base, "lore-cache.db"):
		return true
	case strings.Contains(filepath.ToSlash(rel), ".git/logs/") || strings.Contains(filepath.ToSlash(rel), ".git/objects/"):
		return true
	}
	return false
}

// pfSnapshot hashes every file under dir (path, mode, content), leaving
// out pfSkip paths.
func pfSnapshot(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() || pfSkip(rel) {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		info, _ := d.Info()
		sum := sha256.Sum256(raw)
		lines = append(lines, hex.EncodeToString(sum[:8])+" "+info.Mode().String()+" "+filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}

// pfDiff is the lines only in a or only in b.
func pfDiff(a, b string) string {
	as, bs := strings.Split(a, "\n"), strings.Split(b, "\n")
	var out []string
	for _, l := range as {
		if !slices.Contains(bs, l) {
			out = append(out, "- "+l)
		}
	}
	for _, l := range bs {
		if !slices.Contains(as, l) {
			out = append(out, "+ "+l)
		}
	}
	return strings.Join(out, "\n")
}

func TestReadsNeverWriteOrPush(t *testing.T) {
	t.Parallel()
	w := newCLIWorld(t).person().author().unscoped()
	home := w.env["HOME"]
	hosts := w.path("hosts")
	bare := filepath.Join(hosts, "acme", "notes.git")
	if err := os.MkdirAll(bare, 0o700); err != nil {
		t.Fatal(err)
	}
	w.git(t, bare, "init", "-q", "--bare", "-b", "main")
	gitcfg := "[user]\n\tname = Pat Example\n\temail = pat@example.com\n[init]\n\tdefaultBranch = main\n[url \"" + filepath.ToSlash(hosts) + "/\"]\n\tinsteadOf = https://github.com/\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(gitcfg), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := w.path("repo")
	if err := os.MkdirAll(repo, 0o750); err != nil {
		t.Fatal(err)
	}
	w.git(t, repo, "init", "-q", "-b", "main")
	w.mustLore(t, repo, "init")
	w.mustLore(t, repo, "store", "init", "notes", "https://github.com/acme/notes.git", "--yes")
	// push mechanics: the store opts into push=now (C5: the default waits
	// for lore sync, and a read then names no waiting commit)
	w.mustLore(t, repo, "store", "set", "notes", "push=now")

	// a write pushes and says where: store, commit, branch
	code, out, errOut := w.lore(t, repo, "memory", "add", "--body", "the widget cache warms at boot", "--to", "notes")
	if code != 0 {
		t.Fatalf("memory add: exit %d\n%s\n%s", code, out, errOut)
	}
	m := pfLastPush(errOut)
	if m == "" {
		t.Fatalf("the write's push printed no store/commit/branch line:\n%s\n%s", out, errOut)
	}
	if tip := strings.TrimSpace(w.git(t, bare, "rev-parse", "--short=7", "main")); tip != m {
		t.Fatalf("the push line names commit %s; the remote's main is %s\n%s", m, tip, errOut)
	}
	memID := pfMemoryID.FindString(out + errOut)
	// the write names the row file it wrote, in lore's clone of the store
	if want := "lore: wrote " + filepath.Join(home, ".lore", "stores", "acme", "notes") + string(filepath.Separator); !strings.Contains(errOut, want) || !strings.Contains(errOut, memID+".json (store notes)") {
		t.Fatalf("the write does not name its row file (want %q… %s.json (store notes)):\n%s", want, memID, errOut)
	}

	// a write whose push fails leaves one commit waiting in the clone
	away := bare + ".away"
	if err := os.Rename(bare, away); err != nil {
		t.Fatal(err)
	}
	w.mustLore(t, repo, "memory", "add", "--body", "the ledger keeps integer cents", "--to", "notes")
	if err := os.Rename(away, bare); err != nil {
		t.Fatal(err)
	}

	stores := filepath.Join(home, ".lore", "stores")
	view := func() string {
		return "repo:\n" + pfSnapshot(t, repo) + "\nstores:\n" + pfSnapshot(t, stores) + "\nremote:\n" + w.git(t, bare, "for-each-ref")
	}
	before := view()
	reads := [][]string{
		{"memory", "list", "--store", "notes"}, {"memory", "list"}, {"task", "list"}, {"show", memID},
		{"search", "widget"}, {"whoami"}, {"doctor"}, {"sync", "status"}, {"store", "list"}, {"store", "show"},
		{"rule", "list"}, {"memory", "list", "--json"}, {"where"}, {"where", "--json"},
	}
	waited := false
	for _, args := range reads {
		code, out, errOut := w.lore(t, repo, args...)
		if code != 0 && args[0] != "doctor" { // doctor exits non-zero for its findings
			t.Fatalf("lore %v: exit %d\n%s\n%s", args, code, out, errOut)
		}
		if strings.Contains(errOut, "lore: pushed") {
			t.Fatalf("the read lore %v pushed:\n%s", args, errOut)
		}
		if strings.Contains(errOut, "1 local commit(s) wait to be pushed; a read never pushes: run `lore sync`") {
			waited = true
		}
		if after := view(); after != before {
			t.Fatalf("the read lore %v changed files or the remote:\n%s\nstderr:\n%s", args, pfDiff(before, after), errOut)
		}
	}
	if !waited {
		t.Fatal("no read said that a commit waits to be pushed and that `lore sync` sends it")
	}

	// lore sync pushes it and says so
	code, out, errOut = w.lore(t, repo, "sync")
	if code != 0 {
		t.Fatalf("sync: exit %d\n%s\n%s", code, out, errOut)
	}
	m = pfLastPush(errOut)
	if m == "" {
		t.Fatalf("lore sync printed no push line:\n%s\n%s", out, errOut)
	}
	if strings.Contains(errOut, "a read never pushes") {
		t.Fatalf("lore sync, which pushes, told the person to run lore sync:\n%s", errOut)
	}
	if tip := strings.TrimSpace(w.git(t, bare, "rev-parse", "--short=7", "main")); tip != m {
		t.Fatalf("sync's push line names commit %s; the remote's main is %s\n%s", m, tip, errOut)
	}
}
