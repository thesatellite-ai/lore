//go:build machines && !windows

package machines

// campaign_setup_cli_test.go — the setup area (setup, onboard, doctor,
// machine, whoami, identity, skill, support-bundle) driven through the
// real binary on realistic machine shapes, the way a person runs it.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// csCommands is every command of the setup area (inventory.md).
var csCommands = [][]string{
	{"doctor", "run"}, {"identity", "set"}, {"identity", "show"}, {"identity", "unset"},
	{"machine", "label"}, {"machine", "list"}, {"machine", "show"}, {"onboard"},
	{"setup", "aliases"}, {"setup", "apply"}, {"setup", "browse"}, {"setup", "check"},
	{"setup", "disable"}, {"setup", "discover"}, {"setup", "edit"}, {"setup", "enable"},
	{"setup", "fix"}, {"setup", "folders"}, {"setup", "import"}, {"setup", "import-dismiss"},
	{"setup", "import-scan"}, {"setup", "join"}, {"setup", "list"}, {"setup", "plan"},
	{"setup", "remove"}, {"setup", "repair"}, {"setup", "run"}, {"setup", "show"},
	{"setup", "trash", "list"}, {"setup", "trash", "purge"}, {"setup", "trash", "restore"},
	{"setup", "use"}, {"setup", "web"}, {"skill", "compile"}, {"skill", "install"},
	{"skill", "status"}, {"support-bundle", "run"}, {"whoami", "show"},
}

// TestCampaignSetup_HelpExamples: every command's --help exits 0 and shows
// at least one runnable `lore …` example line.
func TestCampaignSetup_HelpExamples(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	for _, c := range csCommands {
		r := m.Lore(t, "", append(c, "--help")...)
		name := strings.Join(c, " ")
		if r.Code != 0 {
			t.Errorf("%s --help: %s", name, r)
			continue
		}
		_, ex, ok := strings.Cut(r.Stdout, "\nExamples:\n")
		if !ok {
			t.Errorf("lore %s --help has no Examples block", name)
			continue
		}
		block, _, _ := strings.Cut(ex, "\n\n")
		if !strings.Contains(block, "  lore ") {
			t.Errorf("lore %s --help: no `lore …` example line in\n%s", name, block)
		}
	}
}

// csRead is one read command checked on every shape: its --json output
// conforms to the documented schema of op.
type csRead struct {
	op   string
	args []string
	// codes besides 0 the command may exit with (doctor: 1 degraded, 2 broken).
	codes []int
}

var csReads = []csRead{
	{"setup.list", []string{"setup", "list", "--json"}, nil},
	{"setup.trash.list", []string{"setup", "trash", "list", "--json"}, nil},
	{"setup.plan", []string{"setup", "plan", "--json"}, nil},
	{"setup.folders", []string{"setup", "folders", "--json"}, nil},
	{"setup.browse", []string{"setup", "browse", "--json"}, nil},
	{"setup.aliases", []string{"setup", "aliases", "--json"}, nil},
	{"setup.discover", []string{"setup", "discover", "--json"}, nil},
	{"setup.import_scan", []string{"setup", "import-scan", "--json"}, nil},
	{"whoami.show", []string{"whoami", "show", "--json"}, nil},
	{"skill.status", []string{"skill", "status", "--json"}, nil},
	{"doctor.run", []string{"doctor", "run", "--all", "--json"}, []int{1, 2}},
	{"doctor.run", []string{"doctor", "run", "--json"}, []int{1, 2}},
	{"identity.show", []string{"identity", "show", "--json"}, nil},
}

// TestCampaignSetup_TextOutputs: the same reads without --json print
// lines for a person: something on stdout, no error line, the same exit.
func TestCampaignSetup_TextOutputs(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeOwnerLike, loreBin)
	for _, rd := range csReads {
		args := make([]string, 0, len(rd.args))
		for _, a := range rd.args {
			if a != "--json" {
				args = append(args, a)
			}
		}
		for _, cwd := range []string{"", m.Repos[RepoLore]} {
			r := m.Lore(t, cwd, args...)
			if r.Code != 0 && !containsInt(rd.codes, r.Code) {
				t.Errorf("lore %s in %q: %s", strings.Join(args, " "), cwd, r)
				continue
			}
			if strings.TrimSpace(r.Stdout) == "" || strings.Contains(r.Stdout+r.Stderr, "ERROR:") {
				t.Errorf("lore %s in %q printed no lines or an error: %s", strings.Join(args, " "), cwd, r)
			}
			if strings.HasPrefix(strings.TrimSpace(r.Stdout), "{") {
				t.Errorf("lore %s without --json printed JSON: %.200s", strings.Join(args, " "), r.Stdout)
			}
		}
	}
	for _, args := range [][]string{{"setup", "show", "main"}, {"machine", "list"}, {"whoami", "show"}} {
		r := m.Lore(t, m.Repos[RepoLore], args...)
		if r.Code != 0 || strings.TrimSpace(r.Stdout) == "" {
			t.Errorf("lore %s: %s", strings.Join(args, " "), r)
		}
	}
}

