package gitref

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

// refuseFirstHashObject fails the first `hash-object -w` the way Git for
// Windows does when another process has the object file open, and runs
// every other command (and the retry) on the real git.
type refuseFirstHashObject struct {
	inner   Git
	refused atomic.Bool
}

func (g *refuseFirstHashObject) Run(ctx context.Context, inv Invocation) ([]byte, error) {
	if len(inv.Args) > 0 && inv.Args[0] == "hash-object" && g.refused.CompareAndSwap(false, true) {
		return nil, &Error{Args: inv.Args, ExitCode: 128, Stderr: "error: unable to write file ./objects/9e/26dfeeb6e641a33dae4961196235bdb965b21b: Permission denied\nfatal: Unable to add (NULL) to database"}
	}
	return g.inner.Run(ctx, inv)
}

// TestObjectWriteRetriesSharingRefusal: a write whose blob another git
// process is storing at that moment succeeds; before, on Windows, the
// sharing refusal failed the whole write (TestConcurrentWritersNeverLoseACommit
// failed with "unable to write file ./objects/…: Permission denied").
func TestObjectWriteRetriesSharingRefusal(t *testing.T) {
	old := retryObjectWrites
	retryObjectWrites = true
	t.Cleanup(func() { retryObjectWrites = old })
	ctx := context.Background()
	base := newRepo(t, kindBare)
	g := &refuseFirstHashObject{inner: Exec{}}
	r, err := Open(ctx, base.Dir(), g)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.WriteRef(ctx, WriteRequest{Ref: "refs/lore/live", Message: "m", Signature: testSig,
		Changes: Changes{Put: map[string][]byte{"_root/tasks/t.json": []byte("{}")}}})
	if err != nil {
		t.Fatalf("want the write to succeed after the refusal, got %v", err)
	}
	if !g.refused.Load() {
		t.Fatal("the refusal was never injected")
	}
}

// TestObjectWriteRefusedOnlyThatError: other git failures are not retried.
func TestObjectWriteRefusedOnlyThatError(t *testing.T) {
	for _, s := range []string{"fatal: not a git repository", "error: unable to write file x: No space left on device", strings.Repeat("Permission denied", 2)} {
		if objectWriteRefused(s) {
			t.Fatalf("%q read as the sharing refusal", s)
		}
	}
}
