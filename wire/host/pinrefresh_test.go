package host

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/reposync"
	"github.com/thesatellite-ai/lore/op"
	opsdoctor "github.com/thesatellite-ai/lore/ops/doctor"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// pinOld and pinNew are the installed and the running release.
const (
	pinOld = "0.1.11"
	pinNew = "0.1.12"
)

// pinProject is a git work tree with a lore project at its root and the
// lore-sync-merge workflow installed at v0.1.11: auto-pinned, or pinned by
// the person (--lore-version) when fixed. It returns the root and the
// workflow file.
func pinProject(t *testing.T, fixed bool) (string, string) {
	t.Helper()
	top, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, gerr := (gitref.Exec{}).Run(context.Background(), gitref.Invocation{Dir: top, Args: []string{"init", "-q", "-b", "main"}}); gerr != nil {
		t.Fatalf("git init: %v %s", gerr, gitref.Stderr(gerr))
	}
	if merr := os.MkdirAll(filepath.Join(top, ".lore", "data"), 0o750); merr != nil {
		t.Fatal(merr)
	}
	in := reposync.InstallOptions{Dir: top, Branches: []string{"main"}, Own: pinOld}
	if fixed {
		in.LoreVersion = "v" + pinOld
	}
	res, err := reposync.Install(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return top, res.Path
}

// pinnedTo reports whether the workflow at path downloads release v.
func pinnedTo(t *testing.T, path, v string) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(b), "/v"+v+"/")
}

// pinRun runs the pin refresh hook once around an operation with spec.
func pinRun(t *testing.T, workDir string, spec op.Spec, cli, readOnly bool, opErr error) string {
	t.Helper()
	return pinRunJSON(t, workDir, spec, cli, readOnly, false, opErr)
}

// pinRunJSON is pinRun with the command's --json flag set or not.
func pinRunJSON(t *testing.T, workDir string, spec op.Spec, cli, readOnly, asJSON bool, opErr error) string {
	t.Helper()
	fn := func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }
	run := &Run{Entry: op.Define(spec, fn), ReadOnly: readOnly}
	if cli {
		run.Cmd = &cobra.Command{Use: "demo"}
		run.Cmd.Flags().Bool(pinJSONFlag, false, "")
		if asJSON {
			if err := run.Cmd.Flags().Set(pinJSONFlag, "true"); err != nil {
				t.Fatal(err)
			}
		}
	}
	var out bytes.Buffer
	h := PinRefreshHook(workDir, pinNew, &out)
	_, err := h.Around(context.Background(), run, func(context.Context) (op.Result, error) { return op.Result{}, opErr })
	if !errors.Is(err, opErr) {
		t.Fatalf("hook changed the operation's error: %v", err)
	}
	return out.String()
}

var (
	pinReadSpec    = op.Spec{Name: "demo.list", Summary: "s", Permission: op.PermRead, Access: op.AccessRead}
	pinWriteSpec   = op.Spec{Name: "demo.add", Summary: "s", Permission: op.PermWrite, Access: op.AccessWrite}
	pinSyncSpec    = op.Spec{Name: opssync.OpRun, Summary: "s", Permission: op.PermRead, Access: op.AccessRead}
	pinInstallSpec = op.Spec{Name: opssync.OpInstallAction, Summary: "s", Permission: op.PermWrite, Access: op.AccessWrite}
	pinDoctorSpec  = op.Spec{Name: opsdoctor.OpRun, Summary: "s", Permission: op.PermWrite, Access: op.AccessWrite}
)

// pinStaleWant is the one line a command prints for the stale test pin.
const pinStaleWant = "lore: .github/workflows/lore-sync-merge.yml pins lore v" + pinOld + "; run `lore sync install-action` to update it to v" + pinNew + "\n"

