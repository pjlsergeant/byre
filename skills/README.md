# skills/ -- byre's own dev-harness skills

Skill packages that are byre-repo dev tooling, not product. They are not
builtins (users should not carry byre's own dev harness in the binary --
the same reasoning that moved `codereview`/`devlog` out to
[pjlsergeant-byre-skills](https://github.com/pjlsergeant/pjlsergeant-byre-skills)),
and they don't live in that external skills repo either: they co-evolve with
this repo (a wrapper and the test suite it drives, a VM template and the
tests that run on the VM), so they version here.

`byre.preset` references them by qualified id and carries a **path source**
(`[sources]` uri relative to the repo root), so on a fresh machine
`byre preset apply` chauffeurs the install straight from the working tree --
same flow as the https-pinned skills, minus the digest pin (the trust
boundary is the repo itself).

## Editing a skill here

Each `skill.toml` is committed **packed**: the `[[package.files]]` list at
the bottom is generated, with payload hashes. After editing any file in a
skill's directory, re-pack and re-install (from the repo root, with a byre
on PATH):

```sh
BYRE_HOME=$(mktemp -d) sh -c 'mkdir -p "$BYRE_HOME/skills/pjlsergeant" \
  && cp -R skills/inttest "$BYRE_HOME/skills/pjlsergeant/" \
  && byre skill pack pjlsergeant/inttest' > skills/inttest/skill.toml
byre skill install skills/inttest/skill.toml
```

(The temp `BYRE_HOME` is because `pack` operates on a *local* package; a copy
under the real `~/.byre/skills` would contest the installed id.)

Pack output is a fixed point -- re-packing an unchanged directory reproduces
the file byte for byte -- and a forgotten re-pack fails **loudly** at
install: the committed payload hashes stop matching. Two caveats:

- Comment blocks placed before the first non-`[package]` table are swallowed
  by the next re-pack (they read as part of the `[package]` section). Keep
  prose in this README or in comments *after* `[build]`/`[runtime]` headers.
- `version` in `[package]` is display metadata; bump it on meaningful edits
  (replacement itself keys on the digest).

## The skills

- **inttest** (`pjlsergeant/inttest`) -- run the gated integration suite on
  the sacrificial Lima VM (`byre-inttest` on PATH; the VM template rides the
  package and is baked at `/etc/byre/inttest/byre-inttest.yaml`).
