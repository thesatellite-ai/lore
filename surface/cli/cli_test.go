package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// result is one command execution.
type result struct {
	exit           int
	stdout, stderr string
}

func execute(root *cobra.Command, stdin string, args ...string) result {
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.Execute()
	return result{exit: ExitCode(err), stdout: out.String(), stderr: errOut.String()}
}

func echoOf(t *testing.T, r result) string {
	t.Helper()
	var out addOut
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatalf("stdout %q: %v (stderr %q)", r.stdout, err, r.stderr)
	}
	return out.Echo
}

// TestArgsAndFlags: positional args, typed flags, repeatable flags and bool
// flags reach the input struct; every malformed command line fails with the
// class exit (20 bad input, 1 cobra usage).
func TestArgsAndFlags(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	ok := []struct {
		args []string
		echo string
	}{
		{[]string{"memory", "add", "hello", "--json"}, "hello|||0|0s|false"},
		{[]string{"memory", "add", "hello", "--title", "T", "--tag", "a", "--tag", "b", "--limit", "5", "--wait", "1h30m", "--json"}, "hello|T|a,b|5|1h30m0s|false"},
		{[]string{"memory", "add", "--json", "x y", "--force=false"}, "x y|||0|0s|false"},
		{[]string{"memory", "add", "hello", "--force", "--yes", "--json"}, "hello|||0|0s|true"},
	}
	for _, c := range ok {
		r := execute(Build(f.reg, f), "", c.args...)
		if r.exit != 0 {
			t.Fatalf("%v: exit %d stderr %q", c.args, r.exit, r.stderr)
		}
		if got := echoOf(t, r); got != c.echo {
			t.Errorf("%v: echo %q want %q", c.args, got, c.echo)
		}
	}
	bad := []struct {
		args []string
		exit int
	}{
		{[]string{"memory", "add"}, errs.ExitBadInput},                                      // missing required
		{[]string{"memory", "add", "a", "b"}, errs.ExitBadInput},                            // too many args
		{[]string{"memory", "add", "a", "--limit", "many"}, errs.ExitBadInput},              // bad int
		{[]string{"memory", "add", "a", "--wait", "soon"}, errs.ExitBadInput},               // bad duration
		{[]string{"memory", "add", "a", "--title", "x", "--title", "y"}, errs.ExitBadInput}, // one value
		{[]string{"memory", "add", "a", "--colour", "red"}, ExitUsage},                      // unknown flag
		{[]string{"memory", "list", "--yes"}, ExitUsage},                                    // --yes only on confirming ops
		{[]string{"nosuch"}, ExitUsage},                                                     // unknown command
	}
	for _, c := range bad {
		if r := execute(Build(f.reg, f), "", c.args...); r.exit != c.exit {
			t.Errorf("%v: exit %d want %d (stderr %q)", c.args, r.exit, c.exit, r.stderr)
		}
	}
}

// TestExitCodePerClass walks every registered code: the process exit is the
// code's class exit, the --json error object is the spec 9.4 shape on
// stdout, and the human form is `ERROR: [CODE] message` plus a hint line on
// stderr.
func TestExitCodePerClass(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	for _, d := range errs.All() {
		r := execute(Build(f.reg, f), "", "memory", "add", failPrefix+string(d.Code), "--json")
		if r.exit != d.Exit {
			t.Errorf("%s: exit %d want %d", d.Code, r.exit, d.Exit)
		}
		var env errs.Envelope
		if err := json.Unmarshal([]byte(r.stdout), &env); err != nil {
			t.Fatalf("%s: stdout %q: %v", d.Code, r.stdout, err)
		}
		if env.Error.Code != d.Code || env.Error.Exit != d.Exit || env.Error.Class != d.Class || env.Error.Hint != "do the thing" {
			t.Errorf("%s: envelope %+v", d.Code, env)
		}
		h := execute(Build(f.reg, f), "", "memory", "add", failPrefix+string(d.Code))
		want := "ERROR: [" + string(d.Code) + "] forced failure\n  hint: do the thing\n"
		if h.exit != d.Exit || h.stderr != want || h.stdout != "" {
			t.Errorf("%s: human exit %d stderr %q stdout %q", d.Code, h.exit, h.stderr, h.stdout)
		}
	}
}

