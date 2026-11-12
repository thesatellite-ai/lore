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
	// Present is true when the tracking refspec is configured (after
	// EnsureTrackingRefspec: always, unless it failed).
	Present bool
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
	st, err := InspectTrackingRefspec(ctx, r, remote)
	if err != nil || st.Present {
		return st, err
	}
	if _, err := r.Run(ctx, gitref.Invocation{Args: []string{"config", "--add", configFetchKey(remote), TrackingRefspecs(remote)[0]}}); err != nil {
		return RefspecState{}, err
	}
	st.Added, st.Present = true, true
	return st, nil
}

// InspectTrackingRefspec is EnsureTrackingRefspec's look without the
// write: Present says whether the tracking refspec is configured, Unsafe
// lists the dangerous ones. A read command uses it (it writes no git
// config, owner bug 6, 2026-11-12).
func InspectTrackingRefspec(ctx context.Context, r *gitref.Repo, remote string) (RefspecState, error) {
	want := TrackingRefspecs(remote)[0]
	out, err := r.Run(ctx, gitref.Invocation{Args: []string{"config", "--get-all", configFetchKey(remote)}})
	if err != nil {
		// Exit 1 = key not set: nothing configured yet, which is fine.
		if code, isExit := gitref.ExitCode(err); !isExit || code != configMissingExit {
			return RefspecState{}, err
		}
	}
	var st RefspecState
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		spec := strings.TrimSpace(line)
		if spec == "" {
			continue
		}
		if spec == want {
			st.Present = true
		}
		if _, dst, ok := strings.Cut(strings.TrimPrefix(spec, "+"), ":"); ok && strings.HasPrefix(dst, LoreRefsPrefix) {
			st.Unsafe = append(st.Unsafe, spec)
		}
	}
	return st, nil
}

// configMissingExit is `git config --get-all`'s exit status when the key is
// not set.
const configMissingExit = 1
