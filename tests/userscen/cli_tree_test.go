//go:build userscen

package userscen

// cli_tree_test.go — CLI ergonomics over the WHOLE command tree, discovered
// from the binary itself (`lore --help`, then every "Available Commands"
// entry recursively), so a command added tomorrow is covered without
// touching this file. Scenarios:
//
//   - every command: --help exits 0 with a Usage block naming it, nothing
//     on stderr, no ANSI codes without a terminal; -h prints the same bytes;
//     `lore help <noun>` equals `lore <noun> --help`
//   - every Examples line runs a command that exists (an example naming a
//     removed or misspelled command is a doc bug a user copies)
//   - every command: an unknown flag exits 20 with exactly one
//     `ERROR: [E_INVALID_INPUT] unknown flag: --…` line on stderr and
//     nothing on stdout (FLAG2); with --json the same refusal is the
//     documented error object on stdout and nothing on stderr
//   - every command group: an unknown subcommand never exits 0; a group
//     that only groups (usage `lore x [command]`) exits 20 naming the word
//   - every command that takes --json, run bare with --json in an
//     initialised repo: stdout is exactly one JSON document; on success no
//     error object, on failure the documented error object whose "exit"
//     is the process exit; exit codes stay in the documented set and never
//     1 (E_INTERNAL) for a plain bare call; nothing printed twice
//   - every core noun's `list --json` keeps the v0.1.17 envelope
//     {schema_version: 1, kind: "<noun>.list", data: [...]}
//     (CLI_REFERENCE 1.4)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	th "github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// cliWorkers is how many lore processes a tree sweep runs at once, each in
// its own actor's HOME so no two share ~/.lore.
const cliWorkers = 4

// cliTimeout bounds one command of a sweep: every command here runs with
// no input and no terminal, so one that is still running after this long
// is waiting on something a user could not answer.
const cliTimeout = 60 * time.Second

// cliExitUsage is the exit of every usage error (FLAG2, class bad_input).
const cliExitUsage = 20

// cliNoFlag and cliNoWord are a flag and a command word no command has.
const (
	cliNoFlag = "--zz-no-such-flag"
	cliNoWord = "zznosuchword"
)

// cliANSI starts every ANSI escape sequence.
const cliANSI = "\x1b["

// cliNode is one command of the tree as its --help shows it.
type cliNode struct {
	Path []string // words after "lore"; empty for the root
	Help string   // the --help stdout
	Subs []string // the "Available Commands" names (help left out)
	// PureGroup: a group whose usage is only `lore x [command]`: it runs
	// nothing itself, so any word after it must be one of Subs.
	PureGroup bool
	JSON      bool // its own Flags block lists --json
}

// name is "lore <path>".
func (n cliNode) name() string { return strings.TrimSpace("lore " + strings.Join(n.Path, " ")) }

// cliProc is one finished (or killed) lore process.
type cliProc struct {
	Args     []string
	Stdout   string
	Stderr   string
	Code     int
	TimedOut bool
}

func (p cliProc) String() string {
	return fmt.Sprintf("lore %s: exit %d (timed out %v)\nstdout:\n%s\nstderr:\n%s", strings.Join(p.Args, " "), p.Code, p.TimedOut, p.Stdout, p.Stderr)
}

// cliRunMany runs every job (lore's arguments) spread over the clones, one
// worker per clone, each command under cliTimeout with no stdin, and
// returns the results in job order.
func cliRunMany(t *testing.T, clones []*th.Clone, jobs [][]string) []cliProc {
	t.Helper()
	cmds := make([]*exec.Cmd, len(jobs))
	for i, args := range jobs {
		cmds[i] = clones[i%len(clones)].Command(args...)
	}
	out := make([]cliProc, len(jobs))
	var wg sync.WaitGroup
	for w := range clones {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < len(jobs); i += len(clones) {
				out[i] = cliRunOne(cmds[i], jobs[i])
			}
		}(w)
	}
	wg.Wait()
	for _, p := range out {
		if p.Code == -1 && !p.TimedOut {
			t.Fatalf("could not start %s", p)
		}
	}
	return out
}

