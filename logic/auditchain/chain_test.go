package auditchain

import "testing"

// TestHashAndVerify: the 0.1.18 fields change the hash only when set
// (OQ10), a chain links each entry to Hash of the one before, and editing
// or dropping an entry is reported at the entry after it.
func TestHashAndVerify(t *testing.T) {
	base := Record{ID: "aud_1", ActorID: "act_a", Action: "memories.write", TargetTable: "memories", TargetID: "mem_1"}
	if Hash(base) != Hash(Record{ID: "aud_1", ActorID: "act_a", Action: "memories.write", TargetTable: "memories", TargetID: "mem_1",
		CreatedAt: "2026-01-01T00:00:00Z", PrevLogHash: "x"}) {
		t.Fatal("created_at or prev_log_hash changed the hash")
	}
	withSes := base
	withSes.SessionID = "ses_1"
	conf := base
	conf.Confirmed = true
	if Hash(withSes) == Hash(base) || Hash(conf) == Hash(base) || Hash(withSes) == Hash(conf) {
		t.Fatal("session / confirmed must change the hash when set")
	}
	b := Record{ID: "aud_2", ActorID: "act_a", Action: "rules.write", PrevLogHash: Hash(base)}
	c := Record{ID: "aud_3", ActorID: "act_b", Action: "tasks.write", PrevLogHash: Hash(b)}
	for _, tc := range []struct {
		name   string
		chain  []Record
		broken string
		n      int
	}{
		{"empty", nil, "", 0},
		{"intact", []Record{base, b, c}, "", 3},
		{"first entry with a link", []Record{b}, "aud_2", 0},
		{"middle edited", []Record{base, {ID: "aud_2", ActorID: "act_a", Action: "forged", PrevLogHash: Hash(base)}, c}, "aud_3", 2},
		{"middle dropped", []Record{base, c}, "aud_3", 1},
	} {
		if broken, n := Verify(tc.chain); broken != tc.broken || n != tc.n {
			t.Errorf("%s: Verify = %q, %d; want %q, %d", tc.name, broken, n, tc.broken, tc.n)
		}
	}
}
