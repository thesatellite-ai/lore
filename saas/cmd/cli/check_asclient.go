// check_asclient.go — `lore check leaks --as-client` (RES 3.9 "A repo that
// becomes client-readable"): an own repo is read as the client would read
// it before a hand-over, and refused (E_LEAK_BLOCKED) until each finding is
// cleared. No value is ever printed.
//
//	runCheckAsClient
//	   tracked files ─► leakcheck.Check with the client index (every
//	                    internal row: ids, refs, 8-word text, secrets),
//	                    rendered files (LORE.md, tool pointers) included
//	   .lore/data    ─► leakcheck.CheckCI (rows of another project)
//	   resources     ─► leakcheck.AsClientResources (internal / secret_ref /
//	                    sensitive values, store kinds, fields, refs)
//	   git history   ─► leakcheck.AsClientHistory (resource blobs that held
//	                    such values; lore does not rewrite history)
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"saas/pkg/aicoder/errcodes"
	"saas/pkg/aicoder/leakcheck"
	"saas/pkg/aicoder/lsync"
	"saas/pkg/aicoder/projresolve"

	"github.com/thesatellite-ai/lore/model/row"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// asClientMaxFile skips tracked files above this size in the text scan
// (binaries and vendored blobs; row files and rendered context are far
// smaller).
const asClientMaxFile = 2 << 20

// Columns of kind_fields read here (ops/settings owns the shapes).
const (
	acType      = "type"
	acName      = "name"
	acInternal  = "internal"
	acSensitive = "sensitive"
	acTrue      = "true"
	gitNullBlob = "0000000000000000000000000000000000000000"
)

// runCheckAsClient runs every --as-client rule and prints one report.
func runCheckAsClient(ctx context.Context, root string, explain bool) error {
	if coreApp == nil {
		return errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()+" (the core is not open)")
	}
	dataDir := filepath.Join(root, projresolve.MarkerDir, lsync.DataDirName)
	meta, err := lsync.ReadProjectMeta(dataDir)
	if err != nil {
		return errcodes.New(errcodes.InvalidInput, "no .lore/data/_meta.json here; run from the project root")
	}
	var lines []string

	// 1. Every tracked file, as the client's checkout holds it.
	items, err := trackedItems(ctx, root)
	if err != nil {
		return err
	}
	secrets, err := checkSecretScan(ctx, root)
	if err != nil {
		return err
	}
	idx, pid, _, err := leakIndexWith(ctx, root, true)
	if err != nil {
		return err
	}
	if pid == "" {
		pid = meta.ProjectID
	}
	fs := leakcheck.Blocking(leakcheck.Check(items, idx, leakcheck.Options{Secrets: true, SecretScan: secrets, RepoProjectID: pid, ClientRepo: true, DataPrefix: projectDataPrefix(ctx, root), SessionIDs: knownSessionIDs(ctx)}))
	for _, f := range fs {
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
		lines = append(lines, fmt.Sprintf("  %-40s %s\n      fix: %s", where, detail, leakFix(f, leakContext{action: leakActionCommit})))
	}

	// 2. Structure of .lore/data (rows of another project).
	files, err := readDirFiles(dataDir)
	if err != nil {
		return err
	}
	tree := make(map[string][]byte, len(files))
	for rel, b := range files {
		tree[".lore/data/"+rel] = b
	}
	for _, f := range leakcheck.CheckCI(tree, meta.ProjectID) {
		lines = append(lines, fmt.Sprintf("  %-40s %s\n      fix: move it to the store or delete it", f.Where, f.Detail))
	}

	// 3. Resources and 4. their history.
	defs, repoKinds, err := asClientDefs(ctx)
	if err != nil {
		return err
	}
	var rows []leakcheck.ResRow
	repoRes := map[string]bool{}
	current := map[string]map[string]json.RawMessage{}
	for rel, b := range files {
		if !strings.HasPrefix(rel, string(schema.TableResources)+"/") {
			continue
		}
		r, ok := leakcheck.ParseResourceRow(b)
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
	res := leakcheck.AsClientResources(rows, defs, repoRes)
	hist, err := resourceHistory(ctx, root)
	if err != nil {
		return err
	}
	res = append(res, leakcheck.AsClientHistory(hist, current, defs)...)
	for _, f := range res {
		lines = append(lines, fmt.Sprintf("  %s %s  %s: %s\n      fix: %s", f.Row, f.Name, f.Field, f.Why, f.Fix))
	}

	if len(lines) == 0 {
		fmt.Println("✓ lore check leaks --as-client: nothing internal would become visible to the client")
		return nil
	}
	if inCI() {
		return errcodes.New(errcodes.LeakBlocked, fmt.Sprintf("blocked: %d finding(s) would become visible to the client (details hidden in CI)", len(lines)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "lore: blocked, handing this repo to the client would make %d thing(s) visible:\n", len(lines))
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n(clear each, then run lore check leaks --as-client again; values are never printed)")
	return errcodes.New(errcodes.LeakBlocked, b.String())
}

// trackedItems reads every tracked file of the repo (work tree copy) as a
// leak-check item; large files are skipped (see asClientMaxFile).
func trackedItems(ctx context.Context, root string) ([]leakcheck.Item, error) {
	top, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errcodes.New(errcodes.InvalidInput, "--as-client needs a git checkout").WithCause(err)
	}
	out, err := gitOutput(ctx, top, "ls-files", "-z")
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "list tracked files").WithCause(err)
	}
	// os.Root: a tracked symlink or ../ path can never read outside the repo.
	rt, err := os.OpenRoot(top)
	if err != nil {
		return nil, errcodes.New(errcodes.Internal, "open the repo").WithCause(err)
	}
	defer func() { _ = rt.Close() }() // read-only use
	var items []leakcheck.Item
	for _, p := range bytes.Split([]byte(out), []byte{0}) {
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

// asClientDefs reads every kind_fields definition the core can see (the
// repo store and each attached store) and the repo's own kind ids. An
// unreadable store refuses (fail closed, R2).
func asClientDefs(ctx context.Context) (map[string]leakcheck.ResField, map[string]bool, error) {
	idents, err := coreApp.StoreIdentities(ctx)
	if err != nil {
		return nil, nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()).WithCause(err)
	}
	sources := []store.Source{store.SourceRepo}
	for _, si := range idents {
		if si.Name != store.SourceRepo {
			sources = append(sources, si.Name)
		}
	}
	defs := map[string]leakcheck.ResField{}
	repoKinds := map[string]bool{}
	for _, src := range sources {
		fields, lerr := coreApp.List(ctx, src, schema.TableKindFields)
		if lerr != nil {
			return nil, nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()+" (store "+string(src)+")").WithCause(lerr)
		}
		for _, r := range fields {
			if r.Deleted {
				continue
			}
			id := r.ID.String()
			if _, seen := defs[id]; seen && src != store.SourceRepo {
				continue
			}
			defs[id] = resFieldOf(r, src == store.SourceRepo)
		}
		if src != store.SourceRepo {
			continue
		}
		kinds, kerr := coreApp.List(ctx, src, schema.TableKinds)
		if kerr != nil {
			return nil, nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()).WithCause(kerr)
		}
		for _, k := range kinds {
			if !k.Deleted {
				repoKinds[k.ID.String()] = true
			}
		}
	}
	return defs, repoKinds, nil
}

