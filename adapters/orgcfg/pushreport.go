package orgcfg

// pushreport.go — the org link and org file commits push to a store
// (commitStore); the command's output names each push (no hidden network
// writes, plain files sweep 3.3, 2026-11-12). orgcfg prints nothing
// itself: the caller puts a reporter on the context and gets one call per
// push the remote accepted.

import "context"

// StorePush is one push of an org write to a store.
type StorePush struct {
	// Store is the store's normalised url.
	Store string
	// Branch is the store branch the commit went to.
	Branch string
	// Commit is the full commit id now at the branch tip.
	Commit string
}

// pushReporterKey carries the reporter on a context.
type pushReporterKey struct{}

// WithPushReporter returns ctx whose org store pushes call report.
func WithPushReporter(ctx context.Context, report func(StorePush)) context.Context {
	return context.WithValue(ctx, pushReporterKey{}, report)
}

// reportPush hands p to ctx's reporter, if any.
func reportPush(ctx context.Context, p StorePush) {
	if f, ok := ctx.Value(pushReporterKey{}).(func(StorePush)); ok && f != nil {
		f(p)
	}
}
