---
title: Task commitment & deferral — work
---

# Task commitment & deferral — TASKS

Work checklist for [`TASK_COMMITMENT_SPEC.md`](./TASK_COMMITMENT_SPEC.md). Source of truth for status; views on top.

<TaskList filter="is:open" sort="priority:asc"></TaskList>

## Done

<TaskList filter="is:done"></TaskList>

---

## Source — all tasks

- [ ] Add `TaskCommitment` enum + values to `dbent/schema/enums.go` !p0 (commitment) ^task-enum
- [ ] Add `commitment` enum field + `deferred_until` time field + `(project_id,commitment)` index to `dbent/schema/task.go` !p0 (commitment) ^task-schema after:task-enum
- [ ] Regenerate ent (`go generate` / dbent) and `go build ./...` green !p0 (commitment) ^task-codegen after:task-schema
- [ ] `task add`: `--commitment` + `--defer-until` flags; loud `E_INVALID_INPUT` when actor kind is agent and `--commitment` missing; schema default otherwise !p0 (commitment) ^task-add after:task-codegen
- [ ] `task edit`: `--commitment`, `--defer-until`, `--clear-defer` !p1 (commitment) ^task-edit after:task-codegen
- [ ] Shared `activeTaskPredicates(now)` helper; swap default `task list` filter to it !p0 (commitment) ^task-active after:task-codegen
- [ ] `task list` widening flags: `--commitment`, `--include-proposed`, `--include-someday`, `--include-deferred`, `--all` !p1 (commitment) ^task-list-flags after:task-active
- [ ] `task start` / `task done` auto-promote `commitment=accepted` + clear `deferred_until` !p0 (commitment) ^task-promote after:task-codegen
- [ ] New read-only views: `lore task triage` / `someday` / `deferred` !p1 (commitment) ^task-views after:task-active
- [ ] `task search` fetch: apply `ActiveTask` default + `--all` bypass !p1 (commitment) ^task-search after:task-active
- [ ] Directive block: add commitment decision rule for agents !p1 (commitment) ^task-directive after:task-add
- [ ] Update `skills/COMMANDS.md` + `skills/examples/06-task-tracking.md` + `README.md` !p2 (commitment) ^task-docs after:task-add
- [ ] `go build ./...` + `go test ./dbent/... ./lace/... ./saas/...` green !p0 (commitment) ^task-test after:task-promote
- [ ] DEFERRED: global cross-entity `lore search` commitment filter — out of v1 scope, not silently skipped !p3 (commitment) ^task-global-search
