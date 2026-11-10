//go:build machines && !windows

package machines

// lifecycle_test.go — a setup driven through every valid sequence of
// LifeLen lifecycle operations (create, edit, disable, enable, pin, trash,
// restore, purge, restart) on every machine shape, with the machine
// invariants (CheckMachine) and the setup's own state checked after every
// step. Each command is its own lore process, so config.yaml is re-read
// at every step; "restart" also re-reads it twice and wants the same answer.

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// LifeLen is the sequence length; every prefix is checked on the way, so
// the length-LifeLen set also covers every shorter sequence.
const LifeLen = 4

// envFull runs every sequence on every shape (default: the full set on
// the shapes listed in fullShapes, a spread subset on the rest).
const envFull = "MACHINES_FULL"

// fullShapes run every sequence by default: the shapes real bugs came from.
var fullShapes = map[string]bool{ShapeOwnerLike: true, ShapeGraveyard: true, ShapeMigrated: true}

// subsetStride keeps every n-th sequence on the other shapes (offset by
// shape, so together they still cover the whole list).
const subsetStride = 4

// Life states of the setup under test.
type life int

const (
	absent life = iota
	active
	disabled
	trashed
	purged
)

func (l life) String() string {
	return [...]string{"absent", "active", "disabled", "trashed", "purged"}[l]
}

// lifeName is the setup the sequences drive, with its folder.
const (
	lifeName   = "work"
	lifeFolder = "~/code/work/**"
)

// world is the model: what the setup should be after the steps so far.
type world struct {
	state life
	// wasDisabled is the state a restore brings back.
	wasDisabled bool
	pinned      bool
	edits       int
}

type lifeOp struct {
	name  string
	valid func(w world) bool
	// run runs the real command; w is the model AFTER the op (simulate).
	run func(t *testing.T, m *Machine, w world)
}

func ok(t *testing.T, r Result) {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("%s", r)
	}
}

var lifeOps = []lifeOp{
	{"create", func(w world) bool { return w.state == absent || w.state == purged }, func(t *testing.T, m *Machine, w world) {
		ok(t, m.Lore(t, "", "setup", "apply", lifeName, "--step", "kind", "--answers", `{"kind":"solo","name":"`+lifeName+`","default":false}`))
		ok(t, m.Lore(t, "", "setup", "apply", lifeName, "--step", "identity", "--answers", `{"name":"`+PersonName+`","email":"`+lifeName+`@example.test"}`))
		ok(t, m.Lore(t, "", "setup", "apply", lifeName, "--step", "folders", "--answers", `{"folders":["`+lifeFolder+`"],"default":false}`))
	}},
	{"edit", func(w world) bool { return w.state == active || w.state == disabled }, func(t *testing.T, m *Machine, w world) {
		ok(t, m.Lore(t, "", "setup", "edit", lifeName, "--note", fmt.Sprintf("edit %d", w.edits), "--folder", lifeFolder))
	}},
	{"disable", func(w world) bool { return w.state == active }, func(t *testing.T, m *Machine, w world) {
		ok(t, m.Lore(t, "", "setup", "disable", lifeName))
	}},
	{"enable", func(w world) bool { return w.state == disabled }, func(t *testing.T, m *Machine, w world) {
		ok(t, m.Lore(t, "", "setup", "enable", lifeName))
	}},
	{"pin", func(w world) bool { return w.state == active && !w.pinned }, func(t *testing.T, m *Machine, w world) {
		ok(t, m.Lore(t, m.Repos[RepoWorkApp], "setup", "use", lifeName, "--here"))
	}},
	{"trash", func(w world) bool { return w.state == active || w.state == disabled }, func(t *testing.T, m *Machine, w world) {
		ok(t, m.Lore(t, "", "setup", "remove", lifeName))
	}},
	{"restore", func(w world) bool { return w.state == trashed }, func(t *testing.T, m *Machine, w world) {
		ok(t, m.Lore(t, "", "setup", "trash", "restore", lifeName))
	}},
	{"purge", func(w world) bool { return w.state == trashed }, func(t *testing.T, m *Machine, w world) {
		ok(t, m.Lore(t, "", "setup", "trash", "purge", lifeName, "--confirm"))
	}},
	{"restart", func(w world) bool { return w.state != absent }, func(t *testing.T, m *Machine, w world) {
		a, b := m.Lore(t, "", "setup", "list", "--json"), m.Lore(t, "", "setup", "list", "--json")
		if a.Stdout != b.Stdout {
			t.Errorf("two fresh processes disagree:\n%s\n%s", a.Stdout, b.Stdout)
		}
	}},
}

