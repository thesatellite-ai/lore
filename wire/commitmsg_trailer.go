package wire

// commitmsg_trailer.go — the commit-msg half of F9 on the root-module
// binary (v0.1.17 task_coord.go commitMsgHook): while this session holds a
// claim on a task row that lives in the repo being committed to, the
// commit message gets `Lore-Task: <id>`, so a squash merge that keeps the
// trailer is still found by the merged check (mergewatch). A store-only
// task never puts its id into a client repo (Q13: HeldRecord.InRepo false),
// an expired claim adds nothing, and lore's own ride-along commits are
// skipped. Offline by design: it reads only this checkout's held.json.
//
//	`lore task hook commit-msg <file>` ─▶ session (coordSession) ─▶
//	<git dir>/lore/held.json records of it ─▶ live, InRepo ─▶ AddTrailer

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/identity"
	"github.com/thesatellite-ai/lore/adapters/mergewatch"
	"github.com/thesatellite-ai/lore/adapters/signals/coord"
)

// commitMsgFileMode is used only if git's message file vanished meanwhile
// (WriteFile keeps an existing file's mode): owner-only, it is private text.
const commitMsgFileMode = 0o600

// coordSession is the signals session of who: an agent's LORE_SESSION when
// set, else the person's stable key and this host (one session per person
// per machine). coordWorld.session calls it: the claim side and the hook
// share this one rule.
func coordSession(who identity.Resolved) string {
	if who.Kind == identity.KindAgent {
		if v := strings.TrimSpace(os.Getenv(identity.EnvSession)); v != "" {
			return v
		}
	}
	host, _ := os.Hostname() // no host name: the key alone still names the person
	return who.StableKey + humanSessionSeparator + host
}

// CommitMsgTrailers rewrites git's commit message file with a Lore-Task
// trailer per live in-repo claim this session holds (see the file comment).
func (a *App) CommitMsgTrailers(ctx context.Context, file string) error {
	if os.Getenv(envRideActive) == "1" {
		return nil
	}
	repo, err := gitref.Open(ctx, a.projectDir(), nil)
	if err != nil {
		return err
	}
	gitDir, err := repo.Run(ctx, gitref.Invocation{Args: []string{"rev-parse", "--absolute-git-dir"}})
	if err != nil {
		return err
	}
	held := coord.HeldStore{Path: filepath.Join(strings.TrimSpace(string(gitDir)), signalsStateDir, heldFile)}
	if !held.Any() {
		return nil // no claim in this clone: who is committing does not matter (D-16)
	}
	who, err := a.Who(ctx)
	if err != nil {
		return err
	}
	recs := held.List(coordSession(who))
	if len(recs) == 0 {
		return nil
	}
	// The message file is git's (the hook's argument): read and rewrite it
	// through a root at its folder, so the path can name nothing else.
	root, err := os.OpenRoot(filepath.Dir(file))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }() // the rewrite below already returned its own error
	name := filepath.Base(file)
	msg, err := root.ReadFile(name)
	if err != nil {
		return err
	}
	now := a.opts.Wall()
	out := msg
	for _, r := range recs {
		if now.After(r.Expires) || !r.InRepo {
			continue // expired, or a store-only task: its id never enters this repo (Q13)
		}
		out = mergewatch.AddTrailer(out, r.Task)
	}
	if string(out) == string(msg) {
		return nil
	}
	return root.WriteFile(name, out, commitMsgFileMode)
}
