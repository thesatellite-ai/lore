//go:build machines && !windows

package machines

// sweep_test.go — the generic command sweep: on every machine shape
// (EveryShape: the built-ins plus every registered one) run the command
// table of sweep_cmds.go and assert, for every run, that the answer is a
// real answer (no panic, no Go error text, exit 0 or a documented E_ code
// with a hint, --json that parses); after the sweep, that the files on
// disk are right (CheckChanges, CheckNoWriteThroughLinks, CheckConfig,
// CheckModes); and that doctor --all names every issue it counts.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// TestSweepCoversEveryCommand: every leaf command lore has (walked from
// the binary's own help) is swept or in notUserRun with a reason; neither
// list names a command lore does not have, and none is in both.
func TestSweepCoversEveryCommand(t *testing.T) {
	t.Parallel()
	leaves := leafCommands(t)
	swept := sweptCommands()
	for _, c := range leaves {
		_, skipped := notUserRun[c]
		switch in := slices.Contains(swept, c); {
		case in && skipped:
			t.Errorf("%q is swept and in notUserRun; drop it from notUserRun", c)
		case !in && !skipped:
			t.Errorf("%q is neither swept nor in notUserRun: add it to the sweep table, or to notUserRun with its reason", c)
		}
	}
	for _, c := range swept {
		if !slices.Contains(leaves, c) {
			t.Errorf("the sweep runs %q, which is no leaf command of lore", c)
		}
	}
	for c, why := range notUserRun {
		if !slices.Contains(leaves, c) {
			t.Errorf("notUserRun names %q, which is no leaf command of lore", c)
		}
		if why == "" {
			t.Errorf("notUserRun %q has no reason", c)
		}
	}
	if len(leaves) < 400 {
		t.Errorf("only %d leaf commands found; the help walk is broken", len(leaves))
	}
}

// leafCommands walks `lore --help` and every `lore <cmd> --help` below it:
// a command with no "Available Commands" is a leaf. cobra's own help and
// the completion shells are folded into their parent, the way
// docs/reference/commands.md lists them.
func leafCommands(t testing.TB) []string {
	t.Helper()
	home := t.TempDir()
	help := func(path []string) []string {
		c := exec.Command(loreBin, append(slices.Clone(path), "--help")...)
		c.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "NO_COLOR=1", "LORE_NO_UPDATE_CHECK=1"}
		out, err := c.Output()
		if err != nil {
			t.Fatalf("lore %s --help: %v", strings.Join(path, " "), err)
		}
		var subs []string
		inside := false
		for _, line := range strings.Split(string(out), "\n") {
			switch {
			case strings.HasSuffix(line, "Commands:"):
				inside = true
			case inside && strings.TrimSpace(line) == "":
				inside = false
			case inside:
				if f := strings.Fields(line); len(f) > 0 && f[0] != "help" {
					subs = append(subs, f[0])
				}
			}
		}
		return subs
	}
	var (
		mu     sync.Mutex
		leaves []string
		wg     sync.WaitGroup
		sem    = make(chan struct{}, 16)
	)
	var walk func(path []string)
	walk = func(path []string) {
		defer wg.Done()
		sem <- struct{}{}
		subs := help(path)
		<-sem
		if len(subs) == 0 || (len(path) == 1 && path[0] == "completion") {
			mu.Lock()
			leaves = append(leaves, strings.Join(path, " "))
			mu.Unlock()
			return
		}
		for _, s := range subs {
			wg.Add(1)
			go walk(append(slices.Clone(path), s))
		}
	}
	for _, s := range help(nil) {
		wg.Add(1)
		go walk([]string{s})
	}
	wg.Wait()
	slices.Sort(leaves)
	return leaves
}

// documentedCodes are the E_ codes docs/reference/errors.md lists.
var documentedCodes = sync.OnceValues(func() ([]string, error) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "errors.md"))
	if err != nil {
		return nil, err
	}
	codes := regexp.MustCompile(`E_[A-Z0-9_]+`).FindAllString(string(b), -1)
	slices.Sort(codes)
	return slices.Compact(codes), nil
})

// goErrorText is text only a crash or a raw Go error prints: a panic, a
// stack, a source position, a fmt verb error, a nil printed as text.
var goErrorText = regexp.MustCompile(`panic:|goroutine \d+|runtime error|\.go:\d+|%!|<nil>|\[recovered\]`)

// errEnvelope is --json's error form.
type errEnvelope struct {
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
		Exit    int    `json:"exit"`
	} `json:"error"`
}

