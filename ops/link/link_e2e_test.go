package link_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// v017Task and v017Link are real ids from a v0.1.17 run (lore 0.1.17:
// `lore task add`, then `lore link add --entity=<task> --commit=<v017SHA>`
// wrote .lore/data/commit_links/<v017Link>.json). The link's natural id is
// sha-derived from (entity_table, entity_id, sha), so the core must write
// the same file name in the repo store or two writers of one repository
// would keep two copies of one link.
const (
	v017Task = "tsk_01a1a67b1b617dc7acc5f1af36e0e979"
	v017Link = "cml_61badff4bbd97f629ae3686deb4a15fe"
	v017SHA  = "0123456789abcdef0123456789abcdef01234567"
)

// refOf and linkRef are engine refs of a task and a link row.
func refOf(id ids.AnyID) engine.Ref   { return engine.Ref{Table: schema.TableTasks, ID: id} }
func linkRef(id ids.AnyID) engine.Ref { return engine.Ref{Table: schema.TableCommitLinks, ID: id} }

// seedTask writes a live task row under v017Task through the engine.
func seedTask(t *testing.T, h *opstest.H) {
	t.Helper()
	tid, err := ids.ParseAny(v017Task)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := ids.ParseAny(h.Project)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]value.Value{}
	if fields["project_id"], err = value.RefOf(proj); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"title": "link target", "status": "todo", "priority": "medium", "commitment": "accepted"} {
		if fields[k], err = value.TextOf(v); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.App.CreateWithID(context.Background(), store.SourceRepo, schema.TableTasks, tid, fields); err != nil {
		t.Fatal(err)
	}
}

