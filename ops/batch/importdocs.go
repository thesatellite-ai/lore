package batch

// importdocs.go — `lore docs import --from <TECHNICAL.md|README.md>`
// (devco D-940, H-189; per section since D-962, H-221): draft as-built
// notes from a repo's own docs.
//
//	read every --from        a text file under maxDocBytes, no NUL
//	split (logic/docnotes)   one piece per section, titled with its heading
//	                         path, lists kept with their lead-in; a section
//	                         with labelled Decision. / Considered. / Why.
//	                         paragraphs, or a paragraph stating a choice
//	                         and its reason, is a decision; boilerplate
//	                         (contents, license, contributing, link-only
//	                         lines) is left out
//	drop repeats             a section a live or archived memory/decision
//	                         already says is skipped whole; inside a fact
//	                         section each paragraph or list said earlier in
//	                         the import or by a stored note (logic/neardup,
//	                         the check lint near_duplicate runs) is dropped
//	                         and listed, the rest of the section is kept;
//	                         every add also carries an idempotency key over
//	                         its text, so a re-import adds nothing twice
//	write                    memory add (fact) / decision add --status
//	                         proposed (decision), each run through its own
//	                         command's checks (secret scan, policy, roles),
//	                         staged like `lore batch apply`: one commit per
//	                         store, one bat_ id (`lore batch undo`)
//	--dry-run                the same runs, then the stage is dropped
//
// Every draft says it is unverified until someone checks it:
//
//	source_kind   imported
//	source_ref    <doc path>:<line>
//	meta          fact_evidence=<doc path>:<line>[, <code path>:<line>…]
//	              fact_status=unverified (fact_verified_at unset)
//
// Checking one: lore meta set --id <id> --pair fact_status=verified
// --set fact_verified_at=<YYYY-MM-DD>; finding the rest: lore memory list
// --meta fact_status=unverified (and decision list).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thesatellite-ai/lore/adapters/scanner/gitleaks"
	"github.com/thesatellite-ai/lore/engine"
	"github.com/thesatellite-ai/lore/logic/docnotes"
	"github.com/thesatellite-ai/lore/logic/neardup"
	"github.com/thesatellite-ai/lore/logic/plural"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/op"
	"github.com/thesatellite-ai/lore/ops/internal/bulk"
	"github.com/thesatellite-ai/lore/ops/internal/upgrade"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// OpImportDocs is `lore docs import`.
const OpImportDocs = "docs.import"

// Words and limits of docs.import.
const (
	// maxDocBytes bounds one --from file (a long TECHNICAL.md is ~200 KB).
	maxDocBytes = 4 << 20
	// SourceImported is the source_kind every draft carries.
	SourceImported = "imported"
	// MetaEvidence is the metadata key holding where a draft came from
	// (<doc>:<line>, then any <code path>:<line> the text names).
	MetaEvidence = "fact_evidence"
	// MetaStatus is the metadata key marking a draft unverified.
	MetaStatus = "fact_status"
	// MetaVerifiedAt is the key a check sets (the date it was checked).
	MetaVerifiedAt = "fact_verified_at"
	// StatusUnverified is MetaStatus of every draft.
	StatusUnverified = "unverified"
	// StatusVerified is MetaStatus after a check.
	StatusVerified = "verified"
	// decisionProposed: an imported decision is proposed until checked.
	decisionProposed = "proposed"
	opMemoryAdd      = "memory.add"
	opDecisionAdd    = "decision.add"
	// keyPrefix starts the idempotency key of a draft.
	keyPrefix = "doc-"
	keyHexLen = 24
	// evidenceSep joins the evidence references.
	evidenceSep = ", "
	gitDir      = ".git"
)

