//go:build bench

package storegrowth

// harness_test.go — the timing harness (task bench:store; build tag
// bench, manual only: never in the gate or on push). Per size it seeds a
// team store with a real lore (org, roster of three people and an agent,
// one row of every table), generates N commits into its remote with
// Import, then times lore on it: first clone, fetch of 100 new commits,
// write + push, ask and search cold and warm, the authority replay cold
// and warm, a 50-row batch apply and undo, row history and trash from
// history, and the clone's disk. Every lore call runs in a fixed
// environment (no variable of the caller's leaks in, LORE_HUMAN=1) under a
// temp HOME deleted at exit; remotes are file:// bare repos reached
// through url.insteadOf (uploadpack.allowFilter on, for item 3 later).
//
// Settings (environment):
//
//	LORE_BENCH_BIN       the lore binary (required; the task builds it)
//	STOREGROWTH_COMMITS  sizes, comma separated (default 1000)
//	STOREGROWTH_REPEAT   runs per timing (default 3; sizes of 1M and up run once)
//	STOREGROWTH_OUT      report folder (default tmp/bench at the module root)
//	STOREGROWTH_GENERATE_ONLY=1  seed and generate, record the generation
//	                     time, the remote's size and the org file log on
//	                     it, and time nothing else (the 1M acceptance run)
//	STOREGROWTH_KEEP=<dir>  make each world in a new folder under dir and
//	                     leave it there (to profile a command again on it)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rowsPerFolder is how many new rows a store folder takes before the
// generator starts the next one (the busiest measured folder held about
// 6,000 files).
const rowsPerFolder = 6000

// storeURL is the team store's url as people paste it.
const storeURL = "git@github.com:acme-co/agency.git"

// world is one size's sandbox.
type world struct {
	t     *testing.T
	root  string
	bin   string
	home  string
	app   string
	bare  string
	env   []string
	clone string
}

func TestBenchStoreGrowth(t *testing.T) {
	bin := os.Getenv("LORE_BENCH_BIN")
	if bin == "" {
		t.Skip("LORE_BENCH_BIN is not set (task bench:store builds lore and sets it)")
	}
	sizes := []int{1000}
	if s := os.Getenv("STOREGROWTH_COMMITS"); s != "" {
		sizes = nil
		for _, f := range strings.Split(s, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(f))
			if err != nil || n < 100 {
				t.Fatalf("STOREGROWTH_COMMITS: %q is not a size of at least 100", f)
			}
			sizes = append(sizes, n)
		}
	}
	repeat := 3
	if s := os.Getenv("STOREGROWTH_REPEAT"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			t.Fatalf("STOREGROWTH_REPEAT: %q", s)
		}
		repeat = n
	}
	out := os.Getenv("STOREGROWTH_OUT")
	if out == "" {
		out = filepath.Join("..", "..", "..", "tmp", "bench")
	}
	out, _ = filepath.Abs(out)
	sha := "dev"
	if b, err := exec.Command("git", "rev-parse", "--short=9", "HEAD").Output(); err == nil {
		sha = strings.TrimSpace(string(b))
	}
	for _, n := range sizes {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			r := repeat
			if n >= 1000000 {
				r = 1
			}
			run := benchSize(t, bin, n, r)
			run.Sha = sha
			path, err := run.Write(out)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("report: %s\n%s", path, run.Markdown())
		})
	}
}

