package gitleaks

import (
	"strings"
	"sync"
	"testing"

	"github.com/thesatellite-ai/lore/logic/scan"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/schema"
)

// Fixtures are built at runtime so no key-shaped literal sits in source
// (push protection). Failure messages print rule ids and lengths, never
// fixture values.

func rep(s string, n int) string { return strings.Repeat(s, n) }

func packOnly(t *testing.T) *Scanner {
	t.Helper()
	s, err := New(Pack())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func full(t *testing.T, rows ...RuleRow) *Scanner {
	t.Helper()
	base, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	rules, err := Rules(base, rows)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(rules)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ruleIDs(fs []scan.Finding) map[string]bool {
	m := map[string]bool{}
	for _, f := range fs {
		m[f.RuleID] = true
	}
	return m
}

// assertNoLeak fails if any finding exposes more than the preview cap of
// the secret (the Preview may show the first min(8, len/4) runes only).
func assertNoLeak(t *testing.T, name, secret string, fs []scan.Finding) {
	t.Helper()
	for _, f := range fs {
		if strings.Contains(f.Preview, secret) || strings.Contains(f.RuleID, secret) || strings.Contains(f.Field, secret) {
			t.Errorf("%s: finding of %s carries the fixture", name, f.RuleID)
		}
		if n := len([]rune(strings.TrimSuffix(f.Preview, "…"))); n > scan.PreviewMax || n > f.Length/scan.PreviewDivisor {
			t.Errorf("%s: preview of %s shows %d chars of %d", name, f.RuleID, n, f.Length)
		}
	}
}

// TestPackRules: every lore-pack rule hits its fixture and misses a
// near-miss; previews never leak the secret.
func TestPackRules(t *testing.T) {
	t.Parallel()
	hex := "0123456789abcdef"
	alnum := "Ab3dE6gH9jK2mN5pQ8sT1vW4yZ7"
	cases := []struct {
		rule, hit, miss string
	}{
		{RuleAWSAccessKey, "AKIA" + "Q7Z2X9KL0PM4N3B8", "AKIA" + "Q7Z2X9KL"},
		{RuleGitHubToken, "ghp_" + rep("aB3", 12), "ghp_" + rep("aB3", 5)},
		{RuleJWT, "eyJ" + "hbGciOi" + ".eyJ" + "zdWIiOi" + ".sig" + "nature_x", "eyJ" + "hbGciOi only"},
		{RuleStripeLiveKey, "sk_" + "live_" + rep("A1", 12), "sk_" + "live_" + "short"},
		{RuleStripeTestKey, "sk_" + "test_" + rep("B2", 12), "sk_" + "test_" + "short"},
		{RuleModelKey, "sk-" + alnum, "sk-" + "short"},
		{RuleSlackToken, "xox" + "b-" + "1234567890-abcdef", "xox" + "b-" + "12"},
		{RuleGoogleAPIKey, "AIza" + rep("Xy9_A", 7), "AIza" + rep("Xy9", 3)},
		{RulePEMPrivateKey, "-----BEGIN " + "RSA PRIVATE KEY-----", "-----BEGIN " + "PUBLIC KEY-----"},
		{RuleShopifyToken, "shp" + "at_" + rep(hex, 2), "shp" + "at_" + hex},
		{RuleNPMToken, "npm_" + rep("Ab3", 12), "npm_" + "Ab3"},
		{RuleTwilioKey, "SK" + rep(hex, 2), "SK" + hex},
		{RuleAuthorizationBearer, "Authorization: Bearer " + alnum, "Authorization: Bearer <token>"},
		{RulePasswordPair, "password=" + "Zq8#Lm2!Vx", "password=" + "${DB_PASSWORD}"},
		{RuleURLWithPassword, "postgres://app:" + "Zq8Lm2Vx" + "@db.local:5432/x", "postgres://app:" + "${PW}" + "@db.local/x"},
		{RuleURLUserinfoID, "https://" + "glpat-" + rep("Ab3", 7) + "@git.acme.example/a/b", "https://" + "mira" + "@git.acme.example/a/b"},
	}
	s := packOnly(t)
	covered := map[string]bool{}
	for _, c := range cases {
		covered[c.rule] = true
		got := s.Scan("body", "note: "+c.hit+" end")
		if !ruleIDs(got)[c.rule] {
			t.Errorf("%s: fixture (%d chars) not detected", c.rule, len(c.hit))
		}
		assertNoLeak(t, c.rule, c.hit, got)
		if ruleIDs(s.Scan("body", "note: "+c.miss+" end"))[c.rule] {
			t.Errorf("%s: near-miss (%d chars) detected", c.rule, len(c.miss))
		}
	}
	for _, r := range Pack() {
		if !covered[r.ID] {
			t.Errorf("pack rule %s has no test case", r.ID)
		}
	}
}

// TestPasswordPairStrict: a low-entropy word is not a password value.
func TestPasswordPairStrict(t *testing.T) {
	t.Parallel()
	if ruleIDs(packOnly(t).Scan("body", "password = aaaaaaaaaaaa"))[RulePasswordPair] {
		t.Fatal("low-entropy value detected")
	}
}

// TestGitleaksDefaultHits: a rule only gitleaks ships fires through the
// gitleaks engine, with a located, masked finding.
func TestGitleaksDefaultHits(t *testing.T) {
	t.Parallel()
	// gitleaks "gitlab-pat": glpat- + 20 chars; lore's pack has no GitLab rule.
	secret := "glpat-" + "Xk9Lm2Qp7Rt4Vw8Yz1Ab"
	text := "line one\nexport TOKEN=" + secret + "\n"
	got := full(t).Scan("body", text)
	var hit *scan.Finding
	for i := range got {
		if got[i].RuleID == "gitlab-pat" {
			hit = &got[i]
		}
	}
	if hit == nil {
		t.Fatalf("gitlab-pat not found; rules hit: %v", ruleIDs(got))
	}
	if hit.Line != 2 || hit.Offset != strings.Index(text, secret) || hit.Length != len(secret) || hit.MatchHash != scan.HashMatch(secret) {
		t.Errorf("location line=%d off=%d len=%d", hit.Line, hit.Offset, hit.Length)
	}
	assertNoLeak(t, "gitlab-pat", secret, got)
}

// TestRowOff: turning a rule off by row removes its hits (lore pack and
// gitleaks default alike).
func TestRowOff(t *testing.T) {
	t.Parallel()
	gl := "glpat-" + "Xk9Lm2Qp7Rt4Vw8Yz1Ab"
	aws := "AKIA" + "Q7Z2X9KL0PM4N3B8"
	text := gl + " " + aws
	on := ruleIDs(full(t).Scan("body", text))
	if !on["gitlab-pat"] || !on[RuleAWSAccessKey] {
		t.Fatalf("baseline missing: %v", on)
	}
	off := ruleIDs(full(t,
		RuleRow{RuleID: "gitlab-pat", State: schema.SecretRuleOff},
		RuleRow{RuleID: RuleAWSAccessKey, State: schema.SecretRuleOff},
	).Scan("body", text))
	if off["gitlab-pat"] || off[RuleAWSAccessKey] {
		t.Fatalf("off rules still hit: %v", off)
	}
}

// TestRowChanged: a changed rule uses the new regex (and no longer the
// old one); a changed gitleaks rule moves to lore's engine.
func TestRowChanged(t *testing.T) {
	t.Parallel()
	aws := "AKIA" + "Q7Z2X9KL0PM4N3B8"
	s := full(t,
		RuleRow{RuleID: RuleAWSAccessKey, State: schema.SecretRuleChanged, Regex: `ZZTOKEN-[0-9]{6}`},
		RuleRow{RuleID: "gitlab-pat", State: schema.SecretRuleChanged, Regex: `GLX-[0-9]{4}`, Keywords: []string{"glx-"}},
	)
	got := ruleIDs(s.Scan("body", aws+" ZZTOKEN-"+"123456 GLX-"+"1234 glpat-"+"Xk9Lm2Qp7Rt4Vw8Yz1Ab"))
	if !got[RuleAWSAccessKey] || !got["gitlab-pat"] {
		t.Fatalf("new regexes missed: %v", got)
	}
	if ruleIDs(s.Scan("body", aws))[RuleAWSAccessKey] {
		t.Fatal("old aws regex still active")
	}
	if ruleIDs(s.Scan("body", "glpat-"+"Xk9Lm2Qp7Rt4Vw8Yz1Ab"))["gitlab-pat"] {
		t.Fatal("old gitleaks regex still active")
	}
}

// TestRulesBuilder: pure builder keeps order, adds new rules, refuses bad rows.
func TestRulesBuilder(t *testing.T) {
	t.Parallel()
	base := Pack()
	out, err := Rules(base, []RuleRow{
		{RuleID: "acme_token", State: schema.SecretRuleOn, Regex: `ACME-[0-9]{8}`},
		{RuleID: "not_shipped", State: schema.SecretRuleOff},
		{RuleID: RuleJWT, State: schema.SecretRuleOn},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(base)+1 || out[len(out)-1].ID != "acme_token" || out[len(out)-1].Source != scan.SourceStore {
		t.Fatalf("builder output %d rules", len(out))
	}
	if len(base) != len(Pack()) || base[0].ID != Pack()[0].ID {
		t.Fatal("base mutated")
	}
	for name, rows := range map[string][]RuleRow{
		"no id":         {{State: schema.SecretRuleOn, Regex: "x"}},
		"bad state":     {{RuleID: "x", State: "maybe", Regex: "x"}},
		"dup":           {{RuleID: "x", State: schema.SecretRuleOn, Regex: "x"}, {RuleID: "x", State: schema.SecretRuleOff}},
		"changed empty": {{RuleID: RuleJWT, State: schema.SecretRuleChanged}},
		"on existing":   {{RuleID: RuleJWT, State: schema.SecretRuleOn, Regex: "x"}},
		"new no regex":  {{RuleID: "x", State: schema.SecretRuleOn}},
	} {
		if _, err := Rules(base, rows); !errs.Has(err, errs.ConfigInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := New([]scan.Rule{{ID: "nope", Regex: "x", Source: scan.SourceGitleaks}}); !errs.Has(err, errs.ConfigInvalid) {
		t.Errorf("unknown gitleaks id: err = %v", err)
	}
}

// TestSizeGate: above GitleaksMaxBytes gitleaks does not run, the report
// says so, and lore's pack still scans the whole text.
func TestSizeGate(t *testing.T) {
	t.Parallel()
	gl := "glpat-" + "Xk9Lm2Qp7Rt4Vw8Yz1Ab"
	aws := "AKIA" + "Q7Z2X9KL0PM4N3B8"
	s := full(t)
	small := s.ScanReport("body", gl+" "+aws)
	if small.Degraded || small.Note != "" {
		t.Fatal("small text reported degraded")
	}
	big := s.ScanReport("body", rep("x ", GitleaksMaxBytes/2)+gl+" "+aws)
	if !big.Degraded || big.Note != NoteOversize {
		t.Fatal("gate fallback not reported")
	}
	got := ruleIDs(big.Findings)
	if !got[RuleAWSAccessKey] {
		t.Fatal("lore pack skipped above the gate")
	}
	if got["gitlab-pat"] {
		t.Fatal("gitleaks ran above the gate")
	}
	if packOnly(t).ScanReport("body", rep("x", GitleaksMaxBytes+1)).Degraded {
		t.Fatal("scanner without gitleaks rules reported degraded")
	}
}

// TestAllowFilter: a secret_allows entry built from a finding removes it.
func TestAllowFilter(t *testing.T) {
	t.Parallel()
	got := packOnly(t).Scan("body", "AKIA"+"Q7Z2X9KL0PM4N3B8")
	if len(got) != 1 {
		t.Fatalf("findings %d", len(got))
	}
	a := scan.Allow{Field: got[0].Field, RuleID: got[0].RuleID, MatchHash: got[0].MatchHash}
	if len(scan.Filter(a.RowID, got, []scan.Allow{a})) != 0 {
		t.Fatal("allowed finding kept")
	}
}

// TestErrorsNeverCarryRegexOrValue: a bad store regex error names the rule only.
func TestErrorsNeverCarryRegexOrValue(t *testing.T) {
	t.Parallel()
	secretish := "sk-" + rep("Q", 24)
	_, err := New([]scan.Rule{{ID: "bad", Regex: "(" + secretish, Source: scan.SourceStore}})
	if err == nil || strings.Contains(err.Error(), secretish) {
		t.Fatalf("error missing or leaks the regex text (len %d)", len(secretish))
	}
}

// TestConcurrent: one scanner is safe under parallel callers (-race).
func TestConcurrent(t *testing.T) {
	t.Parallel()
	s := full(t)
	text := "glpat-" + "Xk9Lm2Qp7Rt4Vw8Yz1Ab AKIA" + "Q7Z2X9KL0PM4N3B8"
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if len(s.Scan("body", text)) < 2 {
				t.Error("concurrent scan lost findings")
			}
		}()
	}
	wg.Wait()
}

// TestPasswordPairProseFilter pins the 3.4 table (D10).
func TestPasswordPairProseFilter(t *testing.T) {
	s := packOnly(t)
	cases := []struct {
		text string
		hit  bool
	}{
		{"change that password: credentials shouldn't sit in Slack history", false},
		{"password: " + "Sunflower42", true},
		{"password: " + "tractor", true},
		{`password: "` + "tractor and seeds" + `"`, true},
		{"password = " + "tractor; restart api", false},
		{"the password: changeme", false},
		{"PASSWORD=${secret:gopass://acme/x}", false},
		{"pwd: " + "hunter-hunter-2 then restart", true},
	}
	for _, c := range cases {
		if got := ruleIDs(s.Scan("body", c.text))[RulePasswordPair]; got != c.hit {
			t.Errorf("password_pair on case hit=%v: got %v", c.hit, got)
		}
	}
}

// TestURLTokenUserRule pins 14.6/D26: a token user name is a finding, a
// plain user, ssh forms and a clean URL are not.
func TestURLTokenUserRule(t *testing.T) {
	s := packOnly(t)
	tok := "ghp_" + strings.Repeat("C", 36)
	if !ruleIDs(s.Scan("body", "clone https://"+tok+"@github.com/acme/hub.git"))[RuleURLUserinfoID] {
		t.Fatalf("a token user must be a finding")
	}
	for _, clean := range []string{"https://mira@github.com/acme/hub", "git@github.com:acme/hub.git", "ssh://git@github.com/acme/hub", "https://github.com/acme/hub"} {
		got := ruleIDs(s.Scan("body", clean))
		if got[RuleURLUserinfoID] || got[RuleURLWithPassword] {
			t.Errorf("%q must not be a URL credential finding", clean)
		}
	}
}

// TestRepoGitleaksAllow pins D8: only allow regexes and stopwords are
// taken from .gitleaks.toml, rules in it are ignored, a target rule
// scopes an entry, and a bad file fails closed.
func TestRepoGitleaksAllow(t *testing.T) {
	file := []byte(`
[[rules]]
id = "mine"
regex = '''zzz'''

[[allowlists]]
regexes = ['''^sk_live_FIXTURE''']

[[allowlists]]
targetRules = ["github_token"]
stopwords = ["docsonly"]
`)
	allow, err := ParseRepoGitleaks(file)
	if err != nil || len(allow.Entries) != 2 {
		t.Fatalf("parse: %+v %v", allow, err)
	}
	s, err := NewWith(Pack(), Options{RepoAllow: allow})
	if err != nil {
		t.Fatal(err)
	}
	stripe := "sk_live_FIXTURE" + strings.Repeat("A", 20)
	if ruleIDs(s.Scan("body", stripe))[RuleStripeLiveKey] {
		t.Errorf("an allow regex must drop the finding")
	}
	other := "sk_live_" + strings.Repeat("B", 24)
	if !ruleIDs(s.Scan("body", other))[RuleStripeLiveKey] {
		t.Errorf("a non-matching value must still be found")
	}
	gh := "ghp_docsonly" + strings.Repeat("D", 30)
	if ruleIDs(s.Scan("body", gh))[RuleGitHubToken] {
		t.Errorf("a stopword must drop its target rule's finding")
	}
	if _, err := ParseRepoGitleaks([]byte("[[allowlists]]\nregexes = ['''(''']\n")); !errs.Has(err, errs.ConfigInvalid) {
		t.Errorf("a bad regex must fail closed: %v", err)
	}
}

// TestDetectorSettings pins D7 and the decode depth (3.1/3.6).
func TestDetectorSettings(t *testing.T) {
	base, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	if s.detector == nil || !s.detector.IgnoreGitleaksAllow || s.detector.MaxDecodeDepth != 3 {
		t.Fatalf("detector settings: %+v", s.detector)
	}
}

// TestLoreIDNotASecret: a lore row id that a generic rule captures (the
// OV40 measured case, "task key tsk_<hex>.") is no finding; the same
// shape with an unknown prefix or a non-UUIDv7 body still is.
func TestLoreIDNotASecret(t *testing.T) {
	t.Parallel()
	id := "tsk_" + "01a1634ca3ce7b69a304438d07a0cb19"
	if !isLoreID(id) || !isLoreID(id+".") {
		t.Fatal("a lore id is not recognised")
	}
	for _, s := range []string{"zzq_" + "01a1634ca3ce7b69a304438d07a0cb19", id + "x", "tsk_" + rep("Q", 32)} {
		if isLoreID(s) {
			t.Errorf("len %d shape taken for a lore id", len(s))
		}
	}
	text := "the task key = " + id + ".\n"
	for _, f := range full(t).Scan("body", text) {
		t.Errorf("lore id flagged by %s", f.RuleID)
	}
}
