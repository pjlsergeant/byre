---
title: Skills & templates
weight: 50
description: your toolkit, in every folder -- and how to build and share it
---

byre ships templates for go, node, and python, and agent skills for
Claude Code, Codex, Gemini, Grok, and OpenCode; the first `byre develop`
asks which you want, and that's the setup. But the packaging system is
yours: everything the bundled packages do, a package you write can do.

## What a skill does

A **skill** is a portable bundle: the packages and files a capability
needs baked into the image, the env and network endpoints it uses at
runtime, standing instructions for the agent, and any volumes it keeps
state in. A **template** is shape: base image, packages, egress offers
-- the stack a box is built for. Enable a skill from the
**Skills** section of `byre config`; pick a template once, at first
run.

Agent skills carry the agents themselves. More than one can be enabled
in a box -- the config's `agent` key decides which one launches, and
the rest ride along with their own logins (byre's own box runs Claude
with Codex beside it as an
[independent reviewer](/docs/how-do-i/workflow/#set-up-two-agents-in-a-review-loop)).

**Enabling a skill is trusting it.** Skills ship raw Dockerfile lines
and launch hooks; installing one grants nothing, but the moment a box's
config lists it, it builds that box -- and everything it reaches is
named by `byre status`. The sharp version:
[security model](/docs/security-model/).

## Make your own

```sh
byre skill init my-tools        # scaffold ~/.byre/skills/my-tools/
byre skill validate my-tools    # strict parse + resolve check
```

Edit the `skill.toml`, enable it, develop. To start from something that
works, fork any bundled or installed package into an editable local
copy: `byre skill fork byre/go my-go`. Templates use the same verbs
(`byre template init / fork / validate`). Two worked examples in the
cookbook:
[standing instructions](/docs/how-do-i/configure/#give-my-agent-standing-instructions-in-every-box)
and [a custom stack](/docs/how-do-i/toolkit/#make-a-template-for-a-stack-byre-doesnt-ship).

## Share it

```sh
byre skill pack pete/my-tools > skill.toml    # distribution manifest
byre skill install https://... --digest sha256:...
```

`pack` writes a manifest with every file's hash; `install` verifies
byte-for-byte and grants nothing until a box enables the result;
`inspect <uri>` shows what you'd be trusting before anything lands.
Provenance, digest pinning, uninstall semantics, and the full authoring
contract: the [packaging reference](/docs/packaging-reference/).

## MCP servers and Claude Skills

Declared once (`byre mcp add`, `byre claude-skill add` -- or shipped by
a skill), injected into the agent session, attributed in
`byre mcp list` / `byre claude-skill list`. The
[cookbook has the recipe](/docs/how-do-i/configure/#add-an-mcp-server-to-my-agents-session);
the wiring contract is in the
[packaging reference](/docs/packaging-reference/#mcp-and-claude-skills-wiring).
