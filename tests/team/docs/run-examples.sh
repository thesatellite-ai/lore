#!/usr/bin/env bash
# run-examples.sh: run every command example shown in docs/team/** against the
# real lore binary, in throwaway worlds (fake GitHub, one HOME per person), and
# write what each command printed to tests/team/docs/transcripts/<section>.txt.
#
# The docs paste their examples from these transcripts, so every example is a
# real run. The script exits 1 when any command that should succeed fails, or
# any command that should fail (the error examples) succeeds.
#
#   tests/team/docs/run-examples.sh                 # build lore, run every section
#   tests/team/docs/run-examples.sh agents leaks    # only these sections
#   LORE_BIN=/path/to/lore tests/team/docs/run-examples.sh   # use a built binary
#
# The binary is built with -tags loretest so LORE_NOW (a fixed clock) works:
# the agents page shows a lease expiring without waiting twenty minutes. That
# tag changes nothing else in lore's behaviour or output.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../../.." && pwd)"
OUT="$HERE/transcripts"
SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/lore-docs-examples.XXXXXX")"
[ -n "${KEEP:-}" ] && echo "keeping worlds in $SCRATCH" >&2 || trap 'rm -rf "$SCRATCH"' EXIT

if [ -z "${LORE_BIN:-}" ]; then
  LORE_BIN="$SCRATCH/bin/lore"
  (cd "$REPO" && CGO_ENABLED=0 go build -tags loretest -o "$LORE_BIN" ./saas/cmd/cli)
fi
export LORE_BIN

# shellcheck source=lib.sh
source "$HERE/lib.sh"

