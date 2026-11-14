//go:build stories && !windows

package stories

// fixtures.go — generators for the repo SHAPES the scenario tracks ran on.
// Nothing is copied from a real repo: every file is built here from made-up
// words, with the same shape (sizes, row counts per table, layout) as the
// repos the bug hunt and the dev company found bugs in.
//
//	V017Repo(dir, shape)  a lore 0.1.17 project (format 1): `_v: 1` rows,
//	                      _meta.json, LORE.md, the 0.1.17 .gitignore block;
//	                      HubShape is the 5.5k-row big repo, SmallShape a
//	                      day-to-day one
//	Monorepo(dir)         services/*, libs/*, nested go.mod and package.json
//	DocsOnly(dir)         markdown only, no code, no lore data
//	Unicode(dir)          Unicode names and text, CRLF line ends
//	PlainRepo(dir)        a code repo with no lore data (the "brick")
//
// Worktrees, shallow clones and two machines come from git and the World
// (Worktree, ShallowClone here; Persona.NewLaptop in the journeys harness).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/tests/journeys"
	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Shape is a 0.1.17 project's row count per table.
type Shape map[string]int

// Rows is the shape's total row count.
func (sh Shape) Rows() int {
	n := 0
	for _, c := range sh {
		n += c
	}
	return n
}

// HubShape is the big repo of the bug hunt and track C: 5.5k rows, mostly
// comments, tasks, commit links and memories (the proportions of that repo).
var HubShape = Shape{
	"tasks": 1277, "comments": 2058, "commit_links": 679, "memories": 626,
	"decisions": 252, "rules": 202, "hotfixes": 173, "task_lists": 164,
	"snapshots": 33, "missions": 24, "actors": 8, "projects": 1, "repos": 1,
	"incidents": 1, "patterns": 1,
}

// SmallShape is a day-to-day 0.1.17 project (the size of the company's
// service repos).
var SmallShape = Shape{
	"tasks": 47, "decisions": 44, "rules": 38, "hotfixes": 25, "memories": 8,
	"task_lists": 6, "missions": 5, "actors": 2, "projects": 1,
}

// v017Ignore is the block lore 0.1.17 put in a repo's .gitignore.
const v017Ignore = `# lore
.lore/lore.db
.lore/lore.db-shm
.lore/lore.db-wal
.lore/state/
.lore/backups/
# >>> lore-sync >>>
!.lore/data/
!.lore/data/**
.lore/data/**/.DS_Store
.lore/data/**/Desktop.ini
.lore/data/**/Thumbs.db
.lore/data/**/desktop.ini
.lore/data/**/._*
# <<< lore-sync <<<
`

// v017Prefix is the id prefix 0.1.17 gave each table's rows.
var v017Prefix = map[string]string{
	"tasks": "tsk", "comments": "cmt", "commit_links": "cml", "memories": "mem",
	"decisions": "dec", "rules": "rul", "hotfixes": "hfx", "task_lists": "tlt",
	"snapshots": "snp", "missions": "msn", "actors": "act", "projects": "prj",
	"repos": "rep", "incidents": "inc", "patterns": "pat",
}

// v017ID is a 0.1.17-shaped id: prefix, "_", the 32 hex digits of a UUIDv7
// (lore checks the version), time-ordered like the real ones: the n-th row's
// timestamp is n seconds after 2026-10-01, so it sorts after the (n-1)-th.
func v017ID(prefix string, n int) string {
	const base = 1790812800000 // 2026-10-01T00:00:00Z in Unix ms
	mix := uint64(n) * 0x9e3779b97f4a7c15
	return fmt.Sprintf("%s_%012x7%03x%x%03x%012x", prefix, base+int64(n)*1000, n&0xfff, 8|(mix>>62), (mix>>48)&0xfff, mix&0xffffffffffff)
}

// words makes deterministic made-up text of about n bytes.
func words(seed, n int) string {
	vocab := []string{"cache", "router", "billing", "queue", "retry", "schema", "deploy", "token", "worker", "index", "report", "export", "import", "session", "invoice", "webhook"}
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(vocab[(seed*7+i*3)%len(vocab)])
	}
	return b.String()
}

// V017Project is what V017Repo wrote: the project id and every row id by
// table, so a story can read a known row back.
type V017Project struct {
	ID    string
	Rows  map[string][]string
	Count int
}

