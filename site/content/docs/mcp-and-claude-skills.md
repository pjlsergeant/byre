---
title: MCP & Claude Skills
weight: 53
description: give your agent tools and skills, once, in every session
---

Your agent gets better with tools (MCP servers) and packaged expertise
(Claude Skills). byre's job is to make both **declare-once**: say it a
single time, and every session of the box -- every rebuild, every
worktree -- comes up with the tool already wired and attributed.

## MCP servers

```sh
byre mcp add github -- github-mcp-server stdio
byre mcp add tracker https://mcp.example.com/ --bearer TRACKER_TOKEN
```

A server is a URL (remote) or a command (run in the box). `--global`
declares it for every project; `--bearer NAME` wires token auth without
ever writing the token down -- the header names an env var, and the
value resolves inside the box at launch.

`byre mcp list` shows the effective set: what you declared, what your
enabled skills contributed, and where each came from. Removing is
symmetric -- `byre mcp remove <name>` understands the difference
between deleting your own declaration and switching off one a skill
ships. A remote server's network reach is attributed in `byre status`.

### Authenticating a server

Declarations carry env var *names*, never token values -- the
declaration bakes into the image, and secrets don't belong there. The
value arrives at launch, from the box's environment:

```sh
byre mcp add tracker https://mcp.example.com/ --bearer TRACKER_TOKEN
```

sends `Authorization: Bearer <whatever $TRACKER_TOKEN is in the box>`.
Getting the token *into* the box is a separate, visible grant --
`[env_from_host]` passes it through from your host environment at
launch
([recipe](/docs/how-do-i/configure/#use-my-api-key-instead-of-an-agent-login))
-- and command servers work the same way: `--env NAMES` declares what
the process reads. Interactive OAuth flows aren't wired; static tokens
are the supported story.

## Claude Skills

```sh
byre claude-skill add ~/claude-skills/tdd-loop
```

A Claude Skill is a directory with a `SKILL.md` at its root. Declare it
once and it loads in every session as `/tdd-loop` -- no per-box setup,
no copying into the project. `--global` shares it across projects;
`byre claude-skill list` shows the effective set with the same
attribution as MCP. Skills you build *inside* the box win over
byre-delivered ones of the same name -- your working copy shadows the
declaration while you iterate on it.

## Where declarations live

Both ride the config cascade, so the right scope is one decision: this
project, your global defaults, or a shared
[layer](/docs/how-do-i/toolkit/#share-one-config-baseline-across-many-projects).
Skills you enable can ship their own declarations, and you can switch
any single one off without disabling the skill. The precise wiring
contract -- field shapes, header templating, closure semantics -- is in
the [packaging reference](/docs/packaging-reference/#mcp-and-claude-skills-wiring).
