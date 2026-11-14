package wire

// sync_core_host_test.go — what the core adds around a sync host that
// leaves the stores and row files to it (opssync.CoreStores: cmd/lore's
// live host): G-SYNC-2 git-marked row files in conflicts/status/resolve,
// the sync run's store exchange and "· already in sync" line, and the
// `lore sync purge` pointer (FA-DEL-4).

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/wire/internal/isolate"

	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
)

// coreStub is a sync host like cmd/lore's: live conflicts only (live
// numbers of them), the core does the rest. unsupported makes conflicts
// and resolve answer E_UNSUPPORTED (LORE_LIVE=0).
type coreStub struct {
	opssync.Env
	live        int
	lines       []string
	unsupported bool
	resolved    *string
}

func (coreStub) CoreStores() bool { return true }

func (s coreStub) SyncRun(context.Context, opssync.RunIn) (opssync.RunOut, error) {
	return opssync.RunOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindReport, Data: opssync.RunData{Pass: "live"}, Lines: s.lines}, nil
}

func (s coreStub) SyncStatus(context.Context, opssync.StatusIn) (opssync.StatusOut, error) {
	return opssync.StatusOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindStatus}, nil
}

func (s coreStub) SyncConflicts(context.Context, opssync.ConflictsIn) (opssync.ConflictsOut, error) {
	if s.unsupported {
		return opssync.ConflictsOut{}, errs.New(errs.Unsupported, "live rows are not available here")
	}
	out := opssync.ConflictsOut{SchemaVersion: opssync.SchemaVersion, Kind: opssync.KindConflicts, Data: []opssync.Conflict{}}
	for i := range s.live {
		out.Data = append(out.Data, opssync.Conflict{ID: int64(i + 1), Table: "tasks", RowID: "live", Kept: "both"})
		out.Lines = append(out.Lines, "#live")
	}
	out.Count = len(out.Data)
	if out.Count == 0 {
		out.Lines = []string{"· no conflicts"}
	}
	return out, nil
}

func (s coreStub) SyncResolve(_ context.Context, in opssync.ResolveIn) (opssync.DoneOut, error) {
	if s.unsupported {
		return opssync.DoneOut{}, errs.New(errs.Unsupported, "live rows are not available here")
	}
	if s.resolved != nil {
		*s.resolved = in.ID
	}
	return opssync.DoneOut{Done: "host"}, nil
}

func (coreStub) SyncPurge(context.Context, opssync.PurgeIn) (opssync.DoneOut, error) {
	return opssync.DoneOut{}, errs.New(errs.Unsupported, "not here")
}

// coreHostApp is an App in a fresh repo whose sync host is h; notes
// collects Options.Notice lines.
func coreHostApp(t *testing.T, h opssync.Env, notes *[]string) (*App, string) {
	t.Helper()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{Sync: h, Notice: func(m string) { *notes = append(*notes, m) },
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, a)
	return a, repo
}

