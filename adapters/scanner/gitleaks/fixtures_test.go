package gitleaks

import (
	"strings"
	"testing"
)

// S-4 fixtures: every shape is built at run time (CLAUDE.md convention 8),
// so no key-shaped literal sits in the repo.

// fixtureBody is a deterministic token body of n characters: letters and
// digits outside the hex range mixed in, so no window of it can appear in
// a sha256 preview by chance.
func fixtureBody(n int) string {
	const set = "gH7kM2nP9qR4sT6vW8xYz3JhLtNrQwZ5"
	var b strings.Builder
	for i := range n {
		b.WriteByte(set[(i*7+3)%len(set)])
	}
	return b.String()
}

// nonSecretFixtures are real-world texts that hold no credential: lines
// of .env.example files, example configs, code that reads a secret,
// schema fields, docs snippets, gopass entry names and test fixtures.
func nonSecretFixtures() []string {
	pw := "pass" + "word"
	P := strings.ToUpper(pw)
	Pw := "Pass" + "word"
	x := strings.Repeat
	curl := "cu" + `rl -H "Author` + "ization: " + "Bear" + "er " // joined at run time
	return []string{
		// .env.example
		"DATABASE_URL=postgres://user:" + pw + "@localhost:5432/app",
		"DATABASE_URL=postgres:" + "//postgres:" + "postgres@localhost:5432/app",
		"DATABASE_URL=postgresql:" + "//user:" + "pass@localhost/db",
		"REDIS_URL=redis://:" + pw + "@localhost:6379/0",
		"DATABASE_URL=mysql:" + "//root:" + "root@127.0.0.1:3306/app",
		"AMQP_URL=amqp:" + "//guest:" + "guest@localhost:5672/",
		"DB_" + P + "=changeme",
		"DB_" + P + "=your-db-" + pw,
		"DB_" + P + "=",
		"OPENAI_API_KEY=sk-" + x("x", 40),
		"OPENAI_API_KEY=sk-...",
		"OPENAI_API_KEY=your-openai-api-key",
		"ANTHROPIC_API_KEY=sk-ant-" + x("x", 30),
		"STRIPE_SECRET_KEY=sk_" + "test_" + x("x", 24),
		"STRIPE_SECRET_KEY=sk_" + "live_" + x("X", 24),
		"GITHUB_TOKEN=ghp_" + x("x", 36),
		"GITHUB_TOKEN=ghp_" + x("X", 36),
		"AWS_SECRET_ACCESS_KEY=" + "wJalrXUtnFEMI" + "/K7MDENG/" + "bPxRfiCY" + "EXAMPLEKEY",
		"AWS_ACCESS_KEY_ID=AKIA" + x("X", 16),
		"SLACK_BOT_TOKEN=xoxb-" + x("x", 12),
		"SLACK_BOT_TOKEN=xo" + "xb-your-bot-token",
		"GOOGLE_API_KEY=AIza" + x("X", 35),
		"NPM_TOKEN=npm_" + x("x", 36),
		"SECRET_KEY=replace-me",
		"JWT_SECRET=change-this-in-production",
		// example configs
		pw + `: "changeme"`,
		pw + `: "example"`,
		pw + `: "placeholder"`,
		pw + `: "your-` + pw + `-here"`,
		pw + `: "YOUR_` + P + `"`,
		pw + `: REPLACE_ME`,
		pw + `: "<redacted>"`,
		pw + `: "[REDACTED]"`,
		pw + `: ""`,
		pw + `: null`,
		pw + `: ~`,
		"  " + pw + `: !vault |`,
		pw + `: ENC[AES256_GCM,data:abc,iv:def,tag:ghi,type:str]`,
		pw + `: "op://Private/db/` + pw + `"`,
		// code and schemas
		pw + ` = os.Getenv("DB_` + P + `")`,
		pw + `: process.env.DB_` + P + `,`,
		pw + `: config.Database.` + Pw,
		pw + ` := r.FormValue("` + pw + `")`,
		"self." + pw + " = " + pw,
		pw + `= getpass.getpass()`,
		pw + `: string`,
		pw + `: String!`,
		pw + `: str`,
		pw + ` TEXT NOT NULL`,
		Pw + ` string ` + "`json:\"" + pw + "\"`",
		`if ` + pw + ` == "" {`,
		pw + `_hash: bcrypt`,
		"min_" + pw + "_length: 12",
		// docs snippets
		curl + `<token>" https://api.example.com`,
		curl + `$TOKEN" https://api.example.com`,
		curl + `${API_TOKEN}" https://api.example.com`,
		curl + `YOUR_ACCESS_TOKEN" https://api.example.com`,
		strings.ReplaceAll(curl, `"`, "'") + "your-api-token' https://api.example.com",
		"Author" + "ization: " + "Bear" + "er " + x("x", 32),
		"git clone https://<token>@github.com/org/repo.git",
		"git clone https://${GITHUB_TOKEN}@github.com/org/repo.git",
		"git clone https://x-access-token:${GITHUB_TOKEN}@github.com/org/repo.git",
		"git clone https://user:<" + pw + ">@git.example.com/repo.git",
		"Set the " + pw + " in your shell, then run the command.",
		"Use a strong " + pw + ": at least twelve characters long.",
		"Your " + pw + " is stored in the keychain.",
		"-----BEGIN PUBLIC KEY-----",
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGtl user@host",
		// gopass entry names
		"gopass show -o team/db/prod-" + pw,
		"gopass show -o team/github/token",
		"gopass insert team/api/stripe-secret-key",
		"DB_" + P + "=$(gopass show -o team/db/prod)",
		"token: gopass:team/api/token",
		pw + ": gopass://team/db/prod",
		// test fixtures
		"func TestLogin(t *testing.T) { " + pw + " := \"\" }",
		"user@example.com",
		"sha256:" + x("ab", 32),
		"commit " + x("0f", 20),
	}
}

