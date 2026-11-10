package wire

// remote_request.go — a request served for another process (`lore api`,
// OV3 on REST): the agent and the session belong to the HTTP client, never
// to the server process. The CLI runner's audit links every write to this
// process's session row (routedAudit → SessionSource, App.CurrentSession,
// detected from the process environment); a REST request must not inherit
// the server's terminal session that way, so cmd/lore's API runner marks
// each request's context and the audit leaves its session link empty.

import (
	"context"
	"sync/atomic"
)

// remoteRequestKey marks a context as one REST request.
type remoteRequestKey struct{}

// requestID numbers the REST requests of this process from 1; 0 is "no
// request" (a CLI command). State kept per command (storePush's read
// fetch) is kept per request under `lore api`, where one App serves every
// request of the process.
type requestID uint64

// lastRequestID is the newest request number handed out.
var lastRequestID atomic.Uint64

// RemoteRequest marks ctx as a request served for another process
// (`lore api`): the audit records no process session for its writes. Each
// call is a new request.
func RemoteRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, remoteRequestKey{}, requestID(lastRequestID.Add(1)))
}

// IsRemoteRequest reports whether ctx was marked by RemoteRequest.
func IsRemoteRequest(ctx context.Context) bool { return requestOf(ctx) != 0 }

// requestOf is ctx's request number, 0 outside a REST request.
func requestOf(ctx context.Context) requestID {
	v, _ := ctx.Value(remoteRequestKey{}).(requestID)
	return v
}
