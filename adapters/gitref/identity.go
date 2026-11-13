package gitref

import (
	"context"
	"strings"
)

// identityKeys matches git's user.name and user.email (one
// `git config --get-regexp` run reads both).
const (
	identityKeys = `^user\.(name|email)$`
	keyUserName  = "user.name"
	keyUserEmail = "user.email"
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
	// One git run for both keys (a commit path asks often); the last value
	// of a key wins, as with `git config --get`.
	out, err := r.Run(ctx, Invocation{Args: []string{"config", "--get-regexp", identityKeys}})
	if err != nil {
		return Signature{}, false
	}
	var n, e string
	for l := range strings.SplitSeq(string(out), "\n") {
		k, v, _ := strings.Cut(l, " ")
		switch k {
		case keyUserName:
			n = strings.TrimSpace(v)
		case keyUserEmail:
			e = strings.TrimSpace(v)
		}
	}
	if n == "" || e == "" || strings.ContainsAny(n+e, "\n\r<>") {
		return Signature{}, false
	}
	return Signature{Name: n, Email: e}, true
}
