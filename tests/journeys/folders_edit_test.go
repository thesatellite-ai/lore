//go:build journeys && !windows

package journeys

// folders_edit_test.go — Lee edits rows by hand (D2, D3 of the plain-files
// round): `lore edit <id>` opens a row in the editor as Markdown and writes
// it back through the normal write path; `lore path --id <id>` prints the row's
// file. First in a code repo (a branch row, then a live row), then in the
// team store's folder; a refused edit keeps the text; editing the JSON file
// directly keeps working. EDITOR is a script that edits the file, as a
// person's editor would.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// The edit journey's names (placeholders).
const (
	jEditSlug     = "github.com/acme-co/edit-web"
	jEditFolder   = "client_edit"
	jEditTitle    = "Edited: login redirect # keeps the hash"
	jEditBodyLine = "Written in the editor: üñï ✓"
	jEditJSONText = "Changed in the JSON file by hand"
)

// jEditScript replaces the title line and appends a body line: what a
// person does in their editor. The values are quoted YAML, as the view's
// help asks for text with ": " or " #".
const jEditScript = `#!/bin/sh
f="$1"
sed -i 's/^title: .*/title: "` + jEditTitle + `"/' "$f"
printf '%s\n' '` + jEditBodyLine + `' >> "$f"
`

// jEditBadScript makes two mistakes: a status that does not exist and a
// key tasks does not have.
const jEditBadScript = `#!/bin/sh
sed -i 's/^status: .*/status: someday-maybe/; s/^priority: \(.*\)/priority: \1\nprioritee: high/' "$1"
`

// jPathOut is `lore path --json`.
type jPathOut struct {
	ID     string `json:"id"`
	Table  string `json:"table"`
	Path   string `json:"path"`
	Store  string `json:"store"`
	Folder string `json:"folder"`
}

