package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/meta"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// metaKeyRow registers a text key in the repo store with the given
// required_on list (JSON array text, "" for none) and returns its id.
func metaKeyRow(t *testing.T, f *fixture, name, requiredOn string) ids.ID[ids.MetaKey] {
	t.Helper()
	fields := map[string]value.Value{"name": txt(name), "scope": txt("repo"), "type": txt("text")}
	if requiredOn != "" {
		j, err := value.JSONOf([]byte(requiredOn))
		if err != nil {
			t.Fatal(err)
		}
		fields["required_on"] = j
	}
	w, err := f.eng.Create(context.Background(), "", schema.TableMetaKeys, fields)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ids.Parse[ids.MetaKey](w.Row.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// carryTerm is one carried term the way metalist.PlanCarry builds it:
// the key named by id, the old row's JSON value.
func carryTerm(t *testing.T, key ids.ID[ids.MetaKey], jsonValue string) meta.Term {
	t.Helper()
	term, err := meta.ParseWrite(key.String() + "=" + jsonValue)
	if err != nil {
		t.Fatal(err)
	}
	return term
}

// TestSupersedesCarryFillsRequired (OV30-CARRY-REQ, METADATA 3.19): a
// --supersedes add with its own --meta carries the old row's keys in
// BEFORE the required_on check, so a required key set only on the old
// row is satisfied by the copy; the add's own --meta wins over a carried
// value of the same key, and a carry that does not hold the required key
// still fails E_META_REQUIRED (nothing stored).
func TestSupersedesCarryFillsRequired(t *testing.T) {
	t.Parallel()
	type want struct {
		code   errs.Code         // non-empty: refused with this code
		meta   map[string]string // stored metadata (key → canonical JSON)
		filled int               // MetaCarry.Filled after the create
	}
	cases := []struct {
		name  string
		own   []metaArg // the add's own --meta flags
		carry func(client, team ids.ID[ids.MetaKey]) map[ids.ID[ids.MetaKey]]string
		want  want
	}{
		{name: "required only on old row is carried",
			own: []metaArg{{Text: "team=core"}},
			carry: func(c, _ ids.ID[ids.MetaKey]) map[ids.ID[ids.MetaKey]]string {
				return map[ids.ID[ids.MetaKey]]string{c: `"acme"`}
			},
			want: want{meta: map[string]string{"client": quoted("acme"), "team": quoted("core")}, filled: 1}},
		{name: "own --meta wins over the carried value",
			own: []metaArg{{Text: "client=globex"}},
			carry: func(c, _ ids.ID[ids.MetaKey]) map[ids.ID[ids.MetaKey]]string {
				return map[ids.ID[ids.MetaKey]]string{c: `"acme"`}
			},
			want: want{meta: map[string]string{"client": quoted("globex")}, filled: 0}},
		{name: "own wins on one key, the other key is carried",
			own: []metaArg{{Text: "team=infra"}},
			carry: func(c, tm ids.ID[ids.MetaKey]) map[ids.ID[ids.MetaKey]]string {
				return map[ids.ID[ids.MetaKey]]string{c: `"acme"`, tm: `"core"`}
			},
			want: want{meta: map[string]string{"client": quoted("acme"), "team": quoted("infra")}, filled: 1}},
		{name: "carry without the required key is refused",
			own: []metaArg{{Text: "team=core"}},
			carry: func(_, tm ids.ID[ids.MetaKey]) map[ids.ID[ids.MetaKey]]string {
				return map[ids.ID[ids.MetaKey]]string{tm: `"ops"`}
			},
			want: want{code: errs.MetaRequired}},
		{name: "no carry, required key missing is refused",
			own:   []metaArg{{Text: "team=core"}},
			carry: func(_, _ ids.ID[ids.MetaKey]) map[ids.ID[ids.MetaKey]]string { return nil },
			want:  want{code: errs.MetaRequired}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			client := metaKeyRow(t, f, "client", `["memories"]`)
			team := metaKeyRow(t, f, "team", "")
			var terms []meta.Term
			for k, v := range c.carry(client, team) {
				terms = append(terms, carryTerm(t, k, v))
			}
			cctx := WithMeta(ctx, metaWrite(t, c.own))
			cctx, bound := WithMetaCarry(cctx, MetaCarry{Terms: terms, Table: schema.TableMemories})
			w, err := f.eng.Create(cctx, "", schema.TableMemories, f.memFields("replacement"))
			if c.want.code != "" {
				if !errs.Has(err, c.want.code) {
					t.Fatalf("err = %v, want %s", err, c.want.code)
				}
				mems, lerr := f.eng.List(ctx, store.SourceRepo, schema.TableMemories)
				if lerr != nil {
					t.Fatal(lerr)
				}
				if len(mems) != 0 {
					t.Fatalf("a refused add stored %d memories", len(mems))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := metaValues(t, w.Row)
			if len(got) != len(c.want.meta) {
				t.Fatalf("meta = %v, want %v", got, c.want.meta)
			}
			for k, v := range c.want.meta {
				if got[k] != v {
					t.Fatalf("meta %s = %s, want %s", k, got[k], v)
				}
			}
			if bound.Filled() != c.want.filled {
				t.Fatalf("Filled = %d, want %d", bound.Filled(), c.want.filled)
			}
		})
	}
}

// TestMetaTypeClashHintNamesNoMissingCommand (later 433, G-META-7): a
// :type that differs from the registered type is E_META_TYPE, and its hint
// names only commands that exist: drop the :type, or change the key's type
// with `lore meta edit --id <key> --type <type>` (built in 4ecdd709).
func TestMetaTypeClashHintNamesNoMissingCommand(t *testing.T) {
	t.Parallel()
	f := setup(t)
	metaKeyRow(t, f, "client", "")
	ctx := WithMeta(context.Background(), metaWrite(t, []metaArg{{Text: "client:number=3"}}))
	_, err := f.eng.Create(ctx, "", schema.TableMemories, f.memFields("typed"))
	var le *errs.Error
	if !errors.As(err, &le) || le.Code != errs.MetaType {
		t.Fatalf("err = %v, want E_META_TYPE", err)
	}
	if !strings.Contains(le.Hint, "lore meta edit --id client --type number") || !strings.Contains(le.Hint, "drop the :type") {
		t.Fatalf("hint = %q", le.Hint)
	}
}