// commit makes a commit with subject in the harness repo (the message
// goes in on stdin so the git command line is constant) and returns its
// full sha.
func commit(t *testing.T, h *opstest.H, subject string) string {
	t.Helper()
	cmd := exec.Command("git", "-c", "user.name=Test Author", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-F", "-")
	cmd.Dir, cmd.Stdin = h.Repo, strings.NewReader(subject)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = h.Repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// TestLinkNaturalIDMatchesV017: same entity + sha in the repo store gives
// v0.1.17's link id, and the human lines are v0.1.17's.
func TestLinkNaturalIDMatchesV017(t *testing.T) {
	h := opstest.New(t)
	seedTask(t, h)
	res := h.Must("link.add", `{"entity":"`+v017Task+`","commit":"`+v017SHA+`","message":"fix the thing"}`)
	if got := h.ID(res); got != v017Link {
		t.Fatalf("link id %s, v0.1.17 wrote %s", got, v017Link)
	}
	// v0.1.17: "✓ linked 01234567 → tasks/<id>  (<cml>)\n  message: fix the thing\n"
	want := "✓ linked 01234567 → tasks/" + v017Task + "  (" + v017Link + ")\n  message: fix the thing\n"
	if got := opstest.Text(res); got != want {
		t.Fatalf("add text\n got %q\nwant %q", got, want)
	}
	list := h.Must("link.list", `{"entity":"`+v017Task+`"}`)
	if got, want := opstest.Text(list), v017Link+"  01234567 → tasks/"+v017Task+" — fix the thing\n"; got != want {
		t.Fatalf("list text\n got %q\nwant %q", got, want)
	}
	show := h.Must("commit.show", `{"id":"`+v017SHA+`"}`)
	if got, want := opstest.Text(show), "Commit "+v017SHA+" — 1 linked entities:\n  tasks/"+v017Task+"\n"; got != want {
		t.Fatalf("show text\n got %q\nwant %q", got, want)
	}
	// v0.1.17's refusal of the same link, same words.
	msg := h.Fails("link.add", `{"entity":"`+v017Task+`","commit":"`+v017SHA+`"}`, errs.InvalidInput)
	if !strings.Contains(msg, "commit 01234567 already linked to tasks/"+v017Task) {
		t.Fatalf("duplicate message: %s", msg)
	}
}

// TestLinkCommitForms is the --commit matrix: HEAD and auto fill message,
// author and time from git; a short sha resolves to the full one without
// filling; an unknown sha-looking value is kept as given; anything else is
// refused with v0.1.17's words. Empty entity or commit is refused.
func TestLinkCommitForms(t *testing.T) {
	h := opstest.New(t)
	seedTask(t, h)
	head := commit(t, h, "first subject")

	res := h.Must("link.add", `{"entity":"`+v017Task+`","commit":"HEAD","repo_path":`+opstest.Quote(h.Repo)+`}`)
	var rec struct {
		SHA, Message string
	}
	rec.SHA, rec.Message = h.Field(res, "sha"), h.Field(res, "message")
	if rec.SHA != head || rec.Message != "first subject" {
		t.Fatalf("HEAD: %+v, want %s / first subject", rec, head)
	}
	list := h.JSON(h.Must("link.list", `{"commit":"`+head+`"}`))
	if !strings.Contains(list, `"author":"Test Author`) || !strings.Contains(list, `"committed_at":"`) {
		t.Fatalf("HEAD link lacks author/time: %s", list)
	}

	second := commit(t, h, "second subject")
	res = h.Must("link.add", `{"entity":"`+v017Task+`","commit":"auto","message":"mine","repo_path":`+opstest.Quote(h.Repo)+`}`)
	if h.Field(res, "sha") != second || h.Field(res, "message") != "mine" {
		t.Fatalf("auto with --message: %s", h.JSON(res))
	}

	// a short sha resolves to the full one: the same link as auto's.
	if msg := h.Fails("link.add", `{"entity":"`+v017Task+`","commit":"`+second[:7]+`","repo_path":`+opstest.Quote(h.Repo)+`}`, errs.InvalidInput); !strings.Contains(msg, "already linked") {
		t.Fatalf("short sha of a linked commit: %s", msg)
	}
	third := commit(t, h, "third subject")
	res = h.Must("link.add", `{"entity":"`+v017Task+`","commit":"`+third[:9]+`","repo_path":`+opstest.Quote(h.Repo)+`}`)
	if h.Field(res, "sha") != third || h.Field(res, "message") != "" {
		t.Fatalf("short sha: %s (a ref other than HEAD/auto fills nothing)", h.JSON(res))
	}
	kept := "deadbeefcafe1234"
	res = h.Must("link.add", `{"entity":"`+v017Task+`","commit":"`+kept+`"}`)
	if h.Field(res, "sha") != kept || h.Field(res, "message") != "" {
		t.Fatalf("unresolvable sha-like: %s", h.JSON(res))
	}
	for _, bad := range []string{"nothex", "HEAD~99", "ABCDEF0"} {
		msg := h.Fails("link.add", `{"entity":"`+v017Task+`","commit":"`+bad+`","repo_path":`+opstest.Quote(h.Repo)+`}`, errs.InvalidInput)
		if !strings.Contains(msg, "could not resolve") {
			t.Errorf("%s: %s", bad, msg)
		}
	}
	for _, in := range []string{`{}`, `{"entity":"` + v017Task + `"}`, `{"commit":"HEAD"}`} {
		if msg := h.Fails("link.add", in, errs.InvalidInput); !strings.Contains(msg, "--entity and --commit are required") {
			t.Errorf("%s: %s", in, msg)
		}
	}
}

// TestLinkEntityChecks: a core table's target must be a live row (missing
// and deleted are E_NOT_FOUND); a table not on the core is accepted as
// v0.1.17 accepted it; an unknown prefix is refused.
func TestLinkEntityChecks(t *testing.T) {
	h := opstest.New(t)
	missing := "tsk_01a1a67b1b617dc7acc5f1af36e0e000"
	h.Fails("link.add", `{"entity":"`+missing+`","commit":"`+v017SHA+`"}`, errs.NotFound)

	seedTask(t, h)
	tid, err := ids.ParseAny(v017Task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.App.Delete(context.Background(), refOf(tid)); err != nil {
		t.Fatal(err)
	}
	if msg := h.Fails("link.add", `{"entity":"`+v017Task+`","commit":"`+v017SHA+`"}`, errs.NotFound); !strings.Contains(msg, "deleted") {
		t.Fatalf("deleted target: %s", msg)
	}
	if msg := h.Fails("link.add", `{"entity":"xyz_01a1a67b1b617dc7acc5f1af36e0e979","commit":"`+v017SHA+`"}`, errs.InvalidInput); !strings.Contains(msg, "unknown ID prefix") {
		t.Fatalf("unknown prefix: %s", msg)
	}
	// runs are not on the core: accepted without a row (v0.1.17).
	run := "run_01a1a67b1b617dc7acc5f1af36e0e979"
	if _, err := h.Run("link.add", `{"entity":"`+run+`","commit":"`+v017SHA+`"}`); err != nil {
		t.Fatalf("non-core table: %v", err)
	}
	if got := opstest.Text(h.Must("link.list", `{"entity":"`+run+`"}`)); !strings.Contains(got, "runs/"+run) {
		t.Fatalf("non-core link not listed: %q", got)
	}
	// list --entity with a malformed id is v0.1.17's not-found.
	h.Fails("link.list", `{"entity":"garbage"}`, errs.NotFound)
	if got := opstest.Text(h.Must("link.list", `{"commit":"nope"}`)); got != "(no links)\n" {
		t.Fatalf("empty list text %q", got)
	}
	if got := opstest.Text(h.Must("commit.show", `{"id":"nope"}`)); got != "(no entities linked to this commit)\n" {
		t.Fatalf("empty show text %q", got)
	}
	h.Fails("commit.show", `{"id":"  "}`, errs.InvalidInput)
}

// TestLinkRemoveThenReAdd: remove asks for confirmation, keeps a tombstone
// (the link is gone from list and show), and adding the same link again is
// allowed under a fresh id (decision 41cac0c: a tombstoned natural id is
// re-created under a new id when no live row holds the key), so the
// tombstone itself is never resurrected.
func TestLinkRemoveThenReAdd(t *testing.T) {
	h := opstest.New(t)
	seedTask(t, h)
	h.Must("link.add", `{"entity":"`+v017Task+`","commit":"`+v017SHA+`"}`)
	h.Fails("link.remove", `{"id":"`+v017Link+`"}`, errs.NeedsConfirm)
	res := h.MustYes("link.remove", `{"id":"`+v017Link+`"}`)
	if got := opstest.Text(res); got != "✓ link "+v017Link+" removed\n" {
		t.Fatalf("remove text %q", got)
	}
	if got := opstest.Text(h.Must("link.list", ``)); got != "(no links)\n" {
		t.Fatalf("after remove: %q", got)
	}
	id, err := ids.ParseAny(v017Link)
	if err != nil {
		t.Fatal(err)
	}
	if _, r, err := h.App.Get(context.Background(), linkRef(id)); err != nil || !r.Deleted {
		t.Fatalf("tombstone: %v deleted=%v", err, r.Deleted)
	}
	again := h.Must("link.add", `{"entity":"`+v017Task+`","commit":"`+v017SHA+`"}`)
	if got := h.ID(again); got == v017Link || !strings.HasPrefix(got, "cml_") {
		t.Fatalf("re-add id %s: want a fresh cml_ id", got)
	}
	if _, r, err := h.App.Get(context.Background(), linkRef(id)); err != nil || !r.Deleted {
		t.Fatalf("tombstone resurrected: %v deleted=%v", err, r.Deleted)
	}
	h.Fails("link.remove", `{"id":""}`, errs.InvalidInput)
	for _, bad := range []string{" ", "tsk_01a1a67b1b617dc7acc5f1af36e0e979", "cml_zz"} {
		if _, err := h.RunYes("link.remove", `{"id":`+opstest.Quote(bad)+`}`); !errs.Has(err, errs.InvalidID) {
			t.Errorf("remove %q: %v, want E_INVALID_ID", bad, err)
		}
	}
}

// TestLinkMessageExcerpt: list cuts a message over 60 bytes at 57 + "...",
// as v0.1.17 did, but never inside a UTF-8 character (v0.1.17 cut "é…"
// mid-character and printed an invalid byte).
func TestLinkMessageExcerpt(t *testing.T) {
	h := opstest.New(t)
	seedTask(t, h)
	cases := map[string]string{
		strings.Repeat("a", 60): strings.Repeat("a", 60),
		strings.Repeat("a", 61): strings.Repeat("a", 57) + "...",
		strings.Repeat("é", 40): strings.Repeat("é", 28) + "...",
		strings.Repeat("😀", 20): strings.Repeat("😀", 14) + "...",
	}
	sha := 0
	for msg, want := range cases {
		sha++
		s := strings.Repeat(string(rune('a'+sha)), 7)
		h.Must("link.add", `{"entity":"`+v017Task+`","commit":"`+s+`","message":`+opstest.Quote(msg)+`}`)
		got := opstest.Text(h.Must("link.list", `{"commit":"`+s+`"}`))
		if !utf8.ValidString(got) {
			t.Errorf("invalid UTF-8 for %d-byte message: %q", len(msg), got)
		}
		if !strings.HasSuffix(got, " — "+want+"\n") {
			t.Errorf("message %q:\n got %q\nwant suffix %q", msg, got, want)
		}
	}
}
