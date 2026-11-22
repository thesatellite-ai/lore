# WL — an agent renews its own lapsed claim lease (issue #121)

**Who.** Sol, alone on a laptop with one code repo `app` (made-up names) set up with lore and pushed; two coding agents in that checkout, `agent:test/worker-1` and `agent:test/worker-2` (LORE_ACTOR).

**Why.** devco:D-896 (public issue #121): after worker-1's own claim lease ran out with nobody else claiming the task, `task claim --push` said "you hold it already", exited 0 and renewed nothing, while `task heartbeat` and `task done` failed `E_LOST_CLAIM` naming worker-1 itself as the holder, with a hint to stop work; only release and a new claim got it going again.

**Test.** `TestStoryWLOwnLapsedLease` (tests/stories/WL_ownlease_test.go). The clock moves 21 minutes (past the 20m lease, inside the 2m grace) between commands, the shape of the issue's script.

## Steps

1. D-896/claim-renews-my-lapsed-lease: worker-1 claims a task, its lease runs out; `task claim --push` says "renewed your claim on <id> … nobody took the task" (never "you hold it already"); after another lapse `task heartbeat` renews; after another `task done` lands.
2. D-896/heartbeat-and-done-accept-my-lapsed-lease: on a lapsed lease `task heartbeat` renews and `task done` lands, no `E_LOST_CLAIM`.
3. D-896/next-claim-renews-the-lapsed-lease-it-resumes: `task next --claim --push` resumes the lapsed task, renews it and prints the release line; done then lands.
4. D-896/reclaimed-by-another-agent-stays-fenced: past the grace worker-2 reclaims (attempt 2); worker-1's heartbeat and done fail `E_LOST_CLAIM` naming worker-2 with the stop-work hint, its claim is refused as held by worker-2, and worker-2 finishes the task.
