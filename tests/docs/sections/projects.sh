# projects.sh: projects, repos and the shared-database mode (docs/sync/multi-repo-projects.md,
# docs/sync/shared-database-mode-b.md).
#
# Alice (agency developer) keeps notes for the client's website, then for client 2, whose
# product is three repos (app, api, infra) that share one notes database. Fake GitHub only.

lore_repo alice client/website

# --- docs/sync/multi-repo-projects.md --------------------------------------------------
note "project list"
on alice work/website "lore project list"
on alice work/website "lore project show website"

note "add repos"
on alice work/website "lore repo list"
on alice work/website "lore repo add shop --origin https://github.com/client/website --display-name 'Online shop'"
on alice work/website "lore repo add help --display-name 'Help pages'"
local rep_help; rep_help=$(id rep)
on alice work/website "lore repo list"

note "notes per repo"
on alice work/website "lore rule add --body 'Run the tests before you push.'"
on alice work/website "lore rule add --repo shop --body 'Buttons are blue.'"
on alice work/website "lore rule list"
on alice work/website "lore rule list --repo shop"
on! alice work/website "lore rule list --repo blog"

note "archive a repo"
on! alice work/website "lore repo archive help"
on alice work/website "lore repo archive $rep_help"
on alice work/website "lore repo list"
on alice work/website "lore repo unarchive $rep_help"
on alice work/website "lore mount-alias list"
on alice work/website "git add -A && git commit -qm 'lore: repos' && git push -q origin main"

# --- docs/sync/shared-database-mode-b.md -----------------------------------------------
# 0.1.18 retired the shared-database mode (Mode B, decision Q-MODEB): one folder store,
# attached in each repo with `lore store add`, holds the notes the repos share, and
# `lore migrate run` converts a repo that still has a v0.1.17 pointer file.
note "one store"
solo_repo alice client2/app
solo_repo alice client2/api
solo_repo alice client2/infra
quiet alice work/app "lore init --non-interactive --name=app"
quiet alice work/api "lore init --non-interactive --name=api"
on alice work/app "lore store init client2 file://\$HOME/stores/client2"
on alice work/api "lore store add client2 file://\$HOME/stores/client2"
on alice work/app "lore store list"

note "shared notes"
on alice work/app "lore rule add --to client2 --body 'Every screen works on a phone.'"
on alice work/api "lore rule add --to client2 --body 'Every change comes with a test.'"
on alice work/api "lore rule add --body 'The API answers in JSON only.'"
on alice work/api "lore rule list"
on alice work/app "lore rule list"
on alice work/app "git status --short"

note "archive a project"
solo_repo alice client2/scratch
quiet alice work/scratch "lore init --non-interactive --name=scratch"
on alice work/scratch "lore project list"
local prj_scratch; prj_scratch=$(id prj)
on alice work/scratch "lore project archive $prj_scratch"
on alice work/scratch "lore project list"
on alice work/scratch "lore project unarchive $prj_scratch"
on! alice work/scratch "lore project delete $prj_scratch"
on alice work/scratch "lore project delete $prj_scratch --yes"
on alice work/scratch "lore project list"

# A v0.1.17 shared database for infra: a project's rows in a lore.db outside the repo,
# and the two-line pointer file v0.1.17 wrote into .lore/. (The rows come from a
# scratch repo whose lore.db is copied out, which is what v0.1.17 kept there.)
quiet alice work "mkdir -p old-infra && cd old-infra && git init -q && lore init --non-interactive --name=infra && lore rule add --body 'Terraform changes need a plan in the pull request.' && lore memory add --body 'The staging cluster sleeps at night.' && lore sync && mkdir -p ~/.lore && lore backup --out ~/.lore/client2.db"
quiet alice work/infra "mkdir -p .lore && printf 'db_path: \${HOME}/.lore/client2.db\nproject_id: %s\n' \$(cd ../old-infra && lore project list --json | grep -o 'prj_[0-9a-f]*' | head -1) > .lore/lore.yaml && rm -rf ../old-infra"

note "convert a shared database"
on alice work/infra "cat .lore/lore.yaml"
on! alice work/infra "lore rule list"
on alice work/infra "lore migrate run --dry-run"
on alice work/infra "lore migrate run"
on alice work/infra "ls .lore; cat .lore/stores.yaml"
on alice work/infra "lore rule list; lore memory list"

note "errors"
on! alice work "lore rule list"