// ImportDocsIn is the input of docs.import.
type ImportDocsIn struct {
	From   []string `json:"from" flag:"from" desc:"a markdown doc to draft notes from, such as TECHNICAL.md or README.md (repeatable)"`
	To     string   `json:"to,omitempty" flag:"to" alias:"store" desc:"shared:write-to"`
	DryRun bool     `json:"dry_run,omitempty" flag:"dry-run" desc:"run every note's checks, list the notes it would write and the repeats it skips; write nothing"`
}

// DocNote is one note docs.import drafted (or would draft).
type DocNote struct {
	// Kind is memory (a fact) or decision.
	Kind string `json:"kind"`
	// ID is the written row's id ("" for --dry-run).
	ID string `json:"id,omitempty"`
	// Evidence is the fact_evidence value: <doc>:<line>[, <code>:<line>…].
	Evidence string `json:"evidence"`
	// Title is a decision's title, or the start of a fact.
	Title string `json:"title"`
}

// Reasons a piece or block is skipped (DocSkip.Reason; the per-doc
// counts of DocCount name the same reasons).
const (
	// SkipStored: a stored memory or decision already says it.
	SkipStored = "already_stored"
	// SkipRepeat: an earlier piece of the same import says it.
	SkipRepeat = "repeat_in_import"
	// SkipTooLittle: what is left of a section once its repeats are
	// dropped is under the word floor.
	SkipTooLittle = "too_little_left"
)

// DocSkip is a piece docs.import did not write, and why.
type DocSkip struct {
	Evidence string `json:"evidence"`
	Title    string `json:"title"`
	// Existing is the row that already says it ("" when an earlier piece
	// of the same import does).
	Existing string `json:"existing,omitempty"`
	// Reason is SkipStored, SkipRepeat or SkipTooLittle.
	Reason string `json:"reason"`
	Why    string `json:"why"`
	// doc is the repo-relative doc it came from (for DocCount).
	doc string
}

// DocCount is what one --from doc gave (H-221: the owner reads these per
// doc to see how much of a doc became notes and why the rest did not).
type DocCount struct {
	File      string `json:"file"`
	Memories  int    `json:"memories"`
	Decisions int    `json:"decisions"`
	// AlreadyStored, RepeatInImport and TooLittleLeft count the skipped
	// list by its Reason.
	AlreadyStored  int `json:"already_stored"`
	RepeatInImport int `json:"repeat_in_import"`
	TooLittleLeft  int `json:"too_little_left"`
	// Boilerplate, LinkOnly and TooShort are what the split left out
	// (logic/docnotes.Dropped): contents/license/contributing sections,
	// link-only blocks, pieces under the word floor.
	Boilerplate int `json:"boilerplate"`
	LinkOnly    int `json:"link_only"`
	TooShort    int `json:"too_short"`
}

// line is the doc's count line: what it drafts, then each skip reason
// that happened.
func (c DocCount) line(dry bool) string {
	verb := "drafted "
	if dry {
		verb = "would draft "
	}
	s := c.File + ": " + verb + plural.Of(c.Memories, "memory", "memories") + ", " + plural.Count(c.Decisions, "decision")
	var skipped []string
	for _, r := range []struct {
		n         int
		one, many string
	}{
		{c.AlreadyStored, "already stored", "already stored"},
		{c.RepeatInImport, "repeat in this import", "repeats in this import"},
		{c.TooLittleLeft, "section with too little left", "sections with too little left"},
		{c.Boilerplate, "boilerplate section", "boilerplate sections"},
		{c.LinkOnly, "link-only block", "link-only blocks"},
		{c.TooShort, "too short", "too short"},
	} {
		if r.n > 0 {
			skipped = append(skipped, plural.Of(r.n, r.one, r.many))
		}
	}
	if len(skipped) == 0 {
		return s + "; skipped nothing\n"
	}
	return s + "; skipped " + strings.Join(skipped, ", ") + "\n"
}

