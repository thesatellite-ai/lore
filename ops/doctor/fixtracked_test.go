package doctor

import (
	"context"
	"maps"
	"strings"
	"testing"
)

// trackedEnv is a repo whose fix steps change tracked files: the state
// TrackedChanges reports moves from before to after when FixSteps runs.
type trackedEnv struct {
	fakeEnv
	before, after map[string]string
	steps         []FixStep
	ran           bool
	reads         int
}

func (e *trackedEnv) FixSteps(context.Context, bool) []FixStep {
	e.ran = true
	return e.steps
}

func (e *trackedEnv) TrackedChanges(context.Context) (map[string]string, bool) {
	e.reads++
	if e.ran {
		return maps.Clone(e.after), true
	}
	return maps.Clone(e.before), true
}

// TestFixNamesEveryTrackedFileItChanged (devco:D-744): lore doctor --fix in
// a linked repo moved lore's hooks out of the committed .claude/settings.json
// and rewrote the tracked workflow, and its output named only the
// .gitattributes the git wiring step wrote. Every tracked file the run
// changed is named once, in one "commit these:" line with the command that
// commits them; a file the person had changed before and the run left alone
// is not, and neither is one a step's own commit line names.
func TestFixNamesEveryTrackedFileItChanged(t *testing.T) {
	t.Parallel()
	env := &trackedEnv{
		fakeEnv: fakeEnv{host: &fakeHost{base: healthy()}},
		before: map[string]string{
			"notes/todo.md": " M:aaa", // the person's own edit, untouched by the run
			"README.md":     " M:bbb", // the person's edit the run changed again
		},
		after: map[string]string{
			"notes/todo.md":                         " M:aaa",
			"README.md":                             " M:ccc",
			".claude/settings.json":                 " M:ddd",
			".github/workflows/lore-sync-merge.yml": " M:eee",
			".gitattributes":                        " M:fff", // named by the git wiring step
			".lore/data/memories/mem_1.json":        " M:ggg", // named by the migrate step's folder
			"docs/my notes.md":                      " D:deleted",
		},
		steps: []FixStep{
			{Step: StepMigrate, Result: "migrated", Changed: true, Next: `git add .lore/data && git commit -m "lore: migrate rows to format 2"`},
			{Step: StepGitWiring, Result: "wrote", Changed: true, Next: `lore's lines in .gitattributes wait for your commit: git add .gitattributes && git commit -m "lore: git wiring"`},
		},
	}
	r, err := FixProject(context.Background(), env, false)
	if err != nil {
		t.Fatal(err)
	}
	var st *FixStep
	for i := range r.Steps {
		if r.Steps[i].Step == StepTrackedFiles {
			st = &r.Steps[i]
		}
	}
	if st == nil {
		t.Fatalf("no %q step; steps %+v", StepTrackedFiles, r.Steps)
	}
	want := `commit these: .claude/settings.json .github/workflows/lore-sync-merge.yml README.md docs/my notes.md: git add -- .claude/settings.json .github/workflows/lore-sync-merge.yml README.md 'docs/my notes.md' && git commit -m "lore: fix"`
	if !st.Changed || !strings.Contains(st.Next, want) {
		t.Fatalf("next %q\nwant it to hold %q", st.Next, want)
	}
	for _, not := range []string{"notes/todo.md", ".gitattributes", ".lore/data"} {
		if strings.Contains(st.Next, not) {
			t.Fatalf("next %q names %s", st.Next, not)
		}
	}
	if !strings.Contains(r.Text(), want) {
		t.Fatalf("the text leaves out the commit line:\n%s", r.Text())
	}

	// a run that changed no tracked file adds no step
	quiet := &trackedEnv{fakeEnv: fakeEnv{host: &fakeHost{base: healthy()}}, before: env.before, after: env.before}
	q, err := FixProject(context.Background(), quiet, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range q.Steps {
		if s.Step == StepTrackedFiles {
			t.Fatalf("nothing changed, yet %+v", s)
		}
	}
	// a dry run changes nothing and reads nothing
	dry := &trackedEnv{fakeEnv: fakeEnv{host: &fakeHost{base: healthy()}}, before: env.before, after: env.after}
	if _, err := FixProject(context.Background(), dry, true); err != nil || dry.reads != 0 {
		t.Fatalf("dry run read the tracked files %d times (err %v)", dry.reads, err)
	}
}

// TestNextNamesFolderOnlyForPaths: a word of a commit line covers a folder
// only when it looks like a path, so "git add" never covers a folder named
// git.
func TestNextNamesFolderOnlyForPaths(t *testing.T) {
	t.Parallel()
	lines := []string{`git add .lore/data && git commit -m "lore: x"`}
	for p, want := range map[string]bool{
		".lore/data/a.json": true,
		".lore/LORE.md":     false,
		"git/config.txt":    false,
		"lore/x.go":         false,
	} {
		if got := nextNames(lines, p); got != want {
			t.Errorf("nextNames(%q) = %v, want %v", p, got, want)
		}
	}
}