// TestCampaignSetup_ReadsEveryShape: each read command on each machine
// shape answers in the JSON shape its OpenAPI schema documents, never reads a *.migrated
// backup, and changes nothing a user owns (~/.ssh, ~/.gitconfig, repos).
func TestCampaignSetup_ReadsEveryShape(t *testing.T) {
	t.Parallel()
	for _, shape := range AllShapes() {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			m := Build(t, shape, loreBin)
			ssh := csHash(t, filepath.Join(m.Home, ".ssh"))
			code := csHash(t, filepath.Join(m.Home, "code"), csRuntime...)
			for _, rd := range csReads {
				for _, cwd := range []string{"", m.Repos[RepoLore]} {
					r := m.Lore(t, cwd, rd.args...)
					if h := csHash(t, filepath.Join(m.Home, "code"), csRuntime...); h != code {
						t.Errorf("%s in %q changed a repo folder", rd.op, cwd)
						code = h
					}
					if r.Code != 0 && !containsInt(rd.codes, r.Code) {
						t.Errorf("%s in %q: %s", rd.op, cwd, r)
						continue
					}
					csConforms(t, rd.op, r.Stdout)
					if strings.Contains(r.Stdout+r.Stderr, MigratedOnlyEmail) {
						t.Errorf("%s read a *.migrated backup: %s", rd.op, r)
					}
				}
			}
			if csHash(t, filepath.Join(m.Home, ".ssh")) != ssh {
				t.Errorf("a read command changed ~/.ssh")
			}
		})
	}
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// TestCampaignSetup_BadInput: each wrong input stops with its exact code,
// a next step and a non-zero exit, and leaves config.yaml as it was.
func TestCampaignSetup_BadInput(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeMigrated, loreBin)
	long := strings.Repeat("a", 10000)
	cases := []struct {
		name string
		args []string
		code string
	}{
		{"show unknown", []string{"setup", "show", "nope"}, "E_SETUP_NOT_FOUND"},
		{"show long", []string{"setup", "show", long}, "E_INVALID_INPUT"},
		{"show bad name", []string{"setup", "show", "a/b"}, "E_INVALID_INPUT"},
		{"show missing", []string{"setup", "show"}, "E_INVALID_INPUT"},
		{"edit unknown", []string{"setup", "edit", "nope", "--note", "x"}, "E_SETUP_NOT_FOUND"},
		{"edit bad route", []string{"setup", "edit", "main", "--route", "carrier-pigeon"}, "E_CONFIG_INVALID"},
		{"edit relative folder", []string{"setup", "edit", "main", "--folder", "code/**"}, "E_INVALID_INPUT"},
		{"disable unknown", []string{"setup", "disable", "nope"}, "E_SETUP_NOT_FOUND"},
		{"enable unknown", []string{"setup", "enable", "nope"}, "E_SETUP_NOT_FOUND"},
		{"remove unknown", []string{"setup", "remove", "nope"}, "E_SETUP_NOT_FOUND"},
		{"restore unknown", []string{"setup", "trash", "restore", "nope"}, "E_SETUP_NOT_FOUND"},
		{"purge unknown", []string{"setup", "trash", "purge", "nope", "--confirm"}, "E_SETUP_NOT_FOUND"},
		{"purge live setup", []string{"setup", "trash", "purge", "main", "--confirm"}, "E_SETUP_NOT_FOUND"},
		{"apply no step", []string{"setup", "apply", "main"}, "E_INVALID_INPUT"},
		{"apply unknown step", []string{"setup", "apply", "main", "--step", "warp"}, "E_INVALID_INPUT"},
		{"apply bad answers", []string{"setup", "apply", "main", "--step", "identity", "--answers", "{not json"}, "E_INVALID_INPUT"},
		{"apply bad email", []string{"setup", "apply", "main", "--step", "identity", "--answers", `{"name":"Pat","email":"not-an-email"}`}, "E_INVALID_INPUT"},
		{"apply reserved name", []string{"setup", "apply", "me", "--step", "kind", "--answers", csKindAnswers("me")}, "E_INVALID_INPUT"},
		{"apply long name", []string{"setup", "apply", long, "--step", "kind", "--answers", csKindAnswers(long)}, "E_INVALID_INPUT"},
		{"check unknown step", []string{"setup", "check", "main", "--step", "warp"}, "E_INVALID_INPUT"},
		{"fix no check", []string{"setup", "fix", "main"}, "E_INVALID_INPUT"},
		{"repair unknown", []string{"setup", "repair", "nope"}, "E_SETUP_NOT_FOUND"},
		{"use unknown", []string{"setup", "use", "nope"}, "E_SETUP_NOT_FOUND"},
		{"join garbage", []string{"setup", "join", "hello", "--no-open"}, "E_INVITE_INVALID"},
		{"browse outside home", []string{"setup", "browse", "/etc"}, "E_BAD_PATH"},
		{"identity set bad", []string{"identity", "set", "not an email"}, "E_INVALID_INPUT"},
		{"identity set empty", []string{"identity", "set", ""}, "E_INVALID_INPUT"},
		{"identity set key only", []string{"identity", "set", "--key", "human:a@example.com"}, "E_INVALID_INPUT"},
		{"edit unknown tool", []string{"setup", "edit", "main", "--tool", "nope"}, "E_CONFIG_INVALID"},
		{"edit long note", []string{"setup", "edit", "main", "--note", long}, "E_CONFIG_INVALID"},
		{"identity unset unknown org", []string{"identity", "unset", "--org", "nope"}, "E_NOT_FOUND"},
	}
	before := csConfig(t, m)
	for _, c := range cases {
		r := m.Lore(t, "", append(c.args, "--json")...)
		t.Run(c.name, func(t *testing.T) {
			e := csRefused(t, r, c.code)
			if e != nil && len(e.Message) > 2000 {
				t.Errorf("message is %d bytes: it echoes the input instead of naming the problem", len(e.Message))
			}
		})
	}
	if after := csConfig(t, m); after != before {
		t.Errorf("a refused command changed config.yaml:\nbefore %s\nafter  %s", before, after)
	}
	// Machine profiles need a repo (and an own store for a label).
	repo := m.Repos[RepoLore]
	csRefused(t, m.Lore(t, repo, "machine", "show", "mch_zz", "--json"), "E_INVALID_ID")
	csRefused(t, m.Lore(t, repo, "machine", "label", "laptop", "--json"), "E_REFUSED")
}

