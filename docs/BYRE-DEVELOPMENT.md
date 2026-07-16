# Developing byre

How byre's own development environment hangs together: the self-hosted box,
the dev-harness skills that live in this repo, and the sacrificial VM the
gated integration suite runs on. Workflow *rules* (autonomy, commit
discipline, review loop, docs sweep) live in `CLAUDE.md`; this is the
mechanics reference behind them.

## The self-hosted box

byre develops itself: `byre develop` in this repo builds a Go + Claude box
from `byre.preset`. byre itself runs on the **host** (where Docker is); the
box is where the agent writes Go, runs `go build`, and runs unit tests.
Engine-touching integration tests run on neither -- see the VM below.

The preset composes, besides the base/agent:

- **codex** (bundled) -- the independent review binary, ride-along.
- **pjlsergeant/codereview**, **pjlsergeant/devlog** -- the review-loop and
  dev-workflow skills, installed from
  [pjlsergeant-byre-skills](https://github.com/pjlsergeant/pjlsergeant-byre-skills)
  (moved out of the binary 2026-07-13), pinned by URL + digest in the
  preset's `[sources]`.
- **grok** (bundled) -- ride-along second-opinion CLI.
- **pjlsergeant/inttest** -- the gated suite on the sacrificial VM; lives IN
  this repo (below).

**Fresh machine bootstrap:** clone, then `byre preset apply` **from the repo
root** -- it reviews the preset and chauffeurs any missing installs from the
`[sources]` hints (once per machine). A config referencing the qualified ids
without the installs fails loudly at develop with the exact install
commands. The repo-root requirement is inttest's: its source uri is a path,
resolved against the cwd of the apply.

## `skills/` -- dev-harness skills that live in this repo

Skill packages that are byre-repo dev tooling, not product. They are not
builtins (users should not carry byre's own dev harness in the binary -- the
same reasoning that moved codereview/devlog out to the external skills
repo), and they don't live in that external repo either: they co-evolve with
this repo (a wrapper and the test suite it drives, a VM template and the
tests that run on the VM), so they version here. `byre.preset` references
them by qualified id with a **path source** (`[sources]` uri relative to the
repo root) -- the same install flow as the https-pinned skills, minus the
digest pin (the trust boundary is the checkout itself; the committed payload
hashes still verify).

### Editing a skill here

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

(The temp `BYRE_HOME` is because `pack` operates on a *local* package; a
copy under the real `~/.byre/skills` would contest the installed id.)

Pack output is a fixed point -- re-packing an unchanged directory reproduces
the file byte for byte -- and a forgotten re-pack fails **loudly** at
install: the committed payload hashes stop matching. Boxes run the installed
*snapshot*, so a repo edit changes nothing anywhere until re-pack +
re-install; the next `byre develop` rebuild picks it up. Two caveats:

- Comment blocks placed before the first non-`[package]` table are swallowed
  by the next re-pack (they read as part of the `[package]` section). Keep
  prose in this document or in comments *after* `[build]`/`[runtime]`
  headers.
- `version` in `[package]` is display metadata; bump it on meaningful edits
  (replacement itself keys on the digest).

## The inttest VM

Engine-touching tests can't run in the box (no engine; bind mounts resolve
engine-side), and running them against the host's real Docker is the blast
radius byre exists to avoid. So the gated suite
(`BYRE_DOCKER_TESTS=1 go test -run Integration`) runs on a **sacrificial
Lima VM** -- disposable by design; its entire contents are a synced repo
copy and image caches. The VM is a test *runner*, not just an engine: a
remote `DOCKER_HOST` can't work, so the tree is synced over ssh and `go
test` runs there.

The whole VM is one file: `skills/inttest/byre-inttest.yaml` (also baked
into boxes at `/etc/byre/inttest/byre-inttest.yaml`). It mounts nothing
from the host, pins ssh to localhost:60022, and provisions both engines --
docker (get.docker.com) and rootless podman (crun/cgroupfs pinned in
`containers.conf`, linger enabled: bare-ssh sessions have no user dbus, and
runc insists on a systemd slice rootlessly).

**Setup, once per machine** (the wrapper prints these remedies too):

```sh
ssh-keygen -t ed25519 -N '' -f ~/.ssh/byre-inttest     # BEFORE limactl start
limactl start --name=byre-inttest skills/inttest/byre-inttest.yaml
byre deliver ~/.ssh/byre-inttest                        # from this project
# then, in the box:
mv /inbox/byre-inttest /home/dev/.byre-identity/inttest/
chmod 600 /home/dev/.byre-identity/inttest/byre-inttest
```

The key lands on a **machine-scoped volume**, so every box on the machine
shares it. The ssh user rides `env_from_host = { BYRE_INTTEST_USER =
"env:USER" }` in the preset (Lima names the VM user after the host user).

**Use, from any box:** `byre-inttest` runs the full gated suite (the
pre-handoff check -- CI's gated job only fires on push); scope it while
iterating (`byre-inttest ./internal/runner/ -run Integration -v`);
`BYRE_TEST_ENGINE=podman` pins the engine. The wrapper syncs to a
per-worktree directory on the VM, so concurrent sessions don't collide.
Unset, the VM address falls back across `host.docker.internal` (Docker
Desktop) and `host.containers.internal` (podman), both egress-granted by
the skill; native-Linux docker provides neither name, so set
`BYRE_INTTEST_VM` there (and grant that egress yourself on a firewalled
box).

**Lifecycle** (host-side): `limactl stop byre-inttest` pauses;
`limactl delete byre-inttest` + a fresh start resets -- nothing on it is
precious. A re-created VM has a new hostkey: in the box,
`ssh-keygen -R '[<address>]:<port>'` clears the stale entry.
