//go:build machines && !windows

package machines

// life_objects_test.go — the lives of what sits around a setup, through
// the built binary on every shape: a repo's stores (init, detach,
// re-attach, remove), the lore skill per tool (install, reinstall,
// another tool), the top-level identity (set, change, clear), the
// personal store (set, clear as a leftover), the machine profile label
// (set, change, clear), live (on, off) and a row's trash (delete,
// restore, delete, purge). After every transition the setup model's
// checks run (lifeModel.checkModel: list, which setup applies,
// config.yaml, repo files, doctor, the page's reads), plus the object's
// own read-back.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// loShapes: every object life runs on these shapes; the others take one
// object each, rotating (envFull runs all).
var loShapes = map[string]bool{ShapeFresh: true, ShapeOwnerLike: true, ShapeGraveyard: true}

// loLife is one object's life on a machine.
type loLife struct {
	name string
	run  func(t *testing.T, m *Machine, lm *lifeModel, step func(string))
}

var loLives = []loLife{
	{"stores", loStores},
	{"skill", loSkill},
	{"identity", loIdentity},
	{"personal_store", loPersonalStore},
	{"machine_label", loMachineLabel},
	{"live", loLive},
	{"row_trash", loRowTrash},
}

// loNeedsHome are the lives whose first command writes ~/.lore, which a
// broken ~/.lore must refuse.
var loNeedsHome = map[string]bool{"identity": true, "personal_store": true, "machine_label": true, "live": true, "stores": true}

// loFirst is each life's first command.
var loFirst = map[string]func(m *Machine) []string{
	"stores": func(m *Machine) []string {
		return []string{"store", "init", "teamkb", "file://" + m.Dir + "/kb-store", "--yes"}
	},
	"skill":     func(*Machine) []string { return []string{"skill", "install", "--tool", "claude_code"} },
	"row_trash": func(*Machine) []string { return []string{"memory", "add", "--body", "a row to trash"} },
	"identity":  func(*Machine) []string { return []string{"identity", "set", "first-id@example.test"} },
	"personal_store": func(m *Machine) []string {
		return []string{"config", "set", "personal.store", "file://" + m.Dir + "/me-store"}
	},
	"machine_label": func(m *Machine) []string {
		return []string{"store", "init", "me", "file://" + m.Dir + "/me-store", "--yes"}
	},
	"live": func(*Machine) []string { return []string{"config", "set", "live.autopush", "true"} },
}

func TestLifeObjects(t *testing.T) {
	full := os.Getenv(envFull) != ""
	ran := 0
	for si, shape := range EveryShape() {
		for li, l := range loLives {
			if !full && !loShapes[shape] && (si+li)%len(loLives) != 0 {
				continue
			}
			ran++
			t.Run(shape+"/"+l.name, func(t *testing.T) {
				t.Parallel()
				m := Build(t, shape, loreBin)
				if lmBrokenHome(shape) {
					// The life's first command: refused clearly, or (a
					// write that needs nothing under ~/.lore) done.
					r := m.Lore(t, m.Repos[RepoLore], append(loFirst[l.name](m), "--json")...)
					if r.Code != 0 || loNeedsHome[l.name] {
						lmRefusesLoreHome(t, r)
					}
					return
				}
				lm := newLifeModel(t, m)
				l.run(t, m, lm, func(what string) {
					t.Helper()
					lm.checkModel(t, m, shape+"/"+l.name+"/"+what)
					if t.Failed() {
						t.FailNow()
					}
				})
			})
		}
	}
	t.Logf("%d object lives over %d shapes", ran, len(EveryShape()))
}

// loStoreList is store list --json: the names this repo sees.
func loStoreNames(t *testing.T, m *Machine, repo string) []string {
	t.Helper()
	var o struct {
		Stores []struct {
			Name string `json:"name"`
		} `json:"stores"`
	}
	out := lsRun(t, m, repo, "store", "list")
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("store list: %v: %s", err, out)
	}
	var names []string
	for _, s := range o.Stores {
		names = append(names, s.Name)
	}
	return names
}

