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
	Title      string `yaml:"title"`
	Body       string `yaml:"body"`
	Priority   string `yaml:"priority"`
	Commitment string `yaml:"commitment"`
}

// split adds several sub-tasks under one task in one bat_ batch:
//
//	titles (positional…, --title…, --from file|-; shared --priority/--commitment) → checks of the first child (project,
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
	out := SplitOut{ParentID: id.String(), Inherited: par.inh, DryRun: in.DryRun}
	rank := par.rank
	for _, p := range pieces {
		out.Added = append(out.Added, SplitAdded{Title: p.Title, Rank: rank})
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

// splitList numbers the added sub-tasks.
func splitList(xs []SplitAdded) string {
	var b strings.Builder
	for i, x := range xs {
		b.WriteString("  " + strconv.Itoa(i+1) + "  ")
		if x.ID != "" {
			b.WriteString(x.ID + " ")
		}
		b.WriteString(x.Title + "\n")
	}
	return b.String()
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

// splitPieces collects --title values and --from contents, in order.
func splitPieces(in SplitIn) ([]splitPiece, error) {
	var out []splitPiece
	for _, t := range append(slices.Clone(in.TitleArgs), in.Titles...) {
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
	// FB-TASK-2: the shared flags fill what a piece does not set itself.
	for i := range out {
		if out[i].Priority == "" {
			out[i].Priority = in.Priority
		}
		if out[i].Commitment == "" {
			out[i].Commitment = in.Commitment
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
			return nil, errs.Wrap(errs.InvalidInput, "task split: open "+from, err)
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
