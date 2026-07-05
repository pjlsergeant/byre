## Editing your own sandbox (byre --self-edit)

You're running inside **byre** — a throwaway, project-scoped container built from
a TOML config. This session was started with `--self-edit`, so this project's
config is mounted **read-write** at `/home/dev/.byre-self/byre.config`.

Edit it to change your own sandbox. Changes are **not live** — they take effect
the next time `byre develop` runs on the host. After editing, ask the user to
restart the session to apply them. (`byre dockerfile` previews the build.)

### Config keys (the common vocabulary)

- `base = "debian:bookworm"` — the FROM image
- `apt = ["build-essential", "jq"]` — extra Debian packages
- `npm_global = ["prettier"]` — global npm installs
- `env = { FOO = "bar" }` — env vars baked into the image
- `files = { "./seed" = "/opt/seed" }` — copy project files into the image
- `skills = ["codex"]` — enabled skill bundles (the agent is itself a skill)
- `agent = "claude"` — which agent runs (claude | codex | gemini)
- `template = "go"` — a named starter from `~/.byre/templates`
- `engine = "auto"` — auto | docker | podman
- `mounts = [{ host = "~/data", target = "/data", mode = "ro" }]` — host bind mounts
- `volumes = [{ name = "cache", role = "cache", target = "/c" }]` — named volumes
- `dockerfile_pre  = ["RUN ..."]` — raw Dockerfile lines BEFORE byre's core block
- `dockerfile_post = ["RUN ..."]` — raw Dockerfile lines at the project tail
- `run_args = ["--cap-add=SYS_PTRACE"]` — raw `docker run` flags
- `dockerfile = "Dockerfile"` — opt out of generation; bring your own

So: need a **package** → add it to `apt`. Need a **custom build step** → add a
`RUN ...` line to `dockerfile_pre` or `dockerfile_post`.

Cascade rules: this file layers over `~/.byre/default.config` and any template.
Scalars override; `env`/`files` merge per key; list keys union. `!name` removes
an inherited entry — but only for `skills` and for `mounts`/`volumes` (by their
target/name). `apt`/`npm_global` just union (a literal `!x` is kept), and raw
`dockerfile_pre`/`dockerfile_post`/`run_args` blocks always append.
