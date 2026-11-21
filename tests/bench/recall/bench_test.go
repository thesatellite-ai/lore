//go:build bench

package recall

// bench_test.go — the run against a lore binary (task bench:recall; build
// tag bench, so the plain go test ./... and the gate do not run it yet:
// the coordinator turns it on in task check once it passes).
//
// The store is written the way a person's migrated notes end up: lore
// writes the store, the kinds and fields, the resources (one resource
// import per kind) and the first row of every folder; the other ~6,000
// rows are row files copied from those first rows and committed into the
// store clone with plain git, the shape a migrated or adopted folder has
// (rows lore did not write itself, which its cache must index on read).

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Environment knobs (tests/bench/recall/README.md lists them).
const (
	envBin    = "LORE_BENCH_BIN"          // the lore binary to measure (default: build ./cmd/lore)
	envNow    = "LORE_BENCH_NOW"          // the pinned now, YYYY-MM-DD (default: today)
	envSeed   = "LORE_BENCH_SEED"         // the data set's seed (default 46)
	envReport = "LORE_BENCH_REPORT"       // the report file (default <repo>/tmp/bench/recall.md)
	envDir    = "LORE_BENCH_DIR"          // keep the store and tree here (default: a temp folder)
	envLimit  = "LORE_BENCH_CALL_TIMEOUT" // a question's lore call is stopped after this (default 20s)
)

// defaultLimit bounds one lore call of a question: far above every bar, so
// a stopped call changes no verdict, and a slow lore still ends the run.
const defaultLimit = 20 * time.Second

// benchEnv runs commands as the bench's person: own HOME, git identity,
// the measured lore first on PATH, no agent or color in the environment.
type benchEnv struct {
	t     *testing.T
	bin   string
	home  string
	env   []string
	limit time.Duration // a question's call is stopped after this
}

// call is one lore run.
type call struct {
	out      string // stdout + stderr
	code     int
	wall     time.Duration
	cpu      time.Duration // user + system time of the process
	timedOut bool
}

// run runs one lore command from the home folder; limit > 0 stops it after
// that long.
func (e *benchEnv) run(limit time.Duration, args ...string) call {
	e.t.Helper()
	ctx := context.Background()
	if limit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limit)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, e.bin, args...)
	cmd.Dir, cmd.Env = e.home, e.env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	start := time.Now()
	err := cmd.Run()
	c := call{out: out.String(), wall: time.Since(start), timedOut: ctx.Err() != nil}
	if ps := cmd.ProcessState; ps != nil {
		c.cpu = ps.UserTime() + ps.SystemTime()
	}
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok && !c.timedOut {
			e.t.Fatalf("lore %s: %v", strings.Join(args, " "), err)
		}
		c.code = -1
		if ok {
			c.code = ee.ExitCode()
		}
	}
	return c
}

// lore runs a command with no time limit.
func (e *benchEnv) lore(args ...string) (string, int, time.Duration) {
	e.t.Helper()
	c := e.run(0, args...)
	return c.out, c.code, c.wall
}

// must runs a setup command that has to work.
func (e *benchEnv) must(args ...string) string {
	e.t.Helper()
	out, code, _ := e.lore(args...)
	if code != 0 {
		e.t.Fatalf("setup: lore %s: exit %d\n%s", strings.Join(args, " "), code, out)
	}
	return out
}

// git runs git in dir.
func (e *benchEnv) git(dir string, args ...string) {
	e.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, e.env
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// repoRoot is the module root (the folder holding go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			t.Fatal("no go.mod above the bench")
		}
		dir = up
	}
}

// prepared is a loaded run: the store written, the tree on disk, the
// first read done. TestBenchRecall and TestBenchAgent share it.
type prepared struct {
	root, dir, home, tree, side, bare string
	ds                                *Dataset
	e                                 *benchEnv
	seed                              uint64
	useRG                             bool
	started                           time.Time
	load                              time.Duration
	version                           string
}

