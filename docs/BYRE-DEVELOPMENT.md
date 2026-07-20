# Developing byre

How byre's own development environment hangs together: the self-hosted box,
the dev-harness skills that live in this repo, and the sacrificial VM the
gated integration suite runs on. Workflow *rules* (autonomy, commit
discipline, review loop, docs sweep) live in `CLAUDE.md`; this is the
mechanics reference behind them. For the recurring "source-harden an agent
CLI's credential skills" exercise, the playbook is
`docs/ADDING-NEW-LLMS.md`.

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
scripts/repack-skill.sh                 # default: skills/inttest
scripts/repack-skill.sh skills/other    # any skill in this repo
byre skill install skills/inttest/skill.toml    # host-side
```

The script uses a `byre` on PATH if there is one and otherwise builds from
the working tree, so **it also runs inside a box** -- where there is no byre
binary, but there is Go and the source. Only `skill install` is genuinely
host-side (it writes the real `~/.byre/skills`).

It packs via a throwaway `BYRE_HOME`, because `pack` operates on a *local*
package and a copy under the real `~/.byre/skills` would contest the
installed id. It also writes to a temp file and moves it into place only on
success: redirecting pack's stdout straight at the manifest
(`... > skills/inttest/skill.toml`) truncates that file BEFORE pack reads
it, so pack copies an empty primary and the original is destroyed. Recover
with `git checkout skills/inttest/skill.toml`.

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

**Host requirements.** Lima needs qemu on the host (`qemu-utils` for
`qemu-img`, plus the system emulator for your arch -- `--no-install-recommends`
unless you want QEMU's GTK/SDL console pulled in) and it needs **hardware
virtualisation**: without `/dev/kvm` qemu falls back to full software
emulation, which is too slow to provision, let alone run the suite. Check
with `systemd-detect-virt` -- if the machine is ITSELF a guest (most cloud
instances; Hetzner Cloud exposes no nested virt) there is no `/dev/kvm` to
be had, and no template change fixes it. Use the DinD host below instead.

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
`BYRE_INTTEST_VM = "172.17.0.1"` there -- the docker bridge gateway, which
the Lima template binds via the guest sshd's second port (the host's own
address cannot work: Lima's builtin forward is loopback-only). Assumes
docker's default bridge; a custom `bip` moves the gateway, so adjust both
the template's `hostIP` and `BYRE_INTTEST_VM`. Grant that egress yourself
on a firewalled box.

**The agent-contract tier** (`BYRE_AGENT_TESTS=1`, its own gate on top of
the engine): `internal/commands/agent_contract_test.go` builds each agent's
REAL box -- live installers, unpinned versions, exactly what a user's
rebuild pulls -- and probes the agent-side assumptions byre's skills stake
on the binaries (the other half of the contract the unit suite pins with
stub binaries). Loginless probes only; credential mechanics stay field
gates. Its scheduled home is `.github/workflows/agents.yml` (every two
days + push to main -- agent drift runs on the AGENTS' release cadence,
and a red leg usually means "the agent moved", which is why it is not part
of ci.yml or the release gate). Run it ad hoc with
`BYRE_AGENT_TESTS=1 byre-inttest ./internal/commands/ -run AgentContract -v`
-- noting an ad-hoc run rides the VM's warm layer cache (it re-checks the
agent version already built there; only cold builds, like the ephemeral CI
runners', pull today's release).

**Lifecycle** (host-side): `limactl stop byre-inttest` pauses;
`limactl stop byre-inttest && limactl delete byre-inttest` + a fresh start
resets (delete refuses a running VM) -- nothing on it is precious. A
re-created VM has a new hostkey: in the box,
`ssh-keygen -R '[<address>]:<port>'` clears the stale entry. Provisioning
finishes after ssh comes up -- wait for limactl's `READY` line before
judging the VM broken.

## The DinD host (machines without nested virtualisation)

On a host that can't run Lima -- a cloud devbox with no `/dev/kvm` -- the
sacrificial runner is a **privileged Docker-in-Docker container** instead:
`skills/inttest/dind/`. It runs its OWN `dockerd`, so the suite's
containers, images and networks are invisible to the engine hosting your
boxes. It satisfies the same contract the VM does -- an ssh endpoint
carrying docker, podman, go, tmux and git -- so the wrapper's TRANSPORT is
unchanged.

Its **configuration** is not: address, port and egress all differ from the
Lima defaults, and the image needs a build-arg naming the ssh user. Those
are the parts to get right; see the worked example below.

Its package list mirrors the Lima template's `provision:` block -- **keep
the two in step** when either changes.

```sh
cd skills/inttest/dind
docker build --build-arg INTTEST_USER=$USER -t byre-inttest .
docker run -d --name byre-inttest --privileged \
  -p 60022:22 \
  -v byre-inttest-docker:/var/lib/docker \
  -v byre-inttest-podman:/home/$USER/.local/share/containers \
  -v ~/.ssh/byre-inttest.pub:/etc/byre-inttest/authorized_key:ro \
  byre-inttest
