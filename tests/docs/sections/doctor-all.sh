# doctor-all: lore doctor --all and lore setup repair on a machine with two setups
# (docs/operations/doctor-all.md).
#
# Tomas runs the agency org (store git@github.com:agency/notes on the fake GitHub) and
# keeps his own projects under ~/work/personal. GitHub's side of ssh is a fake `ssh` on
# PATH that answers `ssh -T` with GitHub's greeting for one account, and `gh` is a fake
# with no login: nothing reaches the network.

# fake_github_ssh LOGIN: every `ssh -T git@<alias>` logs in as LOGIN; gh has no login.
fake_github_ssh() {
  cat > "$SHIM/ssh" <<SH
#!/bin/sh
case " \$* " in *" -T "*) echo "Hi $1! You've successfully authenticated, but GitHub does not provide shell access." >&2; exit 1;; esac
echo "ssh: not available in the docs world" >&2; exit 255
SH
  cat > "$SHIM/gh" <<'SH'
#!/bin/sh
echo "To get started with GitHub CLI, please run:  gh auth login" >&2
exit 4
SH
  chmod +x "$SHIM/ssh" "$SHIM/gh"
}

person tomas tomas@agency.example
fake_github_ssh tomas-dev
remote agency/notes
lore_repo tomas agency/site agency/site
quiet tomas work "mkdir -p stores && cd stores && git init -q && lore store init agency git@github.com:agency/notes --yes"
config tomas <<'Y'
orgs:
  - name: agency
    identity: {key: "human:tomas@agency.example", name: "Tomas Weber", handle: tomas}
    stores:
      - url: git@github.com:agency/notes
        org_folder: org
    own: [github.com/agency/*]
setups:
  - name: agency
    kind: admin
    org: agency
    default: true
    folders:
      - ~/work/agency/**
    github:
      user: tomas-dev
      host_alias: github.com
  - name: personal
    kind: solo
    folders:
      - ~/work/personal/**
    identity:
      key: human:tomas@home.example
      name: Tomas Weber
    personal_store: none
Y
# Tomas is on the agency roster (his row and its open employee period), so the identity
# line shows his roster handle and period.
quiet tomas work/agency/site "lore actor register"

note "run it"
on! tomas work/agency/site "lore doctor --all"

note "one setup"
on! tomas work/agency/site "lore doctor --all --setup personal"

note "wrong account"
fake_github_ssh mira-dev
on! tomas work/agency/site "lore doctor --all --setup agency --fresh"

note "json"
on tomas work/agency/site "lore doctor --all --setup agency --json | python3 -c 'import json,sys; r=json.load(sys.stdin); print(r[\"status\"], r[\"exit_status\"], r[\"counts\"]); [print(c[\"check\"], c[\"status\"], c[\"level\"], c.get(\"target\",\"-\"), \"|\", c.get(\"detail\",\"\")) for s in r[\"setups\"] for c in s[\"checks\"] if c[\"status\"] not in (\"ok\", \"skip\")]'"

note "repair"
on! tomas work/agency/site "lore setup repair agency --dry-run"
# took_ms and checked_at differ per run: only the step and check ids are shown.
on tomas work/agency/site "lore setup repair agency --json | python3 -c 'import json,sys; r=json.load(sys.stdin); [print(s[\"name\"], st[\"step\"], [c[\"check\"] for c in st[\"checks\"]]) for s in r[\"setups\"] for st in s[\"steps\"]]'"
fake_github_ssh tomas-dev
on tomas work/agency/site "lore setup repair agency --dry-run"