// sequences lists every valid sequence of n ops from an absent setup.
func sequences(n int) [][]lifeOp {
	var out [][]lifeOp
	var walk func(w world, seq []lifeOp)
	walk = func(w world, seq []lifeOp) {
		if len(seq) == n {
			out = append(out, append([]lifeOp(nil), seq...))
			return
		}
		for _, op := range lifeOps {
			if !op.valid(w) {
				continue
			}
			walk(simulate(op.name, w), append(seq, op))
		}
	}
	walk(world{}, nil)
	return out
}

// simulate is the model's transition: what op does to the setup.
func simulate(name string, w world) world {
	switch name {
	case "create":
		return world{state: active}
	case "edit":
		w.edits++
	case "disable":
		w.state = disabled
	case "enable":
		w.state = active
	case "pin":
		w.pinned = true
	case "trash":
		w.wasDisabled = w.state == disabled
		w.state = trashed
	case "restore":
		w.state = active
		if w.wasDisabled {
			w.state = disabled
		}
	case "purge":
		return world{state: purged}
	}
	return w
}

func seqName(seq []lifeOp) string {
	var names []string
	for _, o := range seq {
		names = append(names, o.name)
	}
	return strings.Join(names, "-")
}

func TestLifecycleSequences(t *testing.T) {
	seqs := sequences(LifeLen)
	full := os.Getenv(envFull) != ""
	ran := 0
	for si, shape := range AllShapes() {
		for i, seq := range seqs {
			if !full && !fullShapes[shape] && (i+si)%subsetStride != 0 {
				continue
			}
			ran++
			t.Run(shape+"/"+seqName(seq), func(t *testing.T) {
				t.Parallel()
				runSequence(t, shape, seq)
			})
		}
	}
	t.Logf("%d sequences of %d steps, %d runs over %d shapes", len(seqs), LifeLen, ran, len(AllShapes()))
}

func runSequence(t *testing.T, shape string, seq []lifeOp) {
	m := Build(t, shape, loreBin)
	var w world
	var done []string
	for _, op := range seq {
		w = simulate(op.name, w)
		op.run(t, m, w)
		done = append(done, op.name)
		step := shape + "/" + strings.Join(done, "-")
		gone := goneAtStart(shape)
		switch w.state {
		case purged:
			gone.Purged = append(gone.Purged, lifeName)
		case trashed:
			gone.Trashed = append(gone.Trashed, lifeName)
		}
		m.CheckMachine(t, step, gone)
		checkLife(t, m, step, w)
		if t.Failed() {
			return
		}
	}
}

// checkLife: the setup is listed in its state, and applies to its folder
// only while active (a disabled, trashed or purged setup never applies,
// pinned or not).
func checkLife(t *testing.T, m *Machine, step string, w world) {
	t.Helper()
	l := m.List(t, "")
	listed, isDisabled := false, false
	for _, s := range l.Setups {
		if s.Name == lifeName {
			listed, isDisabled = true, s.Disabled
		}
	}
	wantListed := w.state == active || w.state == disabled
	if listed != wantListed || isDisabled != (w.state == disabled) {
		t.Errorf("[%s] setup %s: listed=%v disabled=%v, model says %s", step, lifeName, listed, isDisabled, w.state)
	}
	here := m.List(t, m.Repos[RepoWorkApp]).Active
	if applies := here.Name == lifeName; applies != (w.state == active) {
		t.Errorf("[%s] in %s the active setup is %q (%s), model says %s", step, RepoWorkApp, here.Name, here.Why, w.state)
	}
}

// TestSequenceCount pins the enumeration so a new op or rule is noticed.
func TestSequenceCount(t *testing.T) {
	if n := len(sequences(LifeLen)); n < 30 {
		t.Fatalf("only %d sequences of %d steps", n, LifeLen)
	}
}