```

Note where the pubkey goes: the entrypoint **installs** it as
`authorized_keys`, rather than it being mounted there. The ssh-loop tier
(`BYRE_SSH_LOOP_TESTS=1`, which the wrapper always sets) rewrites the ssh
user's `~/.ssh` and restores it on cleanup -- against a read-only bind
mount those tests fail with `authorized_keys: read-only file system`.

**Both storage volumes are required**, for the same reason: overlayfs
cannot stack on overlayfs, so each engine's graph storage needs a volume
(ext4) rather than the container's own root. Without the first, every
`docker run` dies `failed to mount ... overlay ... invalid argument`;
without the second, podman fails `'overlay' is not supported over
overlayfs, a mount_program is required`. They double as the image caches,
surviving `docker rm` -- `docker volume rm byre-inttest-docker
byre-inttest-podman` for a clean slate. The ssh key is mounted, never
baked, so the image carries no secret.

**Addressing it.** A box and this container share docker's default bridge,
so the box can reach it directly at the container's IP on port 22
(`docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}'
byre-inttest`) -- no host round trip:

```toml
# ROOT keys first: after an [env] header everything belongs to that table
# until the next header, so an `egress` written below it decodes as
# env.egress -- which byre's string-map env cannot even hold.
#
# egress is needed only if the box's network is CLOSED: the skill grants
# host.docker.internal:60022 / host.containers.internal:60022, so neither
# DinD address is covered. `byre status` reports whether a grant is enforced
# or inert ("unenforced, network open") -- and the same applies to the host
# gateway address below, if you use that path instead.
egress = ["172.17.0.4:22"]

# The container's sshd listens on 22. The wrapper still DEFAULTS to 60022 --
# Lima's forwarded port, which is also this container's host publish mapping
# (-p 60022:22) -- so setting only the address leaves the port wrong.
[env]
  BYRE_INTTEST_VM = "172.17.0.4"   # the container's bridge IP
  BYRE_INTTEST_PORT = "22"
