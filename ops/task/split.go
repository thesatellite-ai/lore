package task

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/thesatellite-ai/lore/logic/tasktree"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/work"
	"github.com/thesatellite-ai/lore/store"
)

// OpSplit is `lore task split` (TASK 6.3).
const OpSplit = "task.split"

// splitStdin is the --from value that reads stdin.
const splitStdin = "-"

// splitPiece is one sub-task of a split file (the task shape of the
// mission file, 15.2, reduced to the fields a split sets).
type splitPiece struct {
	Title      string   `yaml:"title"`
	Body       string   `yaml:"body"`
	Priority   string   `yaml:"priority"`
	Commitment string   `yaml:"commitment"`
	Assign     []string `yaml:"assign"`
	Due        string   `yaml:"due"`
}

// split adds several sub-tasks under one task in one bat_ batch:
//
//	titles (positional… each with its own flags, --title…, --from file|-;
//	shared --priority/--commitment/--assign/--due fill the rest) → checks of the first child (project,
//	depth, closed parent: add --parent's rules) → --dry-run prints and
//	stops → bulk.Begin(task) → add --parent per child, rank after the last
//	sibling, --key <key>/<n> per child (a re-run adds nothing).
func (o ops) split(ctx context.Context, in SplitIn) (SplitOut, error) {
	id, err := taskID(in.ID)
	if err != nil {
		return SplitOut{}, err
	}
	pieces, err := splitPieces(in)
	if err != nil {
		return SplitOut{}, err
	}
	_, pr, err := work.Get(ctx, o.env, Noun, table, id)
	if err != nil {
		return SplitOut{}, err
	}
	project := work.Field(pr, work.FieldProject)
	probe := AddIn{Parent: id.String(), Priority: in.Priority, Commitment: in.Commitment} // shared flags are not inherited
	par, err := o.addParent(ctx, &probe, project)
	if err != nil {
		return SplitOut{}, err
	}
	// 6.4: every piece must fit tasks.max_children before the first add,
	// so a split never stops half way on E_TREE_WIDTH.
	f, err := o.forest(ctx, project)
	if err != nil {
		return SplitOut{}, err
	}
	if err = o.checkWidth(ctx, f, store.Source(probe.To), id.String(), len(pieces)); err != nil {
		return SplitOut{}, err
	}
	dropUnused(&par.inh, pieces)
	out := SplitOut{ParentID: id.String(), Inherited: par.inh, DryRun: in.DryRun}
	rank := par.rank
	for _, p := range pieces {
		out.Added = append(out.Added, SplitAdded{Title: p.Title, Rank: rank, Priority: p.Priority, Commitment: p.Commitment, Assign: p.Assign, Due: p.Due})
		rank = tasktree.After(rank)
	}
	if in.DryRun {
		out.Human = "would add " + strconv.Itoa(len(pieces)) + " sub-tasks under " + id.String() + " " + par.title + ":\n" +
			splitList(out.Added) + inheritedLine(par) + "nothing written (--dry-run)\n"
		return out, nil
	}
	bctx, batch, err := bulk.Begin(ctx, bulk.KindTask)
	if err != nil {
		return SplitOut{}, err
	}
	out.Batch = batch.String()
	for i, p := range pieces {
		key := ""
		if in.Key != "" {
			key = in.Key + "/" + strconv.Itoa(i+1)
		}
		a, aerr := o.add(bctx, AddIn{
			Title: p.Title, Body: p.Body, Priority: p.Priority, Commitment: p.Commitment,
			Assign: p.Assign, Due: p.Due,
			Parent: id.String(), Key: key, Project: project,
		})
		if aerr != nil {
			return SplitOut{}, aerr
		}
		out.Added[i].ID = a.Data.ID
		out.Added[i].Rank = a.Data.Rank
	}
	out.Human = work.Success + " " + strconv.Itoa(len(pieces)) + " sub-tasks under " + id.String() + " " + par.title + " (batch " + out.Batch + "):\n" +
		splitList(out.Added) + inheritedLine(par)
	return out, nil
}

// dropUnused clears the inherited priority / commitment that no piece
// takes from the parent: the probe only knows the shared flags, so with
// every child setting its own value the "inherited:" line (and the --json
// inherited object) would name a value nothing inherits (TASK 6.5: the
// output lists what was inherited; P3-TASK-1 per-child flags).
func dropUnused(inh *Inherited, pieces []splitPiece) {
	needPrio, needCommit := false, false
	for _, p := range pieces {
		needPrio = needPrio || p.Priority == ""
		needCommit = needCommit || p.Commitment == ""
	}
	if !needPrio {
		inh.Priority = ""
	}
	if !needCommit {
		inh.Commitment = ""
	}
}

// splitList numbers the added sub-tasks.
func splitList(xs []SplitAdded) string {
	var b strings.Builder
	for i, x := range xs {
		b.WriteString("  " + strconv.Itoa(i+1) + "  ")
		if x.ID != "" {
			b.WriteString(x.ID + " ")
		}
		b.WriteString(x.Title + pieceFlags(x) + "\n")
	}
	return b.String()
}

