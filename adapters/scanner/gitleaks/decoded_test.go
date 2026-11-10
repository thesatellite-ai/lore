package gitleaks

import (
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/schema"
)

// decodedRule is a store rule only lore's engine runs (gitleaks never
// sees it), so a decoded hit can come only from decodedOwn.
const decodedRule = "acme_token"

// acmeSecret is built at run time (no key-shaped literal in source).
var acmeSecret = "ACMETOK-" + strings.Repeat("7", 12)

func acmeScanner(t *testing.T) *Scanner {
	t.Helper()
	base := Pack()
	rules, err := Rules(base, []RuleRow{{RuleID: decodedRule, State: schema.SecretRuleOn, Regex: `ACMETOK-[0-9]{12}`, Keywords: []string{"acmetok"}}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(rules)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// percentAll percent-encodes every byte (url.QueryEscape keeps letters).
func percentAll(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		b.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{s[i]})))
	}
	return b.String()
}

// TestDecodedOwnRules (SECRETS 3.6, OV4): lore's own rules (pack + store
// rules) find a secret hidden in base64, hex, percent encoding and two
// nested layers; the finding sits on the ENCODED span of the stored text
// (so redaction and allows key on what is stored), is masked and hashed
// on the decoded value, and a plain hit is not reported twice.
func TestDecodedOwnRules(t *testing.T) {
	s := acmeScanner(t)
	b64 := base64.StdEncoding.EncodeToString([]byte("token=" + acmeSecret + ";"))
	nested := base64.StdEncoding.EncodeToString([]byte("x " + b64 + " y"))
	cases := []struct {
		name    string
		encoded string // the encoded segment placed in the text
		want    int    // findings of decodedRule
	}{
		{name: "plain", encoded: acmeSecret, want: 1},
		{name: "base64", encoded: b64, want: 1},
		{name: "hex", encoded: hex.EncodeToString([]byte("token=" + acmeSecret + ";")), want: 1},
		{name: "percent", encoded: percentAll("token=" + acmeSecret + ";"), want: 1},
		{name: "base64 of base64", encoded: nested, want: 1},
		{name: "base64 no secret", encoded: base64.StdEncoding.EncodeToString([]byte("nothing to see in here at all")), want: 0},
		{name: "query-escaped stays literal", encoded: url.QueryEscape("a b " + acmeSecret), want: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text := "deploy note:\nvalue " + c.encoded + " end"
			var got []scan.Finding
			for _, f := range s.Scan("body", text) {
				if f.RuleID == decodedRule {
					got = append(got, f)
				}
			}
			if len(got) != c.want {
				t.Fatalf("%d findings of %s, want %d: %+v", len(got), decodedRule, c.want, got)
			}
			assertNoLeak(t, c.name, acmeSecret, got)
			for _, f := range got {
				span := text[f.Offset : f.Offset+f.Length]
				if !strings.Contains(c.encoded, span) {
					t.Fatalf("finding span is not inside the encoded segment (offset %d len %d)", f.Offset, f.Length)
				}
				if f.Line != 2 {
					t.Fatalf("line = %d, want 2", f.Line)
				}
				if f.MatchHash != scan.HashMatch(acmeSecret) {
					t.Fatalf("hash is not the decoded secret's")
				}
			}
		})
	}
}

// TestDecodedOwnPlainAndEncoded: one plain and one base64 copy of the same
// secret are two findings at two spans (both must be redacted), not one.
func TestDecodedOwnPlainAndEncoded(t *testing.T) {
	s := acmeScanner(t)
	b64 := base64.StdEncoding.EncodeToString([]byte("token=" + acmeSecret + ";"))
	text := acmeSecret + " and " + b64
	var offs []int
	for _, f := range s.Scan("body", text) {
		if f.RuleID == decodedRule {
			offs = append(offs, f.Offset)
		}
	}
	if len(offs) != 2 || offs[0] == offs[1] {
		t.Fatalf("offsets %v, want two distinct", offs)
	}
}

// TestDecodedOwnSizeGate: above the gitleaks size gate decoded text is not
// scanned (the plain scan still runs).
func TestDecodedOwnSizeGate(t *testing.T) {
	s := acmeScanner(t)
	b64 := base64.StdEncoding.EncodeToString([]byte("token=" + acmeSecret + ";"))
	big := strings.Repeat("a ", GitleaksMaxBytes/2+1) + b64 + " " + acmeSecret
	n := 0
	for _, f := range s.Scan("body", big) {
		if f.RuleID == decodedRule {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d findings above the gate, want only the plain one", n)
	}
}

// TestPasswordPairProseFilterOff (SECRETS 3.4, policy
// secrets.password_pair_prose_filter: off): a password_pair match followed
// by prose, which the filter drops, is kept; what the filter keeps stays
// kept; a reference and a placeholder (3.4 step 3, not the prose step)
// are still no finding.
func TestPasswordPairProseFilterOff(t *testing.T) {
	on := packOnly(t)
	off, err := NewWith(Pack(), Options{NoProseFilter: true})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		text      string
		filterOn  bool // hit with the filter on (default)
		filterOff bool // hit with the filter off
	}{
		{"password = " + "tractor; restart api", false, true},
		{"password: " + "Sunflower42", true, true},
		{"pwd: " + "hunter-hunter-2 then restart", true, true},
		{"PASSWORD=${secret:gopass://acme/x}", false, false},
		{"the pass" + "word: changeme", false, false},
		{"pass" + "word: changeme then restart", false, false},
		{"pass" + "word: tractor then restart the service", false, true},
	}
	for _, c := range cases {
		if got := ruleIDs(on.Scan("body", c.text))[RulePasswordPair]; got != c.filterOn {
			t.Errorf("filter on, case want=%v: got %v", c.filterOn, got)
		}
		if got := ruleIDs(off.Scan("body", c.text))[RulePasswordPair]; got != c.filterOff {
			t.Errorf("filter off, case want=%v: got %v", c.filterOff, got)
		}
	}
}
