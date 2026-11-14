//go:build stories && !windows

package stories

// I_two_machines_text_test.go — story I (tests/stories/I-two-machines-text.md):
// one person on two laptops, text in every script, and the environments
// people really run in. Nothing is lost between the laptops, one person is
// one actor, text comes back exactly as stored (after lore's documented
// cleanup: CRLF becomes LF, NFC, bidi controls dropped, ZWJ/ZWNJ kept),
// nothing a teammate writes can drive the reader's terminal, and a plain
// read never writes a tracked file.
//
// Linked journeys: TestJourneyTextSafety walks ESC sequences, ZWJ/ZWNJ and
// CJK search in depth; TestJourneyPullNeverBlocked walks the fresh-clone
// read that wrote tracked rows. This story replays the track's own mix.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/tests/teame2e/teamharness"
)

// Placeholders of story I: one made-up person with two laptops.
const (
	iCtoName = "Chloé Marchetti"
	iCtoMail = "chloe@twomachines.example.test"
	iDevName = "Zoë Lindqvist"
	iDevMail = "zoe@twomachines.example.test"
)

func TestStoryITwoMachines(t *testing.T) {
	s := New(t, "I")
	app := s.Seed(s.Remote("github.com/twomachines/api"), func(dir string) { PlainRepo(t, dir) })
	notes := s.Remote("github.com/twomachines/notes")
	cto := s.Persona(iCtoName, iCtoMail)
	a := cto.Laptop()
	b := cto.NewLaptop("second")
	var ca, cb *teamharness.Clone
	var repoNote, storeNote string

	s.Step("day1/laptop-a-starts-and-shares", func() {
		ca = a.Clone(app, "api")
		a.LoreIn(ca, "init").OK(t)
		a.LoreIn(ca, "store", "init", "--name", "notes", "--url", notes.SSHURL(), "--yes").OK(t)
		repoNote = iAdded(t, a.LoreIn(ca, "memory", "add", "--body", "written on laptop a").OK(t))
		storeNote = iAdded(t, a.LoreIn(ca, "memory", "add", "--body", "store note from a", "--to", "notes").OK(t))
		a.LoreIn(ca, "sync").OK(t)
		ca.CommitAll("lore: adopt")
		ca.Git("push", "-q", "origin", "HEAD:main")
	})

	s.Step("day1/laptop-b-sees-everything-as-the-same-person", func() {
		cb = b.Clone(app, "api")
		b.LoreIn(cb, "sync").OK(t)
		b.LoreIn(cb, "memory", "list").OK(t).Says(t, "written on laptop a", "store note from a")
		wa := a.LoreIn(ca, "whoami").OK(t)
		wb := b.LoreIn(cb, "whoami").OK(t)
		ida, idb := iActor(wa.Out()), iActor(wb.Out())
		if ida == "" || ida != idb {
			t.Errorf("one person on two laptops must be one actor: a %q, b %q\n%s\n%s", ida, idb, wa, wb)
		}
	})

	s.Step("day2/both-edit-the-same-notes-offline", func() {
		s.AdvanceDays(1)
		a.Offline()
		b.Offline()
		a.LoreIn(ca, "memory", "edit", "--id", repoNote, "--body", "edited on laptop a").OK(t)
		a.LoreIn(ca, "memory", "edit", "--id", storeNote, "--body", "store edit from a").OK(t)
		b.LoreIn(cb, "memory", "edit", "--id", repoNote, "--body", "edited on laptop b").OK(t)
		b.LoreIn(cb, "memory", "edit", "--id", storeNote, "--body", "store edit from b").OK(t)
		a.LoreIn(ca, "memory", "add", "--body", "only on a").OK(t)
		b.LoreIn(cb, "memory", "add", "--body", "only on b").OK(t)
		ca.CommitAll("notes on a")
		cb.CommitAll("notes on b")
	})

	s.Step("day2/both-come-back-and-nothing-is-lost", func() {
		a.Online()
		b.Online()
		ca.Git("push", "-q", "origin", "HEAD:main")
		a.LoreIn(ca, "sync")
		// the same field edited on both laptops is a real conflict: git
		// stops, and lore's merge driver keeps both edits in the row file
		r := cb.TryGit("pull", "-q", "--no-rebase", "origin", "main")
		if r.Code == 0 {
			t.Logf("git pull merged both edits of one field without a conflict: %s", r)
		} else {
			f := filepath.Join(cb.Dir, ".lore", "data", "memories", repoNote+".json")
			body, _ := os.ReadFile(f)
			if !strings.Contains(string(body), "edited on laptop a") || !strings.Contains(string(body), "edited on laptop b") {
				t.Fatalf("the conflicted row lost an edit:\n%s\n%s", body, r)
			}
			// lore shows both sides and doctor counts the conflict, so the
			// person settles it with the row's edit command
			b.LoreIn(cb, "memory", "show", "--id", repoNote).Says(t, "edited on laptop a", "edited on laptop b", "conflict markers")
			b.LoreIn(cb, "doctor").Says(t, "open conflicts").Never(t, "HEALTHY")
			b.LoreIn(cb, "memory", "edit", "--id", repoNote, "--body", "edited on laptop b").OK(t)
			cb.Git("add", "-A")
			cb.Git("commit", "-q", "--no-edit")
		}
		if st := cb.Status(); st != "" {
			t.Fatalf("the merge left files behind:\n%s", st)
		}
		b.LoreIn(cb, "sync")
		list := b.LoreIn(cb, "memory", "list").OK(t)
		list.Says(t, "only on a", "only on b")
		got := iShow(t, b, cb, repoNote).Str("body")
		if got != "edited on laptop a" && got != "edited on laptop b" {
			t.Errorf("the note edited on both laptops reads %q", got)
		}
		// the store note: one edit wins, the other is kept where lore says
		sn := iShow(t, b, cb, storeNote).Str("body")
		kept := b.LoreIn(cb, "sync", "conflicts", "--all").Out() + sn
		if !strings.Contains(kept, "store edit from a") && !strings.Contains(kept, "store edit from b") {
			t.Errorf("neither store edit survived: %q / %s", sn, kept)
		}
	})

	iWaiting(t, "D-120", func(t *testing.T) {
		// doctor counts the open store sync conflicts and names the command
		// that settles them; never HEALTHY with "open conflicts: 0"
		b.LoreIn(cb, "doctor").Never(t, "open conflicts: 0", "lore sync pushes them")
	})

	iWaiting(t, "D-121", func(t *testing.T) {
		// sync's counts match what moved: one row arrives, "received 1"
		a.LoreIn(ca, "memory", "add", "--body", "one more from a", "--to", "notes").OK(t)
		a.LoreIn(ca, "sync").OK(t).Says(t, "sent 1")
		b.LoreIn(cb, "sync").OK(t).Says(t, "received 1")
	})

	iWaiting(t, "D-122", func(t *testing.T) {
		// every actor doctor's Team versions names can be looked up
		r := b.LoreIn(cb, "doctor")
		for _, id := range iActors(r.Out()) {
			b.LoreIn(cb, "actor", "show", "--id", id).OK(t)
		}
	})

	iWaiting(t, "D-153", func(t *testing.T) {
		// the org admin's second laptop can join as an admin
		b.LoreIn(cb, "setup", "join", "--help").OK(t).Says(t, "admin")
	})

	s.Step("after/no-surprise-pushes", func() {
		before, nb := s.RefsOf(app), s.RefsOf(notes)
		a.LoreIn(ca, "memory", "list").OK(t)
		b.LoreIn(cb, "doctor")
		s.NoPushSince(t, app, before, "reads and doctor on both laptops")
		s.NoPushSince(t, notes, nb, "reads and doctor on both laptops")
	})
}

