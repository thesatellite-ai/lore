package wire

// check_ci_range.go — which commits `lore check leaks --ci` scans for
// secrets (devco D-330).
//
//	1. --since <rev>        the commits after rev (the last commit CI
//	                        checked); a rev the clone lacks is E_INVALID_INPUT
//	2. the CI's push range  GitHub Actions: the event payload
//	                        ($GITHUB_EVENT_PATH) pull_request.base.sha,
//	                        merge_group.base_sha, else before; GitLab:
//	                        CI_MERGE_REQUEST_DIFF_BASE_SHA, else
//	                        CI_COMMIT_BEFORE_SHA; a value the clone lacks
//	                        (a shallow clone) or all zeros (a new branch)
//	                        is skipped
//	3. origin/HEAD          its merge-base with HEAD, when HEAD is ahead
//	                        (a branch or pull request)
//	4. the last 50 commits  HEAD~50; with less history (a short repo or a
//	                        shallow clone) every tracked file (fail closed)
//
// Steps 1 and 2 use the merge-base of the commit and HEAD, so a force push
// still has everything HEAD brought scanned.
//
// Why: the earlier fallback on the default branch was HEAD's first parent,
// so a push of several commits made past the hooks (--no-verify) had only
// its last commit scanned: a key added in an earlier commit of the same
// push passed CI.

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
)

// ciFallbackDepth is how many commits back the scan reaches when nothing
// names the push range (step 4).
const ciFallbackDepth = 50

// maxEventBytes bounds the GitHub event payload read (GitHub caps a
// webhook payload at 25 MB).
const maxEventBytes = 32 << 20

// CI environment variables the push range is read from (each provider's
// predefined-variables page).
const (
	envGitHubActions   = "GITHUB_ACTIONS"
	envGitHubEventPath = "GITHUB_EVENT_PATH"
	envGitLabCI        = "GITLAB_CI"
	envGitLabMRBase    = "CI_MERGE_REQUEST_DIFF_BASE_SHA"
	envGitLabBefore    = "CI_COMMIT_BEFORE_SHA"
)

// githubEvent holds the fields of a GitHub Actions event payload that name
// the commit the run's changes start from.
type githubEvent struct {
	Before      string `json:"before"`
	PullRequest *struct {
		Base struct {
			SHA string `json:"sha"`
		} `json:"base"`
	} `json:"pull_request"`
	MergeGroup *struct {
		BaseSHA string `json:"base_sha"`
	} `json:"merge_group"`
}

// ciScanBase is the commit the CI secret scan compares with (see the file
// comment), "" for every tracked file. getenv reads the CI environment
// (os.Getenv in the binary).
func ciScanBase(ctx context.Context, top, since string, getenv func(string) string) (string, error) {
	head, err := liveGit(ctx, top, "rev-parse", "HEAD")
	if err != nil {
		return "", nil
	}
	headSHA := strings.TrimSpace(string(head))
	if since != "" {
		c, ok := ciCommit(ctx, top, since)
		if !ok {
			return "", errs.New(errs.InvalidInput, "check leaks --ci: --since "+strconv.Quote(since)+" is not a commit this clone has").
				WithHint("fetch the history it needs (actions/checkout with fetch-depth: 0, or git fetch --deepen <n>), or give --since <a commit this clone has>")
		}
		return ciMergeBase(ctx, top, c, headSHA), nil
	}
	for _, cand := range ciEnvSince(getenv) {
		if c, ok := ciCommit(ctx, top, cand); ok {
			return ciMergeBase(ctx, top, c, headSHA), nil
		}
	}
	if liveRefExists(ctx, top, "origin/HEAD") {
		if mb := ciMergeBase(ctx, top, "origin/HEAD", headSHA); mb != "" && mb != headSHA {
			return mb, nil
		}
	}
	if c, ok := ciCommit(ctx, top, "HEAD~"+strconv.Itoa(ciFallbackDepth)); ok {
		return c, nil
	}
	return "", nil
}

// ciCommit resolves rev to a commit sha in top; false when it names none
// (or looks like an option).
func ciCommit(ctx context.Context, top, rev string) (string, bool) {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return "", false
	}
	out, err := liveGit(ctx, top, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// ciMergeBase is the merge-base of a and b, "" when they share no history
// (every tracked file is scanned then).
func ciMergeBase(ctx context.Context, top, a, b string) string {
	out, err := liveGit(ctx, top, "merge-base", a, b)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ciEnvSince lists the commits the CI environment names as the start of
// this run's changes, best first; values that are not a full commit sha
// (empty, all zeros for a new branch) are left out.
func ciEnvSince(getenv func(string) string) []string {
	var cands []string
	if getenv(envGitHubActions) == "true" {
		if ev, ok := readGitHubEvent(getenv(envGitHubEventPath)); ok {
			if ev.PullRequest != nil {
				cands = append(cands, ev.PullRequest.Base.SHA)
			}
			if ev.MergeGroup != nil {
				cands = append(cands, ev.MergeGroup.BaseSHA)
			}
			cands = append(cands, ev.Before)
		}
	}
	if getenv(envGitLabCI) == "true" {
		cands = append(cands, getenv(envGitLabMRBase), getenv(envGitLabBefore))
	}
	out := cands[:0]
	for _, c := range cands {
		if isCommitSHA(c) {
			out = append(out, c)
		}
	}
	return out
}

// readGitHubEvent reads the event payload at path; false when there is
// none or it does not parse (the scan then falls back, never fails).
func readGitHubEvent(path string) (githubEvent, bool) {
	if path == "" {
		return githubEvent{}, false
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return githubEvent{}, false
	}
	defer func() { _ = f.Close() }()
	var ev githubEvent
	if err := json.NewDecoder(io.LimitReader(f, maxEventBytes)).Decode(&ev); err != nil {
		return githubEvent{}, false
	}
	return ev, true
}

// isCommitSHA reports whether s is a full SHA-1 or SHA-256 commit id that
// is not all zeros (GitHub's and GitLab's "no previous commit").
func isCommitSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	zero := true
	for _, r := range s {
		switch {
		case r == '0':
		case (r >= '1' && r <= '9') || (r >= 'a' && r <= 'f'):
			zero = false
		default:
			return false
		}
	}
	return !zero
}
