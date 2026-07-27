## Editing your own box (byre develop --self-edit)

You're running inside **byre** — a throwaway, project-scoped container built
from a TOML config. This session was started with `--self-edit`, so this
project's config is mounted **read-write** at
`/home/dev/.byre-self/byre.config`.

Edit it to change your own box. Changes are **not live** — they take effect
the next time `byre develop` runs on the host. After editing, ask the user to
restart the session to apply them. (`byre dockerfile` previews the build.)

**Read `/etc/byre/config-reference.md` before editing.** It is the complete
key vocabulary and the cascade's merge rules — the same reference published
at getbyre.com, baked here so it's readable offline. The short version: need
a **package** → add it to `apt`; need a **custom build step** → a `RUN ...`
line in `dockerfile_pre`/`dockerfile_post`; `!name` removes an inherited
list entry (`apt` included).

This file layers over `~/.byre/default.config`, the template, and any
`extends` chain — scalars override, lists union. A repo can ship the same
dialect as `byre.preset` at its root; byre never reads that live — a human
applies it host-side with `byre preset apply`.
