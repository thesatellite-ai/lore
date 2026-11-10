package engine

import (
	"context"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// scriptedConfirmer answers each question with the next scripted answer
// (no more answers: no) and records every question.
type scriptedConfirmer struct {
	answers []bool
	asked   []string
}

func (c *scriptedConfirmer) Confirm(_ context.Context, q string) (bool, error) {
	c.asked = append(c.asked, q)
	if len(c.answers) == 0 {
		return false, nil
	}
	a := c.answers[0]
	c.answers = c.answers[1:]
	return a, nil
}

// TestMetaAmbiguousAsksPerDefinition (P2-META-8, MQ12): a bare key name
// with two visible definitions (the repo's and the agency store's) asks an
// interactive caller once per definition, nearest place first, and uses
// the first yes. All no, --yes, no terminal or no confirmer answer
// E_META_AMBIGUOUS and store nothing; a scoped term never asks.
func TestMetaAmbiguousAsksPerDefinition(t *testing.T) {
	t.Parallel()
	const repoKey, agencyKey = "repo", "agency"
	type want struct {
		code  errs.Code // non-empty: refused with this code
		key   string    // which definition the stored value uses
		asked int
	}
	cases := []struct {
		name      string
		term      string
		terminal  bool
		yes       bool
		confirmer bool
		answers   []bool
		want      want
	}{
		{name: "first yes takes the nearest", term: "client=acme", terminal: true, confirmer: true, answers: []bool{true},
			want: want{key: repoKey, asked: 1}},
		{name: "no then yes takes the second", term: "client=acme", terminal: true, confirmer: true, answers: []bool{false, true},
			want: want{key: agencyKey, asked: 2}},
		{name: "all no is ambiguous", term: "client=acme", terminal: true, confirmer: true, answers: []bool{false, false},
			want: want{code: errs.MetaAmbiguous, asked: 2}},
		{name: "--yes never picks", term: "client=acme", terminal: true, yes: true, confirmer: true, answers: []bool{true},
			want: want{code: errs.MetaAmbiguous}},
		{name: "no terminal never asks", term: "client=acme", confirmer: true, answers: []bool{true},
			want: want{code: errs.MetaAmbiguous}},
		{name: "no confirmer is ambiguous", term: "client=acme", terminal: true,
			want: want{code: errs.MetaAmbiguous}},
		{name: "scoped term never asks", term: "repo:client=acme", terminal: true, confirmer: true, answers: []bool{false},
			want: want{key: repoKey}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conf := &scriptedConfirmer{answers: c.answers}
			var opts []Option
			if c.confirmer {
				opts = append(opts, WithConfirmer(conf))
			}
			f := setup(t, opts...)
			ctx := context.Background()
			keys := map[string]ids.ID[ids.MetaKey]{repoKey: metaKeyRow(t, f, "client", ""), agencyKey: agencyMetaKey(t, f, "client")}
			caller := op.Caller{Actor: testActor, Yes: c.yes, Interactive: c.terminal}
			_, err := f.eng.Run(ctx, memMetaOp(f, metaWrite(t, []metaArg{{Text: c.term}})), op.ArgsDecoder{Args: []string{"body"}}, caller)
			if len(conf.asked) != c.want.asked {
				t.Fatalf("questions = %d (%q), want %d", len(conf.asked), conf.asked, c.want.asked)
			}
			mems, lerr := f.eng.List(ctx, store.SourceRepo, schema.TableMemories)
			if lerr != nil {
				t.Fatal(lerr)
			}
			if c.want.code != "" {
				if !errs.Has(err, c.want.code) {
					t.Fatalf("err = %v, want %s", err, c.want.code)
				}
				if len(mems) != 0 {
					t.Fatalf("a refused add stored %d memories", len(mems))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(mems) != 1 {
				t.Fatalf("stored %d memories", len(mems))
			}
			if _, ok := mems[0].Meta[keys[c.want.key]]; !ok || len(mems[0].Meta) != 1 {
				t.Fatalf("meta = %v, want one value under the %s definition %s", mems[0].Meta, c.want.key, keys[c.want.key])
			}
		})
	}
}

// agencyMetaKey writes a text key definition straight into the read-only
// agency store (a teammate's row arriving by sync) and refreshes the cache.
func agencyMetaKey(t *testing.T, f *fixture, name string) ids.ID[ids.MetaKey] {
	t.Helper()
	ctx := context.Background()
	def, ok := f.eng.reg.Table(schema.TableMetaKeys)
	if !ok {
		t.Fatal("no meta_keys table")
	}
	id, err := f.eng.ids.NewID(def.Prefix)
	if err != nil {
		t.Fatal(err)
	}
	sr, err := store.FromRow(row.Row{Table: schema.TableMetaKeys, ID: id, HLC: hlc.Next(hlc.HLC{}, f.eng.wall(), testActor),
		Fields: map[string]value.Value{"name": txt(name), "scope": txt("store"), "type": txt("text")}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.agency.Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.eng.Refresh(ctx, agency); err != nil {
		t.Fatal(err)
	}
	k, err := ids.Parse[ids.MetaKey](id.String())
	if err != nil {
		t.Fatal(err)
	}
	return k
}
