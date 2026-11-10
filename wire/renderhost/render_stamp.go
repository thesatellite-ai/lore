// render_stamp.go — where each rendered file came from, and whether it
// still matches (SOURCE_OF_TRUTH rows 10 to 12, DECISIONS_LOG 31).
//
// Every file `lore render` writes is a copy of rows. Each carries a stamp,
// a hash of its inputs: the origin (store, id, HLC) of every row it holds,
// the settings that shape its text, and the text itself before the stamp
// is added. The stamp sits where the file already had a generated header:
//
//	.lore/LORE.md                     the canary  <!-- AICODER:CANARY=rnd_<16 hex> SCHEMA=v2 -->
//	~/.lore/render/<repo>/<store>.md  line 2      <!-- lore:inputs <16 hex> -->
//
// LORE.full.md, index.md and the import lines in CLAUDE.local.md carry no
// stamp of their own (their text is fixed by golden tests and transcripts):
// they are compared byte for byte with what render would write now.
//
//	plan (planCommitted / planPrivate, nothing written)
//	  ─▶ file missing          ─▶ CopyMissing
//	  ─▶ stamp differs         ─▶ CopyStale   (a row or setting changed)
//	  ─▶ stamp same, bytes not ─▶ CopyEdited  (edited by hand)
//
// Same inputs, same stamp: the hash reads only row files (identical in
// every clone) and committed settings, never a time or a machine path, so
// two clones render the same bytes (SC-12, the team golden test).

package renderhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/thesatellite-ai/lore/logic/policy"
	"github.com/thesatellite-ai/lore/logic/toolprofile"
	"github.com/thesatellite-ai/lore/model/errs"
	"github.com/thesatellite-ai/lore/model/ids"
	opsrender "github.com/thesatellite-ai/lore/ops/render"
)

// stampHexLen is the stamp's length in hex digits (64 bits, as the canary
// always was).
const stampHexLen = 16

// renderFixCommand is the fix of every rendered copy.
const renderFixCommand = "lore render"

// canaryRe finds LORE.md's canary (its stamp) in the first line.
var canaryRe = regexp.MustCompile(`AICODER:CANARY=(` + string(ids.PrefixRenderHistory) + `_[0-9a-f]+)`)

// privateStampPrefix starts the stamp line of a private store file.
const privateStampPrefix = "<!-- lore:inputs "

// privateStampRe finds a private file's stamp.
var privateStampRe = regexp.MustCompile(`(?m)^<!-- lore:inputs ([0-9a-f]+) -->$`)

// origin is "<store>/<id>@<hlc>": where r came from and which version.
// The store is the attached source name (the repo store, or a shareable
// store's name in the committed stores.yaml), the same in every clone.
func (r renderRow) origin() string {
	return string(r.source) + "/" + r.ID.String() + "@" + r.HLC.String()
}

// inputDigest is the text the LORE.md stamp hashes besides the body: one
// line per rendered row's origin, sorted, and the settings that shape the
// text (warn_at only decides --check, so it is not one of them).
func inputDigest(rules []pinnedRule, hotfixes []pinnedHotfix, s contextSettings) string {
	lines := make([]string, 0, len(rules)+len(hotfixes)+1)
	for _, r := range rules {
		lines = append(lines, "rule "+r.Origin)
	}
	for _, h := range hotfixes {
		lines = append(lines, "hotfix "+h.Origin)
	}
	slices.Sort(lines)
	lines = append(lines, fmt.Sprintf("settings profile=%s budget=%d entry_max=%d", s.Profile, s.Budget, s.EntryMax))
	return strings.Join(lines, "\n")
}

// stampOf is the 16-hex hash of inputs and text.
func stampOf(inputs, text string) string {
	sum := sha256.Sum256([]byte(inputs + "\n\x00\n" + text))
	return hex.EncodeToString(sum[:])[:stampHexLen]
}

// renderCanary is LORE.md's canary: its stamp with the render-history
// prefix (rnd_<16 hex>, the form every reader and transcript knows).
func renderCanary(inputs, content string) string {
	return string(ids.PrefixRenderHistory) + "_" + stampOf(inputs, content)
}

// canaryIn is the canary of a LORE.md text ("" when it has none).
func canaryIn(text string) string {
	first, _, _ := strings.Cut(text, "\n")
	if m := canaryRe.FindStringSubmatch(first); m != nil {
		return m[1]
	}
	return ""
}

// privateStampLine is a private store file's stamp line.
func privateStampLine(stamp string) string { return privateStampPrefix + stamp + " -->" }

// withPrivateStamp puts the stamp of a private store file (its rows'
// origins, the internal budget and its text) on its second line.
func withPrivateStamp(text string, origins []string, budget int) string {
	o := slices.Clone(origins)
	slices.Sort(o)
	inputs := strings.Join(o, "\n") + fmt.Sprintf("\nsettings %s=%d", policy.KeyContextBudgetInternal, budget)
	head, rest, _ := strings.Cut(text, "\n")
	return head + "\n" + privateStampLine(stampOf(inputs, text)) + "\n" + rest
}