// cliRunOne runs one prepared command, killing it past cliTimeout.
func cliRunOne(cmd *exec.Cmd, args []string) cliProc {
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr, cmd.Stdin = &stdout, &stderr, nil
	cmd.WaitDelay = 5 * time.Second
	p := cliProc{Args: args, Code: -1}
	if err := cmd.Start(); err != nil {
		p.Stderr = err.Error()
		return p
	}
	var killed atomic.Bool
	timer := time.AfterFunc(cliTimeout, func() {
		killed.Store(true)
		_ = cmd.Process.Kill()
	})
	_ = cmd.Wait()
	timer.Stop()
	p.TimedOut = killed.Load()
	p.Stdout, p.Stderr = stdout.String(), stderr.String()
	if cmd.ProcessState != nil {
		p.Code = cmd.ProcessState.ExitCode()
	}
	return p
}

// cliActors makes cliWorkers people, each with a folder outside any git
// repository (a Clone over a plain folder, for commands that need none).
func cliActors(t *testing.T, w *th.World) ([]*th.Actor, []*th.Clone) {
	t.Helper()
	var actors []*th.Actor
	var outside []*th.Clone
	for i := range cliWorkers {
		name := fmt.Sprintf("person%d", i+1)
		a := w.Human(name, name+"@example.com")
		dir := filepath.Join(w.Root, "outside", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		actors = append(actors, a)
		outside = append(outside, a.OpenClone(nil, dir))
	}
	return actors, outside
}

// cliInitRepos gives every actor its own initialised checkout of its own
// remote (no two workers share a repo or a HOME).
func cliInitRepos(t *testing.T, w *th.World, actors []*th.Actor) []*th.Clone {
	t.Helper()
	var repos []*th.Clone
	for i, a := range actors {
		c := a.Clone(w.Remote(fmt.Sprintf("github.com/acme/app%d", i+1)), "app")
		c.MustLore("init", "--non-interactive", "--name=app")
		repos = append(repos, c)
	}
	return repos
}

// cliBlock returns the lines of the help block that starts with header
// (e.g. "Usage:") up to the next blank line.
func cliBlock(help, header string) []string {
	var lines []string
	in := false
	for _, l := range strings.Split(help, "\n") {
		switch {
		case !in && l == header:
			in = true
		case in && strings.TrimSpace(l) == "":
			return lines
		case in:
			lines = append(lines, l)
		}
	}
	return lines
}

// cliParse reads one --help output into a node.
func cliParse(path []string, help string) cliNode {
	n := cliNode{Path: path, Help: help}
	for _, l := range cliBlock(help, "Available Commands:") {
		f := strings.Fields(l)
		if len(f) > 0 && f[0] != "help" {
			n.Subs = append(n.Subs, f[0])
		}
	}
	usage := cliBlock(help, "Usage:")
	n.PureGroup = len(n.Subs) > 0 && len(usage) > 0
	for _, u := range usage {
		if !strings.HasSuffix(strings.TrimSpace(u), "[command]") {
			n.PureGroup = false
		}
	}
	for _, l := range cliBlock(help, "Flags:") {
		if strings.Contains(l, "--json ") || strings.HasSuffix(l, "--json") {
			n.JSON = true
		}
	}
	return n
}

// cliWalk discovers the command tree level by level from the binary's own
// --help (run outside any repo: help needs none).
func cliWalk(t *testing.T, outside []*th.Clone) []cliNode {
	t.Helper()
	var all []cliNode
	level := [][]string{{}}
	for len(level) > 0 {
		jobs := make([][]string, len(level))
		for i, p := range level {
			jobs[i] = append(slices.Clone(p), "--help")
		}
		var next [][]string
		for i, r := range cliRunMany(t, outside, jobs) {
			if r.Code != 0 || r.TimedOut {
				t.Errorf("--help must exit 0: %s", r)
				continue
			}
			n := cliParse(level[i], r.Stdout)
			all = append(all, n)
			for _, s := range n.Subs {
				next = append(next, append(slices.Clone(level[i]), s))
			}
		}
		level = next
	}
	if len(all) < 100 {
		t.Fatalf("the command tree has only %d commands: the help parser is broken", len(all))
	}
	return all
}

// cliFind is the node at path, or nil.
func cliFind(nodes []cliNode, path []string) *cliNode {
	for i := range nodes {
		if slices.Equal(nodes[i].Path, path) {
			return &nodes[i]
		}
	}
	return nil
}

// TestCLITree_Help: every command's --help is a real help page; -h is the
// same; `lore help <noun>` is the same; Examples name real commands.
func TestCLITree_Help(t *testing.T) {
	w := newWorld(t)
	_, outside := cliActors(t, w)
	nodes := cliWalk(t, outside)
	t.Logf("%d commands in the tree", len(nodes))

	var shortJobs, helpJobs [][]string
	for _, n := range nodes {
		if strings.Contains(n.Help, cliANSI) {
			t.Errorf("%s --help: ANSI codes with no terminal and NO_COLOR set", n.name())
		}
		if !strings.Contains(n.Help, "Usage:\n  "+n.name()) {
			t.Errorf("%s --help: no Usage line naming the command:\n%s", n.name(), n.Help)
		}
		shortJobs = append(shortJobs, append(slices.Clone(n.Path), "-h"))
		if len(n.Path) == 1 {
			helpJobs = append(helpJobs, []string{"help", n.Path[0]})
		}
		cliCheckExamples(t, nodes, n)
	}
	for i, r := range cliRunMany(t, outside, shortJobs) {
		if r.Code != 0 || r.Stdout != nodes[i].Help || r.Stderr != "" {
			t.Errorf("-h must print exactly what --help prints: %s", r)
		}
	}
	for _, r := range cliRunMany(t, outside, helpJobs) {
		n := cliFind(nodes, r.Args[1:])
		if r.Code != 0 || n == nil || r.Stdout != n.Help {
			t.Errorf("lore help %s must print what lore %s --help prints: %s", r.Args[1], r.Args[1], r)
		}
	}
}

// cliCheckExamples: every "lore …" line of a help's Examples block starts
// with a command path that exists and runs something (not a bare
// grouping noun, not an unknown subcommand).
func cliCheckExamples(t *testing.T, nodes []cliNode, n cliNode) {
	t.Helper()
	for _, l := range cliBlock(n.Help, "Examples:") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		words := cliExampleWords(l)
		if words == nil {
			t.Errorf("%s: example %q runs no lore command", n.name(), l)
			continue
		}
		cur := cliFind(nodes, nil)
		i := 1
		for ; i < len(words); i++ {
			if !slices.Contains(cur.Subs, words[i]) {
				break
			}
			cur = cliFind(nodes, words[1:i+1])
		}
		if i == 1 {
			t.Errorf("%s: example %q names no lore command", n.name(), l)
			continue
		}
		if cur.PureGroup {
			t.Errorf("%s: example %q stops at %s, which only groups commands (word %q is not one of %v)", n.name(), l, cur.name(), strings.Join(words[i:], " "), cur.Subs)
		}
	}
}

