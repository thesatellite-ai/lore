package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// TestProjectNameFromURL (B11 port of saas/cmd/cli usecases_misc_test.go
// TestUC_MiscInferProjectNameFromGit): every address form names the repo.
func TestProjectNameFromURL(t *testing.T) {
	t.Parallel()
	for url, want := range map[string]string{
		"git@github.com:khanakia/aicoder-cli-go.git": "aicoder-cli-go",
		"https://github.com/vercel/next.js.git":      "next.js",
		"https://gitlab.com/org/sub/internal-api":    "internal-api",
		"ssh://git@host:2222/team/widget.git":        "widget",
		"gitlab.com:org/sub/internal-api.git":        "internal-api",
		"https://github.com/acme/app/":               "app",
		"git@github.com:acme/widget-api.git":         "widget-api",
		"":                                           "",
	} {
		if got := projectNameFromURL(url); got != want {
			t.Errorf("%q → %q, want %q", url, got, want)
		}
	}
}

// TestDefaultProjectName (B11 port of TestUC_MiscInitNamesProjectFromOrigin):
// with an origin the project is named after it (lower-cased); without one,
// or outside git, after the folder.
func TestDefaultProjectName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	git := func(dir string, args ...string) {
		t.Helper()
		if _, err := (gitref.Exec{}).Run(ctx, gitref.Invocation{Dir: dir, Args: args}); err != nil {
			t.Fatalf("git %v: %v %s", args, err, gitref.Stderr(err))
		}
	}
	withOrigin := filepath.Join(t.TempDir(), "Checkout")
	git(filepath.Dir(withOrigin), "init", "-q", withOrigin)
	git(withOrigin, "remote", "add", "origin", "git@github.com:acme/Widget-API.git")
	if got := defaultProjectName(ctx, withOrigin); got != "widget-api" {
		t.Fatalf("with origin: %q", got)
	}
	noOrigin := filepath.Join(t.TempDir(), "MyRepo")
	git(filepath.Dir(noOrigin), "init", "-q", noOrigin)
	if got := defaultProjectName(ctx, noOrigin); got != "myrepo" {
		t.Fatalf("no origin: %q", got)
	}
	plain := filepath.Join(t.TempDir(), "Plain")
	if got := defaultProjectName(ctx, plain); got != "plain" {
		t.Fatalf("not a git folder: %q", got)
	}
}

// TestProjectOriginURL: init records origin's URL on the project row (as
// v0.1.17 did) without any user:token part (the row file is committed);
// no origin or no git gives "". The token is built at run time (CLAUDE.md
// conv. 8).
func TestProjectOriginURL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	git := func(dir string, args ...string) {
		t.Helper()
		if _, err := (gitref.Exec{}).Run(ctx, gitref.Invocation{Dir: dir, Args: args}); err != nil {
			t.Fatalf("git %v: %v %s", args, err, gitref.Stderr(err))
		}
	}
	secret := "tok" + strings.Repeat("x", 12)
	for _, c := range []struct {
		name, url, want string
	}{
		{"scp form", "git@github.com:acme/widget.git", "git@github.com:acme/widget.git"},
		{"https", "https://github.com/acme/widget.git", "https://github.com/acme/widget.git"},
		{"https with a token", "https://bob:" + secret + "@github.com/acme/widget.git", "https://github.com/acme/widget.git"},
		{"no origin", "", ""},
	} {
		dir := filepath.Join(t.TempDir(), "r")
		git(filepath.Dir(dir), "init", "-q", dir)
		if c.url != "" {
			git(dir, "remote", "add", "origin", c.url)
		}
		got := projectOriginURL(ctx, dir)
		if got != c.want || strings.Contains(got, secret) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if got := projectOriginURL(ctx, filepath.Join(t.TempDir(), "plain")); got != "" {
		t.Errorf("not a git folder: %q", got)
	}
}
