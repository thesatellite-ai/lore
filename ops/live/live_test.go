package live

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"
)

// fakeRef is an in-memory Ref recording what it was asked.
type fakeRef struct {
	listing  Listing
	file     map[string][]byte
	refs     Refs
	hist     History
	logN     int
	compact  Compacted
	dryRun   bool
	purged   []string
	purgeErr error
}

func (f *fakeRef) List(context.Context) (Listing, error) { return f.listing, nil }
func (f *fakeRef) File(_ context.Context, id string) ([]byte, error) {
	if b, ok := f.file[id]; ok {
		return b, nil
	}
	return nil, errs.New(errs.NotFound, id+" is not live")
}
func (f *fakeRef) Refs(context.Context) (Refs, error) { return f.refs, nil }
func (f *fakeRef) Log(_ context.Context, n int) (History, error) {
	f.logN = n
	return f.hist, nil
}
func (f *fakeRef) Restore(context.Context) (Restored, error) {
	return Restored{Rows: 3, LeftOut: 2}, nil
}
func (f *fakeRef) Compact(_ context.Context, dry bool) (Compacted, error) {
	f.dryRun = dry
	return f.compact, nil
}
func (f *fakeRef) Purge(_ context.Context, ids []string) error {
	f.purged = ids
	return f.purgeErr
}

// env hands out the fake, or err when set.
type env struct {
	r   *fakeRef
	err error
}

func (e env) LiveRef(context.Context) (Ref, error) {
	if e.err != nil {
		return nil, e.err
	}
	return e.r, nil
}

// TestTexts pins v0.1.17's human lines of every verb (the old binary's
// output is the reference where the design is silent).
func TestTexts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	since := time.Date(2026, 11, 5, 13, 14, 0, 0, time.UTC)
	f := &fakeRef{
		listing: Listing{Folder: "_root", Epoch: 2, Rows: []Row{{Table: "tasks", ID: "tsk_1", State: StateTombstone, Since: since}}},
		file:    map[string][]byte{"tsk_1": []byte("{}\n")},
		refs:    Refs{Local: "abc"},
		hist:    History{Epoch: 2, Lines: "abc 2026-11-05 t  lore: live 1 row (_root)\n"},
		compact: Compacted{Plan: CompactPlan{Ref: "refs/lore/live", Commits: 4, Threshold: 1000, Files: 3, FromEpoch: 1, ToEpoch: 2, Purged: 1}, Backup: "refs/lore-backup/live/x"},
	}
	e := env{r: f}
	s, err := show(ctx, e, ShowIn{})
	if err != nil {
		t.Fatal(err)
	}
	want := "tasks      tsk_1                                    tombstone since 2026-11-05 13:14\n1 live rows in _root (epoch 2)\n"
	if s.Text() != want {
		t.Fatalf("show:\n%q\nwant\n%q", s.Text(), want)
	}
	if s, err = show(ctx, e, ShowIn{ID: "tsk_1"}); err != nil || s.Text() != "{}\n" {
		t.Fatalf("show id: %q %v", s.Text(), err)
	}
	if _, err = show(ctx, e, ShowIn{ID: "tsk_2"}); errs.CodeOf(err) != errs.NotFound {
		t.Fatalf("show unknown id: %v", err)
	}
	d, _ := diff(ctx, e)
	if d.Text() != "local     abc\ntracking  (none)\nseen      (none)\npending   0 write(s) not confirmed by the remote\n" || d.Pending == nil {
		t.Fatalf("diff: %q", d.Text())
	}
	l, _ := logOf(ctx, e, LogIn{})
	if f.logN != DefaultLogLimit || !strings.HasPrefix(l.Text(), "epoch 2\nabc ") {
		t.Fatalf("log: n=%d %q", f.logN, l.Text())
	}
	if _, err = logOf(ctx, e, LogIn{Number: -1}); errs.CodeOf(err) != errs.InvalidInput {
		t.Fatalf("log -1: %v", err)
	}
	r, _ := restore(ctx, e)
	if r.Text() != "✓ restored refs/lore/live: 3 rows (left out 2 already landed or purged)\n" {
		t.Fatalf("restore: %q", r.Text())
	}
	c, _ := compact(ctx, e, CompactIn{DryRun: true})
	if !f.dryRun || c.Text() != "refs/lore/live has 4 commits (threshold 1000); compacting would keep 3 files, epoch 1 → 2, dropping 1 purged rows\n" {
		t.Fatalf("compact dry: %q", c.Text())
	}
	c, _ = compact(ctx, e, CompactIn{})
	if c.Text() != "✓ compacted refs/lore/live to 1 commit (epoch 2); backup kept locally as refs/lore-backup/live/x\n" {
		t.Fatalf("compact: %q", c.Text())
	}
	f.compact = Compacted{Nothing: true, Plan: CompactPlan{Ref: "refs/lore/live", Commits: 1, Files: 4, FromEpoch: 3, ToEpoch: 3}}
	for _, dry := range []bool{false, true} {
		if c, _ = compact(ctx, e, CompactIn{DryRun: dry}); c.Text() != "nothing to compact: refs/lore/live is already 1 commit with no purged rows (epoch 3, unchanged)\n" {
			t.Fatalf("compact one commit (dry %v): %q", dry, c.Text())
		}
	}
	f.compact = Compacted{Nothing: true}
	for _, dry := range []bool{false, true} {
		if c, _ = compact(ctx, e, CompactIn{DryRun: dry}); c.Text() != "nothing to compact: no live ref yet\n" {
			t.Fatalf("compact nothing (dry %v): %q", dry, c.Text())
		}
	}
	p, _ := purge(ctx, e, PurgeIn{IDs: []string{" " + purgeIDA + " ", purgeIDB}, Confirm: true})
	if strings.Join(f.purged, ",") != purgeIDA+","+purgeIDB || p.Text() != "✓ purged "+purgeIDA+", "+purgeIDB+" from live; run `lore live compact` to drop them from the ref's history too\n" {
		t.Fatalf("purge: %v %q", f.purged, p.Text())
	}
}

