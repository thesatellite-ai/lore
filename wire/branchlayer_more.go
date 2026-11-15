package wire

// branchlayer_more.go — the branch verbs beyond list/land/drop
// (BRANCH_LAYERS 9, 12, 13, 17): land's options (--kinds, --review,
// --take, --into), restore --revert, adopt, and the CI workflow.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/branchlayer"
	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/op"
	opsbranch "github.com/thesatellite-ai/lore/ops/branch"
	"github.com/thesatellite-ai/lore/store"
)

// takeBranch and takeMain are --take's values.
const (
	takeBranch = "branch"
	takeMain   = "main"
)

// landRequestOf turns land's flags into the adapter's request.
func landRequestOf(key branchlayer.Key, in opsbranch.LandIn) (git.LandRequest, error) {
	req := git.LandRequest{Key: key, DryRun: in.DryRun}
	for _, raw := range in.Kinds {
		for k := range strings.SplitSeq(raw, ",") {
			if k = strings.TrimSpace(k); k == "" {
				continue
			}
			t, err := row.ParseTable(k)
			if err != nil {
				return req, errs.Wrap(errs.InvalidInput, "--kinds "+k+" is not a table", err).WithHint("use table names: tasks, memories, rules, decisions, …")
			}
			req.Kinds = append(req.Kinds, t)
		}
	}
	switch {
	case in.Take == "" && len(in.IDs) > 0:
		return req, errs.New(errs.InvalidInput, "--ids goes with --take").WithHint("--take branch --ids <id>, or --take main --ids <id>")
	case in.Take == "":
	case in.Take != takeBranch && in.Take != takeMain:
		return req, errs.New(errs.InvalidInput, "--take "+in.Take+": use branch or main").WithHint("--take branch keeps the branch's text, --take main the shared notes'")
	default:
		named, err := parseIDs(in.IDs, key.Branch)
		if err != nil {
			return req, err
		}
		if len(named) == 0 {
			return req, errs.New(errs.InvalidInput, "--take needs --ids").WithHint("name the rows E_LAND_CONFLICT listed")
		}
		req.Take = map[ids.AnyID]bool{}
		for _, id := range named {
			req.Take[id] = in.Take == takeBranch
		}
	}
	return req, nil
}

// parseIDs reads comma separated or repeated row ids. branch names the
// code branch whose notes the ids come from ("" when not known yet): a bad
// id names why it is not one and where the branch's ids are listed
// (devco:V-6: it said only "--ids mem_doesnotexist").
func parseIDs(raw []string, branch string) ([]ids.AnyID, error) {
	var out []ids.AnyID
	for _, r := range raw {
		for p := range strings.SplitSeq(r, ",") {
			if p = strings.TrimSpace(p); p == "" {
				continue
			}
			id, err := ids.ParseAny(p)
			if err != nil {
				return nil, badRowID(p, branch, err)
			}
			out = append(out, id)
		}
	}
	return out, nil
}

// badRowID is the refusal of an --ids value that is no row id: the value,
// why (a row id is a kind prefix, an underscore and 32 hex digits, as
// mem_0193f2a7c4b77e1d8a3f5b6c7d8e9f00) and where the real ids are listed.
func badRowID(p, branch string, cause error) error {
	list := "lore branch show --branch <branch> lists the notes of a branch with their ids"
	if branch != "" {
		list = "lore branch show --branch " + branch + " lists the notes of " + branch + " with their ids"
	}
	why := cause.Error()
	var e *errs.Error
	if errors.As(cause, &e) {
		why = e.Message // the cause's own words, not its code a second time
	}
	return errs.Wrap(errs.InvalidID, "--ids "+p+" is not a row id ("+why+"): a row id is a kind prefix, an underscore and 32 hex digits, such as mem_0193f2a7c4b77e1d8a3f5b6c7d8e9f00", cause).
		WithHint(list)
}

// reviewRows asks keep or drop for every row (land --review) and returns
// the dropped ids. Without a terminal it refuses (CI never waits).
func reviewRows(rows []store.Row, req git.LandRequest) ([]ids.AnyID, error) {
	in, ok := reviewInput()
	if !ok {
		return nil, errs.New(errs.NeedsConfirm, "lore branch land --review asks keep or drop for every row, and there is no terminal to ask").
			WithHint("run it in a terminal, or land without --review (--kinds lands only some tables)")
	}
	sc := bufio.NewScanner(in)
	drop := append([]ids.AnyID{}, req.Exclude...)
	for _, r := range rows {
		fmt.Fprintf(os.Stderr, "keep %s %s %q? [Y/n] ", r.Table, r.ID, titleOf(r))
		ans := ""
		if sc.Scan() {
			ans = strings.ToLower(strings.TrimSpace(sc.Text()))
		}
		fmt.Fprintln(os.Stderr)
		if ans == "n" || ans == "no" {
			drop = append(drop, r.ID)
		}
	}
	return drop, nil
}

