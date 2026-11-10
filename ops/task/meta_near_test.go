package task_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/thesatellite-ai/lore/internal/opstest"
	opstask "github.com/thesatellite-ai/lore/ops/task"
)

// nearWarnings runs task add with metas and returns its meta warnings
// keyed by field, joined in order.
func nearWarnings(t *testing.T, h *opstest.H, title string, metas ...string) map[string]string {
	t.Helper()
	b, err := json.Marshal(struct {
		Title string   `json:"title"`
		Meta  []string `json:"meta"`
	}{title, metas})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Warnings []opstask.Warning `json:"warnings"`
	}
	if err = json.Unmarshal([]byte(h.JSON(h.Must(opstask.OpAdd, string(b)))), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, w := range out.Warnings {
		got[w.Field] += w.Message + "\n"
	}
	return got
}

// TestAddMetaNearDuplicateWarns (P2-META-7, METADATA 3.5, later 267): a
// write that registers a new key close to an existing one warns "did you
// mean <nearest>? … --merge-into <nearest>" naming up to three, nearest
// first; a new key close to none, a key that already exists, and the very
// first key get no such line; two new keys in one write each get their
// own.
func TestAddMetaNearDuplicateWarns(t *testing.T) {
	t.Parallel()
	h := opstest.New(t)
	near := "did you mean "
	cases := []struct {
		metas []string
		want  map[string]string // field -> "" (no near line) or the expected near line part
	}{
		{[]string{"client=acme"}, map[string]string{"client": ""}},
		{[]string{"clients=beta"}, map[string]string{"clients": "did you mean client? clients is a new key; fold it with lore meta edit clients --merge-into client"}},
		{[]string{"client=gamma"}, map[string]string{}},
		{[]string{"env=prod"}, map[string]string{"env": ""}},
		{[]string{"owner=x"}, map[string]string{"owner": ""}},
		{[]string{"envs=1", "owners=2"}, map[string]string{"envs": "did you mean env?", "owners": "did you mean owner?"}},
	}
	for i, c := range cases {
		got := nearWarnings(t, h, "near "+strconv.Itoa(i), c.metas...)
		for f, w := range c.want {
			msg, ok := got[f]
			if !ok || !strings.Contains(msg, "new keys in repo: "+f+" (text)") {
				t.Errorf("%v: %s has no new-key line: %q", c.metas, f, msg)
			}
			if w == "" && strings.Contains(msg, near) {
				t.Errorf("%v: %s warns %q, want no near-duplicate line", c.metas, f, msg)
			}
			if w != "" && !strings.Contains(msg, w) {
				t.Errorf("%v: %s warns %q, want %q", c.metas, f, msg, w)
			}
		}
		for f, msg := range got {
			if _, listed := c.want[f]; !listed && strings.Contains(msg, near) {
				t.Errorf("%v: unexpected near line on %s: %q", c.metas, f, msg)
			}
		}
	}
	// up to three names, nearest first
	for _, k := range []string{"host=a", "hosts=b", "hostx=c", "hostz=d"} {
		nearWarnings(t, h, "host "+k, k)
	}
	msg := nearWarnings(t, h, "host y", "hosty=e")["hosty"]
	i := strings.Index(msg, near)
	if i < 0 {
		t.Fatalf("hosty: %q", msg)
	}
	names := strings.Split(strings.SplitN(msg[i+len(near):], "?", 2)[0], ", ")
	if len(names) != 3 {
		t.Errorf("hosty names %v, want 3", names)
	}
}
