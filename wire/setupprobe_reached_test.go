package wire

// setupprobe_reached_test.go — store.read's FixReachedBefore: a failed
// read of a store this machine fetched from before never offers to create
// the repo (journey solo/week1/offline-writes).

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/onboard"
	opssetup "github.com/thesatellite-ai/lore/ops/setup"
)

func TestStoreReadReachedBefore(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	const raw = "git@github.com:pat/lore-me.git"
	canon, err := gitbackend.CanonicalURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(canon)
	rem, err := gitbackend.RemoteOf(u)
	if err != nil {
		t.Fatal(err)
	}
	base, err := gitbackend.CloneBase(home, rem)
	if err != nil {
		t.Fatal(err)
	}
	sp := setupProber{a: &App{opts: Options{Home: home}}}
	fail := func(variant string) opssetup.ProbeResult {
		v := map[string]string{}
		if variant != "" {
			v[onboard.VarFixVariant] = variant
		}
		return opssetup.ProbeResult{Status: onboard.StatusFail, Vars: v}
	}
	existing := opssetup.ProbeRequest{Answers: onboard.Answers{onboard.QIDPersonalStore: {Text: onboard.PersonalStoreExisting}}}
	create := opssetup.ProbeRequest{Answers: onboard.Answers{onboard.QIDPersonalStore: {Text: onboard.PersonalStoreCreate}}}

	// No clone: the default fix (create it) stays.
	if got := sp.reachedBefore(fail(""), raw, existing).Vars[onboard.VarFixVariant]; got != "" {
		t.Fatalf("no clone: variant %q, want the default", got)
	}
	// A clone that never fetched: still the default.
	stateDir := gitbackend.StateDirOf(base)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := sp.reachedBefore(fail(""), raw, existing).Vars[onboard.VarFixVariant]; got != "" {
		t.Fatalf("clone never fetched: variant %q, want the default", got)
	}
	// It fetched once: the repo existed.
	if err := os.WriteFile(filepath.Join(stateDir, gitbackend.FetchStampFile), []byte("2026-01-05T09:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := sp.reachedBefore(fail(""), raw, existing).Vars[onboard.VarFixVariant]; got != string(onboard.FixReachedBefore) {
		t.Fatalf("fetched before: variant %q, want %q", got, onboard.FixReachedBefore)
	}
	// A more precise variant is kept; a store being created is not "reached";
	// a passing read is left alone.
	if got := sp.reachedBefore(fail(string(onboard.FixUnreachable)), raw, existing).Vars[onboard.VarFixVariant]; got != string(onboard.FixUnreachable) {
		t.Fatalf("unreachable replaced by %q", got)
	}
	if got := sp.reachedBefore(fail(""), raw, create).Vars[onboard.VarFixVariant]; got != "" {
		t.Fatalf("create: variant %q, want the default", got)
	}
	ok := opssetup.ProbeResult{Status: onboard.StatusOK, Vars: map[string]string{}}
	if got := sp.reachedBefore(ok, raw, existing).Vars[onboard.VarFixVariant]; got != "" {
		t.Fatalf("ok read got variant %q", got)
	}
	// The fix text names no repo creation.
	if f := onboard.FixFor(onboard.CheckStoreRead, "", onboard.FixReachedBefore); f.Runnable || len(f.Command) != 0 || strings.Contains(f.Text, "repo create") {
		t.Fatalf("reached-before fix: %+v, want a text-only fix that creates nothing", f)
	}
}
