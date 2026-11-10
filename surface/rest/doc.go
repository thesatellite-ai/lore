// Package rest is the local `lore api` HTTP surface (STORE_SERVER_SPEC 9.3,
// 9.4, 9.7, 9.8, 12): one route per registered operation, built at startup
// from an op.Registry, every request running through the injected op.Runner.
//
// Why it exists: agents and tools that cannot shell out talk to lore over
// HTTP with exactly the inputs, errors and checks of the CLI. A handler
// never calls an operation's run function (rest_test proves it), so the
// scanner, policy, confirmations and audit apply on every route.
//
// Security, closed by default: every request (including /openapi.json and
// /v1/spec) needs `Authorization: Bearer <token>`, compared in constant
// time; the server binds 127.0.0.1 only; request bodies are capped at
// MaxBodyBytes; no CORS headers are ever sent, so a browser page cannot call
// it. The token lives in <home>/api-token, mode 0600 (token.go).
//
// HTTP statuses come only from the error's class in model/errs (owner
// decisions O3, O5): E_INTERNAL is the one code that answers 500.
//
// Only net/http from the standard library is used.
package rest