// loreHomeName is how a refusal names lore's own folder.
const loreHomeName = ".lore"

// LoreHomeUnusable reports whether ~/.lore exists but lore cannot write
// in it (a file there, a folder without the owner's write bit, or a
// read-only config.yaml): every
// write must then be refused with an E_ code naming ~/.lore and a hint,
// never succeed half-way or fail with Go text.
func (m *Machine) LoreHomeUnusable() bool {
	info, err := os.Stat(filepath.Join(m.Home, loreHomeName))
	if err == nil && (!info.IsDir() || info.Mode().Perm()&ownerWrite == 0) {
		return true
	}
	// A config.yaml the person made read-only: lore refuses to replace it
	// (wire/configwrite.go), so every config write is refused, naming it.
	cfg, err := os.Stat(m.configPath())
	return err == nil && cfg.Mode().Perm()&ownerWrite == 0
}

// ownerWrite is the owner's write bit.
const ownerWrite = 0o200

// exitInternal is E_INTERNAL's exit: never a documented answer.
const exitInternal = 1

// checkAnswer asserts one run gave a real answer.
// refusal is the sweep's state on a machine whose ~/.lore lore cannot
// write: nil on every other machine. Refused is set once a chain's write
// was refused naming ~/.lore; the chain's later steps then fail as its
// consequence (a setup never made, a step order) and need only be
// documented errors.
type refusal struct{ Refused bool }

func checkAnswer(t testing.TB, where string, s sweepStep, r Result, rs *refusal) {
	t.Helper()
	if goErrorText.MatchString(r.Stdout + r.Stderr) {
		t.Errorf("[%s] Go error text or a stack: %s", where, r)
		return
	}
	if r.Code == 0 || slices.Contains(s.Exits, r.Code) {
		var env errEnvelope
		if r.Code != 0 && json.Unmarshal([]byte(r.Stdout), &env) == nil && env.Error != nil {
			t.Errorf("[%s] exit %d is a report exit, but it printed an error: %s", where, r.Code, r)
			return
		}
		if !s.NoJSON && !json.Valid([]byte(r.Stdout)) {
			t.Errorf("[%s] --json output does not parse: %s", where, r)
		}
		// A write names the store it wrote to; an empty one tells a
		// script nothing.
		var top map[string]json.RawMessage
		if json.Unmarshal([]byte(r.Stdout), &top) == nil && string(top["store"]) == `""` {
			t.Errorf("[%s] the result names no store: %s", where, r)
		}
		return
	}
	if s.Must && rs == nil {
		t.Errorf("[%s] a fixture step failed: %s", where, r)
		return
	}
	var env errEnvelope
	if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil || env.Error == nil {
		t.Errorf("[%s] failed without a --json error: %s", where, r)
		return
	}
	// On a home whose ~/.lore lore cannot write, a fixture write is
	// refused: the refusal must say ~/.lore is the reason.
	if s.Must && rs != nil && !rs.Refused && !strings.Contains(env.Error.Message+" "+env.Error.Hint, loreHomeName) {
		t.Errorf("[%s] a write on an unwritable ~/.lore was refused without naming ~/.lore: %s", where, r)
	}
	if s.Must && rs != nil {
		rs.Refused = true
	}
	codes, err := documentedCodes()
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case r.Code == exitInternal || env.Error.Code == "E_INTERNAL":
		t.Errorf("[%s] internal error: %s", where, r)
	case !slices.Contains(codes, env.Error.Code):
		t.Errorf("[%s] error code %q is not in docs/reference/errors.md: %s", where, env.Error.Code, r)
	case strings.TrimSpace(env.Error.Hint) == "":
		t.Errorf("[%s] error %s has no hint line saying what to do: %s", where, env.Error.Code, r)
	case env.Error.Exit != r.Code:
		t.Errorf("[%s] error says exit %d, the process exited %d: %s", where, env.Error.Exit, r.Code, r)
	}
}