// ImportDocsOut is the output of docs.import.
type ImportDocsOut struct {
	Batch string   `json:"batch"`
	Files []string `json:"files"`
	// Docs is what each --from doc gave, in --from order.
	Docs    []DocCount `json:"docs"`
	Notes   []DocNote  `json:"notes"`
	Skipped []DocSkip  `json:"skipped"`
	Stores  []string   `json:"stores"`
	Writes  int        `json:"writes"`
	DryRun  bool       `json:"dry_run"`
}

// Text implements op.Texter.
func (o ImportDocsOut) Text() string {
	var b strings.Builder
	for _, n := range o.Notes {
		id := n.ID
		if id == "" {
			id = "(new)"
		}
		b.WriteString("  " + n.Kind + " " + id + " — " + n.Title + "  [" + n.Evidence + "]\n")
	}
	for _, s := range o.Skipped {
		b.WriteString("  skipped " + s.Evidence + " — " + s.Title + ": " + s.Why + "\n")
	}
	for _, c := range o.Docs {
		b.WriteString(c.line(o.DryRun))
	}
	files := strings.Join(o.Files, ", ")
	if o.DryRun {
		b.WriteString("would draft " + plural.Count(len(o.Notes), "unverified note") + " from " + files + ", skip " +
			strconv.Itoa(len(o.Skipped)) + "; nothing written (--dry-run)\n")
		return b.String()
	}
	b.WriteString("drafted " + plural.Count(len(o.Notes), "unverified note") + " from " + files + " in " +
		plural.Count(o.Writes, "store write") + ", skipped " + strconv.Itoa(len(o.Skipped)))
	if o.Batch != "" && len(o.Notes) > 0 {
		b.WriteString("; batch " + o.Batch + " (undo: lore batch undo --id " + o.Batch + ")\n")
		b.WriteString("check each against the code, then: lore meta set --id <id> --pair " + MetaStatus + "=" + StatusVerified +
			" --set " + MetaVerifiedAt + "=<YYYY-MM-DD>\nthe rest: lore memory list --meta " + MetaStatus + "=" + StatusUnverified +
			" and lore decision list --meta " + MetaStatus + "=" + StatusUnverified + "\n")
	} else {
		b.WriteString("\n")
	}
	return b.String()
}

// RowReader is the Env upgrade docs.import reads existing notes through
// (*wire.App has it), to skip what lore already holds.
type RowReader interface {
	List(ctx context.Context, source store.Source, table row.Table) ([]row.Row, error)
	Stores() []store.Source
}

func importDocsEntry(o ops) op.Entry {
	return op.Define(op.Spec{Name: OpImportDocs,
		Summary:    "Draft unverified as-built notes (memories for facts, proposed decisions for stated choices) from markdown docs, each with its doc line as evidence",
		Permission: op.PermWrite, Access: op.AccessWrite, Confirm: op.ConfirmRule{MayAsk: true},
		Errors: []errs.Code{errs.InvalidInput, errs.NotFound, errs.SecretDetected, errs.NeedsConfirm, errs.Cancelled, errs.PolicyDenied,
			errs.ConfigInvalid, errs.Stale, errs.StoreReadOnly, errs.StoreLocked, errs.StoreUnreachable, errs.Unsupported, errs.Internal},
		Examples: []op.Example{
			{Title: "see what a doc would add", CLI: "lore docs import --from TECHNICAL.md --dry-run", Want: "nothing written"},
			{Title: "draft notes from two docs", CLI: "lore docs import --from TECHNICAL.md --from README.md"},
		}}, o.importDocs)
}

// draft is one piece ready to write.
type draft struct {
	piece    docnotes.Piece
	evidence string
	docRef   string
	// doc is the repo-relative doc path; root and dir are the repo root
	// and the doc's folder its code references resolve against (a piece
	// rebuilt after the repeat check recomputes its evidence).
	doc, root, dir string
}

