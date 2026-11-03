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

Agent skills carry the agents themselves; more than one can be enabled
in a box, and the config's `agent` key decides which one launches
([a review loop](/docs/how-do-i/workflow/#set-up-two-agents-in-a-review-loop)
is the common use).

**Enabling a skill is trusting it.** Skills ship raw Dockerfile lines
and launch hooks; installing one grants nothing, but the moment a box's
config lists it, it builds that box -- and everything it reaches is
named by `byre status`. What that trust covers:
[security model](/docs/security-model/#skills-and-config).

## Make your own

```sh
byre skill init my-tools        # scaffold ~/.byre/skills/my-tools/
byre skill validate my-tools    # strict parse + resolve check
```

Edit the `skill.toml`, enable it, develop. To start from something that
works, fork any bundled or installed package into an editable local
copy (`byre skill fork <id> <new-id>`), or fork a bundled template
with `byre template fork byre/go my-go`. Two worked examples in the
cookbook:
[launch hooks](/docs/how-do-i/configure/#run-project-setup-automatically)
and [a custom stack](/docs/how-do-i/toolkit/#make-a-template-for-a-stack-byre-doesnt-ship).

## Share it

```sh
byre skill pack pete/my-tools -o skill.toml   # distribution manifest
byre skill install https://... --digest sha256:...
```

`install` grants nothing until a box enables the result, and
`byre skill inspect <uri>` shows what you'd be trusting before anything
lands. Installing, digests and uninstalling:
[packaging reference](/docs/packaging-reference/). Packing, releasing
from another machine, and the full authoring contract:
[docs/SKILLS.md](https://github.com/pjlsergeant/byre/blob/main/docs/SKILLS.md).

Skills can also ship
[MCP servers and Claude Skills](/docs/mcp-and-claude-skills/) --
declared once, attributed, and individually switch-off-able.
