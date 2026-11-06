# environment: the environment variables of docs/reference/environment.md, each set in
# front of one real command (docs/reference/environment.md, docs/sync/how-notes-travel-with-git.md,
# docs/operations/secrets-and-pii.md).
# Alice (an agency developer) works in the client's website repo, which has a remote on
# the fake GitHub, so her rows are live rows by default.

lore_repo alice client/website

note "live off"
on alice work/website "LORE_LIVE=0 lore task add 'Update the privacy page'"
on alice work/website "git status --short"
on alice work/website "lore task add 'Check the contact form on a phone'"
on alice work/website "lore live show"

note "autopush off"
on alice work/website "LORE_LIVE_AUTOPUSH=0 lore task add 'Renew the SSL certificate'"
local t_ssl; t_ssl=$(id tsk)
on alice work/website "lore live diff"
on! alice work/website "LORE_LIVE_AUTOPUSH=0 lore task claim $t_ssl"
on alice work/website "lore sync"

note "live remote"
on alice work "git clone -q -o upstream https://github.com/client/website website-fork && cd website-fork && git remote"
on alice work/website-fork "lore task list"
on alice work/website-fork "LORE_LIVE_REMOTE=upstream lore task list"

note "trace"
on alice work/website "LORE_TRACE=1 lore task list 2>&1 | grep '^trace:' | head -6"

note "sync quiet"
on alice work/website "lore rule add --body 'Run the tests before you push'"
on alice work/website "LORE_SYNC_QUIET=1 lore memory add --body 'The client replies faster on WhatsApp than email.'"

note "sync git off"
solo_repo alice client/shop
on alice work/shop "LORE_SYNC_GIT=0 lore init --non-interactive --name=shop"
on alice work/shop "ls .git/hooks | grep -v sample; cat .gitattributes 2>/dev/null || echo '(no .gitattributes)'"
on alice work/shop "lore sync"
on alice work/shop "ls .git/hooks | grep -v sample; cat .gitattributes"

note "not a secret"
on alice work/website "lore memory add --not-a-secret --reason 'example value, not a real password' --yes --body 'The staging password is pass''word=example-only'"
local m_secret; m_secret=$(id mem)
on alice work/website "ls .lore/data/memories | grep -c $m_secret || true"

note "network drive"
quiet alice work "mkdir -p Dropbox && git clone -q https://github.com/client/website Dropbox/website"
on! alice work/Dropbox/website "lore rule list"
on alice work/Dropbox/website "LORE_ALLOW_NETWORK_FS=1 lore rule list"

# LORE_ALLOW_DB_PATH_OUTSIDE and LORE_HOME now matter only when `lore migrate run`
# reads a v0.1.17 shared-database pointer (Mode B, retired in 0.1.18: decision Q-MODEB).
# A v0.1.17 shared lore.db holding one project's rows: a scratch repo's lore.db, copied out.
quiet alice work "mkdir -p old-blog && cd old-blog && git init -q && lore init --non-interactive --name=blog && lore rule add --body 'Posts go out on Tuesdays' && lore sync && python3 -c \"import sqlite3; sqlite3.connect('.lore/lore.db').backup(sqlite3.connect('../client.db'))\" && mkdir -p ~/shared-dbs && cp ../client.db ~/shared-dbs/client-docs.db && lore project list --json | grep -o 'prj_[0-9a-f]*' | head -1 > ../old-project-id && cd .. && rm -rf old-blog"

note "db path outside"
quiet alice work "mkdir -p blog && cd blog && git init -q && mkdir .lore && printf 'db_path: ../client.db\nproject_id: %s\n' \$(cat ../old-project-id) > .lore/lore.yaml"
on alice work/blog "cat .lore/lore.yaml"
on! alice work/blog "lore migrate run --dry-run"
on alice work/blog "LORE_ALLOW_DB_PATH_OUTSIDE=1 lore migrate run"
on alice work/blog "lore rule list"

note "lore home"
quiet alice work "mkdir -p docs && cd docs && git init -q && mkdir .lore && printf 'db_path: \${LORE_HOME}/client-docs.db\nproject_id: %s\n' \$(cat ../old-project-id) > .lore/lore.yaml"
on alice work/docs "cat .lore/lore.yaml"
on! alice work/docs "lore migrate run --dry-run"
on alice work/docs "LORE_HOME=~/shared-dbs lore migrate run"
on alice work/docs "lore rule list"
on alice . "ls ~/shared-dbs ~/shared-dbs/stores"

note "colour"
on alice work/website "unset NO_COLOR; lore doctor | head -1 | cat -v"
on alice work/website "unset NO_COLOR; FORCE_COLOR=1 lore doctor | head -1 | cat -v"
on alice work/website "NO_COLOR=1 FORCE_COLOR=1 lore doctor | head -1 | cat -v"