func (o ops) importDocs(ctx context.Context, in ImportDocsIn) (ImportDocsOut, error) {
	out := ImportDocsOut{Files: []string{}, Docs: []DocCount{}, Notes: []DocNote{}, Skipped: []DocSkip{}, Stores: []string{}, DryRun: in.DryRun}
	r, ok := upgrade.As[LineRunner](o.env)
	if !ok {
		return out, errs.New(errs.Unsupported, "docs import: this surface cannot run writes")
	}
	if len(in.From) == 0 {
		return out, errs.New(errs.InvalidInput, "docs import: name a doc to read").WithHint("lore docs import --from TECHNICAL.md")
	}
	var drafts []draft
	for _, f := range in.From {
		ds, rel, dropped, err := readDoc(f)
		if err != nil {
			return out, err
		}
		out.Files = append(out.Files, rel)
		out.Docs = append(out.Docs, DocCount{File: rel, Boilerplate: dropped.Boilerplate, LinkOnly: dropped.LinkOnly, TooShort: dropped.TooShort})
		drafts = append(drafts, ds...)
	}
	existing, err := o.existingNotes(ctx)
	if err != nil {
		return out, err
	}
	keep, skipped := dropRepeats(drafts, existing)
	out.Skipped = skipped
	countDocs(out.Docs, keep, skipped)
	if len(keep) == 0 {
		return out, nil
	}
	lines, err := draftLines(r.Registry(), keep, in.To)
	if err != nil {
		return out, err
	}
	bctx, id, err := bulk.Begin(ctx, bulk.KindImport)
	if err != nil {
		return out, err
	}
	bctx = store.WithCommitTitle(bctx, "docs import: "+plural.Count(len(keep), "note")+" from "+strings.Join(out.Files, ", ")+", batch "+id.String())
	ao := ApplyOut{Stores: []string{}, DryRun: in.DryRun}
	if !in.DryRun {
		ao.Batch = id.String()
	}
	a := applier{run: r, ctx: bctx, dry: in.DryRun, out: &ao, what: "docs import", unit: "note"}
	a.open()
	ids := make([]string, len(lines))
	for i, l := range lines {
		res, lerr := a.line(l)
		if lerr != nil {
			engine.Discard(a.stage)
			l.name += " of " + keep[i].docRef
			return out, a.failed(l, lerr)
		}
		ids[i] = resultID(res)
	}
	if in.DryRun {
		a.note(engine.StagedStores(a.stage))
		engine.Discard(a.stage)
	} else if err = a.flush(); err != nil {
		return out, a.failed(applyLine{}, err)
	}
	out.Batch, out.Stores, out.Writes = ao.Batch, ao.Stores, ao.Writes
	for i, d := range keep {
		n := DocNote{Kind: noteKind(d.piece), Evidence: d.evidence, Title: shortTitle(d.piece)}
		if !in.DryRun {
			n.ID = ids[i]
		}
		out.Notes = append(out.Notes, n)
	}
	return out, nil
}

// countDocs fills each doc's drafted and skipped counts. A doc named
// twice in --from shares one count (the first).
func countDocs(docs []DocCount, keep []draft, skipped []DocSkip) {
	at := func(doc string) *DocCount {
		for i := range docs {
			if docs[i].File == doc {
				return &docs[i]
			}
		}
		return nil
	}
	for _, d := range keep {
		if c := at(d.doc); c != nil {
			if d.piece.Kind == docnotes.Decision {
				c.Decisions++
			} else {
				c.Memories++
			}
		}
	}
	for _, s := range skipped {
		c := at(s.doc)
		if c == nil {
			continue
		}
		switch s.Reason {
		case SkipStored:
			c.AlreadyStored++
		case SkipRepeat:
			c.RepeatInImport++
		case SkipTooLittle:
			c.TooLittleLeft++
		}
	}
}

// resultID is the "id" an add's JSON result carries ("" when none).
func resultID(res op.Result) string {
	b, err := res.JSON()
	if err != nil {
		return ""
	}
	var v struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	return v.ID
}

