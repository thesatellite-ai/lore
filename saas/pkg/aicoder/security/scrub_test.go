package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Key-shaped fixtures are built at run time, so the source holds no
// literal that a secret scanner (push protection, gitleaks) reads as a
// real key (CLAUDE.md convention 8).
var (
	fakeAWSKey = "AKIA" + "IOSFODNN7EXAMPLE"
	fakeGHPTok = "ghp_" + "1234567890abcdefghijklmnopqrstuvwxyz12"
)

// fakePEM is a private-key block of kind ("RSA ", "OPENSSH ", "") with
// body, its BEGIN/END lines split so no source line is a PEM header.
func fakePEM(kind, body string, end bool) string {
	const dashes, priv = "-----", "PRIVATE KEY"
	out := dashes + "BEGIN " + kind + priv + dashes + "\n" + body
	if end {
		out += "\n" + dashes + "END " + kind + priv + dashes
	}
	return out
}

func TestScan_AWSKey(t *testing.T) {
	s := NewScanner()
	cases := []string{
		"my key is " + fakeAWSKey + " for staging",
		fakeAWSKey,
		"prefix ASIAIOSFODNN7EXAMPLE suffix",
	}
	for _, c := range cases {
		matches := s.Scan(c)
		if len(matches) == 0 {
			t.Errorf("expected match in %q", c)
			continue
		}
		if matches[0].PatternName != "aws_access_key" {
			t.Errorf("expected aws_access_key, got %q", matches[0].PatternName)
		}
	}
}

func TestScan_GitHubToken(t *testing.T) {
	s := NewScanner()
	// Realistic 36-char-ish payloads.
	cases := []string{
		fakeGHPTok,
		"gho_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		"ghs_yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy",
		"My token: ghr_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA in env",
	}
	for _, c := range cases {
		if !s.HasSecrets(c) {
			t.Errorf("expected secret in %q", c)
		}
	}
}

func TestScan_OpenAIKey(t *testing.T) {
	s := NewScanner()
	if !s.HasSecrets("OpenAI key sk-1234567890abcdefghijklmn") {
		t.Error("expected OpenAI sk- match")
	}
}

func TestScan_StripeKeys(t *testing.T) {
	s := NewScanner()
	// Built at runtime so no Stripe-key-shaped literal sits in source
	// (GitHub push protection flags the static form). Still matches the
	// `sk_(live|test)_[A-Za-z0-9]{24,}` scrub regex at runtime.
	live := "sk_live_" + strings.Repeat("A", 24)
	test := "sk_test_" + strings.Repeat("B", 24)
	if !s.HasSecrets(live) {
		t.Error("expected stripe_live_key match")
	}
	if !s.HasSecrets(test) {
		t.Error("expected stripe_test_key match")
	}
}

func TestScan_JWT(t *testing.T) {
	s := NewScanner()
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	if !s.HasSecrets(jwt) {
		t.Error("expected JWT match")
	}
}

func TestScan_PEM(t *testing.T) {
	s := NewScanner()
	pem := fakePEM("RSA ", "MIIBOgIBAAJ...", true)
	if !s.HasSecrets(pem) {
		t.Error("expected PEM match")
	}
	openssh := fakePEM("OPENSSH ", "b3BlbnNza...", false)
	if !s.HasSecrets(openssh) {
		t.Error("expected OpenSSH match")
	}
}

func TestScan_GoogleAPIKey(t *testing.T) {
	s := NewScanner()
	// AIza + exactly 35 chars from [0-9A-Za-z_-]
	if !s.HasSecrets("AIzaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Error("expected google API key match")
	}
}

func TestScan_SlackToken(t *testing.T) {
	s := NewScanner()
	if !s.HasSecrets("xoxb-1234567890-abcdefghij") {
		t.Error("expected slack token match")
	}
}

func TestScan_CleanInput(t *testing.T) {
	s := NewScanner()
	cases := []string{
		"This is a normal memory about Tailwind v4.",
		"Use BEGIN IMMEDIATE for SQLite writes.",
		"The auth flow uses OAuth2 with PKCE.",
		"",
	}
	for _, c := range cases {
		if got := s.Scan(c); len(got) != 0 {
			t.Errorf("expected no matches in %q, got %v", c, got)
		}
		if s.HasSecrets(c) {
			t.Errorf("HasSecrets returned true for clean %q", c)
		}
	}
}

