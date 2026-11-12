# ops: keep lore healthy and safe (docs/operations/*.md).
# Alice (an agency developer) works in the client's website repo: health checks, the
# audit log, a damaged local database and rebuilding it from the files, a support bundle,
# secret refusal, your settings, read-only mode and the CI switches. No network, no model.

lore_repo alice client/website
quiet alice work/website "lore rule add --body 'Run the tests before you push' && lore memory add --body 'The client replies faster on WhatsApp than email' && git add -A && git commit -qm 'lore: first notes'"

note "doctor"
on alice work/website "lore doctor"
on alice work/website "lore doctor --json | head -8"

note "audit log"
on alice work/website "lore rule add --body 'Never put passwords in the code'"
on alice work/website "lore audit log"
on alice work/website "lore audit verify"

# The audit chain lives in this machine's store (~/.lore/state/machine.db, table
# audit_chain, P3-AUDIT-2). Keep a copy, then change one entry behind lore's back:
# the memory's write is relabelled as a delete.
quiet alice . "cp ~/.lore/state/machine.db ~/machine.db.copy"
quiet alice . "python3 -c \"import os, sqlite3; c=sqlite3.connect(os.path.expanduser('~/.lore/state/machine.db')); c.execute(\\\"update audit_chain set action='memories.delete' where action='memories.write'\\\"); c.commit()\""

note "audit tamper"
on! alice work/website "lore audit verify"
on alice . "cp ~/machine.db.copy ~/.lore/state/machine.db && rm ~/machine.db.copy"
on alice work/website "lore audit verify"

# Row files are the truth: an edit to one is a change git shows, not an audit event.
note "row file edit"
on alice work/website "sed -i 's/Run the tests before you push/Pushing without tests is fine/' .lore/data/rules/*.json && lore rule list"
on alice work/website "git status --short .lore/data"
on alice work/website "lore audit verify"
on alice work/website "git restore .lore/data && lore rule list"

# The repo's local cache file (under ~/.lore/cache/repo/) gets damaged (here: its first bytes are overwritten).
quiet alice work/website "printf 'this is not a db' | dd of=\$(ls ~/.lore/cache/repo/website-*.db) bs=1 conv=notrunc 2>/dev/null"

note "damaged database"
on alice work/website "lore rule list"
on alice work/website "lore doctor | grep '^Status'"
quiet alice work/website "rm -f ~/.lore/cache/repo/*.corrupt-*"

note "rebuild from data"
on alice work/website "rm -f ~/.lore/cache/repo/website-*"
on alice work/website "lore rule list"
on alice work/website "lore doctor | grep '^Status'"

note "search rebuild"
on alice work/website "lore search rebuild"
on alice work/website "lore sync conflicts | head -3"

note "support bundle"
on alice work/website "lore support-bundle --out bundle.json"
on alice work/website "grep -E '\"(binary_version|status|rules|memories|sanitized)\"' bundle.json"
quiet alice work/website "rm -f bundle.json"

note "pii pattern"
on alice work/website "lore secrets rule add invoice-number --regex 'INV-[0-9]{6}' --description 'our invoice numbers' --yes"
on alice work/website "lore secrets rules"
on! alice work/website "lore memory add --body 'Invoice INV-123456 is still open'"

note "db config"
on alice work/website "lore config set default_branch main"
on alice work/website "lore config get default_branch"
on alice work/website "lore config list"
on! alice work/website "lore config set team.note 'Ask Bob before changing the build'"
on! alice work/website "lore config get color"

note "team policy"
on alice work/website "lore policy set trash.retention_days 365 --yes"
on alice work/website "lore policy show trash"

note "read only"
EXTRA_ENV="LORE_READ_ONLY=1" on! alice work/website "lore rule add --body 'Buttons are blue'"
EXTRA_ENV="LORE_READ_ONLY=1" on alice work/website "lore rule list"
on! alice work/website "lore rule add --read-only --body 'Buttons are blue'"

# What CI sees: a fresh clone of the pushed repo, with only .lore/data in it.
quiet alice work/website "git add -A && git commit -qm 'lore: notes' && git push -q origin main"
quiet alice work "git clone -q https://github.com/client/website ci-clone"

note "ci"
EXTRA_ENV="LORE_READ_ONLY=1 LORE_NO_STALE_WARN=1" on alice work/ci-clone "lore rule list"
# OWNERQ-9: lore's git wiring (merge driver config, .gitattributes check, hook blocks) runs
# after every command in a git checkout, read-only ones included (it writes git's own files,
# never rows), so the first command above already wired this clone; there is no install step.
EXTRA_ENV="LORE_READ_ONLY=1 LORE_NO_STALE_WARN=1" on alice work/ci-clone "lore doctor; echo \"doctor exit \$?\""
EXTRA_ENV="LORE_READ_ONLY=1 LORE_NO_STALE_WARN=1" on alice work/ci-clone "lore audit verify"
EXTRA_ENV="LORE_READ_ONLY=1 LORE_NO_STALE_WARN=1" on alice work/ci-clone "lore doctor --json > doctor.json; echo \"doctor exit \$?\""
on alice work/ci-clone "grep '\"status\"' doctor.json"
EXTRA_ENV="LORE_READ_ONLY=1" on alice work/ci-clone "lore context --internal"
quiet alice work/ci-clone "rm -f doctor.json"

note "secret refused"
on! alice work/website "lore memory add --body 'Deploy key for staging: AKIA''IOSFODNN7EXAMPLE'"

note "allow secrets"
on alice work/website "lore memory add --not-a-secret --reason 'AWS documentation example key' --yes --body 'Deploy key for staging: AKIA''IOSFODNN7EXAMPLE'"
local secret_mem; secret_mem=$(id mem)
on alice work/website "grep -rl AKIA .lore/data || echo 'not in .lore/data'"
on alice work/website "lore memory delete $secret_mem --yes"
# A hard-deleted row leaves no warning behind: doctor drops the refused-export
# warning once the row is gone.
on alice work/website "lore doctor"

note "upgrade"
on alice work/website "lore version"
on alice work/website "lore upgrade --check"
on alice work/website "cat .lore/data/_meta.json"
on alice work/website "LORE_VERSION_PIN=0.0.1 lore doctor | grep -iE '^Status|pin'"
on alice work/website "cp .lore/data/_meta.json ~/meta.copy && python3 -c \"import json; p='.lore/data/_meta.json'; m=json.load(open(p)); m['lore_version_pin']='0.1.0'; json.dump(m, open(p,'w'), indent=2)\" && lore doctor | grep -iE '^Status|pin'"
on alice work/website "cp ~/meta.copy .lore/data/_meta.json && lore doctor | grep '^Status'"

note "try cache"
on alice work/website "rm -f ~/.lore/cache/repo/website-* && (ls ~/.lore/cache/repo | grep website || echo 'no cache file for website')"
on alice work/website "lore doctor | grep '^Status'"
on alice work/website "ls ~/.lore/cache/repo | grep website | sed 's/-[0-9a-f]\{12\}\./-<hash>./'"

note "config refusals"
on! alice work/website "lore config set live.autopush maybe"
on! alice work/website "lore config set policies.trash.retention_days 30"
on alice work/website "lore config set live.autopush true"
on alice work/website "lore config check"

note "policy unset"
on! alice work/website "lore policy set trash.retention_days soon --yes"
on alice work/website "lore policy unset trash.retention_days --yes"
on alice work/website "lore policy show trash.retention_days"

note "try settings"
on alice work/website "lore config list"
on alice work/website "lore policy show secrets"
