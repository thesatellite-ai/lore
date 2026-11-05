# start: first project for one person, no team (docs/knowledge/first-project.md).
# Alice (an agency developer) sets lore up in the client's website repo, writes one rule,
# renders .lore/LORE.md and reads the AGENTS.md pointer init wrote (TOOL NEUTRAL: with no
# AI tool file in the repo, init wires AGENTS.md, never CLAUDE.md by default).

solo_repo alice client/website

note "version"
on alice work/website "lore --version"

note "init"
on alice work/website "lore init --non-interactive --name=website"
on alice work/website "git status --short"

note "setup"
on alice work/website "lore setup"

note "first rule"
on alice work/website "lore rule add --body 'Run the tests before you push'"
on alice work/website "lore rule list"

note "render"
on alice work/website "lore render"
on alice work/website "cat AGENTS.md"

note "commit"
on alice work/website "git add -A && git commit -qm 'lore: first rule' && git log --oneline"

note "doctor"
on alice work/website "lore doctor"

note "sql"
on alice work/website "lore sql \"SELECT 'rules' AS kind, count(*) AS n FROM rules UNION ALL SELECT 'memories', count(*) FROM memories\""

note "completion"
on alice work/website "lore completion bash | head -3"
