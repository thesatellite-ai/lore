package coding

// seed.go — the files of the orbit checkout the trap run's agents edit.
// The context gate needs only a path per task (StubFiles); an agent that
// writes code needs something to write it into, so the trap files hold
// small, plausible Go/TS/SQL, and the helpers the hotfixes name
// (clock.UTC, money.RoundHalfEven) exist, so the agent with lore can use
// them and the agent without lore could find them by reading the repo.
// Nothing in these files says what a hotfix says: the trap must be
// avoidable only by knowing the hotfix.

import "path"

// Module is the orbit checkout's Go module path.
const Module = "example.com/orbit"

// NewFiles are task files a trap asks the agent to create: the trap run's
// checkout leaves them out.
var NewFiles = []string{"db/migrations/0043_events_created_index.sql"}

// StubFiles is a one-line file per task path and a README: the context
// gate's checkout (lore reads paths there, never contents).
func StubFiles() map[string]string {
	files := map[string]string{"README.md": "// README.md\n"}
	for _, tk := range Tasks() {
		files[tk.File] = "// " + tk.File + "\n"
	}
	return files
}

// stub is a one-line comment naming the file, in the file's language.
func stub(file string) string {
	switch path.Ext(file) {
	case ".sql":
		return "-- " + file + "\n"
	case ".yaml", ".yml":
		return "# " + file + "\n"
	case ".css":
		return "/* " + file + " */\n"
	}
	return "// " + file + "\n"
}

// RepoFiles is the trap run's checkout: every task path (stubs) with the
// trap files and the helpers they lean on written out, less NewFiles.
func RepoFiles() map[string]string {
	files := map[string]string{}
	for _, tk := range Tasks() {
		files[tk.File] = stub(tk.File)
	}
	for _, f := range NewFiles {
		delete(files, f)
	}
	files["go.mod"] = "module " + Module + "\n\ngo 1.22\n"
	for p, body := range seeds {
		files[p] = body
	}
	return files
}

// seeds are the files with real content.
var seeds = map[string]string{
	"README.md": "# orbit\n\nA billing and projects service: Go API (api/, billing/, internal/), Postgres migrations (db/), React front end (web/).\n",

	"internal/clock/clock.go": `package clock

import "time"

// UTC is the current time in UTC.
func UTC() time.Time { return time.Now().UTC() }
`,

	"billing/money/money.go": `// Package money holds billing's money math (amounts are int64 cents).
package money

import "math"

// RoundHalfEven rounds x to the nearest whole cent, halves to the even cent.
func RoundHalfEven(x float64) int64 { return int64(math.RoundToEven(x)) }
`,

	"billing/invoice.go": `package billing

import (
	"fmt"
	"time"
)

// Line is one invoice line.
type Line struct {
	Desc        string
	AmountCents int64
}

// Invoice is a customer invoice.
type Invoice struct {
	ID       string
	Customer string
	Lines    []Line
	Final    bool
	IssuedAt time.Time
	DueAt    time.Time
}

// Total is the sum of the invoice's lines in cents.
func (inv *Invoice) Total() int64 {
	var sum int64
	for _, l := range inv.Lines {
		sum += l.AmountCents
	}
	return sum
}

// Finalize locks the invoice so it can be sent.
func (inv *Invoice) Finalize() error {
	if inv.Final {
		return fmt.Errorf("finalize %s: already final", inv.ID)
	}
	inv.Final = true
	return nil
}
`,

	"billing/tax.go": `package billing

// VATCents is the VAT on a net amount in cents at ratePercent (21 for 21%).
// EU customers report that some invoices are a cent off.
func VATCents(netCents int64, ratePercent float64) int64 {
	return int64(float64(netCents) * ratePercent / 100)
}
`,

	"api/handlers/webhooks.go": `package handlers

import (
	"io"
	"net/http"
)

// PaymentWebhook receives the payment provider's events. The provider
// signs the raw body with HMAC-SHA256 and our webhook secret and sends the
// hex digest in the X-Signature header.
func PaymentWebhook(secret []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		_ = body
		w.WriteHeader(http.StatusNoContent)
	}
}
`,

	"db/migrations/0001_events.sql": `CREATE TABLE events (
    id bigserial PRIMARY KEY,
    kind text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
`,

	"internal/cache/redis.go": `package cache

import "github.com/gomodule/redigo/redis"

// pool is the process's redis connection pool.
var pool = &redis.Pool{
	MaxIdle:   8,
	MaxActive: 64,
	Dial:      func() (redis.Conn, error) { return redis.Dial("tcp", "localhost:6379") },
}
`,

	"web/src/auth/Login.tsx": `import { useState } from "react";
import { api } from "../api";

export function Login() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const { token } = await api.login({ email, password });
    document.cookie = ` + "`session=${token}; path=/; Secure`" + `;
    window.location.assign("/");
  }

  return (
    <form onSubmit={submit}>
      <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
      <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
      <button type="submit">Log in</button>
    </form>
  );
}
`,

	"internal/auth/ratelimit.go": `package auth

import "time"

// LoginLimit is how many failed logins an account may make per window.
const LoginLimit = 5

// LoginWindow is the window failed logins are counted in.
const LoginWindow = 15 * time.Minute

// AllowLogin reports whether account may try to log in again.
func AllowLogin(account string) bool {
	return true
}

// LoginFailed records a failed login of account.
func LoginFailed(account string) {}
`,
}