// benchSize seeds, generates and times one size.
func benchSize(t *testing.T, bin string, n, repeat int) Run {
	w := newWorld(t, bin)
	plan := w.seed(n)
	run := Run{Commits: n, When: time.Now()}

	t0 := time.Now()
	st, err := Import(context.Background(), w.bare, plan)
	if err != nil {
		t.Fatal(err)
	}
	run.Generated = st
	run.Samples = append(run.Samples, Sample{Name: "Generate (fast-import)", Runs: []time.Duration{time.Since(t0)}, Note: "the remote's history, not a lore path"})
	run.Samples = append(run.Samples, Sample{Name: "Remote size", Value: w.du(w.bare)})
	if os.Getenv("STOREGROWTH_GENERATE_ONLY") == "1" {
		org := Sample{Name: "Org file log on the remote (proxy for acceptedOrgFile)"}
		org.add(w.timeGit(w.bare, "log", "--format=%H", "main", "--", "org/lore-org.yaml"))
		count := Sample{Name: "Commits on main (rev-list --count)"}
		r := w.timeGit(w.bare, "rev-list", "--count", "main")
		count.add(r)
		count.Value = strings.TrimSpace(r.out) + " in " + FormatDuration(r.wall)
		run.Samples = append(run.Samples, org, count)
		return run
	}

	// first clone: lore makes the clone again on the next sync
	clone := Sample{Name: "First clone (lore sync, cold)", Note: "the clone is removed, `lore sync` clones it again and replays the authority"}
	for range repeat {
		if err := os.RemoveAll(w.clone); err != nil {
			t.Fatal(err)
		}
		clone.add(w.lore("sync"))
	}
	run.Samples = append(run.Samples, clone)
	run.Samples = append(run.Samples, w.disk("Clone disk after first clone")...)
	run.Samples = append(run.Samples, w.held())

	// fetch of 100 new remote commits
	fetch := Sample{Name: "Fetch +100 (lore sync)", Note: "100 commits by teammates land on the remote, then `lore sync`"}
	for i := range repeat {
		tail := plan
		tail.Tail, tail.Commits, tail.Seed = true, 100, plan.Seed+int64(i)+1
		tail.Start = plan.Start.Add(time.Duration(n+(i+1)*200) * plan.Step)
		if _, err := Import(context.Background(), w.bare, tail); err != nil {
			t.Fatal(err)
		}
		fetch.add(w.lore("sync"))
	}
	run.Samples = append(run.Samples, fetch)

	// write + push, then ask and search cold (the store just changed) and warm
	write, push := Sample{Name: "Write (lore memory add)"}, Sample{Name: "Push (lore sync)"}
	askCold, askWarm := Sample{Name: "lore ask, cold"}, Sample{Name: "lore ask, warm"}
	searchCold, searchWarm := Sample{Name: "lore search, cold"}, Sample{Name: "lore search, warm"}
	for i := range repeat {
		write.add(w.lore("memory", "add", "--body", fmt.Sprintf("bench write %d: rollback the invoice export before the release", i), "--to", "agency", "--yes"))
		push.add(w.lore("sync"))
		askCold.add(w.lore("ask", "how do we rollback the invoice export"))
		askWarm.add(w.lore("ask", "how do we rollback the invoice export"))
		w.lore("memory", "add", "--body", fmt.Sprintf("bench note %d: the webhook queue retries", i), "--to", "agency", "--yes")
		searchCold.add(w.lore("search", "webhook"))
		searchWarm.add(w.lore("search", "webhook"))
	}
	run.Samples = append(run.Samples, write, push, askCold, askWarm, searchCold, searchWarm)

	// authority replay: cold without the saved replay, warm with it. A
	// read answered from the row cache (memory list, ask) does not replay,
	// so this times `lore sync` with nothing new, which always judges HEAD.
	authCold := Sample{Name: "Authority cold (lore sync, nothing new, no saved replay)"}
	authWarm := Sample{Name: "Authority warm (lore sync, nothing new)"}
	for range repeat {
		w.dropAuthority()
		authCold.add(w.lore("sync"))
		authWarm.add(w.lore("sync"))
	}
	// the merge a sync makes: a teammate's commit merged onto the clone's
	// HEAD (devco:D-999 carries the saved replay over the merged commits;
	// before, every merge replayed the whole history)
	authMerge := Sample{Name: "Authority after a sync merge (lore sync)", Note: "a side commit merged --no-ff onto the clone's HEAD by plain git, then `lore sync` (judges HEAD, pushes the 2 commits)"}
	for i := range repeat {
		side := fmt.Sprintf("bench-side-%d", i)
		w.git(w.clone, "checkout", "-q", "-b", side, "HEAD~1")
		mustWrite(t, filepath.Join(w.clone, "bench", side+".txt"), side+"\n")
		w.git(w.clone, "add", "bench")
		w.git(w.clone, "-c", "user.name=Bench", "-c", "user.email=bench@example.invalid", "commit", "-q", "--no-verify", "-m", side)
		w.git(w.clone, "checkout", "-q", "main")
		w.git(w.clone, "-c", "user.name=Bench", "-c", "user.email=bench@example.invalid", "merge", "-q", "--no-ff", "--no-edit", side)
		w.git(w.clone, "branch", "-q", "-D", side)
		authMerge.add(w.lore("sync"))
	}
	run.Samples = append(run.Samples, authCold, authWarm, authMerge)
	org := Sample{Name: "Org file log (proxy for acceptedOrgFile)", Note: "the path-limited full-history `git log` it runs on every core open"}
	for range repeat {
		org.add(w.timeGit(w.clone, "log", "--format=%H", "main", "--", "org/lore-org.yaml"))
	}
	run.Samples = append(run.Samples, org)

	// a 50-row batch: apply and undo
	apply, undo := Sample{Name: "Batch apply 50 rows"}, Sample{Name: "Batch undo 50 rows"}
	for i := range repeat {
		file := filepath.Join(w.root, fmt.Sprintf("batch-%d.jsonl", i))
		var b strings.Builder
		for j := range 50 {
			fmt.Fprintf(&b, `{"op":"memory add","input":{"body":"batch %d row %d: the cache index","to":"agency"}}`+"\n", i, j)
		}
		mustWrite(t, file, b.String())
		res := w.lore("batch", "apply", "--file", file, "--yes")
		apply.add(res)
		id := regexp.MustCompile(`bat_[0-9a-f]{32}`).FindString(res.out)
		if id == "" {
			t.Errorf("batch apply printed no batch id:\n%s", res.out)
			continue
		}
		undo.add(w.lore("batch", "undo", "--id", id, "--yes"))
	}
	run.Samples = append(run.Samples, apply, undo)
	run.Samples = append(run.Samples, w.fewerCommits(100, 20, 500)...)
	run.Samples = append(run.Samples, w.growthPerWrites(100, 20)...)
	run.Samples = append(run.Samples, Sample{Name: "Land / promote a layer", Note: "not timed yet: needs a layer flow in the harness (spec item 1, LEFT)"})

	// history views
	hist := Sample{Name: "lore task history --git (an edited task)"}
	if id := w.editedTask(); id != "" {
		for range repeat {
			hist.add(w.lore("task", "history", "--id", id, "--git"))
		}
	} else {
		hist.Note = "no edited task in the generated history"
	}
	trash := Sample{Name: "lore trash list --from-history"}
	for range repeat {
		trash.add(w.lore("trash", "list", "--from-history"))
	}
	run.Samples = append(run.Samples, hist, trash)
	run.Samples = append(run.Samples, w.disk("Clone disk at the end")...)
	run.Samples = append(run.Samples, w.maintenance(repeat)...)
	return run
}

