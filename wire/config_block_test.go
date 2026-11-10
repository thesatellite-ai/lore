package wire

import (
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/onboard"
)

// blockConfig is a machine with one solo setup, written by hand in block
// style.
const blockConfig = `identity:
  key: "human:ada@example.test"
  name: Ada Example
personal:
  store: "git@github.com:example/lore-me.git"
setups:
  - name: personal
    kind: solo
    default: true
    folders: [~/code/**]
repo_setups:
  rep_one: personal
`

// flowLines are the lines of a config.yaml that open a flow collection
// holding a list or a mapping (`key: [{…}]`, `key: {a: {…}}`): what a
// hand-written file never looks like.
func flowLines(raw string) []string {
	var out []string
	for line := range strings.SplitSeq(raw, "\n") {
		_, v, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if (strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{")) && strings.ContainsAny(v[1:], "[{") {
			out = append(out, line)
		}
	}
	return out
}

// TestPurgeLastThenAddIsBlock: the owner's sequence. Trashing the last
// setup leaves no `setups: []` / `repo_setups: {}`, purging it leaves no
// `setups: []` in the trash, and the next add writes the list one key per
// line, never `setups: [{name: …, identity: {…}}]` on one line.
func TestPurgeLastThenAddIsBlock(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, blockConfig)
	at := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	if found, err := TrashSetup(home, "personal", at); err != nil || !found {
		t.Fatalf("trash: %v %v", found, err)
	}
	got := readSetupsFile(t, home)
	for _, bad := range []string{"[]", "{}", "\nsetups:", "repo_setups:"} {
		if strings.Contains(got, bad) {
			t.Fatalf("trashing the last setup left %q:\n%s", bad, got)
		}
	}
	if _, err := PurgeSetup(home, "personal", PurgeOptions{At: at}); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if got = readSetupsFile(t, home); strings.Contains(got, "[]") || strings.Contains(got, "{}") {
		t.Fatalf("purging the last trashed setup left an empty list:\n%s", got)
	}
	if _, err := PutSetup(home, onboard.SetupEntry{Name: "personal", Kind: onboard.KindSolo, Default: true,
		Folders: []string{"~/code/**"}, Identity: onboard.SetupIdentity{Key: "human:ada@example.test", Name: "Ada Example"}}); err != nil {
		t.Fatalf("add: %v", err)
	}
	got = readSetupsFile(t, home)
	if f := flowLines(got); len(f) > 0 || !strings.Contains(got, "\nsetups:\n") || !strings.Contains(got, "    identity:\n") {
		t.Fatalf("the added setup is not block style (flow lines %q):\n%s", f, got)
	}
	if c, err := ValidateSetupsConfig(home); err != nil || len(c.Setups) != 1 || c.Setups[0].Identity.Key != "human:ada@example.test" {
		t.Fatalf("re-read: %+v %v", c, err)
	}
}

// flowConfig is the owner's file after the bug: the setups list on one
// line, the rest block style.
const flowConfig = `identity:
  key: "human:ada@example.test"
  name: Ada Example
setups: [{name: personal, kind: solo, default: true, identity: {key: "human:ada@example.test", name: Ada Example}, folders: [~/code/**]}]
tools: [claude, codex]
trash:
  purged:
    - name: old
      kind: solo
`

// TestFlowConfigRepairedOnWrite: any write to a file that already holds a
// nested flow list (here an unrelated repo pin) writes it back block
// style, keeps every value, and leaves a flat scalar list as written.
func TestFlowConfigRepairedOnWrite(t *testing.T) {
	t.Parallel()
	home := writeSetupsConfig(t, flowConfig)
	if err := SetRepoSetup(home, "rep_one", "personal"); err != nil {
		t.Fatal(err)
	}
	got := readSetupsFile(t, home)
	if f := flowLines(got); len(f) > 0 {
		t.Fatalf("still flow after a write: %q\n%s", f, got)
	}
	// The top-level identity: equals the default setup's, so the write
	// also retired it (SOURCE_OF_TRUTH row 1): setups: is now the first key.
	if strings.HasPrefix(got, "identity:") {
		t.Fatalf("top-level identity left beside the same setup identity:\n%s", got)
	}
	for _, want := range []string{"setups:\n  - name: personal\n", "    identity:\n      key: ", "tools: [claude, codex]\n", "rep_one: personal"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	c, err := ValidateSetupsConfig(home)
	if err != nil || len(c.Setups) != 1 || !c.Setups[0].Default || len(c.Setups[0].Folders) != 1 {
		t.Fatalf("re-read: %+v %v", c, err)
	}
}
