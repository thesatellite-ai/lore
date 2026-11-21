package gitfast

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
)

// TestObjectStoresKeptAcrossSessions (devco:D-838): in a process that
// shares object stores (the read server), a second Session reads objects
// from the store the first one opened, with its decoded objects, and
// Session.Close leaves it open; a repack (the old pack gone) or a stop
// gives a fresh store. Answers match git throughout. Not parallel: the
// pool is process state.
func TestObjectStoresKeptAcrossSessions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	work, _, _ := fixture(t)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	blob := gitT(t, work, "rev-parse", "HEAD~3:org/tasks/t1.json") // packed, a delta
	args := []string{"cat-file", "blob", blob}
	want, _ := realGit(t, work, args, nil)
	storeOf := func(s *Session) *objectStore {
		t.Helper()
		r := s.repoFor(work)
		if r == nil {
			t.Fatal("no repository read")
		}
		objs, err := r.objects()
		if err != nil {
			t.Fatal(err)
		}
		return objs
	}
	answer := func(s *Session, what string) {
		t.Helper()
		rep, ok := s.Answer(ctx, work, args, nil, nil)
		if !ok || rep.Code != 0 || !bytes.Equal(rep.Out, want) {
			t.Fatalf("%s: answered %v (exit %d) %q, want git's %q", what, ok, rep.Code, rep.Out, want)
		}
	}

	stop := ShareObjectStores()
	s1 := New()
	answer(s1, "first Session")
	first := storeOf(s1)
	s1.Close()
	s2 := New()
	if storeOf(s2) != first {
		t.Fatal("a second Session opened the object folder again instead of the kept store")
	}
	first.mu.Lock()
	_, decoded := first.cache[blob]
	first.mu.Unlock()
	if !decoded {
		t.Fatal("the kept store lost the object the first Session decoded")
	}
	answer(s2, "second Session (after the first closed)")
	s2.Close()

	gitT(t, work, "repack", "-a", "-d", "-q", "-f") // the kept store's pack is gone
	s3 := New()
	if storeOf(s3) == first {
		t.Fatal("the kept store was reused after its pack was deleted by a repack")
	}
	answer(s3, "after a repack")
	s3.Close()
	stop()

	s4, s5 := New(), New()
	defer s4.Close()
	defer s5.Close()
	if storeOf(s4) == storeOf(s5) {
		t.Fatal("without sharing, two Sessions share an object store")
	}
	answer(s5, "without sharing")
}