```

That IP is assignment-ordered, so a recreate or reboot can shuffle it and
runs start timing out with nothing else changed. The stable alternative is
the published port on the host gateway (`172.17.0.1:60022`), which needs
the host to accept container->host traffic -- a default-deny `INPUT`
(ufw/nftables, common on cloud images) silently drops it, and the symptom
is a connect that hangs rather than refuses.

**Known gaps versus the VM.** Privileged means a shared kernel, so this is
weaker isolation than Lima -- adequate when the host is itself disposable,
not a substitute for a VM on a machine you care about.

Both engines are verified here: the full gated suite passes under docker,
and under `BYRE_TEST_ENGINE=podman` once each engine has its storage volume.
Podman is several times slower (`internal/commands`: ~2 min under docker,
~8 min warm under podman, and it blew go test's 10m DEFAULT on a cold run
while pulling images). That is why `byre-inttest` appends `-timeout 40m`
for podman runs only -- an explicit `-timeout` still wins.

## The TUI test tier (tmux)

`internal/tuitest` drives the BUILT byre binary inside a **private tmux
server per test** and asserts on captured pane text (decision record:
ADR 0038). Gates: `BYRE_TUI_TESTS=1` runs the tier (CI sets it and
installs tmux; the gate set without tmux FAILS rather than skips);
tests that also need an engine or loopback ssh live in
`internal/commands` behind the docker/ssh gates and ride the VM.

The harness deliberately does nothing an agent or a human can't do with
plain tmux -- these are the shared conventions, and their shell
equivalents are the vocabulary for replaying a test's keystrokes by hand
(or, later, for the field-QA agent):

```sh
# A placeholder first, so remain-on-exit is set before the REAL process
# can possibly exit (a fast command would otherwise take the pane with it):
tmux -L <sock> -f /dev/null new-session -d -s main -x 100 -y 30 'sleep 600'
tmux -L <sock> set-option -g remain-on-exit on
tmux -L <sock> respawn-pane -k -t main '<cmd>'   # now the real process
tmux -L <sock> send-keys -t main Down Enter C-s  # keys
tmux -L <sock> send-keys -t main -l 'literal'    # text
tmux -L <sock> set-buffer -- 'x'; tmux -L <sock> paste-buffer -p -t main
tmux -L <sock> capture-pane -p -t main           # the screen, plain text
```

Exit status: run the command through a wrapper that records it —
`'<cmd>; echo $? > /tmp/status'` — and treat a non-empty status file as
"exited". Do NOT read tmux's `#{pane_dead_status}`: it proved
version-sensitive (ubuntu's tmux 3.4 reported 0 where 3.5a reported the
real status; caught by CI on this harness's first push).

Never send two `Escape`s in one `send-keys` — they arrive as `\x1b\x1b`
and bubbletea reads that as a single alt-modified key, so both vanish
(found live by the screen-walker test). One Escape per send, waiting
for each screen in between.

`WaitFor` is a poll loop over `capture-pane` that also fails fast when
the process dies; `WaitForAfter` additionally rejects a match that was
already on screen before the action (transition semantics -- a
persistent footer can't fake a result). House rules: wait for meaning,
never for quiet (blink timers mean screens don't settle); assert exact
product strings (`Saved ✓`, `byre: cancelled — nothing delivered`),
never broad fragments; ENFORCE headlessness where the test needs it
(unset `DISPLAY`/`WAYLAND_DISPLAY`, controlled `PATH`); isolate the
store with `BYRE_HOME`, never a `HOME` swap; a test that flakes twice
gets rewritten or deleted.

## The demo-recording tier (site casts) — PARKED

**Status (2026-07-18): built, not publishing.** The tier below works end
to end, but the site ships no casts and CI runs no recording until the
demos are presentation-quality (TODO.md's Site item is the revival
checklist; site.yml/ci.yml history has the working wiring).

The site's terminal demos are recorded BY TESTS
(`internal/tuitest/demos_test.go`, gate `BYRE_DEMO_REC=1`): each
scenario drives the built binary under the TUI harness with an
asciinema spectator attached (`Opts.RecordTo`), asserts its WaitFors,
and installs `site/static/casts/<slug>.cast` + `<slug>.json`
(duration/geometry for the player shortcode). A layout change fails the
recording test, which fails the site deploy — the assertions are why
this exists instead of a `.tape` file. Design and placement:
`docs/marketing/positioning.md` "Publish-time asciinema demos".

Running it needs tmux, bash-completion (the completion scenario), and
**asciinema v3** — the rust CLI, NOT distro asciinema (python v2, wrong
cast format); CI installs a pinned release binary (see site.yml). Then:

```sh
BYRE_DEMO_REC=1 go test ./internal/tuitest -run TestDemo -v
```

House rules where they DIVERGE from the assertion tier above:

- Demos swap `$HOME` (the one place that's allowed): recorded frames
  paint real paths, so the scenario owns a fake home entirely —
  `BYRE_DEMO_HOME=/home/pete` in the deploy workflow keeps runner paths
  out of published frames; locally a tempdir shows and that's fine
  (local runs are for iterating, CI records what ships).
- Scenarios run on a curated PATH (a symlink farm + per-scenario stubs
  for host capabilities: engine, clipboard), so CI's real docker records
  the same frames as a dev box. The deliver demo's `docker` is a stub
  answering deliver's discovery/exec argv exactly — a transport change
  breaks it loudly.
- `EndCast(sentinel)` cuts the cast at the sentinel's FIRST paint, at
  that line's end within the event: pick a string whose first appearance
  IS the intended final frame, painted in ONE write (styling splits text
  across events), or the trim fails loudly. First, not last — after the
  intended frame the terminal may keep moving (an error line, a
  full-screen repaint that paints the sentinel again), and that footage
  is what the trim drops. Never ship a cast ending on tmux's
  server-exited frame (the poster IS the final frame — P11).
- A scene that must end at an off-camera boundary (develop stopping at
  the engine boundary) records as its own cast; `WriteDemo` concatenates
  scenes with a clear-screen break. The cut is a visible scene change,
  never an edited-out mid-cast frame.
