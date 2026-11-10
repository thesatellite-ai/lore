package probe

// aliases.go — the file facts behind the setup page's GitHub step (ONB
// 4.3): does an alias's IdentityFile exist, which private key files sit
// next to a .pub, and where a missing key probably is. Reads names and
// stat results only: a private key file is never opened.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ExpandPath turns a leading ~/ into the prober's home (ssh_config(5)
// expands ~ the same way for IdentityFile).
func (p *Prober) ExpandPath(path string) string { return expandHome(path, p.o.Home) }

// IsFile reports whether path (~ expanded) is an existing regular file.
func (p *Prober) IsFile(path string) bool {
	st, err := os.Stat(expandHome(path, p.o.Home))
	return err == nil && st.Mode().IsRegular()
}

// PrivateKeyFiles lists the private key files of <home>/.ssh that have a
// .pub beside them (the pair ssh-keygen writes), plus every path of extra
// that exists (an alias's IdentityFile without a .pub). Absolute, sorted,
// each once. Only names and stat are read.
func (p *Prober) PrivateKeyFiles(extra []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		path = expandHome(path, p.o.Home)
		if seen[path] || strings.HasSuffix(path, pubSuffix) || !p.IsFile(path) {
			return
		}
		seen[path] = true
		out = append(out, path)
	}
	pubs, _ := filepath.Glob(filepath.Join(p.o.Home, sshDir, "*"+pubSuffix))
	for _, pub := range pubs {
		add(strings.TrimSuffix(pub, pubSuffix))
	}
	for _, e := range extra {
		add(e)
	}
	slices.Sort(out)
	return out
}

// KeyNearby is where a missing IdentityFile probably is: the same file
// name in <home>/.ssh ("" when there is none). The usual cause is a
// config copied from another machine whose home folder differs
// (/Users/me/.ssh/key on a home of /mnt/data/me).
func (p *Prober) KeyNearby(missing string) string {
	cand := filepath.Join(p.o.Home, sshDir, filepath.Base(missing))
	if cand == expandHome(missing, p.o.Home) || !p.IsFile(cand) {
		return ""
	}
	return cand
}
