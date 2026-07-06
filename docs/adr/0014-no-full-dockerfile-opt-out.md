# No full-Dockerfile opt-out: ejection is raw Docker

byre had a `dockerfile = <path>` config key that stopped generation
entirely and built a hand-written Dockerfile instead, while byre kept
running the result. Removed 2026-07-06: byre either generates the build
or isn't involved -- a user who writes their own Dockerfile uses Docker
directly.

Why. The opt-out split the chassis down the middle: the user owned the
build half (dev user/UID, launcher, launch gate, skills' build blocks)
while byre still ran the runtime half -- which silently assumes the
build half. Every runtime feature had to define behavior on images byre
didn't build, and the firewall made the cost concrete: its netns hook
still fired on opt-out images, but the gate and rule script it needs are
build products, so the documented fail-closed ordering didn't exist
there (2026-07-06 codereview). That tax recurs for every future runtime
feature, and it bought a mode used by nobody: the config UI never
exposed the key, and its constituencies were hypothetical (non-glibc
bases, pre-existing dev images). PRINCIPLES.md #3 ("raw Docker is
first-class") settles where those users go: byre is a templating layer
over Docker, not a replacement -- someone writing the whole Dockerfile
isn't using the templating layer, and raw `docker build && docker run`
is the honest, supported path, not exile.

Considered and rejected: keeping the opt-out with a verified chassis
contract (probe opt-out images for the gate + hook entrypoints; run
hooks when present, degrade loudly when absent). Sound, but it maintains
a growing per-feature compatibility matrix for a constituency that may
never exist. If a real one shows up, the feature can return *as* that
verified-contract mode, deliberately.

Consequences: Debian-derived bases are a hard boundary (`base` +
`dockerfile_pre`/`dockerfile_post` cover custom needs on them; other
bases mean raw Docker). A config carrying `dockerfile =` now fails
loudly as an unknown key rather than being ignored. The raw tier is
exactly the raw blocks: `dockerfile_pre`/`dockerfile_post` at build,
`run_args` at runtime.