func TestJourneyFoldersEditRows(t *testing.T) {
	t.Parallel()
	tt := newTLTeam(t)
	w := tt.w
	lee := tt.person(tlLee)
	parent := filepath.Join(lee.Home, "code")
	app := tt.repo[tlLee.Email]
	bin := filepath.Join(w.Root, "editors")
	tmp := filepath.Join(w.Root, "tmp-lee")
	for _, d := range []string{bin, tmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	good, bad := filepath.Join(bin, "edit.sh"), filepath.Join(bin, "bad.sh")
	for p, body := range map[string]string{good: jEditScript, bad: jEditBadScript} {
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lee.SetEnv("TMPDIR", tmp) // a kept view lands here, not in the host's /tmp
	editor := func(script string) { lee.SetEnv("EDITOR", script) }
	var web, branchTask, liveTask, storeTask string

	w.Step("day1/team-folder-and-repo", func() {
		tlStartTeam(t, lee, app.Dir, tlLeadSetup, tlOrg, tt.store.SSHURL(), tlLee, []string{tlFolders})
		lee.Lore(parent, "store", "folder", "add", "team", jEditFolder).OK(t)
		c := lee.Clone(w.Remote(jEditSlug), "edit-web")
		lee.MoveClone(t, c, filepath.Join(parent, "edit-web"))
		lee.LoreIn(c, "init", "--non-interactive", "--name=edit-web").OK(t)
		lee.LoreIn(c, "org", "link", tlOrg, "--project", jEditFolder, "--yes").OK(t)
		web = c.Dir
		branchTask = jID(t, lee.Lore(web, "task", "add", "Fix the login redirect", "--to", "repo", "--no-live", "--body", "first line", "--json").OK(t))
		liveTask = jID(t, lee.Lore(web, "task", "add", "A live task", "--to", "repo", "--json").OK(t))
		storeTask = jID(t, lee.Lore(web, "task", "add", "A task in the team folder", "--to", tlOrg, "--json").OK(t))
	})

	w.Step("day2/edit-a-branch-row-in-the-repo", func() {
		w.AdvanceDays(1)
		editor(good)
		lee.Lore(web, "edit", branchTask).OK(t).Says(t, branchTask, "updated in store repo", "body, title")
		p := jPath(t, lee.Lore(web, "path", branchTask, "--json").OK(t))
		if p.ID != branchTask || p.Table != "tasks" || p.Store != "repo" || !strings.HasPrefix(p.Path, web+string(filepath.Separator)) {
			t.Fatalf("lore path --json = %+v, want the task's file inside %s", p, web)
		}
		jFileHas(t, p.Path, jEditTitle, "first line\\n"+jEditBodyLine)
		// the plain form is the path alone; open is the same command
		if got := strings.TrimSpace(lee.Lore(web, "path", branchTask).OK(t).Stdout); got != p.Path {
			t.Errorf("lore path --id printed %q, want %q", got, p.Path)
		}
		if got := strings.TrimSpace(lee.Lore(web, "open", branchTask).OK(t).Stdout); got != p.Path {
			t.Errorf("lore open printed %q, want %q", got, p.Path)
		}
		// closing the editor without a change writes nothing
		before := jRead(t, p.Path)
		editor("true")
		lee.Lore(web, "edit", branchTask).OK(t).Says(t, "no change", "nothing written")
		if after := jRead(t, p.Path); after != before {
			t.Errorf("a no-change edit rewrote the file:\nbefore %s\nafter  %s", before, after)
		}
		// a JSON file edited by hand still reads as before
		if err := os.WriteFile(p.Path, []byte(strings.Replace(before, jEditTitle, jEditJSONText, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		lee.Lore(web, "show", branchTask).OK(t).Says(t, jEditJSONText)
	})

	w.Step("day3/a-live-row-has-no-file-yet", func() {
		w.AdvanceDays(1)
		lee.Lore(web, "path", liveTask).Refused(t).Says(t, "E_NOT_FOUND", "live row", "refs/lore/live", "lore edit "+liveTask)
		editor(good)
		lee.Lore(web, "edit", liveTask).OK(t).Says(t, "updated in store repo")
		lee.Lore(web, "show", liveTask).OK(t).Says(t, jEditTitle, jEditBodyLine)
	})

	w.Step("day4/edit-a-row-in-the-store-folder", func() {
		w.AdvanceDays(1)
		mainRef := "refs/heads/main"
		commits := tt.store.CommitCount(mainRef)
		editor("sh " + good) // an EDITOR with arguments, as "code --wait"
		lee.Lore(web, "edit", storeTask).OK(t).Says(t, "updated in store "+tlOrg, "body, title")
		lee.Lore(web, "sync").OK(t) // C5: the edit reaches the store with Lee's sync
		p := jPath(t, lee.Lore(web, "path", storeTask, "--json").OK(t))
		// the store clone's plain layout: <clone>/<folder>/<table>/<id>.json
		// under ~/.lore/stores, no .lore/data inside the store
		want := string(filepath.Separator) + filepath.Join(jEditFolder, "tasks", storeTask+".json")
		if p.Store != tlOrg || p.Folder != jEditFolder || !strings.HasPrefix(p.Path, filepath.Join(lee.Home, ".lore", "stores")+string(filepath.Separator)) ||
			!strings.HasSuffix(p.Path, want) || strings.Contains(p.Path, filepath.Join(".lore", "data")) {
			t.Fatalf("lore path --json = %+v, want the file in Lee's copy of the %s store at …%s", p, tlOrg, want)
		}
		if files := tt.store.FilesMatching("refs/heads/main", storeTask+".json"); len(files) != 1 || files[0] != jEditFolder+"/tasks/"+storeTask+".json" {
			t.Fatalf("the store's main branch holds %v, want %s/tasks/%s.json", files, jEditFolder, storeTask)
		}
		jFileHas(t, p.Path, jEditTitle, jEditBodyLine)
		// the edit is a store write like any other: committed and pushed
		if n := tt.store.CommitCount(mainRef); n <= commits {
			t.Errorf("the edit made no store commit (%d → %d)", commits, n)
		}
		files := tt.store.FilesMatching(mainRef, storeTask+".json")
		if len(files) != 1 {
			t.Fatalf("the store's main branch holds %d files for %s: %v", len(files), storeTask, files)
		}
		if doc, ok := tt.store.Read(mainRef, files[0]); !ok || !strings.Contains(doc, jEditTitle) {
			t.Errorf("the pushed row file lacks the edit: %s", doc)
		}
	})

	w.Step("day5/a-refused-edit-keeps-the-text", func() {
		w.AdvanceDays(1)
		editor(bad)
		r := lee.Lore(web, "edit", storeTask).Refused(t).Says(t, "E_INVALID_INPUT", "2 problems", "unknown key prioritee", "status", "kept in")
		kept := regexp.MustCompile(`kept in (\S+)`).FindStringSubmatch(r.Out())
		if kept == nil || !strings.HasPrefix(kept[1], tmp) {
			t.Fatalf("no kept file under %s named: %s", tmp, r)
		}
		if !regexp.MustCompile(regexp.QuoteMeta(kept[1]) + `:\d+: unknown key prioritee`).MatchString(r.Out()) {
			t.Errorf("the refusal names no file:line for the unknown key: %s", r)
		}
		if body := jRead(t, kept[1]); !strings.Contains(body, "prioritee: high") || !strings.Contains(body, "status: someday-maybe") {
			t.Errorf("the kept file lost the edited text:\n%s", body)
		}
		lee.Lore(web, "show", storeTask).OK(t).Never(t, "someday-maybe")
		// fixed by hand, then written back without the editor
		fixed := strings.NewReplacer("status: someday-maybe", "status: in_progress", "prioritee: high\n", "").Replace(jRead(t, kept[1]))
		if err := os.WriteFile(kept[1], []byte(fixed), 0o600); err != nil {
			t.Fatal(err)
		}
		lee.Lore(web, "edit", storeTask, "--file", kept[1]).OK(t).Says(t, "updated in store "+tlOrg, "status")
		lee.Lore(web, "show", storeTask).OK(t).Says(t, "in_progress")
		if err := os.Remove(kept[1]); err != nil {
			t.Fatal(err)
		}
		// nothing else was left in the temp folder
		if left, _ := os.ReadDir(tmp); len(left) != 0 {
			t.Errorf("lore edit left %d files in %s", len(left), tmp)
		}
	})
}

// jID is the "id" of a --json answer (top level or under data).
func jID(t testing.TB, r teamharness.Result) string {
	t.Helper()
	m := regexp.MustCompile(`"id": "(tsk_[0-9a-f]{32})"`).FindStringSubmatch(r.Out())
	if m == nil {
		t.Fatalf("no task id in: %s", r.Out())
	}
	return m[1]
}

// jPath decodes `lore path --json`.
func jPath(t testing.TB, r teamharness.Result) jPathOut {
	t.Helper()
	var p jPathOut
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Out())), &p); err != nil {
		t.Fatalf("lore path --json is not JSON (%v): %s", err, r.Out())
	}
	return p
}

// jRead reads a file the journey checks.
func jRead(t testing.TB, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// jFileHas fails unless the file at p exists and holds every string.
func jFileHas(t testing.TB, p string, want ...string) {
	t.Helper()
	got := jRead(t, p)
	for _, s := range want {
		if !strings.Contains(got, s) {
			t.Errorf("%s lacks %q:\n%s", p, s, got)
		}
	}
}
