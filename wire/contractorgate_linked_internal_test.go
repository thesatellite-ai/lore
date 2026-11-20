package wire

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/engine/attach"
	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/refs"
	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/memstore"
)

// TestContractorMemberOfCleanedLinkedRepo (devco D-736, cleanlink3): the
// contractor wall asks whether the caller is a member of the repo's
// project, and read that project only from the repo store. A cleaned
// linked repo holds no project row (doctor --fix moved it to the folder),
// so a contractor on the folder's project was walled out of the repo
// ("this repo is one of acme's own repos"). The linked folder's project
// rows now count; with no linked folder the answer stays "not a member".
func TestContractorMemberOfCleanedLinkedRepo(t *testing.T) {
	ctx := context.Background()
	reg, err := schema.NewRegistry(schema.Builtin()...)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := ids.Parse[ids.Actor]("act_01936f0000007a1b8c2d3e4f5a6b7c11")
	if err != nil {
		t.Fatal(err)
	}
	prj, err := ids.Parse[ids.Project]("prj_01936f0000007a1b8c2d3e4f5a6b7c22")
	if err != nil {
		t.Fatal(err)
	}
	mem, err := ids.ParseAny("pmb_01936f0000007a1b8c2d3e4f5a6b7c33")
	if err != nil {
		t.Fatal(err)
	}
	const org, folder store.Source = "acme", "acme/billing"
	data := map[store.Source]*memstore.Data{}
	c := newCaches()
	t.Cleanup(func() { _ = c.Close() })
	sc, err := scan.NewRegexScanner(nil)
	if err != nil {
		t.Fatal(err)
	}
	opts := []engine.Option{engine.WithRegistry(reg), engine.WithActor(actor), engine.WithCache(c), engine.WithScanner(sc), engine.WithAudit(nopAudit{})}
	for i, n := range []store.Source{store.SourceRepo, folder, org} {
		if err = c.open(ctx, n, filepath.Join(t.TempDir(), strconv.Itoa(i)+".db"), reg, refs.Expander{}); err != nil {
			t.Fatal(err)
		}
		data[n] = memstore.NewData()
		lvl := policy.LevelRepo
		if n == org {
			lvl = policy.LevelOrg
		}
		opts = append(opts, engine.WithStore(engine.Attached{Name: n, Backend: data[n].Open(store.OpenOptions{}), Level: lvl}))
	}
	eng, err := engine.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	put := func(n store.Source, table row.Table, id ids.AnyID, fields map[string]value.Value) {
		t.Helper()
		sr, err := store.FromRow(row.Row{Table: table, ID: id, HLC: hlc.Next(hlc.HLC{}, time.Now(), actor), Fields: fields})
		if err != nil {
			t.Fatal(err)
		}
		if err := data[n].Open(store.OpenOptions{}).Write(ctx, []store.Change{{Row: sr}}); err != nil {
			t.Fatal(err)
		}
		if err := c.Apply(ctx, n, []store.Row{sr}, ""); err != nil {
			t.Fatal(err)
		}
	}
	name, _ := value.TextOf("billing")
	put(folder, schema.TableProjects, prj.Any(), map[string]value.Value{"name": name})
	aRef, _ := value.RefOf(actor.Any())
	pRef, _ := value.RefOf(prj.Any())
	put(org, schema.TableProjectMembers, mem, map[string]value.Value{schema.FieldMemberActor: aRef, schema.FieldMemberProject: pRef})

	st := &state{eng: eng, actor: actor}
	attached := []attach.Opened{{Attached: engine.Attached{Name: org, Level: policy.LevelOrg}}}
	if st.memberOfRepoProject(ctx, attached, nil) {
		t.Fatal("no linked folder: the repo store holds no project, so the caller is no member of it")
	}
	if !st.memberOfRepoProject(ctx, attached, func() string { return string(folder) }) {
		t.Fatal("a cleaned linked repo's project is its folder's: a member of it must not be walled out")
	}
}