// resFieldOf narrows a kind_fields row to what the rules read.
func resFieldOf(r row.Row, inRepo bool) leakcheck.ResField {
	txt := func(k string) string {
		v, ok := r.Fields[k]
		if !ok || v == nil || v.Kind() == value.KindNull {
			return ""
		}
		return v.String()
	}
	in := txt(acInternal)
	return leakcheck.ResField{Name: txt(acName), Type: txt(acType), Internal: in == acTrue, InternalSet: in != "", Sensitive: txt(acSensitive) == acTrue, InRepo: inRepo}
}

// resourceHistory reads every resource row blob git history holds for this
// project's .lore/data/resources (every branch and tag).
func resourceHistory(ctx context.Context, root string) ([]leakcheck.ResRow, error) {
	top, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, nil // not a git checkout: no history
	}
	dir := projectDataPrefix(ctx, root) + string(schema.TableResources) + "/"
	out, err := gitOutput(ctx, top, "log", "--all", "--format=", "--raw", "--no-abbrev", "--no-renames", "--", dir)
	if err != nil {
		return nil, nil // no commits yet
	}
	blobs := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
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
	var in bytes.Buffer
	for b := range blobs {
		in.WriteString(b + "\n")
	}
	cmd := exec.CommandContext(ctx, "git", "cat-file", "--batch")
	cmd.Dir = top
	cmd.Stdin = &in
	out, err := cmd.Output()
	if err != nil {
		return nil, errcodes.New(errcodes.LeakBlocked, errCannotCheck.Error()+" (git history)").WithCause(err)
	}
	rd := bufio.NewReader(bytes.NewReader(out))
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
