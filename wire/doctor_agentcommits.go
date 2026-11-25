package wire

// doctor_agentcommits.go — the agent_commits check of `lore doctor`
// (devco:D-983, hunt H-248). Older lore versions signed some of their own
// commits as the detected agent: author and committer "<person> via
// <tool>" <agent.auto/<tool>/<hex>@lore.invalid>, and a Lore-Agent trailer
// naming the tool. Pushed, those commits put an agent's name into the
// history of the person's remotes. lore signs every commit as the person
// now (gitcommit.IsAgentSignature guards every signer); this check finds
// the old ones still on lore's refs, so the person can rewrite them:
//
//	code repo   refs/lore/* (signals, live, …)
//	store clone refs/heads/* and refs/lore/*
//	   ─▶ per ref: how many commits name an agent, one example
//	   ─▶ fix: git filter-repo over those refs only (name, e-mail and
//	      message callbacks), then git push --force of each ref
//
// It only reads; `lore doctor --fix` does not rewrite history (a forced
// push is the person's call).

import (
	"context"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitcommit"
	"github.com/thesatellite-ai/lore/store"
)

// DoctorCheckAgentCommits is the check's JSON name.
const DoctorCheckAgentCommits DoctorCheck = "agent_commits"

// agentTrailerPrefix is the message trailer older versions added to name
// the agent a commit was made through.
const agentTrailerPrefix = "Lore-Agent:"

// agentCommitRefs are the ref namespaces the check reads: a code repo
// holds lore's commits on refs/lore only; a store clone on its branches
// too.
var (
	agentCommitRefsRepo  = []string{"refs/lore"}
	agentCommitRefsStore = []string{"refs/heads", "refs/lore"}
)

// agentRefCount is one ref holding commits that name an agent.
type agentRefCount struct {
	ref     string
	count   int
	example string // "name <email>" of the first such commit, or its trailer
}

// DoctorAgentCommits warns, per git directory, about lore refs whose
// commits are authored or committed as an agent or carry a Lore-Agent
// trailer, and prints how to rewrite them as the person.
func (a *App) DoctorAgentCommits(ctx context.Context) ([]DoctorFinding, error) {
	type place struct {
		dir  string
		src  store.Source
		refs []string
	}
	var places []place
	if top := gitTopLevel(ctx, a.projectDir()); top != "" {
		places = append(places, place{dir: top, refs: agentCommitRefsRepo})
	}
	if st, err := a.state(ctx); err == nil {
		seen := map[string]bool{}
		for _, src := range a.Stores() {
			gb := gitOf(st.byName[src])
			if gb == nil || !gb.CloneMode() || seen[gb.Dir()] {
				continue
			}
			seen[gb.Dir()] = true
			places = append(places, place{dir: gb.Dir(), src: src, refs: agentCommitRefsStore})
		}
	}
	var out []DoctorFinding
	for _, p := range places {
		counts := agentCommitCounts(ctx, p.dir, p.refs)
		if len(counts) == 0 {
			continue
		}
		var refs, parts []string
		total := 0
		for _, c := range counts {
			refs = append(refs, c.ref)
			total += c.count
			parts = append(parts, c.ref+" ("+strconv.Itoa(c.count)+", e.g. "+c.example+")")
		}
		name := effectiveGitConfig(ctx, p.dir, "user.name")
		email := effectiveGitConfig(ctx, p.dir, "user.email")
		msg := "the git repo " + p.dir + " holds " + strconv.Itoa(total) + " lore commit(s) signed as an agent or naming one in a Lore-Agent trailer, written by an older lore version: " + strings.Join(parts, ", ") + "; lore signs every commit as you now, and these keep the agent in the history of every remote they were pushed to"
		fix := agentCommitsFix(p.dir, refs, name, email)
		if fix == "" {
			msg += "; set your git identity there first (git config user.name / user.email), then run lore doctor again for the rewrite commands"
		}
		out = append(out, DoctorFinding{Check: DoctorCheckAgentCommits, Level: DoctorWarning, Store: p.src, Message: msg, Fix: fix})
	}
	return out, nil
}

// agentCommitCounts reads every ref under the namespaces in dir and counts
// the commits naming an agent (gitcommit.IsAgentSignature on author or
// committer, or a Lore-Agent trailer). A git error means no finding: the
// check never fails doctor for a ref it cannot read.
func agentCommitCounts(ctx context.Context, dir string, namespaces []string) []agentRefCount {
	listed, err := gitRun{dir: dir, args: append([]string{"for-each-ref", "--format=%(refname)"}, namespaces...)}.output(ctx)
	if err != nil {
		return nil
	}
	var out []agentRefCount
	for _, ref := range strings.Fields(string(listed)) {
		// one record per commit: author, committer, then the message,
		// fields split by 0x1f and records by 0x1e
		log, err := gitRun{dir: dir, args: []string{"log", "--format=%an\x1f%ae\x1f%cn\x1f%ce\x1f%B\x1e", ref, "--"}}.output(ctx)
		if err != nil {
			continue
		}
		c := agentRefCount{ref: ref}
		for _, rec := range strings.Split(string(log), "\x1e") {
			f := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x1f", 5)
			if len(f) != 5 {
				continue
			}
			ex := ""
			switch {
			case gitcommit.IsAgentSignature(f[0], f[1]):
				ex = f[0] + " <" + f[1] + ">"
			case gitcommit.IsAgentSignature(f[2], f[3]):
				ex = f[2] + " <" + f[3] + ">"
			default:
				for l := range strings.SplitSeq(f[4], "\n") {
					if strings.HasPrefix(l, agentTrailerPrefix) {
						ex = strings.TrimSpace(l)
						break
					}
				}
			}
			if ex == "" {
				continue
			}
			if c.count == 0 {
				c.example = ex
			}
			c.count++
		}
		if c.count > 0 {
			out = append(out, c)
		}
	}
	return out
}

// agentCommitsFix is the copy-paste rewrite of refs in dir as the person
// name <email>: git filter-repo over those refs only (--refs keeps every
// other ref and the remotes as they are), replacing an agent's name and
// e-mail and dropping Lore-Agent trailers, then one forced push per ref
// to origin. "" when the person's git identity is unknown or is itself an
// agent's.
func agentCommitsFix(dir string, refs []string, name, email string) string {
	if name == "" || email == "" || gitcommit.IsAgentSignature(name, email) {
		return ""
	}
	nameCB := "return " + strconv.Quote(name) + ".encode() if b\" via \" in name else name"
	emailCB := "return " + strconv.Quote(email) + ".encode() if email.lower().endswith(b\"@" + gitcommit.AgentEmailDomain + "\") else email"
	msgCB := "return b\"\".join(l for l in message.splitlines(True) if not l.startswith(b\"" + agentTrailerPrefix + "\"))"
	fix := "git -C " + shellWord(dir) + " filter-repo --force --refs " + strings.Join(refs, " ") +
		" --name-callback " + shellQuote(nameCB) + " --email-callback " + shellQuote(emailCB) + " --message-callback " + shellQuote(msgCB)
	for _, r := range refs {
		fix += " && git -C " + shellWord(dir) + " push --force origin " + r
	}
	return fix + " (git filter-repo: https://github.com/newren/git-filter-repo; the forced push replaces each ref on origin, so teammates fetch again)"
}
