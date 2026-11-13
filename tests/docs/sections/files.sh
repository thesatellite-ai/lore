# files.sh: where lore keeps things and how a store is a plain folder you may edit by hand
# (docs/operations/where-lore-keeps-things.md, docs/sync/store-clones.md,
# docs/sync/editing-by-hand.md, docs/knowledge/editing-rows.md).
#
# Alice keeps her team's notes in a git store github.com/acme/notes next to her repo
# github.com/acme/website. Shows lore's home, the store's plain clone and its README,
# `lore where`, `lore path`, `lore edit` with an editor script, hand edits, a refused hand
# edit, a hand delete, plain `git commit` in the clone, and `lore store use` with a clone of
# her own. Fake GitHub only.

lore_repo alice acme/website
remote acme/notes

note "a git store"
on alice work/website "lore store init --name notes --url git@github.com:acme/notes"
on alice work/website "lore task add --title 'Renew the TLS certificate' --to notes --no-live"
local t_cert; t_cert=$(id tsk)
on alice work/website "lore memory add --to notes --body 'The staging database is reset every Monday.'"
local m_stage; m_stage=$(id mem)

note "the store clone"
on alice . "find ~/.lore/stores -maxdepth 3 -not -path '*/.git/*' | sort"
on alice .lore/stores/acme/notes "ls -a; cat lore-store.yaml"
on alice .lore/stores/acme/notes "find _store -type f | sort"
on alice .lore/stores/acme/notes "cat _store/memories/$m_stage.json"
on alice .lore/stores/acme/notes "ls .git/lore"
on alice .lore/stores/acme/notes "head -12 README.md"
on alice .lore/stores/acme/notes "git log --format='%an <%ae>  %s'"

note "the home folder"
on alice . "ls ~/.lore"
on alice . "head -20 ~/.lore/README.md"

note "lore where"
on alice work/website "lore where"

note "lore path"
on alice work/website "lore path --id $t_cert"
on alice work/website "lore path --id $t_cert --json"

note "lore edit"
# The editor is a script that changes the task's priority and adds a line to its body, as a
# person would in vi.
quiet alice . "mkdir -p bin && printf '%s\n' '#!/bin/sh' 'sed -i -e \"s/^priority: .*/priority: high/\" -e \"\\\$a Ask the hosting provider for the new chain first.\" \"\$1\"' > bin/edit-task && chmod +x bin/edit-task"
on alice work/website "lore row markdown --id $t_cert"
on alice work/website "EDITOR=\$HOME/bin/edit-task lore edit $t_cert"
on alice work/website "lore task show --id $t_cert"

note "edit a row file by hand"
on alice .lore/stores/acme/notes "sed -i 's/reset every Monday/reset every Sunday night/' _store/memories/$m_stage.json && git status --short"
on alice work/website "lore memory list --store notes"
on alice .lore/stores/acme/notes "git log -1 --format='%an <%ae>  %s'; git status --short"

note "a refused hand edit"
on alice .lore/stores/acme/notes "sed -i 's/\"status\": \"todo\"/\"status\": \"finished\"/' _store/tasks/$t_cert.json"
on alice work/website "lore task list --store notes"
on alice .lore/stores/acme/notes "git checkout -- _store/tasks/$t_cert.json && git status --short"

note "delete a row file by hand"
on alice .lore/stores/acme/notes "git rm -q _store/memories/$m_stage.json && git status --short"
on alice work/website "lore memory list --store notes"
on alice work/website "lore trash list"

note "plain git in the store"
on alice .lore/stores/acme/notes "git config --get merge.lore.driver; cat .git/info/attributes; git config user.email"
on! alice .lore/stores/acme/notes "sed -i 's/Renew the TLS certificate/Renew the TLS certificate (key AKIA''IOSFODNN7EXAMPLE)/' _store/tasks/$t_cert.json && git add _store/tasks/$t_cert.json && git commit -qm 'note the key'"
on alice .lore/stores/acme/notes "git reset -q -- _store/tasks/$t_cert.json && git checkout -- _store/tasks/$t_cert.json && git status --short"

note "your own clone"
on alice . "git clone -q git@github.com:acme/notes ~/code/notes && ls ~/code/notes"
on alice work/website "lore store use --path ~/code/notes --yes"
on alice work/website "lore store list"
on alice work/website "lore path --id $t_cert"
on alice work/website "lore store use --default notes --yes"

note "lore fix"
on alice work/website "lore fix --dry-run"
on alice work/website "lore fix"
