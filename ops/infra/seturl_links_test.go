package infra_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/infra"
	"github.com/thesatellite-ai/lore/wire"
)

// linkFollower is the App with a recording RepoLinkFollower.
type linkFollower struct {
	*wire.App
	calls [][]string
}

func (f *linkFollower) FollowRepoLinks(_ context.Context, projectID string, confirmed []string) []string {
	f.calls = append(f.calls, append([]string{projectID}, confirmed...))
	return []string{"✓ store agency, folder acme: link updated (" + strings.Repeat("x", len(confirmed)) + ")"}
}

// TestSetURLFollowsStoreLinks (PROVENANCE 10.4, Q7): repo set-url asks the
// Env's RepoLinkFollower once per change of THIS checkout's confirmed
// addresses (primary, mirror), with the project id and the new confirmed
// list, and prints its lines in the text and in --json "links"; never for
// --remove, a repeat that changes nothing, or --repo naming another repo.
func TestSetURLFollowsStoreLinks(t *testing.T) {
	h := checkout(t, "git@github.com:agency/acmehub_backend.git")
	ctx := context.Background()
	if _, err := h.App.CheckRepoAddresses(ctx); err != nil {
		t.Fatal(err)
	}
	actor, err := ids.Parse[ids.Actor](opstest.Actor)
	if err != nil {
		t.Fatal(err)
	}
	env := &linkFollower{App: h.App}
	entries := infra.Ops(env)
	i := slices.IndexFunc(entries, func(e op.Entry) bool { return e.Spec().Name == infra.OpRepoSetURL })
	if i < 0 {
		t.Fatal("no repo.set_url entry")
	}
	runSet := func(in string) (string, string) {
		t.Helper()
		res, rerr := h.App.Run(ctx, entries[i], op.JSONDecoder{Body: []byte(in)}, op.Caller{Actor: actor, Yes: true})
		if rerr != nil {
			t.Fatalf("set-url %s: %v", in, rerr)
		}
		text, _ := res.Text()
		b, jerr := res.JSON()
		if jerr != nil {
			t.Fatal(jerr)
		}
		return text, string(b)
	}
	steps := []struct {
		in     string
		called bool
	}{
		{`{"url":"git@github.com:agency/acme-hub-api.git"}`, true},
		{`{"url":"git@github.com:agency/acme-hub-api.git"}`, false},
		{`{"url":"https://mirror.example/agency/acme-hub-api","mirror":true}`, true},
		{`{"url":"https://mirror.example/agency/acme-hub-api","remove":true}`, false},
	}
	for _, s := range steps {
		before := len(env.calls)
		text, js := runSet(s.in)
		called := len(env.calls) > before
		if called != s.called {
			t.Fatalf("%s: follower called %v, want %v", s.in, called, s.called)
		}
		var out struct {
			Confirmed []string `json:"confirmed"`
			Links     []string `json:"links"`
		}
		if err = json.Unmarshal([]byte(js), &out); err != nil {
			t.Fatal(err)
		}
		if !called {
			if len(out.Links) != 0 || strings.Contains(text, "link updated") {
				t.Fatalf("%s: links printed without a follow: %s", s.in, js)
			}
			continue
		}
		last := env.calls[len(env.calls)-1]
		if last[0] != h.Project || !slices.Equal(last[1:], out.Confirmed) {
			t.Fatalf("%s: follower got %v, want project %s + %v", s.in, last, h.Project, out.Confirmed)
		}
		if len(out.Links) != 1 || !strings.Contains(text, out.Links[0]+"\n") {
			t.Fatalf("%s: lines not in text and json: %q %s", s.in, text, js)
		}
	}
}
