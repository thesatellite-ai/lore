//go:build machines && !windows

package machines

// life_model.go — the model the lifecycle tests (life_setups_test.go,
// life_objects_test.go) hold of a machine's setups, and checkModel, which
// compares every surface lore offers for that state against it after
// every transition:
//
//	setup list --json         names, disabled, default, leftovers
//	setup list in each folder the setup that applies there (lore's "which")
//	~/.lore/config.yaml       block style, parseable, exactly the setups, no
//	                          leftover identity of a gone setup at the top
//	repo files                pointer blocks once, hand-written text kept,
//	                          hooks unchanged by setup work
//	doctor --all --json       a report on exactly the live enabled setups,
//	                          none of them failing an error-level check
//	setup plan / setup check  the page's reads: a finished setup has no
//	                          progress rows, a name check says "taken"
//	                          exactly for a listed setup
//
// The model starts from what the shape already holds (baseline), so the
// same lifecycle runs on every shape in EveryShape.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// lmSetup is one setup the test drives.
type lmSetup struct {
	Kind     string
	Disabled bool
	Folders  []string
	Email    string
	// OldEmails are a solo setup's earlier emails: its identity keeps them
	// as aliases: (an email change keeps one person, SOURCE_OF_TRUTH), so
	// config.yaml still names them while the setup lives.
	OldEmails []string
}

// lmBase is a setup the shape already had.
type lmBase struct {
	Disabled bool
}

// lifeModel is what the machine's setups should be.
type lifeModel struct {
	base map[string]lmBase
	ours map[string]*lmSetup
	// trashed / purged hold our setups' names in those after-lives.
	trashed, purged []string
	// def is the default setup's name ("" = none).
	def string
	// baseActive is the setup active in each probe folder at the start,
	// with its reason; it holds where none of ours covers the folder.
	baseActive map[string]lmActive
	// baseLeftovers is how many leftovers setup list named at the start.
	baseLeftovers int
	// emails are identity emails only our setups use: once every setup
	// that used one is gone, config.yaml outside trash: never names it.
	emails map[string]bool
	// repoHash is each repo's committed files and hooks at the start;
	// claudeMD the hand-written CLAUDE.md's text (read through a link).
	repoHash map[string]string
	claudeMD string
	gone     Gone
	// wroteSetups: a setup of ours was written, so lore rewrote setups:.
	wroteSetups bool
	// baseWarnings are config check's warnings on the shape as built (a
	// key the person wrote that lore does not know); they must stay
	// exactly these, and no new one may appear.
	baseWarnings []string
}

type lmActive struct{ Name, Why string }

// lmProbeRepos are the folders where checkModel asks which setup applies.
var lmProbeRepos = []string{RepoWorkApp, RepoPlain, RepoLore}

// lmWhyDefault is setup list's reason for "the default setup applies".
const lmWhyDefault = "default"

// newLifeModel reads the shape's state into the model's baseline.
func newLifeModel(t *testing.T, m *Machine) *lifeModel {
	t.Helper()
	full := lmList(t, m, "")
	lm := &lifeModel{base: map[string]lmBase{}, ours: map[string]*lmSetup{}, baseActive: map[string]lmActive{},
		emails: map[string]bool{}, repoHash: map[string]string{}, gone: goneAtStart(m.Shape)}
	for _, s := range full.Setups {
		lm.base[s.Name] = lmBase{Disabled: s.Disabled}
		if s.Default {
			lm.def = s.Name
		}
	}
	lm.baseLeftovers = len(full.Leftovers)
	lm.baseWarnings = lmWarnings(t, m)
	for _, r := range lmProbeRepos {
		a := lmList(t, m, m.Repos[r]).Active
		lm.baseActive[r] = lmActive{a.Name, a.Why}
	}
	for _, r := range []string{RepoWorkApp, RepoClaude, RepoAgents, RepoLore} {
		lm.repoHash[r] = lmRepoHash(t, m.Repos[r])
	}
	b, _ := os.ReadFile(filepath.Join(m.Repos[RepoClaude], "CLAUDE.md"))
	lm.claudeMD = string(b)
	return lm
}

// lmListOut is the whole of setup list --json the model reads.
type lmListOut struct {
	Setups []struct {
		Name     string   `json:"name"`
		Kind     string   `json:"kind"`
		Default  bool     `json:"default"`
		Disabled bool     `json:"disabled"`
		Folders  []string `json:"folders"`
	} `json:"setups"`
	Active struct {
		Name string `json:"name"`
		Why  string `json:"why"`
	} `json:"active"`
	Trashed   int               `json:"trashed"`
	Leftovers []json.RawMessage `json:"leftovers"`
}