// TestOutput: --json prints exactly the Out struct; the human form prints
// key: value lines and a list as a table.
func TestOutput(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	r := execute(Build(f.reg, f), "", "memory", "list", "--json", "--limit", "1")
	if r.exit != 0 || r.stdout != `{"items":[{"id":"mem_a","title":"first"}]}`+"\n" {
		t.Fatalf("json %q exit %d", r.stdout, r.exit)
	}
	r = execute(Build(f.reg, f), "", "memory", "list")
	want := "items:\n  ID     TITLE\n  mem_a  first\n  mem_b  second\n"
	if r.stdout != want {
		t.Fatalf("human %q want %q", r.stdout, want)
	}
	r = execute(Build(f.reg, f), "", "secrets", "rule", "add", "--pattern", "x")
	if r.exit != 0 || r.stdout != "id: sru_x\n" {
		t.Fatalf("nested noun %q exit %d stderr %q", r.stdout, r.exit, r.stderr)
	}
}

// TestConfirm: a non-interactive caller without --yes exits 26; --yes runs;
// an interactive caller is asked [y/N] and only "y"/"yes" runs.
func TestConfirm(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	if r := execute(Build(f.reg, f), "", "trash", "purge", "x"); r.exit != errs.ExitNeedsConfirmation {
		t.Fatalf("no tty: exit %d", r.exit)
	}
	if r := execute(Build(f.reg, f), "", "trash", "purge", "x", "--yes"); r.exit != 0 || r.stdout != "purged: 1\n" {
		t.Fatalf("--yes: exit %d out %q", r.exit, r.stdout)
	}
	if !f.lastCaller().Yes || f.lastCaller().Interactive {
		t.Errorf("caller %+v", f.lastCaller())
	}
	tty := WithInteractive(func() bool { return true })
	for answer, exit := range map[string]int{"y\n": 0, "YES\n": 0, "n\n": errs.ExitNeedsConfirmation, "": errs.ExitNeedsConfirmation, "\n": errs.ExitNeedsConfirmation} {
		r := execute(Build(f.reg, f, tty), answer, "trash", "purge", "x")
		if r.exit != exit || !strings.Contains(r.stderr, "really do trash.purge? [y/N]") {
			t.Errorf("answer %q: exit %d stderr %q", answer, r.exit, r.stderr)
		}
	}
}

// TestCallerInjected: WithCaller supplies the actor and agent.
func TestCallerInjected(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	r := execute(Build(f.reg, f, WithCaller(func(_ context.Context) (op.Caller, error) {
		return op.Caller{Agent: "codex"}, nil
	})), "", "memory", "list")
	if r.exit != 0 || f.lastCaller().Agent != "codex" {
		t.Fatalf("exit %d caller %+v", r.exit, f.lastCaller())
	}
	r = execute(Build(f.reg, f, WithCaller(func(context.Context) (op.Caller, error) {
		return op.Caller{}, errs.New(errs.IdentityFailed, "no identity")
	})), "", "memory", "list")
	if r.exit != errs.ExitRefused {
		t.Fatalf("caller error: exit %d", r.exit)
	}
}