// purgeIDA and purgeIDB are full, well-formed task ids (live purge
// refuses anything else).
const (
	purgeIDA = "tsk_01a1b5ff31737b6982135705f591389e"
	purgeIDB = "tsk_01a1b5ff36307e2f825eff6e4400328b"
)

// TestRefusals: no ids is E_INVALID_INPUT before the ref is asked; the
// env's refusal (outside a project, live off) and the ref's errors pass
// through unchanged.
func TestRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fakeRef{}
	if _, err := purge(ctx, env{r: f}, PurgeIn{IDs: []string{" "}}); errs.CodeOf(err) != errs.InvalidInput || f.purged != nil {
		t.Fatalf("purge no ids: %v %v", err, f.purged)
	}
	// INTEG-SYNC-6: a typo, a shortened display id or an unknown prefix is
	// refused before the ref is asked, even beside a valid id, so the
	// confirmation never claims a purge that matches no row.
	for _, bad := range []string{"x", "tsk_01a1…389e", "tsk_nope", "zzz_01a1b5ff31737b6982135705f591389e", purgeIDA + "0"} {
		_, err := purge(ctx, env{r: f}, PurgeIn{IDs: []string{purgeIDA, bad}})
		if errs.CodeOf(err) != errs.InvalidInput || f.purged != nil || !strings.Contains(err.Error(), "nothing purged") {
			t.Fatalf("purge %q: %v %v", bad, err, f.purged)
		}
	}
	root := errs.New(errs.NotProjectRoot, "not inside a lore project")
	for name, run := range map[string]func(Env) error{
		"show":    func(e Env) error { _, err := show(ctx, e, ShowIn{}); return err },
		"diff":    func(e Env) error { _, err := diff(ctx, e); return err },
		"log":     func(e Env) error { _, err := logOf(ctx, e, LogIn{}); return err },
		"restore": func(e Env) error { _, err := restore(ctx, e); return err },
		"compact": func(e Env) error { _, err := compact(ctx, e, CompactIn{}); return err },
		"purge":   func(e Env) error { _, err := purge(ctx, e, PurgeIn{IDs: []string{purgeIDA}}); return err },
	} {
		if err := run(env{err: root}); !errors.Is(err, root) && errs.CodeOf(err) != errs.NotProjectRoot {
			t.Fatalf("%s: %v", name, err)
		}
	}
	f.purgeErr = errs.New(errs.ReadOnly, "blocked")
	if _, err := purge(ctx, env{r: f}, PurgeIn{IDs: []string{purgeIDA}, Confirm: true}); errs.CodeOf(err) != errs.ReadOnly {
		t.Fatalf("purge blocked: %v", err)
	}
}

// TestIsOpAndRegistry: every defined operation is a live op (wire answers
// them before opening a store) and the registry accepts the specs.
func TestIsOpAndRegistry(t *testing.T) {
	t.Parallel()
	entries := Ops(env{r: &fakeRef{}})
	if len(entries) != 6 {
		t.Fatalf("%d ops", len(entries))
	}
	for _, e := range entries {
		if !IsOp(e.Spec().Name) {
			t.Fatalf("%s not IsOp", e.Spec().Name)
		}
	}
	if IsOp("task.add") {
		t.Fatal("task.add is not a live op")
	}
}

// TestPurgeConfirmAndDryRun: like `lore trash purge`, a purge without the
// literal --confirm asks (E_NEEDS_CONFIRM naming `--confirm`, never
// `--yes`) and purges nothing; --dry-run lists what would go, names the
// ids the live folder does not hold, and purges nothing either.
func TestPurgeConfirmAndDryRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := &fakeRef{listing: Listing{Rows: []Row{{Table: "tasks", ID: purgeIDA, State: StateLive}}}}
	_, err := purge(ctx, env{r: f}, PurgeIn{IDs: []string{purgeIDA}})
	var e *errs.Error
	if !errors.As(err, &e) || e.Code != errs.NeedsConfirm || f.purged != nil {
		t.Fatalf("purge without --confirm: %v, purged %v", err, f.purged)
	}
	if next := strings.Join(e.Next.Args, " "); next != "live purge "+purgeIDA+" --confirm" || e.Hint == "" || e.Question == "" {
		t.Fatalf("the refusal names the confirmed command, a hint and a question: next %q hint %q question %q", next, e.Hint, e.Question)
	}
	for _, confirm := range []bool{false, true} { // a dry run never purges, confirmed or not
		p, err := purge(ctx, env{r: f}, PurgeIn{IDs: []string{purgeIDA, purgeIDB}, DryRun: true, Confirm: confirm})
		if err != nil || f.purged != nil || !p.DryRun || strings.Join(p.Purged, ",") != purgeIDA+","+purgeIDB || strings.Join(p.NotLive, ",") != purgeIDB {
			t.Fatalf("dry run (confirm %v): %+v %v, purged %v", confirm, p, err, f.purged)
		}
		want := "would purge " + purgeIDA + ", " + purgeIDB + " from live for good (no clone can push them back)\n" +
			"  " + purgeIDB + " is not live now; the purge still lists it so no clone pushes it back\n" +
			"dry run: nothing written; re-run with --confirm to purge\n"
		if p.Text() != want {
			t.Fatalf("dry run text:\n%q\nwant\n%q", p.Text(), want)
		}
	}
}
