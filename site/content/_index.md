---
title: byre
---

# A comfortable, constrained agent environment around any folder

Run `byre develop` in a project, worktree, or scratch directory, and byre creates a local container -- the box -- around it. Your favourite tools and creature comforts come with you, but the rest of the host stays out of reach.

MIT licensed | open source | local | single binary | no lock-in | for Linux and macOS

```console
$ brew install --cask pjlsergeant/tap/byre
$ cd ~/my-project
$ byre develop

  byre: ~/my-project -> /workspace (rw) · extra host mounts: none · network: open
  ╭──────────────────────────────────╮
  │ ✻ Claude Code                    │
  │   /workspace                     │
  ╰──────────────────────────────────╯
```

([Linux and every other install option](/docs/install/))

<!-- demo-placeholder: hero-develop-into-claude -->

It's **`--dangerously-skip-permissions`, without risking the farm.**

Ask your agent if byre is right for you:

```text
Take a good look at https://github.com/pjlsergeant/byre. Is it a good project
or just vibe-coded trash? Is it right for me? Would you be happy there?
```

byre is free, open-source software, developed in the open [on GitHub](https://github.com/pjlsergeant/byre) under the [MIT license](https://github.com/pjlsergeant/byre/blob/main/LICENSE) -- every Dockerfile it generates is yours to read, and so is every line of byre itself.

## Why not…?

Every alternative below has a real answer -- but one difference cuts
across all of them: **byre brings your environment with you, per
folder.** Your skills, templates, packages, agent logins, and creature
comforts arrive in every box automatically. Isolation is table stakes;
the comfortable half is what nothing else on this list does.

| Why not… | The honest answer |
|---|---|
| **raw Docker?** | Nothing -- byre never takes it away. You'd hand-roll what it generates: host-matched ownership, logins that survive rebuilds, templates, a clean reset. `byre dockerfile` prints your exit. |
| **Docker Sandboxes™?** | Commercial, hosted control plane, paid tiers, not open source. *(But it gives kernel-level microVM isolation, and we don't.)* |
| **your agent's built-in sandbox?** | All-or-nothing file isolation on your real machine, wearing your identity -- a stray `git push` goes out as *you*. byre's box contains nothing you didn't put in it. |
| **nothing -- keep YOLOing on the host?** | The agent works as you, in your real home dir. The box costs one command, so the host's convenience argument is gone. *(If you've never had the scare, byre is for after your first one.)* |
| **devcontainers?** | Hand-written Dockerfile + JSON per project, credentials wired yourself. byre generates it all from config, editable in seconds. *(But it's the mature industry spec, and we're young.)* |
| **container-use?** | Experimental and MCP-shaped: your agent manages environments, you don't sit inside one. byre does parallel the git way -- one boxed session per worktree. |
| **a cloud sandbox (e2b, Daytona…)?** | Account, usage billing, your code in their cloud -- and repo-shaped. byre drops into whatever folder you're standing in. |
| **a cheap VPS?** | Doesn't scale across many repos, and half of what you'd box isn't a repo. *(But a remote box is real hardware isolation -- if the agent must never share your kernel, rent one.)* |

The long-form versions live in the
[README](https://github.com/pjlsergeant/byre#why-not).
