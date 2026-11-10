//go:build setupe2e

package setupe2e

// porttaken_test.go — `lore setup web --port <taken>` fails at once.
//
// setup.run's schema update fetches the attached stores over ssh (the
// v0.1.17 store of a v0117 home, startfetch.go). The page's listener is
// bound before that update (cmd/lore/setupweb.go preListen), so a port
// another program holds fails first: a typed E_* line naming the port and
// the way out, and not one ssh call. The control run on a free port shows
// the same world does fetch, so "no ssh call" is not vacuous.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// takenPortDeadline is how long a refused start may take: binding a port
// is instant, a store fetch is not (the fake ssh alone costs a process).
const takenPortDeadline = 20 * time.Second

func TestSetupWebTakenPortFailsBeforeFetch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	s := Shape{GHAccounts: 1, GHActive: ActiveFirst, SSH: SSHDefaultOnly, Keys: KeysWithPub, LoreHome: HomeV0117, Start: StartLoreRepo}
	w, err := Build(ctx, t.TempDir(), s, loreBin)
	if err != nil {
		t.Fatalf("build world: %v", err)
	}
	before, err := ReadLog(w.Info().Logs.SSH)
	if err != nil {
		t.Fatalf("ssh log: %v", err)
	}

	// another program holds the port
	var lc net.ListenConfig
	held, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port

	cctx, ccancel := context.WithTimeout(ctx, takenPortDeadline)
	defer ccancel()
	c := exec.CommandContext(cctx, loreBin, "setup", "web", "--no-open", "--port", fmt.Sprint(port))
	c.Dir = w.StartDir()
	c.Env = w.env(filepath.Dir(loreBin), "")
	out, err := c.CombinedOutput()
	got := string(out)
	var ee *exec.ExitError
	if !errors.As(err, &ee) || cctx.Err() != nil {
		t.Fatalf("lore setup web --port %d (taken): want a quick non-zero exit, got %v (deadline %v):\n%s", port, err, cctx.Err(), got)
	}
	for _, want := range []string{"E_", fmt.Sprintf("--port %d", port), "--port 0 picks a free one"} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "schema") || reSetupURL.MatchString(got) {
		t.Errorf("the refusal must come before the schema update and the link:\n%s", got)
	}
	after, err := ReadLog(w.Info().Logs.SSH)
	if err != nil {
		t.Fatalf("ssh log: %v", err)
	}
	if n := len(after) - len(before); n != 0 {
		t.Fatalf("a taken port made %d ssh call(s), want none:\n%s", n, startCalls(w, after[len(before):]))
	}

	// control: on a free port the same world fetches the store at start
	if err := w.Start(ctx, 0, loreBin); err != nil {
		t.Fatalf("control start: %v", err)
	}
	defer w.Stop()
	ctrl, _ := ReadLog(w.Info().Logs.SSH)
	if len(ctrl) == len(after) {
		t.Fatalf("control: a free port made no ssh call, so the world does not fetch and the check above proves nothing:\n%s", w.Output())
	}
}
