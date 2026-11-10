package setupweb

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/probe"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
)

// debugToken is a GitHub-token-shaped value built at run time (never a
// literal in the source); debugFree has no known shape, so only the
// registered-secret rule can catch it.
func debugToken() string { return "gh" + "p_" + strings.Repeat("Xy7", 12) }
func debugFree() string  { return "memory-only-" + strings.Repeat("k", 10) }

type checkIn struct {
	Setup string `flag:"setup" json:"setup,omitempty" desc:"setup"`
	Step  string `flag:"step" json:"step,omitempty" desc:"step"`
	Note  string `flag:"note" json:"note,omitempty" desc:"note"`
}

type checkOut struct {
	OK bool `json:"ok"`
}

// debugRig is a Page whose setup.check op behaves like discovery with a
// token in memory: it registers the token, makes an "API call" whose
// header and output echo it, and fails with it in the error message.
func debugRig(t *testing.T, logPath string) *rig {
	t.Helper()
	tok, free := debugToken(), debugFree()
	reg, err := op.NewRegistry(
		op.Define(op.Spec{Name: OpSetupCheck, Summary: "Check a step", Permission: op.PermRead, Access: op.AccessRead, Local: true, RESTNoun: "setup", Errors: []errs.Code{errs.InvalidInput}},
			func(ctx context.Context, in checkIn) (checkOut, error) {
				probe.Secret(ctx, free)
				ctx = probe.WithLabel(ctx, "github.api")
				probe.Record(ctx, probe.Call{Command: "GET https://api.github.com/user -H 'Authorization: token " + tok + "'",
					Output: `{"login":"octo-personal","token":"` + free + `"}`, Exit: 200})
				if in.Note == "fail" {
					return checkOut{}, errs.New(errs.InvalidInput, "GitHub refused "+free+" and "+tok)
				}
				return checkOut{OK: true}, nil
			}),
	)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	r := &rig{t: t, runner: &fakeRunner{calls: map[string]int{}}}
	r.page, err = New(ln, Options{Registry: reg, Runner: r.runner, LoreVersion: "test", DebugLog: logPath})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestDebugLogRedactsFakeToken: a token in a call's header, a call's
// output and an error message reaches neither GET /setup/debug nor the
// log file, while the entry still says what ran, its op, step, status
// and error code.
func TestDebugLogRedactsFakeToken(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "state", DebugLogFile)
	r := debugRig(t, logPath)
	c, csrf := r.session()
	path := r.page.routes[OpSetupCheck].Path
	ok := r.do(http.MethodPost, path, `{"setup":"personal","step":"github"}`, authed(c, csrf))
	if ok.Code != http.StatusOK || ok.Header().Get(HeaderDebugSeq) != "1" {
		t.Fatalf("check: %d seq %q %s", ok.Code, ok.Header().Get(HeaderDebugSeq), ok.Body)
	}
	bad := r.do(http.MethodPost, path, `{"setup":"personal","step":"github","note":"fail"}`, authed(c, csrf))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("fail: %d %s", bad.Code, bad.Body)
	}
	w := r.do(http.MethodGet, PathDebug, "", authed(c, csrf))
	if w.Code != http.StatusOK {
		t.Fatalf("debug: %d %s", w.Code, w.Body)
	}
	var out debugOut
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 2 || out.OS == "" || out.Version != "test" {
		t.Fatalf("debug out %+v", out)
	}
	e := out.Entries[1]
	if e.Op != OpSetupCheck || e.Step != "github" || e.Setup != "personal" || e.Code != string(errs.InvalidInput) || len(e.Calls) != 1 || e.Calls[0].Label != "github.api" {
		t.Fatalf("entry %+v", e)
	}
	if !strings.Contains(e.Message, "GitHub refused") || !strings.Contains(e.Calls[0].Command, "api.github.com/user") {
		t.Fatalf("entry lost its meaning: %+v", e)
	}
	file, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(file), "\n") != 2 {
		t.Fatalf("log lines: %s", file)
	}
	for where, text := range map[string]string{"GET /setup/debug": w.Body.String(), "log file": string(file)} {
		for _, secret := range []string{debugToken(), debugFree()} {
			if strings.Contains(text, secret) {
				t.Errorf("%s holds a secret", where)
			}
		}
	}
	// since= returns only the newer entries; no session, no log.
	if w := r.do(http.MethodGet, PathDebug+"?since=1", "", authed(c, csrf)); !strings.Contains(w.Body.String(), `"seq":2`) || strings.Contains(w.Body.String(), `"seq":1,`) {
		t.Fatalf("since: %s", w.Body)
	}
	if w := r.do(http.MethodGet, PathDebug, "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", w.Code)
	}
}