// cliExampleWords is an example line from its "lore" word on: an example
// may set an environment variable first (LORE_ORG=x lore whoami), feed
// lore from a pipe (gh … | lore check pr-body -) or run inside $(…)
// (eval "$(lore agent start …)") or a process substitution
// (source <(lore completion bash)); the closing quote or paren of those is
// dropped from the words after lore. nil when no word is lore.
func cliExampleWords(line string) []string {
	words := strings.Fields(line)
	for i, w := range words {
		if strings.TrimLeft(w, `"'$(<`) == "lore" {
			rest := slices.Clone(words[i+1:])
			for j := range rest {
				rest[j] = strings.TrimRight(rest[j], `)"'`)
			}
			return append([]string{"lore"}, rest...)
		}
	}
	return nil
}

// TestCLITree_UnknownFlag: every command refuses a flag it does not have
// with exit 20, the usage line and its --help hint on stderr, nothing on
// stdout (FLAG2).
func TestCLITree_UnknownFlag(t *testing.T) {
	w := newWorld(t)
	_, outside := cliActors(t, w)
	nodes := cliWalk(t, outside)
	jobs := make([][]string, len(nodes))
	for i, n := range nodes {
		jobs[i] = append(slices.Clone(n.Path), cliNoFlag)
	}
	for i, r := range cliRunMany(t, outside, jobs) {
		if want := cliFlagRefusal(nodes[i].Path, cliNoFlag); r.Code != cliExitUsage || r.Stderr != want || r.Stdout != "" {
			t.Errorf("unknown flag: want exit %d and exactly %q on stderr: %s", cliExitUsage, want, r)
		}
	}
	// With --json among the arguments (after the bad flag, so cobra never
	// parsed it) every command prints the refusal as the documented error
	// object on stdout, nothing on stderr: a script reading stdout gets
	// one document whatever went wrong (surface/cli usageError).
	for i := range jobs {
		jobs[i] = append(jobs[i], "--json")
	}
	for i, r := range cliRunMany(t, outside, jobs) {
		cmd := strings.Join(append([]string{"lore"}, nodes[i].Path...), " ")
		if !cliFlagRefusedJSON(r, nodes[i].Path, cliNoFlag) {
			t.Errorf("unknown flag with --json: want exit %d, the error object naming %s with hint %q on stdout, nothing on stderr: %s", cliExitUsage, cliNoFlag, cmd+" --help lists its flags", r)
		}
	}
}

