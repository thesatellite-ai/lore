package identity

// binding.go — a coding-tool session bound to the agent started in it
// (devco:D-889, owner 2026-11-22). A coding tool runs each command in a new
// shell, so the `export LORE_ACTOR=… LORE_SESSION=…` that `lore agent
// start` prints never reaches the agent's next command. Instead, agent
// start run inside a tool that names its session (detection's SessionID:
// CODEX_THREAD_ID, CLAUDE_CODE_SESSION_ID, OPENCODE_SESSION_ID, …) keeps
// one small file in the lore home, and the chain reads it for every later
// command of that same tool session:
//
//	lore agent start --label L   (inside tool T, session S)
//	    ─▶ Bind(home, {T, S, agent:L, s-…})  <home>/state/agent-bindings/<T>-<hash(S)>.json
//	later command in T, session S (a new shell, no LORE_ACTOR)
//	    ─▶ Resolve: LORE_ACTOR/--actor? no ─▶ LORE_HUMAN=1? no
//	       ─▶ BoundTo(home, T, S) ─▶ agent:L, Step StepBound, Session s-…
//	`lore agent stop` (or the tool's session-end hook) ─▶ Unbind
//
// A binding belongs to the person (and org) who started the agent
// (devco:D-895): the same tool session in a repo where another person, or
// the same person in another org, answers the chain is not bound, so an
// agent never writes for someone it was not started for (a file without
// a person, written before D-895, binds nobody).
//
// The file is machine-local and private (0600 in a 0700 folder): the tool
// session id is never written, only its hash. A binding older than
// bindingMaxAge is removed when the next one is written, so a session that
// ended without its end hook leaves nothing behind for long (a tool never
// reuses a session id, so a stale file never matches anyway).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/logic/agentdetect"
	"github.com/thesatellite-ai/lore/model/errs"
)

// BindingsDir is the folder under the lore home that holds the bindings
// (slash path: machine-local state, never synced).
const BindingsDir = "state/agent-bindings"

// StepBound answers when this coding-tool session was bound to an agent
// by `lore agent start` (after LORE_ACTOR and LORE_HUMAN=1, before
// detection).
const StepBound Step = "agent start binding"

const (
	// bindingExt ends every binding file name.
	bindingExt = ".json"
	// bindingHashHex is how many hex characters of the session hash name
	// the file (96 bits: no two sessions of one tool collide).
	bindingHashHex = 24
	// bindingMaxAge is how long a binding lives without its session's end.
	bindingMaxAge = 30 * 24 * time.Hour
)

// toolSlug is what a tool slug may hold to become a file name part.
var toolSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// Binding is one coding-tool session bound to a started agent.
type Binding struct {
	// Tool is the detected tool's slug (codex, claude_code, …).
	Tool string `json:"tool"`
	// Actor is the agent's stable key (agent:<label>).
	Actor string `json:"actor"`
	// Session is the LORE_SESSION agent start made.
	Session string `json:"session"`
	// Person is the stable key of the person who started the agent: the
	// binding answers only when the chain resolves this person again
	// (devco:D-895), never for anyone else on the machine.
	Person string `json:"person"`
	// Org is the org entry the person was in ("" outside an org): the
	// agent is on that org's roster only.
	Org string `json:"org,omitempty"`
	// At is when agent start bound it.
	At time.Time `json:"at"`
}

// ErrNotBindable: the tool or the session cannot name a binding.
var ErrNotBindable = errors.New("identity: no tool session to bind")

// bindingPath is the file of tool's session sessionID under home.
func bindingPath(home, tool, sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if !toolSlug.MatchString(tool) || sessionID == "" || strings.TrimSpace(home) == "" {
		return "", ErrNotBindable
	}
	sum := sha256.Sum256([]byte(tool + "\x00" + sessionID))
	return filepath.Join(home, filepath.FromSlash(BindingsDir), tool+"-"+hex.EncodeToString(sum[:])[:bindingHashHex]+bindingExt), nil
}

