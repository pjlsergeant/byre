# inttest -- byre's sacrificial integration-test VM

`byre-inttest` (on PATH) syncs `/workspace` to a throwaway Lima VM and runs
byre's gated integration suite THERE (`BYRE_DOCKER_TESTS=1 go test`).
Engine-touching tests can't run in this box (no engine, and bind mounts
resolve engine-side), and running them against the host's real Docker would
be exactly the blast radius byre exists to avoid -- the VM is disposable by
design: its entire contents are a synced repo copy and image caches.

Habits:

- After changing production code, run `byre-inttest` (defaults to
  `./... -run Integration`) BEFORE calling the work done -- CI's gated job
  only fires on push.
- Scope it while iterating: `byre-inttest ./internal/runner/ -run 'Integration.*Firewall' -v`.
- `BYRE_TEST_ENGINE=podman byre-inttest` pins the engine (the VM carries
  docker AND rootless podman; auto prefers docker).
- If the VM is unreachable (connection refused / timeout), report that and
  ask for it to be started host-side (`limactl start byre-inttest`). Never
  substitute a run against some other engine.

Setup, once per machine (the wrapper prints these remedies when they apply):

- **The VM:** the Lima template is baked at `/etc/byre/inttest/byre-inttest.yaml`
  (source: `skills/inttest/byre-inttest.yaml` in the byre repo). Generate the
  key FIRST -- `ssh-keygen -t ed25519 -N '' -f ~/.ssh/byre-inttest` -- then
  `limactl start --name=byre-inttest skills/inttest/byre-inttest.yaml`
  (the template authorizes `~/.ssh/*.pub` at create time).
- **The key:** deliver it into any box (`byre deliver ~/.ssh/byre-inttest`),
  then `mv /inbox/byre-inttest /home/dev/.byre-identity/inttest/ && chmod 600
  /home/dev/.byre-identity/inttest/byre-inttest`. That directory is a
  machine-scoped volume: every box on this machine shares it, so this
  happens once, not per box.
- **The ssh user:** `BYRE_INTTEST_USER` must be set (Lima names the VM user
  after the host user). byre's own preset passes it through with
  `env_from_host = { BYRE_INTTEST_USER = "env:USER" }`.
- **Native-Linux engines:** the default VM address, `host.docker.internal`,
  is Docker Desktop's magic name; native Linux docker/podman don't provide
  it. Set `BYRE_INTTEST_VM` to the host's address there (and grant that
  endpoint's egress yourself on a firewalled box -- the skill's grant names
  only the default).

VM lifecycle is host-side: `limactl stop byre-inttest` pauses it,
`limactl delete byre-inttest` + a fresh `limactl start` resets it -- nothing
on it is precious. After a re-create the VM's hostkey changes; clear the
stale entry in the box with `ssh-keygen -R '[<address>]:<port>'` -- with the
defaults, `ssh-keygen -R '[host.docker.internal]:60022'` (substitute your
`BYRE_INTTEST_VM`/`BYRE_INTTEST_PORT` overrides if you set them).
