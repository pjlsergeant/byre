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
  (add `disabled = true` to keep one in the config but switched off)
- `ports = [{ container = 3000 }]` — publish container ports on the host
  (binds 127.0.0.1 unless `interface` says otherwise; `host` defaults to `container`)
- `volumes = [{ name = "cache", role = "cache", target = "/c" }]` — named volumes
  (add `scope = "machine"` for one volume shared by ALL the user's projects)
- `dockerfile_pre  = ["RUN ..."]` — raw Dockerfile lines BEFORE byre's core block
- `dockerfile_post = ["RUN ..."]` — raw Dockerfile lines at the project tail
- `run_args = ["--cap-add=SYS_PTRACE"]` — raw `docker run` flags
- `[[mcp]]` blocks — MCP servers for the agent session: `name = "github"` plus
  a local `command = ["srv", "arg"]` or remote `url = "https://..."`;
  `env = ["TOKEN_NAME"]` names consumed vars (values via `env_from_host`/`[env]`)

So: need a **package** → add it to `apt`. Need a **custom build step** → add a
`RUN ...` line to `dockerfile_pre` or `dockerfile_post`.

Cascade rules: this file layers over `~/.byre/default.config` and any template.
Scalars override; `env`/`files` merge per key; list keys union. `!name` removes
an inherited entry — but only for `skills`, for `mounts`/`volumes` (by their
target/name), and for `[[mcp]]` (a `name = "!server"` block, which also drops a
skill-declared server of that name). `apt`/`npm_global` just union (a literal
`!x` is kept), and raw `dockerfile_pre`/`dockerfile_post`/`run_args` blocks
always append.
