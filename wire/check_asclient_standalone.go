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
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

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
		return errs.New(errs.InvalidInput, "no lore project here; run from the project root").WithHint("lore init sets this repo up")
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
	asClientSay(items)
	secrets, err := a.asClientSecrets(ctx, root)
	if err != nil {
		return err
	}
	// C-9: the secret scan of every tracked file (the bulk of the work)
	// runs while the leak index is built (a fresh clone's cache fill);
	// neither reads the other.
	scanned := make(chan leakcheck.SecretScan, 1)
	go func() { scanned <- presetSecrets(items, secrets) }()
	idx, err := a.asClientIndex(ctx)
	secrets = <-scanned
	if err != nil {
		return err
	}
	sessions, err := a.KnownToolSessionIDs(ctx)
	if err != nil {
		return leakCannotCheckErr("this machine's session ids", err)
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
		return nil, leakCannotCheckErr("", err)
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
		return nil, leakCannotCheckErr("the repo's key rows", err)
	}
	return leakcheck.NewIndex(withRepoKeys(rows, keys), nil), nil
}

// asClientSecrets is the secret rule over tracked files: the write scanner
// with the repo's committed allow entries (a hand-over shows what is
// committed).
func (a *App) asClientSecrets(ctx context.Context, root string) (leakcheck.SecretScan, error) {
	sc, err := a.HookScanner(ctx)
	if err != nil {
		return nil, errs.Wrap(errs.LeakBlocked, "cannot check for secrets: the scanner could not be built: "+err.Error(), err)
	}
	allows, err := committedAllows(ctx, root)
	if err != nil {
		return nil, err
	}
	_ = sc.Scan("", "") // build a lazy scanner once, before leakcheck.Check's workers share it
	return func(it leakcheck.Item) []leakcheck.SecretHit {
		return scanDoc(sc, allows, it.Path, it.Content, false)
	}, nil
}

// asClientProgressBytes is the size of tracked text above which
// --as-client says what it reads before it starts: every file the client
// would receive is scanned for secrets and internal notes, and a repo with
// tens of MB of tracked text (generated code) takes seconds even on every
// core (C-9: 74 MB, about 110 CPU-seconds).
const asClientProgressBytes = 8 << 20

// asClientProgress is where that line goes (a test swaps it).
var asClientProgress io.Writer = os.Stderr

// asClientSay prints the progress line for a big checkout: counts only,
// never a path or a value (a CI log is the client's to read).
func asClientSay(items []leakcheck.Item) {
	total := 0
	for _, it := range items {
		total += len(it.Content)
	}
	if total < asClientProgressBytes {
		return
	}
	_, _ = fmt.Fprintf(asClientProgress, "lore check leaks --as-client: reading %d tracked files (%d MB) as the client would, for secrets and internal notes; every file the client receives is checked, so a big repo takes a while\n",
		len(items), total>>20)
}

// presetSecrets runs scan over every item, one worker per CPU, and returns
// a SecretScan that answers from those results (by item path; an item it
// was not given is scanned then). scan must be safe for concurrent use.
func presetSecrets(items []leakcheck.Item, scan leakcheck.SecretScan) leakcheck.SecretScan {
	hits := make([][]leakcheck.SecretHit, len(items))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), len(items)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(items) {
					return
				}
				hits[i] = scan(items[i])
			}
		}()
	}
	wg.Wait()
	byPath := make(map[string][]leakcheck.SecretHit, len(items))
	for i, it := range items {
		byPath[it.Path] = hits[i]
	}
	return func(it leakcheck.Item) []leakcheck.SecretHit {
		if h, ok := byPath[it.Path]; ok {
			return h
		}
		return scan(it)
	}
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

// realGitPath is p with symlinks resolved, so git's top and the project
// root compare on one spelling (macOS /var). resolvedPath also resolves a
// root that does not exist yet through its deepest existing folder; plain
// EvalSymlinks left such a root unresolved and the prefix fell back to the
// top's .lore/data/.
func realGitPath(p string) string { return resolvedPath(p) }

// trackedItems reads every tracked file of the checkout at top (work tree
// copy) as a leak-check item; large files, symlinks and submodules are
// skipped (asClientMaxFile). os.Root: a tracked symlink or ../ path never
// reads outside the repo.
func trackedItems(ctx context.Context, top string) ([]leakcheck.Item, error) {
	out, err := gitRun{dir: top, args: []string{"ls-files", "-z"}}.output(ctx)
	if err != nil {
		return nil, leakCannotCheckErr("the tracked files", err)
	}
	rt, err := os.OpenRoot(top)
	if err != nil {
		return nil, leakCannotCheckErr("the repo", err)
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
		return nil, nil, leakCannotCheckErr("", err)
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
			return nil, nil, leakCannotCheckErr("store "+string(src), lerr)
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
			return nil, nil, leakCannotCheckErr("the repo's kinds", kerr)
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
		return nil, leakCannotCheckErr("git history", err)
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
	cmd := &exec.Cmd{Path: bin, Args: []string{bin, "cat-file", "--batch"}, Dir: top, Env: gitEnv(nil), Stdin: strings.NewReader(strings.Join(ids, "\n") + "\n")}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if rerr := runCancellable(ctx, cmd); rerr != nil {
		return nil, leakCannotCheckErr("git history", rerr)
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

// InCIEnv reports a CI run (any of the CI marker variables set non-empty):
// the host's live hook then reads the checkout only, fetching nothing and
// writing nothing in .git (wire/host/live_still.go).
func InCIEnv() bool { return inCIEnv() }