// TestCampaignSetup_EditRefusedWritesNothing: an edit config.yaml refuses
// writes nothing, also when it asks to become the default (the other
// setup must stay the default).
func TestCampaignSetup_EditRefusedWritesNothing(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeMigrated, loreBin)
	csNewSetup(t, m, "side", "side@example.test")
	before := csConfig(t, m)
	// --route is judged by config.yaml's rules (E_CONFIG_INVALID), --folder
	// by the folders step's question rules first (E_INVALID_INPUT).
	for _, bad := range []struct {
		args []string
		code string
	}{{[]string{"--route", "carrier-pigeon"}, "E_CONFIG_INVALID"}, {[]string{"--folder", "code/**"}, "E_INVALID_INPUT"}} {
		for _, extra := range [][]string{nil, {"--default"}} {
			args := append(append([]string{"setup", "edit", "side"}, bad.args...), extra...)
			e := csRefused(t, m.Lore(t, "", append(args, "--json")...), bad.code)
			if e != nil && !strings.Contains(e.Hint, "nothing was saved") {
				t.Errorf("refused %v: the hint does not say nothing was saved: %q", args, e.Hint)
			}
			if after := csConfig(t, m); after != before {
				t.Fatalf("refused %v changed config.yaml:\nbefore %s\nafter  %s", args, before, after)
			}
		}
	}
	// The good path still moves the default, and back.
	csOK(t, m.Lore(t, "", "setup", "edit", "side", "--default", "--json"))
	csOK(t, m.Lore(t, "", "setup", "edit", "main", "--default", "--json"))
	var shown struct {
		Setup struct {
			Default bool `json:"default"`
		} `json:"setup"`
	}
	_ = json.Unmarshal([]byte(csOK(t, m.Lore(t, "", "setup", "show", "main", "--json"))), &shown)
	if after := csConfig(t, m); strings.Count(after, "default: true") != 1 || !shown.Setup.Default {
		t.Errorf("default did not move back to main: %s", after)
	}
}

