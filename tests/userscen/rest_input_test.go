//go:build userscen

package userscen

// rest_input_test.go — what `lore api` does with bad and odd input, and
// with many callers at once, through the real binary:
//
//   - malformed bodies (not JSON, an array, a string, trailing data, an
//     unknown member, each wrong member type) are 400 E_INVALID_INPUT whose
//     message names the problem (the member, the type it takes) and whose
//     hint says where the fields are listed; nothing is written;
//   - a body over 4 MiB is 400 E_BODY_TOO_LARGE, one just under the cap is
//     accepted; a GET with a body, a POST with query parameters, a query
//     parameter of the wrong type, a repeated one, a path id that differs
//     from the body id are refused with their own message;
//   - X-Lore-Agent and X-Lore-Setup: too long, control characters, an
//     unknown setup name;
//   - odd text (unicode, emoji, RTL, quotes, very long) round-trips
//     byte-for-byte between REST and the CLI;
//   - many concurrent callers: every write lands once with its own id,
//     readers never see a 5xx, and refusals mixed in do not disturb them.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// restMaxBody is MaxBodyBytes of surface/rest (4 MiB).
const restMaxBody = 4 << 20

// TestRestInput_BadBodies is a matrix over body shapes × member types.
func TestRestInput_BadBodies(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	cases := []struct {
		name, method, path, body string
		code                     string
		say                      []string
	}{
		{"not json", http.MethodPost, "/v1/memories", `body=hello`, "E_INVALID_INPUT", []string{"the request body must be one JSON object"}},
		{"array", http.MethodPost, "/v1/memories", `[{"body":"x"}]`, "E_INVALID_INPUT", []string{"one JSON object"}},
		{"string", http.MethodPost, "/v1/memories", `"x"`, "E_INVALID_INPUT", []string{"one JSON object"}},
		{"null", http.MethodPost, "/v1/memories", `null`, "E_INVALID_INPUT", []string{"one JSON object"}},
		{"two objects", http.MethodPost, "/v1/memories", `{"body":"x"}{"body":"y"}`, "E_INVALID_INPUT", []string{"one JSON object"}},
		{"unknown member", http.MethodPost, "/v1/memories", `{"body":"x","colour":"red"}`, "E_INVALID_INPUT", []string{`unknown field "colour"`}},
		{"typo of a member", http.MethodPost, "/v1/tasks", `{"titel":"x"}`, "E_INVALID_INPUT", []string{`unknown field "titel"`}},
		{"number for text", http.MethodPost, "/v1/memories", `{"body":42}`, "E_INVALID_INPUT", []string{`field "body" takes a string, not a JSON number`}},
		{"object for text", http.MethodPost, "/v1/tasks", `{"title":{"x":1}}`, "E_INVALID_INPUT", []string{`field "title" takes a string, not a JSON object`}},
		{"string for list", http.MethodPost, "/v1/tasks", `{"title":"t","after":"tsk_x"}`, "E_INVALID_INPUT", []string{`field "after" takes a list, not a JSON string`}},
		{"string for duration", http.MethodPost, "/v1/tasks", `{"title":"t","timebox":"1h"}`, "E_INVALID_INPUT", []string{`field "timebox" takes integer nanoseconds, not a JSON string`}},
		{"string for bool", http.MethodPost, "/v1/memories", `{"body":"x","live":"yes"}`, "E_INVALID_INPUT", []string{`field "live" takes true or false`}},
		{"missing required", http.MethodPost, "/v1/decisions", `{}`, "E_INVALID_INPUT", []string{"title"}},
		{"empty body on add", http.MethodPost, "/v1/decisions", ``, "E_INVALID_INPUT", []string{"title"}},
		{"yes not a bool", http.MethodDelete, "/v1/memories/mem_01a1b49cf238767ca425d36a07b5cc1e?yes=sure", ``, "E_INVALID_INPUT", []string{"yes", "true or false"}},
		{"body on GET", http.MethodGet, "/v1/memories", `{"limit":1}`, "E_INVALID_INPUT", []string{"a GET request takes query parameters, not a body"}},
		{"query on POST", http.MethodPost, "/v1/memories?body=x", `{}`, "E_INVALID_INPUT", []string{"a POST request takes a JSON body, not query parameters"}},
		{"unknown query parameter", http.MethodGet, "/v1/memories?colour=red", ``, "E_INVALID_INPUT", []string{"unknown parameter colour"}},
		{"bool parameter not a bool", http.MethodGet, "/v1/memories?archived=maybe", ``, "E_INVALID_INPUT", []string{"parameter archived: want true or false"}},
		{"one-value parameter twice", http.MethodGet, "/v1/memories?archived=true&archived=false", ``, "E_INVALID_INPUT", []string{"parameter archived: want one value"}},
		{"path id differs from body id", http.MethodPatch, "/v1/decisions/dec_01a1b49cf238767ca425d36a07b5cc1e", `{"id":"dec_11a1b49cf238767ca425d36a07b5cc1e","title":"x"}`, "E_INVALID_INPUT", []string{"the body id differs from the id in the path"}},
	}
	for _, c := range cases {
		rep := srv.do(t, restReq{method: c.method, path: c.path, body: c.body})
		if rep.status != http.StatusBadRequest || rep.err == nil || rep.err.Code != c.code {
			t.Errorf("%s: want 400 %s, got %d %.300s", c.name, c.code, rep.status, rep.body)
			continue
		}
		for _, s := range c.say {
			if !strings.Contains(rep.err.Message, s) {
				t.Errorf("%s: message %q lacks %q", c.name, rep.err.Message, s)
			}
		}
		if strings.Contains(c.name, "member") || strings.Contains(c.name, " for ") {
			if !strings.Contains(rep.err.Hint, "/openapi.json") {
				t.Errorf("%s: hint %q must say where the fields are listed", c.name, rep.err.Hint)
			}
		}
	}
	// None of the refused bodies wrote anything.
	if out := rw.repo.MustLore("decision", "list", "--json").Stdout; strings.Contains(out, `"title"`) {
		t.Errorf("a refused body wrote a decision: %s", out)
	}
}

