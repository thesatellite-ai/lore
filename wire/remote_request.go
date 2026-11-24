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
func IsRemoteRequest(ctx context.Context) bool {
	v, _ := ctx.Value(remoteRequestKey{}).(requestID)
	return v != 0
}

// servedCommandKey marks a context as one command the read server runs
// for a CLI process (ServedCommand).
type servedCommandKey struct{}

// ServedCommand marks ctx as one command of a process that serves many
// (the read server, app/readserver_unix.go): it gets its own request
// number, so the per-command state of a backend the server keeps across
// commands (a kept open, statepool.go: storePush's read fetch, its
// "commits wait for lore sync" line, OrgResolution's per-command sets)
// starts afresh for it, exactly as in a one-shot process. Why (bug hunt
// H-216): a kept repo open hands its backends to the next command; with
// every served command numbered 0 the second one inherited the first's
// "already said" and "already fetched" marks. Unlike RemoteRequest it
// leaves the audit's session link alone: the command is the CLI's own.
func ServedCommand(ctx context.Context) context.Context {
	return context.WithValue(ctx, servedCommandKey{}, requestID(lastRequestID.Add(1)))
}

// requestOf is ctx's request number: a REST request's, else a served
// command's (ServedCommand), else 0 (a one-shot CLI command).
func requestOf(ctx context.Context) requestID {
	if v, _ := ctx.Value(remoteRequestKey{}).(requestID); v != 0 {
		return v
	}
	v, _ := ctx.Value(servedCommandKey{}).(requestID)
	return v
}
