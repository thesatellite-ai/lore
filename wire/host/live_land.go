package host

// live_land.go — landing (LORE_TEAM_SPEC F5) for cmd/lore, ported from
// saas/cmd/cli live_land.go: finished rows and max-age copies are written
// to the default branch's .lore/data by a commit built on the fetched
// branch and pushed as <sha>:refs/heads/<branch>; rows the branch holds
// leave live. It runs from `lore sync` and `lore land` (live_sync.go).
//
// Not ported (P3-MODEB-3): the F16 leak check of the landing commit (the
// core has no write-time leak check on cmd/lore yet, P3-ORG-3), and
// `lore land`'s row filters and --dry-run plan.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/thesatellite-ai/lore/adapters/gitref"
	"github.com/thesatellite-ai/lore/adapters/live"
	"github.com/thesatellite-ai/lore/logic/canonjson"
	"github.com/thesatellite-ai/lore/schema"
)

// statusField is the column holding a row's lifecycle state.
const statusField = "status"

// Handoff end states (schema has no constants for them yet).
const (
	handoffAccepted = "accepted"
	handoffDeclined = "declined"
)

// liveFinishedStatuses: per kind, the statuses that count as finished
// (rule 6a). A kind not listed has no lifecycle and is finished on every
// write (TEAM-027): each live write lands a copy at once.
var liveFinishedStatuses = map[string]map[string]bool{
	schema.TableTasks:     {schema.TaskStatusDone: true, schema.TaskStatusCancelled: true},
	schema.TableMissions:  {schema.MissionStatusDone: true, schema.MissionStatusCancelled: true},
	schema.TableDecisions: {schema.DecisionStatusSuperseded: true, schema.DecisionStatusDeprecated: true},
	schema.TableHandoffs:  {handoffAccepted: true, handoffDeclined: true},
}

// liveFinished applies liveFinishedStatuses to a row body.
func liveFinished(table string, body canonjson.Object) bool {
	states, hasLifecycle := liveFinishedStatuses[table]
	if !hasLifecycle {
		return true
	}
	return states[body.Str(statusField)]
}

// _purged.json keys (rowfs purged.go: {"_v": 1, "ids": [sorted ids]}).
const (
	purgedKeyIDs     = "ids"
	purgedKeyVersion = "_v"
	purgedFormatV1   = 1
)

// purgedIDs decodes a _purged.json. A file that does not decode or whose
// "ids" is not a list is an error, never an empty set (purged rows would
// come back); non-string entries name no row and are skipped.
func purgedIDs(b []byte) (canonjson.Object, []string, error) {
	doc, err := canonjson.Decode(b, canonjson.DefaultMaxDepth)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", purgedFileName, err)
	}
	raw, has := doc[purgedKeyIDs]
	if !has {
		return doc, nil, nil
	}
	list, ok := raw.AsArray()
	if !ok {
		return nil, nil, fmt.Errorf("%s: %q is not a list of ids", purgedFileName, purgedKeyIDs)
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if id, ok := v.AsString(); ok {
			out = append(out, id)
		}
	}
	return doc, out, nil
}

// addPurged is live.Layout.AddPurged: existing (nil when absent) plus ids,
// sorted, other keys a newer writer added kept.
func addPurged(existing []byte, add []string) ([]byte, error) {
	doc := canonjson.Object{}
	set := map[string]bool{}
	if len(existing) > 0 {
		d, have, err := purgedIDs(existing)
		if err != nil {
			return nil, err
		}
		doc = d
		for _, id := range have {
			set[id] = true
		}
	}
	for _, id := range add {
		set[id] = true
	}
	list := make([]string, 0, len(set))
	for id := range set {
		list = append(list, id)
	}
	sort.Strings(list)
	arr := make([]canonjson.Value, len(list))
	for i, id := range list {
		arr[i] = canonjson.String(id)
	}
	doc[purgedKeyIDs] = canonjson.Array(arr...)
	doc[purgedKeyVersion] = canonjson.Number(strconv.Itoa(purgedFormatV1))
	return canonjson.Encode(doc)
}

// liveLayout is lore's branch layout of a live folder (§3.6).
var liveLayout = live.Layout{DataPath: mainDataPath, AddPurged: addPurged}

// mainPurged reads the ids in ref's _purged.json for folder (an absent ref
// or file is an empty set; anything unreadable is an error).
func (s *liveSession) mainPurged(ctx context.Context, ref, folder string) (map[string]bool, error) {
	p := mainDataPath(folder, purgedFileName)
	set := map[string]bool{}
	b, ok, err := s.repo.ReadFile(ctx, ref, p)
	if errors.Is(err, gitref.ErrNotFound) {
		return set, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s:%s: %w", ref, p, err)
	}
	if !ok {
		return set, nil
	}
	_, list, err := purgedIDs(b)
	if err != nil {
		return nil, fmt.Errorf("%s:%s: %w", ref, p, err)
	}
	for _, id := range list {
		set[id] = true
	}
	return set, nil
}

// landPlanRow is one live row's landing decision.
type landPlanRow struct {
	Key      live.RowKey
	File     live.File
	Decision live.LandDecision
	Leave    bool
}

