// context_sessionstart.go — `lore context --hook <tool> --hook-stdin
// --session-start` (CONTEXT_BUDGET 6.9): the SessionStart answer, the
// in-flight tasks for this person and the rows changed since their last
// session in this repo, capped at contextSessionStartCap.
//
//	contextSessionStart
//	  ├─ last := ~/.lore/state/session-start/<sha256(repo top)[:16]> (then stamp now)
//	  ├─ group "In-flight tasks": task.list {status: in_progress, assignee: @me}, one line each
//	  └─ group "Changed since your last session": rules, hotfixes, memories,
//	       decisions with updated_at after last (none on the first session)
//
// The stamp is machine-local (like the session rows, OQ14). Every pick is a
// line form (id + short form), so the agent fetches the full row with
// `lore show <id>`; these ids are NOT added to the hook's seen set, so the
// per-file hook still sends a changed rule in full when its file is opened.

package renderhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thesatellite-ai/lore/model/errs"

	"github.com/thesatellite-ai/lore/logic/contextfit"
	"github.com/thesatellite-ai/lore/op"
	opstask "github.com/thesatellite-ai/lore/ops/task"
	"github.com/thesatellite-ai/lore/schema"
)

// contextSessionStartCap is the 6.9 SessionStart cap.
const contextSessionStartCap = 2000

// hookEventSessionStart is the SessionStart event name in the hook JSON.
const hookEventSessionStart = "SessionStart"

// sessionStartDir holds the per-repo "last session start" stamps.
const sessionStartDir = "state/session-start"

// Groups of the SessionStart answer.
const (
	groupInFlight = "In-flight tasks"
	groupChanged  = "Changed since your last session"
)

// contextSessionStart builds the SessionStart answer and stamps now.
func (h *Host) contextSessionStart(ctx context.Context) (contextResult, error) {
	stamp := sessionStartFile()
	last := readSessionStart(stamp)
	now := h.now()
	var picks []contextPick
	// A task.list failure (no actor row yet on a fresh machine) leaves the
	// task lines out and is logged; the changed rows still answer.
	tasks, err := h.inFlightTasks(ctx)
	if err != nil {
		h.logHookError(err)
	}
	for _, t := range tasks {
		line := contextfit.LineForm(t.ID, t.Title+" ("+t.Priority+")")
		picks = append(picks, contextPick{ID: t.ID, Kind: ctxKindTask, Why: "in progress", Bytes: len(line), full: line, line: line, group: groupInFlight})
	}
	if !last.IsZero() {
		rows, kerr := h.loadKnowledge(ctx)
		if kerr != nil {
			return contextResult{}, kerr
		}
		for _, k := range rows {
			if !k.r.UpdatedAt.After(last) {
				continue
			}
			p := pickOf(k, "changed since "+last.UTC().Format(time.RFC3339))
			p.full, p.group, p.Bytes = p.line, groupChanged, len(p.line)
			picks = append(picks, p)
		}
	}
	writeSessionStart(stamp, now)
	return contextResult{title: "this session", picks: picks, more: "lore task list --assignee @me"}, nil
}

// taskAssigneeMe is task.list's --assignee spelling of the caller. --mine
// would add team claims, but needs the team roster this surface lacks.
const taskAssigneeMe = "@me"

// inFlightTasks runs task.list for in-progress tasks assigned to the caller.
func (h *Host) inFlightTasks(ctx context.Context) ([]opstask.Brief, error) {
	if h.app == nil {
		return nil, errs.New(errs.Internal, "the core is not mounted")
	}
	e, ok := h.app.Registry().Lookup(opstask.OpList)
	if !ok {
		return nil, errs.New(errs.Internal, opstask.OpList+" is not registered")
	}
	in, err := json.Marshal(opstask.ListIn{Status: schema.TaskStatusInProgress, Assignee: taskAssigneeMe})
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "encode task.list", err)
	}
	res, err := h.app.Run(ctx, e, op.JSONDecoder{Body: in}, op.Caller{})
	if err != nil {
		return nil, err
	}
	b, err := res.JSON()
	if err != nil {
		return nil, err
	}
	var out opstask.ListOut
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, errs.Wrap(errs.Internal, "decode task.list", err)
	}
	return out.Data, nil
}

// sessionStartFile is this repo's stamp path, "" outside a work tree.
func sessionStartFile() string {
	top, err := runGit(context.Background(), false, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(top))))
	return filepath.Join(home, ".lore", filepath.FromSlash(sessionStartDir), hex.EncodeToString(sum[:])[:hookSeenHashLen])
}

// readSessionStart is the stamped time, zero when absent or unreadable.
func readSessionStart(file string) time.Time {
	if file == "" {
		return time.Time{}
	}
	b, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// writeSessionStart stamps t; best effort (a lost stamp only widens the
// next "changed since" list).
func writeSessionStart(file string, t time.Time) {
	if file == "" || os.MkdirAll(filepath.Dir(file), 0o700) != nil {
		return
	}
	_ = os.WriteFile(file, []byte(t.UTC().Format(time.RFC3339Nano)+"\n"), 0o600) // best effort, see above
}
