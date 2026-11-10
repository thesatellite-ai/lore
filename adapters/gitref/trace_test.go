package gitref

// The --trace line (issue 15): it names the host a network call reached
// after the call's own insteadOf options (the setup's ssh alias), never a
// url's credentials and never an env value.

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// aliasArgs are the route options wire's storeRoute gives an owned store.
var aliasArgs = []string{"-c", "url.git@github-work:.insteadOf=git@github.com:", "-c", "url.ssh://git@github-work/.insteadOf=ssh://git@github.com/"}

// placeholderPassword is a fake url password, joined into the url at run
// time so the fixture is not itself a url-with-password literal.
const placeholderPassword = "hunter2"

func TestTraceLine(t *testing.T) {
	exit2 := exec.Command("sh", "-c", "exit 2").Run()
	cases := []struct {
		name string
		call TraceCall
		want string   // the whole line
		not  []string // must not appear
	}{
		{"owned store: the aliased host", TraceCall{Args: append(append([]string(nil), aliasArgs...), "fetch", "origin"), Remote: "git@github.com:acme/hub.git", Took: 12 * time.Millisecond},
			"trace: git " + strings.Join(aliasArgs, " ") + " fetch origin (12ms, ok, remote git@github-work)", nil},
		{"owned store, ssh:// spelling", TraceCall{Args: append(append([]string(nil), aliasArgs...), "push"), Remote: "ssh://git@github.com/acme/hub.git"},
			"trace: git " + strings.Join(aliasArgs, " ") + " push (0s, ok, remote git@github-work)", nil},
		{"unowned store: the plain host", TraceCall{Args: []string{"fetch", "origin"}, Remote: "git@github.com:acme/other.git"},
			"trace: git fetch origin (0s, ok, remote git@github.com)", nil},
		{"a rewrite for another host leaves the url alone", TraceCall{Args: append(append([]string(nil), aliasArgs...), "fetch"), Remote: "git@gitlab.com:acme/hub.git"},
			"trace: git " + strings.Join(aliasArgs, " ") + " fetch (0s, ok, remote git@gitlab.com)", nil},
		{"longest insteadOf prefix wins", TraceCall{Args: []string{"-c", "url.git@a:.insteadOf=git@github.com:", "-c", "url.git@b:acme/.insteadOf=git@github.com:acme/", "fetch"}, Remote: "git@github.com:acme/hub.git"},
			"trace: git -c url.git@a:.insteadOf=git@github.com: -c url.git@b:acme/.insteadOf=git@github.com:acme/ fetch (0s, ok, remote git@b)", nil},
		{"https token is masked and not the host", TraceCall{Args: []string{"clone", "https://ghp_" + "SECRET" + "@github.com/acme/hub.git"}, Remote: "https://ghp_" + "SECRET" + "@github.com/acme/hub.git"},
			"trace: git clone https://***@github.com/acme/hub.git (0s, ok, remote github.com)", []string{"ghp_SECRET"}},
		{"user:password is masked", TraceCall{Args: []string{"fetch", "ssh://git:" + placeholderPassword + "@host/x.git"}},
			"trace: git fetch ssh://***@host/x.git (0s, ok)", []string{placeholderPassword}},
		{"env names only", TraceCall{Args: []string{"push"}, Remote: "git@github.com:acme/hub.git", Env: []string{"GIT_SSH_COMMAND=ssh -i /home/alice/.ssh/work_key"}},
			"trace: git push (0s, ok, remote git@github.com, env GIT_SSH_COMMAND)", []string{"work_key"}},
		{"exit status", TraceCall{Args: []string{"ls-remote"}, Err: errors.Join(errors.New("wrapped"), exit2)},
			"trace: git ls-remote (0s, exit 2)", nil},
		{"git never ran", TraceCall{Args: []string{"fetch"}, Err: context.Canceled},
			"trace: git fetch (0s, error)", nil},
		{"a local path has no host", TraceCall{Args: []string{"fetch"}, Remote: "/srv/acme/hub.git"},
			"trace: git fetch (0s, ok)", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := TraceLine(c.call)
			if got != c.want {
				t.Errorf("line\n got %q\nwant %q", got, c.want)
			}
			for _, n := range c.not {
				if strings.Contains(got, n) {
					t.Errorf("line %q leaks %q", got, n)
				}
			}
		})
	}
}

func TestTracerContext(t *testing.T) {
	ctx := context.Background()
	if TracerFrom(ctx) != nil || TracerFrom(WithTracer(ctx, nil)) != nil {
		t.Fatal("a context without a tracer must return nil")
	}
	var got []string
	f := TracerFrom(WithTracer(ctx, func(c TraceCall) { got = append(got, c.Args[0]) }))
	if f == nil {
		t.Fatal("WithTracer: tracer lost")
	}
	f(TraceCall{Args: []string{"fetch"}})
	if len(got) != 1 || got[0] != "fetch" {
		t.Errorf("tracer calls %v", got)
	}
	if IsMarkedTraced(ctx) || !IsMarkedTraced(MarkTraced(ctx)) {
		t.Error("MarkTraced / IsMarkedTraced disagree")
	}
}
