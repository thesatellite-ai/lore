package coding

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/tests/bench/recall"
)

// fakeCoder edits the checkout as a scripted agent would: the lore arm
// writes the hotfix's fix (after a lore call), the plain arm the trap,
// unless the test says otherwise per arm and task.
type fakeCoder struct {
	write func(task recall.AgentTask) (file, line string) // "" file: no edit
	cmds  map[recall.Arm][]string
	err   map[string]string // task id + arm → run error
}

func (f fakeCoder) Run(_ context.Context, task recall.AgentTask) recall.AgentRun {
	run := recall.AgentRun{Answer: "done", Commands: f.cmds[task.Arm], Wall: time.Millisecond}
	if e := f.err[task.Q.ID+string(task.Arm)]; e != "" {
		run.Err = e
	}
	file, line := f.write(task)
	if file == "" {
		return run
	}
	p := filepath.Join(task.Dir, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		run.Err = err.Error()
		return run
	}
	old, _ := os.ReadFile(filepath.Clean(p))
	if err := os.WriteFile(p, append(old, []byte(line+"\n")...), 0o600); err != nil {
		run.Err = err.Error()
	}
	return run
}

// fixes and falls are an edit per trap: the hotfix's fix and the trap.
var (
	fixes = map[string][2]string{
		"T05": {"billing/invoice.go", "\tinv.IssuedAt = clock.UTC()"},
		"T06": {"billing/tax.go", "\treturn money.RoundHalfEven(float64(netCents) * ratePercent / 100)"},
		"T04": {"api/handlers/webhooks.go", "\tif !hmac.Equal(got, want) {"},
		"T02": {"db/migrations/0043_events_created_index.sql", "CREATE INDEX CONCURRENTLY events_created_at ON events (created_at);"},
		"T14": {"internal/cache/redis.go", "\tconn := pool.Get()\n\tdefer conn.Close()"},
		"T09": {"web/src/auth/Login.tsx", "    document.cookie = `session=${token}; max-age=2592000; SameSite=Lax`;"},
		"T16": {"internal/auth/ratelimit.go", "\tn, err := redis.Int(conn.Do(\"INCR\", key))"},
	}
	falls = map[string][2]string{
		"T05": {"billing/invoice.go", "\tinv.IssuedAt = time.Now()"},
		"T06": {"billing/tax.go", "\treturn int64(math.Round(float64(netCents) * ratePercent / 100))"},
		"T04": {"api/handlers/webhooks.go", "\tif sig != expected {"},
		"T02": {"db/migrations/0043_events_created_index.sql", "CREATE INDEX events_created_at ON events (created_at);"},
		"T14": {"internal/cache/redis.go", "\tconn := pool.Get()\n\treturn conn.Do(\"GET\", key)"},
		"T09": {"web/src/auth/Login.tsx", "    document.cookie = `session=${token}; max-age=2592000; SameSite=Strict`;"},
		"T16": {"internal/auth/ratelimit.go", "var failed = map[string]int{}"},
	}
)

