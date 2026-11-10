package gitleaks

import (
	"strings"
	"testing"
)

// Issue #10: lore v0.1.17's commit check flagged the built setup-page
// bundle (dist/assets/*.js) as password_pair. The match was a
// minified HTML input-type lookup map (each type name as a key with the
// value !0, comma-separated), all on one very long line. The 0.1.18 password_pair regex stops an
// unquoted value at `,` `;` `{` `}` so "!0" is two characters, below the
// six-character floor. These tests pin that: the minified shapes must
// pass, and a real literal password sitting inside the same minified
// line must still be caught (no path-wide exclusion of dist, D4).

// minifiedLine builds a one-line bundle around snippet, the way a
// bundler emits it: thousands of characters, no newlines, no spaces
// between tokens. Built at run time so no secret-shaped text sits in
// source.
func minifiedLine(snippet string) string {
	chunk := `function Qe(e,t){return e.map((n,r)=>t(n,r)).filter(Boolean)}var Ze=Qe;`
	pad := rep(chunk, 60)
	return `"use strict";` + pad + snippet + pad + "\n"
}

// passwordKey is split so the v0.1.17 hook does not refuse this file.
const passwordKey = "pass" + "word"

// inputTypeMap is the exact shape the v0.1.17 hook refused: React's
// supported-input-types table as the bundler prints it. Built at run
// time because the v0.1.17 hook refuses this very text in a test file.
func inputTypeMap() string {
	types := []string{"color", "date", "datetime", `"datetime-local"`, "email", "month", "number",
		passwordKey, "range", "search", "tel", "text", "time", "url", "week"}
	return "Pt={" + strings.Join(types, ":!0,") + ":!0};"
}

// TestPasswordPairMinifiedBundle: minified bundle code that only names
// password as a key, a type or a flag is not a finding; a literal
// password inside the same minified line is, quoted or not.
func TestPasswordPairMinifiedBundle(t *testing.T) {
	t.Parallel()
	// A literal built from parts so source never holds a password-shaped
	// string; mixed case, digits and a symbol keep it above the entropy floor.
	lit := "Hq7" + "vX2" + "!pz9"
	key := passwordKey
	cases := []struct {
		name    string
		snippet string
		hit     bool
	}{
		{"input type map (v0.1.17 false positive)", inputTypeMap(), false},
		{"input type attribute", `ie("input",{type:"password",autoComplete:"current-password",name:"password"});`, false},
		{"flag void 0", `var o={` + key + `:void 0,user:n};`, false},
		{"flag !1", `var o={` + key + `:!1,remember:!0};`, false},
		{"short identifier", `fetch(u,{body:JSON.stringify({user:n,` + key + `:p})});`, false},
		{"double-quoted literal", `var o={user:"alice",` + key + `:"` + lit + `"};`, true},
		{"single-quoted literal", `var o={user:'alice',` + key + `:'` + lit + `'};`, true},
		{"assignment with spaces", `var cfg;cfg.` + key + ` = "` + lit + `";`, true},
		{"unquoted literal", `x.` + key + `=` + lit + `;`, true},
	}
	for _, sc := range []struct {
		name string
		s    *Scanner
	}{{"pack", packOnly(t)}, {"defaults", full(t)}} {
		for _, c := range cases {
			fs := sc.s.Scan("dist/assets/index.js", minifiedLine(c.snippet))
			got := ruleIDs(fs)[RulePasswordPair]
			if got != c.hit {
				t.Errorf("%s/%s: password_pair hit=%v, want %v", sc.name, c.name, got, c.hit)
			}
			if c.hit {
				assertNoLeak(t, sc.name+"/"+c.name, lit, fs)
			}
		}
	}
}