// TestEveryCommandGoesThroughRunner walks the registry: each operation's
// command reaches the Runner exactly once and no run function is entered
// outside it.
func TestEveryCommandGoesThroughRunner(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	sample := map[string][]string{
		"memory.add":       {"x"},
		"trash.purge":      {"x", "--yes"},
		"secrets.rule.add": {"--pattern", "p"},
	}
	for _, e := range f.reg.Entries() {
		name := e.Spec().Name
		args := append(op.CLIPath(name), sample[name]...)
		if r := execute(Build(f.reg, f), "", args...); r.exit != 0 {
			t.Errorf("%s: exit %d stderr %q", name, r.exit, r.stderr)
		}
		if f.viaRunner[name] != 1 {
			t.Errorf("%s: runner calls %d", name, f.viaRunner[name])
		}
	}
	if f.direct != 0 {
		t.Errorf("%d run functions entered outside the Runner", f.direct)
	}
	// The marker check itself catches a direct call.
	e, _ := f.reg.Lookup("memory.list")
	in, err := e.Decode(op.ArgsDecoder{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Invoke(t.Context(), in); err != nil || f.direct != 1 {
		t.Fatalf("direct call not detected: %v %d", err, f.direct)
	}
}

// TestHelp: --help shows the summary, the positional argument, flag
// descriptions with their types, and the examples; hidden operations are not
// listed.
func TestHelp(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	r := execute(Build(f.reg, f), "", "memory", "add", "--help")
	for _, want := range []string{"Add a memory", "add <body>", "body  the memory text (required)", "--limit int", "--wait duration", "--tag stringArray", "# Save a lesson", `lore memory add "Retry on 502"`, "--yes", "--json"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, r.stdout)
		}
	}
	r = execute(Build(f.reg, f), "", "--help")
	if strings.Contains(r.stdout, "merge") {
		t.Errorf("hidden noun listed:\n%s", r.stdout)
	}
}

// TestMount: the old binary's root keeps its commands, gets the new ones,
// reuses a pure group, and refuses a clash or a reserved flag.
func TestMount(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	root := &cobra.Command{Use: "lore"}
	root.AddCommand(&cobra.Command{Use: "init", Run: func(*cobra.Command, []string) {}})
	secrets := &cobra.Command{Use: "secrets"}
	secrets.AddCommand(&cobra.Command{Use: "scan", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(secrets)
	if err := Mount(root, f.reg, f); err != nil {
		t.Fatal(err)
	}
	if r := execute(root, "", "secrets", "rule", "add", "--pattern", "p"); r.exit != 0 {
		t.Fatalf("mounted command: exit %d %q", r.exit, r.stderr)
	}
	if child(child(root, "secrets"), "scan") == nil || child(root, "init") == nil {
		t.Fatal("old commands lost")
	}
	clash := &cobra.Command{Use: "lore"}
	clash.AddCommand(&cobra.Command{Use: "memory", Run: func(*cobra.Command, []string) {}})
	if err := Mount(clash, f.reg, f); !errs.Has(err, errs.ConfigInvalid) || len(clash.Commands()) != 1 {
		t.Fatalf("runnable noun clash: %v", err)
	}
	leaf := &cobra.Command{Use: "lore"}
	mem := &cobra.Command{Use: "memory"}
	mem.AddCommand(&cobra.Command{Use: "add", Run: func(*cobra.Command, []string) {}})
	leaf.AddCommand(mem)
	if err := Mount(leaf, f.reg, f); !errs.Has(err, errs.ConfigInvalid) {
		t.Fatalf("leaf clash: %v", err)
	}
	type jsonIn struct {
		JSON bool `flag:"json" json:"json"`
	}
	bad := op.Define(op.Spec{Name: "a.list", Summary: "s", Permission: op.PermRead, Access: op.AccessRead},
		func(context.Context, jsonIn) (struct{}, error) { return struct{}{}, nil })
	reg, err := op.NewRegistry(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err := Mount(&cobra.Command{Use: "lore"}, reg, f); !errs.Has(err, errs.ConfigInvalid) {
		t.Fatalf("reserved flag: %v", err)
	}
}

// TestExitCode: nil is 0, foreign errors are usage errors, never 0.
func TestExitCode(t *testing.T) {
	t.Parallel()
	if ExitCode(nil) != 0 || ExitCode(errs.New(errs.NotFound, "x")) != ExitUsage || ExitCode(&exitError{code: 27}) != 27 {
		t.Fatal("ExitCode")
	}
}
