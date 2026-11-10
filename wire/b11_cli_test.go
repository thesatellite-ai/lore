package wire

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/adapters/signals"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
	"github.com/thesatellite-ai/lore/logic/queue"
	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
)

// TestCoordErrExitCodes (B11 port of saas/cmd/cli coord_wire_test.go
// TestCoordErrorExitCodes): the exit statuses agents branch on (TEAM spec
// §1 "Error codes"): every coordination failure maps to its own code,
// wrapped or not, and the message or hint names what to do.
func TestCoordErrExitCodes(t *testing.T) {
	t.Parallel()
	held := signals.Signal{Kind: signals.KindClaim, Task: "msn_1", Holder: "act_c", HolderLabel: "agent:coord-1", Session: "s", ClaimToken: "ct", Expires: time.Now()}
	cases := []struct {
		name string
		err  error
		code errs.Code
		exit int
		says string
	}{
		{"lost", &coord.FencedError{Kind: coord.FenceLost, Task: "tsk_1", Detail: "gone"}, errs.LostClaim, 12, ""},
		{"cancelled", &coord.FencedError{Kind: coord.FenceCancelled, Task: "tsk_1"}, errs.Cancelled, 13, "cancelled"},
		{"reassigned", &coord.FencedError{Kind: coord.FenceReassigned, Task: "tsk_1"}, errs.Reassigned, 14, "reassigned"},
		{"unknown fence kind fails closed", &coord.FencedError{Kind: coord.FenceKind("odd"), Task: "tsk_1"}, errs.LostClaim, 12, ""},
		{"wrapped lost", fmt.Errorf("ctx: %w", &coord.FencedError{Kind: coord.FenceLost, Task: "tsk_1"}), errs.LostClaim, 12, ""},
		{"offline", &signals.OfflineError{Reason: "remote unreachable"}, errs.Offline, 15, "never queued"},
		{"no work", &coord.NoWorkError{Waiting: []queue.Waiting{{Task: queue.Task{ID: "tsk_2"}, Reason: "waits for tsk_1 to be merged"}}}, errs.NoWork, 10, "waits for tsk_1"},
		{"no work names the board", &coord.NoWorkError{Waiting: []queue.Waiting{{Task: queue.Task{ID: "tsk_2"}, Reason: "waits"}}}, errs.NoWork, 10, "lore task board"},
		{"nothing open names task add", &coord.NoWorkError{}, errs.NoWork, 10, "lore task add"},
		{"contended", coord.ErrContended, errs.Contended, 11, ""},
		{"bare not held", fmt.Errorf("x: %w", signals.ErrNotHeld), errs.LostClaim, 12, ""},
		{"mission held", &signals.HeldError{By: held}, errs.Contended, 11, "coordinated by coord-1"},
		{"same actor", &signals.HeldError{By: held, SameActor: true}, errs.Refused, 23, "LORE_ACTOR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := coordErr(tc.err)
			var e *errs.Error
			if !errors.As(got, &e) {
				t.Fatalf("not an *errs.Error: %v", got)
			}
			if e.Code != tc.code || errs.DefOf(e.Code).Exit != tc.exit {
				t.Fatalf("code %s exit %d, want %s %d", e.Code, errs.DefOf(e.Code).Exit, tc.code, tc.exit)
			}
			if tc.says != "" && !strings.Contains(e.Message+" "+e.Hint, tc.says) {
				t.Fatalf("message %q / hint %q must say %q", e.Message, e.Hint, tc.says)
			}
		})
	}
	if coordErr(nil) != nil {
		t.Fatal("nil stays nil")
	}
	coded := errs.New(errs.NotFound, "x")
	if got := coordErr(coded); !errors.Is(got, coded) || got.Error() != coded.Error() {
		t.Fatal("an error that already has a code passes through unchanged")
	}
}

// TestNetworkFSRefusesBeforeWriting (B11 port of saas/cmd/cli
// network_fs_order_test.go TestMaybeMaterialize_NetworkFSRefusesBeforeWriting):
// a fresh clone inside a cloud-synced folder (only .lore/data/_meta.json
// committed) is refused with E_NETWORK_FS on its first read and write, and
// nothing but data/ is written into .lore/ (v0.1.17 once built lore.db and
// a backup there before refusing). The override (Options.AllowNetworkFS,
// LORE_ALLOW_NETWORK_FS=1) still works, so the check is a refusal, not a
// lost feature.
func TestNetworkFSRefusesBeforeWriting(t *testing.T) {
	t.Parallel()
	for _, allow := range []bool{false, true} {
		repo := filepath.Join(t.TempDir(), "Dropbox", "site")
		data := filepath.Join(repo, ".lore", "data")
		if err := os.MkdirAll(data, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(data, "_meta.json"), []byte(`{"_v":2}`), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "init", "-q")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
		a := openWith(t, repo, Options{AllowNetworkFS: allow})
		if !allow {
			for _, c := range []struct{ name, in string }{{opsmemory.OpList, `{}`}, {opsmemory.OpAdd, `{"body":"x"}`}} {
				if _, err := run(t, a, c.name, c.in, true); !errs.Has(err, errs.NetworkFS) {
					t.Fatalf("%s: want E_NETWORK_FS, got %v", c.name, err)
				}
			}
			entries, err := os.ReadDir(filepath.Join(repo, ".lore"))
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if e.Name() != "data" {
					t.Errorf(".lore/%s was written before the refusal; only data/ may exist", e.Name())
				}
			}
			continue
		}
		withProject(t, a)
		if _, err := run(t, a, opsmemory.OpAdd, `{"body":"note in a synced folder"}`, true); err != nil {
			t.Fatalf("allowed: %v", err)
		}
	}
}