// addMemory adds a memory and returns its id.
func addMemory(t *testing.T, a *App, body string) string {
	t.Helper()
	out, err := run(t, a, opsmemory.OpAdd, `{"body":"`+body+`"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	return idOf(t, out)
}

// bodyLine is the "body" line of a canonical row file.
var bodyLine = regexp.MustCompile(`(?m)^(\s*)"body": "[^"]*"(,?)$`)

// markLines rewrites the memory file the way git leaves a text clash on
// the body line: marker lines around both versions (diff3 off).
func markLines(t *testing.T, path, ours, theirs string) {
	t.Helper()
	b := mustRead(t, path)
	m := bodyLine.FindSubmatch(b)
	if m == nil {
		t.Fatalf("fixture: no body line in %s", b)
	}
	ind, comma := string(m[1]), string(m[2])
	repl := "<<<<<<< HEAD\n" + ind + `"body": ` + ours + comma + "\n=======\n" + ind + `"body": ` + theirs + comma + "\n>>>>>>> feat"
	if err := os.WriteFile(path, bodyLine.ReplaceAll(b, []byte(repl)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// markField writes a marker block into the JSON body string (the merge
// driver's shape: the file stays JSON).
func markField(t *testing.T, path, ours, theirs string) {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(mustRead(t, path), &doc); err != nil {
		t.Fatal(err)
	}
	// Written as lore's merge driver writes it (canonical JSON: "<" and ">"
	// as themselves, not \u003c escapes).
	var v bytes.Buffer
	enc := json.NewEncoder(&v)
	enc.SetEscapeHTML(false)
	if err := enc.Encode("<<<<<<< ours\n" + ours + "\n=======\n" + theirs + "\n>>>>>>> theirs"); err != nil {
		t.Fatal(err)
	}
	doc["body"] = json.RawMessage(bytes.TrimSpace(v.Bytes()))
	var out bytes.Buffer
	enc = json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// showMemBody is the body memory.show answers ("" when it fails).
func showMemBody(t *testing.T, a *App, id string) string {
	t.Helper()
	out, err := run(t, a, opsmemory.OpShow, `{"id":"`+id+`"}`, false)
	if err != nil {
		return ""
	}
	var v struct {
		Data struct {
			Body string `json:"body"`
		} `json:"data"`
		Body string `json:"body"`
	}
	if err = json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("show: %s %v", out, err)
	}
	if v.Data.Body != "" {
		return v.Data.Body
	}
	return v.Body
}

// TestSyncConflictsMarkedFiles (G-SYNC-2): both marked shapes are listed
// after the host's live conflicts, counted in status, settled by resolve
// with either side, and readable at once afterwards.
func TestSyncConflictsMarkedFiles(t *testing.T) {
	// Heavy on SQLite: its own process keeps it off this process's
	// allocator lock (see package isolate).
	if isolate.Run(t) {
		return
	}
	cases := []struct {
		name     string
		mark     func(t *testing.T, path string)
		take     string
		wantBody string
	}{
		{"git marker lines, kept", func(t *testing.T, p string) { markLines(t, p, `"mine"`, `"theirs"`) }, "kept", "mine"},
		{"git marker lines, other", func(t *testing.T, p string) { markLines(t, p, `"mine"`, `"theirs"`) }, "other", "theirs"},
		{"json field block, kept", func(t *testing.T, p string) { markField(t, p, "mine", "theirs") }, "kept", "mine"},
		{"json field block, other", func(t *testing.T, p string) { markField(t, p, "mine", "theirs") }, "other", "theirs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var notes []string
			var hostGot string
			a, repo := coreHostApp(t, coreStub{live: 1, resolved: &hostGot}, &notes)
			id := addMemory(t, a, "base")
			c.mark(t, memPath(repo, id))
			b := openWith(t, repo, Options{Sync: coreStub{live: 1, resolved: &hostGot}, Notice: func(string) {},
				RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
			env := b.syncEnv()
			list, err := env.SyncConflicts(background(), opssync.ConflictsIn{})
			if err != nil || list.Count != 2 || list.Data[1].RowID != id || list.Data[1].ID != 2 {
				t.Fatalf("conflicts = %+v %v", list, err)
			}
			if !strings.Contains(list.Lines[1], "lore sync resolve --id 2 --take kept|other") || !strings.Contains(list.Lines[1], ".lore/data/memories/"+id+".json") {
				t.Fatalf("line %q", list.Lines[1])
			}
			st, err := env.SyncStatus(background(), opssync.StatusIn{})
			if err != nil || st.Data.OpenConflicts != 1 || !slicesHas(st.Lines, "conflicted files: 1") {
				t.Fatalf("status = %+v %v", st, err)
			}
			if _, err = env.SyncResolve(background(), opssync.ResolveIn{ID: "1", Take: "kept"}); err != nil || hostGot != "1" {
				t.Fatalf("#1 is the host's: %q %v", hostGot, err)
			}
			done, err := env.SyncResolve(background(), opssync.ResolveIn{ID: "2", Take: c.take})
			if err != nil || !strings.Contains(done.Done, "commit .lore/data/memories/"+id+".json") {
				t.Fatalf("resolve 2 = %+v %v", done, err)
			}
			if got := showMemBody(t, b, id); got != c.wantBody {
				t.Fatalf("body after resolve %q, want %q", got, c.wantBody)
			}
			if after, _ := env.SyncConflicts(background(), opssync.ConflictsIn{}); after.Count != 1 {
				t.Fatalf("the settled file is still listed: %+v", after)
			}
		})
	}
}

// slicesHas reports a line equal to want.
func slicesHas(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// TestSyncResolveMarkedFileRefusals: a side that is not valid JSON, a
// bad --take, a number past the list: refused, the file unchanged.
func TestSyncResolveMarkedFileRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, n, take string
		mark          func(t *testing.T, path string)
		code          errs.Code
	}{
		{"side is not valid JSON", "1", "other", func(t *testing.T, p string) { markLines(t, p, `"mine"`, `theirs unquoted`) }, errs.InvalidInput},
		{"bad take", "1", "both", func(t *testing.T, p string) { markLines(t, p, `"a"`, `"b"`) }, errs.InvalidInput},
		{"past the list", "2", "kept", func(t *testing.T, p string) { markLines(t, p, `"a"`, `"b"`) }, errs.NotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var notes []string
			a, repo := coreHostApp(t, coreStub{}, &notes)
			id := addMemory(t, a, "base")
			p := memPath(repo, id)
			c.mark(t, p)
			before := string(mustRead(t, p))
			_, err := a.syncEnv().SyncResolve(background(), opssync.ResolveIn{ID: c.n, Take: c.take})
			if !errs.Has(err, c.code) {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
			if string(mustRead(t, p)) != before {
				t.Fatal("a refused resolve wrote the file")
			}
		})
	}
}

// TestSyncConflictsMarkedFilesLiveOff: a host with no live rows here
// (E_UNSUPPORTED) still lists and resolves the marked files, numbered
// from 1.
func TestSyncConflictsMarkedFilesLiveOff(t *testing.T) {
	t.Parallel()
	var notes []string
	a, repo := coreHostApp(t, coreStub{unsupported: true}, &notes)
	id := addMemory(t, a, "base")
	markLines(t, memPath(repo, id), `"mine"`, `"theirs"`)
	env := a.syncEnv()
	list, err := env.SyncConflicts(background(), opssync.ConflictsIn{})
	if err != nil || list.Count != 1 || list.Data[0].ID != 1 {
		t.Fatalf("conflicts = %+v %v", list, err)
	}
	if _, err = env.SyncResolve(background(), opssync.ResolveIn{ID: "1", Take: "other"}); err != nil {
		t.Fatal(err)
	}
	if got := showMemBody(t, a, id); got != "theirs" {
		t.Fatalf("body %q", got)
	}
}

// TestSyncRunCoreStoresLines: "· already in sync" only when the host
// printed nothing, no store reported and .lore/data is committed; an attached store prints its
// line instead; a host that does not leave stores to the core is untouched.
func TestSyncRunCoreStoresLines(t *testing.T) {
	t.Parallel()
	var notes []string
	a, aRepo := coreHostApp(t, coreStub{}, &notes)
	// E-10: the project row the open wrote is not committed: the line names
	// it and the commit instead of a bare "already in sync"
	out, err := a.syncEnv().SyncRun(background(), opssync.RunIn{})
	if err != nil || len(out.Lines) != 1 || !strings.HasPrefix(out.Lines[0], "· in sync; 1 file(s) in .lore/data wait for your commit (.lore/data/projects/") ||
		!strings.HasSuffix(out.Lines[0], "git add .lore/data && git commit") {
		t.Fatalf("uncommitted row file = %+v %v", out, err)
	}
	gitT(t, aRepo, "add", ".lore/data")
	gitT(t, aRepo, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-q", "-m", "lore data")
	out, err = a.syncEnv().SyncRun(background(), opssync.RunIn{})
	if err != nil || len(out.Lines) != 1 || out.Lines[0] != syncAlreadyInSync || out.Data.Pass != "live" || out.Data.Stores != nil {
		t.Fatalf("nothing to do = %+v %v", out, err)
	}
	b, _ := coreHostApp(t, coreStub{lines: []string{"landed 1 row"}}, &notes)
	if out, err = b.syncEnv().SyncRun(background(), opssync.RunIn{}); err != nil || len(out.Lines) != 1 || out.Lines[0] != "landed 1 row" {
		t.Fatalf("host lines = %+v %v", out, err)
	}
	repo := gitRepo(t)
	kb := folderStoreAt(t, "sto_0e45506e64357c94a87e70ef1c6b0001", "internal")
	writeStoresYAML(t, repo, "stores:\n  - name: kb\n    url: "+kb+"\n")
	c := openWith(t, repo, Options{Sync: coreStub{}, RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, c)
	if out, err = c.syncEnv().SyncRun(background(), opssync.RunIn{}); err != nil || len(out.Data.Stores) != 1 || out.Data.Stores[0].Store != "kb" ||
		len(out.Lines) != 1 || !strings.HasPrefix(out.Lines[0], "store kb: sent ") {
		t.Fatalf("with a store = %+v %v", out, err)
	}
	// A host without CoreStores (the old binary's) keeps its own answer:
	// no store pass, no added line.
	plain := syncRows{Env: stubSync{Env: coreStub{}}, app: c}
	if _, isCore := plain.Env.(opssync.CoreStores); isCore {
		t.Fatal("fixture: stubSync must not implement CoreStores")
	}
	if out, err = plain.SyncRun(background(), opssync.RunIn{}); err != nil || len(out.Lines) != 0 || out.Data.Stores != nil {
		t.Fatalf("host without CoreStores = %+v %v", out, err)
	}
}

// TestSyncPurgePointer (FA-DEL-4): the old spelling prints its pointer and
// answers as `lore trash purge --archived-before` (confirm, dry run, purge).
func TestSyncPurgePointer(t *testing.T) {
	t.Parallel()
	var notes []string
	a, repo := coreHostApp(t, coreStub{}, &notes)
	id := addMemory(t, a, "old note")
	if _, err := run(t, a, opsmemory.OpArchive, `{"id":"`+id+`"}`, true); err != nil {
		t.Fatal(err)
	}
	env := a.syncEnv()
	if _, err := env.SyncPurge(background(), opssync.PurgeIn{}); !errs.Has(err, errs.InvalidInput) {
		t.Fatalf("no cutoff = %v", err)
	}
	if len(notes) != 1 || notes[0] != syncPurgePointer {
		t.Fatalf("pointer = %q", notes)
	}
	if _, err := env.SyncPurge(background(), opssync.PurgeIn{ArchivedBefore: "2999-01-01"}); !errs.Has(err, errs.NeedsConfirm) {
		t.Fatalf("no confirm = %v", err)
	}
	dry, err := env.SyncPurge(background(), opssync.PurgeIn{ArchivedBefore: "2999-01-01", DryRun: true})
	if err != nil || !strings.Contains(dry.Done, id) {
		t.Fatalf("dry run = %+v %v", dry, err)
	}
	if b := mustRead(t, memPath(repo, id)); !strings.Contains(string(b), "old note") {
		t.Fatal("the dry run purged")
	}
	done, err := env.SyncPurge(background(), opssync.PurgeIn{ArchivedBefore: "2999-01-01", Confirm: true})
	if err != nil || !strings.Contains(done.Done, id) {
		t.Fatalf("purge = %+v %v", done, err)
	}
	if b := mustRead(t, memPath(repo, id)); strings.Contains(string(b), "old note") {
		t.Fatal("the purged row kept its content")
	}
}

// TestSyncHandMadeMarkersListed: a row file the reader leaves out as a
// merge conflict although its markers are not git's exact shape (an
// indented start line, as a hand edit leaves it) is in `sync conflicts`
// and in the sync report's conflicts, so the reader's hint "lore sync
// conflicts lists it" and sync --json agree (run:tests:q-sync-json-
// conflicts); resolve refuses it with the fix-the-file next step.
func TestSyncHandMadeMarkersListed(t *testing.T) {
	t.Parallel()
	var notes []string
	a, repo := coreHostApp(t, coreStub{unsupported: true}, &notes)
	id := addMemory(t, a, "base")
	p := memPath(repo, id)
	b := mustRead(t, p)
	m := bodyLine.FindSubmatch(b)
	if m == nil {
		t.Fatalf("fixture: no body line in %s", b)
	}
	ind, comma := string(m[1]), string(m[2])
	repl := ind + "<<<<<<< HEAD\n" + ind + `"body": "mine"` + comma + "\n=======\n" + ind + `"body": "theirs"` + comma + "\n>>>>>>> feat"
	if err := os.WriteFile(p, bodyLine.ReplaceAll(b, []byte(repl)), 0o600); err != nil {
		t.Fatal(err)
	}
	c := openWith(t, repo, Options{Sync: coreStub{unsupported: true}, Notice: func(string) {},
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	env := c.syncEnv()
	list, err := env.SyncConflicts(background(), opssync.ConflictsIn{})
	if err != nil || list.Count != 1 || list.Data[0].RowID != id {
		t.Fatalf("conflicts = %+v %v", list, err)
	}
	out, err := env.SyncRun(background(), opssync.RunIn{})
	if len(out.Data.Conflicts) != 1 || !strings.Contains(out.Data.Conflicts[0].Path, id) {
		t.Fatalf("sync report conflicts = %+v %v", out.Data.Conflicts, err)
	}
	// show never prints the marked body as if it were the row.
	for _, git := range []bool{false, true} {
		if git {
			if err := os.WriteFile(p, b, 0o600); err != nil { // back to the base file
				t.Fatal(err)
			}
			markLines(t, p, `"mine"`, `"theirs"`) // git's exact shape
		}
		if got := showMemBody(t, c, id); got != "" {
			t.Fatalf("show of a marked file (git shape %v) printed %q", git, got)
		}
	}
}