// landInto is land --into: the stacked layer of key lands into the layer
// of its parent branch.
func landInto(ctx context.Context, gb *git.Backend, key branchlayer.Key, in opsbranch.LandIn) (opsbranch.LandOut, error) {
	out := opsbranch.LandOut{Branch: key.Branch, Folder: key.Folder, StoreBranch: gb.Branch(), Into: in.Into, DryRun: in.DryRun}
	if in.DryRun {
		l, err := gb.LayerOpen(ctx, key)
		if err != nil {
			return out, err
		}
		rows, err := gb.LayerRows(ctx, l)
		out.Rows = rowCounts(rows)
		return out, err
	}
	res, err := gb.LayerLandInto(ctx, key, in.Into, true)
	if err != nil {
		return out, err
	}
	out.Rows, out.Commit, out.Trash, out.Pushed, out.Merged = rowCounts(res.Rows), res.Commit, res.Trash, res.Pushed, len(res.Merged)
	return out, nil
}

// revertLanding is restore --revert's second half: the landing of key,
// read from its newest trash commit, reverted on the store branch.
func revertLanding(ctx context.Context, gb *git.Backend, key branchlayer.Key, landing string) (string, error) {
	if landing == "" {
		return "", errs.New(errs.InvalidInput, "the notes of "+key.Branch+" were dropped, never landed: there is no landing to revert").
			WithHint("restore without --revert")
	}
	return gb.LayerRevertLanding(ctx, landing, key.Branch)
}

// BranchAdopt implements opsbranch.Env: rows of the shared notes whose
// provenance says they were born on the branch (_o.git.branch) move into
// its layer, BRANCH_LAYERS 12: each is moved to the trash of the shared
// notes (the delete reason names the branch), the layer is rebased onto
// that commit, and the row is restored there, so landing the branch
// brings it back to the shared notes.
func (a *App) BranchAdopt(ctx context.Context, in opsbranch.AdoptIn) (opsbranch.DoneOut, error) {
	if err := requireBranch("adopt", in.Branch); err != nil {
		return opsbranch.DoneOut{}, err
	}
	st, lb, name, err := a.branchTarget(ctx)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	key, err := keyFor(lb, in.Branch)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	shared, _, err := lb.inner.Read(withoutLayers(ctx), "")
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	var born []store.Row
	for _, r := range shared {
		if r.Deleted {
			continue
		}
		m, derr := r.Decode()
		if derr != nil || m.Origin.IsZero() {
			continue
		}
		if origin.Branch(m.Origin) == in.Branch {
			born = append(born, r)
		}
	}
	if len(born) == 0 {
		return opsbranch.DoneOut{OK: true, Lines: []string{"no shared rows of " + lb.key.Folder + " were written on " + in.Branch + "; nothing to adopt"}}, nil
	}
	if in.DryRun {
		return opsbranch.DoneOut{OK: true, Lines: []string{"would move " + opsbranch.RowsText(rowCounts(born)) + " born on " + in.Branch + " from the shared notes of " + lb.key.Folder + " into its notes (nothing changed)"}}, nil
	}
	if err := a.adoptRows(ctx, st, name, lb, key, born); err != nil {
		return opsbranch.DoneOut{}, err
	}
	return opsbranch.DoneOut{OK: true, Lines: []string{"✓ moved " + opsbranch.RowsText(rowCounts(born)) + " born on " + in.Branch + " into its notes; the shared notes keep them in the trash until the branch lands (lore sync shares both)"}}, nil
}

