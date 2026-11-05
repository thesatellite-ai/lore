package main

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// gitOriginURLKey is the git config key of origin's configured URL. The raw
// config value is read (not `git remote get-url`), so a url.<base>.insteadOf
// rewrite does not rename the project (v0.1.17 readGitOriginURL).
const gitOriginURLKey = "remote.origin.url"

// defaultProjectName is the name `lore init` gives a project when --name is
// not set, as v0.1.17 did: the repo name of origin's URL
// (git@github.com:acme/widget-api.git → widget-api), else the folder name;
// lower-cased either way.
func defaultProjectName(ctx context.Context, dir string) string {
	if n := projectNameFromURL(gitOriginURL(ctx, dir)); n != "" {
		return strings.ToLower(n)
	}
	return strings.ToLower(filepath.Base(dir))
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