# section NAME: start a transcript and a fresh world for one docs section.
section() {
  # Normalize the previous section's transcript (never the default /dev/stdout: that is the caller's terminal or log).
  [[ "${TRANSCRIPT:-}" == "$OUT"/* ]] && [ -f "$TRANSCRIPT" ] && python3 "$HERE/normalize.py" "$TRANSCRIPT"
  mkdir -p "$OUT"
  TRANSCRIPT="$OUT/$1.txt"
  : > "$TRANSCRIPT"
  new_world "$SCRATCH/world-$1"
  # Every section runs on the test clock (LORE_NOW and git dates), so times, ages and
  # commit dates are the same on every run; normalize.py then fixes the random ids.
  CLOCK=2026-11-02T10:00:00Z STEP=5
  echo "== $1" >&2
}

# note TEXT: a heading inside a transcript, so a docs page can find its part.
note() { printf '\n### %s\n' "$*" >> "$TRANSCRIPT"; }

# config NAME: write ~/.lore/config.yaml for NAME from stdin.
config() {
  mkdir -p "$HOMES/$1/.lore"
  cat > "$HOMES/$1/.lore/config.yaml"
}

# client_repo SLUG DIR [PERSON]: the client (default carol) creates SLUG with an initial commit and lore set up.
client_repo() {
  local slug="$1" dir="$2" who="${3:-carol}"
  remote "$slug"
  [ -d "$HOMES/$who" ] || person "$who" "$who@${slug%%/*}.example"
  quiet "$who" work "git clone -q https://github.com/$slug $dir && cd $dir && echo '# $dir' > README.md && git add . && git commit -qm init && lore init --non-interactive --name=$dir >/dev/null 2>&1 && git add -A && git commit -qm 'lore: init' && git push -q origin main"
}

# agency_config NAME KEY DISPLAY HANDLE: the agency's org entry (one monorepo store).
agency_config() {
  config "$1" <<Y
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "$2", name: "$3", handle: $4}
    stores:
      - url: git@github.com:agency/notes
        org_folder: org
    own:   [github.com/agency/*]
    repos: [github.com/client/*, github.com/client2/*, github.com/client3/*]
    defaults:
      client_repos: {task: agency, memory: agency, rule: repo, decision: repo}
Y
}

# ---------------------------------------------------------------------------
# getting-started: one person, one repo, then a first org.
s_getting_started() {
  section getting-started
  remote alice/notes-app
  person alice alice@example.com
  quiet alice work "git clone -q https://github.com/alice/notes-app && cd notes-app && echo '# notes' > README.md && git add . && git commit -qm init && git push -q origin main"

  note "version"
  on alice work/notes-app "lore --version"

  note "init"
  on alice work/notes-app "lore init --non-interactive --name=notes-app"
  on alice work/notes-app "git add -A && git commit -qm 'lore: init' && git push -q origin main"

  note "whoami without config"
  on alice work/notes-app "lore whoami"

  note "first live task"
  on alice work/notes-app "lore task add 'fix login timeout'"
  local t; t=$(id tsk)
  on alice work/notes-app "lore task list"
  on alice work/notes-app "git status --short"
  on alice work/notes-app "git ls-remote origin"
  on alice work/notes-app "lore live show"

  note "first landing"
  on alice work/notes-app "lore task done $t"
  on alice work/notes-app "git fetch -q origin && git log --oneline origin/main"
  on alice work/notes-app "lore live show"
  on alice work/notes-app "lore sync"
  on alice work/notes-app "lore live show"

  note "toml migration"
  quiet alice . "mkdir -p .lore && printf 'stable_key = \"human:alice@example.com\"\ndisplay_name = \"Alice Doe\"\n' > .lore/identity.toml"
  on alice work/notes-app "lore whoami"
  on alice . "ls ~/.lore; cat ~/.lore/identity.yaml"

  note "first config"
  config alice <<'Y'
identity:
  key: human:alice@example.com
  name: Alice Doe
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - url: git@github.com:agency/notes
        org_folder: org
    own:   [github.com/agency/*]
    repos: [github.com/client/*]
Y
  on alice . "cat ~/.lore/config.yaml"
  on alice . "lore config check"
  remote agency/notes
  on alice . "lore store init git@github.com:agency/notes"
  remote agency/tools
  quiet alice work "git clone -q https://github.com/agency/tools && cd tools && echo '# tools' > README.md && git add . && git commit -qm init && git push -q origin main"
  on alice work/tools "lore init --non-interactive --name=tools"
  TTY=1 on alice work/tools "lore whoami"
  on alice work/tools "lore actor register"
  on alice work/notes-app "lore whoami"
}

# ---------------------------------------------------------------------------
# solo: one person, a repo with no remote, then the old behaviour on purpose.
s_solo() {
  section solo
  person alice alice@example.com
  quiet alice work "mkdir -p scratch && cd scratch && git init -q && git commit -q --allow-empty -m init"

  note "no remote"
  on alice work/scratch "lore init --non-interactive --name=scratch >/dev/null 2>&1 && lore rule add --body 'use pnpm, never npm'"
  on alice work/scratch "lore task add 'write the README'"
  local t; t=$(id tsk)
  on alice work/scratch "lore task done $t"
  on alice work/scratch "lore land --all --dry-run"
  on alice work/scratch "git status --short"

  note "tasks with the code"
  config alice <<'Y'
live:
  defaults: {tasks: false}
Y
  on alice work/scratch "lore task add 'tidy the scripts folder'"
  on alice work/scratch "git status --short"
}

# ---------------------------------------------------------------------------
# small-team: three developers on branches in one repo, no org config at all.
s_small_team() {
  section small-team
  remote agency/billing
  person alice alice@agency.example; person bob bob@agency.example
  quiet alice work "git clone -q https://github.com/agency/billing && cd billing && echo '# billing' > README.md && git add . && git commit -qm init && lore init --non-interactive --name=billing >/dev/null 2>&1 && git add -A && git commit -qm 'lore: init' && git push -q origin main"
  quiet bob work "git clone -q https://github.com/agency/billing"

  note "register"
  on alice work/billing "lore actor register"
  on bob work/billing "lore actor register"

  note "assign across branches"
  on bob work/billing "git switch -q -c feat/payments && lore task add 'check the checkout page on a phone' --assign @alice"
  local t1; t1=$(id tsk)
  on alice work/billing "git switch -q -c feat/login && lore task list --mine"
  on alice work/billing "lore sync && lore task list --mine"
  on alice work/billing "lore inbox"

  note "done lands on main"
  on alice work/billing "lore task start $t1"
  on alice work/billing "echo ok > checkout.txt && git add checkout.txt && git commit -qm 'fix the checkout page on phones' && lore task done $t1 --commit \$(git rev-parse --short HEAD)"
  on alice work/billing "git fetch -q origin && git log --oneline origin/main && git show --stat --format=%s origin/main | tail -3"
  on alice work/billing "git log --oneline -1 && git status --short"

  note "urgent rule"
  on bob work/billing "lore rule add --body 'Never deploy on a Friday' --live"
  on bob work/billing "git fetch -q origin && git log --oneline -1 origin/main"

  note "explain"
  on alice work/billing "lore explain $t1"

  note "protected main"
  protect agency/billing
  on bob work/billing "lore task add 'ask the client for the new logo' --assign @alice"
  local t2; t2=$(id tsk)
  on alice work/billing "lore sync >/dev/null && lore task done $t2"
  on alice work/billing "lore explain $t2"
  on alice work/billing "echo 'login ok' > login.txt && git add login.txt && git commit -m 'fix login timeout'"
  on alice work/billing "git log --oneline -3"
  on alice work/billing "git push -q -u origin feat/login"
  on alice work/billing "git status --short"
}

# ---------------------------------------------------------------------------
# agency: the agency works in the client repo client/website and keeps internal notes
# in its store agency/notes; the client's own developer (carol) uses lore too.
s_agency() {
  section agency
  person alice alice@agency.example; person bob bob@agency.example; person carol carol@client.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  agency_config bob "human:bob@agency.example" "Bob Roe" bob
  client_repo client/website app
  remote agency/notes

  note "store init"
  on alice . "lore store init git@github.com:agency/notes"
  quiet alice work "git clone -q https://github.com/client/website website"
  quiet bob work "git clone -q https://github.com/client/website website"

  note "link"
  TTY=1 on alice work/website "lore whoami"
  TTY=1 ANSWER=y on alice work/website "lore org link agency --project client"
  on alice work/website "git status --short"
  on alice work/website "lore actor register"
  on bob work/website "lore actor register"
  TTY=1 on alice work/website "lore whoami"
  on alice work/website "lore whoami"

  note "internal by default"
  on alice work/website "lore memory add --body 'they approve estimates faster when we send them on WhatsApp first'"
  local m1; m1=$(id mem)
  on alice work/website "lore rule add --body 'run the tests before you push' --to repo"
  on alice work/website "git status --short"
  on alice work/website "git add -A && git commit -qm 'lore: rule' && git push -q origin main"

  note "internal task"
  on alice work/website "git switch -q -c feat/checkout && lore task add 'send invoice reminder before demo' --to agency --assign @bob"
  local t1; t1=$(id tsk)
  on bob work/website "lore sync >/dev/null; git switch -q -c feat/login && lore task list"
  on bob work/website "lore inbox"
  on bob work/website "lore task done $t1"
  on bob work/website "git status --short"

  note "render"
  on alice work/website "lore render"
  on alice work/website "cat CLAUDE.local.md; git check-ignore -v CLAUDE.local.md"
  on alice work/website "head -12 ~/.lore/render/*.md"

  note "leak blocked"
  on alice work/website "mkdir -p src && echo '// they approve estimates faster when we send them on WhatsApp' > src/notes.ts && git add src/notes.ts"
  on! alice work/website "git commit -m 'add notes'"
  on alice work/website "git reset -q src/notes.ts && rm -rf src"
  on alice work/website "printf 'Fixes checkout.\n\nThey approve estimates faster when we send them on WhatsApp first.\n' > pr.md"
  on! alice work/website "lore check pr-body pr.md"
  on alice work/website "printf 'Fixes checkout totals for INR.\n' | lore check pr-body -"
  on alice work/website "rm pr.md && lore check leaks --ci"

  note "agent rule refused"
  on alice work/website "lore agent start --label agent-backend --owner @alice"
  local actor session
  actor=$(grep -o 'LORE_ACTOR=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2); session=$(grep -o 'LORE_SESSION=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2)
  EXTRA_ENV="LORE_ACTOR=$actor LORE_SESSION=$session" on! alice work/website "lore rule add --body 'require prepayment from this client'"

  note "share"
  on alice work/website "lore memory add --body 'checkout must support INR and USD'"
  local m2; m2=$(id mem)
  TTY=1 ANSWER=y on alice work/website "lore share $m2 --to repo"
  on alice work/website "git status --short"

  note "client view"
  on carol work/app "git pull -q && lore memory list"
  on carol work/app "lore rule list"
  on carol work/app "lore task list"
  on carol work/app "lore whoami"
  on carol work/app "git fetch -q origin '+refs/*:refs/remotes/all/*' && git grep -i -c whatsapp \$(git for-each-ref --format='%(refname)' refs/remotes/all) -- . || echo 'no match in any ref'"
}

# ---------------------------------------------------------------------------
# group: three client 2 repos act as one product through one internal project.
s_group() {
  section group
  person alice alice@agency.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  remote agency/notes
  quiet alice . "lore store init git@github.com:agency/notes"
  for r in api app infra; do client_repo "client2/$r" "$r" client2dev; quiet alice work "git clone -q https://github.com/client2/$r"; done

  note "link three repos"
  on alice work/api "lore org link agency --project client2 --yes"
  on alice work/app "lore org link agency --project client2 --yes"
  on alice work/infra "lore org link agency --project client2 --yes"
  on alice work/api "lore actor register"

  note "mission across repos"
  on alice work/api "lore mission add 'Invite landing page' --to agency/client2 --key invite"
  local mis; mis=$(id msn)
  on alice work/api "lore task add 'send the invite email from the API' --to agency/client2 --mission $mis --repo api --key invite/backend"
  local tb; tb=$(id tsk)
  on alice work/api "lore task add 'build the invite page' --to agency/client2 --mission $mis --repo app --after $tb --key invite/frontend"
  on alice work/api "lore task add 'send the invite email from the API' --to agency/client2 --mission $mis --repo api --key invite/backend"
  on alice work/app "lore task list"
  on alice work/app "lore task list --all-repos"
  on alice work/infra "lore mission status $mis"
}

# ---------------------------------------------------------------------------
# staging: the same store, a second branch, chosen only on purpose.
s_staging() {
  section staging
  person alice alice@agency.example
  config alice <<'Y'
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", org_folder: org}
    repos: [github.com/client/*]
  - name: agency-stag
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", branch: staging, org_folder: org}
Y
  remote agency/notes
  client_repo client/website app
  quiet alice . "lore store init git@github.com:agency/notes"
  quiet alice work "git clone -q https://github.com/client/website website"
  quiet alice work/website "lore org link agency --project client --yes"

  note "staging branch"
  on alice work/website "lore store init git@github.com:agency/notes --branch staging"
  on alice work/website "lore org use agency-stag"
  on alice work/website "lore whoami"
  on alice work/website "lore org link agency-stag --project client --yes"
  on alice work/website "lore task add 'seed staging with demo data' --to agency-stag"
  on alice work/website "lore task list"
  on alice work/website "lore org use agency"
  on alice work/website "lore task list"
  on alice work/website "LORE_ORG=agency-stag lore task list"
  on alice work/website "git ls-remote https://github.com/agency/notes"
}

# ---------------------------------------------------------------------------
# contractor: one store per client; the contractor lists only the company store and the client's.
s_contractor() {
  section contractor
  person alice alice@agency.example; person bob bob@agency.example
  remote agency/notes; remote agency/notes-client; remote agency/notes-client2
  client_repo client/website app
  config alice <<'Y'
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", org_folder: "."}
      - {url: "git@github.com:agency/notes-client"}
      - {url: "git@github.com:agency/notes-client2"}
    repos: [github.com/client/*, github.com/client2/*]
Y
  config bob <<'Y'
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "human:bob@agency.example", name: "Bob Roe", handle: bob}
    stores:
      - {url: "git@github.com:agency/notes", org_folder: "."}
      - {url: "git@github.com:agency/notes-client"}
    repos: [github.com/client/website]
Y
  note "one store per client"
  on alice . "lore store init git@github.com:agency/notes && lore store init git@github.com:agency/notes-client && lore store init git@github.com:agency/notes-client2"
  quiet alice work "git clone -q https://github.com/client/website website"
  on alice work/website "lore org link agency --project client --store git@github.com:agency/notes-client --yes"
  on alice work/website "lore actor register"
  on alice work/website "lore memory add --body 'the client staging database is restored every Sunday night'"
  on alice work/website "lore rule add --to agency/. --body 'every client estimate is reviewed by two people before it is sent'"

  note "contractor view"
  quiet bob work "git clone -q https://github.com/client/website website"
  TTY=1 on bob work/website "lore whoami"
  on bob work/website "lore actor register"
  on bob work/website "lore memory list"
  on bob . "lore store show"
  on bob . "ls ~/.lore/clones/github.com/agency"
}

# ---------------------------------------------------------------------------
# two-orgs: one person, two companies, one laptop.
s_two_orgs() {
  section two-orgs
  person alice alice@example.com
  config alice <<'Y'
identity:
  key: human:alice@example.com
  name: Alice Doe
personal:
  store: local
orgs:
  - name: agency
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", org_folder: org}
    own:   [github.com/agency/*]
    repos: [github.com/client/*]
  - name: client3
    identity: {key: "human:alice@client3.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:client3/notes", org_folder: org}
    own: [github.com/client3/*]
Y
  remote agency/notes; remote client3/notes
  client_repo client/website app
  client_repo client3/api api client3dev
  client_repo someone/tool tool somedev
  quiet alice . "lore store init git@github.com:agency/notes && lore store init git@github.com:client3/notes"
  quiet alice work "git clone -q https://github.com/client/website website && git clone -q https://github.com/client3/api client3-api && git clone -q https://github.com/someone/tool forked-tool"
  quiet alice work/website "lore org link agency --project client --yes"

  note "whoami per repo"
  on alice work/website "lore whoami"
  on alice work/client3-api "lore whoami"
  on alice work/forked-tool "lore whoami"
  on alice work/forked-tool "lore whoami --explain"

  note "personal notes split"
  on alice work/website "lore memory add --body 'ask the client PM before touching the payment flow' --to me"
  on alice work/client3-api "lore memory add --body 'client3 deploys only on Tuesdays' --to me"
  on alice work/website "lore memory list"
  on alice work/client3-api "lore memory list"
  on alice work/forked-tool "lore memory list"
}

# ---------------------------------------------------------------------------
# personal: private notes, local or synced through your own private repo.
s_personal() {
  section personal
  person alice alice@agency.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  remote agency/notes; client_repo client/website app; remote alice/scratch
  quiet alice . "lore store init git@github.com:agency/notes"
  quiet alice work "git clone -q https://github.com/client/website website"
  quiet alice work/website "lore org link agency --project client --yes"
  quiet alice work "git clone -q https://github.com/alice/scratch 2>/dev/null; cd scratch && git commit -q --allow-empty -m init && git push -q origin main && lore init --non-interactive --name=scratch >/dev/null 2>&1"

  note "local personal store"
  on alice work/website "lore memory add --body 'I always forget the staging VPN profile' --to me"
  on alice work/scratch "lore memory add --body 'I prefer tabs over spaces' --to me"
  TTY=1 ANSWER=n on! alice work/website "lore memory add --body 'agency invoices go to the finance inbox' --to me --everywhere"
  on alice work/website "lore memory list"
  on alice work/scratch "lore memory list"
  on alice . "find ~/.lore/personal -name '*.json' -path '*memories*' | sed 's#.*/personal/##'"

  note "git personal store"
  remote alice/lore-me
  person alice-devbox alice@agency.example
  for who in alice alice-devbox; do
    agency_config "$who" "human:alice@agency.example" "Alice Doe" alice
    sed -i 's#^  store: local#  store: git@github.com:alice/lore-me#' "$HOMES/$who/.lore/config.yaml"
  done
  on alice . "lore store init git@github.com:alice/lore-me"
  on alice work/website "lore memory add --body 'the client invoice template lives in the shared drive' --to me"
  quiet alice-devbox work "git clone -q https://github.com/client/website website"
  on alice-devbox work/website "lore memory list"
}

# ---------------------------------------------------------------------------
# agents: four agents and a person on one client 2 mission (the worked example).
# Agents share alice's machine (HOME) like tmux sessions on one server; each
# has its own clone and its own LORE_ACTOR / LORE_SESSION from `lore agent start`.
declare -A AGENT_ENV
# start_agent LABEL DIR: run `lore agent start` as alice in DIR and remember the agent's env.
start_agent() {
  on alice "$2" "lore agent start --label $1 --owner @alice"
  local a s
  a=$(grep -o 'LORE_ACTOR=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2)
  s=$(grep -o 'LORE_SESSION=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2)
  AGENT_ENV[$1]="LORE_ACTOR=$a LORE_SESSION=$s"
}
# ag LABEL DIR CMD: run CMD as agent LABEL (alice's HOME, the agent's env).
ag()  { AS="$1" EXTRA_ENV="${AGENT_ENV[$1]}" on alice "$2" "$3"; }
ag!() { AS="$1" EXTRA_ENV="${AGENT_ENV[$1]}" on! alice "$2" "$3"; }

s_agents() {
  section agents
  person alice alice@agency.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  remote agency/notes
  quiet alice . "lore store init git@github.com:agency/notes"
  for r in api app infra; do
    client_repo "client2/$r" "$r" client2dev
    quiet alice work "git clone -q https://github.com/client2/$r && cd $r && lore org link agency --project client2 --yes"
  done
  quiet alice work/api "lore actor register"
  # 70 s per step: past lore's 60 s fetch cache, so each agent sees the others' last write.
  STEP=70

  note "plan"
  cat > "$HOMES/alice/work/api/invite.yaml" <<'Y'
mission: {key: invite, title: "Invite landing page", to: agency/client2}
approvers: ["@alice"]
tasks:
  - {key: backend,  title: "send the invite email from the API", repo: api,   timebox: 60m}
  - {key: frontend, title: "build the invite page",       repo: app,   after: [backend], timebox: 60m}
  - {key: proxy,    title: "allow /invite on the proxy", repo: infra}
  - {key: deploy,   title: "deploy to staging",          role: coordinator, after: [frontend, proxy], gate: required}
Y
  on alice work/api "cat invite.yaml"
  start_agent agent-coord work/api
  ag agent-coord work/api "lore mission apply invite.yaml"
  local mis; mis=$(id msn)
  ag agent-coord work/api "lore mission apply invite.yaml"
  ag agent-coord work/api "lore task claim $mis"

  note "workers start"
  for r in api app infra; do quiet alice work/agents "mkdir -p \$(echo $r) && cd $r && [ -d .git ] || git clone -q https://github.com/client2/$r ."; done
  start_agent agent-backend work/agents/api
  ag agent-backend work/agents/api "lore inbox"
  ag agent-backend work/agents/api "lore task next --mission $mis --repo api"
  local tb; tb=$(id tsk)
  start_agent agent-frontend work/agents/app
  ag! agent-frontend work/agents/app "lore task next --mission $mis --repo app"
  start_agent agent-infra work/agents/infra
  ag agent-infra work/agents/infra "lore task next --mission $mis --repo infra --owns nginx/sites/invite.conf"
  local tp; tp=$(id tsk)

  note "finding"
  ag agent-infra work/agents/infra "lore finding add --mission $mis 'invite links must use the public website address, not the server name'"
  ag agent-backend work/agents/api "lore inbox"
  ag agent-infra work/agents/infra "lore task done $tp"

  note "done then merged"
  ag agent-backend work/agents/api "lore task note $tb 'the API sends the invite email and returns a link; the email text is in invite.html'"
  ag agent-backend work/agents/api "git switch -q -c agent-backend/invite && mkdir -p api && echo 'package api' > api/invite.go && git add api && git commit -qm 'send the invite email' && git push -q -u origin agent-backend/invite"
  ag agent-backend work/agents/api "lore task done $tb --commit \$(git rev-parse HEAD) --lesson 'invite links stop working after 48 hours; the README says so'"
  ag! agent-frontend work/agents/app "lore task next --mission $mis --repo app"
  on alice work/api "git pull -q && git merge -q --no-ff --no-edit origin/agent-backend/invite && git push -q origin main"
  on alice work/api "lore task list"
  ag agent-frontend work/agents/app "lore task next --mission $mis --repo app"
  local tf; tf=$(id tsk)

  note "crash and reclaim"
  ag agent-frontend work/agents/app "git switch -q -c agent-frontend/invite-route && mkdir -p src/routes && echo 'export {}' > src/routes/invite.tsx && git add src && git commit -qm 'invite page'"
  ag agent-frontend work/agents/app "lore task note $tf 'route + loader done, form next'"
  CLOCK=$(date -u -d "$CLOCK + 25 minutes" +%Y-%m-%dT%H:%M:%SZ)
  ag agent-coord work/api "lore inbox"
  ag agent-coord work/api "lore mission status $mis"
  quiet alice work/agents "mkdir -p fe2 && cd fe2 && git clone -q https://github.com/client2/app"
  start_agent agent-frontend-2 work/agents/fe2/app
  ag agent-frontend-2 work/agents/fe2/app "lore task next --mission $mis --repo app"
  ag agent-frontend-2 work/agents/fe2/app "git fetch -q origin agent-frontend/invite-route && git log --oneline -1 FETCH_HEAD"
  ag! agent-frontend work/agents/app "lore task done $tf"
  ag agent-frontend-2 work/agents/fe2/app "git switch -q -c agent-frontend-2/invite-route FETCH_HEAD && echo form >> src/routes/invite.tsx && git commit -qam 'invite form' && git push -q -u origin agent-frontend-2/invite-route && lore task done $tf --commit \$(git rev-parse HEAD)"
  on alice work/app "git pull -q && git merge -q --no-ff --no-edit origin/agent-frontend-2/invite-route && git push -q origin main && lore task list >/dev/null"

  note "merged by hand"
  ag! agent-coord work/api "lore task next --mission $mis --role coordinator"
  on alice work/infra "lore task merged $tp"

  note "gate"
  ag! agent-coord work/api "lore task next --mission $mis --role coordinator"
  local tg; tg=$(grep -o 'tsk_[0-9a-f]\{32\}' <<<"$LAST_OUT" | head -1 || true)
  ag! agent-coord work/api "lore gate approve $tg"
  on alice work/api "lore gate approve $tg"
  ag agent-coord work/api "lore task next --mission $mis --role coordinator"

  note "mission status"
  on alice work/infra "lore mission status $mis"
  on alice work/infra "lore task show $tg --from agency/client2"
  on alice work/infra "lore task show $tf"
}

# ---------------------------------------------------------------------------
# leak-cleanup: an internal note was written to the client's live ref by mistake.
s_leak_cleanup() {
  section leak-cleanup
  person alice alice@agency.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  remote agency/notes; client_repo client/website app
  quiet alice . "lore store init git@github.com:agency/notes"
  quiet alice work "git clone -q https://github.com/client/website website && cd website && lore org link agency --project client --yes"

  note "the mistake"
  on alice work/website "lore task add 'chase the client about the unpaid March invoice' --to repo"
  local t; t=$(id tsk)
  on carol work/app "lore sync >/dev/null; lore task list"

  note "purge"
  on alice work/website "lore live purge $t"
  on alice work/website "lore live compact --dry-run"
  on alice work/website "lore live compact"
  on alice work/website "lore live log"
  on carol work/app "lore sync >/dev/null; lore task list"
  person newdev newdev@client.example
  on newdev work "git clone -q --mirror https://github.com/client/website app.git && cd app.git && git for-each-ref --format='%(refname)' && for c in \$(git rev-list --all); do git grep -q -i 'unpaid March' \$c && echo \"found in \$c\"; done; echo 'searched every commit the remote serves'"
}

# ---------------------------------------------------------------------------
# errors: the error messages people meet, each produced for real.
s_errors() {
  section errors
  person alice alice@agency.example; person bob bob@agency.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  agency_config bob "human:bob@agency.example" "Bob Roe" bob
  remote agency/notes; client_repo client/website app
  quiet alice . "lore store init git@github.com:agency/notes"
  quiet alice work "git clone -q https://github.com/client/website website && cd website && lore org link agency --project client --yes && lore actor register"

  note "config invalid"
  cp "$HOMES/alice/.lore/config.yaml" "$WORLD/alice-config.bak"
  sed -i 's#org_folder: org#org_folder: org\n        branch: release/2026#' "$HOMES/alice/.lore/config.yaml"
  on! alice . "lore config check"
  on! alice work/website "lore whoami"
  cp "$WORLD/alice-config.bak" "$HOMES/alice/.lore/config.yaml"

  note "not a store"
  client_repo client2/docs docs client2dev
  quiet client2dev work/docs "mkdir -p src && echo 'export {}' > src/index.ts && echo '{}' > package.json && git add . && git commit -qm code && git push -q origin main"
  cp "$HOMES/alice/.lore/config.yaml" "$WORLD/alice-config.bak"
  sed -i 's#^    own:#      - url: git@github.com:client2/docs\n    own:#' "$HOMES/alice/.lore/config.yaml"
  on! alice work/website "lore whoami"
  on! alice . "lore store doctor"
  cp "$WORLD/alice-config.bak" "$HOMES/alice/.lore/config.yaml"

  note "org ambiguous"
  cp "$HOMES/alice/.lore/config.yaml" "$WORLD/alice-config.bak"
  cat >> "$HOMES/alice/.lore/config.yaml" <<'Y'
  - name: client3
    identity: {key: "human:alice@client3.example", name: "Alice Doe"}
    repos: [github.com/client/*]
Y
  client_repo client/blog site
  quiet alice work "git clone -q https://github.com/client/blog blog"
  on! alice work/blog "lore whoami"
  on! alice work/blog "lore memory add --body 'the site deploys from the release branch'"
  cp "$WORLD/alice-config.bak" "$HOMES/alice/.lore/config.yaml"

  note "link unknown"
  mv "$HOSTS/agency/notes.git" "$WORLD/lore.git.away"
  quiet bob work "git clone -q https://github.com/client/website website"
  on! bob work/website "lore memory add --body 'the client wants weekly demos'"

  note "offline claim"
  mv "$WORLD/lore.git.away" "$HOSTS/agency/notes.git"
  quiet alice work/website "lore task add 'rotate the API keys' --to agency"
  local t; t=$(grep -o 'tsk_[0-9a-f]\{32\}' "$WORLD/quiet.log" | head -1 || true)
  mv "$HOSTS/agency/notes.git" "$WORLD/lore.git.away"
  on! alice work/website "lore task claim $t"
  on alice work/website "lore memory add --body 'the API keys rotate every 90 days' --to agency || true"
  mv "$WORLD/lore.git.away" "$HOSTS/agency/notes.git"
  on alice work/website "lore sync"

  note "store branch missing"
  cat >> "$HOMES/alice/.lore/config.yaml" <<'Y'
  - name: agency-stag
    identity: {key: "human:alice@agency.example", name: "Alice Doe", handle: alice}
    stores:
      - {url: "git@github.com:agency/notes", branch: staging, org_folder: org}
Y
  on! alice work/website "LORE_ORG=agency-stag lore org link agency-stag --project client --yes"

  note "refs refused by the host"
  remote widgets/portal
  cat > "$HOSTS/widgets/portal.git/hooks/pre-receive" <<'HOOK'
#!/bin/sh
while read old new ref; do
  case "$ref" in refs/lore/*) echo "remote: refusing $ref: only branches and tags may be pushed here" >&2; exit 1;; esac
done
HOOK
  chmod +x "$HOSTS/widgets/portal.git/hooks/pre-receive"
  quiet bob work "git clone -q https://github.com/widgets/portal 2>/dev/null; cd portal && git commit -q --allow-empty -m init && git push -q origin main && lore init --non-interactive --name=portal >/dev/null 2>&1"
  on bob work/portal "lore task add 'set up the portal CI'"
  on bob work/portal "lore live diff"
  on bob work/portal "lore doctor 2>&1 | grep -i -E 'live|refs/lore|push' || true"
}

# ---------------------------------------------------------------------------
# internals: what lore actually writes in git, for the concepts and how-it-works pages.
s_internals() {
  section internals
  person alice alice@agency.example
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  remote agency/notes; client_repo client/website app
  quiet alice . "lore store init git@github.com:agency/notes"
  quiet alice work "git clone -q https://github.com/client/website website && cd website && lore org link agency --project client --yes && lore actor register"

  note "refspec"
  on alice work/website "git config --get-all remote.origin.fetch"

  note "monorepo live tree"
  on carol work/app "mkdir -p web && cd web && lore init --non-interactive --name=web >/dev/null 2>&1; cd .. && lore task add 'fix header' --to repo && cd web && lore rule add --body 'Buttons are blue' --live --to repo"
  on carol work/app "git ls-tree -r --name-only refs/lore/live"
  on carol work/app "git log --oneline refs/lore/live"
  on carol work/app "git cat-file -p refs/lore/live:_live/meta.json; echo; git cat-file -p refs/lore/live:_live/epoch"
  on carol work/app "git cat-file -p refs/lore/live:\$(git ls-tree -r --name-only refs/lore/live | grep '_root/tasks/')"
  on carol work/app "git branch -a"

  note "store layout"
  on alice work/website "lore task add 'renew the client SSL certificate' --to agency"
  local t; t=$(id tsk)
  on alice work/website "lore memory add --body 'the client wants a demo every second Friday'"
  on alice . "git -C ~/.lore/clones/github.com/agency/notes.git ls-tree -r --name-only main | grep -v '_meta.json\|.gitattributes' | head -20"
  on alice . "git -C ~/.lore/clones/github.com/agency/notes.git for-each-ref --format='%(refname)'"
  on alice . "git -C ~/.lore/clones/github.com/agency/notes.git ls-tree -r --name-only refs/lore-remote/origin/live/main"
  on alice . "git -C ~/.lore/clones/github.com/agency/notes.git cat-file -p main:client/.lore/data/_meta.json"

  note "signals"
  on alice work/website "lore agent start --label agent-backend --owner @alice"
  local a s2
  a=$(grep -o 'LORE_ACTOR=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2); s2=$(grep -o 'LORE_SESSION=[^ ]*' <<<"$LAST_OUT" | head -1 | cut -d= -f2)
  EXTRA_ENV="LORE_ACTOR=$a LORE_SESSION=$s2" AS=agent-backend on alice work/website "lore task claim $t"
  on alice . "cd ~/.lore/clones/github.com/agency/notes.git && git fetch -q origin && git ls-tree -r --name-only refs/lore-remote/origin/signals/main && git log --oneline refs/lore-remote/origin/signals/main"
  on alice . "cd ~/.lore/clones/github.com/agency/notes.git && git cat-file -p refs/lore-remote/origin/signals/main:\$(git ls-tree -r --name-only refs/lore-remote/origin/signals/main | head -1)"
  EXTRA_ENV="LORE_ACTOR=$a LORE_SESSION=$s2" AS=agent-backend on alice work/website "lore task heartbeat $t"
  on alice . "cd ~/.lore/clones/github.com/agency/notes.git && git fetch -q origin && git log --oneline refs/lore-remote/origin/signals/main"

  note "hooks"
  on alice work/website "ls .git/hooks | grep -v sample; grep -h 'lore' .git/hooks/pre-commit | head -5"
  on alice work/website "cat .git/info/exclude | tail -2"
}

# ---------------------------------------------------------------------------
# team-ops: teams, and the claim commands a person uses on an agent's work
# (release, revoke, reassign) plus shared resources (docs/team/agents.md).
s_team_ops() {
  section team-ops
  person alice alice@agency.example; person bob bob@agency.example
  # 70 s per step: past lore's 60 s fetch cache, so each person sees the other's last write.
  STEP=70
  # First a repo with no org config (like the small-team page): teams and shared resources.
  remote agency/billing
  quiet alice work "git clone -q https://github.com/agency/billing && cd billing && echo '# billing' > README.md && git add . && git commit -qm init && lore init --non-interactive --name=billing >/dev/null 2>&1 && git add -A && git commit -qm 'lore: init' && git push -q origin main && lore actor register"
  quiet bob work "git clone -q https://github.com/agency/billing && cd billing && lore actor register"

  note "teams"
  on alice work/billing "lore team add @frontend --name 'Frontend' --members @alice,@bob"
  on alice work/billing "lore team list"
  on alice work/billing "lore task add 'make the buttons blue' --assign @frontend"
  on bob work/billing "lore task list"

  note "resource"
  on alice work/billing "lore hold claim full-suite --ttl 30m"
  on! bob work/billing "lore hold claim full-suite --ttl 30m"
  on alice work/billing "lore hold release full-suite"
  on bob work/billing "lore hold claim full-suite --ttl 30m"

  # Then the agency's client repo, with the org config: claims a person takes back or moves.
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  agency_config bob "human:bob@agency.example" "Bob Roe" bob
  remote agency/notes; client_repo client/website app
  quiet alice . "lore store init git@github.com:agency/notes"
  quiet alice work "git clone -q https://github.com/client/website website && cd website && lore org link agency --project client --yes && lore actor register"
  quiet bob work "git clone -q https://github.com/client/website website && cd website && lore actor register"

  note "release"
  on alice work/website "lore task add 'send the invoice reminder before Friday' --to agency"
  local t1; t1=$(id tsk)
  on bob work/website "lore task claim $t1"
  on bob work/website "lore task release $t1"
  on alice work/website "lore task show $t1"

  note "revoke"
  start_agent agent-backend work/website
  ag agent-backend work/website "lore task claim $t1"
  on alice work/website "lore task revoke $t1"
  ag! agent-backend work/website "lore task done $t1"

  note "reassign"
  on alice work/website "lore task add 'update the contact page' --to agency --assign @bob"
  local t2; t2=$(id tsk)
  on bob work/website "lore task claim $t2"
  on alice work/website "lore task reassign $t2 --assign @alice"
  on! bob work/website "lore task done $t2"
}

# ---------------------------------------------------------------------------
# people: your identity, joining an org from its store, and store housekeeping
# (docs/team/identity-and-stores.md).
s_people() {
  section people
  person alice alice@agency.example; person bob bob@agency.example
  remote agency/notes; client_repo client/website app

  note "identity"
  quiet alice work "git clone -q https://github.com/client/website website"
  on alice work/website "lore identity show"
  on alice . "lore identity set alice@example.com"
  on alice work/website "lore identity show"
  on alice . "lore identity anonymize on"
  on alice . "lore identity anonymize off"
  on alice . "lore identity unset"
  on alice work/website "lore identity show"

  note "identity per org"
  agency_config alice "human:alice@agency.example" "Alice Doe" alice
  on alice . "lore store init git@github.com:agency/notes"
  on alice . "lore identity set --org agency --key human:alice@agency.example --name 'Alice Doe' --handle alice"
  quiet alice work/website "lore org link agency --project client --yes && lore actor register"
  on alice work/website "lore identity show"

  note "suggest an org entry"
  quiet alice . "git clone -q https://github.com/agency/notes notes-checkout"
  cat > "$HOMES/alice/notes-checkout/org/.lore/lore-org.yaml" <<'Y'
name: agency
stores:
  - {url: "git@github.com:agency/notes", org_folder: org}
own: [github.com/agency/*]
repos: [github.com/client/*]
Y
  on alice notes-checkout "git add org/.lore/lore-org.yaml && git commit -qm 'suggest the agency org entry' && git push -q origin main"

  note "org import"
  on bob . "lore org import git@github.com:agency/notes --key human:bob@agency.example --name 'Bob Roe' --handle bob --yes"
  on bob . "cat ~/.lore/config.yaml"
  quiet bob work "git clone -q https://github.com/client/website website"
  on bob work/website "lore whoami"

  note "org set"
  remote agency/tools
  quiet alice work "git clone -q https://github.com/agency/tools && cd tools && echo '# tools' > README.md && git add . && git commit -qm init && git push -q origin main && lore init --non-interactive --name=tools >/dev/null 2>&1"
  on alice work/tools "lore org set agency --commit"
  on alice work/tools "cat .lore/org.yaml"
  on! alice work/website "lore org set agency --commit"

  note "move"
  on alice work/website "lore memory add --body 'the client replies faster on WhatsApp than email' --to repo"
  local m; m=$(id mem)
  on alice work/website "lore move $m --to agency --yes"
  on alice work/website "lore memory list"

  note "store open and forget"
  on alice . "lore store open git@github.com:agency/notes"
  on alice . "ls ~/.lore/worktrees/github.com/agency/notes/main"
  on bob . "lore store forget git@github.com:agency/notes"

  note "cache prune"
  on alice . "lore cache prune --dry-run"
}

# ---------------------------------------------------------------------------
# live-repair: the live ref deleted on the server and put back with `lore live
# restore`, and --trace showing the git commands lore runs (docs/team/commands.md,
# docs/team/troubleshooting.md).
s_live_repair() {
  section live-repair
  remote agency/billing
  person alice alice@agency.example
  quiet alice work "git clone -q https://github.com/agency/billing && cd billing && echo '# billing' > README.md && git add . && git commit -qm init && lore init --non-interactive --name=billing >/dev/null 2>&1 && git add -A && git commit -qm 'lore: init' && git push -q origin main"

  note "live ref deleted"
  on alice work/billing "lore task add 'send the March invoices'"
  on alice work/billing "lore task add 'chase the unpaid February invoice'"
  on alice work/billing "git push -q origin :refs/lore/live && git ls-remote origin"
  on! alice work/billing "lore task add 'close the books for Q1'"
  on alice work/billing "lore doctor | grep 'remote live ref'"

  note "live restore"
  on alice work/billing "lore live restore"
  on alice work/billing "git ls-remote origin refs/lore/live"
  on alice work/billing "lore task add 'close the books for Q1'"
  on alice work/billing "lore task list"

  note "trace"
  on alice work/billing "lore sync --trace 2>&1 | grep -e '^trace: git fetch' -e '^trace: git push' -e '^·'"
}

main() {
  local all=(getting_started solo small_team agency group staging contractor two_orgs personal agents leak_cleanup errors internals team_ops people live_repair)
  local want=("$@")
  [ ${#want[@]} -eq 0 ] && want=("${all[@]}")
  for s in "${want[@]}"; do
    "s_${s//-/_}"
  done
  python3 "$HERE/normalize.py" "$TRANSCRIPT"
  if [ ${#KNOWN[@]} -ne 0 ]; then
    echo "run-examples: ${#KNOWN[@]} known failure(s), each a reported lore bug (see the KNOWN FAILURE lines above):" >&2
    printf '  %s\n' "${KNOWN[@]}" >&2
  fi
  if [ "$FAILURES" -ne 0 ]; then
    echo "run-examples: $FAILURES example command(s) did not behave as documented" >&2
    exit 1
  fi
  echo "run-examples: every example ran as documented; transcripts in ${OUT#"$REPO"/}" >&2
}

main "$@"