// plan decides, for every live row of folder, whether it leaves live and
// whether it lands (F5 Confirm + ShouldLand).
func (s *liveSession) plan(ctx context.Context, folder string) ([]landPlanRow, error) {
	_, tree, err := s.ref.LocalTree(ctx)
	if err != nil {
		return nil, err
	}
	rows, _ := tree.Rows(folder)
	mainRef := "refs/remotes/" + s.ref.RemoteName + "/" + s.defaultBranch(ctx)
	purged, err := s.mainPurged(ctx, mainRef, folder)
	if err != nil {
		return nil, fmt.Errorf("main's purge list: %w; nothing was landed", err)
	}
	now := s.clk.Now()
	keys := make([]live.RowKey, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	out := make([]landPlanRow, 0, len(keys))
	for _, k := range keys {
		f := rows[k]
		finished := f.Deleted() || liveFinished(k.Table, f.Body)
		mainBody, ok, rerr := s.repo.ReadFile(ctx, mainRef, mainDataPath(folder, k.Table+"/"+k.ID+".json"))
		if rerr != nil || !ok {
			mainBody = nil
		}
		p := landPlanRow{Key: k, File: f}
		p.Leave = live.Confirm(live.ConfirmInputs{File: f, Finished: finished, Now: now, MaxAge: s.cfg.MaxAge, MainBody: mainBody, MainPurged: purged[k.ID]})
		p.Decision = live.ShouldLand(live.LandInputs{File: f, Finished: finished, Now: now, MaxAge: s.cfg.MaxAge, MainBody: mainBody, MainPurged: purged[k.ID]})
		out = append(out, p)
	}
	return out, nil
}

// landOutcome is what one landing pass did.
type landOutcome struct {
	Branch string
	Landed int
	Copied int
	Left   int
	Status live.LandStatus
	Commit string
	Detail string
}

// liveNoteProtected records that the branch refused a direct landing.
const liveNoteProtected = "land-protected"

// landPass runs one landing pass for folder: rows the branch already holds
// leave live, finished rows land, open rows at max age are copied.
func (s *liveSession) landPass(ctx context.Context, folder string) (landOutcome, error) {
	var out landOutcome
	if s.blocked != nil {
		return out, s.blocked
	}
	out.Branch = s.defaultBranch(ctx)
	plan, err := s.plan(ctx, folder)
	if err != nil {
		return out, err
	}
	removeIf := map[live.RowKey][]byte{}
	var items []live.LandItem
	for _, p := range plan {
		if p.Leave {
			if b, err := p.File.Encode(); err == nil {
				removeIf[p.Key] = b
			}
			continue
		}
		switch p.Decision.Action {
		case live.LandFinal, live.LandCopy:
			items = append(items, live.LandItem{Folder: folder, Key: p.Key, File: p.File, Action: p.Decision.Action})
		case live.LandStay:
		}
	}
	items = withoutLoneSupporting(items)
	exact := map[live.RowKey]live.File{}
	if len(items) > 0 {
		res, err := s.ref.Land(ctx, live.LandRequest{Branch: out.Branch, Items: items, Layout: liveLayout})
		if err != nil {
			return out, err
		}
		out.Status, out.Commit, out.Detail = res.Status, res.Commit, res.Detail
		switch res.Status {
		case live.LandDone, live.LandNothing:
			_ = s.writeNote(liveNoteProtected, "") // best effort: feeds `lore live doctor` only
			now := s.clk.Now()
			for _, it := range items {
				if it.Action == live.LandCopy {
					f := it.File
					f.Block.LastCopyAt = now.UTC()
					exact[it.Key] = f
					out.Copied++
				} else {
					out.Landed++
				}
			}
		case live.LandProtected:
			_ = s.writeNote(liveNoteProtected, res.Detail) // best effort: feeds `lore live doctor` only
		case live.LandOffline, live.LandNoRemote, live.LandRetriesOut, live.LandRefused:
			// nothing landed: the rows stay live and the next pass retries
		}
	}
	if len(removeIf)+len(exact) > 0 {
		res, err := s.ref.Write(ctx, live.WriteRequest{Folder: folder, Exact: exact, RemoveIf: removeIf, BinaryVersion: s.h.cfg.Version})
		if err != nil {
			return out, err
		}
		for _, k := range res.Written {
			if _, left := removeIf[k]; left {
				out.Left++
			}
		}
	}
	return out, nil
}

// withoutLoneSupporting drops companion rows when no other row lands with
// them: a plain `lore task add` must not move the branch (F2).
func withoutLoneSupporting(items []live.LandItem) []live.LandItem {
	for _, it := range items {
		if !it.File.Block.Companion {
			return items
		}
	}
	return nil
}

// shortSHALen is how much of a commit id output shows.
const shortSHALen = 7

// landLines are the pass's status lines (v0.1.17 printLandOutcome text).
func landLines(out landOutcome) []string {
	branch := out.Branch
	if branch == "" {
		branch = "the default branch"
	}
	var lines []string
	switch out.Status {
	case live.LandDone:
		sha := out.Commit
		if len(sha) > shortSHALen {
			sha = sha[:shortSHALen]
		}
		msg := "landed on " + branch + " (" + sha + ")"
		if out.Copied > 0 {
			msg += fmt.Sprintf("; %d open row(s) copied, they stay live", out.Copied)
		}
		lines = append(lines, liveLogPrefix+msg+"; leaves live once "+branch+" is fetched")
	case live.LandProtected:
		lines = append(lines, liveLogPrefix+branch+" is protected: the rows ride on your next push (lore: land N rows)")
	case live.LandOffline:
		lines = append(lines, liveLogPrefix+"not landed yet (offline); the next command retries")
	case live.LandRetriesOut:
		lines = append(lines, liveLogPrefix+"not landed yet ("+branch+" kept moving); the next command retries")
	case live.LandRefused:
		lines = append(lines, liveLogPrefix+branch+" refused the landing: "+out.Detail)
	case live.LandNothing, live.LandNoRemote:
	}
	if out.Left > 0 {
		lines = append(lines, fmt.Sprintf("%s%d row(s) left live (%s holds them)", liveLogPrefix, out.Left, branch))
	}
	return lines
}