// BranchInstallAction implements opsbranch.Env: the GitHub workflow that
// lands a branch's notes when its pull request merges
// (.github/workflows/lore-branch-land.yml in the code repo; BRANCH_LAYERS
// 17). It asks first (--yes answers), never runs by itself from another
// command, and refuses in a client's repo: the file would name the
// internal store and folder there.
func (a *App) BranchInstallAction(ctx context.Context, in opsbranch.InstallActionIn) (opsbranch.DoneOut, error) {
	_, lb, name, err := a.branchTarget(ctx)
	if err != nil {
		return opsbranch.DoneOut{}, err
	}
	if client, _ := repoIsClient(ctx, a.opts); client {
		return opsbranch.DoneOut{}, errs.New(errs.LeakBlocked, "this is a client's repo: a workflow in it would name the internal store "+a.storeURLOf(ctx, string(name))+" and folder "+lb.key.Folder+" to the client").
			WithHint("land squash merges by hand (lore branch land --branch <b>), or run the CI step in a repo of your own")
	}
	url := a.storeURLOf(ctx, string(name))
	body := landWorkflow(url, lb.key.Folder)
	if in.DryRun {
		return opsbranch.DoneOut{OK: true, Lines: []string{strings.TrimRight(body, "\n")}}, nil
	}
	path := filepath.Join(lb.code.Top, ".github", "workflows", landWorkflowFile)
	if cur, rerr := os.ReadFile(path); rerr == nil && !strings.Contains(string(cur), landWorkflowMark) && !in.Force {
		return opsbranch.DoneOut{}, errs.New(errs.Refused, path+" exists and lore did not write it").WithHint("move it away, or pass --force to overwrite it")
	}
	if !engine.Confirmed(ctx) && !op.Confirmed(ctx) {
		return opsbranch.DoneOut{}, errs.New(errs.NeedsConfirm, "lore asks before it adds CI to a repo").
			WithQuestion("write " + path + " (a GitHub workflow that runs lore branch land when a pull request merges; it needs the secret LORE_STORE_TOKEN)?").
			WithHint("rerun with --yes to write it, or --dry-run to read it first")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return opsbranch.DoneOut{}, errs.FileIO("create", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return opsbranch.DoneOut{}, errs.FileIO("write", path, err)
	}
	return opsbranch.DoneOut{OK: true, Lines: []string{"✓ wrote " + path + "; add the repository secret LORE_STORE_TOKEN (a token with read and write access to the store repo only), then commit the file"}}, nil
}

// The CI workflow file and the mark that says lore wrote it.
const (
	landWorkflowFile = "lore-branch-land.yml"
	landWorkflowMark = "# written by lore branch install-action"
)

// landWorkflow is the workflow text for the store url and folder.
func landWorkflow(url, folder string) string {
	return landWorkflowMark + `; lore branch install-action --force rewrites it
name: lore branch land
on:
  pull_request:
    types: [closed]
jobs:
  lore-land:
    if: github.event.pull_request.merged == true
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: curl -fsSL https://raw.githubusercontent.com/thesatellite-ai/lore/main/install.sh | sh
      - run: git config --global url."https://x-access-token:${STORE_TOKEN}@github.com/".insteadOf "https://github.com/"
        env:
          STORE_TOKEN: ${{ secrets.LORE_STORE_TOKEN }}
      - run: >
          lore branch land --store ` + url + ` --folder ` + folder + `
          --repo "github.com/${{ github.repository }}" --branch "${{ github.head_ref }}"
          --pr "${{ github.event.pull_request.number }}"
`
}

// adoptRows is BranchAdopt's move: the rows go to the trash of the shared
// notes in one batch, the layer is rebased onto that commit, and a copy
// of each row as it was (restamped newer than the delete) is written to
// the layer, so landing the branch restores it.
func (a *App) adoptRows(ctx context.Context, st *state, name store.Source, lb *layerBackend, key branchlayer.Key, born []store.Row) error {
	main := engine.WithConfirmed(engine.WithDeleteReason(withoutLayers(ctx), "adopted into the notes of branch "+key.Branch))
	refs := make([]engine.Ref, 0, len(born))
	for _, r := range born {
		refs = append(refs, engine.Ref{Store: name, Table: r.Table, ID: r.ID})
	}
	if _, err := st.eng.TrashMany(main, refs); err != nil {
		return err
	}
	if err := lb.gb.LayerRebaseOnto(ctx, key); err != nil {
		return err
	}
	clk := hlc.NewClock(st.actor, a.opts.Wall)
	changes := make([]store.Change, 0, len(born))
	for _, r := range born {
		cur, err := lb.inner.Get(withoutLayers(ctx), r.Table, r.ID)
		if err != nil {
			return err
		}
		m, err := r.Decode()
		if err != nil {
			return err
		}
		m.HLC = clk.Now(cur.HLC)
		m.UpdatedAt, m.UpdatedBy = m.HLC.Wall, st.actor
		doc, err := store.FromRow(m)
		if err != nil {
			return err
		}
		changes = append(changes, store.Change{Row: doc, Expect: cur.HLC})
	}
	facts := branchlayer.Facts{Repo: lb.code.Remote, BaseBranch: lb.code.Default}
	if key.Branch == lb.code.Branch {
		facts.Split, facts.CodeHead = lb.code.Split, lb.code.Head
	}
	_, err := lb.gb.LayerWrite(ctx, git.LayerWriteRequest{Key: key, Changes: changes, Op: opsbranch.OpAdopt, Facts: facts})
	lb.mu.Lock()
	lb.rows = nil
	lb.mu.Unlock()
	return err
}

// ciOffer is the line that offers the CI workflow (BRANCH_LAYERS 17):
// for an own repo with branch notes and no workflow yet; "" otherwise (a
// client's repo never gets one: the file would name the internal store).
// Only offered: writing it is `lore branch install-action`, which asks.
func (a *App) ciOffer(ctx context.Context, lb *layerBackend) string {
	if _, err := os.Stat(filepath.Join(lb.code.Top, ".github", "workflows", landWorkflowFile)); err == nil {
		return ""
	}
	if client, _ := repoIsClient(ctx, a.opts); client {
		return ""
	}
	return "CI: lore branch install-action adds the GitHub workflow that lands a branch's notes when its pull request merges, squash merges included (it asks first)"
}