// loStoresYAML checks the repo's .lore/stores.yaml: parseable, block
// style, naming exactly want among its stores.
func loStoresYAML(t *testing.T, repo, step string, want []string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, ".lore", "stores.yaml"))
	if err != nil {
		if len(want) > 0 {
			t.Errorf("[%s] stores.yaml: %v", step, err)
		}
		return
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Errorf("[%s] stores.yaml does not parse (%v):\n%s", step, err, b)
		return
	}
	if f := firstFlow(&doc); f != nil {
		t.Errorf("[%s] stores.yaml has a flow-style node at line %d:\n%s", step, f.Line, b)
	}
	var got []string
	for _, n := range want {
		if strings.Contains(string(b), "name: "+n+"\n") {
			got = append(got, n)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("[%s] stores.yaml names %v of %v:\n%s", step, got, want, b)
	}
}

// loStores: a folder store made, written to, detached, attached again
// (its rows come back), detached for good.
func loStores(t *testing.T, m *Machine, _ *lifeModel, step func(string)) {
	repo := m.Repos[RepoLore]
	const name = "teamkb"
	dir := filepath.Join(m.Dir, "kb-store")
	url := "file://" + dir
	has := func(what string, want bool) {
		t.Helper()
		if got := slices.Contains(loStoreNames(t, m, repo), name); got != want {
			t.Errorf("[%s] store list has %s=%v, want %v", what, name, got, want)
		}
		var w []string
		if want {
			w = []string{name}
		}
		loStoresYAML(t, repo, what, w)
	}
	lsRun(t, m, repo, "store", "init", name, url, "--yes")
	has("init", true)
	step("store init")
	out := lsRun(t, m, repo, "memory", "add", "--body", "kept in the team store", "--to", name)
	var mem struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(out), &mem)
	if mem.ID == "" {
		t.Fatalf("memory add printed no id: %s", out)
	}
	step("write")
	lsRun(t, m, repo, "store", "remove", name, "--yes")
	has("detach", false)
	step("detach")
	lsRun(t, m, repo, "store", "add", name, url, "--yes")
	has("re-attach", true)
	if got := lsRun(t, m, repo, "memory", "show", mem.ID); !strings.Contains(got, "kept in the team store") {
		t.Errorf("the re-attached store lost its row: %s", got)
	}
	step("re-attach")
	lsRun(t, m, repo, "store", "remove", name, "--yes")
	has("remove", false)
	step("remove")
}