// privateStampIn is a private file's stamp ("" when it has none).
func privateStampIn(text string) string {
	if m := privateStampRe.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// copyVerdict compares one stamped file with what render would write now.
// stampIn reads the stamp from a text; "" kind means it matches.
func copyVerdict(path, want string, stampIn func(string) string) (opsrender.CopyKind, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	switch {
	case os.IsNotExist(err):
		return opsrender.CopyMissing, nil
	case err != nil:
		return "", errs.Wrap(errs.BadPath, "read "+path, err).WithHint("check that the file can be read, then run lore render")
	}
	got := string(data)
	switch {
	case got == want:
		return "", nil
	case stampIn != nil && stampIn(got) == stampIn(want):
		return opsrender.CopyEdited, nil
	}
	return opsrender.CopyStale, nil
}

// copyMessage is the sentence of one rendered-file finding.
func copyMessage(kind opsrender.CopyKind, file, what string) string {
	switch kind {
	case opsrender.CopyMissing:
		return file + " is missing: " + what + " is not on disk"
	case opsrender.CopyEdited:
		return file + " was edited by hand since it was rendered: " + what + " no longer matches its rows (lore render replaces the edit)"
	case opsrender.CopyStale, opsrender.PointerOrphan, opsrender.PointerMissing:
	}
	return file + " is stale: its rows or render settings changed since it was rendered, so " + what + " is out of date"
}

// copiesOf compares the rendered files of the repo at root with plan p:
// LORE.md (out, as render was given it), LORE.full.md, the private store
// files, index.md and the tools' private import files. It writes nothing.
func (h *Host) copiesOf(ctx context.Context, root, out string, p *committedPlan) ([]opsrender.CopyFinding, error) {
	var found []opsrender.CopyFinding
	add := func(kind opsrender.CopyKind, file, what string) {
		if kind != "" {
			found = append(found, opsrender.CopyFinding{Kind: kind, File: file, Message: copyMessage(kind, file, what), Fix: renderFixCommand})
		}
	}
	outPath := out
	if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(root, filepath.FromSlash(out))
	}
	kind, err := copyVerdict(outPath, p.body, canaryIn)
	if err != nil {
		return nil, err
	}
	add(kind, filepath.ToSlash(out), "the summary agents load")
	if p.in.Settings.FullFile && kind != opsrender.CopyMissing {
		fullPath := filepath.Join(filepath.Dir(outPath), fullFileName)
		// A LORE.full.md lore did not write is the person's (render refuses
		// to replace it); it is no copy of the rows.
		if refuseForeignFull(fullPath) == nil {
			fk, ferr := copyVerdict(fullPath, p.full, nil)
			if ferr != nil {
				return nil, ferr
			}
			if fk == opsrender.CopyEdited {
				fk = opsrender.CopyStale
			}
			add(fk, filepath.ToSlash(filepath.Join(filepath.Dir(out), fullFileName)), "the full fallback file")
		}
	}
	priv, err := h.privateCopies(ctx, root)
	if err != nil {
		return nil, err
	}
	return append(found, priv...), nil
}

// privateCopies compares the private render of the repo at root (row 11)
// with what render would write now.
func (h *Host) privateCopies(ctx context.Context, root string) ([]opsrender.CopyFinding, error) {
	if h.app == nil {
		return nil, nil
	}
	pp, err := h.planPrivate(ctx, root)
	if err != nil {
		return nil, err
	}
	var found []opsrender.CopyFinding
	if len(pp.stores) > 0 {
		for _, f := range pp.files {
			stampIn := privateStampIn
			what := "the private render of an internal store"
			if filepath.Base(f.path) == privateIndexFile {
				stampIn, what = nil, "the private render's index"
			}
			kind, verr := copyVerdict(f.path, f.body, stampIn)
			if verr != nil {
				return nil, verr
			}
			if kind == opsrender.CopyEdited && stampIn == nil {
				kind = opsrender.CopyStale
			}
			if kind != "" {
				file := h.redact(f.path)
				found = append(found, opsrender.CopyFinding{Kind: kind, File: file, Message: copyMessage(kind, file, what), Fix: renderFixCommand})
			}
		}
	}
	for _, l := range pp.locals {
		if l.in == l.out {
			continue
		}
		kind, what := opsrender.CopyStale, "its lore import lines"
		if l.in == "" {
			kind = opsrender.CopyMissing
		}
		found = append(found, opsrender.CopyFinding{Kind: kind, File: l.rel, Message: copyMessage(kind, l.rel, what), Fix: renderFixCommand})
	}
	return found, nil
}

