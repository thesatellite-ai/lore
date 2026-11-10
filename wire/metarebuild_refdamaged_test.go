package wire

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/model/errs"
	opsmemory "github.com/thesatellite-ai/lore/ops/memory"
	"github.com/thesatellite-ai/lore/ops/settings"
)

// TestMetaRebuildRefOnlyDamagedCopy (OV64-SPLIT, later 255): a key row
// lost on the checked-out branch whose only copy is a damaged one on
// another branch is "not lost but damaged", refused naming that branch and
// nothing written; once a readable copy exists on any ref, that wins:
// "not lost … is in <ref>".
func TestMetaRebuildRefOnlyDamagedCopy(t *testing.T) {
	t.Parallel()
	repo := gitRepo(t)
	a := openWith(t, repo, Options{})
	withProject(t, a)
	if _, err := run(t, a, settings.OpMetaAdd, `{"name":"client","type":"text"}`, true); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, a, opsmemory.OpAdd, `{"body":"ref probe"}`, true)
	if err != nil {
		t.Fatal(err)
	}
	var mem struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal([]byte(out), &mem); err != nil || mem.ID == "" {
		t.Fatalf("memory add: %s %v", out, err)
	}
	if _, err = run(t, a, settings.OpMetaSet, `{"id":"`+mem.ID+`","pair":"client=acme"}`, true); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	metaGit(t, repo, "add", "-A")
	metaGit(t, repo, "commit", "-q", "-m", "one")
	path, keyID := keyRowFile(t, repo)

	// keep-me holds only a damaged copy of the key row
	metaGit(t, repo, "checkout", "-q", "-b", "keep-me")
	if err = os.WriteFile(path, []byte(`{"_v":2,"broken`), 0o600); err != nil {
		t.Fatal(err)
	}
	metaGit(t, repo, "commit", "-q", "-am", "damage it on keep-me")
	metaGit(t, repo, "checkout", "-q", "-")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	metaGit(t, repo, "add", "-A")
	metaGit(t, repo, "commit", "-q", "-m", "lose it here")

	rebuild := func(input string) error {
		t.Helper()
		b := openWith(t, repo, Options{})
		_, rerr := run(t, b, settings.OpMetaRebuild, input, true)
		return rerr
	}
	for _, in := range []string{`{"dry_run":true}`, `{}`} {
		err = rebuild(in)
		if !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), "not lost but damaged: client (mky_…"+keyID[len(keyID)-4:]+") is damaged in keep-me") || !strings.Contains(err.Error(), "is damaged in keep-me") {
			t.Fatalf("%s: %v, want damaged in keep-me", in, err)
		}
		if _, serr := os.Stat(path); !os.IsNotExist(serr) {
			t.Fatalf("%s: a refused rebuild wrote the row", in)
		}
	}

	// a readable copy on another ref wins over the damaged one
	metaGit(t, repo, "branch", "good-copy", "HEAD~1")
	err = rebuild(`{"dry_run":true}`)
	if !errs.Has(err, errs.Refused) || !strings.Contains(err.Error(), "not lost: ") || !strings.Contains(err.Error(), "is in good-copy") || strings.Contains(err.Error(), "damaged") {
		t.Fatalf("with a readable copy: %v", err)
	}
}
