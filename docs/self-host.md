# Self-hosting byre (dogfood / cutover)

byre runs on the **host** (where Docker is). This container is where the agent
*develops* byre; the actual `byre develop` runs happen host-side — the same place
`./moarcode/develop.sh` runs today. At M5, byre can reproduce its own dev box and
replace moarcode as the harness.

## What `byre develop` produces for this repo

Driven by `byre.config` at the repo root + the built-in `claude` skill:

- base `golang:1.22-bookworm` (Go, git, curl) — project-scoped; the Go base lives
  in this repo's `byre.config` and does not affect your other projects
- the Claude CLI installed via the standalone installer (base-agnostic, no node),
  launched autonomously
- the repo bind-mounted at `/workspace` (rw)
- a per-project `.claude` **state volume** that starts **empty** — you log into
  Claude once and it persists per-project (the volume survives rebuilds;
  `CLAUDE_CONFIG_DIR` keeps Claude's state inside it). byre does **not** copy your
  host `~/.claude`. (If you explicitly want to reuse a host login, add a
  `seed = { host = "~/.claude" }` to a `.claude` volume in your own config —
  byre warns before copying.)
- non-root `dev` user baked to your host UID/GID at build time (the agent runs
  unprivileged as you, no runtime chown); git identity passed through

## Cutover (run on the host)

```sh
# 1. Build the byre binary from this repo (needs Go on the host, or build it
#    inside the current moarcode container and copy it out):
go build -o ~/bin/byre ./cmd/byre      # ensure ~/bin is on PATH

# 2. From the repo root, launch the byre-built dev box:
cd /path/to/byre
byre develop
```

That builds the image (cache-fast after the first time), mounts the repo, and
drops you into Claude — replacing `./moarcode/develop.sh`. The `.claude` volume
starts empty; you log into Claude once and it persists per-project. byre does
not copy your host `~/.claude`.

Useful while iterating:

```sh
byre dockerfile   # inspect the generated Dockerfile
byre status       # what the box can touch (mounts, skills, volumes, container)
byre develop      # a second invocation reports the live session (and how to stop it)
byre shell        # open a shell (as dev) in the running session
```

## Notes / known rough edges (verify on the host)

- The Claude skill installs the standalone CLI **as the `dev` user** (via `gosu
  dev`) into `/home/dev/.local/bin` — where Claude's doctor/auto-update expect it
  — and symlinks it onto `/usr/local/bin`. If the installer drops the binary
  elsewhere, the `test -x` build step fails loudly; edit the install path in
  `~/.byre/skills/claude/skill.toml` (materialized there, inspectable).
- `~/.byre` is bind-mounted host⇄container, so skills/templates/generated
  Dockerfiles are shared between where you edit and where byre runs.
- The moarcode review workflow can be re-added later as a byre skill (a throwaway
  experiment lives under the git-ignored `curiosity/`); it is not required for
  self-hosting the runtime.
