# Project config lives host-side; an in-tree config is a proposal

> Amended by ADR 0029 (2026-07-13): a repo-shipped config is a preset,
> applied via `byre preset apply`.

The config that defines the sandbox must live outside the sandbox: the
project layer is stored under `~/.byre/projects/<id>/`, never read live
from the project tree. A `byre.config` committed in the repo would sit on
the rw project mount, where the boxed agent could rewrite its own sandbox
(mounts, caps, base) and have it applied on the next host-run develop.

A config shipped in the repo is therefore a **proposal**: a preset
(`byre.preset`) that reaches the host-side store only through the explicit
`byre preset apply` flow (ADR 0029) -- cloning gives you a file, not a
prompt. The agent can edit the proposal freely; it stays inert until a
human applies it.

Consequences: `--self-edit` (mounting the project's host-side store rw)
is the one deliberate, announced exception that lets an agent edit its
own live config. Per the footgun doctrine (PRINCIPLES.md #1), that's a
user's right to grant; status makes it visible.