// TestRestInput_BodySizeCap: 4 MiB is the cap, on every body route and on
// the dynamic-kind rewrite, and the cap answers before anything runs.
func TestRestInput_BodySizeCap(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	over := `{"body":"` + strings.Repeat("x", restMaxBody) + `"}`
	for _, path := range []string{"/v1/memories", "/v1/tasks", "/v1/resources/host"} {
		rep := srv.post(t, path, over)
		if rep.status != http.StatusBadRequest || rep.err == nil || rep.err.Code != "E_BODY_TOO_LARGE" || !strings.Contains(rep.err.Message, "request body is over 4194304 bytes") {
			t.Errorf("%s over the cap: %d %.300s", path, rep.status, rep.body)
		}
	}
	// Without the token the cap never matters: 401 first.
	rep := srv.do(t, restReq{method: http.MethodPost, path: "/v1/memories", body: over, token: restNoToken})
	restRefused(t, "over the cap without a token", rep)
	// A large body under the cap reaches the operation (whose own row-size
	// rule may then refuse it, but never as a transport failure or a 5xx).
	under := `{"body":"` + strings.Repeat("y", restMaxBody-64) + `"}`
	if rep := srv.post(t, "/v1/memories", under); rep.status >= 500 || (rep.err != nil && rep.err.Code == "E_BODY_TOO_LARGE" && strings.Contains(rep.err.Message, "request body")) {
		t.Errorf("under the cap: %d %.300s", rep.status, rep.body)
	}
}

// TestRestInput_Headers: X-Lore-Agent and X-Lore-Setup checks.
func TestRestInput_Headers(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	cases := []struct {
		name, header, value, code, say string
	}{
		{"agent too long", "X-Lore-Agent", strings.Repeat("a", 129), "E_INVALID_INPUT", "X-Lore-Agent is longer than 128 bytes"},
		{"agent control char", "X-Lore-Agent", "codex\tbot", "E_INVALID_INPUT", "X-Lore-Agent holds a control character"},
		{"setup too long", "X-Lore-Setup", strings.Repeat("s", 129), "E_INVALID_INPUT", "X-Lore-Setup is longer than 128 bytes"},
	}
	for _, c := range cases {
		rep := srv.do(t, restReq{method: http.MethodGet, path: "/v1/memories", header: []string{c.header, c.value}})
		if rep.err == nil || rep.err.Code != c.code || !strings.Contains(rep.err.Message, c.say) {
			t.Errorf("%s: want %s %q, got %d %.300s", c.name, c.code, c.say, rep.status, rep.body)
		}
		if rep.status/100 == 2 || rep.status >= 500 {
			t.Errorf("%s: status %d", c.name, rep.status)
		}
	}
	// An unknown setup name is refused on both surfaces, even with no
	// setups on this machine (INTEG-MIGREST-5): E_SETUP_NOT_FOUND with the
	// same message, which says there are none, and the hint that makes one.
	const noSetup = `no setup named "no-such-setup"; this machine has no setups`
	const noSetupHint = "lore setup makes one; drop --setup to run without one"
	cli := rw.repo.Lore("--setup", "no-such-setup", "memory", "list", "--json")
	if e := restCLIErr(cli); e == nil || e.Code != "E_SETUP_NOT_FOUND" || e.Message != noSetup || e.Hint != noSetupHint {
		t.Errorf("unknown --setup: want E_SETUP_NOT_FOUND %q (hint %q): exit %d %s", noSetup, noSetupHint, cli.Code, cli.Out())
	}
	rw.repo.Lore("--setup", "no-such-setup", "memory", "list").Fails(t, cli.Code, "[E_SETUP_NOT_FOUND] "+noSetup, "hint: "+noSetupHint)
	rep := srv.do(t, restReq{method: http.MethodGet, path: "/v1/memories", header: []string{"X-Lore-Setup", "no-such-setup"}})
	if rep.err == nil || rep.err.Code != "E_SETUP_NOT_FOUND" || rep.err.Message != noSetup || rep.status/100 != 4 {
		t.Errorf("unknown X-Lore-Setup: want E_SETUP_NOT_FOUND %q, got %d %.300s", noSetup, rep.status, rep.body)
	}
	// A 128-byte printable agent is accepted.
	if rep := srv.do(t, restReq{method: http.MethodGet, path: "/v1/memories", header: []string{"X-Lore-Agent", strings.Repeat("a", 128)}}); rep.status != http.StatusOK {
		t.Errorf("128-byte agent: %d %.300s", rep.status, rep.body)
	}
}

