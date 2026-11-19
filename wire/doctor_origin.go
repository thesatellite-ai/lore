package wire

// doctor_origin.go — doctor's look at the repo's origin (O-19): a repo
// whose origin was moved, deleted or names a host that does not exist was
// HEALTHY in lore doctor (even --fresh), doctor --all and fix --all; only
// lore sync named it, so a fleet health check missed a dead remote.
//
//	origin url ─▶ none: nothing (a repo with no remote is fine)
//	           ─▶ a path on this machine (/srv/x.git, ./x, file:///x):
//	              is it a git repository? (a stat, no network)
//	           ─▶ a hosted url: does its host name exist? one name lookup
//	              (an ssh alias is first turned into its HostName with
//	              ssh -G, which reads ~/.ssh/config only); only a definite
//	              "no such host" counts: offline, a slow or failing DNS
//	              server says nothing about the remote
//
// It never connects to the remote: a repo whose host exists but refuses
// or lost the repo is lore sync's to name (the fetch reads it anyway).
//
// It runs no git process for a healthy repo (devco:D-281-land7b): the url
// is read from .git/config (doctor --all runs this once per repo, inside a
// git budget per repo). git itself is asked only when the file cannot say
// (a worktree or submodule, whose .git is a file) or before a fault is
// reported: url.<base>.insteadOf and include rules can rewrite the url,
// which only git applies, so a fault is always the url git really uses.

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/logic/gitfault"
	"github.com/thesatellite-ai/lore/logic/repourl"
)

// originRemote is the remote doctor looks at: the one lore sync uses.
const originRemote = "origin"

// originLookupTimeout bounds the host name lookup: a slow DNS server
// must not stall doctor (doctor --all runs it once per repo).
const originLookupTimeout = 2 * time.Second

// hostLookup resolves a host name (net.DefaultResolver.LookupHost; tests
// pass a fake so no network is used).
type hostLookup func(ctx context.Context, host string) ([]string, error)

// originURL is a way to read the url of a repo's origin ("" when none).
type originURL func(ctx context.Context, dir string) string

// originFault is the doctor line of a repo whose origin cannot work, ""
// when it can (or cannot be told without connecting). dir is the repo top.
func originFault(ctx context.Context, dir string, lookup hostLookup) string {
	return originFaultVia(ctx, dir, lookup, gitOriginURL)
}

// originFaultVia is originFault asking git for the url through askGit
// (tests count the calls): only when .git/config cannot tell, and once
// more to confirm a fault found from the file's url.
func originFaultVia(ctx context.Context, dir string, lookup hostLookup, askGit originURL) string {
	if fi, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !fi.IsDir() {
		return originFaultOf(ctx, dir, askGit(ctx, dir), lookup) // a worktree or submodule: git finds its config
	}
	raw := gitbackend.OriginAt(dir)
	if raw == "" {
		return "" // no origin: a repo of this machine only
	}
	line := originFaultOf(ctx, dir, raw, lookup)
	if line == "" {
		return ""
	}
	if real := askGit(ctx, dir); real != raw {
		return originFaultOf(ctx, dir, real, lookup) // a rewritten url: judge the one git uses
	}
	return line
}

// gitOriginURL is origin's url as git resolves it (insteadOf, includes).
func gitOriginURL(ctx context.Context, dir string) string {
	out, err := gitRun{dir: dir, args: []string{"remote", "get-url", originRemote}}.output(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// originFaultOf is the doctor line for the origin url raw of the repo at
// dir, "" when it can work, cannot be told, or there is no origin.
func originFaultOf(ctx context.Context, dir, raw string, lookup hostLookup) string {
	if raw == "" {
		return "" // no origin: a repo of this machine only
	}
	shown := repourl.MaskText(raw)
	if p, ok := originPath(dir, raw); ok {
		// git tries the path as given, then with .git appended (a remote
		// /srv/git/kit reaches /srv/git/kit.git), so doctor does too
		if isGitRepoDir(p) || isGitRepoDir(p+".git") {
			return ""
		}
		return originFaultLine(gitfault.Summary(gitfault.Missing, shown) + " (moved or deleted)")
	}
	host := originHost(ctx, raw)
	if host == "" || lookup == nil {
		return ""
	}
	lctx, cancel := context.WithTimeout(ctx, originLookupTimeout)
	defer cancel()
	_, lerr := lookup(lctx, host)
	var dnsErr *net.DNSError
	if lerr == nil || !errors.As(lerr, &dnsErr) || !dnsErr.IsNotFound {
		return "" // found, or no definite answer (offline, timeout)
	}
	return originFaultLine("cannot reach " + shown + " (no such host: " + host + ")")
}

// originFaultLine is the whole doctor line: the cause, what it stops and
// the next step, with the command that records a new address.
func originFaultLine(cause string) string {
	return "origin: " + cause + "; lore sync cannot fetch or push this repo's notes until it is fixed: " +
		"if the repo moved, run git remote set-url " + originRemote + " <new-url>, then lore sync"
}

// originPath is the folder a path remote names (absolute, relative to
// dir, or a file:// url); ok is false for a hosted url.
func originPath(dir, raw string) (string, bool) {
	if rest, ok := strings.CutPrefix(raw, "file://"); ok {
		if u, err := url.Parse(raw); err == nil && u.Path != "" {
			return filepath.FromSlash(u.Path), true
		}
		return filepath.FromSlash(rest), true
	}
	if strings.Contains(raw, "://") || isSCPRemote(raw) {
		return "", false
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(dir, raw)
	}
	return filepath.Clean(raw), true
}

// isSCPRemote reports git's scp-like form (host:path, user@host:path):
// a colon before the first slash, and not a Windows drive letter.
func isSCPRemote(raw string) bool {
	i := strings.Index(raw, ":")
	if i <= 1 { // none, or "C:" (a drive)
		return false
	}
	slash := strings.Index(raw, "/")
	return slash < 0 || i < slash
}

// isGitRepoDir reports whether p is a git repository: a bare one (HEAD
// and objects/ at its top) or a work tree (holding .git).
func isGitRepoDir(p string) bool {
	if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
		return true
	}
	_, herr := os.Stat(filepath.Join(p, "HEAD"))
	fi, oerr := os.Stat(filepath.Join(p, "objects"))
	return herr == nil && oerr == nil && fi.IsDir()
}

// originHost is the DNS name a hosted origin connects to ("" when it
// cannot be told). An ssh host is first resolved through ssh -G (aliases
// in ~/.ssh/config name another HostName); without ssh it is kept as is
// only when it looks like a DNS name (holds a dot), since an alias that
// does not resolve says nothing.
func originHost(ctx context.Context, raw string) string {
	var host string
	sshLike := false
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		host, sshLike = u.Hostname(), u.Scheme == "ssh" || strings.HasPrefix(u.Scheme, "git+ssh")
	case isSCPRemote(raw):
		host, sshLike = raw[:strings.Index(raw, ":")], true
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
	}
	if host == "" {
		return ""
	}
	if sshLike {
		if real := sshHostName(ctx, host); real != "" {
			return real
		}
		if !strings.Contains(host, ".") {
			return ""
		}
	}
	return host
}

// sshHostName is ssh's HostName for alias (ssh -G reads the config and
// connects nowhere), "" when ssh is missing or says nothing.
func sshHostName(ctx context.Context, alias string) string {
	if strings.HasPrefix(alias, "-") {
		return "" // never an option to ssh
	}
	cctx, cancel := context.WithTimeout(ctx, originLookupTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "ssh", "-G", alias).Output() // #nosec G204 -- alias is one argument, never an option
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(l, "hostname "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