// TestCampaignSetup_OddNames: setup names a user may type are refused
// with E_INVALID_INPUT naming the rule, by every command that takes one,
// and nothing is written.
func TestCampaignSetup_OddNames(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	names := []string{"my setup", "a/b", "../x", "é名", "🙂", "שלום", "W1", "a:b", strings.Repeat("a", 65), strings.Repeat("a", 10000)}
	for _, n := range names {
		for _, args := range [][]string{
			{"setup", "apply", n, "--step", "kind", "--answers", csKindAnswers(n)},
			{"setup", "show", n}, {"setup", "remove", n}, {"setup", "disable", n},
		} {
			r := m.Lore(t, "", append(args, "--json")...)
			e := csRefused(t, r, "E_INVALID_INPUT")
			if e == nil {
				continue
			}
			if len(e.Message) > 300 {
				t.Errorf("%s %.20q: message is %d bytes, it echoes the whole name", args[1], n, len(e.Message))
			}
			if len(n) > 64 && !strings.Contains(e.Message, "64") {
				t.Errorf("%s %.20q: a %d-char name is refused without naming the 64-char limit: %s", args[1], n, len(n), e.Message)
			}
		}
	}
	if cfg := csConfig(t, m); cfg != "" {
		t.Errorf("refused names wrote config.yaml: %s", cfg)
	}
}

// TestCampaignSetup_Lifecycle: add → show → edit → disable → enable →
// remove → trash list → restore → remove → purge, each step run twice,
// list after each, and the purge's --dry-run writes nothing.
func TestCampaignSetup_Lifecycle(t *testing.T) {
	t.Parallel()
	for _, shape := range []string{ShapeFresh, ShapeMigrated, ShapeOwnerLike, ShapeGraveyard} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			m := Build(t, shape, loreBin)
			const n = "client-x"
			has := func(step string, want bool) {
				t.Helper()
				found := false
				for _, s := range m.List(t, "").Setups {
					found = found || s.Name == n
				}
				if found != want {
					t.Errorf("after %s: setup listed=%v, want %v", step, found, want)
				}
			}
			csNewSetup(t, m, n, "client@example.test")
			has("add", true)
			// The same kind step again is a no-op, not a second entry.
			r := m.Lore(t, "", "setup", "apply", n, "--step", "kind", "--answers", csKindAnswers(n), "--json")
			var ap struct {
				Changed bool `json:"changed"`
			}
			if json.Unmarshal([]byte(csOK(t, r)), &ap) != nil || ap.Changed {
				t.Errorf("re-applying the kind step changed something: %s", r)
			}
			if c := strings.Count(csConfig(t, m), "name: "+n); c != 1 {
				t.Errorf("config.yaml names %s %d times", n, c)
			}
			show := csOK(t, m.Lore(t, "", "setup", "show", n, "--json"))
			csConforms(t, "setup.show", show)
			if !strings.Contains(show, "client@example.test") {
				t.Errorf("show lacks the identity: %s", show)
			}
			for i := range 2 {
				r := m.Lore(t, "", "setup", "edit", n, "--folder", "~/clients/x/**", "--note", "a client • ünïcode", "--json")
				var ed struct {
					Changed bool `json:"changed"`
				}
				_ = json.Unmarshal([]byte(csOK(t, r)), &ed)
				if ed.Changed != (i == 0) {
					t.Errorf("edit run %d: changed=%v", i+1, ed.Changed)
				}
			}
			if s := csOK(t, m.Lore(t, "", "setup", "show", n, "--json")); !strings.Contains(s, "~/clients/x/**") || !strings.Contains(s, "ünïcode") {
				t.Errorf("edit not shown: %s", s)
			}
			for _, verb := range []string{"disable", "enable"} {
				for i := range 2 {
					r := m.Lore(t, "", "setup", verb, n, "--json")
					var o struct {
						Disabled bool `json:"disabled"`
						Changed  bool `json:"changed"`
					}
					_ = json.Unmarshal([]byte(csOK(t, r)), &o)
					if o.Changed != (i == 0) || o.Disabled != (verb == "disable") {
						t.Errorf("%s run %d: %s", verb, i+1, r.Stdout)
					}
				}
			}
			has("enable", true)
			csOK(t, m.Lore(t, "", "setup", "remove", n, "--json"))
			has("remove", false)
			csRefused(t, m.Lore(t, "", "setup", "remove", n, "--json"), "E_SETUP_NOT_FOUND")
			if tr := m.Trash(t); !containsStr(tr, n) {
				t.Errorf("trash lacks %s: %v", n, tr)
			}
			csOK(t, m.Lore(t, "", "setup", "trash", "restore", n, "--json"))
			has("restore", true)
			if s := csOK(t, m.Lore(t, "", "setup", "show", n, "--json")); !strings.Contains(s, "~/clients/x/**") {
				t.Errorf("restore lost the edit: %s", s)
			}
			csRefused(t, m.Lore(t, "", "setup", "trash", "restore", n, "--json"), "E_SETUP_NOT_FOUND")
			csOK(t, m.Lore(t, "", "setup", "remove", n, "--json"))
			home := csHash(t, m.Home)
			dry := csOK(t, m.Lore(t, "", "setup", "trash", "purge", n, "--dry-run", "--json"))
			csConforms(t, "setup.trash.purge", dry)
			csRefused(t, m.Lore(t, "", "setup", "trash", "purge", n, "--json"), "E_NEEDS_CONFIRM")
			if csHash(t, m.Home) != home {
				t.Errorf("purge --dry-run or an unconfirmed purge changed HOME")
			}
			csOK(t, m.Lore(t, "", "setup", "trash", "purge", n, "--confirm", "--json"))
			has("purge", false)
			if containsStr(m.Trash(t), n) {
				t.Errorf("purged setup still in the trash")
			}
			csRefused(t, m.Lore(t, "", "setup", "trash", "purge", n, "--confirm", "--json"), "E_SETUP_NOT_FOUND")
			m.CheckDoctor(t, "lifecycle")
		})
	}
}

