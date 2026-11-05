package leakcheck

import (
	"strings"
	"testing"
)

// Ids of the metadata fixtures (arbitrary hex).
const (
	keyRowID    = "mtk_0d000000000000000000000000000001" // an internal meta_keys row of a store
	tokRowID    = "mem_0d000000000000000000000000000002" // a store row holding internal meta values
	ownKeyID    = "mtk_0d000000000000000000000000000003" // a key row of the checked client repo
	foreignKey  = "mtk_0d000000000000000000000000000004" // a key id from outside the repo
	clientRowID = "tsk_0d000000000000000000000000000005"
	ownScope    = "repo"
)

// Values of the fixtures: printed nowhere in a finding (3.15 step 7).
const (
	hostValue  = "vpn-7.internal.acme.example"
	shortValue = "acme5"  // 5 characters: below MinTokenLen
	marginPair = "35"     // the value of the margin_pct pair
	sixValue   = "zq81xk" // exactly MinTokenLen
)

// metaIndexRows is the index of the metadata matrix: one internal key row
// (two key names, one long enough for the key-name rule, one too short),
// one store row with internal values, and the client repo's own key row.
func metaIndexRows() *Index {
	return NewIndex([]IndexRow{
		{ID: keyRowID, Table: tableMetaKeys, Label: "[northwind/org]", KeyNames: []string{"margin_pct", "tier"}},
		{ID: tokRowID, Table: "memories", Label: "[northwind/acme]", Tokens: []Token{
			{Value: hostValue, Key: "vpn_host", Type: "dns"},
			{Value: shortValue, Key: "client_code", Type: "text"},
			{Value: marginPair, Key: "margin_pct", Type: "number"},
			{Value: sixValue, Key: "pin_code", Type: "text"},
		}},
		{ID: ownKeyID, Table: tableMetaKeys, RepoKey: true, RepoKeyScope: ownScope},
	}, nil)
}

// metaFindings is Check's metadata-rule findings (key-name, key-pair,
// exact-token, foreign-key) as rule@row.
func metaFindings(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		switch f.Rule {
		case RuleKeyName, RuleKeyPair, RuleExactToken, RuleForeignKey:
			out = append(out, string(f.Rule)+"@"+f.RowID)
		case RuleID, RuleRef, RuleText, RuleSecret, RuleActor, RuleSession:
		}
	}
	return out
}

// TestMetaRulesMatrix (METADATA 3.15 steps 1, 3, 6, 7; 3.11 item 1;
// FB-META-1): each metadata rule at its boundary, in a source file, a
// commit message and a client row file; no finding ever carries a value.
func TestMetaRulesMatrix(t *testing.T) {
	idx := metaIndexRows()
	src := func(s string) Item { return Item{Where: "src/a.go", Path: "src/a.go", Content: []byte(s)} }
	msg := func(s string) Item { return Item{Where: "commit message", Content: []byte(s)} }
	rowFile := func(body string) Item {
		p := ".lore/data/tasks/" + clientRowID + ".json"
		return Item{Where: p, Path: p, Content: []byte(body)}
	}
	cases := []struct {
		name   string
		item   Item
		client bool
		want   []string // metadata findings, in Check's order
	}{
		// key-name
		{"key name in source", src("// see margin_pct for the rule"), false, []string{"key-name@" + keyRowID}},
		{"key name upper case", msg("MARGIN_PCT changed"), false, []string{"key-name@" + keyRowID}},
		{"key name dotted", msg("read margin_pct. later"), false, []string{"key-name@" + keyRowID}},
		{"key name inside a longer identifier", msg("gross_margin_pct_v2 changed"), false, nil},
		{"short key name alone is an ordinary word", msg("the tier changed"), false, nil},
		// key-pair (any value length)
		{"pair name value", msg("margin_pct 35 for acme"), false, []string{"key-pair@" + tokRowID, "key-name@" + keyRowID}},
		{"pair with colon", msg("client_code: acme5 today"), false, []string{"key-pair@" + tokRowID}},
		{"short value alone", msg("ship acme5 today"), false, nil},
		{"pair value differs", msg("client_code: acme6"), false, nil},
		// exact-token (min 6, whole token, case-insensitive)
		{"host whole token", msg("connect to " + hostValue + " first"), false, []string{"exact-token@" + tokRowID}},
		{"host upper case", msg("connect to " + strings.ToUpper(hostValue)), false, []string{"exact-token@" + tokRowID}},
		{"host with .org suffix is another host", msg("connect to " + hostValue + ".org now"), false, nil},
		{"host with -2 suffix is another host", msg("connect to " + hostValue + "-2"), false, nil},
		{"host at end of sentence", msg("connect to " + hostValue + "."), false, []string{"exact-token@" + tokRowID}},
		{"host inside a longer word", msg("x" + hostValue), false, nil},
		{"six-character value is a token", msg("pin " + sixValue + " set"), false, []string{"exact-token@" + tokRowID}},
		{"six-character value inside a word", msg("pin " + sixValue + "abc"), false, nil},
		// meta entry ids (3.15 step 3) and the client foreign-key rule (3.11)
		{"own key entry in client row", rowFile(`{"id":"` + clientRowID + `","meta":{"` + ownKeyID + `":{"v":"x","scope":"repo"}}}`), true, nil},
		{"foreign key entry in client row", rowFile(`{"id":"` + clientRowID + `","meta":{"` + foreignKey + `":{"v":"x"}}}`), true, []string{"foreign-key@" + clientRowID}},
		{"own key with another scope label", rowFile(`{"id":"` + clientRowID + `","meta":{"` + ownKeyID + `":{"v":"x","scope":"project:web"}}}`), true, []string{"foreign-key@" + clientRowID}},
		{"foreign key entry in an own repo", rowFile(`{"id":"` + clientRowID + `","meta":{"` + foreignKey + `":{"v":"x"}}}`), false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := Check([]Item{c.item}, idx, Options{ClientRepo: c.client})
			if got := metaFindings(fs); strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("findings %v, want %v", got, c.want)
			}
			for _, f := range fs {
				for _, v := range []string{hostValue, shortValue, sixValue, "margin_pct 35"} {
					if strings.Contains(strings.ToLower(f.Detail), strings.ToLower(v)) {
						t.Errorf("finding %s prints a value: %q", f.Rule, f.Detail)
					}
				}
			}
		})
	}
}

