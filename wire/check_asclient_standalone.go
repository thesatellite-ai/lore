package wire

// check_asclient_standalone.go — `lore check leaks --as-client` on cmd/lore
// (RES 3.9 "A repo that becomes client-readable", later 515): an own repo
// is read as its client would read it before a hand-over, and refused
// (E_LEAK_BLOCKED) until each finding is cleared. Port of the old
// binary's check_asclient.go onto the core's leak index. No value is ever
// printed.
//
//	CheckAsClient
//	   tracked files ─► leakcheck.Check as a client repo with the full
//	                    leak index (every internal row: ids, refs, 8-word
//	                    text, metadata) and the write scanner (secrets);
//	                    rendered files (LORE.md, tool pointers) included
//	   .lore/data    ─► leakcheck.CheckCI (rows of another project)
//	   resources     ─► leakcheck.AsClientResources (internal / secret_ref /
//	                    sensitive values, store kinds, fields, refs)
//	   git history   ─► leakcheck.AsClientHistory (resource blobs that held
//	                    such values; lore does not rewrite history)
//
// Fails closed: a store, table, tracked file list or history that cannot
// be read refuses rather than passing.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	gitbackend "github.com/thesatellite-ai/lore/adapters/backend/git"
	"github.com/thesatellite-ai/lore/adapters/backend/rowfs"
	"github.com/thesatellite-ai/lore/adapters/orgcfg"
	"github.com/thesatellite-ai/lore/logic/leakcheck"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// AsClientClean is the --as-client pass line (v0.1.17's wording).
const AsClientClean = "✓ lore check leaks --as-client: nothing internal would become visible to the client"

// asClientMaxFile skips tracked files above this size in the text scan
// (binaries and vendored blobs; row files and rendered context are far
// smaller).
const asClientMaxFile = 2 << 20

// Columns of kind_fields the resource rules read (ops/settings owns the
// shapes), and git's null blob id in a --raw log line.
const (
	acType      = "type"
	acName      = "name"
	acInternal  = "internal"
	acSensitive = "sensitive"
	acTrue      = "true"
	gitNullBlob = "0000000000000000000000000000000000000000"
)