func containsStr(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// TestCampaignSetup_RestoreAsTakenName: restoring into a taken name is
// refused with E_SETUP_NAME_TAKEN; --as restores under a new name.
func TestCampaignSetup_RestoreAsTakenName(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	csNewSetup(t, m, "dup", "one@example.test")
	csOK(t, m.Lore(t, "", "setup", "remove", "dup", "--json"))
	csNewSetup(t, m, "dup", "two@example.test")
	csRefused(t, m.Lore(t, "", "setup", "trash", "restore", "dup", "--json"), "E_SETUP_NAME_TAKEN")
	csRefused(t, m.Lore(t, "", "setup", "trash", "restore", "dup", "--as", "Bad Name", "--json"), "E_INVALID_INPUT")
	csOK(t, m.Lore(t, "", "setup", "trash", "restore", "dup", "--as", "dup-old", "--json"))
	if s := csOK(t, m.Lore(t, "", "setup", "show", "dup-old", "--json")); !strings.Contains(s, "one@example.test") {
		t.Errorf("restore --as lost the old identity: %s", s)
	}
	if s := csOK(t, m.Lore(t, "", "setup", "show", "dup", "--json")); !strings.Contains(s, "two@example.test") {
		t.Errorf("restore --as touched the live setup: %s", s)
	}
}

// TestCampaignSetup_Identity: set → show → unset round trip with odd
// display values, the top level and one org's, run twice.
func TestCampaignSetup_Identity(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeOrgMember, loreBin)
	for i := range 2 {
		csOK(t, m.Lore(t, "", "identity", "set", "alice@example.com", "--json"))
		if r := m.Lore(t, "", "identity", "show", "--json"); !strings.Contains(csOK(t, r), "human:alice@example.com") {
			t.Errorf("run %d: show after set: %s", i+1, r)
		}
	}
	for _, name := range []string{"Zoë Ünïcode 🙂", "שלום עולם", strings.Repeat("n", 300)} {
		r := m.Lore(t, "", "identity", "set", "--org", "acme", "--key", "human:"+WorkEmail, "--name", name, "--handle", "zoe", "--json")
		csOK(t, r)
		if cfg := csConfig(t, m); !strings.Contains(cfg, "zoe") {
			t.Errorf("org identity not written for name %.20q: %s", name, cfg)
		}
	}
	for i := range 2 {
		r := m.Lore(t, "", "identity", "unset", "--json")
		csOK(t, r)
		if strings.Contains(csConfig(t, m), "human:alice@example.com") {
			t.Errorf("unset run %d left the identity", i+1)
		}
	}
	csOK(t, m.Lore(t, "", "identity", "unset", "--org", "acme", "--json"))
	if strings.Contains(csConfig(t, m), "handle: zoe") {
		t.Errorf("unset --org left the org identity: %s", csConfig(t, m))
	}
	// v0.1.17 compatibility: a missing org entry is E_CONFIG_INVALID on set.
	csRefused(t, m.Lore(t, "", "identity", "set", "--org", "nope", "--key", "human:a@example.com", "--json"), "E_CONFIG_INVALID")
}