// readPin returns the workflow file's bytes.
func readPin(t *testing.T, file string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// devco:D-742 (E-FATES-5 before it): no command but install-action and
// doctor --fix rewrites the tracked workflow. A writing command or `lore
// sync` that finds the auto pin stale leaves the file byte for byte as it
// was and prints one line naming `lore sync install-action`; a read, a
// failed write, a REST request, --json, install-action and doctor print
// nothing here, and a user-pinned file is never stale.
func TestPinRefreshHook(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		spec     op.Spec
		cli      bool
		readOnly bool
		asJSON   bool
		opErr    error
		fixed    bool
		line     bool
	}{
		{name: "read-only command", spec: pinReadSpec, cli: true},
		{name: "failed write", spec: pinWriteSpec, cli: true, opErr: errors.New("refused")},
		{name: "REST write", spec: pinWriteSpec},
		{name: "user pin", spec: pinWriteSpec, cli: true, fixed: true},
		{name: "write with --json", spec: pinWriteSpec, cli: true, asJSON: true},
		{name: "install-action", spec: pinInstallSpec, cli: true},
		{name: "doctor", spec: pinDoctorSpec, cli: true},
		{name: "write under --read-only", spec: pinWriteSpec, cli: true, readOnly: true, line: true},
		{name: "writing command", spec: pinWriteSpec, cli: true, line: true},
		{name: "lore sync", spec: pinSyncSpec, cli: true, line: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			top, file := pinProject(t, tc.fixed)
			before := readPin(t, file)
			out := pinRunJSON(t, top, tc.spec, tc.cli, tc.readOnly, tc.asJSON, tc.opErr)
			if !bytes.Equal(before, readPin(t, file)) {
				t.Fatal("the hook rewrote the tracked workflow")
			}
			want := ""
			if tc.line {
				want = pinStaleWant
			}
			if out != want {
				t.Fatalf("output %q, want %q", out, want)
			}
		})
	}
}

// devco:D-742: one run says it once, however many operations it runs.
func TestPinRefreshLineOnce(t *testing.T) {
	t.Parallel()
	top, _ := pinProject(t, false)
	fn := func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }
	var out bytes.Buffer
	h := PinRefreshHook(top, pinNew, &out)
	for range 3 {
		run := &Run{Entry: op.Define(pinWriteSpec, fn), Cmd: &cobra.Command{Use: "demo"}}
		if _, err := h.Around(context.Background(), run, func(context.Context) (op.Result, error) { return op.Result{}, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if out.String() != pinStaleWant {
		t.Fatalf("output %q, want the line once", out.String())
	}
}

// E-FATES-6: a writing command run from a plain subfolder of the project
// names the project's one workflow and writes no file.
func TestPinRefreshFromSubfolder(t *testing.T) {
	t.Parallel()
	top, file := pinProject(t, false)
	sub := filepath.Join(top, "src", "deep")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	out := pinRun(t, sub, pinWriteSpec, true, false, nil)
	if pinnedTo(t, file, pinNew) || !strings.Contains(out, "lore-sync-merge.yml pins lore v"+pinOld) {
		t.Fatalf("from a subfolder: moved %v, output %q", pinnedTo(t, file, pinNew), out)
	}
	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil || len(entries) != 1 {
		t.Fatalf("workflow files: %v %v", entries, err)
	}
}

// LORE_SYNC_GIT=0 and LORE_SYNC=0 say nothing and write nothing.
func TestPinRefreshSwitchedOff(t *testing.T) {
	for _, env := range []string{EnvSyncGit, EnvSync} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, envOff)
			top, file := pinProject(t, false)
			if out := pinRun(t, top, pinWriteSpec, true, false, nil); out != "" || !pinnedTo(t, file, pinOld) {
				t.Fatalf("%s=0: output %q", env, out)
			}
		})
	}
}

// LORE_SYNC_QUIET=1 hides the line; the file stays as it was.
func TestPinRefreshQuiet(t *testing.T) {
	t.Setenv(pinQuietEnv, pinQuietOn)
	top, file := pinProject(t, false)
	if out := pinRun(t, top, pinWriteSpec, true, false, nil); out != "" || !pinnedTo(t, file, pinOld) {
		t.Fatalf("quiet: moved = %v, output %q", pinnedTo(t, file, pinNew), out)
	}
}

