package sqlite

// METADATA 4 "Budget from measurement" (D26, OQ6): the metadata cache
// bench. Opt-in (LORE_BENCH_META=1): it builds a generated store of
// metaBenchRows memories with metaBenchKeys keys each (list, object,
// allowed-value and sensitive keys among them) and records the first
// build time, the cache size and a warm two-filter search over row_meta
// (the `lore search key:value` / `lore sql` path; `<noun> list --meta`
// filters decoded rows in Go and is not measured here).
//
// Measured on a 48-core Linux machine ( load 7-11), 2026-11-05 (G-META-3):
//
//	rows x 20 keys   first build   cache     warm two-filter search
//	 2,000             6.0 s        12.5 MB    0.97 s
//	 6,000            28.0 s        32.5 MB    2.9 s
//	20,000           214 s         102 MB     10.3 s
//
// The search grows linearly (row_meta is a view over every row's JSON,
// OV39-VIEW) and the build faster than linearly, so the design's 100k x
// 20 store would take tens of minutes to build and about a minute per
// search: that store needs the row_meta surrogate-key tables of METADATA
// 4 (a measured optimisation for a later release). The bench therefore
// runs 20,000 rows and gates on the 20k numbers plus a margin; the bench
// fails when a later build exceeds them. LORE_BENCH_META_ROWS overrides
// the row count (the build and size gates scale by rows; the search gate
// scales too, as the search is linear).

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/metaquery"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

const (
	envMetaBench     = "LORE_BENCH_META"
	envMetaBenchRows = "LORE_BENCH_META_ROWS"
	metaBenchRows    = 20_000
	metaBenchKeys    = 20
	metaBenchBatch   = 2_000 // rows per Apply (one sync's worth)
	metaBenchRuns    = 3     // the warm list takes the fastest run
	// Gate per metaBenchRows rows: the 20k measurement plus about 40%
	// (G-META-3; the host is shared, so the margin covers load).
	metaGateBuild = 300 * time.Second // measured 214 s
	metaGateBytes = int64(145 << 20)  // measured 102 MB
	metaGateList  = 15 * time.Second  // measured 10.3 s
)

// metaBenchKey is one generated key: how its entry value is written.
type metaBenchKey struct {
	id        ids.ID[ids.MetaKey]
	name      string
	shape     string // text | list | object | allowed | sensitive
	sensitive bool
}

func metaBenchKeysSet() []metaBenchKey {
	shapes := []string{"text", "text", "text", "text", "text", "text", "text", "text", "text", "text",
		"list", "list", "list", "object", "object", "object", "allowed", "allowed", "sensitive", "sensitive"}
	out := make([]metaBenchKey, metaBenchKeys)
	for i := range out {
		out[i] = metaBenchKey{id: must(ids.New[ids.MetaKey]()), name: "k" + strconv.Itoa(i), shape: shapes[i], sensitive: shapes[i] == "sensitive"}
	}
	return out
}

// entry is key k's entry JSON on row n (values repeat every few rows so
// a filter matches a share of the store, as real metadata does).
func (k metaBenchKey) entry(n int) string {
	v := strconv.Itoa(n % 50)
	var val string
	switch k.shape {
	case "list":
		val = `["a` + v + `","b` + strconv.Itoa(n%7) + `","c"]`
	case "object":
		val = `{"region":"r` + strconv.Itoa(n%5) + `","n":` + v + `}`
	case "allowed":
		val = `"opt` + strconv.Itoa(n%4) + `"`
	default:
		val = `"val-` + v + `"`
	}
	s := `{"name":"` + k.name + `","scope":"repo","v":` + val
	if k.sensitive {
		s += `,"sensitive":true`
	}
	return s + "}"
}

func TestMetaBench(t *testing.T) {
	// Not t.Parallel: it is a timing gate, so it runs in the serial phase
	// with the machine to itself (parallel tests start only after it).
	if os.Getenv(envMetaBench) != "1" {
		t.Skip("metadata bench (METADATA 4, OQ6) runs for minutes; " + envMetaBench + "=1 runs it")
	}
	rows := metaBenchRows
	if s := os.Getenv(envMetaBenchRows); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < metaBenchBatch {
			t.Fatalf("%s=%q: want a number >= %d", envMetaBenchRows, s, metaBenchBatch)
		}
		rows = n
	}
	f := newFixture(t)
	c := f.open("bench.db")
	keys := metaBenchKeysSet()
	var reg []store.Row
	for _, k := range keys {
		reg = append(reg, f.live(schema.TableMetaKeys, k.id.Any(), map[string]value.Value{
			fieldName: text(t, k.name), "scope": text(t, "repo"), "type": text(t, "text"), fieldSensitive: value.BoolOf(k.sensitive),
		}))
	}
	f.apply(c, store.SourceRepo, "0", reg...)

	start := time.Now()
	batch := make([]store.Row, 0, metaBenchBatch)
	for n := range rows {
		m := map[ids.ID[ids.MetaKey]]string{}
		for _, k := range keys {
			m[k.id] = k.entry(n)
		}
		batch = append(batch, f.metaMemory(newMem(), "bench row "+strconv.Itoa(n), m))
		if len(batch) == metaBenchBatch || n == rows-1 {
			f.apply(c, store.SourceRepo, strconv.Itoa(n+1), batch...)
			batch = batch[:0]
		}
	}
	build := time.Since(start)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	size := dirBytes(t, f.dir)
	c = f.open("bench.db")

	cond := func(key, val string) string {
		s, err := RowMetaCond("x", metaquery.Term{Kind: metaquery.Cond, Key: key, Value: val})
		if err != nil {
			t.Fatal(err)
		}
		return `EXISTS (SELECT 1 FROM row_meta x WHERE x.entity_table = r.entity_table AND x.entity_id = r.entity_id AND x.key_name = '` + key + `' AND ` + s + `)`
	}
	q := `SELECT COUNT(DISTINCT r.entity_id) FROM row_meta r WHERE r.entity_table = 'memories' AND r.item_idx = -1 AND r.key_name = 'k0' AND ` +
		cond("k0", "val-7") + ` AND ` + cond("k16", "opt3")
	list, hits := time.Duration(0), 0
	for range metaBenchRuns {
		t0 := time.Now()
		hits = count(t, c, q)
		if d := time.Since(t0); list == 0 || d < list {
			list = d
		}
	}
	scale := float64(rows) / metaBenchRows
	t.Logf("OQ6 metadata bench: %d rows x %d keys: first build %v, cache %.1f MB, warm two-filter search %v (%d hits)",
		rows, metaBenchKeys, build.Round(time.Millisecond), float64(size)/(1<<20), list.Round(time.Millisecond), hits)
	if hits == 0 {
		t.Fatal("the two-filter search matched nothing: the fixture or the query is wrong")
	}
	if build > time.Duration(float64(metaGateBuild)*scale) {
		t.Errorf("first build %v over the gate %v", build, time.Duration(float64(metaGateBuild)*scale))
	}
	if size > int64(float64(metaGateBytes)*scale) {
		t.Errorf("cache %d bytes over the gate %d", size, int64(float64(metaGateBytes)*scale))
	}
	if limit := time.Duration(float64(metaGateList) * scale); list > limit {
		t.Errorf("warm two-filter search %v over the gate %v", list, limit)
	}
}

// dirBytes is the total size of the files in dir (the db and its -wal).
func dirBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "bench.db") {
			continue
		}
		fi, ierr := e.Info()
		if ierr != nil {
			t.Fatal(ierr)
		}
		n += fi.Size()
	}
	return n
}