// TestDebugLogRotates: past debugLogMax the file moves to .1, .1 to .2,
// and the oldest is dropped; at most debugLogKeep files exist.
func TestDebugLogRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), DebugLogFile)
	chunk := []byte(strings.Repeat("x", debugLogMax/2+1) + "\n")
	for i := range 6 {
		line := append([]byte(strconv.Itoa(i)), chunk...)
		if err := appendRotated(path, line); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{path, path + ".1", path + ".2"} {
		fi, err := os.Stat(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if fi.Size() > debugLogMax {
			t.Fatalf("%s is %d bytes", f, fi.Size())
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf(".3 exists: %v", err)
	}
	if b, _ := os.ReadFile(path); b[0] != '5' {
		t.Fatalf("live file starts with %q", b[0])
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != debugFileMode {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

// TestRouteMatch: op names are found for templated paths.
func TestRouteMatch(t *testing.T) {
	for _, c := range []struct {
		tmpl, path string
		want       bool
	}{
		{"/v1/setups/{id}", "/v1/setups/personal", true},
		{"/v1/setups/{id}", "/v1/setups", false},
		{"/v1/setup/check", "/v1/setup/check", true},
		{"/v1/setup/check", "/v1/setup/plan", false},
	} {
		if got := routeMatch(c.tmpl, c.path); got != c.want {
			t.Errorf("%s vs %s: %v", c.tmpl, c.path, got)
		}
	}
}

// TestDebugLogJobEntry: a background job started by a request (probe.StartJob)
// lands in GET /setup/debug as its own JOB entry with its calls, after the
// request's entry, which holds none of them.
func TestDebugLogJobEntry(t *testing.T) {
	done := make(chan struct{})
	reg, err := op.NewRegistry(
		op.Define(op.Spec{Name: OpSetupCheck, Summary: "Check a step", Permission: op.PermRead, Access: op.AccessRead, Local: true, RESTNoun: "setup", Errors: []errs.Code{errs.InvalidInput}},
			func(ctx context.Context, _ checkIn) (checkOut, error) {
				jctx, finish := probe.StartJob(context.WithoutCancel(ctx), "discovery job")
				go func() {
					probe.Record(jctx, probe.Call{Command: "ssh -T git@github.com"})
					finish(nil)
					close(done)
				}()
				return checkOut{OK: true}, nil
			}),
	)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	r := &rig{t: t, runner: &fakeRunner{calls: map[string]int{}}}
	if r.page, err = New(ln, Options{Registry: reg, Runner: r.runner, LoreVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	c, csrf := r.session()
	if w := r.do(http.MethodPost, r.page.routes[OpSetupCheck].Path, `{"setup":"personal"}`, authed(c, csrf)); w.Code != http.StatusOK {
		t.Fatalf("check: %d %s", w.Code, w.Body)
	}
	<-done
	var out debugOut
	if err := json.Unmarshal(r.do(http.MethodGet, PathDebug, "", authed(c, csrf)).Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var job *DebugEntry
	for i := range out.Entries {
		e := &out.Entries[i]
		if e.Method == DebugJobMethod {
			job = e
		} else if len(e.Calls) != 0 {
			t.Fatalf("request entry holds the job's calls: %+v", e)
		}
	}
	if job == nil || job.Path != "discovery job" || job.Op != OpSetupCheck || job.Status != http.StatusOK || len(job.Calls) != 1 || job.Calls[0].Command != "ssh -T git@github.com" {
		t.Fatalf("job entry %+v in %+v", job, out.Entries)
	}
}