// prepare reads the environment knobs, writes the store and the tree and
// makes the first read (every folder's cache built from nothing).
func prepare(t *testing.T) *prepared {
	t.Helper()
	started := time.Now()
	root := repoRoot(t)
	now := time.Now().UTC()
	if v := os.Getenv(envNow); v != "" {
		p, err := time.Parse(time.DateOnly, v)
		if err != nil {
			t.Fatalf("%s=%q: want YYYY-MM-DD: %v", envNow, v, err)
		}
		now = p
	}
	seed := uint64(DefaultSeed)
	if v := os.Getenv(envSeed); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			t.Fatalf("%s=%q: %v", envSeed, v, err)
		}
		seed = n
	}
	dir := os.Getenv(envDir)
	if dir == "" {
		dir = t.TempDir()
	} else if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	bin := os.Getenv(envBin)
	if bin == "" {
		bin = filepath.Join(dir, "bin", "lore")
		build := exec.Command("go", "build", "-o", bin, "./cmd/lore")
		build.Dir, build.Env = root, append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build lore: %v\n%s", err, out)
		}
	}
	bin, _ = filepath.Abs(bin)
	ds := Generate(seed, now)
	home, tree := filepath.Join(dir, "home"), filepath.Join(dir, "tree")
	for _, d := range []string{home, tree, filepath.Join(dir, "srv")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = Pia Holm\n\temail = pia@notes.example\n[init]\n\tdefaultBranch = main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := os.Getenv("TMPDIR")
	if tmp == "" {
		tmp = os.TempDir()
	}
	limit := defaultLimit
	if v := os.Getenv(envLimit); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatalf("%s=%q: %v", envLimit, v, err)
		}
		limit = d
	}
	e := &benchEnv{t: t, bin: bin, home: home, limit: limit, env: HarnessEnv(home, filepath.Dir(bin), tmp, ds.Now)}
	useRG := false
	if _, err := exec.LookPath("rg"); err == nil {
		useRG = true
	}

	loadStart := time.Now()
	bare, side := filepath.Join(dir, "srv", "me.git"), filepath.Join(dir, "side")
	writeStore(t, e, ds, bare, side)
	load := time.Since(loadStart)
	if err := ds.WriteTree(tree); err != nil {
		t.Fatal(err)
	}
	version, _, _ := e.lore("--version")
	// the first read after the load builds every folder's cache: timed and
	// reported, not a question
	_, _, first := e.lore("search", "--store", "me", "--query", "warmup")
	t.Logf("store loaded in %s; the next read (search) %d ms", load.Round(time.Millisecond), first.Milliseconds())

	return &prepared{root: root, dir: dir, home: home, tree: tree, side: side, bare: bare, ds: ds, e: e, seed: seed, useRG: useRG, started: started, load: load, version: version}
}

