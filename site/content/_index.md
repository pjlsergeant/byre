---
title: byre
---

# A comfortable, constrained agent environment around any folder

Run `byre develop` in a project, worktree, or scratch directory, and byre creates a local container -- the box -- around it. Your favourite tools and creature comforts come with you, but the rest of the host stays out of reach.

MIT licensed | open source | local | single binary | no lock-in | for Linux and macOS

```sh
brew install --cask pjlsergeant/tap/byre
cd ~/my-project
byre develop
```

([Linux and every other install option](/docs/install/))

```text
  byre: ~/my-project -> /workspace (rw) · extra host mounts: none · network: open
  ╭──────────────────────────────────╮
  │ ✻ Claude Code                    │
  │   /workspace                     │
  ╰──────────────────────────────────╯
```

It's **`--dangerously-skip-permissions`, without risking the farm.**

byre is free, open-source software, developed in the open [on GitHub](https://github.com/pjlsergeant/byre) under the [MIT license](https://github.com/pjlsergeant/byre/blob/main/LICENSE) -- every Dockerfile it generates is yours to read, and so is every line of byre itself.

**⚠️ byre is a young project. I spend all day, every day inside it, for literally all of my work, but features are liable to change quickly.**