func lmList(t *testing.T, m *Machine, cwd string) lmListOut {
	t.Helper()
	r := m.Lore(t, cwd, "setup", "list", "--json")
	var o lmListOut
	if r.Code != 0 || json.Unmarshal([]byte(r.Stdout), &o) != nil {
		t.Fatalf("setup list: %s", r)
	}
	return o
}

// ── model transitions ─────────────────────────────────────────────────

func (lm *lifeModel) add(name string, s lmSetup) {
	cp := s
	lm.ours[name] = &cp
	lm.wroteSetups = true
	lm.purged = slices.DeleteFunc(lm.purged, func(n string) bool { return n == name })
	if s.Email != "" {
		lm.emails[s.Email] = true
	}
}

func (lm *lifeModel) setDefault(name string, on bool) {
	switch {
	case on:
		lm.def = name
	case lm.def == name:
		lm.def = ""
	}
}

func (lm *lifeModel) trash(name string) {
	delete(lm.ours, name)
	lm.trashed = append(lm.trashed, name)
}

// trashedSetups keeps the trashed entries for restore.
func (lm *lifeModel) restore(name, as string, s lmSetup) {
	lm.trashed = slices.DeleteFunc(lm.trashed, func(n string) bool { return n == name })
	cp := s
	lm.ours[as] = &cp
	if lm.def == name && as != name {
		lm.def = as
	}
}

func (lm *lifeModel) purge(name string) {
	lm.trashed = slices.DeleteFunc(lm.trashed, func(n string) bool { return n == name })
	lm.purged = append(lm.purged, name)
	if lm.def == name {
		lm.def = ""
	}
}