func TestBenchRecall(t *testing.T) {
	p := prepare(t)
	root, home, tree, side, bare, ds, e, seed, useRG, started, load, version := p.root, p.home, p.tree, p.side, p.bare, p.ds, p.e, p.seed, p.useRG, p.started, p.load, p.version
	qs := ds.Questions(useRG)
	clone := storeClone(t, home)
	var rs []Result
	boxLoad := [2]string{BoxLoad()}
	for i, q := range qs {
		r := Result{Q: q}
		touchStore(t, e, clone, side, bare, i)
		cold := e.run(e.limit, q.Lore...)
		// the warm time is the median of WarmRepeats warm calls (H-106):
		// one call's wall time on a shared box can include waiting for a
		// core; the answer is graded from the first warm call, and any
		// warm call that fails or is stopped makes the answer wrong
		warms := make([]call, WarmRepeats)
		walls, cpus := make([]time.Duration, WarmRepeats), make([]time.Duration, WarmRepeats)
		for k := range warms {
			warms[k] = e.run(e.limit, q.Lore...)
			walls[k], cpus[k] = warms[k].wall, warms[k].cpu
		}
		warm := warms[0]
		r.Lore.Calls, r.Lore.Cold, r.Lore.Warm, r.Lore.WarmRuns, r.Lore.Chars, r.Lore.Exit = 1, cold.wall, WarmOf(walls), walls, len(warm.out), warm.code
		r.Lore.ColdCPU, r.Lore.WarmCPU = cold.cpu, WarmOf(cpus)
		r.Lore.Correct, r.Lore.Missing = Grade(q, warm.out)
		for _, w := range warms {
			if w.timedOut {
				r.Lore.Correct = false
				r.Lore.Missing = append([]string{fmt.Sprintf("stopped after %s", e.limit)}, r.Lore.Missing...)
				break
			}
			if w.code != 0 {
				r.Lore.Correct, r.Lore.Exit = false, w.code
				r.Lore.Missing = append([]string{fmt.Sprintf("exit %d: %s", w.code, firstLine(w.out))}, r.Lore.Missing...)
				break
			}
		}
		// the accuracy grade reads the same call's structured answer (a
		// third run, not timed: the agent's one call is the text one)
		js := e.run(e.limit, append(slices.Clone(q.Lore), "--json")...)
		switch items, err := Items([]byte(js.out)); {
		case js.timedOut || js.code != 0:
			r.Lore.AccErr = fmt.Sprintf("--json exit %d: %s", js.code, firstLine(js.out))
		case err != nil:
			r.Lore.AccErr = err.Error()
		default:
			r.Lore.Acc = GradeAccuracy(q, items)
		}
		t.Logf("%s cold %d ms (cpu %d), warm %d ms (runs %s, cpu %d), correct %v, recall@3 %v, top-1 %v, stale %d, noise %d %s", q.ID, cold.wall.Milliseconds(), cold.cpu.Milliseconds(), r.Lore.Warm.Milliseconds(), runsMS(r.Lore), r.Lore.WarmCPU.Milliseconds(), r.Lore.Correct, r.Lore.Acc.Recall, r.Lore.Acc.Top1, len(r.Lore.Acc.Stale), r.Lore.Acc.Noise, r.Lore.AccErr)
		var all strings.Builder
		for _, c := range q.Grep {
			cmd := exec.Command("bash", "-c", c)
			cmd.Dir, cmd.Env = tree, e.env
			var buf bytes.Buffer
			cmd.Stdout, cmd.Stderr = &buf, &buf
			start := time.Now()
			_ = cmd.Run() // a search that finds nothing exits 1: its (empty) output is the answer
			r.Grep.Wall += time.Since(start)
			r.Grep.Calls++
			r.Grep.Chars += buf.Len()
			all.Write(buf.Bytes())
		}
		r.Grep.Correct, r.Grep.Missing = Grade(q, all.String())
		r.Grep.Present, r.Grep.Stale = GrepAccuracy(q, all.String())
		rs = append(rs, r)
	}
	boxLoad[1] = BoxLoad()

	tool := "grep (rg is not installed)"
	if useRG {
		tool = "rg"
	}
	report := Report(RunInfo{Now: ds.Now, Seed: seed, Rows: ds.Rows(), Lore: strings.TrimSpace(version), GrepTool: tool, Started: started, Load: load, BoxLoad: boxLoad}, rs)
	path := os.Getenv(envReport)
	if path == "" {
		path = filepath.Join(root, "tmp", "bench", "recall.md")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(report), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Summarize(rs)
	t.Logf("report: %s\nsummary: %s", path, s.Line())
	if misses := Misses(rs); len(misses) > 0 {
		t.Errorf("lore does not beat grep: %d bar(s) missed (report %s):\n  %s", len(misses), path, strings.Join(misses, "\n  "))
	}
}

// firstLine is the first line of s.
func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// storeClone is lore's clone of the personal store.
func storeClone(t *testing.T, home string) string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(home, ".lore", "stores", "*", "me"))
	if len(m) != 1 {
		t.Fatalf("want one clone of store me under ~/.lore/stores, found %v", m)
	}
	return m[0]
}

