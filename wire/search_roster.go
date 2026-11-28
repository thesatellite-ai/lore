package wire

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/searchrank"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/profilestore"
	opssearch "github.com/thesatellite-ai/lore/ops/search"
	opsteam "github.com/thesatellite-ai/lore/ops/team"
)

// searchRoster finds the org's people (EntityMember: name, handle, email,
// GitHub login) or teams (EntityTeam: handle, name) holding the query's
// words (devco D-489: `lore search <handle>` found nobody and `--tables
// team` was refused). They live in the org roster, which no store cache
// indexes, so the roster's own lists (member list, team list) are read
// and each word is matched as a case-insensitive part of those fields;
// the hits are ranked like every table's (rankTable). A place with no
// roster has no people to find.
//
//	words ─▶ roster list ─▶ rows holding ≥1 word ─▶ rankTable (title = name)
func (a *App) searchRoster(ctx context.Context, t opssearch.Target, w searchrank.Words, perTable int) ([]opssearch.Hit, error) {
	// terms by w.Terms index ("" for a word that is only '*')
	terms := make([]string, len(w.Terms))
	some := false
	for k, term := range w.Terms {
		terms[k] = strings.ToLower(strings.TrimSuffix(term, "*"))
		some = some || terms[k] != ""
	}
	if !some {
		return nil, nil
	}
	var in []rankedHit
	// add lists one person or team holding a word in its fields; names
	// are the fields that name it (devco:D-1007): a word there counts in
	// full in the merged order, a word only the rest holds (a team's
	// display name, which reads as a description) by its rarity
	add := func(id, title, body string, names []string, fields ...string) {
		hay := strings.ToLower(strings.Join(append(slices.Clone(names), fields...), "\n"))
		named := strings.ToLower(strings.Join(names, "\n"))
		heldW, nameW := make([]bool, len(terms)), make([]bool, len(terms))
		n := 0
		for k, term := range terms {
			if term != "" && strings.Contains(hay, term) {
				n++
				heldW[k], nameW[k] = true, strings.Contains(named, term)
			}
		}
		if n == 0 {
			return
		}
		h := opssearch.Hit{EntityTable: t.Table.String(), Entity: t.Entity, ID: id, Pretty: id, Title: title, Body: title, Snippet: body}
		if w.Natural {
			h.Held, h.NameHeld = heldW, nameW
		}
		in = append(in, rankedHit{hit: h, matched: n})
	}
	switch t.Entity {
	case opssearch.EntityMember:
		var out opsteam.StaffListOut
		found, err := a.runRosterList(ctx, profilestore.OpStaffList, &out)
		if err != nil || !found {
			return nil, err
		}
		for _, m := range out.Members {
			// a person's name, handle, GitHub login and email name them;
			// the email's domain is the company's, shared by everyone, so
			// it is held but names nobody (devco:D-1034): a question with
			// the company's name in it held every person in full
			local, _, _ := strings.Cut(m.Email, "@")
			add(m.ID, m.Name, rosterHandle(m.Handle)+" "+m.Email, []string{m.Name, m.Handle, local, m.GitHub}, m.Email)
		}
	case opssearch.EntityTeam:
		var out opsteam.TeamListOut
		found, err := a.runRosterList(ctx, profilestore.OpTeamList, &out)
		if err != nil || !found {
			return nil, err
		}
		for _, tm := range out.Teams {
			if tm.Active {
				add(tm.ID, rosterHandle(tm.Handle), tm.Name, []string{tm.Handle}, tm.Name)
			}
		}
	}
	return rankTable(in, w, perTable), nil
}

// rosterHandle is "@handle", "" for none.
func rosterHandle(h string) string {
	if h == "" || strings.HasPrefix(h, rosterHandleMark) {
		return h
	}
	return rosterHandleMark + h
}

// runRosterList runs one roster list operation (no input) and decodes its
// JSON into out. found is false when this place has no roster it can
// list (no org, a store-only checkout without one, a client repo whose
// roster is the org's), which a search treats as nothing to find.
func (a *App) runRosterList(ctx context.Context, name string, out interface{ Text() string }) (bool, error) {
	e, ok := a.Registry().Lookup(name)
	if !ok {
		return false, nil
	}
	c, _ := engine.CallerOf(ctx)
	res, err := a.runOnOpen(ctx, e, op.JSONDecoder{Body: []byte(`{}`)}, c)
	if err != nil {
		// no roster here, or one this place may not read (a client repo:
		// E_LEAK_BLOCKED): a search of the content never fails on the
		// people list; `lore member list` says why it cannot be read
		return false, nil
	}
	raw, err := res.JSON()
	if err != nil {
		return false, errs.Wrap(errs.Internal, "search: read "+name, err)
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return false, errs.Wrap(errs.Internal, "search: decode "+name, err)
	}
	return true, nil
}

// runOnOpen runs the roster list e on the open the search already holds:
// the roster lists are direct ops (profilestore.IsDirect) that read the
// roster through App.Roster, which reuses this App's state when the
// roster is one of its stores. A fresh open per list (devco D-489's
// runAside) re-statused every attached store's clone twice per search
// (devco D-496, the D-418 git budget). Should the read still reopen the
// App (App.rosterState's reopenForStore), that open is closed and the
// search's own is put back, so its engine, a store folder's --all-repos
// widening and extra folders stay as they were (devco D-485).
func (a *App) runOnOpen(ctx context.Context, e op.Entry, d op.Decoder, c op.Caller) (op.Result, error) {
	a.mu.Lock()
	opened, st, serr, cfgOpened, cfgErr, cfg := a.opened, a.st, a.err, a.cfgOpened, a.cfgErr, a.cfg
	extra := slices.Clone(a.extra)
	a.mu.Unlock()
	res, err := runDirect(profileContext(ctx, c), e, d)
	a.mu.Lock()
	if a.st != nil && a.st != st {
		_ = a.st.close()
	}
	a.opened, a.st, a.err, a.cfgOpened, a.cfgErr, a.cfg = opened, st, serr, cfgOpened, cfgErr, cfg
	a.extra = extra
	a.mu.Unlock()
	return res, err
}
