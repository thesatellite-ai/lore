# reference: the outputs the reference pages quote (docs/reference/*.md).
# Alice (an agency developer) works in the client's website repo. She lists the
# error codes, sees how one error looks and which exit status it gives, checks
# her ~/.lore/config.yaml with `lore config check` (first a good file, then one
# with mistakes), and points one command at another database with --db / LORE_DB.

lore_repo alice client/website

note "errors list"
on alice work/website "lore errors list | head -4"
on alice work/website "lore errors list --json | head -9"

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
  store: local
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

note "invalid config stops writes"
on alice work/website "lore rule list"
on! alice work/website "lore rule add --body 'Run the tests before you push'"
quiet alice . "rm .lore/config.yaml"

note "db flag"
quiet alice work/website "lore rule add --body 'Run the tests before you push'"
on alice . "lore rule list --db work/website/.lore/lore.db"
EXTRA_ENV="LORE_DB=work/website/.lore/lore.db" on alice . "lore rule list"

note "db config"
on alice work/website "lore config list"
on alice work/website "lore config get schema_version"