// touchStore changes the store before a question's cold call: one more
// feed event, written on another machine (the side clone) and brought
// into lore's clone the way a sync brings it.
func touchStore(t *testing.T, e *benchEnv, clone, side, bare string, i int) {
	t.Helper()
	tpl := templateRow(t, filepath.Join(side, FolderFeeds, "memories"))
	at := time.Now().UTC()
	id := rowID("mem", at, uint64(900000+i))
	doc := tpl.with(id, at)
	doc.set("body", fmt.Sprintf("Feed heartbeat %d: the bench touched the store.", i))
	doc.write(t, filepath.Join(side, FolderFeeds, "memories", id+".json"))
	syncIn(t, e, clone, side, bare, "bench: heartbeat "+strconv.Itoa(i))
}

// syncIn commits the side clone's new rows, pushes them to the store's
// remote and fast-forwards lore's clone to them: rows from another
// machine, as a sync brings them (no commit in lore's clone, so its own
// commit hook does not run).
func syncIn(t *testing.T, e *benchEnv, clone, side, bare, msg string) {
	t.Helper()
	e.git(side, "add", "-A")
	e.git(side, "commit", "-q", "-m", msg)
	e.git(side, "push", "-q", "origin", "HEAD:main")
	e.git(clone, "fetch", "-q", bare, "main")
	e.git(clone, "merge", "-q", "--ff-only", "FETCH_HEAD")
}