// TestCampaignSetup_UsePin: `setup use --here` pins a lore repo, twice is
// a no-op, --clear removes it; a folder lore is not set up in is refused.
func TestCampaignSetup_UsePin(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeMigrated, loreBin)
	repo := m.Repos[RepoWorkApp]
	for range 2 {
		csOK(t, m.Lore(t, repo, "setup", "use", "main", "--here", "--json"))
		if l := m.List(t, repo); l.Active.Name != "main" {
			t.Errorf("pinned repo: active %+v", l.Active)
		}
	}
	csOK(t, m.Lore(t, repo, "setup", "use", "--here", "--clear", "--json"))
	csOK(t, m.Lore(t, repo, "setup", "use", "--here", "--clear", "--json"))
	// For a person: the shell scope is only the line to paste or eval,
	// and a pin or a cleared pin says what happened.
	if out := csOK(t, m.Lore(t, repo, "setup", "use", "main")); out != "export LORE_SETUP=main\n" {
		t.Errorf("setup use main printed %q, want only the export line", out)
	}
	if out := csOK(t, m.Lore(t, repo, "setup", "use", "main", "--here")); !strings.Contains(out, "pinned this repo to setup main") {
		t.Errorf("setup use --here printed %q", out)
	}
	if out := csOK(t, m.Lore(t, repo, "setup", "use", "--here", "--clear")); strings.Contains(out, "name:") || !strings.Contains(out, "cleared") {
		t.Errorf("setup use --here --clear printed %q", out)
	}
	r := m.Lore(t, m.Repos[RepoNonGit], "setup", "use", "main", "--here", "--json")
	if r.Code == 0 {
		t.Errorf("pinning a folder that is no repo succeeded: %s", r)
	} else if e := csErrOf(r); e == nil || (e.Hint == "" && e.Next == nil) {
		t.Errorf("pin refusal names no next step: %s", r)
	}
}

