package formatgate

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/gitenv"
)

// TestSourceReleaseNotBehindTag fails when SourceRelease is older than the
// newest release tag (v<major>.<minor>.<patch>) reachable from HEAD
// (devco:D-735): a dev build counts as SourceRelease in the format gate,
// so a stale value locks a build of the tagged code out of an org whose
// floor is that release. The commit a release is tagged on must raise it.
// Skipped when git is missing or no release tag is reachable (a shallow
// clone, a source tarball).
func TestSourceReleaseNotBehindTag(t *testing.T) {
	tag := newestReleaseTag(t)
	if tag == "" {
		t.Skip("no v* release tag reachable from HEAD (shallow clone or no git)")
	}
	if VersionBelow(SourceRelease, tag) {
		t.Fatalf("formatgate.SourceRelease is %s, older than the release tag %s reachable from HEAD: raise it to %s in logic/formatgate/version.go in the release commit", SourceRelease, tag, strings.TrimPrefix(tag, "v"))
	}
}

// newestReleaseTag is the highest v<major>.<minor>.<patch> tag merged into
// HEAD ("" when none, or git cannot say). git runs without the GIT_*
// variables a hook would pass, so it reads this module's repository.
func newestReleaseTag(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "tag", "--merged", "HEAD", "--list", "v*")
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(gitenv.Names(), name)
	})
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	top := ""
	for _, tag := range strings.Fields(string(out)) {
		if ValidVersion(tag) && (top == "" || VersionBelow(top, tag)) {
			top = tag
		}
	}
	return top
}
