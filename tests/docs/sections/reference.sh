# reference: the outputs the reference pages quote (docs/reference/*.md).
# Alice (an agency developer) works in the client's website repo. She sees how one error looks and which exit status it gives, checks
# her ~/.lore/config.yaml with `lore config check` (first a good file, then one
# with mistakes), and sees that --db is refused (rows are read from the repo's files)
# while LORE_REPO / LORE_PROJECT_ID still pick the repo and project.

lore_repo alice client/website

note "an error"
on! alice work "lore rule list"

note "errors with json"
on! alice work "lore rule list --json"
on! alice work/website "lore rule list --colour never"

note "read only"
EXTRA_ENV="LORE_READ_ONLY=1" on! alice work/website "lore rule add --body 'Never put passwords in the code'"
on! alice work/website "lore rule add --read-only --body 'Never put passwords in the code'"

note "exit status"
on alice work/website "lore task add 'Write the release notes'"
first=$(id tsk)
on alice work/website "lore task add 'Send the release notes to the client' --after $first"
second=$(id tsk)
on alice work/website "lore task edit $first --after $second; echo \"exit status: \$?\""

note "config check good"
config alice <<'Y'
# Everything below is optional; this file sets most keys to show where they go.
identity:
  key: human:alice@agency.example
  name: Alice
personal:
  store: git@github.com:alice/lore-me
orgs:
  - name: agency
    identity:
      key: human:alice@agency.example
      name: Alice
      handle: alice
    stores:
      - url: github.com/agency/notes
        org_folder: "."
    own:
      - github.com/agency/*
    repos:
      - github.com/client/*
      - github.com/client2/**
    defaults:
      client_repos:
        task: agency
        memory: agency/website
    agent_details: internal
    lease:
      ttl: 30m
live:
  max_age: 7d
  fetch_ttl: 60s
  autopush: true
  compact_after: 1000
  remote: origin
  defaults:
    memories: true
lease:
  ttl: 20m
  renew_every: 5m
  grace: 2m
  push_retries: 5
agents:
  max_claims_per_agent: 1
  max_attempts: 3
merge_check:
  adapter: none
link_index:
  max_age: 1h
untrusted_hosts:
  - build-*.client.example
upgrade_notice: true
auto_upgrade: false
context:
  profiles:
    - name: mytool
      pointer_file: MYTOOL.md
      import: at
      budget: 20000
Y
on alice . "cat .lore/config.yaml"
on alice work/website "lore config check"

note "config check bad"
config alice <<'Y'
orgs:
  - name: Agency
    identity:
      key: alice@agency.example
    stores:
      - url: github.com/agency/notes
        branch: team/staging
lease:
  ttl: 5m
  renew_every: 10m
merge_check:
  adapter: gitlab
Y
on alice . "cat .lore/config.yaml"
on! alice work/website "lore config check"

note "config check typo"
config alice <<'Y'
live:
  max_age: 1d12h
Y
on! alice work/website "lore config check"

# A config lore cannot read blocks reads too (fail closed, CFG): lore cannot tell which
# stores a row may come from.
note "invalid config stops writes"
on! alice work/website "lore rule list"
on! alice work/website "lore rule add --body 'Run the tests before you push'"
quiet alice . "rm .lore/config.yaml"

note "db flag"
quiet alice work/website "lore rule add --body 'Run the tests before you push'"
on! alice . "lore rule list --db work/website/.lore/lore.db"
on! alice . "lore rule list"
on alice . "cd work/website && lore rule list"

note "db config"
on alice work/website "lore config list"
on alice work/website "lore config get default_branch || echo 'not set'"

# --- default_branch (docs/reference/config.md) -------------------------------------------
note "default branch"
quiet alice work/website "git push -q origin main:develop"
config alice <<'Y'
default_branch: develop
Y
on alice . "cat .lore/config.yaml"
on alice work/website "lore task add 'Fix the footer links'"
local t_footer; t_footer=$(id tsk)
on alice work/website "lore task done $t_footer"
on alice work/website "git fetch -q origin && git log --oneline origin/develop -2 && git log --oneline origin/main -1"
quiet alice . "rm .lore/config.yaml"

# --- one error from each group (docs/reference/errors.md) --------------------------------
note "no work"
lore_repo alice client/landing
on! alice work/landing "lore task next"

# --- the store marker, the org choice and --force-org (docs/reference/config.md, flags.md) ---
remote agency/notes
remote other/notes
note "store marker"
config alice <<'Y'
orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", org_folder: org}
    repos: [github.com/client/*]
  - name: freelance
    identity: {key: "human:alice@example.com", name: "Alice", handle: alice}
    stores:
      - {url: "git@github.com:other/notes", org_folder: org}
    repos: [github.com/client/*]
Y
on alice . "cat .lore/config.yaml"
# 0.1.18: a store is attached to the repo by name (`lore store init <name> <url>`).
on alice work/website "LORE_ORG=agency lore store init agency git@github.com:agency/notes --yes"
on alice work/website "LORE_ORG=freelance lore store init freelance git@github.com:other/notes --yes"
on alice work/website "cat .lore/stores.yaml"

note "org use"
on! alice work/website "lore whoami"
on alice work/website "lore org use agency"
on alice . "grep -A3 repo_orgs ~/.lore/config.yaml"
on alice work/website "lore org link agency --project client --yes"

note "force org"
on! alice work/website "LORE_ORG=freelance lore memory add --body 'The client pays invoices in 30 days'"
# --force-org is gone: name the store the note goes to with --to.
on! alice work/website "lore memory add --force-org --body 'The client pays invoices in 30 days'"
on alice work/website "lore memory add --to freelance --body 'The client pays invoices in 30 days'"
