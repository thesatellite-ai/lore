package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thesatellite-ai/lore/logic/origin"
	"github.com/thesatellite-ai/lore/model/hlc"
	"github.com/thesatellite-ai/lore/model/ids"
	"github.com/thesatellite-ai/lore/model/value"
	"github.com/thesatellite-ai/lore/schema"
	"github.com/thesatellite-ai/lore/store"
)

// fakeAWSKey is a made-up key in the shape the test scanner matches,
// built at run time so no key-shaped literal is committed.
func fakeAWSKey() string { return "AKIA" + strings.Repeat("Q7", 8) }

// placeProv is a Provenance whose branch and folder the test chooses.
type placeProv struct {
	fakeProv
	branch, dir string
}

func (p placeProv) Place(context.Context) origin.Facts {
	return origin.Facts{Repo: provRepo, Dir: p.dir, Git: origin.Git{State: origin.GitBranch, Branch: p.branch, Commit: provCommit}}
}

// TestOriginNeverRecordsASecretBranch is devco:R-10: a branch (or folder)
// name that matches a secret rule is never written into `_o`; the row
// says it was made on a branch, and keeps its commit, but not the name.
// An ordinary branch name is kept as before.
func TestOriginNeverRecordsASecretBranch(t *testing.T) {
	t.Parallel()
	key := fakeAWSKey()
	w := newProvWorld(t, placeProv{branch: "feat/" + key, dir: "tools/" + key + "/x"})
	prj := w.project(t, agency, "acme")
	m := w.memIn(t, agency, prj, "made on a secret branch")
	o := m.Origin.String()
	if strings.Contains(o, key) {
		t.Fatalf("_o holds the token: %s", o)
	}
	if !strings.Contains(o, `"branch":"`+origin.RedactedBranch+`"`) || !strings.Contains(o, `"commit":"`+provCommit+`"`) {
		t.Fatalf("_o lost the branch marker or the commit: %s", o)
	}
	if strings.Contains(o, `"dir"`) {
		t.Fatalf("_o kept a folder that holds a token: %s", o)
	}
	plain := newProvWorld(t, placeProv{branch: "feat/billing", dir: "services/billing"})
	pm := plain.memIn(t, agency, plain.project(t, agency, "acme"), "ordinary")
	if o := pm.Origin.String(); !strings.Contains(o, `"branch":"feat/billing"`) || !strings.Contains(o, `"dir":"services/billing"`) {
		t.Fatalf("an ordinary branch or folder was dropped: %s", o)
	}
}

// handWrite replaces row id's body in store n behind the engine's back
// (a teammate's hand-made row committed with the hooks off).
func (w *f15World) handWrite(t *testing.T, n store.Source, id ids.AnyID, body string) {
	t.Helper()
	ctx := context.Background()
	b := w.data[n].Open(store.OpenOptions{})
	raw, err := b.Get(ctx, schema.TableMemories, id)
	if err != nil {
		t.Fatal(err)
	}
	r, err := raw.Decode()
	if err != nil {
		t.Fatal(err)
	}
	r.Fields["body"] = txt(body)
	r.HLC = hlc.Next(r.HLC, time.Now(), testActor)
	sr, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Write(ctx, []store.Change{{Row: sr, Expect: raw.HLC}}); err != nil {
		t.Fatal(err)
	}
}

