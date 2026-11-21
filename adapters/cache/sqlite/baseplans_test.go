package sqlite

import (
	"regexp"
	"testing"

	"github.com/thesatellite-ai/lore/schema"
)

// TestBasePlansBuiltOncePerRegistry (devco D-825): one read opens the cache
// of every folder with the same registry; the built-in tables' plans are
// built by the first open and shared by the next ones (same column slice),
// instead of ~60 plans built again per cache. A different registry gets
// plans of its own, and a kind plan added to one cache's state never shows
// in the shared set.
func TestBasePlansBuiltOncePerRegistry(t *testing.T) {
	f := newFixture(t)
	a, b := f.open("a.db"), f.open("b.db")
	pa, pb := a.st.plans[schema.TableResources], b.st.plans[schema.TableResources]
	if len(pa.columns) == 0 || len(pb.columns) == 0 {
		t.Fatalf("resources plan missing: %d / %d columns", len(pa.columns), len(pb.columns))
	}
	if &pa.columns[0] != &pb.columns[0] {
		t.Fatal("two caches opened with one registry built the resources plan twice; want it built once and shared")
	}
	base, err := basePlansFor(f.reg)
	if err != nil {
		t.Fatal(err)
	}
	for name := range a.st.plans {
		if _, ok := base[name]; !ok && a.st.plans[name].view == "" {
			t.Fatalf("plan %s in the cache state is neither built-in nor a kind", name)
		}
	}
	a.st.plans["zz_test_only"] = plan{}
	if _, leaked := base["zz_test_only"]; leaked {
		t.Fatal("a cache's state map is the shared base map; loadState must clone it")
	}
	other := newFixture(t)
	c := other.open("c.db")
	if pc := c.st.plans[schema.TableResources]; &pc.columns[0] == &pa.columns[0] {
		t.Fatal("another registry reused the first registry's plans")
	}
}

// TestValidIdentMatchesPattern: the byte loop accepts exactly the names
// ^[a-z_][a-z0-9_]*$ does (the pattern it replaced, D-825).
func TestValidIdentMatchesPattern(t *testing.T) {
	re := regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	names := []string{"", "_", "a", "z9", "9a", "_9", "res_knd_0a1b", "Abc", "a-b", "a b", "a\"b", "ä", "a\x00", "tasks", "_rows", "a.b", "__", "x_"}
	for c := 0; c < 256; c++ {
		names = append(names, string(rune(c)), "a"+string([]byte{byte(c)}))
	}
	for _, n := range names {
		if got, want := validIdent(n), re.MatchString(n); got != want {
			t.Errorf("validIdent(%q) = %v, pattern says %v", n, got, want)
		}
	}
}
