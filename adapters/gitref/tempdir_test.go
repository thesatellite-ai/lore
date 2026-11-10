package gitref

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestWriteRefWithTMPDIRGone: a ref write works on every repo kind when
// TMPDIR names a folder that does not exist (the machine sweep's
// env_tmpdir_missing shape: every live write failed with E_INTERNAL
// "write the local live ref"), and leaves no temp folder in the git dir.
func TestWriteRefWithTMPDIRGone(t *testing.T) {
	for _, kind := range allKinds {
		t.Run(string(kind), func(t *testing.T) {
			r := newRepo(t, kind)
			t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "gone"))
			if _, err := r.WriteRef(context.Background(), WriteRequest{Ref: "refs/lore/live", Message: "m", Signature: testSig,
				Changes: Changes{Put: map[string][]byte{"_root/x.json": []byte("{}")}}}); err != nil {
				t.Fatalf("write with TMPDIR gone: %v", err)
			}
			gitDir := git(t, r.Dir(), "rev-parse", "--absolute-git-dir")
			left, _ := filepath.Glob(filepath.Join(gitDir, tempDirPrefix+"*"))
			if len(left) > 0 {
				t.Errorf("temp folders left in the git dir: %v", left)
			}
			if _, err := os.Stat(gitDir); err != nil {
				t.Fatal(err)
			}
		})
	}
}
