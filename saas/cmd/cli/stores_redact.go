// stores_redact.go — §6.5 redacted output: when lore's output may be read
// by someone other than the person at the terminal (no TTY on stdout, or CI
// set), guard and store messages name no store URL, repo or org.
package main

import (
	"os"
	"regexp"

	"golang.org/x/term"
)

// envCI is the most common CI marker (the first of ciMarkers).
const envCI = "CI"

// ciMarkers are environment variables that mean "this output goes to a CI
// log someone else may read" (coordinator Q-stores-3): set → guard output
// names no internal id, path, store or org.
// ds:def id=team-ci-markers-t4gm77bb owner=@khanakia stability=stable desc="CI variables that turn on redacted output"
var ciMarkers = []string{envCI, "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "JENKINS_URL", "TF_BUILD", "BITBUCKET_BUILD_NUMBER"}

// inCI reports whether any CI marker is set.
func inCI() bool {
	for _, k := range ciMarkers {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}

// redactedRepo replaces a host/owner/repo in quiet output.
const redactedRepo = "<redacted>"

// repoSlugRe matches a normalised or URL-form repo reference
// (host.tld/owner/repo, with an optional scheme or user@host: prefix).
var repoSlugRe = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://)?(?:[^\s@/]+@)?[a-z0-9-]+(?:\.[a-z0-9-]+)+[:/][^\s/:]+/[^\s/:,;()]+`)

// quietOutput reports whether output must be redacted: stdout is not a
// terminal, or CI is set. `--share` (where offered) overrides on a terminal.
func quietOutput() bool {
	if inCI() {
		return true
	}
	return !term.IsTerminal(int(os.Stdout.Fd()))
}

// redactIfQuiet masks every repo reference in s when quietOutput.
func redactIfQuiet(s string) string {
	if !quietOutput() {
		return s
	}
	return repoSlugRe.ReplaceAllString(s, redactedRepo)
}
