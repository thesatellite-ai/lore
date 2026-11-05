package gitremote

import (
	"context"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/gitref"
)

// RefspecState reports a remote's fetch configuration for lore refs.
type RefspecState struct {
	// Added is true when this call added the tracking refspec.
	Added bool
	// Unsafe lists configured fetch refspecs whose destination is inside
	// refs/lore/* itself. A plain `git fetch` with one of them overwrites
	// local live and signals refs, losing unpushed live writes (the bug F3
	// exists to prevent). lore's own fetches ignore them (`--refmap=`), but
	// the user's do not: `lore doctor` reports them; removing one is the
	// user's decision.
	Unsafe []string
}

// configFetchKey is the multi-valued config key holding a remote's fetch
// refspecs.
func configFetchKey(remote string) string { return "remote." + remote + ".fetch" }

// EnsureTrackingRefspec makes sure remote's configured fetch refspecs
// include the lore tracking refspec (TrackingRefspecs(remote)[0]), so a
// plain `git fetch` also keeps refs/lore-remote/<remote>/* current. It is
// idempotent and never removes or rewrites an existing refspec. remote must
// be a configured remote name (not a URL).
func EnsureTrackingRefspec(ctx context.Context, r *gitref.Repo, remote string) (RefspecState, error) {
	want := TrackingRefspecs(remote)[0]
	key := configFetchKey(remote)
	out, err := r.Run(ctx, gitref.Invocation{Args: []string{"config", "--get-all", key}})
	if err != nil {
		// Exit 1 = key not set: nothing configured yet, which is fine.
		if code, isExit := gitref.ExitCode(err); !isExit || code != configMissingExit {
			return RefspecState{}, err
		}
	}
	var st RefspecState
	present := false
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		spec := strings.TrimSpace(line)
		if spec == "" {
			continue
		}
		if spec == want {
			present = true
		}
		if _, dst, ok := strings.Cut(strings.TrimPrefix(spec, "+"), ":"); ok && strings.HasPrefix(dst, LoreRefsPrefix) {
			st.Unsafe = append(st.Unsafe, spec)
		}
	}
	if present {
		return st, nil
	}
	if _, err := r.Run(ctx, gitref.Invocation{Args: []string{"config", "--add", key, want}}); err != nil {
		return RefspecState{}, err
	}
	st.Added = true
	return st, nil
}

// configMissingExit is `git config --get-all`'s exit status when the key is
// not set.
const configMissingExit = 1
