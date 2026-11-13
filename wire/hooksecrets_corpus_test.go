package wire_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	"github.com/thesatellite-ai/lore/model/errs"
)

// corpusBody is a deterministic token body of n characters with letters
// outside the hex range, so no window of it can show up in a sha256
// preview by chance (built at run time, CLAUDE.md convention 8).
func corpusBody(n int) string {
	const set = "gH7kM2nP9qR4sT6vW8xYz3JhLtNrQwZ5"
	var b strings.Builder
	for i := range n {
		b.WriteByte(set[(i*7+3)%len(set)])
	}
	return b.String()
}

// refusalSecrets are real secret shapes in the lines people commit or
// save; secret is the part that must never be printed, not even a piece.
func refusalSecrets() []struct{ line, secret string } {
	b := corpusBody
	pw := "pass" + "word"
	return []struct{ line, secret string }{
		{"DATABASE_URL=postgres://app:Zq7" + b(14) + "@db.internal/app", "Zq7" + b(14)},
		{pw + `: "Zq7` + b(14) + `"`, "Zq7" + b(14)},
		{"GITHUB_TOKEN=ghp_" + b(36), b(36)},
		{"OPENAI_API_KEY=sk-proj-" + b(48), b(48)},
		{"SLACK_BOT_TOKEN=xoxb-" + b(24), b(24)},
		{"AWS_ACCESS_KEY_ID=AKIA" + strings.ToUpper(b(16)), strings.ToUpper(b(16))},
		{"curl -H \"Authorization: Bearer " + b(40) + "\" https://api.example.com", b(40)},
	}
}

// assertNoPiece fails when out holds any 6-character window of secret.
func assertNoPiece(t *testing.T, what, secret, out string) {
	t.Helper()
	for i := 0; i+6 <= len(secret); i++ {
		if strings.Contains(out, secret[i:i+6]) {
			t.Errorf("%s prints part of the value (window at %d of %d)", what, i, len(secret))
			return
		}
	}
}

// errText is every printed form of an error: message, hint and JSON.
func errText(t *testing.T, err error) string {
	t.Helper()
	out := err.Error()
	var e *errs.Error
	if errors.As(err, &e) {
		out += "\n" + e.Hint
	}
	j, _ := json.Marshal(err)
	return out + "\n" + string(j) + "\n" + fmt.Sprintf("%+v", err)
}

// TestRefusalsNeverPrintTheValue (S-4): a commit and a row write holding
// a real secret are refused, and no printed form of the refusal (message,
// hint, JSON) carries any 6-character piece of the value. A real-world
// .env.example beside them passes the commit check.
func TestRefusalsNeverPrintTheValue(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	ctx := context.Background()
	pw := "pass" + "word"
	example := "DATABASE_URL=postgres:" + "//postgres:" + "postgres@localhost:5432/app\n" +
		"OPENAI_API_KEY=sk-" + strings.Repeat("x", 40) + "\n" +
		"SLACK_BOT_TOKEN=xo" + "xb-your-bot-token\n" +
		"DB_" + strings.ToUpper(pw) + "=changeme\n"
	stageFile(t, h.Repo, ".env.example", example)
	if err := h.App.HookSecrets(ctx, h.Repo, commitWhere, map[string][]byte{".env.example": []byte(example)}); err != nil {
		t.Fatalf(".env.example: err = %v, want pass", err)
	}
	for i, c := range refusalSecrets() {
		rel := fmt.Sprintf("conf/s%d.env", i)
		stageFile(t, h.Repo, rel, c.line+"\n")
		err := h.App.HookSecrets(ctx, h.Repo, commitWhere, map[string][]byte{rel: []byte(c.line + "\n")})
		if !errs.Has(err, errs.LeakBlocked) {
			t.Errorf("fixture %d: commit err = %v, want E_LEAK_BLOCKED", i, err)
			continue
		}
		assertNoPiece(t, fmt.Sprintf("commit refusal %d", i), c.secret, errText(t, err))
		body, _ := json.Marshal(map[string]string{"body": "note: " + c.line})
		_, err = h.Run("memory.add", string(body))
		if err == nil {
			t.Errorf("fixture %d: memory.add stored a secret", i)
			continue
		}
		assertNoPiece(t, fmt.Sprintf("write refusal %d", i), c.secret, errText(t, err))
	}
}
