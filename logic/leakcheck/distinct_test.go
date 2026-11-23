package leakcheck

import (
	"strings"
	"testing"
)

// TestDistinctiveToken (#138, devco:D-916): dates, datetimes, numbers,
// flags, statuses, versions, durations and periods are never exact tokens
// on their own, whatever the key's type (an auto-registered key is text);
// row ids, hosts, IPs, emails, URLs, values mixing letters and digits and
// long values are.
func TestDistinctiveToken(t *testing.T) {
	cases := []struct {
		name, v, typ string
		want         bool
	}{
		{"iso date", "2026-11-22", "text", false},
		{"slash date", "2026/11/22", "text", false},
		{"day first date", "22.11.2026", "text", false},
		{"month", "2026-11", "text", false},
		{"written date", "22 November 2026", "text", false},
		{"written date us", "Nov 22, 2026", "text", false},
		{"datetime", "2026-11-22T19:23:52.893250625Z", "text", false},
		{"datetime offset", "2026-11-22 10:00:00+05:30", "text", false},
		{"time", "10:30:00", "text", false},
		{"integer", "1234567", "text", false},
		{"long integer", "12345678901234567890123456", "text", false},
		{"decimal", "35.125", "text", false},
		{"grouped number", "1,250,000", "text", false},
		{"number with unit", "512mb", "text", false},
		{"percent", "99.95%", "text", false},
		{"boolean", "false", "text", false},
		{"flag word", "enabled", "text", false},
		{"status", "in_progress", "text", false},
		{"status word", "approved", "text", false},
		{"version", "v0.1.38", "text", false},
		{"version suffix", "1.2.3-rc1", "text", false},
		{"duration", "2h45m30s", "text", false},
		{"iso duration", "pt1h30m", "text", false},
		{"quarter", "q4-2026", "text", false},
		{"fiscal year", "fy2026", "text", false},
		{"two-label word pair", "node.js", "text", false},
		{"short", "zq81x", "text", false},
		{"typed date", "2026-11-22", "date", false},
		{"typed number with letters", "1e10abc", "number", false},
		{"typed list of dates", "2026-11-22", "list<date>", false},
		{"typed bool", "enabled", "bool", false},
		{"row id", "mem_0d000000000000000000000000000002", "text", true},
		{"host", "vpn-7.internal.acme.example", "text", true},
		{"three-label host", "db.internal.example", "text", true},
		{"ipv4", "203.0.113.10", "text", true},
		{"cidr", "10.20.0.0/16", "text", true},
		{"email", "ops@acme.example", "text", true},
		{"url", "https://grafana.acme.example/d/x", "text", true},
		{"letters and digits", "zq81xk", "text", true},
		{"long phrase", "the billing replica in the east", "text", true},
		{"typed dns two labels", "acme.example", "dns", true},
		{"typed email", "a@b.example", "email", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DistinctiveToken(strings.ToLower(c.v), c.typ); got != c.want {
				t.Fatalf("DistinctiveToken(%q, %q) = %v, want %v", c.v, c.typ, got, c.want)
			}
		})
	}
}

// TestExactTokenSkipsCommonValues (#138): in a commit message or source
// line, a date, datetime, number, flag or status equal to an internal
// metadata value is no finding on its own; its "key value" pair still is;
// a host, email or row id value still blocks by exact token, and every
// metadata finding names the row.
func TestExactTokenSkipsCommonValues(t *testing.T) {
	const row = "mem_0d000000000000000000000000000009"
	idx := NewIndex([]IndexRow{{ID: row, Table: "memories", Label: "[acme-co/ops]", Tokens: []Token{
		{Value: "2026-11-22", Key: "fact_verified_at", Type: "text"},
		{Value: "2026-11-22T19:23:52Z", Key: "reviewed_at", Type: "text"},
		{Value: "4815162342", Key: "ticket_no", Type: "text"},
		{Value: "true", Key: "billing_ok", Type: "text"},
		{Value: "in_progress", Key: "phase", Type: "text"},
		{Value: "vpn-7.internal.acme.example", Key: "vpn_host", Type: "text"},
		{Value: "ops@acme.example", Key: "ops_mail", Type: "text"},
		{Value: "tsk_0d00000000000000000000000000000a", Key: "parent_ref", Type: "text"},
	}}}, nil)
	cases := []struct {
		name, text string
		want       []string
	}{
		{"date", "written before 2026-11-22, when the loop moved", nil},
		{"datetime", "built at 2026-11-22T19:23:52Z by the job", nil},
		{"number", "see ticket 4815162342 upstream", nil},
		{"boolean", "set flag true", nil},
		{"status", "status in_progress for now", nil},
		{"date pair still caught", "fact_verified_at 2026-11-22 for the billing note", []string{"key-pair@" + row}},
		{"host", "connect to vpn-7.internal.acme.example first", []string{"exact-token@" + row}},
		{"email", "mail ops@acme.example today", []string{"exact-token@" + row}},
		{"row id value", "parent tsk_0d00000000000000000000000000000a here", []string{"exact-token@" + row}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := Check([]Item{{Where: "src/a.go", Path: "src/a.go", Content: []byte(c.text)}}, idx, Options{})
			got := metaFindings(fs)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("findings %v, want %v", got, c.want)
			}
			for _, f := range fs {
				if (f.Rule == RuleExactToken || f.Rule == RuleKeyPair) && !strings.Contains(f.Detail, "of row "+row) {
					t.Errorf("%s finding does not name its row: %q", f.Rule, f.Detail)
				}
			}
		})
	}
}

// TestMetaFindingAllowed (#138): an exact-token or key-pair finding a
// text allow covers (its row, the hash the finding carries, its file) is
// reported but does not block; another file or another value still does.
func TestMetaFindingAllowed(t *testing.T) {
	const row = "mem_0d00000000000000000000000000000b"
	idx := NewIndex([]IndexRow{{ID: row, Table: "memories", Tokens: []Token{
		{Value: "vpn-7.internal.acme.example", Key: "vpn_host", Type: "text"},
		{Value: "db-2.internal.acme.example", Key: "db_host", Type: "text"},
	}}}, nil)
	item := func(path, text string) Item { return Item{Where: path, Path: path, Content: []byte(text)} }
	first := Check([]Item{item("src/a.go", "use vpn-7.internal.acme.example")}, idx, Options{})
	if len(first) != 1 || first[0].Rule != RuleExactToken || first[0].Hash == 0 {
		t.Fatalf("want one exact-token finding with a hash, got %+v", first)
	}
	allows := []TextAllow{{ID: "sal_1", RowID: row, Hash: first[0].Hash, Path: "src/a.go", Reason: "public status page"}}
	fs := Check([]Item{item("src/a.go", "use vpn-7.internal.acme.example")}, idx, Options{TextAllows: allows})
	if len(Blocking(fs)) != 0 || len(fs) != 1 || fs[0].Allowed.ID != "sal_1" || !strings.Contains(fs[0].Detail, "allowed by sal_1") {
		t.Fatalf("allowed value still blocks or is not named: %+v", fs)
	}
	if fs := Check([]Item{item("src/b.go", "use vpn-7.internal.acme.example")}, idx, Options{TextAllows: allows}); len(Blocking(fs)) != 1 {
		t.Fatalf("an allow narrowed to src/a.go let src/b.go through: %+v", fs)
	}
	if fs := Check([]Item{item("src/a.go", "use db-2.internal.acme.example")}, idx, Options{TextAllows: allows}); len(Blocking(fs)) != 1 {
		t.Fatalf("an allow for one value let another value of the row through: %+v", fs)
	}
}