// maintenance times store growth SG-2 (devco:D-996): the pass on the clone
// as the run left it, the clone's disk after it, the path-limited reads
// the commit-graph's changed-path filters speed up, and a write while a
// pass runs beside it (the pass must never slow a command by more than 5%).
func (w *world) maintenance(repeat int) []Sample {
	var out []Sample
	pass := Sample{Name: "Maintenance pass (lore store maintain --all)", Note: "repack of the loose objects and small packs, prune, commit-graph with changed-path filters"}
	r := w.lore("store", "maintain", "--all")
	pass.add(r)
	pass.Value = firstLine(r.out)
	out = append(out, pass)
	out = append(out, w.disk("Clone disk after the pass")...)
	hist := Sample{Name: "lore task history --git after the pass"}
	if id := w.editedTask(); id != "" {
		for range repeat {
			hist.add(w.lore("task", "history", "--id", id, "--git"))
		}
	}
	org := Sample{Name: "Org file log after the pass"}
	auth := Sample{Name: "Authority cold after the pass"}
	for range repeat {
		org.add(w.timeGit(w.clone, "log", "--format=%H", "main", "--", "org/lore-org.yaml"))
		w.dropAuthority()
		auth.add(w.lore("sync"))
	}
	out = append(out, hist, org, auth)
	// a write alone, then a write while a full pass runs (the commit-graph
	// is dropped first so the pass rewrites it for every commit)
	alone, beside := Sample{Name: "Write alone (lore memory add)"}, Sample{Name: "Write while a pass runs"}
	for i := range repeat {
		alone.add(w.lore("memory", "add", "--body", fmt.Sprintf("maint alone %d: the export cron", i), "--to", "agency", "--yes"))
		if err := os.RemoveAll(filepath.Join(w.clone, ".git", "objects", "info", "commit-graphs")); err != nil {
			w.t.Fatal(err)
		}
		cmd := exec.Command(w.loreBin(), "store", "maintain", "--all")
		cmd.Dir, cmd.Env = w.app, w.env
		if err := cmd.Start(); err != nil {
			w.t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond) // the pass has its lock and is writing
		beside.add(w.lore("memory", "add", "--body", fmt.Sprintf("maint beside %d: the export cron", i), "--to", "agency", "--yes"))
		_ = cmd.Wait() // its own result is the pass sample above
	}
	return append(out, alone, beside)
}

