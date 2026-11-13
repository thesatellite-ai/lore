# projects.sh: projects, repos and the shared-database mode (docs/sync/multi-repo-projects.md,
# docs/sync/shared-database-mode-b.md).
#
# Alice (agency developer) keeps notes for the client's website, then for client 2, whose
# product is three repos (app, api, infra) that share one notes database. Fake GitHub only.

lore_repo alice client/website

# --- docs/sync/multi-repo-projects.md --------------------------------------------------
note "project list"
on alice work/website "lore project list"
local prj_website; prj_website=$(id prj)
on alice work/website "lore project show --id website"

note "add repos"
on alice work/website "lore repo list"
on alice work/website "lore repo add --mount-name shop --origin https://github.com/client/website --display-name 'Online shop'"
on alice work/website "lore repo add --mount-name help --display-name 'Help pages'"
local rep_help; rep_help=$(id rep)
on alice work/website "lore repo list"

note "notes per repo"
on alice work/website "lore rule add --body 'Run the tests before you push.'"
on alice work/website "lore rule add --repo shop --body 'Buttons are blue.'"
on alice work/website "lore rule list"
on alice work/website "lore rule list --repo shop"
on! alice work/website "lore rule list --repo blog"

note "repo from the environment"
on alice work/website "LORE_REPO=shop lore rule list"
on alice work/website "LORE_REPO=shop lore rule list --repo website"
on alice work/website "LORE_PROJECT_ID=$prj_website LORE_REPO=shop lore rule list"
on! alice work/website "LORE_PROJECT_ID=prj_00000000000000000000000000000000 lore rule list"

note "archive a repo"
on! alice work/website "lore repo archive --id help"
on alice work/website "lore repo archive --id $rep_help"
on alice work/website "lore repo list"
on alice work/website "lore repo unarchive --id $rep_help"
on alice work/website "git add -A && git commit -qm 'lore: repos' && git push -q origin main"

note "repo moved"
# The client renamed the repo on GitHub: a new bare repo on the fake GitHub stands in for it.
remote client/website-v2
on alice work/website "git remote set-url origin https://github.com/client/website-v2 && git push -q origin main"
on alice work/website "lore repo set-url --url git@github.com:client/website-v2.git"
on alice work/website "lore repo set-url --url https://gitlab.com/client/website-v2 --mirror"
on alice work/website "lore repo list"
on alice work/website "lore repo merge-ids --dry-run"
on alice work/website "git add -A && git commit -qm 'lore: repo address' && git push -q origin main"

note "fork"
# Alice forks the client's repo to github.com/alice/website (a bare copy on the fake GitHub).
quiet alice work "git clone -q --bare https://github.com/client/website-v2 \$HOME/../../hosts/github.com/alice/website.git"
on alice work "git clone -q https://github.com/alice/website website-fork"
on! alice work/website-fork "git remote add upstream https://github.com/client/website-v2 && git remote set-url origin https://github.com/client/website-v2 && lore repo fork"
on alice work/website-fork "git remote set-url origin https://github.com/alice/website"
on alice work/website-fork "lore repo fork"
on alice work/website-fork "lore repo list"
on alice work/website-fork "git status --short"

# --- docs/sync/shared-database-mode-b.md -----------------------------------------------
# 0.1.18 retired the shared-database mode (Mode B, decision Q-MODEB): one folder store,
# attached in each repo with `lore store add`, holds the notes the repos share, and
# `lore migrate run` converts a repo that still has a v0.1.17 pointer file.
note "one store"
solo_repo alice client2/app
solo_repo alice client2/api
solo_repo alice client2/infra
quiet alice work/app "lore init --name=app"
quiet alice work/api "lore init --name=api"
on alice work/app "lore store init --name client2 --url file://\$HOME/stores/client2"
on alice work/api "lore store add --name client2 --url file://\$HOME/stores/client2"
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
quiet alice work/scratch "lore init --name=scratch"
on alice work/scratch "lore project list"
local prj_scratch; prj_scratch=$(id prj)
on alice work/scratch "lore project archive --id $prj_scratch"
on alice work/scratch "lore project list"
on alice work/scratch "lore project unarchive --id $prj_scratch"
on! alice work/scratch "lore project delete --id $prj_scratch"
# Q-R3-11: the checkout's own repos row (PROV1) goes with the project in one batch.
on alice work/scratch "lore project delete --id $prj_scratch --yes"
on alice work/scratch "lore project list"

# A v0.1.17 shared database for infra: a project's rows in a lore.db outside the repo,
# and the two-line pointer file v0.1.17 wrote into .lore/. (The rows are written in
# v0.1.17's own table shapes by tests/docs/v017_shared_db.py: 0.1.18 never writes rows
# into a lore.db.)
quiet alice work "mkdir -p ~/.lore infra && python3 '$HERE/v017_shared_db.py' ~/.lore/client2.db infra 'rule:Terraform changes need a plan in the pull request.' 'memory:The staging cluster sleeps at night.' > old-infra-id"
quiet alice work/infra "mkdir -p .lore && printf 'db_path: \${HOME}/.lore/client2.db\nproject_id: %s\n' \$(cat ../old-infra-id) > .lore/lore.yaml && rm ../old-infra-id"

note "convert a shared database"
on alice work/infra "cat .lore/lore.yaml"
on! alice work/infra "lore rule list"
on alice work/infra "lore migrate run --dry-run"
on alice work/infra "lore migrate run"
on alice work/infra "ls .lore; cat .lore/stores.yaml"
on alice work/infra "lore rule list; lore memory list"

note "migration status"
on alice work/infra "lore migrate status"
on alice work/app "lore migrate status"
on! alice work/app "lore migrate rollback"
on alice work/app "lore migrate unlock"
on alice work/app "lore migrate enable --feature meta"
on alice work/app "lore migrate enable --feature meta"
on alice work/app "lore migrate status | grep -E '^(features|min_lore_version)'"

note "errors"
on! alice work "lore rule list"
