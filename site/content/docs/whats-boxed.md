---
title: What's boxed, what isn't
weight: 80
description: the honest contract, including what byre is not
---

- **Boxed:** your host filesystem, environment, and credentials. The agent
  sees only what you mount or pass.
- **Not boxed, by design:** the network (open by default -- enable the
  default-deny firewall skill to close it) and the project itself (mounted
  read-write -- it's the agent's job to edit it).
- **Not a security product:** a container is not a microVM. If you need
  the strongest isolation story, use one. byre is meant to protect you
  from over-eager and reckless agents, not from state-sponsored malware.
- **Not your nanny:** the box is locked against the *agent*, not against
  you. Every protection is one config edit away from off, and skills can
  widen the box as far as you like -- you can hang yourself with skills,
  and that's intentional. byre's promise is that `byre status` always
  tells you where the rope is.

The full security model:
[docs/SECURITY.md](https://github.com/pjlsergeant/byre/blob/main/docs/SECURITY.md).