// devco:D-708: a dry run of a writing command leaves the workflow byte for
// byte as it was (since D-742 every run does) and gives the same line; the
// flag is read from the command line and from a REST body (dryRunFlag), and
// a plain task next is no dry run.
func TestPinRefreshDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	top, file := pinProject(t, false)
	before := readPin(t, file)
	fn := func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }
	cmd := &cobra.Command{Use: "demo"}
	cmd.Flags().Bool(fixDryRunFlag, false, "")
	if err := cmd.Flags().Set(fixDryRunFlag, "true"); err != nil {
		t.Fatal(err)
	}
	run := &Run{Entry: op.Define(pinWriteSpec, fn), Cmd: cmd}
	run.DryRun = dryRunFlag(run)
	if !run.DryRun || !run.WritesNothing() || NeedsGitignore(run) {
		t.Fatalf("--dry-run: DryRun %v, WritesNothing %v, NeedsGitignore %v", run.DryRun, run.WritesNothing(), NeedsGitignore(run))
	}
	var out bytes.Buffer
	if _, err := PinRefreshHook(top, pinNew, &out).Around(context.Background(), run, func(context.Context) (op.Result, error) { return op.Result{}, nil }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, readPin(t, file)) {
		t.Fatal("a dry run rewrote the lore-sync-merge workflow")
	}
	if out.String() != pinStaleWant {
		t.Fatalf("output %q, want %q", out.String(), pinStaleWant)
	}
	rest := &Run{Entry: op.Define(pinWriteSpec, fn), Decoder: op.JSONDecoder{Body: []byte(`{"dry_run": true}`)}}
	if !dryRunFlag(rest) {
		t.Fatal("a REST body's dry_run is a dry run")
	}
	if plain := (&Run{Entry: op.Define(pinWriteSpec, fn), Cmd: &cobra.Command{Use: "demo"}}); dryRunFlag(plain) || plain.WritesNothing() {
		t.Fatal("a command without --dry-run is no dry run")
	}
}

// devco:D-742: `lore doctor` warns about a stale pin with the command that
// updates it and writes nothing; `lore doctor --fix` moves it and lists it;
// `lore sync install-action` as printed (no flags) updates it too, keeping
// its triggers, and the pin is then fresh.
func TestDoctorWorkflowPin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	top, file := pinProject(t, false)
	before := readPin(t, file)
	var warns []string
	warn := func(s string) { warns = append(warns, s) }
	doctorWorkflowPin(ctx, top, pinNew, false, warn)
	want := ".github/workflows/lore-sync-merge.yml pins lore v" + pinOld + "; run `lore sync install-action` to update it to v" + pinNew + " (or `lore doctor --fix`)"
	if len(warns) != 1 || warns[0] != want || !bytes.Equal(before, readPin(t, file)) {
		t.Fatalf("doctor: warnings %q (want %q), file changed %v", warns, want, !bytes.Equal(before, readPin(t, file)))
	}
	warns = nil
	doctorWorkflowPin(ctx, top, pinNew, true, warn)
	if len(warns) != 1 || !strings.Contains(warns[0], "updated now to v"+pinNew) || !pinnedTo(t, file, pinNew) {
		t.Fatalf("doctor --fix: warnings %q, moved %v", warns, pinnedTo(t, file, pinNew))
	}
	warns = nil
	doctorWorkflowPin(ctx, top, pinNew, false, warn)
	if len(warns) != 0 {
		t.Fatalf("after --fix: %q", warns)
	}

	top2, file2 := pinProject(t, false)
	if _, err := reposync.Install(ctx, reposync.InstallOptions{Dir: top2, Own: pinNew}); err != nil {
		t.Fatal(err)
	}
	if !pinnedTo(t, file2, pinNew) {
		t.Fatal("install-action as printed did not update the pin")
	}
	if line := pinStaleLine(ctx, top2, pinNew, ""); line != "" {
		t.Fatalf("after install-action: %q", line)
	}
	if r, err := reposync.PreviewPin(ctx, top2, "v"+pinNew); err != nil || r.To != "" {
		t.Fatalf("install-action left an auto pin that is still stale: %+v %v", r, err)
	}
}