// TestCLITree_UnknownSubcommand: no command group answers a mistyped
// subcommand with success; a pure group exits 20 naming the word. Before
// the fix `lore spec`, `lore completion` and `lore api token` printed
// their help and exited 0, so `lore spec doc` looked like it worked.
func TestCLITree_UnknownSubcommand(t *testing.T) {
	w := newWorld(t)
	_, outside := cliActors(t, w)
	nodes := cliWalk(t, outside)
	var groups []cliNode
	var jobs [][]string
	for _, n := range nodes {
		if len(n.Subs) == 0 {
			continue
		}
		groups = append(groups, n)
		jobs = append(jobs, append(slices.Clone(n.Path), cliNoWord))
	}
	for i, r := range cliRunMany(t, outside, jobs) {
		n := groups[i]
		if r.Code == 0 || r.TimedOut {
			t.Errorf("%s %s: a mistyped subcommand must not succeed: %s", n.name(), cliNoWord, r)
			continue
		}
		if !n.PureGroup {
			continue
		}
		want := fmt.Sprintf("ERROR: [E_INVALID_INPUT] unknown command %q for %q\n", cliNoWord, n.name())
		if r.Code != cliExitUsage || r.Stderr != want || r.Stdout != "" {
			t.Errorf("%s: want exit %d and exactly %q: %s", n.name(), cliExitUsage, want, r)
		}
	}
	// Bare, a group still prints its help (exit 0), and so does --help.
	for _, path := range [][]string{{"spec"}, {"completion"}, {"api", "token"}, {"rule"}} {
		n := cliFind(nodes, path)
		if n == nil {
			t.Fatalf("lore %v is not in the tree", path)
		}
		r := cliRunMany(t, outside[:1], [][]string{path})[0]
		if r.Code != 0 || r.Stdout != n.Help {
			t.Errorf("bare lore %v must print its help: %s", path, r)
		}
	}
}

// cliSweepSkip are command paths the --json sweep does not run bare: they
// open a browser or a server, replace the binary, or print a shell script
// (each has its own scenario: setup belongs to the setup run, upgrade is
// TestCLI_Upgrade, api is the rest area).
var cliSweepSkip = [][]string{{"setup"}, {"onboard"}, {"api"}, {"tui"}, {"upgrade"}, {"completion"}, {"spec"}}

func cliSkipped(path []string) bool {
	for _, s := range cliSweepSkip {
		if len(path) >= len(s) && slices.Equal(path[:len(s)], s) {
			return true
		}
	}
	return false
}

// cliExits is every documented exit status (errors.md "Exit statuses"),
// plus doctor's own 2.
var cliExits = []int{0, 1, 2, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27}

// cliErrorDoc is the documented --json error object (errors.md Pitfalls,
// CLI_REFERENCE 1.4).
type cliErrorDoc struct {
	Error *struct {
		Code        string `json:"code"`
		Message     string `json:"message"`
		Hint        string `json:"hint"`
		Exit        int    `json:"exit"`
		Class       string `json:"class"`
		Retryable   *bool  `json:"retryable"`
		NeedsPerson *bool  `json:"needs_person"`
	} `json:"error"`
}

// cliOneJSON decodes stdout as exactly one JSON document.
func cliOneJSON(stdout string) (json.RawMessage, error) {
	dec := json.NewDecoder(strings.NewReader(stdout))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err == nil {
		return nil, fmt.Errorf("more than one JSON document")
	}
	return raw, nil
}

// TestCLITree_JSON: every command that takes --json, run bare with --json
// in an initialised repo, prints one JSON document and a documented exit.
func TestCLITree_JSON(t *testing.T) {
	w := newWorld(t)
	actors, outside := cliActors(t, w)
	nodes := cliWalk(t, outside)
	repos := cliInitRepos(t, w, actors)
	var jobs [][]string
	for _, n := range nodes {
		if n.JSON && len(n.Path) > 0 && !cliSkipped(n.Path) {
			jobs = append(jobs, append(slices.Clone(n.Path), "--json"))
		}
	}
	if len(jobs) < 300 {
		t.Fatalf("only %d commands take --json: the help parser is broken", len(jobs))
	}
	t.Logf("%d commands run bare with --json", len(jobs))
	for _, r := range cliRunMany(t, repos, jobs) {
		cliCheckJSON(t, r)
	}
}

