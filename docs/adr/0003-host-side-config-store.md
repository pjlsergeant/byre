# Project config lives host-side; an in-tree byre.config is a proposal

The config that defines the sandbox must live outside the sandbox: the
project layer is stored under `~/.byre/projects/<id>/`, never read live
from the project tree. A `byre.config` committed in the repo would sit on
the rw project mount, where the boxed agent could rewrite its own sandbox
(mounts, caps, base) and have it applied on the next host-run develop.

A committed `<project>/byre.config` is therefore a **proposal**: on
`develop`, byre shows a human its grants and asks before **adopting** it
into the host-side store (direnv-allow style; a sha256 record re-prompts
on change; non-TTY never adopts). The agent can edit the proposal freely --
it stays inert until adopted.

Consequences: `--self-edit` (mounting the project's host-side store rw)
is the one deliberate, announced exception that lets an agent edit its
own live config. Per the footgun doctrine (PRINCIPLES.md #1), that's a
user's right to grant; status makes it visible.