// writeStore makes the personal store and fills it.
func writeStore(t *testing.T, e *benchEnv, ds *Dataset, bare, side string) {
	t.Helper()
	e.git(filepath.Dir(bare), "init", "-q", "--bare", "-b", "main", bare)
	e.must("store", "init", "--name", "me", "--url", bare, "--yes")
	work := "me/" + FolderWork
	e.must("memory", "add", "--body", ds.Notes[0].Body, "--at", day(ds.Notes[0].Day), "--to", work, "--create", "--yes")

	// kinds, fields and resources, through lore (one import per kind)
	kinds := []struct {
		kind   string
		fields []string
		rows   [][]string
	}{
		{KindCompany, []string{"industry", "city"}, nil},
		{KindPerson, []string{"role", "company", "nickname", "email"}, nil},
		{KindProject, []string{"owner", "company"}, nil},
	}
	for _, c := range ds.Companies {
		kinds[0].rows = append(kinds[0].rows, []string{c.Name, c.Industry, c.City})
	}
	for _, p := range ds.People {
		company := p.Company
		if p.Former != "" {
			company = p.Former // the move is an edit below, so the history holds it
		}
		kinds[1].rows = append(kinds[1].rows, []string{p.Name, p.Role, company, p.Nickname, p.Email})
	}
	for _, p := range ds.Projects {
		kinds[2].rows = append(kinds[2].rows, []string{p.Name, p.Owner, p.Company})
	}
	for _, k := range kinds {
		e.must("resource", "kind", "add", "--kind", k.kind, "--name-format", "dns", "--to", work, "--yes")
		for _, f := range k.fields {
			e.must("resource", "field", "add", "--kind", k.kind, "--field", f, "--type", "text", "--to", work, "--yes")
		}
		var b bytes.Buffer
		w := csv.NewWriter(&b)
		_ = w.Write(append([]string{"name"}, k.fields...))
		_ = w.WriteAll(k.rows)
		file := filepath.Join(e.home, k.kind+".csv")
		if err := os.WriteFile(file, b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		e.must("resource", "import", "--file", file, "--kind", k.kind, "--to", work, "--yes")
	}
	for _, p := range ds.People {
		if p.Former != "" {
			e.must("resource", "edit", "--id", p.Name, "--set", "company="+p.Company, "--store", work, "--yes")
		}
	}

	// the first row of every other folder, and the first task
	life := firstOf(ds.Notes, FolderLife)
	e.must("memory", "add", "--body", life.Body, "--at", day(life.Day), "--to", "me/"+FolderLife, "--create", "--yes")
	j := ds.Journal[0]
	e.must("memory", "add", "--body", j.Body(), "--at", day(j.Day), "--meta", "day:date="+day(j.Day), "--to", "me/"+FolderJournal, "--create", "--yes")
	f := ds.Feeds[0]
	e.must("memory", "add", "--body", f.Body, "--at", day(f.Day), "--source", "imported", "--to", "me/"+FolderFeeds, "--create", "--yes")
	clone := storeClone(t, e.home)
	res := resourceIDs(t, filepath.Join(clone, FolderWork, "resources"))
	t0 := ds.Tasks[0]
	e.must("task", "add", "--title", t0.Title, "--meta", "owed_by="+t0.OwedBy, "--meta", "owed_to="+t0.OwedTo, "--ref", res[t0.OwedTo], "--relation", "owed_to", "--to", work, "--yes")

	// the rest as row files copied from those, written in a side clone and
	// brought in with one sync
	e.git(clone, "push", "-q", bare, "HEAD:main")
	e.git(filepath.Dir(side), "clone", "-q", bare, side)
	tpl := map[string]rowDoc{}
	for _, fo := range []string{FolderWork, FolderLife, FolderJournal, FolderFeeds} {
		tpl[fo] = templateRow(t, filepath.Join(side, fo, "memories"))
	}
	n := uint64(0)
	next := func(prefix string, at time.Time) string { n++; return rowID(prefix, at, n) }
	for _, note := range ds.Notes {
		if note.Body == ds.Notes[0].Body || note.Body == life.Body {
			continue
		}
		id := next("mem", note.Day)
		doc := tpl[note.Folder].with(id, note.Day)
		doc.set("body", note.Body)
		doc.set("valid_at", stamp(midnight(note.Day)))
		doc.write(t, filepath.Join(side, note.Folder, "memories", id+".json"))
	}
	for _, jr := range ds.Journal[1:] {
		id := next("mem", jr.Day)
		doc := tpl[FolderJournal].with(id, jr.Day)
		doc.set("body", jr.Body())
		doc.set("valid_at", stamp(midnight(jr.Day)))
		doc.setMeta(t, "day", day(jr.Day))
		doc.write(t, filepath.Join(side, FolderJournal, "memories", id+".json"))
	}
	for _, fe := range ds.Feeds[1:] {
		id := next("mem", fe.Day)
		doc := tpl[FolderFeeds].with(id, fe.Day)
		doc.set("body", fe.Body)
		doc.set("valid_at", stamp(midnight(fe.Day)))
		doc.write(t, filepath.Join(side, FolderFeeds, "memories", id+".json"))
	}
	taskTpl := templateRow(t, filepath.Join(side, FolderWork, "tasks"))
	refTpl := templateRow(t, filepath.Join(side, FolderWork, "knowledge_refs"))
	for _, tk := range ds.Tasks[1:] {
		id := next("tsk", tk.Created)
		doc := taskTpl.with(id, tk.Created)
		doc.set("title", tk.Title)
		doc.setMeta(t, "owed_by", tk.OwedBy)
		doc.setMeta(t, "owed_to", tk.OwedTo)
		if tk.Done {
			doc.set("status", "done")
		}
		doc.write(t, filepath.Join(side, FolderWork, "tasks", id+".json"))
		who, rel := tk.OwedTo, "owed_to"
		if tk.OwedTo == Me {
			who, rel = tk.OwedBy, "owed_by"
		}
		rid := next("kfr", tk.Created)
		ref := refTpl.with(rid, tk.Created)
		ref.set("src_id", id)
		ref.set("dst_id", res[who])
		ref.set("relation", rel)
		ref.write(t, filepath.Join(side, FolderWork, "knowledge_refs", rid+".json"))
	}
	syncIn(t, e, clone, side, bare, "bench: migrated notes, journal, feeds and tasks")

	// lore must see every row before a question is asked
	want := len(ds.Notes) + len(ds.Journal) + len(ds.Feeds)
	start := time.Now()
	out := e.must("sql", "run", "--store", "me", "--format", "csv", "--query", "SELECT COUNT(*) AS n FROM memories_all")
	t.Logf("the first read after the load (sql count) took %d ms", time.Since(start).Milliseconds())
	if got := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "n")); got != strconv.Itoa(want) {
		t.Fatalf("setup: lore sees %s memories, the bench wrote %d:\n%s", got, want, out)
	}
}