// V017Repo writes a lore 0.1.17 project (format 1) into the git work tree
// dir, named name, with shape's rows: what a repo looked like after weeks of
// 0.1.17 use. The caller commits it.
func V017Repo(t testing.TB, dir, name string, shape Shape) V017Project {
	t.Helper()
	data := filepath.Join(dir, ".lore", "data")
	prj := v017ID("prj", 1)
	act := v017ID("act", 1)
	p := V017Project{ID: prj, Rows: map[string][]string{}}
	at := func(n int) string {
		return fmt.Sprintf("2026-10-%02dT%02d:%02d:%02d.000000000Z", 1+n%28, n%24, n%60, (n*7)%60)
	}
	tables := make([]string, 0, len(shape))
	for tb := range shape {
		tables = append(tables, tb)
	}
	sort.Strings(tables)
	for _, tb := range tables {
		prefix, ok := v017Prefix[tb]
		if !ok {
			t.Fatalf("V017Repo: no 0.1.17 shape for table %q", tb)
		}
		if err := os.MkdirAll(filepath.Join(data, tb), 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= shape[tb]; i++ {
			id := v017ID(prefix, i)
			if tb == "projects" {
				id = prj
			} else if tb == "actors" && i == 1 {
				id = act
			}
			row := v017Row(tb, id, prj, act, name, i, at(i))
			b, err := json.MarshalIndent(row, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(data, tb, id+".json"), append(b, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
			p.Rows[tb] = append(p.Rows[tb], id)
			p.Count++
		}
	}
	meta := fmt.Sprintf("{\n  \"_v\": 1,\n  \"project_id\": %q\n}\n", prj)
	writeFile(t, filepath.Join(data, "_meta.json"), meta)
	writeFile(t, filepath.Join(dir, ".lore", "LORE.md"), "<!-- This file is generated by lore render. Do not edit by hand. -->\n\n# "+name+"\n")
	appendFile(t, filepath.Join(dir, ".gitignore"), v017Ignore)
	return p
}

// v017Value is one field value of a 0.1.17 row (string, number or null).
type v017Value = json.RawMessage

func str(s string) v017Value { b, _ := json.Marshal(s); return b }

var null = v017Value("null")

// v017Row is one row in 0.1.17's shape for table tb (field names and kinds
// as 0.1.17 wrote them; values made up).
func v017Row(tb, id, prj, act, name string, i int, at string) map[string]v017Value {
	r := map[string]v017Value{
		"_table": str(tb), "_v": v017Value("1"), "id": str(id),
		"created_at": str(at), "updated_at": str(at),
	}
	note := func(title bool) {
		r["project_id"] = str(prj)
		r["created_by_actor_id"] = str(act)
		r["body"] = str(words(i, 120+i%400))
		if title {
			r["title"] = str(words(i+3, 30+i%40))
		}
		r["archived_at"], r["confidence"], r["last_validated_at"], r["repo_id"] = null, null, null, null
		r["source_kind"], r["source_ref"], r["trust_score"] = str("manual"), null, v017Value("0.5")
	}
	switch tb {
	case "projects":
		r["name"], r["origin_url"], r["archived_at"] = str(name), str("git@github.com:acme/"+name+".git"), null
	case "actors":
		r["display_name"], r["kind"] = str(fmt.Sprintf("Person %d", i)), str("human")
		r["stable_key"] = str(fmt.Sprintf("human:person%d@example.test", i))
	case "repos":
		r["project_id"], r["display_name"], r["mount_name"] = str(prj), str(name), str(name)
		r["origin_url"], r["archived_at"] = str("git@github.com:acme/"+name+".git"), null
	case "tasks":
		r["project_id"], r["created_by_actor_id"] = str(prj), str(act)
		r["title"], r["body"] = str(words(i, 40+i%40)), str(words(i+1, 100+i%300))
		r["status"] = str([]string{"todo", "in_progress", "done", "blocked"}[i%4])
		r["priority"] = str([]string{"low", "medium", "high"}[i%3])
		r["commitment"] = str("accepted")
		for _, k := range []string{"assigned_to_actor_id", "completed_at", "deferred_until", "due_at", "mission_id", "plan_id", "repo_id", "started_at", "tasklist_id"} {
			r[k] = null
		}
	case "task_lists":
		r["project_id"], r["title"], r["body"], r["status_str"] = str(prj), str(words(i, 24)), str(words(i, 80)), str("active")
	case "missions":
		r["project_id"], r["created_by_actor_id"] = str(prj), str(act)
		r["title"], r["body"], r["status"] = str(words(i, 30)), str(words(i, 200)), str("active")
		r["completed_at"], r["target_date"] = null, null
	case "comments":
		r["body"], r["created_by_actor_id"] = str(words(i, 60+i%200)), null
		r["entity_table"], r["entity_id"] = str("tasks"), str(v017ID("tsk", 1+i%1277))
	case "commit_links":
		r["project_id"], r["created_by_actor_id"] = str(prj), str(act)
		r["entity_table"], r["entity_id"] = str("tasks"), str(v017ID("tsk", 1+i%1277))
		r["sha"] = str(fmt.Sprintf("%040x", uint64(i)*0x9e3779b97f4a7c15))
		r["author"], r["committed_at"], r["message"], r["repo_path"] = null, null, null, null
	case "memories":
		note(false)
		r["kind"], r["superseded_by_id"], r["tx_at"] = str("core"), null, str(at)
		r["valid_at"], r["valid_until"], r["validated_by_actor_id"] = null, null, null
	case "rules":
		note(false)
		r["activation"], r["severity"], r["globs"], r["applies_to_description"], r["superseded_by_id"] = str("always"), str("must"), null, null, null
	case "decisions":
		note(true)
		r["status"], r["superseded_by_id"] = str("accepted"), null
	case "hotfixes":
		note(true)
		r["severity"], r["superseded_by_id"] = str("critical"), null
	case "snapshots":
		note(true)
		r["taken_at"] = str(at)
	case "incidents", "patterns":
		note(true)
	}
	return r
}

// Monorepo writes a monorepo's shape into dir: services and libraries in
// nested folders, each with its own module file, and a docs folder.
func Monorepo(t testing.TB, dir string) {
	t.Helper()
	for i, svc := range []string{"billing", "gateway", "search", "notify", "ledger"} {
		base := filepath.Join(dir, "services", svc)
		writeFile(t, filepath.Join(base, "go.mod"), "module example.test/mono/services/"+svc+"\n\ngo 1.22\n")
		writeFile(t, filepath.Join(base, "cmd", svc, "main.go"), "package main\n\nfunc main() {}\n")
		writeFile(t, filepath.Join(base, "internal", "core", "core.go"), "package core\n\n// "+words(i, 60)+"\n")
		writeFile(t, filepath.Join(base, "README.md"), "# "+svc+"\n\n"+words(i, 200)+"\n")
	}
	for i, lib := range []string{"ui", "sdk"} {
		base := filepath.Join(dir, "libs", lib)
		writeFile(t, filepath.Join(base, "package.json"), fmt.Sprintf("{\n  \"name\": \"@mono/%s\",\n  \"version\": \"0.0.%d\"\n}\n", lib, i))
		writeFile(t, filepath.Join(base, "src", "index.ts"), "export const name = \""+lib+"\";\n")
	}
	writeFile(t, filepath.Join(dir, "docs", "ARCHITECTURE.md"), "# Architecture\n\n"+words(9, 400)+"\n")
	writeFile(t, filepath.Join(dir, "go.work"), "go 1.22\n\nuse (\n\t./services/billing\n\t./services/gateway\n)\n")
	writeFile(t, filepath.Join(dir, "README.md"), "# mono\n")
}

// DocsOnly writes a docs-only repo: markdown pages and an image folder, no
// code and no lore data.
func DocsOnly(t testing.TB, dir string) {
	t.Helper()
	for i, page := range []string{"index", "guide/install", "guide/usage", "reference/api", "faq"} {
		writeFile(t, filepath.Join(dir, page+".md"), "# "+filepath.Base(page)+"\n\n"+words(i, 300)+"\n")
	}
	writeFile(t, filepath.Join(dir, "img", "README.md"), "images go here\n")
}

// UnicodeTitle and UnicodeBody are note texts with scripts, accents and
// emoji: what a person in another language writes. They must come back
// exactly as written.
const (
	UnicodeTitle = "Zahlungsprüfung für Ümlaut-Kunden — 日本語 ✓"
	UnicodeBody  = "Привет, мир. Café crème, naïve façade; emoji 🚀 and RTL שלום."
)

// Unicode writes files with Unicode names and text and CRLF line ends (a
// repo edited on Windows), plus a .gitattributes that keeps them CRLF.
func Unicode(t testing.TB, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, ".gitattributes"), "*.txt -text\n")
	writeFile(t, filepath.Join(dir, "notes", "überblick.txt"), crlf(UnicodeTitle+"\n"+UnicodeBody+"\n"))
	writeFile(t, filepath.Join(dir, "docs", "日本語.txt"), crlf("一行目\n二行目\n"))
	writeFile(t, filepath.Join(dir, "README.txt"), crlf("# unicode\nline two\n"))
}

// CRLF turns \n into \r\n (a note body pasted from a Windows editor).
func CRLF(s string) string { return crlf(s) }

func crlf(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

// PlainRepo writes a small code repo with no lore data (the brick every
// track checks stays byte-identical).
func PlainRepo(t testing.TB, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(dir, "README.md"), "# brick\n\n"+words(3, 120)+"\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "/bin/\n")
}

func writeFile(t testing.TB, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t testing.TB, p, body string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
}

// Worktree adds a linked worktree of the clone at dir on a new branch and
// returns its folder (a second checkout of the same repo, as people keep
// for a hotfix or an agent session).
func (s *Story) Worktree(t testing.TB, cloneDir, branch string) string {
	t.Helper()
	wt := filepath.Join(filepath.Dir(cloneDir), filepath.Base(cloneDir)+"-"+branch)
	s.Git(cloneDir, "worktree", "add", "-q", "-b", branch, wt)
	return wt
}

// ShallowClone clones r into m's HOME at name with only the last depth
// commits (a CI checkout, `git clone --depth 1`) and hands it to m, so lore
// runs in it as m. The origin URL is the remote's usual https form.
func (s *Story) ShallowClone(t testing.TB, m *journeys.Machine, r *teamharness.Remote, name string, depth int) *teamharness.Clone {
	t.Helper()
	dir := filepath.Join(m.Home, name)
	s.Git(m.Home, "clone", "-q", "--depth", fmt.Sprint(depth), "--no-local", "file://"+r.Dir, dir)
	s.Git(dir, "remote", "set-url", "origin", r.URL())
	return m.OpenClone(r, dir)
}