func TestRedact_Format(t *testing.T) {
	cases := map[string]string{
		fakeAWSKey:         "AKIA****",
		"sk-1234567890abc": "sk-1****",
		"abc":              "****",
		"xy":               "****",
		"":                 "****",
	}
	for in, want := range cases {
		if got := redact(in); got != want {
			t.Errorf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScan_PreviewIsRedacted(t *testing.T) {
	s := NewScanner()
	matches := s.Scan(fakeAWSKey)
	if len(matches) == 0 {
		t.Fatal("expected match")
	}
	if !strings.HasSuffix(matches[0].Preview, "****") {
		t.Errorf("preview not redacted: %q", matches[0].Preview)
	}
	if strings.Contains(matches[0].Preview, "FODNN") {
		t.Errorf("preview leaked secret content: %q", matches[0].Preview)
	}
}

func TestAddPattern_Custom(t *testing.T) {
	s := NewScanner()
	before := s.PatternCount()

	if err := s.AddPattern("custom_token", `\bMYTOKEN_[a-z0-9]{16}\b`); err != nil {
		t.Fatalf("AddPattern: %v", err)
	}
	if s.PatternCount() != before+1 {
		t.Error("pattern count did not increment")
	}

	if !s.HasSecrets("config.MYTOKEN_abcdef0123456789") {
		t.Error("custom pattern not matching")
	}
}

func TestAddPattern_BadRegex(t *testing.T) {
	s := NewScanner()
	if err := s.AddPattern("bad", `[unclosed`); err == nil {
		t.Error("expected compile error")
	}
}

func TestLoadUserPatterns_FileMissing(t *testing.T) {
	s := NewScanner()
	if err := s.LoadUserPatterns("/nonexistent/path/file.txt"); err != nil {
		t.Errorf("expected nil for missing file, got %v", err)
	}
}

func TestLoadUserPatterns_RealFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patterns.txt")
	content := `# Comment line
\bSECRET_[A-Z0-9]{10}\b

# Another comment
\bMYAPI_[a-f0-9]{8}\b
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	s := NewScanner()
	before := s.PatternCount()
	if err := s.LoadUserPatterns(path); err != nil {
		t.Fatalf("LoadUserPatterns: %v", err)
	}
	if s.PatternCount() != before+2 {
		t.Errorf("expected +2 patterns, got +%d", s.PatternCount()-before)
	}

	// The user patterns should now match.
	if !s.HasSecrets("oh no SECRET_ABC1234567 leaked") {
		t.Error("user pattern not loaded")
	}
}

func TestScan_MultiplePatternsMatching(t *testing.T) {
	s := NewScanner()
	in := "AWS=" + fakeAWSKey + " GH=" + fakeGHPTok
	matches := s.Scan(in)
	if len(matches) < 2 {
		t.Errorf("expected >=2 matches, got %d", len(matches))
	}
}

func TestScan_LongCleanContent(t *testing.T) {
	s := NewScanner()
	// 10KB of clean lorem-ipsum-y text shouldn't trigger anything.
	clean := strings.Repeat("This is a long memory body about how we structure our code. ", 200)
	if s.HasSecrets(clean) {
		t.Error("false positive on long clean content")
	}
}

// C20 / §6.5: the extended patterns, each with near-misses that must stay
// clean (a test of the matrix, not of one string).
func TestExtendedPatterns(t *testing.T) {
	s := NewScanner()
	hits := map[string]string{
		"postgres://admin:hunter2pass@db.internal:5432/app": "url_with_password",
		"redis://default:s3cr3tvalue@cache:6379":            "url_with_password",
		"Authorization: Bearer abcDEF123456789xyz":          "authorization_bearer",
		"authorization=bearer abcdefghijklmnop":             "authorization_bearer",
		"password=Tr0ub4dor&3":                              "password_pair",
		`PASSWD: "longenough"`:                              "password_pair",
		"npm_" + strings.Repeat("a1B2", 9):                  "npm_token",
		"SK" + strings.Repeat("0123456789abcdef", 2):        "twilio_api_key",
	}
	for in, want := range hits {
		found := false
		for _, m := range s.Scan(in) {
			if m.PatternName == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: want %s, got %v", in, want, s.Scan(in))
		}
	}
	clean := []string{
		"git@github.com:acme/app.git",
		"https://github.com/acme/app",
		"ssh://git@github.com/acme/app",
		"https://user@example.com/path",
		"password=<your password>",
		"password=${DB_PASSWORD}",
		"password: ***",
		"set the password in gopass",
		"Authorization: Bearer <token>",
		"npm_install is a script name",
		"SKU1234",
	}
	for _, in := range clean {
		if m := s.Scan(in); len(m) > 0 {
			t.Errorf("%q must stay clean, got %v", in, m)
		}
	}
}