// cliCheckJSON holds one bare --json run to the contract.
func cliCheckJSON(t *testing.T, r cliProc) {
	t.Helper()
	if r.TimedOut {
		t.Errorf("hung with no input: %s", r)
		return
	}
	if strings.Contains(r.Stdout+r.Stderr, cliANSI) {
		t.Errorf("ANSI codes without a terminal: %s", r)
	}
	if !slices.Contains(cliExits, r.Code) {
		t.Errorf("exit %d is not a documented exit status: %s", r.Code, r)
	}
	isDoctor := len(r.Args) > 0 && r.Args[0] == "doctor"
	if r.Code == 1 && !isDoctor {
		t.Errorf("exit 1 (E_INTERNAL, an unexpected failure) for a bare call: %s", r)
	}
	raw, err := cliOneJSON(r.Stdout)
	if err != nil {
		t.Errorf("stdout is not exactly one JSON document (%v): %s", err, r)
		return
	}
	var doc cliErrorDoc
	_ = json.Unmarshal(raw, &doc) // an array or a scalar has no error object
	if r.Code == 0 || (isDoctor && doc.Error == nil) {
		if doc.Error != nil {
			t.Errorf("exit 0 with an error object: %s", r)
		}
		return
	}
	e := doc.Error
	switch {
	case e == nil:
		t.Errorf("exit %d without the --json error object: %s", r.Code, r)
	case !strings.HasPrefix(e.Code, "E_") || e.Message == "" || e.Class == "" || e.Retryable == nil || e.NeedsPerson == nil:
		t.Errorf("error object misses code/message/class/retryable/needs_person: %s", r)
	case e.Exit != r.Code:
		t.Errorf("error object says exit %d, the process exited %d: %s", e.Exit, r.Code, r)
	case strings.Contains(r.Stderr, "ERROR:"):
		t.Errorf("the error printed twice (JSON on stdout and a text line on stderr): %s", r)
	}
}

// cliCoreNouns keep v0.1.17's list envelope (CLI_REFERENCE 1.4 and the
// core noun list of section 2.16).
var cliCoreNouns = []string{
	"memory", "pattern", "playbook", "prompt", "rule", "decision", "hotfix", "task", "mission", "tasklist", "plan",
	"reminder", "tag", "comment", "link", "architecturenote", "behaviour", "cookbookrecipe", "incident", "suggestion",
	"tastepref", "workflow", "workspace", "handoff", "snapshot", "techdoc", "external-source", "actor", "project", "repo",
}

// cliEnvelope is v0.1.17's --json list envelope.
type cliEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	Kind          string          `json:"kind"`
	Data          json.RawMessage `json:"data"`
}

// TestCLITree_ListEnvelope: `<core noun> list --json` prints the envelope
// with kind "<noun>.list" and a data array, on an empty repo and after a
// row was added (same shape both times).
func TestCLITree_ListEnvelope(t *testing.T) {
	w := newWorld(t)
	a := w.Human("alice", "alice@example.com")
	c := a.Clone(w.Remote("github.com/acme/app"), "app")
	c.MustLore("init", "--non-interactive", "--name=app")
	check := func(when string) {
		for _, noun := range cliCoreNouns {
			r := c.Lore(noun, "list", "--json")
			env := th.DecodeJSON[cliEnvelope](t, r)
			wantKind := noun + ".list"
			if env.SchemaVersion != 1 || !strings.EqualFold(env.Kind, wantKind) {
				t.Errorf("%s: lore %s list --json: schema_version %d kind %q, want 1 and %q", when, noun, env.SchemaVersion, env.Kind, wantKind)
			}
			var rows []json.RawMessage
			if err := json.Unmarshal(env.Data, &rows); err != nil {
				t.Errorf("%s: lore %s list --json: data is not an array: %v\n%s", when, noun, err, r.Stdout)
			}
		}
	}
	check("empty repo")
	c.MustLore("rule", "add", "Run the tests before a push")
	c.MustLore("memory", "add", "The staging box reboots on Sundays")
	c.MustLore("task", "add", "Write the release notes")
	check("after adding rows")
	var rules []json.RawMessage
	env := th.DecodeJSON[cliEnvelope](t, c.Lore("rule", "list", "--json"))
	if err := json.Unmarshal(env.Data, &rules); err != nil || len(rules) != 1 {
		t.Fatalf("rule list --json after one add: %d rows (%v): %s", len(rules), err, env.Data)
	}
}