// TestCampaignSetup_SkillInstall: --dry-run writes nothing, the install
// is idempotent and status then reports it installed.
func TestCampaignSetup_SkillInstall(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeFresh, loreBin)
	before := csHash(t, m.Home)
	csOK(t, m.Lore(t, "", "skill", "install", "--tool", "claude_code", "--dry-run", "--json"))
	if csHash(t, m.Home) != before {
		t.Fatalf("skill install --dry-run wrote files")
	}
	csOK(t, m.Lore(t, "", "skill", "install", "--tool", "claude_code", "--json"))
	once := csHash(t, filepath.Join(m.Home, ".claude"))
	csOK(t, m.Lore(t, "", "skill", "install", "--tool", "claude_code", "--json"))
	if csHash(t, filepath.Join(m.Home, ".claude")) != once {
		t.Errorf("a second install changed the skill folder")
	}
	st := csOK(t, m.Lore(t, "", "skill", "status", "--json"))
	var s struct {
		Tools []struct {
			Tool      string `json:"tool"`
			Installed bool   `json:"installed"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(st), &s); err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, x := range s.Tools {
		ok = ok || (x.Tool == "claude_code" && x.Installed)
	}
	if !ok {
		t.Errorf("status after install: %s", st)
	}
	csRefused(t, m.Lore(t, "", "skill", "install", "--tool", "no-such-tool", "--json"), "E_INVALID_INPUT")
}

// TestCampaignSetup_SupportBundle: the bundle is written to --out, is JSON
// and carries no identity email or ssh key path of the machine.
func TestCampaignSetup_SupportBundle(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeOwnerLike, loreBin)
	out := filepath.Join(m.Dir, "tmp", "bundle.json")
	csOK(t, m.Lore(t, m.Repos[RepoLore], "support-bundle", "run", "--out", out, "--json"))
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(b) {
		t.Fatalf("bundle is not JSON: %.200s", b)
	}
	// To stdout, the same document.
	so := csOK(t, m.Lore(t, m.Repos[RepoLore], "support-bundle", "run"))
	if !json.Valid([]byte(so)) {
		t.Errorf("bundle on stdout is not JSON: %.200s", so)
	}
	for _, secret := range []string{"PRIVATE KEY", MigratedOnlyEmail} {
		if strings.Contains(string(b), secret) {
			t.Errorf("bundle carries %q", secret)
		}
	}
}

// TestCampaignSetup_ConcurrentApply: two setups saved at the same moment
// by two processes both land in config.yaml, which stays readable.
func TestCampaignSetup_ConcurrentApply(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeMigrated, loreBin)
	names := []string{"par-a", "par-b", "par-c", "par-d"}
	var wg sync.WaitGroup
	res := make([]Result, len(names))
	for i, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[i] = m.Lore(t, "", "setup", "apply", n, "--step", "kind", "--answers", csKindAnswers(n), "--json")
		}()
	}
	wg.Wait()
	for _, r := range res {
		if r.Code != 0 {
			if e := csErrOf(r); e == nil || !e.retryable() {
				t.Errorf("concurrent apply failed without a retryable refusal: %s", r)
			}
		}
	}
	got := map[string]bool{}
	for _, s := range m.List(t, "").Setups {
		got[s.Name] = true
	}
	for i, n := range names {
		if res[i].Code == 0 && !got[n] {
			t.Errorf("apply of %s said ok but the setup is gone (lost update): %v", n, got)
		}
	}
	if !got["main"] {
		t.Errorf("concurrent applies lost the existing setup")
	}
}

// retryable is the envelope's retryable flag.
func (e *csErr) retryable() bool { return e != nil && e.Retryable }

// TestCampaignSetup_RESTParity: the same read through `lore api` answers
// what the CLI's --json prints.
func TestCampaignSetup_RESTParity(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeGraveyard, loreBin)
	repo := m.Repos[RepoLore]
	api := csStartAPI(t, m, repo)
	cases := []struct {
		op   string
		cli  []string
		path string
		body string
	}{
		{"setup.list", []string{"setup", "list"}, "", ""},
		{"setup.show", []string{"setup", "show", GraveLive}, "/v1/setup/" + GraveLive, ""},
		{"setup.trash.list", []string{"setup", "trash", "list"}, "", ""},
		{"setup.plan", []string{"setup", "plan", "--kind", "solo"}, "", `{"kind":"solo"}`},
		{"whoami.show", []string{"whoami", "show"}, "", ""},
	}
	for _, c := range cases {
		rt := csRouteOf(t, c.op)
		path := c.path
		if path == "" {
			path = rt.Path
		}
		body := c.body
		if body == "" && rt.Method != "GET" {
			body = "{}"
		}
		status, rest := api.call(t, rt.Method, path, body)
		cli := csOK(t, m.Lore(t, repo, append(c.cli, "--json")...))
		if status != 200 {
			t.Errorf("%s %s %s → %d %s", c.op, rt.Method, path, status, rest)
			continue
		}
		drop := []string{"checked_at", "took_ms", "dir", "root", "here"}
		if a, b := csCanon(t, cli, drop...), csCanon(t, rest, drop...); a != b {
			t.Errorf("%s: CLI and REST differ\ncli  %s\nrest %s", c.op, a, b)
		}
	}
	// A missing setup is the same refusal on both surfaces.
	status, rest := api.call(t, "GET", "/v1/setup/nope", "")
	cli := m.Lore(t, "", "setup", "show", "nope", "--json")
	if e := csErrOf(cli); status != 404 || e == nil || !strings.Contains(rest, e.Code) {
		t.Errorf("missing setup: REST %d %s, CLI %s", status, rest, cli)
	}
}
