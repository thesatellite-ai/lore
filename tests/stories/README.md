# User stories (`tests/stories`)

## Every scenario and every bug is a replayable regression

A story is one scenario lore was tried against, written down so it can run again: who the people are, what they do in plain words, and what must hold afterwards (`tests/stories/<id>-<name>.md`), plus a hermetic Go test that replays it end to end with the real binary. Every finding ever fixed has a row in `REGRESSIONS.md` naming the test that guards it, and a guard test fails when a row names a test that does not exist or a finding has no test. The stories run in the full gate (`task check` and `task check:after-land`, step `stories`), not in `task check:fast`.

Every new scenario, bug fix or feature lands with its story, its test and its REGRESSIONS.md row in the same commit, or it does not land.

## What a story checks

Besides its own steps, every story checks the owner's standing rules where its steps touch them (`rules.go`): nothing pushes without `lore sync`, `lore sync land`, `lore store push` or the person's own `git push` (`RefsOf`, `NoPushSince`); a repo without lore data stays byte-identical (`TreeOf`, `Untouched`); a project on the lore 0.1.17 format stays on it until a person runs `lore migrate run` (`NewFormatFiles`); no secret value is ever printed, not even its tail (`FakeSecret`, `NotPrinted`); every hint lore prints uses named flags (`NamedFlags`). The shared journey invariants (readable output, doctor, config, file modes) run after every step as well.

## The harness

A story is a [journeys](../journeys/README.md) World (`New(t, "<id>")`): the fake GitHub whose remotes are bare repositories on disk, one sandbox HOME per machine with a made-up git identity, the `LORE_NOW` clock, `Step`, `Fact` and the shared invariants. A story that walks the same ground as an existing journey links that journey from its page instead of repeating it.

| File | Holds |
|------|-------|
| `story.go` | `Story`, `New`, `Person`, `Seed` (fill a remote's first commit from a generator) |
| `rules.go` | the standing-rule checks |
| `fixtures.go` | the repo shape generators: `V017Repo` with `HubShape` (the 5.5k-row 0.1.17 repo) or `SmallShape`, `Monorepo`, `DocsOnly`, `Unicode` (Unicode names and text, CRLF), `PlainRepo` (no lore data), `Worktree`, `ShallowClone`; two machines are `Persona.NewLaptop` |
| `harness_test.go` | the harness's own tests: every rule check bites, the generators build their shapes, one short replay |
| `<id>-<name>.md` + `<id>_<name>_test.go` | one story |

Every fixture is generated: no file is copied from a real repo, and every name, email, URL and text is made up.

## Run

`task lore:test:stories` (all, in parallel), or `go test -tags 'stories journeys' -run '^TestStoryJ1' ./tests/stories/` (builds the binary itself when `STORIES_LORE` is unset).

## Add a story

1. Write `tests/stories/<id>-<name>.md`: who, the steps in plain words, what must hold (the standing rules it touches included), the journeys it links, and the findings it guards. One physical line per paragraph.
2. Write `tests/stories/<id>_<name>_test.go` (build tag `stories && !windows`) with `TestStory<Id>…` tests that start with `s := New(t, "<id>")`; keep each under about 3 minutes (split a long story into parts).
3. Add a `REGRESSIONS.md` row for every finding the story guards.
