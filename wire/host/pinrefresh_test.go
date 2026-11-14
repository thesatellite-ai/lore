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
	fn := func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }
	run := &Run{Entry: op.Define(spec, fn), ReadOnly: readOnly}
	if cli {
		run.Cmd = &cobra.Command{Use: "demo"}
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
	pinReadSpec  = op.Spec{Name: "demo.list", Summary: "s", Permission: op.PermRead, Access: op.AccessRead}
	pinWriteSpec = op.Spec{Name: "demo.add", Summary: "s", Permission: op.PermWrite, Access: op.AccessWrite}
	pinSyncSpec  = op.Spec{Name: opssync.OpRun, Summary: "s", Permission: op.PermRead, Access: op.AccessRead}
)

// E-FATES-5 (v0.1.17 TestE2E_WorkflowPinFollowsLoreUpgrade): after an
// upgrade, a writing command or `lore sync` moves the auto-pinned workflow
// with v0.1.17's notice; a read-only command, --read-only, a failed write,
// a REST request and a user-pinned file leave it alone.
func TestPinRefreshHook(t *testing.T) {
	t.Parallel()
	notice := "lore sync: updated .github/workflows/lore-sync-merge.yml to lore v" + pinNew + " (was v" + pinOld + ") — commit it\n"
	for _, tc := range []struct {
		name     string
		spec     op.Spec
		cli      bool
		readOnly bool
		opErr    error
		fixed    bool
		moved    bool
	}{
		{name: "read-only command", spec: pinReadSpec, cli: true},
		{name: "write under --read-only", spec: pinWriteSpec, cli: true, readOnly: true},
		{name: "sync under --read-only", spec: pinSyncSpec, cli: true, readOnly: true},
		{name: "failed write", spec: pinWriteSpec, cli: true, opErr: errors.New("refused")},
		{name: "REST write", spec: pinWriteSpec},
		{name: "user pin", spec: pinWriteSpec, cli: true, fixed: true},
		{name: "writing command", spec: pinWriteSpec, cli: true, moved: true},
		{name: "lore sync", spec: pinSyncSpec, cli: true, moved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			top, file := pinProject(t, tc.fixed)
			out := pinRun(t, top, tc.spec, tc.cli, tc.readOnly, tc.opErr)
			if pinnedTo(t, file, pinNew) != tc.moved || pinnedTo(t, file, pinOld) == tc.moved {
				t.Fatalf("moved = %v, want %v", !tc.moved, tc.moved)
			}
			want := ""
			if tc.moved {
				want = notice
			}
			if out != want {
				t.Fatalf("output %q, want %q", out, want)
			}
		})
	}
}

// E-FATES-6: a writing command run from a plain subfolder of the project
// moves the project's one workflow and writes no second file.
func TestPinRefreshFromSubfolder(t *testing.T) {
	t.Parallel()
	top, file := pinProject(t, false)
	sub := filepath.Join(top, "src", "deep")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	out := pinRun(t, sub, pinWriteSpec, true, false, nil)
	if !pinnedTo(t, file, pinNew) || !strings.Contains(out, "updated .github/workflows/lore-sync-merge.yml") {
		t.Fatalf("not moved from a subfolder: %q", out)
	}
	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil || len(entries) != 1 {
		t.Fatalf("workflow files: %v %v", entries, err)
	}
}

// LORE_SYNC_GIT=0 and LORE_SYNC=0 leave the workflow alone.
func TestPinRefreshSwitchedOff(t *testing.T) {
	for _, env := range []string{EnvSyncGit, EnvSync} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, envOff)
			top, file := pinProject(t, false)
			if out := pinRun(t, top, pinWriteSpec, true, false, nil); out != "" || !pinnedTo(t, file, pinOld) {
				t.Fatalf("%s=0 moved the workflow: %q", env, out)
			}
		})
	}
}

// LORE_SYNC_QUIET=1 hides the notice, never the move itself.
func TestPinRefreshQuiet(t *testing.T) {
	t.Setenv(pinQuietEnv, pinQuietOn)
	top, file := pinProject(t, false)
	if out := pinRun(t, top, pinWriteSpec, true, false, nil); out != "" || !pinnedTo(t, file, pinNew) {
		t.Fatalf("quiet: moved = %v, output %q", pinnedTo(t, file, pinNew), out)
	}
}
