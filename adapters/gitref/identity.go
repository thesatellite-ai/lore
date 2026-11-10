package gitref

import (
	"context"
	"strings"
)

// GitIdentity is the identity git itself would sign a commit in this repo
// with (`git config user.name` / `user.email`, which also read the
// GIT_CONFIG_* layers and includes). ok is false when either is unset, so
// the caller falls back to its own signer.
//
// FLAG4 (owner): a commit lore makes on a person's behalf follows "git
// identity wins": the person's own git identity when git has one, else the
// lore identity. Callers that must not carry a personal address (an org
// store, Q-R2-1) do not ask.
func (r *Repo) GitIdentity(ctx context.Context) (Signature, bool) {
	name, nerr := r.Run(ctx, Invocation{Args: []string{"config", "--get", "user.name"}})
	email, eerr := r.Run(ctx, Invocation{Args: []string{"config", "--get", "user.email"}})
	if nerr != nil || eerr != nil {
		return Signature{}, false
	}
	n, e := strings.TrimSpace(string(name)), strings.TrimSpace(string(email))
	if n == "" || e == "" || strings.ContainsAny(n+e, "\n\r<>") {
		return Signature{}, false
	}
	return Signature{Name: n, Email: e}, true
}
