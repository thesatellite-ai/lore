package branchlayer

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyOrder(t *testing.T) {
	t.Parallel()
	def := DefaultSettings()
	withSets := def
	withSets.Isolate = []string{"dev-*"}
	withSets.IsolatedSet = []string{"dev-pat", "staging"}
	withSets.SharedSet = []string{"dev-pat", "long/lived", "dev-x"}
	shareMode := def
	shareMode.DefaultMode = Shared
	off := def
	off.Layers = false
	tests := []struct {
		name   string
		branch string
		s      Settings
		mode   Mode
		step   Step
		rule   string
	}{
		{"default branch shared", "main", def, Shared, StepDefault, ""},
		{"default branch shared even if isolated by name", "main", Settings{Layers: true, IsolatedSet: []string{"main"}}, Shared, StepDefault, ""},
		{"layers off", "feat/x", off, Shared, StepOff, ""},
		{"any feature name isolated", "feat/experiment", def, Isolated, StepDefaultMode, "isolate"},
		{"no prefix needed", "experiment", def, Isolated, StepDefaultMode, "isolate"},
		{"dev main-like", "dev", def, Shared, StepMainLike, "dev"},
		{"release glob", "release/1.2", def, Shared, StepMainLike, "release/*"},
		{"release glob one segment only", "release/1.2/hotfix", def, Isolated, StepDefaultMode, "isolate"},
		{"exact isolate beats main_like", "staging", withSets, Isolated, StepExact, "staging"},
		{"exact isolate beats exact share", "dev-pat", withSets, Isolated, StepExact, "dev-pat"},
		{"exact share beats isolate glob", "dev-x", withSets, Shared, StepExact, "dev-x"},
		{"isolate glob beats main_like", "dev-y", Settings{Layers: true, Isolate: []string{"dev-*"}, MainLike: []string{"dev*"}}, Isolated, StepIsolateGlob, "dev-*"},
		{"share mode", "feat/x", shareMode, Shared, StepDefaultMode, "share"},
		{"unknown mode is stricter", "feat/x", Settings{Layers: true, DefaultMode: "weird"}, Isolated, StepDefaultMode, "isolate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := Classify(tc.branch, "main", tc.s)
			if d.Mode != tc.mode || d.Step != tc.step || d.Rule != tc.rule {
				t.Fatalf("Classify(%q) = %+v, want mode %s step %d rule %q", tc.branch, d, tc.mode, tc.step, tc.rule)
			}
			if d.Why("main") == "" {
				t.Fatalf("no explanation for %+v", d)
			}
		})
	}
}

func TestMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		p, n string
		want bool
	}{
		{"dev", "dev", true}, {"dev", "dev2", false}, {"release/*", "release/1", true},
		{"release/*", "release", false}, {"release/**", "release/a/b", true}, {"**/wip", "a/b/wip", true},
		{"**/wip", "wip", true}, {"feat/**/x", "feat/x", true}, {"[", "[", false}, {"", "", true},
	}
	for _, tc := range tests {
		if got := Match(tc.p, tc.n); got != tc.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tc.p, tc.n, got, tc.want)
		}
	}
}

func TestRefsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, branch := range []string{"feat/experiment", "feat", "a%2Fb", "x/y/z", "üñí/çødé", "100%"} {
		k := Key{StoreBranch: "main", Folder: "app", RepoKey: RepoKey("github.com/acme/app"), Branch: branch}
		if err := k.Check(); err != nil {
			t.Fatal(err)
		}
		ref := k.Ref()
		if strings.Count(strings.TrimPrefix(ref, RefPrefix), "/") != 3 {
			t.Fatalf("%s: the branch key is not one flat segment", ref)
		}
		got, ok := ParseRef(RefPrefix, ref)
		if !ok || got != k {
			t.Fatalf("ParseRef(%s) = %+v %v, want %+v", ref, got, ok, k)
		}
		at := time.Date(2026, 11, 20, 9, 12, 0, 0, time.UTC)
		tk, tat, ok := ParseTrashRef(TrashPrefix, k.TrashRef(at))
		if !ok || tk != k || !tat.Equal(at) {
			t.Fatalf("ParseTrashRef(%s) = %+v %v %v", k.TrashRef(at), tk, tat, ok)
		}
	}
	// a second trash move in the same second gets a sequence number
	k0 := Key{StoreBranch: "main", Folder: ".", RepoKey: "abc", Branch: "feat"}
	at := time.Date(2026, 11, 20, 9, 12, 0, 0, time.UTC)
	if k, got, ok := ParseTrashRef(TrashPrefix, k0.TrashRefN(at, 2)); !ok || k != k0 || !got.Equal(at) || k0.TrashRefN(at, 1) != k0.TrashRef(at) {
		t.Fatalf("TrashRefN: %+v %v %v", k, got, ok)
	}
	if !strings.Contains(k0.Ref(), "/"+RootFolderSeg+"/") {
		t.Fatalf("root folder segment: %s", k0.Ref())
	}
	// feat and feat/x never nest: neither ref is a prefix folder of the other
	a := Key{StoreBranch: "main", Folder: "app", RepoKey: "abc", Branch: "feat"}.Ref()
	b := Key{StoreBranch: "main", Folder: "app", RepoKey: "abc", Branch: "feat/x"}.Ref()
	if strings.HasPrefix(b, a+"/") {
		t.Fatalf("%s nests under %s", b, a)
	}
	if len(RepoKey("x")) != repoKeyLen || RepoKey("a") == RepoKey("b") {
		t.Fatal("repo key")
	}
	for _, bad := range []Key{{}, {StoreBranch: "a/b", Folder: "f", RepoKey: "r", Branch: "b"}, {StoreBranch: "m", Folder: "", RepoKey: "r", Branch: "b"}, {StoreBranch: "m", Folder: "f", RepoKey: "r"}} {
		if bad.Check() == nil {
			t.Fatalf("%+v passed Check", bad)
		}
	}
	if _, ok := ParseRef(RefPrefix, RefPrefix+"main/app/abc"); ok {
		t.Fatal("short ref parsed")
	}
	if _, _, ok := ParseTrashRef(TrashPrefix, TrashPrefix+"main/app/abc/feat@nope"); ok {
		t.Fatal("bad stamp parsed")
	}
}

func TestFactsRoundTrip(t *testing.T) {
	t.Parallel()
	f := Facts{Repo: "github.com/acme/app", Folder: "app", Branch: "feat/x", BaseBranch: "main", Split: "9f3c", CodeHead: "77ab",
		Started: time.Date(2026, 11, 20, 9, 12, 0, 0, time.UTC), By: "act_1", State: StateLanded, Landed: "1a2b", Parent: "feat/a"}
	msg := "lore: 2 rows on feat/x (app)\n\n" + f.Trailers()
	got, ok := ParseFacts(msg)
	if !ok || got != f {
		t.Fatalf("ParseFacts = %+v %v, want %+v", got, ok, f)
	}
	if _, ok := ParseFacts("lore: merge origin\n"); ok {
		t.Fatal("a plain commit parsed as a layer commit")
	}
	multi := Facts{Branch: "a\nb"}.Trailers()
	if strings.Count(multi, "\n") != 2 {
		t.Fatalf("a value broke onto two lines: %q", multi)
	}
}