// readDoc reads one --from file into drafts; rel is its repo-relative
// path, dropped what the split left out.
func readDoc(path string) ([]draft, string, docnotes.Dropped, error) {
	var none docnotes.Dropped
	if strings.TrimSpace(path) == "" {
		return nil, "", none, errs.New(errs.InvalidInput, "docs import: --from is empty").WithHint("lore docs import --from TECHNICAL.md")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, "", none, errs.Wrap(errs.InvalidInput, "docs import: "+path, err)
	}
	fh, err := os.Open(abs)
	if err != nil {
		return nil, "", none, errs.Wrap(errs.NotFound, "docs import: open "+path, err).WithHint("check the path: --from names a readable markdown file")
	}
	defer func() { _ = fh.Close() }() // read only
	b, err := io.ReadAll(io.LimitReader(fh, maxDocBytes+1))
	if err != nil {
		return nil, "", none, errs.Wrap(errs.InvalidInput, "docs import: read "+path, err)
	}
	if len(b) > maxDocBytes {
		return nil, "", none, errs.New(errs.InvalidInput, "docs import: "+path+" is over "+strconv.Itoa(maxDocBytes>>20)+" MB").
			WithHint("split it and import each part: lore docs import --from <part.md>")
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return nil, "", none, errs.New(errs.InvalidInput, "docs import: "+path+" is not a text file").WithHint("--from takes a markdown doc such as README.md")
	}
	root := repoRoot(filepath.Dir(abs))
	rel := relTo(root, abs)
	var out []draft
	pieces, dropped := docnotes.SplitCounted(string(b))
	for _, p := range pieces {
		out = append(out, newDraft(p, rel, root, filepath.Dir(abs)))
	}
	return out, rel, dropped, nil
}

// newDraft is p of the doc rel with its evidence: the doc line, then each
// code path:line the text names that exists under root or dir.
func newDraft(p docnotes.Piece, rel, root, dir string) draft {
	docRef := rel + ":" + strconv.Itoa(p.Line)
	ev := []string{docRef}
	for _, c := range p.CodeRefs {
		if codeRefExists(root, dir, c) {
			ev = append(ev, c)
		}
	}
	return draft{piece: p, evidence: strings.Join(ev, evidenceSep), docRef: docRef, doc: rel, root: root, dir: dir}
}

// repoRoot is the nearest folder at or above dir holding .git; dir when
// none does (the paths are then relative to the doc's folder).
func repoRoot(dir string) string {
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, gitDir)); err == nil {
			return d
		}
		up := filepath.Dir(d)
		if up == d {
			return dir
		}
		d = up
	}
}