// pointerCopies is row 12: the tool files against context.targets (the
// source). Only a repo whose targets come from its context.targets row is
// compared; a pointer file is never removed (it may hold the person's own
// lines), so an orphan's fix keeps it listed and the message says how to
// drop it by hand.
func pointerCopies(root string, pick targetPick) []opsrender.CopyFinding {
	if pick.sel.Reason != toolprofile.ReasonSetting {
		return nil
	}
	names := make([]string, 0, len(pick.targets))
	var found []opsrender.CopyFinding
	for _, t := range pick.targets {
		names = append(names, t.profile.Name)
		if !t.profile.HasPointerFile() {
			continue
		}
		if (rootProbe{root: root}).State(t.profile.PointerFile) == toolprofile.FileWired {
			continue
		}
		found = append(found, opsrender.CopyFinding{Kind: opsrender.PointerMissing, File: t.profile.PointerFile,
			Message: fmt.Sprintf("%s has no lore pointer, but %s lists %s, so %s does not load %s",
				t.profile.PointerFile, policy.KeyContextTargets, t.profile.Name, t.profile.Name, LoreMDRel),
			Fix: renderFixCommand})
	}
	for _, p := range pick.sel.Unlisted {
		keep := strings.Join(append(slices.Clone(names), p.Name), ",")
		found = append(found, opsrender.CopyFinding{Kind: opsrender.PointerOrphan, File: p.PointerFile,
			Message: fmt.Sprintf("%s carries lore's pointer, but %s (%s) does not list %s, so render no longer keeps it up to date; keep it with the fix, or delete the lore:pointer block from %s by hand (lore never removes it for you)",
				p.PointerFile, policy.KeyContextTargets, strings.Join(names, ","), p.Name, p.PointerFile),
			Fix: "lore policy set " + string(policy.KeyContextTargets) + " " + keep + " --yes"})
	}
	return found
}

// renderedCopies is the doctor's view (opsrender.CopyChecker): every
// rendered file and pointer file of the repo at root against its source.
// A repo that never rendered has none.
func (h *Host) renderedCopies(ctx context.Context, root string) ([]opsrender.CopyFinding, error) {
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(LoreMDRel))); err != nil {
		return nil, nil
	}
	projectID, err := h.projectID(ctx)
	if err != nil {
		return nil, err
	}
	p, err := h.planCommitted(ctx, projectID, root, nil, nil)
	if err != nil {
		return nil, err
	}
	found, err := h.copiesOf(ctx, root, LoreMDRel, p)
	if err != nil {
		return nil, err
	}
	return append(found, pointerCopies(root, p.pick)...), nil
}

// staleError is render --check's refusal for out-of-date copies (nil when
// there are none). DECISION OV18-1: a failed check is E_REFUSED.
func staleError(found []opsrender.CopyFinding) error {
	if len(found) == 0 {
		return nil
	}
	parts := make([]string, 0, len(found))
	for _, f := range found {
		parts = append(parts, f.Message)
	}
	what := "a rendered file is"
	if len(found) > 1 {
		what = fmt.Sprintf("%d rendered files are", len(found))
	}
	return errs.New(errs.Refused, what+" out of date: "+strings.Join(parts, "; ")).
		WithHint("run `lore render` to write them from the rows again")
}

// rerenderStaleQuiet re-renders .lore/LORE.md and LORE.full.md (no
// pointer files, no output) after a sync when LORE.md's stamp no longer
// matches its rows: the sync brought rows in, or rows were added since the
// last render. Only
// for a project that renders, run from its root; a failure is a warning,
// never a failed sync.
func (h *Host) rerenderStaleQuiet(ctx context.Context, root string) {
	out := filepath.Join(root, filepath.FromSlash(LoreMDRel))
	if _, err := os.Stat(out); err != nil {
		return
	}
	cwd, err := os.Getwd()
	if err != nil || filepath.Clean(cwd) != filepath.Clean(root) {
		return
	}
	projectID, err := h.projectID(ctx)
	if err != nil {
		return
	}
	p, err := h.planCommitted(ctx, projectID, root, nil, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, h.decor.warn(syncLogPrefix+"check "+LoreMDRel+" against its rows: "+err.Error()))
		return
	}
	// Only a stale stamp re-renders: a file edited by hand (stamp current)
	// is left for the doctor to name, never overwritten behind a sync.
	if kind, err := copyVerdict(out, p.body, canaryIn); err != nil || kind != opsrender.CopyStale {
		return
	}
	rf := &renderFlags{outPath: LoreMDRel, noPointer: true, quiet: true}
	if err := h.runRender(ctx, rf); err != nil {
		fmt.Fprintln(os.Stderr, h.decor.warn(syncLogPrefix+"re-render "+LoreMDRel+": "+err.Error()))
	}
}
