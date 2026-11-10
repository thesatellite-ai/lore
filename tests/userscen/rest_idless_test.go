//go:build userscen

package userscen

// rest_idless_test.go — the id-less forms over `lore api` (INTEG-MIGREST-4).
// An operation whose id is optional is served on its route with the /{id}
// segment taken out, so every no-id CLI form is reachable over REST and
// answers what the CLI answers:
//
//	TestRestIDLessTwins  GET /v1/policy = `lore policy show`, GET /v1/live = `lore live show`,
//	                     POST /v1/tasks/heartbeat = `lore task heartbeat`; and the no-id
//	                     refusals of land run, meta set, pii-pattern enable, task reassign
//	                     and resource check carry the CLI's code and message

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRestIDLessTwins(t *testing.T) {
	rw := newRestWorld(t)
	srv := restStart(t, rw.repo)

	// Successful reads and writes: the same JSON as the CLI's --json.
	for _, c := range []struct {
		method, path string
		cli          []string
	}{
		{http.MethodGet, "/v1/policy", []string{"policy", "show"}},
		{http.MethodGet, "/v1/live", []string{"live", "show"}},
		{http.MethodPost, "/v1/tasks/heartbeat", []string{"task", "heartbeat"}},
	} {
		body := ""
		if c.method == http.MethodPost {
			body = "{}"
		}
		rep := srv.do(t, restReq{method: c.method, path: c.path, body: body})
		cli := rw.repo.Lore(append(c.cli, "--json")...)
		if rep.status != http.StatusOK || cli.Code != 0 {
			t.Errorf("%s %s: status %d %.300s; CLI %v exit %d", c.method, c.path, rep.status, rep.body, c.cli, cli.Code)
			continue
		}
		if !restSameJSON(t, rep.body, cli.Stdout) {
			t.Errorf("%s %s answers\n%.400s\nbut lore %v --json answers\n%.400s", c.method, c.path, rep.body, c.cli, cli.Stdout)
		}
	}

	// No-id refusals: the operation itself answers, with the CLI's error.
	for _, c := range []struct {
		path string
		cli  []string
	}{
		{"/v1/lands/run", []string{"land", "run"}},
		{"/v1/metas/set", []string{"meta", "set"}},
		{"/v1/pii_patterns/enable", []string{"pii-pattern", "enable"}},
		{"/v1/tasks/reassign", []string{"task", "reassign"}},
		{"/v1/resources/check", []string{"resource", "check"}},
	} {
		rep := srv.post(t, c.path, "{}")
		cliErr := restCLIErr(rw.repo.Lore(append(c.cli, "--json")...))
		if cliErr == nil || rep.err == nil || rep.err.Code != cliErr.Code || rep.err.Message != cliErr.Message {
			t.Errorf("POST %s: %d %.300s; lore %v --json: %+v", c.path, rep.status, rep.body, c.cli, cliErr)
		}
		if rep.err != nil && strings.HasPrefix(rep.err.Message, "no route ") {
			t.Errorf("POST %s is not served", c.path)
		}
	}
}

// restSameJSON reports whether two JSON documents are the same value
// (member order and spacing aside).
func restSameJSON(t *testing.T, a, b string) bool {
	t.Helper()
	return json.Valid([]byte(a)) && json.Valid([]byte(b)) && migJSONEqual(a, b)
}
