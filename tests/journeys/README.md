# Real-user journeys (`tests/journeys`)

A journey is one person or team using lore over weeks and months, in order, the way people actually do: install, first notes, a second repo, a push that fails, a rename, a new laptop, an upgrade, a teammate who leaves. Unit tests ask "does this action work right now?"; a journey asks "what happens later, when things change?".

Every journey is committed Go test code (build tag `journeys`) that drives the real `lore` binary (built with `-tags loretest`, so the `LORE_NOW` clock works). After every step the shared invariants run on every machine of the World, so an invariant added once checks every journey at once.

Run: `task lore:test:journeys` (all), `task lore:test:journeys:<persona>` (one), or `go test -tags journeys -run '^TestJourneySolo' ./tests/journeys/` (builds the binary itself when `JOURNEYS_LORE` is unset). Each persona is its own step of `task check` (every other journey runs in the catch-all step `journey-rest`) and stays under about 3 minutes; split a longer one into `TestJourney<Name><Part>` tests (the gate step runs `-run '^TestJourney<Name>'`, so the parts stay in it). `JOURNEYS_STRICT=1` makes steps marked `Pending` fail.

## The harness

| File | Holds |
|------|-------|
| `world.go` | `World`: the fake GitHub (`w.Remote("github.com/org/repo")`, from `tests/teame2e/teamharness`), every persona and machine, one clock (`w.AdvanceDays(n)`, `w.Advance(d)`, `w.Day()`) |
| `persona.go` | `Persona`: name and email (`p.Rename`), machines (`p.Laptop()`, `p.NewLaptop("desk")`, `p.Machine(label)`) |
| `machine.go` | `Machine`: a HOME with a clean PATH; `m.Lore(dir, …)`, `m.Clone(remote, name)`, `m.LoreIn(clone, …)`, `m.Offline()`/`Online()`, `m.Sleep()`/`Wake()` (a laptop left in a drawer: skipped by invariants until woken), `m.SleepMidPush(dir, after, …)` (lore killed at its git push), `m.MoveClone`, `m.MoveFolder`; the teamharness Actor underneath (`SetEnv`, `WriteConfig`, `LorePath`, `Agent`, …) |
| `step.go` | `w.Step(name, fn)`, `w.Fact(name, check)`, `Pending(t, owner, what, fn)` |
| `invariants.go` | the shared invariants and `RegisterInvariant` |
| `frame_test.go` | the harness's own tests: every action once, every invariant bites, `Pending` records |
| `<persona>_test.go` | one persona's journey |

A lore run that takes over 5 minutes (`TEAMHARNESS_CMD_TIMEOUT`, a Go duration, changes it) is stopped with SIGQUIT and the step fails naming the command, its folder and the goroutine dump of that lore (`tests/teame2e/teamharness/deadline.go`), so a hang under load names the command instead of only the test's own goroutines.

## Add a persona

1. Create `tests/journeys/<persona>_test.go` with `//go:build journeys && !windows` and tests named `TestJourney<Name>…`.
2. Start a World and the people: `w := NewWorld(t)`, `dev := w.Persona("Dev One", "dev.one@example.test")`. Placeholders only, never a real name, email, URL or key.
3. Write the timeline as steps (below), including the undo paths a real person takes: delete then restore, purge then regret, edit then edit back, remove a setup then re-add it, leave the setup page halfway and come back the next day.
4. Nothing more is needed for the gate: a test no named part runs is run by the catch-all part `test:journeys:rest` (step `journey-rest`). A persona that needs its own step gets a `test:journeys:<persona>` task in `Taskfile.lore.yml`, a `journey-<persona>` line in `GATE_STEPS` and in the `FAST_MAP` rules that name the other journey parts, and its `-run` pattern in journey-rest's `-skip`. `gate_parts_test.go` (no build tag, so `go test ./...` runs it) fails when a test is in no part or in two, when a part is not a gate step, or when a `FAST_MAP` rule runs journey parts without the catch-all.

## Add a step

```go
w.Step("week1/offline-write", func() {
	m.Offline()
	r := m.LoreIn(repo, "memory", "add", "--body", "written on the train")
	r.OK(t).Says(t, "saved")
})
```

Name steps `when/what` (`day1/install`, `month3/new-laptop`); failures print `day N when/what [check on machine]`. Inside the step, assert what that person would look at: the answer reads right, the files on disk are right, every copy of what changed is current or visibly flagged. Work out the right answer first; never paste today's output in as the expectation without checking it is right.

Something the journey established that must stay true from then on is a Fact, checked now and after every later step:

```go
w.Fact("the train note reads the same on both laptops", func(c Ctx) { … c.Errorf("…") })
```

A step whose right behaviour waits on another branch's fix is written anyway, inside `Pending(t, "<branch>", "<what>", func(tb testing.TB) { … })`: it runs, logs `PENDING(<branch>)` with every failure, and fails for real under `JOURNEYS_STRICT=1`. Remove the wrapper when the fix lands.

**New features add their journey steps here.** A feature that changes what a person does over time (a new command, a new kind of store, a new sync mode, a new refusal) adds the steps that use it to the persona who would, in the same change.

## Add an invariant

An invariant must hold for any person at any point of any journey. Add it to `invariants.go`, or in its own `invariant_<name>.go`:

```go
func init() {
	RegisterInvariant(Invariant{Name: "no-temp-leftovers", Check: func(c Ctx) {
		// c.M is the machine, c.W the World; report with c.Errorf.
	}})
}
```

It runs after every step on every awake machine (machines are checked at the same time, so an invariant never calls `Fatal`). Then add a case to `TestFrameInvariantsBite` that breaks what it guards and shows it fails: a checker that never fails checks nothing.

Shared invariants now: `readable-output` (no crash, formatting debris or `<nil>` in anything lore printed; a failure always says something), `doctor` (in HOME and every clone: a report whose status, exit and counts agree, and every non-ok check or warning says what and why), `config` (`config check` knows every key in `config.yaml`), `modes` (nothing in `~/.lore` readable by others; committed files under a repo's `.lore/` readable by all).