// CheckAsClient runs every --as-client rule over the checkout at root;
// explain adds each finding's rule and source. nil means nothing would
// become visible; otherwise one E_LEAK_BLOCKED report (in CI only the
// count: a CI log is the client's to read).
func (a *App) CheckAsClient(ctx context.Context, root string, explain bool) error {
	if r, err := a.knownRepoTop(ctx); err == nil && r != "" {
		root = r // the project's own folder, also when run from a sub-folder
	}
	pid := orgcfg.ProjectID(root)
	if pid == "" { // no _meta.json yet: the project row the App resolves
		if p, perr := a.Project(ctx); perr == nil && !p.IsZero() {
			pid = p.String()
		}
	}
	if pid == "" {
		return errs.New(errs.InvalidInput, "no lore project here; run from the project root (lore init)")
	}
	top := gitTop(ctx, root)
	if top == "" {
		return errs.New(errs.InvalidInput, "--as-client needs a git checkout")
	}
	prefix := asClientDataPrefix(top, root)

	// 1. Every tracked file, as the client's checkout holds it.
	items, err := trackedItems(ctx, top)
	if err != nil {
		return err
	}
	idx, err := a.asClientIndex(ctx)
	if err != nil {
		return err
	}
	secrets, err := a.asClientSecrets(ctx, root)
	if err != nil {
		return err
	}
	sessions, err := a.KnownToolSessionIDs(ctx)
	if err != nil {
		return errs.Wrap(errs.LeakBlocked, leakCannotCheck+" (this machine's session ids)", err)
	}
	var lines []string
	fs := leakcheck.Blocking(leakcheck.Check(items, idx, leakcheck.Options{Secrets: true, SecretScan: secrets,
		RepoProjectID: pid, ClientRepo: true, DataPrefix: prefix, SessionIDs: sessions}))
	for _, f := range fs {
		lines = append(lines, asClientLine(f, explain))
	}

	// 2. Structure of this project's .lore/data (rows of another project).
	tree, err := readDataTree(filepath.Join(root, rowfs.LoreDir, rowfs.DataDir))
	if errors.Is(err, iofs.ErrNotExist) {
		tree, err = map[string][]byte{}, nil // a project with no row file yet
	}
	if err != nil {
		return errs.Wrap(errs.LeakBlocked, "cannot check: .lore/data could not be read", err)
	}
	for _, f := range leakcheck.CheckCI(tree, pid) {
		lines = append(lines, fmt.Sprintf("  %-40s %s\n      fix: move it to the store or delete it", f.Where, f.Detail))
	}

	// 3. Resources and 4. their history.
	res, err := a.asClientResources(ctx, top, prefix, tree)
	if err != nil {
		return err
	}
	for _, f := range res {
		lines = append(lines, fmt.Sprintf("  %s %s  %s: %s\n      fix: %s", f.Row, f.Name, f.Field, f.Why, f.Fix))
	}

	if len(lines) == 0 {
		return nil
	}
	if inCIEnv() {
		return errs.New(errs.LeakBlocked, fmt.Sprintf("blocked: %d finding(s) would become visible to the client (details hidden in CI)", len(lines)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "lore: blocked, handing this repo to the client would make %d thing(s) visible:\n", len(lines))
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n(clear each, then run lore check leaks --as-client again; values are never printed)")
	return errs.New(errs.LeakBlocked, b.String())
}

// asClientLine is one finding of the tracked-file scan with its fix. A
// text finding names the internal note by id, never its words, unless
// explain asks for the rule and the source.
func asClientLine(f leakcheck.Finding, explain bool) string {
	where := f.Where
	if f.Line > 0 {
		where = fmt.Sprintf("%s:%d", where, f.Line)
	}
	detail := f.Detail
	if !explain && f.Rule == leakcheck.RuleText {
		detail = fmt.Sprintf("matches internal note %s (%d words)", f.RowID, f.Words)
	}
	if explain {
		detail = fmt.Sprintf("[%s rule] %s %s", f.Rule, detail, f.Label)
	}
	return fmt.Sprintf("  %-40s %s\n      fix: %s", where, detail, leakFix(LeakWhereCommit, f.Path))
}

// asClientIndex is the leak index of a repo read as a client's: every
// internal and personal row of every attached store, plus this repo's own
// key rows for the foreign-key rule. An unreadable store refuses.
func (a *App) asClientIndex(ctx context.Context) (*leakcheck.Index, error) {
	idents, err := a.StoreIdentities(ctx)
	if err != nil {
		return nil, errs.Wrap(errs.LeakBlocked, leakCannotCheck, err)
	}
	names := make([]store.Source, 0, len(idents))
	for _, si := range idents {
		names = append(names, si.Name)
	}
	rows, err := a.leakRows(ctx, true, names)
	if err != nil {
		return nil, err
	}
	keys, err := a.List(ctx, store.SourceRepo, schema.TableMetaKeys)
	if err != nil {
		return nil, errs.Wrap(errs.LeakBlocked, leakCannotCheck+" (the repo's key rows)", err)
	}
	return leakcheck.NewIndex(withRepoKeys(rows, keys), nil), nil
}

// asClientSecrets is the secret rule over tracked files: the write scanner
// with the repo's committed allow entries (a hand-over shows what is
// committed).
func (a *App) asClientSecrets(ctx context.Context, root string) (leakcheck.SecretScan, error) {
	sc, err := a.HookScanner(ctx)
	if err != nil {
		return nil, errs.Wrap(errs.LeakBlocked, "cannot check for secrets: the scanner could not be built", err)
	}
	allows, err := committedAllows(ctx, root)
	if err != nil {
		return nil, err
	}
	return func(it leakcheck.Item) []leakcheck.SecretHit {
		return scanDoc(sc, allows, it.Path, it.Content)
	}, nil
}

// asClientDataPrefix is this project's .lore/data folder relative to the
// git top ("<folder>/.lore/data/" in a monorepo sub-project): only row
// files under it must carry this project's id, and only its resource
// history is read.
func asClientDataPrefix(top, root string) string {
	rel, err := filepath.Rel(realGitPath(top), realGitPath(root))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return checkDataPrefix
	}
	return filepath.ToSlash(rel) + "/" + checkDataPrefix
}

// realGitPath is p with symlinks resolved (p itself when that fails), so
// git's top and the project root compare on one spelling (macOS /var).
func realGitPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// trackedItems reads every tracked file of the checkout at top (work tree
// copy) as a leak-check item; large files, symlinks and submodules are
// skipped (asClientMaxFile). os.Root: a tracked symlink or ../ path never
// reads outside the repo.
func trackedItems(ctx context.Context, top string) ([]leakcheck.Item, error) {
	out, err := gitRun{dir: top, args: []string{"ls-files", "-z"}}.output(ctx)
	if err != nil {
		return nil, errs.Wrap(errs.LeakBlocked, leakCannotCheck+" (the tracked files)", err)
	}
	rt, err := os.OpenRoot(top)
	if err != nil {
		return nil, errs.Wrap(errs.LeakBlocked, leakCannotCheck+" (the repo)", err)
	}
	defer func() { _ = rt.Close() }() // read-only use
	var items []leakcheck.Item
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		path := string(p)
		st, serr := rt.Lstat(path)
		if serr != nil || !st.Mode().IsRegular() || st.Size() > asClientMaxFile {
			continue // deleted in the work tree, a symlink, a submodule, or too large
		}
		content, rerr := rt.ReadFile(path)
		if rerr != nil {
			continue
		}
		items = append(items, leakcheck.Item{Where: path, Path: path, Content: content})
	}
	return items, nil
}

