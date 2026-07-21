# inttest -- byre's sacrificial integration-test runner

`byre-inttest` (on PATH) syncs `/workspace` to a throwaway RUNNER and runs
byre's gated integration suite THERE (`BYRE_DOCKER_TESTS=1 go test`).
Engine-touching tests can't run in this box (no engine, and bind mounts
resolve engine-side), and running them against the host's real Docker would
be exactly the blast radius byre exists to avoid -- the runner is disposable
by design: its entire contents are a synced repo copy and image caches.

The runner is a **Lima VM** where the host can nest VMs, and a **privileged
DinD container** where it can't (see below). The wrapper's transport is the
same either way -- but the CONFIG is not: address, port and egress all differ
per runner. Check `BYRE_INTTEST_VM` / `BYRE_INTTEST_PORT` before assuming the
Lima defaults apply.

Habits:

- After changing production code, run `byre-inttest` (defaults to
  `./... -run Integration`) BEFORE calling the work done -- CI's gated job
  only fires on push.
- Scope it while iterating: `byre-inttest ./internal/runner/ -run 'Integration.*Firewall' -v`.
- `BYRE_TEST_ENGINE=podman byre-inttest` pins the engine (either runner
  carries docker AND rootless podman; auto prefers docker). Podman is
  several times slower, so the wrapper lifts go test's 10m default to 40m
  for podman runs only.
- If the runner is unreachable (connection refused / timeout), report that
  and ask for it to be started host-side (`limactl start byre-inttest`, or
  `docker start byre-inttest` where the runner is the DinD container below).
  Never substitute a run against some other engine -- least of all the
  host's, which is the engine your own box is running on.
  Where the runner is DinD, a connect TIMEOUT is usually the container's
  bridge IP having drifted (they are assignment-ordered): ask for
  `docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}'
  byre-inttest` and update `BYRE_INTTEST_VM`.
- Don't pipe the wrapper through `tail`/`head`: the pipe takes the exit
  status and a RED suite reports success. Redirect to a file, read that.

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
- **The VM address:** unset, the wrapper tries `host.docker.internal`
  (Docker Desktop) then `host.containers.internal` (podman); both carry
  egress grants. Native-Linux docker provides neither name, and the host's
  own address does NOT work either -- Lima's builtin forward binds host
  loopback only. The template binds a second forward on the docker bridge
  gateway for exactly this: set `BYRE_INTTEST_VM = "172.17.0.1"` (port stays
  60022; grant that endpoint's egress yourself on a firewalled box -- the
  skill's grants name only the two defaults). That address assumes docker's
  DEFAULT bridge: a custom `bip` moves the gateway, so adjust the template's
  `hostIP` and `BYRE_INTTEST_VM` together. Newer Lima versions bind the
  builtin forward wider than loopback, so the template's bridge forward can
  warn `bind: address already in use` at start -- harmless either way: the
  endpoint is served, by one listener or the other (verified 2026-07-21,
  Lima-latest on native-Linux docker).

**Hosts without nested virtualisation** (a cloud devbox with no `/dev/kvm`
-- check `systemd-detect-virt`): Lima can only emulate there, far too slow
to use. The runner is a privileged Docker-in-Docker container instead
(`skills/inttest/dind/`), reachable from this box at its bridge IP. The
wrapper's transport is unchanged; its CONFIG is not:

- `BYRE_INTTEST_PORT = "22"` -- the container's own sshd port. The wrapper
  still defaults to `60022`, which is Lima's forwarded port and also the
  DinD container's HOST publish mapping (`-p 60022:22`) -- neither applies
  when reaching the container directly. Setting only `BYRE_INTTEST_VM`
  leaves the port wrong and ssh fails.
- `egress = ["<bridge-ip>:22"]` in the config if the box's network is
  closed. The skill grants only `host.docker.internal:60022` /
  `host.containers.internal:60022`, so neither DinD address is covered.
  (`byre status` shows whether the grant is enforced or inert.)

**IPv6-only hosts** (no v4 egress at all -- e.g. a hostedpi Pi with NAT64/
DNS64): qemu's slirp gives the Lima VM NO external egress there (external
v6 times out through slirp; v4 has nowhere to go), and DinD is no refuge if
the host's storage can't hold `security.capability` xattrs (NFS root: the
image won't even unpack). What works: the guest CAN reach host loopback at
`192.168.5.2`, so run a proxy container published on the host's
`127.0.0.1` and point the VM's apt, dockerd (systemd drop-in), docker
client (`~/.docker/config.json` proxies -- buildkit injects into RUN
steps), and `/etc/environment` at `http://192.168.5.2:<port>`. Sufficient
for the build/run/deliver/TUI tests. NOT sufficient for the firewall
tests: their IP-literal v4 asserts (`1.1.1.1` allow) can never pass
without v4 -- scope them out with `-run` and note it, don't chase them.

Build and caveats: `docs/BYRE-DEVELOPMENT.md`.

VM lifecycle is host-side: `limactl stop byre-inttest` pauses it,
`limactl stop byre-inttest && limactl delete byre-inttest` + a fresh
`limactl start` resets it (delete refuses a running VM) -- nothing
on it is precious. After a re-create the VM's hostkey changes -- and on
setups where Lima re-seeds cloud-init per start, EVERY restart regenerates
it. Clear the stale entry in the box with
`ssh-keygen -R '[<address>]:<port>'` -- with the defaults,
`ssh-keygen -R '[host.docker.internal]:60022'` (substitute your
`BYRE_INTTEST_VM`/`BYRE_INTTEST_PORT` overrides if you set them).