// secretFixture is a text holding one credential and the value that must
// never be printed.
type secretFixture struct{ text, value string }

// secretFixtures are real secret shapes, several placed next to the
// non-secret shapes above (a real password in an example-looking config,
// a token with a mask-like start) so the exemptions stay narrow.
func secretFixtures() []secretFixture {
	pw := "pass" + "word"
	b := fixtureBody
	real := "Zq7" + b(13)
	f := func(text, value string) secretFixture { return secretFixture{text, value} }
	aws := "AKIA" + strings.ToUpper(b(16))
	// URLs are joined with spaces round the value so no URL with a
	// password sits on one source line.
	appURL := "DATABASE_URL=postgres://app:" + real + "@db.internal:5432/app"
	localURL := "DATABASE_URL=postgres://postgres:" + real + "@localhost:5432/app"
	cloneURL := "git clone https://ghp_" + b(36) + "@github.com/org/repo.git"
	return []secretFixture{
		// AWS's documented example key stays a hit: the gates' tests use it
		// as the stand-in real key.
		f("AWS_ACCESS_KEY_ID=AKIA"+"IOSFODNN7"+"EXAMPLE", "IOSFODNN7"+"EXAMPLE"),
		f("AWS_ACCESS_KEY_ID="+aws, aws),
		f("GITHUB_TOKEN=ghp_"+b(36), "ghp_"+b(36)),
		f("GITHUB_TOKEN=github_pat_"+b(40), "github_pat_"+b(40)),
		f("token: ghp_xxxx"+b(32), "ghp_xxxx"+b(32)),
		f("OPENAI_API_KEY=sk-proj-"+b(48), "sk-proj-"+b(48)),
		f("ANTHROPIC_API_KEY=sk-ant-api03-"+b(60), "sk-ant-api03-"+b(60)),
		f("STRIPE_SECRET_KEY=sk_"+"live_"+b(24), "sk_"+"live_"+b(24)),
		f("SLACK_BOT_TOKEN=xoxb-"+b(24), "xoxb-"+b(24)),
		f("GOOGLE_API_KEY=AIza"+b(35), "AIza"+b(35)),
		f("NPM_TOKEN=npm_"+b(36), "npm_"+b(36)),
		f("SHOPIFY_TOKEN=shpat_"+strings.Repeat("a1b2c3d4", 4), "shpat_"+strings.Repeat("a1b2c3d4", 4)),
		f("eyJ"+"hbGciOiJIUzI1NiJ9"+".eyJ"+"zdWIiOiIxMjM0NTY3ODkwIn0"+"."+b(43), "eyJ"+"hbGciOiJIUzI1NiJ9"),
		f("-----BEGIN RSA PRIVATE "+"KEY-----\n"+b(64)+"\n", "PRIVATE "+"KEY-----"),
		f("curl -H \"Authorization: Bearer "+b(40)+"\" https://api.example.com", b(40)),
		f(appURL, real),
		f(localURL, real),
		f(pw+`: "`+real+`"`, real),
		f(pw+`: "your-`+real+`"`, real),
		f(pw+`: `+real, real),
		f(pw+`: Admin.2024x`, "Admin.2024x"),
		f(pw+`: "my.config.value"`, "my.config.value"),
		f(cloneURL, "ghp_"+b(36)),
	}
}

// TestFixtureCorpus (S-4): the real-world non-secrets pass every rule;
// every real secret shape is caught; no finding carries the value.
// Break-verified: with isPlaceholderPhrase, the token-mask regex,
// passIsUser or unquotedNonSecret off, non-secret lines are flagged.
func TestFixtureCorpus(t *testing.T) {
	t.Parallel()
	s := full(t)
	for _, c := range nonSecretFixtures() {
		if fs := s.Scan("body", c); len(fs) > 0 {
			t.Errorf("non-secret flagged by %v: %q", ruleIDs(fs), c)
		}
	}
	for i, c := range secretFixtures() {
		fs := s.Scan("body", c.text)
		if len(fs) == 0 {
			t.Errorf("secret fixture %d (%d bytes) not caught", i, len(c.text))
			continue
		}
		assertNoLeak(t, "secret fixture", c.value, fs)
	}
}
