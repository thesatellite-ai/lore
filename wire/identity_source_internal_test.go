package wire

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/model/errs"
)

// The owner's real case with placeholders (SOURCE_OF_TRUTH row 1): the
// top-level identity says Pat One, the default solo setup says Pat Two,
// one key.
const (
	srcTopPatOne = "identity:\n  key: human:pat@example.com\n  name: Pat One\n"
	srcTopPatTwo = "identity:\n  key: human:pat@example.com\n  name: Pat Two\n"
	srcSetupHead = "setups:\n  - name: personal\n    kind: solo\n    default: true\n"
	srcSetupTwo  = srcSetupHead + "    identity:\n      key: human:pat@example.com\n      name: Pat Two\n"
)

// srcHome writes config.yaml into a fresh lore home.
func srcHome(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ConfigFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// srcDoc reads config.yaml back as the setups document.
func srcDoc(t *testing.T, home string) setupsDoc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	d, err := decodeSetupsDoc(raw)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// otherWrite is an ordinary config write that is not about identity.
func otherWrite(t *testing.T, home string) {
	t.Helper()
	err := editMachineConfig(home, func(root *yaml.Node) error {
		mapSet(mapChild(root, "setup"), "idle_minutes", &yaml.Node{Kind: yaml.ScalarNode, Value: "45"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestConfigWriteMigratesSameIdentity: the next write retires a top-level
// block the default setup already holds (leftover) or lacks (fill).
func TestConfigWriteMigratesSameIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body, wantName string }{
		{"same value", srcTopPatTwo + srcSetupTwo, "Pat Two"},
		{"setup has none", srcTopPatOne + srcSetupHead, "Pat One"},
		{"key-less setup, same name", srcTopPatTwo + srcSetupHead + "    identity:\n      name: Pat Two\n", "Pat Two"},
	} {
		home := srcHome(t, tc.body)
		otherWrite(t, home)
		d := srcDoc(t, home)
		if !d.Identity.IsZero() {
			t.Errorf("%s: top-level identity left: %+v", tc.name, d.Identity)
		}
		got := d.Setups[0].Identity
		if got.Key != "human:pat@example.com" || got.Name != tc.wantName {
			t.Errorf("%s: setup identity = %+v, want pat / %s", tc.name, got, tc.wantName)
		}
	}
}

// TestConfigWriteNeverPicksBetweenTwoNames: two names stay two names after
// a write; migrate without --keep refuses and leaves the file byte for
// byte; each --keep keeps its own value. Break-verified once: Decide
// answering SourceFill for a conflict made the first check fail.
func TestConfigWriteNeverPicksBetweenTwoNames(t *testing.T) {
	t.Parallel()
	home := srcHome(t, srcTopPatOne+srcSetupTwo)
	otherWrite(t, home)
	d := srcDoc(t, home)
	if d.Identity.Name != "Pat One" || d.Setups[0].Identity.Name != "Pat Two" {
		t.Fatalf("a write picked a name: top %+v, setup %+v", d.Identity, d.Setups[0].Identity)
	}
	before, _ := os.ReadFile(filepath.Join(home, ConfigFile))
	_, err := migrateTopIdentity(home, "personal", "")
	if !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), "Pat One") || !strings.Contains(err.Error(), "Pat Two") {
		t.Fatalf("migrate without --keep = %v, want E_REFUSED naming both", err)
	}
	if after, _ := os.ReadFile(filepath.Join(home, ConfigFile)); !bytes.Equal(before, after) {
		t.Fatalf("a refused migrate rewrote config.yaml:\n%s", after)
	}
	for _, tc := range []struct {
		keep identity.Keep
		want string
	}{{identity.KeepSetup, "Pat Two"}, {identity.KeepTop, "Pat One"}} {
		h := srcHome(t, srcTopPatOne+srcSetupTwo)
		m, err := migrateTopIdentity(h, "personal", tc.keep)
		if err != nil || !m.Changed || m.Verdict != identity.SourceConflict {
			t.Fatalf("keep %s: %+v, %v", tc.keep, m, err)
		}
		d := srcDoc(t, h)
		if !d.Identity.IsZero() || d.Setups[0].Identity.Name != tc.want {
			t.Errorf("keep %s: top %+v, setup %+v; want no top and %s", tc.keep, d.Identity, d.Setups[0].Identity, tc.want)
		}
	}
}

// TestDoctorFlagsTopLevelIdentity: a leftover gets one fix, two names get
// one fix per value.
func TestDoctorFlagsTopLevelIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"leftover", srcTopPatTwo + srcSetupTwo, []string{"old copy", "lore identity migrate --keep setup --setup personal"}},
		{"two names", srcTopPatOne + srcSetupTwo, []string{"never picks", "--keep setup --setup personal (keeps Pat Two", "--keep top --setup personal (keeps Pat One"}},
	} {
		c, err := ReadSetupsConfig(srcHome(t, tc.body))
		if err != nil {
			t.Fatal(err)
		}
		f, ok := topLevelFinding(c, c.Setups[0])
		if !ok {
			t.Fatalf("%s: no finding", tc.name)
		}
		for _, w := range tc.want {
			if !strings.Contains(f.Line(), w) {
				t.Errorf("%s: %q lacks %q", tc.name, f.Line(), w)
			}
		}
	}
	c, err := ReadSetupsConfig(srcHome(t, srcSetupTwo))
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := topLevelFinding(c, c.Setups[0]); ok {
		t.Errorf("no top level, yet a finding: %s", f.Line())
	}
}

