package wire

// remote_request.go — a request served for another process (`lore api`,
// OV3 on REST): the agent and the session belong to the HTTP client, never
// to the server process. The CLI runner's audit links every write to this
// process's session row (routedAudit → SessionSource, App.CurrentSession,
// detected from the process environment); a REST request must not inherit
// the server's terminal session that way, so cmd/lore's API runner marks
// each request's context and the audit leaves its session link empty.

import "context"

// remoteRequestKey marks a context as one REST request.
type remoteRequestKey struct{}

// RemoteRequest marks ctx as a request served for another process
// (`lore api`): the audit records no process session for its writes.
func RemoteRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, remoteRequestKey{}, true)
}

// IsRemoteRequest reports whether ctx was marked by RemoteRequest.
func IsRemoteRequest(ctx context.Context) bool {
	v, ok := ctx.Value(remoteRequestKey{}).(bool)
	return ok && v
}
