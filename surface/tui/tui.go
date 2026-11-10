// Package tui is the terminal browser of the core (SPEC 10.4, Q-TUI):
// read-only, driven by the table definitions at run time (no ent, no lsync
// bridge, no generated code), reading rows through the engine of every
// attached store. A line-oriented loop over an io.Reader/io.Writer, so it
// runs in any terminal and in tests without a TTY library.
//
// Flow:
//
//	Run ─▶ tables screen: every synced table with its live-row count
//	   │      <n> opens table n · q quits
//	   ├─▶ rows screen: one page of rows (id, store, label)
//	   │      <n> shows row n · n/p next/previous page · /text filters ·
//	   │      b back · q quits
//	   └─▶ row screen: every field (sensitive fields masked, FA-OLDCODE-3)
//	          b back · q quits
//
// Options.Kind opens on one table's rows screen first (`lore tui --kind
// decision`, P3-TUI-1); b from there goes to the tables screen.
package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// Source is what the browser reads: the table definitions and each attached
// store's live rows. The binary passes the engine (through a small adapter).
type Source interface {
	Tables() []schema.TableDef
	Stores() []store.Source
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
}

// Screen limits and words.
const (
	// PageSize is how many rows one rows screen shows.
	PageSize = 20
	// labelWidth bounds a row's label on the rows screen.
	labelWidth = 70
	// masked replaces a sensitive field's value (never printed here).
	masked  = "••••"
	cmdQuit = "q"
	cmdBack = "b"
	cmdNext = "n"
	cmdPrev = "p"
	cmdFind = "/"
	prompt  = "> "
)

// labelFields are tried in order for a row's one-line label.
var labelFields = []string{"title", "name", "body", "text", "key", "url"}

// errQuit unwinds every screen when the person types q (never returned by Run).
var errQuit = errors.New("tui: quit")

// Options tune one session.
type Options struct {
	// Kind is the table to open first, by table name ("decisions") or its
	// singular ("decision"); empty opens on the tables screen.
	Kind string
}

// hit is one row with the store it came from.
type hit struct {
	src store.Source
	r   row.Row
}

// browser is one session's state.
type browser struct {
	ctx context.Context
	src Source
	in  *bufio.Scanner
	out io.Writer
}

// Run browses src until the person quits or in ends. opts is optional (at
// most one is read; variadic so existing callers keep compiling). An unknown
// Kind is E_INVALID_INPUT naming the tables, before anything is shown.
func Run(ctx context.Context, src Source, in io.Reader, out io.Writer, opts ...Options) error {
	b := &browser{ctx: ctx, src: src, in: bufio.NewScanner(in), out: out}
	defs := b.defs()
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.Kind != "" {
		d, err := resolve(defs, o.Kind)
		if err != nil {
			return err
		}
		if err := b.table(d); err != nil {
			return quitOK(err)
		}
	}
	return quitOK(b.tables(defs))
}

// quitOK maps the quit unwind to a clean end.
func quitOK(err error) error {
	if errors.Is(err, errQuit) {
		return nil
	}
	return err
}

// defs is every synced table, sorted by name.
func (b *browser) defs() []schema.TableDef {
	defs := slices.DeleteFunc(slices.Clone(b.src.Tables()), func(d schema.TableDef) bool { return !d.Synced })
	slices.SortFunc(defs, func(x, y schema.TableDef) int { return strings.Compare(string(x.Name), string(y.Name)) })
	return defs
}

// resolve finds kind among defs: the table name, or its singular as
// v0.1.17's --kind spelled it (memory → memories, decision → decisions).
func resolve(defs []schema.TableDef, kind string) (schema.TableDef, error) {
	k := strings.ToLower(strings.TrimSpace(kind))
	want := []string{k, k + "s", k + "es"}
	if base, ok := strings.CutSuffix(k, "y"); ok {
		want = append(want, base+"ies")
	}
	for _, w := range want {
		for _, d := range defs {
			if string(d.Name) == w {
				return d, nil
			}
		}
	}
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = string(d.Name)
	}
	return schema.TableDef{}, errs.New(errs.InvalidInput, fmt.Sprintf("unknown kind %q for lore tui --kind; tables: %s", kind, strings.Join(names, ", "))).
		WithHint("give one of the tables above (a singular works too: --kind memory), or run lore tui without --kind to pick one")
}

// read prints the prompt and returns the next trimmed line; ok=false at EOF.
func (b *browser) read() (string, bool) {
	b.put(prompt)
	if !b.in.Scan() {
		return "", false
	}
	return strings.TrimSpace(b.in.Text()), true
}

// put writes s to out; a broken terminal ends the session at the next read.
func (b *browser) put(s string) {
	_, _ = io.WriteString(b.out, s)
}