// runStep runs one step in each folder it names.
func runStep(t testing.TB, m *Machine, chain string, s sweepStep, id, prev *string, rs *refusal) {
	t.Helper()
	dirs := []string{s.In}
	if s.In == inBoth {
		dirs = []string{inHome, inRepo}
	}
	for _, d := range dirs {
		cwd := ""
		if d != inHome {
			cwd = m.Repos[d]
		}
		args := strings.Fields(s.Cmd)
		for _, a := range s.Args {
			args = append(args, strings.NewReplacer(varID, *id, varPrev, *prev).Replace(a))
		}
		if !s.NoJSON {
			args = append(args, "--json")
		}
		r := m.Lore(t, cwd, args...)
		where := m.Shape + "/" + chain + "/" + strings.Join(args, " ")
		if d != inHome {
			where += " (in " + d + ")"
		}
		checkAnswer(t, where, s, r, rs)
		if s.Save && r.Code == 0 {
			var out struct {
				ID   string `json:"id"`
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			_ = json.Unmarshal([]byte(r.Stdout), &out)
			*prev = *id
			if *id = out.ID; *id == "" {
				*id = out.Data.ID
			}
			if *id == "" && rs == nil {
				t.Fatalf("[%s] no id in the result: %s", where, r)
			}
		}
	}
}

// TestSweepReads, TestSweepKnowledge, TestSweepRows and TestSweepTasks run one group of
// the table each on every shape and check the disk and doctor afterwards.
func TestSweepReads(t *testing.T)     { sweepShapes(t, groupReads) }
func TestSweepKnowledge(t *testing.T) { sweepShapes(t, groupKnowledge) }
func TestSweepRows(t *testing.T)      { sweepShapes(t, groupRows) }
func TestSweepTasks(t *testing.T)     { sweepShapes(t, groupTasks) }

func sweepShapes(t *testing.T, group string) {
	for _, shape := range EveryShape() {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			m := Build(t, shape, loreBin)
			cfgBefore, _ := os.ReadFile(m.configPath())
			before := m.Snapshot(t)
			places := m.PlacesBefore()
			refusing := m.LoreHomeUnusable()
			for _, c := range sweepChains(group) {
				var id, prev string
				var rs *refusal
				if refusing {
					rs = &refusal{}
				}
				for _, s := range c.Steps {
					runStep(t, m, c.Name, s, &id, &prev, rs)
				}
			}
			after := m.Snapshot(t)
			step := shape + "/after the " + group + " sweep"
			m.CheckChanges(t, step, before, after, places)
			m.CheckNoWriteThroughLinks(t, step, before, after, places)
			m.CheckConfig(t, step, string(cfgBefore))
			m.CheckOldLoreCalls(t, step)
			m.CheckModes(t, step)
			m.CheckDoctorNames(t, step, refusing)
		})
	}
}

// doctorAll is the part of doctor --all --json the sweep reads.
type doctorAll struct {
	Status string   `json:"status"`
	Errors []string `json:"errors"`
	Setups []struct {
		Name   string                `json:"name"`
		Checks []onboard.CheckResult `json:"checks"`
	} `json:"setups"`
	Machine struct {
		Checks []onboard.CheckResult `json:"checks"`
	} `json:"machine"`
	Counts map[onboard.Status]int `json:"counts"`
}

// CheckDoctorNames runs doctor --all --json: every check that is not ok
// or skipped names its check id and a fix, and the counts equal the
// checks it lists, status by status.
func (m *Machine) CheckDoctorNames(t testing.TB, step string, refusing bool) {
	t.Helper()
	r := m.Lore(t, "", "doctor", "--all", "--json")
	if refusing && !slices.Contains(doctorExits, r.Code) {
		// doctor may refuse an unusable ~/.lore outright, but only the
		// documented way, naming it.
		checkAnswer(t, step+"/doctor --all", sweepStep{Cmd: "doctor run", Must: true}, r, &refusal{})
		return
	}
	if !slices.Contains(doctorExits, r.Code) {
		t.Errorf("[%s] doctor --all: %s", step, r)
		return
	}
	var d doctorAll
	if err := json.Unmarshal([]byte(r.Stdout), &d); err != nil {
		t.Errorf("[%s] doctor --all --json does not parse (%v): %s", step, err, r)
		return
	}
	seen := map[onboard.Status]int{}
	var all []onboard.CheckResult
	for _, s := range d.Setups {
		all = append(all, s.Checks...)
	}
	all = append(all, d.Machine.Checks...)
	for _, c := range all {
		seen[c.Status]++
		if c.Status == onboard.StatusOK || c.Status == onboard.StatusSkip {
			continue
		}
		if c.Check == "" || strings.TrimSpace(c.Fix.Text) == "" {
			t.Errorf("[%s] doctor issue without a check id or a fix: %+v", step, c)
		}
	}
	for _, st := range []onboard.Status{onboard.StatusOK, onboard.StatusWarn, onboard.StatusFail, onboard.StatusSkip, onboard.StatusError} {
		if d.Counts[st] != seen[st] {
			t.Errorf("[%s] doctor counts %d %s, lists %d such checks: %s", step, d.Counts[st], st, seen[st], r.Stdout)
		}
	}
	for _, e := range d.Errors {
		t.Errorf("[%s] doctor error without a check id: %s", step, e)
	}
}

