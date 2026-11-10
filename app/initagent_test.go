package app

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/logic/agentdetect"
	hosthooks "github.com/thesatellite-ai/lore/wire/host"
)

// clearAgentEnv blanks every variable a built-in detector reads, so the
// test decides which tool (if any) the command sees.
func clearAgentEnv(t *testing.T) {
	t.Helper()
	t.Setenv(agentdetect.EnvAIAgent, "")
	t.Setenv(identity.EnvActor, "")
	t.Setenv(identity.EnvHuman, "")
	for _, d := range agentdetect.Builtin() {
		for _, s := range append(append([]agentdetect.Signal{}, d.Strong...), d.Weak...) {
			t.Setenv(s.Env, "")
		}
		for _, e := range append(append([]string{}, d.SessionEnv...), d.ModelEnv...) {
			t.Setenv(e, "")
		}
	}
}

// TestInitAsAgentStampsEveryRow (G-ORGMIG-8, b-flow3): `lore init` run by
// a detected agent in a repo with no org writes every row (project, repo,
// actor, the context.targets policy) under ONE writer, the agent's own
// actor (kind agent); before the fix the repos and policies rows had no
// writer at all. A person (LORE_HUMAN=1) gets the same: one writer, a
// person.
func TestInitAsAgentStampsEveryRow(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	for _, c := range []struct {
		name string
		env  map[string]string
		kind string
	}{
		{"detected agent", map[string]string{"OPENCODE": "1"}, "agent"},
		{"person", map[string]string{identity.EnvHuman: "1"}, "human"},
	} {
		t.Run(c.name, func(t *testing.T) {
			clearAgentEnv(t)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
			t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
			if err := os.WriteFile(filepath.Join(home, "gitconfig"), []byte("[user]\n\tname = Tom\n\temail = tom@example.com\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(hosthooks.EnvRepo, "")
			t.Setenv(hosthooks.EnvProjectID, "")
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cmd := &exec.Cmd{Path: git, Args: []string{git, "-C", dir, "init", "-q", "-b", "main"}}
			if out, cerr := cmd.CombinedOutput(); cerr != nil {
				t.Fatalf("git init: %v %s", cerr, out)
			}
			t.Chdir(dir)
			if code, out, errOut := runCaptured(t, "init"); code != 0 {
				t.Fatalf("lore init: exit %d\n%s\n%s", code, out, errOut)
			}
			writers := map[string][]string{}
			tables := map[string]bool{}
			var actorKind string
			// Walked and read through a root at .lore/data, so reads stay inside it.
			rt, err := os.OpenRoot(filepath.Join(dir, ".lore", "data"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rt.Close() }() // read-only; each read reports its own error
			fsys := rt.FS()
			err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, werr error) error {
				if werr != nil || d.IsDir() || path.Base(p) == "_meta.json" {
					return werr
				}
				b, rerr := fs.ReadFile(fsys, p)
				if rerr != nil {
					return rerr
				}
				var r struct {
					HLC       string `json:"_hlc"`
					Table     string `json:"_table"`
					CreatedBy string `json:"created_by"`
					Kind      string `json:"kind"`
				}
				if jerr := json.Unmarshal(b, &r); jerr != nil {
					return jerr
				}
				who := r.HLC[strings.LastIndex(r.HLC, "/")+1:]
				writers[who] = append(writers[who], r.Table)
				tables[r.Table] = true
				if r.Table == "actors" {
					actorKind = r.Kind
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"projects", "repos", "actors", "policies"} {
				if !tables[want] {
					t.Fatalf("init wrote no %s row: %v", want, writers)
				}
			}
			if len(writers) != 1 || writers[""] != nil {
				t.Fatalf("rows of one init have writers %v, want one non-empty actor", writers)
			}
			if actorKind != c.kind {
				t.Fatalf("actor kind %q, want %q", actorKind, c.kind)
			}
		})
	}
}
