package wire

// who.go — the machine-local session block of `lore task show --who`
// (AGENT_IDENTITY 6.2 / 9, Q-LOCAL): which tool, tool session and model
// wrote a row, and whether a write was confirmed with --yes.
//
//	opstask.who ─ upgrade.As[Whoer](env) ─ (*App).WhoLines
//	  ├─ Options.Audit implements WhoSource (the old binary's sink:
//	  │    the machine store audit chain ⋈ sessions, P3-LOCAL-7) → its lines
//	  └─ else the fallback .lore/state/audit-v2.jsonl: one line per
//	       ses_ id with the confirmed flag (no tool/model: that file
//	       never holds them)
//
// Both sources are machine-local, so the block appears only on the machine
// that made the write; elsewhere --who shows only what the rows carry.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/thesatellite-ai/lore/model/errs"
)

// WhoSource is an optional capability of Options.Audit: the host that owns
// the session store answers the --who session lines itself. Lines are
// already redacted by the host's rules (6.10: a raw tool session id only
// on a terminal outside CI).
type WhoSource interface {
	WhoLines(ctx context.Context, rowID string) ([]string, error)
}

// whoMaxLine bounds one audit-v2.jsonl line read by the fallback (the
// lines are ids and counts only, far below this).
const whoMaxLine = 1 << 20

// WhoLines implements opstask.Whoer.
func (a *App) WhoLines(ctx context.Context, rowID string) ([]string, error) {
	if src, ok := a.opts.Audit.(WhoSource); ok {
		return src.WhoLines(ctx, rowID)
	}
	if a.opts.Audit != nil {
		return nil, nil // a host sink without session lines: nothing local to show
	}
	top, err := a.repoTop(ctx)
	if err != nil {
		if errs.Has(err, errs.NotAStore) {
			return nil, nil
		}
		return nil, err
	}
	return whoFromAuditFile(filepath.Join(top, filepath.FromSlash(AuditFile)), rowID)
}

// whoFromAuditFile folds the fallback audit file's events for rowID into
// one line per session, in first-seen order; a session is "confirmed yes"
// when any of its writes was.
func whoFromAuditFile(path, rowID string) ([]string, error) {
	f, err := os.Open(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "who: open the audit file", err)
	}
	defer func() { _ = f.Close() }() // read-only
	var order []string
	confirmed := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), whoMaxLine)
	for sc.Scan() {
		var l auditLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.ID != rowID || l.Session == "" {
			continue // a torn or foreign line never fails --who
		}
		if !slices.Contains(order, l.Session) {
			order = append(order, l.Session)
		}
		confirmed[l.Session] = confirmed[l.Session] || l.Confirmed
	}
	if err := sc.Err(); err != nil {
		return nil, errs.Wrap(errs.Internal, "who: read the audit file", err)
	}
	out := make([]string, 0, len(order))
	for _, s := range order {
		out = append(out, "session "+s+"  tool -  model -  confirmed "+yesNo(confirmed[s]))
	}
	return out, nil
}

// yesNo spells a flag the way the --who block prints it.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