// checkouts writes a lore and a plain checkout per trap under dir (git
// only: the fake agent needs no store) and returns them with their
// snapshot trees.
func checkouts(t *testing.T, traps []Trap) (map[string]TrapDirs, map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	dirs, bases := map[string]TrapDirs{}, map[string]string{}
	for _, tr := range traps {
		d := TrapDirs{Lore: filepath.Join(dir, tr.Task, "lore"), Plain: filepath.Join(dir, tr.Task, "plain"), LoreBin: filepath.Join(dir, "bin")}
		for _, c := range []string{d.Lore, d.Plain} {
			for f, body := range RepoFiles() {
				p := filepath.Join(c, filepath.FromSlash(f))
				if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			git := exec.Command("git", "init", "-q")
			git.Dir = c
			if out, err := git.CombinedOutput(); err != nil {
				t.Fatalf("git init: %v %s", err, out)
			}
			// lore's folder is never diffed
			if err := os.MkdirAll(filepath.Join(c, ".lore"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(c, ".lore", "cache"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			base, err := GitSnapshot(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			bases[c] = base
		}
		dirs[tr.Task] = d
	}
	return dirs, bases
}

func runFake(t *testing.T, f fakeCoder) []TrapResult {
	t.Helper()
	traps := Traps()
	dirs, bases := checkouts(t, traps)
	tasks := TrapTasks(traps, "SKILL", func(tr Trap) TrapDirs { return dirs[tr.Task] })
	differ := func(ctx context.Context, dir string) (string, error) {
		if err := os.WriteFile(filepath.Join(dir, ".lore", "cache"), []byte("changed by a lore read"), 0o600); err != nil {
			return "", err
		}
		return GitDiff(ctx, dir, bases[dir])
	}
	return RunTraps(context.Background(), f, tasks, 3, time.Minute, differ)
}

// byArm is the edit per arm: the lore agent fixes, the plain agent falls.
func byArm(task recall.AgentTask) (string, string) {
	e := falls[task.Q.ID]
	if task.Arm == ArmLore {
		e = fixes[task.Q.ID]
	}
	return e[0], e[1]
}

func TestRunTrapsGradesDiffs(t *testing.T) {
	rs := runFake(t, fakeCoder{write: byArm, cmds: map[recall.Arm][]string{ArmLore: {"lore context --for-file billing/tax.go", "ls"}, ArmPlain: {"ls", "grep -rn pool ."}}})
	if len(rs) != 2*len(Traps()) {
		t.Fatalf("%d results", len(rs))
	}
	for _, r := range rs {
		if r.Err != "" || !r.Edited {
			t.Errorf("%s %s: err %q edited %v\n%s", r.Trap.Task, r.Arm, r.Err, r.Edited, r.Diff)
		}
		if strings.Contains(r.Diff, ".lore") {
			t.Errorf("%s %s: the diff holds the lore folder:\n%s", r.Trap.Task, r.Arm, r.Diff)
		}
		switch r.Arm {
		case ArmLore:
			if r.Fell || r.LoreCalls != 1 || !r.Avoided() {
				t.Errorf("%s lore: fell %v (%q), lore calls %d", r.Trap.Task, r.Fell, r.Line, r.LoreCalls)
			}
			if r.Trap.Good != nil && !r.Fixed {
				t.Errorf("%s lore: the hotfix's fix not seen in\n%s", r.Trap.Task, r.Diff)
			}
		case ArmPlain:
			if !r.Fell || r.RanLore || !r.Proves() {
				t.Errorf("%s plain: fell %v ran lore %v\n%s", r.Trap.Task, r.Fell, r.RanLore, r.Diff)
			}
		}
	}
	if m := TrapMisses(rs); len(m) > 0 {
		t.Errorf("misses %q", m)
	}
	s := SummarizeTraps(rs)
	if len(s.Shown) != len(Traps()) || len(s.Unproven) != 0 {
		t.Errorf("shown %v unproven %v", s.Shown, s.Unproven)
	}
	if line := s.Line(); !strings.HasPrefix(line, "traps lore/plain: fell in 0/7 of 7, edited 7/7") || !strings.Contains(line, "shown 7") {
		t.Errorf("line %q", line)
	}
}

func TestTrapMissesNameEveryFailure(t *testing.T) {
	// the lore agent falls into T05, makes no edit on T06, fails on T04;
	// the plain agent avoids T14 and runs lore on T16
	write := func(task recall.AgentTask) (string, string) {
		switch {
		case task.Arm == ArmLore && task.Q.ID == "T05":
			return falls["T05"][0], falls["T05"][1]
		case task.Arm == ArmLore && task.Q.ID == "T06":
			return "", ""
		case task.Arm == ArmPlain && task.Q.ID == "T14":
			return fixes["T14"][0], fixes["T14"][1]
		}
		return byArm(task)
	}
	f := fakeCoder{write: write, err: map[string]string{"T04lore": "stopped after 5m"},
		cmds: map[recall.Arm][]string{ArmPlain: {"ls"}}}
	rs := runFake(t, f)
	// T16's plain agent ran lore: grade that run again with the command
	for i, r := range rs {
		if r.Arm == ArmPlain && r.Trap.Task == "T16" {
			run := r.Run
			run.Commands = []string{"cd internal && /usr/bin/lore ask --query x"}
			rs[i] = GradeTrap(TrapTask{Trap: r.Trap, Agent: recall.AgentTask{Arm: ArmPlain}}, run, r.Diff)
		}
	}
	m := strings.Join(TrapMisses(rs), "\n")
	for _, want := range []string{"T05 (h-tz): lore agent fell into the trap: inv.IssuedAt = time.Now()", "T06 (h-taxround): lore agent made no edit", "T04 (h-hmac): lore agent run failed: stopped after 5m", "T16 (h-ratelimit): plain agent ran lore"} {
		if !strings.Contains(m, want) {
			t.Errorf("misses lack %q:\n%s", want, m)
		}
	}
	s := SummarizeTraps(rs)
	if strings.Join(s.Unproven, " ") != "T14 T16" {
		t.Errorf("unproven %v, want T14 (plain avoided) T16 (plain ran lore)", s.Unproven)
	}
	rep := TrapReport(TrapRunInfo{Started: time.Now(), Model: "m", Jobs: 1, Lore: "lore 0.0.0"}, rs)
	for _, want := range []string{"Result: FAIL", "| T14 | h-pool | avoided (applied the hotfix) | 0 | avoided (applied the hotfix) | nothing: the plain agent did not fall in |", "| T16 | h-ratelimit |", "nothing: the plain agent ran lore", "| T05 | h-tz | fell in: `inv.IssuedAt = time.Now()`", "no: the lore agent did not avoid it"} {
		if !strings.Contains(rep, want) {
			t.Errorf("report lacks %q:\n%s", want, rep)
		}
	}
}

func TestTrapMissesWhenNothingShown(t *testing.T) {
	// both agents avoid every trap: lore passed, but the run shows nothing
	rs := runFake(t, fakeCoder{write: func(task recall.AgentTask) (string, string) {
		e := fixes[task.Q.ID]
		return e[0], e[1]
	}})
	m := TrapMisses(rs)
	if len(m) != 1 || !strings.Contains(m[0], "no trap shows the difference") {
		t.Errorf("misses %q", m)
	}
}

func TestTrapPromptsBlind(t *testing.T) {
	tasks := TrapTasks(Traps(), "THE-SKILL", func(Trap) TrapDirs { return TrapDirs{Lore: "/l", Plain: "/p", LoreBin: "/b"} })
	for _, tk := range tasks {
		a := tk.Agent
		text := a.System + "\n" + a.Prompt
		// nothing of the hotfix reaches either agent but through lore
		for _, r := range Rows() {
			if r.Key == tk.Trap.Hotfix && (strings.Contains(text, r.Body) || r.Title != "" && strings.Contains(text, r.Title)) {
				t.Errorf("%s %s: the prompt holds the hotfix", tk.Trap.Task, a.Arm)
			}
		}
		for _, re := range []*regexp.Regexp{tk.Trap.Good} {
			if re != nil && re.MatchString(text) {
				t.Errorf("%s %s: the prompt holds the fix %s", tk.Trap.Task, a.Arm, re)
			}
		}
		if a.Prompt != tk.Trap.Prompt || strings.Join(a.Tools, ",") != "Bash,Read,Edit,Write,Glob,Grep" {
			t.Errorf("%s %s: prompt %q tools %v", tk.Trap.Task, a.Arm, a.Prompt, a.Tools)
		}
		// devco D-835: both arms run with their checkout's own settings,
		// so the hooks lore setup --agent-hooks put in the lore checkout fire
		if !a.Checkout {
			t.Errorf("%s %s: runs bare, so the checkout's hooks never fire", tk.Trap.Task, a.Arm)
		}
		switch a.Arm {
		case ArmLore:
			if a.Dir != "/l" || a.PathPrefix != "/b" || !strings.Contains(a.System, "THE-SKILL") {
				t.Errorf("lore task %+v", a)
			}
		case ArmPlain:
			if a.Dir != "/p" || a.PathPrefix != "" || strings.Contains(a.System, "THE-SKILL") || strings.Contains(a.System, "lore") || strings.Join(a.Deny, ",") != "Bash(lore:*)" {
				t.Errorf("plain task %+v", a)
			}
		}
	}
	// the trap files exist (the agent edits them), a new file does not,
	// and nothing in the checkout says what a hotfix says
	files := RepoFiles()
	for _, tr := range Traps() {
		for _, r := range Rows() {
			if r.Key != tr.Hotfix {
				continue
			}
			for f, body := range files {
				if tr.Good != nil && tr.Path.MatchString(f) && tr.Good.MatchString(body) {
					t.Errorf("%s: %s already holds the fix", tr.Task, f)
				}
				if tr.Path.MatchString(f) && tr.Bad.MatchString(body) {
					t.Errorf("%s: %s already holds the trap", tr.Task, f)
				}
			}
		}
	}
	for _, f := range NewFiles {
		if _, ok := files[f]; ok {
			t.Errorf("%s: a file the agent creates is in the checkout", f)
		}
	}
}

func TestKeepTrapSection(t *testing.T) {
	sec := trapBegin + "\n## Blind agent trap run\n\nrun 1\n" + trapEnd + "\n"
	old := MergeTrapSection("# Coding-agent context gate\n\nold gate\n", sec)
	got := KeepTrapSection(old, "# Coding-agent context gate\n\nnew gate\n")
	if !strings.Contains(got, "new gate") || strings.Contains(got, "old gate") || strings.Count(got, trapBegin) != 1 || !strings.Contains(got, "run 1") {
		t.Errorf("kept: %q", got)
	}
	if got := KeepTrapSection("", "gate\n"); got != "gate\n" {
		t.Errorf("no old report: %q", got)
	}
	two := MergeTrapSection(got, strings.Replace(sec, "run 1", "run 2", 1))
	if strings.Contains(two, "run 1") || !strings.Contains(two, "run 2") || !strings.HasPrefix(two, "# Coding-agent context gate\n\nnew gate") {
		t.Errorf("replaced: %q", two)
	}
}

func TestTrapAnswersJSON(t *testing.T) {
	tr := Traps()[0]
	b, err := TrapAnswersJSON([]TrapResult{{Trap: tr, Arm: ArmLore, Diff: "+x", Run: recall.AgentRun{Answer: "ok", Commands: []string{"lore ask"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"task": "T05"`, `"arm": "lore"`, `"diff": "+x"`, `"commands": [`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("json lacks %s:\n%s", want, b)
		}
	}
}
