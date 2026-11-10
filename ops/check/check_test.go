package check

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// fakeChecker records what the ops pass on; blockText blocks any body
// that holds it.
type fakeChecker struct {
	leaks     []LeaksIn
	texts     []TextIn
	blockText string
	leakErr   error
}

func (c *fakeChecker) CheckLeaks(_ context.Context, in LeaksIn) (LeaksOut, error) {
	c.leaks = append(c.leaks, in)
	if c.leakErr != nil {
		return LeaksOut{}, c.leakErr
	}
	return LeaksOut{Message: "✓ ok", Mode: "ignored: the op sets it"}, nil
}

func (c *fakeChecker) CheckText(_ context.Context, in TextIn) error {
	c.texts = append(c.texts, in)
	if c.blockText != "" && strings.Contains(string(in.Body), c.blockText) {
		return errs.New(errs.LeakBlocked, "blocked: "+in.Where)
	}
	return nil
}

func invoke(t *testing.T, c Checker, name string, d op.ArgsDecoder) (op.Result, error) {
	t.Helper()
	for _, e := range Ops(c) {
		if e.Spec().Name != name {
			continue
		}
		in, err := e.Decode(d)
		if err != nil {
			return op.Result{}, err
		}
		return e.Invoke(context.Background(), in)
	}
	t.Fatalf("no op %s", name)
	return op.Result{}, nil
}

func flags(kv ...string) map[string][]string {
	m := map[string][]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = append(m[kv[i]], kv[i+1])
	}
	return m
}

func TestOpsAreLocalAndReadOnly(t *testing.T) {
	t.Parallel()
	for _, e := range Ops(&fakeChecker{}) {
		s := e.Spec()
		if !s.Local || s.Permission != op.PermRead || s.Access != op.AccessRead {
			t.Errorf("%s: Local %v perm %v access %v; a hook must never open the engine or write (P3-CFG-1)", s.Name, s.Local, s.Permission, s.Access)
		}
	}
}

// TestLeaksModes: every flag combination maps to one mode, or is refused
// before the checker runs.
func TestLeaksModes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		flags map[string][]string
		mode  string
		code  errs.Code
	}{
		{name: "bare", mode: ModeCommit},
		{name: "staged", flags: flags("staged", "true"), mode: ModeCommit},
		{name: "commit-msg", flags: flags("commit-msg", ".git/COMMIT_EDITMSG"), mode: ModeCommit},
		{name: "staged + commit-msg", flags: flags("staged", "true", "commit-msg", "m"), mode: ModeCommit},
		{name: "ci", flags: flags("ci", "true"), mode: ModeCI},
		{name: "ci + explain", flags: flags("ci", "true", "explain", "true"), mode: ModeCI},
		{name: "as-client", flags: flags("as-client", "true"), mode: ModeAsClient},
		{name: "rows", flags: flags("rows", "true"), mode: ModeRows},
		{name: "rows + explain", flags: flags("rows", "true", "explain", "true"), mode: ModeRows},
		{name: "ci + as-client", flags: flags("ci", "true", "as-client", "true"), code: errs.InvalidInput},
		{name: "rows + ci", flags: flags("rows", "true", "ci", "true"), code: errs.InvalidInput},
		{name: "rows + as-client", flags: flags("rows", "true", "as-client", "true"), code: errs.InvalidInput},
		{name: "rows + staged", flags: flags("rows", "true", "staged", "true"), code: errs.InvalidInput},
		{name: "rows + commit-msg", flags: flags("rows", "true", "commit-msg", "m"), code: errs.InvalidInput},
	}
	for _, c := range cases {
		fc := &fakeChecker{}
		res, err := invoke(t, fc, OpLeaks, op.ArgsDecoder{Flags: c.flags})
		if c.code != "" {
			if errs.CodeOf(err) != c.code || len(fc.leaks) != 0 {
				t.Errorf("%s: err %v (want %s), checker ran %d times", c.name, err, c.code, len(fc.leaks))
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		b, _ := res.JSON()
		if !strings.Contains(string(b), `"mode":"`+c.mode+`"`) {
			t.Errorf("%s: %s, want mode %s", c.name, b, c.mode)
		}
		if len(fc.leaks) != 1 || fc.leaks[0].Mode() != c.mode {
			t.Errorf("%s: checker got %+v", c.name, fc.leaks)
		}
	}
}

func TestLeaksBlockPassesThrough(t *testing.T) {
	t.Parallel()
	fc := &fakeChecker{leakErr: errs.New(errs.LeakBlocked, "staged file names an internal task")}
	if _, err := invoke(t, fc, OpLeaks, op.ArgsDecoder{}); errs.CodeOf(err) != errs.LeakBlocked {
		t.Fatalf("err %v", err)
	}
}

func TestLeaksSilentPass(t *testing.T) {
	t.Parallel()
	if (LeaksOut{}).Text() != "" {
		t.Error("a silent commit-check pass must print nothing")
	}
	if (LeaksOut{Message: "✓ x"}).Text() != "✓ x\n" {
		t.Error("message line")
	}
}

func TestPRBody(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	clean := filepath.Join(dir, "clean.md")
	dirty := filepath.Join(dir, "dirty.md")
	empty := filepath.Join(dir, "empty.md")
	for p, body := range map[string]string{clean: "Fixes the retry loop.\n", dirty: "see tsk_internal for context", empty: ""} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fc := &fakeChecker{blockText: "tsk_internal"}
	res, err := invoke(t, fc, OpPRBody, op.ArgsDecoder{Args: []string{clean}, Flags: flags("explain", "true")})
	if err != nil {
		t.Fatal(err)
	}
	if txt, _ := res.Text(); txt != prBodyClean+"\n" {
		t.Errorf("clean text %q", txt)
	}
	if got := fc.texts[0]; got.Where != WherePRBody || !got.Explain || string(got.Body) != "Fixes the retry loop.\n" {
		t.Errorf("checker got %+v", got)
	}
	if _, err := invoke(t, fc, OpPRBody, op.ArgsDecoder{Args: []string{empty}}); err != nil {
		t.Errorf("empty body: %v", err)
	}
	if _, err := invoke(t, fc, OpPRBody, op.ArgsDecoder{Args: []string{dirty}}); errs.CodeOf(err) != errs.LeakBlocked {
		t.Errorf("dirty body: %v", err)
	}
	if _, err := invoke(t, fc, OpPRBody, op.ArgsDecoder{Args: []string{filepath.Join(dir, "missing.md")}}); errs.CodeOf(err) != errs.InvalidInput {
		t.Errorf("missing file: %v", err)
	}
}

func TestPRBodyTooLarge(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "big.md")
	if err := os.WriteFile(p, make([]byte, maxBodyBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	fc := &fakeChecker{}
	if _, err := invoke(t, fc, OpPRBody, op.ArgsDecoder{Args: []string{p}}); errs.CodeOf(err) != errs.InvalidInput || len(fc.texts) != 0 {
		t.Errorf("over the bound: %v, checker ran %d", err, len(fc.texts))
	}
}
