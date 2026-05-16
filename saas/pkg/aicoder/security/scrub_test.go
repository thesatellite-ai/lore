package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScan_AWSKey(t *testing.T) {
	s := NewScanner()
	cases := []string{
		"my key is AKIAIOSFODNN7EXAMPLE for staging",
		"AKIAIOSFODNN7EXAMPLE",
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
		"ghp_1234567890abcdefghijklmnopqrstuvwxyz12",
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
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJ...\n-----END RSA PRIVATE KEY-----"
	if !s.HasSecrets(pem) {
		t.Error("expected PEM match")
	}
	openssh := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNza..."
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
		"AKIAIOSFODNN7EXAMPLE": "AKIA****",
		"sk-1234567890abc":     "sk-1****",
		"abc":                  "****",
		"xy":                   "****",
		"":                     "****",
	}
	for in, want := range cases {
		if got := redact(in); got != want {
			t.Errorf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScan_PreviewIsRedacted(t *testing.T) {
	s := NewScanner()
	matches := s.Scan("AKIAIOSFODNN7EXAMPLE")
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
	in := "AWS=AKIAIOSFODNN7EXAMPLE GH=ghp_1234567890abcdefghijklmnopqrstuvwxyz12"
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
