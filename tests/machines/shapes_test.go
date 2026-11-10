//go:build machines && !windows

package machines

// shapes_test.go — every shape as it is, before any setup change: the
// machine invariants hold, lore init in every kind of repo folder keeps
// what was there and writes readable files, and the owner-like ssh/gh
// layout is read without refusing a key that has no .pub.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goneAtStart is what each shape starts with gone.
func goneAtStart(shape string) Gone {
	if shape == ShapeGraveyard {
		return Gone{Purged: []string{GravePurged}, Trashed: []string{GraveTrashed}}
	}
	return Gone{}
}

func TestShapes(t *testing.T) {
	for _, shape := range AllShapes() {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			m := Build(t, shape, loreBin)
			m.CheckMachine(t, shape+"/as built", goneAtStart(shape))
			if shape == ShapeGraveyard {
				l := m.List(t, "")
				for _, s := range l.Setups {
					if s.Name == GraveDisabled && !s.Disabled {
						t.Errorf("%s is not listed disabled", GraveDisabled)
					}
				}
			}
		})
	}
}

// alreadyInit: lore init in a lore repo exits E_ALREADY_INITIALIZED (23)
// with its hint, never another failure.
func alreadyInit(r Result) bool {
	return r.Code == 0 || (r.Code == exitAlreadyInit && strings.Contains(r.Stderr, "E_ALREADY_INITIALIZED"))
}

// exitAlreadyInit is E_ALREADY_INITIALIZED's exit code.
const exitAlreadyInit = 23

// TestInitInEveryFolder runs lore init in each repo folder kind on every
// shape: it never fails on a git repo, keeps hand-written CLAUDE.md and
// AGENTS.md text, refuses a non-git folder with a reason (never a crash),
// and every file it writes is readable by the clones (CheckModes).
func TestInitInEveryFolder(t *testing.T) {
	for _, shape := range AllShapes() {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			m := Build(t, shape, loreBin)
			for _, repo := range []string{RepoClaude, RepoAgents, RepoPlain} {
				if r := m.Lore(t, m.Repos[repo], "init"); r.Code != 0 {
					t.Errorf("lore init in %s: %s", repo, r)
				}
			}
			// A repo lore already knows: init says so and changes nothing.
			for _, repo := range []string{RepoLore, RepoPlain} {
				if r := m.Lore(t, m.Repos[repo], "init"); !alreadyInit(r) {
					t.Errorf("lore init again in %s: %s", repo, r)
				}
			}
			r := m.Lore(t, m.Repos[RepoNonGit], "init")
			if strings.Contains(r.Stderr, "panic") || (r.Code != 0 && !strings.Contains(r.Stdout+r.Stderr, "E_")) {
				t.Errorf("lore init in a non-git folder: %s", r)
			}
			for repo, body := range map[string]string{RepoClaude: ClaudeBody, RepoAgents: AgentsBody} {
				f := "CLAUDE.md"
				if repo == RepoAgents {
					f = "AGENTS.md"
				}
				got, err := os.ReadFile(filepath.Join(m.Repos[repo], f))
				if err != nil || !strings.Contains(string(got), strings.TrimSpace(body)) {
					t.Errorf("%s/%s lost its hand-written text (%v):\n%s", repo, f, err, got)
				}
			}
			m.CheckModes(t, shape+"/after init")
			m.CheckDoctor(t, shape+"/after init")
		})
	}
}

type aliasesOut struct {
	Aliases []struct {
		Alias string `json:"alias"`
		User  string `json:"user"`
	} `json:"aliases"`
}

// TestOwnerLikeAliases: every alias whose key works is read with the login
// it logs in as, including the key without a .pub; the alias whose key is
// gone and the non-GitHub host never fail the command.
func TestOwnerLikeAliases(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeOwnerLike, loreBin)
	r := m.Lore(t, "", "setup", "aliases", "--json", "--keys")
	if r.Code != 0 {
		t.Fatalf("setup aliases: %s", r)
	}
	for _, login := range []string{LoginAlpha, LoginBravo, LoginCharlie} {
		if !strings.Contains(r.Stdout, `"`+login+`"`) {
			t.Errorf("setup aliases does not name %s (its alias logs in as it): %s", login, r.Stdout)
		}
	}
	// The key without a .pub can be chosen for a setup.
	if r := m.Lore(t, "", "setup", "edit", "main", "--host-alias", "github-charlie", "--ssh-key", "~/.ssh/charlie_nopub", "--github-user", LoginCharlie); r.Code != 0 {
		t.Errorf("a key without .pub was refused: %s", r)
	}
	m.CheckMachine(t, "owner_like/charlie key", Gone{})
}

// TestPinNeedsInit: `lore setup use --here` in a git repo lore was never
// set up in names the missing step (lore init), not "run it inside a git
// repo"; after lore init the pin works.
func TestPinNeedsInit(t *testing.T) {
	t.Parallel()
	m := Build(t, ShapeOwnerLike, loreBin)
	r := m.Lore(t, m.Repos[RepoPlain], "setup", "use", "main", "--here")
	if r.Code == 0 || !strings.Contains(r.Stderr, "lore init") || strings.Contains(r.Stderr, "inside a git repo") {
		t.Errorf("pin in a git repo without lore: %s", r)
	}
	if r := m.Lore(t, m.Repos[RepoNonGit], "setup", "use", "main", "--here"); r.Code == 0 || !strings.Contains(r.Stderr, "git repo") {
		t.Errorf("pin outside a git repo: %s", r)
	}
	if r := m.Lore(t, m.Repos[RepoPlain], "init"); r.Code != 0 {
		t.Fatalf("init: %s", r)
	}
	if r := m.Lore(t, m.Repos[RepoPlain], "setup", "use", "main", "--here"); r.Code != 0 {
		t.Errorf("pin after lore init: %s", r)
	}
}

// TestPurgeMigratedSetup replays the owner's report (2026-11-07) on the
// shapes that hold *.migrated backups: the setup the migration made is
// trashed, then purged (with and without --clear-identity); no source may
// offer it again, by name or by its identity.
func TestPurgeMigratedSetup(t *testing.T) {
	for _, shape := range []string{ShapeMigrated, ShapeOwnerLike} {
		for _, extra := range [][]string{nil, {"--clear-identity"}, {"--clear-identity", "--clear-personal-store"}} {
			t.Run(shape+"/"+strings.Join(append([]string{"purge"}, extra...), " "), func(t *testing.T) {
				t.Parallel()
				m := Build(t, shape, loreBin)
				if r := m.Lore(t, "", "setup", "remove", "main"); r.Code != 0 {
					t.Fatalf("%s", r)
				}
				m.CheckMachine(t, shape+"/trash main", Gone{Trashed: []string{"main"}})
				if r := m.Lore(t, "", append([]string{"setup", "trash", "purge", "main", "--confirm"}, extra...)...); r.Code != 0 {
					t.Fatalf("%s", r)
				}
				m.CheckMachine(t, shape+"/purge main", Gone{Purged: []string{"main"}, Identities: []string{"human:" + PersonEmail}})
			})
		}
	}
}
