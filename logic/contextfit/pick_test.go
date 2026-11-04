package contextfit

import "testing"

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pat, p string
		want   bool
	}{
		{"services/billing/**", "services/billing/invoice.go", true},
		{"services/billing/**", "services/billing/a/b/c.go", true},
		{"services/billing/**", "services/auth/x.go", false},
		{"**/*.sql", "db/migrations/001.sql", true},
		{"*.sql", "db/migrations/001.sql", true},
		{"db/*.sql", "db/migrations/001.sql", false},
		{"./src/*.ts", "src/a.ts", true},
		{"", "a", false},
		{"[", "a", false},
	}
	for _, c := range cases {
		if got := MatchGlob(c.pat, c.p); got != c.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", c.pat, c.p, got, c.want)
		}
	}
	if g := SplitGlobs(`["services/billing/**","db/*.sql"]`); len(g) != 2 || g[0] != "services/billing/**" {
		t.Errorf("SplitGlobs JSON = %v", g)
	}
	if g := SplitGlobs("a/**, b/*.go;c\nd"); len(g) != 4 {
		t.Errorf("SplitGlobs = %v", g)
	}
}

func TestWordsAndPick(t *testing.T) {
	w := Words("services/billing/invoice_test.go and the Invoice")
	if len(w) != 4 || w[0] != "services" || w[2] != "invoice" || w[3] != "test" {
		t.Fatalf("Words = %v", w)
	}
	ps := []Piece{{ID: "a", Full: "aaaaaaaaaa", Line: "a"}, {ID: "b", Full: "bbbbbbbbbb", Line: "b"}, {ID: "c", Full: "cccccccccc", Line: "c"}}
	got := Pick(ps, 11, 0)
	if got.Text != "aaaaaaaaaab" || got.Lines != 1 || !got.More || got.Shown != 2 {
		t.Fatalf("Pick = %+v", got)
	}
	if all := Pick(ps, 0, 0); all.More || all.Shown != 3 {
		t.Fatalf("uncapped Pick = %+v", all)
	}
}
