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
on alice work/website "LORE_REPO=shop lore rule list"
on! alice work/website "lore rule list --repo blog"

note "archive a repo"
on! alice work/website "lore repo archive help"
on alice work/website "lore repo archive $rep_help"
on alice work/website "lore repo list"
on alice work/website "lore repo unarchive $rep_help"
on alice work/website "lore mount-alias list"
on alice work/website "git add -A && git commit -qm 'lore: repos' && git push -q origin main"

# --- docs/sync/shared-database-mode-b.md -----------------------------------------------
note "shared init"
solo_repo alice client2/app
solo_repo alice client2/api
solo_repo alice client2/infra
on alice work/app "lore project shared-init --db '\${HOME}/.lore/client2.db' --name app"
on alice work/app "cat .lore/lore.yaml"
on alice work/api "lore project shared-init --db '\${HOME}/.lore/client2.db' --name api"
on alice work/infra "lore project shared-init --db '\${HOME}/.lore/client2.db' --name infra"
on alice work "lore project shared-list --db ~/.lore/client2.db"

note "shared notes"
on alice work/app "lore rule add --body 'Every screen works on a phone.'"
on alice work/api "lore rule add --body 'Every change comes with a test.'"
on alice work/api "lore rule list"
on alice work/app "lore rule list"
on alice work/app "git status --short"

note "pick the database"
on alice work "lore rule list --db ~/.lore/client2.db --project api"
on alice work "LORE_DB=~/.lore/client2.db lore rule list --project app"
on alice work/infra "LORE_PROJECT_ID=\$(grep project_id ../api/.lore/lore.yaml | cut -d' ' -f2) lore rule list"
on alice work "LORE_DB=~/.lore/client2.db LORE_PROJECT_ID=\$(grep project_id api/.lore/lore.yaml | cut -d' ' -f2) lore rule list"

note "archive a project"
on alice work "mkdir -p scratch && cd scratch && lore project shared-init --db '\${HOME}/.lore/client2.db' --name scratch"
local prj_scratch; prj_scratch=$(id prj)
on alice work/scratch "lore project archive $prj_scratch"
on alice work "lore project shared-list --db ~/.lore/client2.db"
on alice work/scratch "lore project unarchive $prj_scratch"
on! alice work/scratch "lore project delete $prj_scratch"
on alice work/scratch "lore project delete $prj_scratch --confirm"
on alice work "rm -rf scratch && lore project shared-list --db ~/.lore/client2.db"

note "old pointer file"
on alice work/infra "mv .lore/lore.yaml ~/pointer.keep; printf 'db_path = \"\${HOME}/.lore/client2.db\"\nproject_id = \"%s\"\n' \$(grep project_id ~/pointer.keep | cut -d' ' -f2) > .lore/lore.toml; rm ~/pointer.keep; ls .lore"
on alice work/infra "lore rule list"
on alice work/infra "ls .lore"

note "errors"
on! alice work "lore rule list"
on! alice work/app "touch .lore/lore.db && lore rule list"
on alice work/app "rm .lore/lore.db"
on! alice work/app "cd .lore && lore rule list"
