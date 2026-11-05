package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/backend/folder"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// purgeOnDeletePolicy sets trash.purge_on_delete for every store.
func purgeOnDeletePolicy(t *testing.T, on bool) Option {
	t.Helper()
	set, err := policy.Resolve([]policy.Entry{{Level: policy.LevelRepo, Key: policy.KeyTrashPurgeOnDelete, Value: value.BoolOf(on)}})
	if err != nil {
		t.Fatal(err)
	}
	return WithSettings(fixedSettings{set: set})
}

// purgeFixtureWith is purgeFixture with extra engine options.
func purgeFixtureWith(t *testing.T, extra ...Option) *fixture {
	t.Helper()
	dir := t.TempDir()
	sid, err := ids.New[ids.Store]()
	if err != nil {
		t.Fatal(err)
	}
	if ierr := folder.Init(dir, rowfs.Marker{Version: rowfs.MarkerVersion, ID: sid, Visibility: rowfs.VisibilityInternal, Created: "2026-11-05"}); ierr != nil {
		t.Fatal(ierr)
	}
	b, err := folder.Open(dir, rowfs.FolderRoot, store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() }) // test teardown
	return setup(t, append([]Option{WithStore(Attached{Name: kbStore, Backend: b, KeyNamespace: sid.String()})}, extra...)...)
}

// delIn names the row a test delete removes.
type delIn struct {
	ID string `arg:"0" json:"id"`
}

// delOut is a test delete's result.
type delOut struct {
	// Notes are the write's warnings as "code field: message" lines.
	Notes []string `json:"notes"`
}

// deleteOp deletes in.ID with pr and reason riding on the context, the
// way a noun's delete passes --purge, --confirm and --reason.
func deleteOp(f *fixture, pr *PurgeRequest, reason string) *op.Op[delIn, delOut] {
	return op.Define(op.Spec{Name: "memory.delete", Summary: "delete", Permission: op.PermWrite, Access: op.AccessWrite,
		Errors: []errs.Code{errs.NeedsConfirm}},
		func(ctx context.Context, in delIn) (delOut, error) {
			id, err := ids.ParseAny(in.ID)
			if err != nil {
				return delOut{}, err
			}
			w, err := f.eng.Delete(WithDeleteReason(WithPurge(ctx, pr), reason), Ref{ID: id})
			out := delOut{Notes: []string{}}
			for _, wn := range w.Warnings {
				out.Notes = append(out.Notes, string(wn.Code)+" "+wn.Field+": "+wn.Message)
			}
			return out, err
		})
}