// relTo is abs relative to root with forward slashes.
func relTo(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// codeRefExists reports whether ref (path:line) names a file under the
// repo root or the doc's folder that has at least that many lines: an
// evidence link must lead somewhere.
func codeRefExists(root, docDir, ref string) bool {
	i := strings.LastIndexByte(ref, ':')
	p, n := ref[:i], ref[i+1:]
	line, err := strconv.Atoi(n)
	if err != nil || filepath.IsAbs(p) || strings.Contains(filepath.ToSlash(p), "../") {
		return false
	}
	for _, base := range []string{root, docDir} {
		b, rerr := os.ReadFile(filepath.Join(base, filepath.FromSlash(p)))
		if rerr == nil {
			return bytes.Count(b, []byte("\n"))+1 >= line
		}
	}
	return false
}

// existingNotes is every memory and decision the attached stores hold
// (archived ones too: a note someone threw away is not drafted again).
func (o ops) existingNotes(ctx context.Context) ([]neardup.Item, error) {
	rr, ok := upgrade.As[RowReader](o.env)
	if !ok {
		return nil, nil
	}
	var out []neardup.Item
	for _, t := range []row.Table{schema.TableMemories, schema.TableDecisions} {
		for _, src := range engine.ReadStoresOf(rr) {
			rs, err := rr.List(ctx, src, t)
			if err != nil {
				return nil, err
			}
			for _, r := range rs {
				if r.Deleted {
					continue
				}
				out = append(out, neardup.Item{Table: string(t), ID: r.ID.String(), Title: neardup.Headline(str(r, "title"), "", str(r, "body")), Body: str(r, "body")})
			}
		}
	}
	return out, nil
}

// str is a text field of r ("" when unset).
func str(r row.Row, field string) string {
	if v, ok := r.Fields[field]; ok && v != nil && v.Kind() != value.KindNull {
		return v.String()
	}
	return ""
}

// dropRepeats keeps what no earlier draft and no existing note already
// says; the rest is listed as skipped. A whole section a stored note says
// (a re-import, a note written from it by hand) is skipped whole; a
// decision is compared whole; a fact section loses only the paragraphs and
// lists said before (by a stored note or earlier in this import) and keeps
// the rest under its heading (H-221: a section is one note, but one
// repeated paragraph must not drop what the section adds).
func dropRepeats(drafts []draft, existing []neardup.Item) ([]draft, []DocSkip) {
	var keep []draft
	skipped := []DocSkip{}
	seen := map[string]string{} // folded text -> the doc line that said it first
	var earlier []neardup.Item  // the kept blocks of this import (ID: their doc line)
	var doc string              // the doc of the draft being checked
	skip := func(ev, title, existingID, reason, why string) {
		skipped = append(skipped, DocSkip{Evidence: ev, Title: title, Existing: existingID, Reason: reason, Why: why, doc: doc})
	}
	for i, d := range drafts {
		doc = d.doc
		if m := neardup.Find([]neardup.Item{pieceItem(i, d)}, existing); len(m) > 0 {
			skip(d.evidence, shortTitle(d.piece), m[0].Existing.ID, SkipStored, "already noted as "+m[0].Existing.ID+" ("+string(m[0].Why)+")")
			continue
		}
		if d.piece.Kind == docnotes.Decision {
			norm := docnotes.Norm(blocksText(d.piece.Blocks))
			if first, dup := seen[norm]; dup {
				skip(d.evidence, shortTitle(d.piece), "", SkipRepeat, "repeats "+first+" of this import")
				continue
			}
			seen[norm] = d.docRef
			keep = append(keep, d)
			continue
		}
		var blocks []docnotes.Block
		for j, b := range d.piece.Blocks {
			ref := d.doc + ":" + strconv.Itoa(b.Line)
			title := gitleaks.MaskText(docnotes.Title(b.Text))
			norm := docnotes.Norm(b.Text)
			if first, dup := seen[norm]; dup {
				skip(ref, title, "", SkipRepeat, "repeats "+first+" of this import")
				continue
			}
			it := blockItem(i, j, b)
			if m := neardup.Find([]neardup.Item{it}, existing); len(m) > 0 {
				skip(ref, title, m[0].Existing.ID, SkipStored, "already noted as "+m[0].Existing.ID+" ("+string(m[0].Why)+")")
				continue
			}
			if m := neardup.Find([]neardup.Item{it}, earlier); len(m) > 0 {
				skip(ref, title, "", SkipRepeat, "repeats "+m[0].Existing.ID+" of this import ("+string(m[0].Why)+")")
				continue
			}
			seen[norm] = ref
			it.ID = ref
			earlier = append(earlier, it)
			blocks = append(blocks, b)
		}
		if len(blocks) == len(d.piece.Blocks) {
			keep = append(keep, d)
			continue
		}
		if p, ok := d.piece.WithBlocks(blocks); ok {
			keep = append(keep, newDraft(p, d.doc, d.root, d.dir))
		} else if len(blocks) > 0 {
			skip(d.evidence, shortTitle(d.piece), "", SkipTooLittle, "too little left once the repeats are dropped")
		}
	}
	return keep, skipped
}

// blocksText is the blocks' text without the heading path.
func blocksText(bs []docnotes.Block) string {
	parts := make([]string, len(bs))
	for i, b := range bs {
		parts[i] = b.Text
	}
	return strings.Join(parts, "\n\n")
}

// pieceItem is a whole draft as the near-duplicate detector reads it. A
// fact is compared by its body only: its title is a heading path, and two
// docs' "Overview" sections are not the same note.
func pieceItem(i int, d draft) neardup.Item {
	if d.piece.Kind == docnotes.Decision {
		return neardup.Item{Table: string(schema.TableDecisions), ID: "draft-" + strconv.Itoa(i), Title: d.piece.Title, Body: d.piece.Text}
	}
	return neardup.Item{Table: string(schema.TableMemories), ID: "draft-" + strconv.Itoa(i), Body: d.piece.Text}
}

// blockItem is one paragraph or list of a fact draft as the detector
// reads it (titled by its first line, as a stored memory is).
func blockItem(i, j int, b docnotes.Block) neardup.Item {
	return neardup.Item{Table: string(schema.TableMemories), ID: "draft-" + strconv.Itoa(i) + "-" + strconv.Itoa(j),
		Title: neardup.Headline("", "", b.Text), Body: b.Text}
}

// draftLines are the memory add / decision add lines of the drafts.
func draftLines(reg *op.Registry, ds []draft, to string) ([]applyLine, error) {
	out := make([]applyLine, 0, len(ds))
	for i, d := range ds {
		in := draftInput{Body: d.piece.Text, Source: SourceImported, SourceRef: d.docRef, To: to, Key: draftKey(d.piece),
			Meta: []string{MetaEvidence + "=" + d.evidence, MetaStatus + "=" + StatusUnverified}}
		name := opMemoryAdd
		if d.piece.Kind == docnotes.Decision {
			name = opDecisionAdd
			in.Title, in.Status = d.piece.Title, decisionProposed
		}
		body, err := json.Marshal(in)
		if err != nil {
			return nil, errs.Wrap(errs.Internal, "docs import: encode "+d.docRef, err)
		}
		e, ok := reg.Lookup(name)
		if !ok {
			return nil, errs.New(errs.Internal, "docs import: no "+name+" op")
		}
		out = append(out, applyLine{n: i + 1, name: strings.ReplaceAll(name, ".", " "), entry: e, body: body})
	}
	return out, nil
}

// draftInput is the JSON input of the memory add / decision add a draft
// runs (the flag names of those commands).
type draftInput struct {
	Title     string   `json:"title,omitempty"`
	Body      string   `json:"body"`
	Status    string   `json:"status,omitempty"`
	Source    string   `json:"source"`
	SourceRef string   `json:"source_ref"`
	Meta      []string `json:"meta"`
	Key       string   `json:"key"`
	To        string   `json:"to,omitempty"`
}

// draftKey is a draft's idempotency key: the hash of its folded text, so
// the same paragraph imported again (from any line) finds its row.
func draftKey(p docnotes.Piece) string {
	sum := sha256.Sum256([]byte(string(p.Kind) + "\x00" + docnotes.Norm(p.Text)))
	return keyPrefix + hex.EncodeToString(sum[:])[:keyHexLen]
}

// noteKind is the row kind a piece drafts into.
func noteKind(p docnotes.Piece) string {
	if p.Kind == docnotes.Decision {
		return "decision"
	}
	return "memory"
}

// shortTitle is a piece's title (a fact's heading path, a decision's
// heading or first sentence) as the output prints it: a value the secret rules flag is masked (a doc's token
// must not reach scrollback, even when --secret-unstored will store it as
// ${secret:unstored}).
func shortTitle(p docnotes.Piece) string {
	t := p.Title
	if t == "" {
		t = docnotes.Title(p.Text)
	}
	return gitleaks.MaskText(t)
}
