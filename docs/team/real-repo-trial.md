# Real-repo trial

**In short:** `task lore:trial:github` plays an agency and its client on two real repos on a real git host, step by step, and checks after every step that nothing internal reached the client's clone. It is a manual check for maintainers: it never runs in CI or in any automatic gate.

The examples on the other pages run against a fake GitHub made of plain repos on disk (see [How these examples were made](README.md#how-these-examples-were-made)). This trial runs the same kind of story against a real host instead, with real ssh, real network delays, real ref advertisements and real branch rules, which is where sync, leak checks, claims and landing can behave differently.

## What it checks

Two agency developers (`dev1`, `dev2`), two agents (`agent-a`, `agent-b`) and the client (`client`) each get their own home folder and their own clone of the client's repo. The agency keeps its internal notes in a second repo, the store. Every internal row the trial writes starts with `INTERNAL-MARKER-<n>`.

| Step | What happens |
|---|---|
| 00 wipe both remotes | the safety guard (below), then every branch, tag and `refs/lore/*` ref of both repos is deleted and one fresh first commit is pushed |
| 01 setup | homes, git identities, the agency config (orgs `agency` and `agency-stag`), one clone per person |
| 02 init, store, org link | `lore init` in the client repo, `lore store init`, `lore org link` (refused without `--yes` when there is no terminal), `lore actor register` |
| 03 internal notes, live tasks | internal notes and an internal task go to the store; a repo task reaches `dev2` on another branch at once; the client sees only the repo task |
| 04 landing, merge-only main | a repo task done with `--commit`, an internal task done in the store, both feature branches merged into `main` with `git merge`; every checkout stays clean |
| 05 protected main (local sim) | ride-along landing when `main` refuses direct pushes (a local repo whose hook answers like GitHub's GH006, since protecting a real branch needs admin API access) |
| 06 leak attempts | a commit with internal text (with and without the marker), `git commit --no-verify` followed by `git push`, `lore check pr-body`, `lore task add --to repo` and `lore rule add --to repo` with internal text: every one is refused, and the branch never reaches the client's remote |
| 07 share one note | `lore share --to repo` refuses without a terminal or `--yes`, then shares one note on purpose; the client sees exactly that note |
| 08a agents: plan, claims, merge | `lore agent start`, `lore mission apply` (twice: created, then found), claims, `E_NO_WORK`, a finding, a note, done → merged after a real merge; `agent-b` is killed while it holds a claim |
| 08b agents: reclaim, gate, done | after the lease expires, `agent-a` reclaims the task on `agent-b`'s pushed branch, `agent-b` gets `E_LOST_CLAIM`, an agent cannot approve the gate, `dev1` approves, the mission ends with 3 done |
| 09 leak cleanup, compaction | an internal task written with `--to repo` by mistake, seen by a second client clone, then `lore live purge` and `lore live compact`; a fresh mirror of the client repo holds no marker in any commit |
| 10 staging store branch | `lore store init <name> '<url>?branch=staging'`, `lore org use agency-stag`, and rows that never mix between the two branches |

After every step from 01 on, the client's own clone fetches every ref the client repo serves and is searched for `INTERNAL-MARKER-`: the checkout, the tree of every ref (including `refs/lore/*`), `git log --all -p`, every object in its object store (unreachable ones too) and the client's own `lore task list`, `lore memory list` and `lore rule list`. Step 09's mistake is fetched only by the second clone, as in [Cleaning up after a leak](use-cases/leak-cleanup.md); the main client clone fetches again only after the cleanup.

The whole run takes about six minutes, two and a half of them waiting for the killed agent's lease to expire.

## Create the two repos

Create two new, empty, private repos on your git host, for example `you/lore-trial-client` and `you/lore-trial-store`: no README, no license, no `.gitignore`. Your ssh key needs push access to both. Use them for nothing else: the trial deletes every ref in them on each run.

## Run it

```bash
LORE_TRIAL_CLIENT_REMOTE=git@github.com:you/lore-trial-client.git \
LORE_TRIAL_STORE_REMOTE=git@github.com:you/lore-trial-store.git \
task lore:trial:github
```

| Variable | Effect |
|---|---|
| `LORE_TRIAL_CLIENT_REMOTE` | the client's repo (required; there is no default) |
| `LORE_TRIAL_STORE_REMOTE` | the agency's store repo (required; there is no default) |
| `LORE_TRIAL_DIR` | where homes, clones and logs go (default: a new `mktemp -d`); must be empty or absent |
| `LORE_BIN` | a lore binary to drive instead of building one from this checkout |
| `LORE_TRIAL_ADOPT` | one or more remote URLs (space-separated) that may be wiped once although they carry no marker; see below |

The task builds lore from the checkout (`go build ./saas/cmd/cli`), puts it first on `PATH` so the git hooks lore installs call it, and leaves the work directory in place so you can read the logs: `logs/trial.log` (every command and its output, plus each check), `logs/clientcheck.log` (each client scan) and `logs/heartbeat-agent-b.log`. Every git call the trial makes itself uses `GIT_SSH_COMMAND="ssh -o ConnectTimeout=20"` and up to five tries.

Without both variables it refuses before doing anything:

```console
$ task lore:trial:github
refused: set LORE_TRIAL_CLIENT_REMOTE and LORE_TRIAL_STORE_REMOTE to two private repos you created for this trial, e.g.
  LORE_TRIAL_CLIENT_REMOTE=git@github.com:you/lore-trial-client.git
  LORE_TRIAL_STORE_REMOTE=git@github.com:you/lore-trial-store.git
The trial deletes every ref of both, so never point them at a real repo. See docs/team/real-repo-trial.md.
```

## The safety marker

Before it builds, clones or pushes anything, the trial checks both remotes. It wipes a remote only when:

- it has no refs at all (a new, empty repo), or
- its default branch is `main` and the first commit of `main` holds a file `README.lore-trial.md` with the trial's exact text (a `README` name, because a lore store refuses other files at its top level). The wipe writes that file into the fresh first commit, so every later run accepts the repo.

Any other repo is refused with exit status 2 and is not touched, so a real repo given by mistake can never be wiped:

```console
refused: the client remote git@github.com:you/website.git has commits, and the first commit of main has no README.lore-trial.md file with the trial's text.
  The trial deletes every ref of both remotes, so it runs only on an empty repo or on one an earlier run created.
  Nothing was changed. If this really is a scratch repo for the trial, empty it, or set LORE_TRIAL_ADOPT=git@github.com:you/website.git for one run.
```

`LORE_TRIAL_ADOPT` is the one-time way in for a scratch repo that already has commits from before the marker existed: it must name the exact URL. After that run the repo carries the marker and the variable is no longer needed. The wipe is the only place the trial force-pushes: it replaces `main` with the fresh commit and deletes every other branch, tag and `refs/lore/*` ref.

## Read the table

The run ends with one row per step on stdout:

```text
step                               checks      leak scan  result
00 wipe both remotes                   2/2     -          PASS
01 setup                               9/9     clean      PASS
...
PASS: every check passed and no internal marker reached the client clone (/tmp/tmp.Xb2…)
```

- **checks** counts the checks of the step that passed out of all of them: the exit code of each command (0, or the refusal it must give, such as 19 for `E_LEAK_BLOCKED`), and what its output must or must not say. Each failed check is also printed on stderr with the last lines of its output; `logs/trial.log` has the whole transcript.
- **leak scan** is `clean` when the client clone held no `INTERNAL-MARKER-` anywhere after that step, `LEAK` when it did, and `-` for the wipe (nothing to scan yet).
- **result** is `FAIL` when any check failed or the scan found a marker.

The task exits 1 when any step failed or any marker reached the client clone, 2 when it refused to start, and 0 otherwise.

## When to run it

Run it by hand before a release, and after any change to sync and live refs, the leak checks, claims and leases, or landing. It is not part of `task check` or of any workflow, and must never be: it needs push access to two real repos and deletes everything in them.