// firstOf is the first note of a folder.
func firstOf(ns []Note, folder string) Note {
	for _, n := range ns {
		if n.Folder == folder {
			return n
		}
	}
	panic("recall: no note in " + folder)
}

// rowDoc is a row file as JSON fields (values kept raw).
type rowDoc map[string]json.RawMessage

// templateRow reads the one row file lore wrote in dir.
func templateRow(t *testing.T, dir string) rowDoc {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(m) == 0 {
		t.Fatalf("no row file in %s to copy", dir)
	}
	raw, err := os.ReadFile(filepath.Clean(m[0]))
	if err != nil {
		t.Fatal(err)
	}
	var d rowDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("%s: %v", m[0], err)
	}
	return d
}

// with copies d as a new row id written at.
func (d rowDoc) with(id string, at time.Time) rowDoc {
	c := rowDoc{}
	for k, v := range d {
		c[k] = v
	}
	c.set("id", id)
	for _, k := range []string{"created_at", "updated_at", "tx_at"} {
		if _, ok := c[k]; ok {
			c.set(k, stamp(at))
		}
	}
	var hlc string
	if json.Unmarshal(d["_hlc"], &hlc) == nil {
		if i := strings.LastIndex(hlc, "/"); i > 0 {
			c.set("_hlc", at.UTC().Format("2006-01-02T15:04:05.000Z")+"/0000"+hlc[i:])
		}
	}
	return c
}

// set replaces one field.
func (d rowDoc) set(k, v string) {
	b, _ := json.Marshal(v)
	d[k] = b
}

// metaValue is one metadata value of a row file.
type metaValue struct {
	Name  string `json:"name,omitempty"`
	Scope string `json:"scope,omitempty"`
	V     string `json:"v"`
}

// setMeta replaces the value of the metadata key name (which the template
// row carries).
func (d rowDoc) setMeta(t *testing.T, name, v string) {
	t.Helper()
	var m map[string]metaValue
	if err := json.Unmarshal(d["meta"], &m); err != nil {
		t.Fatalf("template meta: %v", err)
	}
	found := false
	for k, mv := range m {
		if mv.Name == name {
			mv.V = v
			m[k] = mv
			found = true
		}
	}
	if !found {
		t.Fatalf("template row has no metadata key %s: %s", name, d["meta"])
	}
	b, _ := json.Marshal(m)
	d["meta"] = b
}

// write writes the row file the way lore does (sorted keys, two-space
// indent, a final newline).
func (d rowDoc) write(t *testing.T, path string) {
	t.Helper()
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// resourceIDs maps resource names to ids.
func resourceIDs(t *testing.T, dir string) map[string]string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	out := map[string]string{}
	for _, p := range m {
		raw, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			t.Fatal(err)
		}
		var r struct{ ID, Name string }
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		out[r.Name] = r.ID
	}
	return out
}

// rowID makes a time-ordered row id (prefix_ + 32 hex) unique by n.
func rowID(prefix string, at time.Time, n uint64) string {
	return fmt.Sprintf("%s_%012x7%03x8%015x", prefix, at.UnixMilli(), n%4096, n)
}

// stamp is a row timestamp.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

// midnight is the start of t's day.
func midnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
