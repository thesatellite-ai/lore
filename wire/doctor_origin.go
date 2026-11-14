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

// originFault is the doctor line of a repo whose origin cannot work, ""
// when it can (or cannot be told without connecting). dir is the repo top.
func originFault(ctx context.Context, dir string, lookup hostLookup) string {
	out, err := gitRun{dir: dir, args: []string{"remote", "get-url", originRemote}}.output(ctx)
	raw := strings.TrimSpace(string(out))
	if err != nil || raw == "" {
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
