package lint

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
	"github.com/thesatellite-ai/lore/store/localstate"
)

// memState is an in-memory localstate.Store (usage docs only).
type memState map[string][]byte

func (m memState) Put(_ context.Context, t localstate.Table, key string, doc []byte) error {
	m[string(t)+"/"+key] = doc
	return nil
}

func (m memState) Get(_ context.Context, t localstate.Table, key string) (localstate.Entry, bool, error) {
	d, ok := m[string(t)+"/"+key]
	return localstate.Entry{Key: key, Doc: d}, ok, nil
}

func (m memState) List(context.Context, localstate.Table, string) ([]localstate.Entry, error) {
	return nil, nil
}

func (m memState) Delete(_ context.Context, t localstate.Table, key string) error {
	delete(m, string(t)+"/"+key)
	return nil
}

func (memState) Close() error { return nil }

// usageEnv is an Env whose only upgrade is LocalState (no other method is
// called by usageFindings).
type usageEnv struct {
	Env
	s memState
}

func (u usageEnv) LocalState(context.Context) (localstate.Store, error) { return u.s, nil }

// TestAgeBoundaries (CONTEXT_BUDGET 6.12, fa-ctx later:46) on a fixed
// clock: high_hotfix_old fires past 45 days and not at exactly 45;
// not_validated fires past 90 days from last_validated_at (else created)
// and not at exactly 90, and a recent validation clears an old row;
// not_used fires past 60 days of the last recorded use and not at exactly
// 60, never for a row with no record (FA-CTX-4); critical hotfixes are
// never "old", unpinned rows are never checked.
func TestAgeBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	ago := func(d int) time.Time { return now.Add(-time.Duration(d) * day) }
	dt := func(at time.Time) value.Value {
		v, err := value.DateTimeOf(at.Format(time.RFC3339))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	n := byte(0)
	mk := func(table row.Table, sev string, created time.Time, validated time.Time) sourced {
		n++
		p := ids.PrefixRule
		if table == schema.TableHotfixes {
			p = ids.PrefixHotfix
		}
		f := map[string]value.Value{"severity": tx(t, sev), "body": tx(t, "b")}
		if !validated.IsZero() {
			f[fValidated] = dt(validated)
		}
		return sourced{src: store.SourceRepo, r: row.Row{ID: id(t, p, n), CreatedAt: created, Fields: f}}
	}
	cases := []struct {
		name string
		s    sourced
		used time.Time // zero: no usage record
		want map[Code]int
	}{
		{"high hotfix 45d", mk(schema.TableHotfixes, "high", ago(45), now), time.Time{}, map[Code]int{}},
		{"high hotfix 46d", mk(schema.TableHotfixes, "high", ago(46), now), time.Time{}, map[Code]int{HighHotfixOld: 1}},
		{"critical hotfix 200d validated", mk(schema.TableHotfixes, "critical", ago(200), now), time.Time{}, map[Code]int{}},
		{"medium hotfix 200d", mk(schema.TableHotfixes, "medium", ago(200), time.Time{}), ago(100), map[Code]int{}},
		{"rule created 90d", mk(schema.TableRules, "must", ago(90), time.Time{}), time.Time{}, map[Code]int{}},
		{"rule created 91d", mk(schema.TableRules, "must", ago(91), time.Time{}), time.Time{}, map[Code]int{NotValidated: 1}},
		{"rule 300d validated 10d", mk(schema.TableRules, "must", ago(300), ago(10)), time.Time{}, map[Code]int{}},
		{"rule validated 91d", mk(schema.TableRules, "must", ago(300), ago(91)), time.Time{}, map[Code]int{NotValidated: 1}},
		{"should rule 300d", mk(schema.TableRules, "should", ago(300), time.Time{}), ago(100), map[Code]int{}},
		{"used 60d", mk(schema.TableRules, "must", ago(5), time.Time{}), ago(60), map[Code]int{}},
		{"used 61d", mk(schema.TableRules, "must", ago(5), time.Time{}), ago(61), map[Code]int{NotUsed: 1}},
		{"no usage record", mk(schema.TableRules, "must", ago(5), time.Time{}), time.Time{}, map[Code]int{}},
		{"high hotfix 46d unused 61d", mk(schema.TableHotfixes, "high", ago(46), ago(100)), ago(61), map[Code]int{HighHotfixOld: 1, NotValidated: 1, NotUsed: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			table := row.Table(schema.TableRules)
			if c.s.r.ID.Prefix() == ids.PrefixHotfix {
				table = schema.TableHotfixes
			}
			all := map[row.Table][]sourced{table: {c.s}}
			st := memState{}
			if !c.used.IsZero() {
				doc, err := json.Marshal(rowUsage{LastUsed: c.used})
				if err != nil {
					t.Fatal(err)
				}
				if err = st.Put(ctx, localstate.Usage, c.s.r.ID.String(), doc); err != nil {
					t.Fatal(err)
				}
			}
			fs := append(ageFindings(all, now), ops{env: usageEnv{s: st}}.usageFindings(ctx, all, now)...)
			got := codes(fs)
			if len(got) != len(c.want) {
				t.Fatalf("findings %v, want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Fatalf("findings %v, want %v", got, c.want)
				}
			}
		})
	}
}
