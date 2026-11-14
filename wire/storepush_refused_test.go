package wire

// storepush_refused_test.go — refused store pushes (storepush_refused.go):
// recorded on every inner Sync that came back with refusals (read fetch,
// push after a write, lore sync), cleared by a sync that reached the
// remote with none, listed by App.StorePushRefusals; and storePush.Sync
// reporting what the command's earlier syncs already sent.

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsstores "github.com/thesatellite-ai/lore/ops/stores"
	"github.com/thesatellite-ai/lore/store"
)

// refusedIDs are two memory ids, joined at run time (convention 8).
func refusedIDs(t *testing.T) (ids.AnyID, ids.AnyID) {
	t.Helper()
	a, err := ids.ParseAny("mem_" + "01a1a6841fc0784d" + "92a62b0acf14ab01")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ids.ParseAny("mem_" + "01a1a6841fc0784d" + "92a62b0acf14ab02")
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

// refusalsAt lists the records under home.
func refusalsAt(t *testing.T, home string) []StorePushRefusal {
	t.Helper()
	got, err := (&App{opts: Options{Home: home}}).StorePushRefusals(background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestStorePushRefusalRecordAndClear: the record follows the last sync's
// verdict, whichever path ran it.
func TestStorePushRefusalRecordAndClear(t *testing.T) {
	t.Parallel()
	m1, m2 := refusedIDs(t)
	refused := store.SyncReport{Refused: []store.Refusal{
		{ID: m2, Code: errs.PolicyDenied, Reason: "the remote refused the push: hook declined"},
		{ID: m1, Code: errs.PolicyDenied, Reason: "the remote refused the push: hook declined"},
		{ID: m1, Code: errs.PolicyDenied, Reason: "the remote refused the push: hook declined"},
	}}
	whole := store.SyncReport{Refused: []store.Refusal{{Code: errs.PolicyDenied, Reason: "the remote refused the push: protected"}}}
	cases := []struct {
		name     string
		steps    []store.SyncReport // inner Sync answers, in order
		errs     []error
		path     string // "write", "read", "sync"
		wantRows []string
		want     bool // a record remains
	}{
		{name: "refused on push after write", steps: []store.SyncReport{refused}, path: "write", want: true, wantRows: []string{m1.String(), m2.String()}},
		{name: "refused on lore sync", steps: []store.SyncReport{refused}, path: "sync", want: true, wantRows: []string{m1.String(), m2.String()}},
		{name: "refused on read fetch", steps: []store.SyncReport{refused}, path: "read", want: true, wantRows: []string{m1.String(), m2.String()}},
		{name: "whole push (no ids)", steps: []store.SyncReport{whole}, path: "sync", want: true},
		{name: "refused then landed: cleared", steps: []store.SyncReport{refused, {Sent: 2}}, path: "sync", want: false},
		{name: "refused then nothing to send: cleared", steps: []store.SyncReport{refused, {}}, path: "sync", want: false},
		{name: "refused then offline: kept", steps: []store.SyncReport{refused, {Offline: true}}, path: "sync", want: true, wantRows: []string{m1.String(), m2.String()}},
		{name: "refused then error: kept", steps: []store.SyncReport{refused, {}}, errs: []error{nil, errors.New("boom")}, path: "sync", want: true, wantRows: []string{m1.String(), m2.String()}},
		{name: "refused, later refusal replaces", steps: []store.SyncReport{refused, whole}, path: "sync", want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			res := pushRes(t)
			f := &fakePushBackend{caps: store.Capabilities{Remote: true}}
			for i, rep := range c.steps {
				f.rep, f.syncErr = rep, nil
				if i < len(c.errs) {
					f.syncErr = c.errs[i]
				}
				b := &storePush{inner: f, name: "kb", res: res, notes: &bytes.Buffer{}, push: nowPush()} // one command each
				var err error
				switch c.path {
				case "write":
					err = b.Write(background(), nil)
				case "read":
					_, err = b.Get(background(), "memories", m1)
					if errors.Is(err, store.ErrNotFound) {
						err = nil
					}
				default:
					_, err = b.Sync(background())
					if f.syncErr != nil {
						err = nil // the sync error is the command's; the record is what is checked
					}
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			got := refusalsAt(t, res.home)
			if (len(got) == 1) != c.want || len(got) > 1 {
				t.Fatalf("records = %+v, want one: %v", got, c.want)
			}
			if !c.want {
				return
			}
			r := got[0]
			last := c.steps[len(c.steps)-1]
			if c.want && len(last.Refused) == 0 {
				last = c.steps[0] // offline / error kept the first record
			}
			if r.Store != "kb" || r.Code != string(errs.PolicyDenied) || r.Reason != last.Refused[0].Reason {
				t.Fatalf("record %+v", r)
			}
			if !slices.Equal(r.Rows, c.wantRows) {
				t.Fatalf("rows %v, want %v (sorted, de-duplicated)", r.Rows, c.wantRows)
			}
			if _, perr := time.Parse(time.RFC3339, r.At); perr != nil {
				t.Fatalf("at %q: %v", r.At, perr)
			}
		})
	}
}

// TestStorePushRefusalNeverCreatesLocalDB: a clear (a push that landed)
// and the listing never create local.db on a machine that has none.
func TestStorePushRefusalNeverCreatesLocalDB(t *testing.T) {
	t.Parallel()
	res := pushRes(t)
	f := &fakePushBackend{caps: store.Capabilities{Remote: true}, rep: store.SyncReport{Sent: 1}}
	b := &storePush{inner: f, name: "kb", res: res, notes: &bytes.Buffer{}, push: nowPush()}
	if _, err := b.Sync(background()); err != nil {
		t.Fatal(err)
	}
	if got := refusalsAt(t, res.home); len(got) != 0 {
		t.Fatalf("records %+v", got)
	}
	if _, err := os.Stat(LocalStatePath(res.home)); !os.IsNotExist(err) {
		t.Fatalf("local.db was created (%v)", err)
	}
}

// TestStorePushRefusalsSorted: two stores list by name.
func TestStorePushRefusalsSorted(t *testing.T) {
	t.Parallel()
	res := pushRes(t)
	rep := store.SyncReport{Refused: []store.Refusal{{Code: errs.PolicyDenied, Reason: "no"}}}
	for _, name := range []store.Source{"zeta", "alpha"} {
		b := &storePush{inner: &fakePushBackend{caps: store.Capabilities{Remote: true}, rep: rep}, name: name, res: res, notes: &bytes.Buffer{}, push: nowPush()}
		if _, err := b.Sync(background()); err != nil {
			t.Fatal(err)
		}
	}
	got := refusalsAt(t, res.home)
	if len(got) != 2 || got[0].Store != "alpha" || got[1].Store != "zeta" {
		t.Fatalf("records %+v", got)
	}
}

// TestStorePushSyncCountsEarlierSends: the rows a command's read fetch or
// push after a write already sent are in the next Sync's report once
// (lore sync's "sent N" line; it said 0 when the first read had pushed).
func TestStorePushSyncCountsEarlierSends(t *testing.T) {
	t.Parallel()
	m1, _ := refusedIDs(t)
	f := &fakePushBackend{caps: store.Capabilities{Remote: true}, rep: store.SyncReport{Sent: 3, Received: 1, Merged: []ids.AnyID{m1}}}
	b := &storePush{inner: f, name: "kb", res: pushRes(t), notes: &bytes.Buffer{}, push: nowPush()}
	if _, err := b.Get(background(), "memories", m1); err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	f.rep = store.SyncReport{Sent: 1}
	rep, err := b.Sync(background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sent != 4 || rep.Received != 1 || len(rep.Merged) != 1 {
		t.Fatalf("first Sync = %+v, want sent 4 received 1 merged 1", rep)
	}
	f.rep = store.SyncReport{}
	if rep, err = b.Sync(background()); err != nil || rep.Sent != 0 || rep.Received != 0 || len(rep.Merged) != 0 {
		t.Fatalf("carried counts must be reported once: %+v %v", rep, err)
	}
	// A failed earlier sync carries nothing.
	f2 := &fakePushBackend{caps: store.Capabilities{Remote: true}, rep: store.SyncReport{Sent: 5}, syncErr: errors.New("boom")}
	b2 := &storePush{inner: f2, name: "kb", res: pushRes(t), notes: &bytes.Buffer{}, push: nowPush()}
	_, _ = b2.Get(background(), "memories", m1)
	f2.rep, f2.syncErr = store.SyncReport{}, nil
	if rep, err = b2.Sync(background()); err != nil || rep.Sent != 0 {
		t.Fatalf("an errored sync must carry nothing: %+v %v", rep, err)
	}
}

// TestStorePushRefusalHeld (owner decision 7): one refused row of the
// store folder "platform" stops the clone's push for every folder. The
// folder that holds the row records the refusal; another folder records
// only a held marker naming that folder and its rows, is never cleared
// while the refusal stands, and the status names the holder by its
// attached name.
func TestStorePushRefusalHeld(t *testing.T) {
	t.Parallel()
	m1, _ := refusedIDs(t)
	res := pushRes(t)
	conflict := store.Refusal{ID: m1, Code: errs.Stale, Reason: "both sides changed the row", Folder: "platform"}
	held := conflict
	held.Held = true
	sync := func(name store.Source, r store.Refusal) {
		t.Helper()
		f := &fakePushBackend{caps: store.Capabilities{Remote: true}, rep: store.SyncReport{Refused: []store.Refusal{r}}}
		b := &storePush{inner: f, name: name, res: res, notes: &bytes.Buffer{}, push: nowPush()}
		if _, err := b.Sync(background()); err != nil {
			t.Fatal(err)
		}
	}
	sync("team/platform", conflict)
	sync("team/org", held)
	sync("team/org", held) // still held: the marker stays
	got := refusalsAt(t, res.home)
	if len(got) != 2 {
		t.Fatalf("records %+v", got)
	}
	org, platform := got[0], got[1]
	if platform.Store != "team/platform" || platform.HeldBy != "" || !slices.Equal(platform.Rows, []string{m1.String()}) {
		t.Fatalf("the row's folder must record the refusal: %+v", platform)
	}
	if org.Store != "team/org" || org.HeldBy != "platform" || !slices.Equal(org.Rows, []string{m1.String()}) || org.Code != string(errs.Stale) {
		t.Fatalf("another folder must record a held marker: %+v", org)
	}
	own, heldRecs := splitHeldRecords(got)
	if len(own) != 1 || len(heldRecs) != 1 {
		t.Fatalf("split %+v / %+v", own, heldRecs)
	}
	if by := opsstores.HeldNames(heldOn(org, own)); by != "team/platform" {
		t.Fatalf("held by %q, want the attached name of the row's folder", by)
	}
	if by := opsstores.HeldNames(heldOn(org, nil)); by != "folder platform" {
		t.Fatalf("held by %q with no refusal naming the row", by)
	}
}

// TestStorePushRefusalHeldByTwo: rows of two other folders ("platform" and
// "web") both refused: the held folder's record, its status line, its
// next step and its store doctor problem name both folders and their
// rows, never only the first.
func TestStorePushRefusalHeldByTwo(t *testing.T) {
	t.Parallel()
	m1, m2 := refusedIDs(t)
	res := pushRes(t)
	a := store.Refusal{ID: m1, Code: errs.Stale, Reason: "both sides changed the row", Folder: "platform", Held: true}
	b := store.Refusal{ID: m2, Code: errs.PolicyDenied, Reason: "pre-receive hook declined", Folder: "web", Held: true}
	f := &fakePushBackend{caps: store.Capabilities{Remote: true}, rep: store.SyncReport{Refused: []store.Refusal{b, a}}}
	bp := &storePush{inner: f, name: "team/org", res: res, notes: &bytes.Buffer{}, push: nowPush()}
	if _, err := bp.Sync(background()); err != nil {
		t.Fatal(err)
	}
	got := refusalsAt(t, res.home)
	if len(got) != 1 || got[0].HeldBy != "platform" || len(got[0].HeldOn) != 2 {
		t.Fatalf("record %+v, want held by platform and web", got)
	}
	own := []StorePushRefusal{{Store: "team/platform", Rows: []string{m1.String()}}}
	on := heldOn(got[0], own)
	line := opsstores.HeldLines(on)
	want := "held: waiting on team/platform's conflict (" + m1.String() + ") and folder web's refused push (" + m2.String() + ")"
	if line != want {
		t.Fatalf("held line\n got %q\nwant %q", line, want)
	}
	if next := heldNext(on); !strings.Contains(next, "settle the rows in team/platform and folder web") {
		t.Fatalf("next step %q names one folder only", next)
	}
	if by := opsstores.HeldNames(on); by != "team/platform, folder web" {
		t.Fatalf("held_by %q", by)
	}
}
