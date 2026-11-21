package coding

import (
	"strings"
	"testing"
)

// The data set holds: 30 tasks, every named row exists, required rows are
// must-rules or critical hotfixes, and every critical hotfix is required
// by at least one task (else the gate would not test it).
func TestDataValid(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, tk := range Tasks() {
		for _, k := range tk.Required {
			used[k] = true
		}
	}
	for _, r := range Rows() {
		if r.Kind == KindHotfix && r.Severity == "critical" && !used[r.Key] {
			t.Errorf("critical hotfix %s is required by no task", r.Key)
		}
	}
}

func TestGradeIDs(t *testing.T) {
	tk := Task{ID: "T", Required: []string{"a", "b"}, Also: []string{"c"}}
	keyOf := map[string]string{"rul_aaaaaaaaaaaaaaaaaaaa1": "a", "dec_cccccccccccccccccccc1": "c", "mem_dddddddddddddddddddd1": "d", "rul_eeeeeeeeeeeeeeeeeeee1": "r-errwrap"}
	out := "<rule id=\"rul_aaaaaaaaaaaaaaaaaaaa1\">\nrul_aaaaaaaaaaaaaaaaaaaa1 again\ndec_cccccccccccccccccccc1 mem_dddddddddddddddddddd1 rul_eeeeeeeeeeeeeeeeeeee1"
	g := GradeIDs(tk, IDsIn(out), keyOf)
	if strings.Join(g.Missing, ",") != "b" {
		t.Errorf("missing = %v, want [b]", g.Missing)
	}
	// d applies nowhere; an always-on rule is never noise
	if strings.Join(g.Noise, ",") != "d" {
		t.Errorf("noise = %v, want [d]", g.Noise)
	}
	if g.Recall(tk) != 0.5 || g.Precision() != 0.75 {
		t.Errorf("recall %v precision %v, want 0.5 0.75", g.Recall(tk), g.Precision())
	}
	a := ArmResult{Name: "x", Grades: map[string]Grade{"T": g}}
	m := a.Misses([]Task{tk})
	if len(m) != 2 || !strings.Contains(m[0], "T b") || !strings.Contains(m[1], "precision 75%") {
		t.Errorf("misses = %q", m)
	}
}

func TestTrapsCheckDiffs(t *testing.T) {
	diff := func(file string, lines ...string) string {
		return "--- a/" + file + "\n+++ b/" + file + "\n@@ -1 +1 @@\n+" + strings.Join(lines, "\n+") + "\n"
	}
	cases := []struct {
		hotfix, diff string
		fell         bool
	}{
		{"h-tz", diff("billing/invoice.go", "inv.IssuedAt = time.Now()"), true},
		{"h-tz", diff("billing/invoice.go", "inv.IssuedAt = clock.UTC()"), false},
		{"h-tz", diff("billing/other.go", "x := time.Now()"), false},
		{"h-taxround", diff("billing/tax.go", "v := math.Round(x*100) / 100"), true},
		{"h-taxround", diff("billing/tax.go", "v := money.RoundHalfEven(x)"), false},
		{"h-hmac", diff("api/handlers/webhooks.go", "if sig != expected {"), true},
		{"h-hmac", diff("api/handlers/webhooks.go", "if !hmac.Equal(sig, expected) {"), false},
		// devco D-835: a missing-header check is not a signature comparison
		{"h-hmac", diff("api/handlers/webhooks.go", `if expectedSig == "" {`), false},
		{"h-hmac", diff("api/handlers/webhooks.go", `if sig == nil {`), false},
		{"h-hmac", diff("api/handlers/webhooks.go", `if sig == "" || sig != expected {`), true},
		{"h-hmac", diff("api/handlers/webhooks.go", `if "" != sig && sig == computed {`), true},
		{"h-migr-lock", diff("db/migrations/0043_events_created_index.sql", "CREATE INDEX events_created ON events (created_at);"), true},
		{"h-migr-lock", diff("db/migrations/0043_events_created_index.sql", "CREATE INDEX CONCURRENTLY events_created ON events (created_at);"), false},
		{"h-pool", diff("internal/cache/redis.go", "conn := pool.Get()", "return conn.Do(\"GET\", k)"), true},
		{"h-pool", diff("internal/cache/redis.go", "conn := pool.Get()", "defer conn.Close()"), false},
		{"h-cookie", diff("web/src/auth/Login.tsx", "document.cookie = `s=${v}; SameSite=Strict`"), true},
		{"h-cookie", diff("web/src/auth/Login.tsx", "document.cookie = `s=${v}; SameSite=Lax`"), false},
		{"h-ratelimit", diff("internal/auth/ratelimit.go", "var attempts = map[string]int{}"), true},
		{"h-ratelimit", diff("internal/auth/ratelimit.go", "n, err := redis.Incr(ctx, key)"), false},
	}
	traps := map[string]Trap{}
	for _, tr := range Traps() {
		traps[tr.Hotfix] = tr
	}
	for _, c := range cases {
		tr, ok := traps[c.hotfix]
		if !ok {
			t.Fatalf("no trap for %s", c.hotfix)
		}
		if fell, line := tr.FellIn(c.diff); fell != c.fell {
			t.Errorf("%s on %q: fell %v (%q), want %v", c.hotfix, c.diff, fell, line, c.fell)
		}
	}
	// every trap's task requires its hotfix
	tasks := map[string]Task{}
	for _, tk := range Tasks() {
		tasks[tk.ID] = tk
	}
	for _, tr := range Traps() {
		if !strings.Contains(strings.Join(tasks[tr.Task].Required, " "), tr.Hotfix) {
			t.Errorf("trap %s: task %s does not require it", tr.Hotfix, tr.Task)
		}
	}
}