func TestStoryIText(t *testing.T) {
	s := New(t, "I")
	app := s.Seed(s.Remote("github.com/twomachines/words"), func(dir string) { Unicode(t, dir) })
	m := s.Person(iDevName, iDevMail)
	var c *teamharness.Clone
	long := strings.Repeat("Ünïcödé title words ", 15) // 300 characters
	texts := []struct{ name, title, body, wantBody string }{
		{"accents", UnicodeTitle, UnicodeBody, UnicodeBody},
		{"cjk", "数据库迁移规则 日本語のテスト 한국어", "不要在生产环境直接修改表。", "不要在生产环境直接修改表。"},
		{"joined-emoji-and-zwnj", "Deploy 🚀 👩‍💻 👨‍👩‍👧‍👦 می‌خواهم", "हिन्दी क्‍ष", "हिन्दी क्‍ष"},
		{"rtl", "שלום עולם مرحبا", "טקסט מימין לשמאל", "טקסט מימין לשמאל"},
		{"long-title", strings.TrimSpace(long), "x", "x"},
		{"markdown", "Markdown table", "| a | b |\n|---|---|\n| 1 | 2 |\n\n```go\nfunc main() {}\n```", "| a | b |\n|---|---|\n| 1 | 2 |\n\n```go\nfunc main() {}\n```"},
		// documented cleanup: Windows line ends become LF
		{"crlf", "Pasted from a Windows editor", CRLF("first line\nsecond line"), "first line\nsecond line"},
	}

	s.Step("day1/every-script-round-trips", func() {
		c = m.Clone(app, "words")
		m.LoreIn(c, "init").OK(t)
		for _, x := range texts {
			id := iAdded(t, m.LoreIn(c, "decision", "add", "--title", x.title, "--body", x.body).OK(t))
			got := iShow(t, m, c, id)
			if got.Str("title") != x.title || got.Str("body") != x.wantBody {
				t.Errorf("%s: show --json gives title %q body %q, want %q / %q", x.name, got.Str("title"), got.Str("body"), x.title, x.wantBody)
			}
			m.LoreIn(c, "show", id).OK(t).Says(t, x.title)
		}
		r := m.LoreIn(c, "decision", "list", "--json").OK(t)
		if !utf8.ValidString(r.Stdout) || !json.Valid([]byte(r.Stdout)) {
			t.Errorf("decision list --json is not valid UTF-8 JSON:\n%s", r.Stdout)
		}
	})

	s.Step("day1/cjk-and-emoji-are-found", func() {
		for _, q := range []string{"迁移", "日本語", "生产环境", "🚀", "Ümlaut", "naïve", "שלום"} {
			m.LoreIn(c, "search", q).OK(t).Never(t, "(no matches)")
		}
		m.LoreIn(c, "search", "zzqqxx").OK(t).Says(t, "(no matches)")
	})

	s.Step("day1/control-characters-print-escaped", func() {
		const esc = "\x1b"
		id := iAdded(t, m.LoreIn(c, "memory", "add", "--body", "log "+esc+"[31mred"+esc+"[0m "+esc+"[2J").OK(t))
		for _, args := range [][]string{{"show", id}, {"memory", "list"}, {"search", "log"}} {
			m.LoreIn(c, args...).OK(t).Never(t, esc)
		}
	})

	s.Step("day1/a-1mb-body-is-found-fast", func() {
		var b strings.Builder
		for i := 0; b.Len() < 1<<20; i++ {
			fmt.Fprintf(&b, "line %d of a very long pasted log with words in it\n", i)
		}
		b.WriteString("needle-word-at-the-end")
		// a 1 MB argument cannot be passed (the kernel caps one at 128 KB):
		// people paste it through lore's Markdown edit, as the track did
		id := iAdded(t, m.LoreIn(c, "memory", "add", "--body", "placeholder body").OK(t))
		md := m.LoreIn(c, "row", "markdown", "--id", id).OK(t).Stdout
		file := filepath.Join(t.TempDir(), "big.md")
		if err := os.WriteFile(file, []byte(strings.Replace(md, "placeholder body", b.String(), 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		m.LoreIn(c, "edit", "--id", id, "--file", file).OK(t)
		start := time.Now()
		m.LoreIn(c, "search", "needle-word-at-the-end").OK(t).Never(t, "(no matches)")
		// generous for a loaded machine: before the fix it took ~10 s of CPU
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("search hitting a 1 MB body took %s", d)
		}
	})

	s.Step("day1/render-never-hard-wraps", func() {
		m.LoreIn(c, "rule", "add", "--body", strings.TrimSpace(long)+" must stay on one line").OK(t)
		m.LoreIn(c, "render").OK(t)
		b, err := os.ReadFile(filepath.Join(c.Dir, ".lore", "LORE.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), strings.TrimSpace(long)+" must stay on one line") {
			t.Errorf("LORE.md split the long rule over lines:\n%s", b)
		}
	})
}

func TestStoryIEnvironment(t *testing.T) {
	s := New(t, "I")
	app := s.Seed(s.Remote("github.com/twomachines/service"), func(dir string) { PlainRepo(t, dir) })
	site := s.Seed(s.Remote("github.com/twomachines/site"), func(dir string) { PlainRepo(t, dir) })
	cto := s.Person(iCtoName, iCtoMail)
	dev := s.Persona(iDevName, iDevMail)
	m := dev.Laptop()
	var ca, cs *teamharness.Clone
	var siteTree, siteRefs string

	s.Step("day1/a-project-with-notes", func() {
		ca = cto.Clone(app, "service")
		cto.LoreIn(ca, "init").OK(t)
		cto.LoreIn(ca, "memory", "add", "--body", "Café crème naïve 日本語").OK(t)
		ca.CommitAll("lore: adopt")
		ca.Git("push", "-q", "origin", "HEAD:main")
		cs = m.Clone(site, "site")
		siteTree, siteRefs = TreeOf(t, cs.Dir), s.RefsOf(site)
	})

	s.Step("day1/a-read-on-a-fresh-clone-writes-nothing-tracked", func() {
		c := m.Clone(app, "service")
		m.LoreIn(c, "memory", "list").OK(t).Says(t, "Café crème naïve")
		m.LoreIn(c, "sync")
		if st := c.Status(); st != "" {
			t.Errorf("a read on a fresh clone changed tracked files:\n%s", st)
		}
	})

	for _, mode := range []string{"true", "input"} {
		s.Step("day2/autocrlf-"+mode+"-stays-byte-stable", func() {
			dir := filepath.Join(m.Home, "service-autocrlf-"+mode)
			s.Git(m.Home, "clone", "-q", "-c", "core.autocrlf="+mode, app.Dir, dir)
			c := m.OpenClone(app, dir)
			m.LoreIn(c, "memory", "list").OK(t)
			m.LoreIn(c, "memory", "add", "--body", "written with autocrlf "+mode).OK(t)
			m.LoreIn(c, "render").OK(t)
			c.CommitAll("lore: a note")
			m.LoreIn(c, "memory", "list").OK(t)
			m.LoreIn(c, "render").OK(t)
			if st := c.Status(); st != "" {
				t.Errorf("autocrlf=%s: lore files keep showing as changed:\n%s", mode, st)
			}
		})
	}

	s.Step("day3/paths-with-spaces-and-accents-and-lang-c", func() {
		c := m.Clone(app, "my repo ü")
		m.LoreIn(c, "memory", "list").OK(t).Says(t, "Café crème naïve 日本語")
		m.SetEnv("LANG", "C")
		m.SetEnv("LC_ALL", "C")
		m.LoreIn(c, "memory", "list").OK(t).Says(t, "Café crème naïve 日本語")
		m.LoreIn(c, "search", "crème").OK(t).Never(t, "(no matches)")
		m.SetEnv("LANG", "C.UTF-8")
		m.SetEnv("LC_ALL", "C.UTF-8")
	})

	iWaiting(t, "D-136", func(t *testing.T) {
		// lists show the viewer's local date: a reminder due tomorrow 20:00
		// in Los Angeles is listed on that day, not the next UTC day
		m.SetEnv("TZ", "America/Los_Angeles")
		m.LoreIn(ca, "reminder", "add", "--message", "standup", "--due", "tomorrow 20:00").OK(t)
		m.LoreIn(ca, "reminder", "list").OK(t).Says(t, s.Now().Add(24*time.Hour).Format("2006-01-02"))
	})

	iWaiting(t, "D-137", func(t *testing.T) {
		// search folds the Turkish dotted and dotless i
		m.SetEnv("LANG", "tr_TR.UTF-8")
		m.LoreIn(ca, "memory", "add", "--body", "ıspanak DİYARBAKIR").OK(t)
		m.LoreIn(ca, "search", "ISPANAK").OK(t).Never(t, "(no matches)")
		m.LoreIn(ca, "search", "diyarbakır").OK(t).Never(t, "(no matches)")
	})

	iWaiting(t, "D-138", func(t *testing.T) {
		// a handle with an accent keeps it, and its ASCII spelling resolves
		cto.LoreIn(ca, "member", "add", "--email", "chloe.b@twomachines.example.test", "--handle", "chloé").OK(t).Never(t, "@chlo ")
		cto.LoreIn(ca, "task", "add", "--title", "x", "--assign", "@chloe").OK(t)
	})

	iWaiting(t, "D-139", func(t *testing.T) {
		// a shared group checkout: under umask 002 a teammate in the group
		// can edit a row; under 077 lore keeps the checkout's own modes
		f := hRows(t, ca.Dir, "memories")[0]
		fi, err := os.Stat(f)
		if err != nil || fi.Mode().Perm()&0o060 != 0o060 {
			t.Errorf("row file %s mode %v under umask 002", f, fi.Mode())
		}
	})

	iWaiting(t, "D-145", func(t *testing.T) {
		// lore where labels a live client repo's caches as live, never gone
		m.LoreIn(cs, "where").Never(t, "gone: moved or deleted")
	})

	iWaiting(t, "D-146", func(t *testing.T) {
		// a refused write in a client repo never hints lore init there
		m.LoreIn(cs, "memory", "add", "--body", "x").Never(t, "lore init")
	})

	iWaiting(t, "D-147", func(t *testing.T) {
		// init writes its pointer once when CLAUDE.md imports @AGENTS.md
		cs.WriteFile("AGENTS.md", "# agents\n")
		cs.WriteFile("CLAUDE.md", "@AGENTS.md\n")
		m.LoreIn(cs, "init").OK(t)
		got, _ := cs.ReadFile("CLAUDE.md")
		if strings.Contains(got, "LORE.md") {
			t.Errorf("init pointed CLAUDE.md at lore too although it imports AGENTS.md:\n%s", got)
		}
	})

	s.Step("after/untouched-repo-and-no-pushes", func() {
		s.Untouched(t, cs.Dir, siteTree, "every environment step")
		s.NoPushSince(t, site, siteRefs, "every environment step")
	})
}

// iActor is the act_ id `lore whoami` prints on its identity line.
func iActor(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "identity") {
			return iActorID.FindString(line)
		}
	}
	return ""
}

// iActors is every act_ id an output names.
func iActors(out string) []string { return iActorID.FindAllString(out, -1) }