// TestKeylessSetupIdentityReported: a solo setup identity without key: is
// reported by config check (with the line and a fix) instead of being
// skipped silently; a write is not refused for it.
func TestKeylessSetupIdentityReported(t *testing.T) {
	t.Parallel()
	home := srcHome(t, srcSetupHead+"    identity:\n      name: Pat Two\n")
	_, err := standaloneConfig{home: home}.CheckConfig(context.Background())
	if !errs.Has(err, errs.ConfigInvalid) || !strings.Contains(err.Error(), "setups[personal].identity.key: missing") {
		t.Fatalf("config check = %v, want the missing key named", err)
	}
	a := &App{opts: Options{Home: home}}
	ps := a.ConfigProblems(context.Background())
	if len(ps) != 1 || ps[0].Line != 5 || !strings.Contains(ps[0].Fix, "key: human:<your email>") {
		t.Fatalf("config problems = %+v", ps)
	}
	otherWrite(t, home) // reported, never refused
}

// TestSetAndUnsetWriteTheSetup: identity set's file side writes the
// setup's block (its emails kept) and unset clears it again.
func TestSetAndUnsetWriteTheSetup(t *testing.T) {
	t.Parallel()
	home := srcHome(t, srcSetupHead+"    identity:\n      key: human:pat@example.com\n      name: Pat Two\n      emails: [pat@work.example]\n")
	if err := setSetupIdentity(home, "personal", identity.Entry{Key: "human:pat@example.com", Name: "Pat Three"}); err != nil {
		t.Fatal(err)
	}
	got := srcDoc(t, home).Setups[0].Identity
	if got.Name != "Pat Three" || len(got.Emails) != 1 {
		t.Fatalf("after set: %+v", got)
	}
	if cleared, err := clearSetupIdentity(home, "personal"); err != nil || !cleared {
		t.Fatalf("clear: %v %v", cleared, err)
	}
	got = srcDoc(t, home).Setups[0].Identity
	if got.Key != "" || got.Name != "" || len(got.Emails) != 1 {
		t.Fatalf("after unset: %+v (the extra addresses stay)", got)
	}
}

// TestSetANewEmailKeepsTheOldKey: identity set with a new key on the same
// solo setup appends the old key to aliases: once (oldest first), so the
// actor keeps the first key's id; the same key again adds nothing.
func TestSetANewEmailKeepsTheOldKey(t *testing.T) {
	t.Parallel()
	home := srcHome(t, srcSetupHead+"    identity:\n      key: human:pat@old.example\n      name: Pat\n")
	for _, k := range []string{"human:pat@new.example", "human:pat@new.example", "human:pat@third.example"} {
		if err := setSetupIdentity(home, "personal", identity.Entry{Key: k, Name: "Pat"}); err != nil {
			t.Fatal(err)
		}
	}
	got := srcDoc(t, home).Setups[0].Identity
	if got.Key != "human:pat@third.example" || !slices.Equal(got.Aliases, []string{"human:pat@old.example", "human:pat@new.example"}) {
		t.Fatalf("after two email changes: %+v, want key third and aliases [old new]", got)
	}
	raw, err := os.ReadFile(filepath.Join(home, ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "aliases: [") {
		t.Errorf("aliases written in flow style:\n%s", raw)
	}
}