// result is one timed command.
type result struct {
	wall time.Duration
	over bool
	err  error
	out  string
}

// add records r in s.
func (s *Sample) add(r result) {
	if r.over {
		s.Over = true
		return
	}
	if r.err != nil {
		s.Note = strings.TrimSpace(s.Note + " (a run failed: " + firstLine(r.out) + ")")
		return
	}
	s.Runs = append(s.Runs, r.wall)
}

// firstLine is the first non-empty line of s, cut to 120 bytes.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l[:min(len(l), 120)]
		}
	}
	return ""
}

// newWorld makes the sandbox: a bin folder with lore, mira's home with
// her git identity and the url rewrite, and the two bare remotes.
func newWorld(t *testing.T, bin string) *world {
	t.Helper()
	root := t.TempDir()
	if keep := os.Getenv("STOREGROWTH_KEEP"); keep != "" {
		// a kept world outlives the run, for profiling (doctor_test.go)
		if err := os.MkdirAll(keep, 0o755); err != nil {
			t.Fatal(err)
		}
		d, err := os.MkdirTemp(keep, "world-")
		if err != nil {
			t.Fatal(err)
		}
		root = d
	}
	w := &world{t: t, root: root, bin: filepath.Join(root, "bin"), home: filepath.Join(root, "mira")}
	t.Cleanup(w.stopReadServers) // runs before the temp folder goes (cleanups run last-in first)
	for _, d := range []string{w.bin, w.home, filepath.Join(root, "tmp"), filepath.Join(root, "hosts", "github.com", "acme-co")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	abs, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(abs, filepath.Join(w.bin, "lore")); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(w.home, ".gitconfig"), fmt.Sprintf("[user]\n\tname = Mira Castell\n\temail = mira@acme.test\n[init]\n\tdefaultBranch = main\n[url \"file://%s/hosts/github.com/\"]\n\tinsteadOf = git@github.com:\n", root))
	w.env = []string{
		"HOME=" + w.home,
		"PATH=" + w.bin + ":/usr/local/bin:/usr/bin:/bin",
		"TMPDIR=" + filepath.Join(root, "tmp"),
		"TZ=UTC", "LANG=C.UTF-8", "NO_COLOR=1", "LORE_HUMAN=1",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(w.home, ".gitconfig"), "GIT_CONFIG_NOSYSTEM=1",
	}
	w.bare = filepath.Join(root, "hosts", "github.com", "acme-co", "agency.git")
	appBare := filepath.Join(root, "hosts", "github.com", "acme-co", "app.git")
	for _, b := range []string{w.bare, appBare} {
		w.git(root, "init", "-q", "--bare", "-b", "main", b)
	}
	w.git(w.bare, "config", "uploadpack.allowFilter", "true")
	w.app = filepath.Join(w.home, "code", "app")
	mustWrite(t, filepath.Join(w.app, "README.md"), "# app\n")
	w.git(w.app, "init", "-q", "-b", "main")
	w.git(w.app, "add", "-A")
	w.git(w.app, "commit", "-q", "-m", "init")
	w.git(w.app, "remote", "add", "origin", "git@github.com:acme-co/app.git")
	w.git(w.app, "push", "-q", "origin", "main")
	return w
}

