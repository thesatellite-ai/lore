package wire

// handedit_migrated_test.go — B1-10/B1-16: after lore fix rewrote every
// v0.1.17 row file as `_v: 2` (not committed yet), each later command took
// the migration's own field changes (an empty ref written as null) for
// hand edits and re-stamped those rows with the person's name, and looked
// every modified file up in the engine: a minute per command in a repo of
// 5000 rows.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
)

// TestPickUpSkipsTheMigrationsRewrite: a committed v1 row the migration
// rewrote (uncommitted) is not a hand edit; an edit made by hand on top of
// that rewrite still is, naming only the field the person changed.
func TestPickUpSkipsTheMigrationsRewrite(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := newTestApp(t, repo)
	withProject(t, a)
	const id = "tsk_01a1a59b891c70b789144cd3e711fd51"
	v1Row(t, repo, schema.TableTasks, id, `"project_id":"`+projectID(t, a)+`","title":"ship it","status":"todo","priority":"high","commitment":"accepted","tasklist_id":""`)
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "v0.1.17 rows")
	if got, err := a.RewriteV1(background(), schema.TableTasks, false); err != nil || len(got) != 1 {
		t.Fatalf("rewrite: %v %v", got, err)
	}
	path := filepath.Join(repo, ".lore", "data", schema.TableTasks, id+".json")
	migrated := string(mustRead(t, path))
	if !strings.Contains(migrated, `"_v": 2`) {
		t.Fatalf("not migrated: %s", migrated)
	}

	notices := func() (*App, func() []string) {
		var mu sync.Mutex
		var lines []string
		b := newTestAppWith(t, repo, func(o *Options) {
			o.Notice = func(m string) { mu.Lock(); lines = append(lines, m); mu.Unlock() }
		})
		return b, func() []string { mu.Lock(); defer mu.Unlock(); return lines }
	}
	b, said := notices()
	if _, _, err := b.Get(background(), engineRef(schema.TableTasks, id)); err != nil {
		t.Fatal(err)
	}
	for _, l := range said() {
		if strings.Contains(l, "hand edit") {
			t.Fatalf("the migration's rewrite was taken for a hand edit: %q", l)
		}
	}
	if got := string(mustRead(t, path)); got != migrated {
		t.Fatalf("a read re-stamped the migrated row:\n%s\nwas\n%s", got, migrated)
	}

	// a person edits the migrated file by hand (the stamp left as it is)
	if err := os.WriteFile(path, []byte(strings.Replace(migrated, `"ship it"`, `"ship it today"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	c, said2 := notices()
	if _, _, err := c.Get(background(), engineRef(schema.TableTasks, id)); err != nil {
		t.Fatal(err)
	}
	picked := ""
	for _, l := range said2() {
		if strings.Contains(l, "picked up a hand edit") {
			picked = l
		}
	}
	if !strings.Contains(picked, "(title)") {
		t.Fatalf("a hand edit of a migrated row must be picked up naming only title: %q", said2())
	}
}