// TestReadMasksAStoredSecret is devco:R-14: a row whose stored text
// matches a secret rule (it reached the store without the write scan)
// reads with the match replaced in List and Get, the read names the row
// and its author (MaskedReads, for render to leave it out), a raw read
// (the secret checks) still sees the stored text, the row file is not
// changed, and an allow entry for the match makes it readable again.
func TestReadMasksAStoredSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := fakeAWSKey()
	w := newProvWorld(t, fakeProv{})
	w.eng.readScanner = w.eng.scanner
	prj := w.project(t, agency, "acme")
	m := w.memIn(t, agency, prj, "clean at write")
	w.handWrite(t, agency, m.ID, "deploy key "+key)

	want := "deploy key " + ReadRedacted + awsRule + "]"
	rows, err := w.eng.List(ctx, agency, schema.TableMemories)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: %v %d", err, len(rows))
	}
	if got := rows[0].Fields["body"].String(); got != want {
		t.Fatalf("list body = %q, want %q", got, want)
	}
	_, got, err := w.eng.Get(ctx, Ref{ID: m.ID})
	if err != nil || got.Fields["body"].String() != want {
		t.Fatalf("get body = %v (%v)", got.Fields["body"], err)
	}
	if !w.eng.WasMasked(m.ID) {
		t.Fatal("the masked row is not named")
	}
	ms := w.eng.MaskedReads()
	if len(ms) != 1 || ms[0].ID.String() != m.ID.String() || ms[0].Author != testActor || ms[0].Source != agency ||
		strings.Join(ms[0].Fields, ",") != "body" || strings.Join(ms[0].Rules, ",") != awsRule {
		t.Fatalf("masked reads = %+v", ms)
	}
	_, raw, err := w.eng.Get(WithRawRead(ctx), Ref{ID: m.ID})
	if err != nil || !strings.Contains(raw.Fields["body"].String(), key) {
		t.Fatalf("a raw read lost the stored text: %v %v", raw.Fields["body"], err)
	}
	sr, err := w.data[agency].Open(store.OpenOptions{}).Get(ctx, schema.TableMemories, m.ID)
	if err != nil || !strings.Contains(string(sr.Doc), key) {
		t.Fatal("the read changed the row file")
	}

	// An allow entry for exactly this match: the text reads as stored.
	f := w.eng.scanner.Scan("body", raw.Fields["body"].String())
	if len(f) != 1 {
		t.Fatalf("findings: %+v", f)
	}
	if _, err := w.eng.Create(ctx, agency, schema.TableSecretAllows, map[string]value.Value{
		"row_id": txt(m.ID.String()), "field": txt("body"), "rule_id": txt(awsRule),
		"match_hash": value.Int(int64(f[0].MatchHash)), "reason": txt("a test fixture"),
	}); err != nil {
		t.Fatal(err)
	}
	_, got, err = w.eng.Get(ctx, Ref{ID: m.ID})
	if err != nil || !strings.Contains(got.Fields["body"].String(), key) {
		t.Fatalf("an allowed match is still masked: %v %v", got.Fields["body"], err)
	}
}

// TestReadMasksASecretInOrigin is devco:R-10 for rows written before the
// branch scrub (or by hand): a token inside `_o` reads redacted.
func TestReadMasksASecretInOrigin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key := fakeAWSKey()
	w := newProvWorld(t, fakeProv{})
	w.eng.readScanner = w.eng.scanner
	m := w.memIn(t, agency, w.project(t, agency, "acme"), "plain")
	b := w.data[agency].Open(store.OpenOptions{})
	raw, err := b.Get(ctx, schema.TableMemories, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	r, err := raw.Decode()
	if err != nil {
		t.Fatal(err)
	}
	o, err := origin.Build(origin.Facts{Store: "agency", Git: origin.Git{State: origin.GitBranch, Branch: "feat/" + key, Commit: provCommit}})
	if err != nil {
		t.Fatal(err)
	}
	r.Origin = o
	r.HLC = hlc.Next(r.HLC, time.Now(), testActor)
	sr, err := store.FromRow(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Write(ctx, []store.Change{{Row: sr, Expect: raw.HLC}}); err != nil {
		t.Fatal(err)
	}
	_, got, err := w.eng.Get(ctx, Ref{ID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	if s := got.Origin; s.IsZero() || strings.Contains(s.String(), key) || !strings.Contains(s.String(), provCommit) {
		t.Fatalf("_o on read = %v", s)
	}
}