// seed makes the team store with lore and returns the plan that
// continues it with n commits.
func (w *world) seed(n int) Plan {
	t := w.t
	w.must("store", "init", "--name", "agency", "--url", storeURL)
	w.must("org", "create", "--org", "acme-co", "--store", storeURL, "--own", "github.com/acme-co/*", "--key", "human:mira@acme.test", "--name", "Mira", "--handle", "mira", "--yes")
	w.must("init")
	w.must("member", "add", "--email", "tomas@acme.test", "--name", "Tomas", "--handle", "tomas", "--role", "member", "--yes")
	w.must("member", "add", "--email", "erin@acme.test", "--name", "Erin", "--handle", "erin", "--role", "member", "--yes")
	w.must("actor", "register", "--kind", "agent", "--label", "nightly-agent", "--yes")
	w.must("memory", "add", "--body", "deploys go out on tuesdays", "--to", "agency", "--yes")
	w.must("decision", "add", "--title", "use postgres", "--body", "one database for every service", "--to", "agency", "--yes")
	w.must("rule", "add", "--body", "never push on friday", "--to", "agency", "--yes")
	w.must("task", "add", "--title", "rotate the logs", "--to", "agency", "--yes")
	w.must("hotfix", "add", "--title", "pin the driver", "--body", "the v2 driver drops rows", "--to", "agency", "--yes")
	w.must("playbook", "add", "--title", "release", "--body", "1. tag 2. push", "--to", "agency", "--yes")
	w.clone = w.findClone()
	task := strings.TrimSuffix(filepath.Base(w.glob(filepath.Join(w.clone, "_store", "tasks", "*.json"))[0]), ".json")
	w.must("comment", "add", "--on-id", task, "--body", "looks good", "--yes")
	folders := []string{"_store"}
	for i := 1; i <= (n*14/10)/rowsPerFolder; i++ {
		name := fmt.Sprintf("proj_%04d", i)
		w.must("store", "folder", "add", "agency", name)
		folders = append(folders, name)
	}
	w.must("sync")

	p := Plan{
		Commits: n, Seed: 1, Step: 3 * time.Minute, Branch: "main",
		Folders: folders, RowsPerFolder: rowsPerFolder, OrgEvery: 5000,
		Templates: map[string][]byte{}, OrgFilePath: "org/lore-org.yaml",
	}
	p.Start = time.Now().Add(-time.Duration(n+1000) * p.Step)
	for table := range tableWeight {
		files := w.glob(filepath.Join(w.clone, "_store", table, "*.json"))
		if len(files) == 0 {
			t.Fatalf("the seed has no %s row", table)
		}
		p.Templates[table] = w.read(files[0])
	}
	p.OrgFile = w.read(filepath.Join(w.clone, p.OrgFilePath))
	type actor struct {
		ID, Kind, StableKey, DisplayName string
	}
	byEmail := map[string]actor{}
	for _, f := range w.glob(filepath.Join(w.clone, "org", "actors", "*.json")) {
		var a struct {
			ID          string `json:"id"`
			Kind        string `json:"kind"`
			StableKey   string `json:"stable_key"`
			DisplayName string `json:"display_name"`
			Handle      string `json:"handle"`
		}
		if err := json.Unmarshal(w.read(f), &a); err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.HasPrefix(a.StableKey, "human:"):
			byEmail[strings.TrimPrefix(a.StableKey, "human:")] = actor{ID: a.ID, Kind: a.Kind, DisplayName: a.DisplayName}
		case a.Kind == "agent" && strings.Contains(a.StableKey+a.Handle, "nightly-agent"):
			p.AgentActorID = a.ID
		}
	}
	for _, who := range []struct {
		name, email string
		admin       bool
	}{{"Mira Castell", "mira@acme.test", true}, {"Tomas", "tomas@acme.test", false}, {"Erin", "erin@acme.test", false}} {
		a, ok := byEmail[who.email]
		if !ok {
			t.Fatalf("%s is not on the seed's roster", who.email)
		}
		p.People = append(p.People, Person{Name: who.name, Email: who.email, ActorID: a.ID, Admin: who.admin})
	}
	for _, f := range w.glob(filepath.Join(w.clone, "org", "actor_periods", "*.json")) {
		b := w.read(f)
		if strings.Contains(string(b), `"actor_id": "`+p.People[1].ActorID+`"`) {
			p.PromotionPath, p.Promotion = strings.TrimPrefix(f, w.clone+"/"), b
		}
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return p
}

