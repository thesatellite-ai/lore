package wire

// sync_store_exchange_test.go — `lore sync` on a core host (cmd/lore)
// delivers the commits an attached git store holds locally (written
// offline, or with autopush off: this App has no push wrapper) and runs
// the queued run time flush (TIME 11.9) after the live pass.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/timeentry"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	opssync "github.com/thesatellite-ai/lore/ops/sync"
	"github.com/thesatellite-ai/lore/store/localstate"
	"github.com/thesatellite-ai/lore/wire/internal/isolate"
)

// TestSyncRunPushesStoreCommits: a memory written to a git store sits in
// the local clone; lore sync sends it (the store line counts it, data
// .stores carries the report) and the remote branch then holds the row;
// a second sync sends nothing and prints the store line, not "already in
// sync".
func TestSyncRunPushesStoreCommits(t *testing.T) {
	if isolate.Run(t) {
		return
	}
	hosts := histGitWorld(t)
	kbURL, kbDir := bareStore(t, hosts, "acme/kb")
	repo := gitRepo(t)
	var notes []string
	a := openWith(t, repo, Options{Sync: coreStub{}, Notice: func(m string) { notes = append(notes, m) },
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }})
	withProject(t, a)
	if _, err := run(t, a, opsstores.OpInit, `{"name":"kb","url":"`+kbURL+`"}`, true); err != nil {
		t.Fatal(err)
	}
	a = openWith(t, repo, Options{Sync: coreStub{}, Notice: func(m string) { notes = append(notes, m) },
		RepoIsClient: func(context.Context) (bool, error) { return false, nil }}) // reopen: kb is attached now
	id := histAddMemory(t, a, "kb", "for the team")
	if tree := gitIn(t, kbDir, nil, "ls-tree", "-r", "--name-only", "main"); strings.Contains(tree, id) {
		t.Fatal("fixture: the write was pushed before lore sync")
	}
	out, err := a.syncEnv().SyncRun(background(), opssync.RunIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Data.Stores) != 1 || out.Data.Stores[0].Store != "kb" || out.Data.Stores[0].Sent < 1 || out.Data.Stores[0].Offline {
		t.Fatalf("store report %+v", out.Data.Stores)
	}
	if len(out.Lines) != 1 || !strings.HasPrefix(out.Lines[0], "store kb: sent ") || strings.HasPrefix(out.Lines[0], "store kb: sent 0") {
		t.Fatalf("lines %q", out.Lines)
	}
	if tree := gitIn(t, kbDir, nil, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(tree, id) {
		t.Fatalf("the remote does not hold %s after lore sync:\n%s", id, tree)
	}
	again, err := a.syncEnv().SyncRun(background(), opssync.RunIn{})
	if err != nil || len(again.Lines) != 1 || again.Lines[0] != "store kb: sent 0, received 0, merged 0, refused 0" {
		t.Fatalf("second sync = %q %v", again.Lines, err)
	}
}

// TestSyncRunFlushesTimeOutbox: lore sync runs the queued run time flush;
// a span whose task is not found stays queued and is reported once on
// stderr (FlushOutbox's Failed), never failing the sync.
func TestSyncRunFlushesTimeOutbox(t *testing.T) {
	t.Parallel()
	var notes []string
	a, _ := coreHostApp(t, coreStub{}, &notes)
	eid, err := ids.New[timeentry.Tag]()
	if err != nil {
		t.Fatal(err)
	}
	type queued struct { // ops/run outboxSpan's JSON
		TaskID string          `json:"task_id"`
		Entry  timeentry.Entry `json:"entry"`
	}
	span, err := json.Marshal(queued{TaskID: "tsk_" + "01a1a6841fc0784d" + "92a62b0acf14ab09",
		Entry: timeentry.Entry{ID: eid, Kind: timeentry.KindPerson, Source: timeentry.SourceRun, Seconds: 60}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := a.LocalState(background())
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Put(background(), localstate.TimeOutbox, eid.String(), span); err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := a.syncEnv().SyncRun(background(), opssync.RunIn{})
	if err != nil {
		t.Fatalf("a span that cannot be written must not fail the sync: %v", err)
	}
	found := false
	for _, n := range notes {
		found = found || strings.HasPrefix(n, "! queued run time for task ")
	}
	if !found {
		t.Fatalf("no flush report on stderr: %q (lines %q)", notes, out.Lines)
	}
	st, err = a.LocalState(background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	left, err := st.List(background(), localstate.TimeOutbox, "")
	if err != nil || len(left) != 1 {
		t.Fatalf("the span must stay queued: %d %v", len(left), err)
	}
}
