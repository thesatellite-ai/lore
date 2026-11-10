package op

import "context"

// remoteKey marks a call that arrived over the network (REST, `lore api`)
// rather than from the person's own terminal.
type remoteKey struct{}

// WithRemote returns ctx marked as a network call. The REST surface sets
// it on every request; the CLI never does.
//
// Why: the API is a different trust boundary from the CLI. A path the
// person types on the CLI (`lore render --out ~/notes/x.md`) is their own
// choice; the same path in a JSON body comes from whatever HTTP client
// reached the server, so a Local operation that writes files refuses any
// path outside the repository when Remote(ctx) is true.
func WithRemote(ctx context.Context) context.Context {
	return context.WithValue(ctx, remoteKey{}, true)
}

// Remote reports whether ctx carries the network-call mark; false when no
// surface set one (the CLI, tests).
func Remote(ctx context.Context) bool {
	on, _ := ctx.Value(remoteKey{}).(bool)
	return on
}

// pageKey marks a call from the setup page's own server (127.0.0.1, its
// session cookie and CSRF check), as opposed to `lore api`.
type pageKey struct{}

// WithPage returns ctx marked as a setup-page call. surface/setupweb sets
// it after the session check; `lore api` never does.
//
// Why: both are REST (op.Remote), but only the page is the person at
// their own screen; an op that acts on that screen (machine.reveal opens
// Finder) runs for the page and the CLI, never for any local HTTP client.
func WithPage(ctx context.Context) context.Context {
	return context.WithValue(ctx, pageKey{}, true)
}

// Page reports whether ctx carries the setup-page mark.
func Page(ctx context.Context) bool {
	on, _ := ctx.Value(pageKey{}).(bool)
	return on
}
