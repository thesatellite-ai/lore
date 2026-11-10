package probe

import (
	"context"
	"os"
	"strings"
	"testing"
)

// fakeGHToken builds a GitHub-token-shaped value at run time (never a
// literal in the source, so the repo's leak scanner stays quiet).
func fakeGHToken() string { return "gh" + "o_" + strings.Repeat("Ab3", 12) }

// fakeKeyBlock is a PEM private key block built at run time.
func fakeKeyBlock() string {
	return "-----BEGIN OPENSSH PRIVATE " + "KEY-----\n" + strings.Repeat("QUFBQUFB", 8) + "\n-----END OPENSSH PRIVATE " + "KEY-----"
}

// TestRedactTextMatrix: every secret shape × where it appears.
func TestRedactTextMatrix(t *testing.T) {
	tok := fakeGHToken()
	pat := "github" + "_pat_" + strings.Repeat("Z9", 15)
	free := "s3cr3t-" + strings.Repeat("q", 12) // no known shape: only a registered secret catches it
	cases := []struct {
		name, in, gone string
		secrets        []string
	}{
		{"gh token in output", "Logged in\nToken: " + tok + "\n", tok, nil},
		{"fine-grained pat", "using " + pat, pat, nil},
		{"auth header", "Authorization: token " + tok, tok, nil},
		{"auth header json", `{"Authorization":"Bearer ` + free + `"}`, free, nil},
		{"bearer anywhere", "curl -H 'x: Bearer " + free + "'", free, nil},
		{"password pair", "pass" + "word=" + free + " user=mira", free, nil},
		{"json secret field", `{"client_secret": "` + free + `"}`, free, nil},
		{"url userinfo", "https://mira:" + free + "@github.com/x.git", free, nil},
		{"private key block", "key:\n" + fakeKeyBlock() + "\nafter", "QUFBQUFB", nil},
		{"unterminated key", "-----BEGIN RSA PRIVATE " + "KEY-----\nQUFBQUFBQUFB", "QUFBQUFBQUFB", nil},
		{"registered secret", "echo " + free, free, []string{free}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactText(c.in, c.secrets...)
			if strings.Contains(got, c.gone) {
				t.Fatalf("secret survived: %q", got)
			}
			if !strings.Contains(got, "redacted") {
				t.Fatalf("no mask in %q", got)
			}
		})
	}
	// Ordinary text is untouched (paths, key paths, ssh greetings).
	plain := "ssh -T -o BatchMode=yes -i ~/.ssh/id_personal git@github.com\nHi octo-personal! You've successfully authenticated"
	if got := RedactText(plain); got != plain {
		t.Fatalf("plain text changed: %q", got)
	}
}

// TestTraceRecordsRunRedacted: a probe whose output and stderr echo a fake
// token is recorded with its command, exit code and duration, the token
// nowhere in the Call; without a trace nothing is recorded.
func TestTraceRecordsRunRedacted(t *testing.T) {
	s := newSandbox(t)
	tok := fakeGHToken()
	free := "plainsecretvalue42"
	fakeBin(t, s.bin, "gh", `echo "Token: `+tok+`"; echo "oops `+free+`" >&2; exit 3`)
	if err := os.MkdirAll(s.home+"/work", 0o755); err != nil {
		t.Fatal(err)
	}
	p := s.prober(Options{})
	ctx, tr := WithTrace(context.Background())
	tr.AddSecret(free)
	ctx = WithLabel(ctx, "gh.login")
	res := p.run(ctx, cmdSpec{bin: "gh", args: []string{"api", "user"}, env: []string{"GH_PROMPT_DISABLED=1"}, dir: s.home + "/work", stdin: "x"})
	if res.exit != 3 {
		t.Fatalf("exit %d", res.exit)
	}
	calls := tr.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls %d", len(calls))
	}
	c := calls[0]
	if c.Command != "GH_PROMPT_DISABLED=1 gh api user" || c.Exit != 3 || c.Label != "gh.login" || c.Kind != CallExec || c.Dir != "~/work" || c.Stdin == "" {
		t.Fatalf("call %+v", c)
	}
	all := c.Command + c.Output + c.Error + c.Stdin
	if strings.Contains(all, tok) || strings.Contains(all, free) {
		t.Fatalf("secret in call: %+v", c)
	}
	if !strings.Contains(c.Output, "Token: "+RedactMask) || !strings.Contains(c.Output, "oops") {
		t.Fatalf("output %q", c.Output)
	}
	// A missing program is recorded with its error.
	p.run(ctx, cmdSpec{bin: "no-such-program-xyz"})
	if c := tr.Calls()[1]; !c.Missing || c.Error == "" {
		t.Fatalf("missing call %+v", c)
	}
	// No trace in ctx: run works, nothing kept anywhere.
	if r := p.run(context.Background(), cmdSpec{bin: "gh"}); r.exit != 3 {
		t.Fatalf("untraced exit %d", r.exit)
	}
	// Record (http callers) redacts headers and labels too.
	Secret(ctx, "short") // below minSecretLen: ignored, not an error
	Record(ctx, Call{Command: "GET https://api.github.com/user", Output: "Authorization: token " + tok, Error: "401 " + free})
	h := tr.Calls()[2]
	if h.Kind != CallHTTP || h.Label != "gh.login" || strings.Contains(h.Output+h.Error, tok) || strings.Contains(h.Error, free) {
		t.Fatalf("http call %+v", h)
	}
}

// TestTraceHomeAsTilde: a key path under the home folder is shown as ~/…
// so the copied command runs for the person; other paths stay quoted.
func TestTraceHomeAsTilde(t *testing.T) {
	p := New(Options{Home: "/home/mira"})
	got := p.displayCommand(cmdSpec{bin: "ssh", args: []string{"-T", "-o", "BatchMode=yes", "-i", "/home/mira/.ssh/id_personal", "git@github.com", "/tmp/a b"}})
	want := "ssh -T -o BatchMode=yes -i ~/.ssh/id_personal git@github.com '/tmp/a b'"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestTrimOutput: long output is cut to the line and byte bounds, marked.
func TestTrimOutput(t *testing.T) {
	long := strings.Repeat("line\n", TraceOutputLines+5)
	if got := TrimOutput(long); strings.Count(got, "\n") != TraceOutputLines || !strings.HasSuffix(got, truncMark) {
		t.Fatalf("lines: %q", got)
	}
	if got := TrimOutput(strings.Repeat("x", TraceOutputBytes*2)); len(got) > TraceOutputBytes+len(truncMark)+1 {
		t.Fatalf("bytes: %d", len(got))
	}
	if got := TrimOutput("ok\n\n"); got != "ok" {
		t.Fatalf("short: %q", got)
	}
}