// tables is the first screen.
func (b *browser) tables(defs []schema.TableDef) error {
	for {
		b.put("\nlore tables (live rows in every attached store)\n")
		for i, d := range defs {
			hits, err := b.rows(d.Name)
			if err != nil {
				return err
			}
			b.put(fmt.Sprintf("%3d  %-28s %6d  %s\n", i+1, d.Name, len(hits), d.Description))
		}
		b.put("<n> open · q quit\n")
		line, ok := b.read()
		if !ok || line == cmdQuit {
			return nil
		}
		if i, err := strconv.Atoi(line); err == nil && i >= 1 && i <= len(defs) {
			if err := b.table(defs[i-1]); err != nil {
				return err
			}
		}
	}
}

// rows lists the live rows of table in every store, in store order; a
// resource row's sensitive field values are masked here (resmask.go).
func (b *browser) rows(table row.Table) ([]hit, error) {
	var kf map[string]kindFieldInfo
	if table == schema.TableResources {
		var err error
		if kf, err = b.kindFields(); err != nil {
			return nil, err
		}
	}
	var out []hit
	for _, s := range b.src.Stores() {
		rs, err := b.src.List(b.ctx, s, table)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			if r.Deleted {
				continue
			}
			if kf != nil {
				r = maskResource(kf, r)
			}
			out = append(out, hit{src: s, r: r})
		}
	}
	return out, nil
}

// table is the rows screen of d. Returns nil on back, errQuit on q or EOF.
func (b *browser) table(d schema.TableDef) error {
	all, err := b.rows(d.Name)
	if err != nil {
		return err
	}
	shown, page, filter := all, 0, ""
	for {
		pages := max(1, (len(shown)+PageSize-1)/PageSize)
		page = min(page, pages-1)
		lo, hi := page*PageSize, min(len(shown), (page+1)*PageSize)
		b.put(fmt.Sprintf("\n%s — %d–%d of %d (page %d/%d)", d.Name, min(lo+1, len(shown)), hi, len(shown), page+1, pages))
		if filter != "" {
			b.put(fmt.Sprintf(" filter %q", filter))
		}
		b.put("\n")
		for i, h := range shown[lo:hi] {
			b.put(fmt.Sprintf("%3d  %s  %-8s %s\n", lo+i+1, h.r.ID, h.src, label(d, h.r)))
		}
		b.put("<n> show · n/p page · /text filter · b back · q quit\n")
		line, ok := b.read()
		switch {
		case !ok || line == cmdQuit:
			return errQuit
		case line == cmdBack:
			return nil
		case line == cmdNext:
			page++
		case line == cmdPrev:
			page = max(0, page-1)
		case strings.HasPrefix(line, cmdFind):
			filter = strings.TrimSpace(strings.TrimPrefix(line, cmdFind))
			shown, page = match(d, all, filter), 0
		default:
			if i, err := strconv.Atoi(line); err == nil && i >= 1 && i <= len(shown) {
				if !b.show(d, shown[i-1]) {
					return errQuit
				}
			}
		}
	}
}

// show prints every field of one row; false on q or EOF.
func (b *browser) show(d schema.TableDef, h hit) bool {
	b.put(fmt.Sprintf("\n%s  %s  store %s\n", h.r.ID, d.Name, h.src))
	names := make([]string, 0, len(h.r.Fields))
	for k := range h.r.Fields {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, k := range names {
		b.put(fmt.Sprintf("  %s: %s\n", k, fieldText(d, h.r, k)))
	}
	if !h.r.UpdatedAt.IsZero() {
		b.put(fmt.Sprintf("  updated: %s by %s\n", h.r.UpdatedAt.UTC().Format("2006-01-02 15:04"), h.r.UpdatedBy))
	}
	if d.Name == schema.TableActors { // TP-23: profile block (profile.go)
		b.profile(h)
	}
	b.put("b back · q quit\n")
	line, ok := b.read()
	return ok && line != cmdQuit
}

// fieldText is a field's text; a sensitive field is masked (FA-OLDCODE-3:
// a browser on screen is not a place for secrets, like lore sql's default).
func fieldText(d schema.TableDef, r row.Row, name string) string {
	if f, ok := d.Field(name); ok && f.Sensitive {
		return masked
	}
	v := r.Fields[name]
	if v == nil {
		return ""
	}
	return v.String()
}

// label is a row's one-line label: the first non-empty label field.
func label(d schema.TableDef, r row.Row) string {
	for _, k := range labelFields {
		if s := strings.TrimSpace(fieldText(d, r, k)); s != "" {
			s = strings.Join(strings.Fields(s), " ")
			if rs := []rune(s); len(rs) > labelWidth {
				s = string(rs[:labelWidth-1]) + "…"
			}
			return s
		}
	}
	return ""
}

// match keeps the rows whose id or any non-sensitive field contains text
// (case-insensitive).
func match(d schema.TableDef, all []hit, text string) []hit {
	if text == "" {
		return all
	}
	t := strings.ToLower(text)
	var out []hit
	for _, h := range all {
		if strings.Contains(strings.ToLower(h.r.ID.String()), t) {
			out = append(out, h)
			continue
		}
		for k := range h.r.Fields {
			if strings.Contains(strings.ToLower(fieldText(d, h.r, k)), t) {
				out = append(out, h)
				break
			}
		}
	}
	return out
}