// Bind binds tool's session sessionID to b.Actor (an agent:<label> key),
// replacing an earlier binding of the same session, and removes bindings
// older than bindingMaxAge.
func Bind(home, sessionID string, b Binding) error {
	if kindOf(b.Actor) != KindAgent || len(b.Actor) <= len(KindAgent+kindSep) {
		return errs.New(errs.InvalidInput, "identity: only an agent:<label> actor can be bound to a tool session")
	}
	if kindOf(b.Person) == KindAgent || strings.TrimSpace(b.Person) == "" {
		return errs.New(errs.InvalidInput, "identity: a tool session is bound for the person who started the agent; no person was given")
	}
	path, err := bindingPath(home, b.Tool, sessionID)
	if err != nil {
		return errs.Wrap(errs.InvalidInput, "identity: bind a tool session", err)
	}
	if b.At.IsZero() {
		b.At = time.Now().UTC()
	}
	data, err := json.Marshal(b)
	if err != nil {
		return errs.Wrap(errs.Internal, "identity: encode the binding", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return errs.Wrap(errs.Internal, "identity: make "+BindingsDir, err)
	}
	pruneBindings(dir, b.At)
	tmp, err := os.CreateTemp(dir, ".bind-*")
	if err != nil {
		return errs.Wrap(errs.Internal, "identity: write the binding", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after the rename; a leftover on failure
	if err := tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		return errs.Wrap(errs.Internal, "identity: write the binding", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return errs.Wrap(errs.Internal, "identity: write the binding", err)
	}
	if err := tmp.Close(); err != nil {
		return errs.Wrap(errs.Internal, "identity: write the binding", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil { // atomic: a reader sees the old or the new binding
		return errs.Wrap(errs.Internal, "identity: write the binding", err)
	}
	return nil
}

// BoundTo is the binding of tool's session sessionID; ok is false when
// there is none (or it cannot be read: a broken file never stops a
// command, it only leaves the session unbound).
func BoundTo(home, tool, sessionID string) (Binding, bool) {
	path, err := bindingPath(home, tool, sessionID)
	if err != nil {
		return Binding{}, false
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Binding{}, false
	}
	var b Binding
	if json.Unmarshal(data, &b) != nil || kindOf(b.Actor) != KindAgent || b.Tool != tool {
		return Binding{}, false
	}
	return b, true
}

// boundFor is the binding of det's tool session when it was made for
// person (same stable key and org); a binding made for anyone else (or
// with no person, before devco:D-895) does not answer.
func boundFor(home string, person Resolved, det agentdetect.Result) (Binding, bool) {
	b, ok := BoundTo(home, det.Tool, det.SessionID)
	if !ok || b.Person == "" || b.Person != person.StableKey || b.Org != person.Org {
		return Binding{}, false
	}
	return b, true
}

// Unbind removes the binding of tool's session sessionID and returns it;
// ok is false when there was none.
func Unbind(home, tool, sessionID string) (Binding, bool, error) {
	b, ok := BoundTo(home, tool, sessionID)
	path, err := bindingPath(home, tool, sessionID)
	if err != nil {
		return Binding{}, false, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Binding{}, false, errs.Wrap(errs.Internal, "identity: remove the binding", err)
	}
	return b, ok, nil
}

// pruneBindings removes the bindings in dir older than bindingMaxAge
// (best effort: a file that cannot be removed is tried again next time).
func pruneBindings(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), bindingExt) {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > bindingMaxAge {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// boundOf is the identity of a bound tool session: the same identity an
// explicit LORE_ACTOR=<b.Actor> LORE_SESSION=<b.Session> prefix gives
// (an agent:<label> is accepted inside an org too), with the detection
// kept and the person's org and notes carried.
func boundOf(person Resolved, b Binding, det *agentdetect.Result) Resolved {
	r := fromKey(b.Actor, "", StepBound)
	r.Agent, r.Org, r.Session = r.StableKey, person.Org, b.Session
	r.Notes, r.ActorNote, r.Detection = person.Notes, person.ActorNote, det
	return r
}

// SessionOf is who's agent session id: LORE_SESSION when set (an
// explicit prefix wins), else the session a tool binding gave (StepBound),
// else "".
func SessionOf(who Resolved) string {
	if v := strings.TrimSpace(os.Getenv(EnvSession)); v != "" {
		return v
	}
	return who.Session
}
