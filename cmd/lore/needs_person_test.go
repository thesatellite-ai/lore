package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/surface/rest"
)

// needsPersonTrue matches the envelope field in compact or indented JSON.
var needsPersonTrue = regexp.MustCompile(`"needs_person":\s*true`)

// TestPersonOnlyNeedsPerson: the agents.person_only refusal tells an
// agent to hand the command to its person (needs_person true) on both
// surfaces: the CLI's --json error envelope for a detected agent session
// and the REST answer for a request carrying X-Lore-Agent.
func TestPersonOnlyNeedsPerson(t *testing.T) {
	envScopeRepo(t)
	asPerson(t)
	if code, _, errOut := runCaptured(t, "policy", "set", "agents.person_only", "memory.delete", "--yes"); code != 0 {
		t.Fatalf("policy set: exit %d %s", code, errOut)
	}
	h := apiServer(t)
	status, body := apiDo(t, h, http.MethodPost, "/v1/memories", `{"body":"keep me"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("POST: %d %s", status, body)
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	status, body = apiDo(t, h, http.MethodDelete, "/v1/memories/"+res.ID, `{"yes":true}`, map[string]string{rest.HeaderAgent: "codex"})
	if status != http.StatusConflict || !strings.Contains(body, "E_POLICY_DENIED") || !needsPersonTrue.MatchString(body) {
		t.Fatalf("REST agent delete: %d %s, want 409 E_POLICY_DENIED with needs_person true", status, body)
	}
	asClaude(t, "sess-needs-person")
	code, out, errOut := runCaptured(t, "memory", "delete", res.ID, "--yes", "--json")
	if code != 23 || !strings.Contains(out, "E_POLICY_DENIED") || !needsPersonTrue.MatchString(out) {
		t.Fatalf("CLI agent delete: exit %d out %s err %s, want 23 E_POLICY_DENIED with needs_person true", code, out, errOut)
	}
}
