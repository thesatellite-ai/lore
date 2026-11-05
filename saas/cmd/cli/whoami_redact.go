package main

// whoami_redact.go — §6.5 "redacted output" for `lore whoami`: with no TTY
// on stdout, or CI set, no store URL and no internal folder is printed
// (quietOutput decides, as for lore-stores' redactIfQuiet). --share
// prints everything, but only on a terminal, so a CI log never gets it.

import (
	"os"
	"sort"
	"strings"

	"golang.org/x/term"

	"saas/pkg/aicoder/lorecfg"
	"saas/pkg/aicoder/orgresolve"
	"saas/pkg/aicoder/repourl"
)

// whoamiRedactor masks internal names in whoami output. The zero value
// (on=false) passes every string through.
//
// It masks exactly what is internal — every configured store URL in each
// form a message may carry it, and each link's "store folder/" fragment —
// not every host/owner/repo shape: the repo's own remotes and the
// own:/repos: patterns are not internal and stay readable (E08 reads
// "matched by: own pattern github.com/betalabs/*" without a TTY).
type whoamiRedactor struct {
	on bool
	// secrets are replaced longest first, so "store folder/" fragments go
	// before the bare store URL inside them.
	secrets []string
}

// newWhoamiRedactor decides redaction for this run: quietOutput() unless
// --share was given on a terminal. Stores of EVERY entry are secrets, not
// only the resolved one's, so a refusal naming another org's store is
// masked too (fail closed).
func newWhoamiRedactor(s *orgSituation, share bool) whoamiRedactor {
	on := quietOutput()
	if share && term.IsTerminal(int(os.Stdout.Fd())) {
		on = false
	}
	r := whoamiRedactor{on: on}
	if !on {
		return r
	}
	add := func(v string) {
		if v = strings.TrimSpace(v); v != "" {
			r.secrets = append(r.secrets, v)
		}
	}
	addStore := func(raw string) {
		add(raw)
		add(strings.TrimSuffix(raw, ".git"))
		if n, err := repourl.Normalize(raw); err == nil {
			add(n)
			// scp-like form host:owner/repo, as git and ssh errors print it
			if host, rest, ok := strings.Cut(n, "/"); ok {
				add(host + ":" + rest)
			}
		}
	}
	if s.Config != nil {
		for i := range s.Config.Orgs {
			for _, st := range s.Config.Orgs[i].Stores {
				addStore(st.URL)
			}
		}
	}
	for _, l := range s.Result.Links {
		add(l.StoreURL + " " + l.Folder + "/")
		addStore(l.StoreURL)
	}
	sort.SliceStable(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
	return r
}

// str masks every secret in v.
func (r whoamiRedactor) str(v string) string {
	if !r.on {
		return v
	}
	for _, sec := range r.secrets {
		v = strings.ReplaceAll(v, sec, redactedRepo)
	}
	return v
}

// strs applies str to every element (a new slice; nil stays nil).
func (r whoamiRedactor) strs(vs []string) []string {
	if !r.on || vs == nil {
		return vs
	}
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = r.str(v)
	}
	return out
}

// folder masks an internal folder name (org_folder, a link folder).
func (r whoamiRedactor) folder(f string) string {
	if !r.on || f == "" {
		return f
	}
	return redactedRepo
}

// stores masks URL and org_folder of each store (JSON output).
func (r whoamiRedactor) stores(ss []lorecfg.Store) []lorecfg.Store {
	if !r.on {
		return ss
	}
	out := make([]lorecfg.Store, len(ss))
	for i, st := range ss {
		st.URL, st.OrgFolder = redactedRepo, r.folder(st.OrgFolder)
		out[i] = st
	}
	return out
}

// trace masks every rule's reason.
func (r whoamiRedactor) trace(t []orgresolve.RuleTrace) []orgresolve.RuleTrace {
	if !r.on {
		return t
	}
	out := make([]orgresolve.RuleTrace, len(t))
	for i, x := range t {
		x.Reason = r.str(x.Reason)
		out[i] = x
	}
	return out
}
