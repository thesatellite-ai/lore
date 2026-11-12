package store

import (
	"context"
	"strings"
)

// Per-write commit message (plain files round, brief 2.1): a write path
// that writes on someone's behalf with a meaning of its own (the pick-up of
// hand edits: "hand edit: tasks tsk_…") names the commit itself. A backend
// that makes commits uses it instead of its own subject; one without
// commits ignores it, so it is never part of the Backend contract.
//
//	wire pick-up ─ WithCommitMessage(ctx, "hand edit: tasks tsk_…", lines…)
//	  └─ Backend.Write(ctx, …) ─ git backend: CommitMessage(ctx) → commit -m

// commitMessageKey is the context key of the per-write commit message.
type commitMessageKey struct{}

// commitMessage is a subject and its body lines.
type commitMessage struct {
	subject string
	body    []string
}

// WithCommitMessage returns ctx whose writes are committed with subject
// (one line) and body (one line each, after a blank line). An empty or
// multi-line subject leaves ctx as it is.
func WithCommitMessage(ctx context.Context, subject string, body ...string) context.Context {
	subject = strings.TrimSpace(subject)
	if subject == "" || strings.ContainsAny(subject, "\r\n") {
		return ctx
	}
	var keep []string
	for _, l := range body {
		if l = strings.TrimSpace(l); l != "" && !strings.ContainsAny(l, "\r\n") {
			keep = append(keep, l)
		}
	}
	return context.WithValue(ctx, commitMessageKey{}, commitMessage{subject: subject, body: keep})
}

// CommitMessage is the message ctx carries for its writes' commits ("" and
// false when none): the subject, then a blank line and the body lines.
func CommitMessage(ctx context.Context) (string, bool) {
	m, ok := ctx.Value(commitMessageKey{}).(commitMessage)
	if !ok || m.subject == "" {
		return "", false
	}
	if len(m.body) == 0 {
		return m.subject, true
	}
	return m.subject + "\n\n" + strings.Join(m.body, "\n"), true
}

// Per-write author (brief 2.1, "committed as that person"): a write that
// is a person's own change lore only checks and commits for them (the
// pick-up of hand edits) is authored by that person even in a store whose
// commits an org identity signs; the org identity stays the committer. A
// backend without commits ignores it.
//
//	wire pick-up ─ WithPersonAuthor(ctx)
//	  └─ git backend: PersonAuthored(ctx) → GIT_AUTHOR_* = the person,
//	                  GIT_COMMITTER_* = the org identity (when it signs)

// personAuthorKey is the context key of WithPersonAuthor.
type personAuthorKey struct{}

// WithPersonAuthor returns ctx whose writes are committed with the person
// as author (PersonAuthored).
func WithPersonAuthor(ctx context.Context) context.Context {
	return context.WithValue(ctx, personAuthorKey{}, true)
}

// PersonAuthored reports whether ctx's writes are the person's own change
// (WithPersonAuthor), so their commits name the person as author.
func PersonAuthored(ctx context.Context) bool {
	v, _ := ctx.Value(personAuthorKey{}).(bool)
	return v
}