// asClientResources applies the resource rules to this project's live
// resource row files (tree, keyed ".lore/data/<rel>") and to every
// resource blob in git history under prefix.
func (a *App) asClientResources(ctx context.Context, top, prefix string, tree map[string][]byte) ([]leakcheck.AsClientFinding, error) {
	defs, repoKinds, err := a.asClientDefs(ctx)
	if err != nil {
		return nil, err
	}
	resDir := checkDataPrefix + string(schema.TableResources) + "/"
	paths := make([]string, 0, len(tree))
	for p := range tree {
		if strings.HasPrefix(p, resDir) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	var rows []leakcheck.ResRow
	repoRes := map[string]bool{}
	current := map[string]map[string]json.RawMessage{}
	for _, p := range paths {
		r, ok := leakcheck.ParseResourceRow(tree[p])
		if !ok {
			continue
		}
		r.KindInRepo = repoKinds[r.KindID]
		rows = append(rows, r)
		repoRes[r.ID] = true
		cur := map[string]json.RawMessage{}
		for _, e := range r.Entries {
			cur[e.FieldID] = e.Value
		}
		current[r.ID] = cur
	}
	out := leakcheck.AsClientResources(rows, defs, repoRes)
	hist, err := resourceHistory(ctx, top, prefix)
	if err != nil {
		return nil, err
	}
	return append(out, leakcheck.AsClientHistory(hist, current, defs)...), nil
}

// asClientDefs reads every kind_fields definition the App can see (the
// repo store first, then each attached store) and the repo's own kind ids.
// An unreadable store refuses (fail closed, R2).
func (a *App) asClientDefs(ctx context.Context) (map[string]leakcheck.ResField, map[string]bool, error) {
	idents, err := a.StoreIdentities(ctx)
	if err != nil {
		return nil, nil, errs.Wrap(errs.LeakBlocked, leakCannotCheck, err)
	}
	sources := []store.Source{store.SourceRepo}
	for _, si := range idents {
		sources = append(sources, si.Name)
	}
	defs := map[string]leakcheck.ResField{}
	repoKinds := map[string]bool{}
	for _, src := range sources {
		fields, lerr := a.List(ctx, src, schema.TableKindFields)
		if lerr != nil {
			return nil, nil, errs.Wrap(errs.LeakBlocked, leakCannotCheck+" (store "+string(src)+")", lerr)
		}
		for _, r := range fields {
			if r.Deleted {
				continue
			}
			id := r.ID.String()
			if _, seen := defs[id]; seen {
				continue // the repo's own definition wins (read first)
			}
			defs[id] = resFieldOf(r, src == store.SourceRepo)
		}
		if src != store.SourceRepo {
			continue
		}
		kinds, kerr := a.List(ctx, src, schema.TableKinds)
		if kerr != nil {
			return nil, nil, errs.Wrap(errs.LeakBlocked, leakCannotCheck+" (the repo's kinds)", kerr)
		}
		for _, k := range kinds {
			if !k.Deleted {
				repoKinds[k.ID.String()] = true
			}
		}
	}
	return defs, repoKinds, nil
}

// resFieldOf narrows a kind_fields row to what the rules read. internal
// unset is recorded (InternalSet false): the rules treat it as internal.
func resFieldOf(r row.Row, inRepo bool) leakcheck.ResField {
	str := func(k string) string {
		v, ok := r.Fields[k]
		if !ok || v == nil || v.Kind() == value.KindNull {
			return ""
		}
		return v.String()
	}
	flag := func(k string) (on, set bool) {
		v, ok := r.Fields[k]
		if !ok || v == nil || v.Kind() == value.KindNull {
			return false, false
		}
		return v.String() == acTrue, true // a bool value prints true/false
	}
	internal, set := flag(acInternal)
	sensitive, _ := flag(acSensitive)
	return leakcheck.ResField{Name: str(acName), Type: str(acType), Internal: internal, InternalSet: set, Sensitive: sensitive, InRepo: inRepo}
}

// resourceHistory reads every resource row blob git history holds under
// prefix + "resources/" (every branch and tag). No commits yet: none.
func resourceHistory(ctx context.Context, top, prefix string) ([]leakcheck.ResRow, error) {
	dir := prefix + string(schema.TableResources) + "/"
	out, err := gitRun{dir: top, args: []string{"log", "--all", "--format=", "--raw", "--no-abbrev", "--no-renames", "--", dir}}.output(ctx)
	if err != nil {
		if _, herr := (gitRun{dir: top, args: []string{"rev-parse", "--verify", "-q", "HEAD"}}).output(ctx); herr != nil {
			return nil, nil // no commits yet: no history
		}
		return nil, errs.Wrap(errs.LeakBlocked, leakCannotCheck+" (git history)", err)
	}
	blobs := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		// ":100644 100644 <old> <new> M\t<path>"
		f := strings.Fields(strings.SplitN(l, "\t", 2)[0])
		if len(f) < 5 || !strings.HasPrefix(f[0], ":") {
			continue
		}
		for _, b := range f[2:4] {
			if b != gitNullBlob {
				blobs[b] = true
			}
		}
	}
	if len(blobs) == 0 {
		return nil, nil
	}
	return catResourceBlobs(ctx, top, blobs)
}