// loSkillStatus is skill status --json for one tool: installed or not.
func loSkillInstalled(t *testing.T, m *Machine, tool string) bool {
	t.Helper()
	out := lsRun(t, m, "", "skill", "status", "--tool", tool)
	var o struct {
		Tools []struct {
			Tool      string `json:"tool"`
			Installed bool   `json:"installed"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("skill status: %v: %s", err, out)
	}
	for _, s := range o.Tools {
		if s.Tool == tool {
			return s.Installed
		}
	}
	t.Fatalf("skill status names no %s: %s", tool, out)
	return false
}

// loSkill: the skill installed for one tool, again (no change), then for
// a second tool; each one reads back installed.
func loSkill(t *testing.T, m *Machine, _ *lifeModel, step func(string)) {
	for _, tool := range []string{"claude_code", "codex"} {
		lsRun(t, m, "", "skill", "install", "--tool", tool)
		if !loSkillInstalled(t, m, tool) {
			t.Errorf("skill install --tool %s: status says not installed", tool)
		}
		step("install " + tool)
		lsRun(t, m, "", "skill", "install", "--tool", tool)
		if !loSkillInstalled(t, m, tool) {
			t.Errorf("skill reinstall --tool %s: status says not installed", tool)
		}
		step("reinstall " + tool)
	}
}

// loIdentityKey is identity show --json's resolved key.
func loIdentityShow(t *testing.T, m *Machine) string {
	t.Helper()
	return lsRun(t, m, m.Repos[RepoPlain], "identity", "show")
}

// loIdentity: set, change, clear the top-level identity. A shape whose
// setups use the top identity keeps working; the leftovers count follows
// whether a setup uses it.
func loIdentity(t *testing.T, m *Machine, lm *lifeModel, step func(string)) {
	for _, e := range []string{"first-id@example.test", "second-id@example.test"} {
		lsRun(t, m, "", "identity", "set", e)
		if got := csConfig(t, m); !strings.Contains(got, "human:"+e) {
			t.Errorf("identity set %s: config.yaml lacks it:\n%s", e, got)
		}
		// Set by hand: yours, never a leftover Home offers to clear.
		step("set " + e)
	}
	lsRun(t, m, "", "identity", "unset")
	if got := csConfig(t, m); strings.Contains(got, "second-id@example.test") {
		t.Errorf("identity unset: config.yaml still names it:\n%s", got)
	}
	if s := loIdentityShow(t, m); strings.Contains(s, "second-id@example.test") {
		t.Errorf("identity show after unset: %s", s)
	}
	step("unset")
}

// loPersonalStore: personal.store set by hand (never a leftover), read
// back, cleared with config unset.
func loPersonalStore(t *testing.T, m *Machine, lm *lifeModel, step func(string)) {
	url := "file://" + filepath.Join(m.Dir, "me-store")
	lsRun(t, m, "", "config", "set", "personal.store", url)
	if got := strings.TrimSpace(m.Lore(t, "", "config", "get", "personal.store").Stdout); got != url {
		t.Errorf("config get personal.store = %q, want %q", got, url)
	}
	// Set by hand: not a leftover (checkModel counts them).
	step("set")
	lsRun(t, m, "", "config", "unset", "personal.store")
	if strings.Contains(csConfig(t, m), url) {
		t.Errorf("config unset personal.store left it in config.yaml:\n%s", csConfig(t, m))
	}
	step("clear")
}

// loMachineLabel: this machine's label set, changed, cleared, each read
// back by machine list in a lore repo.
func loMachineLabel(t *testing.T, m *Machine, _ *lifeModel, step func(string)) {
	repo := m.Repos[RepoLore]
	// A profile lives in an own store: the personal one, a local folder.
	lsRun(t, m, repo, "store", "init", "me", "file://"+filepath.Join(m.Dir, "me-store"), "--yes")
	step("personal store")
	for _, label := range []string{"box-one", "box-two", ""} {
		lsRun(t, m, repo, "machine", "label", label)
		out := lsRun(t, m, repo, "machine", "list")
		for _, old := range []string{"box-one", "box-two"} {
			if old != label && strings.Contains(out, `"`+old+`"`) {
				t.Errorf("label %q: machine list still shows %q: %s", label, old, out)
			}
		}
		if label != "" && !strings.Contains(out, label) {
			t.Errorf("label %q: machine list lacks it: %s", label, out)
		}
		step("label " + label)
	}
}

// loLive: live autopush and a type's live default switched on and off;
// config get reads each back and config check knows the keys.
func loLive(t *testing.T, m *Machine, _ *lifeModel, step func(string)) {
	for _, on := range []string{"true", "false", "true"} {
		for _, k := range []string{"live.autopush", "live.defaults.memories"} {
			lsRun(t, m, "", "config", "set", k, on)
			if got := strings.TrimSpace(m.Lore(t, "", "config", "get", k).Stdout); got != on {
				t.Errorf("config get %s = %q after set %s", k, got, on)
			}
		}
		step("live " + on)
	}
}

// loTrashList is trash list --json's ids.
func loTrashIDs(t *testing.T, m *Machine, repo string) string {
	t.Helper()
	return lsRun(t, m, repo, "trash", "list")
}

// loRowTrash: a row deleted, restored, deleted, purged; the trash and
// the list agree at every step and a purged row never comes back.
func loRowTrash(t *testing.T, m *Machine, _ *lifeModel, step func(string)) {
	repo := m.Repos[RepoLore]
	out := lsRun(t, m, repo, "memory", "add", "--body", "a row to trash")
	var mem struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(out), &mem)
	if mem.ID == "" {
		t.Fatalf("memory add printed no id: %s", out)
	}
	listed := func(what string, inList, inTrash bool) {
		t.Helper()
		l := lsRun(t, m, repo, "memory", "list")
		if strings.Contains(l, mem.ID) != inList {
			t.Errorf("[%s] memory list has %s=%v, want %v", what, mem.ID, !inList, inList)
		}
		if strings.Contains(loTrashIDs(t, m, repo), mem.ID) != inTrash {
			t.Errorf("[%s] trash list has %s=%v, want %v", what, mem.ID, !inTrash, inTrash)
		}
	}
	listed("add", true, false)
	step("add")
	lsRun(t, m, repo, "memory", "delete", mem.ID, "--yes")
	listed("delete", false, true)
	step("delete")
	lsRun(t, m, repo, "trash", "restore", mem.ID, "--yes")
	listed("restore", true, false)
	step("restore")
	lsRun(t, m, repo, "memory", "delete", mem.ID, "--yes")
	lsRun(t, m, repo, "trash", "purge", mem.ID, "--confirm")
	listed("purge", false, false)
	if r := m.Lore(t, repo, "trash", "restore", mem.ID, "--yes", "--json"); r.Code == 0 {
		t.Errorf("a purged row was restored: %s", r)
	}
	step("purge")
}