// TestMetaEntryKeyIDIsAReference (3.15 step 3): an internal key id used as
// a meta map key of a client row file is a reference finding, like any
// other internal id in the file.
func TestMetaEntryKeyIDIsAReference(t *testing.T) {
	idx := metaIndexRows()
	p := ".lore/data/tasks/" + clientRowID + ".json"
	fs := Check([]Item{{Where: p, Path: p, Content: []byte(`{"id":"` + clientRowID + `","meta":{"` + keyRowID + `":{"v":"x"}}}`)}}, idx, Options{})
	found := false
	for _, f := range fs {
		if f.Rule == RuleRef && f.RowID == keyRowID {
			found = true
		}
	}
	if !found {
		t.Fatalf("internal key id as a meta entry id not reported: %v", rules(fs))
	}
}

// TestMetaRulesAllowedRow: a deliberate transfer (AllowRows) of the row
// holding the values or the key row silences that row's metadata rules
// only.
func TestMetaRulesAllowedRow(t *testing.T) {
	idx := metaIndexRows()
	text := "margin_pct 35 and " + hostValue
	it := Item{Where: "commit message", Content: []byte(text), AllowRows: []string{tokRowID}}
	if got := metaFindings(Check([]Item{it}, idx, Options{})); strings.Join(got, ",") != "key-name@"+keyRowID {
		t.Fatalf("with the value row allowed: %v", got)
	}
	it.AllowRows = []string{tokRowID, keyRowID}
	if got := metaFindings(Check([]Item{it}, idx, Options{})); len(got) != 0 {
		t.Fatalf("with both rows allowed: %v", got)
	}
}

// TestCheckCIMeta (3.15 step 4): CheckCI's metadata structure rules over
// the client's own files: an entry of an unknown key or another scope, an
// internal client key row, and merged_into / replaced_by naming a key
// outside the repo; findings name no id.
func TestCheckCIMeta(t *testing.T) {
	keyPath := ".lore/data/meta_keys/" + ownKeyID + ".json"
	key := `{"id":"` + ownKeyID + `","name":"jira","scope":"repo"}`
	rowPath := ".lore/data/tasks/" + clientRowID + ".json"
	cases := []struct {
		name  string
		key   string
		row   string
		wants []string // substrings of the expected details, in order
	}{
		{"clean", key, `{"id":"` + clientRowID + `","meta":{"` + ownKeyID + `":{"v":"x","scope":"repo"}}}`, nil},
		{"unknown key entry", key, `{"id":"` + clientRowID + `","meta":{"` + foreignKey + `":{"v":"x"}}}`, []string{"not one of this repo's key rows"}},
		{"entry with another scope", key, `{"id":"` + clientRowID + `","meta":{"` + ownKeyID + `":{"v":"x","scope":"project:web"}}}`, []string{"scope label is not this repo's"}},
		{"internal client key row", `{"id":"` + ownKeyID + `","name":"jira","scope":"repo","internal":true}`, `{"id":"` + clientRowID + `"}`, []string{"marked internal"}},
		{"merged_into outside", `{"id":"` + ownKeyID + `","name":"jira","scope":"repo","merged_into":"` + foreignKey + `"}`, `{"id":"` + clientRowID + `"}`, []string{"merged_into names a key outside"}},
		{"replaced_by outside", `{"id":"` + ownKeyID + `","name":"jira","scope":"repo","replaced_by":"` + foreignKey + `"}`, `{"id":"` + clientRowID + `"}`, []string{"replaced_by names a key outside"}},
		{"merged_into own key", `{"id":"` + ownKeyID + `","name":"jira","scope":"repo","merged_into":"` + ownKeyID + `"}`, `{"id":"` + clientRowID + `"}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := CheckCI(map[string][]byte{keyPath: []byte(c.key), rowPath: []byte(c.row)}, repoPID)
			if len(fs) != len(c.wants) {
				t.Fatalf("%d findings %+v, want %d", len(fs), fs, len(c.wants))
			}
			for i, w := range c.wants {
				if !strings.Contains(fs[i].Detail, w) {
					t.Errorf("finding %d %q, want %q", i, fs[i].Detail, w)
				}
			}
			for _, f := range fs {
				if strings.Contains(f.Detail, foreignKey) || strings.Contains(f.Detail, ownKeyID) || strings.Contains(f.Where, clientRowID) {
					t.Errorf("CI finding names an id: %+v", f)
				}
			}
		})
	}
}
