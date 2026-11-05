# ops: keep lore healthy and safe (docs/operations/*.md).
# Alice (an agency developer) works in the client's website repo: health checks, the
# audit log, backups, restore and repair, a support bundle, secret refusal, the plugin
# allow-list, database settings, read-only mode and the CI switches. No network, no model.

lore_repo alice client/website
quiet alice work/website "lore rule add --body 'Run the tests before you push' && lore memory add --body 'The client replies faster on WhatsApp than email' && git add -A && git commit -qm 'lore: first notes'"

note "doctor"
on alice work/website "lore doctor"
on alice work/website "lore doctor --json | head -8"

note "audit log"
on alice work/website "lore rule add --body 'Never put passwords in the code'"
on alice work/website "lore audit log"
on alice work/website "lore audit verify"

# Someone edits the database file directly with a SQLite tool (no lore command):
# the first rule's text is changed behind lore's back.
quiet alice work/website "python3 -c \"import sqlite3; c=sqlite3.connect('.lore/lore.db'); c.execute(\\\"update rules set body='Pushing without tests is fine' where body='Run the tests before you push'\\\"); c.commit()\""

note "audit tamper"
on! alice work/website "lore audit verify"
on alice work/website "lore rule list"
on alice work/website "lore sync"
on alice work/website "lore audit log --limit 2"
on alice work/website "lore audit verify"

note "backup"
on alice work/website "lore backup --out .lore/backups/before-buttons.sqlite"
on alice work/website "ls .lore/backups"

note "restore"
on alice work/website "lore rule add --body 'Buttons are blue'"
on alice work/website "lore rule list"
on! alice work/website "lore restore .lore/backups/before-buttons.sqlite"
on alice work/website "lore restore .lore/backups/before-buttons.sqlite --confirm"
on alice work/website "lore rule list"
on alice work/website "git status --short"

# The database file gets damaged (here: its first bytes are overwritten).
quiet alice work/website "rm -f .lore/lore.db.broken.* && printf 'this is not a db' | dd of=.lore/lore.db bs=1 conv=notrunc 2>/dev/null"

note "repair"
on! alice work/website "lore doctor"
# Shared notes are read from .lore/data, so they still list while lore.db is broken.
on alice work/website "lore rule list"
on alice work/website "lore repair --tier=2 --confirm"
on alice work/website "lore rule list"

note "after repair"
on alice work/website "lore doctor"
on alice work/website "lore sync conflicts | head -3"
quiet alice work/website "rm -f .lore/lore.db.broken.*"

note "repair tier 1"
on! alice work/website "lore repair --tier=1 --confirm"

note "rebuild from data"
on alice work/website "mv .lore/lore.db .lore/lore.db.old && rm -f .lore/lore.db-wal .lore/lore.db-shm"
on alice work/website "lore rule list"
on alice work/website "lore sync"
on alice work/website "lore doctor | head -1"
quiet alice work/website "rm -f .lore/lore.db.old"

note "support bundle"
on alice work/website "lore support-bundle --out bundle.json"
on alice work/website "grep -E '\"(binary_version|status|rules|memories|sanitized)\"' bundle.json"
quiet alice work/website "rm -f bundle.json"

note "pii pattern"
on alice work/website "lore secrets rule add invoice-number --regex 'INV-[0-9]{6}' --description 'our invoice numbers' --yes"
on alice work/website "lore secrets rules"
on! alice work/website "lore memory add --body 'Invoice INV-123456 is still open'"

note "plugin"
on alice work/website "lore plugin list"
on alice work/website "printf '#!/bin/sh\necho hello\n' > hello.sh"
on alice work/website "lore plugin trust --name hello --sha256 \$(sha256sum hello.sh | cut -c1-64)"
on alice work/website "lore plugin list"
quiet alice work/website "rm -f hello.sh"

note "db config"
on alice work/website "lore config set default_branch main"
on alice work/website "lore config get default_branch"
on alice work/website "lore config list"
on! alice work/website "lore config set team.note 'Ask Bob before changing the build'"
on! alice work/website "lore config get color"

note "team policy"
on alice work/website "lore policy set trash.retention_days 365 --yes"
on alice work/website "lore policy show trash"

note "errors list"
on alice work/website "lore errors list | head -5"
on alice work/website "lore errors list | wc -l"
on alice work/website "lore errors list --json | head -9"

note "read only"
EXTRA_ENV="LORE_READ_ONLY=1" on! alice work/website "lore rule add --body 'Buttons are blue'"
EXTRA_ENV="LORE_READ_ONLY=1" on alice work/website "lore rule list"
on! alice work/website "lore rule add --read-only --body 'Buttons are blue'"

# Make the search index look out of date (what an upgrade without `lore setup` leaves).
quiet alice work/website "python3 -c \"import sqlite3; c=sqlite3.connect('.lore/lore.db'); c.execute(\\\"update config set value='0000000000000000' where key='fts_registry_fingerprint'\\\"); c.commit()\""

note "stale warning"
on alice work/website "lore plugin list"
EXTRA_ENV="LORE_NO_STALE_WARN=1" on alice work/website "lore plugin list"
on alice work/website "lore setup"
on alice work/website "lore plugin list"

# What CI sees: a fresh clone of the pushed repo, with only .lore/data in it.
quiet alice work/website "git add -A && git commit -qm 'lore: notes' && git push -q origin main"
quiet alice work "git clone -q https://github.com/client/website ci-clone"

note "ci"
EXTRA_ENV="LORE_READ_ONLY=1 LORE_NO_STALE_WARN=1" on alice work/ci-clone "lore rule list"
EXTRA_ENV="LORE_READ_ONLY=1 LORE_NO_STALE_WARN=1" on! alice work/ci-clone "lore doctor"
EXTRA_ENV="LORE_READ_ONLY=1 LORE_NO_STALE_WARN=1" on alice work/ci-clone "lore sync install-git"
EXTRA_ENV="LORE_READ_ONLY=1 LORE_NO_STALE_WARN=1" on alice work/ci-clone "lore audit verify"
EXTRA_ENV="LORE_READ_ONLY=1 LORE_NO_STALE_WARN=1" on alice work/ci-clone "lore doctor --json > doctor.json; echo \"doctor exit \$?\""
on alice work/ci-clone "grep '\"status\"' doctor.json"
EXTRA_ENV="LORE_READ_ONLY=1" on alice work/ci-clone "lore context --internal"
quiet alice work/ci-clone "rm -f doctor.json"

note "secret refused"
on! alice work/website "lore memory add --body 'Deploy key for staging: AKIAIOSFODNN7EXAMPLE'"

note "allow secrets"
on alice work/website "lore memory add --not-a-secret --reason 'AWS documentation example key' --yes --body 'Deploy key for staging: AKIAIOSFODNN7EXAMPLE'"
local secret_mem; secret_mem=$(id mem)
on alice work/website "grep -rl AKIA .lore/data || echo 'not in .lore/data'"
on alice work/website "lore memory delete $secret_mem --yes"
# A hard-deleted row leaves no warning behind: doctor drops the refused-export
# warning once the row is gone.
on alice work/website "lore doctor"