// fewerCommits measures store growth item 4 (devco:D-1000): writes synced
// every `every` writes reach the remote as one commit per sync (spec
// acceptance: 1,000 writes synced every 20 = at most 50), and a batch
// undo of undoRows rows is one commit. A count over the bound is an error.
func (w *world) fewerCommits(writes, every, undoRows int) []Sample {
	t := w.t
	count := func(dir, rev string) int {
		n, err := strconv.Atoi(strings.TrimSpace(w.git(dir, "rev-list", "--count", rev)))
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	w.lore("sync")
	before := count(w.bare, "main")
	push := Sample{Name: fmt.Sprintf("Push of %d writes (lore sync)", every), Note: "every write is a local commit; a push sends one commit per author (squash on)"}
	for i := range writes {
		w.lore("memory", "add", "--body", fmt.Sprintf("growth write %d: the invoice export runs nightly", i), "--to", "agency", "--yes")
		if (i+1)%every == 0 {
			push.add(w.lore("sync"))
		}
	}
	got, most := count(w.bare, "main")-before, (writes+every-1)/every
	if got > most {
		t.Errorf("%d writes synced every %d gave the remote %d commits, want at most %d", writes, every, got, most)
	}
	commits := Sample{Name: fmt.Sprintf("Remote commits for %d writes synced every %d", writes, every), Value: fmt.Sprintf("%d (at most %d; before squash on every push: %d)", got, most, writes)}
	file := filepath.Join(w.root, "undo-rows.jsonl")
	var b strings.Builder
	for j := range undoRows {
		fmt.Fprintf(&b, `{"op":"memory add","input":{"body":"undo row %d: the cache index","to":"agency"}}`+"\n", j)
	}
	mustWrite(t, file, b.String())
	res := w.lore("batch", "apply", "--file", file, "--yes")
	id := regexp.MustCompile(`bat_[0-9a-f]{32}`).FindString(res.out)
	if id == "" {
		t.Errorf("batch apply printed no batch id:\n%s", res.out)
		return []Sample{push, commits}
	}
	local := count(w.clone, "HEAD")
	w.lore("batch", "undo", "--id", id, "--yes")
	made := count(w.clone, "HEAD") - local
	if made != 1 {
		t.Errorf("a %d-row batch undo made %d commits, want 1", undoRows, made)
	}
	undo := Sample{Name: fmt.Sprintf("Commits for a %d-row batch undo", undoRows), Value: fmt.Sprintf("%d (before: %d)", made, undoRows)}
	w.lore("sync")
	return []Sample{push, commits, undo}
}

// growthPerWrites measures the spec §7 row ".git growth per 1,000 writes
// (packed)" (devco:D-1002): writes one-row writes synced every `every`,
// once with squash on (the default) and once with squash=off (one commit
// per write, the old behaviour), and reports how much the remote's and
// the clone's packed .git grew, scaled to 1,000 writes. Each side is
// repacked (git gc --prune=now) before and after, so loose objects do not
// count.
func (w *world) growthPerWrites(writes, every int) []Sample {
	set := func(mode string) {
		if r := w.lore("store", "set", "--store", "agency", "--settings", "squash="+mode); r.err != nil {
			w.t.Fatalf("store set squash=%s: %v\n%s", mode, r.err, r.out)
		}
		if r := w.lore("store", "show", "--store", "agency", "--json"); !strings.Contains(r.out, `"squash": "`+mode+`"`) {
			w.t.Fatalf("store show after squash=%s:\n%s", mode, r.out)
		}
	}
	round := func(mode string) Sample {
		set(mode)
		w.lore("sync")
		bare0, clone0 := w.packed(w.bare), w.packed(w.clone)
		for i := range writes {
			w.lore("memory", "add", "--body", fmt.Sprintf("growth %s %d: the widget export runs nightly", mode, i), "--to", "agency", "--yes")
			if (i+1)%every == 0 {
				w.lore("sync")
			}
		}
		w.lore("sync")
		bare1, clone1 := w.packed(w.bare), w.packed(w.clone)
		per := func(d int) int { return d * 1000 / writes }
		return Sample{
			Name: fmt.Sprintf(".git growth per 1,000 writes (squash %s, synced every %d)", mode, every),
			Value: fmt.Sprintf("remote +%d objects, %+d KB packed; clone +%d objects, %+d KB packed",
				per(bare1.objects-bare0.objects), per(bare1.kib-bare0.kib), per(clone1.objects-clone0.objects), per(clone1.kib-clone0.kib)),
			Note: fmt.Sprintf("%d writes, scaled; objects are exact, the packed size moves with how git gc deltifies the whole history (it can shrink)", writes),
		}
	}
	return []Sample{round("off"), round("on")} // ends on the default, squash on
}

// packedSize is a git dir's object count and size after a repack.
type packedSize struct{ objects, kib int }

// packed repacks the git dir at dir (git gc --prune=now) and returns its
// objects and their size in KiB (packs plus any loose object left).
func (w *world) packed(dir string) packedSize {
	w.git(dir, "gc", "--prune=now", "--quiet")
	cv := map[string]int{}
	for _, l := range strings.Split(w.git(dir, "count-objects", "-v"), "\n") {
		if k, v, ok := strings.Cut(l, ": "); ok {
			cv[k], _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	return packedSize{objects: cv["in-pack"] + cv["count"], kib: cv["size-pack"] + cv["size"]}
}

// held checks the two planted changes are still held back after the
// clone (a speed change that drops a check fails the bench).
func (w *world) held() Sample {
	r := w.lore("doctor")
	s := Sample{Name: "Held back after clone", Note: "the non-admin's org file edit and self-promotion, named by lore doctor"}
	org := strings.Contains(r.out, "lore-org.yaml: held back")
	promo := strings.Contains(r.out, "actor_periods") && strings.Contains(r.out, "makes its author an admin")
	s.Value = fmt.Sprintf("org file %v, self-promotion %v, %d held line(s)", org, promo, strings.Count(r.out, "held back"))
	if !org || !promo {
		w.t.Errorf("a planted change is no longer held back (org file %v, self-promotion %v):\n%s", org, promo, r.out)
	}
	return s
}

// editedTask is a generated task id that has an edit in the history.
func (w *world) editedTask() string {
	out := w.git(w.clone, "log", "-1", "--format=", "--name-only", "--diff-filter=M", "main", "--", "*/tasks/*.json")
	if out == "" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(strings.Fields(out)[0]), ".json")
}

// dropAuthority removes the saved roster replay, so the next read replays
// the whole history.
func (w *world) dropAuthority() {
	for _, f := range w.glob(filepath.Join(w.clone, ".git", "lore-authority.gob*")) {
		if err := os.Remove(f); err != nil {
			w.t.Fatal(err)
		}
	}
}

// disk is the clone's .git size and object counts.
func (w *world) disk(name string) []Sample {
	cv := map[string]string{}
	for _, l := range strings.Split(w.git(w.clone, "count-objects", "-v"), "\n") {
		if k, v, ok := strings.Cut(l, ": "); ok {
			cv[k] = v
		}
	}
	return []Sample{
		{Name: name + ": .git size", Value: w.du(filepath.Join(w.clone, ".git"))},
		{Name: name + ": packs, packed size", Value: cv["packs"] + " packs, " + kib(cv["size-pack"])},
		{Name: name + ": loose objects", Value: cv["count"] + " (" + kib(cv["size"]) + ")"},
	}
}

// kib prints a count-objects KiB value in MB.
func kib(v string) string {
	n, _ := strconv.Atoi(v)
	return fmt.Sprintf("%.1f MB", float64(n)/1024)
}

// du is a folder's size in MB.
func (w *world) du(dir string) string {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return fmt.Sprintf("%.1f MB", float64(total)/(1<<20))
}

// lore runs lore in the app repo and times it.
func (w *world) lore(args ...string) result {
	return w.timed(w.app, "lore", args...)
}

// must runs lore and fails the test when it fails.
func (w *world) must(args ...string) {
	w.t.Helper()
	if r := w.lore(args...); r.err != nil || r.over {
		w.t.Fatalf("lore %s: %v\n%s", strings.Join(args, " "), r.err, r.out)
	}
}

// timeGit times a git command.
func (w *world) timeGit(dir string, args ...string) result {
	return w.timed(dir, "git", args...)
}

// timed runs name in dir with the world's environment, at most Limit.
// lore is the world's own binary by its path: exec resolves a bare name
// with this test process's PATH, not cmd.Env's, so a bare "lore" ran the
// lore installed on the machine instead of the build under test.
func (w *world) timed(dir, name string, args ...string) result {
	ctx, cancel := context.WithTimeout(context.Background(), Limit)
	defer cancel()
	if name == "lore" {
		name = w.loreBin()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, w.env
	t0 := time.Now()
	out, err := cmd.CombinedOutput()
	r := result{wall: time.Since(t0), err: err, out: string(out)}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		r.over = true
	}
	if err != nil && !r.over {
		w.t.Logf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return r
}

// git runs git in dir with the world's environment and returns its output.
func (w *world) git(dir string, args ...string) string {
	w.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, w.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		w.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// stopReadServers stops the read servers (`lore serve-reads`) lore started
// for this world's home: they outlive the test by their idle time
// otherwise. Linux only (it reads /proc); elsewhere they idle out.
func (w *world) stopReadServers() {
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, e := range procs {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		environ, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if !strings.Contains(string(cmdline), "serve-reads") || !strings.Contains("\x00"+string(environ), "\x00HOME="+w.home+"\x00") {
			continue
		}
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	}
}

// findClone is lore's clone of the team store.
func (w *world) findClone() string {
	m := w.glob(filepath.Join(w.home, ".lore", "stores", "*", "agency"))
	if len(m) != 1 {
		w.t.Fatalf("want one clone of agency under ~/.lore/stores, found %v", m)
	}
	return m[0]
}

// glob is filepath.Glob that fails the test on a bad pattern.
func (w *world) glob(pattern string) []string {
	m, err := filepath.Glob(pattern)
	if err != nil {
		w.t.Fatal(err)
	}
	return m
}

// read is os.ReadFile that fails the test.
func (w *world) read(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		w.t.Fatal(err)
	}
	return b
}

// loreBin is the world's lore (the build under test, linked into its bin
// folder), by path.
func (w *world) loreBin() string { return filepath.Join(w.bin, "lore") }

// TestBenchRunsTheBuildUnderTest: the harness runs the world's own lore
// (LORE_BENCH_BIN, linked into its bin folder), never the lore on the
// caller's PATH: exec resolves a bare name with the test process's PATH,
// so every timing was of the installed release (devco:D-996).
func TestBenchRunsTheBuildUnderTest(t *testing.T) {
	stub := filepath.Join(t.TempDir(), "stub")
	mustWrite(t, stub, "#!/bin/sh\necho build-under-test \"$@\"\n")
	if err := os.Chmod(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, stub)
	r := w.lore("version")
	if r.err != nil || strings.TrimSpace(r.out) != "build-under-test version" {
		t.Fatalf("w.lore ran %q (%v), want the world's binary", r.out, r.err)
	}
}
