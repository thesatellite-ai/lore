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
	"github.com/thesatellite-ai/lore/adapters/probe"
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
	// H-11: "not GitHub" only names the host; a store read before on
	// another host gets the moved/deleted fix, never "check the url and
	// this setup's key" or (for the marker) "store init" over it
	if got := sp.reachedBefore(fail(string(onboard.FixNotGitHub)), raw, existing).Vars[onboard.VarFixVariant]; got != string(onboard.FixReachedBefore) {
		t.Fatalf("not GitHub, fetched before: variant %q, want %q", got, onboard.FixReachedBefore)
	}
	if got := sp.markerVars(fail(string(onboard.FixNotGitHub)), probe.ClassNoAccess, raw, existing).Vars[onboard.VarFixVariant]; got != string(onboard.FixReachedBefore) {
		t.Fatalf("marker, not GitHub, fetched before: variant %q, want %q", got, onboard.FixReachedBefore)
	}
	for _, c := range []onboard.CheckID{onboard.CheckStoreRead, onboard.CheckStoreMarker} {
		f := onboard.FixFor(c, "", onboard.FixReachedBefore)
		if !strings.Contains(f.Text, "lore store set-url --name {store_name}") || !strings.Contains(f.Text, "--restore --yes") {
			t.Fatalf("%s reached-before fix names neither set-url nor the restore: %q", c, f.Text)
		}
	}
	ok := opssetup.ProbeResult{Status: onboard.StatusOK, Vars: map[string]string{}}
	if got := sp.reachedBefore(ok, raw, existing).Vars[onboard.VarFixVariant]; got != "" {
		t.Fatalf("ok read got variant %q", got)
	}
	// F-11: store.marker of a store that refused the read is never "not a
	// lore store yet; store init": fetched before is FixReachedBefore (its
	// text names lost access), no answer FixUnreachable, an absent marker
	// on a store that answered keeps the default.
	for _, c := range []struct {
		class probe.Class
		want  onboard.FixVariant
	}{
		{probe.ClassNoAccess, onboard.FixReachedBefore},
		{probe.ClassAuthDenied, onboard.FixReachedBefore},
		{probe.ClassUnreachable, onboard.FixUnreachable},
		{probe.ClassOK, onboard.FixDefault},
	} {
		if got := sp.markerVars(fail(""), c.class, raw, existing).Vars[onboard.VarFixVariant]; got != string(c.want) {
			t.Errorf("marker after %s: variant %q, want %q", c.class, got, c.want)
		}
	}
	if got := (setupProber{a: &App{opts: Options{Home: t.TempDir()}}}).markerVars(fail(""), probe.ClassNoAccess, raw, existing).Vars[onboard.VarFixVariant]; got != string(onboard.FixNoRead) {
		t.Errorf("marker of a store never read here: variant %q, want %q", got, onboard.FixNoRead)
	}
	for _, v := range []onboard.FixVariant{onboard.FixReachedBefore, onboard.FixUnreachable, onboard.FixNoRead} {
		if f := onboard.FixFor(onboard.CheckStoreMarker, "", v); f.Text == "" || strings.Contains(f.Text, "not a lore store yet") || strings.Contains(f.Text, "Save will run") {
			t.Errorf("marker fix %s: %q", v, f.Text)
		}
	}
	if f := onboard.FixFor(onboard.CheckStoreMarker, "", onboard.FixReachedBefore); !strings.Contains(f.Text, "access was most likely removed") {
		t.Errorf("reached-before marker fix does not name lost access: %q", f.Text)
	}
	// The fix text names no repo creation.
	if f := onboard.FixFor(onboard.CheckStoreRead, "", onboard.FixReachedBefore); f.Runnable || len(f.Command) != 0 || strings.Contains(f.Text, "repo create") {
		t.Fatalf("reached-before fix: %+v, want a text-only fix that creates nothing", f)
	}
}
