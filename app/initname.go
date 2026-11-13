package app

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// gitOriginURLKey is the git config key of origin's configured URL. The raw
// config value is read (not `git remote get-url`), so a url.<base>.insteadOf
// rewrite does not rename the project (v0.1.17 readGitOriginURL).
const gitOriginURLKey = "remote.origin.url"

// defaultProjectName is the name `lore init` gives a project when --name is
// not set, as v0.1.17 did: the repo name of origin's URL
// (git@github.com:acme/widget-api.git → widget-api), else the folder name;
// lower-cased either way.
//
// In a folder below the repo top (a monorepo app or package, D-1) the
// folder name is added: <repo>-<folder> (acme-api-extraction), so the
// sub-project and the project at the repo root never share one name.
func defaultProjectName(ctx context.Context, dir string) string {
	n := projectNameFromURL(gitOriginURL(ctx, dir))
	if n == "" {
		return strings.ToLower(filepath.Base(dir))
	}
	if top := gitTopOf(ctx, dir); top != "" && realPath(top) != realPath(dir) {
		if base := filepath.Base(dir); !strings.EqualFold(base, n) {
			n += "-" + base
		}
	}
	return strings.ToLower(n)
}

// gitTopOf is the top of the git work tree dir is in, "" outside one.
func gitTopOf(ctx context.Context, dir string) string {
	out, err := gitref.Exec{}.Run(ctx, gitref.Invocation{Dir: dir, Args: []string{"rev-parse", "--show-toplevel"}})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitOriginURL is origin's first configured URL in dir, or "" (no git, no
// origin, not a work tree).
func gitOriginURL(ctx context.Context, dir string) string {
	out, err := gitref.Exec{}.Run(ctx, gitref.Invocation{Dir: dir, Args: []string{"config", "--get", gitOriginURLKey}})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// projectNameFromURL is the last path segment of a repo address without
// ".git": the text after the last '/' or ':' (scp-style ssh addresses use
// ':'), v0.1.17 inferProjectNameFromGit. "" for an empty address.
func projectNameFromURL(url string) string {
	url = strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(url), "/"), ".git")
	if i := strings.LastIndexAny(url, "/:"); i >= 0 {
		return url[i+1:]
	}
	return url
}

// projectOriginURL is origin's URL as the project row records it, with any
// user:token part removed (the row file is committed and shared); "" when
// there is no origin or the URL cannot be read.
func projectOriginURL(ctx context.Context, dir string) string {
	raw := gitOriginURL(ctx, dir)
	if raw == "" {
		return ""
	}
	clean, _, err := repourl.StripCredentials(raw)
	if err != nil {
		return ""
	}
	return clean
}