// catResourceBlobs reads blobs through one `git cat-file --batch`.
func catResourceBlobs(ctx context.Context, top string, blobs map[string]bool) ([]leakcheck.ResRow, error) {
	ids := make([]string, 0, len(blobs))
	for b := range blobs {
		ids = append(ids, b)
	}
	sort.Strings(ids)
	bin, err := exec.LookPath(gitbackend.DefaultBinary)
	if err != nil {
		return nil, errs.Wrap(errs.Internal, "wire: git is not installed", err)
	}
	// Built directly, as gitRun does: an absolute LookPath'd binary, fixed
	// argv entries, no shell; runCancellable kills it when ctx ends.
	cmd := &exec.Cmd{Path: bin, Args: []string{bin, "cat-file", "--batch"}, Dir: top, Stdin: strings.NewReader(strings.Join(ids, "\n") + "\n")}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if rerr := runCancellable(ctx, cmd); rerr != nil {
		return nil, errs.Wrap(errs.LeakBlocked, leakCannotCheck+" (git history)", rerr)
	}
	rd := bufio.NewReader(&stdout)
	var rows []leakcheck.ResRow
	for {
		hdr, rerr := rd.ReadString('\n')
		if rerr != nil {
			break
		}
		var sha, typ string
		var size int
		if n, _ := fmt.Sscanf(hdr, "%s %s %d", &sha, &typ, &size); n != 3 {
			continue // "<sha> missing"
		}
		body := make([]byte, size)
		if _, rerr = io.ReadFull(rd, body); rerr != nil {
			break
		}
		_, _ = rd.ReadByte() // the newline after each object
		if r, ok := leakcheck.ParseResourceRow(body); ok {
			rows = append(rows, r)
		}
	}
	return rows, nil
}

// inCIEnv reports a CI run (any of ciMarkers set non-empty).
func inCIEnv() bool {
	for _, k := range ciMarkers {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}