// pieceFlags is the "  (priority high, assign @x, due fri)" tail of a
// split line: what the split set for that sub-task, so a dry run shows
// where each per-child flag landed (P3-TASK-1).
func pieceFlags(x SplitAdded) string {
	var parts []string
	for _, kv := range [][2]string{{"priority", x.Priority}, {"commitment", x.Commitment}, {"assign", strings.Join(x.Assign, " ")}, {"due", x.Due}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+" "+kv[1])
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "  (" + strings.Join(parts, ", ") + ")"
}

// inheritedLine is the "inherited:" line of an add --parent.
func inheritedLine(p *addParent) string {
	h := p.human()
	_, after, ok := strings.Cut(h, "\n")
	if !ok {
		return ""
	}
	return after
}

// maxSplitBytes bounds a --from file.
const maxSplitBytes = 1 << 20

// splitPieces collects positional titles (with their own flags), --title
// values and --from contents, in order.
func splitPieces(in SplitIn) ([]splitPiece, error) {
	var out []splitPiece
	for i, c := range in.Children {
		t := strings.TrimSpace(c.Title)
		if t == "" {
			// P3-TASK-1: an item may carry flags, so an empty title is
			// refused rather than skipped (its flags would vanish).
			return nil, errs.New(errs.InvalidInput, "task split: sub-task "+strconv.Itoa(i+1)+" has no title")
		}
		out = append(out, splitPiece{Title: t, Priority: c.Priority, Commitment: c.Commitment, Assign: c.Assign, Due: c.Due})
	}
	for _, t := range in.Titles {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, splitPiece{Title: t})
		}
	}
	if in.From != "" {
		raw, err := readSplitFrom(in.From)
		if err != nil {
			return nil, err
		}
		more, err := parseSplit(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, more...)
	}
	if len(out) == 0 {
		return nil, errs.New(errs.InvalidInput, "task split: no sub-task titles").
			WithHint(`lore task split <id> "First" "Second" (or --title …), or --from <file|->`)
	}
	// FB-TASK-2, P3-TASK-1: the shared flags fill what a piece does not
	// set itself (a piece's own --assign replaces the shared list).
	for i := range out {
		if out[i].Priority == "" {
			out[i].Priority = in.Priority
		}
		if out[i].Commitment == "" {
			out[i].Commitment = in.Commitment
		}
		if len(out[i].Assign) == 0 {
			out[i].Assign = slices.Clone(in.Assign)
		}
		if out[i].Due == "" {
			out[i].Due = in.Due
		}
	}
	return out, nil
}

// readSplitFrom reads a --from file, or stdin for "-".
func readSplitFrom(from string) ([]byte, error) {
	var r io.Reader = os.Stdin
	if from != splitStdin {
		fh, err := os.Open(filepath.Clean(from))
		if err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "task split: open "+from, err).WithHint("check the path, or pass - to read the titles from stdin")
		}
		defer func() { _ = fh.Close() }() // read only
		r = fh
	}
	b, err := io.ReadAll(io.LimitReader(r, maxSplitBytes+1))
	if err != nil {
		return nil, errs.Wrap(errs.InvalidInput, "task split: read "+from, err)
	}
	if len(b) > maxSplitBytes {
		return nil, errs.New(errs.InvalidInput, "task split: "+from+" is over "+strconv.Itoa(maxSplitBytes)+" bytes")
	}
	return b, nil
}

// parseSplit reads YAML (a list of titles or of {title, …}) when the text
// is a YAML list, else one title per non-empty line.
func parseSplit(raw []byte) ([]splitPiece, error) {
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("-")) {
		var nodes []yaml.Node
		if err := yaml.Unmarshal(raw, &nodes); err != nil {
			return nil, errs.Wrap(errs.InvalidInput, "task split: the YAML list does not parse", err)
		}
		out := make([]splitPiece, 0, len(nodes))
		for i, n := range nodes {
			var p splitPiece
			switch n.Kind {
			case yaml.ScalarNode:
				p.Title = n.Value
			case yaml.MappingNode:
				if err := n.Decode(&p); err != nil {
					return nil, errs.Wrap(errs.InvalidInput, "task split: item "+strconv.Itoa(i+1), err)
				}
			default:
				return nil, errs.New(errs.InvalidInput, "task split: item "+strconv.Itoa(i+1)+" is neither a title nor {title: …}")
			}
			if p.Title = strings.TrimSpace(p.Title); p.Title == "" {
				return nil, errs.New(errs.InvalidInput, "task split: item "+strconv.Itoa(i+1)+" has no title")
			}
			out = append(out, p)
		}
		return out, nil
	}
	var out []splitPiece
	for _, line := range strings.Split(string(raw), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, splitPiece{Title: t})
		}
	}
	return out, nil
}