// TestRestInput_OddTextRoundTrips: text written over REST reads back the
// same over the CLI and over REST, and the reverse.
func TestRestInput_OddTextRoundTrips(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	texts := map[string]string{
		"unicode": "Grüße, 你好, привет — ünïcödé",
		"emoji":   "ship it 🚀 then 🧪 test",
		"rtl":     "שלום עולם مرحبا",
		"quotes":  `she said "no" and \ left`,
		"newline": "line one\nline two\n\ttabbed",
		"long":    strings.Repeat("long text ", 2000) + "end",
	}
	type show struct {
		Data struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"data"`
	}
	for name, text := range texts {
		b, err := json.Marshal(struct {
			Body string `json:"body"`
		}{text})
		if err != nil {
			t.Fatal(err)
		}
		rep := srv.post(t, "/v1/memories", string(b))
		m := restIDRe.FindStringSubmatch(rep.body)
		if rep.status != http.StatusCreated || m == nil {
			t.Errorf("%s: REST add %d %.300s", name, rep.status, rep.body)
			continue
		}
		var viaCLI show
		if err := json.Unmarshal([]byte(rw.repo.MustLore("memory", "show", m[1], "--json").Stdout), &viaCLI); err != nil {
			t.Fatalf("%s: CLI show: %v", name, err)
		}
		if viaCLI.Data.Body != text {
			t.Errorf("%s: REST→CLI changed the text: %q", name, viaCLI.Data.Body)
		}
		// And the reverse: CLI add, REST show.
		add := rw.repo.MustLore("memory", "add", "--body", text+" (cli)", "--json")
		cm := restIDRe.FindStringSubmatch(add.Stdout)
		if cm == nil {
			t.Fatalf("%s: CLI add printed no id: %s", name, add.Out())
		}
		var viaREST show
		if err := json.Unmarshal([]byte(srv.get(t, "/v1/memories/"+cm[1]).body), &viaREST); err != nil {
			t.Fatalf("%s: REST show: %v", name, err)
		}
		if viaREST.Data.Body != text+" (cli)" {
			t.Errorf("%s: CLI→REST changed the text: %q", name, viaREST.Data.Body)
		}
	}
}

// TestRestInput_ConcurrentCallers: writers, readers and refused callers at
// once; every write lands exactly once with a unique id.
func TestRestInput_ConcurrentCallers(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)
	const writers, readers, strangers = 16, 16, 8
	var wg sync.WaitGroup
	ids := make(chan string, writers)
	problems := make(chan string, writers+readers+strangers)
	for i := range writers {
		wg.Go(func() {
			rep, err := srv.try(restReq{method: http.MethodPost, path: "/v1/tasks", body: fmt.Sprintf(`{"title":"concurrent task %02d","to":"repo","commitment":"accepted"}`, i)})
			if err != nil || rep.status != http.StatusCreated {
				problems <- fmt.Sprintf("writer %d: %v %d %.200s", i, err, rep.status, rep.body)
				return
			}
			if m := restIDRe.FindStringSubmatch(rep.body); m != nil {
				ids <- m[1]
			}
		})
	}
	for i := range readers {
		wg.Go(func() {
			rep, err := srv.try(restReq{method: http.MethodGet, path: "/v1/tasks?all=true"})
			if err != nil || rep.status != http.StatusOK {
				problems <- fmt.Sprintf("reader %d: %v %d %.200s", i, err, rep.status, rep.body)
			}
		})
	}
	for i := range strangers {
		wg.Go(func() {
			rep, err := srv.try(restReq{method: http.MethodPost, path: "/v1/tasks", body: `{"title":"stranger"}`, token: restNoToken})
			if err != nil || rep.status != http.StatusUnauthorized {
				problems <- fmt.Sprintf("stranger %d: %v %d", i, err, rep.status)
			}
		})
	}
	wg.Wait()
	close(ids)
	close(problems)
	for p := range problems {
		t.Error(p)
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("id %s handed out twice", id)
		}
		seen[id] = true
	}
	if len(seen) != writers {
		t.Errorf("%d ids for %d writers", len(seen), writers)
	}
	out := rw.repo.MustLore("task", "list", "--all").Out()
	for i := range writers {
		if want := fmt.Sprintf("concurrent task %02d", i); strings.Count(out, want) != 1 {
			t.Errorf("%q appears %d times in task list", want, strings.Count(out, want))
		}
	}
	if strings.Contains(out, "stranger") {
		t.Errorf("a refused caller's task landed")
	}
}