// TestDeletePurgeMatrix (DELETION 2.5, 3.5, 3.12, DQ2, DQ8): a delete is
// a purge when --purge is asked or policy trash.purge_on_delete is on;
// then only the literal --confirm purges (--yes never does) and the
// question names the group's dependents; a purge fills Purged with every
// tombstoned id and warns W_PURGED_HISTORY (plus a second warning when the
// reason holds a secret); a refused purge changes nothing; without --purge
// and the policy a delete is soft (the row waits in the trash).
func TestDeletePurgeMatrix(t *testing.T) {
	type want struct {
		code     errs.Code // non-empty: refused with this code
		ask      []string  // words the refusal holds
		purged   int       // ids in Purged (0 = a soft delete)
		trashed  int       // rows left in the trash
		warnings int       // W_PURGED_HISTORY warnings
	}
	cases := []struct {
		name                    string
		group, asked, conf, yes bool
		policyOn                bool
		reason                  string
		want                    want
	}{
		{name: "plain delete is soft", yes: true, want: want{trashed: 1}},
		{name: "plain --confirm stays soft", conf: true, yes: true, want: want{trashed: 1}},
		{name: "--purge --yes asks for --confirm", asked: true, yes: true,
			want: want{code: errs.NeedsConfirm, ask: []string{"--purge removes", "for good"}}},
		{name: "--purge no flags asks", asked: true, want: want{code: errs.NeedsConfirm, ask: []string{"--purge removes"}}},
		{name: "--purge --confirm purges", asked: true, conf: true, want: want{purged: 1, warnings: 1}},
		{name: "--purge --confirm --yes purges", asked: true, conf: true, yes: true, want: want{purged: 1, warnings: 1}},
		{name: "group --purge asks with the dependent count", group: true, asked: true, yes: true,
			want: want{code: errs.NeedsConfirm, ask: []string{"and 2 dependent rows"}}},
		{name: "group --purge --confirm purges every row", group: true, asked: true, conf: true, want: want{purged: 3, warnings: 1}},
		{name: "group plain delete without --yes asks (soft)", group: true, want: want{code: errs.NeedsConfirm}},
		{name: "group plain delete --yes is soft", group: true, yes: true, want: want{trashed: 3}},
		{name: "policy on, --yes asks for --confirm", policyOn: true, yes: true,
			want: want{code: errs.NeedsConfirm, ask: []string{"policy trash.purge_on_delete"}}},
		{name: "policy on, --confirm purges", policyOn: true, conf: true, want: want{purged: 1, warnings: 1}},
		{name: "policy on, group --confirm", policyOn: true, group: true, conf: true, want: want{purged: 3, warnings: 1}},
		{name: "policy off explicitly, soft", yes: true, want: want{trashed: 1}},
		{name: "secret in the reason warns twice", asked: true, conf: true, reason: "rotated " + fakeKey,
			want: want{purged: 1, warnings: 2}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := purgeFixtureWith(t, purgeOnDeletePolicy(t, c.policyOn))
			ctx := context.Background()
			kbPrj, err := f.eng.Create(ctx, kbStore, schema.TableProjects, map[string]value.Value{"name": txt("kb-project")})
			if err != nil {
				t.Fatal(err)
			}
			inKB := func(table row.Table, fields map[string]value.Value) ids.AnyID {
				t.Helper()
				fields["project_id"] = txt(kbPrj.Row.ID.String())
				w, cerr := f.eng.Create(ctx, kbStore, table, fields)
				if cerr != nil {
					t.Fatal(cerr)
				}
				return w.Row.ID
			}
			var target ids.AnyID
			if c.group {
				list := inKB(schema.TableTasklists, map[string]value.Value{"title": txt("sprint"), "status_str": txt("open")})
				inKB(schema.TableTasks, f.taskFields("one", list))
				inKB(schema.TableTasks, f.taskFields("two", list))
				target = list
			} else {
				target = inKB(schema.TableMemories, f.memFields("old note"))
			}
			pr := &PurgeRequest{Asked: c.asked, Confirmed: c.conf}
			res, err := f.eng.Run(ctx, deleteOp(f, pr, c.reason), op.ArgsDecoder{Args: []string{target.String()}}, op.Caller{Actor: testActor, Yes: c.yes})
			trashed, terr := f.eng.Trashed(ctx)
			if terr != nil {
				t.Fatal(terr)
			}
			if c.want.code != "" {
				if !errs.Has(err, c.want.code) {
					t.Fatalf("err = %v, want %s", err, c.want.code)
				}
				for _, w := range c.want.ask {
					if !strings.Contains(err.Error(), w) {
						t.Fatalf("refusal %q does not say %q", err.Error(), w)
					}
				}
				if len(trashed) != 0 || len(pr.Purged) != 0 {
					t.Fatalf("a refused delete changed rows: %d trashed, %d purged", len(trashed), len(pr.Purged))
				}
				if _, _, gerr := f.eng.Get(ctx, Ref{ID: target}); gerr != nil {
					t.Fatalf("the row is gone after a refusal: %v", gerr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(pr.Purged) != c.want.purged || len(trashed) != c.want.trashed {
				t.Fatalf("purged %d, trashed %d; want %d, %d", len(pr.Purged), len(trashed), c.want.purged, c.want.trashed)
			}
			if c.want.purged > 0 && pr.Purged[0] != target {
				t.Fatalf("Purged[0] = %s, want the root %s", pr.Purged[0], target)
			}
			js, jerr := res.JSON()
			if jerr != nil {
				t.Fatal(jerr)
			}
			if got := strings.Count(string(js), string(WarnPurgedHistory)); got != c.want.warnings {
				t.Fatalf("W_PURGED_HISTORY warnings = %d, want %d: %s", got, c.want.warnings, js)
			}
			if strings.Contains(string(js), fakeKey) {
				t.Fatalf("a warning echoes the secret: %s", js)
			}
		})
	}
}

// TestDeletePurgeUnsupportedStoreWritesNothing: a confirmed --purge on a
// store that keeps no purged-ids list (no Purger) is refused E_UNSUPPORTED
// before anything is written: the row is neither trashed nor purged.
func TestDeletePurgeUnsupportedStoreWritesNothing(t *testing.T) {
	f := setup(t) // the repo store is a memstore: no Purger
	ctx := context.Background()
	mem := f.memory(t, "keep me")
	pr := &PurgeRequest{Asked: true, Confirmed: true}
	if _, err := f.eng.Run(ctx, deleteOp(f, pr, ""), op.ArgsDecoder{Args: []string{mem.ID.String()}}, op.Caller{Actor: testActor}); !errs.Has(err, errs.Unsupported) {
		t.Fatalf("err = %v, want E_UNSUPPORTED", err)
	}
	trashed, err := f.eng.Trashed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 0 || len(pr.Purged) != 0 {
		t.Fatalf("a refused purge wrote: %d trashed, %d purged", len(trashed), len(pr.Purged))
	}
	if _, _, err := f.eng.Get(ctx, Ref{ID: mem.ID}); err != nil {
		t.Fatalf("the row is gone: %v", err)
	}
}