// liveNames are every listed setup: the shape's own and ours.
func (lm *lifeModel) liveNames() []string {
	var out []string
	for n := range lm.base {
		out = append(out, n)
	}
	for n := range lm.ours {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func (lm *lifeModel) disabled(name string) bool {
	if s, ok := lm.ours[name]; ok {
		return s.Disabled
	}
	return lm.base[name].Disabled
}

// covers reports whether a folder glob of ours holds dir.
func covers(home string, globs []string, dir string) bool {
	for _, g := range globs {
		p := strings.TrimSuffix(strings.TrimSuffix(g, "**"), "/")
		p = strings.Replace(p, "~", home, 1)
		if dir == p || strings.HasPrefix(dir, p+"/") {
			return true
		}
	}
	return false
}

// wantActive is the setup that applies in repo: ours when an enabled one
// of ours covers it (our globs are deeper than any shape's), else the
// shape's folder match, else the default when enabled.
func (lm *lifeModel) wantActive(m *Machine, repo string) string {
	dir := m.Repos[repo]
	for n, s := range lm.ours {
		if !s.Disabled && covers(m.Home, s.Folders, dir) {
			return n
		}
	}
	if b := lm.baseActive[repo]; b.Name != "" && b.Why != lmWhyDefault {
		if _, live := lm.base[b.Name]; live && !lm.base[b.Name].Disabled {
			return b.Name
		}
	}
	if lm.def != "" && !lm.disabled(lm.def) {
		if _, live := lm.base[lm.def]; live {
			return lm.def
		}
		if _, live := lm.ours[lm.def]; live {
			return lm.def
		}
	}
	return ""
}

// ── the check ─────────────────────────────────────────────────────────

// checkModel compares every surface against the model (see the file
// comment). step names the transitions so far.
func (lm *lifeModel) checkModel(t *testing.T, m *Machine, step string) {
	t.Helper()
	gone := lm.gone
	gone.Trashed = append(slices.Clone(gone.Trashed), lm.trashed...)
	gone.Purged = append(slices.Clone(gone.Purged), lm.purged...)
	if len(lm.baseWarnings) == 0 {
		m.CheckMachine(t, step, gone)
	} else {
		// CheckMachine wants no config warning at all; this shape has
		// the person's own. Same invariants, with the warnings compared
		// to the shape's.
		if w := lmWarnings(t, m); !slices.Equal(w, lm.baseWarnings) {
			t.Errorf("[%s] config check warns %v, the shape had %v", step, w, lm.baseWarnings)
		}
		m.CheckDoctor(t, step)
		m.CheckModes(t, step)
	}
	lm.checkLight(t, m, step)
}

// lmWarnings is config check --json's warnings.
func lmWarnings(t *testing.T, m *Machine) []string {
	t.Helper()
	r := m.Lore(t, "", "config", "check", "--json")
	var o struct {
		Warnings []string `json:"warnings"`
	}
	_ = json.Unmarshal([]byte(r.Stdout), &o)
	for i, w := range o.Warnings {
		o.Warnings[i] = lmWarningLine.ReplaceAllString(w, "")
	}
	return o.Warnings
}

// lmWarningLine is the "line N: " of a config check warning. The
// comparison with the shape's warnings drops it: a config write retires
// the old top-level identity: block (SOURCE_OF_TRUTH row 1), so the
// person's own unknown key moves up the file while the warning about it
// stays the same one.
var lmWarningLine = regexp.MustCompile(`line [0-9]+: `)

// checkLight is checkModel without the machine-wide invariants
// (CheckMachine: import-scan, discover, file modes, …), for the
// transitions between milestones that change one step of one setup; it
// keeps every comparison against the model.
func (lm *lifeModel) checkLight(t *testing.T, m *Machine, step string) {
	t.Helper()
	l := lmList(t, m, "")
	var names []string
	for _, s := range l.Setups {
		names = append(names, s.Name)
		if s.Disabled != lm.disabled(s.Name) {
			t.Errorf("[%s] %s disabled=%v, model %v", step, s.Name, s.Disabled, lm.disabled(s.Name))
		}
		if s.Default != (s.Name == lm.def) {
			t.Errorf("[%s] %s default=%v, model's default is %q", step, s.Name, s.Default, lm.def)
		}
		if o, ok := lm.ours[s.Name]; ok {
			if s.Kind != o.Kind {
				t.Errorf("[%s] %s kind %q, model %q", step, s.Name, s.Kind, o.Kind)
			}
			if !slices.Equal(s.Folders, o.Folders) {
				t.Errorf("[%s] %s folders %v, model %v", step, s.Name, s.Folders, o.Folders)
			}
		}
	}
	slices.Sort(names)
	if want := lm.liveNames(); !slices.Equal(names, want) {
		t.Errorf("[%s] setup list names %v, model %v", step, names, want)
	}
	if len(l.Leftovers) != lm.baseLeftovers {
		t.Errorf("[%s] setup list names %d leftovers, the shape had %d: %s", step, len(l.Leftovers), lm.baseLeftovers, l.Leftovers)
	}
	for _, r := range lmProbeRepos {
		a := lmList(t, m, m.Repos[r]).Active
		if want := lm.wantActive(m, r); a.Name != want {
			t.Errorf("[%s] in %s the active setup is %q (%s), model %q", step, r, a.Name, a.Why, want)
		}
	}
	lm.checkConfig(t, m, step)
	lm.checkRepos(t, m, step)
	lm.checkDoctor(t, m, step)
	lm.checkPage(t, m, step)
}

// checkConfig: config.yaml parses, setups: is block style all the way
// down, holds exactly the live setups once each, and outside trash: and
// orgs: names no email only gone setups of ours used.
func (lm *lifeModel) checkConfig(t *testing.T, m *Machine, step string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(m.Home, ".lore", "config.yaml"))
	if os.IsNotExist(err) && len(lm.liveNames()) == 0 {
		return // a machine with no setups need not have written one
	}
	if err != nil {
		t.Errorf("[%s] config.yaml: %v", step, err)
		return
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err == nil && len(doc.Content) == 0 && len(lm.liveNames()) == 0 {
		return // emptied (every key cleared): an empty mapping
	}
	if err != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		t.Errorf("[%s] config.yaml is not a YAML mapping (%v):\n%s", step, err, b)
		return
	}
	top := doc.Content[0]
	var names []string
	for i := 0; i+1 < len(top.Content); i += 2 {
		k, v := top.Content[i].Value, top.Content[i+1]
		switch k {
		case "setups":
			// The list itself and every entry lore wrote (ours) are block
			// style all the way down; an entry the person wrote keeps
			// the style they gave it.
			// (A list lore never rewrote, as a shape built it, may be
			// flow: the 0.1.17 migration wrote one.)
			if v.Style&yaml.FlowStyle != 0 && lm.wroteSetups {
				t.Errorf("[%s] config.yaml setups: is a flow-style list:\n%s", step, b)
			}
			for _, s := range v.Content {
				for j := 0; j+1 < len(s.Content); j += 2 {
					if s.Content[j].Value != "name" {
						continue
					}
					n := s.Content[j+1].Value
					names = append(names, n)
					if _, ours := lm.ours[n]; !ours {
						continue
					}
					if flow := firstFlow(s); flow != nil {
						t.Errorf("[%s] config.yaml setup %s has a flow-style node at line %d:\n%s", step, n, flow.Line, b)
					}
				}
			}
		case "trash", "orgs":
			// The trash holds removed entries; an org entry (with your
			// identity in it) outlives its setups by design (setup remove
			// and purge never touch orgs).
			continue
		}
		var out strings.Builder
		_ = yaml.NewEncoder(&out).Encode(v)
		for e := range lm.emails {
			if lm.emailUsed(e) {
				continue
			}
			if strings.Contains(out.String(), e) {
				t.Errorf("[%s] config.yaml %s: still names %s, which only a gone setup used:\n%s", step, k, e, b)
			}
		}
	}
	slices.Sort(names)
	if want := lm.liveNames(); !slices.Equal(names, want) {
		t.Errorf("[%s] config.yaml setups %v, model %v:\n%s", step, names, want, b)
	}
}

// emailUsed: a live setup of ours has email e.
func (lm *lifeModel) emailUsed(e string) bool {
	for _, s := range lm.ours {
		if s.Email == e || slices.Contains(s.OldEmails, e) {
			return true
		}
	}
	return false
}

// firstFlow is the first flow-style collection under n (nil: none).
func firstFlow(n *yaml.Node) *yaml.Node {
	if (n.Kind == yaml.SequenceNode || n.Kind == yaml.MappingNode) && n.Style&yaml.FlowStyle != 0 && len(n.Content) > 0 {
		return n
	}
	for _, c := range n.Content {
		if f := firstFlow(c); f != nil {
			return f
		}
	}
	return nil
}

// pointerStart opens lore's block in AGENTS.md / CLAUDE.md.
const pointerStart = "<!-- lore:pointer:start -->"

// checkRepos: setup work never touches a repo (its committed files and
// hooks hash as at the start), and each pointer file holds lore's block
// at most once with the hand-written text kept.
func (lm *lifeModel) checkRepos(t *testing.T, m *Machine, step string) {
	t.Helper()
	for r, h := range lm.repoHash {
		if got := lmRepoHash(t, m.Repos[r]); got != h {
			t.Errorf("[%s] setup work changed files or hooks in %s", step, r)
		}
		for _, f := range []string{"AGENTS.md", "CLAUDE.md"} {
			b, err := os.ReadFile(filepath.Join(m.Repos[r], f))
			if err != nil {
				continue
			}
			if c := strings.Count(string(b), pointerStart); c > 1 {
				t.Errorf("[%s] %s/%s holds lore's block %d times", step, r, f, c)
			}
		}
	}
	if b, _ := os.ReadFile(filepath.Join(m.Repos[RepoClaude], "CLAUDE.md")); string(b) != lm.claudeMD {
		t.Errorf("[%s] hand-written CLAUDE.md changed: %q, was %q", step, b, lm.claudeMD)
	}
}

// lmRepoHash hashes a repo's files (not lore's runtime ones) and its git
// hooks.
func lmRepoHash(t *testing.T, repo string) string {
	t.Helper()
	return csHash(t, repo, ".git", ".lore") + csHash(t, filepath.Join(repo, ".git", "hooks"))
}

// lmDoctor is the part of doctor --all --json the model reads.
type lmDoctor struct {
	Setups []struct {
		Name   string `json:"name"`
		Checks []struct {
			Check  string `json:"check"`
			Status string `json:"status"`
			Level  string `json:"level"`
			Detail string `json:"detail"`
		} `json:"checks"`
	} `json:"setups"`
}

// checkStatuses is the closed set of a check's status (error: the probe
// could not run).
var checkStatuses = []string{"ok", "warn", "fail", "skip", "error"}

// checkDoctor: doctor --all reports exactly the live enabled setups, and
// no setup of ours fails an error-level check.
func (lm *lifeModel) checkDoctor(t *testing.T, m *Machine, step string) {
	t.Helper()
	r := m.Lore(t, "", "doctor", "--all", "--json")
	var d lmDoctor
	if json.Unmarshal([]byte(r.Stdout), &d) != nil {
		t.Errorf("[%s] doctor --all: %s", step, r)
		return
	}
	var got, want []string
	for _, s := range d.Setups {
		got = append(got, s.Name)
		for _, c := range s.Checks {
			if !slices.Contains(checkStatuses, c.Status) {
				t.Errorf("[%s] doctor: %s %s has status %q", step, s.Name, c.Check, c.Status)
			}
			if _, ours := lm.ours[s.Name]; ours && (c.Status == "fail" || c.Status == "error") && c.Level == "error" {
				t.Errorf("[%s] doctor: %s fails %s: %s", step, s.Name, c.Check, c.Detail)
			}
		}
	}
	for _, n := range lm.liveNames() {
		if !lm.disabled(n) {
			want = append(want, n)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("[%s] doctor --all checks setups %v, model %v", step, got, want)
	}
}

// lmProgress is setup plan's progress list.
type lmProgress struct {
	Progress []struct {
		Step   string `json:"step"`
		Status string `json:"status"`
	} `json:"progress"`
}

// lmCheckOut is setup check --json.
type lmCheckOut struct {
	Checks []struct {
		Check  string `json:"check"`
		Status string `json:"status"`
		Detail string `json:"detail"`
	} `json:"checks"`
}

// nameCheck is the kind step's name check for name, asked while making
// another setup: true when it passes ("name is free").
func nameCheck(t *testing.T, m *Machine, name string) (free bool, detail string) {
	t.Helper()
	r := m.Lore(t, "", "setup", "check", "lm-probe", "--step", "kind", "--answers", `{"kind":"solo","name":"`+name+`"}`, "--json")
	var o lmCheckOut
	if json.Unmarshal([]byte(r.Stdout), &o) != nil {
		t.Fatalf("setup check: %s", r)
	}
	for _, c := range o.Checks {
		if c.Check == "setup.name_free" {
			return c.Status == "ok", c.Detail
		}
	}
	t.Fatalf("setup check ran no setup.name_free: %s", r)
	return false, ""
}

// checkPage: the reads the page makes agree with list. A finished setup
// of ours has no progress rows (editing it never reopens the new-setup
// flow); the name check says taken for every listed setup and free for
// every purged one of ours.
func (lm *lifeModel) checkPage(t *testing.T, m *Machine, step string) {
	t.Helper()
	for n := range lm.ours {
		r := m.Lore(t, "", "setup", "plan", "--setup", n, "--json")
		var p lmProgress
		if r.Code != 0 || json.Unmarshal([]byte(r.Stdout), &p) != nil {
			t.Errorf("[%s] setup plan --setup %s: %s", step, n, r)
			continue
		}
		// A finished setup reads as every step done (ONB 2.3), never as a
		// setup still being made.
		for _, pr := range p.Progress {
			if pr.Status != "done" {
				t.Errorf("[%s] finished setup %s: plan shows step %s %s (the page would reopen it as new)", step, n, pr.Step, pr.Status)
			}
		}
		if free, d := nameCheck(t, m, n); free {
			t.Errorf("[%s] name check says %q is free (%s) while setup list shows it", step, n, d)
		}
	}
	for _, n := range lm.purged {
		if _, back := lm.ours[n]; back {
			continue
		}
		if free, d := nameCheck(t, m, n); !free {
			t.Errorf("[%s] name check says purged %q is taken: %s", step, n, d)
		}
	}
}

// lmBrokenHome: shapes whose ~/.lore cannot be written, by design (a
// file in its place, a 0500 folder, a 0444 config.yaml): every life there ends at its first
// write, which must refuse clearly (lmRefusesLoreHome).
func lmBrokenHome(shape string) bool {
	return shape == ShapeEnvLoreHomeFile || shape == ShapeEnvLoreHomeReadOnly || shape == ShapeLinksReadonly
}

// lmRefusesLoreHome asserts r refused with an E_ code whose message or
// hint names ~/.lore and gives a next step, without a panic.
func lmRefusesLoreHome(t *testing.T, r Result) {
	t.Helper()
	e := csErrOf(r)
	switch {
	case r.Code == 0:
		t.Errorf("a write to a broken ~/.lore succeeded: %s", r)
	case strings.Contains(r.Stderr, "panic"):
		t.Errorf("a write to a broken ~/.lore panicked: %s", r)
	case e == nil || !strings.HasPrefix(e.Code, "E_"):
		t.Errorf("a write to a broken ~/.lore printed no E_ error: %s", r)
	case !strings.Contains(e.Message+e.Hint, ".lore"):
		t.Errorf("the refusal does not name ~/.lore: %s", r)
	case e.Hint == "" && e.Next == nil:
		t.Errorf("the refusal names no fix: %s", r)
	}
}
