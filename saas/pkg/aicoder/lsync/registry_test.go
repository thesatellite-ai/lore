package lsync

import (
	"regexp"
	"testing"

	"dbent/gen/ent/migrate"

	"entgo.io/ent/dialect/sql/schema"
	"entgo.io/ent/schema/field"
)

// TestRegistry_EveryTableClassified is the gate that stops a new ent table
// from silently skipping the synced-or-local decision (§7).
func TestRegistry_EveryTableClassified(t *testing.T) {
	t.Parallel()
	reg, unclassified := NewRegistry()
	if len(unclassified) > 0 {
		t.Fatalf("tables without a sync decision: %v — add each to syncedTables or localTables in registry.go", unclassified)
	}
	if err := reg.validate(); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, st := range migrate.Tables {
		known[st.Name] = true
	}
	for name := range syncedTables {
		if _, inLocal := localTables[name]; inLocal {
			t.Fatalf("%s is both synced and local", name)
		}
		if !known[name] {
			t.Fatalf("syncedTables lists %s, which is not in the schema", name)
		}
	}
	for name := range localTables {
		if !known[name] {
			t.Fatalf("localTables lists %s, which is not in the schema", name)
		}
	}
}

// TestRegistry_GateFires proves the classification gate is live by feeding
// it a table nobody classified (a gate never seen to fail is unverified).
func TestRegistry_GateFires(t *testing.T) {
	t.Parallel()
	fake := &schema.Table{Name: "brand_new_table", Columns: []*schema.Column{{Name: "id", Type: field.TypeString}}}
	_, unclassified := buildRegistry([]*schema.Table{fake})
	if len(unclassified) != 1 || unclassified[0] != "brand_new_table" {
		t.Fatalf("gate did not fire: %v", unclassified)
	}
}

func TestRegistry_VolatileAndBytesExcluded(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry()
	mem, ok := reg.Table("memories")
	if !ok {
		t.Fatal("memories must sync")
	}
	for _, c := range mem.Columns {
		if c.Type == field.TypeBytes {
			t.Fatalf("binary column %s must not sync", c.Name)
		}
		if _, vol := volatileColumns[c.Name]; vol {
			t.Fatalf("volatile column %s must not sync", c.Name)
		}
	}
	actors, _ := reg.Table("actors")
	found := false
	for _, c := range actors.Volatile {
		if c.Name == "last_seen_at" {
			found = true
		}
	}
	if !found {
		t.Fatal("NOT NULL volatile column must be tracked for inserts")
	}
	if _, synced := reg.Table("query_logs"); synced {
		t.Fatal("telemetry must not sync")
	}
	if _, synced := reg.Table("knowledge_revisions"); synced {
		t.Fatal("knowledge_revisions must stay local (E5)")
	}
}

func TestRegistry_NaturalKeys(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry()
	for table, want := range map[string][]string{
		"tags":        {"project_id", "name"},
		"actors":      {"stable_key"},
		"repos":       {"project_id", "mount_name"},
		"entity_tags": {"entity_table", "entity_id", "tag_id"},
	} {
		tb, ok := reg.Table(table)
		if !ok {
			t.Fatalf("%s not synced", table)
		}
		found := false
		for _, k := range tb.NaturalKeys {
			if len(k) == len(want) {
				match := true
				for i := range k {
					if k[i] != want[i] {
						match = false
					}
				}
				found = found || match
			}
		}
		if !found {
			t.Fatalf("%s natural keys = %v, want %v", table, tb.NaturalKeys, want)
		}
	}
}

// counterNamePattern matches column names that usually hold per-row
// sequence numbers or counters. Synced tables must not carry them: two
// branches incrementing the same sequence collide on merge (E5/E6).
var counterNamePattern = regexp.MustCompile(`(^seq$|_seq$|_num$|^count$|_count$|_counter$)`)

// counterWhitelist lists synced integer columns that are NOT sequences,
// with the reason.
var counterWhitelist = map[string]string{}

func TestRegistry_NoCountersInSyncedTables(t *testing.T) {
	t.Parallel()
	reg, _ := NewRegistry()
	for _, name := range reg.Names() {
		tb, _ := reg.Table(name)
		for _, c := range tb.Columns {
			isInt := c.Type == field.TypeInt || c.Type == field.TypeInt64 || c.Type == field.TypeInt32 || c.Type == field.TypeUint64
			if !isInt || !counterNamePattern.MatchString(c.Name) {
				continue
			}
			if _, ok := counterWhitelist[name+"."+c.Name]; ok {
				continue
			}
			t.Errorf("synced %s.%s looks like a sequence/counter; it will collide across branches — make it volatile, local, or whitelist it with a reason", name, c.Name)
		}
	}
}

func TestRegistry_CounterGateFires(t *testing.T) {
	t.Parallel()
	for _, n := range []string{"seq", "revision_num", "hit_count", "count"} {
		if !counterNamePattern.MatchString(n) {
			t.Fatalf("gate misses %s", n)
		}
	}
	for _, n := range []string{"sequence_name", "account_id", "numeric"} {
		if counterNamePattern.MatchString(n) {
			t.Fatalf("gate false positive %s", n)
		}
	}
}