// recorder is a testing.TB that keeps what Errorf reported.
type recorder struct {
	testing.TB
	errs []string
}

func (r *recorder) Errorf(format string, args ...any) { r.errs = append(r.errs, format) }
func (r *recorder) Helper()                           {}

// TestSweepCheckersBite: each disk check reports the fault it exists for
// (a check that never fires would pass every sweep).
func TestSweepCheckersBite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := &Machine{Dir: dir, Home: filepath.Join(dir, "h"), Repos: map[string]string{}}
	for _, d := range []string{".lore", "code/r", "elsewhere"} {
		if err := os.MkdirAll(filepath.Join(m.Home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m.Repos[RepoLore] = filepath.Join(m.Home, "code/r")
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(m.Home, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	theirs := "setups: [{name: a}]\nmine: 1\n"
	write(".lore/config.yaml", theirs)
	if err := os.Symlink(filepath.Join(m.Home, "elsewhere"), filepath.Join(m.Home, ".lore", "linked")); err != nil {
		t.Fatal(err)
	}
	before := m.Snapshot(t)
	// Faults: a flow list lore wrote, a lost key, a file outside lore's
	// places, a write through a link out of ~/.lore.
	write(".lore/config.yaml", "setups: [{name: a}]\norgs: [{name: acme}]\n")
	write("stray.txt", "x")
	write(".lore/linked/through.txt", "x")
	after := m.Snapshot(t)
	for name, check := range map[string]func(tb testing.TB){
		"flow list lore wrote":   func(tb testing.TB) { m.CheckConfig(tb, "t", theirs) },
		"outside lore's places":  func(tb testing.TB) { m.CheckChanges(tb, "t", before, after, m.lorePlaces()) },
		"written through a link": func(tb testing.TB) { m.CheckNoWriteThroughLinks(tb, "t", before, after, m.lorePlaces()) },
	} {
		rec := &recorder{TB: t}
		check(rec)
		if len(rec.errs) == 0 {
			t.Errorf("%s: the check reported nothing", name)
		}
	}
	rec := &recorder{TB: t}
	m.CheckConfig(rec, "t", theirs)
	if !slices.ContainsFunc(rec.errs, func(e string) bool { return strings.Contains(e, "lost the key") }) {
		t.Errorf("a lost key was not reported: %v", rec.errs)
	}
}

// TestSweepSkillLinkRule: a skill-folder link whose end is lore's own
// skill makes that end a lore place (writing lore's skill there is lore
// writing its own content); a link to a folder that is not a lore skill
// does not, so a write through it is reported.
func TestSweepSkillLinkRule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, skill string
		allowed     bool
	}{
		{"lore skill at the end", "---\nname: lore\nlore_version: dev\n---\n", true},
		{"foreign folder at the end", "# someone else's notes\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			m := &Machine{Dir: dir, Home: filepath.Join(dir, "h"), Repos: map[string]string{}}
			end := filepath.Join(dir, "store", "lore")
			for _, d := range []string{end, filepath.Join(m.Home, ".lore"), filepath.Join(m.Home, ".agents", "skills"), filepath.Join(m.Home, ".claude", "skills")} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(end, "SKILL.md"), []byte(tc.skill), 0o644); err != nil {
				t.Fatal(err)
			}
			// a two-hop chain, as a shared-skills tool makes it
			if err := os.Symlink(end, filepath.Join(m.Home, ".agents", "skills", "lore")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../../.agents/skills/lore", filepath.Join(m.Home, ".claude", "skills", "lore")); err != nil {
				t.Fatal(err)
			}
			places := m.PlacesBefore()
			before := m.Snapshot(t)
			if err := os.WriteFile(filepath.Join(m.Home, ".claude", "skills", "lore", "COMMANDS.md"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			after := m.Snapshot(t)
			rec := &recorder{TB: t}
			m.CheckChanges(rec, "t", before, after, places)
			m.CheckNoWriteThroughLinks(rec, "t", before, after, places)
			if reported := len(rec.errs) > 0; reported == tc.allowed {
				t.Errorf("write at the link's end: reported %v (%v), want reported %v", reported, rec.errs, !tc.allowed)
			}
		})
	}
}
